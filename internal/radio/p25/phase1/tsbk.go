package phase1

import (
	"encoding/binary"
	"fmt"

	"github.com/MattCheramie/GopherTrunk/internal/radio/framing"
)

// TSBK is one Trunking Signaling Block parsed from the TSDU. The header
// fields are common to every opcode; Payload holds the 8 opcode-specific
// octets (bits 16..79 of the 96-bit info block).
type TSBK struct {
	LB      bool   // Last Block in TSDU sequence
	P       bool   // Protected
	Opcode  Opcode // 6-bit opcode
	MFID    uint8  // Manufacturer ID (0x00 = standard)
	Payload [8]byte
}

// CRCError is returned by ParseTSBK when the trailer CRC fails.
var CRCError = fmt.Errorf("p25/phase1: TSBK CRC check failed")

// ParseTSBK consumes 96 info bits (12 bytes) and returns a parsed block.
// The trailer CRC is verified; CRCError is returned on mismatch (the
// partially-parsed TSBK is still returned so callers can log the contents
// for diagnostics).
func ParseTSBK(info []byte) (TSBK, error) {
	if len(info) != 12 {
		return TSBK{}, fmt.Errorf("p25/phase1: TSBK info must be 12 bytes, got %d", len(info))
	}
	var t TSBK
	t.LB = info[0]&0x80 != 0
	t.P = info[0]&0x40 != 0
	t.Opcode = Opcode(info[0] & 0x3F)
	t.MFID = info[1]
	copy(t.Payload[:], info[2:10])

	storedCRC := binary.BigEndian.Uint16(info[10:12]) ^ 0xFFFF
	want := framing.CRCCCITT(info[:10])
	if storedCRC != want {
		return t, CRCError
	}
	return t, nil
}

// AssembleTSBK constructs a 12-byte TSBK info block from the structured
// fields. Used in tests and for any future encoder work.
func AssembleTSBK(t TSBK) []byte {
	out := make([]byte, 12)
	if t.LB {
		out[0] |= 0x80
	}
	if t.P {
		out[0] |= 0x40
	}
	out[0] |= byte(t.Opcode) & 0x3F
	out[1] = t.MFID
	copy(out[2:10], t.Payload[:])
	crc := framing.CRCCCITT(out[:10]) ^ 0xFFFF
	binary.BigEndian.PutUint16(out[10:12], crc)
	return out
}
