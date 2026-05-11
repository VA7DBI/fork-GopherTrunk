package ltr

import (
	"log/slog"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/trunking"
)

// TestProcessLocksOnFirstStatusWord: build a bit stream of three
// back-to-back LTR Status words, run Process, and assert the
// state machine publishes a KindCCLocked carrying the first
// word's Area + Home.
func TestProcessLocksOnFirstStatusWord(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := New(Options{
		Bus:         bus,
		Log:         slog.Default(),
		SystemName:  "Sys",
		FrequencyHz: 935_012_500,
	})

	// Three idle frames in a row (Group=false, GroupID=0).
	idle := Status{Sync: true, Area: 7, Channel: 3, Home: 4, Free: 5}
	stream := append([]byte{}, StatusBits(idle)...)
	stream = append(stream, StatusBits(idle)...)
	stream = append(stream, StatusBits(idle)...)

	cc.Process(stream, 0)

	var lockState LockState
	var sawLock bool
	for !sawLock {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindCCLocked {
				lockState, _ = ev.Payload.(LockState)
				sawLock = true
			}
		default:
			t.Fatalf("Process did not publish a KindCCLocked")
		}
	}
	if lockState.FrequencyHz != 935_012_500 {
		t.Errorf("LockState.FrequencyHz = %d, want 935012500", lockState.FrequencyHz)
	}
	if lockState.Area != 7 {
		t.Errorf("LockState.Area = %d, want 7", lockState.Area)
	}
	if lockState.Repeater != 4 {
		t.Errorf("LockState.Repeater = %d, want 4", lockState.Repeater)
	}
}

// TestProcessPublishesGrantOnActiveStatus: an active Status word
// (Group=true, GroupID != 0) must trigger a KindGrant on the bus.
func TestProcessPublishesGrantOnActiveStatus(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := New(Options{
		Bus:         bus,
		Log:         slog.Default(),
		SystemName:  "Sys",
		FrequencyHz: 935_012_500,
	})

	active := Status{
		Sync:    true,
		Area:    7,
		Group:   true,
		Channel: 3,
		Home:    4,
		GroupID: 0x42,
	}
	stream := append([]byte{}, StatusBits(active)...)
	stream = append(stream, StatusBits(active)...) // dedup test
	cc.Process(stream, 0)

	grants := 0
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindGrant {
				g, ok := ev.Payload.(trunking.Grant)
				if !ok {
					t.Fatalf("Grant payload type = %T", ev.Payload)
				}
				if g.System != "Sys" {
					t.Errorf("Grant.System = %q", g.System)
				}
				if g.Protocol != "ltr" {
					t.Errorf("Grant.Protocol = %q, want ltr", g.Protocol)
				}
				if g.GroupID != 0x42 {
					t.Errorf("Grant.GroupID = %#x", g.GroupID)
				}
				grants++
			}
		default:
			if grants == 0 {
				t.Errorf("Process did not publish a KindGrant for an active Status")
			}
			if grants > 1 {
				t.Errorf("Process published %d Grants for one call; activeGroup dedup broke", grants)
			}
			return
		}
	}
}

// TestProcessManchesterStrictDecodesEncodedStream: when the
// receiver is configured for Manchester decoding, an input bit
// stream that's been Manchester-encoded must still drive
// KindCCLocked once the adapter decodes it.
func TestProcessManchesterStrictDecodesEncodedStream(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := New(Options{Bus: bus, Log: slog.Default(), SystemName: "Sys"})
	cc.SetManchesterMode(ManchesterStrict)

	idle := Status{Sync: true, Area: 5, Channel: 2, Home: 3}
	bits := StatusBits(idle)
	encoded := framingManchesterEncode(bits)
	cc.Process(encoded, 0)

	var sawLock bool
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindCCLocked {
				sawLock = true
			}
		default:
			if !sawLock {
				t.Errorf("Process did not publish a KindCCLocked after Manchester decode")
			}
			return
		}
	}
}

// framingManchesterEncode is a tiny local wrapper around
// framing.ManchesterEncode so this test file doesn't grow a wider
// framing import that the other tests don't need.
func framingManchesterEncode(in []byte) []byte {
	out := make([]byte, 2*len(in))
	for i, b := range in {
		if b&1 != 0 {
			out[2*i] = 1
			out[2*i+1] = 0
		} else {
			out[2*i] = 0
			out[2*i+1] = 1
		}
	}
	return out
}

// TestProcessHandlesFrameSpanningCalls: a Status word that
// straddles a chunk boundary still drives Ingest after the second
// Process call delivers the trailing bits.
func TestProcessHandlesFrameSpanningCalls(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := New(Options{Bus: bus, Log: slog.Default(), SystemName: "Sys"})

	idle := Status{Sync: true, Area: 1, Channel: 2, Home: 3}
	bits := StatusBits(idle)

	// Split the 41-bit frame at offset 20.
	cc.Process(bits[:20], 0)
	cc.Process(bits[20:], 20)

	var sawLock bool
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindCCLocked {
				sawLock = true
			}
		default:
			if !sawLock {
				t.Errorf("Process did not publish a KindCCLocked across the chunk boundary")
			}
			return
		}
	}
}
