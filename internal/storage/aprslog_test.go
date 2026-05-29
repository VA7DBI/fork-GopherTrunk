package storage

import (
	"context"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

func openAPRSTestStore(t *testing.T) (*APRSLog, *events.Bus, *DB) {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	bus := events.NewBus(8)
	log, err := NewAPRSLog(db, bus, nil)
	if err != nil {
		t.Fatalf("NewAPRSLog: %v", err)
	}
	t.Cleanup(func() {
		_ = log.Close()
		bus.Close()
		_ = db.Close()
	})
	return log, bus, db
}

func TestAPRSLogInsertsBusPacket(t *testing.T) {
	log, bus, _ := openAPRSTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind: events.KindAPRSPacket,
		Payload: APRSPacket{
			ReceivedAt: time.Unix(1735000000, 0),
			Src:        "W1AW-9",
			Dst:        "APRS",
			Path:       "WIDE1-1,WIDE2-1*",
			Type:       "position",
			Body:       "49.0583,-72.0292 Test",
			Latitude:   49.0583,
			Longitude:  -72.0292,
			RawInfo:    "!4903.50N/07201.75W-Test",
			FCSOK:      true,
		},
	})

	var recent []APRSPacket
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
	if r.Src != "W1AW-9" || r.Type != "position" || !r.FCSOK {
		t.Errorf("Recent[0] = %+v", r)
	}
	if r.Latitude < 49 || r.Latitude > 50 {
		t.Errorf("latitude = %f", r.Latitude)
	}
}

func TestAPRSLogIgnoresUnrelatedEvents(t *testing.T) {
	log, bus, _ := openAPRSTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind:    events.KindBookmarkCreated,
		Payload: Bookmark{Name: "x"},
	})
	bus.Publish(events.Event{
		Kind:    events.KindAPRSPacket,
		Payload: "wrong payload type",
	})

	time.Sleep(50 * time.Millisecond)
	recent, _ := log.Recent(10)
	if len(recent) != 0 {
		t.Errorf("Recent = %d, want 0 (no valid APRS events fired)", len(recent))
	}
}

func TestAPRSLogRecentOrderNewestFirst(t *testing.T) {
	log, bus, _ := openAPRSTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	for i, ts := range []time.Time{
		time.Unix(1700000000, 0),
		time.Unix(1700000100, 0),
		time.Unix(1700000050, 0),
	} {
		bus.Publish(events.Event{
			Kind: events.KindAPRSPacket,
			Payload: APRSPacket{
				ReceivedAt: ts,
				Src:        "TEST",
				Dst:        "APRS",
				Type:       "status",
				Body:       "row-" + string(rune('0'+i)),
			},
		})
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		recent, _ := log.Recent(10)
		if len(recent) == 3 {
			if recent[0].Body != "row-1" || recent[1].Body != "row-2" || recent[2].Body != "row-0" {
				t.Errorf("order = %q %q %q", recent[0].Body, recent[1].Body, recent[2].Body)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("did not receive all three rows within 1s")
}
