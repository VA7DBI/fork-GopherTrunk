package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	radiofleetync "github.com/MattCheramie/GopherTrunk/internal/radio/fleetync"
)

// FleetSyncMessage is one persisted FleetSync frame.
type FleetSyncMessage struct {
	ID         int64     `json:"id"`
	ReceivedAt time.Time `json:"received_at"`
	Version    uint8     `json:"version"`
	Command    uint8     `json:"command"`
	Subcommand uint8     `json:"subcommand"`
	FromFleet  uint8     `json:"from_fleet"`
	FromUnit   uint16    `json:"from_unit"`
	ToFleet    uint8     `json:"to_fleet"`
	ToUnit     uint16    `json:"to_unit"`
	AllFlag    bool      `json:"all_flag"`
	Emergency  bool      `json:"emergency"`
	Priority   bool      `json:"priority"`
	Payload    []byte    `json:"payload"`
	RawBytes   []byte    `json:"raw_bytes"`
}

// FleetSyncFilter scopes list queries over the FleetSync log.
type FleetSyncFilter struct {
	Limit           int
	Since           time.Time
	Until           time.Time
	SourceUnit      *uint16
	DestinationUnit *uint16
	Command         *uint8
}

// FleetSyncCommandStat is one command histogram bucket.
type FleetSyncCommandStat struct {
	Command uint8 `json:"command"`
	Count   int64 `json:"count"`
}

// FleetSyncStats summarizes FleetSync messages for a filter range.
type FleetSyncStats struct {
	Total     int64                  `json:"total"`
	Emergency int64                  `json:"emergency"`
	Priority  int64                  `json:"priority"`
	FirstSeen time.Time              `json:"first_seen"`
	LastSeen  time.Time              `json:"last_seen"`
	Commands  []FleetSyncCommandStat `json:"commands"`
}

// FleetSyncLog drains FleetSync events off the bus and persists them.
type FleetSyncLog struct {
	db        *DB
	bus       *events.Bus
	log       *slog.Logger
	sub       *events.Subscription
	runDone   chan struct{}
	closeOnce sync.Once
}

func NewFleetSyncLog(db *DB, bus *events.Bus, logger *slog.Logger) (*FleetSyncLog, error) {
	if db == nil {
		return nil, errors.New("storage/fleetsynclog: DB is required")
	}
	if bus == nil {
		return nil, errors.New("storage/fleetsynclog: events.Bus is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	f := &FleetSyncLog{db: db, bus: bus, log: logger, runDone: make(chan struct{})}
	f.sub = bus.Subscribe()
	return f, nil
}

func (f *FleetSyncLog) Run(ctx context.Context) error {
	defer close(f.runDone)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-f.sub.C:
			if !ok {
				return nil
			}
			if ev.Kind != events.KindFleetSyncMessage {
				continue
			}
			switch msg := ev.Payload.(type) {
			case radiofleetync.Message:
				if err := f.insert(fromRadioFleetSync(msg)); err != nil {
					f.log.Error("fleetsynclog: insert failed", "err", err)
				}
			case *radiofleetync.Message:
				if msg != nil {
					if err := f.insert(fromRadioFleetSync(*msg)); err != nil {
						f.log.Error("fleetsynclog: insert failed", "err", err)
					}
				}
			case FleetSyncMessage:
				if err := f.insert(msg); err != nil {
					f.log.Error("fleetsynclog: insert failed", "err", err)
				}
			}
		}
	}
}

func fromRadioFleetSync(m radiofleetync.Message) FleetSyncMessage {
	return FleetSyncMessage{
		ReceivedAt: m.Timestamp,
		Version:    uint8(m.Version),
		Command:    m.Command,
		Subcommand: m.Subcommand,
		FromFleet:  m.FromFleet,
		FromUnit:   m.FromUnit,
		ToFleet:    m.ToFleet,
		ToUnit:     m.ToUnit,
		AllFlag:    m.AllFlag,
		Emergency:  m.Emergency,
		Priority:   m.Priority,
		Payload:    append([]byte(nil), m.Payload...),
		RawBytes:   append([]byte(nil), m.RawBytes...),
	}
}

func (f *FleetSyncLog) insert(m FleetSyncMessage) error {
	at := m.ReceivedAt
	if at.IsZero() {
		at = time.Now()
	}
	payload := m.Payload
	if payload == nil {
		payload = []byte{}
	}
	rawBytes := m.RawBytes
	if rawBytes == nil {
		rawBytes = []byte{}
	}
	_, err := f.db.SQL().Exec(
		`INSERT INTO fleetsync_log
		 (received_at, version, command, subcommand, from_fleet, from_unit, to_fleet, to_unit,
		  all_flag, emergency, priority, payload, raw_bytes)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		at.UnixNano(), m.Version, m.Command, m.Subcommand, m.FromFleet, m.FromUnit,
		m.ToFleet, m.ToUnit, boolToInt(m.AllFlag), boolToInt(m.Emergency), boolToInt(m.Priority),
		payload, rawBytes,
	)
	return err
}

func (f *FleetSyncLog) List(filter FleetSyncFilter) ([]FleetSyncMessage, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 5000 {
		limit = 5000
	}

	query := strings.Builder{}
	query.WriteString(`SELECT id, received_at, version, command, subcommand, from_fleet, from_unit, to_fleet, to_unit,
		all_flag, emergency, priority, payload, raw_bytes FROM fleetsync_log`)
	whereSQL, args := buildFleetSyncWhere(filter)
	query.WriteString(whereSQL)
	query.WriteString(" ORDER BY received_at DESC LIMIT ?")
	args = append(args, limit)

	rows, err := f.db.SQL().Query(query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("storage/fleetsynclog: query: %w", err)
	}
	defer rows.Close()

	out := []FleetSyncMessage{}
	for rows.Next() {
		msg, err := scanFleetSyncMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

// Stats computes aggregate FleetSync statistics and a command histogram
// over the requested filter window.
func (f *FleetSyncLog) Stats(filter FleetSyncFilter) (FleetSyncStats, error) {
	whereSQL, args := buildFleetSyncWhere(filter)

	query := `SELECT COUNT(*), COALESCE(SUM(emergency), 0), COALESCE(SUM(priority), 0), MIN(received_at), MAX(received_at) FROM fleetsync_log` + whereSQL
	row := f.db.SQL().QueryRow(query, args...)
	var (
		stats               FleetSyncStats
		firstSeen, lastSeen sql.NullInt64
	)
	if err := row.Scan(&stats.Total, &stats.Emergency, &stats.Priority, &firstSeen, &lastSeen); err != nil {
		return FleetSyncStats{}, fmt.Errorf("storage/fleetsynclog: stats scan: %w", err)
	}
	if firstSeen.Valid {
		stats.FirstSeen = time.Unix(0, firstSeen.Int64)
	}
	if lastSeen.Valid {
		stats.LastSeen = time.Unix(0, lastSeen.Int64)
	}

	rows, err := f.db.SQL().Query(`SELECT command, COUNT(*) FROM fleetsync_log`+whereSQL+` GROUP BY command ORDER BY command`, args...)
	if err != nil {
		return FleetSyncStats{}, fmt.Errorf("storage/fleetsynclog: stats commands: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cmd uint8
			cnt int64
		)
		if err := rows.Scan(&cmd, &cnt); err != nil {
			return FleetSyncStats{}, fmt.Errorf("storage/fleetsynclog: stats commands scan: %w", err)
		}
		stats.Commands = append(stats.Commands, FleetSyncCommandStat{Command: cmd, Count: cnt})
	}
	if err := rows.Err(); err != nil {
		return FleetSyncStats{}, fmt.Errorf("storage/fleetsynclog: stats commands rows: %w", err)
	}
	return stats, nil
}

func (f *FleetSyncLog) Get(id int64) (FleetSyncMessage, error) {
	row := f.db.SQL().QueryRow(
		`SELECT id, received_at, version, command, subcommand, from_fleet, from_unit, to_fleet, to_unit,
		 all_flag, emergency, priority, payload, raw_bytes
		 FROM fleetsync_log WHERE id = ?`, id)
	msg, err := scanFleetSyncMessage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FleetSyncMessage{}, err
		}
		return FleetSyncMessage{}, err
	}
	return msg, nil
}

type fleetSyncScanner interface {
	Scan(dest ...any) error
}

func scanFleetSyncMessage(scanner fleetSyncScanner) (FleetSyncMessage, error) {
	var (
		msg                          FleetSyncMessage
		receivedAt                   int64
		version, command, subcommand int64
		fromFleet, fromUnit          int64
		toFleet, toUnit              int64
		allFlag, emergency, priority int64
	)
	if err := scanner.Scan(
		&msg.ID, &receivedAt, &version, &command, &subcommand, &fromFleet, &fromUnit, &toFleet, &toUnit,
		&allFlag, &emergency, &priority, &msg.Payload, &msg.RawBytes,
	); err != nil {
		return FleetSyncMessage{}, fmt.Errorf("storage/fleetsynclog: scan: %w", err)
	}
	msg.ReceivedAt = time.Unix(0, receivedAt)
	msg.Version = uint8(version)
	msg.Command = uint8(command)
	msg.Subcommand = uint8(subcommand)
	msg.FromFleet = uint8(fromFleet)
	msg.FromUnit = uint16(fromUnit)
	msg.ToFleet = uint8(toFleet)
	msg.ToUnit = uint16(toUnit)
	msg.AllFlag = allFlag != 0
	msg.Emergency = emergency != 0
	msg.Priority = priority != 0
	return msg, nil
}

func (f *FleetSyncLog) Close() error {
	f.closeOnce.Do(func() {
		f.sub.Close()
		select {
		case <-f.runDone:
		case <-time.After(time.Second):
		}
	})
	return nil
}

func buildFleetSyncWhere(filter FleetSyncFilter) (string, []any) {
	clauses := make([]string, 0, 5)
	args := make([]any, 0, 5)
	if filter.SourceUnit != nil {
		clauses = append(clauses, "from_unit = ?")
		args = append(args, *filter.SourceUnit)
	}
	if filter.DestinationUnit != nil {
		clauses = append(clauses, "to_unit = ?")
		args = append(args, *filter.DestinationUnit)
	}
	if filter.Command != nil {
		clauses = append(clauses, "command = ?")
		args = append(args, *filter.Command)
	}
	if !filter.Since.IsZero() {
		clauses = append(clauses, "received_at >= ?")
		args = append(args, filter.Since.UnixNano())
	}
	if !filter.Until.IsZero() {
		clauses = append(clauses, "received_at <= ?")
		args = append(args, filter.Until.UnixNano())
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}
