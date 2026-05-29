package demod

import (
	"math"
	"math/rand"
	"testing"
)

// p25 C4FM symbol → dibit-index mapping mirror, kept local to the test.
// symbol −3,−1,+1,+3 → index 0,1,2,3.
func symIdx(sym int8) int { return int((sym + 3) / 2) }

// The four nominal post-AGC levels for slicerScale s: −s, −s/3, +s/3, +s.
func nominalLevels(s float64) [4]float64 {
	return [4]float64{-s, -s / 3, s / 3, s}
}

// buildEyeStream synthesises a soft-sample stream for a known symbol
// sequence by drawing each symbol from its target level plus Gaussian
// noise. Returns the soft samples and the true symbols.
func buildEyeStream(rng *rand.Rand, n int, levels [4]float64, noise float64) (soft []float32, truth []int8) {
	syms := []int8{-3, -1, 1, 3}
	soft = make([]float32, n)
	truth = make([]int8, n)
	for i := 0; i < n; i++ {
		s := syms[rng.Intn(4)]
		truth[i] = s
		soft[i] = float32(levels[symIdx(s)] + rng.NormFloat64()*noise)
	}
	return soft, truth
}

func decodeErrors(dst, truth []int8) int {
	errs := 0
	for i := range truth {
		if dst[i] != truth[i] {
			errs++
		}
	}
	return errs
}

// posOuterConfusions counts +1↔+3 mis-decisions: the positive-outer
// boundary the adaptive slicer repositions. This is the boundary that
// matters for the frame-sync word (which is built almost entirely of outer
// symbols), so it is where an asymmetric eye does its real damage even
// when the overall symbol error-rate looks modest.
func posOuterConfusions(dst, truth []int8) int {
	n := 0
	for i := range truth {
		if (truth[i] == 1 || truth[i] == 3) && (dst[i] == 1 || dst[i] == 3) && dst[i] != truth[i] {
			n++
		}
	}
	return n
}

// TestAdaptiveSlicerRecoversAsymmetricEyeWhereFixedFails is the headline
// issue #402 test: a stream drawn from the MMR Site 9 asymmetric centroids
// (+3 rail stretched to ~1.6× nominal, negative rail nominal) is mis-sliced
// by the fixed-threshold slicer but recovered by the adaptive one.
func TestAdaptiveSlicerRecoversAsymmetricEyeWhereFixedFails(t *testing.T) {
	const slicerScale = 0.2356 // 2π·1800/48000
	rng := rand.New(rand.NewSource(1))

	// Observed #402 eye: nominal negative rail, stretched positive outer
	// (~1.6× nominal), slightly compressed positive inner. With this eye the
	// fixed +1/+3 threshold (2·slicerScale/3 ≈ 0.157) sits far too close to
	// the +1 rail (0.068, gap 0.089) and far from +3 (0.374, gap 0.217), so
	// noisy +1 symbols leak into +3. The correct midpoint is ≈ 0.221.
	asym := [4]float64{-0.239, -0.078, 0.068, 0.374}
	const n = 30_000
	const noise = 0.05
	soft, truth := buildEyeStream(rng, n, asym, noise)

	// Fixed slicer at nominal thresholds (±2·slicerScale/3 ≈ ±0.157).
	fixed := NewC4FMWithTaps([]float32{1}, slicerScale)
	fixedOut := fixed.SliceMany(nil, soft)

	// Adaptive slicer. Skip past the warmup window + EMA convergence
	// (warmup 2048 then a few thousand symbols at rate 1/512) when scoring.
	adapt := NewAdaptiveC4FMSlicer(slicerScale)
	adaptOut := adapt.SliceMany(nil, soft)
	const warm = 10_000

	// Overall: the adaptive slicer must be strictly better. (The inner-rail
	// zero-crossing errors are common-mode — both slicers share them — so
	// the overall gap is modest; the decisive win is at the outer boundary,
	// checked next.)
	fixedErrs := decodeErrors(fixedOut[warm:], truth[warm:])
	adaptErrs := decodeErrors(adaptOut[warm:], truth[warm:])
	t.Logf("overall: fixed=%d adaptive=%d (of %d)", fixedErrs, adaptErrs, n-warm)
	if adaptErrs >= fixedErrs {
		t.Errorf("adaptive slicer no better overall: adaptive=%d fixed=%d", adaptErrs, fixedErrs)
	}

	// Outer +1/+3 boundary — what the FSW rides on. The fixed slicer should
	// confuse many; the adaptive slicer, with its repositioned midpoint
	// threshold, should confuse near-zero. Assert at least a 10× reduction.
	fixedConf := posOuterConfusions(fixedOut[warm:], truth[warm:])
	adaptConf := posOuterConfusions(adaptOut[warm:], truth[warm:])
	t.Logf("+1/+3 confusions: fixed=%d adaptive=%d", fixedConf, adaptConf)
	if fixedConf < 50 {
		t.Fatalf("fixed slicer only %d +1/+3 confusions — test setup too gentle to demonstrate the fix", fixedConf)
	}
	if adaptConf*10 > fixedConf {
		t.Errorf("adaptive slicer +1/+3 confusions=%d, want ≤ fixed/10 (=%d)", adaptConf, fixedConf/10)
	}

	// Thresholds should have converged toward the eye midpoints: the +1/+3
	// boundary near (0.068+0.374)/2 ≈ 0.221, well above the fixed 0.157.
	lv := adapt.Levels()
	tPosOuter := (lv[2] + lv[3]) / 2
	if tPosOuter < 0.18 {
		t.Errorf("positive-outer threshold = %.4f, want ≳ 0.18 (converged toward the 0.221 midpoint)", tPosOuter)
	}
}

// TestAdaptiveSlicerMatchesFixedOnCleanSymmetricEye guards against
// regression on a normal site: on a clean symmetric eye the adaptive
// slicer must decode as well as the fixed one (both near-perfect).
func TestAdaptiveSlicerMatchesFixedOnCleanSymmetricEye(t *testing.T) {
	const slicerScale = 0.2356
	rng := rand.New(rand.NewSource(2))
	sym := nominalLevels(slicerScale)
	const n = 20_000
	soft, truth := buildEyeStream(rng, n, sym, 0.02)

	fixed := NewC4FMWithTaps([]float32{1}, slicerScale)
	fixedErrs := decodeErrors(fixed.SliceMany(nil, soft), truth)

	adapt := NewAdaptiveC4FMSlicer(slicerScale)
	adaptErrs := decodeErrors(adapt.SliceMany(nil, soft), truth)

	if fixedErrs > n/100 {
		t.Fatalf("fixed slicer err=%d on a clean eye — test setup wrong", fixedErrs)
	}
	if adaptErrs > n/100 {
		t.Errorf("adaptive slicer err=%d on a clean symmetric eye, want ≤ %d", adaptErrs, n/100)
	}
}

// TestAdaptiveSlicerChunkBoundaryDeterminism pins that slicing a stream in
// one call and in pieces produces identical output — the determinism the
// receiver relies on across IQ chunk boundaries.
func TestAdaptiveSlicerChunkBoundaryDeterminism(t *testing.T) {
	const slicerScale = 0.2356
	rng := rand.New(rand.NewSource(3))
	soft, _ := buildEyeStream(rng, 5_000, [4]float64{-0.239, -0.078, 0.068, 0.374}, 0.03)

	whole := NewAdaptiveC4FMSlicer(slicerScale).SliceMany(nil, soft)

	piecewise := NewAdaptiveC4FMSlicer(slicerScale)
	var got []int8
	for i := 0; i < len(soft); i += 137 { // odd chunk size
		end := i + 137
		if end > len(soft) {
			end = len(soft)
		}
		out := piecewise.SliceMany(nil, soft[i:end])
		got = append(got, out...)
	}
	if len(got) != len(whole) {
		t.Fatalf("length mismatch: piecewise=%d whole=%d", len(got), len(whole))
	}
	for i := range whole {
		if got[i] != whole[i] {
			t.Fatalf("chunk-boundary nondeterminism at %d: piecewise=%d whole=%d", i, got[i], whole[i])
		}
	}
}

// TestAdaptiveSlicerBoundedOnDegenerateInput pins the safety net: a
// pathological all-positive stream (loss-of-signal / no real eye) must not
// drive the tracked levels out of order or far past their bounds, so the
// thresholds stay within a bounded band of the fixed ones.
func TestAdaptiveSlicerBoundedOnDegenerateInput(t *testing.T) {
	const slicerScale = 0.2356
	a := NewAdaptiveC4FMSlicer(slicerScale)
	soft := make([]float32, 10_000)
	for i := range soft {
		soft[i] = float32(slicerScale * 5) // way above any rail, all one sign
	}
	a.SliceMany(nil, soft)
	lv := a.Levels()

	// Ordering invariant must hold.
	if !(lv[0] < lv[1] && lv[1] < 0 && lv[2] > 0 && lv[2] < lv[3]) {
		t.Errorf("ordering invariant violated after degenerate input: levels=%v", lv)
	}
	// Each level stays within its clamp band (outer ≤ 2.5×, inner ≤ 1.3×).
	if math.Abs(float64(lv[3])) > 2.5*slicerScale+1e-6 {
		t.Errorf("positive-outer level %.4f exceeded 2.5× bound", lv[3])
	}
	if math.Abs(float64(lv[0])) > 2.5*slicerScale+1e-6 {
		t.Errorf("negative-outer level %.4f exceeded 2.5× bound", lv[0])
	}
}

// TestAdaptiveSlicerResetRestoresNominal confirms Reset wipes the tracked
// eye back to the symmetric nominal so a stream re-sync starts clean.
func TestAdaptiveSlicerResetRestoresNominal(t *testing.T) {
	const slicerScale = 0.2356
	a := NewAdaptiveC4FMSlicer(slicerScale)
	want := NewAdaptiveC4FMSlicer(slicerScale).Levels() // pristine nominal
	rng := rand.New(rand.NewSource(4))
	soft, _ := buildEyeStream(rng, 10_000, [4]float64{-0.239, -0.078, 0.068, 0.374}, 0.03)
	a.SliceMany(nil, soft)
	if a.Levels() == want {
		t.Fatal("levels never moved from nominal — test setup wrong")
	}
	a.Reset()
	if a.Levels() != want {
		t.Errorf("after Reset levels=%v, want nominal %v", a.Levels(), want)
	}
}
