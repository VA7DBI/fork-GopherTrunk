package framing

// Extended Golay (24, 12, 8): triple-error-correcting / quadruple-error-
// detecting linear block code used widely in P25 and DMR framing.
//
// The implementation uses the standard systematic generator matrix
// G = [I_12 | B] where B is the 12×12 parity matrix below (taken from
// MacWilliams & Sloane, "The Theory of Error-Correcting Codes", §2.6).

var golayB = [12]uint16{
	0xC75, 0x63B, 0xF68, 0x7B4, 0x3DA, 0xD99,
	0xECC, 0x766, 0xCB3, 0xA51, 0xD2A, 0x995,
}

// GolayEncode24_12 encodes 12 data bits (in the low 12 bits of input) into
// a 24-bit codeword. Output layout: [data(12) | parity(12)] with the data
// bits in bits 23..12 and parity in bits 11..0.
func GolayEncode24_12(data uint16) uint32 {
	d := uint32(data & 0x0FFF)
	var parity uint32
	for i := 0; i < 12; i++ {
		if d&(1<<uint(i)) != 0 {
			parity ^= uint32(golayB[i])
		}
	}
	return d<<12 | parity
}

// GolayDecode24_12 decodes a 24-bit codeword. It searches the 4096-codeword
// space for the minimum-Hamming-distance match. Returns (data, errors)
// where errors is the corrected bit count (-1 if > 3, which exceeds the
// guaranteed correction radius).
func GolayDecode24_12(cw uint32) (uint16, int) {
	cw &= 0x00FFFFFF
	bestData := uint16(0)
	bestDist := 25
	for d := uint32(0); d < 1<<12; d++ {
		c := GolayEncode24_12(uint16(d))
		dist := PopCount64(uint64(c ^ cw))
		if dist < bestDist {
			bestDist = dist
			bestData = uint16(d)
			if dist == 0 {
				return bestData, 0
			}
		}
	}
	if bestDist > 3 {
		return bestData, -1
	}
	return bestData, bestDist
}
