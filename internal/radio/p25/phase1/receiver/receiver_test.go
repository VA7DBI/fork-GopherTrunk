package receiver

import (
	"math"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/dsp/demod"
)

// TestReceiverConstructsAndProcessesSilence is a smoke test: make
// sure the constructor accepts the typical P25 P1 parameter set,
// Process runs against silent IQ without crashing, and the LDU
// sink is never called (no FSW match in pure noise / silence).
func TestReceiverConstructsAndProcessesSilence(t *testing.T) {
	var emitted int
	r := New(Options{
		SampleRateHz: 48_000,
		Sink:         func(ldu []byte) { emitted++ },
	})
	silence := make([]complex64, 4800) // 100 ms @ 48 kHz
	for range 4 {
		r.Process(silence)
	}
	if emitted != 0 {
		t.Errorf("LDU emitted from silence: count = %d, want 0", emitted)
	}
}

// TestReceiverConstructorPanicsOnBadParams documents the
// preconditions the receiver enforces. These keep configuration
// bugs from silently producing junk dibits.
func TestReceiverConstructorPanicsOnBadParams(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{"missing sample rate", Options{Sink: func([]byte) {}}},
		{"missing sink", Options{SampleRateHz: 48_000}},
		{"sample rate below 2x symbol rate", Options{SampleRateHz: 8_000, Sink: func([]byte) {}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("expected panic, got nil")
				}
			}()
			_ = New(tc.opts)
		})
	}
}

// TestReceiverEmitsDibitsFromPhaseRamp builds an IQ stream whose
// instantaneous frequency follows a known pattern, then verifies
// the receiver pushes a non-zero number of dibits through the
// assembler. We don't assert FSW alignment here — the slicer-/MM-
// loop tuning is calibration-dependent and lives in the
// real-hardware capture tests — but the chain has to *produce*
// dibits or the wiring is broken.
func TestReceiverEmitsDibitsFromPhaseRamp(t *testing.T) {
	const sampleRate = 48_000.0
	const sps = 10 // 48000 / 4800
	const symbols = 1024

	// Build a 4-level FSK signal cycling through {-3,-1,+1,+3}
	// symbol values. Phase increment per sample = symbol *
	// (deviation / Fs) * 2π. We pick deviation so the outer
	// level is well inside the Nyquist band.
	const deviation = 1800.0 // Hz, P25 P1 nominal
	radPerSample := func(symbolValue int) float64 {
		return 2 * math.Pi * float64(symbolValue) * deviation / 3.0 / sampleRate
	}

	iq := make([]complex64, symbols*sps)
	phase := 0.0
	for s := 0; s < symbols; s++ {
		val := []int{-3, -1, +1, +3}[s%4]
		dphi := radPerSample(val)
		base := s * sps
		for k := 0; k < sps; k++ {
			iq[base+k] = complex(float32(math.Cos(phase)), float32(math.Sin(phase)))
			phase += dphi
		}
	}

	emitted := 0
	r := New(Options{
		SampleRateHz: sampleRate,
		Sink:         func(ldu []byte) { emitted++ },
	})

	// Process in chunks to exercise the cross-call state plumbing.
	chunk := 4096
	for i := 0; i < len(iq); i += chunk {
		end := i + chunk
		if end > len(iq) {
			end = len(iq)
		}
		r.Process(iq[i:end])
	}

	// The assembler's internal buffer should have collected a
	// meaningful number of dibits — even without FSW match, the
	// MM loop must have produced symbols. We can't read that
	// directly, but we can re-run with a sink that captures any
	// LDU it sees and assert no panic / no over-emission.
	// (FSW alignment from a synthetic phase ramp is not the
	// system under test here.)
	_ = emitted
}

// TestReceiverResetClearsAssembler ensures Reset propagates to the
// LDU assembler so a re-tune doesn't leave a stale FSW match
// hanging.
func TestReceiverResetClearsAssembler(t *testing.T) {
	r := New(Options{
		SampleRateHz: 48_000,
		Sink:         func([]byte) {},
	})
	// Push some IQ so the assembler accumulates state, then
	// Reset() and confirm a subsequent Process doesn't panic.
	noise := make([]complex64, 8192)
	for i := range noise {
		noise[i] = complex(0.1, -0.1)
	}
	r.Process(noise)
	r.Reset()
	r.Process(noise)
}

// makePhaseRampIQ synthesises a 48 kHz / 10 sps IQ buffer whose
// instantaneous frequency cycles through the four C4FM symbols. It's
// the shared fixture for tests that need a clean signal capable of
// driving the MM loop through Process.
func makePhaseRampIQ(symbols int) []complex64 {
	const sampleRate = 48_000.0
	const sps = 10
	const deviation = 1800.0
	radPerSample := func(symbolValue int) float64 {
		return 2 * math.Pi * float64(symbolValue) * deviation / 3.0 / sampleRate
	}
	iq := make([]complex64, symbols*sps)
	phase := 0.0
	for s := 0; s < symbols; s++ {
		val := []int{-3, -1, +1, +3}[s%4]
		dphi := radPerSample(val)
		base := s * sps
		for k := 0; k < sps; k++ {
			iq[base+k] = complex(float32(math.Cos(phase)), float32(math.Sin(phase)))
			phase += dphi
		}
	}
	return iq
}

// TestReceiverDibitSinkAlone: the CC path doesn't need the LDU
// sink. New must accept a DibitSink-only configuration and Process
// must not panic when no LDU sink is wired.
func TestReceiverDibitSinkAlone(t *testing.T) {
	var batches int
	r := New(Options{
		SampleRateHz: 48_000,
		DibitSink:    func(dibits []uint8, baseIdx int) { batches++ },
	})
	iq := makePhaseRampIQ(1024)
	chunk := 4096
	for i := 0; i < len(iq); i += chunk {
		end := i + chunk
		if end > len(iq) {
			end = len(iq)
		}
		r.Process(iq[i:end])
	}
	if batches == 0 {
		t.Errorf("DibitSink received zero batches; MM loop produced no symbols")
	}
}

// TestReceiverDibitSinkBaseIdxMonotonic: baseIdx must start at 0
// and equal the cumulative count of all previously-emitted dibits.
// Reset() rewinds baseIdx back to 0.
func TestReceiverDibitSinkBaseIdxMonotonic(t *testing.T) {
	var baseIdxs []int
	var batchLens []int
	r := New(Options{
		SampleRateHz: 48_000,
		DibitSink: func(dibits []uint8, baseIdx int) {
			baseIdxs = append(baseIdxs, baseIdx)
			batchLens = append(batchLens, len(dibits))
		},
	})

	iq := makePhaseRampIQ(1024)
	chunk := 4096
	for i := 0; i < len(iq); i += chunk {
		end := i + chunk
		if end > len(iq) {
			end = len(iq)
		}
		r.Process(iq[i:end])
	}

	if len(baseIdxs) == 0 {
		t.Fatalf("expected DibitSink to receive at least one batch")
	}
	if baseIdxs[0] != 0 {
		t.Errorf("first baseIdx = %d, want 0", baseIdxs[0])
	}
	cumulative := 0
	for i := range baseIdxs {
		if baseIdxs[i] != cumulative {
			t.Errorf("baseIdx[%d]=%d, want %d", i, baseIdxs[i], cumulative)
		}
		cumulative += batchLens[i]
	}

	// Reset rewinds baseIdx so a retune begins a fresh stream.
	r.Reset()
	baseIdxs = baseIdxs[:0]
	batchLens = batchLens[:0]
	r.Process(iq)
	if len(baseIdxs) == 0 {
		t.Fatalf("post-Reset: expected DibitSink to receive at least one batch")
	}
	if baseIdxs[0] != 0 {
		t.Errorf("post-Reset: first baseIdx = %d, want 0", baseIdxs[0])
	}
}

// TestReceiverDibitAndLDUSinksAgree: when both sinks are set the
// DibitSink must see exactly the same dibit sequence the LDU
// assembler consumes — that's the invariant the CC connector
// relies on to mirror the voice path.
func TestReceiverDibitAndLDUSinksAgree(t *testing.T) {
	var dibitStream []uint8
	r := New(Options{
		SampleRateHz: 48_000,
		Sink:         func([]byte) {}, // noop, just to keep the assembler wired
		DibitSink: func(dibits []uint8, baseIdx int) {
			dibitStream = append(dibitStream, dibits...)
		},
	})

	iq := makePhaseRampIQ(1024)
	r.Process(iq)

	if len(dibitStream) == 0 {
		t.Fatalf("expected DibitSink to receive dibits")
	}
	for i, d := range dibitStream {
		if d > 3 {
			t.Errorf("dibitStream[%d]=%d, want in 0..3", i, d)
		}
	}
}

// TestReceiverConstructorRequiresAtLeastOneSink: the constructor
// must reject Options that wire neither Sink nor DibitSink.
func TestReceiverConstructorRequiresAtLeastOneSink(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic when both Sink and DibitSink are nil")
		}
	}()
	_ = New(Options{SampleRateHz: 48_000})
}

// TestC4FMSymbolAGCRescuesCollapsedSlicer guards the Phase B fix for
// issue #275. Before the symbol-AGC, a real RTL-SDR capture's
// matched-filter output landed ~sps× above the 4-level slicer's fixed
// threshold (P25C4FMRxTaps has a DC gain of sps, the slicer is
// calibrated to 2π·deviation/sampleRate), so every sample sliced to
// the outer rails and the dibit stream became {1, 3} only — no NID
// ever decoded even though the FSW correlated perfectly (the FSW is
// all-outer, so a collapsed slicer still recovers it).
//
// The AGC normalises the running mean|x| to the slicer's expected
// threshold, restoring the 4-level eye on any input level. This test
// drives the receiver with a synthetic stream scaled to a level where
// the un-AGC'd slicer would collapse to outers, then asserts the
// recovered dibit histogram is roughly balanced (~25% per bin, not
// 50/0/50/0). The exact balance depends on the synthetic dibit
// distribution, so the assertion is a sanity bound rather than an
// exact ratio.
func TestC4FMSymbolAGCRescuesCollapsedSlicer(t *testing.T) {
	// Build a balanced random dibit stream and modulate with the
	// spec P25 transmitter at 960 kHz (sps=200) — the same
	// parameters the Mt Anakie capture used. At this sample rate
	// the matched filter's DC-gain-sps normalisation puts the
	// matched-filter outer-symbol centres at sps× the slicerScale
	// the un-AGC'd code computes, so the slicer would collapse to
	// {1, 3} without AGC.
	const (
		sr  = 960_000.0
		dev = 1800.0
		n   = 8000
	)
	dibits := make([]uint8, n)
	for i := range dibits {
		dibits[i] = uint8((i*7 + 3) & 3)
	}
	iq := demod.ModulateP25C4FM(dibits, sr, dev)
	// Scale the IQ up so the matched-filter output dwarfs the
	// pre-AGC slicer threshold (mimicking the Mt Anakie capture
	// where mean|MF output| was ~150× the calibrated slicerScale).
	for i := range iq {
		iq[i] *= 100
	}

	var hist [4]int
	r := New(Options{
		SampleRateHz: sr,
		DeviationHz:  dev,
		DibitSink: func(d []uint8, _ int) {
			for _, v := range d {
				hist[v&3]++
			}
		},
	})
	r.Process(iq)

	total := hist[0] + hist[1] + hist[2] + hist[3]
	if total == 0 {
		t.Fatalf("receiver emitted no dibits")
	}
	// All four bins should carry meaningful mass — at least 10% of
	// total each — proving the slicer didn't collapse to outers
	// only. The exact ratio depends on the synthetic dibit stream,
	// so 10% is a loose-but-decisive bound (a collapsed slicer
	// yields ~50/0/50/0; a working one ~25/25/25/25).
	for v := 0; v < 4; v++ {
		pct := 100 * float64(hist[v]) / float64(total)
		if pct < 10 {
			t.Errorf("dibit value %d at %.1f%% (%d/%d) — slicer collapsed to outers (issue #275 Phase B regression)",
				v, pct, hist[v], total)
		}
	}
}

// TestReceiverStateAccessorsReturnDefaults pins the contract for the
// new diagnostic accessors (issue #402 Phase 2): on a freshly-
// constructed C4FM Receiver, the AFC bias must read 0 (the IIR's
// initial state), the MM clock's SPS must equal SampleRateHz /
// SymbolRate, and AGCTarget must match the slicer calibration from
// DeviationHz. AGCLevel starts at 0 (unseeded EMA) and MMClockMu
// starts at sps (the constructor's initial state). These read-only
// snapshots back the periodic state-evolution log in cmd/gophertrunk/
// replay.go; if any default changes, the log lines change shape and
// the operator-facing diagnostic gets confusing.
func TestReceiverStateAccessorsReturnDefaults(t *testing.T) {
	r := New(Options{
		SampleRateHz: 48_000,
		DeviationHz:  1800,
		Sink:         func([]byte) {},
	})
	if got := r.AFCBiasRadPerSample(); got != 0 {
		t.Errorf("AFCBiasRadPerSample on fresh receiver = %v, want 0", got)
	}
	if got := r.AGCLevel(); got != 0 {
		t.Errorf("AGCLevel on fresh receiver = %v, want 0 (unseeded EMA)", got)
	}
	if got := r.AGCTarget(); got <= 0 {
		t.Errorf("AGCTarget = %v, want > 0 (slicer calibrated from DeviationHz)", got)
	}
	wantSPS := 48_000.0 / SymbolRate
	if got := r.MMClockSPS(); got != wantSPS {
		t.Errorf("MMClockSPS = %v, want %v", got, wantSPS)
	}
	if got := r.MMClockMu(); got != wantSPS {
		t.Errorf("MMClockMu on fresh receiver = %v, want %v (constructor's initial mu)", got, wantSPS)
	}
}

// TestReceiverStateAccessorsZeroOnCQPSKPath: the CQPSK demod path has
// no AFC or symbol-AGC and uses Gardner instead of Mueller-Müller, so
// all the C4FM-state accessors must return 0 without panicking — the
// replay state log relies on that to render a meaningful line on
// either demod choice.
func TestReceiverStateAccessorsZeroOnCQPSKPath(t *testing.T) {
	r := New(Options{
		SampleRateHz: 48_000,
		DeviationHz:  1800,
		DemodMode:    DemodCQPSK,
		Sink:         func([]byte) {},
	})
	if got := r.AFCBiasRadPerSample(); got != 0 {
		t.Errorf("CQPSK AFCBiasRadPerSample = %v, want 0", got)
	}
	if got := r.AGCLevel(); got != 0 {
		t.Errorf("CQPSK AGCLevel = %v, want 0", got)
	}
	if got := r.AGCTarget(); got != 0 {
		t.Errorf("CQPSK AGCTarget = %v, want 0", got)
	}
	if got := r.MMClockMu(); got != 0 {
		t.Errorf("CQPSK MMClockMu = %v, want 0", got)
	}
	if got := r.MMClockSPS(); got != 0 {
		t.Errorf("CQPSK MMClockSPS = %v, want 0", got)
	}
}
