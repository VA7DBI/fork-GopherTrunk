package storage

import (
	"context"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

func openVesselTestStore(t *testing.T) (*VesselLog, *events.Bus, *DB) {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	bus := events.NewBus(8)
	log, err := NewVesselLog(db, bus, nil)
	if err != nil {
		t.Fatalf("NewVesselLog: %v", err)
	}
	t.Cleanup(func() {
		_ = log.Close()
		bus.Close()
		_ = db.Close()
	})
	return log, bus, db
}

func TestVesselLogInsertsBusPosition(t *testing.T) {
	log, bus, _ := openVesselTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind: events.KindAISMessage,
		Payload: AISMessage{
			ReceivedAt:       time.Unix(1735000000, 0),
			MMSI:             366053209,
			Type:             "position-a",
			Body:             "CLASS-A MMSI=366053209 37.8021,-122.3416 SOG=0.0 COG=51.0",
			Latitude:         37.8021,
			Longitude:        -122.3416,
			SpeedOverGround:  0.0,
			CourseOverGround: 51.0,
			Heading:          511,
			HasPosition:      true,
			RawHex:           "deadbeef",
			FCSOK:            true,
		},
	})

	var recent []AISMessage
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
	if r.MMSI != 366053209 || r.Type != "position-a" || !r.FCSOK || !r.HasPosition {
		t.Errorf("Recent[0] = %+v", r)
	}
	if r.Latitude < 37.7 || r.Latitude > 37.9 {
		t.Errorf("latitude = %f", r.Latitude)
	}
}

func TestVesselLogInsertsStaticData(t *testing.T) {
	log, bus, _ := openVesselTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind: events.KindAISMessage,
		Payload: AISMessage{
			ReceivedAt:  time.Unix(1735000000, 0),
			MMSI:        366053209,
			Type:        "static-voyage",
			Body:        "STATIC MMSI=366053209 name=\"NAUTICAL LIMITS\" dest=\"SF BAY\" type=70",
			VesselName:  "NAUTICAL LIMITS",
			Callsign:    "WCB1234",
			Destination: "SF BAY",
			ShipType:    70,
			IMO:         9123456,
			RawHex:      "abc123",
			FCSOK:       true,
		},
	})

	var recent []AISMessage
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
	if r.VesselName != "NAUTICAL LIMITS" || r.Callsign != "WCB1234" || r.Destination != "SF BAY" {
		t.Errorf("static fields = name=%q callsign=%q dest=%q",
			r.VesselName, r.Callsign, r.Destination)
	}
	if r.ShipType != 70 || r.IMO != 9123456 {
		t.Errorf("ship_type = %d imo = %d", r.ShipType, r.IMO)
	}
	if r.HasPosition {
		t.Error("HasPosition = true on static-only message")
	}
}

func TestVesselLogIgnoresUnrelatedEvents(t *testing.T) {
	log, bus, _ := openVesselTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind:    events.KindBookmarkCreated,
		Payload: Bookmark{Name: "x"},
	})
	bus.Publish(events.Event{
		Kind:    events.KindAISMessage,
		Payload: "wrong payload type",
	})

	time.Sleep(50 * time.Millisecond)
	recent, _ := log.Recent(10)
	if len(recent) != 0 {
		t.Errorf("Recent = %d, want 0", len(recent))
	}
}

func TestVesselLogRecentOrderNewestFirst(t *testing.T) {
	log, bus, _ := openVesselTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	for i := uint32(0); i < 3; i++ {
		bus.Publish(events.Event{
			Kind: events.KindAISMessage,
			Payload: AISMessage{
				ReceivedAt: time.Unix(1735000000+int64(i)*60, 0),
				MMSI:       100000000 + i,
				Type:       "position-a",
			},
		})
	}

	var recent []AISMessage
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
	if recent[0].MMSI != 100000002 || recent[2].MMSI != 100000000 {
		t.Errorf("ordering wrong: %v",
			[]uint32{recent[0].MMSI, recent[1].MMSI, recent[2].MMSI})
	}
}
