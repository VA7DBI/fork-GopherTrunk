package framing

import "testing"

func TestBCHEncode31_21RoundTrip(t *testing.T) {
	for _, data := range []uint32{
		0x000000, 0x000001, 0x100000, 0x1FFFFF, // edges
		0x055555, 0x0AAAAA, 0x123456, 0x0DEAD7,
	} {
		cw := BCHEncode31_21(data)
		// Info bits must be in positions 30..10.
		gotInfo := (cw >> 10) & 0x1FFFFF
		if gotInfo != data {
			t.Errorf("BCHEncode31_21(0x%x): info field = 0x%x, want 0x%x",
				data, gotInfo, data)
		}
		// Decode the just-encoded codeword should give errors=0.
		gotData, errs := BCHDecode31_21(cw)
		if errs != 0 {
			t.Errorf("BCHDecode31_21(encode(0x%x)): errors = %d, want 0",
				data, errs)
		}
		if gotData != data {
			t.Errorf("BCHDecode31_21(encode(0x%x)): data = 0x%x", data, gotData)
		}
	}
}

func TestBCHDecode31_21CorrectsSingleBitError(t *testing.T) {
	data := uint32(0x123456)
	cw := BCHEncode31_21(data)
	for bit := 0; bit < 31; bit++ {
		flipped := cw ^ (1 << uint(bit))
		gotData, errs := BCHDecode31_21(flipped)
		if errs != 1 {
			t.Errorf("single flip at bit %d: errors = %d, want 1", bit, errs)
		}
		if gotData != data {
			t.Errorf("single flip at bit %d: data = 0x%x, want 0x%x",
				bit, gotData, data)
		}
	}
}

func TestBCHDecode31_21CorrectsDoubleBitError(t *testing.T) {
	// Spot-check a few two-bit error patterns.
	data := uint32(0x0AAAAA)
	cw := BCHEncode31_21(data)
	pairs := [][2]int{
		{0, 1},
		{5, 17},
		{10, 11},
		{0, 30},
		{14, 21},
	}
	for _, p := range pairs {
		flipped := cw ^ (1 << uint(p[0])) ^ (1 << uint(p[1]))
		gotData, errs := BCHDecode31_21(flipped)
		if errs != 2 {
			t.Errorf("double flip at %v: errors = %d, want 2", p, errs)
		}
		if gotData != data {
			t.Errorf("double flip at %v: data = 0x%x, want 0x%x",
				p, gotData, data)
		}
	}
}

func TestBCHDecode31_21RejectsTripleBitError(t *testing.T) {
	data := uint32(0x123456)
	cw := BCHEncode31_21(data)
	// Three deliberate bit flips — well outside BCH(31,21)'s
	// 2-bit correction radius. The decoder should report -1 to
	// signal "uncorrectable".
	flipped := cw ^ (1 << 0) ^ (1 << 7) ^ (1 << 19)
	_, errs := BCHDecode31_21(flipped)
	if errs != -1 {
		t.Errorf("triple flip: errors = %d, want -1 (uncorrectable)", errs)
	}
}

func TestBCH3121ParityBit(t *testing.T) {
	for _, c := range []struct {
		cw     uint32
		expect byte
	}{
		{0x00000000, 0},
		{0x00000001, 1},
		{0x00000003, 0},
		{0x7FFFFFFF, 1}, // 31 ones → odd
		{0x55555555, 0}, // even count, but the high bit beyond 31 is masked off
	} {
		got := BCH3121ParityBit(c.cw)
		if got != c.expect {
			t.Errorf("parity(0x%x) = %d, want %d", c.cw, got, c.expect)
		}
	}
}
