// MDC1200 log writer — drains KindMDC1200Message events off the
// shared bus and writes one row per decoded signaling burst to the
// SQLite mdc1200_log table. Mirrors dsclog.go / aprslog.go.
package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

// MDC1200Message is one persisted decoded MDC1200 burst.
type MDC1200Message struct {
	ID         int64     `json:"id"`
	ReceivedAt time.Time `json:"received_at"`
	Op         uint8     `json:"op"`
	Arg        uint8     `json:"arg"`
	UnitID     uint16    `json:"unit_id"`
	Operation  string    `json:"operation"` // "PTT ID" | "Emergency" | ... ("" if unknown)
	Body       string    `json:"body"`
	RawHex     string    `json:"raw_hex"`
	CRCOK      bool      `json:"crc_ok"`
}

// MDC1200Log drains KindMDC1200Message events until ctx cancels or the
// bus closes.
type MDC1200Log struct {
	db        *DB
	bus       *events.Bus
	log       *slog.Logger
	sub       *events.Subscription
	runDone   chan struct{}
	closeOnce sync.Once
}

// NewMDC1200Log wires the log to the bus. Subscription happens at
// construction so events published before Run() begins aren't lost.
func NewMDC1200Log(db *DB, bus *events.Bus, logger *slog.Logger) (*MDC1200Log, error) {
	if db == nil {
		return nil, errors.New("storage/mdc1200log: DB is required")
	}
	if bus == nil {
		return nil, errors.New("storage/mdc1200log: events.Bus is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	m := &MDC1200Log{db: db, bus: bus, log: logger, runDone: make(chan struct{})}
	m.sub = bus.Subscribe()
	return m, nil
}

// Run drains KindMDC1200Message events until ctx cancels or the bus
// closes.
func (m *MDC1200Log) Run(ctx context.Context) error {
	defer close(m.runDone)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-m.sub.C:
			if !ok {
				return nil
			}
			if ev.Kind != events.KindMDC1200Message {
				continue
			}
			msg, ok := ev.Payload.(MDC1200Message)
			if !ok {
				continue
			}
			if err := m.insert(msg); err != nil {
				m.log.Error("mdc1200log: insert failed", "err", err)
			}
		}
	}
}

func (m *MDC1200Log) insert(msg MDC1200Message) error {
	at := msg.ReceivedAt
	if at.IsZero() {
		at = time.Now()
	}
	crcOK := 0
	if msg.CRCOK {
		crcOK = 1
	}
	_, err := m.db.SQL().Exec(
		`INSERT INTO mdc1200_log
		 (received_at, op, arg, unit_id, operation, body, raw_hex, crc_ok)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		at.UnixNano(), msg.Op, msg.Arg, msg.UnitID, msg.Operation,
		msg.Body, msg.RawHex, crcOK,
	)
	return err
}

// Recent returns the most recent bursts, newest first, capped at
// limit. limit ≤ 0 picks 200; limit > 5000 caps at 5000.
func (m *MDC1200Log) Recent(limit int) ([]MDC1200Message, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := m.db.SQL().Query(
		`SELECT id, received_at, op, arg, unit_id, operation, body,
		        raw_hex, crc_ok
		 FROM mdc1200_log ORDER BY received_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("storage/mdc1200log: query: %w", err)
	}
	defer rows.Close()
	var out []MDC1200Message
	for rows.Next() {
		var (
			msg   MDC1200Message
			ns    int64
			op    int
			arg   int
			unit  int
			crcOK int
		)
		if err := rows.Scan(&msg.ID, &ns, &op, &arg, &unit, &msg.Operation,
			&msg.Body, &msg.RawHex, &crcOK); err != nil {
			return nil, fmt.Errorf("storage/mdc1200log: scan: %w", err)
		}
		msg.ReceivedAt = time.Unix(0, ns)
		msg.Op = uint8(op)
		msg.Arg = uint8(arg)
		msg.UnitID = uint16(unit)
		msg.CRCOK = crcOK != 0
		out = append(out, msg)
	}
	return out, rows.Err()
}

// Close releases the bus subscription and waits for Run to drain.
func (m *MDC1200Log) Close() error {
	m.closeOnce.Do(func() {
		m.sub.Close()
		select {
		case <-m.runDone:
		case <-time.After(time.Second):
		}
	})
	return nil
}
