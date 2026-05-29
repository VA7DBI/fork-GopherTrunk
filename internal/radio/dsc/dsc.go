// Package dsc decodes Digital Selective Calling messages
// transmitted on marine VHF channel 70 (156.525 MHz) and
// medium / high-frequency DSC channels (2.187.5 kHz, 8.414.5 kHz,
// etc.). DSC is the SOLAS-mandated digital calling protocol used
// for distress / urgency / safety alerts, ship-to-ship paging,
// and the routine call-up that precedes a voice working-frequency
// hand-off — every Class A AIS-equipped vessel also carries a DSC
// controller, and shore stations (CG, RCC, coast radio) listen
// continuously.
//
// Why DSC: distress alerts are the highest-priority operational
// data on marine VHF, and DSC is what triggers them. A coast-
// guard MMSI on the channel-70 DSC stream landing in the bus
// gives operators near-instant visibility into search-and-rescue
// activity; the routine calls give working-frequency
// announcements (which channel two stations are about to switch
// to for voice traffic).
//
// This package owns the protocol parsing: 7-bit data symbols
// (already BCH-corrected by the bit-stream layer above) come in,
// a typed Message with the format / category / source MMSI /
// (where present) position + time + working channel comes out.
// Less-common formats stay tagged with TypeUnknown and the raw
// symbols preserved.
//
// Spec references:
//   - ITU-R M.493-15 (Recommendation, 2019) — DSC message format,
//     symbol table, BCH(10,7) FEC. Authoritative.
//   - ITU-R M.541 — operational use, station identification.
package dsc

import (
	"fmt"
)

// Format identifies the DSC message format specifier — the first
// byte after the phasing sequence. ITU-R M.493-15 Table 3.5.
type Format uint8

const (
	FormatUnknown        Format = 0
	FormatDistress       Format = 112 // "Distress alert"
	FormatAllShips       Format = 116 // "All ships"
	FormatGroup          Format = 114 // "Group call"
	FormatIndividual     Format = 120 // "Individual call"
	FormatGeographic     Format = 102 // "Geographic-area call"
	FormatDistressRelay  Format = 116 // Same byte as AllShips — disambiguated by category
	FormatAutoIndividual Format = 123 // "Automatic individual"
)

// FormatString returns a stable lowercase label for a Format —
// used as the column value on the dsc_log SQLite table and the
// API DTO's `format` field.
func FormatString(f Format) string {
	switch f {
	case FormatDistress:
		return "distress"
	case FormatAllShips:
		return "all-ships"
	case FormatGroup:
		return "group"
	case FormatIndividual:
		return "individual"
	case FormatGeographic:
		return "geographic"
	case FormatAutoIndividual:
		return "auto-individual"
	}
	return "unknown"
}

// Category identifies the DSC message priority class. ITU-R
// M.493-15 §3.5.5.
type Category uint8

const (
	CategoryUnknown  Category = 0
	CategoryRoutine  Category = 100
	CategorySafety   Category = 108
	CategoryUrgency  Category = 110
	CategoryDistress Category = 112
)

// CategoryString returns a stable lowercase label for a Category.
func CategoryString(c Category) string {
	switch c {
	case CategoryDistress:
		return "distress"
	case CategoryUrgency:
		return "urgency"
	case CategorySafety:
		return "safety"
	case CategoryRoutine:
		return "routine"
	}
	return "unknown"
}

// NatureOfDistress identifies the cause of a distress alert per
// ITU-R M.493-15 Table 3.7. Only present when Format == Distress.
type NatureOfDistress uint8

const (
	DistressFire         NatureOfDistress = 100
	DistressFlooding     NatureOfDistress = 101
	DistressCollision    NatureOfDistress = 102
	DistressGrounding    NatureOfDistress = 103
	DistressListing      NatureOfDistress = 104
	DistressSinking      NatureOfDistress = 105
	DistressDisabled     NatureOfDistress = 106
	DistressUndesignated NatureOfDistress = 107
	DistressAbandoning   NatureOfDistress = 108
	DistressPiracy       NatureOfDistress = 109
	DistressMOB          NatureOfDistress = 110
	DistressEPIRB        NatureOfDistress = 112
)

// NatureString returns a stable lowercase label for a distress
// nature. Empty string for unrecognised values.
func NatureString(n NatureOfDistress) string {
	switch n {
	case DistressFire:
		return "fire / explosion"
	case DistressFlooding:
		return "flooding"
	case DistressCollision:
		return "collision"
	case DistressGrounding:
		return "grounding"
	case DistressListing:
		return "listing"
	case DistressSinking:
		return "sinking"
	case DistressDisabled:
		return "disabled and adrift"
	case DistressUndesignated:
		return "undesignated distress"
	case DistressAbandoning:
		return "abandoning ship"
	case DistressPiracy:
		return "piracy / armed attack"
	case DistressMOB:
		return "man overboard"
	case DistressEPIRB:
		return "EPIRB emission"
	}
	return ""
}

// Position is the decoded lat/lon for a distress alert with a
// position field. Latitude / Longitude are degrees; positive
// = N / E. HasPosition is false when the position field is the
// spec's "unknown" sentinel (all 9s).
type Position struct {
	Latitude    float64
	Longitude   float64
	HasPosition bool
}

// Message is one decoded DSC sequence.
type Message struct {
	Format         Format
	Category       Category
	SelfMMSI       uint64           // sender's 9-digit MMSI
	TargetMMSI     uint64           // recipient's MMSI (0 for all-ships)
	Nature         NatureOfDistress // distress only; 0 otherwise
	Position       *Position
	TimeUTC        string // HH:MM, distress only; empty otherwise
	WorkingChannel int    // working frequency channel for ack; 0 if absent
	EOS            uint8  // end-of-sequence symbol
	RawSymbols     string // hex-encoded 7-bit symbol stream for debugging
	EFCChecksumOK  bool   // ECC byte matched
}

// Decode parses a sequence of post-BCH-corrected 7-bit data
// symbols (the body of a DSC sequence, between the phasing
// preamble and the EOS pattern) and returns a typed Message.
//
// The bit-stream layer above this package handles sync detection,
// BCH(10,7) error correction, and the DX / RX redundancy merge.
// By the time symbols reach Decode each entry is one 7-bit value
// 0..127.
//
// Never returns an error — short / malformed sequences come back
// with Format = FormatUnknown and the raw symbols preserved on
// RawSymbols. Real-world DSC receivers see noisy half-frames
// constantly; surface what we can and pass through the rest.
func Decode(symbols []byte) Message {
	m := Message{RawSymbols: symbolsToHex(symbols)}
	if len(symbols) < 2 {
		return m
	}
	m.Format = Format(symbols[0])
	// All formats but Distress include the address (5 symbols)
	// immediately after the format byte. For Distress the address
	// is the sender's self-ID instead (see §3.5.3.5 special case).
	off := 1

	// Address: 5 symbols, decoded as pairs of decimal digits ×5
	// (10 digits total — but only 9 are MMSI; the 10th is a
	// "extra/format-specific" digit). For ship-MMSI calls the
	// 10th digit is 0; for area calls it can carry a quadrant.
	if m.Format == FormatDistress {
		// Distress: self-MMSI comes immediately, no separate
		// target address.
		if mmsi, ok := decodeMMSI(symbols[off : off+5]); ok {
			m.SelfMMSI = mmsi
		}
		off += 5
		// Nature of distress: 1 symbol.
		if off < len(symbols) {
			m.Nature = NatureOfDistress(symbols[off])
			off++
		}
		// Position: 5 symbols (10 digits, with a quadrant + 4 lat
		// + 5 lon digits).
		if off+5 <= len(symbols) {
			if pos, ok := decodePosition(symbols[off : off+5]); ok {
				m.Position = &pos
			}
			off += 5
		}
		// Time UTC: 2 symbols = 4 digits = HHMM.
		if off+2 <= len(symbols) {
			h := decodeDigitsPair(symbols[off])
			min := decodeDigitsPair(symbols[off+1])
			if h != "" && min != "" {
				m.TimeUTC = h + ":" + min
			}
			off += 2
		}
		// EOS at end.
		if len(symbols) > 0 {
			m.EOS = symbols[len(symbols)-1]
		}
		m.Category = CategoryDistress
		return m
	}

	// Non-distress: address (5 symbols) → category → self-MMSI.
	if off+5 <= len(symbols) {
		if mmsi, ok := decodeMMSI(symbols[off : off+5]); ok {
			m.TargetMMSI = mmsi
		}
		off += 5
	}
	if off < len(symbols) {
		m.Category = Category(symbols[off])
		off++
	}
	if off+5 <= len(symbols) {
		if mmsi, ok := decodeMMSI(symbols[off : off+5]); ok {
			m.SelfMMSI = mmsi
		}
		off += 5
	}
	// Remaining symbols (type of call, frequency, etc.) get
	// preserved on Raw — full per-format parsing is a follow-up.
	if len(symbols) > 0 {
		m.EOS = symbols[len(symbols)-1]
	}
	return m
}

// String renders the message for log / panel display.
func (m Message) String() string {
	switch m.Format {
	case FormatDistress:
		base := fmt.Sprintf("DISTRESS MMSI=%09d", m.SelfMMSI)
		if nat := NatureString(m.Nature); nat != "" {
			base += fmt.Sprintf(" nature=%q", nat)
		}
		if m.Position != nil && m.Position.HasPosition {
			base += fmt.Sprintf(" pos=%.4f,%.4f",
				m.Position.Latitude, m.Position.Longitude)
		}
		if m.TimeUTC != "" {
			base += fmt.Sprintf(" t=%sZ", m.TimeUTC)
		}
		return base
	case FormatAllShips:
		return fmt.Sprintf("ALL-SHIPS %s MMSI=%09d",
			CategoryString(m.Category), m.SelfMMSI)
	case FormatIndividual:
		return fmt.Sprintf("INDIVIDUAL %s MMSI=%09d → %09d",
			CategoryString(m.Category), m.SelfMMSI, m.TargetMMSI)
	case FormatGeographic:
		return fmt.Sprintf("GEOGRAPHIC %s MMSI=%09d",
			CategoryString(m.Category), m.SelfMMSI)
	case FormatGroup:
		return fmt.Sprintf("GROUP %s MMSI=%09d → %09d",
			CategoryString(m.Category), m.SelfMMSI, m.TargetMMSI)
	}
	return fmt.Sprintf("%s MMSI=%09d",
		FormatString(m.Format), m.SelfMMSI)
}
