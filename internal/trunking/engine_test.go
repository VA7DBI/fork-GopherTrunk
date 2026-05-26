package trunking

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

type testBusCollector struct {
	mu sync.Mutex
	xs []events.Event
}

func (c *testBusCollector) drain(sub *events.Subscription, until int, deadline time.Duration) []events.Event {
	t := time.NewTimer(deadline)
	defer t.Stop()
	for {
		c.mu.Lock()
		n := len(c.xs)
		c.mu.Unlock()
		if n >= until {
			break
		}
		select {
		case ev, ok := <-sub.C:
			if !ok {
				break
			}
			c.mu.Lock()
			c.xs = append(c.xs, ev)
			c.mu.Unlock()
		case <-t.C:
			return c.snapshot()
		}
	}
	return c.snapshot()
}

func (c *testBusCollector) snapshot() []events.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]events.Event, len(c.xs))
	copy(out, c.xs)
	return out
}

func mkEngine(t *testing.T, devices int) (*Engine, *VoicePool, *events.Bus, []*fakeVoiceTuner) {
	t.Helper()
	bus := events.NewBus(32)
	pool, tuners := mkPool(devices)
	e, err := NewEngine(EngineOptions{
		Bus:         bus,
		VoicePool:   pool,
		Talkgroups:  NewTalkgroupDB(),
		CallTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e, pool, bus, tuners
}

func TestEngineStartsCallOnGrant(t *testing.T) {
	e, _, bus, tuners := mkEngine(t, 1)
	defer bus.Close()

	sub := bus.Subscribe()
	defer sub.Close()
	collector := &testBusCollector{}
	go func() { collector.drain(sub, 1, 500*time.Millisecond) }()

	g := Grant{System: "X", Protocol: "p25", GroupID: 100, FrequencyHz: 851_000_000}
	e.HandleGrant(g)

	evs := collector.drain(sub, 1, 500*time.Millisecond)
	if len(evs) == 0 || evs[0].Kind != events.KindCallStart {
		t.Fatalf("events = %+v", evs)
	}
	cs, ok := evs[0].Payload.(CallStart)
	if !ok || cs.Grant.GroupID != 100 || cs.DeviceSerial != "A-voice" {
		t.Errorf("CallStart payload = %+v", evs[0].Payload)
	}
	if got := tuners[0].tuned(); len(got) != 1 || got[0] != 851_000_000 {
		t.Errorf("device retune = %v", got)
	}
}

func TestEngineTalkgroupForDevice(t *testing.T) {
	e, _, bus, _ := mkEngine(t, 1)
	defer bus.Close()
	e.talkgroups.Add(&TalkGroup{ID: 100, AlphaTag: "OPS", Mute: true})

	if e.TalkgroupForDevice("A-voice") != nil {
		t.Fatal("no call active yet — TalkgroupForDevice should be nil")
	}
	e.HandleGrant(Grant{System: "X", Protocol: "p25", GroupID: 100, FrequencyHz: 851_000_000})

	tg := e.TalkgroupForDevice("A-voice")
	if tg == nil || tg.ID != 100 || !tg.Mute {
		t.Fatalf("TalkgroupForDevice = %+v, want the muted OPS talkgroup", tg)
	}
	if e.TalkgroupForDevice("no-such-device") != nil {
		t.Error("unknown device should return nil")
	}
}

func TestEngineDropsLockedOutGrant(t *testing.T) {
	e, _, bus, tuners := mkEngine(t, 1)
	defer bus.Close()
	e.talkgroups.Add(&TalkGroup{ID: 50, AlphaTag: "BLOCKED", Lockout: true})

	sub := bus.Subscribe()
	defer sub.Close()

	e.HandleGrant(Grant{System: "X", Protocol: "p25", GroupID: 50, FrequencyHz: 1_000_000})

	select {
	case ev := <-sub.C:
		t.Errorf("unexpected event for locked-out grant: %s", ev.Kind)
	case <-time.After(50 * time.Millisecond):
	}
	if got := tuners[0].tuned(); len(got) != 0 {
		t.Errorf("locked-out grant should not retune; got %v", got)
	}
}

func TestEngineScanModeListDropsTGOutsideList(t *testing.T) {
	e, _, bus, tuners := mkEngine(t, 1)
	defer bus.Close()
	e.SetScanMode(ScanModeList)
	// TG with Scan=false should be dropped.
	e.talkgroups.Add(&TalkGroup{ID: 77, AlphaTag: "OFF-LIST", Scan: false})

	sub := bus.Subscribe()
	defer sub.Close()
	e.HandleGrant(Grant{System: "X", Protocol: "p25", GroupID: 77, FrequencyHz: 1_000_000})

	select {
	case ev := <-sub.C:
		t.Errorf("unexpected event for off-list grant: %s", ev.Kind)
	case <-time.After(50 * time.Millisecond):
	}
	if got := tuners[0].tuned(); len(got) != 0 {
		t.Errorf("off-list grant should not retune; got %v", got)
	}
}

func TestEngineScanModeListAllowsScanTrueTG(t *testing.T) {
	e, _, bus, _ := mkEngine(t, 1)
	defer bus.Close()
	e.SetScanMode(ScanModeList)
	e.talkgroups.Add(&TalkGroup{ID: 78, AlphaTag: "ON-LIST", Scan: true})

	sub := bus.Subscribe()
	defer sub.Close()
	e.HandleGrant(Grant{System: "X", Protocol: "p25", GroupID: 78, FrequencyHz: 1_000_000})

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindCallStart {
				return
			}
		case <-deadline:
			t.Fatal("no CallStart published for on-list grant")
		}
	}
}

func TestEngineScanModeListBypassedByEmergency(t *testing.T) {
	e, _, bus, _ := mkEngine(t, 1)
	defer bus.Close()
	e.SetScanMode(ScanModeList)
	// Off-list (Scan=false) but Emergency — must still fire.
	e.talkgroups.Add(&TalkGroup{ID: 79, AlphaTag: "OFF-LIST-EMER", Scan: false})

	sub := bus.Subscribe()
	defer sub.Close()
	e.HandleGrant(Grant{System: "X", Protocol: "p25", GroupID: 79, FrequencyHz: 1_000_000, Emergency: true})

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindCallStart {
				return
			}
		case <-deadline:
			t.Fatal("Emergency grant should bypass scan-list gate")
		}
	}
}

func TestEngineScanModeListUnknownTGDropped(t *testing.T) {
	// TG not in the DB at all (Lookup returns nil) → dropped.
	e, _, bus, _ := mkEngine(t, 1)
	defer bus.Close()
	e.SetScanMode(ScanModeList)

	sub := bus.Subscribe()
	defer sub.Close()
	e.HandleGrant(Grant{System: "X", Protocol: "p25", GroupID: 999, FrequencyHz: 1_000_000})

	select {
	case ev := <-sub.C:
		t.Errorf("unexpected event for unknown TG: %s", ev.Kind)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEngineScanModeAllIgnoresScanFlag(t *testing.T) {
	// Default mode (all): a TG with Scan=false still fires.
	e, _, bus, _ := mkEngine(t, 1)
	defer bus.Close()
	e.talkgroups.Add(&TalkGroup{ID: 80, AlphaTag: "OFF-LIST", Scan: false})

	sub := bus.Subscribe()
	defer sub.Close()
	e.HandleGrant(Grant{System: "X", Protocol: "p25", GroupID: 80, FrequencyHz: 1_000_000})

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindCallStart {
				return
			}
		case <-deadline:
			t.Fatal("ScanModeAll should ignore Scan flag")
		}
	}
}

func TestEngineDropsZeroFrequencyGrant(t *testing.T) {
	e, _, bus, _ := mkEngine(t, 1)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	e.HandleGrant(Grant{System: "X", GroupID: 100, FrequencyHz: 0})

	select {
	case ev := <-sub.C:
		t.Errorf("unexpected event: %s", ev.Kind)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEnginePreemptsLowerPriority(t *testing.T) {
	e, _, bus, _ := mkEngine(t, 1)
	defer bus.Close()
	e.talkgroups.Add(&TalkGroup{ID: 100, Priority: 5, AlphaTag: "LOW"})
	e.talkgroups.Add(&TalkGroup{ID: 200, Priority: 1, AlphaTag: "HIGH"})

	sub := bus.Subscribe()
	defer sub.Close()
	collector := &testBusCollector{}

	low := Grant{System: "X", Protocol: "p25", GroupID: 100, FrequencyHz: 851_000_000}
	high := Grant{System: "X", Protocol: "p25", GroupID: 200, FrequencyHz: 852_000_000}

	// HandleGrant publishes synchronously (Bus.Publish is buffered at 32
	// here), so events land on the subscription channel before we drain.
	e.HandleGrant(low)
	e.HandleGrant(high) // should preempt

	evs := collector.drain(sub, 3, 1*time.Second)
	if len(evs) < 3 {
		t.Fatalf("got %d events, want >= 3 (start, end, start)", len(evs))
	}
	if evs[0].Kind != events.KindCallStart {
		t.Errorf("evs[0] = %s, want call.start", evs[0].Kind)
	}
	if evs[1].Kind != events.KindCallEnd {
		t.Errorf("evs[1] = %s, want call.end", evs[1].Kind)
	} else if ce, ok := evs[1].Payload.(CallEnd); ok && ce.Reason != EndReasonPreempted {
		t.Errorf("CallEnd reason = %s, want preempted", ce.Reason)
	}
	if evs[2].Kind != events.KindCallStart {
		t.Errorf("evs[2] = %s, want call.start", evs[2].Kind)
	} else if cs, ok := evs[2].Payload.(CallStart); ok && cs.Grant.GroupID != 200 {
		t.Errorf("preempting grant.GroupID = %d, want 200", cs.Grant.GroupID)
	}
}

func TestEngineDoesNotPreemptEqualPriority(t *testing.T) {
	e, _, bus, _ := mkEngine(t, 1)
	defer bus.Close()
	e.talkgroups.Add(&TalkGroup{ID: 100, Priority: 3})
	e.talkgroups.Add(&TalkGroup{ID: 200, Priority: 3})

	g1 := Grant{GroupID: 100, FrequencyHz: 851_000_000}
	g2 := Grant{GroupID: 200, FrequencyHz: 852_000_000}
	e.HandleGrant(g1)
	e.HandleGrant(g2) // should be dropped

	if calls := e.ActiveCalls(); len(calls) != 1 || calls[0].Grant.GroupID != 100 {
		t.Errorf("active = %+v, want only original call", calls)
	}
}

func TestEngineEmergencyPreemptsAnything(t *testing.T) {
	e, _, bus, _ := mkEngine(t, 1)
	defer bus.Close()
	e.talkgroups.Add(&TalkGroup{ID: 100, Priority: 1, AlphaTag: "TOP"})

	g1 := Grant{GroupID: 100, FrequencyHz: 851_000_000}
	emer := Grant{GroupID: 999, FrequencyHz: 853_000_000, Emergency: true}
	e.HandleGrant(g1)
	e.HandleGrant(emer)

	calls := e.ActiveCalls()
	if len(calls) != 1 || calls[0].Grant.GroupID != 999 {
		t.Errorf("after emergency: active = %+v, want emergency call", calls)
	}
}

func TestEngineExplicitEndCall(t *testing.T) {
	e, pool, bus, _ := mkEngine(t, 1)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	g := Grant{GroupID: 100, FrequencyHz: 851_000_000}
	e.HandleGrant(g)
	d := pool.Active()[0].Device

	if !e.EndCall(d.Serial, EndReasonNormal) {
		t.Fatal("EndCall reported no active call")
	}
	if got := e.ActiveCalls(); len(got) != 0 {
		t.Errorf("active after EndCall = %+v, want []", got)
	}

	// drain the call.start + call.end events
	got := []events.Event{}
	deadline := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case ev := <-sub.C:
			got = append(got, ev)
			if ev.Kind == events.KindCallEnd {
				break loop
			}
		case <-deadline:
			break loop
		}
	}
	if len(got) < 2 {
		t.Fatalf("events = %+v", got)
	}
	end, ok := got[len(got)-1].Payload.(CallEnd)
	if !ok || end.Reason != EndReasonNormal {
		t.Errorf("last event reason = %v", got[len(got)-1])
	}
}

func TestEngineWatchdogTimesOutSilentCall(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	pool, _ := mkPool(1)
	now := time.Unix(1000, 0)
	clock := &fakeClock{t: now}
	e, _ := NewEngine(EngineOptions{
		Bus:         bus,
		VoicePool:   pool,
		Talkgroups:  NewTalkgroupDB(),
		CallTimeout: 1 * time.Second,
		Now:         clock.Now,
	})

	g := Grant{GroupID: 100, FrequencyHz: 851_000_000}
	e.HandleGrant(g)
	if len(e.ActiveCalls()) != 1 {
		t.Fatal("call did not start")
	}

	// Advance the clock past the timeout and run the watchdog.
	clock.t = clock.t.Add(2 * time.Second)
	e.runWatchdog()
	if got := e.ActiveCalls(); len(got) != 0 {
		t.Errorf("watchdog should have ended the call; active = %+v", got)
	}
}

func TestEngineRunDispatchesGrantEvents(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	pool, tuners := mkPool(1)
	e, _ := NewEngine(EngineOptions{
		Bus:         bus,
		VoicePool:   pool,
		Talkgroups:  NewTalkgroupDB(),
		CallTimeout: 5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Run(ctx)

	bus.Publish(events.Event{
		Kind:    events.KindGrant,
		Payload: Grant{GroupID: 7, FrequencyHz: 851_000_000},
	})

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(tuners[0].tuned()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := tuners[0].tuned(); len(got) != 1 || got[0] != 851_000_000 {
		t.Errorf("device retune = %v, want [851000000]", got)
	}
}

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time { return c.t }

func TestEngineHandleCallEncryptionBackfillsActiveCall(t *testing.T) {
	bus := events.NewBus(16)
	defer bus.Close()
	pool, _ := mkPool(1)
	e, _ := NewEngine(EngineOptions{
		Bus:         bus,
		VoicePool:   pool,
		Talkgroups:  NewTalkgroupDB(),
		CallTimeout: 5 * time.Second,
	})

	// Start a Phase 1 call. ALGID/KID are zero (Phase 1 grant TSBK
	// doesn't carry them).
	e.HandleGrant(Grant{
		System: "MMR", Protocol: "p25",
		GroupID: 4321, FrequencyHz: 851_000_000,
		Encrypted: true,
	})

	actives := e.ActiveCalls()
	if len(actives) != 1 {
		t.Fatalf("expected 1 active call, got %d", len(actives))
	}
	dev := actives[0].Device.Serial
	if actives[0].Grant.AlgorithmID != 0 || actives[0].Grant.KeyID != 0 {
		t.Fatalf("pre-backfill alg/key should be zero, got %v/%v",
			actives[0].Grant.AlgorithmID, actives[0].Grant.KeyID)
	}

	// Subscribe BEFORE driving the encryption update so we observe
	// the enriched republish without racing the event loop.
	sub := bus.Subscribe()
	defer sub.Close()

	// Composer publishes the raw event (System/Protocol/GroupID empty);
	// the engine must backfill, enrich, and republish.
	e.handleCallEncryption(CallEncryption{
		DeviceSerial: dev,
		AlgorithmID:  0x84, // AES-256
		KeyID:        0x1234,
		At:           time.Now(),
	})

	// The pool's ActiveCall.Grant should now carry the values so the
	// next CallEnd payload includes them.
	updated := e.ActiveCalls()
	if len(updated) != 1 {
		t.Fatalf("expected 1 active call after backfill, got %d", len(updated))
	}
	if updated[0].Grant.AlgorithmID != 0x84 || updated[0].Grant.KeyID != 0x1234 {
		t.Errorf("backfill did not land on Grant: alg=0x%X key=0x%X",
			updated[0].Grant.AlgorithmID, updated[0].Grant.KeyID)
	}

	// Enriched republish should be on the bus with system / tg filled.
	select {
	case ev := <-sub.C:
		if ev.Kind != events.KindCallEncryption {
			t.Fatalf("expected KindCallEncryption republish, got %s", ev.Kind)
		}
		ce, ok := ev.Payload.(CallEncryption)
		if !ok {
			t.Fatalf("payload type = %T", ev.Payload)
		}
		if ce.System != "MMR" || ce.Protocol != "p25" || ce.GroupID != 4321 {
			t.Errorf("enriched payload missing identity fields: %+v", ce)
		}
		if ce.AlgorithmID != 0x84 || ce.KeyID != 0x1234 {
			t.Errorf("enriched payload alg/key = 0x%X/0x%X", ce.AlgorithmID, ce.KeyID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("never received enriched republish")
	}
}

func TestEngineHandleCallEncryptionUnknownDeviceDoesNotPanic(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	pool, _ := mkPool(1)
	e, _ := NewEngine(EngineOptions{
		Bus:         bus,
		VoicePool:   pool,
		Talkgroups:  NewTalkgroupDB(),
		CallTimeout: 5 * time.Second,
	})

	// No active call on this device serial. Should silently drop.
	e.handleCallEncryption(CallEncryption{
		DeviceSerial: "no-such-device",
		AlgorithmID:  0x84,
		KeyID:        0x1234,
	})
}

func TestEngineEmptyVoicePoolWarnsOnceThenDebug(t *testing.T) {
	// Issue #379: a daemon with trunking systems but zero `role: voice`
	// SDRs builds an empty VoicePool. Every grant used to log a
	// misleading "voice pool full but no actives" warning. The engine
	// now logs one actionable WARN and drops the rest at DEBUG.
	bus := events.NewBus(8)
	defer bus.Close()
	pool, _ := mkPool(0)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	e, err := NewEngine(EngineOptions{
		Bus:        bus,
		Log:        log,
		VoicePool:  pool,
		Talkgroups: NewTalkgroupDB(),
	})
	if err != nil {
		t.Fatal(err)
	}

	g := Grant{System: "X", Protocol: "p25", GroupID: 1, FrequencyHz: 851_000_000}
	e.HandleGrant(g)
	e.HandleGrant(g)
	e.HandleGrant(g)

	out := buf.String()
	// Misleading legacy message must be gone.
	if strings.Contains(out, "voice pool full but no actives") {
		t.Errorf("legacy misleading warning still present:\n%s", out)
	}
	// Actionable WARN logged exactly once across three grants.
	warnCount := strings.Count(out, "level=WARN")
	if warnCount != 1 {
		t.Errorf("expected exactly one WARN, got %d:\n%s", warnCount, out)
	}
	if !strings.Contains(out, "no voice SDR available") {
		t.Errorf("expected actionable WARN about missing voice SDR:\n%s", out)
	}
	// Every grant drops at DEBUG; the first also emits the one-shot WARN.
	debugCount := strings.Count(out, `msg="dropping grant: no voice SDR"`)
	if debugCount != 3 {
		t.Errorf("expected 3 DEBUG drops (one per grant), got %d:\n%s", debugCount, out)
	}
}

func TestEngineHandleCallEncryptionEnrichedRepublishDoesNotLoop(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	pool, _ := mkPool(1)
	e, _ := NewEngine(EngineOptions{
		Bus:         bus,
		VoicePool:   pool,
		Talkgroups:  NewTalkgroupDB(),
		CallTimeout: 5 * time.Second,
	})
	e.HandleGrant(Grant{
		System: "MMR", Protocol: "p25",
		GroupID: 1, FrequencyHz: 851_000_000, Encrypted: true,
	})
	dev := e.ActiveCalls()[0].Device.Serial

	// An event with System already set is the engine's own republish
	// coming back through the subscription. Must be ignored — otherwise
	// the engine would publish another republish, and so on.
	sub := bus.Subscribe()
	defer sub.Close()
	e.handleCallEncryption(CallEncryption{
		DeviceSerial: dev,
		System:       "MMR",
		AlgorithmID:  0x84,
		KeyID:        0x1234,
	})
	select {
	case ev := <-sub.C:
		t.Fatalf("expected no republish for already-enriched event, got %s", ev.Kind)
	case <-time.After(100 * time.Millisecond):
		// pass
	}
}
