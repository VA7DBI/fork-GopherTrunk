package storage

import (
	"context"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

func openMDC1200TestStore(t *testing.T) (*MDC1200Log, *events.Bus, *DB) {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	bus := events.NewBus(8)
	log, err := NewMDC1200Log(db, bus, nil)
	if err != nil {
		t.Fatalf("NewMDC1200Log: %v", err)
	}
	t.Cleanup(func() {
		_ = log.Close()
		bus.Close()
		_ = db.Close()
	})
	return log, bus, db
}

func TestMDC1200LogInsertsBurst(t *testing.T) {
	log, bus, _ := openMDC1200TestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind: events.KindMDC1200Message,
		Payload: MDC1200Message{
			ReceivedAt: time.Unix(1735000000, 0),
			Op:         0x01,
			Arg:        0x80,
			UnitID:     0x1234,
			Operation:  "PTT ID",
			Body:       "Unit 1234: PTT ID",
			RawHex:     "018012340000",
			CRCOK:      true,
		},
	})

	var recent []MDC1200Message
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
	if r.Op != 0x01 || r.Arg != 0x80 || r.UnitID != 0x1234 {
		t.Errorf("header not round-tripped: %+v", r)
	}
	if r.Operation != "PTT ID" || !r.CRCOK {
		t.Errorf("Recent[0] = %+v", r)
	}
}

func TestMDC1200LogIgnoresUnrelatedEvents(t *testing.T) {
	log, bus, _ := openMDC1200TestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{Kind: events.KindBookmarkCreated, Payload: Bookmark{Name: "x"}})
	bus.Publish(events.Event{Kind: events.KindMDC1200Message, Payload: "wrong type"})

	time.Sleep(50 * time.Millisecond)
	recent, _ := log.Recent(10)
	if len(recent) != 0 {
		t.Errorf("Recent = %d, want 0", len(recent))
	}
}

func TestMDC1200LogRecentOrderNewestFirst(t *testing.T) {
	log, bus, _ := openMDC1200TestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	for i := 0; i < 3; i++ {
		bus.Publish(events.Event{
			Kind: events.KindMDC1200Message,
			Payload: MDC1200Message{
				ReceivedAt: time.Unix(1735000000+int64(i)*60, 0),
				Op:         0x01,
				UnitID:     uint16(0x1000 + i),
			},
		})
	}
	var recent []MDC1200Message
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
	if recent[0].UnitID != 0x1002 || recent[2].UnitID != 0x1000 {
		t.Errorf("ordering wrong: %+v", recent)
	}
}
