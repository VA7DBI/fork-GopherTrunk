package phase2

import "encoding/binary"

// Structured accessors for the standard (non-manufacturer-specific) P25
// Phase 2 MAC opcodes beyond the group-voice-grant / NSB / IdentifierUp
// trio handled in mac.go. Each As...() accessor returns (struct, false)
// when the PDU's opcode does not match, so a caller can probe a PDU
// against several accessors in turn.

// UnitToUnitVoiceChannelGrant is the structured shape of a Phase 2
// unit-to-unit (private call) voice-grant MAC PDU (opcode 0x48):
//
//	byte 0    : service options
//	bytes 1-2 : channel ID + channel number (4 + 12 bits)
//	bytes 3-5 : target unit ID (24 bits)
//	bytes 6-8 : source unit ID (24 bits)
type UnitToUnitVoiceChannelGrant struct {
	ServiceOptions uint8
	ChannelID      uint8
	ChannelNumber  uint16
	TargetID       uint32
	SourceID       uint32
}

// AsUnitToUnitVoiceChannelGrant returns the structured grant if the PDU
// opcode is OpUnitToUnitVoiceChannelGrant, otherwise (zero, false).
func (p MACPDU) AsUnitToUnitVoiceChannelGrant() (UnitToUnitVoiceChannelGrant, bool) {
	if p.Opcode != OpUnitToUnitVoiceChannelGrant {
		return UnitToUnitVoiceChannelGrant{}, false
	}
	if len(p.Payload) < 9 {
		return UnitToUnitVoiceChannelGrant{}, false
	}
	chanField := binary.BigEndian.Uint16(p.Payload[1:3])
	return UnitToUnitVoiceChannelGrant{
		ServiceOptions: p.Payload[0],
		ChannelID:      uint8(chanField >> 12),
		ChannelNumber:  chanField & 0x0FFF,
		TargetID:       uint32(p.Payload[3])<<16 | uint32(p.Payload[4])<<8 | uint32(p.Payload[5]),
		SourceID:       uint32(p.Payload[6])<<16 | uint32(p.Payload[7])<<8 | uint32(p.Payload[8]),
	}, true
}

// RFSSStatusBroadcast is the structured shape of a Phase 2 RFSS Status
// Broadcast - Update MAC PDU (opcode 0xFA). It names the site the
// receiver is camped on so a scanner can log RFSS / site topology:
//
//	byte 0    : LRA (Location Registration Area)
//	bytes 1-2 : System ID (12 bits, low 12)
//	byte 3    : RFSS ID
//	byte 4    : Site ID
//	bytes 5-6 : channel ID + channel number (4 + 12 bits)
type RFSSStatusBroadcast struct {
	LRA           uint8
	SystemID      uint16 // 12-bit
	RFSS          uint8
	Site          uint8
	ChannelID     uint8
	ChannelNumber uint16
}

// AsRFSSStatusBroadcast returns the structured RFSS status if the PDU
// opcode is OpRFSSStatusBroadcastUpdate, otherwise (zero, false).
func (p MACPDU) AsRFSSStatusBroadcast() (RFSSStatusBroadcast, bool) {
	if p.Opcode != OpRFSSStatusBroadcastUpdate {
		return RFSSStatusBroadcast{}, false
	}
	if len(p.Payload) < 7 {
		return RFSSStatusBroadcast{}, false
	}
	chanField := binary.BigEndian.Uint16(p.Payload[5:7])
	return RFSSStatusBroadcast{
		LRA:           p.Payload[0],
		SystemID:      uint16(p.Payload[1]&0x0F)<<8 | uint16(p.Payload[2]),
		RFSS:          p.Payload[3],
		Site:          p.Payload[4],
		ChannelID:     uint8(chanField >> 12),
		ChannelNumber: chanField & 0x0FFF,
	}, true
}
