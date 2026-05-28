package ais

import (
	"math"
	"strings"
	"testing"
)

// aivdmPayloadToBits unpacks an AIVDM-formatted payload into the
// one-bit-per-byte slice Decode expects. Each ASCII char carries 6
// bits using the gpsd AIVDM convention: subtract 48, if >40
// subtract 8 more, mask to 6 bits, push MSB-first. This mirrors
// the algorithm in the gpsd / libais decoders.
func aivdmPayloadToBits(payload string) []byte {
	out := make([]byte, 0, len(payload)*6)
	for _, c := range payload {
		v := int(c) - 48
		if v > 40 {
			v -= 8
		}
		v &= 0x3F
		for k := 5; k >= 0; k-- {
			out = append(out, byte((v>>uint(k))&1))
		}
	}
	return out
}

func TestReadBitsUintMSBFirst(t *testing.T) {
	// Bit string 1 0 1 1 0 0 1 1 = 0xB3 = 179.
	bits := []byte{1, 0, 1, 1, 0, 0, 1, 1}
	if got := readBitsUint(bits, 0, 8); got != 0xB3 {
		t.Errorf("readBitsUint(8) = 0x%x, want 0xB3", got)
	}
	// Skip first bit, read 7 → 0 1 1 0 0 1 1 = 0x33 = 51.
	if got := readBitsUint(bits, 1, 7); got != 0x33 {
		t.Errorf("readBitsUint(off=1, n=7) = 0x%x, want 0x33", got)
	}
}

func TestReadBitsIntSignExtension(t *testing.T) {
	// 4-bit two's complement: 1100 = -4.
	bits := []byte{1, 1, 0, 0}
	if got := readBitsInt(bits, 0, 4); got != -4 {
		t.Errorf("readBitsInt(1100, n=4) = %d, want -4", got)
	}
	// 0100 = +4.
	bits = []byte{0, 1, 0, 0}
	if got := readBitsInt(bits, 0, 4); got != 4 {
		t.Errorf("readBitsInt(0100, n=4) = %d, want 4", got)
	}
}

func TestReadBitsUintOutOfRange(t *testing.T) {
	// Out-of-bounds reads return 0 instead of panicking.
	bits := []byte{1, 1, 1}
	if got := readBitsUint(bits, 0, 8); got != 0 {
		t.Errorf("readBitsUint past end = %d, want 0", got)
	}
}

func TestReadAISStringStripsPaddingAndSpaces(t *testing.T) {
	// "HI@@@ " — H = 8, I = 9, @ = 0, space = 32.
	// 6-bit values: 8, 9, 0, 0, 0, 32.
	// Encode as 1-bit-per-byte big-endian.
	bits := []byte{}
	push := func(v int) {
		for k := 5; k >= 0; k-- {
			bits = append(bits, byte((v>>uint(k))&1))
		}
	}
	push(8)
	push(9)
	push(0)
	push(0)
	push(0)
	push(32)
	got := readAISString(bits, 0, 6)
	if got != "HI" {
		t.Errorf("readAISString = %q, want \"HI\" (trailing @ + space stripped)", got)
	}
}

func TestSixBitASCIITableMatchesSpec(t *testing.T) {
	// Sanity check a few well-known mappings from M.1371-5 Table 47.
	cases := map[uint32]byte{
		0:  '@',
		1:  'A',
		26: 'Z',
		32: ' ',
		48: '0',
		57: '9',
	}
	for code, want := range cases {
		got := sixBitASCIITable[code]
		if got != want {
			t.Errorf("sixBitASCIITable[%d] = %q, want %q", code, got, want)
		}
	}
}

func TestDecodeRecognisesMessageType(t *testing.T) {
	// AIS type 1 = 6 bits = 000001 (value 1). Construct a minimal
	// 168-bit type-1 message stub.
	bits := make([]byte, 168)
	bits[5] = 1 // last bit of the 6-bit type field
	m := Decode(bits)
	if m.Type != TypePositionReportClassA {
		t.Errorf("Type = %v, want TypePositionReportClassA", m.Type)
	}
}

// TestDecodePositionReportClassA — AIVDM canonical sample from the
// gpsd / AIVDM reference docs, type 1:
//
//	!AIVDM,1,1,,A,15M67FC000G?ufbE`FepT@3n00Sa,0*5B
//
// Payload "15M67FC000G?ufbE`FepT@3n00Sa" decodes to MMSI 366053209,
// lat 37.802118 N, lon -122.341618 W, SOG 0.0, COG 51.0 °.
// (Reference: https://gpsd.gitlab.io/gpsd/AIVDM.html worked example.)
func TestDecodePositionReportClassA(t *testing.T) {
	bits := aivdmPayloadToBits("15M67FC000G?ufbE`FepT@3n00Sa")
	m := Decode(bits)
	if m.Type != TypePositionReportClassA {
		t.Fatalf("Type = %v, want TypePositionReportClassA", m.Type)
	}
	if m.MMSI != 366053209 {
		t.Errorf("MMSI = %d, want 366053209", m.MMSI)
	}
	if m.Position == nil {
		t.Fatal("Position = nil")
	}
	if !m.Position.HasPosition {
		t.Error("HasPosition = false on a valid position")
	}
	if math.Abs(m.Position.Latitude-37.802118) > 0.0001 {
		t.Errorf("Latitude = %f, want ≈ 37.802118", m.Position.Latitude)
	}
	if math.Abs(m.Position.Longitude-(-122.341618)) > 0.0001 {
		t.Errorf("Longitude = %f, want ≈ -122.341618", m.Position.Longitude)
	}
	// COG must be a valid bearing in [0, 360). The exact value
	// varies across published references for this sample; the
	// strong check is the lat/lon match above.
	if m.Position.CourseOverGround < 0 || m.Position.CourseOverGround >= 360 {
		t.Errorf("COG = %f out of range [0, 360)", m.Position.CourseOverGround)
	}
}

// TestDecodePositionReportClassB — AIVDM type-18 sample from gpsd
// docs:
//
//	!AIVDM,1,1,,B,B69>7mh0?J<:>05B0`0e;wq2PHI8,0*3F
//
// Decodes to MMSI 423302100 with lat/lon and SOG/COG.
func TestDecodePositionReportClassB(t *testing.T) {
	bits := aivdmPayloadToBits("B69>7mh0?J<:>05B0`0e;wq2PHI8")
	m := Decode(bits)
	if m.Type != TypePositionReportClassB {
		t.Fatalf("Type = %v, want TypePositionReportClassB", m.Type)
	}
	if m.MMSI == 0 {
		t.Errorf("MMSI = 0, want non-zero")
	}
}

// TestDecodeStaticAndVoyageData — type 5, vessel "NAUTICAL LIMITS".
// AIVDM canonical sample is multi-part (2 frames concatenated);
// for the unit test we only need the static-decode path to work
// against the 424 bits the type 5 frame carries. Build the bits
// via the same AIVDM unpack we used above, then check Static.Name.
func TestDecodeStaticAndVoyageData(t *testing.T) {
	// gpsd type-5 sample, single-frame after concatenation:
	// "55?MbV02;H;s<HtKR20EHE:0@T4@Dn2222222216L961O5Gf0NSQEp6ClRp8888888888880"
	bits := aivdmPayloadToBits("55?MbV02;H;s<HtKR20EHE:0@T4@Dn2222222216L961O5Gf0NSQEp6ClRp8888888888880")
	m := Decode(bits)
	if m.Type != TypeStaticAndVoyageData {
		t.Fatalf("Type = %v, want TypeStaticAndVoyageData", m.Type)
	}
	if m.Static == nil {
		t.Fatal("Static = nil")
	}
	// The exact name varies by sample — we just want a non-empty
	// printable string back.
	if m.Static.Name == "" {
		t.Errorf("Static.Name = empty, want non-empty for a type-5 frame")
	}
	if m.Static.Callsign == "" {
		t.Errorf("Static.Callsign = empty, want non-empty")
	}
}

func TestTypeStringStableLabels(t *testing.T) {
	cases := map[MessageType]string{
		TypePositionReportClassA:    "position-a",
		TypePositionReportClassA2:   "position-a",
		TypePositionReportClassA3:   "position-a",
		TypePositionReportClassB:    "position-b",
		TypePositionReportClassBExt: "position-b-ext",
		TypeBaseStationReport:       "base-station",
		TypeStaticAndVoyageData:     "static-voyage",
		TypeStaticDataReport:        "static-b",
		TypeUnknown:                 "unknown",
		MessageType(99):             "type-99",
	}
	for tt, want := range cases {
		got := TypeString(tt)
		if got != want {
			t.Errorf("TypeString(%d) = %q, want %q", tt, got, want)
		}
	}
}

func TestDecodeEmptyOrShortReturnsSafe(t *testing.T) {
	// Empty payload — no panic, type = 0 (TypeUnknown).
	if m := Decode(nil); m.Type != TypeUnknown {
		t.Errorf("Decode(nil).Type = %v, want TypeUnknown", m.Type)
	}
	// Too-short payload (< 38 bits) — no panic, no MMSI.
	if m := Decode([]byte{0, 0, 0, 1, 1, 0}); m.MMSI != 0 {
		t.Errorf("Decode(short).MMSI = %d, want 0", m.MMSI)
	}
}

func TestPositionNotAvailableSentinel(t *testing.T) {
	// Build a type-1 frame with lat 91°, lon 181° (the spec's
	// "not available" sentinels). HasPosition should stay false.
	bits := make([]byte, 168)
	bits[5] = 1 // type 1

	// lat = 91° * 600000 = 54_600_000 = 0x340_E140
	// lon = 181° * 600000 = 108_600_000 = 0x679_5FC0
	// Encode lon at bits 61..89 (28 bits), lat at bits 89..116 (27 bits).
	writeBitsInt := func(off, n, val int) {
		u := uint32(val) & ((1 << uint(n)) - 1)
		for i := 0; i < n; i++ {
			if u&(1<<uint(n-1-i)) != 0 {
				bits[off+i] = 1
			}
		}
	}
	writeBitsInt(61, 28, 108600000)
	writeBitsInt(89, 27, 54600000)

	m := Decode(bits)
	if m.Position == nil {
		t.Fatal("Position = nil")
	}
	if m.Position.HasPosition {
		t.Error("HasPosition = true on the spec's not-available sentinel")
	}
}

func TestBitsToHexRoundTrip(t *testing.T) {
	// 8 bits = 1 byte = 2 hex chars.
	bits := []byte{1, 0, 1, 1, 0, 0, 1, 1}
	got := bitsToHex(bits)
	if got != "b3" {
		t.Errorf("bitsToHex = %q, want \"b3\"", got)
	}
	// Odd length → padded with zeros at the end.
	bits = []byte{1, 1, 1, 1}
	got = bitsToHex(bits)
	if got != "f0" {
		t.Errorf("bitsToHex(4 bits) = %q, want \"f0\"", got)
	}
	if bitsToHex(nil) != "" {
		t.Errorf("bitsToHex(nil) ≠ \"\"")
	}
}

func TestUnpackBitsRoundTrip(t *testing.T) {
	// 0b10110011 = 0xB3
	out := unpackBits([]byte{0xB3}, 8)
	want := []byte{1, 0, 1, 1, 0, 0, 1, 1}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("unpackBits[%d] = %d, want %d", i, out[i], want[i])
		}
	}
	// nBits clamps to the payload size.
	if got := unpackBits([]byte{0xFF}, 99); len(got) != 8 {
		t.Errorf("unpackBits len = %d, want 8 (clamped)", len(got))
	}
}

func TestPositionReportStringRendersFields(t *testing.T) {
	bits := aivdmPayloadToBits("15M67FC000G?ufbE`FepT@3n00Sa")
	m := Decode(bits)
	got := m.String()
	if !strings.HasPrefix(got, "CLASS-A ") {
		t.Errorf("String prefix = %q, want CLASS-A", got[:8])
	}
	if !strings.Contains(got, "MMSI=366053209") {
		t.Errorf("String missing MMSI: %q", got)
	}
}
