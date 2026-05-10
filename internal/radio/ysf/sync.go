// Package ysf decodes the wire format of Yaesu System Fusion, the
// amateur-radio digital mode. YSF runs 4800-baud 4-level C4FM (the
// same modulation P25 Phase 1 and NXDN-9600 use); each 100 ms frame
// is 480 symbols / dibits / 960 bits, structured as:
//
//	dibits[0..19]    FSW (Frame Sync Word, 40 bits)
//	dibits[20..119]  FICH (Frame Information Channel, Trellis-encoded)
//	dibits[120..479] DCH (voice / data payload)
//
// This pass ships the sync detector + frame layout + a per-frequency
// state machine that publishes cc.locked when the FSW correlates.
// Full FICH decode (½-rate convolutional Viterbi + Yaesu-specific bit
// packing) is a self-contained follow-up — landing the sync layer is
// the first half of the same scaffolding NXDN / P25 Phase 1 use, and
// gives the orchestration layer something to lock onto for ham
// repeaters that operators want to see in the active-systems UI.
package ysf

// FSW is the 40-bit Frame Sync Word that opens every YSF frame. The
// pattern is fixed by the Yaesu specification and matches what every
// open-source decoder (DSDcc, MMDVMHost, OP25) uses on-air.
const FSWBits uint64 = 0xD471C9634D

// FSWDibits is the length of the FSW measured in dibits (20 dibits =
// 40 bits = 5 bytes).
const FSWDibits = 20

// FSWPattern is the 20-dibit decomposition of FSWBits, MSB-first. The
// sync detector compares dibit values directly against this slice.
var FSWPattern = bitsToDibits(FSWBits, FSWDibits)

func bitsToDibits(bits uint64, n int) []uint8 {
	out := make([]uint8, n)
	for i := 0; i < n; i++ {
		shift := uint(2 * (n - 1 - i))
		out[i] = uint8((bits >> shift) & 0x3)
	}
	return out
}

// SyncDetector slides a window over an incoming dibit stream and
// emits the absolute index where the YSF FSW matches within the
// configured tolerance. Mirrors the NXDN / P25 Phase 1 detectors;
// callers should pre-map their slicer output to dibit values.
type SyncDetector struct {
	pattern   []uint8
	tolerance int
	hist      []uint8
	primed    int
	pos       int
}

// NewSyncDetector returns a detector keyed on the standard YSF FSW
// pattern with the given max-mismatch tolerance. tolerance < 0 is
// clamped to 1 (matches the NXDN detector default).
func NewSyncDetector(tolerance int) *SyncDetector {
	if tolerance < 0 {
		tolerance = 1
	}
	return &SyncDetector{
		pattern:   FSWPattern,
		tolerance: tolerance,
		hist:      make([]uint8, FSWDibits),
	}
}

// Process scans src and appends the absolute dibit indices where the
// FSW ends to dst. baseIndex is the absolute dibit index of src[0].
// Returns the new baseIndex.
func (s *SyncDetector) Process(dst []int, src []uint8, baseIndex int) ([]int, int) {
	for i, d := range src {
		s.hist[s.pos] = d
		s.pos = (s.pos + 1) % FSWDibits
		// Compare on every iteration where the history holds at
		// least FSWDibits samples — including the one that just
		// filled the buffer for the first time, so an FSW that
		// sits at the very start of src still triggers a hit.
		if s.primed < FSWDibits {
			s.primed++
		}
		if s.primed < FSWDibits {
			continue
		}
		errs := 0
		idx := s.pos
		for k := 0; k < FSWDibits; k++ {
			if s.hist[idx] != s.pattern[k] {
				errs++
				if errs > s.tolerance {
					break
				}
			}
			idx = (idx + 1) % FSWDibits
		}
		if errs <= s.tolerance {
			dst = append(dst, baseIndex+i)
		}
	}
	return dst, baseIndex + len(src)
}
