package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/storage"
)

type fakePagerProvider struct {
	msgs []storage.PagerMessage
	err  error
}

func (f *fakePagerProvider) RecentPagerMessages(limit int) ([]storage.PagerMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	if limit > 0 && limit < len(f.msgs) {
		return f.msgs[:limit], nil
	}
	return f.msgs, nil
}

func newPagerTestServer(t *testing.T, prov PagerProvider) *httptest.Server {
	t.Helper()
	bus := events.NewBus(8)
	t.Cleanup(bus.Close)
	srv, err := NewServer(ServerOptions{
		Addr:  "127.0.0.1:0",
		Bus:   bus,
		Pager: prov,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestPagerMessagesReturns503WhenNotWired(t *testing.T) {
	ts := newPagerTestServer(t, nil)
	resp, err := http.Get(ts.URL + "/api/v1/pager/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestPagerMessagesReturnsList(t *testing.T) {
	prov := &fakePagerProvider{
		msgs: []storage.PagerMessage{
			{
				ID:         1,
				ReceivedAt: time.Unix(1735000000, 0).UTC(),
				RIC:        0x12345,
				Func:       1,
				Encoding:   "numeric",
				Body:       "12345",
				Corrected:  0,
			},
			{
				ID:         2,
				ReceivedAt: time.Unix(1735000010, 0).UTC(),
				RIC:        0x6789A,
				Func:       2,
				Encoding:   "alpha",
				Body:       "STRUCTURE FIRE",
				Corrected:  1,
			},
		},
	}
	ts := newPagerTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/pager/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []PagerMessageDTO
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].RIC != 0x12345 || got[1].Body != "STRUCTURE FIRE" {
		t.Errorf("rows = %+v", got)
	}
}

func TestPagerMessagesRespectsLimitParameter(t *testing.T) {
	prov := &fakePagerProvider{}
	for i := 0; i < 10; i++ {
		prov.msgs = append(prov.msgs, storage.PagerMessage{ID: int64(i + 1), RIC: uint32(i)})
	}
	ts := newPagerTestServer(t, prov)

	resp, err := http.Get(ts.URL + "/api/v1/pager/messages?limit=3")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got []PagerMessageDTO
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 3 {
		t.Errorf("limit=3 len = %d, want 3", len(got))
	}
}
