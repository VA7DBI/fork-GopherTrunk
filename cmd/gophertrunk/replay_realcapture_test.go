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

// TestReplayMMRSite9TuningPipeline exercises the replay channel-tuning path
// against a real MMR Site 9 capture (mmr-s9-cc.cfile: a 5 s slice, GNU Radio
// f32, 48 kHz). The recording is wideband with the channel off-centre, so
// this pins the tuning front-end end-to-end: estimate the dominant carrier,
// shift it to DC (ccdecoder.Downconverter's tuning offset), and run the
// production CQPSK receiver + control-channel chain without panicking.
//
// IMPORTANT — this is a CHARACTERISATION test, not a successful-decode test.
// The CQPSK receiver does NOT yet decode this signal: the recovered symbols
// collapse so the demod emits only dibits 0 and 2 (one bit per symbol lost),
// every NID then BCH-collapses to the all-zero codeword (NAC=0x000 / DUID
// HDU) and zero TSBKs decode. The test asserts that known-broken signature
// so a real fix is forced to update it. The open problem is tracked as the
// #402 CQPSK-decode follow-up.
//
// What this DOES guard today:
//   - dsp.EstimateCarrierOffsetHz is deterministic on real IQ (the spectral
//     peak this capture presents; note it is NOT the true modulation centre,
//     which is part of why decode fails — see #402).
//   - ccdecoder.Downconverter's tuning offset + the CQPSK receiver run the
//     full chain and produce a dibit stream.
func TestReplayMMRSite9TuningPipeline(t *testing.T) {
	const (
		sampleRateHz  = 48_000.0
		wantCarrierHz = 752.0 // dominant spectral peak of this capture
		carrierTolHz  = 40.0
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

	// The auto-tune estimator must be deterministic on real IQ.
	carrier := dsp.EstimateCarrierOffsetHz(iq, sampleRateHz, sampleRateHz*0.5)
	if carrier < wantCarrierHz-carrierTolHz || carrier > wantCarrierHz+carrierTolHz {
		t.Errorf("carrier estimate %.1f Hz outside [%.0f, %.0f]",
			carrier, wantCarrierHz-carrierTolHz, wantCarrierHz+carrierTolHz)
	}

	bus := events.NewBus(1024)
	sub := bus.Subscribe()
	doneEvents := make(chan struct{})
	go func() {
		defer close(doneEvents)
		for range sub.C {
		}
	}()

	cc := p25phase1.New(p25phase1.Options{
		Bus:        bus,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		SystemName: "mmr-s9-test",
		Rotations:  p25phase1.RotationsAll,
	})
	var dibitHist [4]int
	var totalDibits int
	rx := p25phase1rx.New(p25phase1rx.Options{
		SampleRateHz: sampleRateHz,
		DeviationHz:  1800.0,
		DemodMode:    p25phase1rx.DemodCQPSK,
		DibitSink: func(dibits []uint8, baseIdx int) {
			for _, d := range dibits {
				dibitHist[d&3]++
			}
			totalDibits += len(dibits)
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

	if totalDibits == 0 {
		t.Fatalf("pipeline produced no dibits — tuning/demod chain is broken")
	}
	st := cc.Stats()
	odd := dibitHist[1] + dibitHist[3]
	t.Logf("Site 9 CQPSK: carrier=%.1fHz dibits=%d hist=%v (dibit1+3=%.2f%%) NIDTrusted=%d TSBKDecoded=%d",
		carrier, totalDibits, dibitHist,
		100*float64(odd)/float64(totalDibits), st.NIDTrusted, st.TSBKDecoded)

	// Document the collapse: the demod currently emits an essentially binary
	// {0,2} dibit stream. When the #402 CQPSK-decode bug is fixed the outer
	// dibits (1,3) reappear and this assertion must be updated to require a
	// healthy 4-way distribution plus TSBKDecoded > 0.
	if frac := float64(odd) / float64(totalDibits); frac > 0.05 {
		t.Errorf("dibit 1+3 fraction = %.3f (>0.05): the CQPSK collapse may be fixed — "+
			"update this characterisation test to assert real decode (TSBK > 0)", frac)
	}
}
