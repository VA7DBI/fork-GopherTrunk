package adsb

import (
	"encoding/hex"
	"math"
	"testing"
	"time"
)

// fixedNow is a deterministic timestamp tracker tests use so the
// 10 s pair-window logic doesn't rely on wall-clock timing.
const fixedNow int64 = 1_700_000_000_000_000_000

// TestTrackerPairsEvenOddToGlobalPosition feeds the canonical
// dump1090 even+odd pair through the tracker and asserts the
// second message comes back with HasGlobalPosition + the right
// lat/lon. The pair sequence is odd → even so the more-recent
// half is even, matching the canonical reference's
// mostRecentIsEven=true expected value (52.2572 N / 3.91937 E).
func TestTrackerPairsEvenOddToGlobalPosition(t *testing.T) {
	tr := NewTracker()
	even := Decode(decodeHexSilent("8D40621D58C382D690C8AC2863A7"))
	odd := Decode(decodeHexSilent("8D40621D58C386435CC412692AD6"))

	// Odd arrives first — no pair yet.
	out, paired := tr.Update(odd, fixedNow)
	if paired {
		t.Error("first half (odd): paired = true, want false")
	}
	if out.Position != nil && out.Position.HasGlobalPosition {
		t.Error("first half: HasGlobalPosition = true, want false")
	}

	// Even arrives second (more recent) — pair completes.
	out, paired = tr.Update(even, fixedNow+int64(500*time.Millisecond))
	if !paired {
		t.Fatal("second half: paired = false, want true")
	}
	if !out.Position.HasGlobalPosition {
		t.Fatal("HasGlobalPosition = false on completed pair")
	}
	if math.Abs(out.Position.Latitude-52.2572) > 0.0001 {
		t.Errorf("Latitude = %f, want 52.2572", out.Position.Latitude)
	}
	if math.Abs(out.Position.Longitude-3.91937) > 0.0001 {
		t.Errorf("Longitude = %f, want 3.91937", out.Position.Longitude)
	}
}

// TestTrackerRejectsPairOlderThan10s confirms the spec's 10 s
// window: an even half from a stale buffer doesn't pair with a
// fresh odd half.
func TestTrackerRejectsPairOlderThan10s(t *testing.T) {
	tr := NewTracker()
	even := Decode(decodeHexSilent("8D40621D58C382D690C8AC2863A7"))
	odd := Decode(decodeHexSilent("8D40621D58C386435CC412692AD6"))

	_, _ = tr.Update(even, fixedNow)
	// Odd half arrives 12 s later — past the spec's 10 s window.
	_, paired := tr.Update(odd, fixedNow+int64(12*time.Second))
	if paired {
		t.Error("paired = true across 12 s gap; want false (spec window is 10 s)")
	}
}

// TestTrackerPassesThroughNonPositionMessages confirms the
// tracker ignores identification / velocity / status frames.
func TestTrackerPassesThroughNonPositionMessages(t *testing.T) {
	tr := NewTracker()
	ident := Decode(decodeHexSilent("8D4840D6202CC371C32CE0576098"))
	if ident.Kind != KindIdentification {
		t.Fatalf("test fixture: Kind = %v, want KindIdentification", ident.Kind)
	}

	out, paired := tr.Update(ident, fixedNow)
	if paired {
		t.Error("identification message paired = true, want false")
	}
	if out.Identification == nil || out.Identification.Callsign == "" {
		t.Error("identification fields not preserved across Update")
	}
	if tr.Size() != 0 {
		t.Errorf("Size = %d after non-position message, want 0", tr.Size())
	}
}

// TestTrackerIgnoresCRCFailed frames (m.ICAO == 0) — the parser
// emits position payloads even on CRC-failed frames so the upper
// layers can render "marginal signal" rows, but the tracker
// can't trust the ICAO so it should ignore them.
func TestTrackerIgnoresZeroICAO(t *testing.T) {
	tr := NewTracker()
	m := Message{
		Kind: KindAirbornePosition,
		ICAO: 0,
		Position: &Position{
			CPRFormat: 0, CPRLatEven: 1234, CPRLonEven: 5678,
		},
	}
	_, paired := tr.Update(m, fixedNow)
	if paired {
		t.Error("paired = true on ICAO 0; want false")
	}
	if tr.Size() != 0 {
		t.Errorf("Size = %d after ICAO-0 message, want 0", tr.Size())
	}
}

// TestTrackerHandlesMultipleICAOsIndependently confirms two
// aircraft's CPR halves don't cross-contaminate.
func TestTrackerHandlesMultipleICAOsIndependently(t *testing.T) {
	tr := NewTracker()

	// Aircraft A — even only.
	a := Decode(decodeHexSilent("8D40621D58C382D690C8AC2863A7"))
	_, _ = tr.Update(a, fixedNow)

	// Aircraft B — both halves.
	bEven := a // borrow A's even encoding
	bEven.ICAO = 0xDEADBE
	bOdd := Decode(decodeHexSilent("8D40621D58C386435CC412692AD6"))
	bOdd.ICAO = 0xDEADBE

	_, _ = tr.Update(bEven, fixedNow)
	_, paired := tr.Update(bOdd, fixedNow+int64(500*time.Millisecond))
	if !paired {
		t.Error("aircraft B pair didn't complete; want true")
	}
	if tr.Size() != 2 {
		t.Errorf("Size = %d, want 2 (both ICAOs tracked)", tr.Size())
	}
}

// TestTrackerPruneDropsStaleEntries confirms aircraft that haven't
// transmitted in > 10 s get evicted from tracker state.
func TestTrackerPruneDropsStaleEntries(t *testing.T) {
	tr := NewTracker()
	even := Decode(decodeHexSilent("8D40621D58C382D690C8AC2863A7"))
	_, _ = tr.Update(even, fixedNow)
	if tr.Size() != 1 {
		t.Fatalf("Size after Update = %d, want 1", tr.Size())
	}

	dropped := tr.Prune(fixedNow + int64(11*time.Second))
	if dropped != 1 {
		t.Errorf("Prune dropped %d, want 1", dropped)
	}
	if tr.Size() != 0 {
		t.Errorf("Size after Prune = %d, want 0", tr.Size())
	}
}

// TestTrackerResetClearsState confirms Reset drops everything.
func TestTrackerResetClearsState(t *testing.T) {
	tr := NewTracker()
	even := Decode(decodeHexSilent("8D40621D58C382D690C8AC2863A7"))
	_, _ = tr.Update(even, fixedNow)
	tr.Reset()
	if tr.Size() != 0 {
		t.Errorf("Size after Reset = %d, want 0", tr.Size())
	}
}

// decodeHexSilent is the tracker tests' hex helper; panics on
// bad input — tests pass hand-curated literals, so a parse
// failure means the test is broken, not the code under test.
func decodeHexSilent(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("decodeHexSilent: bad hex: " + err.Error())
	}
	return b
}
