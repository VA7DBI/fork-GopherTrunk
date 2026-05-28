package panels

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MattCheramie/GopherTrunk/internal/tui/client"
	"github.com/MattCheramie/GopherTrunk/internal/tui/state"
)

type fakeRing[T any] struct{ rows []T }

func (f *fakeRing[T]) Len() int      { return len(f.rows) }
func (f *fakeRing[T]) Snapshot() []T { return append([]T(nil), f.rows...) }
func (f *fakeRing[T]) Latest(n int) []T {
	if n >= len(f.rows) {
		return append([]T(nil), f.rows...)
	}
	return append([]T(nil), f.rows[len(f.rows)-n:]...)
}

func TestFleetSyncPanel_RendersRowsAndDetails(t *testing.T) {
	p := NewFleetSync()
	ev := makeFleetSyncEvent(t, fleetSyncMessage{
		Timestamp:  time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC),
		Version:    2,
		Command:    0x02,
		Subcommand: 0x80,
		FromFleet:  7,
		FromUnit:   101,
		ToFleet:    9,
		ToUnit:     202,
		Emergency:  true,
		Payload:    []byte{0x01, 0x02},
		RawBytes:   []byte{0xAA, 0xBB, 0xCC},
	})
	s := &state.SharedState{EventLog: &fakeRing[client.Event]{rows: []client.Event{ev}}}
	_, _ = p.Update(tea.WindowSizeMsg{Width: 120, Height: 30}, s)
	if got := len(p.tbl.Rows()); got != 1 {
		t.Fatalf("rows=%d want 1", got)
	}
	p.details = true
	view := p.View(120, 30, true, s)
	for _, want := range []string{"FleetSync", "FS2", "7/101", "9/202", "0x02", "EMERGENCY", "payload="} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestFleetSyncPanel_FilterModes(t *testing.T) {
	p := NewFleetSync()
	events := []client.Event{
		makeFleetSyncEvent(t, fleetSyncMessage{Timestamp: time.Now(), Version: 1, Command: 0x01, FromFleet: 1, FromUnit: 111, ToFleet: 2, ToUnit: 222}),
		makeFleetSyncEvent(t, fleetSyncMessage{Timestamp: time.Now(), Version: 1, Command: 0x09, FromFleet: 3, FromUnit: 333, ToFleet: 4, ToUnit: 444}),
	}
	s := &state.SharedState{EventLog: &fakeRing[client.Event]{rows: events}}

	// source filter
	p.mode = fleetSyncFilterSource
	p.filter.SetValue("3/333")
	_, _ = p.Update(tea.WindowSizeMsg{Width: 120, Height: 30}, s)
	if got := len(p.tbl.Rows()); got != 1 {
		t.Fatalf("source filter rows=%d want 1", got)
	}

	// destination filter
	p.mode = fleetSyncFilterDestination
	p.filter.SetValue("2/222")
	_, _ = p.Update(tea.WindowSizeMsg{Width: 120, Height: 30}, s)
	if got := len(p.tbl.Rows()); got != 1 {
		t.Fatalf("destination filter rows=%d want 1", got)
	}

	// command filter
	p.mode = fleetSyncFilterCommand
	p.filter.SetValue("0x09")
	_, _ = p.Update(tea.WindowSizeMsg{Width: 120, Height: 30}, s)
	if got := len(p.tbl.Rows()); got != 1 {
		t.Fatalf("command filter rows=%d want 1", got)
	}
}

func TestFleetSyncPanel_ModeCycleAndClear(t *testing.T) {
	p := NewFleetSync()
	s := &state.SharedState{EventLog: &fakeRing[client.Event]{}}
	if p.mode != fleetSyncFilterAll {
		t.Fatalf("initial mode=%d want all", p.mode)
	}
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")}, s)
	if p.mode != fleetSyncFilterSource {
		t.Fatalf("mode after f=%d want source", p.mode)
	}
	p.filter.SetValue("abc")
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}, s)
	if p.filter.Value() != "" {
		t.Fatalf("clear filter failed, got %q", p.filter.Value())
	}
}

func makeFleetSyncEvent(t *testing.T, m fleetSyncMessage) client.Event {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return client.Event{Kind: "fleetsync.message", Time: m.Timestamp, Raw: raw}
}
