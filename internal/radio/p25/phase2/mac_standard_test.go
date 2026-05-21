package phase2

import (
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/trunking"
)

func u2uPDU(target, src uint32, chanID uint8, chanNum uint16) MACPDU {
	chanField := (uint16(chanID&0xF) << 12) | (chanNum & 0x0FFF)
	return MACPDU{
		Opcode: OpUnitToUnitVoiceChannelGrant,
		Payload: []byte{
			0x00,
			byte(chanField >> 8), byte(chanField),
			byte(target >> 16), byte(target >> 8), byte(target),
			byte(src >> 16), byte(src >> 8), byte(src),
		},
	}
}

func TestAsUnitToUnitVoiceChannelGrant(t *testing.T) {
	pdu := u2uPDU(0x00BEEF, 0x00ABCD, 0x2, 0x123)
	g, ok := pdu.AsUnitToUnitVoiceChannelGrant()
	if !ok {
		t.Fatal("AsUnitToUnitVoiceChannelGrant returned !ok")
	}
	if g.TargetID != 0x00BEEF || g.SourceID != 0x00ABCD {
		t.Errorf("target/source = %06X/%06X, want BEEF/ABCD", g.TargetID, g.SourceID)
	}
	if g.ChannelID != 0x2 || g.ChannelNumber != 0x123 {
		t.Errorf("channel = (%d,%d), want (2,0x123)", g.ChannelID, g.ChannelNumber)
	}
}

func TestAsUnitToUnitWrongOpcode(t *testing.T) {
	pdu := MACPDU{Opcode: OpGroupVoiceChannelGrant, Payload: make([]byte, 9)}
	if _, ok := pdu.AsUnitToUnitVoiceChannelGrant(); ok {
		t.Error("AsUnitToUnitVoiceChannelGrant returned ok for a group-grant opcode")
	}
}

func TestAsRFSSStatusBroadcast(t *testing.T) {
	chanField := (uint16(0x3) << 12) | 0x044
	pdu := MACPDU{
		Opcode: OpRFSSStatusBroadcastUpdate,
		Payload: []byte{
			0x07,       // LRA
			0x01, 0x23, // SystemID = 0x123
			0x05,                                  // RFSS
			0x09,                                  // Site
			byte(chanField >> 8), byte(chanField), // channel
		},
	}
	r, ok := pdu.AsRFSSStatusBroadcast()
	if !ok {
		t.Fatal("AsRFSSStatusBroadcast returned !ok")
	}
	if r.LRA != 0x07 || r.SystemID != 0x123 || r.RFSS != 0x05 || r.Site != 0x09 {
		t.Errorf("RFSS status = %+v", r)
	}
	if r.ChannelID != 0x3 || r.ChannelNumber != 0x044 {
		t.Errorf("channel = (%d,%d), want (3,0x44)", r.ChannelID, r.ChannelNumber)
	}
}

// TestControlChannelPublishesUnitToUnitGrant confirms a unit-to-unit
// grant is published as a KindGrant with the target unit in GroupID.
func TestControlChannelPublishesUnitToUnitGrant(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := New(Options{Bus: bus, SystemName: "p2", FrequencyHz: 851_000_000,
		Now: func() time.Time { return time.Unix(0, 0) }})
	cc.Ingest(u2uPDU(0x00BEEF, 0x00ABCD, 0x1, 0x010))

	gotGrant := false
	for i := 0; i < 2; i++ {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindGrant {
				g := ev.Payload.(trunking.Grant)
				gotGrant = true
				if g.GroupID != 0x00BEEF {
					t.Errorf("grant GroupID = %06X, want target BEEF", g.GroupID)
				}
				if g.SourceID != 0x00ABCD {
					t.Errorf("grant SourceID = %06X, want ABCD", g.SourceID)
				}
			}
		case <-time.After(time.Second):
			t.Fatal("timed out draining events")
		}
	}
	if !gotGrant {
		t.Fatal("unit-to-unit grant not published")
	}
}
