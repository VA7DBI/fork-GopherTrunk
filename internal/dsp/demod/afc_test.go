package demod

import (
	"math"
	"testing"
)

// TestCoarseAFCRemovesConstantBias feeds a balanced 4-level stream with
// a constant DC bias (the signature of a carrier-frequency offset on an
// FM-discriminator output) and confirms the tracker estimates the bias
// and subtracts it back to a near-zero-mean stream.
func TestCoarseAFCRemovesConstantBias(t *testing.T) {
	const sps = 10
	const bias = 0.0654 // ≈ 500 Hz offset at 48 kHz, in rad/sample
	levels := []float32{1, 3, -1, -3}

	buf := make([]float32, 20000)
	for i := range buf {
		buf[i] = levels[i%4] + float32(bias)
	}
	afc := NewCoarseAFC(sps)
	afc.Process(buf)

	if math.Abs(afc.Offset()-bias) > 0.05*bias {
		t.Errorf("Offset() = %.5f, want within 5%% of %.5f", afc.Offset(), bias)
	}
	// After convergence the corrected stream's mean should be ~0 (the
	// balanced levels), not the input mean of `bias`.
	var sum float64
	tail := buf[len(buf)-4000:]
	for _, x := range tail {
		sum += float64(x)
	}
	if mean := sum / float64(len(tail)); math.Abs(mean) > 0.01 {
		t.Errorf("post-AFC tail mean = %.4f, want ≈ 0", mean)
	}
}

// TestCoarseAFCResetClearsEstimate confirms Reset returns the tracker
// to its initial state so a re-tune doesn't carry a stale offset.
func TestCoarseAFCResetClearsEstimate(t *testing.T) {
	afc := NewCoarseAFC(10)
	buf := make([]float32, 5000)
	for i := range buf {
		buf[i] = 0.2
	}
	afc.Process(buf)
	if afc.Offset() == 0 {
		t.Fatalf("Offset() = 0 after processing a biased stream, want non-zero")
	}
	afc.Reset()
	if afc.Offset() != 0 {
		t.Errorf("Offset() = %v after Reset, want 0", afc.Offset())
	}
}

// TestNewCoarseAFCRejectsBadSps documents the constructor precondition.
func TestNewCoarseAFCRejectsBadSps(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewCoarseAFC(0) did not panic")
		}
	}()
	NewCoarseAFC(0)
}

// TestCoarseAFCSubtractDoesNotUpdate confirms Subtract removes the
// current estimate but leaves it alone — the "frozen" mode the
// receiver flips to after the DDA takes over. Without this contract,
// folding CoarseAFC.Offset() into the DDA and continuing to call
// Process would double-update on every batch.
func TestCoarseAFCSubtractDoesNotUpdate(t *testing.T) {
	afc := NewCoarseAFC(10)
	afc.SetOffset(0.1)

	buf := []float32{1, 2, 3, 4, 5}
	afc.Subtract(buf)

	if afc.Offset() != 0.1 {
		t.Errorf("Offset() = %v after Subtract, want 0.1 (unchanged)", afc.Offset())
	}
	want := []float32{0.9, 1.9, 2.9, 3.9, 4.9}
	for i, v := range buf {
		if math.Abs(float64(v-want[i])) > 1e-6 {
			t.Errorf("buf[%d] = %v, want %v", i, v, want[i])
		}
	}
}

// TestDDAImmuneToDataMean: the entire point of the DDA. Feed a stream
// of all-outer-positive symbols (the most pathological case for an
// open-loop integrator — mean is +slicerScale, not zero) with zero
// carrier offset and correct decisions, and confirm the DDA's
// estimate stays at zero. Issue #402: this is what CoarseAFC fails to
// do, and why afc_hz_est swings 2 kHz / 20 kHz on a locked CC.
func TestDDAImmuneToDataMean(t *testing.T) {
	const (
		slicerScale = 0.2356 // 2π·1800/48000
		sampleRate  = 48000.0
		maxOffset   = 25000.0
	)
	dda := NewDecisionDirectedAFC(maxOffset, sampleRate, slicerScale)

	for i := 0; i < 4*ddaSymbols; i++ {
		// All +3 outer: huge data mean, but correct decisions.
		soft := float32(slicerScale)
		expected := float32(slicerScale)
		dda.Update(soft, expected, 1.0)
	}

	if math.Abs(dda.Offset()) > 1e-6 {
		t.Errorf("DDA Offset() = %v on biased-but-correct-decision stream, want ~0 — DDA tracked the data mean", dda.Offset())
	}
}

// TestDDATracksCarrierOffsetUnderFeedback: the DDA is wired in the
// receiver as a control loop — the matched-filter buffer is
// corrected by `−dc` before sampling, so the residual fed back to
// the next Update is `true_offset − dc`. This test mirrors that
// feedback so the integrator's zero-error fixed point (dc =
// true_offset) is the convergence target.
//
// Without the feedback simulation an open-loop test could only
// assert "integrator grows" — useless for validating the loop's
// equilibrium. This test pins the loop's math; the receiver-level
// handoff/lock test (TestReceiverDDAHandoffFiresOnCleanLockedStream)
// exercises the real chain end-to-end.
func TestDDATracksCarrierOffsetUnderFeedback(t *testing.T) {
	const (
		slicerScale = 0.2356
		sampleRate  = 48000.0
		maxOffset   = 25000.0
		carrierBias = 0.04 // rad/sample, well inside the gate
	)
	dda := NewDecisionDirectedAFC(maxOffset, sampleRate, slicerScale)

	syms := []float64{slicerScale, slicerScale / 3, -slicerScale / 3, -slicerScale}
	for i := 0; i < 12*ddaSymbols; i++ {
		expected := float32(syms[i%4])
		// Simulate the receiver's correction: subtract the
		// current dc before forming soft.
		soft := float32(syms[i%4] + carrierBias - dda.Offset())
		dda.Update(soft, expected, 1.0)
	}

	if math.Abs(dda.Offset()-carrierBias) > 0.05*carrierBias {
		t.Errorf("DDA Offset() = %.5f, want within 5%% of %.5f (the carrier bias)", dda.Offset(), carrierBias)
	}
}

// TestDDAGateRejectsWrongDecisions: residuals larger than the gate
// (slicerScaleNorm / 3) are skipped, so a stream of mis-sliced
// symbols can't teach the loop a phantom offset. Update returns
// false on a skipped sample.
func TestDDAGateRejectsWrongDecisions(t *testing.T) {
	const (
		slicerScale = 0.2356
		sampleRate  = 48000.0
		maxOffset   = 25000.0
	)
	dda := NewDecisionDirectedAFC(maxOffset, sampleRate, slicerScale)

	// soft is at +slicerScale (a real outer), but expected says +1
	// (slicerScale/3) — residual ≈ 2*slicerScale/3, well past gate.
	for i := 0; i < ddaSymbols; i++ {
		if dda.Update(float32(slicerScale), float32(slicerScale/3), 1.0) {
			t.Fatalf("Update returned true on a residual past the gate")
		}
	}
	if dda.Offset() != 0 {
		t.Errorf("Offset() = %v after gated-out updates, want 0", dda.Offset())
	}
}

// TestDDAClampHolds: the integrator caps at ±2π·maxOffsetHz/Fs even
// when agcUnscale amplifies the residual. AddOffset hits the same
// clamp so the handoff from CoarseAFC can't smuggle in an out-of-
// range estimate. The integrator (dc += β·r) grows without bound on
// a constant non-zero residual, so this test runs without
// simulating receiver feedback — the clamp is the only stop.
func TestDDAClampHolds(t *testing.T) {
	const (
		slicerScale = 0.2356
		sampleRate  = 48000.0
		maxOffset   = 5000.0
	)
	dda := NewDecisionDirectedAFC(maxOffset, sampleRate, slicerScale)
	clamp := 2.0 * math.Pi * maxOffset / sampleRate

	// Negative within-gate residual amplified by agcUnscale=100
	// integrates rapidly past the clamp — verifies the safety net.
	for i := 0; i < 100*ddaSymbols; i++ {
		dda.Update(float32(slicerScale*0.7), float32(slicerScale), 100.0)
	}
	if math.Abs(dda.Offset()-(-clamp)) > 1e-6 {
		t.Errorf("Offset() = %v, want exactly -clamp = %v after sustained adversarial updates", dda.Offset(), -clamp)
	}

	dda.Reset()
	dda.AddOffset(2 * clamp)
	if dda.Offset() != clamp {
		t.Errorf("AddOffset past clamp: Offset() = %v, want clamp = %v", dda.Offset(), clamp)
	}
}

// TestNewDecisionDirectedAFCRejectsBadArgs documents the constructor
// preconditions — symmetric with TestNewCoarseAFCRejectsBadSps.
func TestNewDecisionDirectedAFCRejectsBadArgs(t *testing.T) {
	cases := []struct {
		name                                string
		maxOff, sampleRate, slicerScaleNorm float64
	}{
		{"zero maxOff", 0, 48000, 0.2},
		{"zero sampleRate", 25000, 0, 0.2},
		{"zero slicerScale", 25000, 48000, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("constructor accepted %+v", tc)
				}
			}()
			NewDecisionDirectedAFC(tc.maxOff, tc.sampleRate, tc.slicerScaleNorm)
		})
	}
}
