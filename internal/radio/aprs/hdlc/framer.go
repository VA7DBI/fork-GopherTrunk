// Package hdlc implements the High-level Data Link Control framing
// AX.25 (and therefore APRS) wraps its bytes in. HDLC is the
// bit-stream → frame-bytes layer that sits between the symbol
// slicer (which emits raw 0/1 bits) and the AX.25 frame parser
// (which expects a contiguous frame body between two 0x7E flag
// bytes).
//
// What HDLC handles:
//
//  1. Frame delimiter: the byte 0x7E (binary 01111110) marks the
//     start and end of every frame. The pattern is unique because
//     the data section can never contain six consecutive 1 bits —
//     see (2).
//  2. Bit stuffing: in the data section, after every run of five
//     consecutive 1 bits the transmitter inserts a 0. The receiver
//     drops that 0. This guarantees the flag byte's six-1s pattern
//     cannot occur inside the data.
//  3. Bit ordering: AX.25 sends each byte LSB-first on the wire,
//     so this framer packs bits accordingly.
//
// What HDLC does NOT handle:
//
//   - NRZI decoding. AFSK transmitters typically NRZI-encode the
//     wire bits (transition = 0, no transition = 1) before HDLC
//     framing. The DSP layer above this package handles NRZI in
//     the AFSK-demod-to-bits step; by the time bits reach the
//     Framer they're plain {0,1}.
//   - CRC validation. The AX.25 parser checks the trailing 16-bit
//     FCS on the frame body the Framer emits.
//
// The Framer is sync-aware: it slides a shift register looking for
// flag bytes, ignores everything between flags that doesn't make
// structural sense (too short, byte-misaligned), and emits one
// frame body per complete (flag, ...data..., flag) sequence.
package hdlc

const (
	// FlagPattern is the HDLC opening / closing flag (0x7E in
	// LSB-first bit order, which is what an AX.25 transmitter
	// puts on the wire). Six consecutive 1 bits surrounded by 0s.
	FlagPattern uint8 = 0x7E

	// MaxFrameBytes caps how many bytes the Framer will buffer
	// for a single frame before giving up. AX.25 frames are at
	// most 332 bytes (dst+src+8*path+control+pid+256-info+fcs);
	// allow generous headroom for malformed streams.
	MaxFrameBytes = 1024

	// MinFrameBytes mirrors ax25.MinFrameBytes — a frame body
	// shorter than this can't possibly be valid. The Framer
	// discards under-length bodies silently rather than passing
	// them on for parsing.
	MinFrameBytes = 18
)

// Framer consumes a stream of HDLC bits and emits complete frame
// bodies (the bytes between two flag-byte delimiters, after
// bit-stuffing reversal). One Framer per receiver instance —
// keeps the sliding window, ones counter, and partial-byte
// accumulator across Push calls.
type Framer struct {
	// shiftReg holds the most recent 8 bits, LSB-first. Used for
	// flag-byte detection at every bit position so the framer
	// resynchronises automatically after byte-mis-alignment.
	shiftReg uint8

	// inFrame is true between the opening flag and the closing
	// flag. When false, the Framer is just sliding looking for
	// the next 0x7E pattern.
	inFrame bool

	// onesCount counts consecutive 1-bits seen since the last 0
	// or flag. Six 1s in a row are a flag (handled in Push);
	// five 1s followed by a 0 mean the 0 was inserted by the
	// transmitter for bit-stuffing and must be dropped.
	onesCount int

	// byteAcc + bitInByte assemble the next data byte LSB-first
	// from the post-de-stuffing bit stream.
	byteAcc   uint8
	bitInByte uint8

	// body accumulates the in-progress frame body.
	body []byte

	// abort latches when the in-progress frame exceeds
	// MaxFrameBytes — we drop until the next flag and resync.
	abort bool
}

// New constructs an empty Framer in the searching-for-flag state.
func New() *Framer {
	return &Framer{}
}

// Push feeds one bit (0 or 1, LSB-first) and returns a fully-
// formed frame body if one just completed, else nil. Values
// outside {0, 1} are treated as 1 — matches the syncer-side
// convention from the POCSAG implementation.
func (f *Framer) Push(bit byte) []byte {
	if bit > 1 {
		bit = 1
	}
	// Slide the bit into the shift register (LSB-first, so the
	// new bit lands in bit 7 and existing bits shift right by 1).
	// This matches how the wire delivers an LSB-first byte: the
	// first bit of byte N is the LSB of byte N, so once 8 bits
	// have accumulated the shift register holds byte N with its
	// LSB at position 0.
	f.shiftReg = (f.shiftReg >> 1) | (uint8(bit) << 7)

	// Flag detection: the 8-bit window matches FlagPattern.
	// This always means "frame boundary" and resets bit-stuffing
	// state regardless of where we were.
	if f.shiftReg == FlagPattern {
		out := f.closeFrame()
		f.openFrame()
		return out
	}

	if !f.inFrame {
		// Before the opening flag — nothing to accumulate.
		return nil
	}
	if f.abort {
		// Mid-frame abort; wait for the next flag.
		return nil
	}

	// Bit-stuffing reversal: after five 1s, drop the next bit
	// (which the transmitter inserted to break up the run).
	if bit == 0 && f.onesCount == 5 {
		f.onesCount = 0
		return nil
	}

	// Accumulate the bit into the current byte.
	f.byteAcc >>= 1
	if bit != 0 {
		f.byteAcc |= 0x80
		f.onesCount++
	} else {
		f.onesCount = 0
	}
	f.bitInByte++
	if f.bitInByte == 8 {
		f.body = append(f.body, f.byteAcc)
		f.bitInByte = 0
		f.byteAcc = 0
		if len(f.body) > MaxFrameBytes {
			// Too long — abort and resync at next flag.
			f.abort = true
			f.body = f.body[:0]
		}
	}
	// Seven consecutive 1s means abort (HDLC abort sequence:
	// 7+ 1s mid-frame). Sliding-flag detection above already
	// catches 6 (= flag), so this branch only fires on 7.
	if f.onesCount >= 7 {
		f.abort = true
		f.body = f.body[:0]
	}
	return nil
}

// openFrame resets per-frame state to start collecting a new body.
func (f *Framer) openFrame() {
	f.inFrame = true
	f.onesCount = 0
	f.byteAcc = 0
	f.bitInByte = 0
	f.body = f.body[:0]
	f.abort = false
}

// closeFrame consumes the in-progress body — if any — and returns
// it when it's long enough to be parseable. Discards aborted /
// under-length bodies silently. Always resets the in-frame state
// to false.
//
// A non-zero bitInByte at close time is expected: the framer
// accumulated the leading 0..7 bits of the closing flag into a
// "partial byte" before the 8th bit completed the flag pattern
// and triggered the close. Those leading bits are part of the
// flag, not the body, so we discard them silently — only the
// fully-accumulated body bytes are real.
func (f *Framer) closeFrame() []byte {
	if !f.inFrame {
		return nil
	}
	f.inFrame = false
	if f.abort {
		return nil
	}
	// Discard the partial byte; it's just the leading bits of
	// the closing flag (or noise mid-stream).
	f.bitInByte = 0
	f.byteAcc = 0
	if len(f.body) < MinFrameBytes {
		return nil
	}
	// Hand a copy out — the receiver may hold the slice while
	// the framer reuses its buffer.
	out := make([]byte, len(f.body))
	copy(out, f.body)
	return out
}

// Reset clears all state. Tests use this between cases; the
// production receiver shouldn't need it — framers are persistent
// across an SDR's bit stream.
func (f *Framer) Reset() {
	f.shiftReg = 0
	f.inFrame = false
	f.onesCount = 0
	f.byteAcc = 0
	f.bitInByte = 0
	f.body = f.body[:0]
	f.abort = false
}
