package ysf

import "testing"

func TestFSWPatternRoundTrips(t *testing.T) {
	if len(FSWPattern) != FSWDibits {
		t.Fatalf("FSWPattern len = %d, want %d", len(FSWPattern), FSWDibits)
	}
	// Reassemble the pattern bits and compare against FSWBits.
	var got uint64
	for _, d := range FSWPattern {
		got = (got << 2) | uint64(d&0x3)
	}
	if got != FSWBits {
		t.Errorf("FSWPattern reassemble = %010X, want %010X", got, FSWBits)
	}
}

func TestSyncDetectorMatchesCleanFSW(t *testing.T) {
	det := NewSyncDetector(0)
	stream := make([]uint8, 80)
	copy(stream[15:], FSWPattern)
	hits, _ := det.Process(nil, stream, 0)
	if len(hits) != 1 {
		t.Fatalf("hits = %v, want exactly 1", hits)
	}
	wantIdx := 15 + FSWDibits - 1
	if hits[0] != wantIdx {
		t.Errorf("hit index = %d, want %d (FSW ends here)", hits[0], wantIdx)
	}
}

func TestSyncDetectorTolerance(t *testing.T) {
	stream := make([]uint8, 80)
	copy(stream[5:], FSWPattern)
	// Flip 2 dibits inside the FSW window.
	stream[7] ^= 1
	stream[15] ^= 1

	if hits, _ := NewSyncDetector(2).Process(nil, stream, 0); len(hits) != 1 {
		t.Errorf("tolerance=2: hits = %v, want 1", hits)
	}
	if hits, _ := NewSyncDetector(0).Process(nil, stream, 0); len(hits) != 0 {
		t.Errorf("tolerance=0: hits = %v, want 0 (corrupt FSW)", hits)
	}
}

func TestSyncDetectorIgnoresUnrelatedStream(t *testing.T) {
	det := NewSyncDetector(1)
	// Pseudo-random dibits unlikely to match the FSW within tolerance.
	stream := make([]uint8, 1024)
	for i := range stream {
		stream[i] = uint8(i*7) & 0x3
	}
	hits, _ := det.Process(nil, stream, 0)
	if len(hits) != 0 {
		t.Errorf("unrelated stream produced %d false hits: %v", len(hits), hits)
	}
}

func TestSyncDetectorNegativeToleranceClampsToOne(t *testing.T) {
	det := NewSyncDetector(-3)
	if det.tolerance != 1 {
		t.Errorf("negative tolerance: got %d, want 1 (clamp)", det.tolerance)
	}
}
