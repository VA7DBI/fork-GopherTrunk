package pocsag

import (
	"time"
)

// Syncer consumes a packed bit stream (one bit per byte, 0 or 1) and
// emits Pages as it recognises POCSAG batches. The syncer is
// polarity-agnostic — it tries both the on-wire bit pattern and its
// bit-inverse for the sync codeword so a polarity-inverted FM demod
// (the usual gotcha for FSK-over-FM) still works without operator
// intervention.
//
// Lifecycle: Feed each new bit as it arrives off the symbol slicer
// (typically Mueller-Müller in DSP). Push() emits any completed
// pages on its return value — typically zero or one per call. The
// caller consumes pages and forwards them onto the events bus or
// SQLite log.
//
// Internal state machine:
//
//   - locking: the syncer slides a 32-bit window over the bit stream
//     looking for the sync codeword. Until it matches we accumulate
//     bits but emit nothing.
//   - in_batch: after a sync match we consume the next 16 codewords
//     (512 bits) one at a time and feed each through the parser /
//     assembler. After the 16th codeword we revert to locking and
//     look for the next sync.
type Syncer struct {
	// nowFn supplies the timestamp stamped on each emitted Page.
	// Defaults to time.Now; tests inject a deterministic clock.
	nowFn func() time.Time

	// Bit window. We keep a sliding 32-bit shift register so we can
	// match sync without storing the entire pre-sync junk.
	window      uint32
	bitsInBatch int // 0..512 once locked
	batchBits   [BatchBits - CodewordBits]byte
	locked      bool
	polarity    byte // 0 or 1 — bits XORed with this before parsing

	// Page assembler state. When an address codeword starts a page
	// we collect subsequent message codewords until the next
	// address or idle codeword terminates the page.
	collecting    bool
	pageRIC       uint32
	pageFunc      Function
	pageMsgs      []Codeword
	pageCorrected int
	// pageStart is the wallclock time of the address codeword that
	// began the currently-collecting page. Stamped on the emitted
	// Page when the page finishes.
	pageStart time.Time
}

// NewSyncer constructs an unlocked syncer. Bits must be fed via
// repeated Push calls — the syncer doesn't poll an input channel
// itself (that's the DSP layer's job).
func NewSyncer() *Syncer {
	return &Syncer{nowFn: time.Now}
}

// SetClock overrides the timestamp source. Test-only.
func (s *Syncer) SetClock(fn func() time.Time) { s.nowFn = fn }

// Locked reports whether the syncer has acquired the sync
// codeword. Surfaces as a diagnostic counter on the web panel.
func (s *Syncer) Locked() bool { return s.locked }

// Push feeds one bit (0 or 1) and returns any pages that completed
// as a result. Most calls return nil; pages emerge on the order of
// once per batch or less.
//
// A bit value other than 0 or 1 is treated as 1 — symbol slicers
// occasionally emit out-of-range values when their soft decision
// straddles zero; rejecting them outright would desync the syncer
// for no benefit.
func (s *Syncer) Push(bit byte) []Page {
	if bit > 1 {
		bit = 1
	}
	s.window = (s.window << 1) | uint32(bit)

	if !s.locked {
		// Try both polarities.
		if s.window == SyncCodeword {
			s.locked = true
			s.polarity = 0
			s.bitsInBatch = 0
			return nil
		}
		if ^s.window == SyncCodeword {
			s.locked = true
			s.polarity = 1
			s.bitsInBatch = 0
			return nil
		}
		return nil
	}

	// Already locked — accumulate the bit into the current batch
	// (post-polarity-correction).
	s.batchBits[s.bitsInBatch] = bit ^ s.polarity
	s.bitsInBatch++

	if s.bitsInBatch < (BatchBits - CodewordBits) {
		return nil
	}

	// 16 codewords accumulated — parse them.
	pages := s.processBatch()

	// Revert to locking for the next sync.
	s.locked = false
	s.window = 0
	s.bitsInBatch = 0
	return pages
}

// processBatch parses 16 codewords from the just-completed batch
// and feeds them through the page assembler. Returns any pages
// that finished within this batch.
func (s *Syncer) processBatch() []Page {
	var out []Page
	for i := 0; i < BatchCodewords; i++ {
		start := i * CodewordBits
		cw := packCodeword(s.batchBits[start : start+CodewordBits])
		decoded := Decode(cw)
		if decoded.CorrectedErrors < 0 && decoded.Type != WordTypeSync && decoded.Type != WordTypeIdle {
			// Uncorrectable codeword — terminate any in-progress
			// page (the body is now suspect) and continue scanning.
			if p := s.flushPage(); p != nil {
				out = append(out, *p)
			}
			continue
		}
		switch decoded.Type {
		case WordTypeAddress:
			if p := s.flushPage(); p != nil {
				out = append(out, *p)
			}
			s.collecting = true
			s.pageRIC = ReconstructRIC(decoded.Address, FrameSlot(i))
			s.pageFunc = decoded.Func
			s.pageMsgs = s.pageMsgs[:0]
			s.pageCorrected = decoded.CorrectedErrors
			s.pageStart = s.nowFn()
		case WordTypeMessage:
			if s.collecting {
				s.pageMsgs = append(s.pageMsgs, decoded)
				if decoded.CorrectedErrors > 0 {
					s.pageCorrected += decoded.CorrectedErrors
				}
			}
		case WordTypeIdle:
			if p := s.flushPage(); p != nil {
				out = append(out, *p)
			}
		}
	}
	return out
}

// flushPage emits the in-progress page if one is being collected,
// then clears the assembler state. Returns nil when there is no
// in-progress page.
func (s *Syncer) flushPage() *Page {
	if !s.collecting {
		return nil
	}
	s.collecting = false

	// Heuristic encoding pick: function code B (0x1) traditionally
	// carries numeric, C (0x2) traditionally carries alpha. Function
	// codes A and D vary by network — fall back to alpha (the
	// printable-safe choice; a numeric stream rendered as alpha
	// looks like garbage and the operator can pick the right
	// decoder from the panel).
	enc := EncodingAlpha
	switch s.pageFunc {
	case 1:
		enc = EncodingNumeric
	case 2:
		enc = EncodingAlpha
	}
	var text string
	if enc == EncodingNumeric {
		text = DecodeNumeric(s.pageMsgs)
	} else {
		text = DecodeAlpha(s.pageMsgs)
	}
	p := Page{
		RIC:       s.pageRIC,
		Func:      s.pageFunc,
		Encoding:  enc,
		Text:      text,
		Corrected: s.pageCorrected,
	}
	// Reuse the buffer.
	s.pageMsgs = s.pageMsgs[:0]
	s.pageCorrected = 0
	_ = s.pageStart // available if a follow-up wants per-page timestamps off the assembler
	return &p
}

// Flush emits any in-progress page without waiting for a terminator.
// Called at end-of-stream (operator stops the SDR, replay finishes,
// the connection drops) so the last page in flight doesn't get
// lost. Returns nil when no page is in progress.
func (s *Syncer) Flush() *Page {
	return s.flushPage()
}
