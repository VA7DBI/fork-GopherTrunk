package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

func newTestStore(t *testing.T) (*BookmarkStore, *events.Bus, func()) {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	bus := events.NewBus(8)
	store, err := NewBookmarkStore(db, bus)
	if err != nil {
		t.Fatalf("NewBookmarkStore: %v", err)
	}
	return store, bus, func() {
		bus.Close()
		_ = db.Close()
	}
}

func TestBookmarkCreateRoundTrip(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	b, err := store.Create(ctx, Bookmark{
		Name:    "Marine Ch 16",
		FreqHz:  156_800_000,
		Mode:    "FM",
		CTCSSHz: 0,
		Notes:   "International distress",
		Group:   "marine",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.ID == 0 {
		t.Error("Create returned zero ID")
	}
	if b.CreatedAt.IsZero() || b.UpdatedAt.IsZero() {
		t.Error("Create did not stamp CreatedAt / UpdatedAt")
	}

	got, err := store.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Marine Ch 16" || got.FreqHz != 156_800_000 || got.Group != "marine" {
		t.Errorf("Get returned %+v", got)
	}
}

func TestBookmarkCreateRequiresNameAndFreq(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := store.Create(ctx, Bookmark{FreqHz: 100_000_000}); err == nil {
		t.Error("Create with empty name: want error")
	}
	if _, err := store.Create(ctx, Bookmark{Name: "no freq"}); err == nil {
		t.Error("Create with zero freq: want error")
	}
}

func TestBookmarkCreateDefaultsModeToFM(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	b, err := store.Create(ctx, Bookmark{Name: "NOAA WX", FreqHz: 162_550_000})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.Mode != "FM" {
		t.Errorf("Mode = %q, want FM (default)", b.Mode)
	}
}

func TestBookmarkListSortedByGroupAndName(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Insert in random order.
	_, _ = store.Create(ctx, Bookmark{Name: "Z-thing", FreqHz: 100, Group: "alpha"})
	_, _ = store.Create(ctx, Bookmark{Name: "A-thing", FreqHz: 200, Group: "alpha"})
	_, _ = store.Create(ctx, Bookmark{Name: "Mid", FreqHz: 300, Group: "beta"})

	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Name != "A-thing" || got[1].Name != "Z-thing" || got[2].Name != "Mid" {
		t.Errorf("sort order = [%q, %q, %q], want [A-thing, Z-thing, Mid]",
			got[0].Name, got[1].Name, got[2].Name)
	}
}

func TestBookmarkUpdate(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	b, _ := store.Create(ctx, Bookmark{Name: "old name", FreqHz: 100_000_000, Group: "test"})
	originalCreated := b.CreatedAt

	// Force a measurable updated_at delta.
	time.Sleep(2 * time.Millisecond)

	b.Name = "new name"
	b.FreqHz = 200_000_000
	b.Notes = "freshly edited"
	got, err := store.Update(ctx, b)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Name != "new name" || got.FreqHz != 200_000_000 || got.Notes != "freshly edited" {
		t.Errorf("Update returned %+v", got)
	}
	if !got.CreatedAt.Equal(originalCreated) {
		t.Error("Update changed CreatedAt; should preserve it")
	}
	if !got.UpdatedAt.After(originalCreated) {
		t.Error("Update did not refresh UpdatedAt")
	}
}

func TestBookmarkUpdateMissingID(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	_, err := store.Update(ctx, Bookmark{ID: 9999, Name: "x", FreqHz: 1})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Update unknown id: err = %v, want sql.ErrNoRows", err)
	}
}

func TestBookmarkDelete(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	b, _ := store.Create(ctx, Bookmark{Name: "transient", FreqHz: 100_000_000})
	if err := store.Delete(ctx, b.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, b.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Get after Delete: err = %v, want sql.ErrNoRows", err)
	}
	// Second delete is a no-op-but-not-success.
	if err := store.Delete(ctx, b.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("double-Delete: err = %v, want sql.ErrNoRows", err)
	}
}

func TestBookmarkMutationsPublishOnBus(t *testing.T) {
	store, bus, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	sub := bus.Subscribe()
	defer sub.Close()

	b, err := store.Create(ctx, Bookmark{Name: "watch me", FreqHz: 462_562_500})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	awaitEvent(t, sub.C, events.KindBookmarkCreated)

	b.Notes = "edit"
	if _, err := store.Update(ctx, b); err != nil {
		t.Fatalf("Update: %v", err)
	}
	awaitEvent(t, sub.C, events.KindBookmarkUpdated)

	if err := store.Delete(ctx, b.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	awaitEvent(t, sub.C, events.KindBookmarkDeleted)
}

func TestBookmarkStoreWithoutBusStillWorks(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	store, err := NewBookmarkStore(db, nil)
	if err != nil {
		t.Fatalf("NewBookmarkStore: %v", err)
	}
	if _, err := store.Create(context.Background(), Bookmark{Name: "x", FreqHz: 1}); err != nil {
		t.Errorf("Create without bus: %v", err)
	}
}

func awaitEvent(t *testing.T, ch <-chan events.Event, kind events.Kind) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("bus closed")
		}
		if ev.Kind != kind {
			t.Errorf("got event %q, want %q", ev.Kind, kind)
		}
	case <-time.After(time.Second):
		t.Fatalf("did not receive %q within 1s", kind)
	}
}
