package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/dsp"
	"github.com/MattCheramie/GopherTrunk/internal/events"
	p25phase1 "github.com/MattCheramie/GopherTrunk/internal/radio/p25/phase1"
	p25phase1rx "github.com/MattCheramie/GopherTrunk/internal/radio/p25/phase1/receiver"
	"github.com/MattCheramie/GopherTrunk/internal/scanner/ccdecoder"
)

// TestReplayMMRSite9CQPSKLocks is a regression guard against a real
// off-centre LSM/CQPSK capture. mmr-s9-cc.cfile is a 5-second slice of an
// MMR Site 9 control-channel recording (GNU Radio f32, 48 kHz). The control
// channel is π/4-DQPSK simulcast sitting ~+750 Hz off centre, so it
// exercises the full replay tuning path: estimate the carrier, shift it to
// DC (ccdecoder.Downconverter's tuning offset), then run the production
// CQPSK receiver + control-channel chain.
//
// It pins two facts established while reverse-engineering the capture
// (issue #402): the auto-tune estimator finds the carrier within tolerance,
// and the CQPSK receiver locks sync + NID on the real signal. TSBK decode
// is deliberately not asserted here — at the time of writing every NID
// collapses to NAC=0/HDU and 0 TSBKs decode (the open CQPSK rotation /
// differential-decode bug); the follow-up that fixes it tightens this test
// to require TSBKDecoded > 0.
func TestReplayMMRSite9CQPSKLocks(t *testing.T) {
	const (
		sampleRateHz  = 48_000.0
		wantCarrierHz = 752.0 // measured offset of the Site 9 control channel
		carrierTolHz  = 40.0
		minNIDTrusted = 8 // 5 s of capture yields ~17; 8 leaves convergence margin
	)

	path := filepath.Join("testdata", "mmr-s9-cc.cfile")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	pairs := len(raw) / 8
	if pairs == 0 {
		t.Fatalf("fixture %s has no samples", path)
	}
	iq := make([]complex64, pairs)
	decodeF32Replay(raw[:pairs*8], iq)

	// Auto-tune: estimate the dominant carrier across the band.
	carrier := dsp.EstimateCarrierOffsetHz(iq, sampleRateHz, sampleRateHz*0.5)
	if carrier < wantCarrierHz-carrierTolHz || carrier > wantCarrierHz+carrierTolHz {
		t.Errorf("carrier estimate %.1f Hz outside [%.0f, %.0f]",
			carrier, wantCarrierHz-carrierTolHz, wantCarrierHz+carrierTolHz)
	}

	// Build the production chain the way replay does: tuning down-converter
	// → CQPSK receiver → control-channel decoder.
	bus := events.NewBus(1024)
	sub := bus.Subscribe()
	doneEvents := make(chan struct{})
	go func() {
		defer close(doneEvents)
		for range sub.C { // drain so the bus never blocks
		}
	}()

	cc := p25phase1.New(p25phase1.Options{
		Bus:        bus,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		SystemName: "mmr-s9-test",
		Rotations:  p25phase1.RotationsAll,
	})
	rx := p25phase1rx.New(p25phase1rx.Options{
		SampleRateHz: sampleRateHz,
		DeviationHz:  1800.0,
		DemodMode:    p25phase1rx.DemodCQPSK,
		DibitSink: func(dibits []uint8, baseIdx int) {
			cc.Process(dibits, baseIdx)
		},
	})
	ddc := ccdecoder.NewDownconverterWithOffset(sampleRateHz, sampleRateHz, carrier)

	const chunk = 8192
	var ddcOut []complex64
	for off := 0; off < len(iq); off += chunk {
		end := off + chunk
		if end > len(iq) {
			end = len(iq)
		}
		ddcOut = ddc.Process(ddcOut, iq[off:end])
		rx.Process(ddcOut)
	}

	bus.Close()
	<-doneEvents

	st := cc.Stats()
	t.Logf("Site 9 CQPSK: carrier=%.1fHz NIDTrusted=%d NIDFailed=%d TSBKDecoded=%d",
		carrier, st.NIDTrusted, st.NIDFailed, st.TSBKDecoded)
	if st.NIDTrusted < minNIDTrusted {
		t.Errorf("NIDTrusted = %d, want >= %d (CQPSK lock regressed on the real capture)",
			st.NIDTrusted, minNIDTrusted)
	}
}
