// Package receiver wires FleetSync protocol decoding onto a live IQ
// stream from the iqtap broker. The pipeline is:
//
//	IQ chunks (Fs Hz, complex64)
//	  -> FM demod (internal/dsp/demod/fm.FM)
//	  -> real resampler to 8 kHz
//	  -> u8 sample quantization
//	  -> fleetync.Demodulator.ProcessSamples
//	  -> events.KindFleetSyncMessage on the bus
package receiver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/MattCheramie/GopherTrunk/internal/dsp"
	"github.com/MattCheramie/GopherTrunk/internal/dsp/demod"
	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/radio/fleetync"
)

// OutputRateHz is the demodulator sample rate FleetSync expects.
const OutputRateHz = 8000

// Options configures a FleetSync receiver.
type Options struct {
	InputRateHz uint32
	SourceName  string
	Version     string // auto|fleetsync1|fleetsync2
	Bus         *events.Bus
	Log         *slog.Logger
}

// Receiver runs FleetSync decode against IQ chunks.
type Receiver struct {
	inputRate uint32
	source    string
	bus       *events.Bus
	log       *slog.Logger

	fm       *demod.FM
	rsmp     *dsp.RealResampler
	demod    *fleetync.Demodulator
	demodBuf []float32
	rsmpBuf  []float32
	u8Buf    []fleetync.Sample

	messagesEmitted atomic.Uint64
}

// RuntimeMetrics is a snapshot of receiver-level telemetry.
type RuntimeMetrics struct {
	MessagesEmitted uint64
	Demod           fleetync.FSyncMetrics
}

// New constructs a receiver and configures demodulator mode.
func New(opts Options) (*Receiver, error) {
	if opts.Bus == nil {
		return nil, errors.New("fleetync/receiver: Bus is required")
	}
	if opts.InputRateHz == 0 {
		return nil, errors.New("fleetync/receiver: InputRateHz is required")
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	out := uint32(OutputRateHz)
	g := gcd(out, opts.InputRateHz)
	L := int(out / g)
	M := int(opts.InputRateHz / g)
	if L == 0 || M == 0 {
		return nil, fmt.Errorf("fleetync/receiver: bad resample ratio L=%d M=%d", L, M)
	}

	d, err := fleetync.NewDemodulator(OutputRateHz)
	if err != nil {
		return nil, err
	}
	d.SetVersion(parseVersion(opts.Version))

	r := &Receiver{
		inputRate: opts.InputRateHz,
		source:    opts.SourceName,
		bus:       opts.Bus,
		log:       log,
		fm:        demod.NewFM(),
		rsmp:      dsp.NewRealResampler(L, M, 16, 7.0),
		demod:     d,
	}
	r.demod.SetMessageCallback(func(msg *fleetync.Message) {
		r.publish(msg)
	})
	return r, nil
}

// Process consumes IQ until context cancellation or input close.
func (r *Receiver) Process(ctx context.Context, in <-chan []complex64) error {
	if in == nil {
		return errors.New("fleetync/receiver: input channel is nil")
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
	r.demodBuf = r.fm.Process(r.demodBuf, chunk)
	r.rsmpBuf = r.rsmp.Process(r.rsmpBuf, r.demodBuf)
	if cap(r.u8Buf) < len(r.rsmpBuf) {
		r.u8Buf = make([]fleetync.Sample, len(r.rsmpBuf))
	} else {
		r.u8Buf = r.u8Buf[:len(r.rsmpBuf)]
	}
	for i, s := range r.rsmpBuf {
		r.u8Buf[i] = floatToU8(s)
	}
	r.demod.ProcessSamples(r.u8Buf)
}

// MessagesEmitted returns the number of bus events published.
func (r *Receiver) MessagesEmitted() uint64 { return r.messagesEmitted.Load() }

// Source returns the configured receiver source label.
func (r *Receiver) Source() string { return r.source }

// Metrics returns receiver runtime telemetry for diagnostics.
func (r *Receiver) Metrics() RuntimeMetrics {
	return RuntimeMetrics{
		MessagesEmitted: r.messagesEmitted.Load(),
		Demod:           r.demod.Metrics(),
	}
}

func (r *Receiver) publish(msg *fleetync.Message) {
	if msg == nil {
		return
	}
	published := *msg
	published.Source = r.source
	r.bus.Publish(events.Event{Kind: events.KindFleetSyncMessage, Payload: published})
	r.messagesEmitted.Add(1)
}

func parseVersion(raw string) fleetync.FSyncVersion {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "fleetsync2":
		return fleetync.VersionFleetSync2
	case "fleetsync1", "auto", "":
		fallthrough
	default:
		return fleetync.VersionFleetSync1
	}
}

func floatToU8(v float32) fleetync.Sample {
	// FM demod output is approximately in [-1,1]. Scale + clamp.
	x := int(v*127.0 + 128.0)
	if x < 0 {
		x = 0
	}
	if x > 255 {
		x = 255
	}
	return fleetync.Sample(uint8(x))
}

func gcd(a, b uint32) uint32 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
