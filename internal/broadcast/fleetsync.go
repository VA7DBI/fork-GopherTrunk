package broadcast

import (
	"bytes"
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
	receivedAt := msg.Timestamp
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	return &FleetSyncEvent{
		ReceivedAt: receivedAt,
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
	Queued                          int                `json:"queued"`
	Dropped                         int                `json:"dropped"`
	LastEventAt                     time.Time          `json:"last_event_at,omitempty"`
	LastSendAt                      time.Time          `json:"last_send_at,omitempty"`
	LastFailureAt                   time.Time          `json:"last_failure_at,omitempty"`
	TelemetryAgeSeconds             float64            `json:"telemetry_age_seconds,omitempty"`
	QueueDepth                      int                `json:"queue_depth"`
	QueueCapacity                   int                `json:"queue_capacity"`
	QueueUtilization                float64            `json:"queue_utilization"`
	QueueUtilizationLast60sAvg      float64            `json:"queue_utilization_last_60s_avg,omitempty"`
	QueueUtilizationLast60sPeak     float64            `json:"queue_utilization_last_60s_peak,omitempty"`
	DroppedBySource                 map[string]int     `json:"dropped_by_source"`
	DroppedPerMinuteBySource        map[string]float64 `json:"dropped_per_minute_by_source,omitempty"`
	DroppedLast60sTotal             int                `json:"dropped_last_60s_total,omitempty"`
	DroppedPerMinuteLast60sTotal    float64            `json:"dropped_per_minute_last_60s_total,omitempty"`
	DroppedLast60sBySource          map[string]int     `json:"dropped_last_60s_by_source,omitempty"`
	DroppedPerMinuteLast60sBySource map[string]float64 `json:"dropped_per_minute_last_60s_by_source,omitempty"`
	Sent                            map[string]int     `json:"sent"`
	SentLast60s                     map[string]int     `json:"sent_last_60s,omitempty"`
	SentLast60sTotal                int                `json:"sent_last_60s_total,omitempty"`
	Failed                          map[string]int     `json:"failed"`
	FailedLast60s                   map[string]int     `json:"failed_last_60s,omitempty"`
	FailedLast60sTotal              int                `json:"failed_last_60s_total,omitempty"`
	SuccessRateLast60s              float64            `json:"success_rate_last_60s,omitempty"`
	FailureRateLast60s              float64            `json:"failure_rate_last_60s,omitempty"`
	Attempts                        map[string]int     `json:"attempts"`
	AttemptsLast60s                 map[string]int     `json:"attempts_last_60s,omitempty"`
	Retried                         map[string]int     `json:"retried"`
	RetriedLast60s                  map[string]int     `json:"retried_last_60s,omitempty"`
	RetriedLast60sTotal             int                `json:"retried_last_60s_total,omitempty"`
	RetryRateLast60s                float64            `json:"retry_rate_last_60s,omitempty"`
	DroppedToAttemptsRateLast60s    float64            `json:"dropped_to_attempts_rate_last_60s,omitempty"`
	Backends                        []string           `json:"backends"`
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

	mu                  sync.Mutex
	startedAt           time.Time
	queued              int
	dropped             int
	lastEventAt         time.Time
	lastSendAt          time.Time
	lastFailureAt       time.Time
	recentQueueSamples  []queueUtilizationSample
	droppedBySource     map[string]int
	recentDropsBySource map[string][]time.Time
	sent                map[string]int
	recentSent          map[string][]time.Time
	failed              map[string]int
	recentFailed        map[string][]time.Time
	attempts            map[string]int
	recentAttempts      map[string][]time.Time
	retried             map[string]int
	recentRetried       map[string][]time.Time
}

type queueUtilizationSample struct {
	at          time.Time
	utilization float64
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
		bus:                 opts.Bus,
		log:                 opts.Log,
		backends:            opts.Backends,
		maxRetries:          opts.MaxRetries,
		retryBase:           opts.RetryBase,
		startedAt:           time.Now(),
		sub:                 opts.Bus.Subscribe(),
		jobs:                make(chan *FleetSyncEvent, defaultQueueDepth),
		runDone:             make(chan struct{}),
		droppedBySource:     make(map[string]int),
		recentDropsBySource: make(map[string][]time.Time),
		sent:                make(map[string]int),
		recentSent:          make(map[string][]time.Time),
		failed:              make(map[string]int),
		recentFailed:        make(map[string][]time.Time),
		attempts:            make(map[string]int),
		recentAttempts:      make(map[string][]time.Time),
		retried:             make(map[string]int),
		recentRetried:       make(map[string][]time.Time),
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
		f.lastEventAt = time.Now().UTC()
		f.queued++
		f.recordQueueUtilizationLocked(time.Now())
		f.mu.Unlock()
	default:
		f.mu.Lock()
		f.lastEventAt = time.Now().UTC()
		f.dropped++
		f.droppedBySource[msg.Source]++
		now := time.Now()
		f.recentDropsBySource[msg.Source] = append(f.recentDropsBySource[msg.Source], now)
		f.pruneRecentDropsLocked(now.Add(-60 * time.Second))
		f.recordQueueUtilizationLocked(now)
		f.mu.Unlock()
		f.log.Warn("broadcast/fleetsync: export queue full, dropping message",
			"source", msg.Source, "command", msg.Command, "from_unit", msg.FromUnit)
	}
}

func (f *FleetSyncExporter) pruneRecentDropsLocked(cutoff time.Time) {
	for source, stamps := range f.recentDropsBySource {
		idx := 0
		for idx < len(stamps) && stamps[idx].Before(cutoff) {
			idx++
		}
		if idx == len(stamps) {
			delete(f.recentDropsBySource, source)
			continue
		}
		if idx > 0 {
			f.recentDropsBySource[source] = append([]time.Time(nil), stamps[idx:]...)
		}
	}
}

func (f *FleetSyncExporter) worker() {
	defer f.wg.Done()
	for msg := range f.jobs {
		f.mu.Lock()
		f.recordQueueUtilizationLocked(time.Now())
		f.mu.Unlock()
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
		now := time.Now()
		f.mu.Lock()
		f.attempts[backend.Name()]++
		f.recentAttempts[backend.Name()] = append(f.recentAttempts[backend.Name()], now)
		if attempt > 0 {
			f.retried[backend.Name()]++
			f.recentRetried[backend.Name()] = append(f.recentRetried[backend.Name()], now)
		}
		f.pruneRecentBackendLocked(now.Add(-60 * time.Second))
		f.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := backend.Send(ctx, msg)
		cancel()
		if err == nil {
			now = time.Now()
			f.mu.Lock()
			f.lastSendAt = now.UTC()
			f.sent[backend.Name()]++
			f.recentSent[backend.Name()] = append(f.recentSent[backend.Name()], now)
			f.pruneRecentBackendLocked(now.Add(-60 * time.Second))
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
	now := time.Now()
	f.mu.Lock()
	f.lastFailureAt = now.UTC()
	f.failed[backend.Name()]++
	f.recentFailed[backend.Name()] = append(f.recentFailed[backend.Name()], now)
	f.pruneRecentBackendLocked(now.Add(-60 * time.Second))
	f.mu.Unlock()
}

func (f *FleetSyncExporter) Stats() FleetSyncStats {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := FleetSyncStats{
		Queued:                          f.queued,
		Dropped:                         f.dropped,
		LastEventAt:                     f.lastEventAt,
		LastSendAt:                      f.lastSendAt,
		LastFailureAt:                   f.lastFailureAt,
		QueueDepth:                      len(f.jobs),
		QueueCapacity:                   cap(f.jobs),
		DroppedBySource:                 make(map[string]int, len(f.droppedBySource)),
		DroppedPerMinuteBySource:        make(map[string]float64, len(f.droppedBySource)),
		DroppedLast60sBySource:          make(map[string]int, len(f.recentDropsBySource)),
		DroppedPerMinuteLast60sBySource: make(map[string]float64, len(f.recentDropsBySource)),
		Sent:                            make(map[string]int, len(f.sent)),
		SentLast60s:                     make(map[string]int, len(f.recentSent)),
		Failed:                          make(map[string]int, len(f.failed)),
		FailedLast60s:                   make(map[string]int, len(f.recentFailed)),
		Attempts:                        make(map[string]int, len(f.attempts)),
		AttemptsLast60s:                 make(map[string]int, len(f.recentAttempts)),
		Retried:                         make(map[string]int, len(f.retried)),
		RetriedLast60s:                  make(map[string]int, len(f.recentRetried)),
	}
	mins := time.Since(f.startedAt).Minutes()
	if mins <= 0 {
		mins = 1.0 / 60.0
	}
	if out.QueueCapacity > 0 {
		out.QueueUtilization = float64(out.QueueDepth) / float64(out.QueueCapacity)
	}
	now := time.Now()
	f.pruneRecentDropsLocked(now.Add(-60 * time.Second))
	f.pruneRecentBackendLocked(now.Add(-60 * time.Second))
	f.recordQueueUtilizationLocked(now)
	for key, value := range f.droppedBySource {
		out.DroppedBySource[key] = value
		out.DroppedPerMinuteBySource[key] = float64(value) / mins
	}
	for key, recent := range f.recentDropsBySource {
		out.DroppedLast60sTotal += len(recent)
		out.DroppedLast60sBySource[key] = len(recent)
		out.DroppedPerMinuteLast60sBySource[key] = float64(len(recent))
	}
	out.DroppedPerMinuteLast60sTotal = float64(out.DroppedLast60sTotal)
	for key, value := range f.sent {
		out.Sent[key] = value
	}
	for key, recent := range f.recentSent {
		out.SentLast60s[key] = len(recent)
		out.SentLast60sTotal += len(recent)
	}
	for key, value := range f.failed {
		out.Failed[key] = value
	}
	for key, recent := range f.recentFailed {
		out.FailedLast60s[key] = len(recent)
		out.FailedLast60sTotal += len(recent)
	}
	for key, value := range f.attempts {
		out.Attempts[key] = value
	}
	for key, recent := range f.recentAttempts {
		out.AttemptsLast60s[key] = len(recent)
	}
	attemptsLast60sTotal := 0
	for _, count := range out.AttemptsLast60s {
		attemptsLast60sTotal += count
	}
	for key, value := range f.retried {
		out.Retried[key] = value
	}
	for key, recent := range f.recentRetried {
		out.RetriedLast60s[key] = len(recent)
		out.RetriedLast60sTotal += len(recent)
	}
	for _, backend := range f.backends {
		out.Backends = append(out.Backends, backend.Name())
	}
	rollingOutcomes := out.SentLast60sTotal + out.FailedLast60sTotal
	if rollingOutcomes > 0 {
		out.SuccessRateLast60s = float64(out.SentLast60sTotal) / float64(rollingOutcomes)
		out.FailureRateLast60s = float64(out.FailedLast60sTotal) / float64(rollingOutcomes)
	}
	if attemptsLast60sTotal > 0 {
		out.RetryRateLast60s = float64(out.RetriedLast60sTotal) / float64(attemptsLast60sTotal)
		out.DroppedToAttemptsRateLast60s = float64(out.DroppedLast60sTotal) / float64(attemptsLast60sTotal)
	}
	if len(f.recentQueueSamples) > 0 {
		sum := 0.0
		peak := 0.0
		for _, sample := range f.recentQueueSamples {
			sum += sample.utilization
			if sample.utilization > peak {
				peak = sample.utilization
			}
		}
		out.QueueUtilizationLast60sAvg = sum / float64(len(f.recentQueueSamples))
		out.QueueUtilizationLast60sPeak = peak
	}
	latest := out.LastEventAt
	if out.LastSendAt.After(latest) {
		latest = out.LastSendAt
	}
	if out.LastFailureAt.After(latest) {
		latest = out.LastFailureAt
	}
	if !latest.IsZero() {
		out.TelemetryAgeSeconds = time.Since(latest).Seconds()
	}
	return out
}

func (f *FleetSyncExporter) pruneRecentBackendLocked(cutoff time.Time) {
	pruneTimestampMapLocked(f.recentSent, cutoff)
	pruneTimestampMapLocked(f.recentFailed, cutoff)
	pruneTimestampMapLocked(f.recentAttempts, cutoff)
	pruneTimestampMapLocked(f.recentRetried, cutoff)
}

func (f *FleetSyncExporter) recordQueueUtilizationLocked(now time.Time) {
	utilization := 0.0
	if cap(f.jobs) > 0 {
		utilization = float64(len(f.jobs)) / float64(cap(f.jobs))
	}
	f.recentQueueSamples = append(f.recentQueueSamples, queueUtilizationSample{at: now, utilization: utilization})
	cutoff := now.Add(-60 * time.Second)
	idx := 0
	for idx < len(f.recentQueueSamples) && f.recentQueueSamples[idx].at.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		f.recentQueueSamples = append([]queueUtilizationSample(nil), f.recentQueueSamples[idx:]...)
	}
}

func pruneTimestampMapLocked(values map[string][]time.Time, cutoff time.Time) {
	for key, stamps := range values {
		idx := 0
		for idx < len(stamps) && stamps[idx].Before(cutoff) {
			idx++
		}
		if idx == len(stamps) {
			delete(values, key)
			continue
		}
		if idx > 0 {
			values[key] = append([]time.Time(nil), stamps[idx:]...)
		}
	}
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
	if msg == nil {
		return errors.New("broadcast/fleetsync: webhook message is nil")
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("broadcast/fleetsync: marshal webhook payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.url, bytes.NewReader(body))
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
	if msg == nil {
		return errors.New("broadcast/fleetsync: spool message is nil")
	}
	receivedAt := msg.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	entryDir := filepath.Join(b.dir, fmt.Sprintf("fleetync-%d-cmd%02X-unit%d", receivedAt.UnixNano(), msg.Command, msg.FromUnit))
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
