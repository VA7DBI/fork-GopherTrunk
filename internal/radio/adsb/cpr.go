package adsb

import "math"

// Compact Position Reporting (CPR) — ADS-B's lat/lon encoding
// trick. A full lat/lon takes 41 bits; ADS-B only spends 34 bits
// per position message, so the encoder splits the world into a
// grid and broadcasts a "local" position relative to that grid.
// Two consecutive messages with different "format" flags (even
// = 0 vs odd = 1) sample the grid at slightly different
// resolutions; the receiver pairs them and recovers the global
// position uniquely.
//
// Spec: DO-260B §2.2.3.2.3.7 — "Compact Position Reporting".
//
// This file implements the globally-unambiguous decode (even +
// odd pair). The locally-referenced decode (single message
// against a known reference location) is the obvious follow-up
// when the daemon starts caching the receiver's location.

const (
	// cprNZ is the number of latitude zones in the northern
	// hemisphere; the spec sets NZ = 15 for ADS-B at airborne
	// altitudes.
	cprNZ = 15
)

// CPRDecodeGlobal recovers the global (lat, lon) in degrees from
// an even-format and an odd-format CPR-encoded position pair.
// Returns (lat, lon, true) on success, (0, 0, false) when the
// pair straddles a longitude-zone boundary that requires a
// reference position to resolve.
//
// The even position is treated as the "newer" sample: when
// mostRecentIsEven is true the function returns the lat/lon
// derived from the even half (matching the timestamp of the
// most recently received message), otherwise from the odd half.
//
// Inputs are the 17-bit raw CPR-encoded values from the
// extended-squitter ME field (Position.CPRLatEven / etc.).
func CPRDecodeGlobal(latEven, lonEven, latOdd, lonOdd int, mostRecentIsEven bool) (lat, lon float64, ok bool) {
	// Latitude
	const dlatEven = 360.0 / 60.0
	const dlatOdd = 360.0 / 59.0

	lateF := float64(latEven) / 131072.0
	latoF := float64(latOdd) / 131072.0
	loneF := float64(lonEven) / 131072.0
	lonoF := float64(lonOdd) / 131072.0

	j := math.Floor(59*lateF - 60*latoF + 0.5)

	latE := dlatEven * (cprModFloat(j, 60) + lateF)
	latO := dlatOdd * (cprModFloat(j, 59) + latoF)

	// Latitudes > 270 wrap to the southern hemisphere.
	if latE >= 270 {
		latE -= 360
	}
	if latO >= 270 {
		latO -= 360
	}

	// Sanity: even and odd halves must agree on the same NL
	// (number of longitude zones at this latitude).
	if cprNL(latE) != cprNL(latO) {
		return 0, 0, false
	}

	if mostRecentIsEven {
		lat = latE
	} else {
		lat = latO
	}

	// Longitude
	nl := float64(cprNL(lat))
	var dlon float64
	if mostRecentIsEven {
		dlon = 360.0 / math.Max(nl, 1)
	} else {
		dlon = 360.0 / math.Max(nl-1, 1)
	}
	m := math.Floor(loneF*(nl-1) - lonoF*nl + 0.5)
	var nIdx float64
	if mostRecentIsEven {
		nIdx = math.Max(nl, 1)
		lon = dlon * (cprModFloat(m, nIdx) + loneF)
	} else {
		nIdx = math.Max(nl-1, 1)
		lon = dlon * (cprModFloat(m, nIdx) + lonoF)
	}
	// Wrap [180, 360) to [-180, 0).
	if lon >= 180 {
		lon -= 360
	}
	return lat, lon, true
}

// cprNL returns the "number of longitude zones" at a given
// latitude. Per DO-260B Appendix A the table is symmetric about
// the equator; the closed-form below comes from the same source
// and matches the reference dump1090 implementation.
func cprNL(lat float64) int {
	lat = math.Abs(lat)
	if lat >= 87.0 {
		return 1
	}
	if lat == 0 {
		return 59
	}
	a := 1.0 - math.Cos(math.Pi/(2.0*cprNZ))
	b := math.Cos(math.Pi / 180.0 * lat)
	b *= b
	nl := math.Floor(2 * math.Pi / math.Acos(1.0-a/b))
	return int(nl)
}

// cprModFloat is the modulo defined as a - b * floor(a/b) so
// the result is always in [0, b). Go's math.Mod uses truncated
// division, which gives the wrong sign for negative operands.
func cprModFloat(a, b float64) float64 {
	r := a - b*math.Floor(a/b)
	if r < 0 {
		r += b
	}
	return r
}
