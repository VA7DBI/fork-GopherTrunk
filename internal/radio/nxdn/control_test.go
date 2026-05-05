package nxdn

import (
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

func TestControlChannelEmitsLockOnSiteInfo(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := NewControlChannel(bus, nil, 467_000_000, Rate9600)
	lich := ParseLICH(AssembleLICH(LICH{RFCh: RFChControl, FCT: FCTNSACCH, Direction: DirectionOutbound}))
	cac := &CACMessage{Type: RCCHSITEINFO, Payload: [8]byte{0x12, 0x34, 0x05, 0x06, 0xCA, 0xFE, 0, 0}}
	cc.IngestFrame(lich, cac)

	select {
	case ev := <-sub.C:
		ls := ev.Payload.(LockState)
		if ls.SystemID != 0xCAFE || ls.SiteID != 0x0506 {
			t.Errorf("payload = %+v", ls)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
}

func TestControlChannelIgnoresTrafficLICH(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := NewControlChannel(bus, nil, 467_000_000, Rate9600)
	lich := ParseLICH(AssembleLICH(LICH{RFCh: RFChTraffic, FCT: FCTNUDCH}))
	cac := &CACMessage{Type: RCCHSITEINFO, Payload: [8]byte{}}
	cc.IngestFrame(lich, cac)

	select {
	case ev := <-sub.C:
		t.Errorf("unexpected event: %s", ev.Kind)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestControlChannelIgnoresBadParity(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := NewControlChannel(bus, nil, 467_000_000, Rate9600)
	lich := LICH{RFCh: RFChControl, ParityOK: false}
	cac := &CACMessage{Type: RCCHSITEINFO}
	cc.IngestFrame(lich, cac)

	select {
	case ev := <-sub.C:
		t.Errorf("unexpected event: %s", ev.Kind)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestControlChannelMarkLost(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := NewControlChannel(bus, nil, 467_000_000, Rate4800)
	lich := ParseLICH(AssembleLICH(LICH{RFCh: RFChControl}))
	cc.IngestFrame(lich, &CACMessage{Type: RCCHCCH})
	<-sub.C // CCLocked

	cc.MarkLost()
	select {
	case ev := <-sub.C:
		if ev.Kind != events.KindCCLost {
			t.Errorf("kind = %s, want cc.lost", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("no cc.lost")
	}
}
