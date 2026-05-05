package phase1

import "testing"

func TestParseNID(t *testing.T) {
	// NAC = 0x293, DUID = TSDU (0x7).
	const wantNAC = uint16(0x293)
	const wantDUID = DUIDTrunkingSignaling
	bits := make([]byte, 64)
	v := uint16(wantNAC)
	for i := 0; i < 12; i++ {
		bits[i] = byte((v >> uint(11-i)) & 1)
	}
	d := uint8(wantDUID)
	for i := 0; i < 4; i++ {
		bits[12+i] = (d >> uint(3-i)) & 1
	}
	nid, err := ParseNID(bits)
	if err != nil {
		t.Fatalf("ParseNID: %v", err)
	}
	if nid.NAC != wantNAC {
		t.Errorf("NAC = %X, want %X", nid.NAC, wantNAC)
	}
	if nid.DUID != wantDUID {
		t.Errorf("DUID = %s, want %s", nid.DUID, wantDUID)
	}
}

func TestDUIDString(t *testing.T) {
	cases := map[DUID]string{
		DUIDHeader: "HDU", DUIDLogicalLink1: "LDU1", DUIDTrunkingSignaling: "TSDU",
		DUIDLogicalLink2: "LDU2", DUIDTerminatorWithLC: "TDULC",
	}
	for d, want := range cases {
		if got := d.String(); got != want {
			t.Errorf("DUID(%X).String() = %s, want %s", uint8(d), got, want)
		}
	}
}

func TestNIDFromDibits(t *testing.T) {
	bits := make([]byte, 64)
	bits[3] = 1   // NAC bit 8
	bits[14] = 1  // DUID bit 1
	dibits := make([]uint8, 32)
	for i := 0; i < 32; i++ {
		dibits[i] = (bits[2*i] << 1) | bits[2*i+1]
	}
	nid, err := NIDFromDibits(dibits)
	if err != nil {
		t.Fatalf("NIDFromDibits: %v", err)
	}
	want, _ := ParseNID(bits)
	if nid != want {
		t.Errorf("NIDFromDibits = %+v, want %+v", nid, want)
	}
}
