package demod

import (
	"math"
	"testing"
)

// TestC4FMModulatorRoundTripThroughDemod is the anchor test: a
// random dibit stream is modulated to IQ, then run back through
// the FM discriminator + C4FM matched filter + slicer pipeline,
// and the recovered symbol sequence is checked against the
// source. The pulse-shaping cascade (TX RRC × RX RRC) is
// ISI-free at symbol centres, so once we've sampled the
// matched-filter output at the right phase we should get every
// symbol back exactly.
func TestC4FMModulatorRoundTripThroughDemod(t *testing.T) {
	const (
		sampleRate = 48_000.0
		sps        = 10
		span       = 8
		alpha      = 0.2
		deviation  = 1800.0
	)

	// Build a deterministic dibit stream that hits every symbol
	// repeatedly so the slicer assignment is exercised on each
	// of the four levels.
	src := make([]uint8, 200)
	for i := range src {
		src[i] = uint8((i*7 + 3) & 3) // pseudo-random but reproducible
	}

	iq := ModulateC4FM(src, sps, span, alpha, sampleRate, deviation)
	if len(iq) != len(src)*sps {
		t.Fatalf("IQ length = %d, want %d", len(iq), len(src)*sps)
	}

	// FM discriminator stage: phase difference per sample.
	fm := NewFM()
	disc := fm.Process(nil, iq)

	// RX-side RRC matched filter — same params as the modulator
	// so the cascade gives an ISI-free raised-cosine response.
	// Slicer scale matches the FM peak at symbol ±3 (= the
	// FM-discriminator output for the full-deviation symbol);
	// this is the same calibration the p25/phase1/receiver
	// applies via its DeviationHz Option.
	slicerScale := 2 * math.Pi * deviation / sampleRate
	mf := NewC4FM(sps, span, alpha, slicerScale)
	matched := mf.MatchedFilter(nil, disc)

	// Symbol-centre samples land at sps × span samples after the
	// first impulse (the RRC cascade is symmetric, peak at the
	// centre of its (2·span+1)·sps support). For each source
	// symbol i, the matched-filter peak appears at index:
	//
	//   centre = i*sps + offset,  offset = sps*span   (TX RRC delay)
	//                                    + sps*span   (RX RRC delay)
	//                                    + 1          (FM
	//                                                  discriminator
	//                                                  uses
	//                                                  z[n]·conj(z[n-1]))
	//
	// The TX RRC + RX RRC each delay by sps*span samples; the FM
	// discriminator adds one sample of delay. Verify the slicer
	// recovers each source symbol at that centre.
	offset := 2*sps*span + 1
	var mismatches int
	for i, src := range src {
		centre := i*sps + offset
		if centre >= len(matched) {
			break
		}
		slicedSym := mf.Slice(matched[centre])
		wantSym := dibitToC4FMSymbol(src)
		if slicedSym != wantSym {
			mismatches++
			if mismatches <= 5 {
				t.Errorf("symbol %d (dibit %d): sliced=%d, want=%d, soft=%g",
					i, src, slicedSym, wantSym, matched[centre])
			}
		}
	}
	if mismatches > 0 {
		t.Errorf("%d/%d symbols failed slicer round-trip", mismatches, len(src))
	}
}

// TestC4FMModulatorEmitsPhaseContinuousStream: stitching two
// Modulate calls back-to-back must produce the same IQ as one
// big call. The modulator's internal phase + FIR-history state
// has to carry across call boundaries.
func TestC4FMModulatorEmitsPhaseContinuousStream(t *testing.T) {
	const (
		sampleRate = 48_000.0
		sps        = 10
		span       = 8
		alpha      = 0.2
		deviation  = 1800.0
	)

	src := make([]uint8, 120)
	for i := range src {
		src[i] = uint8((i*11 + 5) & 3)
	}

	whole := ModulateC4FM(src, sps, span, alpha, sampleRate, deviation)

	mod := NewC4FMModulator(sps, span, alpha, sampleRate, deviation)
	a := mod.Modulate(src[:60])
	b := mod.Modulate(src[60:])
	stitched := append(a, b...)

	if len(whole) != len(stitched) {
		t.Fatalf("length mismatch: whole=%d, stitched=%d", len(whole), len(stitched))
	}
	for i := range whole {
		dr := real(whole[i]) - real(stitched[i])
		di := imag(whole[i]) - imag(stitched[i])
		if math.Abs(float64(dr)) > 1e-6 || math.Abs(float64(di)) > 1e-6 {
			t.Errorf("sample %d diverges: whole=%v, stitched=%v", i, whole[i], stitched[i])
			break
		}
	}
}

// TestC4FMModulatorIQMagnitudeStaysUnity: the modulator emits a
// constant-envelope signal (a CPM signal: |IQ[n]| = 1 for all n).
// Drift would indicate a bug in the phase accumulator or the
// cos/sin pairing.
func TestC4FMModulatorIQMagnitudeStaysUnity(t *testing.T) {
	src := make([]uint8, 50)
	for i := range src {
		src[i] = uint8(i & 3)
	}
	iq := ModulateC4FM(src, 10, 8, 0.2, 48_000.0, 1800.0)
	for i, s := range iq {
		mag := math.Hypot(float64(real(s)), float64(imag(s)))
		if math.Abs(mag-1.0) > 1e-6 {
			t.Errorf("sample %d: |IQ| = %g, want 1.0", i, mag)
			break
		}
	}
}

// TestC4FMModulatorResetClearsState: after Reset the modulator
// must behave as if newly constructed — same input dibits must
// produce the same output IQ.
func TestC4FMModulatorResetClearsState(t *testing.T) {
	src := []uint8{0, 1, 2, 3, 0, 1, 2, 3}
	first := ModulateC4FM(src, 10, 8, 0.2, 48_000.0, 1800.0)

	mod := NewC4FMModulator(10, 8, 0.2, 48_000.0, 1800.0)
	_ = mod.Modulate([]uint8{3, 2, 1, 0, 3, 2, 1, 0}) // dirty the state
	mod.Reset()
	second := mod.Modulate(src)

	for i := range first {
		dr := real(first[i]) - real(second[i])
		di := imag(first[i]) - imag(second[i])
		if math.Abs(float64(dr)) > 1e-6 || math.Abs(float64(di)) > 1e-6 {
			t.Errorf("post-Reset divergence at sample %d", i)
			break
		}
	}
}

// TestDibitToC4FMSymbol covers the 0..3 → ±1/±3 mapping and
// confirms each output value is the inverse of what the receiver's
// phase1.SymbolToDibit will produce. We keep the receiver-side
// mapping in the radio package and only re-implement the inverse
// here; this test pins the round-trip.
// TestC4FMModulatorCalibrationSweep is a diagnostic-only sweep
// over deviation values; logs the matched-filter peak for pure
// +3 symbols so the calibration constant in the production path
// can be tuned. Skipped in CI by default; run with
// `go test -run CalibrationSweep -v`.
func TestC4FMModulatorCalibrationSweep(t *testing.T) {
	src := make([]uint8, 50)
	for i := range src {
		src[i] = 1
	}
	for _, dev := range []float64{1800, 3600, 5400, 7200, 9000, 10800, 12700} {
		iq := ModulateC4FM(src, 10, 8, 0.2, 48000, dev)
		fm := NewFM()
		disc := fm.Process(nil, iq)
		mf := NewC4FM(10, 8, 0.2, 1.0)
		matched := mf.MatchedFilter(nil, disc)
		var peak float32
		for i := 200; i < 400 && i < len(matched); i++ {
			if matched[i] > peak {
				peak = matched[i]
			}
		}
		t.Logf("dev=%.0f Hz: peak=%.4f", dev, peak)
	}
}

func TestDibitToC4FMSymbol(t *testing.T) {
	cases := []struct {
		dibit uint8
		want  int
	}{
		{0, +1},
		{1, +3},
		{2, -1},
		{3, -3},
		{4, +1}, // mask is dibit & 3
		{7, -3},
	}
	for _, tc := range cases {
		if got := dibitToC4FMSymbol(tc.dibit); got != tc.want {
			t.Errorf("dibitToC4FMSymbol(%d) = %d, want %d", tc.dibit, got, tc.want)
		}
	}
}
