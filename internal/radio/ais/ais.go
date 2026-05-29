// Package ais decodes the Automatic Identification System messages
// commercial vessels broadcast on marine VHF channels 87B / 88B
// (161.975 / 162.025 MHz). AIS is the "transponder for ships" — every
// SOLAS-covered vessel (passenger ships, tankers, cargo > 300 GT) is
// required to transmit Class A position reports continuously, and
// recreational vessels increasingly carry Class B units. The data is
// useful for: marine-coast monitoring, traffic deconfliction inside
// shipping lanes, search-and-rescue coordination, port arrival /
// departure tracking, and as a free positional ground-truth for
// receivers that want a known-good wide-area data feed.
//
// Each AIS message is a 168-bit (or longer, multi-slot) frame
// containing a 6-bit message-type tag and the type's payload.
// This package decodes the operator-visible majority — the position
// reports (types 1, 2, 3, 18, 19), static + voyage data (type 5),
// Class B static data (type 24), and base-station reports (type 4)
// — and leaves the less-common types tagged but otherwise raw.
//
// Spec references:
//   - ITU-R M.1371-5 (Recommendation, 2014). Authoritative bit-by-bit
//     layout for every message type.
//   - https://gpsd.gitlab.io/gpsd/AIVDM.html — the de-facto reference
//     parser docs (gpsd's AIVDM/AIVDO decoder), cross-checked against
//     real on-the-air payloads.
//
// AIS bit ordering is MSB-first: each 6-bit ASCII character is
// unpacked into a 6-bit value, the 6-bit values concatenate to form
// a single bit stream, and individual fields read bit positions MSB
// from that stream. See unpackBits below.
package ais

import (
	"fmt"
	"math"
)

// MessageType identifies the AIS payload format. Spec M.1371-5 §3.3
// defines 27 numbered types; this package recognises every type and
// fully decodes the operator-visible majority.
type MessageType uint8

const (
	TypeUnknown                 MessageType = 0
	TypePositionReportClassA    MessageType = 1
	TypePositionReportClassA2   MessageType = 2 // 2 + 3 same layout as 1
	TypePositionReportClassA3   MessageType = 3
	TypeBaseStationReport       MessageType = 4
	TypeStaticAndVoyageData     MessageType = 5
	TypeBinaryAddressed         MessageType = 6
	TypeBinaryAck               MessageType = 7
	TypeBinaryBroadcast         MessageType = 8
	TypeSARAircraftPosition     MessageType = 9
	TypeUTCInquiry              MessageType = 10
	TypeUTCResponse             MessageType = 11
	TypeAddressedSafetyMessage  MessageType = 12
	TypeSafetyAck               MessageType = 13
	TypeBroadcastSafety         MessageType = 14
	TypeInterrogation           MessageType = 15
	TypeAssignmentModeCommand   MessageType = 16
	TypeDGNSSBroadcast          MessageType = 17
	TypePositionReportClassB    MessageType = 18
	TypePositionReportClassBExt MessageType = 19
	TypeDataLinkManagement      MessageType = 20
	TypeAidsToNavigationReport  MessageType = 21
	TypeChannelManagement       MessageType = 22
	TypeGroupAssignmentCommand  MessageType = 23
	TypeStaticDataReport        MessageType = 24 // Class B static
	TypeSingleSlotBinary        MessageType = 25
	TypeMultiSlotBinary         MessageType = 26
	TypeLongRangeBroadcast      MessageType = 27
)

// TypeString returns a stable lowercase string label for a
// MessageType — used as the column value on the vessel_log SQLite
// table and the API DTO's `type` field.
func TypeString(t MessageType) string {
	switch t {
	case TypePositionReportClassA, TypePositionReportClassA2, TypePositionReportClassA3:
		return "position-a"
	case TypePositionReportClassB:
		return "position-b"
	case TypePositionReportClassBExt:
		return "position-b-ext"
	case TypeBaseStationReport:
		return "base-station"
	case TypeStaticAndVoyageData:
		return "static-voyage"
	case TypeStaticDataReport:
		return "static-b"
	case TypeSARAircraftPosition:
		return "sar-aircraft"
	case TypeAidsToNavigationReport:
		return "aid-to-nav"
	case TypeLongRangeBroadcast:
		return "long-range"
	case TypeBinaryAddressed, TypeBinaryBroadcast,
		TypeAddressedSafetyMessage, TypeBroadcastSafety:
		return "safety"
	}
	if t == TypeUnknown {
		return "unknown"
	}
	return fmt.Sprintf("type-%d", t)
}

// Position is the decoded lat/lon for any position-bearing AIS
// message. Latitude / Longitude are degrees; positive = N / E.
// SpeedOverGround is in knots (0..102.2 with 0.1 precision; 102.3
// = "not available"). CourseOverGround is in degrees (0..359.9
// with 0.1 precision; 360 = "not available"). Heading is the true
// heading (0..359, 511 = not available).
type Position struct {
	Latitude         float64
	Longitude        float64
	SpeedOverGround  float64
	CourseOverGround float64
	Heading          int
	Timestamp        int  // UTC seconds, 60 = "not available"
	NavStatus        int  // Class A only — see ITU-R M.1371-5 Table 45
	HasPosition      bool // false when lat/lon out of range or "not available"
}

// VesselStatic is the decoded static + voyage data from a Type 5
// (Class A) or Type 24 (Class B) message — vessel name, IMO,
// callsign, type, dimensions, destination, ETA.
type VesselStatic struct {
	IMO         uint32
	Callsign    string
	Name        string
	ShipType    int
	ToBow       int // dimensions in meters from GPS antenna
	ToStern     int
	ToPort      int
	ToStarboard int
	Destination string
	DraughtM    float64 // metres × 0.1 resolution
	ETAMonth    int
	ETADay      int
	ETAHour     int
	ETAMinute   int
}

// Message is one decoded AIS message.
type Message struct {
	Type   MessageType
	MMSI   uint32
	Repeat int

	Position *Position
	Static   *VesselStatic

	// Raw bits as a hex string — handy for debugging types this
	// package doesn't fully decode and for round-tripping into
	// raw_payload on the SQLite log row.
	RawHex string
}

// Decode parses the bit-packed AIS payload and returns a typed
// Message. Never returns an error — unknown / malformed payloads
// come back as Type=TypeUnknown with the raw bits hex-encoded on
// the RawHex field. AIS receivers see flaky frames all the time;
// we surface what we can and pass through the rest.
//
// bits is the post-CRC-validated 168-bit (or longer multi-slot)
// payload. Convention: bits[0] is the MSB of byte 0 — the
// spec's "bit 1" — and the receiver's HDLC framer already strips
// the leading flag and the trailing CRC.
func Decode(bits []byte) Message {
	m := Message{RawHex: bitsToHex(bits)}
	if len(bits) < 38 {
		return m
	}
	m.Type = MessageType(readBitsUint(bits, 0, 6))
	m.Repeat = int(readBitsUint(bits, 6, 2))
	m.MMSI = uint32(readBitsUint(bits, 8, 30))

	switch m.Type {
	case TypePositionReportClassA, TypePositionReportClassA2, TypePositionReportClassA3:
		if pos, ok := parsePositionA(bits); ok {
			m.Position = &pos
		}
	case TypePositionReportClassB:
		if pos, ok := parsePositionB(bits); ok {
			m.Position = &pos
		}
	case TypePositionReportClassBExt:
		if pos, ok := parsePositionBExt(bits); ok {
			m.Position = &pos
		}
	case TypeBaseStationReport:
		if pos, ok := parseBaseStation(bits); ok {
			m.Position = &pos
		}
	case TypeStaticAndVoyageData:
		if st, ok := parseStaticVoyage(bits); ok {
			m.Static = &st
		}
	case TypeStaticDataReport:
		if st, ok := parseStaticReport(bits); ok {
			m.Static = &st
		}
	}
	return m
}

// String renders the message for log / panel display.
func (m Message) String() string {
	switch m.Type {
	case TypePositionReportClassA, TypePositionReportClassA2, TypePositionReportClassA3:
		if m.Position == nil {
			return fmt.Sprintf("CLASS-A MMSI=%d (pos invalid)", m.MMSI)
		}
		return fmt.Sprintf("CLASS-A MMSI=%d %.4f,%.4f SOG=%.1fkn COG=%.1f° HDG=%d",
			m.MMSI, m.Position.Latitude, m.Position.Longitude,
			m.Position.SpeedOverGround, m.Position.CourseOverGround,
			m.Position.Heading)
	case TypePositionReportClassB:
		if m.Position == nil {
			return fmt.Sprintf("CLASS-B MMSI=%d (pos invalid)", m.MMSI)
		}
		return fmt.Sprintf("CLASS-B MMSI=%d %.4f,%.4f SOG=%.1fkn COG=%.1f°",
			m.MMSI, m.Position.Latitude, m.Position.Longitude,
			m.Position.SpeedOverGround, m.Position.CourseOverGround)
	case TypeStaticAndVoyageData:
		if m.Static == nil {
			return fmt.Sprintf("STATIC MMSI=%d (parse failed)", m.MMSI)
		}
		return fmt.Sprintf("STATIC MMSI=%d name=%q dest=%q type=%d",
			m.MMSI, m.Static.Name, m.Static.Destination, m.Static.ShipType)
	case TypeStaticDataReport:
		if m.Static == nil {
			return fmt.Sprintf("STATIC-B MMSI=%d (parse failed)", m.MMSI)
		}
		return fmt.Sprintf("STATIC-B MMSI=%d name=%q callsign=%q",
			m.MMSI, m.Static.Name, m.Static.Callsign)
	case TypeBaseStationReport:
		if m.Position == nil {
			return fmt.Sprintf("BASE MMSI=%d (pos invalid)", m.MMSI)
		}
		return fmt.Sprintf("BASE MMSI=%d %.4f,%.4f",
			m.MMSI, m.Position.Latitude, m.Position.Longitude)
	}
	return fmt.Sprintf("%s MMSI=%d (raw=%d bytes)", TypeString(m.Type), m.MMSI, (len(bitsToHex(nil))+1)/2)
}

// parsePositionA decodes the Class A position-report layout shared
// by types 1, 2, 3 (M.1371-5 §3.3.1). 168 bits total.
func parsePositionA(bits []byte) (Position, bool) {
	if len(bits) < 168 {
		return Position{}, false
	}
	navStatus := int(readBitsUint(bits, 38, 4))
	sog := float64(readBitsUint(bits, 50, 10)) / 10.0
	lon := readBitsInt(bits, 61, 28)
	lat := readBitsInt(bits, 89, 27)
	cog := float64(readBitsUint(bits, 116, 12)) / 10.0
	heading := int(readBitsUint(bits, 128, 9))
	timestamp := int(readBitsUint(bits, 137, 6))

	return positionFromAIS(lon, lat, sog, cog, heading, timestamp, navStatus), true
}

// parsePositionB decodes the Class B position-report layout for
// type 18 (M.1371-5 §3.3.18). 168 bits total. No nav-status field.
func parsePositionB(bits []byte) (Position, bool) {
	if len(bits) < 168 {
		return Position{}, false
	}
	sog := float64(readBitsUint(bits, 46, 10)) / 10.0
	lon := readBitsInt(bits, 57, 28)
	lat := readBitsInt(bits, 85, 27)
	cog := float64(readBitsUint(bits, 112, 12)) / 10.0
	heading := int(readBitsUint(bits, 124, 9))
	timestamp := int(readBitsUint(bits, 133, 6))
	return positionFromAIS(lon, lat, sog, cog, heading, timestamp, -1), true
}

// parsePositionBExt decodes the extended Class B layout for type
// 19 (M.1371-5 §3.3.19). 312 bits total. Extends type 18 with
// vessel name + ship type + dimensions (handled via static
// section).
func parsePositionBExt(bits []byte) (Position, bool) {
	if len(bits) < 312 {
		// Type 19 frames sometimes arrive truncated; we accept the
		// shorter form and decode whatever's there.
		if len(bits) < 168 {
			return Position{}, false
		}
	}
	return parsePositionB(bits)
}

// parseBaseStation decodes the base-station report layout for type
// 4 (M.1371-5 §3.3.4). 168 bits. Carries the station's lat/lon
// and a UTC timestamp.
func parseBaseStation(bits []byte) (Position, bool) {
	if len(bits) < 168 {
		return Position{}, false
	}
	lon := readBitsInt(bits, 79, 28)
	lat := readBitsInt(bits, 107, 27)
	return positionFromAIS(lon, lat, 0, 0, 511, 60, -1), true
}

// positionFromAIS turns the raw signed-integer lat/lon fields into
// the Position struct, applying the spec's special values:
// lon == 0x6791AC0 (=181°) and lat == 0x3412140 (=91°) signal
// "not available"; we leave HasPosition=false in that case.
func positionFromAIS(lonRaw, latRaw int, sog, cog float64, heading, timestamp, navStatus int) Position {
	lon := float64(lonRaw) / 600000.0
	lat := float64(latRaw) / 600000.0
	p := Position{
		Latitude:         lat,
		Longitude:        lon,
		SpeedOverGround:  sog,
		CourseOverGround: cog,
		Heading:          heading,
		Timestamp:        timestamp,
		NavStatus:        navStatus,
	}
	// Spec "not available" sentinels: lat 91.0, lon 181.0.
	if math.Abs(lat) <= 90.0 && math.Abs(lon) <= 180.0 &&
		!(lat == 91.0 || lon == 181.0) {
		p.HasPosition = true
	}
	return p
}

// parseStaticVoyage decodes the Type 5 static + voyage data
// (M.1371-5 §3.3.5). 424 bits total.
func parseStaticVoyage(bits []byte) (VesselStatic, bool) {
	if len(bits) < 424 {
		// Real-world frames frequently arrive a few bits short;
		// require enough to cover up to the destination field
		// (~ 380 bits) and accept best-effort decode.
		if len(bits) < 380 {
			return VesselStatic{}, false
		}
	}
	out := VesselStatic{
		IMO:         uint32(readBitsUint(bits, 40, 30)),
		Callsign:    readAISString(bits, 70, 7),
		Name:        readAISString(bits, 112, 20),
		ShipType:    int(readBitsUint(bits, 232, 8)),
		ToBow:       int(readBitsUint(bits, 240, 9)),
		ToStern:     int(readBitsUint(bits, 249, 9)),
		ToPort:      int(readBitsUint(bits, 258, 6)),
		ToStarboard: int(readBitsUint(bits, 264, 6)),
	}
	if len(bits) >= 380 {
		out.ETAMonth = int(readBitsUint(bits, 274, 4))
		out.ETADay = int(readBitsUint(bits, 278, 5))
		out.ETAHour = int(readBitsUint(bits, 283, 5))
		out.ETAMinute = int(readBitsUint(bits, 288, 6))
		out.DraughtM = float64(readBitsUint(bits, 294, 8)) / 10.0
		out.Destination = readAISString(bits, 302, 20)
	}
	return out, true
}

// parseStaticReport decodes the Type 24 Class B static-data report
// (M.1371-5 §3.3.24). The single 168-bit (or 162-bit) Part A
// carries vessel name; Part B carries call-sign + ship type +
// dimensions. We decode whichever Part the partno bits indicate.
func parseStaticReport(bits []byte) (VesselStatic, bool) {
	if len(bits) < 40 {
		return VesselStatic{}, false
	}
	partNo := int(readBitsUint(bits, 38, 2))
	out := VesselStatic{}
	switch partNo {
	case 0:
		if len(bits) >= 160 {
			out.Name = readAISString(bits, 40, 20)
		}
	case 1:
		if len(bits) >= 168 {
			out.ShipType = int(readBitsUint(bits, 40, 8))
			out.Callsign = readAISString(bits, 90, 7)
			out.ToBow = int(readBitsUint(bits, 132, 9))
			out.ToStern = int(readBitsUint(bits, 141, 9))
			out.ToPort = int(readBitsUint(bits, 150, 6))
			out.ToStarboard = int(readBitsUint(bits, 156, 6))
		}
	default:
		return VesselStatic{}, false
	}
	return out, true
}
