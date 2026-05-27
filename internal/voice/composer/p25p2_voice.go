package composer

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/dsp/filter"
	"github.com/MattCheramie/GopherTrunk/internal/events"
	p25p2 "github.com/MattCheramie/GopherTrunk/internal/radio/p25/phase2"
	p25p2rx "github.com/MattCheramie/GopherTrunk/internal/radio/p25/phase2/receiver"
	"github.com/MattCheramie/GopherTrunk/internal/trunking"
)

// p25p2VoiceIntermediateHz is the rate the wideband IQ is decimated to
// before the P25 Phase 2 receiver runs. 48 kHz gives the 6000-baud
// H-DQPSK symbol stream 8 samples per symbol — ample for the matched
// filter and Gardner clock recovery without running them at the SDR's
// native multi-MS/s rate.
const p25p2VoiceIntermediateHz = 48_000

// p25p2VoiceGardnerGain matches the value newP25Phase2Pipeline settled
// on for the H-DQPSK symbol clock (smaller than the receiver default
// because H-DQPSK slips differently than C4FM).
const p25p2VoiceGardnerGain = 0.005

// runP25Phase2VoiceChain consumes IQ for one P25 Phase 2 voice call. It
// decimates the wideband IQ to an H-DQPSK-friendly rate, recovers the
// dibit stream with the Phase 2 receiver, assembles 360 ms superframes,
// and for every voice-bearing sub-frame FEC-decodes its AMBE+2 frames
// and appends them to the recorder's .raw sidecar.
//
// In parallel it dispatches the MAC-typed sub-frames that ride the same
// superframe (Phase 2 voice traffic channels interleave MAC PDUs —
// talker alias, encryption sync, in-call signalling — with voice).
// macCfg pins the FEC pipeline DecodeSuperframeMACPDUs runs on those
// sub-frames; on a completed talker-alias the chain publishes a
// trunking.TalkerAlias event itself, mirroring the CC's
// publishTalkerAlias path. This is the only way display names surface
// on Phase 2 systems whose CCs never emit alias fragments (e.g. MMR).
//
// The recorder maps protocol "p25-phase2" to the pure-Go AMBE+2
// vocoder (voice.DefaultVocoderForProtocol), so WriteRawFrame here
// decodes each 7-byte frame to PCM and into the call's WAV — unlike the
// DMR chain, whose pre-FEC frames the vocoder cannot consume.
func (c *Composer) runP25Phase2VoiceChain(ctx context.Context, serial string, system string, macCfg p25p2.MACDecodeConfig, iqCh <-chan []complex64, done chan<- struct{}) {
	defer close(done)

	decim := int(c.iqHz) / p25p2VoiceIntermediateHz
	if decim < 1 {
		decim = 1
	}
	symbolHz := float64(c.iqHz) / float64(decim)

	// Front-end LPF: doubles as the anti-aliasing filter for the
	// decimation, so it is only needed when the IQ is actually
	// decimated (decim == 1 only in tests that feed IQ already at the
	// intermediate rate).
	cutoff := float64(c.bw) / float64(c.iqHz)
	if cutoff > 0.45 {
		cutoff = 0.45
	}
	lpf := filter.NewFIR(filter.LowpassKaiser(81, cutoff, 8.6))

	rs, _ := c.sink.(rawFrameSink)
	sfDec := p25p2.NewSuperframeDecoder()
	aliasAsm := p25p2.NewTalkerAliasAssembler(nil)
	// voiceSubframes counts P25 Phase 2 voice-bearing subframes the
	// receiver delivered — i.e. real voice activity. The touch ticker
	// (below) only refreshes the engine's LastHeardAt when this counter
	// has advanced since the previous tick. Without this gate a stalled
	// decoder still kept the call alive forever via an unconditional
	// 1 s heartbeat (issue #356).
	var voiceSubframes atomic.Uint64
	rx := p25p2rx.New(p25p2rx.Options{
		SampleRateHz: symbolHz,
		ClockMode:    p25p2rx.ClockGardner,
		GardnerGain:  p25p2VoiceGardnerGain,
		DibitSink: func(dibits []uint8, baseIdx int) {
			for _, sf := range sfDec.Process(dibits, baseIdx) {
				for _, sub := range sf.Subframes {
					if !sub.SlotType.IsVoice() {
						continue
					}
					voiceSubframes.Add(1)
					if rs == nil {
						continue
					}
					frames, _, err := p25p2.ExtractVoiceFrames(sub)
					if err != nil {
						c.log.Warn("composer: p25p2 voice extract failed",
							"serial", serial, "err", err)
					}
					for _, f := range frames {
						if f == nil {
							continue
						}
						if werr := rs.WriteRawFrame(serial, f); werr != nil {
							c.log.Warn("composer: p25p2 raw-frame write failed",
								"serial", serial, "err", werr)
						}
					}
				}
				for _, pdu := range p25p2.DecodeSuperframeMACPDUs(sf, macCfg) {
					f, ok := pdu.AsTalkerAliasFragment()
					if !ok {
						continue
					}
					alias, src, complete := aliasAsm.Add(f)
					if !complete {
						continue
					}
					c.publishP25Phase2TalkerAlias(system, src, alias)
				}
			}
		},
	})

	touchTicker := time.NewTicker(c.touchEvery)
	defer touchTicker.Stop()
	var lastSubframes uint64

	for {
		select {
		case <-ctx.Done():
			return
		case <-touchTicker.C:
			n := voiceSubframes.Load()
			if n != lastSubframes && c.engine != nil {
				c.engine.Touch(serial)
				lastSubframes = n
			}
		case iq, ok := <-iqCh:
			if !ok {
				return
			}
			samples := iq
			if decim > 1 {
				samples = decimateComplex(lpf.Process(nil, iq), decim)
			}
			rx.Process(samples)
		}
	}
}

// publishP25Phase2TalkerAlias mirrors phase2.ControlChannel.publishTalkerAlias
// for the voice-channel MAC dispatch path: a completed alias the
// composer reassembled off the traffic channel surfaces on the bus
// with the same payload shape as one decoded on the CC.
func (c *Composer) publishP25Phase2TalkerAlias(system string, sourceID uint32, alias string) {
	if c.bus == nil {
		return
	}
	c.bus.Publish(events.Event{
		Kind: events.KindTalkerAlias,
		Payload: trunking.TalkerAlias{
			System:   system,
			Protocol: "p25-phase2",
			SourceID: sourceID,
			Alias:    alias,
			At:       time.Now().UTC(),
		},
	})
	c.log.Info("composer: p25p2 talker alias",
		"system", system, "src", sourceID, "alias", alias)
}
