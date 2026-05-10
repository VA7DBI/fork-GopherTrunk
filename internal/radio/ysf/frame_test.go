package ysf

import "testing"

func TestFrameLayoutAddsUp(t *testing.T) {
	if got := FSWDibits + FICHDibits + PayloadDibits; got != FrameDibits {
		t.Errorf("layout sum = %d, want %d", got, FrameDibits)
	}
}

func TestFrameOffsetsContiguous(t *testing.T) {
	if FSWOffset != 0 {
		t.Errorf("FSWOffset = %d, want 0", FSWOffset)
	}
	if FICHOffset != FSWDibits {
		t.Errorf("FICHOffset = %d, want %d", FICHOffset, FSWDibits)
	}
	if PayloadOffset != FSWDibits+FICHDibits {
		t.Errorf("PayloadOffset = %d, want %d", PayloadOffset, FSWDibits+FICHDibits)
	}
	if PayloadOffset+PayloadDibits != FrameDibits {
		t.Errorf("payload end = %d, want frame end %d",
			PayloadOffset+PayloadDibits, FrameDibits)
	}
}

func TestFrameDurationMatches4800Baud(t *testing.T) {
	// 4800 sym/s × 0.1 s = 480 symbols. The FrameDurationMs constant
	// should match what the symbol-rate / frame-length math implies.
	const symbolRateHz = 4800
	wantMs := FrameDibits * 1000 / symbolRateHz
	if FrameDurationMs != wantMs {
		t.Errorf("FrameDurationMs = %d, want %d at 4800 sym/s", FrameDurationMs, wantMs)
	}
}
