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

type fakeAISProvider struct {
	msgs []storage.AISMessage
}

func (f *fakeAISProvider) RecentAISMessages(limit int) ([]storage.AISMessage, error) {
	if limit > 0 && limit < len(f.msgs) {
		return f.msgs[:limit], nil
	}
	return f.msgs, nil
}

func newAISTestServer(t *testing.T, prov AISProvider) *httptest.Server {
	t.Helper()
	bus := events.NewBus(8)
	t.Cleanup(bus.Close)
	srv, err := NewServer(ServerOptions{
		Addr: "127.0.0.1:0",
		Bus:  bus,
		AIS:  prov,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestAISVesselsReturns503WhenNotWired(t *testing.T) {
	ts := newAISTestServer(t, nil)
	resp, err := http.Get(ts.URL + "/api/v1/ais/vessels")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestAISVesselsReturnsList(t *testing.T) {
	prov := &fakeAISProvider{msgs: []storage.AISMessage{
		{
			ID:               1,
			ReceivedAt:       time.Unix(1735000000, 0).UTC(),
			MMSI:             366053209,
			Type:             "position-a",
			Body:             "CLASS-A MMSI=366053209 37.80,-122.34",
			Latitude:         37.8021,
			Longitude:        -122.3416,
			SpeedOverGround:  12.3,
			CourseOverGround: 51.0,
			Heading:          50,
			HasPosition:      true,
			FCSOK:            true,
		},
		{
			ID:          2,
			ReceivedAt:  time.Unix(1735000010, 0).UTC(),
			MMSI:        366053210,
			Type:        "static-voyage",
			Body:        "STATIC ...",
			VesselName:  "NAUTICAL LIMITS",
			Callsign:    "WCB1234",
			Destination: "SF BAY",
			ShipType:    70,
			IMO:         9123456,
			FCSOK:       true,
		},
	}}
	ts := newAISTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/ais/vessels")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []AISMessageDTO
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].MMSI != 366053209 || !got[0].HasPosition {
		t.Errorf("row 0 = %+v", got[0])
	}
	if got[1].VesselName != "NAUTICAL LIMITS" {
		t.Errorf("row 1 name = %q", got[1].VesselName)
	}
}

func TestAISVesselsRespectsLimit(t *testing.T) {
	prov := &fakeAISProvider{}
	for i := uint32(0); i < 10; i++ {
		prov.msgs = append(prov.msgs, storage.AISMessage{
			ID:   int64(i + 1),
			MMSI: 100000000 + i,
		})
	}
	ts := newAISTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/ais/vessels?limit=3")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got []AISMessageDTO
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 3 {
		t.Errorf("limit=3 len = %d, want 3", len(got))
	}
}
