// Bookmark storage — operator-managed conventional channel bookmarks.
// Persisted to SQLite alongside the call log and location log so they
// survive a daemon restart and back up with the rest of the daemon
// state.
//
// The store is decoupled from the SDR pool and the scanner — a
// bookmark is plain metadata (frequency + display name + mode +
// notes); the spectrum panel uses it to render click-to-tune markers,
// the conventional scanner uses it as an alternative to the YAML
// channel list. Mutations are bus-published so subscribed surfaces
// (web SPA, TUI) can re-render without polling.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

// Bookmark is one row in the bookmarks table.
type Bookmark struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	FreqHz    uint32    `json:"freq_hz"`
	Mode      string    `json:"mode"` // "FM", "NFM", "AM", "USB", "LSB", "CW", "DMR", ...
	CTCSSHz   float64   `json:"ctcss_hz,omitempty"`
	DCSCode   uint16    `json:"dcs_code,omitempty"`
	Notes     string    `json:"notes,omitempty"`
	Group     string    `json:"group,omitempty"` // operator-defined category
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BookmarkStore is the CRUD layer over the bookmarks table.
type BookmarkStore struct {
	db  *DB
	bus *events.Bus
}

// NewBookmarkStore returns a store backed by the given DB. The bus is
// optional — when nil, no mutation events are published (useful for
// tests). When non-nil, every Create / Update / Delete publishes a
// bookmark.{created,updated,deleted} event so UI surfaces refresh
// without polling.
func NewBookmarkStore(db *DB, bus *events.Bus) (*BookmarkStore, error) {
	if db == nil {
		return nil, errors.New("storage/bookmarks: DB is required")
	}
	return &BookmarkStore{db: db, bus: bus}, nil
}

// Create inserts a bookmark and returns it with ID + timestamps
// populated. Name is required; mode defaults to "FM" when empty.
func (s *BookmarkStore) Create(ctx context.Context, b Bookmark) (Bookmark, error) {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return Bookmark{}, errors.New("storage/bookmarks: name is required")
	}
	if b.FreqHz == 0 {
		return Bookmark{}, errors.New("storage/bookmarks: freq_hz is required")
	}
	if b.Mode == "" {
		b.Mode = "FM"
	}
	now := time.Now()
	res, err := s.db.SQL().ExecContext(ctx,
		`INSERT INTO bookmarks
		 (name, freq_hz, mode, ctcss_hz, dcs_code, notes, grouping, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.Name, b.FreqHz, b.Mode, b.CTCSSHz, b.DCSCode, b.Notes, b.Group,
		now.UnixNano(), now.UnixNano())
	if err != nil {
		return Bookmark{}, fmt.Errorf("storage/bookmarks: insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Bookmark{}, fmt.Errorf("storage/bookmarks: lastid: %w", err)
	}
	b.ID = id
	b.CreatedAt = now
	b.UpdatedAt = now
	s.publish(events.KindBookmarkCreated, b)
	return b, nil
}

// Get returns the bookmark with the given id, or sql.ErrNoRows when
// it's missing.
func (s *BookmarkStore) Get(ctx context.Context, id int64) (Bookmark, error) {
	row := s.db.SQL().QueryRowContext(ctx,
		`SELECT id, name, freq_hz, mode, ctcss_hz, dcs_code, notes,
		        grouping, created_at, updated_at
		 FROM bookmarks WHERE id = ?`, id)
	return scanBookmark(row)
}

// List returns all bookmarks sorted by group then name. Empty result
// is not an error.
func (s *BookmarkStore) List(ctx context.Context) ([]Bookmark, error) {
	rows, err := s.db.SQL().QueryContext(ctx,
		`SELECT id, name, freq_hz, mode, ctcss_hz, dcs_code, notes,
		        grouping, created_at, updated_at
		 FROM bookmarks ORDER BY grouping, name`)
	if err != nil {
		return nil, fmt.Errorf("storage/bookmarks: list: %w", err)
	}
	defer rows.Close()
	var out []Bookmark
	for rows.Next() {
		b, err := scanBookmark(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Update modifies the bookmark identified by b.ID. Only the editable
// fields (name, freq, mode, ctcss, dcs, notes, group) are written;
// created_at is preserved, updated_at refreshed. Returns the new row.
func (s *BookmarkStore) Update(ctx context.Context, b Bookmark) (Bookmark, error) {
	if b.ID == 0 {
		return Bookmark{}, errors.New("storage/bookmarks: id is required")
	}
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return Bookmark{}, errors.New("storage/bookmarks: name is required")
	}
	if b.FreqHz == 0 {
		return Bookmark{}, errors.New("storage/bookmarks: freq_hz is required")
	}
	if b.Mode == "" {
		b.Mode = "FM"
	}
	now := time.Now()
	res, err := s.db.SQL().ExecContext(ctx,
		`UPDATE bookmarks
		 SET name = ?, freq_hz = ?, mode = ?, ctcss_hz = ?, dcs_code = ?,
		     notes = ?, grouping = ?, updated_at = ?
		 WHERE id = ?`,
		b.Name, b.FreqHz, b.Mode, b.CTCSSHz, b.DCSCode, b.Notes, b.Group,
		now.UnixNano(), b.ID)
	if err != nil {
		return Bookmark{}, fmt.Errorf("storage/bookmarks: update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Bookmark{}, sql.ErrNoRows
	}
	updated, err := s.Get(ctx, b.ID)
	if err != nil {
		return Bookmark{}, err
	}
	s.publish(events.KindBookmarkUpdated, updated)
	return updated, nil
}

// Delete removes the bookmark by id. Returns sql.ErrNoRows when the
// id is unknown.
func (s *BookmarkStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.SQL().ExecContext(ctx, `DELETE FROM bookmarks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("storage/bookmarks: delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	s.publish(events.KindBookmarkDeleted, Bookmark{ID: id})
	return nil
}

// rowScanner is the subset of *sql.Row / *sql.Rows that scanBookmark
// needs. Lets one helper serve both Get and List.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanBookmark(s rowScanner) (Bookmark, error) {
	var (
		b         Bookmark
		createdNs int64
		updatedNs int64
	)
	if err := s.Scan(&b.ID, &b.Name, &b.FreqHz, &b.Mode, &b.CTCSSHz, &b.DCSCode,
		&b.Notes, &b.Group, &createdNs, &updatedNs); err != nil {
		return Bookmark{}, err
	}
	b.CreatedAt = time.Unix(0, createdNs)
	b.UpdatedAt = time.Unix(0, updatedNs)
	return b, nil
}

func (s *BookmarkStore) publish(kind events.Kind, b Bookmark) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(events.Event{Kind: kind, Payload: b})
}
