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

type fakeDSCProvider struct {
	msgs []storage.DSCMessage
}

func (f *fakeDSCProvider) RecentDSCMessages(limit int) ([]storage.DSCMessage, error) {
	if limit > 0 && limit < len(f.msgs) {
		return f.msgs[:limit], nil
	}
	return f.msgs, nil
}

func newDSCTestServer(t *testing.T, prov DSCProvider) *httptest.Server {
	t.Helper()
	bus := events.NewBus(8)
	t.Cleanup(bus.Close)
	srv, err := NewServer(ServerOptions{
		Addr: "127.0.0.1:0",
		Bus:  bus,
		DSC:  prov,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestDSCMessagesReturns503WhenNotWired(t *testing.T) {
	ts := newDSCTestServer(t, nil)
	resp, err := http.Get(ts.URL + "/api/v1/dsc/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestDSCMessagesReturnsList(t *testing.T) {
	prov := &fakeDSCProvider{msgs: []storage.DSCMessage{
		{
			ID:          1,
			ReceivedAt:  time.Unix(1735000000, 0).UTC(),
			Format:      "distress",
			Category:    "distress",
			SelfMMSI:    366053209,
			Nature:      "fire / explosion",
			TimeUTC:     "14:25",
			Latitude:    37.8,
			Longitude:   122.4,
			HasPosition: true,
			Body:        "DISTRESS MMSI=366053209",
		},
		{
			ID:         2,
			ReceivedAt: time.Unix(1735000010, 0).UTC(),
			Format:     "individual",
			Category:   "routine",
			SelfMMSI:   366053210,
			TargetMMSI: 3660000,
			Body:       "INDIVIDUAL routine",
		},
	}}
	ts := newDSCTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/dsc/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []DSCMessageDTO
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Format != "distress" || !got[0].HasPosition {
		t.Errorf("row 0 = %+v", got[0])
	}
	if got[1].TargetMMSI != 3660000 {
		t.Errorf("row 1 target = %d", got[1].TargetMMSI)
	}
}

func TestDSCMessagesRespectsLimit(t *testing.T) {
	prov := &fakeDSCProvider{}
	for i := uint64(0); i < 10; i++ {
		prov.msgs = append(prov.msgs, storage.DSCMessage{
			ID:       int64(i + 1),
			SelfMMSI: 366050000 + i,
		})
	}
	ts := newDSCTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/dsc/messages?limit=3")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got []DSCMessageDTO
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 3 {
		t.Errorf("limit=3 len = %d, want 3", len(got))
	}
}
