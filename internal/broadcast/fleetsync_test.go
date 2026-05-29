package broadcast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	radiofleetync "github.com/MattCheramie/GopherTrunk/internal/radio/fleetync"
)

func testFleetSyncMessage() radiofleetync.Message {
	return radiofleetync.Message{
		Timestamp:  time.Unix(1735000000, 123).UTC(),
		Source:     "utilities-east",
		Version:    radiofleetync.VersionFleetSync2,
		Command:    0x02,
		Subcommand: 0x80,
		FromFleet:  7,
		FromUnit:   101,
		ToFleet:    8,
		ToUnit:     202,
		Emergency:  true,
		Payload:    []byte{0x01, 0x02},
		RawBytes:   []byte{0xAA, 0xBB},
	}
}

func TestFleetSyncWebhookSend(t *testing.T) {
	var got FleetSyncEvent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content-type=%q want application/json", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	backend, err := NewFleetSyncWebhook(FleetSyncWebhookConfig{
		URL:     srv.URL,
		Headers: map[string]string{"X-Feed": "fleetsync"},
	}, srv.Client())
	if err != nil {
		t.Fatalf("NewFleetSyncWebhook: %v", err)
	}
	if err := backend.Send(context.Background(), fleetSyncEventFromMessage(testFleetSyncMessage())); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.Source != "utilities-east" || got.PayloadHex != "0102" || got.RawHex != "AABB" {
		t.Fatalf("payload = %+v", got)
	}
}

func TestFleetSyncWebhookSendRejectsNilMessage(t *testing.T) {
	backend, err := NewFleetSyncWebhook(FleetSyncWebhookConfig{URL: "http://127.0.0.1:1"}, nil)
	if err != nil {
		t.Fatalf("NewFleetSyncWebhook: %v", err)
	}
	if err := backend.Send(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil webhook message")
	}
}

func TestFleetSyncSpoolSend(t *testing.T) {
	dir := t.TempDir()
	backend, err := NewFleetSyncSpool(FleetSyncSpoolConfig{Dir: dir})
	if err != nil {
		t.Fatalf("NewFleetSyncSpool: %v", err)
	}
	msg := fleetSyncEventFromMessage(testFleetSyncMessage())
	if err := backend.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1", len(entries))
	}
	entryDir := filepath.Join(dir, entries[0].Name())
	body, err := os.ReadFile(filepath.Join(entryDir, "message.json"))
	if err != nil {
		t.Fatalf("ReadFile message.json: %v", err)
	}
	var got FleetSyncEvent
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("Unmarshal message.json: %v", err)
	}
	if got.FromUnit != 101 || got.Command != 0x02 {
		t.Fatalf("message.json = %+v", got)
	}
	payload, err := os.ReadFile(filepath.Join(entryDir, "payload.bin"))
	if err != nil {
		t.Fatalf("ReadFile payload.bin: %v", err)
	}
	if !bytes.Equal(payload, msg.Payload) {
		t.Fatalf("payload.bin=%x want %x", payload, msg.Payload)
	}
	rawBytes, err := os.ReadFile(filepath.Join(entryDir, "raw.bin"))
	if err != nil {
		t.Fatalf("ReadFile raw.bin: %v", err)
	}
	if !bytes.Equal(rawBytes, msg.RawBytes) {
		t.Fatalf("raw.bin=%x want %x", rawBytes, msg.RawBytes)
	}
}

func TestFleetSyncSpoolSendRejectsNilMessage(t *testing.T) {
	dir := t.TempDir()
	backend, err := NewFleetSyncSpool(FleetSyncSpoolConfig{Dir: dir})
	if err != nil {
		t.Fatalf("NewFleetSyncSpool: %v", err)
	}
	if err := backend.Send(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil spool message")
	}
}

func TestFleetSyncEventFromMessageZeroTimestamp(t *testing.T) {
	msg := testFleetSyncMessage()
	msg.Timestamp = time.Time{}
	ev := fleetSyncEventFromMessage(msg)
	if ev.ReceivedAt.IsZero() {
		t.Fatal("ReceivedAt should be normalized when message timestamp is zero")
	}
}

type fakeFleetSyncBackend struct {
	name      string
	filter    sourceFilter
	failFirst int
	blockFor  time.Duration

	mu       sync.Mutex
	attempts int
	got      []*FleetSyncEvent
}

func (f *fakeFleetSyncBackend) Name() string                     { return f.name }
func (f *fakeFleetSyncBackend) AcceptsSource(source string) bool { return f.filter.Accepts(source) }
func (f *fakeFleetSyncBackend) Send(_ context.Context, msg *FleetSyncEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blockFor > 0 {
		time.Sleep(f.blockFor)
	}
	f.attempts++
	if f.attempts <= f.failFirst {
		return errors.New("transient failure")
	}
	f.got = append(f.got, msg)
	return nil
}
func (f *fakeFleetSyncBackend) delivered() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}
func (f *fakeFleetSyncBackend) tries() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

func TestFleetSyncExporterStreamsEvent(t *testing.T) {
	bus := events.NewBus(16)
	defer bus.Close()
	backend := &fakeFleetSyncBackend{name: "hook"}
	exporter, err := NewFleetSyncExporter(FleetSyncOptions{Bus: bus, Backends: []FleetSyncBackend{backend}, RetryBase: time.Millisecond})
	if err != nil {
		t.Fatalf("NewFleetSyncExporter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = exporter.Run(ctx) }()
	defer func() {
		cancel()
		_ = exporter.Close()
	}()

	bus.Publish(events.Event{Kind: events.KindFleetSyncMessage, Payload: testFleetSyncMessage()})
	waitFor(t, func() bool { return backend.delivered() == 1 })
	if backend.got[0].Source != "utilities-east" {
		t.Fatalf("source=%q want utilities-east", backend.got[0].Source)
	}
}

func TestFleetSyncExporterRespectsSourceFilter(t *testing.T) {
	bus := events.NewBus(16)
	defer bus.Close()
	backend := &fakeFleetSyncBackend{name: "hook", filter: newSourceFilter([]string{"north-yard"})}
	exporter, err := NewFleetSyncExporter(FleetSyncOptions{Bus: bus, Backends: []FleetSyncBackend{backend}, RetryBase: time.Millisecond})
	if err != nil {
		t.Fatalf("NewFleetSyncExporter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = exporter.Run(ctx) }()
	defer func() {
		cancel()
		_ = exporter.Close()
	}()

	bus.Publish(events.Event{Kind: events.KindFleetSyncMessage, Payload: testFleetSyncMessage()})
	time.Sleep(100 * time.Millisecond)
	if backend.delivered() != 0 {
		t.Fatal("backend should not receive a filtered-out source")
	}
}

func TestFleetSyncExporterRetriesTransientFailure(t *testing.T) {
	bus := events.NewBus(16)
	defer bus.Close()
	backend := &fakeFleetSyncBackend{name: "hook", failFirst: 2}
	exporter, err := NewFleetSyncExporter(FleetSyncOptions{Bus: bus, Backends: []FleetSyncBackend{backend}, RetryBase: time.Millisecond, MaxRetries: 3})
	if err != nil {
		t.Fatalf("NewFleetSyncExporter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = exporter.Run(ctx) }()
	defer func() {
		cancel()
		_ = exporter.Close()
	}()

	bus.Publish(events.Event{Kind: events.KindFleetSyncMessage, Payload: testFleetSyncMessage()})
	waitFor(t, func() bool { return backend.delivered() == 1 })
	if backend.tries() != 3 {
		t.Fatalf("attempts=%d want 3", backend.tries())
	}
	if stats := exporter.Stats(); stats.Sent["hook"] != 1 || stats.Failed["hook"] != 0 || stats.Attempts["hook"] != 3 || stats.Retried["hook"] != 2 {
		t.Fatalf("stats=%+v", stats)
	}
	if stats := exporter.Stats(); stats.SentLast60s["hook"] != 1 || stats.FailedLast60s["hook"] != 0 || stats.AttemptsLast60s["hook"] != 3 || stats.RetriedLast60s["hook"] != 2 {
		t.Fatalf("rolling stats=%+v", stats)
	}
	if stats := exporter.Stats(); stats.SentLast60sTotal != 1 || stats.FailedLast60sTotal != 0 || stats.SuccessRateLast60s != 1.0 || stats.FailureRateLast60s != 0.0 {
		t.Fatalf("rolling totals/rates=%+v", stats)
	}
	if stats := exporter.Stats(); stats.RetriedLast60sTotal != 2 || stats.RetryRateLast60s != 2.0/3.0 {
		t.Fatalf("rolling retry pressure=%+v", stats)
	}
	if stats := exporter.Stats(); stats.LastEventAt.IsZero() || stats.LastSendAt.IsZero() || stats.TelemetryAgeSeconds < 0 {
		t.Fatalf("liveness stats=%+v", stats)
	}
}

func TestFleetSyncExporterRecordsPermanentFailure(t *testing.T) {
	bus := events.NewBus(16)
	defer bus.Close()
	backend := &fakeFleetSyncBackend{name: "hook", failFirst: 10}
	exporter, err := NewFleetSyncExporter(FleetSyncOptions{Bus: bus, Backends: []FleetSyncBackend{backend}, RetryBase: time.Millisecond, MaxRetries: 2})
	if err != nil {
		t.Fatalf("NewFleetSyncExporter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = exporter.Run(ctx) }()
	defer func() {
		cancel()
		_ = exporter.Close()
	}()

	bus.Publish(events.Event{Kind: events.KindFleetSyncMessage, Payload: testFleetSyncMessage()})
	waitFor(t, func() bool {
		stats := exporter.Stats()
		return stats.Failed["hook"] == 1
	})
	stats := exporter.Stats()
	if stats.Sent["hook"] != 0 || stats.Failed["hook"] != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	if stats.Attempts["hook"] != 3 || stats.Retried["hook"] != 2 {
		t.Fatalf("retry stats=%+v", stats)
	}
	if stats.AttemptsLast60s["hook"] != 3 || stats.RetriedLast60s["hook"] != 2 || stats.FailedLast60s["hook"] != 1 {
		t.Fatalf("rolling retry stats=%+v", stats)
	}
	if stats.SentLast60sTotal != 0 || stats.FailedLast60sTotal != 1 || stats.SuccessRateLast60s != 0.0 || stats.FailureRateLast60s != 1.0 {
		t.Fatalf("rolling totals/rates=%+v", stats)
	}
	if stats.RetriedLast60sTotal != 2 || stats.RetryRateLast60s != 2.0/3.0 {
		t.Fatalf("rolling retry pressure=%+v", stats)
	}
	if stats.LastEventAt.IsZero() || stats.LastFailureAt.IsZero() || stats.TelemetryAgeSeconds < 0 {
		t.Fatalf("liveness stats=%+v", stats)
	}
}

func TestFleetSyncExporterTracksDroppedBySource(t *testing.T) {
	bus := events.NewBus(128)
	defer bus.Close()
	backend := &fakeFleetSyncBackend{name: "hook", blockFor: 40 * time.Millisecond}
	exporter, err := NewFleetSyncExporter(FleetSyncOptions{Bus: bus, Backends: []FleetSyncBackend{backend}, Workers: 1, RetryBase: time.Millisecond})
	if err != nil {
		t.Fatalf("NewFleetSyncExporter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = exporter.Run(ctx) }()
	defer func() {
		cancel()
		_ = exporter.Close()
	}()

	msg := testFleetSyncMessage()
	for i := 0; i < 1200; i++ {
		m := msg
		if i%2 == 0 {
			m.Source = "utilities-east"
		} else {
			m.Source = "utilities-west"
		}
		bus.Publish(events.Event{Kind: events.KindFleetSyncMessage, Payload: m})
	}

	waitFor(t, func() bool {
		stats := exporter.Stats()
		return stats.Dropped > 0
	})
	stats := exporter.Stats()
	if stats.DroppedBySource["utilities-east"] == 0 || stats.DroppedBySource["utilities-west"] == 0 {
		t.Fatalf("dropped_by_source=%+v dropped=%d", stats.DroppedBySource, stats.Dropped)
	}
	if stats.QueueCapacity <= 0 {
		t.Fatalf("queue_capacity=%d", stats.QueueCapacity)
	}
	if stats.QueueDepth < 0 || stats.QueueDepth > stats.QueueCapacity {
		t.Fatalf("queue_depth=%d queue_capacity=%d", stats.QueueDepth, stats.QueueCapacity)
	}
	if stats.QueueUtilization < 0 || stats.QueueUtilization > 1 {
		t.Fatalf("queue_utilization=%f", stats.QueueUtilization)
	}
	if stats.QueueUtilizationLast60sAvg < 0 || stats.QueueUtilizationLast60sAvg > 1 {
		t.Fatalf("queue_utilization_last_60s_avg=%f", stats.QueueUtilizationLast60sAvg)
	}
	if stats.QueueUtilizationLast60sPeak < 0 || stats.QueueUtilizationLast60sPeak > 1 || stats.QueueUtilizationLast60sPeak < stats.QueueUtilizationLast60sAvg {
		t.Fatalf("queue_utilization_last_60s_peak=%f avg=%f", stats.QueueUtilizationLast60sPeak, stats.QueueUtilizationLast60sAvg)
	}
	if stats.DroppedPerMinuteBySource["utilities-east"] <= 0 || stats.DroppedPerMinuteBySource["utilities-west"] <= 0 {
		t.Fatalf("dropped_per_minute_by_source=%+v", stats.DroppedPerMinuteBySource)
	}
	if stats.DroppedLast60sBySource["utilities-east"] <= 0 || stats.DroppedLast60sBySource["utilities-west"] <= 0 {
		t.Fatalf("dropped_last_60s_by_source=%+v", stats.DroppedLast60sBySource)
	}
	if stats.DroppedPerMinuteLast60sBySource["utilities-east"] <= 0 || stats.DroppedPerMinuteLast60sBySource["utilities-west"] <= 0 {
		t.Fatalf("dropped_per_minute_last_60s_by_source=%+v", stats.DroppedPerMinuteLast60sBySource)
	}
	if stats.DroppedLast60sTotal <= 0 {
		t.Fatalf("dropped_last_60s_total=%d", stats.DroppedLast60sTotal)
	}
	if stats.DroppedPerMinuteLast60sTotal <= 0 {
		t.Fatalf("dropped_per_minute_last_60s_total=%f", stats.DroppedPerMinuteLast60sTotal)
	}
}

func TestNewFleetSyncExporterRequiresBus(t *testing.T) {
	if _, err := NewFleetSyncExporter(FleetSyncOptions{}); err == nil {
		t.Fatal("expected error for missing bus")
	}
}
