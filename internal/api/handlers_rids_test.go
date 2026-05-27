package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/storage"
	"github.com/MattCheramie/GopherTrunk/internal/trunking"
)

type fakeAffiliationProvider struct {
	units []trunking.UnitActivity
}

func (f *fakeAffiliationProvider) Affiliations() []trunking.UnitActivity { return f.units }

func TestListRIDsMergesConfiguredAndLive(t *testing.T) {
	bus := events.NewBus(4)
	defer bus.Close()

	db := trunking.NewRIDDB()
	db.Add(&trunking.RID{ID: 100, Alias: "CHIEF", Owner: "Cmdr. Riker", Watch: true})
	db.Add(&trunking.RID{ID: 200, Alias: "LOCKED", Lockout: true})

	live := &fakeAffiliationProvider{units: []trunking.UnitActivity{
		// 100 — configured + live observation
		{
			RadioID: 100, System: "Metro", Protocol: "p25-phase1",
			Talkgroup: 50, TalkerAlias: "CHIEF-ENG", CallCount: 3,
			LastSeen: time.Unix(1700, 0).UTC(),
		},
		// 300 — live only, no configured catalogue row
		{
			RadioID: 300, System: "Metro", Protocol: "p25-phase1",
			Talkgroup: 50, CallCount: 1,
			LastSeen: time.Unix(1800, 0).UTC(),
		},
	}}

	base, teardown := mkServer(t, ServerOptions{
		Bus:          bus,
		RIDs:         db,
		Affiliations: live,
	})
	defer teardown()

	resp, err := http.Get(base + "/api/v1/rids")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		RIDs []RIDDTO `json:"rids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.RIDs) != 3 {
		t.Fatalf("rids = %d, want 3 (100 configured+live, 200 configured-only, 300 live-only)", len(body.RIDs))
	}
	byID := map[uint32]RIDDTO{}
	for _, r := range body.RIDs {
		byID[r.ID] = r
	}
	// 100 — merged: configured alias and live observation both present.
	if r := byID[100]; r.Alias != "CHIEF" || !r.Configured || r.TalkerAlias != "CHIEF-ENG" ||
		r.LastTalkgroup != 50 || r.CallCount != 3 {
		t.Errorf("RID 100 = %+v", r)
	}
	// 200 — configured but never observed.
	if r := byID[200]; !r.Configured || r.CallCount != 0 || r.LastTalkgroup != 0 ||
		!r.Lockout || r.Alias != "LOCKED" {
		t.Errorf("RID 200 = %+v", r)
	}
	// 300 — live only, not in catalogue.
	if r := byID[300]; r.Configured || r.Alias != "" || r.CallCount != 1 || r.LastTalkgroup != 50 {
		t.Errorf("RID 300 = %+v", r)
	}
}

func TestListRIDsEmptyWhenNothingWired(t *testing.T) {
	bus := events.NewBus(4)
	defer bus.Close()
	base, teardown := mkServer(t, ServerOptions{Bus: bus})
	defer teardown()

	resp, _ := http.Get(base + "/api/v1/rids")
	defer resp.Body.Close()
	var body struct {
		RIDs []RIDDTO `json:"rids"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.RIDs) != 0 {
		t.Errorf("expected empty rids, got %d", len(body.RIDs))
	}
}

func TestGetRIDReturnsConfiguredOnlyRow(t *testing.T) {
	bus := events.NewBus(4)
	defer bus.Close()
	db := trunking.NewRIDDB()
	db.Add(&trunking.RID{ID: 42, Alias: "ANSWER", Watch: true})

	base, teardown := mkServer(t, ServerOptions{Bus: bus, RIDs: db})
	defer teardown()

	resp, _ := http.Get(base + "/api/v1/rids/42")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got RIDDTO
	json.NewDecoder(resp.Body).Decode(&got)
	if got.ID != 42 || got.Alias != "ANSWER" || !got.Configured {
		t.Errorf("got = %+v", got)
	}
}

func TestGetRIDReturnsLiveOnlyRow(t *testing.T) {
	bus := events.NewBus(4)
	defer bus.Close()
	live := &fakeAffiliationProvider{units: []trunking.UnitActivity{
		{RadioID: 7, System: "X", Talkgroup: 11, LastSeen: time.Unix(100, 0).UTC()},
	}}
	base, teardown := mkServer(t, ServerOptions{Bus: bus, Affiliations: live})
	defer teardown()

	resp, _ := http.Get(base + "/api/v1/rids/7")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got RIDDTO
	json.NewDecoder(resp.Body).Decode(&got)
	if got.ID != 7 || got.Configured || got.LastTalkgroup != 11 {
		t.Errorf("got = %+v", got)
	}
}

func TestGetRIDNotFound(t *testing.T) {
	bus := events.NewBus(4)
	defer bus.Close()
	base, teardown := mkServer(t, ServerOptions{Bus: bus})
	defer teardown()

	resp, _ := http.Get(base + "/api/v1/rids/999")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetRIDInvalidID(t *testing.T) {
	bus := events.NewBus(4)
	defer bus.Close()
	base, teardown := mkServer(t, ServerOptions{Bus: bus})
	defer teardown()

	resp, _ := http.Get(base + "/api/v1/rids/notanumber")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRIDHistoryReturnsSourceFilteredRows(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	dbPath := filepath.Join(t.TempDir(), "calls.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cl, err := storage.NewCallLog(db, bus, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cl.Run(ctx)

	now := time.Now().UTC().Truncate(time.Microsecond)
	for i, src := range []uint32{4242, 4242, 7777} {
		bus.Publish(events.Event{
			Kind: events.KindCallStart,
			Payload: trunking.CallStart{
				Grant: trunking.Grant{
					System: "Metro", Protocol: "p25",
					GroupID: 50, SourceID: src, FrequencyHz: 851_000_000,
				},
				DeviceSerial: "VOICE-1",
				// Distinct StartedAt per row — call_log is keyed by
				// (device_serial, started_at), so identical timestamps
				// collapse via INSERT OR REPLACE.
				StartedAt: now.Add(time.Duration(i) * time.Second),
			},
		})
	}
	// Wait for rows to flush.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		rows, _ := db.History(context.Background(), storage.HistoryFilter{Limit: 10})
		if len(rows) == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	base, teardown := mkServer(t, ServerOptions{
		Bus:     bus,
		History: HistoryFromStorage(db),
	})
	defer teardown()

	resp, _ := http.Get(base + "/api/v1/rids/4242/history")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Calls []CallRow `json:"calls"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Calls) != 2 {
		t.Fatalf("rid 4242 history = %d, want 2", len(body.Calls))
	}
	for _, c := range body.Calls {
		if c.SourceID != 4242 {
			t.Errorf("row source = %d, want 4242", c.SourceID)
		}
	}
}

func TestRIDHistory503WithoutHistoryWired(t *testing.T) {
	bus := events.NewBus(4)
	defer bus.Close()
	base, teardown := mkServer(t, ServerOptions{Bus: bus})
	defer teardown()

	resp, _ := http.Get(base + "/api/v1/rids/1/history")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestUpdateRIDMutatesConfiguredRow(t *testing.T) {
	bus := events.NewBus(4)
	defer bus.Close()
	db := trunking.NewRIDDB()
	db.Add(&trunking.RID{ID: 100, Alias: "OLD", Watch: true})

	base, teardown := mkServer(t, ServerOptions{
		Bus:            bus,
		RIDs:           db,
		AllowMutations: true,
	})
	defer teardown()

	body, _ := json.Marshal(map[string]any{"alias": "NEW", "priority": 5, "watch": false})
	req, _ := http.NewRequest(http.MethodPatch, base+"/api/v1/rids/100", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got RIDDTO
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Alias != "NEW" || got.Priority != 5 || got.Watch {
		t.Errorf("got = %+v", got)
	}
	// Confirm the in-memory DB reflects the change.
	if r := db.Lookup(100); r == nil || r.Alias != "NEW" || r.Priority != 5 || r.Watch {
		t.Errorf("db row = %+v", r)
	}
}

func TestUpdateRIDRejectsEmptyBody(t *testing.T) {
	bus := events.NewBus(4)
	defer bus.Close()
	db := trunking.NewRIDDB()
	db.Add(&trunking.RID{ID: 1, Watch: true})

	base, teardown := mkServer(t, ServerOptions{Bus: bus, RIDs: db, AllowMutations: true})
	defer teardown()

	req, _ := http.NewRequest(http.MethodPatch, base+"/api/v1/rids/1", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUpdateRIDLiveOnlyReturns404(t *testing.T) {
	// A RID only seen over the air (no static catalogue row) cannot
	// be patched — operators must add it to the alias file first.
	bus := events.NewBus(4)
	defer bus.Close()
	live := &fakeAffiliationProvider{units: []trunking.UnitActivity{
		{RadioID: 5, Talkgroup: 1},
	}}
	base, teardown := mkServer(t, ServerOptions{
		Bus: bus, RIDs: trunking.NewRIDDB(), Affiliations: live, AllowMutations: true,
	})
	defer teardown()

	body, _ := json.Marshal(map[string]any{"alias": "NEW"})
	req, _ := http.NewRequest(http.MethodPatch, base+"/api/v1/rids/5", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
