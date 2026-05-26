package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/storage"
)

// fakeBookmarkProvider is an in-memory implementation of
// BookmarkProvider for handler tests. Tracks ID allocation and
// returns sql.ErrNoRows for missing rows so the handler's not-found
// path is exercised correctly.
type fakeBookmarkProvider struct {
	mu     sync.Mutex
	nextID int64
	store  map[int64]storage.Bookmark
}

func newFakeBookmarkProvider() *fakeBookmarkProvider {
	return &fakeBookmarkProvider{store: map[int64]storage.Bookmark{}}
}

func (f *fakeBookmarkProvider) ListBookmarks() ([]storage.Bookmark, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]storage.Bookmark, 0, len(f.store))
	for _, b := range f.store {
		out = append(out, b)
	}
	return out, nil
}

func (f *fakeBookmarkProvider) GetBookmark(id int64) (storage.Bookmark, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.store[id]
	if !ok {
		return storage.Bookmark{}, sql.ErrNoRows
	}
	return b, nil
}

func (f *fakeBookmarkProvider) CreateBookmark(b storage.Bookmark) (storage.Bookmark, error) {
	if b.Name == "" {
		return storage.Bookmark{}, errors.New("name required")
	}
	if b.FreqHz == 0 {
		return storage.Bookmark{}, errors.New("freq required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	b.ID = f.nextID
	if b.Mode == "" {
		b.Mode = "FM"
	}
	f.store[b.ID] = b
	return b, nil
}

func (f *fakeBookmarkProvider) UpdateBookmark(b storage.Bookmark) (storage.Bookmark, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.store[b.ID]; !ok {
		return storage.Bookmark{}, sql.ErrNoRows
	}
	if b.Name == "" {
		return storage.Bookmark{}, errors.New("name required")
	}
	f.store[b.ID] = b
	return b, nil
}

func (f *fakeBookmarkProvider) DeleteBookmark(id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.store[id]; !ok {
		return sql.ErrNoRows
	}
	delete(f.store, id)
	return nil
}

func newBookmarkTestServer(t *testing.T, prov BookmarkProvider) *httptest.Server {
	t.Helper()
	bus := events.NewBus(8)
	t.Cleanup(bus.Close)
	srv, err := NewServer(ServerOptions{
		Addr:           "127.0.0.1:0",
		Bus:            bus,
		Bookmarks:      prov,
		AllowMutations: true,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestBookmarksReturn503WhenNotWired(t *testing.T) {
	ts := newBookmarkTestServer(t, nil)
	resp, err := http.Get(ts.URL + "/api/v1/bookmarks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestBookmarkCreateAndList(t *testing.T) {
	prov := newFakeBookmarkProvider()
	ts := newBookmarkTestServer(t, prov)

	body := `{"name":"Marine Ch 16","freq_hz":156800000,"mode":"FM","group":"marine"}`
	resp, err := http.Post(ts.URL+"/api/v1/bookmarks", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201", resp.StatusCode)
	}
	var created BookmarkDTO
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID == 0 || created.Name != "Marine Ch 16" || created.FreqHz != 156_800_000 {
		t.Errorf("created = %+v", created)
	}

	listResp, err := http.Get(ts.URL + "/api/v1/bookmarks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer listResp.Body.Close()
	var got []BookmarkDTO
	if err := json.NewDecoder(listResp.Body).Decode(&got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(got) != 1 || got[0].ID != created.ID {
		t.Errorf("list = %+v, want one bookmark with id %d", got, created.ID)
	}
}

func TestBookmarkCreateBadJSON(t *testing.T) {
	prov := newFakeBookmarkProvider()
	ts := newBookmarkTestServer(t, prov)

	resp, err := http.Post(ts.URL+"/api/v1/bookmarks", "application/json", bytes.NewBufferString("not json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad JSON status = %d, want 400", resp.StatusCode)
	}
}

func TestBookmarkUpdate(t *testing.T) {
	prov := newFakeBookmarkProvider()
	ts := newBookmarkTestServer(t, prov)

	// Seed one bookmark.
	seed, _ := prov.CreateBookmark(storage.Bookmark{Name: "orig", FreqHz: 100_000_000})

	req := httpPatch(t, ts.URL+"/api/v1/bookmarks/"+itoa(seed.ID),
		`{"name":"edited","freq_hz":200000000,"mode":"FM"}`)
	if req.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200", req.StatusCode)
	}
	var updated BookmarkDTO
	_ = json.NewDecoder(req.Body).Decode(&updated)
	if updated.Name != "edited" || updated.FreqHz != 200_000_000 {
		t.Errorf("updated = %+v", updated)
	}
	req.Body.Close()
}

func TestBookmarkUpdateNotFound(t *testing.T) {
	prov := newFakeBookmarkProvider()
	ts := newBookmarkTestServer(t, prov)

	req := httpPatch(t, ts.URL+"/api/v1/bookmarks/9999",
		`{"name":"x","freq_hz":1,"mode":"FM"}`)
	if req.StatusCode != http.StatusNotFound {
		t.Errorf("PATCH on missing id = %d, want 404", req.StatusCode)
	}
	req.Body.Close()
}

func TestBookmarkDelete(t *testing.T) {
	prov := newFakeBookmarkProvider()
	ts := newBookmarkTestServer(t, prov)

	seed, _ := prov.CreateBookmark(storage.Bookmark{Name: "doomed", FreqHz: 100})

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/bookmarks/"+itoa(seed.ID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE status = %d, want 204", resp.StatusCode)
	}

	// Second delete returns 404.
	req2, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/bookmarks/"+itoa(seed.ID), nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("second DELETE: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("second DELETE = %d, want 404", resp2.StatusCode)
	}
}

func TestBookmarkMutationsAreGated(t *testing.T) {
	// AuthModeRequired without a token rejects every mutation —
	// confirms the bookmark POST/PATCH/DELETE routes are wrapped in
	// s.gate like every other write endpoint.
	prov := newFakeBookmarkProvider()
	bus := events.NewBus(8)
	t.Cleanup(bus.Close)
	srv, err := NewServer(ServerOptions{
		Addr:      "127.0.0.1:0",
		Bus:       bus,
		Bookmarks: prov,
		Auth: AuthConfig{
			Mode:  AuthModeRequired,
			Token: "test-token",
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)

	// List is open even with auth required (read endpoint).
	listResp, err := http.Get(ts.URL + "/api/v1/bookmarks")
	if err != nil {
		t.Fatalf("GET list: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Errorf("GET list = %d, want 200", listResp.StatusCode)
	}

	// POST without a token must fail with 401.
	createResp, err := http.Post(ts.URL+"/api/v1/bookmarks", "application/json",
		bytes.NewBufferString(`{"name":"x","freq_hz":1}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST without token = %d, want 401", createResp.StatusCode)
	}
}

// helpers — keep here so the package's existing helpers stay
// untouched.

func httpPatch(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	return resp
}

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
