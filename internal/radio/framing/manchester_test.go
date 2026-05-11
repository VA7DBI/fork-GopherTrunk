package framing

import (
	"bytes"
	"errors"
	"testing"
)

func TestManchesterEncodeKnownPattern(t *testing.T) {
	in := []byte{0, 1, 0, 1, 1, 0, 0, 1}
	want := []byte{0, 1, 1, 0, 0, 1, 1, 0, 1, 0, 0, 1, 0, 1, 1, 0}
	if got := ManchesterEncode(in); !bytes.Equal(got, want) {
		t.Errorf("ManchesterEncode mismatch:\n  got  %v\n  want %v", got, want)
	}
}

func TestManchesterRoundtrip(t *testing.T) {
	in := []byte{1, 0, 1, 1, 0, 0, 1, 0, 1, 1, 1, 0, 0, 1, 0, 0, 1, 1, 0, 1}
	encoded := ManchesterEncode(in)
	decoded, err := ManchesterDecode(encoded)
	if err != nil {
		t.Fatalf("ManchesterDecode: %v", err)
	}
	if !bytes.Equal(decoded, in) {
		t.Errorf("round-trip mismatch:\n  got  %v\n  want %v", decoded, in)
	}
}

func TestManchesterDecodeReportsTransitionFailure(t *testing.T) {
	// 01 01 11 10 → first two bits decode OK (0, 0), then 11 fails.
	bad := []byte{0, 1, 0, 1, 1, 1, 1, 0}
	got, err := ManchesterDecode(bad)
	if !errors.Is(err, ErrManchesterInvalid) {
		t.Errorf("err = %v, want ErrManchesterInvalid", err)
	}
	want := []byte{0, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("partial decode = %v, want %v", got, want)
	}
}

func TestManchesterDecodeMajorityCountsInvalidPairs(t *testing.T) {
	// 01 (valid 0) + 11 (invalid) + 10 (valid 1) + 00 (invalid)
	bits := []byte{0, 1, 1, 1, 1, 0, 0, 0}
	got, invalid := ManchesterDecodeMajority(bits)
	if invalid != 2 {
		t.Errorf("invalid = %d, want 2", invalid)
	}
	// Invalid pairs fall back to the first sample → 0b1 then 0b0.
	want := []byte{0, 1, 1, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("decoded = %v, want %v", got, want)
	}
}

func TestManchesterDecodeOddLengthDropsTrailingBit(t *testing.T) {
	in := []byte{0, 1, 1, 0, 1} // 2 pairs + trailing 1
	got, err := ManchesterDecode(in)
	if err != nil {
		t.Fatalf("ManchesterDecode: %v", err)
	}
	want := []byte{0, 1}
	if !bytes.Equal(got, want) {
		t.Errorf("decoded = %v, want %v", got, want)
	}
}
