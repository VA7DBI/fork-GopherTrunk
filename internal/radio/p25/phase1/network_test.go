package phase1

import (
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

func TestNetworkModelAccumulates(t *testing.T) {
	var m NetworkModel
	m.ApplyNetworkStatus(NetworkStatusBroadcast{WACN: 0xABCDE, SystemID: 0x123})
	m.ApplyRFSSStatus(RFSSStatusBroadcast{SystemID: 0x123, RFSS: 4, Site: 7, LRA: 9})
	m.ApplySecondaryControlChannel(SecondaryControlChannelBroadcast{
		ChannelAID: 1, ChannelANumber: 100, ChannelBID: 1, ChannelBNumber: 200})
	m.ApplyAdjacentSite(AdjacentSiteStatusBroadcast{RFSS: 4, Site: 8, ChannelID: 1, ChannelNumber: 300})
	m.ApplyAdjacentSite(AdjacentSiteStatusBroadcast{RFSS: 4, Site: 9, ChannelID: 1, ChannelNumber: 301})
	// Re-broadcast of site 8 must update in place, not duplicate.
	m.ApplyAdjacentSite(AdjacentSiteStatusBroadcast{RFSS: 4, Site: 8, ChannelID: 1, ChannelNumber: 305})

	cfg := m.Snapshot()
	if cfg.WACN != 0xABCDE || cfg.SystemID != 0x123 {
		t.Errorf("WACN/SystemID = %#x/%#x", cfg.WACN, cfg.SystemID)
	}
	if cfg.RFSS != 4 || cfg.Site != 7 || cfg.LRA != 9 {
		t.Errorf("RFSS/Site/LRA = %d/%d/%d", cfg.RFSS, cfg.Site, cfg.LRA)
	}
	if len(cfg.Secondary) != 2 {
		t.Errorf("Secondary = %v, want 2 channels", cfg.Secondary)
	}
	if len(cfg.Neighbors) != 2 {
		t.Fatalf("Neighbors = %v, want 2 (site 8 deduped)", cfg.Neighbors)
	}
	for _, n := range cfg.Neighbors {
		if n.Site == 8 && n.ChannelNumber != 305 {
			t.Errorf("site 8 neighbour not updated: %+v", n)
		}
	}
}

// TestControlChannelAccumulatesTopology drives status-broadcast TSBKs
// through the control channel and checks NetworkSnapshot reflects them.
func TestControlChannelAccumulatesTopology(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	cc := New(Options{Bus: bus, SystemName: "S"})

	// NSB payload: WACN 0xABCDE (p0<<12|p1<<4|p2>>4), SystemID 0x123
	// ((p2&0x0F)<<8|p3) — see ParseNetworkStatusBroadcast.
	nsb := TSBK{Opcode: OpNetworkStatusBroadcast,
		Payload: [8]byte{0xAB, 0xCD, 0xE1, 0x23}}
	// RFSS payload: LRA p0, RFSS p2, Site p3 — see ParseRFSSStatusBroadcast.
	rfss := TSBK{Opcode: OpRFSSStatusBroadcast,
		Payload: [8]byte{9, 0, 4, 7}}
	adj := TSBK{Opcode: OpAdjacentSiteStatusBroadcast,
		Payload: AssembleAdjacentSiteStatusBroadcast(AdjacentSiteStatusBroadcast{RFSS: 4, Site: 8, ChannelID: 1, ChannelNumber: 300})}

	base := 0
	for _, tsbk := range []TSBK{nsb, rfss, adj} {
		cc.Process(buildLockedStreamWithTSBK(10, 0x293, DUIDTrunkingSignaling, tsbk), base)
		base += 1 << 20
	}

	cfg := cc.NetworkSnapshot()
	if cfg.WACN != 0xABCDE || cfg.RFSS != 4 || cfg.Site != 7 {
		t.Errorf("snapshot = %+v, want WACN 0xABCDE / RFSS 4 / Site 7", cfg)
	}
	if len(cfg.Neighbors) != 1 || cfg.Neighbors[0].Site != 8 {
		t.Errorf("neighbours = %v, want one site-8 entry", cfg.Neighbors)
	}
}
