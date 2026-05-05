package nxdn

import "testing"

func TestHexToDibitsRoundTrip(t *testing.T) {
	d := hexToDibits(0xC55A, 8)
	want := []uint8{3, 0, 1, 1, 1, 1, 2, 2}
	for i := range want {
		if d[i] != want[i] {
			t.Errorf("dibit[%d] = %d, want %d", i, d[i], want[i])
		}
	}
}

func TestSyncDetectorMatchesOutbound(t *testing.T) {
	det := NewSyncDetector([][]uint8{FSWDibitsOutbound}, 0)
	stream := make([]uint8, 30)
	copy(stream[10:], FSWDibitsOutbound)
	hits, _ := det.Process(nil, stream, 0)
	if len(hits) != 1 {
		t.Fatalf("hits = %v, want exactly 1", hits)
	}
	if hits[0].Index != 10+FSWDibits-1 {
		t.Errorf("index = %d, want %d", hits[0].Index, 10+FSWDibits-1)
	}
}

func TestSyncDetectorMatchesInbound(t *testing.T) {
	det := NewSyncDetector([][]uint8{FSWDibitsOutbound, FSWDibitsInbound}, 0)
	stream := make([]uint8, 30)
	copy(stream[5:], FSWDibitsInbound)
	hits, _ := det.Process(nil, stream, 0)
	if len(hits) != 1 || !hits[0].Inbound {
		t.Errorf("hits = %+v, want one inbound hit", hits)
	}
}

func TestSyncDetectorTolerates1Error(t *testing.T) {
	det := NewSyncDetector([][]uint8{FSWDibitsOutbound}, 1)
	stream := make([]uint8, 30)
	copy(stream[10:], FSWDibitsOutbound)
	stream[12] = (stream[12] + 1) % 4
	hits, _ := det.Process(nil, stream, 0)
	if len(hits) != 1 {
		t.Errorf("hits = %v, want 1", hits)
	}
}

func TestSyncDetectorRejectsTooManyErrors(t *testing.T) {
	det := NewSyncDetector([][]uint8{FSWDibitsOutbound}, 0)
	stream := make([]uint8, 30)
	copy(stream[10:], FSWDibitsOutbound)
	stream[12] = (stream[12] + 1) % 4
	hits, _ := det.Process(nil, stream, 0)
	if len(hits) != 0 {
		t.Errorf("hits = %v, want []", hits)
	}
}
