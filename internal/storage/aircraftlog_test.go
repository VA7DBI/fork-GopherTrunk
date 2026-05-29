package storage

import (
	"context"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

func openAircraftTestStore(t *testing.T) (*AircraftLog, *events.Bus, *DB) {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	bus := events.NewBus(8)
	log, err := NewAircraftLog(db, bus, nil)
	if err != nil {
		t.Fatalf("NewAircraftLog: %v", err)
	}
	t.Cleanup(func() {
		_ = log.Close()
		bus.Close()
		_ = db.Close()
	})
	return log, bus, db
}

func TestAircraftLogInsertsPosition(t *testing.T) {
	log, bus, _ := openAircraftTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind: events.KindAircraftReport,
		Payload: AircraftReport{
			ReceivedAt:  time.Unix(1735000000, 0),
			ICAO:        0x40621D,
			ICAOHex:     "40621D",
			Kind:        "airborne-pos",
			Body:        "AIRBORNE-POS 40621D CPR-even alt=38000ft",
			CRCValid:    true,
			Latitude:    52.2572,
			Longitude:   3.91937,
			Altitude:    38000,
			HasPosition: true,
			HasAltitude: true,
			RawHex:      "8d40621d58c382d690c8ac2863a7",
		},
	})

	var recent []AircraftReport
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
	if r.ICAO != 0x40621D || r.Kind != "airborne-pos" || !r.CRCValid {
		t.Errorf("Recent[0] = %+v", r)
	}
	if !r.HasPosition || r.Latitude < 52.2 || r.Latitude > 52.3 {
		t.Errorf("position not round-tripped: %+v", r)
	}
	if r.Altitude != 38000 {
		t.Errorf("altitude = %d, want 38000", r.Altitude)
	}
}

func TestAircraftLogInsertsIdentification(t *testing.T) {
	log, bus, _ := openAircraftTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind: events.KindAircraftReport,
		Payload: AircraftReport{
			ReceivedAt: time.Unix(1735000000, 0),
			ICAO:       0x4840D6,
			ICAOHex:    "4840D6",
			Kind:       "ident",
			Body:       "IDENT 4840D6 \"KLM1023\" cat=4",
			CRCValid:   true,
			Callsign:   "KLM1023",
			Category:   4,
		},
	})

	var recent []AircraftReport
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
	if r.Callsign != "KLM1023" {
		t.Errorf("Callsign = %q, want KLM1023", r.Callsign)
	}
	if r.HasPosition {
		t.Error("HasPosition = true on identification-only frame")
	}
}

func TestAircraftLogIgnoresUnrelatedEvents(t *testing.T) {
	log, bus, _ := openAircraftTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind:    events.KindBookmarkCreated,
		Payload: Bookmark{Name: "x"},
	})
	bus.Publish(events.Event{
		Kind:    events.KindAircraftReport,
		Payload: "wrong payload type",
	})

	time.Sleep(50 * time.Millisecond)
	recent, _ := log.Recent(10)
	if len(recent) != 0 {
		t.Errorf("Recent = %d, want 0", len(recent))
	}
}

func TestAircraftLogRecentOrderNewestFirst(t *testing.T) {
	log, bus, _ := openAircraftTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	for i := uint32(0); i < 3; i++ {
		bus.Publish(events.Event{
			Kind: events.KindAircraftReport,
			Payload: AircraftReport{
				ReceivedAt: time.Unix(1735000000+int64(i)*60, 0),
				ICAO:       0x4840D0 + i,
				Kind:       "velocity",
			},
		})
	}

	var recent []AircraftReport
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
	if recent[0].ICAO != 0x4840D2 || recent[2].ICAO != 0x4840D0 {
		t.Errorf("ordering wrong")
	}
}
