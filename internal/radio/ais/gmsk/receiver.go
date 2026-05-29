// Package gmsk wires the AIS DSP pipeline together: an IQ stream
// from the iqtap broker becomes 9600 Bd GMSK audio, then a
// pre-NRZI bit stream, then HDLC frame bodies, then AIS messages
// on the events bus. The pipeline is:
//
//	IQ chunks (Fs Hz, complex64)
//	  → FM demod (internal/dsp/demod/fm.FM)
//	  → real resampler (internal/dsp/resampler_real) to AudioRateHz
//	  → GFSK matched filter (internal/dsp/demod/gfsk.GFSK, BT=0.4)
//	  → Mueller-Müller symbol-timing recovery
//	    (internal/dsp/sync.MuellerMuller, 8 sps → 1 sample/symbol)
//	  → zero-threshold slicer (raw NRZI bit on the wire)
//	  → NRZI decode (transition = 0, no transition = 1)
//	  → ais/receiver.Receiver.Push(bit)
//	  → events.KindAISMessage on the bus
//
// Layout mirrors internal/radio/aprs/afsk: one Receiver per (SDR,
// AIS-frequency) pair, the daemon pumps the broker subscription
// into Process. The orchestrator at ais/receiver (bits → message
// on the bus) is the inner stage; this package owns IQ-to-bits.
//
// AIS DSP design notes:
//
//   - GMSK is a constant-envelope FM variant with a Gaussian premod
//     filter (BT = 0.4 per ITU-R M.1371-5). FM-discriminating the
//     IQ stream and matched-filtering the resulting audio recovers
//     the data tones cleanly.
//   - Symbol rate is 9600 Bd; Oversample = 8 gives the
//     Mueller-Müller loop enough sub-sample resolution to track
//     the AIS clock at its specified ±50 ppm tolerance.
//   - NRZI is shared with AX.25: 0 → tone transition, 1 → hold.
//     The HDLC framer above us slides through the 24-bit alternating
//     training preamble and locks onto the first 0x7E flag.
package gmsk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/MattCheramie/GopherTrunk/internal/dsp"
	"github.com/MattCheramie/GopherTrunk/internal/dsp/demod"
	dspsync "github.com/MattCheramie/GopherTrunk/internal/dsp/sync"
	"github.com/MattCheramie/GopherTrunk/internal/events"
	aisrx "github.com/MattCheramie/GopherTrunk/internal/radio/ais/receiver"
)

// BaudHz is the AIS signalling rate per ITU-R M.1371-5: 9600 Bd
// fixed on the standard channels.
const BaudHz = 9600

// Oversample is the number of GFSK-matched-filter samples per AIS
// bit fed into the Mueller-Müller timing-recovery loop. 8 mirrors
// the choice in the APRS AFSK receiver — enough sub-sample
// resolution to track clock without overspending CPU.
const Oversample = 8

// AudioRateHz is the fixed audio rate the matched filter runs at.
// baud × Oversample = 9600 × 8 = 76,800.
const AudioRateHz = BaudHz * Oversample

// GaussianBT is the Gaussian premod BT product the AIS transmitter
// uses (ITU-R M.1371-5 §4.1.4 specifies BT = 0.4). The receiver's
// matched filter shape matches.
const GaussianBT = 0.4

// gfskSpan is the span of the Gaussian matched filter in symbols.
// 4 is the conventional choice — captures > 99% of the pulse
// energy at BT = 0.4 without excessive ISI.
const gfskSpan = 4

// mmGain is the Mueller-Müller loop gain. 0.05 mirrors the APRS
// AFSK frontend — fast enough to settle inside an AIS training
// preamble (24 bits at 9600 Bd = 2.5 ms), slow enough that the
// 168-bit payload doesn't yank the clock around.
const mmGain = 0.05

// Options configures a Receiver.
type Options struct {
	// InputRateHz is the IQ sample rate the broker is feeding at.
	// The receiver derives the FM-demod → resampler ratios from
	// this and AudioRateHz.
	InputRateHz uint32

	// SourceName is stamped on log lines and surfaces in metrics.
	// The daemon typically uses the SDR's serial.
	SourceName string

	// Bus is required — messages publish onto KindAISMessage via
	// the ais/receiver orchestrator.
	Bus *events.Bus

	// DropBadFCS, when true, silently drops CRC-failed messages
	// at the orchestrator. Defaults to false — corrupted messages
	// publish with FCSOK=false so the web panel can highlight
	// marginal signals.
	DropBadFCS bool

	// DropNonPosition, when true, silently drops messages without
	// a usable position. Defaults to false — static-data and
	// base-station messages still surface.
	DropNonPosition bool

	// Log is optional; defaults to slog.Default.
	Log *slog.Logger
}

// Receiver runs an AIS / GMSK decode pipeline against a stream of
// IQ chunks. One Receiver per (SDR, AIS-frequency) pair; the
// daemon instantiates one for every entry under ais.channels and
// pumps the broker subscription into Process.
type Receiver struct {
	inputRate uint32
	source    string
	log       *slog.Logger

	fm    *demod.FM
	rsmp  *dsp.RealResampler
	gfsk  *demod.GFSK
	mm    *dspsync.MuellerMuller
	nrzi  *NRZIDecoder
	inner *aisrx.Receiver

	// Per-call scratch buffers reused across Process iterations
	// so the hot path never allocates.
	demodBuf    []float32
	rsmpBuf     []float32
	gfskBuf     []float32
	symBuf      []float32
	samplesSeen atomic.Uint64
	bitsEmitted atomic.Uint64
}

// New constructs a Receiver. Returns an error if opts.Bus is nil or
// InputRateHz is unset.
func New(opts Options) (*Receiver, error) {
	if opts.Bus == nil {
		return nil, errors.New("ais/gmsk: Bus is required")
	}
	if opts.InputRateHz == 0 {
		return nil, errors.New("ais/gmsk: InputRateHz is required")
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	// Rational resampler from InputRateHz down to AudioRateHz.
	// L = AudioRateHz, M = InputRateHz, reduced by GCD.
	out := uint32(AudioRateHz)
	g := gcd(out, opts.InputRateHz)
	L := int(out / g)
	M := int(opts.InputRateHz / g)
	if L == 0 || M == 0 {
		return nil, fmt.Errorf("ais/gmsk: bad resample ratio L=%d M=%d", L, M)
	}

	return &Receiver{
		inputRate: opts.InputRateHz,
		source:    opts.SourceName,
		log:       log,
		fm:        demod.NewFM(),
		rsmp:      dsp.NewRealResampler(L, M, 16, 7.0),
		gfsk:      demod.NewGFSK(Oversample, gfskSpan, GaussianBT),
		mm:        dspsync.NewMuellerMuller(float64(Oversample), mmGain),
		nrzi:      NewNRZIDecoder(),
		inner: aisrx.New(aisrx.Options{
			Bus:             opts.Bus,
			DropBadFCS:      opts.DropBadFCS,
			DropNonPosition: opts.DropNonPosition,
		}),
	}, nil
}

// Process pumps IQ chunks from in through the decode pipeline until
// ctx cancels or in closes. Returns ctx.Err() on cancellation, or
// nil when the input channel closes cleanly.
func (r *Receiver) Process(ctx context.Context, in <-chan []complex64) error {
	if in == nil {
		return errors.New("ais/gmsk: input channel is nil")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-in:
			if !ok {
				return nil
			}
			r.processChunk(chunk)
		}
	}
}

func (r *Receiver) processChunk(chunk []complex64) {
	r.samplesSeen.Add(uint64(len(chunk)))
	r.demodBuf = r.fm.Process(r.demodBuf, chunk)
	r.rsmpBuf = r.rsmp.Process(r.rsmpBuf, r.demodBuf)
	r.gfskBuf = r.gfsk.MatchedFilter(r.gfskBuf, r.rsmpBuf)
	r.symBuf = r.mm.Process(r.symBuf, r.gfskBuf)
	for _, s := range r.symBuf {
		r.feedSymbol(s)
	}
}

// feedSymbol slices one recovered symbol to a raw (pre-NRZI) bit,
// decodes through NRZI, and pushes the logical bit into the
// orchestrator. GMSK is symmetric around DC so the slicer
// threshold is fixed at zero — no DC-tracking needed.
func (r *Receiver) feedSymbol(s float32) {
	var raw byte
	if s > 0 {
		raw = 1
	}
	r.inner.Push(r.nrzi.Decode(raw))
	r.bitsEmitted.Add(1)
}

// Inner returns the bit-stream orchestrator the GMSK frontend is
// driving. Exposed so the daemon can read Stats() for /metrics
// surfacing — there's no other reason to reach inside.
func (r *Receiver) Inner() *aisrx.Receiver { return r.inner }

// Stats reports cumulative DSP-frontend counters. Useful for
// /metrics and end-to-end tests.
type Stats struct {
	IQSamplesSeen uint64 // raw IQ samples Process has consumed
	BitsEmitted   uint64 // bits handed to the orchestrator
}

func (r *Receiver) Stats() Stats {
	return Stats{
		IQSamplesSeen: r.samplesSeen.Load(),
		BitsEmitted:   r.bitsEmitted.Load(),
	}
}

// gcd computes the greatest common divisor via Euclid's algorithm.
// Used to reduce the resample L/M ratio.
func gcd(a, b uint32) uint32 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
