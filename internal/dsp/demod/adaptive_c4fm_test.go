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
	// (~1.6× nominal), slightly compressed positive inner. The fixed +1/+3
	// threshold (2·slicerScale/3 ≈ 0.157) sits close to the +1 rail (0.068,
	// gap 0.089), so noisy +1 symbols leak into +3. The adaptive slicer keeps
	// its +1/+3 boundary anchored a nominal spacing above the tracked +1
	// rail (≈ 0.068 + s/3 ≈ 0.146) — comfortably between the rails — rather
	// than letting a truncation-biased +3 level drag it outward toward the
	// 0.221 midpoint (or, on a closed eye, past it), which is the #402
	// regression covered by TestAdaptiveSlicerNoOuterThresholdRunawayOnClosedEye.
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

	// Outer +1/+3 boundary — what the FSW rides on. The fixed slicer confuses
	// many (its 0.157 threshold sits close to the +1 rail); the adaptive
	// slicer, with its repositioned midpoint near ~0.20, confuses far fewer.
	// We assert at least a 3× reduction: the leak-toward-nominal regularizer
	// settles level[+3] at ~0.33 (not the synthetic-true 0.374), so the
	// midpoint lands a touch below the 0.221 optimum — a deliberate trade of a
	// little balanced-eye accuracy for the safety that stops the #402 runaway.
	fixedConf := posOuterConfusions(fixedOut[warm:], truth[warm:])
	adaptConf := posOuterConfusions(adaptOut[warm:], truth[warm:])
	t.Logf("+1/+3 confusions: fixed=%d adaptive=%d", fixedConf, adaptConf)
	if fixedConf < 50 {
		t.Fatalf("fixed slicer only %d +1/+3 confusions — test setup too gentle to demonstrate the fix", fixedConf)
	}
	if adaptConf*3 > fixedConf {
		t.Errorf("adaptive slicer +1/+3 confusions=%d, want ≤ fixed/3 (=%d)", adaptConf, fixedConf/3)
	}

	// The +1/+3 threshold must land in the FSW-safe band: above the +1 rail
	// (0.068) with margin and near the true midpoint (0.221), but NOT dragged
	// past it toward the ~0.265 runaway that collapsed the outer-heavy FSW on
	// the real capture. With Mechanism B debiasing level[+3] the midpoint lands
	// near ~0.20; the cap only bounds genuine runaway.
	lv := adapt.Levels()
	tPosOuter := posOuterThreshold(lv, slicerScale)
	if tPosOuter < 0.14 || tPosOuter > 0.24 {
		t.Errorf("positive-outer threshold = %.4f, want in [0.14, 0.24] (near the 0.221 midpoint, below the ~0.265 runaway)", tPosOuter)
	}
	// Mechanism B: the soft-responsibility update must converge level[+3]
	// toward the true centroid (~0.374), not the lower-truncated ~0.43 a hard
	// decided-rail EMA produced (the value the #402 diag reported).
	if lv[3] < 0.33 || lv[3] > 0.40 {
		t.Errorf("tracked +3 level = %.4f, want in [0.33, 0.40] (≈0.8·0.374+0.2·nominal; under 0.33 means the rail is under-tracking)", lv[3])
	}
}

// posOuterThreshold mirrors the slicer's anchored +1/+3 decision boundary:
// the inner/outer midpoint, capped at a full nominal inner→outer spacing
// (2·s/3) above the tracked +1 level. Kept local to the test.
func posOuterThreshold(lv [4]float32, slicerScale float64) float32 {
	gap := float32(slicerScale) * 2 / 3
	mid := (lv[2] + lv[3]) / 2
	if cap := lv[2] + gap; mid > cap {
		return cap
	}
	return mid
}

// buildEyeStreamWeighted is buildEyeStream with a non-uniform symbol
// distribution. weights are the relative probabilities of -3,-1,+1,+3 (any
// positive scale); an outer-heavy mix mimics the frame-sync-word preambles
// that most aggressively drove the #402 truncation runaway.
func buildEyeStreamWeighted(rng *rand.Rand, n int, levels [4]float64, noise float64, weights [4]float64) (soft []float32, truth []int8) {
	syms := []int8{-3, -1, 1, 3}
	var total float64
	for _, w := range weights {
		total += w
	}
	pick := func() int {
		x := rng.Float64() * total
		for i, w := range weights {
			if x < w {
				return i
			}
			x -= w
		}
		return 3
	}
	soft = make([]float32, n)
	truth = make([]int8, n)
	for i := 0; i < n; i++ {
		k := pick()
		truth[i] = syms[k]
		soft[i] = float32(levels[k] + rng.NormFloat64()*noise)
	}
	return soft, truth
}

// outerRetention returns the fraction of true `symbol` samples the slicer kept
// as that symbol (e.g. how many true +3 were sliced +3). The FSW is mostly
// outer symbols, so +3 retention collapsing is what kills sync acquisition.
func outerRetention(dst, truth []int8, symbol int8) float64 {
	var kept, total int
	for i := range truth {
		if truth[i] == symbol {
			total++
			if dst[i] == symbol {
				kept++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(kept) / float64(total)
}

// TestAdaptiveSlicerNoOuterThresholdRunawayOnClosedEye reproduces the #432
// regression reported on the real MMR Site 9 capture: on a more-closed,
// outer-heavy eye the original hard decided-rail EMA dragged the +1/+3
// threshold outward (~0.265, well past the 0.221 midpoint), the tracked +3
// level read biased-high (~0.43 vs the true 0.374), and +3 symbols collapsed
// into +1 — FSW hits fell 10→3, dibit +3 share fell to ~11%. The anchored
// thresholds (Mechanism A) + soft-responsibility update (Mechanism B) must
// hold the threshold in the FSW-safe band and keep +3 retention high. These
// assertions fail on the pre-fix slicer.
func TestAdaptiveSlicerNoOuterThresholdRunawayOnClosedEye(t *testing.T) {
	const slicerScale = 0.2356 // 2π·1800/48000
	rng := rand.New(rand.NewSource(7))

	// Real #402 centroids, but a closed eye (noise 0.10) and an outer-heavy
	// mix (+3 favoured, like an FSW preamble) — the conditions that drove the
	// runaway on the real capture.
	asym := [4]float64{-0.239, -0.078, 0.068, 0.374}
	weights := [4]float64{0.15, 0.20, 0.20, 0.45} // -3,-1,+1,+3
	const n = 60_000
	const noise = 0.10
	soft, truth := buildEyeStreamWeighted(rng, n, asym, noise, weights)

	adapt := NewAdaptiveC4FMSlicer(slicerScale)
	adaptOut := adapt.SliceMany(nil, soft)
	const warm = 12_000 // clear warmup + EMA convergence before scoring

	lv := adapt.Levels()
	tPosOuter := posOuterThreshold(lv, slicerScale)
	if tPosOuter > 0.24 {
		t.Errorf("positive-outer threshold = %.4f, want ≤ 0.24 (pre-fix ran away to ~0.265)", tPosOuter)
	}
	if tPosOuter < 0.12 {
		t.Errorf("positive-outer threshold = %.4f, want ≥ 0.12 (must stay above the +1 rail)", tPosOuter)
	}

	// +3 must not collapse into +1 (pre-fix retention fell to ~11%).
	ret := outerRetention(adaptOut[warm:], truth[warm:], 3)
	t.Logf("+3 retention=%.3f  threshold=%.4f  level[+3]=%.4f", ret, tPosOuter, lv[3])
	if ret < 0.85 {
		t.Errorf("+3 retention = %.3f, want ≥ 0.85 (outer symbols must survive for FSW)", ret)
	}

	// Adaptive must be no worse than fixed at the +1/+3 boundary on this eye —
	// the real-capture finding was the regression making it worse.
	fixed := NewC4FMWithTaps([]float32{1}, slicerScale)
	fixedOut := fixed.SliceMany(nil, soft)
	adaptConf := posOuterConfusions(adaptOut[warm:], truth[warm:])
	fixedConf := posOuterConfusions(fixedOut[warm:], truth[warm:])
	t.Logf("+1/+3 confusions: adaptive=%d fixed=%d", adaptConf, fixedConf)
	if adaptConf > fixedConf {
		t.Errorf("adaptive +1/+3 confusions=%d worse than fixed=%d on the closed eye", adaptConf, fixedConf)
	}

	// Mechanism B: tracked +3 level debiased toward the true centroid.
	if lv[3] > 0.40 {
		t.Errorf("tracked +3 level = %.4f, want ≤ 0.40 (pre-fix truncation bias read ~0.43)", lv[3])
	}
}

// TestAdaptiveSlicerOuterRailTrackingGain pins the equilibrium tracking gain
// of the soft-responsibility level update. The intended mix is
// rate/(rate+leak) = 0.8 of the way from nominal to the observed centroid.
// The first DDA cut (#439) scaled only the data pull by responsibility and
// left the leak firing on every sample, which halved the mix to ~0.5 and made
// the outer rail under-track (the #402 follow-up: +3 settled ~0.30 against a
// ~0.40 centroid). This test fails on that code and passes once the leak is
// also responsibility-weighted.
func TestAdaptiveSlicerOuterRailTrackingGain(t *testing.T) {
	const slicerScale = 0.2356
	rng := rand.New(rand.NewSource(11))

	// Balanced eye: negative rail + inner rails nominal, +3 outer stretched to
	// a known centroid. Low noise + wide separation so the EMA converges to its
	// equilibrium without eye-closure bias.
	nom := nominalLevels(slicerScale)
	const c3 = 0.40
	levels := [4]float64{nom[0], nom[1], nom[2], c3}
	const n = 80_000
	soft, _ := buildEyeStream(rng, n, levels, 0.03)

	a := NewAdaptiveC4FMSlicer(slicerScale)
	a.SliceMany(nil, soft)
	lv := a.Levels()

	const mix = 0.8 // rate/(rate+leak) with leak = rate/4
	want := mix*c3 + (1-mix)*nom[3]
	got := float64(lv[3])
	t.Logf("+3 rail: got=%.4f want≈%.4f (centroid=%.3f nominal=%.4f); #439 bug gave ≈%.4f",
		got, want, c3, nom[3], 0.5*c3+0.5*nom[3])
	if math.Abs(got-want) > 0.02 {
		t.Errorf("+3 rail tracking gain off: got=%.4f want≈%.4f (mix should be ~0.8, not the ~0.5 of the #439 bug)", got, want)
	}
	// The negative outer rail's centroid is its nominal, so it must stay put —
	// the leak is responsibility-weighted but a rail at its centroid sees a
	// zero net pull either way.
	if math.Abs(float64(lv[0])-nom[0]) > 0.02 {
		t.Errorf("-3 rail drifted from nominal: got=%.4f want≈%.4f", lv[0], nom[0])
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
