package phase1

import "testing"

func TestSymbolToDibit(t *testing.T) {
	cases := map[int8]uint8{1: 0, 3: 1, -1: 2, -3: 3}
	for sym, want := range cases {
		if got := SymbolToDibit(sym); got != want {
			t.Errorf("SymbolToDibit(%d) = %d, want %d", sym, got, want)
		}
	}
}

func TestSyncDetectorMatchesCleanFSW(t *testing.T) {
	det := NewSyncDetector(0)
	stream := make([]uint8, 100)
	// Place an FSW at offset 50.
	copy(stream[50:], FrameSyncWord[:])
	hits, _ := det.Process(nil, stream, 0)
	if len(hits) != 1 || hits[0] != 50+24-1 {
		t.Errorf("hits = %v, want [%d]", hits, 50+24-1)
	}
}

func TestSyncDetectorTolerates2Errors(t *testing.T) {
	det := NewSyncDetector(2)
	stream := make([]uint8, 100)
	copy(stream[20:], FrameSyncWord[:])
	stream[22] = (stream[22] + 1) % 4
	stream[40] = (stream[40] + 1) % 4
	hits, _ := det.Process(nil, stream, 0)
	if len(hits) != 1 {
		t.Errorf("hits = %v, want exactly 1", hits)
	}
}

func TestSyncDetectorRejectsTooManyErrors(t *testing.T) {
	det := NewSyncDetector(1)
	stream := make([]uint8, 60)
	copy(stream[10:], FrameSyncWord[:])
	stream[12] = (stream[12] + 1) % 4
	stream[14] = (stream[14] + 1) % 4
	stream[16] = (stream[16] + 1) % 4
	hits, _ := det.Process(nil, stream, 0)
	if len(hits) != 0 {
		t.Errorf("hits = %v, want []", hits)
	}
}
