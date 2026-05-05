package framing

import "testing"

func TestGolayRoundTrip(t *testing.T) {
	for d := uint16(0); d < 1<<12; d += 31 {
		cw := GolayEncode24_12(d)
		got, errs := GolayDecode24_12(cw)
		if errs != 0 || got != d {
			t.Errorf("d=%x: got=%x errs=%d", d, got, errs)
		}
	}
}

func TestGolayCorrectsTripleErrors(t *testing.T) {
	d := uint16(0xABC)
	cw := GolayEncode24_12(d)
	// Flip three bits.
	corrupted := cw ^ 0b101010 // bits 1, 3, 5
	got, errs := GolayDecode24_12(corrupted)
	if got != d {
		t.Fatalf("triple-error decode: got %x, want %x", got, d)
	}
	if errs != 3 {
		t.Errorf("errors = %d, want 3", errs)
	}
}

func TestGolayDistanceProperty(t *testing.T) {
	// Min distance must be 8: any two distinct codewords differ in >=8 bits.
	a := GolayEncode24_12(0x000)
	b := GolayEncode24_12(0x001)
	d := PopCount64(uint64(a ^ b))
	if d < 8 {
		t.Errorf("d(c0, c1) = %d, want >= 8", d)
	}
}
