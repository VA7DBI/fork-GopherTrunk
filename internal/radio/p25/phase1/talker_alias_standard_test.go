package phase1

import (
	"testing"
	"time"
)

// makeHeader builds an LC content matching the standard talker-alias
// HEADER working model: MFID + format byte + declared length + 5
// alias bytes in octets 4..8.
func makeHeader(length uint8, tail string) [lcContentOctets]byte {
	var c [lcContentOctets]byte
	c[0] = LCOTalkerAliasHeader
	c[3] = length
	for i := 0; i < 5 && i < len(tail); i++ {
		c[4+i] = tail[i]
	}
	return c
}

func makeBlock(lcf uint8, payload string) [lcContentOctets]byte {
	var c [lcContentOctets]byte
	c[0] = lcf
	for i := 0; i < 7 && i < len(payload); i++ {
		c[2+i] = payload[i]
	}
	return c
}

func TestStandardTalkerAliasBufHeaderBlock1Block2(t *testing.T) {
	b := NewStandardTalkerAliasBuf(nil)
	// HEADER + BLOCK1 alone shouldn't emit — the assembler waits for
	// BLOCK2 so the first emission is the full alias.
	if alias, ok := b.AddFragment(LCOTalkerAliasHeader, makeHeader(13, "ENGIN")); ok {
		t.Errorf("HEADER alone emitted %q", alias)
	}
	if alias, ok := b.AddFragment(LCOTalkerAliasBlock1, makeBlock(LCOTalkerAliasBlock1, "E-12 UN")); ok {
		t.Errorf("HEADER+BLOCK1 emitted %q before BLOCK2", alias)
	}
	alias, ok := b.AddFragment(LCOTalkerAliasBlock2, makeBlock(LCOTalkerAliasBlock2, "IT"))
	if !ok {
		t.Fatal("BLOCK2 did not complete the alias")
	}
	// HEADER tail = "ENGIN", BLOCK1 = "E-12 UN", BLOCK2 = "IT" → 14
	// concatenated chars; declared length 13 keeps the first 13.
	if alias != "ENGINE-12 UNI" {
		t.Errorf("alias = %q, want ENGINE-12 UNI (13 declared bytes)", alias)
	}
}

func TestStandardTalkerAliasBufZeroLengthUsesAllAvailable(t *testing.T) {
	// Some implementations leave the HEADER's length byte at 0 and
	// expect the receiver to strip trailing zeros itself. The
	// assembler keeps all 19 bytes when length is 0, then cleanAlias
	// drops the non-printable padding.
	b := NewStandardTalkerAliasBuf(nil)
	b.AddFragment(LCOTalkerAliasHeader, makeHeader(0, "FIRE\x00"))
	b.AddFragment(LCOTalkerAliasBlock1, makeBlock(LCOTalkerAliasBlock1, "\x00\x00\x00\x00\x00\x00\x00"))
	alias, ok := b.AddFragment(LCOTalkerAliasBlock2, makeBlock(LCOTalkerAliasBlock2, "\x00\x00\x00\x00\x00\x00\x00"))
	if !ok {
		t.Fatal("BLOCK2 did not complete the alias")
	}
	if alias != "FIRE" {
		t.Errorf("alias = %q, want FIRE (length=0 means strip non-printable)", alias)
	}
}

func TestStandardTalkerAliasBufNoEmitWithoutHeader(t *testing.T) {
	b := NewStandardTalkerAliasBuf(nil)
	if _, ok := b.AddFragment(LCOTalkerAliasBlock1, makeBlock(LCOTalkerAliasBlock1, "OUTOFORD")); ok {
		t.Error("BLOCK1 alone should not emit")
	}
	if _, ok := b.AddFragment(LCOTalkerAliasBlock2, makeBlock(LCOTalkerAliasBlock2, "ER")); ok {
		t.Error("BLOCK1+BLOCK2 without HEADER should not emit")
	}
}

func TestStandardTalkerAliasBufNoDoubleEmit(t *testing.T) {
	// The voice channel repeats the same alias sequence throughout
	// the call; the assembler should suppress duplicate emissions.
	b := NewStandardTalkerAliasBuf(nil)
	b.AddFragment(LCOTalkerAliasHeader, makeHeader(7, "UNIT-"))
	b.AddFragment(LCOTalkerAliasBlock1, makeBlock(LCOTalkerAliasBlock1, "12     "))
	first, ok := b.AddFragment(LCOTalkerAliasBlock2, makeBlock(LCOTalkerAliasBlock2, "       "))
	if !ok || first != "UNIT-12" {
		t.Fatalf("first emission = %q, ok=%v, want UNIT-12", first, ok)
	}
	// Replay the same sequence — the second pass must not re-emit.
	b.AddFragment(LCOTalkerAliasHeader, makeHeader(7, "UNIT-"))
	b.AddFragment(LCOTalkerAliasBlock1, makeBlock(LCOTalkerAliasBlock1, "12     "))
	if alias, ok := b.AddFragment(LCOTalkerAliasBlock2, makeBlock(LCOTalkerAliasBlock2, "       ")); ok {
		t.Errorf("re-emitted same alias %q", alias)
	}
}

func TestStandardTalkerAliasBufNewHeaderResetsBlocks(t *testing.T) {
	// A repeated HEADER means a new alias is being sent — drop any
	// in-flight BLOCK state so a partial reassembly doesn't blend
	// with the new alias.
	b := NewStandardTalkerAliasBuf(nil)
	b.AddFragment(LCOTalkerAliasHeader, makeHeader(10, "OLDAL"))
	b.AddFragment(LCOTalkerAliasBlock1, makeBlock(LCOTalkerAliasBlock1, "IAS    "))
	// New HEADER arrives before BLOCK2 — old BLOCK1 must be cleared.
	b.AddFragment(LCOTalkerAliasHeader, makeHeader(8, "NEWAL"))
	if alias, ok := b.AddFragment(LCOTalkerAliasBlock1, makeBlock(LCOTalkerAliasBlock1, "IAS    ")); ok {
		t.Errorf("emitted %q before BLOCK2 of new alias", alias)
	}
	alias, ok := b.AddFragment(LCOTalkerAliasBlock2, makeBlock(LCOTalkerAliasBlock2, "       "))
	if !ok || alias != "NEWALIAS" {
		t.Errorf("new alias = %q (ok=%v), want NEWALIAS", alias, ok)
	}
}

func TestStandardTalkerAliasBufStaleEviction(t *testing.T) {
	// A long pause between fragments evicts the partial reassembly
	// so a stray BLOCK2 from a previous alias doesn't accidentally
	// complete it after the gap.
	clk := time.Now()
	b := NewStandardTalkerAliasBuf(func() time.Time { return clk })
	b.AddFragment(LCOTalkerAliasHeader, makeHeader(13, "ENGIN"))
	b.AddFragment(LCOTalkerAliasBlock1, makeBlock(LCOTalkerAliasBlock1, "E-12 UN"))
	clk = clk.Add(10 * time.Second) // far past staleness window
	if alias, ok := b.AddFragment(LCOTalkerAliasBlock2, makeBlock(LCOTalkerAliasBlock2, "IT")); ok {
		t.Errorf("emitted %q after stale eviction", alias)
	}
}

func TestStandardTalkerAliasBufResetClearsEmitted(t *testing.T) {
	// Reset is what the voice composer calls when a new call starts
	// on the same device — the previous call's emitted-hash must
	// not block the new call from emitting the same alias.
	b := NewStandardTalkerAliasBuf(nil)
	b.AddFragment(LCOTalkerAliasHeader, makeHeader(5, "ENGIN"))
	b.AddFragment(LCOTalkerAliasBlock1, makeBlock(LCOTalkerAliasBlock1, "       "))
	first, _ := b.AddFragment(LCOTalkerAliasBlock2, makeBlock(LCOTalkerAliasBlock2, "       "))
	if first != "ENGIN" {
		t.Fatalf("setup: first emission %q, want ENGIN", first)
	}
	b.Reset()
	b.AddFragment(LCOTalkerAliasHeader, makeHeader(5, "ENGIN"))
	b.AddFragment(LCOTalkerAliasBlock1, makeBlock(LCOTalkerAliasBlock1, "       "))
	second, ok := b.AddFragment(LCOTalkerAliasBlock2, makeBlock(LCOTalkerAliasBlock2, "       "))
	if !ok || second != "ENGIN" {
		t.Errorf("after Reset, emitted %q (ok=%v), want ENGIN", second, ok)
	}
}

func TestStandardTalkerAliasBufIgnoresUnrelatedLCO(t *testing.T) {
	b := NewStandardTalkerAliasBuf(nil)
	if _, ok := b.AddFragment(LCOGroupVoiceChannelUser, [lcContentOctets]byte{}); ok {
		t.Error("non-alias LCO must not emit")
	}
}
