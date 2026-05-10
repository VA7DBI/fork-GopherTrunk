package voice

import (
	"errors"
	"fmt"
	"io"
)

// ErrPartialFrame is returned by DecodeStream when the input ends
// in the middle of a vocoder frame — the trailing bytes don't make
// up a complete frame for the chosen vocoder. Callers can inspect
// the byte count returned alongside the error to decide whether
// the partial trailer is recoverable (typically: it isn't).
var ErrPartialFrame = errors.New("voice: input ended mid-frame")

// DecodeStream reads vocoder frames from in, decodes each via the
// named vocoder from DefaultRegistry, and writes 8 kHz / 16-bit /
// mono PCM as a WAV stream to out. Returns the number of frames
// decoded successfully.
//
// out must be an io.WriteSeeker so the WAV header length fields
// can be patched on close (file handles satisfy this; in-memory
// callers can wrap a bytes.Buffer with a seeker shim).
//
// Frame size is determined by the chosen vocoder via FrameSize().
// Input must be an exact multiple of that frame size; trailing
// bytes are reported via ErrPartialFrame after the leading
// complete frames have been written.
//
// On a per-frame Decode error, DecodeStream stops and returns the
// number of frames decoded so far + the error. The WAV is closed
// (length fields patched) before returning so callers get a
// playable file even on partial decode.
func DecodeStream(in io.Reader, vocoderName string, out io.WriteSeeker) (int, error) {
	v, err := DefaultRegistry.New(vocoderName)
	if err != nil {
		return 0, err
	}
	defer v.Close()
	return DecodeStreamWithVocoder(in, v, out)
}

// DecodeStreamWithVocoder is the lower-level entry point: callers
// supply a constructed Vocoder (so they can pin reproducibility
// via NewWithSeed or tune AGC via NewWithConfig before handing it
// off). Behaviour matches DecodeStream — see that function's doc
// for the contract.
//
// The Vocoder is not Reset before use; the caller controls
// initial state. The caller is responsible for closing v.
func DecodeStreamWithVocoder(in io.Reader, v Vocoder, out io.WriteSeeker) (int, error) {
	frameSize := v.FrameSize()
	if frameSize <= 0 {
		return 0, fmt.Errorf("voice: vocoder %q reports invalid FrameSize=%d", v.Name(), frameSize)
	}

	wav, err := NewWavWriter(out, pcmHzDefault)
	if err != nil {
		return 0, err
	}

	frame := make([]byte, frameSize)
	frames := 0
	for {
		// io.ReadFull returns io.EOF if zero bytes were read,
		// io.ErrUnexpectedEOF on a partial read.
		_, err := io.ReadFull(in, frame)
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			_ = wav.Close()
			return frames, ErrPartialFrame
		}
		if err != nil {
			_ = wav.Close()
			return frames, err
		}

		samples, err := v.Decode(frame)
		if err != nil {
			_ = wav.Close()
			return frames, fmt.Errorf("voice: decode frame %d: %w", frames, err)
		}
		if err := wav.WriteSamples(samples); err != nil {
			_ = wav.Close()
			return frames, fmt.Errorf("voice: write frame %d: %w", frames, err)
		}
		frames++
	}

	if err := wav.Close(); err != nil {
		return frames, err
	}
	return frames, nil
}
