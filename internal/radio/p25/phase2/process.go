package phase2

import (
	"github.com/MattCheramie/GopherTrunk/internal/radio/framing"
)

// processState is the cross-call dibit buffering + sync-detection
// state the Process adapter holds. Lazily initialised.
type processState struct {
	det          *SyncDetector
	remaining    int
	macDibits    []uint8
	matchScratch []int
}

// macPDUDibits is the count of dibits the adapter collects after
// each 20-dibit outbound sync match. A MAC PDU after FEC removal
// is 18 bytes = 144 bits = 72 dibits (1 opcode + up to 17 payload
// bytes). The Trellis + interleaver layer that spans the full
// 180-dibit subframe isn't reversed here — the adapter reads the
// 72 information dibits straight from the wire, which works for
// test fixtures + clean signals but typically fails on noisy
// on-air captures. Trellis decoding is the documented follow-up.
const macPDUDibits = 72

// Process consumes a window of raw dibits from the Phase 2
// receiver (the IQ → H-DQPSK dibit chain in
// internal/radio/p25/phase2/receiver/), runs the 20-dibit
// outbound sync detector, slices the following 72-dibit MAC PDU
// out of the stream, parses it via ParseMACPDU, and forwards the
// result to Ingest.
//
// baseIdx is the absolute dibit index of dibits[0]. The adapter's
// internal countdown survives across Process calls so a sync
// match in one chunk and the MAC PDU payload in the next still
// decode cleanly.
//
// Returns baseIdx + len(dibits) to match the ControlChannel.Process
// contracts shared across protocols.
func (c *ControlChannel) Process(dibits []uint8, baseIdx int) int {
	if c.proc == nil {
		c.proc = &processState{
			det:       NewSyncDetector(OutboundSyncDibits(), 2),
			macDibits: make([]uint8, 0, macPDUDibits),
		}
	}
	p := c.proc

	p.matchScratch, _ = p.det.Process(p.matchScratch[:0], dibits, baseIdx)
	matchIdx := 0

	for i, d := range dibits {
		absPos := baseIdx + i
		if p.remaining > 0 {
			p.macDibits = append(p.macDibits, d)
			p.remaining--
			if p.remaining == 0 {
				bits := framing.DibitsToBits(p.macDibits)
				info := framing.PackBitsMSB(bits)
				if len(info) >= 18 {
					if pdu, err := ParseMACPDU(info[:18]); err == nil {
						c.Ingest(pdu)
					}
				}
				p.macDibits = p.macDibits[:0]
			}
		}
		for matchIdx < len(p.matchScratch) && p.matchScratch[matchIdx] == absPos {
			p.remaining = macPDUDibits
			p.macDibits = p.macDibits[:0]
			matchIdx++
		}
	}
	return baseIdx + len(dibits)
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
