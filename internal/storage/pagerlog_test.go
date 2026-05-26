package storage

import (
	"context"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

func openPagerTestStore(t *testing.T) (*PagerLog, *events.Bus, *DB) {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	bus := events.NewBus(8)
	log, err := NewPagerLog(db, bus, nil)
	if err != nil {
		t.Fatalf("NewPagerLog: %v", err)
	}
	t.Cleanup(func() {
		_ = log.Close()
		bus.Close()
		_ = db.Close()
	})
	return log, bus, db
}

func TestPagerLogInsertsBusMessage(t *testing.T) {
	log, bus, _ := openPagerTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind: events.KindPagerMessage,
		Payload: PagerMessage{
			ReceivedAt: time.Unix(1735000000, 0),
			RIC:        0x12345,
			Func:       1,
			Encoding:   "numeric",
			Body:       "12345",
			Corrected:  0,
		},
	})

	// Poll briefly for the row to land.
	var recent []PagerMessage
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		recent, _ = log.Recent(10)
		if len(recent) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(recent) != 1 {
		t.Fatalf("Recent = %d rows, want 1", len(recent))
	}
	r := recent[0]
	if r.RIC != 0x12345 || r.Func != 1 || r.Body != "12345" || r.Encoding != "numeric" {
		t.Errorf("Recent[0] = %+v", r)
	}
}

func TestPagerLogIgnoresUnrelatedEvents(t *testing.T) {
	log, bus, _ := openPagerTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	// Wrong kind — should be silently ignored.
	bus.Publish(events.Event{
		Kind:    events.KindBookmarkCreated,
		Payload: Bookmark{Name: "x"},
	})
	// Right kind, wrong payload type — also ignored.
	bus.Publish(events.Event{
		Kind:    events.KindPagerMessage,
		Payload: "not a pager message",
	})

	time.Sleep(50 * time.Millisecond)
	recent, err := log.Recent(10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(recent) != 0 {
		t.Errorf("Recent = %d, want 0 (no real pager events fired)", len(recent))
	}
}

func TestPagerLogRecentOrderNewestFirst(t *testing.T) {
	log, bus, _ := openPagerTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	for i, ts := range []time.Time{
		time.Unix(1700000000, 0),
		time.Unix(1700000100, 0),
		time.Unix(1700000050, 0),
	} {
		bus.Publish(events.Event{
			Kind: events.KindPagerMessage,
			Payload: PagerMessage{
				ReceivedAt: ts,
				RIC:        uint32(0x100 + i),
				Body:       "x",
				Encoding:   "alpha",
			},
		})
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		recent, _ := log.Recent(10)
		if len(recent) == 3 {
			// Newest first.
			if recent[0].RIC != 0x101 || recent[1].RIC != 0x102 || recent[2].RIC != 0x100 {
				t.Errorf("order = %d, %d, %d; want 0x101, 0x102, 0x100",
					recent[0].RIC, recent[1].RIC, recent[2].RIC)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("did not receive all three rows within 1s")
}
