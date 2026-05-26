// Package diag provides developer/diagnostic helpers — IQ-sample
// decimation, energy + bandwidth estimation — that feed the web
// console's Constellation panel. Pure-Go, no CGO.
//
// The Constellation panel renders raw IQ samples as a 2D scatter so
// the operator can visually identify what's on a frequency:
//
//   - PSK / QPSK constellations show as small clusters at the symbol
//     points
//   - FSK shows as two arcs (or four for C4FM)
//   - AM voice shows as a rotating cluster modulated in amplitude
//   - Wideband noise shows as a diffuse circle
//   - DC bias shows as an offset blob
//
// Streaming full-rate IQ to the browser would swamp the wire — a
// 2.048 MS/s stream is 16 MB/s of complex64. Instead a Decimator
// downsamples to a configurable target rate (default 2000 samples/s,
// well below the canvas's render budget) and ships chunks over WS.
package diag

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
)

// DefaultDecimatedRateSPS is the per-WS-subscriber sample rate when
// the client doesn't override it. 2 ksps is enough density for the
// canvas to look "lively" without saturating the network.
const DefaultDecimatedRateSPS = 2000

// IQPoint is one complex sample on the wire. Float32 keeps frames
// compact; clients render directly without conversion.
type IQPoint struct {
	I float32 `json:"i"`
	Q float32 `json:"q"`
}

// IQFrame is one batch of decimated points. Sent periodically (every
// ~50 ms by default) so the browser canvas redraws at a steady rate
// rather than burning CPU on tiny per-sample frames.
type IQFrame struct {
	TimestampNs int64     `json:"ts_ns"`
	SampleRate  uint32    `json:"sample_rate"`
	CenterHz    uint32    `json:"center_hz"`
	Points      []IQPoint `json:"points"`
	// Energy is the average power of the original (pre-decimation)
	// chunk in dBFS — the same shape the spectrum producer reports.
	// Useful as a "is anything there?" indicator while the
	// constellation is being rendered.
	EnergyDBFS float32 `json:"energy_dbfs"`
}

// Decimator consumes a stream of full-rate IQ chunks and emits IQ
// frames at a controlled output rate. The output rate is achieved by
// taking every Nth sample (where N = input_rate / target_rate),
// matching what the spectrum producer's rate-cap loop does at the
// FFT layer — simple, deterministic, no anti-alias filter needed
// because the target rate is a tiny fraction of the input and the
// goal is visualization, not faithful spectral reconstruction.
type Decimator struct {
	inputRateSPS   uint32
	targetRateSPS  uint32
	stride         int
	chunksPerFrame int

	pending []IQPoint

	dropped atomic.Uint64
}

// Options configures a Decimator.
type Options struct {
	// InputRateSPS is the upstream IQ sample rate (the SDR's
	// configured sample rate). Required.
	InputRateSPS uint32
	// TargetRateSPS is the post-decimation sample rate emitted to
	// the WS client. Zero picks DefaultDecimatedRateSPS.
	TargetRateSPS uint32
	// ChunksPerFrame batches multiple downsampled chunks into one
	// IQFrame so the WS write cost stays reasonable. Zero defaults
	// to 4 (about 50 ms of audio at 2 ksps with 1024-sample input
	// chunks at 2.048 MS/s).
	ChunksPerFrame int
}

// New constructs a Decimator. Returns an error if InputRateSPS is
// zero or smaller than TargetRateSPS.
func New(opts Options) (*Decimator, error) {
	if opts.InputRateSPS == 0 {
		return nil, errors.New("diag: InputRateSPS is required")
	}
	target := opts.TargetRateSPS
	if target == 0 {
		target = DefaultDecimatedRateSPS
	}
	if target > opts.InputRateSPS {
		return nil, errors.New("diag: TargetRateSPS must be <= InputRateSPS")
	}
	stride := int(opts.InputRateSPS / target)
	if stride < 1 {
		stride = 1
	}
	chunksPerFrame := opts.ChunksPerFrame
	if chunksPerFrame <= 0 {
		chunksPerFrame = 4
	}
	return &Decimator{
		inputRateSPS:   opts.InputRateSPS,
		targetRateSPS:  target,
		stride:         stride,
		chunksPerFrame: chunksPerFrame,
	}, nil
}

// Run consumes from in and emits IQFrames on out until ctx cancels
// or in closes. centerFreq is captured per-frame via the supplied
// getter so a retune mid-stream is reflected on the next frame.
//
// The getter pattern matches the spectrum producer's SetCenter /
// SetSampleRate hooks; in the daemon both are backed by the iqtap
// broker so they stay coherent with the underlying SDR's tuning.
func (d *Decimator) Run(
	ctx context.Context,
	in <-chan []complex64,
	out chan<- IQFrame,
	centerHz func() uint32,
	timestampNs func() int64,
) error {
	if in == nil {
		return errors.New("diag: input channel is nil")
	}
	if out == nil {
		return errors.New("diag: output channel is nil")
	}

	chunksSeen := 0
	var energySum float64
	var energyCount int

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-in:
			if !ok {
				return nil
			}
			// Compute pre-decimation energy for the diagnostic banner.
			for _, s := range chunk {
				re := float64(real(s))
				im := float64(imag(s))
				energySum += re*re + im*im
			}
			energyCount += len(chunk)

			// Downsample by stride.
			for i := 0; i < len(chunk); i += d.stride {
				s := chunk[i]
				d.pending = append(d.pending, IQPoint{
					I: real(s),
					Q: imag(s),
				})
			}

			chunksSeen++
			if chunksSeen < d.chunksPerFrame {
				continue
			}

			// Emit one frame.
			points := d.pending
			d.pending = make([]IQPoint, 0, len(points))

			mean := 1e-30
			if energyCount > 0 {
				mean = energySum / float64(energyCount)
			}
			if mean < 1e-30 {
				mean = 1e-30
			}
			energy := float32(10 * math.Log10(mean))
			energySum = 0
			energyCount = 0
			chunksSeen = 0

			frame := IQFrame{
				TimestampNs: timestampNs(),
				SampleRate:  d.targetRateSPS,
				CenterHz:    centerHz(),
				Points:      points,
				EnergyDBFS:  energy,
			}
			select {
			case out <- frame:
			default:
				d.dropped.Add(1)
			}
		}
	}
}

// TargetRateSPS reports the configured post-decimation sample rate.
func (d *Decimator) TargetRateSPS() uint32 { return d.targetRateSPS }

// Stride reports the input-to-output downsample ratio.
func (d *Decimator) Stride() int { return d.stride }

// Dropped reports the cumulative frames dropped because a downstream
// subscriber's channel was full. Non-zero is expected in practice on
// a busy SDR; sustained growth indicates a wedged WS client.
func (d *Decimator) Dropped() uint64 { return d.dropped.Load() }
