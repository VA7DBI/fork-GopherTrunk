package framing

import (
	"bytes"
	"testing"
)

func TestPackUnpackRoundTrip(t *testing.T) {
	bits := []byte{1, 0, 1, 1, 0, 1, 0, 1, 1, 1}
	packed := PackBitsMSB(bits)
	want := []byte{0xB5, 0xC0} // 10110101 11000000
	if !bytes.Equal(packed, want) {
		t.Errorf("packed = %x, want %x", packed, want)
	}
	back := UnpackBitsMSB(packed, len(bits))
	if !bytes.Equal(back, bits) {
		t.Errorf("unpacked = %v, want %v", back, bits)
	}
}

func TestDibitsBits(t *testing.T) {
	dibits := []uint8{0, 1, 2, 3, 1, 2}
	bits := DibitsToBits(dibits)
	want := []byte{0, 0, 0, 1, 1, 0, 1, 1, 0, 1, 1, 0}
	if !bytes.Equal(bits, want) {
		t.Errorf("DibitsToBits = %v, want %v", bits, want)
	}
	back := BitsToDibits(bits)
	if !bytes.Equal(back, dibits) {
		t.Errorf("BitsToDibits = %v, want %v", back, dibits)
	}
}

func TestPopCount64(t *testing.T) {
	cases := map[uint64]int{0: 0, 1: 1, 0xFF: 8, 0xFFFFFFFFFFFFFFFF: 64, 0xAAAA: 8}
	for v, want := range cases {
		if got := PopCount64(v); got != want {
			t.Errorf("PopCount64(%x) = %d, want %d", v, got, want)
		}
	}
}

func TestCRCCCITTKnownVector(t *testing.T) {
	// CRC-CCITT/FALSE of "123456789" is 0x29B1.
	got := CRCCCITT([]byte("123456789"))
	if got != 0x29B1 {
		t.Errorf("CRC-CCITT('123456789') = %04X, want 29B1", got)
	}
}

func TestCRCBitsMatchesBytes(t *testing.T) {
	msg := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	want := CRCCCITT(msg)
	bits := UnpackBitsMSB(msg, 32)
	got := CRCCCITTBits(bits)
	if got != want {
		t.Errorf("CRCCCITTBits = %04X, want %04X", got, want)
	}
}
