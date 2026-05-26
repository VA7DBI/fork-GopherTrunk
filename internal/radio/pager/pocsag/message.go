package pocsag

import (
	"strings"
)

// MessageEncoding distinguishes the two payload shapes POCSAG
// supports.
type MessageEncoding uint8

const (
	// EncodingNumeric — 4 bits per BCD digit, 5 digits per
	// codeword. POCSAG BCD adds extra symbols: 0-9 = digits;
	// A/0xA = "spare" (some networks use it for *); B/0xB = "U"
	// (Urgent); C/0xC = "spare"; D/0xD = space; E/0xE = "-";
	// F/0xF = ")". The lookup below matches the CCIR 584 table.
	EncodingNumeric MessageEncoding = iota

	// EncodingAlpha — 7 bits per character, packed MSB-first
	// across message codeword payloads. Characters are ASCII with
	// some operator-specific extensions in the 0x00–0x1F control
	// range.
	EncodingAlpha
)

var numericTable = [...]byte{
	'0', '1', '2', '3', '4', '5', '6', '7',
	'8', '9', '*', 'U', ' ', '-', ')', '(',
}

// numericDigit returns the printable POCSAG BCD digit for the
// 4-bit value n.
func numericDigit(n uint32) byte {
	return numericTable[n&0xF]
}

// reverseNibble bit-reverses the low 4 bits — POCSAG numeric
// messages send the LSB first within each nibble (a quirk
// dating to the on-air bit-streaming order), so each 4-bit
// group has to be flipped before mapping through the table.
func reverseNibble(n uint32) uint32 {
	n = ((n & 0x5) << 1) | ((n & 0xA) >> 1)
	n = ((n & 0x3) << 2) | ((n & 0xC) >> 2)
	return n
}

// DecodeNumeric concatenates the 20-bit message fields from each
// codeword in the supplied slice into a numeric POCSAG string.
// Trailing space-padding (0xC in POCSAG BCD) is trimmed.
func DecodeNumeric(messageWords []Codeword) string {
	var b strings.Builder
	for _, w := range messageWords {
		if w.Type != WordTypeMessage {
			continue
		}
		// 20 bits / 4 bits per digit = 5 digits per codeword,
		// MSB-first within the 20-bit field.
		for shift := 16; shift >= 0; shift -= 4 {
			nib := (w.MessageBits >> uint(shift)) & 0xF
			b.WriteByte(numericDigit(reverseNibble(nib)))
		}
	}
	out := b.String()
	// Trim trailing spaces — POCSAG pads short messages with
	// 0xC (the "space" symbol) to fill the last codeword.
	out = strings.TrimRight(out, " ")
	return out
}

// DecodeAlpha decodes a 7-bit-per-character alphanumeric message
// from the supplied message codewords. Characters are read MSB-
// first, packed across the 20-bit message fields with no
// alignment to codeword boundaries — a character can straddle
// the boundary between two codewords.
//
// Standard POCSAG sends bits LSB-first within each character
// (another on-air bit-order quirk), so each 7-bit chunk has to be
// reversed before mapping to ASCII. Control codes 0x00–0x1F and
// 0x7F are mapped to '.' to keep the output printable in a
// terminal; printable chars (0x20–0x7E) pass through. The trailing
// NUL pad-character that fills the last codeword is dropped.
func DecodeAlpha(messageWords []Codeword) string {
	var bits []byte
	for _, w := range messageWords {
		if w.Type != WordTypeMessage {
			continue
		}
		// Append the 20 message bits MSB-first.
		for i := 19; i >= 0; i-- {
			bits = append(bits, byte((w.MessageBits>>uint(i))&1))
		}
	}
	var out strings.Builder
	for i := 0; i+7 <= len(bits); i += 7 {
		// POCSAG alpha sends bits LSB-first within each
		// character. The 7 bits at bits[i..i+7) are
		// {b0, b1, ..., b6} so we need to assemble them
		// back into a byte with bit 0 = b0 etc.
		var c byte
		for j := 0; j < 7; j++ {
			if bits[i+j] != 0 {
				c |= 1 << uint(j)
			}
		}
		if c == 0 {
			// NUL pad — POCSAG fills the last partial codeword
			// with zeros.
			continue
		}
		if c < 0x20 || c == 0x7F {
			c = '.'
		}
		out.WriteByte(c)
	}
	return out.String()
}

// Page is the decoded payload of one POCSAG transmission:
// address (RIC + function) plus the reassembled message.
type Page struct {
	// RIC is the full 21-bit pager address.
	RIC uint32
	// Func is the 2-bit function code (A/B/C/D).
	Func Function
	// Encoding distinguishes numeric vs. alphanumeric. Heuristic
	// at this layer — the function code traditionally implies it
	// (A = tone-only, B = numeric, C = alpha — but real-world
	// networks vary). The caller picks; DecodeNumeric +
	// DecodeAlpha are both available.
	Encoding MessageEncoding
	// Text is the decoded payload string.
	Text string
	// Corrected reports the total bit-error count BCH had to fix
	// across every codeword in the page. High values indicate a
	// marginal signal.
	Corrected int
}
