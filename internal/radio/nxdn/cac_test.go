package nxdn

import "testing"

func TestCACAssembleParseRoundTrip(t *testing.T) {
	in := CACMessage{
		Type:    RCCHVCALL,
		Payload: [8]byte{0x80, 0xAB, 0xCD, 0x12, 0x34, 0, 0, 0},
	}
	bytes := AssembleCAC(in)
	if len(bytes) != 11 {
		t.Fatalf("AssembleCAC = %d bytes, want 11", len(bytes))
	}
	out, err := ParseCAC(bytes)
	if err != nil {
		t.Fatalf("ParseCAC: %v", err)
	}
	if out != in {
		t.Errorf("round-trip:\n got %+v\nwant %+v", out, in)
	}
}

func TestCACDetectsCRCError(t *testing.T) {
	in := CACMessage{Type: RCCHSITEINFO, Payload: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}}
	b := AssembleCAC(in)
	b[5] ^= 0x10
	_, err := ParseCAC(b)
	if err != CRCError {
		t.Errorf("err = %v, want CRCError", err)
	}
}

func TestParseVCall(t *testing.T) {
	p := [8]byte{0x80, 0x12, 0x34, 0x56, 0x78, 0xAB, 0xCD, 0xEF}
	v := ParseVCall(p)
	if v.ServiceOptions != 0x80 {
		t.Errorf("ServiceOptions = %02X", v.ServiceOptions)
	}
	if v.GroupAddress != 0x1234 || v.SourceID != 0x5678 {
		t.Errorf("Group/Source = %04X/%04X", v.GroupAddress, v.SourceID)
	}
}

func TestParseSiteInfo(t *testing.T) {
	p := [8]byte{0x12, 0x34, 0x05, 0x06, 0xCA, 0xFE, 0, 0}
	s := ParseSiteInfo(p)
	if s.LocationID != 0x1234 {
		t.Errorf("LocationID = %04X", s.LocationID)
	}
	if s.SystemID != 0xCAFE {
		t.Errorf("SystemID = %04X", s.SystemID)
	}
}

func TestRCCHTypeString(t *testing.T) {
	cases := map[RCCHType]string{
		RCCHVCALL:      "VCALL",
		RCCHSITEINFO:   "SITE_INFO",
		RCCHCCH:        "CCH_ANNOUNCE",
		RCCHType(0x77): "RCCHType(77)",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("%X.String() = %s, want %s", uint8(r), got, want)
		}
	}
}
