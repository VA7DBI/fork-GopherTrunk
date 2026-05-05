package framing

import (
	"bytes"
	"testing"
)

func TestTrellisEncodeDecodeRoundTrip(t *testing.T) {
	tr := Trellis4StatePoly(0b111, 0b101) // (7,5) octal
	in := []byte{1, 0, 1, 1, 0, 1, 0, 0, 1, 1, 1, 0, 0, 1, 0, 1, 0, 0}
	rx := tr.Encode(in)
	got := tr.DecodeHard(rx)
	if !bytes.Equal(got, in) {
		t.Errorf("clean round-trip mismatch:\n got %v\nwant %v", got, in)
	}
}

func TestTrellisDecodesWithSingleErrors(t *testing.T) {
	tr := Trellis4StatePoly(0b111, 0b101)
	in := []byte{1, 1, 0, 1, 0, 1, 1, 1, 0, 0, 1, 0, 0, 1, 1, 0, 1, 0, 1, 1, 0}
	rx := tr.Encode(in)
	// Flip one bit in one dibit (maximum local error of 1 dibit-distance).
	rx[5] ^= 0b01
	got := tr.DecodeHard(rx)
	if !bytes.Equal(got, in) {
		t.Errorf("single-error decode mismatch:\n got %v\nwant %v", got, in)
	}
}

func TestTrellisOutputShape(t *testing.T) {
	tr := Trellis4StatePoly(0b111, 0b101)
	in := make([]byte, 100)
	out := tr.Encode(in)
	if len(out) != len(in) {
		t.Errorf("encoded len = %d, want %d", len(out), len(in))
	}
	for _, d := range out {
		if d > 3 {
			t.Errorf("dibit out of range: %d", d)
		}
	}
}
