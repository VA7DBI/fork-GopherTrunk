package ax25

import (
	"bytes"
	"testing"
)

// encodeAddress packs an Address into the 7-byte AX.25 wire format.
// Test helper — mirrors what a transmitter does.
func encodeAddress(a Address, last, hOrC bool) []byte {
	out := make([]byte, 7)
	cs := a.Callsign
	for len(cs) < 6 {
		cs += " "
	}
	for i := 0; i < 6; i++ {
		out[i] = cs[i] << 1
	}
	ssid := byte(0b01100000) // reserved bits 5 + 6 set per the spec
	ssid |= (a.SSID & 0x0F) << 1
	if hOrC {
		ssid |= 0x80
	}
	if last {
		ssid |= 0x01
	}
	out[6] = ssid
	return out
}

// buildFrame assembles a fully-formed AX.25 frame body and appends
// a correct FCS.
func buildFrame(t *testing.T, dst, src Address, path []Address, control, pid byte, info []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write(encodeAddress(dst, false, false))
	buf.Write(encodeAddress(src, len(path) == 0, true))
	for i, p := range path {
		last := i == len(path)-1
		buf.Write(encodeAddress(p, last, p.HBit))
	}
	buf.WriteByte(control)
	buf.WriteByte(pid)
	buf.Write(info)
	fcs := computeFCS(buf.Bytes())
	buf.WriteByte(byte(fcs & 0xFF))
	buf.WriteByte(byte(fcs >> 8))
	return buf.Bytes()
}

func TestParseRoundTrip(t *testing.T) {
	dst := Address{Callsign: "APRS", SSID: 0}
	src := Address{Callsign: "W1AW", SSID: 9}
	path := []Address{
		{Callsign: "WIDE1", SSID: 1, HBit: false},
		{Callsign: "WIDE2", SSID: 1, HBit: true},
	}
	info := []byte(`!4903.50N/07201.75W-Test`)
	body := buildFrame(t, dst, src, path, 0x03, 0xF0, info)

	f, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !f.FCSOK {
		t.Error("FCSOK = false on clean frame; want true")
	}
	if f.Src.String() != "W1AW-9" {
		t.Errorf("src = %q, want W1AW-9", f.Src.String())
	}
	if !f.IsUI() {
		t.Error("IsUI() = false on UI/PID-0xF0 frame")
	}
	if got := f.PathString(); got != "WIDE1-1,WIDE2-1*" {
		t.Errorf("PathString = %q, want WIDE1-1,WIDE2-1*", got)
	}
	if !bytes.Equal(f.Info, info) {
		t.Errorf("info mismatch")
	}
}

func TestParseDetectsCorruptedFCS(t *testing.T) {
	body := buildFrame(t,
		Address{Callsign: "APRS"},
		Address{Callsign: "W1AW", SSID: 9},
		nil, 0x03, 0xF0, []byte("test"))
	body[len(body)-3] ^= 0x80
	f, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.FCSOK {
		t.Error("FCSOK = true after bit-flip; want false")
	}
}

func TestParseRejectsTooShort(t *testing.T) {
	if _, err := Parse(make([]byte, 10)); err != ErrFrameTooShort {
		t.Errorf("Parse(short) = %v, want ErrFrameTooShort", err)
	}
}

func TestParseRejectsMissingEndOfAddress(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(encodeAddress(Address{Callsign: "APRS"}, false, false))
	buf.Write(encodeAddress(Address{Callsign: "W1AW", SSID: 9}, false, true))
	for i := 0; i < 9; i++ {
		buf.Write(encodeAddress(Address{Callsign: "DIGI", SSID: uint8(i)}, false, false))
	}
	buf.WriteByte(0x03)
	buf.WriteByte(0xF0)
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)
	if _, err := Parse(buf.Bytes()); err != ErrBadAddress {
		t.Errorf("Parse(no-end) = %v, want ErrBadAddress", err)
	}
}

func TestAddressStringFormats(t *testing.T) {
	for _, c := range []struct {
		a    Address
		want string
	}{
		{Address{Callsign: "W1AW", SSID: 0}, "W1AW"},
		{Address{Callsign: "W1AW", SSID: 9}, "W1AW-9"},
		{Address{Callsign: "WIDE2", SSID: 1, HBit: true}, "WIDE2-1*"},
	} {
		if got := c.a.String(); got != c.want {
			t.Errorf("%+v.String() = %q, want %q", c.a, got, c.want)
		}
	}
}

func TestParseTrimsCallsignSpaces(t *testing.T) {
	body := buildFrame(t,
		Address{Callsign: "AB"},
		Address{Callsign: "CD", SSID: 3},
		nil, 0x03, 0xF0, []byte(""))
	f, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Dst.Callsign != "AB" || f.Src.Callsign != "CD" {
		t.Errorf("dst.Callsign=%q src.Callsign=%q", f.Dst.Callsign, f.Src.Callsign)
	}
}

func TestPathStringEmpty(t *testing.T) {
	body := buildFrame(t,
		Address{Callsign: "APRS"},
		Address{Callsign: "W1AW"},
		nil, 0x03, 0xF0, []byte(""))
	f, _ := Parse(body)
	if got := f.PathString(); got != "" {
		t.Errorf("PathString = %q, want \"\"", got)
	}
}

func TestNonUIFrameRecognised(t *testing.T) {
	body := buildFrame(t,
		Address{Callsign: "DEST"},
		Address{Callsign: "SRC"},
		nil, 0x00, 0xCC, []byte(""))
	f, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.IsUI() {
		t.Error("IsUI() = true on I-frame; want false")
	}
}
