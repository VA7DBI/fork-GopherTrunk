package phase1

import "fmt"

// DUID is the 4-bit Data Unit ID that identifies the kind of frame
// following the NID (TIA-102.BAAA §6.2).
type DUID uint8

const (
	DUIDHeader              DUID = 0x0 // HDU
	DUIDTerminator          DUID = 0x3 // TDU (without LC)
	DUIDLogicalLink1        DUID = 0x5 // LDU1
	DUIDTrunkingSignaling   DUID = 0x7 // TSDU (control channel)
	DUIDLogicalLink2        DUID = 0xA // LDU2
	DUIDPacketDataUnit      DUID = 0xC // PDU
	DUIDTerminatorWithLC    DUID = 0xF // TDULC
)

func (d DUID) String() string {
	switch d {
	case DUIDHeader:
		return "HDU"
	case DUIDTerminator:
		return "TDU"
	case DUIDLogicalLink1:
		return "LDU1"
	case DUIDTrunkingSignaling:
		return "TSDU"
	case DUIDLogicalLink2:
		return "LDU2"
	case DUIDPacketDataUnit:
		return "PDU"
	case DUIDTerminatorWithLC:
		return "TDULC"
	default:
		return fmt.Sprintf("DUID(%X)", uint8(d))
	}
}

// NID is the 64-bit Network ID immediately following the FSW. Bits 0..11
// are the NAC (Network Access Code), bits 12..15 are the DUID, and bits
// 16..63 are a BCH(63,16) parity field plus a single parity bit.
type NID struct {
	NAC  uint16
	DUID DUID
}

// ParseNID extracts the NAC and DUID from 64 received bits (MSB-first).
// It does NOT yet perform full BCH(63,16,11) error correction; callers may
// validate with a future framing.BCH63_16 decoder.
func ParseNID(bits []byte) (NID, error) {
	if len(bits) < 64 {
		return NID{}, fmt.Errorf("p25/phase1: NID requires 64 bits, got %d", len(bits))
	}
	var nac uint16
	for i := 0; i < 12; i++ {
		nac = (nac << 1) | uint16(bits[i]&1)
	}
	var duid uint8
	for i := 12; i < 16; i++ {
		duid = (duid << 1) | (bits[i] & 1)
	}
	return NID{NAC: nac, DUID: DUID(duid)}, nil
}

// NIDFromDibits is a convenience wrapper that takes 32 dibits (= 64 bits).
func NIDFromDibits(dibits []uint8) (NID, error) {
	if len(dibits) < 32 {
		return NID{}, fmt.Errorf("p25/phase1: NID requires 32 dibits, got %d", len(dibits))
	}
	bits := make([]byte, 64)
	for i := 0; i < 32; i++ {
		bits[2*i] = (dibits[i] >> 1) & 1
		bits[2*i+1] = dibits[i] & 1
	}
	return ParseNID(bits)
}
