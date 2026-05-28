package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	radiofleetync "github.com/MattCheramie/GopherTrunk/internal/radio/fleetync"
)

func openFleetSyncTestStore(t *testing.T) (*FleetSyncLog, *events.Bus, *DB) {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	bus := events.NewBus(8)
	log, err := NewFleetSyncLog(db, bus, nil)
	if err != nil {
		t.Fatalf("NewFleetSyncLog: %v", err)
	}
	t.Cleanup(func() {
		_ = log.Close()
		bus.Close()
		_ = db.Close()
	})
	return log, bus, db
}

func TestFleetSyncLogInsertsBusMessage(t *testing.T) {
	log, bus, _ := openFleetSyncTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	bus.Publish(events.Event{
		Kind: events.KindFleetSyncMessage,
		Payload: radiofleetync.Message{
			Timestamp:  time.Unix(1735000000, 0),
			Version:    radiofleetync.VersionFleetSync2,
			Command:    0x02,
			Subcommand: 0x80,
			FromFleet:  7,
			FromUnit:   101,
			ToFleet:    8,
			ToUnit:     202,
			Emergency:  true,
			Payload:    []byte{0x01, 0x02},
			RawBytes:   []byte{0xAA, 0xBB},
		},
	})

	var recent []FleetSyncMessage
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		recent, _ = log.List(FleetSyncFilter{Limit: 10})
		if len(recent) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(recent) != 1 {
		t.Fatalf("List = %d rows, want 1", len(recent))
	}
	if recent[0].Command != 0x02 || recent[0].FromUnit != 101 || !recent[0].Emergency {
		t.Fatalf("row = %+v", recent[0])
	}
}

func TestFleetSyncLogFiltersAndGet(t *testing.T) {
	log, bus, _ := openFleetSyncTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = log.Run(ctx) }()

	publish := func(ts time.Time, cmd uint8, src, dst uint16) {
		bus.Publish(events.Event{Kind: events.KindFleetSyncMessage, Payload: radiofleetync.Message{
			Timestamp: ts, Version: radiofleetync.VersionFleetSync1, Command: cmd,
			FromUnit: src, ToUnit: dst,
		}})
	}
	publish(time.Unix(1700000000, 0), 0x01, 100, 200)
	publish(time.Unix(1700000100, 0), 0x02, 101, 201)
	publish(time.Unix(1700000200, 0), 0x01, 102, 202)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		rows, _ := log.List(FleetSyncFilter{Limit: 10})
		if len(rows) == 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	src := uint16(101)
	rows, err := log.List(FleetSyncFilter{SourceUnit: &src, Limit: 10})
	if err != nil {
		t.Fatalf("List source filter: %v", err)
	}
	if len(rows) != 1 || rows[0].FromUnit != 101 {
		t.Fatalf("source filter rows = %+v", rows)
	}

	cmd := uint8(0x01)
	rows, err = log.List(FleetSyncFilter{Command: &cmd, Limit: 10})
	if err != nil {
		t.Fatalf("List command filter: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("command filter len = %d, want 2", len(rows))
	}

	since := time.Unix(1700000050, 0)
	rows, err = log.List(FleetSyncFilter{Since: since, Limit: 10})
	if err != nil {
		t.Fatalf("List since filter: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("since filter len = %d, want 2", len(rows))
	}

	got, err := log.Get(rows[0].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != rows[0].ID {
		t.Fatalf("Get id = %d want %d", got.ID, rows[0].ID)
	}
}

func TestFleetSyncLogGetMissing(t *testing.T) {
	log, _, _ := openFleetSyncTestStore(t)
	_, err := log.Get(999)
	if err == nil {
		t.Fatal("expected not found error")
	}
	if err != nil && !isSQLNoRows(err) {
		t.Fatalf("err = %v, want sql.ErrNoRows wrapped", err)
	}
}

func isSQLNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
