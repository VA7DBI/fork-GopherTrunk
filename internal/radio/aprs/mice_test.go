package aprs

import (
	"math"
	"strings"
	"testing"
)

// buildMicEInfo synthesises a Mic-E info field byte stream. Helper
// for the table tests below — the tone of the spec layout makes
// hand-rolled []byte literals hard to read.
func buildMicEInfo(dti byte, lonDegRaw, lonMinRaw, lonHunRaw, sp, dc, se, symCode, symTable byte, trailer string) []byte {
	out := []byte{dti, lonDegRaw, lonMinRaw, lonHunRaw, sp, dc, se, symCode, symTable}
	out = append(out, []byte(trailer)...)
	return out
}

func TestMicECharTableSpotChecks(t *testing.T) {
	cases := []struct {
		c          byte
		wantDigit  byte
		wantBit    bool
		wantStd    bool
		wantCustom bool
		wantIsLat  bool
	}{
		{'0', '0', false, false, false, true},
		{'9', '9', false, false, false, true},
		{'A', '0', true, false, true, true},
		{'J', '9', true, false, true, true},
		{'K', ' ', true, false, true, true},
		{'L', ' ', false, false, false, true},
		{'P', '0', true, true, false, true},
		{'Y', '9', true, true, false, true},
		{'Z', ' ', true, true, false, true},
		{'M', 0, false, false, false, false}, // M is in the gap between J and P; not a valid Mic-E char
		{'_', 0, false, false, false, false},
	}
	for _, c := range cases {
		digit, bit, std, custom, ok := micEChar(c.c)
		if digit != c.wantDigit || bit != c.wantBit || std != c.wantStd ||
			custom != c.wantCustom || ok != c.wantIsLat {
			t.Errorf("micEChar(%q) = (digit=%q bit=%v std=%v custom=%v ok=%v), "+
				"want (digit=%q bit=%v std=%v custom=%v ok=%v)",
				c.c, digit, bit, std, custom, ok,
				c.wantDigit, c.wantBit, c.wantStd, c.wantCustom, c.wantIsLat)
		}
	}
}

func TestMicEMessageLabels(t *testing.T) {
	cases := []struct {
		bits   uint8
		custom bool
		want   string
	}{
		{0b111, false, "M0 Off Duty"},
		{0b110, false, "M1 En Route"},
		{0b101, false, "M2 In Service"},
		{0b100, false, "M3 Returning"},
		{0b011, false, "M4 Committed"},
		{0b010, false, "M5 Special"},
		{0b001, false, "M6 Priority"},
		{0b000, false, "Emergency"},
		{0b000, true, "Emergency"}, // shared across std + custom
		{0b111, true, "M0 Custom-0"},
		{0b100, true, "M3 Custom-3"},
	}
	for _, c := range cases {
		got := micEMessageLabel(c.bits, c.custom)
		if got != c.want {
			t.Errorf("micEMessageLabel(%03b, custom=%v) = %q, want %q",
				c.bits, c.custom, got, c.want)
		}
	}
}

// TestParseMicEDestStandardNorth checks a clean standard-range
// destination encoding a Northern / Western position with the M1
// En Route message code (3-bit pattern 110).
func TestParseMicEDestStandardNorth(t *testing.T) {
	// Lat: 49° 03.50' N → DDMMhh = 49 03 50 = "490350"
	// Std N/W message bits 1,1,0 in positions 0,1,2.
	// Std N (pos 3 bit=1): P-Z range → use 'P' (digit 0) for tens of min
	// Std +100° (pos 4 bit=1): P-Z range
	// Std W (pos 5 bit=1): P-Z range
	//
	// Char picks:
	//   pos 0: lat=4, msg bit 1 (std) → 'T' (P+4)
	//   pos 1: lat=9, msg bit 1 (std) → 'Y' (P+9)
	//   pos 2: lat=0, msg bit 0       → '0'
	//   pos 3: lat=3, N flag set      → 'S' (P+3)
	//   pos 4: lat=5, +100° set       → 'U' (P+5)
	//   pos 5: lat=0, W flag set      → 'P'
	dst := []byte("TY0SUP")
	lat, msgBits, custom, north, lonOff100, west, ok := parseMicEDest(dst)
	if !ok {
		t.Fatal("parseMicEDest: !ok")
	}
	if math.Abs(lat-(49+3.5/60)) > 1e-6 {
		t.Errorf("Latitude = %f, want ≈ 49.0583", lat)
	}
	if msgBits != 0b110 {
		t.Errorf("msgBits = %03b, want 110", msgBits)
	}
	if custom {
		t.Error("custom = true, want false (std range)")
	}
	if !north {
		t.Error("north = false, want true")
	}
	if !lonOff100 {
		t.Error("lonOff100 = false, want true")
	}
	if !west {
		t.Error("west = false, want true")
	}
}

// TestParseMicEDestSouthernEast covers the inverse: 0-9/L chars,
// S/E hemispheres, no longitude offset.
func TestParseMicEDestSouthernEast(t *testing.T) {
	// Lat: 33° 27.50' S → DDMMhh = 33 27 50
	// All 0-9/L chars (no flags set).
	// pos 3: lat=2, S (no flag)        → '2'
	// pos 4: lat=7, +0° (no flag)      → '7'
	// pos 5: lat=0, E (no flag)        → '0'
	dst := []byte("332750")
	lat, msgBits, custom, north, lonOff100, west, ok := parseMicEDest(dst)
	if !ok {
		t.Fatal("parseMicEDest: !ok")
	}
	wantLat := 33 + 27.5/60
	if math.Abs(lat-wantLat) > 1e-6 {
		t.Errorf("Latitude = %f, want ≈ %f", lat, wantLat)
	}
	if msgBits != 0 {
		t.Errorf("msgBits = %03b, want 000", msgBits)
	}
	if custom {
		t.Error("custom = true, want false")
	}
	if north {
		t.Error("north = true, want false (S)")
	}
	if lonOff100 {
		t.Error("lonOff100 = true, want false")
	}
	if west {
		t.Error("west = true, want false (E)")
	}
}

func TestParseMicEDestRejectsInvalidChars(t *testing.T) {
	// 'M' falls in the gap (J = end of A-J, P = start of P-Z), so
	// it's not a valid Mic-E destination char.
	dst := []byte("M99999")
	if _, _, _, _, _, _, ok := parseMicEDest(dst); ok {
		t.Error("parseMicEDest(M99999): want !ok, got ok")
	}
}

func TestParseMicEDestAmbiguityDigits(t *testing.T) {
	// 'L' carries digit ' ' (space) and 0-bit. Three Ls represent
	// "low-precision" digits which the spec treats as 0.
	dst := []byte("33LL00")
	lat, _, _, _, _, _, ok := parseMicEDest(dst)
	if !ok {
		t.Fatal("parseMicEDest: !ok")
	}
	wantLat := 33 + 0.0
	if math.Abs(lat-wantLat) > 1e-6 {
		t.Errorf("Latitude with ambiguity digits = %f, want ≈ %f", lat, wantLat)
	}
}

// TestParseMicEFullRoundTrip uses a known-good test vector adapted
// from APRS101 §10 worked example: lat 33° 25.64' N, lon 112° 09.18' W,
// speed 20 knots, course 251°, symbol code '>'  (car) on the
// primary table. Standard set, M3 Returning (msg bits 100).
func TestParseMicEFullRoundTrip(t *testing.T) {
	// Build a destination encoding:
	//   33 25 64 with M3 Returning (bits 1 0 0), N, +100°, W.
	//   pos 0: lat=3, msg=1 → S (P+3)
	//   pos 1: lat=3, msg=0 → '3'
	//   pos 2: lat=2, msg=0 → '2'
	//   pos 3: lat=5, N=1   → U (P+5)
	//   pos 4: lat=6, +100  → V (P+6)
	//   pos 5: lat=4, W=1   → T (P+4)
	dst := []byte("S32UVT")

	// Info: longitude 112° 09.18' W. With +100° offset:
	//   lonDeg field raw value = 112 - 100 = 12  → byte = 12 + 28 = 40 = '('
	//   lonMin field           = 9              → byte = 9 + 28  = 37 = '%'
	//   lonHun field           = 18             → byte = 18 + 28 = 46 = '.'
	// Speed 20 knots, course 251°:
	//   sp = speed / 10 = 2     → byte = 2 + 28  = 30
	//   dc = (speed % 10) * 10 + course/100 = 0*10 + 2 = 2 → byte = 2 + 28 = 30
	//   se = course % 100 = 51  → byte = 51 + 28 = 79 = 'O'
	// Symbol code '>' (car), table '/' (primary).
	info := buildMicEInfo(
		'`',                                 // DTI
		byte(12+28),                         // lon deg
		byte(9+28),                          // lon min
		byte(18+28),                         // lon hun
		byte(2+28), byte(2+28), byte(51+28), // sp / dc / se
		'>', '/', // symbol code / table
		"",
	)

	p := DecodeWithDst(info, dst)
	if p.Type != TypeMicE {
		t.Fatalf("Type = %v, want TypeMicE", p.Type)
	}
	if p.MicE == nil {
		t.Fatal("MicE = nil, want non-nil")
	}
	wantLat := 33 + 25.64/60
	if math.Abs(p.MicE.Latitude-wantLat) > 1e-4 {
		t.Errorf("Latitude = %f, want ≈ %f", p.MicE.Latitude, wantLat)
	}
	wantLon := -(112 + 9.18/60)
	if math.Abs(p.MicE.Longitude-wantLon) > 1e-4 {
		t.Errorf("Longitude = %f, want ≈ %f", p.MicE.Longitude, wantLon)
	}
	if p.MicE.Speed != 20 {
		t.Errorf("Speed = %d, want 20", p.MicE.Speed)
	}
	if p.MicE.Course != 251 {
		t.Errorf("Course = %d, want 251", p.MicE.Course)
	}
	if p.MicE.MessageCode != "M3 Returning" {
		t.Errorf("MessageCode = %q, want M3 Returning", p.MicE.MessageCode)
	}
	if !p.MicE.Standard {
		t.Error("Standard = false, want true")
	}
	if p.MicE.SymbolCode != '>' || p.MicE.SymbolTable != '/' {
		t.Errorf("Symbol = %c%c, want />", p.MicE.SymbolTable, p.MicE.SymbolCode)
	}

	// Also surfaces through the standard Position field so the
	// receiver / storage layer can read lat/lon without
	// special-casing Mic-E.
	if p.Position == nil {
		t.Fatal("Position = nil for Mic-E; want surfaced for downstream callers")
	}
	if math.Abs(p.Position.Latitude-wantLat) > 1e-4 {
		t.Errorf("Position.Latitude = %f, want ≈ %f", p.Position.Latitude, wantLat)
	}
}

func TestDecodeWithDstFallsBackWhenDstMissing(t *testing.T) {
	// Mic-E packet but caller doesn't have the dst (e.g. an APRS
	// log replay tool calling Decode directly). Type should stay
	// TypeMicE, MicE should be nil, Position should be nil.
	info := []byte("`Mic-E payload no dst")
	p := DecodeWithDst(info, nil)
	if p.Type != TypeMicE {
		t.Errorf("Type = %v, want TypeMicE", p.Type)
	}
	if p.MicE != nil {
		t.Errorf("MicE = %+v, want nil (dst missing)", p.MicE)
	}
	if p.Position != nil {
		t.Errorf("Position = %+v, want nil", p.Position)
	}
}

func TestDecodeWithDstNonMicEUnchanged(t *testing.T) {
	// Non-Mic-E packets pass through unchanged regardless of dst.
	info := []byte("!4903.50N/07201.75W-Test")
	dst := []byte("APRS  ")
	p := DecodeWithDst(info, dst)
	if p.Type != TypePosition {
		t.Errorf("Type = %v, want TypePosition", p.Type)
	}
	if p.MicE != nil {
		t.Error("MicE = non-nil for position packet")
	}
}

func TestExtractMicEAltitudeRoundTrip(t *testing.T) {
	// APRS101 §10.6: altitude encoded as 3 base-91 chars + "}".
	// value = (c0-33)*91*91 + (c1-33)*91 + (c2-33), altitude = value - 10000.
	// 1000 m altitude → value = 11000 → c0=1, c1=29, c2=80
	// (11000 = 1·8281 + 29·91 + 80). base-91 char N = '!' + N →
	// c0='"', c1='>', c2='q'.
	trailer := []byte(`">q}rest of comment`)
	alt, rest, ok := extractMicEAltitude(trailer)
	if !ok {
		t.Fatal("extractMicEAltitude: !ok")
	}
	if alt != 1000 {
		t.Errorf("altitude = %d, want 1000", alt)
	}
	if string(rest) != "rest of comment" {
		t.Errorf("comment = %q, want %q", string(rest), "rest of comment")
	}
}

func TestExtractMicEAltitudeAbsent(t *testing.T) {
	// No "}" in trailer → altitude not present.
	alt, rest, ok := extractMicEAltitude([]byte("just a normal comment"))
	if ok {
		t.Errorf("extractMicEAltitude(no marker): ok = true (alt=%d)", alt)
	}
	if string(rest) != "just a normal comment" {
		t.Errorf("rest = %q, want trailer unchanged", string(rest))
	}
}

func TestDecodeWithDstAttachesAltitudeFromComment(t *testing.T) {
	// Mic-E packet whose comment trailer carries an altitude
	// marker. Altitude should surface on the MicE struct.
	dst := []byte("S32UVT")
	// Same minimal info as the round-trip test, with the altitude
	// marker appended as the trailer ("Dn} → 1000 m, no more
	// comment text).
	info := buildMicEInfo('`',
		byte(12+28), byte(9+28), byte(18+28),
		byte(2+28), byte(2+28), byte(51+28),
		'>', '/',
		`">q}`,
	)
	p := DecodeWithDst(info, dst)
	if p.MicE == nil {
		t.Fatal("MicE = nil")
	}
	if !p.MicE.HasAltitude {
		t.Error("HasAltitude = false, want true")
	}
	if p.MicE.Altitude != 1000 {
		t.Errorf("Altitude = %d, want 1000", p.MicE.Altitude)
	}
	if p.MicE.Comment != "" {
		t.Errorf("Comment = %q, want empty (only altitude in trailer)", p.MicE.Comment)
	}
}

func TestPacketStringRendersMicE(t *testing.T) {
	dst := []byte("S32UVT")
	info := buildMicEInfo('`',
		byte(12+28), byte(9+28), byte(18+28),
		byte(2+28), byte(2+28), byte(51+28),
		'>', '/',
		"",
	)
	p := DecodeWithDst(info, dst)
	got := p.String()
	if !strings.HasPrefix(got, "MIC-E ") {
		t.Errorf("String() = %q, want MIC-E prefix", got)
	}
	if !strings.Contains(got, "M3 Returning") {
		t.Errorf("String() = %q, want M3 Returning embedded", got)
	}
	if !strings.Contains(got, "20kn") {
		t.Errorf("String() = %q, want 20kn speed embedded", got)
	}
}
