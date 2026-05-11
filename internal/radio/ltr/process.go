package ltr

import "github.com/MattCheramie/GopherTrunk/internal/radio/framing"

// processState is the cross-call bit buffering + frame-alignment
// state the Process adapter holds. Lazily initialised.
type processState struct {
	buf []byte
	// aligned is true once we've committed to a 41-bit frame
	// boundary. Locked alignment processes frames at fixed
	// statusBits offsets; an unexpected Sync=0 at the boundary
	// unlocks and triggers a fresh search.
	aligned bool
	// off is the cursor into buf. While unaligned it's the next
	// bit to test for Sync=1; while aligned it's the next frame
	// start. Buffer trimming happens after each Process call so
	// off resets to 0.
	off int
}

// ManchesterDecodeMode controls whether the Process adapter
// Manchester-decodes its input bit stream before frame alignment.
// Some LTR variants transmit the sub-audible status word in bi-
// phase / Manchester encoding (each bit doubled, requiring a mid-
// bit transition); others ship raw NRZ. Operators configure the
// mode at receiver construction; the default is ManchesterOff
// since the dominant deployment is NRZ.
type ManchesterDecodeMode uint8

const (
	// ManchesterOff treats the BitSink stream as raw NRZ — no
	// Manchester decoding.
	ManchesterOff ManchesterDecodeMode = iota
	// ManchesterStrict requires every bit pair to carry a mid-
	// bit transition (01 or 10). On any transition-less pair the
	// adapter drops the current decode buffer + restarts at the
	// next pair. Useful when the operator is confident the system
	// uses Manchester so noise-pair detection is a strong filter.
	ManchesterStrict
	// ManchesterSoft majority-decodes each bit pair (01 → 0,
	// 10 → 1, 00 / 11 → first sample) and counts invalid pairs
	// per chunk. Falls back gracefully on bursts of noise.
	ManchesterSoft
)

// SetManchesterMode configures whether the Process adapter
// Manchester-decodes its input. Call before the first Process
// call; switching mode mid-stream resets the adapter's buffer.
func (c *ControlChannel) SetManchesterMode(m ManchesterDecodeMode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.manchesterMode = m
	if c.proc != nil {
		c.proc.buf = c.proc.buf[:0]
		c.proc.off = 0
		c.proc.aligned = false
	}
}

// statusBits is the on-air length of one LTR Status word (41 bits)
// transmitted continuously at 300 bps under the in-band voice. The
// MSB of every word is the Sync bit (always 1) — there is no
// separate sync pattern preceding each word.
const statusBits = 41

// Process consumes a window of raw bits from the LTR receiver (the
// IQ → sub-audible bit chain in internal/radio/ltr/receiver/) and
// drives the LTR state machine.
//
// LTR has no fixed sync pattern that frames Status words on the
// wire — instead every word starts with a Sync bit (always 1)
// and frames run back-to-back. The adapter searches the buffered
// stream for the first Sync=1 position, commits to that 41-bit
// alignment, and follows it forward. If a subsequent frame's
// Sync bit is 0 the alignment unlocks and the search restarts.
// Per-Status fields (Area, Group, GroupID, …) are validated by
// the state machine's Ingest method via its Sync + Area filters
// and the activeGroup dedup.
//
// baseIdx is the absolute bit index of bits[0] across the stream
// lifetime; the adapter doesn't use it directly today.
//
// FCS (12-bit trailer) verification and Manchester de-encoding of
// the on-air bit stream are documented follow-ups; until they
// ship the adapter is honest about its noise floor — false
// alignment leads to spurious Ingest calls that the state
// machine silently drops, and a small fraction of correctly-
// aligned frames drive cc.locked + grant publication.
func (c *ControlChannel) Process(bits []byte, baseIdx int) int {
	if c.proc == nil {
		c.proc = &processState{}
	}
	p := c.proc

	// Optional Manchester preprocess: each input bit pair decodes
	// to one frame-level bit. Configure via SetManchesterMode.
	switch c.manchesterMode {
	case ManchesterStrict:
		decoded, err := framing.ManchesterDecode(bits)
		if err != nil {
			// Transition-less pair detected — drop the rest of
			// this chunk + reset alignment so the next call
			// re-anchors. Partial decode is still pushed so
			// alignment can pick up at the first good frame.
			p.aligned = false
		}
		bits = decoded
	case ManchesterSoft:
		decoded, _ := framing.ManchesterDecodeMajority(bits)
		bits = decoded
	}

	p.buf = append(p.buf, bits...)

	for {
		if !p.aligned {
			// Slide forward until we find a Sync=1 with room
			// for the rest of the 41-bit window.
			for p.off+statusBits <= len(p.buf) && p.buf[p.off] != 1 {
				p.off++
			}
			if p.off+statusBits > len(p.buf) {
				break
			}
			st, _ := StatusFromBits(p.buf[p.off : p.off+statusBits])
			c.Ingest(st)
			p.aligned = true
			p.off += statusBits
			continue
		}
		// Aligned: pull the next frame at the fixed offset.
		if p.off+statusBits > len(p.buf) {
			break
		}
		window := p.buf[p.off : p.off+statusBits]
		if window[0] != 1 {
			// Sync invariant broken — unlock and re-search.
			p.aligned = false
			continue
		}
		st, _ := StatusFromBits(window)
		c.Ingest(st)
		p.off += statusBits
	}

	// Trim consumed bits from the front. Keeps any partial frame
	// (or unconsumed search prefix) for the next call.
	if p.off > 0 {
		drop := p.off
		if drop > len(p.buf) {
			drop = len(p.buf)
		}
		copy(p.buf, p.buf[drop:])
		p.buf = p.buf[:len(p.buf)-drop]
		p.off = 0
	}
	return baseIdx + len(bits)
}
