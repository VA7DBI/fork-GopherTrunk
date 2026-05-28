// Package wbvoice puts P25 / DMR voice grants on the same SDR that's
// hosting a trunked control channel via the wideband channelizer.
//
// A wideband dongle is pinned to a centre frequency that spans the
// system's IQ band (typically 2.4 MS/s ≈ ±1.08 MHz). The control
// channel is already decoded by internal/scanner/widebandt2 from a
// fixed-offset tap on that band. Voice grants land on different
// frequencies — but, on most P25 / DMR systems, the same band the
// CC sits in covers the voice carriers too. A VirtualTuner is the
// glue that lets the trunking voice pool follow a grant by allocating
// a per-call DDC tap on the wideband IQ stream instead of retuning
// a separate role: voice dongle.
//
// Each VirtualTuner implements both trunking.Tuner (SetCenterFreq)
// and the composer's IQSource (StreamIQ + SampleRateHz) — so it
// plugs into the existing voice-pool + composer machinery as if it
// were a physical SDR with a sample rate of 48 kHz. The voice pool's
// engine calls SetCenterFreq once at grant binding; the composer's
// per-call chain then opens StreamIQ and consumes the decimated
// narrow-band stream until the call ends.
//
// Out-of-window grants (the requested centre frequency falls outside
// the wideband dongle's IQ band, minus a 5 % guard) return
// ErrOutOfBand from SetCenterFreq. The trunking engine treats that
// as a non-fatal "wrong tuner for this grant" signal and tries the
// next free device — so a daemon configured with one wideband
// dongle plus a backup physical role: voice SDR still follows the
// out-of-window grants on the physical SDR.
package wbvoice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/MattCheramie/GopherTrunk/internal/dsp/tuner"
	"github.com/MattCheramie/GopherTrunk/internal/sdr/iqtap"
)

// NarrowbandRateHz is the per-tap sample rate the virtual tuner emits.
// 48 kHz matches the rate every composer voice chain (DMR, P25 Phase
// 1, P25 Phase 2) targets after decimation — so the composer's
// existing decimator collapses to a no-op when the source IS a
// virtual tuner. See composer.runP25Phase1VoiceChain etc.
const NarrowbandRateHz = 48_000

// GuardFrac mirrors the constant the wideband config validator uses
// (5 % of the IQ band at each edge) so a frequency that's accepted
// here also makes it past load-time validation in nearby code paths.
const GuardFrac = 0.05

// ErrOutOfBand is returned by SetCenterFreq when the requested centre
// frequency lies outside the wideband dongle's usable IQ window.
// The trunking engine surfaces this sentinel to fall back to a
// physical voice SDR instead of dropping the grant.
var ErrOutOfBand = errors.New("wbvoice: target frequency out of wideband window")

// VirtualTuner is one per-grant DDC tap on a shared wideband SDR's
// IQ stream. It is safe for concurrent use by the trunking engine
// (SetCenterFreq) and the composer (StreamIQ); the two interact
// through targetHz under tunerMu.
type VirtualTuner struct {
	serial     string
	log        *slog.Logger
	broker     *iqtap.Broker
	widebandHz uint32
	inRateHz   uint32

	tunerMu  sync.Mutex
	targetHz uint32 // 0 ⇒ no SetCenterFreq yet
}

// Options bundle the per-tap inputs the daemon supplies at startup.
type Options struct {
	// Serial uniquely identifies this virtual tuner across the
	// daemon's voice pool + composer.FindBySerial lookups. Picked
	// by the caller (typically "<wideband-serial>:tap-N").
	Serial string
	// Broker wraps the underlying wideband SDR's IQ stream. The
	// virtual tuner subscribes per StreamIQ call; chunks delivered
	// to subscribers are the same wide-band IQ the channelizer
	// sees, stamped with the dongle's centre frequency.
	Broker *iqtap.Broker
	// WidebandCenterHz is the centre frequency the wideband dongle
	// is pinned to. The DDC mixes by (target − wideband) Hz to
	// shift the voice carrier to baseband before decimation.
	WidebandCenterHz uint32
	// SDRSampleRateHz is the chunk rate the broker delivers. Used
	// to size the DDC's decimation ratio and to validate the
	// requested centre frequency against the usable IQ window.
	SDRSampleRateHz uint32
	// Log labels diagnostics. Optional; defaults to slog.Default().
	Log *slog.Logger
}

// New validates opts and returns a VirtualTuner ready to plug into
// the voice pool. Returns an error when opts are obviously invalid
// (zero rates, nil broker) — that's a daemon wiring bug, not an
// operator config one.
func New(opts Options) (*VirtualTuner, error) {
	if opts.Broker == nil {
		return nil, errors.New("wbvoice: Broker is required")
	}
	if opts.WidebandCenterHz == 0 {
		return nil, errors.New("wbvoice: WidebandCenterHz is required")
	}
	if opts.SDRSampleRateHz == 0 {
		return nil, errors.New("wbvoice: SDRSampleRateHz is required")
	}
	if opts.Serial == "" {
		return nil, errors.New("wbvoice: Serial is required")
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &VirtualTuner{
		serial:     opts.Serial,
		log:        log.With("wbvoice", opts.Serial),
		broker:     opts.Broker,
		widebandHz: opts.WidebandCenterHz,
		inRateHz:   opts.SDRSampleRateHz,
	}, nil
}

// Serial returns the virtual tuner's identity. Matches the serial the
// daemon registered with the voice pool + composer.FindBySerial.
func (v *VirtualTuner) Serial() string { return v.serial }

// SampleRateHz reports the per-tap rate this source emits. The
// composer reads it once per call to size its decimator; for a
// virtual tuner the rate is already the chain's intermediate
// rate (48 kHz), so the composer's decimation collapses to a
// pass-through.
func (v *VirtualTuner) SampleRateHz() uint32 { return NarrowbandRateHz }

// CanTune reports whether hz lies inside the wideband dongle's usable
// IQ window. The trunking pool consults this before calling
// SetCenterFreq so an out-of-window voice grant falls through to a
// physical voice SDR (if configured) without producing a Bind error.
func (v *VirtualTuner) CanTune(hz uint32) bool {
	half := float64(v.inRateHz) * (0.5 - GuardFrac)
	offset := float64(hz) - float64(v.widebandHz)
	return offset >= -half && offset <= half
}

// SetCenterFreq stores the desired tap centre. Returns ErrOutOfBand
// when hz falls outside the wideband dongle's usable IQ window
// (centre_freq_hz ± sample_rate/2 with a 5 % guard at each edge);
// the trunking engine treats that as a non-fatal "wrong tuner"
// signal and tries another free device.
//
// The actual DDC NCO offset isn't applied until the next StreamIQ
// call — see StreamIQ. Per-grant lifecycle: the engine calls
// SetCenterFreq once at bind, then the composer immediately opens
// StreamIQ. SetCenterFreq during an active stream isn't expected
// from the voice pool (voice grants don't re-bind mid-call) and
// would not retune an already-open stream.
func (v *VirtualTuner) SetCenterFreq(hz uint32) error {
	if !v.CanTune(hz) {
		return ErrOutOfBand
	}
	v.tunerMu.Lock()
	v.targetHz = hz
	v.tunerMu.Unlock()
	return nil
}

// StreamIQ subscribes to the wideband broker, builds a single-tap
// DDCBank at the (target − wideband) offset, and returns a channel
// of decimated 48 kHz IQ chunks centred on the previously-set
// target. The returned channel closes on ctx cancellation (the
// composer's per-call chain context).
//
// Each call opens a fresh subscription + DDC: a previous call's
// resampler state and the previous subscriber's drop counter both
// reset. The DDC tap is rebuilt rather than retuned in place
// because the upstream tuner.DDCBank doesn't expose a per-tap
// retune helper; rebuilds at call-start are cheap (the Kaiser FIR
// prototype is a few thousand floats).
func (v *VirtualTuner) StreamIQ(ctx context.Context) (<-chan []complex64, error) {
	v.tunerMu.Lock()
	target := v.targetHz
	v.tunerMu.Unlock()
	if target == 0 {
		return nil, errors.New("wbvoice: StreamIQ called before SetCenterFreq")
	}
	offset := float64(target) - float64(v.widebandHz)

	out := make(chan []complex64, 16)

	bank := tuner.NewDDCBank(float64(v.inRateHz), float64(NarrowbandRateHz), GuardFrac)
	if err := bank.AddTap(offset, func(narrow []complex64) {
		if len(narrow) == 0 {
			return
		}
		cp := make([]complex64, len(narrow))
		copy(cp, narrow)
		// Blocking send so back-pressure flows back to the
		// broker subscription (which itself drops at its
		// bounded buffer when the consumer falls behind). The
		// ctx check lets the goroutine unwind when the call
		// ends.
		select {
		case out <- cp:
		case <-ctx.Done():
		}
	}); err != nil {
		close(out)
		return nil, fmt.Errorf("wbvoice: AddTap offset=%.0f Hz: %w", offset, err)
	}

	sub := v.broker.Subscribe()
	go func() {
		defer close(out)
		defer sub.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-sub.C:
				if !ok {
					return
				}
				bank.Process(chunk)
			}
		}
	}()
	return out, nil
}
