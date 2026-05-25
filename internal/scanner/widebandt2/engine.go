// Package widebandt2 monitors several DMR Tier II conventional
// repeaters with a single SDR dongle. The dongle is pinned to a
// configured centre frequency; an internal/dsp/tuner.Bank extracts
// one narrow-band IQ stream per repeater carrier inside the dongle's
// IQ band; each stream feeds a DMR receiver and a tier2 state
// machine that publishes cc.locked / grant events on the bus.
//
// One Engine owns one wideband SDR. Multiple wideband dongles each
// get their own Engine; they run independently and share only the
// events.Bus and the metrics surface.
package widebandt2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/dsp/tuner"
	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/radio/dmr"
	"github.com/MattCheramie/GopherTrunk/internal/radio/dmr/receiver"
	"github.com/MattCheramie/GopherTrunk/internal/radio/dmr/tier2"
	"github.com/MattCheramie/GopherTrunk/internal/sdr"
)

// narrowbandRateHz is the per-tap sample rate the bank decimates to.
// 48 kHz matches the rate the DMR receiver's matched filter +
// Mueller-Müller clock loop are sized for (≈10 samples/symbol at
// 4800 baud), the same rate the existing single-channel ccdecoder
// down-converter targets.
const narrowbandRateHz = 48_000.0

// guardFrac is the fraction of the IQ band the tuner reserves at
// each edge as a guard against alias roll-off. Mirrors the value
// the config validator uses to reject out-of-band channels.
const guardFrac = 0.05

// strategyAutoThreshold is the channel-count cutoff for the "auto"
// tuner strategy: at or below it, DDCBank wins on simplicity;
// above it, ChannelizerBank's shared wide-band filter pays off.
const strategyAutoThreshold = 6

// channelizerBins is the bin count NewChannelizerBank uses when
// strategy=="polyphase" or auto picks it. Power of two, large
// enough that 12.5 kHz DMR repeater spacing maps to distinct bins
// inside a 2.4 MS/s IQ band: 2_400_000 / 16 = 150 kHz per bin.
const channelizerBins = 16

// channelizerTapsPerBranch and channelizerKaiserBeta match the
// channelizer package's own test defaults — wideband-clean
// passband, ~70 dB peak sidelobe.
const channelizerTapsPerBranch = 16
const channelizerKaiserBeta = 9.0

// ChannelConfig binds one repeater frequency to the trunking system
// it belongs to. The Engine creates one tier2.ConventionalChannel
// per entry.
type ChannelConfig struct {
	FrequencyHz uint32
	SystemName  string
}

// Options bundles the inputs Engine needs at construction time.
type Options struct {
	Log *slog.Logger
	Bus *events.Bus

	// Device is the wideband SDR. It is set to CenterFreqHz, then
	// streamed continuously until Run's context cancels.
	Device sdr.Device
	// SampleRateHz is the dongle's IQ sample rate.
	SampleRateHz uint32
	// CenterFreqHz is the wide-band centre frequency.
	CenterFreqHz uint32

	// TunerStrategy picks the Bank implementation. "" / "auto"
	// auto-selects by channel count; "ddc" forces DDCBank;
	// "polyphase" forces ChannelizerBank.
	TunerStrategy string

	// Channels is the list of per-repeater carriers to monitor.
	// All FrequencyHz values must fall inside the dongle's IQ
	// band (CenterFreqHz ± SampleRateHz/2 minus guardFrac).
	Channels []ChannelConfig

	// Now overrides the wall clock the per-channel state machines
	// use for Grant.At timestamps. nil ⇒ time.Now.
	Now func() time.Time
}

// Engine owns the per-dongle IQ pump goroutine and the per-channel
// receiver + state-machine fan-out.
type Engine struct {
	log         *slog.Logger
	bus         *events.Bus
	device      sdr.Device
	centerHz    uint32
	sampleRate  uint32
	bank        tuner.Bank
	channels    []*engineChannel
	strategyTag string
}

type engineChannel struct {
	freqHz   uint32
	sysName  string
	cc       *tier2.ConventionalChannel
	receiver *receiver.Receiver
}

// New constructs an Engine. The device is not opened or streamed
// here; that happens in Run. Returns an error if construction
// inputs are invalid (offset out of band, colliding channelizer
// bins, etc.).
func New(opts Options) (*Engine, error) {
	if opts.Device == nil {
		return nil, errors.New("widebandt2: Device is required")
	}
	if opts.Bus == nil {
		return nil, errors.New("widebandt2: Bus is required")
	}
	if opts.SampleRateHz == 0 {
		return nil, errors.New("widebandt2: SampleRateHz is required")
	}
	if opts.CenterFreqHz == 0 {
		return nil, errors.New("widebandt2: CenterFreqHz is required")
	}
	if len(opts.Channels) == 0 {
		return nil, errors.New("widebandt2: at least one channel is required")
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	strategy, tag := pickStrategy(opts.TunerStrategy, len(opts.Channels))
	var bank tuner.Bank
	switch strategy {
	case "ddc":
		bank = tuner.NewDDCBank(float64(opts.SampleRateHz), narrowbandRateHz, guardFrac)
	case "polyphase":
		bank = tuner.NewChannelizerBank(float64(opts.SampleRateHz), narrowbandRateHz, guardFrac,
			channelizerBins, channelizerTapsPerBranch, channelizerKaiserBeta)
	default:
		return nil, fmt.Errorf("widebandt2: unknown tuner strategy %q", strategy)
	}

	engine := &Engine{
		log:         log,
		bus:         opts.Bus,
		device:      opts.Device,
		centerHz:    opts.CenterFreqHz,
		sampleRate:  opts.SampleRateHz,
		bank:        bank,
		strategyTag: tag,
	}

	for _, ch := range opts.Channels {
		offset := float64(ch.FrequencyHz) - float64(opts.CenterFreqHz)
		cc := tier2.New(tier2.Options{
			Bus:         opts.Bus,
			Log:         log.With("system", ch.SystemName, "freq_hz", ch.FrequencyHz),
			SystemName:  ch.SystemName,
			FrequencyHz: ch.FrequencyHz,
			Now:         opts.Now,
		})
		// dibitSink hands the receiver's dibits to the Tier II
		// process adapter. The adapter buffers across calls,
		// runs sync detection, and emits cc.locked / grant
		// events on the bus.
		dibitSink := func(cc *tier2.ConventionalChannel) dmr.DibitSink {
			return func(dibits []uint8, baseIdx int) {
				cc.Process(dibits, baseIdx)
			}
		}(cc)
		rcv := receiver.New(receiver.Options{
			SampleRateHz: narrowbandRateHz,
			DibitSink:    dibitSink,
			DeviationHz:  receiver.SymbolRate * 0.405, // ~1944 Hz per ETSI TS 102 361-1 §6.3
		})
		ec := &engineChannel{
			freqHz:   ch.FrequencyHz,
			sysName:  ch.SystemName,
			cc:       cc,
			receiver: rcv,
		}
		sink := func(ec *engineChannel) tuner.SinkFunc {
			return func(out []complex64) {
				if len(out) == 0 {
					return
				}
				ec.receiver.Process(out)
			}
		}(ec)
		if err := bank.AddTap(offset, sink); err != nil {
			return nil, fmt.Errorf("widebandt2: AddTap freq=%d offset=%.0f Hz: %w",
				ch.FrequencyHz, offset, err)
		}
		engine.channels = append(engine.channels, ec)
	}
	return engine, nil
}

// Channels returns the per-channel frequencies the engine is
// monitoring. Used by callers (the daemon, tests) for logging and
// status snapshots.
func (e *Engine) Channels() []uint32 {
	out := make([]uint32, 0, len(e.channels))
	for _, c := range e.channels {
		out = append(out, c.freqHz)
	}
	return out
}

// Strategy reports which tuner strategy the engine resolved at
// construction ("ddc" or "polyphase"). Used for diagnostics /
// startup logging.
func (e *Engine) Strategy() string { return e.strategyTag }

// Run programs the dongle's centre frequency, opens its IQ stream,
// and pumps chunks through the tuner bank until ctx cancels. The
// per-tap fan-out and per-channel receivers / state machines run
// inline on this goroutine — they are not concurrent with each
// other, so chunk ordering is preserved.
func (e *Engine) Run(ctx context.Context) error {
	if err := e.device.SetCenterFreq(e.centerHz); err != nil {
		return fmt.Errorf("widebandt2: SetCenterFreq %d Hz: %w", e.centerHz, err)
	}
	e.log.Info("widebandt2: starting",
		"center_freq_hz", e.centerHz,
		"sample_rate_hz", e.sampleRate,
		"channels", len(e.channels),
		"strategy", e.strategyTag,
	)
	stream, err := e.device.StreamIQ(ctx)
	if err != nil {
		return fmt.Errorf("widebandt2: StreamIQ: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case chunk, ok := <-stream:
			if !ok {
				return nil
			}
			e.bank.Process(chunk)
		}
	}
}

// pickStrategy resolves the user-facing strategy choice into the
// internal bank kind. "" / "auto" auto-selects: small channel counts
// favour DDCBank (linear, no alignment constraint); larger counts
// favour the shared polyphase channelizer.
func pickStrategy(requested string, channelCount int) (kind, tag string) {
	switch requested {
	case "ddc":
		return "ddc", "ddc"
	case "polyphase":
		return "polyphase", "polyphase"
	case "", "auto":
		if channelCount <= strategyAutoThreshold {
			return "ddc", "auto(ddc)"
		}
		return "polyphase", "auto(polyphase)"
	default:
		return requested, requested
	}
}
