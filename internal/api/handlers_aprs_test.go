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

type fakeAPRSProvider struct {
	pkts []storage.APRSPacket
}

func (f *fakeAPRSProvider) RecentAPRSPackets(limit int) ([]storage.APRSPacket, error) {
	if limit > 0 && limit < len(f.pkts) {
		return f.pkts[:limit], nil
	}
	return f.pkts, nil
}

func newAPRSTestServer(t *testing.T, prov APRSProvider) *httptest.Server {
	t.Helper()
	bus := events.NewBus(8)
	t.Cleanup(bus.Close)
	srv, err := NewServer(ServerOptions{
		Addr: "127.0.0.1:0",
		Bus:  bus,
		APRS: prov,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestAPRSPacketsReturns503WhenNotWired(t *testing.T) {
	ts := newAPRSTestServer(t, nil)
	resp, err := http.Get(ts.URL + "/api/v1/aprs/packets")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestAPRSPacketsReturnsList(t *testing.T) {
	prov := &fakeAPRSProvider{pkts: []storage.APRSPacket{
		{
			ID:         1,
			ReceivedAt: time.Unix(1735000000, 0).UTC(),
			Src:        "W1AW-9",
			Dst:        "APRS",
			Path:       "WIDE1-1",
			Type:       "position",
			Body:       "49.06,-72.03 Test",
			Latitude:   49.0583,
			Longitude:  -72.0292,
			FCSOK:      true,
		},
		{
			ID:         2,
			ReceivedAt: time.Unix(1735000010, 0).UTC(),
			Src:        "W2XYZ",
			Dst:        "APN001",
			Type:       "status",
			Body:       "On the net",
			FCSOK:      true,
		},
	}}
	ts := newAPRSTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/aprs/packets")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []APRSPacketDTO
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Src != "W1AW-9" || got[1].Body != "On the net" {
		t.Errorf("rows = %+v", got)
	}
}

func TestAPRSPacketsRespectsLimit(t *testing.T) {
	prov := &fakeAPRSProvider{}
	for i := 0; i < 10; i++ {
		prov.pkts = append(prov.pkts, storage.APRSPacket{ID: int64(i + 1), Src: "TEST"})
	}
	ts := newAPRSTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/aprs/packets?limit=3")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got []APRSPacketDTO
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 3 {
		t.Errorf("limit=3 len = %d, want 3", len(got))
	}
}
