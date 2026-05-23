package framing

// CRCCCITT returns the 16-bit CRC-CCITT/FALSE of msg using polynomial
// 0x1021, initial value 0xFFFF, no input/output reflection, no final XOR.
// This is the variant used by the P25 TSBK trailer (the trailer stores the
// bitwise complement; callers handle that at protocol level).
func CRCCCITT(msg []byte) uint16 {
	return CRCCCITTWithInit(msg, 0xFFFF)
}

// CRCCCITTWithInit is the same CRC-CCITT (poly 0x1021, no reflection,
// no final XOR) but with a caller-supplied initial value. Pass
// 0xFFFF for the CCITT/FALSE variant the P25 TSBK trailer uses; pass
// 0x0000 for the XMODEM / YSF FICH variant.
func CRCCCITTWithInit(msg []byte, init uint16) uint16 {
	const poly uint16 = 0x1021
	crc := init
	for _, b := range msg {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ poly
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// CRCCCITTBits computes CRC-CCITT over a bit slice (each entry 0/1, MSB-first
// nibbles within each byte). Useful when the caller already has a bit array.
func CRCCCITTBits(bits []byte) uint16 {
	return CRCCCITT(PackBitsMSB(bits))
}

// CRCCCITTAugmented is the "augmented codeword" variant of CRC-CCITT
// the P25 TSBK trailer uses (TIA-102.AABF, cross-checked against
// OP25's op25/gr-op25_repeater/lib/p25p1_fdma.cc::crc16):
//
//   - Polynomial 0x1021 (same as CRC-CCITT/FALSE).
//   - Init register at 0 (NOT 0xFFFF — that's the CCITT/FALSE variant).
//   - Each message bit is shifted INTO the register MSB-first; when the
//     register overflows beyond 16 bits, the low 16 bits XOR the
//     polynomial.
//   - Final XOR with 0xFFFF.
//
// Encode: trailer = CRCCCITTAugmented(info ‖ 16 zero bits). Append to
// the message; receiver checks CRCCCITTAugmented(info ‖ trailer) == 0.
//
// This is NOT the same algorithm as CRCCCITT (which is the
// "CRC-CCITT/FALSE" variant — init 0xFFFF, byte-shift-out direction,
// no final XOR). Both share the polynomial but produce different
// values on the same input. Issue #275: the original P25 TSBK code
// used CRCCCITT, which caused every on-air TSBK to fail verification
// on the Mt Anakie capture even when the trellis decoder reported
// metric=0 (clean path); on-air trailers verify cleanly under
// CRCCCITTAugmented.
func CRCCCITTAugmented(msg []byte) uint16 {
	const poly uint32 = 0x1021
	var crc uint32
	for _, b := range msg {
		for j := 0; j < 8; j++ {
			bit := uint32((b >> (7 - j)) & 1)
			crc = ((crc << 1) | bit) & 0x1FFFF
			if crc&0x10000 != 0 {
				crc = (crc & 0xFFFF) ^ poly
			}
		}
	}
	crc ^= 0xFFFF
	return uint16(crc & 0xFFFF)
}
