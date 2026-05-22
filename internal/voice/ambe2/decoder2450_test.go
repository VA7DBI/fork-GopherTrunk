package ambe2

import (
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/voice"
)

// packFrame packs a 49-element bit slice (one bit per byte, MSB-first)
// into the 7-byte frame Decode expects.
func packFrame(bits []byte) []byte {
	f := make([]byte, FrameBytes)
	for i, b := range bits {
		if b&1 != 0 {
			f[i>>3] |= 1 << uint(7-(i&7))
		}
	}
	return f
}

func TestDMRVocoderRegistered(t *testing.T) {
	v, err := voice.DefaultRegistry.New(DMRVocoderName)
	if err != nil {
		t.Fatalf("registry.New(%q): %v", DMRVocoderName, err)
	}
	if v.Name() != DMRVocoderName {
		t.Errorf("Name() = %q, want %q", v.Name(), DMRVocoderName)
	}
	if v.FrameSize() != FrameBytes {
		t.Errorf("FrameSize() = %d, want %d", v.FrameSize(), FrameBytes)
	}
}

// TestDMRDecodeProducesFrames runs the 3600x2450 decode path over a
// spread of inputs and confirms each yields a full 160-sample frame
// without error — exercising every b0..b8 codebook lookup.
func TestDMRDecodeProducesFrames(t *testing.T) {
	d := NewDMR()
	for seed := 0; seed < 24; seed++ {
		bits := make([]byte, InfoBits)
		x := uint32(seed)*2654435761 + 1
		for i := range bits {
			x = x*1664525 + 1013904223
			bits[i] = byte(x >> 31)
		}
		pcm, err := d.Decode(packFrame(bits))
		if err != nil {
			t.Fatalf("seed %d: Decode: %v", seed, err)
		}
		if len(pcm) != 160 {
			t.Fatalf("seed %d: Decode returned %d samples, want 160", seed, len(pcm))
		}
	}
}

func TestDMRDecodeDeterministic(t *testing.T) {
	frame := packFrame(make([]byte, InfoBits))
	a, err := NewDMR().Decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewDMR().Decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic output at sample %d: %d vs %d", i, a[i], b[i])
		}
	}
}

// TestDMRDecodeRejectsBadLength confirms the frame-size guard fires.
func TestDMRDecodeRejectsBadLength(t *testing.T) {
	if _, err := NewDMR().Decode(make([]byte, FrameBytes-1)); err == nil {
		t.Error("expected an error for a short frame")
	}
}
