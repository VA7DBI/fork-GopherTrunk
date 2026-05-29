package demod

// AdaptiveC4FMSlicer is a four-level C4FM slicer that tracks the
// *observed* per-symbol levels and places its decision thresholds at the
// midpoints between adjacent levels, so it follows an asymmetric or
// off-nominal eye that the fixed-threshold C4FM.Slice mis-decides
// (issue #402).
//
// The fixed slicer assumes a symmetric eye whose four levels sit at the
// nominal ±slicerScale (outer) and ±slicerScale/3 (inner), with
// thresholds frozen at 0 and ±2·slicerScale/3. On a real transmitter
// whose deviation is asymmetric — or whose TX pulse shaping doesn't quite
// match the receive matched filter — the demodulated eye is skewed (on
// the MMR Site 9 capture in #402 the +3 rail landed ~60 % high while the
// −3 rail was nominal), and the fixed thresholds slice it wrong: outer
// symbols leak into inner and the frame-sync word (mostly outer symbols)
// collapses. p25_survey / OP25 / SDRTrunk all handle this with adaptive
// level tracking; this is GopherTrunk's equivalent for the P25 Phase 1
// C4FM path.
//
// The whole upstream chain (atan2 FM discriminator, linear matched
// filter, scalar mean|x| AGC) is provably linear and symmetric, so it
// cannot create the skew — the skew is physical, in the received signal,
// and a data-driven slicer is the right place to absorb it.
//
// Safety. A data-driven slicer is a feedback loop, and #402's earlier
// decision-directed AFC showed how such a loop can false-lock. Three
// mechanisms keep this one in check, none of which need an external lock
// signal (the coupling that made the DDA fragile):
//
//   - A warmup window: it decides at the fixed nominal thresholds until
//     the upstream symbol-AGC has settled, so it never adapts to a
//     mis-scaled transient.
//   - A leak toward nominal on every update, so a level only departs
//     nominal as far as the data *persistently* supports it; an open,
//     well-separated eye is tracked, an ambiguous one stays near fixed.
//   - Per-level clamps and a strict-ordering invariant, so the thresholds
//     can't run away on noise / loss-of-signal.
//
// On an open eye — symmetric or asymmetric, which is what a decodable
// signal presents (the #402 capture is open at the production sps) — the
// slicer tracks the eye and decodes at least as well as the fixed slicer,
// and on a clean symmetric eye reproduces its decisions exactly. On a
// pathologically *closed* eye (rails overlapping) decision-directed
// tracking is inherently unreliable for any slicer, fixed or adaptive; the
// three mechanisms above bound the damage rather than eliminate it, and
// such a signal does not decode under the fixed thresholds either.
type AdaptiveC4FMSlicer struct {
	// level[i] is the running EMA of the *signed* soft value for symbols
	// decided into rail i, indexed -3,-1,+1,+3 → 0,1,2,3. Signed (not
	// magnitude) so positive- and negative-rail asymmetry is tracked
	// independently.
	level   [4]float32
	nominal [4]float32 // seed + fallback: {-s, -s/3, +s/3, +s}
	loBound [4]float32 // per-level clamp (signed, ordered with nominal)
	hiBound [4]float32
	rate    float32 // single-pole EMA coefficient
	warmup  int     // symbols to slice at nominal thresholds before adapting
}

// adaptiveSlicerRate is the EMA time constant for level tracking, in
// symbols. ~1/512 (≈107 ms at 4800 baud) is slow enough to ride out the
// symbol-distribution swings a control channel produces (an all-outer FSW
// preamble, an inner-leaning idle run) without chasing them, but fast
// enough to converge onto a site's eye well within the first second of a
// capture.
const adaptiveSlicerRate = 1.0 / 512.0

// adaptiveSlicerLeak is a regularization pull back toward the nominal eye,
// applied to every tracked level each symbol alongside the data-directed
// update. It makes "degrade gracefully to the fixed slicer" a real
// property rather than just a clamp: a level only departs nominal as far
// as the data persistently supports it. At equilibrium a level settles at
// rate/(rate+leak) of the way from nominal to the observed centroid — with
// leak = rate/4 that's 0.8, so a strongly-supported asymmetry (the #402
// +3 rail at ~1.6× nominal) still tracks most of the way, while an
// ambiguous or partly-closed eye — where decision-directed tracking would
// otherwise run away and do *worse* than fixed — is held near nominal.
const adaptiveSlicerLeak = adaptiveSlicerRate / 4

// adaptiveSlicerWarmup is the number of symbols the slicer decides at its
// nominal (fixed-threshold) eye before it starts adapting. Upstream of the
// slicer, the receiver's symbol-AGC is a 1/256 EMA that takes ~1300 symbols
// to settle; until it has, the soft levels are mis-scaled, and adapting to
// that transient drives a tracked level into its clamp where the slow EMA
// leaves it stuck (it slices like the fixed slicer during warmup, so this
// is purely a safe deferral, not a behaviour change). 2048 symbols clears
// the AGC settle with margin and is a tiny fraction of any real capture.
const adaptiveSlicerWarmup = 2048

// NewAdaptiveC4FMSlicer builds a slicer seeded to the nominal symmetric
// eye for the given slicerScale (the post-AGC outer-symbol level, i.e.
// 2π·deviation/sampleRate on the C4FM path — the same value C4FM.Slice
// uses as its deviation). The tracked levels are clamped to a band around
// these nominals so the slicer degrades gracefully to ~fixed-threshold
// behaviour rather than running away.
func NewAdaptiveC4FMSlicer(slicerScale float64) *AdaptiveC4FMSlicer {
	s := float32(slicerScale)
	nominal := [4]float32{-s, -s / 3, s / 3, s}
	a := &AdaptiveC4FMSlicer{
		level:   nominal,
		nominal: nominal,
		rate:    adaptiveSlicerRate,
		warmup:  adaptiveSlicerWarmup,
	}
	// Per-rail clamp bands. Outer rails may stretch generously (the #402
	// site ran +3 at ~1.6× nominal); inner rails are held tighter since a
	// collapsed inner level is the dangerous case (it would pull the zero
	// threshold or the outer threshold onto the wrong side). The bands are
	// chosen so the four clamped ranges cannot overlap into a different
	// rail's nominal, keeping the ordering invariant satisfiable.
	for i := 0; i < 4; i++ {
		mag := a.nominal[i]
		if mag < 0 {
			mag = -mag
		}
		var lo, hi float32
		if i == 0 || i == 3 { // outer
			lo, hi = 0.4*mag, 2.5*mag
		} else { // inner
			lo, hi = 0.2*mag, 1.3*mag
		}
		if a.nominal[i] < 0 {
			a.loBound[i], a.hiBound[i] = -hi, -lo
		} else {
			a.loBound[i], a.hiBound[i] = lo, hi
		}
	}
	return a
}

// thresholds returns the three live decision boundaries derived from the
// tracked levels: the negative-outer (−3/−1), zero (−1/+1), and
// positive-outer (+1/+3) midpoints.
func (a *AdaptiveC4FMSlicer) thresholds() (tNegOuter, tZero, tPosOuter float32) {
	return (a.level[0] + a.level[1]) / 2,
		(a.level[1] + a.level[2]) / 2,
		(a.level[2] + a.level[3]) / 2
}

// slice maps one soft sample to a symbol in {-3,-1,+1,+3} using the live
// midpoint thresholds.
func (a *AdaptiveC4FMSlicer) slice(soft float32) int8 {
	tNegOuter, tZero, tPosOuter := a.thresholds()
	switch {
	case soft >= tPosOuter:
		return 3
	case soft >= tZero:
		return 1
	case soft >= tNegOuter:
		return -1
	default:
		return -3
	}
}

// update folds one decided sample into the tracked level for its rail,
// then re-applies the clamp + ordering invariant.
func (a *AdaptiveC4FMSlicer) update(soft float32, sliced int8) {
	idx := (sliced + 3) / 2 // -3,-1,+1,+3 → 0,1,2,3
	// Data-directed pull toward the observed soft value, plus a leak back
	// toward nominal that regularizes the estimate (see adaptiveSlicerLeak).
	a.level[idx] += a.rate*(soft-a.level[idx]) + adaptiveSlicerLeak*(a.nominal[idx]-a.level[idx])
	a.clampLevel(idx)
	a.enforceOrder()
}

// clampLevel holds a single tracked level inside its nominal band.
func (a *AdaptiveC4FMSlicer) clampLevel(i int8) {
	if a.level[i] < a.loBound[i] {
		a.level[i] = a.loBound[i]
	} else if a.level[i] > a.hiBound[i] {
		a.level[i] = a.hiBound[i]
	}
}

// enforceOrder restores the invariant level[0] < level[1] < 0 < level[2]
// < level[3]. A degenerate stream (e.g. all symbols decided into one
// rail during loss-of-signal) can push levels out of order; when that
// happens the offending level is snapped back to its nominal, which —
// together with the per-level clamp — keeps the thresholds within a
// bounded band of the fixed ones.
func (a *AdaptiveC4FMSlicer) enforceOrder() {
	// Outer rails must sit beyond their inner neighbour; inner rails must
	// keep the correct sign. Reset any violator to nominal.
	if !(a.level[1] < 0) || a.level[1] <= a.level[0] {
		a.level[1] = a.nominal[1]
	}
	if !(a.level[2] > 0) || a.level[2] >= a.level[3] {
		a.level[2] = a.nominal[2]
	}
	if a.level[0] >= a.level[1] {
		a.level[0] = a.nominal[0]
	}
	if a.level[3] <= a.level[2] {
		a.level[3] = a.nominal[3]
	}
}

// SliceMany slices a batch of soft samples, adapting after each decision.
// Mirrors C4FM.SliceMany so the receiver can swap it in for slicing while
// keeping the C4FM matched filter. The adaptation is per-sample and
// deterministic given the input, so the output is identical regardless of
// how the stream is chunked across calls.
func (a *AdaptiveC4FMSlicer) SliceMany(dst []int8, src []float32) []int8 {
	if cap(dst) < len(src) {
		dst = make([]int8, len(src))
	} else {
		dst = dst[:len(src)]
	}
	for i, s := range src {
		sym := a.slice(s)
		dst[i] = sym
		if a.warmup > 0 {
			a.warmup-- // hold at the nominal eye until the AGC has settled
			continue
		}
		a.update(s, sym)
	}
	return dst
}

// Levels returns the four tracked levels (−3,−1,+1,+3 order). Diagnostic:
// lets the replay state log show the slicer converging onto a site's eye.
func (a *AdaptiveC4FMSlicer) Levels() [4]float32 { return a.level }

// Reset restores the tracked levels to the nominal symmetric eye. Call on
// stream re-sync so a stale eye estimate doesn't bleed across the
// discontinuity.
func (a *AdaptiveC4FMSlicer) Reset() {
	a.level = a.nominal
	a.warmup = adaptiveSlicerWarmup
}
