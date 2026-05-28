package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/storage"
)

type fakeFleetSyncProvider struct {
	rows       []storage.FleetSyncMessage
	listErr    error
	getErr     error
	lastFilter storage.FleetSyncFilter
}

func (f *fakeFleetSyncProvider) ListFleetSyncMessages(filter storage.FleetSyncFilter) ([]storage.FleetSyncMessage, error) {
	f.lastFilter = filter
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.rows, nil
}

func (f *fakeFleetSyncProvider) GetFleetSyncMessage(id int64) (storage.FleetSyncMessage, error) {
	if f.getErr != nil {
		return storage.FleetSyncMessage{}, f.getErr
	}
	for _, row := range f.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return storage.FleetSyncMessage{}, sql.ErrNoRows
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

func TestFleetSyncMessagesReturnsListAndFilter(t *testing.T) {
	prov := &fakeFleetSyncProvider{rows: []storage.FleetSyncMessage{{
		ID: 1, ReceivedAt: time.Unix(1735000000, 0).UTC(), Version: 2,
		Command: 0x02, Subcommand: 0x80, FromFleet: 7, FromUnit: 101,
		ToFleet: 8, ToUnit: 202, Emergency: true, Payload: []byte{0x01, 0x02}, RawBytes: []byte{0xAA},
	}}}
	ts := newFleetSyncTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/messages?limit=3&source_unit=101&command=0x02")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	if prov.lastFilter.Limit != 3 {
		t.Fatalf("limit=%d want 3", prov.lastFilter.Limit)
	}
	if prov.lastFilter.SourceUnit == nil || *prov.lastFilter.SourceUnit != 101 {
		t.Fatalf("source filter = %+v", prov.lastFilter.SourceUnit)
	}
	if prov.lastFilter.Command == nil || *prov.lastFilter.Command != 0x02 {
		t.Fatalf("command filter = %+v", prov.lastFilter.Command)
	}
	var got []FleetSyncMessageDTO
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].PayloadHex != "0102" || got[0].RawHex != "AA" {
		t.Fatalf("rows = %+v", got)
	}
}

func TestFleetSyncMessageByID(t *testing.T) {
	prov := &fakeFleetSyncProvider{rows: []storage.FleetSyncMessage{{ID: 5, Command: 0x01, FromUnit: 123}}}
	ts := newFleetSyncTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/messages/5")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	var got FleetSyncMessageDTO
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != 5 || got.FromUnit != 123 {
		t.Fatalf("row = %+v", got)
	}
}

func TestFleetSyncMessagesRejectBadQuery(t *testing.T) {
	prov := &fakeFleetSyncProvider{}
	ts := newFleetSyncTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/messages?command=bogus")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", resp.StatusCode)
	}
}
