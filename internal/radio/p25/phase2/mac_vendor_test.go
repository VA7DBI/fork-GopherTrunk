package phase2

import (
	"bytes"
	"testing"
)

func TestIsManufacturerSpecific(t *testing.T) {
	for _, o := range []Opcode{0x80, 0x81, 0xA0, 0xBF} {
		if !o.IsManufacturerSpecific() {
			t.Errorf("%#x should be manufacturer-specific", uint8(o))
		}
	}
	for _, o := range []Opcode{0x00, 0x44, 0x7D, 0xC0, 0xFB} {
		if o.IsManufacturerSpecific() {
			t.Errorf("%#x should not be manufacturer-specific", uint8(o))
		}
	}
}

// TestMACPDUVendorRoundTrip confirms a manufacturer-specific PDU carries
// its MFID through Assemble → Parse, and that a standard PDU does not
// grow an MFID octet.
func TestMACPDUVendorRoundTrip(t *testing.T) {
	payload := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	vendor := MACPDU{Opcode: OpVendorGroupRegroup, MFID: MFIDMotorola, Payload: payload}
	b := AssembleMACPDU(vendor)
	if b[0] != byte(OpVendorGroupRegroup) || b[1] != MFIDMotorola {
		t.Fatalf("assembled vendor header = %02X %02X, want 81 90", b[0], b[1])
	}
	got, err := ParseMACPDU(b)
	if err != nil {
		t.Fatalf("ParseMACPDU: %v", err)
	}
	if got.Opcode != OpVendorGroupRegroup || got.MFID != MFIDMotorola {
		t.Errorf("parsed opcode/MFID = %v/%02X", got.Opcode, got.MFID)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Errorf("parsed payload = %x, want %x", got.Payload, payload)
	}

	std := MACPDU{Opcode: OpGroupVoiceChannelGrant, Payload: payload}
	sb := AssembleMACPDU(std)
	if len(sb) != 1+len(payload) {
		t.Errorf("standard PDU assembled length = %d, want %d (no MFID octet)",
			len(sb), 1+len(payload))
	}
	sgot, _ := ParseMACPDU(sb)
	if sgot.MFID != MFIDStandard {
		t.Errorf("standard PDU MFID = %02X, want 0", sgot.MFID)
	}
}

func TestAsMotorolaPatchGroup(t *testing.T) {
	pdu := MACPDU{
		Opcode:  OpVendorGroupRegroup,
		MFID:    MFIDMotorola,
		Payload: []byte{0x12, 0x34, 0x00, 0x0A, 0x00, 0x0B, 0x00, 0x00},
	}
	g, ok := pdu.AsMotorolaPatchGroup()
	if !ok {
		t.Fatal("AsMotorolaPatchGroup returned !ok")
	}
	if g.SuperGroup != 0x1234 {
		t.Errorf("SuperGroup = %04X, want 1234", g.SuperGroup)
	}
	want := []uint16{0x000A, 0x000B}
	if len(g.Patched) != len(want) {
		t.Fatalf("Patched = %v, want %v", g.Patched, want)
	}
	for i := range want {
		if g.Patched[i] != want[i] {
			t.Errorf("Patched[%d] = %04X, want %04X", i, g.Patched[i], want[i])
		}
	}
}

func TestAsHarrisRegroup(t *testing.T) {
	pdu := MACPDU{
		Opcode:  OpVendorGroupRegroup,
		MFID:    MFIDHarris,
		Payload: []byte{0x05, 0x55, 0x00, 0xBE, 0xEF},
	}
	r, ok := pdu.AsHarrisRegroup()
	if !ok {
		t.Fatal("AsHarrisRegroup returned !ok")
	}
	if r.RegroupGroup != 0x0555 || r.TargetID != 0x00BEEF {
		t.Errorf("HarrisRegroup = %+v, want {0x555, 0xBEEF}", r)
	}
}

// TestVendorDispatchByMFID is the core MFID-dispatch check: one opcode
// value decodes as a different message per manufacturer.
func TestVendorDispatchByMFID(t *testing.T) {
	moto := MACPDU{Opcode: OpVendorGroupRegroup, MFID: MFIDMotorola,
		Payload: []byte{0x12, 0x34, 0, 0, 0, 0, 0, 0}}
	if _, ok := moto.AsMotorolaPatchGroup(); !ok {
		t.Error("Motorola PDU: AsMotorolaPatchGroup returned !ok")
	}
	if _, ok := moto.AsHarrisRegroup(); ok {
		t.Error("Motorola PDU: AsHarrisRegroup should return !ok")
	}

	harris := MACPDU{Opcode: OpVendorGroupRegroup, MFID: MFIDHarris,
		Payload: []byte{0x05, 0x55, 0, 0, 0}}
	if _, ok := harris.AsHarrisRegroup(); !ok {
		t.Error("Harris PDU: AsHarrisRegroup returned !ok")
	}
	if _, ok := harris.AsMotorolaPatchGroup(); ok {
		t.Error("Harris PDU: AsMotorolaPatchGroup should return !ok")
	}
}

func TestManufacturerName(t *testing.T) {
	cases := map[uint8]string{
		MFIDStandard: "Standard",
		MFIDMotorola: "Motorola",
		MFIDHarris:   "Harris",
	}
	for mfid, want := range cases {
		if got := ManufacturerName(mfid); got != want {
			t.Errorf("ManufacturerName(%02X) = %q, want %q", mfid, got, want)
		}
	}
}
