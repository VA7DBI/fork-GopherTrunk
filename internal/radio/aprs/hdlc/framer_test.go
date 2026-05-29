package hdlc

import (
	"bytes"
	"testing"
)

// encodeFrame builds the HDLC bit stream that wraps the supplied
// payload with opening + closing flag bytes and applies the
// "insert a 0 after every 5 consecutive 1s" bit-stuffing rule.
// Result is one byte per bit, LSB-first.
func encodeFrame(payload []byte) []byte {
	var out []byte
	out = appendByte(out, FlagPattern, false)
	out = appendBytesStuffed(out, payload)
	out = appendByte(out, FlagPattern, false)
	return out
}

// appendByte emits the 8 bits of b LSB-first onto out. When
// stuff is true (payload bytes), a 0 is inserted after every 5
// consecutive 1s; when false (flag bytes), the byte goes through
// raw.
func appendByte(out []byte, b uint8, stuff bool) []byte {
	if !stuff {
		for i := 0; i < 8; i++ {
			out = append(out, (b>>i)&1)
		}
		return out
	}
	// Stuffed path lives in appendBytesStuffed below; we
	// don't actually need a stuff-mode here because the only
	// stuff-mode caller (appendBytesStuffed) tracks the
	// inter-byte ones counter itself.
	for i := 0; i < 8; i++ {
		out = append(out, (b>>i)&1)
	}
	return out
}

// appendBytesStuffed walks the payload bit-by-bit LSB-first
// across byte boundaries, tracking the global ones counter so a
// 1-run that straddles two bytes still triggers a stuff.
func appendBytesStuffed(out []byte, payload []byte) []byte {
	ones := 0
	for _, b := range payload {
		for i := 0; i < 8; i++ {
			bit := (b >> i) & 1
			out = append(out, bit)
			if bit != 0 {
				ones++
				if ones == 5 {
					// Stuff a 0 to break the run.
					out = append(out, 0)
					ones = 0
				}
			} else {
				ones = 0
			}
		}
	}
	return out
}

// pushBits feeds the bit stream through a fresh Framer and
// returns every emitted frame body.
func pushBits(t *testing.T, bits []byte) [][]byte {
	t.Helper()
	f := New()
	var out [][]byte
	for _, b := range bits {
		if body := f.Push(b); body != nil {
			out = append(out, body)
		}
	}
	return out
}

func TestFramerRoundTripsSimpleBody(t *testing.T) {
	// 18-byte minimal valid frame body (the MinFrameBytes
	// threshold) — a real AX.25 body would have more structure
	// but the framer doesn't care; it just delivers complete
	// flag-delimited bodies.
	payload := bytes.Repeat([]byte{0x55}, MinFrameBytes)
	bits := encodeFrame(payload)
	frames := pushBits(t, bits)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if !bytes.Equal(frames[0], payload) {
		t.Errorf("frame body = %x, want %x", frames[0], payload)
	}
}

func TestFramerBitDestuffsCorrectly(t *testing.T) {
	// Payload with a run of 5 consecutive 1s in the LSB-first
	// bit ordering: byte 0x1F = 00011111 → LSB-first = 11111000
	// → 5 consecutive 1s at the start, so the encoder will
	// insert a 0 after them. The framer must drop that 0 so the
	// decoded byte still reads 0x1F.
	payload := append(bytes.Repeat([]byte{0x00}, 8), 0x1F)
	payload = append(payload, bytes.Repeat([]byte{0x00}, 9)...) // pad to MinFrameBytes
	bits := encodeFrame(payload)
	frames := pushBits(t, bits)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if !bytes.Equal(frames[0], payload) {
		t.Errorf("frame body = %x, want %x", frames[0], payload)
	}
}

func TestFramerRejectsTooShort(t *testing.T) {
	// A 17-byte body — one less than MinFrameBytes — must be
	// dropped silently.
	payload := bytes.Repeat([]byte{0xAA}, MinFrameBytes-1)
	bits := encodeFrame(payload)
	frames := pushBits(t, bits)
	if len(frames) != 0 {
		t.Errorf("got %d frames for short body, want 0", len(frames))
	}
}

func TestFramerHandlesMultipleFrames(t *testing.T) {
	a := bytes.Repeat([]byte{0xAA}, MinFrameBytes)
	b := bytes.Repeat([]byte{0xBB}, MinFrameBytes+1)
	bits := append(encodeFrame(a), encodeFrame(b)...)
	frames := pushBits(t, bits)
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
	if !bytes.Equal(frames[0], a) || !bytes.Equal(frames[1], b) {
		t.Errorf("frame contents mismatch")
	}
}

func TestFramerResyncsAcrossNoise(t *testing.T) {
	// Garbage bits before the first flag → framer should ignore
	// them and only emit the framed payload.
	garbage := []byte{0, 1, 1, 0, 1, 0, 1, 1, 0, 0, 1, 0, 1, 0}
	payload := bytes.Repeat([]byte{0xC3}, MinFrameBytes)
	bits := append(garbage, encodeFrame(payload)...)
	frames := pushBits(t, bits)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1 (ignore pre-flag noise)", len(frames))
	}
	if !bytes.Equal(frames[0], payload) {
		t.Errorf("frame body mismatch")
	}
}

func TestFramerSharedFlagBetweenAdjacentFrames(t *testing.T) {
	// HDLC permits "shared flag" packing where the closing
	// flag of frame A doubles as the opening flag of frame B.
	// The framer should still emit both bodies.
	a := bytes.Repeat([]byte{0x42}, MinFrameBytes)
	b := bytes.Repeat([]byte{0x99}, MinFrameBytes)
	bits := []byte{}
	bits = appendByte(bits, FlagPattern, false)
	bits = appendBytesStuffed(bits, a)
	bits = appendByte(bits, FlagPattern, false) // closes A, opens B
	bits = appendBytesStuffed(bits, b)
	bits = appendByte(bits, FlagPattern, false)
	frames := pushBits(t, bits)
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2 (shared flag)", len(frames))
	}
}

func TestFramerAbortsOnSevenOnes(t *testing.T) {
	// 7 consecutive 1s mid-frame is the HDLC abort sequence.
	// Construct a bit stream: opening flag, a few normal bits,
	// then 7 ones, then bits, then closing flag. The framer
	// should drop the body and emit nothing.
	var bits []byte
	bits = appendByte(bits, FlagPattern, false)
	// Inject 4 zeros + 7 ones — 7 1s with no preceding stuffer.
	bits = append(bits, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1)
	// Pad to MinFrameBytes worth of bits, then close.
	bits = append(bits, make([]byte, MinFrameBytes*8)...)
	bits = appendByte(bits, FlagPattern, false)
	frames := pushBits(t, bits)
	if len(frames) != 0 {
		t.Errorf("got %d frames after abort sequence, want 0", len(frames))
	}
}

func TestFramerResetClearsState(t *testing.T) {
	f := New()
	// Push the opening flag + partial body.
	for i := 0; i < 8; i++ {
		f.Push((FlagPattern >> i) & 1)
	}
	for i := 0; i < 20; i++ {
		f.Push(1)
	}
	f.Reset()
	if f.inFrame || f.bitInByte != 0 || len(f.body) != 0 {
		t.Errorf("Reset did not fully clear state: %+v", f)
	}
}
