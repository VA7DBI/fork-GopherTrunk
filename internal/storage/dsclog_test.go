package storage

import (
	"context"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

func openDSCTestStore(t *testing.T) (*DSCLog, *events.Bus, *DB) {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	bus := events.NewBus(8)
	log, err := NewDSCLog(db, bus, nil)
	if err != nil {
		t.Fatalf("NewDSCLog: %v", err)
	}
	t.Cleanup(func() {
		_ = log.Close()
		bus.Close()
		_ = db.Close()
	})
	return log, bus, db
}

func TestDSCLogInsertsDistress(t *testing.T) {
	log, bus, _ := openDSCTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind: events.KindDSCMessage,
		Payload: DSCMessage{
			ReceivedAt:  time.Unix(1735000000, 0),
			Format:      "distress",
			Category:    "distress",
			SelfMMSI:    366053209,
			Nature:      "fire / explosion",
			TimeUTC:     "14:25",
			Latitude:    37.8,
			Longitude:   122.4,
			HasPosition: true,
			Body:        "DISTRESS MMSI=366053209 fire 37.80,122.40",
			RawHex:      "deadbeef",
		},
	})

	var recent []DSCMessage
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		recent, _ = log.Recent(10)
		if len(recent) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(recent) != 1 {
		t.Fatalf("Recent = %d, want 1", len(recent))
	}
	r := recent[0]
	if r.Format != "distress" || r.SelfMMSI != 366053209 ||
		r.Nature != "fire / explosion" || r.TimeUTC != "14:25" {
		t.Errorf("Recent[0] = %+v", r)
	}
	if !r.HasPosition || r.Latitude < 37.7 || r.Latitude > 37.9 {
		t.Errorf("position not round-tripped: %+v", r)
	}
}

func TestDSCLogInsertsIndividualCall(t *testing.T) {
	log, bus, _ := openDSCTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind: events.KindDSCMessage,
		Payload: DSCMessage{
			ReceivedAt: time.Unix(1735000000, 0),
			Format:     "individual",
			Category:   "routine",
			SelfMMSI:   366053209,
			TargetMMSI: 3660000,
			Body:       "INDIVIDUAL 366053209 → 003660000 routine",
		},
	})

	var recent []DSCMessage
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		recent, _ = log.Recent(10)
		if len(recent) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(recent) != 1 {
		t.Fatalf("Recent = %d, want 1", len(recent))
	}
	r := recent[0]
	if r.Format != "individual" || r.TargetMMSI != 3660000 {
		t.Errorf("Recent[0] = %+v", r)
	}
	if r.HasPosition {
		t.Error("HasPosition = true on non-distress call")
	}
}

func TestDSCLogIgnoresUnrelatedEvents(t *testing.T) {
	log, bus, _ := openDSCTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind:    events.KindBookmarkCreated,
		Payload: Bookmark{Name: "x"},
	})
	bus.Publish(events.Event{
		Kind:    events.KindDSCMessage,
		Payload: "wrong payload type",
	})

	time.Sleep(50 * time.Millisecond)
	recent, _ := log.Recent(10)
	if len(recent) != 0 {
		t.Errorf("Recent = %d, want 0", len(recent))
	}
}

func TestDSCLogRecentOrderNewestFirst(t *testing.T) {
	log, bus, _ := openDSCTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	for i := uint64(0); i < 3; i++ {
		bus.Publish(events.Event{
			Kind: events.KindDSCMessage,
			Payload: DSCMessage{
				ReceivedAt: time.Unix(1735000000+int64(i)*60, 0),
				Format:     "all-ships",
				Category:   "safety",
				SelfMMSI:   3660000000 + i,
			},
		})
	}
	var recent []DSCMessage
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		recent, _ = log.Recent(10)
		if len(recent) == 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(recent) != 3 {
		t.Fatalf("Recent = %d, want 3", len(recent))
	}
	if recent[0].SelfMMSI != 3660000002 || recent[2].SelfMMSI != 3660000000 {
		t.Errorf("ordering wrong")
	}
}
