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

	backend, err := NewFleetSyncWebhook(FleetSyncWebhookConfig{URL: srv.URL}, srv.Client())
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

type fakeFleetSyncBackend struct {
	name      string
	filter    sourceFilter
	failFirst int

	mu       sync.Mutex
	attempts int
	got      []*FleetSyncEvent
}

func (f *fakeFleetSyncBackend) Name() string                     { return f.name }
func (f *fakeFleetSyncBackend) AcceptsSource(source string) bool { return f.filter.Accepts(source) }
func (f *fakeFleetSyncBackend) Send(_ context.Context, msg *FleetSyncEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	if stats := exporter.Stats(); stats.Sent["hook"] != 1 || stats.Failed["hook"] != 0 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestNewFleetSyncExporterRequiresBus(t *testing.T) {
	if _, err := NewFleetSyncExporter(FleetSyncOptions{}); err == nil {
		t.Fatal("expected error for missing bus")
	}
}
