// Package afsk wires the APRS DSP pipeline together: an IQ stream
// from the iqtap broker becomes Bell-202 AFSK audio, then a
// pre-NRZI bit stream, then HDLC frame bodies, then APRS packets
// on the events bus. The pipeline is:
//
//	IQ chunks (Fs Hz, complex64)
//	  → FM demod (internal/dsp/demod/fm.FM)
//	  → real resampler (internal/dsp/resampler_real) to AudioRateHz
//	  → FFSK tone discriminator (internal/dsp/demod/ffsk.FFSK,
//	    markHz=1200, spaceHz=2200)
//	  → Mueller-Müller symbol-timing recovery
//	    (internal/dsp/sync.MuellerMuller, 8 sps → 1 sample/symbol)
//	  → DC-tracking slicer (raw NRZI bit on the wire)
//	  → NRZI decode (transition = 0, no transition = 1)
//	  → aprs/receiver.Receiver.Push(bit)
//	  → events.KindAPRSPacket on the bus
//
// Layout mirrors internal/radio/pager/pocsag/receiver: one Receiver
// per (SDR, APRS-frequency) pair, the daemon pumps the broker
// subscription into Process. The orchestrator at aprs/receiver
// (bits → packet on the bus) is the inner stage; this package owns
// IQ-to-bits.
//
// Bell-202 AFSK signals are messier than POCSAG direct-FSK at the
// slicer (audio-band tones overlap from the FM discriminator's
// noise floor, real radios have variable AGC, the symbol clock
// drifts a few hundred ppm against the nominal 1200 baud), so this
// receiver uses Mueller-Müller closed-loop timing recovery instead
// of POCSAG's open-loop integrator. The recovered-symbol output
// then feeds a slow-mean slicer + NRZI decoder before reaching the
// HDLC framer's sliding-flag detector.
package afsk

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
	aprsrx "github.com/MattCheramie/GopherTrunk/internal/radio/aprs/receiver"
)

// Bell-202 tone frequencies — the AFSK convention APRS / AX.25
// inherits from the 1976 Bell System Data Set 202 modem.
const (
	MarkHz  = 1200.0
	SpaceHz = 2200.0
)

// Oversample is the number of FFSK-discriminator samples per APRS
// bit fed into the Mueller-Müller timing-recovery loop. 8 is the
// same balance the POCSAG receiver uses — enough sub-sample
// resolution for the loop to track the symbol clock, cheap enough
// at 1200 baud that CPU isn't a concern.
const Oversample = 8

// mmGain is the Mueller-Müller loop gain. 0.05 is the conventional
// AFSK-paired starting point — fast enough to settle inside the
// AX.25 preamble (~30 flags = 240 bits), slow enough that random
// payload bytes don't yank the symbol clock around.
const mmGain = 0.05

// BaudHz is the APRS signalling rate. APRS pre-Bell-202 is fixed
// at 1200 baud — VHF / HF variants other than 1200 (the rare 300 Bd
// HF AX.25, 9600 Bd G3RUH FSK) are out of scope for this DSP
// frontend; the bit-stream layers above (hdlc / ax25 / aprs) cope
// fine if a future receiver hands them a 300-Bd bit stream.
const BaudHz = 1200

// AudioRateHz is the fixed audio rate the FFSK discriminator runs
// at. baud × Oversample = 1200 × 8 = 9600, comfortably above the
// Nyquist of the higher (2200 Hz) tone and a round divisor of most
// common SDR sample rates.
const AudioRateHz = BaudHz * Oversample

// Options configures a Receiver.
type Options struct {
	// InputRateHz is the IQ sample rate the broker is feeding at.
	// The receiver derives the FM-demod → resampler ratios from
	// this and AudioRateHz.
	InputRateHz uint32

	// SourceName is stamped on log lines and surfaces in metrics.
	// The daemon typically uses the SDR's serial.
	SourceName string

	// Bus is required — packets publish onto KindAPRSPacket via
	// the aprs/receiver orchestrator.
	Bus *events.Bus

	// DropBadFCS, when true, silently drops CRC-failed frames at
	// the orchestrator. Defaults to false — corrupted frames
	// publish with FCSOK=false so the web panel can highlight
	// marginal signals.
	DropBadFCS bool

	// DropNonUI, when true, silently drops non-UI AX.25 frames at
	// the orchestrator. APRS only emits UI frames; setting this
	// trims rare non-UI noise other AX.25 implementations
	// occasionally put on the channel.
	DropNonUI bool

	// Log is optional; defaults to slog.Default.
	Log *slog.Logger
}

// Receiver runs an APRS / Bell-202 AFSK decode pipeline against a
// stream of IQ chunks. One Receiver per (SDR, APRS-frequency)
// pair; the daemon instantiates one for every entry under
// aprs.channels and pumps the broker subscription into Process.
type Receiver struct {
	inputRate uint32
	source    string
	log       *slog.Logger

	fm    *demod.FM
	rsmp  *dsp.RealResampler
	ffsk  *demod.FFSK
	mm    *dspsync.MuellerMuller
	nrzi  *NRZIDecoder
	inner *aprsrx.Receiver

	// Per-call scratch buffers reused across Process iterations
	// so the hot path never allocates.
	demodBuf    []float32
	rsmpBuf     []float32
	ffskBuf     []float32
	symBuf      []float32
	meanEMA     float32
	meanReady   bool
	samplesSeen atomic.Uint64
	bitsEmitted atomic.Uint64
}

// New constructs a Receiver. Returns an error if opts.Bus is nil or
// InputRateHz is unset.
func New(opts Options) (*Receiver, error) {
	if opts.Bus == nil {
		return nil, errors.New("aprs/afsk: Bus is required")
	}
	if opts.InputRateHz == 0 {
		return nil, errors.New("aprs/afsk: InputRateHz is required")
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
		return nil, fmt.Errorf("aprs/afsk: bad resample ratio L=%d M=%d", L, M)
	}

	return &Receiver{
		inputRate: opts.InputRateHz,
		source:    opts.SourceName,
		log:       log,
		fm:        demod.NewFM(),
		// Same polyphase taps/branch as POCSAG — empirically OK
		// for a wideband-to-narrow step; tune against real
		// fixtures when they land.
		rsmp: dsp.NewRealResampler(L, M, 16, 7.0),
		ffsk: demod.NewFFSK(float64(AudioRateHz), MarkHz, SpaceHz),
		mm:   dspsync.NewMuellerMuller(float64(Oversample), mmGain),
		nrzi: NewNRZIDecoder(),
		inner: aprsrx.New(aprsrx.Options{
			Bus:        opts.Bus,
			DropBadFCS: opts.DropBadFCS,
			DropNonUI:  opts.DropNonUI,
		}),
	}, nil
}

// Process pumps IQ chunks from in through the decode pipeline until
// ctx cancels or in closes. Returns ctx.Err() on cancellation, or
// nil when the input channel closes cleanly.
func (r *Receiver) Process(ctx context.Context, in <-chan []complex64) error {
	if in == nil {
		return errors.New("aprs/afsk: input channel is nil")
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

// processChunk runs one IQ chunk through FM → resample → FFSK
// discrimination → MM symbol-time recovery → slice / NRZI / push.
func (r *Receiver) processChunk(chunk []complex64) {
	r.samplesSeen.Add(uint64(len(chunk)))
	r.demodBuf = r.fm.Process(r.demodBuf, chunk)
	r.rsmpBuf = r.rsmp.Process(r.rsmpBuf, r.demodBuf)
	r.ffskBuf = r.ffsk.Discriminate(r.ffskBuf, r.rsmpBuf)
	r.symBuf = r.mm.Process(r.symBuf, r.ffskBuf)
	for _, s := range r.symBuf {
		r.feedSymbol(s)
	}
}

// feedSymbol consumes one recovered symbol from the timing loop,
// slices it to a raw (pre-NRZI) bit, decodes through NRZI, and
// pushes the logical bit into the orchestrator.
func (r *Receiver) feedSymbol(s float32) {
	// Slow mean tracks DC bias from imperfect tuning. 1/64 EMA at
	// the recovered-symbol rate gives ~50 ms memory at 1200 baud,
	// short enough to settle on a fresh signal, long enough that
	// legitimate flag-byte runs don't yank the threshold around.
	if !r.meanReady {
		r.meanEMA = s
		r.meanReady = true
	} else {
		r.meanEMA += (s - r.meanEMA) * (1.0 / 64.0)
	}

	var raw byte
	if s > r.meanEMA {
		raw = 1
	}
	r.inner.Push(r.nrzi.Decode(raw))
	r.bitsEmitted.Add(1)
}

// Inner returns the bit-stream orchestrator the AFSK frontend is
// driving. Exposed so the daemon can read Stats() for /metrics
// surfacing — there's no other reason to reach inside.
func (r *Receiver) Inner() *aprsrx.Receiver { return r.inner }

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
