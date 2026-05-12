package dstar

// processState is the cross-call bit buffering + sync-detection
// state the Process adapter holds. Lazily initialised on the first
// Process call.
type processState struct {
	det *SyncDetector
	// remaining > 0: collecting PCH header bits after a Frame Sync
	// match; counts down to 0 as Process feeds bits forward.
	remaining int
	// pch accumulates the 41-byte (328-bit) PCH header window.
	pch []byte
	// matchScratch is reused across calls so SyncDetector.Process
	// doesn't allocate fresh slices.
	matchScratch []int
}

// HeaderBits is the on-wire size of the D-STAR PCH header in
// information bits, before the rate-1/2 convolutional / scrambler /
// interleaver chain that the JARL spec wraps around it.
//
// 41 bytes × 8 bits = 328 information bits. Real on-air D-STAR runs
// these through inner FEC that doubles the wire-side bit count, but
// the Process adapter consumes the post-FEC information bits — see
// the package doc for the "honest deferral" of the inner code.
const HeaderBits = 41 * 8

// Process consumes a window of raw bits from the D-STAR receiver
// (the IQ → GMSK bit chain in internal/radio/dstar/receiver/), runs
// the 32-bit Frame Sync detector, slices the following 328-bit PCH
// header window out of the stream, packs it into 41 bytes, runs
// CRC-CCITT verify, and forwards the parsed Header to Ingest.
//
// baseIdx is the absolute bit index of bits[0] across the stream
// lifetime. The adapter's internal countdown survives across
// Process calls so a sync match in one chunk and the payload in
// the next still decode cleanly.
//
// The convolutional rate-1/2 inner code / scrambler / interleaver
// the on-air PCH carries are documented follow-ups; this adapter
// works on synthesized fixtures and pre-FEC-stripped inputs.
//
// Returns baseIdx + len(bits) to match the Process contract every
// other ControlChannel package exposes.
func (c *ControlChannel) Process(bits []byte, baseIdx int) int {
	if c.proc == nil {
		// tolerance=0 because FrameSync (0x55555555) is perfectly
		// periodic and any tolerance > 0 fires near-matches a few
		// bits before the exact sync position, mis-aligning the
		// 328-bit header window. Real-air robustness against
		// single-bit errors lands when the JARL DV-mode preamble
		// (which carries non-periodic structure) replaces the toggle
		// placeholder in sync.go.
		c.proc = &processState{
			det: NewSyncDetector(FrameSyncBitsSlice(), 0),
			pch: make([]byte, 0, HeaderBits),
		}
	}
	p := c.proc

	p.matchScratch, _ = p.det.Process(p.matchScratch[:0], bits, baseIdx)
	matchIdx := 0

	for i, b := range bits {
		absPos := baseIdx + i
		// Collect first (this bit completes the 328-bit PCH if
		// remaining counts down to 0). Order matters: the sync
		// match's absolute index is the LAST bit of the 32-bit
		// Frame Sync, so the PCH starts at the NEXT iteration.
		if p.remaining > 0 {
			p.pch = append(p.pch, b&1)
			p.remaining--
			if p.remaining == 0 {
				if hdr, ok := c.parseHeader(p.pch); ok {
					c.Ingest(hdr)
				}
				p.pch = p.pch[:0]
			}
		}
		for matchIdx < len(p.matchScratch) && p.matchScratch[matchIdx] == absPos {
			// Skip matches that fire while we're still
			// collecting the previous window — D-STAR's
			// 0x55555555 Frame Sync is a near-alternating
			// pattern that produces tolerance-1 / tolerance-2
			// near-matches every couple of bits at the leading
			// edge of a real sync burst. Resetting on those
			// would lose the alignment we just locked.
			if p.remaining == 0 {
				p.remaining = HeaderBits
				p.pch = p.pch[:0]
			}
			matchIdx++
		}
	}
	return baseIdx + len(bits)
}

// parseHeader packs 328 MSB-first information bits into 41 bytes,
// verifies the CRC-CCITT trailer over the first 39 bytes against
// the 16-bit trailing field, and returns the structured Header.
// Returns (zero, false) when the CRC mismatches so the state
// machine ignores corrupted frames.
func (c *ControlChannel) parseHeader(bits []byte) (Header, bool) {
	if len(bits) != HeaderBits {
		return Header{}, false
	}
	bytesOut := make([]byte, 41)
	for i := 0; i < 41; i++ {
		var v byte
		for j := 0; j < 8; j++ {
			v = (v << 1) | (bits[i*8+j] & 1)
		}
		bytesOut[i] = v
	}
	hdr, err := ParseHeader(bytesOut)
	if err != nil {
		return Header{}, false
	}
	if hdr.CRC != ComputeCRC(bytesOut[:39]) {
		return Header{}, false
	}
	return hdr, true
}

// Reset clears the SyncDetector's history so a stale match doesn't
// fire after a stream re-sync.
func (s *SyncDetector) Reset() {
	for i := range s.hist {
		s.hist[i] = 0
	}
	s.primed = 0
	s.pos = 0
}
