package trunking

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cc.json")

	c, err := OpenCache(path)
	if err != nil {
		t.Fatalf("OpenCache (new): %v", err)
	}
	if _, ok := c.Get("Foo"); ok {
		t.Error("empty cache should not return entries")
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := c.Set("Foo", CachedSystem{LastFrequencyHz: 851_000_000, LastLockAt: now, NAC: 0x293}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	c2, err := OpenCache(path)
	if err != nil {
		t.Fatalf("OpenCache (reload): %v", err)
	}
	got, ok := c2.Get("Foo")
	if !ok {
		t.Fatal("Foo not present after reload")
	}
	if got.LastFrequencyHz != 851_000_000 || got.NAC != 0x293 || !got.LastLockAt.Equal(now) {
		t.Errorf("reloaded entry = %+v", got)
	}
}

func TestCacheMissingFileIsEmpty(t *testing.T) {
	c, err := OpenCache(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("OpenCache: %v", err)
	}
	if names := c.Names(); len(names) != 0 {
		t.Errorf("Names = %v, want []", names)
	}
}

func TestCacheNamesSorted(t *testing.T) {
	c, err := OpenCache(filepath.Join(t.TempDir(), "cc.json"))
	if err != nil {
		t.Fatalf("OpenCache: %v", err)
	}
	for _, n := range []string{"Bravo", "Alpha", "Charlie"} {
		if err := c.Set(n, CachedSystem{LastFrequencyHz: 100}); err != nil {
			t.Fatal(err)
		}
	}
	names := c.Names()
	want := []string{"Alpha", "Bravo", "Charlie"}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("Names = %v, want %v", names, want)
			break
		}
	}
}
