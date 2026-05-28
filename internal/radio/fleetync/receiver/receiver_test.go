package receiver

import (
	"context"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/radio/fleetync"
)

func TestNew(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()

	_, err := New(Options{})
	if err == nil {
		t.Fatal("expected error for missing options")
	}

	r, err := New(Options{InputRateHz: 2_400_000, Bus: bus, Version: "fleetsync2"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r == nil {
		t.Fatal("receiver is nil")
	}
}

func TestProcessNilInput(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	r, err := New(Options{InputRateHz: 2_400_000, Bus: bus})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = r.Process(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil input channel")
	}
}

func TestProcessClosedInput(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	r, err := New(Options{InputRateHz: 2_400_000, Bus: bus})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	in := make(chan []complex64)
	close(in)
	if err := r.Process(context.Background(), in); err != nil {
		t.Fatalf("Process on closed channel: %v", err)
	}
}

func TestProcessContextCancel(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	r, err := New(Options{InputRateHz: 2_400_000, Bus: bus})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan []complex64)
	cancel()
	err = r.Process(ctx, in)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestFloatToU8Clamp(t *testing.T) {
	if got := floatToU8(-5.0); got != 0 {
		t.Fatalf("floatToU8(-5)=%d want 0", got)
	}
	if got := floatToU8(5.0); got != 255 {
		t.Fatalf("floatToU8(5)=%d want 255", got)
	}
	mid := floatToU8(0)
	if mid < 120 || mid > 136 {
		t.Fatalf("floatToU8(0)=%d out of expected mid range", mid)
	}
}

func TestParseVersion(t *testing.T) {
	if got := parseVersion("fleetsync2"); got != 2 {
		t.Fatalf("parseVersion fleetsync2=%d want 2", got)
	}
	if got := parseVersion("auto"); got != 1 {
		t.Fatalf("parseVersion auto=%d want 1", got)
	}
	if got := parseVersion("bogus"); got != 1 {
		t.Fatalf("parseVersion bogus=%d want 1", got)
	}
}

func TestPublishEvent(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	r, err := New(Options{InputRateHz: 2_400_000, Bus: bus, SourceName: "utilities-east"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sub := bus.Subscribe()
	defer sub.Close()

	r.publish(&dummyMsg)
	select {
	case ev := <-sub.C:
		if ev.Kind != events.KindFleetSyncMessage {
			t.Fatalf("kind=%s want %s", ev.Kind, events.KindFleetSyncMessage)
		}
		msg, ok := ev.Payload.(fleetync.Message)
		if !ok {
			t.Fatalf("payload type=%T want fleetync.Message", ev.Payload)
		}
		if msg.Source != "utilities-east" {
			t.Fatalf("source=%q want %q", msg.Source, "utilities-east")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for FleetSync event")
	}
	if r.MessagesEmitted() != 1 {
		t.Fatalf("MessagesEmitted=%d want 1", r.MessagesEmitted())
	}
}

var dummyMsg = fleetync.Message{Timestamp: time.Now(), Version: fleetync.VersionFleetSync1}
