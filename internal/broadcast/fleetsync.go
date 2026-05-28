package broadcast

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	radiofleetync "github.com/MattCheramie/GopherTrunk/internal/radio/fleetync"
)

// FleetSyncEvent is the normalized export payload for one decoded
// FleetSync frame.
type FleetSyncEvent struct {
	ReceivedAt time.Time `json:"received_at"`
	Source     string    `json:"source,omitempty"`
	Version    uint8     `json:"version"`
	Command    uint8     `json:"command"`
	Subcommand uint8     `json:"subcommand"`
	FromFleet  uint8     `json:"from_fleet"`
	FromUnit   uint16    `json:"from_unit"`
	ToFleet    uint8     `json:"to_fleet"`
	ToUnit     uint16    `json:"to_unit"`
	AllFlag    bool      `json:"all_flag"`
	Emergency  bool      `json:"emergency"`
	Priority   bool      `json:"priority"`
	PayloadHex string    `json:"payload_hex"`
	RawHex     string    `json:"raw_hex"`
	Payload    []byte    `json:"-"`
	RawBytes   []byte    `json:"-"`
}

func fleetSyncEventFromMessage(msg radiofleetync.Message) *FleetSyncEvent {
	payload := append([]byte(nil), msg.Payload...)
	rawBytes := append([]byte(nil), msg.RawBytes...)
	return &FleetSyncEvent{
		ReceivedAt: msg.Timestamp,
		Source:     msg.Source,
		Version:    uint8(msg.Version),
		Command:    msg.Command,
		Subcommand: msg.Subcommand,
		FromFleet:  msg.FromFleet,
		FromUnit:   msg.FromUnit,
		ToFleet:    msg.ToFleet,
		ToUnit:     msg.ToUnit,
		AllFlag:    msg.AllFlag,
		Emergency:  msg.Emergency,
		Priority:   msg.Priority,
		PayloadHex: strings.ToUpper(hex.EncodeToString(payload)),
		RawHex:     strings.ToUpper(hex.EncodeToString(rawBytes)),
		Payload:    payload,
		RawBytes:   rawBytes,
	}
}

// FleetSyncBackend is one outbound FleetSync export destination.
type FleetSyncBackend interface {
	Name() string
	AcceptsSource(source string) bool
	Send(ctx context.Context, msg *FleetSyncEvent) error
}

type sourceFilter struct {
	sources map[string]bool
}

func newSourceFilter(values []string) sourceFilter {
	if len(values) == 0 {
		return sourceFilter{}
	}
	out := make(map[string]bool, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out[trimmed] = true
		}
	}
	if len(out) == 0 {
		return sourceFilter{}
	}
	return sourceFilter{sources: out}
}

func (f sourceFilter) Accepts(source string) bool {
	if f.sources == nil {
		return true
	}
	return f.sources[source]
}

// FleetSyncOptions configure a FleetSyncExporter.
type FleetSyncOptions struct {
	Bus        *events.Bus
	Log        *slog.Logger
	Backends   []FleetSyncBackend
	Workers    int
	MaxRetries int
	RetryBase  time.Duration
}

// FleetSyncStats is a point-in-time snapshot of exporter counters.
type FleetSyncStats struct {
	Queued   int            `json:"queued"`
	Dropped  int            `json:"dropped"`
	Sent     map[string]int `json:"sent"`
	Failed   map[string]int `json:"failed"`
	Backends []string       `json:"backends"`
}

// FleetSyncExporter fans decoded FleetSync frames out to outbound
// webhook and file-spool backends.
type FleetSyncExporter struct {
	bus        *events.Bus
	log        *slog.Logger
	backends   []FleetSyncBackend
	maxRetries int
	retryBase  time.Duration

	sub       *events.Subscription
	jobs      chan *FleetSyncEvent
	wg        sync.WaitGroup
	runDone   chan struct{}
	closeOnce sync.Once

	mu      sync.Mutex
	queued  int
	dropped int
	sent    map[string]int
	failed  map[string]int
}

func NewFleetSyncExporter(opts FleetSyncOptions) (*FleetSyncExporter, error) {
	if opts.Bus == nil {
		return nil, errors.New("broadcast/fleetsync: events.Bus is required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Workers <= 0 {
		opts.Workers = defaultWorkers
	}
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = defaultMaxRetries
	}
	if opts.RetryBase <= 0 {
		opts.RetryBase = defaultRetryBase
	}
	f := &FleetSyncExporter{
		bus:        opts.Bus,
		log:        opts.Log,
		backends:   opts.Backends,
		maxRetries: opts.MaxRetries,
		retryBase:  opts.RetryBase,
		sub:        opts.Bus.Subscribe(),
		jobs:       make(chan *FleetSyncEvent, defaultQueueDepth),
		runDone:    make(chan struct{}),
		sent:       make(map[string]int),
		failed:     make(map[string]int),
	}
	for i := 0; i < opts.Workers; i++ {
		f.wg.Add(1)
		go f.worker()
	}
	return f, nil
}

func (f *FleetSyncExporter) Backends() int { return len(f.backends) }

func (f *FleetSyncExporter) Run(ctx context.Context) error {
	defer close(f.runDone)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-f.sub.C:
			if !ok {
				return nil
			}
			if ev.Kind != events.KindFleetSyncMessage {
				continue
			}
			switch msg := ev.Payload.(type) {
			case radiofleetync.Message:
				f.dispatch(fleetSyncEventFromMessage(msg))
			case *radiofleetync.Message:
				if msg != nil {
					f.dispatch(fleetSyncEventFromMessage(*msg))
				}
			}
		}
	}
}

func (f *FleetSyncExporter) dispatch(msg *FleetSyncEvent) {
	if msg == nil || len(f.backends) == 0 {
		return
	}
	wanted := false
	for _, backend := range f.backends {
		if backend.AcceptsSource(msg.Source) {
			wanted = true
			break
		}
	}
	if !wanted {
		return
	}
	select {
	case f.jobs <- msg:
		f.mu.Lock()
		f.queued++
		f.mu.Unlock()
	default:
		f.mu.Lock()
		f.dropped++
		f.mu.Unlock()
		f.log.Warn("broadcast/fleetsync: export queue full, dropping message",
			"source", msg.Source, "command", msg.Command, "from_unit", msg.FromUnit)
	}
}

func (f *FleetSyncExporter) worker() {
	defer f.wg.Done()
	for msg := range f.jobs {
		for _, backend := range f.backends {
			if !backend.AcceptsSource(msg.Source) {
				continue
			}
			f.sendWithRetry(backend, msg)
		}
	}
}

func (f *FleetSyncExporter) sendWithRetry(backend FleetSyncBackend, msg *FleetSyncEvent) {
	backoff := f.retryBase
	for attempt := 0; attempt <= f.maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := backend.Send(ctx, msg)
		cancel()
		if err == nil {
			f.mu.Lock()
			f.sent[backend.Name()]++
			f.mu.Unlock()
			return
		}
		f.log.Warn("broadcast/fleetsync: export failed",
			"backend", backend.Name(), "source", msg.Source,
			"command", msg.Command, "attempt", attempt+1,
			"of", f.maxRetries+1, "err", err)
		if attempt < f.maxRetries {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	f.mu.Lock()
	f.failed[backend.Name()]++
	f.mu.Unlock()
}

func (f *FleetSyncExporter) Stats() FleetSyncStats {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := FleetSyncStats{
		Queued:  f.queued,
		Dropped: f.dropped,
		Sent:    make(map[string]int, len(f.sent)),
		Failed:  make(map[string]int, len(f.failed)),
	}
	for key, value := range f.sent {
		out.Sent[key] = value
	}
	for key, value := range f.failed {
		out.Failed[key] = value
	}
	for _, backend := range f.backends {
		out.Backends = append(out.Backends, backend.Name())
	}
	return out
}

func (f *FleetSyncExporter) Close() error {
	f.closeOnce.Do(func() {
		f.sub.Close()
		select {
		case <-f.runDone:
		case <-time.After(2 * time.Second):
		}
		close(f.jobs)
		f.wg.Wait()
	})
	return nil
}

// FleetSyncWebhookConfig is one outbound JSON webhook.
type FleetSyncWebhookConfig struct {
	Name    string
	URL     string
	Sources []string
	Headers map[string]string
}

type fleetSyncWebhookBackend struct {
	name    string
	url     string
	headers map[string]string
	filter  sourceFilter
	client  *http.Client
}

func NewFleetSyncWebhook(cfg FleetSyncWebhookConfig, client *http.Client) (FleetSyncBackend, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, errors.New("broadcast/fleetsync: webhook url is required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "fleetsync-webhook"
	}
	return &fleetSyncWebhookBackend{
		name:    name,
		url:     cfg.URL,
		headers: cfg.Headers,
		filter:  newSourceFilter(cfg.Sources),
		client:  client,
	}, nil
}

func (b *fleetSyncWebhookBackend) Name() string                     { return b.name }
func (b *fleetSyncWebhookBackend) AcceptsSource(source string) bool { return b.filter.Accepts(source) }

func (b *fleetSyncWebhookBackend) Send(ctx context.Context, msg *FleetSyncEvent) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("broadcast/fleetsync: marshal webhook payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.url, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("broadcast/fleetsync: new webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range b.headers {
		req.Header.Set(key, value)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("broadcast/fleetsync: webhook status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// FleetSyncSpoolConfig is one disk spool target.
type FleetSyncSpoolConfig struct {
	Name    string
	Dir     string
	Sources []string
}

type fleetSyncSpoolBackend struct {
	name   string
	dir    string
	filter sourceFilter
}

func NewFleetSyncSpool(cfg FleetSyncSpoolConfig) (FleetSyncBackend, error) {
	if strings.TrimSpace(cfg.Dir) == "" {
		return nil, errors.New("broadcast/fleetsync: spool dir is required")
	}
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "fleetsync-spool"
	}
	return &fleetSyncSpoolBackend{name: name, dir: cfg.Dir, filter: newSourceFilter(cfg.Sources)}, nil
}

func (b *fleetSyncSpoolBackend) Name() string                     { return b.name }
func (b *fleetSyncSpoolBackend) AcceptsSource(source string) bool { return b.filter.Accepts(source) }

func (b *fleetSyncSpoolBackend) Send(_ context.Context, msg *FleetSyncEvent) error {
	entryDir := filepath.Join(b.dir, fmt.Sprintf("fleetync-%d-cmd%02X-unit%d", msg.ReceivedAt.UnixNano(), msg.Command, msg.FromUnit))
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		return fmt.Errorf("broadcast/fleetsync: mkdir %s: %w", entryDir, err)
	}
	jsonBody, err := json.MarshalIndent(msg, "", "  ")
	if err != nil {
		return fmt.Errorf("broadcast/fleetsync: marshal spool payload: %w", err)
	}
	if err := os.WriteFile(filepath.Join(entryDir, "message.json"), append(jsonBody, '\n'), 0o644); err != nil {
		return fmt.Errorf("broadcast/fleetsync: write message.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(entryDir, "payload.bin"), msg.Payload, 0o644); err != nil {
		return fmt.Errorf("broadcast/fleetsync: write payload.bin: %w", err)
	}
	if err := os.WriteFile(filepath.Join(entryDir, "raw.bin"), msg.RawBytes, 0o644); err != nil {
		return fmt.Errorf("broadcast/fleetsync: write raw.bin: %w", err)
	}
	return nil
}
