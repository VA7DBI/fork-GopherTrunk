package phase2

import (
	"errors"
	"fmt"

	"github.com/MattCheramie/GopherTrunk/internal/radio/framing"
)

// ISCH is the Inter-Slot Signalling Channel field that prefixes every
// P25 Phase 2 sub-frame and names what the sub-frame carries. It is the
// field a receiver reads to tell a voice sub-frame from a MAC sub-frame
// without inspecting the payload — see SuperframeDecoder, which decodes
// one ISCH per sub-frame and stamps the result onto Subframe.SlotType.
//
// Layout note: TIA-102.BBAB §6.3 defines the ISCH and its FEC. The repo
// has no figure for the exact bit packing or code, so this file is the
// project's working model — a 12-bit field protected by the extended
// Golay(24, 12, 8) code already in internal/radio/framing. All ISCH
// knowledge is deliberately confined here so a spec correction is a
// local change: the rest of the pipeline only ever sees a decoded
// SlotType. The 12 data bits are:
//
//	bits 0-3  : SlotType
//	bits 4-7  : Counter (0..11 sub-frame position within the superframe)
//	bits 8-11 : reserved (transmitted as 0)
type ISCH struct {
	SlotType SlotType
	Counter  uint8 // 0..11 sub-frame counter
}

// ISCH on-wire geometry.
const (
	// ISCHOffset is the dibit offset of the ISCH field within every
	// sub-frame. It sits immediately after the SyncDibits-wide region
	// so it never collides with the outbound sync word that occupies
	// the head of sub-frame SyncSubframeIndex.
	ISCHOffset = SyncDibits
	// ISCHDibits is the on-wire width of the ISCH: a 12-bit field
	// protected by extended Golay(24, 12, 8) = 24 bits = 12 dibits.
	ISCHDibits = 12
)

// ErrISCHLength is returned when DecodeISCH receives a dibit slice that
// is not exactly ISCHDibits long.
var ErrISCHLength = errors.New("p25/phase2: ISCH input must be ISCHDibits dibits")

// ErrISCHUncorrectable is returned when the Golay decoder cannot
// recover the ISCH codeword within its 3-bit correction radius.
var ErrISCHUncorrectable = errors.New("p25/phase2: ISCH Golay uncorrectable")

// DecodeISCH recovers the ISCH from its ISCHDibits-wide on-wire region.
// It returns the decoded ISCH, the number of bit errors the Golay
// decoder corrected (0 on a clean codeword), and a non-nil error if the
// input is malformed or the codeword is uncorrectable.
func DecodeISCH(dibits []uint8) (ISCH, int, error) {
	if len(dibits) != ISCHDibits {
		return ISCH{}, -1, fmt.Errorf("%w: got %d", ErrISCHLength, len(dibits))
	}
	var cw uint32
	for _, d := range dibits {
		cw = cw<<2 | uint32(d&3)
	}
	data, errs := framing.GolayDecode24_12(cw)
	if errs < 0 {
		return ISCH{}, -1, ErrISCHUncorrectable
	}
	return ISCH{
		SlotType: SlotType(data & 0x0F),
		Counter:  uint8((data >> 4) & 0x0F),
	}, errs, nil
}

// EncodeISCH is the inverse of DecodeISCH: it Golay-encodes an ISCH into
// its ISCHDibits-wide on-wire dibit form. Used to build synthesized
// superframe fixtures.
func EncodeISCH(i ISCH) []uint8 {
	data := uint16(i.SlotType&0x0F) | uint16(i.Counter&0x0F)<<4
	cw := framing.GolayEncode24_12(data)
	out := make([]uint8, ISCHDibits)
	for k := 0; k < ISCHDibits; k++ {
		shift := uint(2 * (ISCHDibits - 1 - k))
		out[k] = uint8((cw >> shift) & 3)
	}
	return out
}
