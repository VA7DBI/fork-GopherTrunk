// DSC log writer — drains KindDSCMessage events off the shared bus
// and writes one row per decoded sequence to the SQLite dsc_log
// table. Mirrors aprslog.go / vessellog.go / pagerlog.go.
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

// DSCMessage is one persisted decoded DSC sequence.
type DSCMessage struct {
	ID         int64     `json:"id"`
	ReceivedAt time.Time `json:"received_at"`
	Format     string    `json:"format"`   // "distress" | "all-ships" | "individual" | "group" | ...
	Category   string    `json:"category"` // "distress" | "urgency" | "safety" | "routine"
	SelfMMSI   uint64    `json:"self_mmsi"`
	TargetMMSI uint64    `json:"target_mmsi,omitempty"`
	Nature     string    `json:"nature,omitempty"`   // distress nature ("fire", "sinking", ...)
	TimeUTC    string    `json:"time_utc,omitempty"` // HH:MM, distress only

	// Position fields — populated only on distress alerts that
	// included a position field with a non-sentinel value.
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
	HasPosition bool    `json:"has_position"`

	Body   string `json:"body"`    // type-specific summary
	RawHex string `json:"raw_hex"` // hex-encoded 7-bit symbol stream
}

// DSCLog drains KindDSCMessage events until ctx cancels or the bus
// closes.
type DSCLog struct {
	db        *DB
	bus       *events.Bus
	log       *slog.Logger
	sub       *events.Subscription
	runDone   chan struct{}
	closeOnce sync.Once
}

// NewDSCLog wires the log to the bus. Subscription happens at
// construction so events published before Run() begins aren't lost.
func NewDSCLog(db *DB, bus *events.Bus, logger *slog.Logger) (*DSCLog, error) {
	if db == nil {
		return nil, errors.New("storage/dsclog: DB is required")
	}
	if bus == nil {
		return nil, errors.New("storage/dsclog: events.Bus is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	d := &DSCLog{db: db, bus: bus, log: logger, runDone: make(chan struct{})}
	d.sub = bus.Subscribe()
	return d, nil
}

// Run drains KindDSCMessage events until ctx cancels or the bus
// closes.
func (d *DSCLog) Run(ctx context.Context) error {
	defer close(d.runDone)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-d.sub.C:
			if !ok {
				return nil
			}
			if ev.Kind != events.KindDSCMessage {
				continue
			}
			m, ok := ev.Payload.(DSCMessage)
			if !ok {
				continue
			}
			if err := d.insert(m); err != nil {
				d.log.Error("dsclog: insert failed", "err", err)
			}
		}
	}
}

func (d *DSCLog) insert(m DSCMessage) error {
	at := m.ReceivedAt
	if at.IsZero() {
		at = time.Now()
	}
	hasPos := 0
	if m.HasPosition {
		hasPos = 1
	}
	_, err := d.db.SQL().Exec(
		`INSERT INTO dsc_log
		 (received_at, format, category, self_mmsi, target_mmsi,
		  nature, time_utc, latitude, longitude, has_position,
		  body, raw_hex)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		at.UnixNano(), m.Format, m.Category, m.SelfMMSI, m.TargetMMSI,
		m.Nature, m.TimeUTC, m.Latitude, m.Longitude, hasPos,
		m.Body, m.RawHex,
	)
	return err
}

// Recent returns the most recent messages, newest first, capped at
// limit. limit ≤ 0 picks 200; limit > 5000 caps at 5000.
func (d *DSCLog) Recent(limit int) ([]DSCMessage, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := d.db.SQL().Query(
		`SELECT id, received_at, format, category, self_mmsi,
		        target_mmsi, nature, time_utc, latitude, longitude,
		        has_position, body, raw_hex
		 FROM dsc_log ORDER BY received_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("storage/dsclog: query: %w", err)
	}
	defer rows.Close()
	var out []DSCMessage
	for rows.Next() {
		var (
			m      DSCMessage
			ns     int64
			hasPos int
		)
		if err := rows.Scan(&m.ID, &ns, &m.Format, &m.Category,
			&m.SelfMMSI, &m.TargetMMSI, &m.Nature, &m.TimeUTC,
			&m.Latitude, &m.Longitude, &hasPos, &m.Body, &m.RawHex); err != nil {
			return nil, fmt.Errorf("storage/dsclog: scan: %w", err)
		}
		m.ReceivedAt = time.Unix(0, ns)
		m.HasPosition = hasPos != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

// Close releases the bus subscription and waits for Run to drain.
func (d *DSCLog) Close() error {
	d.closeOnce.Do(func() {
		d.sub.Close()
		select {
		case <-d.runDone:
		case <-time.After(time.Second):
		}
	})
	return nil
}
