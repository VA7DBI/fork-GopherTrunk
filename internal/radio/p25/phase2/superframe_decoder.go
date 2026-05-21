package phase2

// SuperframeDecoder assembles structured P25 Phase 2 superframes from a
// raw dibit stream. It is the TDMA-layer counterpart of the flat
// ControlChannel.Process adapter (process.go): where Process slices a
// MAC PDU window on every outbound-sync match, the SuperframeDecoder
// locks onto the 360 ms superframe boundary via that same 20-dibit
// outbound sync and then slices all 12 sub-frames at the fixed
// DibitsPerSubframe (180-dibit) TDMA cadence.
//
// Sub-frames alternate between the two TDMA timeslots — even Index →
// timeslot 0, odd Index → timeslot 1 — so a caller routing voice or
// MAC sub-frames can demultiplex the two logical channels by Index
// parity without any further symbol-level work.
//
// A SuperframeDecoder is stateful and not safe for concurrent use;
// construct one per traffic-channel decode chain. Buffering spans
// Process calls so a superframe straddling IQ-chunk boundaries is
// still assembled.

// Subframe is one of the 12 sub-frames of a Superframe.
type Subframe struct {
	// Index is the sub-frame position 0..11 within the superframe.
	Index int
	// Timeslot is the TDMA timeslot the sub-frame belongs to, 0 or 1,
	// derived from Index parity.
	Timeslot int
	// SlotType names what the sub-frame carries (voice vs MAC). It is
	// SlotTypeUnknown until the ISCH decode in isch.go fills it.
	SlotType SlotType
	// Dibits holds the DibitsPerSubframe raw dibits of the sub-frame.
	Dibits []uint8
}

// Superframe is one decoded 360 ms P25 Phase 2 superframe.
type Superframe struct {
	// StartDibit is the absolute dibit index of sub-frame 0's first
	// dibit in the source stream.
	StartDibit int
	// Subframes holds the 12 sub-frames in transmission order.
	Subframes [SubframesPerSuperframe]Subframe
}

// syncLookback maps a sync match — reported by SyncDetector as the
// absolute index of the sync word's last dibit — back to the first
// dibit of the sub-frame that carries it.
const syncLookback = SyncDibits - 1

// superframeBufKeep retains enough dibits that a pending anchor near
// the tail of the buffer can still slice its full superframe once the
// trailing sub-frames arrive. Dibits are one byte each, so the cost is
// trivial.
const superframeBufKeep = 2 * DibitsPerSuperframe

// SuperframeDecoder is the stateful dibit → Superframe assembler.
type SuperframeDecoder struct {
	det      *SyncDetector
	buf      []uint8
	bufStart int   // absolute dibit index of buf[0]
	pending  []int // sync-match indices awaiting a full superframe
}

// NewSuperframeDecoder returns a SuperframeDecoder ready to consume
// dibits.
func NewSuperframeDecoder() *SuperframeDecoder {
	return &SuperframeDecoder{det: NewSyncDetector(OutboundSyncDibits(), 2)}
}

// Reset clears all buffered state. Call on a stream re-sync so a stale
// anchor does not slice across the discontinuity.
func (d *SuperframeDecoder) Reset() {
	d.buf = d.buf[:0]
	d.bufStart = 0
	d.pending = d.pending[:0]
	d.det = NewSyncDetector(OutboundSyncDibits(), 2)
}

// Process consumes a window of dibits and returns every superframe that
// completed within it. baseIdx is the absolute dibit index of
// dibits[0]; it must be monotonically non-decreasing across calls.
// Superframes are returned in stream order.
func (d *SuperframeDecoder) Process(dibits []uint8, baseIdx int) []Superframe {
	if len(d.buf) == 0 {
		d.bufStart = baseIdx
	}
	d.buf = append(d.buf, dibits...)

	matches, _ := d.det.Process(nil, dibits, baseIdx)
	d.pending = append(d.pending, matches...)

	var out []Superframe
	bufEnd := d.bufStart + len(d.buf)
	keep := d.pending[:0]
	for _, m := range d.pending {
		start := m - syncLookback - SyncSubframeIndex*DibitsPerSubframe
		if start+DibitsPerSuperframe > bufEnd {
			keep = append(keep, m) // trailing sub-frames not buffered yet
			continue
		}
		if start < d.bufStart {
			continue // anchor fell off the front of the buffer
		}
		out = append(out, d.sliceSuperframe(start))
	}
	d.pending = keep
	d.trim()
	return out
}

// trim bounds the cross-call buffer. A pending anchor whose data is
// dropped is discarded by the start < bufStart guard in Process.
func (d *SuperframeDecoder) trim() {
	if len(d.buf) <= superframeBufKeep {
		return
	}
	drop := len(d.buf) - superframeBufKeep
	copy(d.buf, d.buf[drop:])
	d.buf = d.buf[:superframeBufKeep]
	d.bufStart += drop
}

// sliceSuperframe cuts the 12 fixed-length sub-frames starting at
// absolute dibit index start. The caller has confirmed the full
// superframe span is buffered.
func (d *SuperframeDecoder) sliceSuperframe(start int) Superframe {
	sf := Superframe{StartDibit: start}
	off := start - d.bufStart
	for i := 0; i < SubframesPerSuperframe; i++ {
		sub := make([]uint8, DibitsPerSubframe)
		copy(sub, d.buf[off+i*DibitsPerSubframe:off+(i+1)*DibitsPerSubframe])
		sf.Subframes[i] = Subframe{
			Index:    i,
			Timeslot: i & 1,
			SlotType: SlotTypeUnknown,
			Dibits:   sub,
		}
	}
	return sf
}
