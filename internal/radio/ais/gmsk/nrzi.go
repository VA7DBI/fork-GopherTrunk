package gmsk

// NRZIDecoder reverses the Non-Return-to-Zero Inverted line coding
// AIS transmitters apply between the bit-stuffer and the GMSK
// modulator (same convention as AX.25 — see ITU-R M.1371-5 §4.2.4):
//
//   - 0 on the wire → tone transition
//   - 1 on the wire → no transition (tone stays put)
//
// So decoding is: emit 1 when the current raw bit matches the
// previous one, 0 when it doesn't.
//
// The decoder seeds its previous-bit state on the first sample and
// emits a placeholder 1 for that bit — the HDLC framer's sliding-
// flag detector tolerates the initial bit of garbage and resyncs
// on the next 0x7E it sees.
//
// Identical to internal/radio/aprs/afsk.NRZIDecoder; copied rather
// than shared so both DSP frontends stay self-contained until a
// refactor PR makes the shared placement obvious.
type NRZIDecoder struct {
	last   byte
	primed bool
}

// NewNRZIDecoder returns a decoder in the unseeded state.
func NewNRZIDecoder() *NRZIDecoder { return &NRZIDecoder{} }

// Decode returns the logical bit corresponding to the next raw bit
// off the slicer. Values outside {0, 1} are clamped to 1 to match
// the convention upstream (hdlc.Framer.Push, ais/receiver.Push).
func (d *NRZIDecoder) Decode(raw byte) byte {
	if raw > 1 {
		raw = 1
	}
	if !d.primed {
		d.last = raw
		d.primed = true
		return 1
	}
	var out byte = 1
	if raw != d.last {
		out = 0
	}
	d.last = raw
	return out
}

// Reset returns the decoder to its unseeded state. Call when the
// upstream demod loses lock (FM squelch close, retune) so a stale
// last-bit doesn't garble the first bit after re-acquisition.
func (d *NRZIDecoder) Reset() {
	d.last = 0
	d.primed = false
}
