package framing

// POCSAG paging uses BCH(31,21) — a binary BCH code that carries 21
// information bits in 31 codeword bits, correcting up to 2 bit errors
// per codeword. POCSAG extends this to 32 bits by appending an
// overall even-parity bit (the "POCSAG codeword" on the wire).
//
// Generator polynomial g(x) (CCIR Recommendation 584 §3.2.1 — the
// POCSAG / RPC-1 specification):
//
//	g(x) = x^10 + x^9 + x^8 + x^6 + x^5 + x^3 + 1
//
// representable as the 11-bit constant 0x769:
//
//	bits MSB→LSB:  1 1 1 0 1 1 0 1 0 0 1
//	binary:        11101101001
//	hex:           0x769
//
// Codewords are stored systematically:
//
//	bit 30..10  = 21 info bits  (MSB-first, bit 30 = MSB)
//	bit 9..0    = 10 parity bits
//
// We pack the 31-bit codeword into the low bits of a uint32. The
// extra POCSAG even-parity bit lives at bit 0 of the 32-bit on-wire
// codeword (POCSAG codeword layout below).
//
// POCSAG codeword format (32 bits, MSB-first as transmitted):
//
//	bit 31     flag bit (0 = address, 1 = message)
//	bit 30..11 20 data bits
//	bit 10..1  10 BCH parity bits
//	bit 0      overall even-parity bit
//
// In this code we model the BCH(31,21) primitive without the extra
// parity bit — the wrapper that handles the full 32-bit POCSAG
// codeword (flag + data + BCH + parity) lives in
// internal/radio/pager/pocsag. The BCH layer treats the flag as the
// MSB of its 21-bit info field.

const bch3121Generator uint32 = 0x769

// BCHEncode31_21 encodes 21 information bits into a 31-bit BCH
// codeword. Only the low 21 bits of data are used. The result
// occupies the low 31 bits of the returned uint32 (info in
// 30..10, parity in 9..0).
func BCHEncode31_21(data uint32) uint32 {
	info := data & 0x1FFFFF // low 21 bits
	rem := info << 10
	for i := 30; i >= 10; i-- {
		if rem&(uint32(1)<<uint(i)) != 0 {
			rem ^= bch3121Generator << uint(i-10)
		}
	}
	return (info << 10) | (rem & 0x3FF)
}

// BCHDecode31_21 decodes a 31-bit BCH codeword by minimum-Hamming-
// distance search across all 2^21 valid codewords. Returns (data,
// errors) where errors is the bit-error count corrected, or -1 if
// the closest valid codeword is more than 2 bits away
// (uncorrectable; data is the best guess but should not be
// trusted).
//
// The exhaustive search is fast enough for POCSAG's modest rates
// (≤2400 bd → ≤75 codewords/s) on any modern CPU — 2^21 ≈ 2 M
// XOR+popcount operations per decode is single-digit milliseconds.
// A future optimisation could use the standard syndrome / Chien
// search, but the brute-force version matches the same shape as
// the existing BCH(63,16) decoder for symmetry.
func BCHDecode31_21(cw uint32) (uint32, int) {
	cw &= 0x7FFFFFFF // mask to 31 bits
	var bestData uint32
	bestDist := 32
	for d := uint32(0); d < 1<<21; d++ {
		c := BCHEncode31_21(d)
		dist := popCount32(c ^ cw)
		if dist < bestDist {
			bestDist = dist
			bestData = d
			if dist == 0 {
				return bestData, 0
			}
		}
	}
	if bestDist > 2 {
		return bestData, -1
	}
	return bestData, bestDist
}

// BCH3121ParityBit returns the even-parity bit over a 31-bit BCH
// codeword. The trailing bit appended to form the 32-bit POCSAG
// on-wire codeword.
func BCH3121ParityBit(cw uint32) byte {
	return byte(popCount32(cw&0x7FFFFFFF) & 1)
}

func popCount32(x uint32) int {
	x = x - ((x >> 1) & 0x55555555)
	x = (x & 0x33333333) + ((x >> 2) & 0x33333333)
	x = (x + (x >> 4)) & 0x0F0F0F0F
	return int((x * 0x01010101) >> 24)
}
