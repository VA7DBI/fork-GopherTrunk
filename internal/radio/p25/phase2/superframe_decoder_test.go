package phase2

import "testing"

// idleSuperframe builds a synthesized all-idle superframe: 12 idle
// sub-frames with the outbound sync injected into sub-frame
// SyncSubframeIndex by EncodeSuperframe.
func idleSuperframe() []uint8 {
	var subs [SubframesPerSuperframe][]uint8
	for i := range subs {
		subs[i] = IdleSubframe()
	}
	return EncodeSuperframe(subs)
}

func TestSuperframeDecoderSlicesOneSuperframe(t *testing.T) {
	const warmup = 50
	stream := make([]uint8, 0, warmup+DibitsPerSuperframe+warmup)
	stream = append(stream, make([]uint8, warmup)...) // warmup, no sync
	stream = append(stream, idleSuperframe()...)
	stream = append(stream, make([]uint8, warmup)...) // trailer

	d := NewSuperframeDecoder()
	got := d.Process(stream, 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 superframe, got %d", len(got))
	}
	sf := got[0]
	if sf.StartDibit != warmup {
		t.Errorf("StartDibit = %d, want %d", sf.StartDibit, warmup)
	}
	for i := 0; i < SubframesPerSuperframe; i++ {
		s := sf.Subframes[i]
		if s.Index != i {
			t.Errorf("sub-frame %d: Index = %d", i, s.Index)
		}
		if s.Timeslot != i&1 {
			t.Errorf("sub-frame %d: Timeslot = %d, want %d", i, s.Timeslot, i&1)
		}
		if len(s.Dibits) != DibitsPerSubframe {
			t.Errorf("sub-frame %d: len(Dibits) = %d, want %d", i, len(s.Dibits), DibitsPerSubframe)
		}
		// idleSuperframe builds all-zero sub-frames; an all-zero ISCH
		// codeword Golay-decodes to SlotType 0 = Unknown.
		if s.SlotType != SlotTypeUnknown {
			t.Errorf("sub-frame %d: SlotType = %v, want Unknown for an idle fixture", i, s.SlotType)
		}
	}
	// Sub-frame SyncSubframeIndex must carry the outbound sync verbatim.
	sync := OutboundSyncDibits()
	head := sf.Subframes[SyncSubframeIndex].Dibits[:SyncDibits]
	for i, d := range sync {
		if head[i] != d {
			t.Fatalf("sync dibit %d = %d, want %d", i, head[i], d)
		}
	}
}

func TestSuperframeDecoderTwoConsecutive(t *testing.T) {
	// The shared SyncDetector cannot evaluate a window until 20 dibits
	// are buffered, so a sync starting at absolute dibit 0 is never
	// checked — a real stream always has lead-in. The fixture prepends
	// a short warmup to model that.
	const warmup = 50
	stream := make([]uint8, 0, warmup+2*DibitsPerSuperframe)
	stream = append(stream, make([]uint8, warmup)...)
	stream = append(stream, idleSuperframe()...)
	stream = append(stream, idleSuperframe()...)

	d := NewSuperframeDecoder()
	got := d.Process(stream, 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 superframes, got %d", len(got))
	}
	if got[0].StartDibit != warmup {
		t.Errorf("first StartDibit = %d, want %d", got[0].StartDibit, warmup)
	}
	if got[1].StartDibit != warmup+DibitsPerSuperframe {
		t.Errorf("second StartDibit = %d, want %d", got[1].StartDibit, warmup+DibitsPerSuperframe)
	}
}

func TestSuperframeDecoderChunkedInput(t *testing.T) {
	const warmup = 50
	stream := make([]uint8, 0)
	stream = append(stream, make([]uint8, warmup)...)
	stream = append(stream, idleSuperframe()...)
	stream = append(stream, make([]uint8, warmup)...)

	d := NewSuperframeDecoder()
	var got []Superframe
	const chunk = 64
	for off := 0; off < len(stream); off += chunk {
		end := off + chunk
		if end > len(stream) {
			end = len(stream)
		}
		got = append(got, d.Process(stream[off:end], off)...)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 superframe across chunks, got %d", len(got))
	}
	if got[0].StartDibit != warmup {
		t.Errorf("StartDibit = %d, want %d", got[0].StartDibit, warmup)
	}
}

func TestSuperframeDecoderResetReacquires(t *testing.T) {
	const warmup = 50
	full := make([]uint8, 0, warmup+DibitsPerSuperframe)
	full = append(full, make([]uint8, warmup)...)
	full = append(full, idleSuperframe()...)

	d := NewSuperframeDecoder()
	// Feed a truncated stream (no full superframe yet), then re-sync.
	d.Process(full[:warmup+DibitsPerSuperframe/2], 0)
	d.Reset()
	got := d.Process(full, 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 superframe after Reset, got %d", len(got))
	}
	if got[0].StartDibit != warmup {
		t.Errorf("StartDibit = %d, want %d", got[0].StartDibit, warmup)
	}
}
