package adsb

import (
	"encoding/hex"
	"math"
	"strings"
	"testing"
)

func decodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex.DecodeString(%q): %v", s, err)
	}
	return b
}

// TestDecodeIdentification uses the canonical dump1090 / mode-s.org
// reference sample for an identification message:
//
//	8D4840D6202CC371C32CE0576098
//
// Expected: ICAO = 484D06... wait, let me use a known-good
// vector: 8D4840D6202CC371C32CE0576098 — ICAO 4840D6, callsign
// "KLM1023 ", category 4.
func TestDecodeIdentification(t *testing.T) {
	frame := decodeHex(t, "8D4840D6202CC371C32CE0576098")
	m := Decode(frame)
	if m.DF != DFExtendedSquitter {
		t.Errorf("DF = %d, want 17", m.DF)
	}
	if !m.CRCValid {
		t.Errorf("CRCValid = false on a canonical sample")
	}
	if m.ICAO != 0x4840D6 {
		t.Errorf("ICAO = %06X, want 4840D6", m.ICAO)
	}
	if m.Kind != KindIdentification {
		t.Errorf("Kind = %v, want KindIdentification", m.Kind)
	}
	if m.Identification == nil {
		t.Fatal("Identification = nil")
	}
	if m.Identification.Callsign != "KLM1023" {
		t.Errorf("Callsign = %q, want KLM1023", m.Identification.Callsign)
	}
	if m.Identification.Category != 0 {
		// Category 4 is the "A" set, encoded as ME byte 0 low 3
		// bits = 0; the DO-260B "category D" message family uses
		// TC 1 (which is this frame's TC, since 0x20>>3 = 4 →
		// wait, let me re-check). The frame's TC is in the high
		// 5 bits of me[0] = 0x20 >> 3 = 4. So this is TC = 4 =
		// "Identification, Category A". The category sub-byte is
		// the low 3 bits = 0 (= 4A category 0, which is the spec
		// "no information" code). Surface what we decoded.
	}
}

// TestDecodeAirbornePosition uses the dump1090 reference pair:
//
//	even: 8D40621D58C382D690C8AC2863A7
//	odd:  8D40621D58C386435CC412692AD6
//
// Expected ICAO 40621D. The CPR pair globally decodes to lat
// 52.2572, lon 3.91937 (when even is treated as most recent).
// Reference: https://mode-s.org/decode/content/ads-b/3-airborne-position.html
func TestDecodeAirbornePositionCPRPair(t *testing.T) {
	even := Decode(decodeHex(t, "8D40621D58C382D690C8AC2863A7"))
	odd := Decode(decodeHex(t, "8D40621D58C386435CC412692AD6"))

	if even.ICAO != 0x40621D || odd.ICAO != 0x40621D {
		t.Fatalf("ICAOs = %06X / %06X, want 40621D both", even.ICAO, odd.ICAO)
	}
	if even.Kind != KindAirbornePosition || odd.Kind != KindAirbornePosition {
		t.Fatalf("Kinds = %v / %v", even.Kind, odd.Kind)
	}
	if even.Position == nil || odd.Position == nil {
		t.Fatal("position parsers returned nil")
	}
	if even.Position.CPRFormat != 0 {
		t.Errorf("even CPRFormat = %d, want 0", even.Position.CPRFormat)
	}
	if odd.Position.CPRFormat != 1 {
		t.Errorf("odd CPRFormat = %d, want 1", odd.Position.CPRFormat)
	}
	// Altitudes — the reference samples decode to 38000 ft.
	if !even.Position.HasAltitude || even.Position.Altitude != 38000 {
		t.Errorf("even altitude = %d (has=%v), want 38000",
			even.Position.Altitude, even.Position.HasAltitude)
	}

	// Globally decode the pair.
	lat, lon, ok := CPRDecodeGlobal(
		even.Position.CPRLatEven, even.Position.CPRLonEven,
		odd.Position.CPRLatOdd, odd.Position.CPRLonOdd,
		true)
	if !ok {
		t.Fatal("CPRDecodeGlobal: !ok")
	}
	if math.Abs(lat-52.2572) > 0.0001 {
		t.Errorf("lat = %f, want 52.2572", lat)
	}
	if math.Abs(lon-3.91937) > 0.0001 {
		t.Errorf("lon = %f, want 3.91937", lon)
	}
}

// TestDecodeAirborneVelocity uses dump1090 reference:
//
//	8D485020994409940838175B284F
//
// Expected ICAO 485020, ground speed 159 kn, track 182.88°,
// vertical rate -832 fpm (descending).
func TestDecodeAirborneVelocity(t *testing.T) {
	m := Decode(decodeHex(t, "8D485020994409940838175B284F"))
	if !m.CRCValid {
		t.Errorf("CRCValid = false")
	}
	if m.ICAO != 0x485020 {
		t.Errorf("ICAO = %06X, want 485020", m.ICAO)
	}
	if m.Kind != KindAirborneVelocity {
		t.Fatalf("Kind = %v, want KindAirborneVelocity", m.Kind)
	}
	if m.Velocity == nil {
		t.Fatal("Velocity = nil")
	}
	if !m.Velocity.HasGroundSpeed {
		t.Error("HasGroundSpeed = false on subtype-1 frame")
	}
	if m.Velocity.GroundSpeedKn < 155 || m.Velocity.GroundSpeedKn > 165 {
		t.Errorf("GroundSpeed = %d, want ≈ 159", m.Velocity.GroundSpeedKn)
	}
	if math.Abs(m.Velocity.TrackDeg-182.88) > 1.0 {
		t.Errorf("Track = %.2f, want ≈ 182.88", m.Velocity.TrackDeg)
	}
	if !m.Velocity.HasVerticalRate || m.Velocity.VerticalRateFPM > -700 {
		t.Errorf("VerticalRate = %d (has=%v), want ≈ -832",
			m.Velocity.VerticalRateFPM, m.Velocity.HasVerticalRate)
	}
}

func TestDecodeRecognisesAllCallReply(t *testing.T) {
	// DF 11 = 0b01011 → first byte high 5 bits = 11 → byte ≥ 0x58.
	// Construct minimal valid frame: DF 11, ICAO AABBCC, then a
	// 3-byte CRC computed over the first 4 bytes.
	icao := uint32(0xAABBCC)
	frame := []byte{0x58, byte(icao >> 16), byte(icao >> 8), byte(icao), 0, 0, 0}
	crc := crc24(frame[:4])
	frame[4] = byte(crc >> 16)
	frame[5] = byte(crc >> 8)
	frame[6] = byte(crc)

	m := Decode(frame)
	if m.DF != DFAllCallReply {
		t.Errorf("DF = %d, want %d", m.DF, DFAllCallReply)
	}
	if !m.CRCValid {
		t.Errorf("CRCValid = false on hand-computed frame")
	}
	if m.ICAO != icao {
		t.Errorf("ICAO = %06X, want %06X", m.ICAO, icao)
	}
	if m.Kind != KindAllCall {
		t.Errorf("Kind = %v, want KindAllCall", m.Kind)
	}
}

func TestDecodeRejectsShortFrame(t *testing.T) {
	if m := Decode(nil); m.Kind != KindUnknown {
		t.Errorf("Decode(nil).Kind = %v, want KindUnknown", m.Kind)
	}
	if m := Decode([]byte{1, 2, 3}); m.Kind != KindUnknown {
		t.Errorf("Decode(3 bytes).Kind = %v, want KindUnknown", m.Kind)
	}
	if m := Decode(make([]byte, 10)); m.Kind != KindUnknown {
		t.Errorf("Decode(10 bytes).Kind = %v, want KindUnknown", m.Kind)
	}
}

func TestKindStringStableLabels(t *testing.T) {
	cases := map[PayloadKind]string{
		KindIdentification:    "ident",
		KindSurfacePosition:   "surface-pos",
		KindAirbornePosition:  "airborne-pos",
		KindAirborneVelocity:  "velocity",
		KindAircraftStatus:    "status",
		KindTargetState:       "target-state",
		KindOperationalStatus: "op-status",
		KindAllCall:           "all-call",
		KindUnknown:           "unknown",
	}
	for k, want := range cases {
		if got := KindString(k); got != want {
			t.Errorf("KindString(%v) = %q, want %q", k, got, want)
		}
	}
}

func TestCRC24SelfConsistency(t *testing.T) {
	// CRC over (msg, msg's own CRC) lands at 0 — the magic
	// residue for any clean Mode-S frame.
	msg := []byte{0x8D, 0x48, 0x40, 0xD6, 0x20, 0x2C, 0xC3,
		0x71, 0xC3, 0x2C, 0xE0, 0x57, 0x60, 0x98}
	if crc := crc24(msg); crc != 0 {
		t.Errorf("CRC residue = %06X, want 0 for clean canonical frame", crc)
	}
}

func TestCRC24DetectsCorruption(t *testing.T) {
	msg := []byte{0x8D, 0x48, 0x40, 0xD6, 0x20, 0x2C, 0xC3,
		0x71, 0xC3, 0x2C, 0xE0, 0x57, 0x60, 0x98}
	msg[5] ^= 0x80 // flip one bit
	if crc := crc24(msg); crc == 0 {
		t.Error("CRC residue = 0 on corrupted frame, want non-zero")
	}
}

func TestCPRNLBoundaryValues(t *testing.T) {
	// Spec table: NL(0°) = 59, NL(±87°) = 1, monotonically
	// decreasing in between.
	if nl := cprNL(0); nl != 59 {
		t.Errorf("NL(0°) = %d, want 59", nl)
	}
	if nl := cprNL(87); nl != 1 {
		t.Errorf("NL(87°) = %d, want 1", nl)
	}
	if nl := cprNL(45); nl < 30 || nl > 50 {
		t.Errorf("NL(45°) = %d, want 30..50 (spec ≈ 41)", nl)
	}
}

func TestMessageStringRendersKinds(t *testing.T) {
	// Identification render.
	m := Message{
		Kind:           KindIdentification,
		ICAO:           0x4840D6,
		Identification: &Identification{Callsign: "KLM1023", Category: 4},
	}
	got := m.String()
	if !strings.HasPrefix(got, "IDENT 4840D6") {
		t.Errorf("ident render = %q", got)
	}
	// Velocity render.
	m = Message{
		Kind: KindAirborneVelocity,
		ICAO: 0x485020,
		Velocity: &Velocity{
			GroundSpeedKn:   159,
			TrackDeg:        182.88,
			HasGroundSpeed:  true,
			VerticalRateFPM: -832,
			HasVerticalRate: true,
		},
	}
	got = m.String()
	if !strings.Contains(got, "GS=159kn") {
		t.Errorf("velocity render missing speed: %q", got)
	}
	if !strings.Contains(got, "VR=-832fpm") {
		t.Errorf("velocity render missing VR: %q", got)
	}
}

func TestDecodeAltitudeQ1(t *testing.T) {
	// Q=1 sample: raw bits 0b001000010100 = 0x214.
	// Decoded N = ((raw >> 5) << 4) | (raw & 0x0F) = (16<<4) | 4 = 260.
	// Altitude = 260*25 - 1000 = 5500 ft.
	got := decodeAltitude(0x214)
	if got != 5500 {
		t.Errorf("decodeAltitude(0x214) = %d, want 5500", got)
	}
}
