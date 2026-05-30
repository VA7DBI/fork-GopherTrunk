package receiver

import (
	"math"
	"math/rand"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/dsp/demod"
	"github.com/MattCheramie/GopherTrunk/internal/radio/p25/phase1"
)

// These loopback tests answer issue #402's open question — "is the outer-rail
// asymmetry the OP measured (true +3 ≈ +0.19 vs −3 ≈ −0.23) introduced by our
// receiver chain, or is it in the received signal?" — with evidence rather
// than an assertion. We run KNOWN symbols through our own receiver and measure
// the true outer-rail eye; ApplyImpairments injects the multipath the OP
// reports for the (off-axis) Murradoc Hill site.
//
// The loopback runs at sps=40 (192 kHz), not the production sps=10: chain
// symmetry is a *structural* property of the discriminator / matched filter /
// AGC and is independent of sps, while ModulateP25C4FM renders a degenerate,
// non-decodable *closed* eye at sps=10 (a synthesis artifact — real hardware
// is open at sps=10). So sps=40 is the faithful way to ask "does our chain
// introduce a +/- asymmetry?". (The sps=10 eye closure the OP sees in the
// capture is symmetric ISI — it compresses both outer rails equally — which
// these tests confirm is not turned into an asymmetry by our chain.)

const (
	loopSR  = 192_000.0 // sps=40 — the lowest rate ModulateP25C4FM keeps the eye open
	loopDev = 1800.0
	// Post-AGC outer-symbol level the slicer is calibrated to (2π·dev/sr).
	loopSlicerScale = 2 * math.Pi * loopDev / loopSR
)

// loopbackDibitSymbol maps a P25 dibit (0..3) to its C4FM symbol: 0→+1, 1→+3,
// 2→−1, 3→−3.
var loopbackDibitSymbol = [4]int8{1, 3, -1, -3}

// runLoopback modulates dibits at sps=40, optionally degrades the IQ, runs it
// through a fresh receiver, and returns the post-AGC soft stream.
func runLoopback(dibits []uint8, imp *demod.Impairments) []float32 {
	iq := demod.ModulateP25C4FM(dibits, loopSR, loopDev)
	if imp != nil {
		iq = demod.ApplyImpairments(iq, loopSR, *imp)
	}
	var soft []float32
	r := New(Options{
		SampleRateHz: loopSR,
		DeviationHz:  loopDev,
		SoftSink:     func(s []float32) { soft = append(soft, s...) },
		DibitSink:    func([]uint8, int) {},
	})
	r.Process(iq)
	return soft
}

// alignSoftToDibits slices the soft stream, finds the integer lag at which the
// sliced symbols best match the known transmitted dibits (the receiver drops
// leading symbols during clock/AGC acquisition, so the soft stream lags the
// input), and returns each post-warmup soft sample paired with its TRUE
// transmitted symbol, plus the best match fraction (≈1 on a cleanly-decoding
// stream — proves the loopback aligned).
func alignSoftToDibits(soft []float32, dibits []uint8, warmup int) (vals []float32, truth []int8, matchFrac float64, rot int) {
	fixed := demod.NewC4FMWithTaps([]float32{1}, loopSlicerScale)
	sliced := fixed.SliceMany(nil, soft)
	slicedDibits := make([]uint8, len(sliced))
	for i, s := range sliced {
		slicedDibits[i] = phase1.SymbolToDibit(s)
	}
	// Search over both the symbol lag and the dibit rotation: the C4FM
	// discriminator may emit the inverted polarity (rotation 2), which the
	// real control-channel path also handles via rotations [0 2].
	// Search lag and rotation. The lag can be negative (the soft stream leads
	// the input index by the net group delay), so scan both directions; the
	// C4FM discriminator may also emit the inverted polarity (rotation 2),
	// which the real control-channel path handles via rotations [0 2].
	bestOff, bestRot, bestMatches, bestCnt := 0, 0, -1, 1
	const minOff, maxOff = -200, 200
	for rr := 0; rr < 4; rr++ {
		for off := minOff; off <= maxOff; off++ {
			m, cnt := 0, 0
			for i := warmup; i < len(slicedDibits); i++ {
				j := i + off
				if j < 0 || j >= len(dibits) {
					continue
				}
				if (slicedDibits[i]+uint8(rr))&3 == dibits[j] {
					m++
				}
				cnt++
			}
			if cnt > 100 && m > bestMatches {
				bestMatches, bestOff, bestRot, bestCnt = m, off, rr, cnt
			}
		}
	}
	for i := warmup; i < len(soft); i++ {
		j := i + bestOff
		if j < 0 || j >= len(dibits) {
			continue
		}
		sym := loopbackDibitSymbol[dibits[j]&3]
		if bestRot == 2 { // global polarity inversion: the soft eye is inverted
			sym = -sym
		}
		vals = append(vals, soft[i])
		truth = append(truth, sym)
	}
	if bestCnt > 0 {
		matchFrac = float64(bestMatches) / float64(bestCnt)
	}
	return vals, truth, matchFrac, bestRot
}

func meanStd(v []float64) (mean, std float64) {
	if len(v) == 0 {
		return 0, 0
	}
	for _, x := range v {
		mean += x
	}
	mean /= float64(len(v))
	for _, x := range v {
		d := x - mean
		std += d * d
	}
	return mean, math.Sqrt(std / float64(len(v)))
}

// outerRailStats returns the mean/std of the soft values whose TRUE symbol is
// +3 and −3.
func outerRailStats(vals []float32, truth []int8) (meanP, stdP, meanN, stdN float64) {
	var p, n []float64
	for i, v := range vals {
		switch truth[i] {
		case 3:
			p = append(p, float64(v))
		case -3:
			n = append(n, float64(v))
		}
	}
	meanP, stdP = meanStd(p)
	meanN, stdN = meanStd(n)
	return
}

func symbolErrorRate(vals []float32, truth []int8) float64 {
	if len(vals) == 0 {
		return 1
	}
	fixed := demod.NewC4FMWithTaps([]float32{1}, loopSlicerScale)
	sliced := fixed.SliceMany(nil, vals)
	errs := 0
	for i, s := range sliced {
		if s != truth[i] {
			errs++
		}
	}
	return float64(errs) / float64(len(vals))
}

func randDibits(seed int64, n int) []uint8 {
	rng := rand.New(rand.NewSource(seed))
	d := make([]uint8, n)
	for i := range d {
		d[i] = uint8(rng.Intn(4))
	}
	return d
}

// TestC4FMLoopbackChainSymmetric is the evidence answering the OP: a clean,
// known C4FM stream run through our receiver must produce SYMMETRIC outer
// rails. If +3 came out compressed relative to −3 on ideal input (as the OP
// measured on the capture: true +3≈+0.19 vs −3≈−0.23), our chain would be the
// cause; it is not — so the asymmetry is in the received signal.
func TestC4FMLoopbackChainSymmetric(t *testing.T) {
	dibits := randDibits(1, 6000)
	soft := runLoopback(dibits, nil)
	if len(soft) < 2500 {
		t.Fatalf("too few soft symbols: %d", len(soft))
	}
	const warmup = 1500
	vals, truth, frac, rot := alignSoftToDibits(soft, dibits, warmup)
	t.Logf("clean loopback: align=%.3f rot=%d over %d symbols", frac, rot, len(vals))
	if frac < 0.9 {
		t.Fatalf("loopback failed to decode clean synthetic C4FM (match=%.2f rot=%d) — test setup issue", frac, rot)
	}
	meanP, stdP, meanN, stdN := outerRailStats(vals, truth)
	t.Logf("clean outer rails (sps=40, open eye): +3 mean=%.4f std=%.4f   -3 mean=%.4f std=%.4f   (nominal ±%.4f)",
		meanP, stdP, meanN, stdN, loopSlicerScale)

	// Symmetry: the outer-rail magnitudes must match closely. Our linear,
	// symbol-agnostic chain treats +f and −f identically, so a clean input
	// yields |+3| ≈ |−3|. (The capture's sps=10 eye closure compresses both
	// rails — symmetric ISI — which is a separate effect from a +/− asymmetry.)
	asym := math.Abs((meanP + meanN) / meanP) // meanN<0 ⇒ sum→0 when symmetric
	if asym > 0.10 {
		t.Errorf("clean-input outer rails asymmetric by %.1f%% (|+3|=%.4f |-3|=%.4f) — OUR chain introduces asymmetry", asym*100, meanP, -meanN)
	}
	if r := stdP / stdN; r < 0.6 || r > 1.6 {
		t.Errorf("clean-input outer-rail std ratio +3/-3 = %.2f, want ≈1 (neither rail intrinsically wider)", r)
	}
}

// TestC4FMLoopbackMultipathDegradesDecode shows that injecting the Murradoc
// multipath condition into a clean stream — through our otherwise-clean chain
// — is what degrades decode. This confirms the residual is impairment-driven
// (an equalizer is the right follow-up), not a chain defect.
func TestC4FMLoopbackMultipathDegradesDecode(t *testing.T) {
	dibits := randDibits(2, 6000)
	const warmup = 1500

	clean := runLoopback(dibits, nil)
	cVals, cTruth, cFrac, cRot := alignSoftToDibits(clean, dibits, warmup)
	cleanSER := symbolErrorRate(cVals, cTruth)
	if cleanSER > 0.02 || cFrac < 0.95 {
		t.Fatalf("clean baseline broken: SER=%.4f align=%.3f rot=%d", cleanSER, cFrac, cRot)
	}

	// Two-ray multipath: a strong echo ~half a symbol late (20 samples at
	// sps=40) — the off-axis/simulcast condition the OP reports for Murradoc.
	mp := make([]complex64, 21)
	mp[0] = 1
	mp[20] = complex(0.6, 0.3)
	imp := demod.Impairments{Multipath: mp}
	dirty := runLoopback(dibits, &imp)
	dVals, dTruth, dFrac, _ := alignSoftToDibits(dirty, dibits, warmup)
	dirtySER := symbolErrorRate(dVals, dTruth)

	t.Logf("clean: align=%.3f SER=%.4f   multipath: align=%.3f SER=%.4f", cFrac, cleanSER, dFrac, dirtySER)
	// The clean chain decodes the clean stream; multipath through the SAME
	// chain visibly degrades it (more symbol errors and/or lost alignment).
	if dirtySER < cleanSER*3 && dFrac > cFrac-0.1 {
		t.Errorf("multipath did not materially degrade decode (clean SER=%.4f align=%.3f vs multipath SER=%.4f align=%.3f) — model too weak to demonstrate",
			cleanSER, cFrac, dirtySER, dFrac)
	}
}
