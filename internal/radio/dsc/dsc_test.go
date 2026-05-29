package dsc

import (
	"math"
	"strings"
	"testing"
)

func TestFormatAndCategoryStableLabels(t *testing.T) {
	formatCases := map[Format]string{
		FormatDistress:       "distress",
		FormatAllShips:       "all-ships",
		FormatGroup:          "group",
		FormatIndividual:     "individual",
		FormatGeographic:     "geographic",
		FormatAutoIndividual: "auto-individual",
		FormatUnknown:        "unknown",
		Format(99):           "unknown",
	}
	for f, want := range formatCases {
		if got := FormatString(f); got != want {
			t.Errorf("FormatString(%d) = %q, want %q", f, got, want)
		}
	}
	catCases := map[Category]string{
		CategoryDistress: "distress",
		CategoryUrgency:  "urgency",
		CategorySafety:   "safety",
		CategoryRoutine:  "routine",
		Category(99):     "unknown",
	}
	for c, want := range catCases {
		if got := CategoryString(c); got != want {
			t.Errorf("CategoryString(%d) = %q, want %q", c, got, want)
		}
	}
}

func TestNatureStringSampleValues(t *testing.T) {
	cases := map[NatureOfDistress]string{
		DistressFire:      "fire / explosion",
		DistressCollision: "collision",
		DistressMOB:       "man overboard",
		DistressEPIRB:     "EPIRB emission",
		NatureOfDistress(50): "",
	}
	for n, want := range cases {
		if got := NatureString(n); got != want {
			t.Errorf("NatureString(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDecodeMMSIRoundTrip(t *testing.T) {
	// MMSI 366053209 — symbols encode pairs of digits:
	//   "36" "60" "53" "20" "9?" (10th digit is format extension)
	symbols := []byte{36, 60, 53, 20, 90}
	got, ok := decodeMMSI(symbols)
	if !ok {
		t.Fatal("decodeMMSI: !ok")
	}
	if got != 366053209 {
		t.Errorf("MMSI = %d, want 366053209", got)
	}
}

func TestDecodeMMSIRejectsOutOfRange(t *testing.T) {
	symbols := []byte{36, 60, 100, 20, 90} // 100 > 99 → invalid
	if _, ok := decodeMMSI(symbols); ok {
		t.Error("decodeMMSI(out-of-range): ok = true, want false")
	}
}

func TestDecodePositionRoundTrip(t *testing.T) {
	// Quadrant 0 (NE), lat 37° 48' N, lon 122° 24' E.
	// Digits: Q=0, lat=3748, lon=12224 → "0374812224".
	// As 5 symbol pairs: 03 74 81 22 24.
	symbols := []byte{3, 74, 81, 22, 24}
	pos, ok := decodePosition(symbols)
	if !ok {
		t.Fatal("decodePosition: !ok")
	}
	if !pos.HasPosition {
		t.Error("HasPosition = false on a valid position")
	}
	wantLat := 37.0 + 48.0/60.0
	if math.Abs(pos.Latitude-wantLat) > 1e-6 {
		t.Errorf("Latitude = %f, want %f", pos.Latitude, wantLat)
	}
	wantLon := 122.0 + 24.0/60.0
	if math.Abs(pos.Longitude-wantLon) > 1e-6 {
		t.Errorf("Longitude = %f, want %f", pos.Longitude, wantLon)
	}
}

func TestDecodePositionApplyQuadrantSign(t *testing.T) {
	// Quadrant 3 (SW), lat 33° 27' S, lon 70° 39' W.
	// Digits: Q=3, lat=3327, lon=07039 → "3332707039".
	// Symbol pairs: 33 32 70 70 39.
	symbols := []byte{33, 32, 70, 70, 39}
	pos, ok := decodePosition(symbols)
	if !ok {
		t.Fatal("decodePosition: !ok")
	}
	if pos.Latitude > 0 {
		t.Errorf("Latitude = %f, want negative (S)", pos.Latitude)
	}
	if pos.Longitude > 0 {
		t.Errorf("Longitude = %f, want negative (W)", pos.Longitude)
	}
}

func TestDecodePositionUnknownSentinel(t *testing.T) {
	// All-9s = "position not available" per spec.
	symbols := []byte{99, 99, 99, 99, 99}
	pos, ok := decodePosition(symbols)
	if !ok {
		t.Fatal("decodePosition(unknown sentinel): !ok")
	}
	if pos.HasPosition {
		t.Error("HasPosition = true on unknown sentinel")
	}
}

func TestDecodeDistressMessage(t *testing.T) {
	// Distress alert: format=112, self-MMSI=366053209,
	// nature=fire (100), position 37°48' N / 122°24' E,
	// time 14:25 UTC, EOS=127.
	symbols := []byte{
		112,                       // format = Distress
		36, 60, 53, 20, 90,        // self-MMSI 366053209
		100,                       // nature = fire
		3, 74, 81, 22, 24,         // position (NE 37°48' / 122°24')
		14, 25,                    // time 14:25
		127,                       // EOS
	}
	m := Decode(symbols)
	if m.Format != FormatDistress {
		t.Errorf("Format = %d, want FormatDistress (%d)", m.Format, FormatDistress)
	}
	if m.Category != CategoryDistress {
		t.Errorf("Category = %d, want CategoryDistress (%d)", m.Category, CategoryDistress)
	}
	if m.SelfMMSI != 366053209 {
		t.Errorf("SelfMMSI = %d, want 366053209", m.SelfMMSI)
	}
	if m.Nature != DistressFire {
		t.Errorf("Nature = %d, want DistressFire (%d)", m.Nature, DistressFire)
	}
	if m.Position == nil || !m.Position.HasPosition {
		t.Fatal("Position = nil or HasPosition = false")
	}
	if math.Abs(m.Position.Latitude-(37+48.0/60)) > 1e-6 {
		t.Errorf("Latitude = %f", m.Position.Latitude)
	}
	if m.TimeUTC != "14:25" {
		t.Errorf("TimeUTC = %q, want 14:25", m.TimeUTC)
	}
}

func TestDecodeIndividualCall(t *testing.T) {
	// Individual call: format=120, target-MMSI=003660000 (coast
	// station), category=routine (100), self-MMSI=366053209,
	// EOS=127. Symbol pairs for MMSI 003660000:
	//   00 36 60 00 00.
	symbols := []byte{
		120,                       // format = Individual
		0, 36, 60, 0, 0,           // target-MMSI 003660000
		100,                       // category = routine
		36, 60, 53, 20, 90,        // self-MMSI 366053209
		127,                       // EOS
	}
	m := Decode(symbols)
	if m.Format != FormatIndividual {
		t.Errorf("Format = %d, want FormatIndividual", m.Format)
	}
	if m.Category != CategoryRoutine {
		t.Errorf("Category = %d, want CategoryRoutine", m.Category)
	}
	if m.TargetMMSI != 3660000 {
		t.Errorf("TargetMMSI = %d, want 3660000", m.TargetMMSI)
	}
	if m.SelfMMSI != 366053209 {
		t.Errorf("SelfMMSI = %d, want 366053209", m.SelfMMSI)
	}
}

func TestDecodeAllShipsSafety(t *testing.T) {
	// All-ships safety: format=116, MMSI all-ships (any),
	// category=safety (108), self-MMSI=003669999.
	symbols := []byte{
		116,                       // format = AllShips
		0, 0, 0, 0, 0,             // address (ignored for all-ships)
		108,                       // category = safety
		0, 36, 69, 99, 90,         // self-MMSI 003669999
		127,                       // EOS
	}
	m := Decode(symbols)
	if m.Format != FormatAllShips {
		t.Errorf("Format = %d, want FormatAllShips", m.Format)
	}
	if m.Category != CategorySafety {
		t.Errorf("Category = %d, want CategorySafety", m.Category)
	}
	if m.SelfMMSI != 3669999 {
		t.Errorf("SelfMMSI = %d, want 3669999", m.SelfMMSI)
	}
}

func TestDecodeShortPayloadReturnsSafe(t *testing.T) {
	if m := Decode(nil); m.Format != FormatUnknown {
		t.Errorf("Decode(nil).Format = %d, want FormatUnknown", m.Format)
	}
	// Too short for an MMSI block — degrades gracefully.
	if m := Decode([]byte{120, 0, 36}); m.SelfMMSI != 0 {
		t.Errorf("Decode(short).SelfMMSI = %d, want 0", m.SelfMMSI)
	}
}

func TestMessageStringRendersFormatVariants(t *testing.T) {
	// Distress
	m := Message{
		Format:   FormatDistress,
		SelfMMSI: 366053209,
		Nature:   DistressFire,
		Position: &Position{Latitude: 37.8, Longitude: 122.4, HasPosition: true},
		TimeUTC:  "14:25",
	}
	got := m.String()
	if !strings.HasPrefix(got, "DISTRESS ") {
		t.Errorf("String prefix = %q", got[:9])
	}
	if !strings.Contains(got, "MMSI=366053209") {
		t.Errorf("String missing MMSI: %q", got)
	}
	// All-ships
	m = Message{Format: FormatAllShips, Category: CategorySafety, SelfMMSI: 3669999}
	got = m.String()
	if !strings.HasPrefix(got, "ALL-SHIPS ") {
		t.Errorf("String prefix = %q", got)
	}
}
