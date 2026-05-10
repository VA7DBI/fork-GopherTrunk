package voice

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// nullFrameSize is the frame size we use for NullVocoder-driven
// tests. Anything > 0 works; 11 mirrors the IMBE contract so the
// tests read naturally to a future maintainer who's seen the imbe
// package.
const nullFrameSize = 11

func TestDecodeStreamUnknownVocoder(t *testing.T) {
	var sink memWriteSeeker
	frames, err := DecodeStream(bytes.NewReader(nil), "no-such-vocoder", &sink)
	if err == nil {
		t.Fatal("expected an error for unknown vocoder")
	}
	if frames != 0 {
		t.Errorf("frames = %d, want 0 on registry miss", frames)
	}
}

// TestDecodeStreamEmptyInput: zero input bytes still produce a
// valid (zero-length data chunk) WAV header so callers always get
// a playable file. We use the always-registered "null" vocoder so
// the voice package's tests don't depend on imbe / ambe2 (which
// would create an import cycle).
func TestDecodeStreamEmptyInput(t *testing.T) {
	var sink memWriteSeeker
	frames, err := DecodeStream(bytes.NewReader(nil), "null", &sink)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if frames != 0 {
		t.Errorf("frames = %d, want 0", frames)
	}
	if len(sink.buf) != wavHeaderSize {
		t.Errorf("wrote %d bytes, want exactly the %d-byte header", len(sink.buf), wavHeaderSize)
	}
	if string(sink.buf[0:4]) != "RIFF" {
		t.Errorf("missing RIFF magic")
	}
	dataSize := binary.LittleEndian.Uint32(sink.buf[40:44])
	if dataSize != 0 {
		t.Errorf("data chunk size = %d, want 0 for empty input", dataSize)
	}
}

func TestDecodeStreamMultipleFrames(t *testing.T) {
	const n = 4
	v := NewNullVocoder(nullFrameSize)
	in := bytes.NewReader(make([]byte, n*nullFrameSize))
	var sink memWriteSeeker
	frames, err := DecodeStreamWithVocoder(in, v, &sink)
	if err != nil {
		t.Fatalf("DecodeStreamWithVocoder: %v", err)
	}
	if frames != n {
		t.Errorf("frames = %d, want %d", frames, n)
	}
	// NullVocoder emits 160 zero samples per frame; total payload =
	// n * 160 * 2 bytes.
	wantBytes := wavHeaderSize + n*pcmHzDefault*frameDurationMs/1000*2
	if len(sink.buf) != wantBytes {
		t.Errorf("wrote %d bytes, want %d", len(sink.buf), wantBytes)
	}
	// All samples must be zero (NullVocoder emits silence).
	for i := wavHeaderSize; i+1 < len(sink.buf); i += 2 {
		s := int16(binary.LittleEndian.Uint16(sink.buf[i : i+2]))
		if s != 0 {
			t.Fatalf("sample[%d] = %d, want 0", (i-wavHeaderSize)/2, s)
		}
	}
}

func TestDecodeStreamPartialFrameReturnsError(t *testing.T) {
	v := NewNullVocoder(nullFrameSize)
	// One full frame + 3 trailing bytes (incomplete next frame).
	in := bytes.NewReader(append(make([]byte, nullFrameSize), 0xAA, 0xBB, 0xCC))
	var sink memWriteSeeker
	frames, err := DecodeStreamWithVocoder(in, v, &sink)
	if !errors.Is(err, ErrPartialFrame) {
		t.Errorf("err = %v, want ErrPartialFrame", err)
	}
	if frames != 1 {
		t.Errorf("frames = %d, want 1 (one complete frame written before the partial)", frames)
	}
	// WAV must still be closed (header patched) so the file is
	// playable up to the partial point.
	dataSize := binary.LittleEndian.Uint32(sink.buf[40:44])
	wantData := uint32(pcmHzDefault * frameDurationMs / 1000 * 2)
	if dataSize != wantData {
		t.Errorf("data chunk size = %d, want %d (one frame written before partial)",
			dataSize, wantData)
	}
}

// TestDecodeStreamWavSampleRateIs8kHz: pin the WAV header's
// sample-rate / channel / bits-per-sample fields so future
// plumbing changes don't accidentally produce wrongly-labelled
// WAVs (which most players would re-pitch or refuse).
func TestDecodeStreamWavSampleRateIs8kHz(t *testing.T) {
	var sink memWriteSeeker
	v := NewNullVocoder(nullFrameSize)
	if _, err := DecodeStreamWithVocoder(bytes.NewReader(make([]byte, nullFrameSize)), v, &sink); err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	rate := binary.LittleEndian.Uint32(sink.buf[24:28])
	if rate != pcmHzDefault {
		t.Errorf("sample rate = %d, want %d", rate, pcmHzDefault)
	}
	bps := binary.LittleEndian.Uint16(sink.buf[34:36])
	if bps != wavBitsPerSample {
		t.Errorf("bits per sample = %d, want %d", bps, wavBitsPerSample)
	}
	channels := binary.LittleEndian.Uint16(sink.buf[22:24])
	if channels != wavChannels {
		t.Errorf("channel count = %d, want %d", channels, wavChannels)
	}
}

// TestDecodeStreamRejectsZeroFrameSizeVocoder: a hostile or buggy
// Vocoder that reports FrameSize() == 0 must be rejected up front
// — the read loop would otherwise spin forever on empty reads.
func TestDecodeStreamRejectsZeroFrameSizeVocoder(t *testing.T) {
	v := NewNullVocoder(0)
	var sink memWriteSeeker
	_, err := DecodeStreamWithVocoder(bytes.NewReader(nil), v, &sink)
	if err == nil {
		t.Fatal("expected error for FrameSize=0 vocoder")
	}
}

// TestDecodeStreamPropagatesReadError: an io.Reader returning a
// non-EOF / non-ErrUnexpectedEOF error mid-stream surfaces to the
// caller along with the count of frames written so far.
func TestDecodeStreamPropagatesReadError(t *testing.T) {
	v := NewNullVocoder(nullFrameSize)
	in := &errReader{
		readN:    nullFrameSize, // first read succeeds (one frame)
		errAfter: errors.New("synthetic mid-stream error"),
	}
	var sink memWriteSeeker
	frames, err := DecodeStreamWithVocoder(in, v, &sink)
	if err == nil || err.Error() != "synthetic mid-stream error" {
		t.Errorf("err = %v, want synthetic mid-stream error", err)
	}
	if frames != 1 {
		t.Errorf("frames = %d, want 1 (one frame written before the read error)", frames)
	}
}

// errReader returns readN bytes (zeroed) on its first Read, then
// errAfter on subsequent reads. Used to test mid-stream error
// propagation.
type errReader struct {
	readN    int
	errAfter error
	served   bool
}

func (r *errReader) Read(p []byte) (int, error) {
	if !r.served {
		r.served = true
		n := r.readN
		if n > len(p) {
			n = len(p)
		}
		// p is already zero from the caller's make([]byte, …) but
		// be explicit for clarity.
		for i := 0; i < n; i++ {
			p[i] = 0
		}
		return n, nil
	}
	return 0, r.errAfter
}

// silence the "unused import" warning when test files are pruned.
var _ = io.Discard
