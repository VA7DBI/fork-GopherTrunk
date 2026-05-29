package dsc

// BCH(10,7) is the forward-error-correction code DSC wraps around
// every 7-bit data symbol. ITU-R M.493-15 §3.4: each transmitted
// "character" is 10 bits — 7 data bits (LSB-first inside the
// codeword) followed by 3 check bits computed as a CRC-3 with
// generator polynomial g(x) = x³ + x + 1 (binary 1011).
//
// Despite the "BCH" label the spec uses, the code's minimum
// Hamming distance is 2 — single-bit errors are reliably
// **detected** but not reliably **corrected** at this layer.
// Multiple legal codewords lie within distance 1 of any received
// word (for example, encode(69) and encode(85) differ in only 2
// bits, so flipping the right bit of a corrupted encode(85) can
// produce a valid encode(69)). DSC achieves the actual error
// correction through DX / RX redundancy: each character is sent
// twice, the receiver compares the two streams, and a mismatch
// triggers re-acquisition via the next available redundant copy.
// That comparison lives in the bit-stream layer above this
// package.
//
// Encoding: the codeword is the systematic form data×2³ +
// (data×2³) mod g(x). Decoding here is just the syndrome check —
// callers should treat a non-zero syndrome as "this symbol
// failed; drop it and rely on the DX/RX redundant pair".

// BCHEncode wraps a 7-bit data symbol into its 10-bit BCH(10,7)
// codeword. data must be in 0..127; values outside are masked to
// the low 7 bits.
func BCHEncode(data uint16) uint16 {
	data &= 0x7F
	// Shift data left 3 to make room for parity, then compute
	// parity = (data << 3) mod g(x). g(x) = x³ + x + 1 = 0b1011 = 11.
	dividend := data << 3
	for i := 9; i >= 3; i-- {
		if dividend&(1<<uint(i)) != 0 {
			dividend ^= 0xB << uint(i-3)
		}
	}
	return (data << 3) | (dividend & 0x7)
}

// BCHCheck verifies the 10-bit codeword's CRC-3 parity. Returns
// (data, true) when the syndrome is zero (no error detected),
// (data, false) when the syndrome is non-zero. In both cases the
// returned data is the raw 7 data bits from the codeword; on
// !ok the caller is expected to drop the symbol and rely on the
// DX / RX redundant copy.
func BCHCheck(codeword uint16) (uint16, bool) {
	codeword &= 0x3FF
	syndrome := bchSyndrome(codeword)
	return (codeword >> 3) & 0x7F, syndrome == 0
}

// bchSyndrome computes the 3-bit syndrome of a codeword by
// dividing it by g(x) = 0x0B and returning the remainder.
func bchSyndrome(codeword uint16) uint16 {
	r := codeword & 0x3FF
	for i := 9; i >= 3; i-- {
		if r&(1<<uint(i)) != 0 {
			r ^= 0xB << uint(i-3)
		}
	}
	return r & 0x7
}
