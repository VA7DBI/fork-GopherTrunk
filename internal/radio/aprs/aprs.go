// Package aprs decodes the APRS info-field payload that rides on
// top of AX.25 (the link layer in package aprs/ax25). APRS is a
// huge, semi-formal protocol — the spec covers position, weather,
// message, telemetry, status, object, item, query, and bulletin
// formats, plus all the trailing-comment compressed variants.
//
// This package handles the operator-visible majority: position
// reports (uncompressed lat/lon NMEA-style), messages, status, and
// bulletins. Mic-E / weather / telemetry / object are recognised
// by their data-type indicator (DTI) byte but full decoders for
// those land in follow-ups.
//
// Spec references:
//   - APRS Protocol Reference 1.0.1 (1998-08-07).
//     http://www.aprs.org/doc/APRS101.PDF
//   - aprs.fi parser source code as cross-check for the messy
//     real-world variants the spec doesn't quite pin down.
package aprs

import (
	"fmt"
	"strconv"
	"strings"
)

// PacketType is what the info field's first byte / leading pattern
// identifies the payload as. APRS uses an inline data-type
// indicator (DTI) byte for everything except the comment-only
// case; the parser dispatches on it.
type PacketType uint8

const (
	TypeUnknown   PacketType = iota
	TypePosition             // "!" / "=" / "/" / "@" prefixed lat/lon
	TypeStatus               // ">" prefixed free-text
	TypeMessage              // ":" addressee + ":" body
	TypeObject               // ";" — like position but for a named object
	TypeMicE                 // 0x1C / 0x1D / "'" / "`" — Mic-E compressed
	TypeWeather              // "_" / "#" / "*" — full weather report
	TypeTelemetry            // "T#" prefix
	TypeBulletin             // ":BLNxxxxx:" — addressed bulletin board
)

// TypeString returns a stable lowercase string label for a
// PacketType — used as the column value on the aprs_log SQLite
// table and the API DTO's `type` field.
func TypeString(t PacketType) string {
	switch t {
	case TypePosition:
		return "position"
	case TypeStatus:
		return "status"
	case TypeMessage:
		return "message"
	case TypeObject:
		return "object"
	case TypeMicE:
		return "mic-e"
	case TypeWeather:
		return "weather"
	case TypeTelemetry:
		return "telemetry"
	case TypeBulletin:
		return "bulletin"
	}
	return "unknown"
}

// Position is the decoded latitude / longitude payload from an
// uncompressed position report. APRS encodes latitude as DDMM.hhH
// (8 chars including the N/S hemisphere) and longitude as
// DDDMM.hhH (9 chars including E/W). Both hundredths-of-a-minute
// precision.
type Position struct {
	Latitude      float64
	Longitude     float64
	SymbolTable   byte
	SymbolCode    byte
	Comment       string
	HasTimestamp  bool
	TimestampRaw  string
	WithMessaging bool // true for "=" and "@" (messaging-capable)
}

// Message is the decoded payload for the message format:
// ":ADDRESSEE :body{seqno}".
type Message struct {
	Addressee string
	Body      string
	SeqNo     string
	Ack       bool
	Rej       bool
}

// Status is the decoded payload for the status format: ">body".
type Status struct {
	Text string
}

// Bulletin is the decoded payload for the bulletin format:
// ":BLNxxxxxx:body".
type Bulletin struct {
	ID   string
	Body string
}

// Packet is the decoded result of one APRS info field.
type Packet struct {
	Type     PacketType
	Position *Position
	Message  *Message
	Status   *Status
	Bulletin *Bulletin
	Raw      string
}

// Decode parses one APRS info field and returns a typed Packet.
// Never returns an error — unknown / malformed payloads come back
// as Type=TypeUnknown with the raw bytes preserved on the Raw
// field. APRS is messy; we surface what we can and pass through
// the rest.
func Decode(info []byte) Packet {
	p := Packet{Raw: string(info)}
	if len(info) == 0 {
		return p
	}
	dti := info[0]
	switch dti {
	case '!':
		if pos, ok := parseUncompressedPosition(string(info[1:]), false); ok {
			p.Type = TypePosition
			p.Position = &pos
			return p
		}
	case '=':
		if pos, ok := parseUncompressedPosition(string(info[1:]), true); ok {
			p.Type = TypePosition
			p.Position = &pos
			return p
		}
	case '/':
		if len(info) >= 8 {
			ts := string(info[1:8])
			if pos, ok := parseUncompressedPosition(string(info[8:]), false); ok {
				pos.HasTimestamp = true
				pos.TimestampRaw = ts
				p.Type = TypePosition
				p.Position = &pos
				return p
			}
		}
	case '@':
		if len(info) >= 8 {
			ts := string(info[1:8])
			if pos, ok := parseUncompressedPosition(string(info[8:]), true); ok {
				pos.HasTimestamp = true
				pos.TimestampRaw = ts
				p.Type = TypePosition
				p.Position = &pos
				return p
			}
		}
	case ':':
		if msg, bln, isBulletin, ok := parseMessage(string(info[1:])); ok {
			if isBulletin {
				p.Type = TypeBulletin
				p.Bulletin = bln
			} else {
				p.Type = TypeMessage
				p.Message = msg
			}
			return p
		}
	case '>':
		p.Type = TypeStatus
		p.Status = &Status{Text: string(info[1:])}
		return p
	case ';':
		p.Type = TypeObject
		return p
	case 0x1C, 0x1D, '`', '\'':
		p.Type = TypeMicE
		return p
	case '_':
		p.Type = TypeWeather
		return p
	case 'T':
		if len(info) >= 2 && info[1] == '#' {
			p.Type = TypeTelemetry
			return p
		}
	}
	return p
}

func parseUncompressedPosition(s string, withMsg bool) (Position, bool) {
	if len(s) < 19 {
		return Position{}, false
	}
	latStr := s[0:8]
	symTable := s[8]
	lonStr := s[9:18]
	symCode := s[18]
	comment := s[19:]

	lat, ok := parseLatLon(latStr, true)
	if !ok {
		return Position{}, false
	}
	lon, ok := parseLatLon(lonStr, false)
	if !ok {
		return Position{}, false
	}
	return Position{
		Latitude:      lat,
		Longitude:     lon,
		SymbolTable:   symTable,
		SymbolCode:    symCode,
		Comment:       comment,
		WithMessaging: withMsg,
	}, true
}

// parseLatLon decodes a DDMM.hhH (latitude) or DDDMM.hhH
// (longitude) string into decimal degrees. Negative south /
// west. APRS supports "ambiguity spaces" — digit positions sent
// as space to indicate reduced precision; treat as 0.
func parseLatLon(s string, isLat bool) (float64, bool) {
	wantLen := 8
	degDigits := 2
	if !isLat {
		wantLen = 9
		degDigits = 3
	}
	if len(s) != wantLen {
		return 0, false
	}
	clean := []byte(s)
	for i := 0; i < degDigits+4; i++ {
		if clean[i] == ' ' {
			clean[i] = '0'
		}
	}
	if clean[degDigits+2] != '.' {
		return 0, false
	}
	deg, err := strconv.ParseFloat(string(clean[:degDigits]), 64)
	if err != nil {
		return 0, false
	}
	min, err := strconv.ParseFloat(
		string(clean[degDigits:degDigits+2])+"."+string(clean[degDigits+3:degDigits+5]),
		64)
	if err != nil {
		return 0, false
	}
	val := deg + min/60
	hemi := clean[wantLen-1]
	if isLat {
		switch hemi {
		case 'N':
		case 'S':
			val = -val
		default:
			return 0, false
		}
	} else {
		switch hemi {
		case 'E':
		case 'W':
			val = -val
		default:
			return 0, false
		}
	}
	return val, true
}

func parseMessage(s string) (*Message, *Bulletin, bool, bool) {
	if len(s) < 10 || s[9] != ':' {
		return nil, nil, false, false
	}
	addr := strings.TrimRight(s[0:9], " ")
	body := s[10:]
	if strings.HasPrefix(addr, "BLN") {
		return nil, &Bulletin{ID: addr, Body: body}, true, true
	}
	msg := &Message{Addressee: addr, Body: body}
	if i := strings.LastIndex(body, "{"); i >= 0 && strings.HasSuffix(body, "}") {
		msg.SeqNo = body[i+1 : len(body)-1]
		msg.Body = body[:i]
	}
	if strings.HasPrefix(msg.Body, "ack") {
		msg.Ack = true
		msg.SeqNo = msg.Body[3:]
		msg.Body = ""
	} else if strings.HasPrefix(msg.Body, "rej") {
		msg.Rej = true
		msg.SeqNo = msg.Body[3:]
		msg.Body = ""
	}
	return msg, nil, false, true
}

// String renders the packet for log / panel display.
func (p Packet) String() string {
	switch p.Type {
	case TypePosition:
		if p.Position == nil {
			break
		}
		return fmt.Sprintf("POSITION %.4f,%.4f %q",
			p.Position.Latitude, p.Position.Longitude, p.Position.Comment)
	case TypeMessage:
		if p.Message == nil {
			break
		}
		if p.Message.Ack {
			return fmt.Sprintf("ACK to %s seq=%s", p.Message.Addressee, p.Message.SeqNo)
		}
		if p.Message.Rej {
			return fmt.Sprintf("REJ to %s seq=%s", p.Message.Addressee, p.Message.SeqNo)
		}
		return fmt.Sprintf("MSG to %s: %q", p.Message.Addressee, p.Message.Body)
	case TypeStatus:
		if p.Status == nil {
			break
		}
		return fmt.Sprintf("STATUS %q", p.Status.Text)
	case TypeBulletin:
		if p.Bulletin == nil {
			break
		}
		return fmt.Sprintf("BULLETIN %s: %q", p.Bulletin.ID, p.Bulletin.Body)
	case TypeMicE:
		return "MIC-E (compressed; decoder pending)"
	case TypeWeather:
		return "WEATHER " + p.Raw
	case TypeTelemetry:
		return "TELEMETRY " + p.Raw
	case TypeObject:
		return "OBJECT " + p.Raw
	}
	return "UNKNOWN " + p.Raw
}
