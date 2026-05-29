package dsc

import (
	"encoding/hex"
	"strconv"
)

// decodeMMSI decodes 5 consecutive 7-bit DSC symbols into a 9-digit
// MMSI. ITU-R M.493-15 §3.5.3: each symbol encodes two decimal
// digits (00..99). The 10th digit (low digit of the 5th symbol) is
// the format-extension byte; the MMSI proper is the high 9 digits.
//
// Returns (mmsi, true) on a clean decode, (0, false) when any
// symbol is out of range (>99, the spec's "not used" zone).
func decodeMMSI(symbols []byte) (uint64, bool) {
	if len(symbols) != 5 {
		return 0, false
	}
	var mmsi uint64
	for i, s := range symbols {
		if s > 99 {
			return 0, false
		}
		if i < 4 {
			// First 4 symbols → 8 digits.
			mmsi = mmsi*100 + uint64(s)
		} else {
			// 5th symbol's high digit is the 9th MMSI digit;
			// low digit is the format extension (ignored).
			mmsi = mmsi*10 + uint64(s)/10
		}
	}
	return mmsi, true
}

// decodeDigitsPair turns one 7-bit symbol (0..99) into a 2-char
// decimal string. Returns "" for invalid / "not used" symbols.
func decodeDigitsPair(s byte) string {
	if s > 99 {
		return ""
	}
	return leftPad2(int(s))
}

// leftPad2 formats an integer 0..99 as a zero-padded 2-char string.
func leftPad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// decodePosition decodes 5 DSC symbols holding a 10-digit position
// per ITU-R M.493-15 §3.5.3.6: 1 quadrant digit + 4 latitude digits
// (DDMM) + 5 longitude digits (DDDMM). The first symbol's high
// nibble is the quadrant code (0 = NE, 1 = NW, 2 = SE, 3 = SW); the
// low nibble starts the latitude. The spec sentinel for "unknown
// position" is all 9s — we collapse HasPosition to false in that
// case.
func decodePosition(symbols []byte) (Position, bool) {
	if len(symbols) != 5 {
		return Position{}, false
	}
	for _, s := range symbols {
		if s > 99 {
			return Position{}, false
		}
	}
	// Build a 10-digit string Q.DD.MM.DDD.MM.
	digits := make([]byte, 0, 10)
	for _, s := range symbols {
		digits = append(digits, '0'+(s/10), '0'+(s%10))
	}
	// Unknown sentinel: any digit being '9' for all 10 → position
	// not available.
	allNine := true
	for _, d := range digits {
		if d != '9' {
			allNine = false
			break
		}
	}
	if allNine {
		return Position{}, true
	}
	quadrant := digits[0] - '0'
	if quadrant > 3 {
		return Position{}, false
	}
	latDeg, err1 := strconv.Atoi(string(digits[1:3]))
	latMin, err2 := strconv.Atoi(string(digits[3:5]))
	lonDeg, err3 := strconv.Atoi(string(digits[5:8]))
	lonMin, err4 := strconv.Atoi(string(digits[8:10]))
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return Position{}, false
	}
	lat := float64(latDeg) + float64(latMin)/60.0
	lon := float64(lonDeg) + float64(lonMin)/60.0
	// Quadrant: bit 0 set = West, bit 1 set = South.
	if quadrant&2 != 0 {
		lat = -lat
	}
	if quadrant&1 != 0 {
		lon = -lon
	}
	return Position{Latitude: lat, Longitude: lon, HasPosition: true}, true
}

// symbolsToHex hex-encodes the 7-bit symbol stream for raw_payload
// logging. Each symbol packs into one byte.
func symbolsToHex(symbols []byte) string {
	if len(symbols) == 0 {
		return ""
	}
	return hex.EncodeToString(symbols)
}
