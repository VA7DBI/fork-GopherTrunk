package main

import (
	"fmt"
	"io"
	"sort"

	p25phase1 "github.com/MattCheramie/GopherTrunk/internal/radio/p25/phase1"
)

// iqDiag accumulates the full dibit stream a replay produced and prints
// a structured demod-quality report at EOF — built for issue #275
// Phase B, where Phase A's NID-search widening (e60d3c5) ruled
// alignment out as the dominant failure mode on the Mt Anakie capture.
// The report surfaces what the existing nid-failure diag cannot:
//
//   - dibit-value histogram: a clean C4FM stream slices ~25% into each
//     of {0,1,2,3}; a strong skew tells the slicer thresholds are
//     mis-calibrated, an empty bin tells the slicer is collapsed.
//
//   - FSW-correlation histogram per rotation: for every dibit position
//     in the buffer, count the best Hamming distance to the 24-dibit
//     FrameSyncWord under each of the 4 cyclic rotations. The shape
//     tells whether the demod produces canonical dibits at all:
//     - tight low-mismatch peak under rot=0 ≈ clean C4FM
//     - peak under rot=2 only ≈ inverted discriminator polarity
//     - peak under rot=1 or rot=3 ≈ I/Q swap or 90° clock-recovery slip
//       (non-physical on a C4FM stream — strong evidence of a bug)
//     - no peak under any rotation ≈ SNR / demod-quality limited
//
//   - hit count per rotation: how many positions had a best-mismatch ≤
//     tolerance under each rotation. Asymmetry across rotations is the
//     same evidence as the peak shape but quantified.
//
// Designed to add nothing to the hot path: only allocates and runs
// when -diag is set.
type iqDiag struct {
	dibits []uint8
	// soft samples (pre-slicer matched-filter output) accumulated
	// from receiver.Options.SoftSink. Used to surface the slicer's
	// decision-region behaviour: if the matched-filter centres land
	// far outside the slicer's threshold, every sample collapses
	// to ±3 and the dibit histogram skews to {1, 3} only.
	soft []float32
}

// observe appends a chunk of dibits to the rolling buffer the EOF
// report walks. Called from the DibitSink in replay.go when -diag
// is set.
func (d *iqDiag) observe(dibits []uint8) {
	d.dibits = append(d.dibits, dibits...)
}

// observeSoft appends pre-slicer per-symbol soft samples to the
// rolling soft buffer.
func (d *iqDiag) observeSoft(soft []float32) {
	d.soft = append(d.soft, soft...)
}

// printReport walks the accumulated dibit stream and emits the full
// demod-quality report to w. Idempotent.
func (d *iqDiag) printReport(w io.Writer) {
	if len(d.dibits) == 0 {
		fmt.Fprintln(w, "diag: no dibits emitted — receiver produced nothing")
		return
	}

	fmt.Fprintln(w, "---- diag ----")
	fmt.Fprintf(w, "diag: %d dibits buffered\n", len(d.dibits))

	// Pre-slicer soft-sample distribution (C4FM path: matched-filter
	// output at symbol times, in rad/sample). For a clean P25 Phase 1
	// signal at 1800 Hz peak deviation, outer centres sit at
	// ±2π·1800/SampleRateHz and inner at ±(1/3) of that, with the
	// slicer threshold at ±(2/3)·outer. If outer-bin counts ≫
	// inner-bin counts, the matched-filter output is bigger than the
	// slicer expects and inner symbols are being misclassified as
	// outer (issue #275 Phase B).
	if len(d.soft) > 0 {
		var sumAbs, maxAbs float64
		var negPosInner, negPosOuter, signZero int
		for _, s := range d.soft {
			a := float64(s)
			if a < 0 {
				a = -a
			}
			sumAbs += a
			if a > maxAbs {
				maxAbs = a
			}
			if s == 0 {
				signZero++
			}
			// Bin into rough quartiles around the abs-mean to spot
			// inner-vs-outer balance.
			_, _ = negPosInner, negPosOuter
		}
		meanAbs := sumAbs / float64(len(d.soft))
		// Threshold at the inner-vs-outer slicer boundary the C4FM
		// stage uses (2/3 of the deviation calibration). Without
		// access to the receiver's deviation here we use meanAbs as
		// a proxy mid-point — most useful as a relative comparison.
		var nInner, nOuter int
		for _, s := range d.soft {
			a := float64(s)
			if a < 0 {
				a = -a
			}
			if a < meanAbs {
				nInner++
			} else {
				nOuter++
			}
		}
		fmt.Fprintf(w, "diag: pre-slicer soft samples: n=%d  mean|x|=%.6f  max|x|=%.6f  <mean|x|: %d (%.1f%%)  ≥mean|x|: %d (%.1f%%)\n",
			len(d.soft), meanAbs, maxAbs,
			nInner, 100*float64(nInner)/float64(len(d.soft)),
			nOuter, 100*float64(nOuter)/float64(len(d.soft)))

		// Coarse histogram of soft sample magnitudes in deciles
		// of maxAbs — surfaces whether the distribution is bimodal
		// (clean 4-level eye: two peaks near ±inner and ±outer
		// centres) or unimodal saturated (everything piled near
		// ±max).
		const bins = 10
		var mhist [bins]int
		for _, s := range d.soft {
			a := float64(s)
			if a < 0 {
				a = -a
			}
			b := int(a / maxAbs * float64(bins))
			if b >= bins {
				b = bins - 1
			}
			mhist[b]++
		}
		fmt.Fprintln(w, "diag: |soft| magnitude histogram (10 bins of max|x|):")
		for b := 0; b < bins; b++ {
			fmt.Fprintf(w, "diag:   [%.3f,%.3f): %7d  (%5.2f%%)\n",
				float64(b)/bins*maxAbs, float64(b+1)/bins*maxAbs,
				mhist[b], 100*float64(mhist[b])/float64(len(d.soft)))
		}
	}

	// Dibit value histogram — should be ~25% per bin on a clean C4FM
	// control channel carrying TSDU traffic. A bin near zero says the
	// 4-level slicer collapsed; a single dominant bin says the signal
	// is below the slicer thresholds and everything quantizes to one
	// symbol.
	var hist [4]int
	for _, db := range d.dibits {
		hist[db&3]++
	}
	fmt.Fprintln(w, "diag: dibit value histogram (expect ~25% per bin on clean C4FM):")
	total := float64(len(d.dibits))
	for v := 0; v < 4; v++ {
		fmt.Fprintf(w, "diag:   %d: %7d  (%5.2f%%)\n", v, hist[v], 100*float64(hist[v])/total)
	}

	// FSW correlation landscape per rotation. At every dibit position,
	// compute the Hamming distance from the next 24 dibits to the
	// canonical FrameSyncWord under each cyclic rotation, and
	// histogram the best distance per rotation. The shape of the
	// distribution at low distances tells whether the demod produces
	// canonical dibits and which rotation aligns the stream.
	type rotStats struct {
		hist     [25]int // distance 0..24
		hits     int     // positions with mismatch ≤ tolerance
		bestDist int     // global minimum across all positions
		bestPos  int
	}
	const fswLen = 24
	const tolerance = 4
	var stats [4]rotStats
	for i := range stats {
		stats[i].bestDist = fswLen + 1
	}
	if len(d.dibits) >= fswLen {
		for pos := 0; pos+fswLen <= len(d.dibits); pos++ {
			for rot := uint8(0); rot < 4; rot++ {
				mismatch := 0
				for kk := 0; kk < fswLen; kk++ {
					if ((d.dibits[pos+kk] + rot) & 3) != p25phase1.FrameSyncWord[kk] {
						mismatch++
					}
				}
				stats[rot].hist[mismatch]++
				if mismatch <= tolerance {
					stats[rot].hits++
				}
				if mismatch < stats[rot].bestDist {
					stats[rot].bestDist = mismatch
					stats[rot].bestPos = pos
				}
			}
		}
	}

	fmt.Fprintln(w, "diag: FSW correlation per rotation (Hamming distance histogram, tolerance=4):")
	fmt.Fprintln(w, "diag:   rot  best_dist  hits≤4   dist=0..6 counts")
	for rot := 0; rot < 4; rot++ {
		s := stats[rot]
		fmt.Fprintf(w, "diag:   %d    %3d (@pos %d)  %6d   %d/%d/%d/%d/%d/%d/%d\n",
			rot, s.bestDist, s.bestPos, s.hits,
			s.hist[0], s.hist[1], s.hist[2], s.hist[3], s.hist[4], s.hist[5], s.hist[6])
	}

	// Pick the rotation with the most low-distance hits as the
	// "winning" rotation, and show the first few hit positions so the
	// reader can spot whether they cluster at regular frame intervals
	// (good) or are scattered (likely false positives).
	winner := 0
	for rot := 1; rot < 4; rot++ {
		if stats[rot].hits > stats[winner].hits {
			winner = rot
		}
	}
	fmt.Fprintf(w, "diag: winning rotation = %d (%d hits ≤ tolerance)\n", winner, stats[winner].hits)
	if stats[winner].hits > 0 {
		positions := make([]int, 0, stats[winner].hits)
		for pos := 0; pos+fswLen <= len(d.dibits); pos++ {
			mismatch := 0
			for kk := 0; kk < fswLen; kk++ {
				if ((d.dibits[pos+kk]+uint8(winner))&3) != p25phase1.FrameSyncWord[kk] {
					mismatch++
				}
			}
			if mismatch <= tolerance {
				positions = append(positions, pos)
			}
		}
		// Inter-hit deltas: a real control channel emits FSWs at
		// frame intervals (~163 on-air dibits including status
		// symbols for a TSBK frame, ~864 + status for an LDU). A
		// modal delta of one of those values says we're detecting
		// real frames; broadly scattered deltas say most hits are
		// false positives.
		var deltas []int
		for i := 1; i < len(positions); i++ {
			deltas = append(deltas, positions[i]-positions[i-1])
		}
		sort.Ints(deltas)
		fmt.Fprintf(w, "diag: first %d hit positions (rot=%d):", min(8, len(positions)), winner)
		for i := 0; i < min(8, len(positions)); i++ {
			fmt.Fprintf(w, " %d", positions[i])
		}
		fmt.Fprintln(w)
		if len(deltas) > 0 {
			modal := modalInt(deltas)
			fmt.Fprintf(w, "diag: inter-hit dibit-deltas: min=%d  median=%d  max=%d  modal=%d  (P25 TSBK frame ≈ 163 on-air dibits, LDU ≈ 864)\n",
				deltas[0], deltas[len(deltas)/2], deltas[len(deltas)-1], modal)
		}

		// For each of the first few perfect-distance FSW hits, dump
		// the FSW + next 32 dibits raw AND run them through the
		// real BCH NID decoder under both strip modes. If any
		// decode, the issue is signal-quality (some NIDs corrupt,
		// some clean); if none decode despite perfect FSW
		// alignment, framing (status-symbol offset, dibit-bit
		// ordering) is broken.
		printed := 0
		for _, pos := range positions {
			if printed >= 5 {
				break
			}
			mismatch := 0
			for kk := 0; kk < fswLen; kk++ {
				if ((d.dibits[pos+kk]+uint8(winner))&3) != p25phase1.FrameSyncWord[kk] {
					mismatch++
				}
			}
			if mismatch != 0 || pos+fswLen+33 > len(d.dibits) {
				continue
			}
			printed++
			fmt.Fprintf(w, "diag: FSW@%d (dist=0, rot=%d) — payload[0..31] then BCH:\n", pos, winner)
			rawNID := make([]uint8, 33) // 32 NID + 1 for strip variant
			for j := 0; j < 33; j++ {
				rawNID[j] = (d.dibits[pos+fswLen+j] + uint8(winner)) & 3
			}
			fmt.Fprint(w, "diag:   raw  ")
			for j := 0; j < 32; j++ {
				fmt.Fprintf(w, "%d", rawNID[j])
				if (j+1)%4 == 0 {
					fmt.Fprint(w, " ")
				}
			}
			fmt.Fprintln(w)
			// Strip=false: take payload[0..31] directly
			n0, e0, _, err0 := p25phase1.NIDFromDibitsWithErrors(rawNID[:32])
			// Strip=true: skip payload[11] (the spec status-symbol position
			// when FSW is at frame_pos 0..23 and status falls at 35)
			stripped := make([]uint8, 0, 32)
			stripped = append(stripped, rawNID[:11]...)
			stripped = append(stripped, rawNID[12:33]...)
			n1, e1, _, err1 := p25phase1.NIDFromDibitsWithErrors(stripped)
			fmt.Fprintf(w, "diag:   strip=false: errs=%d nac=%#x duid=%d err=%v\n", e0, n0.NAC, n0.DUID, err0)
			fmt.Fprintf(w, "diag:   strip=true:  errs=%d nac=%#x duid=%d err=%v\n", e1, n1.NAC, n1.DUID, err1)
		}
	}
}

// modalInt returns the most-frequent value in a sorted int slice. Ties
// resolve to the smaller value.
func modalInt(sorted []int) int {
	if len(sorted) == 0 {
		return 0
	}
	bestVal := sorted[0]
	bestCount := 1
	curVal := sorted[0]
	curCount := 1
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == curVal {
			curCount++
		} else {
			if curCount > bestCount {
				bestVal, bestCount = curVal, curCount
			}
			curVal, curCount = sorted[i], 1
		}
	}
	if curCount > bestCount {
		bestVal = curVal
	}
	return bestVal
}

