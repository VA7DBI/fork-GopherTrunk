package nxdn

import "testing"

func TestLICHRoundTrip(t *testing.T) {
	cases := []LICH{
		{RFCh: RFChControl, FCT: FCTNSACCH, Option: 0, Direction: DirectionOutbound},
		{RFCh: RFChTraffic, FCT: FCTNUDCH, Option: 1, Direction: DirectionInbound},
		{RFCh: RFChControl, FCT: FCTFrameStep, Option: 2, Direction: DirectionOutbound},
		{RFCh: RFChTraffic, FCT: FCTReserved, Option: 3, Direction: DirectionInbound},
	}
	for _, in := range cases {
		b := AssembleLICH(in)
		out := ParseLICH(b)
		if !out.ParityOK {
			t.Errorf("%+v: parity should be OK after assemble", in)
		}
		if out.RFCh != in.RFCh || out.FCT != in.FCT || out.Option != in.Option || out.Direction != in.Direction {
			t.Errorf("round-trip:\n got %+v\nwant %+v", out, in)
		}
	}
}

func TestLICHParityDetectsSingleError(t *testing.T) {
	in := LICH{RFCh: RFChControl, FCT: FCTNSACCH, Option: 1, Direction: DirectionOutbound}
	b := AssembleLICH(in)
	for bit := 0; bit < 8; bit++ {
		corrupted := b ^ (1 << uint(bit))
		out := ParseLICH(corrupted)
		if out.ParityOK {
			t.Errorf("bit %d flip: ParityOK = true, expected false", bit)
		}
	}
}

func TestLICHWireDoubleAndDecode(t *testing.T) {
	in := LICH{RFCh: RFChControl, FCT: FCTNSACCH, Option: 0, Direction: DirectionOutbound}
	info := AssembleLICH(in)
	wire := EncodeLICHWire(info)
	if len(wire) != 16 {
		t.Fatalf("wire len = %d, want 16", len(wire))
	}
	got, errs := DecodeLICHWire(wire)
	if errs != 0 || got != info {
		t.Errorf("decode: got %02X errs %d, want %02X 0", got, errs, info)
	}
}

func TestLICHWireMajorityOnSingleErr(t *testing.T) {
	in := LICH{RFCh: RFChControl, FCT: FCTFrameStep, Option: 2, Direction: DirectionOutbound}
	info := AssembleLICH(in)
	wire := EncodeLICHWire(info)
	wire[0] ^= 1 // corrupt first copy of bit 0
	got, errs := DecodeLICHWire(wire)
	if errs != 1 {
		t.Errorf("disagreements = %d, want 1", errs)
	}
	// Soft decoder takes whichever copy came first; we just verify the
	// other 7 info bits are intact.
	if (got & 0x7F) != (info & 0x7F) {
		t.Errorf("non-corrupted bits changed: got %02X, info %02X", got, info)
	}
}

func TestRFChannelTypeString(t *testing.T) {
	if RFChControl.String() != "RCCH" || RFChTraffic.String() != "RDCH" {
		t.Errorf("RFCh strings wrong")
	}
}
