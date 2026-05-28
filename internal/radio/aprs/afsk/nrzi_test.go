package afsk

import "testing"

func TestNRZIDecoderInitialOutput(t *testing.T) {
	d := NewNRZIDecoder()
	if got := d.Decode(0); got != 1 {
		t.Errorf("first Decode(0) = %d, want 1 (placeholder for unseeded predecessor)", got)
	}
	if got := d.Decode(1); got != 0 {
		t.Errorf("transition 0→1 = %d, want 0", got)
	}
}

func TestNRZIDecoderTransitionVsHold(t *testing.T) {
	// AX.25 NRZI: 0 = transition, 1 = no transition.
	// Driving the decoder with a known raw stream and asserting
	// the logical-bit output verifies the polarity.
	d := NewNRZIDecoder()
	// Seed: first Decode primes the previous-bit register and
	// returns 1 unconditionally — discard it.
	_ = d.Decode(0)

	cases := []struct {
		raw  byte
		want byte
		name string
	}{
		{raw: 0, want: 1, name: "0→0 (no transition) → logical 1"},
		{raw: 1, want: 0, name: "0→1 (transition)    → logical 0"},
		{raw: 1, want: 1, name: "1→1 (no transition) → logical 1"},
		{raw: 0, want: 0, name: "1→0 (transition)    → logical 0"},
		{raw: 0, want: 1, name: "0→0 (no transition) → logical 1"},
	}
	for _, c := range cases {
		got := d.Decode(c.raw)
		if got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestNRZIDecoderClampsBadInput(t *testing.T) {
	d := NewNRZIDecoder()
	_ = d.Decode(0) // seed
	// Out-of-range values clamp to 1 — matches upstream Push
	// conventions in hdlc.Framer + aprs/receiver.
	if got := d.Decode(255); got != 0 {
		t.Errorf("Decode(255) = %d, want 0 (clamped to 1, which is a transition from 0)", got)
	}
	if got := d.Decode(7); got != 1 {
		t.Errorf("Decode(7) = %d, want 1 (clamped to 1, no transition from previous 1)", got)
	}
}

func TestNRZIDecoderResetClears(t *testing.T) {
	d := NewNRZIDecoder()
	_ = d.Decode(1)
	_ = d.Decode(0)
	d.Reset()
	// After Reset, the first Decode returns the placeholder 1
	// again regardless of the value, because the decoder is
	// unseeded.
	if got := d.Decode(0); got != 1 {
		t.Errorf("after Reset, first Decode = %d, want 1", got)
	}
}
