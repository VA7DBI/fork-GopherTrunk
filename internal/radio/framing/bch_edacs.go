package framing

// EDACS BCH(40, 28, 2) codeword check.
//
// EDACS Standard control-channel words (CCWs) are protected by a
// shortened BCH(40, 28, 2) code per lwvmobile/edacs-fm's bch3.h —
// the most-cited public reference for the EDACS channel coding.
// The shortened code derives from the BCH(63, 51, 2) mother code
// over GF(2^6) with primitive polynomial x^6 + x + 1; the
// generator polynomial is the product of the minimal polynomials
// of α and α^3:
//
//	m₁(x) = x^6 + x + 1
//	m₃(x) = x^6 + x^4 + x^2 + x + 1
//	g(x)  = m₁(x) · m₃(x)
//	      = x^12 + x^10 + x^8 + x^5 + x^4 + x^3 + 1
//	      = 0x1539  (= 0x0539 without the implicit x^12)
//
// The code corrects up to t = 2 bit errors per 40-bit codeword,
// with designed minimum distance d = 5.
//
// Codeword layout (systematic):
//
//	bits 0..11   12-bit BCH parity (low bits)
//	bits 12..39  28-bit information field (high bits)

const (
	bchEDACSCodewordBits = 40
	bchEDACSInfoBits     = 28
	bchEDACSParityBits   = 12
	bchEDACSInfoMask     = uint32(1)<<bchEDACSInfoBits - 1
	bchEDACSParityMask   = uint16(1)<<bchEDACSParityBits - 1

	// bchEDACSGenerator is the degree-12 generator polynomial
	// g(x) = x^12 + x^10 + x^8 + x^5 + x^4 + x^3 + 1, packed
	// into a 13-bit uint16 (bit 12 = x^12, bit 0 = 1).
	bchEDACSGenerator uint16 = 0x1539
)

// bchEDACSSyndromes[i] is the 12-bit syndrome of a single-bit
// error at codeword position i (i.e. x^i mod g(x)), computed at
// package init time. The decoder uses this table for both
// single-bit and double-bit error correction (a double-bit error
// at positions (i, j) has syndrome syndromes[i] ^ syndromes[j]).
var bchEDACSSyndromes [bchEDACSCodewordBits]uint16

func init() {
	// Compute x^i mod g(x) for i in 0..39 via repeated shift +
	// reduction.
	v := uint16(1)
	for i := 0; i < bchEDACSCodewordBits; i++ {
		bchEDACSSyndromes[i] = v
		// Multiply by x: shift left and reduce if the new top bit
		// is set.
		v <<= 1
		if v&(1<<bchEDACSParityBits) != 0 {
			v ^= bchEDACSGenerator
		}
		v &= bchEDACSParityMask
	}
}

// computeBCHEDACSSyndrome returns the 12-bit BCH syndrome of a
// 40-bit codeword: cw(x) mod g(x).
func computeBCHEDACSSyndrome(cw uint64) uint16 {
	var s uint16
	for i := 0; i < bchEDACSCodewordBits; i++ {
		if cw&(uint64(1)<<uint(i)) != 0 {
			s ^= bchEDACSSyndromes[i]
		}
	}
	return s
}

// BCHEncodeEDACS builds a 40-bit EDACS codeword from 28 bits of
// information. Only the low 28 bits of info are used. The
// codeword is systematic: info bits occupy positions 12..39
// (high), the 12-bit BCH parity occupies positions 0..11 (low).
func BCHEncodeEDACS(info uint32) uint64 {
	info &= bchEDACSInfoMask
	// Place info at bits 12..39, then derive parity such that the
	// total codeword is divisible by g(x). The systematic parity
	// is the syndrome of (info << 12).
	cw := uint64(info) << bchEDACSParityBits
	parity := computeBCHEDACSSyndrome(cw)
	return cw | uint64(parity)
}

// BCHDecodeEDACS validates and (where possible) corrects a 40-bit
// EDACS codeword. Returns (info, errs) where:
//
//   - errs = 0: codeword passed cleanly; info is the recovered
//     28-bit information field.
//   - errs = 1 or 2: a 1- or 2-bit error was detected and
//     corrected; info reflects the corrected information field.
//   - errs = -1: the codeword carries more than 2 bit errors (or
//     a pattern this BCH(40, 28, 2) can't isolate); info is the
//     received information field but should not be trusted.
//
// Single-bit corrections are looked up directly in the per-
// position syndrome table; double-bit corrections iterate the
// 780 ordered pairs of bit positions and match against the
// observed syndrome.
func BCHDecodeEDACS(cw uint64) (uint32, int) {
	cw &= (uint64(1) << bchEDACSCodewordBits) - 1
	syndrome := computeBCHEDACSSyndrome(cw)
	if syndrome == 0 {
		return uint32(cw>>bchEDACSParityBits) & bchEDACSInfoMask, 0
	}
	// Single-bit correction.
	for pos := 0; pos < bchEDACSCodewordBits; pos++ {
		if bchEDACSSyndromes[pos] == syndrome {
			corrected := cw ^ (uint64(1) << uint(pos))
			return uint32(corrected>>bchEDACSParityBits) & bchEDACSInfoMask, 1
		}
	}
	// Double-bit correction.
	for i := 0; i < bchEDACSCodewordBits-1; i++ {
		for j := i + 1; j < bchEDACSCodewordBits; j++ {
			if bchEDACSSyndromes[i]^bchEDACSSyndromes[j] == syndrome {
				corrected := cw ^ (uint64(1) << uint(i)) ^ (uint64(1) << uint(j))
				return uint32(corrected>>bchEDACSParityBits) & bchEDACSInfoMask, 2
			}
		}
	}
	return uint32(cw>>bchEDACSParityBits) & bchEDACSInfoMask, -1
}
