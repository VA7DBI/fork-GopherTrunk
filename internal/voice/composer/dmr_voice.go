package composer

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/dsp/filter"
	dmrrx "github.com/MattCheramie/GopherTrunk/internal/radio/dmr/receiver"
	dmrvoice "github.com/MattCheramie/GopherTrunk/internal/radio/dmr/voice"
)

// dmrVoiceIntermediateHz is the rate the wideband IQ is decimated to
// before the DMR receiver runs. 48 kHz gives the 4800-baud DMR symbol
// stream 10 samples per symbol — ample for the receiver's RRC matched
// filter and Mueller-Müller clock recovery, without the cost of
// running them at the SDR's native multi-MS/s rate.
const dmrVoiceIntermediateHz = 48_000

// rawFrameSink is the subset of voice.Recorder the DMR voice chain
// needs. The composer holds its sink as a PCMSink; runDMRVoiceChain
// type-asserts to this, so a sink that only implements WritePCM
// (analog-only callers, test stubs) still works for the FM path.
type rawFrameSink interface {
	WriteRawFrame(deviceSerial string, frame []byte) error
}

// runDMRVoiceChain consumes IQ for one DMR voice call. It decimates
// the wideband IQ to a DMR-symbol-friendly rate, recovers the dibit
// stream with the shared DMR receiver, assembles A–F voice
// superframes, FEC-decodes each superframe's 18 AMBE+2 frames to
// their 49-bit vocoder payload, and appends them (packed into 7
// bytes) to the recorder's .raw sidecar.
//
// AMBE forward-error-correction is applied per frame
// (dmrvoice.DecodeAMBEFrame): the 72-bit on-air frame is FEC-decoded
// to its 49-bit vocoder payload before being written. Vocoder decode
// to PCM is still out of scope — the .raw sidecar carries the
// post-FEC frames for out-of-band decode (issue #276).
func (c *Composer) runDMRVoiceChain(ctx context.Context, serial string, iqCh <-chan []complex64, iqHz uint32, done chan<- struct{}) {
	defer close(done)

	decim := int(iqHz) / dmrVoiceIntermediateHz
	if decim < 1 {
		decim = 1
	}
	symbolHz := float64(iqHz) / float64(decim)

	// Front-end LPF: doubles as the anti-aliasing filter for the
	// decimation, so it is only needed when the IQ is actually
	// decimated (the live multi-MS/s path; decim == 1 only in tests
	// that feed IQ already at the intermediate rate).
	cutoff := float64(c.bw) / float64(iqHz)
	if cutoff > 0.45 {
		cutoff = 0.45
	}
	lpf := filter.NewFIR(filter.LowpassKaiser(81, cutoff, 8.6))

	rs, _ := c.sink.(rawFrameSink)
	voiceDec := dmrvoice.NewDecoder()
	// superframes counts DMR voice superframes the receiver delivered —
	// i.e. real voice activity. The touch ticker (below) only refreshes
	// the engine's LastHeardAt when this counter has advanced since the
	// previous tick. Without this gate a stalled decoder still kept the
	// call alive forever via an unconditional 1 s heartbeat (issue #356).
	var superframes atomic.Uint64
	rx := dmrrx.New(dmrrx.Options{
		SampleRateHz: symbolHz,
		// DMR spec peak deviation per ETSI TS 102 361-1 §6.3 — matches
		// the control-channel receiver in internal/scanner/ccdecoder.
		DeviationHz: 1944.0,
		ClockGain:   0.025,
		DibitSink: func(dibits []uint8, baseIdx int) {
			for _, sf := range voiceDec.Process(dibits, baseIdx) {
				superframes.Add(1)
				if rs == nil {
					continue
				}
				for i := range sf.Frames {
					info, _, err := dmrvoice.DecodeAMBEFrame(sf.Frames[i])
					if err != nil {
						c.log.Warn("composer: DMR AMBE FEC decode failed",
							"serial", serial, "err", err)
						continue
					}
					if err := rs.WriteRawFrame(serial, packBits(info)); err != nil {
						c.log.Warn("composer: DMR raw-frame write failed",
							"serial", serial, "err", err)
					}
				}
			}
		},
	})

	touchTicker := time.NewTicker(c.touchEvery)
	defer touchTicker.Stop()
	var lastSuperframes uint64

	for {
		select {
		case <-ctx.Done():
			return
		case <-touchTicker.C:
			n := superframes.Load()
			if n != lastSuperframes && c.engine != nil {
				c.engine.Touch(serial)
				lastSuperframes = n
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

// packBits packs a bit slice (one bit per byte, MSB-first) into bytes
// — 49 FEC-decoded AMBE payload bits become a 7-byte .raw frame.
func packBits(bits []byte) []byte {
	out := make([]byte, (len(bits)+7)/8)
	for i := range bits {
		if bits[i]&1 != 0 {
			out[i>>3] |= 1 << uint(7-(i&7))
		}
	}
	return out
}
