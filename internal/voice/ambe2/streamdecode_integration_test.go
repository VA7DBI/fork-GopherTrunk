package ambe2

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/voice"
)

// memWriteSeeker mirrors the shape used by voice's wav_test.go so
// ambe2-package tests can exercise voice.DecodeStream without
// touching the filesystem.
type memWriteSeeker struct {
	buf []byte
	pos int64
}

func (m *memWriteSeeker) Write(p []byte) (int, error) {
	end := m.pos + int64(len(p))
	if end > int64(len(m.buf)) {
		grown := make([]byte, end)
		copy(grown, m.buf)
		m.buf = grown
	}
	copy(m.buf[m.pos:], p)
	m.pos = end
	return len(p), nil
}

func (m *memWriteSeeker) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		m.pos = off
	case io.SeekCurrent:
		m.pos += off
	case io.SeekEnd:
		m.pos = int64(len(m.buf)) + off
	}
	return m.pos, nil
}

const wavHeaderSize = 44

// TestDecodeStreamThroughAmbe2: voice.DecodeStream with name
// "ambe2" pulls the pure-Go AMBE+2 decoder out of the registry
// and produces a playable WAV from a stream of synthetic AMBE+2
// frames. Pins the out-of-band ".raw → .wav" pipeline operators
// use to decode captured P25 P2 / DMR / NXDN frames after a
// session.
func TestDecodeStreamThroughAmbe2(t *testing.T) {
	const n = 4
	frame := make([]byte, FrameBytes) // all zero ⇒ b₀=0 voice
	in := bytes.NewReader(bytes.Repeat(frame, n))
	var sink memWriteSeeker
	frames, err := voice.DecodeStream(in, VocoderName, &sink)
	if err != nil {
		t.Fatalf("voice.DecodeStream: %v", err)
	}
	if frames != n {
		t.Errorf("frames = %d, want %d", frames, n)
	}
	wantBytes := wavHeaderSize + n*160*2
	if len(sink.buf) != wantBytes {
		t.Errorf("wrote %d bytes, want %d", len(sink.buf), wantBytes)
	}
	// AMBE+2 voice synthesis produces non-zero PCM through the
	// shared mbe pipeline; confirm at least some samples are
	// non-zero.
	var nonZero int
	for i := wavHeaderSize; i+1 < len(sink.buf); i += 2 {
		if int16(binary.LittleEndian.Uint16(sink.buf[i:i+2])) != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Error("all samples zero; AMBE+2 synthesis didn't run through DecodeStream")
	}
}

// TestDecodeStreamToneFrameThroughAmbe2: a valid single-tone
// AMBE+2 frame (b₁ ∈ [5, 122]) routed through DecodeStream
// produces non-zero PCM (the synthSingleTone path). Pins that
// out-of-band decode honours all the AMBE+2 frame paths, not just
// voice.
func TestDecodeStreamToneFrameThroughAmbe2(t *testing.T) {
	in := bytes.NewReader(singleToneFrame(12, 200))
	var sink memWriteSeeker
	frames, err := voice.DecodeStream(in, VocoderName, &sink)
	if err != nil {
		t.Fatalf("voice.DecodeStream: %v", err)
	}
	if frames != 1 {
		t.Errorf("frames = %d, want 1", frames)
	}
	var nonZero int
	for i := wavHeaderSize; i+1 < len(sink.buf); i += 2 {
		if int16(binary.LittleEndian.Uint16(sink.buf[i:i+2])) != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Error("tone frame produced no audio through DecodeStream")
	}
}
