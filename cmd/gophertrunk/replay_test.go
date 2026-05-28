package main

import (
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/dsp"
	"github.com/MattCheramie/GopherTrunk/internal/dsp/demod"
	"github.com/MattCheramie/GopherTrunk/internal/events"
	p25phase1 "github.com/MattCheramie/GopherTrunk/internal/radio/p25/phase1"
	p25phase1rx "github.com/MattCheramie/GopherTrunk/internal/radio/p25/phase1/receiver"
	"github.com/MattCheramie/GopherTrunk/internal/scanner/ccdecoder"
)

// TestReplayDDCCompositionLocks mirrors what runReplay does at runtime
// (issue #402 Phase 2): construct the production ccdecoder.Downconverter
// from outside the ccdecoder package, feed it wideband synthetic IQ,
// pipe the decimated output into the same p25phase1rx.Receiver +
// p25phase1.ControlChannel the daemon uses. A lock here proves replay
// reproduces the production decode path exactly, which is the whole
// point of the DDC-in-replay wiring — without it, a capture that
// fails in the daemon would decode (or fail) differently in replay
// because the matched filter would size for 2.4 MHz instead of 48 kHz.
//
// If this test starts failing, the contract that "what the daemon
// decodes is what replay decodes" has broken — the operator's offline
// reproducer is no longer trustworthy.
func TestReplayDDCCompositionLocks(t *testing.T) {
	const (
		nac          = 0x293
		sdrRateHz    = 2_048_000.0
		narrowRateHz = ccdecoder.DDCTargetRateHz
		deviationHz  = 1800.0
		frameRepeats = 30
	)

	// Synthesize the control channel at the narrowband target rate,
	// then upsample (L/M = 128/3) to the raw SDR rate so the
	// Downconverter has genuine wideband IQ to channelize back down.
	// The L/M and helper match the production wideband-DDC test in
	// internal/scanner/ccdecoder/decoder_test.go.
	dibits := buildReplayCCDibits(nac, frameRepeats)
	narrow := demod.ModulateP25C4FM(dibits, narrowRateHz, deviationHz)
	wide := dsp.NewResampler(128, 3, 8, 8.0).Process(nil, narrow)

	bus := events.NewBus(256)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := p25phase1.New(p25phase1.Options{
		Bus:        bus,
		SystemName: "replay-test",
		Rotations:  p25phase1.RotationsC4FM,
	})

	// Build the same Downconverter runReplay builds when the SDR rate
	// exceeds the C4FM target. The receiver must use the achieved
	// output rate, NOT the SDR rate — otherwise the matched filter
	// sizes for 2.4 MHz and the FSW never correlates.
	ddc := ccdecoder.NewDownconverter(sdrRateHz, narrowRateHz)
	receiverRate := ddc.OutRateHz()
	if receiverRate != narrowRateHz {
		t.Fatalf("DDC achieved rate = %v, want %v", receiverRate, narrowRateHz)
	}

	rx := p25phase1rx.New(p25phase1rx.Options{
		SampleRateHz: receiverRate,
		DeviationHz:  deviationHz,
		DemodMode:    p25phase1rx.DemodC4FM,
		DibitSink: func(dibits []uint8, baseIdx int) {
			cc.Process(dibits, baseIdx)
		},
	})

	// Pump in chunks the size of a real RTL-SDR USB transfer (8192
	// complex samples ≈ 19 P25 symbols after the DDC), draining the
	// bus after each so a CCLocked event can't be silently dropped
	// by a full subscriber buffer.
	const chunk = 8_192
	var ddcOut []complex64
	var locked bool
	for off := 0; off < len(wide) && !locked; off += chunk {
		end := off + chunk
		if end > len(wide) {
			end = len(wide)
		}
		ddcOut = ddc.Process(ddcOut, wide[off:end])
		rx.Process(ddcOut)
		for drained := false; !drained; {
			select {
			case ev := <-sub.C:
				if ev.Kind == events.KindCCLocked {
					if ls, ok := ev.Payload.(p25phase1.LockState); ok && ls.NAC == nac {
						locked = true
					}
				}
			default:
				drained = true
			}
		}
	}

	if !locked {
		t.Fatalf("replay composition did NOT lock on NAC %#x after %d wideband samples — production-replay drift",
			nac, len(wide))
	}

	// At least one NID + TSBK must have cleared; otherwise the lock
	// was a fluke and the per-frame stats are zero.
	deadline := time.Now().Add(10 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case <-sub.C:
		default:
			time.Sleep(time.Millisecond)
		}
	}
	got := cc.Stats()
	if got.NIDTrusted == 0 && got.NIDMarginal == 0 {
		t.Errorf("Stats after lock = %+v, want at least one NID-accept", got)
	}
}

// buildReplayCCDibits assembles a P25 Phase 1 control-channel dibit
// stream — warmup + repeats × (FSW + NID + trellis-TSBK + idle) +
// trailer — with the same shape the ccdecoder wideband-DDC test uses,
// duplicated here so this test stays self-contained and doesn't
// depend on a package-internal helper.
func buildReplayCCDibits(nac uint16, repeats int) []uint8 {
	frame := make([]uint8, 0, 24+32+98)
	frame = append(frame, p25phase1.FrameSyncWord[:]...)
	nidBits := p25phase1.EncodeNIDBits(nac, p25phase1.DUIDTrunkingSignaling)
	for i := 0; i < 32; i++ {
		frame = append(frame, (nidBits[2*i]<<1)|nidBits[2*i+1])
	}
	tsbk := p25phase1.AssembleTSBK(p25phase1.TSBK{
		LB: true, Opcode: p25phase1.OpRFSSStatusBroadcast,
	})
	frame = append(frame, p25phase1.EncodeTSBKChannel(tsbk)...)
	frame = p25phase1.InjectControlStatusSymbols(frame)

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
