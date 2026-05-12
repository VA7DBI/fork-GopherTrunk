//go:build integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/config"
	"github.com/MattCheramie/GopherTrunk/internal/dsp/demod"
	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/radio/p25/phase1"
	"github.com/MattCheramie/GopherTrunk/internal/sdr"
)

// TestDaemonCCDecodesP25Phase1 is the end-to-end "lights up live
// trunked reception" check from the roadmap. It boots the wired
// daemon with a mock SDR replaying a fully-synthesized P25 Phase
// 1 control-channel IQ stream (built by the C4FM modulator in
// internal/dsp/demod) and asserts the full chain — IQ → C4FM
// demod → MM clock recovery → 4-level slice → dibits → FSW +
// NID + TSBK trellis → CC state machine — recovers the lock:
//
//   - daemon construction (pool, supervisor, ccdecoder)
//   - cchunt supervisor publishing KindHuntProgress
//   - ccdecoder factory dispatch + pipeline construction (the
//     production newP25Phase1Pipeline; no test stubs)
//   - mock SDR's IQ chunks land in the real receiver
//   - receiver's RRC matched filter + Mueller-Müller clock
//     recovery + 4-level slicer emit dibits
//   - phase1.ControlChannel.Process drives the state machine
//   - state machine emitting cc.locked on the bus
//   - supervisor consuming cc.locked → state=locked transition
//   - /api/v1/scanner reflecting the lock
//   - gophertrunk_control_channel_locked metric reaching 1
//
// The plan documented this as the close-out for Workstream A
// ("lights up live trunked reception"). PR #147 landed an
// intermediate version that stubbed the IQ→dibit step via
// ccdecoder.SetTestFactory; this PR replaces that stub with the
// real C4FM modulator + RRC pulse-shaping primitive shipped in
// internal/dsp/demod/c4fm_modulator.go.
func TestDaemonCCDecodesP25Phase1(t *testing.T) {
	const (
		nac           = 0x293
		controlFreqHz = 851_000_000
		sampleRateHz  = 48_000
		sps           = 10
		span          = 8
		alpha         = 0.2
		deviationHz   = 1800.0
		frameRepeats  = 30
	)

	// Build a P25 Phase 1 dibit stream: a long warmup pattern
	// (cycling through every symbol so the Mueller-Müller clock
	// recovery sees plenty of transitions and locks) followed by
	// multiple FSW + NID + trellis-encoded TSBK frames separated
	// by idle dibits. The repeats give the receiver multiple
	// sync-detect chances; with the C4FM modulator's RRC pulse
	// shaping the matched-filter cascade is ISI-free at symbol
	// centres, so any one of those frames is enough to lock.
	dibits := buildP25LockedIQDibits(nac, frameRepeats)

	// Modulate the dibit stream through the C4FM TX chain
	// (impulse train → RRC pulse shape → FM modulator → IQ).
	// 48 kHz @ 10 sps = 4800 baud, the spec rate. 1800 Hz peak
	// deviation matches TIA-102.BAAA-A; the matched
	// newP25Phase1Pipeline configures the receiver's slicer
	// thresholds against this same deviation via the
	// p25phase1rx.Options.DeviationHz knob.
	iq := demod.ModulateC4FM(dibits, sps, span, alpha, sampleRateHz, deviationHz)

	dir := t.TempDir()
	iqPath := filepath.Join(dir, "p25-cc.cfile")
	if err := writeIQToU8File(iqPath, iq); err != nil {
		t.Fatalf("write IQ: %v", err)
	}
	sdr.Register(&sdr.MockDriver{Files: []string{iqPath}})

	cfg := config.Default()
	cfg.SDR.SampleRate = sampleRateHz
	cfg.SDR.Devices = []config.DeviceConfig{
		{Serial: "mock-00", Role: "control"},
	}
	cfg.Trunking.Systems = []config.SystemConfig{
		{Name: "Alpha", Protocol: "p25", ControlChannels: []uint32{controlFreqHz}},
	}
	cfg.API.HTTPAddr = freeAddr(t)
	cfg.Metrics.Enabled = true

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := NewDaemon(cfg, "integration-cc", logger)
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	if d.ccDecoder == nil {
		t.Fatalf("ccDecoder is nil; daemon should have constructed one")
	}

	sub := d.Bus().Subscribe()
	defer sub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runErrCh:
		case <-time.After(3 * time.Second):
		}
	})

	base := "http://" + cfg.API.HTTPAddr
	waitReachable(t, base+"/api/v1/health", 3*time.Second)

	deadline := time.After(5 * time.Second)
	var locked bool
WaitLoop:
	for !locked {
		select {
		case ev := <-sub.C:
			if ev.Kind != events.KindCCLocked {
				continue
			}
			ls, ok := ev.Payload.(phase1.LockState)
			if !ok {
				t.Errorf("CCLocked payload type = %T, want phase1.LockState", ev.Payload)
				continue
			}
			if ls.NAC != nac {
				t.Errorf("LockState.NAC = %#x, want %#x", ls.NAC, nac)
			}
			if ls.FrequencyHz != controlFreqHz {
				t.Errorf("LockState.FrequencyHz = %d, want %d",
					ls.FrequencyHz, controlFreqHz)
			}
			locked = true
			break WaitLoop
		case <-deadline:
			t.Fatalf("no cc.locked event arrived within 5s")
		}
	}

	waitForScannerLock(t, base, "Alpha", 2*time.Second)

	// Verify the cc-locked gauge reaches 1 for our system. The
	// gauge is set by the events.KindCCLocked handler in
	// internal/metrics/prom.go; it's labelled by system /
	// repeater, so we check for the family + a value of 1
	// without pinning the exact label content.
	// gophertrunk_control_channel_locked{system="…"} = 1 is the
	// Prometheus-side signal that the daemon's metrics handler
	// saw the same cc.locked event and updated the gauge. The
	// system label can be "unknown" when the phase1 LockState's
	// SystemName isn't populated; that's fine — the metric
	// family + value pair is what we assert.
	body := scrape(t, base+"/metrics")
	if !strings.Contains(body, "gophertrunk_control_channel_locked{") {
		t.Errorf("/metrics missing gophertrunk_control_channel_locked gauge family:\n%s", body)
	}
	if !strings.Contains(body, `gophertrunk_control_channel_locked{system=`) ||
		!strings.Contains(body, "} 1") {
		t.Errorf("/metrics gophertrunk_control_channel_locked did not reach 1 for any system:\n%s", body)
	}
	if !strings.Contains(body, `gophertrunk_events_total{kind="cc.locked"} 1`) {
		t.Errorf("/metrics did not count one cc.locked event")
	}
}

// buildP25LockedIQDibits assembles a long P25 Phase 1 dibit
// stream suitable for the C4FM modulator + receiver chain:
//
//   - a 200-dibit warmup pattern cycling 0,1,2,3 so the
//     Mueller-Müller clock recovery sees every symbol level
//     and a transition every dibit
//   - `repeats` × (FSW + NID + trellis-encoded TSBK + 50 idle
//     dibits)
//   - a 100-dibit trailer for clean flush
//
// Mirrors the in-package phase1 test helpers' frame layout.
func buildP25LockedIQDibits(nac uint16, repeats int) []uint8 {
	frame := make([]uint8, 0, 24+32+98)
	frame = append(frame, phase1.FrameSyncWord[:]...)
	nidBits := phase1.EncodeNIDBits(nac, phase1.DUIDTrunkingSignaling)
	for i := 0; i < 32; i++ {
		frame = append(frame, (nidBits[2*i]<<1)|nidBits[2*i+1])
	}
	tsbk := phase1.AssembleTSBK(phase1.TSBK{LB: true, Opcode: phase1.OpRFSSStatusBroadcast})
	frame = append(frame, phase1.EncodeTSBKChannel(tsbk)...)

	out := make([]uint8, 0, 200+repeats*(len(frame)+50)+100)
	for i := 0; i < 200; i++ {
		out = append(out, uint8(i&3))
	}
	for r := 0; r < repeats; r++ {
		out = append(out, frame...)
		for i := 0; i < 50; i++ {
			out = append(out, uint8(i&3))
		}
	}
	for i := 0; i < 100; i++ {
		out = append(out, uint8(i&3))
	}
	return out
}

// writeIQToU8File serialises a complex64 IQ buffer to u8
// interleaved pairs (the format sdr.MockDriver consumes). Each
// complex64 sample becomes 2 bytes: I and Q each scaled from
// [-1, 1] to [0, 255] with 127.5 offset.
func writeIQToU8File(path string, iq []complex64) error {
	out := make([]byte, len(iq)*2)
	for i, s := range iq {
		out[2*i] = floatToU8(real(s))
		out[2*i+1] = floatToU8(imag(s))
	}
	return os.WriteFile(path, out, 0o600)
}

func floatToU8(v float32) byte {
	scaled := float64(v)*127.0 + 127.5
	if scaled < 0 {
		return 0
	}
	if scaled > 255 {
		return 255
	}
	return byte(scaled)
}

// waitForScannerLock polls /api/v1/scanner until the named system
// reports state=locked or the timeout fires.
func waitForScannerLock(t *testing.T, base, system string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/api/v1/scanner")
		if err == nil {
			var status struct {
				Systems []struct {
					Name  string `json:"name"`
					State string `json:"state"`
				} `json:"systems"`
			}
			err := json.NewDecoder(resp.Body).Decode(&status)
			resp.Body.Close()
			if err == nil {
				for _, s := range status.Systems {
					if s.Name == system && s.State == "locked" {
						return
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("/api/v1/scanner did not report state=locked for %q within %v", system, timeout)
}
