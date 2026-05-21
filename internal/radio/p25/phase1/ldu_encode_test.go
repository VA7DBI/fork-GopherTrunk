package phase1

import (
	"bytes"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/voice/imbe"
)

func TestAssembleLDURoundTrip(t *testing.T) {
	var voice [LDUVoiceSubframeCount][]byte
	var want [LDUVoiceSubframeCount][]byte
	for s := range voice {
		info := make([]byte, imbe.InfoBits)
		for i := range info {
			info[i] = byte((s*7 + i*3) & 1)
		}
		ch, err := imbe.EncodeChannel(info)
		if err != nil {
			t.Fatalf("EncodeChannel: %v", err)
		}
		onAir, err := imbe.Scramble(ch)
		if err != nil {
			t.Fatalf("Scramble: %v", err)
		}
		// Scramble/Descramble mutate in place — keep an independent copy
		// for the LDU so the want computation below cannot corrupt it.
		voice[s] = append([]byte(nil), onAir...)
		want[s], _, _ = imbe.DecodeChannelToFrame(onAir)
	}
	var lces [LDULCESBlockCount][]byte
	var lsd [LDULSDBlockCount][]byte

	ldu, err := AssembleLDU(0x123, DUIDLogicalLink1, voice, lces, lsd)
	if err != nil {
		t.Fatalf("AssembleLDU: %v", err)
	}
	if len(ldu) != LDUTotalBits {
		t.Fatalf("AssembleLDU len = %d, want %d", len(ldu), LDUTotalBits)
	}

	frames, _, err := ExtractVoiceFrames(ldu)
	if err != nil {
		t.Fatalf("ExtractVoiceFrames: %v", err)
	}
	for i := range frames {
		if !bytes.Equal(frames[i], want[i]) {
			t.Errorf("subframe %d: extracted frame != assembled frame", i)
		}
	}
}
