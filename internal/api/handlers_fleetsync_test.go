package api

import (
	"database/sql"
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
	rows            []storage.FleetSyncMessage
	stats           storage.FleetSyncStats
	runtime         FleetSyncRuntimeStatsDTO
	listErr         error
	getErr          error
	statsErr        error
	lastFilter      storage.FleetSyncFilter
	lastStatsFilter storage.FleetSyncFilter
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

func (f *fakeFleetSyncProvider) FleetSyncStats(filter storage.FleetSyncFilter) (storage.FleetSyncStats, error) {
	f.lastStatsFilter = filter
	if f.statsErr != nil {
		return storage.FleetSyncStats{}, f.statsErr
	}
	return f.stats, nil
}

func (f *fakeFleetSyncProvider) FleetSyncRuntimeStats() FleetSyncRuntimeStatsDTO {
	return f.runtime
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
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/messages?limit=3&source_unit=101&destination_unit=202&command=0x02&since=2024-12-24T23:00:00Z&until=2024-12-25T01:00:00Z")
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
	if prov.lastFilter.DestinationUnit == nil || *prov.lastFilter.DestinationUnit != 202 {
		t.Fatalf("destination filter = %+v", prov.lastFilter.DestinationUnit)
	}
	if prov.lastFilter.Command == nil || *prov.lastFilter.Command != 0x02 {
		t.Fatalf("command filter = %+v", prov.lastFilter.Command)
	}
	if got, want := prov.lastFilter.Since.UTC().Format(time.RFC3339), "2024-12-24T23:00:00Z"; got != want {
		t.Fatalf("since = %s want %s", got, want)
	}
	if got, want := prov.lastFilter.Until.UTC().Format(time.RFC3339), "2024-12-25T01:00:00Z"; got != want {
		t.Fatalf("until = %s want %s", got, want)
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

func TestFleetSyncMessageByIDReturnsNotFound(t *testing.T) {
	prov := &fakeFleetSyncProvider{}
	ts := newFleetSyncTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/messages/99")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
}

func TestFleetSyncMessageByIDRejectsBadID(t *testing.T) {
	prov := &fakeFleetSyncProvider{}
	ts := newFleetSyncTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/messages/not-a-number")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", resp.StatusCode)
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

func TestFleetSyncMessagesRejectInvalidRange(t *testing.T) {
	prov := &fakeFleetSyncProvider{}
	ts := newFleetSyncTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/messages?since=2024-12-25T01:00:00Z&until=2024-12-24T23:00:00Z")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", resp.StatusCode)
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

func TestFleetSyncStatsReturnsAggregate(t *testing.T) {
	prov := &fakeFleetSyncProvider{stats: storage.FleetSyncStats{
		Total:     3,
		Emergency: 1,
		Priority:  1,
		FirstSeen: time.Unix(1700000000, 0).UTC(),
		LastSeen:  time.Unix(1700000200, 0).UTC(),
		Commands:  []storage.FleetSyncCommandStat{{Command: 0x01, Count: 2}, {Command: 0x02, Count: 1}},
	}, runtime: FleetSyncRuntimeStatsDTO{
		MessagesEmitted: 9,
		TotalSamples:    48000,
		TotalMessagesRx: 3,
		SyncErrors:      1,
		CRCErrors:       2,
		LastMessageTime: time.Unix(1700000200, 0).UTC(),
		MessageRate:     1.5,
		Channels: []FleetSyncRuntimeChannelStatsDTO{
			{Source: "utilities-east", MessagesEmitted: 5, TotalSamples: 30000, TotalMessagesRx: 2, SyncErrors: 1, CRCErrors: 0, MessageRate: 1.0},
			{Source: "utilities-west", MessagesEmitted: 4, TotalSamples: 18000, TotalMessagesRx: 1, SyncErrors: 0, CRCErrors: 2, MessageRate: 0.5},
		},
		Export: FleetSyncExportRuntimeStatsDTO{
			Queued:                          10,
			Dropped:                         1,
			LastEventAt:                     time.Unix(1700000190, 0).UTC(),
			LastSendAt:                      time.Unix(1700000185, 0).UTC(),
			LastFailureAt:                   time.Unix(1700000180, 0).UTC(),
			TelemetryAgeSeconds:             10,
			QueueDepth:                      4,
			QueueCapacity:                   1024,
			QueueUtilization:                4.0 / 1024.0,
			QueueUtilizationLast60sAvg:      0.25,
			QueueUtilizationLast60sPeak:     0.75,
			SentLast60sTotal:                4,
			FailedLast60sTotal:              1,
			SuccessRateLast60s:              0.8,
			FailureRateLast60s:              0.2,
			RetriedLast60sTotal:             1,
			RetryRateLast60s:                0.2,
			DroppedToAttemptsRateLast60s:    0.2,
			DroppedBySource:                 map[string]int{"utilities-west": 1},
			DroppedPerMinuteBySource:        map[string]float64{"utilities-west": 2.5},
			DroppedLast60sTotal:             1,
			DroppedPerMinuteLast60sTotal:    1.0,
			DroppedLast60sBySource:          map[string]int{"utilities-west": 1},
			DroppedPerMinuteLast60sBySource: map[string]float64{"utilities-west": 1.0},
			Backends: []FleetSyncExportBackendStatsDTO{
				{Name: "webhook-main", Sent: 8, SentLast60s: 4, SuccessRateLast60s: 0.8, Failed: 1, FailedLast60s: 1, FailureRateLast60s: 0.2, Attempts: 11, AttemptsLast60s: 5, Retried: 3, RetriedLast60s: 1},
			},
		},
	}}
	ts := newFleetSyncTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/stats?command=0x01")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	if prov.lastStatsFilter.Command == nil || *prov.lastStatsFilter.Command != 0x01 {
		t.Fatalf("command filter = %+v", prov.lastStatsFilter.Command)
	}
	var got FleetSyncStatsDTO
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != 3 || len(got.Commands) != 2 {
		t.Fatalf("stats = %+v", got)
	}
	if got.Runtime.MessagesEmitted != 9 || got.Runtime.SyncErrors != 1 || got.Runtime.CRCErrors != 2 {
		t.Fatalf("runtime = %+v", got.Runtime)
	}
	if len(got.Runtime.Channels) != 2 || got.Runtime.Channels[0].Source != "utilities-east" {
		t.Fatalf("runtime.channels = %+v", got.Runtime.Channels)
	}
	if got.Runtime.Export.Dropped != 1 || len(got.Runtime.Export.Backends) != 1 || got.Runtime.Export.Backends[0].Retried != 3 {
		t.Fatalf("runtime.export = %+v", got.Runtime.Export)
	}
	if got.Runtime.Export.DroppedBySource["utilities-west"] != 1 {
		t.Fatalf("runtime.export.dropped_by_source = %+v", got.Runtime.Export.DroppedBySource)
	}
	if got.Runtime.Export.DroppedPerMinuteBySource["utilities-west"] != 2.5 {
		t.Fatalf("runtime.export.dropped_per_minute_by_source = %+v", got.Runtime.Export.DroppedPerMinuteBySource)
	}
	if got.Runtime.Export.DroppedLast60sBySource["utilities-west"] != 1 {
		t.Fatalf("runtime.export.dropped_last_60s_by_source = %+v", got.Runtime.Export.DroppedLast60sBySource)
	}
	if got.Runtime.Export.DroppedPerMinuteLast60sBySource["utilities-west"] != 1.0 {
		t.Fatalf("runtime.export.dropped_per_minute_last_60s_by_source = %+v", got.Runtime.Export.DroppedPerMinuteLast60sBySource)
	}
	if got.Runtime.Export.QueueDepth != 4 || got.Runtime.Export.QueueCapacity != 1024 {
		t.Fatalf("runtime.export.queue = depth=%d capacity=%d", got.Runtime.Export.QueueDepth, got.Runtime.Export.QueueCapacity)
	}
	if got.Runtime.Export.QueueUtilization != 4.0/1024.0 {
		t.Fatalf("runtime.export.queue_utilization = %f", got.Runtime.Export.QueueUtilization)
	}
	if got.Runtime.Export.QueueUtilizationLast60sAvg != 0.25 || got.Runtime.Export.QueueUtilizationLast60sPeak != 0.75 {
		t.Fatalf("runtime.export.queue_trend = avg=%f peak=%f", got.Runtime.Export.QueueUtilizationLast60sAvg, got.Runtime.Export.QueueUtilizationLast60sPeak)
	}
	if got.Runtime.Export.LastEventAt.IsZero() || got.Runtime.Export.LastSendAt.IsZero() || got.Runtime.Export.LastFailureAt.IsZero() || got.Runtime.Export.TelemetryAgeSeconds != 10 {
		t.Fatalf("runtime.export.liveness = event=%v send=%v failure=%v age=%f", got.Runtime.Export.LastEventAt, got.Runtime.Export.LastSendAt, got.Runtime.Export.LastFailureAt, got.Runtime.Export.TelemetryAgeSeconds)
	}
	if got.Runtime.Export.SentLast60sTotal != 4 || got.Runtime.Export.FailedLast60sTotal != 1 {
		t.Fatalf("runtime.export.rolling_outcome_totals = sent=%d failed=%d", got.Runtime.Export.SentLast60sTotal, got.Runtime.Export.FailedLast60sTotal)
	}
	if got.Runtime.Export.SuccessRateLast60s != 0.8 || got.Runtime.Export.FailureRateLast60s != 0.2 {
		t.Fatalf("runtime.export.rolling_outcome_rates = success=%f failure=%f", got.Runtime.Export.SuccessRateLast60s, got.Runtime.Export.FailureRateLast60s)
	}
	if got.Runtime.Export.RetriedLast60sTotal != 1 || got.Runtime.Export.RetryRateLast60s != 0.2 {
		t.Fatalf("runtime.export.rolling_retry_pressure = retried=%d rate=%f", got.Runtime.Export.RetriedLast60sTotal, got.Runtime.Export.RetryRateLast60s)
	}
	if got.Runtime.Export.DroppedToAttemptsRateLast60s != 0.2 {
		t.Fatalf("runtime.export.dropped_to_attempts_rate_last_60s = %f", got.Runtime.Export.DroppedToAttemptsRateLast60s)
	}
	if got.Runtime.Export.DroppedLast60sTotal != 1 || got.Runtime.Export.DroppedPerMinuteLast60sTotal != 1.0 {
		t.Fatalf("runtime.export.rolling_drop_totals = total=%d rate=%f", got.Runtime.Export.DroppedLast60sTotal, got.Runtime.Export.DroppedPerMinuteLast60sTotal)
	}
	if got.Runtime.Export.Backends[0].SentLast60s != 4 || got.Runtime.Export.Backends[0].FailedLast60s != 1 || got.Runtime.Export.Backends[0].AttemptsLast60s != 5 || got.Runtime.Export.Backends[0].RetriedLast60s != 1 {
		t.Fatalf("runtime.export.backends rolling = %+v", got.Runtime.Export.Backends[0])
	}
	if got.Runtime.Export.Backends[0].SuccessRateLast60s != 0.8 || got.Runtime.Export.Backends[0].FailureRateLast60s != 0.2 {
		t.Fatalf("runtime.export.backends rolling ratios = %+v", got.Runtime.Export.Backends[0])
	}
}

func TestFleetSyncStatsReturns500OnProviderError(t *testing.T) {
	prov := &fakeFleetSyncProvider{statsErr: errors.New("boom")}
	ts := newFleetSyncTestServer(t, prov)
	resp, err := http.Get(ts.URL + "/api/v1/fleetsync/stats")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", resp.StatusCode)
	}
}
