package framing

import "testing"

func TestHammingRoundTrip(t *testing.T) {
	for d := uint16(0); d < 1<<11; d++ {
		cw := HammingEncode15_11(d)
		got, errs := HammingDecode15_11(cw)
		if errs != 0 {
			t.Fatalf("clean codeword %x reported %d errors", d, errs)
		}
		if got != d {
			t.Fatalf("decode(encode(%x)) = %x", d, got)
		}
	}
}

func TestHammingCorrectsSingleError(t *testing.T) {
	for d := uint16(0); d < 1<<11; d += 17 { // sparse sweep
		cw := HammingEncode15_11(d)
		for bit := 0; bit < 15; bit++ {
			corrupted := cw ^ (1 << uint(bit))
			got, errs := HammingDecode15_11(corrupted)
			if errs != 1 {
				t.Errorf("d=%x bit=%d: errs=%d, want 1", d, bit, errs)
			}
			if got != d {
				t.Errorf("d=%x bit=%d: got=%x, want %x", d, bit, got, d)
			}
		}
	}
}
