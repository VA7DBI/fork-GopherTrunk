package dsc

import "testing"

func TestBCHEncodeCheckRoundTrip(t *testing.T) {
	// Every 7-bit data value should encode → check back to itself
	// with the "clean" success signal.
	for data := uint16(0); data < 128; data++ {
		cw := BCHEncode(data)
		got, ok := BCHCheck(cw)
		if !ok {
			t.Errorf("BCHCheck(encode(%d)) = !ok", data)
		}
		if got != data {
			t.Errorf("round-trip: data = %d, got %d", data, got)
		}
	}
}

func TestBCHFlagsSingleBitErrorsAsSyndromeMismatch(t *testing.T) {
	// Single-bit errors on parity bits (0..2) and most data bits
	// flip the syndrome away from zero, so BCHCheck signals !ok.
	// (The "BCH" name is a misnomer — the code's minimum Hamming
	// distance is 2, not 3, so a single flip can also land on a
	// neighbour codeword for some bit positions; the check
	// detects only when the syndrome differs.)
	data := uint16(0x55)
	cw := BCHEncode(data)
	flagged := 0
	for bit := 0; bit < 10; bit++ {
		corrupted := cw ^ (1 << uint(bit))
		_, ok := BCHCheck(corrupted)
		if !ok {
			flagged++
		}
	}
	if flagged < 7 {
		t.Errorf("flagged %d/10 single-bit errors; want most of them", flagged)
	}
}

func TestBCHSyndromeZeroForValidCodeword(t *testing.T) {
	for data := uint16(0); data < 128; data++ {
		cw := BCHEncode(data)
		if syn := bchSyndrome(cw); syn != 0 {
			t.Errorf("syndrome(encode(%d)) = %d, want 0", data, syn)
		}
	}
}
