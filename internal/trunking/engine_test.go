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

// TestEngineSkipsOutOfBandVirtualTunerInFavorOfPhysical covers the
// Phase B fallback: when a wideband virtual voice tuner can't serve
// a grant (frequency outside its IQ window) and a physical voice
// SDR is also free, the engine binds the physical one instead of
// dropping. Exercises voicepool.FindFreeForFrequency end-to-end.
func TestEngineSkipsOutOfBandVirtualTunerInFavorOfPhysical(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	// Wideband tap listed first (preferred for in-band grants); a
	// physical tuner second (catches the spillover).
	virtual := &constrainedTuner{lo: 851_000_000, hi: 852_000_000}
	physical := &fakeVoiceTuner{}
	pool := NewVoicePool([]*VoiceDevice{
		{Tuner: virtual, Serial: "wb:00000003:tap-0"},
		{Tuner: physical, Serial: "phys-voice"},
	})
	e, err := NewEngine(EngineOptions{
		Bus: bus, VoicePool: pool, Talkgroups: NewTalkgroupDB(),
		CallTimeout: 1 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Out-of-window grant (900 MHz). Should land on the physical
	// tuner without erroring.
	e.HandleGrant(Grant{System: "X", Protocol: "p25", GroupID: 1, FrequencyHz: 900_000_000})

	calls := e.ActiveCalls()
	if len(calls) != 1 || calls[0].Device.Serial != "phys-voice" {
		t.Fatalf("expected one call on phys-voice, got %+v", calls)
	}
	if len(virtual.freqs) != 0 {
		t.Errorf("virtual tuner should not have been retuned, got %v", virtual.freqs)
	}
	if len(physical.tuned()) != 1 || physical.tuned()[0] != 900_000_000 {
		t.Errorf("physical tuner = %v, want [900_000_000]", physical.tuned())
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

// TestEngineHandleGrantDeduplicatesDuplicateGrant covers the issue
// #356 follow-up where the Phase 1 CC repeats voice-grant TSBKs while
// the call is active. Before the dedup, the engine bound a second
// voice SDR to the same TG/freq, producing duplicate WAV files and
// tying up a tuner the operator needed for other grants. After the
// fix the second grant refreshes the existing bind's LastHeardAt
// (treating the repeat as the CC re-asserting "this call is still
// going") and does not allocate a second device.
func TestEngineHandleGrantDeduplicatesDuplicateGrant(t *testing.T) {
	bus := events.NewBus(16)
	defer bus.Close()
	pool, tuners := mkPool(2)
	clock := &fakeClock{t: time.Unix(1000, 0)}
	e, _ := NewEngine(EngineOptions{
		Bus:         bus,
		VoicePool:   pool,
		Talkgroups:  NewTalkgroupDB(),
		CallTimeout: 5 * time.Second,
		Now:         clock.Now,
	})

	sub := bus.Subscribe()
	defer sub.Close()

	g := Grant{System: "X", Protocol: "p25", GroupID: 32181, FrequencyHz: 773_431_250}
	e.HandleGrant(g)
	if got := len(e.ActiveCalls()); got != 1 {
		t.Fatalf("first grant: active calls = %d, want 1", got)
	}
	firstHeard := e.ActiveCalls()[0].LastHeardAt
	firstDevice := e.ActiveCalls()[0].Device.Serial

	// Advance the clock so the dedup's Touch produces a distinguishable
	// LastHeardAt and fire the identical grant. The second call must
	// not allocate device #2 — only one tuner should ever be retuned.
	clock.t = clock.t.Add(20 * time.Millisecond)
	e.HandleGrant(g)

	if got := len(e.ActiveCalls()); got != 1 {
		t.Fatalf("after duplicate grant: active calls = %d, want 1", got)
	}
	if got := e.ActiveCalls()[0].Device.Serial; got != firstDevice {
		t.Errorf("duplicate grant rebound device: got %q, want %q", got, firstDevice)
	}
	if got := e.ActiveCalls()[0].LastHeardAt; !got.After(firstHeard) {
		t.Errorf("duplicate grant did not refresh LastHeardAt: got %v, want >%v", got, firstHeard)
	}
	// Verify the second device was never touched.
	for i, tn := range tuners {
		if i == 0 {
			continue
		}
		if got := tn.tuned(); len(got) != 0 {
			t.Errorf("device %d was retuned on duplicate grant: tuned=%v", i, got)
		}
	}

	// Drain the bus and confirm exactly one CallStart was published —
	// the second grant must not emit a second start event.
	deadline := time.Now().Add(200 * time.Millisecond)
	starts := 0
	for time.Now().Before(deadline) {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindCallStart {
				starts++
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if starts != 1 {
		t.Errorf("CallStart events = %d, want 1", starts)
	}
}

// TestEngineHandleGrantAllowsDifferentFrequencyGrants is the
// complement guard for the dedup: a same-TG grant on a different
// frequency (rare but legitimate — e.g. site rebind on multi-site
// systems) must NOT be suppressed.
func TestEngineHandleGrantAllowsDifferentFrequencyGrants(t *testing.T) {
	e, _, bus, _ := mkEngine(t, 2)
	defer bus.Close()

	e.HandleGrant(Grant{System: "X", Protocol: "p25", GroupID: 100, FrequencyHz: 851_000_000})
	e.HandleGrant(Grant{System: "X", Protocol: "p25", GroupID: 100, FrequencyHz: 852_000_000})

	if got := len(e.ActiveCalls()); got != 2 {
		t.Errorf("active calls = %d, want 2 (different frequencies)", got)
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

// TestEngineHandleCallSourceUpdateBackfillsActiveCall guards the
// Phase 2 traffic-channel source / encryption backfill flow (the
// MMR fix for issue #376). The CC publishes a grant with src=0 and
// enc=false (compressed grant form); the voice composer recovers
// the real source RID + encrypted state from an in-call
// GROUP_VOICE_CHANNEL_USER PDU on the traffic channel and publishes
// CallSourceUpdate. The engine must (1) patch the bound
// ActiveCall.Grant via VoicePool.UpdateSource, (2) republish with
// System / Protocol / GroupID filled.
func TestEngineHandleCallSourceUpdateBackfillsActiveCall(t *testing.T) {
	bus := events.NewBus(16)
	defer bus.Close()
	pool, _ := mkPool(1)
	e, _ := NewEngine(EngineOptions{
		Bus:         bus,
		VoicePool:   pool,
		Talkgroups:  NewTalkgroupDB(),
		CallTimeout: 5 * time.Second,
	})

	// Phase 2 grant arrives in a compressed form: src=0, enc=false.
	e.HandleGrant(Grant{
		System: "MMR", Protocol: "p25-phase2",
		GroupID: 20202, FrequencyHz: 421_387_500,
	})
	actives := e.ActiveCalls()
	if len(actives) != 1 {
		t.Fatalf("expected 1 active call, got %d", len(actives))
	}
	dev := actives[0].Device.Serial
	if actives[0].Grant.SourceID != 0 || actives[0].Grant.Encrypted {
		t.Fatalf("pre-backfill src/enc should be 0/false, got %v/%v",
			actives[0].Grant.SourceID, actives[0].Grant.Encrypted)
	}

	sub := bus.Subscribe()
	defer sub.Close()

	e.handleCallSourceUpdate(CallSourceUpdate{
		DeviceSerial: dev,
		SourceID:     315203, // @er-imagery's example MMR radio
		Encrypted:    true,
		At:           time.Now(),
	})

	updated := e.ActiveCalls()
	if len(updated) != 1 {
		t.Fatalf("expected 1 active call after backfill, got %d", len(updated))
	}
	if updated[0].Grant.SourceID != 315203 || !updated[0].Grant.Encrypted {
		t.Errorf("backfill did not land on Grant: src=%d enc=%v",
			updated[0].Grant.SourceID, updated[0].Grant.Encrypted)
	}

	select {
	case ev := <-sub.C:
		if ev.Kind != events.KindCallSourceUpdate {
			t.Fatalf("expected KindCallSourceUpdate republish, got %s", ev.Kind)
		}
		c, ok := ev.Payload.(CallSourceUpdate)
		if !ok {
			t.Fatalf("payload type = %T", ev.Payload)
		}
		if c.System != "MMR" || c.Protocol != "p25-phase2" || c.GroupID != 20202 {
			t.Errorf("enriched payload missing identity fields: %+v", c)
		}
		if c.SourceID != 315203 || !c.Encrypted {
			t.Errorf("enriched payload src/enc = %d/%v", c.SourceID, c.Encrypted)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("never received enriched republish")
	}
}

// TestEngineHandleCallSourceUpdateUnknownDeviceDoesNotPanic guards
// the late-arriving PDU after a call ends.
func TestEngineHandleCallSourceUpdateUnknownDeviceDoesNotPanic(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	pool, _ := mkPool(1)
	e, _ := NewEngine(EngineOptions{
		Bus:         bus,
		VoicePool:   pool,
		Talkgroups:  NewTalkgroupDB(),
		CallTimeout: 5 * time.Second,
	})
	e.handleCallSourceUpdate(CallSourceUpdate{
		DeviceSerial: "no-such-device",
		SourceID:     999,
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
