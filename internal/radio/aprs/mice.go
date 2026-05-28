package aprs

import (
	"strings"
)

// MicE is the decoded payload of a Mic-E (Mic-Encoded) APRS packet
// — the compressed lat/lon/speed/course format common on mobile
// trackers (Kenwood TH-D74, Yaesu FT-3D, vehicle trackers). The
// "Mic" originates with the Mic-Encoder, a 1990s plug-in for the
// Kenwood TM-D700; it packs an APRS position into the 7-byte AX.25
// destination address + 9 bytes of info field, a third the size
// of an uncompressed beacon. That saves channel time, which is
// why almost every mobile tracker uses it.
//
// Mic-E is a two-half codec:
//
//   - The AX.25 destination address (frame.Dst.Callsign, 6 ASCII
//     chars) encodes six latitude digits (DDMMhh) plus three
//     "message" bits, the N/S hemisphere, the longitude offset
//     (+0° or +100°), and the E/W hemisphere.
//   - The info field (starting at the DTI byte 0x1C / 0x1D / "'" /
//     "`") encodes longitude (3 bytes), speed + course (3 bytes),
//     symbol code + symbol table (2 bytes), and an optional
//     altitude / comment trailer.
//
// Decoding the destination needs the AX.25 envelope, so the entry
// point is DecodeWithDst(info, dst). The DTI-only Decode(info)
// preserves the pre-Mic-E behaviour (Type=TypeMicE with the raw
// bytes preserved on Raw).
//
// Spec reference: APRS Protocol Reference 1.0.1, Chapter 10.
type MicE struct {
	Latitude  float64
	Longitude float64

	// Speed in knots (0..799, biased / wrapped per spec).
	Speed int
	// Course in degrees (0..359, biased / wrapped per spec).
	Course int

	// SymbolTable + SymbolCode are the APRS symbol-set identifiers
	// (e.g. "/" + ">" = car, "\\" + ">" = ambulance). See APRS
	// Symbols spec for the full table.
	SymbolTable byte
	SymbolCode  byte

	// MessageCode is the 3-bit message field decoded as a label
	// ("M0 Off Duty", "M3 Returning", "Emergency", etc.). Custom
	// codes from the A-K char range surface as "Custom-N".
	MessageCode string

	// Standard is true when the message-bit chars are all from the
	// P-Z range (standard set), false when from A-K (custom set).
	// Mixing the two ranges in the same packet is invalid per
	// spec; we report whichever range the third byte falls in.
	Standard bool

	// Altitude in meters above sea level. Optional — present when
	// the comment trailer contains the "XXX}" base-91 altitude
	// marker. HasAltitude distinguishes "0 m" from "absent".
	Altitude    int
	HasAltitude bool

	// Comment is the free-form text after the symbol bytes (and
	// after the optional altitude marker, if any).
	Comment string
}

// DecodeWithDst decodes one APRS info field and returns a typed
// Packet. dst is the AX.25 destination callsign (frame.Dst.Callsign
// — 6 ASCII chars, no SSID), required for Mic-E because half of
// the Mic-E payload lives there. Pass nil / "" if the destination
// is unavailable; Mic-E packets will then come back as
// Type=TypeMicE with the raw bytes preserved on Raw (i.e. the same
// behaviour as Decode).
//
// Never returns an error — unknown / malformed payloads come back
// as Type=TypeUnknown with the raw bytes preserved on the Raw
// field. APRS is messy; we surface what we can and pass through
// the rest.
func DecodeWithDst(info, dst []byte) Packet {
	p := Decode(info)
	if p.Type != TypeMicE || len(dst) < 6 {
		return p
	}
	if mice, ok := parseMicE(info, dst); ok {
		p.MicE = &mice
		// Mic-E carries a position; surface it through the
		// standard Position field too so existing callers
		// (storage.APRSPacket.Latitude / .Longitude, the /aprs
		// web panel) pick it up without special-casing the
		// Mic-E shape.
		p.Position = &Position{
			Latitude:    mice.Latitude,
			Longitude:   mice.Longitude,
			SymbolTable: mice.SymbolTable,
			SymbolCode:  mice.SymbolCode,
			Comment:     mice.Comment,
		}
	}
	return p
}

// parseMicE decodes a Mic-E (info, dst) pair. Returns false if the
// payload is too short or the destination address is malformed.
// Spec layout (APRS 1.0.1 §10):
//
//	dst[0..5]:   6 lat digits (DDMMhh) + 3 msg bits + N/S +
//	             lon-offset + E/W
//	info[0]:     DTI (0x1C / 0x1D / "`" / "'")
//	info[1]:     longitude degrees + 28
//	info[2]:     longitude minutes  + 28
//	info[3]:     longitude hundredths-of-minute + 28
//	info[4..6]:  speed / course (3 bytes, biased + interleaved)
//	info[7]:     symbol code
//	info[8]:     symbol table
//	info[9..]:   optional altitude marker + comment
func parseMicE(info, dst []byte) (MicE, bool) {
	if len(info) < 9 || len(dst) < 6 {
		return MicE{}, false
	}
	lat, msgBits, msgFromCustomRange, north, lonOff100, west, ok := parseMicEDest(dst[:6])
	if !ok {
		return MicE{}, false
	}

	// Longitude degrees: byte - 28 (then offset if +100°), with
	// the printable-ASCII wraparound corrections from APRS101
	// §10.5 (raw 180-189 → 100-109, raw 190-199 → 0-9).
	lonDeg := int(info[1]) - 28
	if lonOff100 {
		lonDeg += 100
	}
	switch {
	case lonDeg >= 180 && lonDeg <= 189:
		lonDeg -= 80
	case lonDeg >= 190 && lonDeg <= 199:
		lonDeg -= 190
	}

	// Longitude minutes: byte - 28, biased into 60-69 by some
	// implementations to keep the byte printable; collapse back
	// to 0-9.
	lonMin := int(info[2]) - 28
	if lonMin >= 60 {
		lonMin -= 60
	}
	lonHun := int(info[3]) - 28

	lon := float64(lonDeg) + (float64(lonMin)+float64(lonHun)/100.0)/60.0
	if west {
		lon = -lon
	}
	if !north {
		lat = -lat
	}

	// Speed + course interleaved encoding (see §10.4):
	//
	//	speed  = (sp_byte - 28) * 10 + (dc_byte - 28) / 10
	//	course = ((dc_byte - 28) % 10) * 100 + (se_byte - 28)
	//
	// The 800/400 wrap corrections handle the bias offsets the
	// printable-ASCII encoding introduces.
	sp := int(info[4]) - 28
	dc := int(info[5]) - 28
	se := int(info[6]) - 28
	speed := sp*10 + dc/10
	course := (dc%10)*100 + se
	if speed >= 800 {
		speed -= 800
	}
	if course >= 400 {
		course -= 400
	}

	out := MicE{
		Latitude:    lat,
		Longitude:   lon,
		Speed:       speed,
		Course:      course,
		SymbolCode:  info[7],
		SymbolTable: info[8],
		MessageCode: micEMessageLabel(msgBits, msgFromCustomRange),
		Standard:    !msgFromCustomRange,
	}

	// Optional altitude + comment after the symbol bytes. APRS101
	// §10.6: altitude lives at the start of the comment as 3
	// base-91 chars followed by "}", value = base91 - 10000 meters.
	trailer := info[9:]
	if alt, rest, hasAlt := extractMicEAltitude(trailer); hasAlt {
		out.HasAltitude = true
		out.Altitude = alt
		out.Comment = string(rest)
	} else {
		out.Comment = string(trailer)
	}

	return out, true
}

// parseMicEDest decodes the 6-character AX.25 destination callsign
// into its latitude / message-bit / hemisphere / lon-offset pieces.
// Returns false if any character is out of range — Mic-E permits
// only 0-9, A-Z (plus L and K as special "space" carriers).
func parseMicEDest(dst []byte) (
	latitude float64,
	msgBits uint8,
	msgFromCustomRange bool,
	north bool,
	lonOff100 bool,
	west bool,
	ok bool,
) {
	latDigits := [6]byte{}
	hasCustom := false
	hasStandard := false
	for i := 0; i < 6; i++ {
		c := dst[i]
		digit, bit, std, custom, isLat := micEChar(c)
		if !isLat {
			return 0, 0, false, false, false, false, false
		}
		latDigits[i] = digit
		switch i {
		case 0, 1, 2:
			// Top 3 chars carry the message-bit triplet.
			if bit {
				msgBits |= 1 << uint(2-i)
			}
			if custom {
				hasCustom = true
			}
			if std {
				hasStandard = true
			}
		case 3:
			// N/S hemisphere — std/custom range → North, else
			// South. The "bit" field doubles as the hemisphere
			// indicator at this position.
			if bit {
				north = true
			}
		case 4:
			// Longitude offset — std/custom range → +100°, else 0°.
			if bit {
				lonOff100 = true
			}
		case 5:
			// W/E hemisphere — std/custom range → West, else East.
			if bit {
				west = true
			}
		}
	}

	// Per spec, a Mic-E with mixed custom + standard chars in the
	// message-bit positions is malformed. Most receivers fall back
	// to the standard label set; do the same and prefer "standard"
	// when the third char is unambiguous.
	msgFromCustomRange = hasCustom && !hasStandard

	// Assemble latitude DDMM.hh from the 6 digits. Spaces (ambiguity
	// digits) collapse to 0 — same convention as the uncompressed
	// position parser.
	for i := range latDigits {
		if latDigits[i] == ' ' {
			latDigits[i] = '0'
		}
	}
	deg := int(latDigits[0]-'0')*10 + int(latDigits[1]-'0')
	min := int(latDigits[2]-'0')*10 + int(latDigits[3]-'0')
	hun := int(latDigits[4]-'0')*10 + int(latDigits[5]-'0')
	latitude = float64(deg) + (float64(min)+float64(hun)/100.0)/60.0
	ok = true
	return
}

// micEChar decodes one Mic-E destination character per APRS101
// §10.5 Table. Returns the latitude digit ('0'-'9' or ' '), the
// indicator bit, whether the char is from the standard (P-Z)
// range, whether it's from the custom (A-K) range, and whether
// the char is a valid Mic-E destination char at all.
//
// The same "bit" doubles as the message bit (positions 0-2), the
// N/S indicator (position 3), the lon-offset indicator (position
// 4), and the W/E indicator (position 5) — the caller decides
// which meaning applies based on position.
func micEChar(c byte) (digit byte, bit, standard, custom, ok bool) {
	switch {
	case c >= '0' && c <= '9':
		return c, false, false, false, true
	case c >= 'A' && c <= 'J':
		return c - 'A' + '0', true, false, true, true
	case c == 'K':
		return ' ', true, false, true, true
	case c == 'L':
		return ' ', false, false, false, true
	case c >= 'P' && c <= 'Y':
		return c - 'P' + '0', true, true, false, true
	case c == 'Z':
		return ' ', true, true, false, true
	}
	return 0, false, false, false, false
}

// micEMessageLabel maps the 3-bit message code to a human-readable
// label per APRS101 §10.3 / 10.4. The std vs custom distinction
// changes only the friendly name; emergency (all zeros) is shared
// across both ranges.
func micEMessageLabel(bits uint8, custom bool) string {
	if bits == 0b000 {
		return "Emergency"
	}
	if custom {
		switch bits {
		case 0b111:
			return "M0 Custom-0"
		case 0b110:
			return "M1 Custom-1"
		case 0b101:
			return "M2 Custom-2"
		case 0b100:
			return "M3 Custom-3"
		case 0b011:
			return "M4 Custom-4"
		case 0b010:
			return "M5 Custom-5"
		case 0b001:
			return "M6 Custom-6"
		}
	}
	switch bits {
	case 0b111:
		return "M0 Off Duty"
	case 0b110:
		return "M1 En Route"
	case 0b101:
		return "M2 In Service"
	case 0b100:
		return "M3 Returning"
	case 0b011:
		return "M4 Committed"
	case 0b010:
		return "M5 Special"
	case 0b001:
		return "M6 Priority"
	}
	return "Unknown"
}

// extractMicEAltitude scans the comment trailer for the optional
// "XXX}" base-91 altitude marker. Per APRS101 §10.6, the three
// base-91 chars are at the start of the trailer when the marker
// is present; some implementations embed it after a leading
// space-separated comment, so we scan up to the first "}".
// Returns (altitude_m, remaining_comment_bytes, true) when found,
// or (0, original, false) otherwise.
func extractMicEAltitude(trailer []byte) (int, []byte, bool) {
	// Look for the literal "}" with at least 3 chars preceding it.
	closer := strings.IndexByte(string(trailer), '}')
	if closer < 3 {
		return 0, trailer, false
	}
	// Walk back 3 chars; require each to be a valid base-91 char
	// (printable ASCII 33-123).
	chars := trailer[closer-3 : closer]
	for _, b := range chars {
		if b < '!' || b > '{' {
			return 0, trailer, false
		}
	}
	value := int(chars[0]-'!')*91*91 + int(chars[1]-'!')*91 + int(chars[2]-'!')
	altitude := value - 10000

	// Stitch the rest of the comment together: anything before
	// the 3 marker chars, plus anything after the "}".
	rest := make([]byte, 0, len(trailer)-4)
	rest = append(rest, trailer[:closer-3]...)
	rest = append(rest, trailer[closer+1:]...)
	return altitude, rest, true
}
