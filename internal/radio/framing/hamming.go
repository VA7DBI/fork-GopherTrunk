package framing

// Hamming(15,11,3): single-error-correcting linear block code with the
// systematic form used by P25 (TIA-102.BAAA Annex B). The four parity bits
// are computed from a fixed generator matrix; this implementation stores
// the 11-bit→15-bit codewords in straightforward bit form.
//
// Generator polynomial: 1 + x + x^4. The parity bits are:
//   p0 = d0 + d1 + d2 + d3 + d5 + d7 + d8
//   p1 = d1 + d2 + d3 + d4 + d6 + d8 + d9
//   p2 = d2 + d3 + d4 + d5 + d7 + d9 + d10
//   p3 = d0 + d1 + d2 + d4 + d6 + d7 + d10
// (per TIA-102.BAAA section B.2). Codeword layout: [d10 d9 ... d0 p3 p2 p1 p0].

// HammingEncode15_11 encodes 11 data bits (LSB-first packed in lower 11
// bits of input) into the 15-bit codeword. Output occupies bits 0..14.
func HammingEncode15_11(data uint16) uint16 {
	d := data & 0x07FF
	bit := func(i int) uint16 { return (d >> i) & 1 }
	p0 := bit(0) ^ bit(1) ^ bit(2) ^ bit(3) ^ bit(5) ^ bit(7) ^ bit(8)
	p1 := bit(1) ^ bit(2) ^ bit(3) ^ bit(4) ^ bit(6) ^ bit(8) ^ bit(9)
	p2 := bit(2) ^ bit(3) ^ bit(4) ^ bit(5) ^ bit(7) ^ bit(9) ^ bit(10)
	p3 := bit(0) ^ bit(1) ^ bit(2) ^ bit(4) ^ bit(6) ^ bit(7) ^ bit(10)
	return d<<4 | p3<<3 | p2<<2 | p1<<1 | p0
}

// HammingDecode15_11 decodes a 15-bit codeword. Returns (data, errors),
// where errors == 0 means no error, 1 means a single bit was corrected,
// and -1 means the codeword was uncorrectable (multiple bit errors).
func HammingDecode15_11(cw uint16) (uint16, int) {
	cw &= 0x7FFF
	// Recompute parity from received data bits.
	enc := HammingEncode15_11(cw >> 4)
	syn := (enc ^ cw) & 0x0F
	if syn == 0 {
		return cw >> 4, 0
	}
	// Look up the bit position for this syndrome. Build the table by
	// flipping each of the 15 bits and recomputing the syndrome.
	for i := 0; i < 15; i++ {
		flipped := cw ^ (1 << uint(i))
		if (HammingEncode15_11(flipped>>4)^flipped)&0x0F == 0 {
			return flipped >> 4, 1
		}
	}
	return cw >> 4, -1
}
