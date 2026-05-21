package phase2

import (
	"errors"
	"testing"
)

func TestISCHRoundTrip(t *testing.T) {
	slots := []SlotType{
		SlotTypeUnknown, SlotTypeVoice4V, SlotTypeVoice2V,
		SlotTypeMACPTT, SlotTypeMACEnd, SlotTypeMACIdle,
		SlotTypeMACActive, SlotTypeMACHangtime, SlotTypeMACSignaling,
		SlotTypeMACEndCont,
	}
	for _, st := range slots {
		for counter := uint8(0); counter < SubframesPerSuperframe; counter++ {
			in := ISCH{SlotType: st, Counter: counter}
			dibits := EncodeISCH(in)
			if len(dibits) != ISCHDibits {
				t.Fatalf("EncodeISCH len = %d, want %d", len(dibits), ISCHDibits)
			}
			got, errs, err := DecodeISCH(dibits)
			if err != nil {
				t.Fatalf("DecodeISCH(%v) error: %v", in, err)
			}
			if errs != 0 {
				t.Errorf("DecodeISCH(%v) clean codeword corrected %d errors", in, errs)
			}
			if got != in {
				t.Errorf("DecodeISCH round-trip = %+v, want %+v", got, in)
			}
		}
	}
}

func TestISCHBitFlipCorrected(t *testing.T) {
	in := ISCH{SlotType: SlotTypeVoice4V, Counter: 7}
	dibits := EncodeISCH(in)
	// Flip 3 single bits across 3 distinct dibits — extended Golay
	// (24,12,8) corrects up to 3 bit errors.
	corrupt := append([]uint8(nil), dibits...)
	corrupt[1] ^= 0x1
	corrupt[5] ^= 0x2
	corrupt[9] ^= 0x1
	got, errs, err := DecodeISCH(corrupt)
	if err != nil {
		t.Fatalf("DecodeISCH with 3 bit errors should still decode: %v", err)
	}
	if errs != 3 {
		t.Errorf("corrected error count = %d, want 3", errs)
	}
	if got != in {
		t.Errorf("DecodeISCH after 3-bit corruption = %+v, want %+v", got, in)
	}
}

func TestISCHLengthError(t *testing.T) {
	_, _, err := DecodeISCH(make([]uint8, ISCHDibits-1))
	if !errors.Is(err, ErrISCHLength) {
		t.Errorf("DecodeISCH short input err = %v, want ErrISCHLength", err)
	}
}

func TestSuperframeDecoderPopulatesSlotType(t *testing.T) {
	// A representative mix of voice-bearing and MAC-bearing sub-frames.
	want := [SubframesPerSuperframe]SlotType{
		SlotTypeMACPTT, SlotTypeVoice4V, SlotTypeVoice2V, SlotTypeVoice4V,
		SlotTypeVoice2V, SlotTypeVoice4V, SlotTypeMACSignaling, SlotTypeVoice4V,
		SlotTypeVoice2V, SlotTypeVoice4V, SlotTypeVoice2V, SlotTypeMACEnd,
	}
	var subs [SubframesPerSuperframe][]uint8
	for i := range subs {
		sub := IdleSubframe()
		WriteISCH(sub, want[i], uint8(i))
		subs[i] = sub
	}

	const warmup = 50
	stream := make([]uint8, 0, warmup+DibitsPerSuperframe)
	stream = append(stream, make([]uint8, warmup)...)
	stream = append(stream, EncodeSuperframe(subs)...)

	d := NewSuperframeDecoder()
	got := d.Process(stream, 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 superframe, got %d", len(got))
	}
	for i := 0; i < SubframesPerSuperframe; i++ {
		s := got[0].Subframes[i]
		if s.SlotType != want[i] {
			t.Errorf("sub-frame %d: SlotType = %v, want %v", i, s.SlotType, want[i])
		}
		if s.SlotType.IsVoice() != want[i].IsVoice() {
			t.Errorf("sub-frame %d: IsVoice mismatch", i)
		}
	}
}
