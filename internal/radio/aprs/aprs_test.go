package aprs

import (
	"math"
	"testing"
)

func TestDecodeUncompressedPositionNoTimestampNoMsg(t *testing.T) {
	info := []byte("!4903.50N/07201.75W-Test 001234")
	p := Decode(info)
	if p.Type != TypePosition {
		t.Fatalf("Type = %v, want TypePosition", p.Type)
	}
	if p.Position == nil {
		t.Fatal("Position is nil")
	}
	if math.Abs(p.Position.Latitude-49.0583) > 1e-3 {
		t.Errorf("lat = %f", p.Position.Latitude)
	}
	if math.Abs(p.Position.Longitude+72.0292) > 1e-3 {
		t.Errorf("lon = %f", p.Position.Longitude)
	}
	if p.Position.SymbolTable != '/' || p.Position.SymbolCode != '-' {
		t.Errorf("symbol = %c%c", p.Position.SymbolTable, p.Position.SymbolCode)
	}
	if p.Position.Comment != "Test 001234" {
		t.Errorf("comment = %q", p.Position.Comment)
	}
}

func TestDecodeUncompressedPositionWithMessaging(t *testing.T) {
	p := Decode([]byte("=4903.50N/07201.75W-"))
	if p.Type != TypePosition || !p.Position.WithMessaging {
		t.Errorf("WithMessaging = false on '='")
	}
}

func TestDecodeSouthernAndWesternHemisphere(t *testing.T) {
	p := Decode([]byte("!3354.40S/15110.20W>"))
	if p.Position.Latitude > 0 || p.Position.Longitude > 0 {
		t.Errorf("expected negative S/W: %+v", p.Position)
	}
}

func TestDecodePositionWithTimestamp(t *testing.T) {
	p := Decode([]byte("/092345z4903.50N/07201.75W-"))
	if p.Type != TypePosition {
		t.Fatalf("Type = %v", p.Type)
	}
	if !p.Position.HasTimestamp || p.Position.TimestampRaw != "092345z" {
		t.Errorf("timestamp = %+v", p.Position)
	}
}

func TestDecodeMessage(t *testing.T) {
	p := Decode([]byte(":W1AW-1   :Hello from W2XYZ{42}"))
	if p.Type != TypeMessage {
		t.Fatalf("Type = %v", p.Type)
	}
	if p.Message.Addressee != "W1AW-1" || p.Message.Body != "Hello from W2XYZ" || p.Message.SeqNo != "42" {
		t.Errorf("Message = %+v", p.Message)
	}
}

func TestDecodeMessageAck(t *testing.T) {
	p := Decode([]byte(":W1AW-1   :ack42"))
	if !p.Message.Ack || p.Message.SeqNo != "42" {
		t.Errorf("Message = %+v", p.Message)
	}
}

func TestDecodeStatus(t *testing.T) {
	p := Decode([]byte(">Net control on 146.52"))
	if p.Type != TypeStatus || p.Status.Text != "Net control on 146.52" {
		t.Errorf("Status = %+v", p.Status)
	}
}

func TestDecodeBulletin(t *testing.T) {
	p := Decode([]byte(":BLN1     :Weekly net Tuesday"))
	if p.Type != TypeBulletin || p.Bulletin.ID != "BLN1" {
		t.Errorf("Bulletin = %+v", p.Bulletin)
	}
}

func TestDecodeMicE(t *testing.T) {
	p := Decode([]byte("`micE payload"))
	if p.Type != TypeMicE {
		t.Errorf("Type = %v, want TypeMicE", p.Type)
	}
}

func TestDecodeUnknownLeavesRawIntact(t *testing.T) {
	p := Decode([]byte("xxxxxx unknown payload"))
	if p.Type != TypeUnknown || p.Raw != "xxxxxx unknown payload" {
		t.Errorf("packet = %+v", p)
	}
}

func TestDecodeEmptyInfoSafe(t *testing.T) {
	p := Decode(nil)
	if p.Type != TypeUnknown || p.Raw != "" {
		t.Errorf("packet = %+v", p)
	}
}

func TestPacketStringSwitchesOnType(t *testing.T) {
	for _, c := range []struct {
		info string
		want string
	}{
		{"!4903.50N/07201.75W-Test", "POSITION 49.0583,-72.0292 \"Test\""},
		{":W1AW-1   :Hi there", "MSG to W1AW-1: \"Hi there\""},
		{":W1AW-1   :ack9", "ACK to W1AW-1 seq=9"},
		{">on the net", "STATUS \"on the net\""},
	} {
		got := Decode([]byte(c.info)).String()
		if got != c.want {
			t.Errorf("Decode(%q).String() = %q, want %q", c.info, got, c.want)
		}
	}
}

func TestTypeString(t *testing.T) {
	cases := map[PacketType]string{
		TypePosition:  "position",
		TypeMessage:   "message",
		TypeStatus:    "status",
		TypeBulletin:  "bulletin",
		TypeObject:    "object",
		TypeMicE:      "mic-e",
		TypeWeather:   "weather",
		TypeTelemetry: "telemetry",
		TypeUnknown:   "unknown",
	}
	for k, want := range cases {
		if got := TypeString(k); got != want {
			t.Errorf("TypeString(%d) = %q, want %q", k, got, want)
		}
	}
}
