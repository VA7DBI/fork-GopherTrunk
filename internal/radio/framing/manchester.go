package framing

import "errors"

// ManchesterEncode turns an input bit stream into a Manchester-encoded
// (bi-phase-level) stream — each input bit produces 2 output bits with
// a mandatory mid-bit transition:
//
//	0  →  01
//	1  →  10
//
// (The opposite convention — 0→10, 1→01 — is also used in the wild;
// some LTR / Motorola variants pick the inverse mapping. Callers
// needing the inverse can pass the input through ManchesterFlip or
// invert the output before / after this call.)
//
// Manchester encoding doubles the wire rate but provides a self-
// clocking signal and rejects DC. It's used by LTR's sub-audible
// status word, some EDACS variants, and a handful of legacy
// trunked-radio data layers.
func ManchesterEncode(bits []byte) []byte {
	out := make([]byte, 2*len(bits))
	for i, b := range bits {
		if b&1 != 0 {
			out[2*i] = 1
			out[2*i+1] = 0
		} else {
			out[2*i] = 0
			out[2*i+1] = 1
		}
	}
	return out
}

// ErrManchesterInvalid is returned when ManchesterDecode sees a
// same-value pair (00 or 11) — both encode no mid-bit transition and
// therefore can't have come from a valid Manchester-encoded source.
// The partial decode up to the first error is still returned so the
// caller can decide whether to drop the frame or attempt soft
// recovery.
var ErrManchesterInvalid = errors.New("framing: Manchester pair lacks a mid-bit transition")

// ManchesterDecode reverses ManchesterEncode: each pair of input bits
// decodes to one output bit (01 → 0, 10 → 1). Same-value pairs (00
// or 11) signal a transition-less wire window — most likely a bit
// error or a non-Manchester-encoded stream — and abort the decode
// with ErrManchesterInvalid. The returned slice holds the bits
// decoded up to the error position so the caller can use partial
// results for diagnostics.
//
// The input length must be even; odd-length input drops the trailing
// bit silently (the caller is expected to align at a frame boundary).
func ManchesterDecode(bits []byte) ([]byte, error) {
	n := len(bits) / 2
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		a := bits[2*i] & 1
		b := bits[2*i+1] & 1
		switch {
		case a == 0 && b == 1:
			out = append(out, 0)
		case a == 1 && b == 0:
			out = append(out, 1)
		default:
			return out, ErrManchesterInvalid
		}
	}
	return out, nil
}

// ManchesterDecodeMajority is the soft alternative to ManchesterDecode:
// instead of returning an error on a transition-less pair, it picks
// the bit value indicated by the first sample of the pair and reports
// the number of pairs that disagreed. Useful when the upstream demod
// produces occasional bit errors and the caller would rather get a
// best-effort decode than no decode at all.
//
// Returns the decoded bits plus the count of invalid pairs. A non-
// zero count signals the caller's BER is high; a count equal to the
// output length means the stream is almost certainly not Manchester-
// encoded.
func ManchesterDecodeMajority(bits []byte) ([]byte, int) {
	n := len(bits) / 2
	out := make([]byte, n)
	invalid := 0
	for i := 0; i < n; i++ {
		a := bits[2*i] & 1
		b := bits[2*i+1] & 1
		switch {
		case a == 0 && b == 1:
			out[i] = 0
		case a == 1 && b == 0:
			out[i] = 1
		default:
			invalid++
			out[i] = a // tie → take the first sample
		}
	}
	return out, invalid
}
