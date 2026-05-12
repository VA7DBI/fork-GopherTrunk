package phase2

import (
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/radio/framing"
)

// assembleNSBPayload builds an 18-byte MAC PDU info window for a
// Network Status Broadcast - Update with the supplied identity
// fields. Mirrors the on-wire byte layout
// AsNetworkStatusBroadcast parses.
func assembleNSBPayload(lra uint8, wacn uint32, sysID, cc uint16, chanID uint8, chanNum uint16) []byte {
	pdu := make([]byte, 18)
	pdu[0] = byte(OpNetworkStatusBroadcastUpdate)
	// Payload bytes start at pdu[1]. Layout (relative to payload[0]):
	//   0       LRA
	//   1..3    WACN (20 bits in upper 20 of bytes 1..3)
	//   3..4    SystemID (low nibble of byte 3 + byte 4)
	//   5..6    Color Code (12 bits, upper 12 of bytes 5..6)
	//   7..8    Channel ID + Number
	pdu[1] = lra
	pdu[2] = byte(wacn >> 12)
	pdu[3] = byte(wacn >> 4)
	pdu[4] = byte((wacn&0x0F)<<4) | byte(sysID>>8)&0x0F
	pdu[5] = byte(sysID & 0xFF)
	pdu[6] = byte(cc >> 4)
	pdu[7] = byte((cc & 0x0F) << 4)
	pdu[8] = (chanID << 4) | byte(chanNum>>8)&0x0F
	pdu[9] = byte(chanNum)
	return pdu
}

// TestAsNetworkStatusBroadcastExtractsIdentity verifies the parser
// recovers the (WACN, SystemID, ColorCode) triple from a hand-built
// NSB payload.
func TestAsNetworkStatusBroadcastExtractsIdentity(t *testing.T) {
	const (
		wacn  uint32 = 0xABCDE
		sysID uint16 = 0x123
		cc    uint16 = 0xF7
		lra   uint8  = 0x42
	)
	info := assembleNSBPayload(lra, wacn, sysID, cc, 0x3, 0x456)
	pdu, err := ParseMACPDU(info)
	if err != nil {
		t.Fatalf("ParseMACPDU: %v", err)
	}
	nsb, ok := pdu.AsNetworkStatusBroadcast()
	if !ok {
		t.Fatalf("AsNetworkStatusBroadcast returned !ok")
	}
	if nsb.WACN != wacn {
		t.Errorf("WACN = %#x, want %#x", nsb.WACN, wacn)
	}
	if nsb.SystemID != sysID {
		t.Errorf("SystemID = %#x, want %#x", nsb.SystemID, sysID)
	}
	if nsb.ColorCode != cc {
		t.Errorf("ColorCode = %#x, want %#x", nsb.ColorCode, cc)
	}
	if nsb.LRA != lra {
		t.Errorf("LRA = %#x, want %#x", nsb.LRA, lra)
	}
	if nsb.ChannelID != 0x3 {
		t.Errorf("ChannelID = %#x, want 0x3", nsb.ChannelID)
	}
	if nsb.ChannelNumber != 0x456 {
		t.Errorf("ChannelNumber = %#x, want 0x456", nsb.ChannelNumber)
	}
}

// TestAsNetworkStatusBroadcastRejectsWrongOpcode confirms the
// accessor rejects PDUs whose opcode is not NSB-Update.
func TestAsNetworkStatusBroadcastRejectsWrongOpcode(t *testing.T) {
	pdu := MACPDU{
		Opcode:  OpGroupVoiceChannelGrant,
		Payload: make([]byte, 17),
	}
	if _, ok := pdu.AsNetworkStatusBroadcast(); ok {
		t.Errorf("AsNetworkStatusBroadcast accepted non-NSB opcode")
	}
}

// TestAsNetworkStatusBroadcastRejectsShortPayload confirms the
// accessor rejects a payload too short to carry the identity
// fields.
func TestAsNetworkStatusBroadcastRejectsShortPayload(t *testing.T) {
	pdu := MACPDU{
		Opcode:  OpNetworkStatusBroadcastUpdate,
		Payload: make([]byte, 8), // need 9
	}
	if _, ok := pdu.AsNetworkStatusBroadcast(); ok {
		t.Errorf("AsNetworkStatusBroadcast accepted 8-byte payload")
	}
}

// TestIngestNSBInstallsScramblerSeed exercises the full auto-seed
// path: hand the state machine an NSB-Update PDU and assert the
// ControlChannel updates its scrambler seed to the
// PN44SeedFromIdentity(WACN, SysID, CC) value.
//
// This is the key behaviour change that closes the Phase 2
// NSB-driven-seed follow-up.
func TestIngestNSBInstallsScramblerSeed(t *testing.T) {
	const (
		wacn  uint32 = 0xABCDE
		sysID uint16 = 0x123
		cc    uint16 = 0xF7
	)
	wantSeed := framing.PN44SeedFromIdentity(wacn, sysID, cc)

	bus := events.NewBus(8)
	defer bus.Close()
	cc1 := New(Options{
		Bus:         bus,
		SystemName:  "P25P2",
		FrequencyHz: 851_000_000,
	})
	if cc1.ScramblerSeed() == wantSeed {
		t.Fatalf("scrambler seed already matches target before NSB ingest; test is meaningless")
	}

	info := assembleNSBPayload(0, wacn, sysID, cc, 0, 0)
	pdu, err := ParseMACPDU(info)
	if err != nil {
		t.Fatalf("ParseMACPDU: %v", err)
	}
	cc1.Ingest(pdu)

	if got := cc1.ScramblerSeed(); got != wantSeed {
		t.Errorf("ScramblerSeed after NSB Ingest = %#x, want %#x", got, wantSeed)
	}
}

// TestIngestNSBOverwritesInitialSeed confirms an NSB-derived seed
// replaces a previously-installed config seed when the two
// disagree. Initial seed installed via SetScramblerSeed; NSB
// arrives with different identity values; final seed reflects the
// NSB.
func TestIngestNSBOverwritesInitialSeed(t *testing.T) {
	const (
		initialSeed = uint64(0xDEADBEEF)
		wacn        = uint32(0x12345)
		sysID       = uint16(0x678)
		cc          = uint16(0x9AB)
	)
	wantSeed := framing.PN44SeedFromIdentity(wacn, sysID, cc)

	bus := events.NewBus(8)
	defer bus.Close()
	cc1 := New(Options{
		Bus:         bus,
		SystemName:  "P25P2",
		FrequencyHz: 851_000_000,
	})
	cc1.SetScramblerSeed(initialSeed)
	if cc1.ScramblerSeed() != initialSeed {
		t.Fatalf("initial seed setup failed; got %#x", cc1.ScramblerSeed())
	}

	info := assembleNSBPayload(0, wacn, sysID, cc, 0, 0)
	pdu, _ := ParseMACPDU(info)
	cc1.Ingest(pdu)

	if got := cc1.ScramblerSeed(); got != wantSeed {
		t.Errorf("ScramblerSeed after NSB overwrite = %#x, want %#x (initial was %#x)",
			got, wantSeed, initialSeed)
	}
}
