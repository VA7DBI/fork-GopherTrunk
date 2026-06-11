package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/storage"
)

type fakeFleetSyncProvider struct {
	rows      []storage.FleetSyncMessage
	listErr   error
	lastLimit int
}

func (f *fakeFleetSyncProvider) RecentFleetSyncMessages(limit int) ([]storage.FleetSyncMessage, error) {
	f.lastLimit = limit
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.rows, nil
}

func newFleetSyncTestServer(t *testing.T, prov FleetSyncProvider) *httptest.Server {
	t.Helper()
	bus := events.NewBus(8)
	t.Cleanup(bus.Close)
	srv, err := NewServer(ServerOptions{Addr: "127.0.0.1:0", Bus: bus, FleetSync: prov})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestFleetSyncMessagesReturns503WhenNotWired(t *testing.T) {
	ts := newFleetSyncTestServer(t, nil)
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", resp.StatusCode)
	}
}

func TestFleetSyncMessagesReturnsList(t *testing.T) {
	prov := &fakeFleetSyncProvider{rows: []storage.FleetSyncMessage{{
		ID: 1, ReceivedAt: time.Unix(1735000000, 0).UTC(), Version: 2,
		Command: 0x02, Subcommand: 0x80, FromFleet: 7, FromUnit: 101,
		ToFleet: 8, ToUnit: 202, Emergency: true, Payload: []byte{0x01, 0x02}, RawBytes: []byte{0xAA},
	}}}
	ts := newFleetSyncTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/messages?limit=3")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	if prov.lastLimit != 3 {
		t.Fatalf("limit=%d want 3", prov.lastLimit)
	}
	var got []FleetSyncMessageDTO
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].PayloadHex != "0102" || got[0].RawHex != "AA" {
		t.Fatalf("rows = %+v", got)
	}
}

func TestFleetSyncMessagesIgnoresBadLimit(t *testing.T) {
	prov := &fakeFleetSyncProvider{}
	ts := newFleetSyncTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/messages?limit=bogus")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	if prov.lastLimit != 200 {
		t.Fatalf("limit=%d want 200", prov.lastLimit)
	}
}

func TestFleetSyncMessagesReturns500OnProviderError(t *testing.T) {
	prov := &fakeFleetSyncProvider{listErr: errors.New("boom")}
	ts := newFleetSyncTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", resp.StatusCode)
	}
}
