package ais

import (
	"encoding/hex"
	"strings"
)

// AIS-encoded text fields use "six-bit ASCII": each character is
// packed into 6 bits, with the alphabet defined by ITU-R M.1371-5
// Table 47 (the AIS 6-bit ASCII character set). This is a fixed
// 64-entry table — capital letters, digits, and a small handful
// of punctuation. Unused / reserved code points map to '?'.
//
// Layout: bits 0..5 of the encoded value map to one character; six
// successive 6-bit values form a 36-bit ASCII string. AIS uses
// MSB-first bit ordering throughout (see unpackBits).
var sixBitASCIITable = [64]byte{
	'@', 'A', 'B', 'C', 'D', 'E', 'F', 'G',
	'H', 'I', 'J', 'K', 'L', 'M', 'N', 'O',
	'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W',
	'X', 'Y', 'Z', '[', '\\', ']', '^', '_',
	' ', '!', '"', '#', '$', '%', '&', '\'',
	'(', ')', '*', '+', ',', '-', '.', '/',
	'0', '1', '2', '3', '4', '5', '6', '7',
	'8', '9', ':', ';', '<', '=', '>', '?',
}

// readAISString reads a multi-character AIS 6-bit ASCII string
// starting at bit offset off and running for nChars characters.
// Trailing "@" padding (the spec's empty-slot marker, code 0) and
// trailing spaces are stripped to match the on-screen rendering
// real AIS UIs use.
func readAISString(bits []byte, off, nChars int) string {
	if off+nChars*6 > len(bits) {
		// Truncated frame — read whatever's left, pad with @.
		nChars = (len(bits) - off) / 6
		if nChars <= 0 {
			return ""
		}
	}
	var b strings.Builder
	b.Grow(nChars)
	for i := 0; i < nChars; i++ {
		code := readBitsUint(bits, off+i*6, 6)
		b.WriteByte(sixBitASCIITable[code&0x3F])
	}
	return strings.TrimRight(b.String(), " @")
}

// readBitsUint reads an n-bit unsigned integer from bits[off:off+n]
// in MSB-first order. n must be 1..32; out-of-range or out-of-
// bounds reads return 0.
//
// AIS bit layout: bits[i] is one logical bit (0 or 1) — the MSB of
// the on-the-wire byte at position i/8 when i%8==0. The HDLC
// framer hands us bit-by-bit, so a slice of 0/1 bytes is the
// natural shape and avoids byte-alignment fences mid-field.
func readBitsUint(bits []byte, off, n int) uint32 {
	if n <= 0 || n > 32 {
		return 0
	}
	if off+n > len(bits) {
		// Truncated frame — return 0 rather than panicking; the
		// caller is expected to validate the message length up
		// front.
		return 0
	}
	var v uint32
	for i := 0; i < n; i++ {
		v = (v << 1) | uint32(bits[off+i]&1)
	}
	return v
}

// readBitsInt reads an n-bit signed integer using two's-complement
// sign-extension from the high bit. Used for the lat/lon fields,
// which AIS encodes as signed integers (lon as 28 bits 1/600000
// minute, lat as 27 bits).
func readBitsInt(bits []byte, off, n int) int {
	if n <= 0 || n > 32 {
		return 0
	}
	if off+n > len(bits) {
		return 0
	}
	u := readBitsUint(bits, off, n)
	// Sign-extend: if the high bit is set, fill 1s up to bit 31.
	if u&(1<<uint(n-1)) != 0 {
		u |= ^uint32(0) << uint(n)
	}
	return int(int32(u))
}

// bitsToHex renders the bit slice as a hex string for raw-payload
// logging. Bits are packed MSB-first per byte, padded with zeros.
// Empty input → empty string.
func bitsToHex(bits []byte) string {
	if len(bits) == 0 {
		return ""
	}
	bytesLen := (len(bits) + 7) / 8
	buf := make([]byte, bytesLen)
	for i, b := range bits {
		if b&1 != 0 {
			buf[i/8] |= 1 << uint(7-(i%8))
		}
	}
	return hex.EncodeToString(buf)
}

// unpackBits expands a packed-byte AIS payload into the one-bit-
// per-byte slice the parsers above expect. Mirrors what the future
// AIS HDLC framer will hand off — exposed here so tests + offline
// tools can drive Decode without standing up the full bit-stream
// pipeline.
func unpackBits(payload []byte, nBits int) []byte {
	if nBits <= 0 {
		nBits = len(payload) * 8
	}
	if nBits > len(payload)*8 {
		nBits = len(payload) * 8
	}
	out := make([]byte, nBits)
	for i := 0; i < nBits; i++ {
		if payload[i/8]&(1<<uint(7-(i%8))) != 0 {
			out[i] = 1
		}
	}
	return out
}
