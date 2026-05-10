package phase1

import (
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

// buildLockedStream constructs a synthetic dibit window that places the
// FSW at the given offset followed by a properly BCH-encoded NID for
// the supplied NAC and DUID.
func buildLockedStream(offset int, nac uint16, duid DUID) []uint8 {
	out := make([]uint8, offset+24+32+16)
	copy(out[offset:], FrameSyncWord[:])
	bits := EncodeNIDBits(nac, duid)
	for i := 0; i < 32; i++ {
		out[offset+24+i] = (bits[2*i] << 1) | bits[2*i+1]
	}
	return out
}

func TestControlChannelEmitsLockOnTSDU(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := NewControlChannel(bus, nil, 851_000_000)
	stream := buildLockedStream(10, 0x293, DUIDTrunkingSignaling)
	cc.Process(stream, 0)

	select {
	case ev := <-sub.C:
		if ev.Kind != events.KindCCLocked {
			t.Errorf("kind = %s, want cc.locked", ev.Kind)
		}
		ls, ok := ev.Payload.(LockState)
		if !ok {
			t.Fatalf("payload type = %T, want LockState", ev.Payload)
		}
		if ls.NAC != 0x293 || ls.DUID != DUIDTrunkingSignaling {
			t.Errorf("payload = %+v", ls)
		}
	case <-time.After(time.Second):
		t.Fatal("no event published")
	}
}

func TestControlChannelIgnoresNonTSDU(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := NewControlChannel(bus, nil, 851_000_000)
	stream := buildLockedStream(10, 0x123, DUIDLogicalLink1)
	cc.Process(stream, 0)

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

	cc := NewControlChannel(bus, nil, 851_000_000)
	cc.Process(buildLockedStream(10, 0x456, DUIDTrunkingSignaling), 0)
	<-sub.C // CCLocked

	cc.MarkLost()
	select {
	case ev := <-sub.C:
		if ev.Kind != events.KindCCLost {
			t.Errorf("kind = %s, want cc.lost", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("no CCLost event")
	}
}

func TestControlChannelPublishesDecodeErrorOnUncorrectable(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	// Stream with FSW followed by 32 dibits of garbage (not a valid NID).
	stream := make([]uint8, 10+24+32+16)
	copy(stream[10:], FrameSyncWord[:])
	for i := 0; i < 32; i++ {
		stream[10+24+i] = uint8(i*7) & 0x3
	}

	cc := NewControlChannel(bus, nil, 851_000_000)
	cc.Process(stream, 0)

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindDecodeError {
				de, ok := ev.Payload.(events.DecodeError)
				if !ok {
					t.Fatalf("payload type = %T, want DecodeError", ev.Payload)
				}
				if de.Protocol != "p25" || de.Stage != "nid-bch" {
					t.Errorf("DecodeError = %+v", de)
				}
				return
			}
			// Any non-DecodeError event means the garbage somehow decoded
			// to a valid TSDU — extremely unlikely (~2^-47) but treat as
			// a wash and keep waiting.
		case <-deadline:
			t.Fatal("no decode-error event published")
		}
	}
}
