package pocsag

import "testing"

// buildNumericPayload packs a string of POCSAG-BCD digits into 20-bit
// codeword payloads. Mirrors what DecodeNumeric reverses; used here
// to round-trip synthetic numeric pages.
func buildNumericPayload(t *testing.T, digits string) []Codeword {
	t.Helper()
	if len(digits)%5 != 0 {
		t.Fatalf("test setup: digits len %d not multiple of 5", len(digits))
	}
	var out []Codeword
	for i := 0; i < len(digits); i += 5 {
		var payload uint32
		for j := 0; j < 5; j++ {
			nib := encodeDigit(digits[i+j])
			payload |= reverseNibble(uint32(nib)) << uint(16-4*j)
		}
		out = append(out, Codeword{Type: WordTypeMessage, MessageBits: payload})
	}
	return out
}

// encodeDigit is the inverse of numericDigit — maps a printable
// POCSAG digit back to its 4-bit nibble. Test helper only.
func encodeDigit(c byte) byte {
	for i, want := range numericTable {
		if want == c {
			return byte(i)
		}
	}
	return 0xC // space
}

func TestDecodeNumericRoundTrip(t *testing.T) {
	for _, c := range []struct {
		digits string
		want   string
	}{
		{"00000", "00000"},
		{"12345", "12345"},
		{"911--", "911--"},
		{"5550CCCCC", "5550"}, // trailing space-pad (0xC = ' ') trimmed
	} {
		words := buildNumericPayload(t, padToFive(c.digits))
		got := DecodeNumeric(words)
		if got != c.want {
			t.Errorf("DecodeNumeric(%q) = %q, want %q", c.digits, got, c.want)
		}
	}
}

func padToFive(s string) string {
	for len(s)%5 != 0 {
		s += "C" // POCSAG numeric space-pad
	}
	return s
}

func TestDecodeAlphaSimpleASCII(t *testing.T) {
	// Build a synthetic alpha message: "HI" = 0x48 0x49.
	// POCSAG alpha sends 7 LSB-first bits per char, MSB-first
	// packed across the 20-bit message field.
	msg := "HI"
	var bitStream []byte
	for _, c := range msg {
		// Emit 7 bits LSB-first.
		for j := 0; j < 7; j++ {
			bitStream = append(bitStream, byte((byte(c)>>uint(j))&1))
		}
	}
	// Pad to a multiple of 20 (one codeword).
	for len(bitStream)%20 != 0 {
		bitStream = append(bitStream, 0)
	}
	// Pack into codewords (20 bits MSB-first into MessageBits).
	var words []Codeword
	for off := 0; off < len(bitStream); off += 20 {
		var payload uint32
		for j := 0; j < 20; j++ {
			if bitStream[off+j] != 0 {
				payload |= 1 << uint(19-j)
			}
		}
		words = append(words, Codeword{Type: WordTypeMessage, MessageBits: payload})
	}
	if got := DecodeAlpha(words); got != "HI" {
		t.Errorf("DecodeAlpha = %q, want %q", got, "HI")
	}
}

func TestDecodeNumericSkipsNonMessageCodewords(t *testing.T) {
	// Mixed slice with an address codeword should ignore it.
	mixed := []Codeword{
		{Type: WordTypeAddress, Address: 0x100, Func: 0},
		buildNumericPayload(t, "12345")[0],
	}
	if got := DecodeNumeric(mixed); got != "12345" {
		t.Errorf("DecodeNumeric(mixed) = %q, want %q", got, "12345")
	}
}

func TestNumericDigitTable(t *testing.T) {
	// Spot-check the printable mapping.
	cases := map[uint32]byte{
		0:  '0',
		9:  '9',
		11: 'U',
		12: ' ',
		13: '-',
	}
	for n, want := range cases {
		if got := numericDigit(n); got != want {
			t.Errorf("numericDigit(%d) = %q, want %q", n, got, want)
		}
	}
}
