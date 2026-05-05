// Package phase1 decodes the P25 Phase 1 (C4FM, FDMA) frame structure.
// Inputs are dibits (0..3) recovered by the upstream C4FM demodulator and
// symbol-time recovery; outputs are parsed NID and TSBK records.
package phase1

import "github.com/MattCheramie/GopherTrunk/internal/radio/framing"

// FrameSyncWord is the 48-bit P25 Phase 1 frame sync word, expressed as
// 24 dibits (TIA-102.BAAA §6.1.1). Hex: 0x5575F5FF77FF, MSB-first dibits.
var FrameSyncWord = [24]uint8{
	1, 1, 1, 1, 1, 3, 1, 1, 3, 3, 1, 1,
	3, 3, 3, 3, 1, 3, 1, 3, 3, 3, 3, 3,
}

// C4FM symbol-to-dibit mapping per TIA-102.BAAA: +3→01, +1→00, -1→10, -3→11.
// Input is the slicer output {-3,-1,+1,+3}; output is the dibit value 0..3.
func SymbolToDibit(sym int8) uint8 {
	switch sym {
	case 1:
		return 0
	case 3:
		return 1
	case -1:
		return 2
	case -3:
		return 3
	}
	return 0
}

// SymbolsToDibits converts a slicer-output stream into dibits.
func SymbolsToDibits(syms []int8) []uint8 {
	out := make([]uint8, len(syms))
	for i, s := range syms {
		out[i] = SymbolToDibit(s)
	}
	return out
}

// FrameSyncBits returns the 48 bits of the FSW MSB-first.
func FrameSyncBits() []byte { return framing.DibitsToBits(FrameSyncWord[:]) }

// SyncDetector slides a window over an incoming dibit stream and emits the
// (zero-based) dibit index where the FSW best matches above tolerance.
// Tolerance is the maximum allowed dibit-symbol mismatch; default 4.
type SyncDetector struct {
	tolerance int
	hist      [24]uint8
	primed    int
	pos       int
}

func NewSyncDetector(tolerance int) *SyncDetector {
	if tolerance < 0 {
		tolerance = 4
	}
	return &SyncDetector{tolerance: tolerance}
}

// Process appends to dst the indices (relative to baseIndex) where the FSW
// is detected. baseIndex is the absolute dibit index of src[0].
func (s *SyncDetector) Process(dst []int, src []uint8, baseIndex int) ([]int, int) {
	for i, d := range src {
		s.hist[s.pos] = d
		s.pos = (s.pos + 1) % 24
		if s.primed < 24 {
			s.primed++
			continue
		}
		mismatch := 0
		idx := s.pos
		for k := 0; k < 24; k++ {
			if s.hist[idx] != FrameSyncWord[k] {
				mismatch++
				if mismatch > s.tolerance {
					break
				}
			}
			idx = (idx + 1) % 24
		}
		if mismatch <= s.tolerance {
			// Match position: end of the FSW lands at this index.
			dst = append(dst, baseIndex+i)
		}
	}
	return dst, baseIndex + len(src)
}
