// Package receiver wires the IQ → H-DQPSK dibit chain that feeds the
// P25 Phase 2 control-channel state machine.
//
//	IQ samples
//	  → RRC matched filter (internal/dsp/demod.PiOver4DQPSK)
//	  → naive decimation to one sample per symbol (offset by the
//	    RRC group delay so the centre tap lands on the eye)
//	  → π/8-rotated differential decode → 0..3 dibit
//	  → phase2.DibitSink
//
// P25 Phase 2 uses H-DQPSK at 6000 sym/s with α = 0.20 RRC pulse
// shaping. The constellation is rotated by π/8 from the standard
// π/4-DQPSK reference, which is what the `rotation` argument on the
// PiOver4DQPSK helper compensates for.
//
// The receiver is intentionally minimal: it composes the matched
// filter + decoder and emits one dibit per `sps` complex samples
// from the matched-filter output. Symbol-time clock recovery
// (Gardner-style on complex IQ, or eye-tracking on |y|² envelope)
// is a follow-up — the connector that lands later wraps a proper
// timing-recovery loop around this when a real-air capture is
// available to calibrate against.
//
// The receiver is stateful and not safe for concurrent Process
// calls. Instantiate one per tuned frequency / per call chain.
package receiver

import (
	"math"

	"github.com/MattCheramie/GopherTrunk/internal/dsp/demod"
	"github.com/MattCheramie/GopherTrunk/internal/radio/p25/phase2"
)

// P25 Phase 2 H-DQPSK on-air parameters (TIA-102.BBAB).
const (
	// SymbolRate is the channel symbol rate. Each symbol carries
	// one dibit (2 bits) for a total channel capacity of 12 kbps
	// before TDMA slot multiplexing.
	SymbolRate = 6000.0
	// RolloffAlpha is the RRC pulse-shape roll-off.
	RolloffAlpha = 0.20
	// PulseSpanSymbols is the half-span of the RRC pulse on each
	// side of the symbol time.
	PulseSpanSymbols = 8
	// Rotation is the constellation offset for H-DQPSK. The
	// PiOver4DQPSK helper subtracts this from each phase delta
	// before quadrant classification, so a clean +π/8 phase delta
	// lands squarely in the 0b00 quadrant.
	Rotation = math.Pi / 8
)

// Options configures a Receiver.
type Options struct {
	// SampleRateHz is the IQ sample rate after any upstream
	// channelization. Required; must be ≥ 2 × SymbolRate (12 kHz).
	SampleRateHz float64
	// DibitSink receives the raw dibit stream the receiver decodes
	// from IQ. Required.
	DibitSink phase2.DibitSink
	// PulseSpanSymbols overrides the RRC half-span. <= 0 uses
	// PulseSpanSymbols.
	PulseSpanSymbols int
	// Alpha overrides the RRC roll-off. <= 0 uses RolloffAlpha.
	Alpha float64
}

// Receiver is the composed IQ → dibit pipeline.
type Receiver struct {
	dq        *demod.PiOver4DQPSK
	sps       int
	dibitSink phase2.DibitSink
	dibitBase int
	// rxOffset is the absolute sample index where the next symbol
	// centre should be picked from the matched-filter output. It
	// advances by sps each time we emit a dibit, and wraps when the
	// pending matched-filter buffer is consumed.
	rxOffset int

	// Scratch buffers reused across calls.
	matched []complex64
	dibits  []uint8
	// pending holds matched-filter samples from prior Process calls
	// that didn't align with a symbol centre and are needed for the
	// next decimation step.
	pending []complex64
}

// New constructs a Receiver. Panics if SampleRateHz or DibitSink are
// unset, or the resulting samples-per-symbol is below 2.
func New(opts Options) *Receiver {
	if opts.SampleRateHz <= 0 {
		panic("receiver: SampleRateHz is required")
	}
	if opts.DibitSink == nil {
		panic("receiver: DibitSink is required")
	}
	sps := opts.SampleRateHz / SymbolRate
	if sps < 2 {
		panic("receiver: SampleRateHz must be >= 2*SymbolRate (12000 Hz)")
	}
	span := opts.PulseSpanSymbols
	if span <= 0 {
		span = PulseSpanSymbols
	}
	alpha := opts.Alpha
	if alpha <= 0 {
		alpha = RolloffAlpha
	}
	return &Receiver{
		dq:        demod.NewPiOver4DQPSK(int(sps+0.5), span, alpha, Rotation),
		sps:       int(sps + 0.5),
		dibitSink: opts.DibitSink,
	}
}

// Process pushes one chunk of complex64 IQ samples through the
// matched filter, decimates to symbol time, and emits dibits via
// DibitSink.
func (r *Receiver) Process(iq []complex64) {
	if len(iq) == 0 {
		return
	}
	r.matched = r.dq.MatchedFilter(r.matched, iq)
	r.pending = append(r.pending, r.matched...)

	// Decimate the pending buffer: pick one sample per sps
	// starting at rxOffset.
	r.dibits = r.dibits[:0]
	var symbols []complex64
	for r.rxOffset < len(r.pending) {
		symbols = append(symbols, r.pending[r.rxOffset])
		r.rxOffset += r.sps
	}
	if len(symbols) == 0 {
		return
	}
	r.dibits = r.dq.Decode(r.dibits, symbols)
	r.dibitSink(r.dibits, r.dibitBase)
	r.dibitBase += len(r.dibits)

	// Trim the pending buffer: keep only the samples we haven't
	// consumed yet. rxOffset becomes the offset into the trimmed
	// buffer.
	drop := r.rxOffset - r.sps
	if drop < 0 {
		drop = 0
	}
	if drop > len(r.pending) {
		drop = len(r.pending)
	}
	if drop > 0 {
		copy(r.pending, r.pending[drop:])
		r.pending = r.pending[:len(r.pending)-drop]
		r.rxOffset -= drop
		if r.rxOffset < 0 {
			r.rxOffset = 0
		}
	}
}

// Reset returns the receiver to its initial state. Call on stream
// re-sync (control-channel hunt success, IQ underrun recovery) so
// the matched filter + differential decoder shed their history and
// the DibitSink baseIdx restarts at 0.
func (r *Receiver) Reset() {
	r.dibitBase = 0
	r.dq.Reset()
	r.pending = r.pending[:0]
	r.rxOffset = 0
}
