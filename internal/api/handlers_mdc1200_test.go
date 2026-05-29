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

type fakeMDC1200Provider struct {
	msgs []storage.MDC1200Message
}

func (f *fakeMDC1200Provider) RecentMDC1200Messages(limit int) ([]storage.MDC1200Message, error) {
	if limit > 0 && limit < len(f.msgs) {
		return f.msgs[:limit], nil
	}
	return f.msgs, nil
}

func newMDC1200TestServer(t *testing.T, prov MDC1200Provider) *httptest.Server {
	t.Helper()
	bus := events.NewBus(8)
	t.Cleanup(bus.Close)
	srv, err := NewServer(ServerOptions{
		Addr:    "127.0.0.1:0",
		Bus:     bus,
		MDC1200: prov,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestMDC1200MessagesReturns503WhenNotWired(t *testing.T) {
	ts := newMDC1200TestServer(t, nil)
	resp, err := http.Get(ts.URL + "/api/v1/mdc1200/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestMDC1200MessagesReturnsList(t *testing.T) {
	prov := &fakeMDC1200Provider{msgs: []storage.MDC1200Message{
		{
			ID:         1,
			ReceivedAt: time.Unix(1735000000, 0).UTC(),
			Op:         0x01,
			Arg:        0x80,
			UnitID:     0x1234,
			Operation:  "PTT ID",
			Body:       "Unit 1234: PTT ID",
			CRCOK:      true,
		},
		{
			ID:         2,
			ReceivedAt: time.Unix(1735000010, 0).UTC(),
			Op:         0x00,
			Arg:        0x90,
			UnitID:     0x0042,
			Operation:  "Emergency",
			Body:       "Unit 0042: Emergency",
			CRCOK:      true,
		},
	}}
	ts := newMDC1200TestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/mdc1200/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []MDC1200MessageDTO
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Operation != "PTT ID" || got[0].UnitID != 0x1234 {
		t.Errorf("row 0 = %+v", got[0])
	}
	if got[1].Operation != "Emergency" {
		t.Errorf("row 1 = %+v", got[1])
	}
}

func TestMDC1200MessagesRespectsLimit(t *testing.T) {
	prov := &fakeMDC1200Provider{}
	for i := 0; i < 10; i++ {
		prov.msgs = append(prov.msgs, storage.MDC1200Message{
			ID:     int64(i + 1),
			UnitID: uint16(0x1000 + i),
		})
	}
	ts := newMDC1200TestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/mdc1200/messages?limit=3")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got []MDC1200MessageDTO
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 3 {
		t.Errorf("limit=3 len = %d, want 3", len(got))
	}
}
