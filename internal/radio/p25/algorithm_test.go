package p25

import "testing"

func TestAlgorithmName(t *testing.T) {
	cases := []struct {
		id   uint8
		want string
	}{
		{0x80, "CLEAR"},
		{0x81, "DES-OFB"},
		{0x84, "AES-256"},
		{0x85, "AES-128"},
		{0xAA, "ADP/RC4"},
		{0x00, "unknown"},
		{0xFF, "unknown"},
	}
	for _, c := range cases {
		if got := AlgorithmName(c.id); got != c.want {
			t.Errorf("AlgorithmName(0x%02X) = %q, want %q", c.id, got, c.want)
		}
	}
}

func TestFormatAlgorithm(t *testing.T) {
	cases := []struct {
		id   uint8
		want string
	}{
		{0x84, "0x84 (AES-256)"},
		{0x80, "0x80 (CLEAR)"},
		{0x42, "0x42 (unknown)"},
	}
	for _, c := range cases {
		if got := FormatAlgorithm(c.id); got != c.want {
			t.Errorf("FormatAlgorithm(0x%02X) = %q, want %q", c.id, got, c.want)
		}
	}
}
