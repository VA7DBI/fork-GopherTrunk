package framing

import (
	"math/rand"
	"testing"
)

func TestBCHEDACSRoundTripCleanCodeword(t *testing.T) {
	infos := []uint32{
		0,
		1,
		0x0123456,
		0x0FFFFFF,
		0x0AAAAAA,
		0x0555555,
		0x0DEADBE, // 28-bit truncated
	}
	for _, info := range infos {
		want := info & bchEDACSInfoMask
		cw := BCHEncodeEDACS(info)
		got, errs := BCHDecodeEDACS(cw)
		if errs != 0 {
			t.Errorf("info=%#x: errs=%d, want 0", info, errs)
		}
		if got != want {
			t.Errorf("info=%#x: decoded=%#x, want=%#x", info, got, want)
		}
	}
}

func TestBCHEDACSCorrectsAnySingleBitError(t *testing.T) {
	const info = uint32(0x0CAFEBA)
	want := info & bchEDACSInfoMask
	cw := BCHEncodeEDACS(info)
	for pos := 0; pos < bchEDACSCodewordBits; pos++ {
		corrupted := cw ^ (uint64(1) << uint(pos))
		got, errs := BCHDecodeEDACS(corrupted)
		if errs != 1 {
			t.Errorf("pos=%d: errs=%d, want 1", pos, errs)
			continue
		}
		if got != want {
			t.Errorf("pos=%d: decoded info=%#x, want %#x", pos, got, want)
		}
	}
}

func TestBCHEDACSCorrectsAnyDoubleBitError(t *testing.T) {
	const info = uint32(0x0123456)
	want := info & bchEDACSInfoMask
	cw := BCHEncodeEDACS(info)
	for i := 0; i < bchEDACSCodewordBits-1; i++ {
		for j := i + 1; j < bchEDACSCodewordBits; j++ {
			corrupted := cw ^ (uint64(1) << uint(i)) ^ (uint64(1) << uint(j))
			got, errs := BCHDecodeEDACS(corrupted)
			if errs != 2 {
				t.Errorf("pos=(%d,%d): errs=%d, want 2", i, j, errs)
				continue
			}
			if got != want {
				t.Errorf("pos=(%d,%d): decoded info=%#x, want %#x", i, j, got, want)
			}
		}
	}
}

func TestBCHEDACSRejectsTripleBitErrors(t *testing.T) {
	const info = uint32(0x0ABCDEF)
	want := info & bchEDACSInfoMask
	cw := BCHEncodeEDACS(info)
	// BCH(40,28,2) corrects t=2 errors; triple-bit patterns
	// should mostly be rejected (errs == -1) or mis-corrected
	// to a wrong info — never silently accepted as clean.
	wrongCount := 0
	total := 0
	for i := 0; i < bchEDACSCodewordBits-2; i++ {
		for j := i + 1; j < bchEDACSCodewordBits-1; j++ {
			for k := j + 1; k < bchEDACSCodewordBits; k++ {
				corrupted := cw ^ (uint64(1) << uint(i)) ^ (uint64(1) << uint(j)) ^ (uint64(1) << uint(k))
				got, errs := BCHDecodeEDACS(corrupted)
				total++
				if errs == 0 && got == want {
					t.Errorf("triple-error (%d, %d, %d) silently passed", i, j, k)
				}
				if errs == -1 || got != want {
					wrongCount++
				}
			}
		}
	}
	// Almost all triple-bit errors should be detected or
	// mis-corrected to a wrong info. A small fraction map to
	// syndromically-equivalent ≤2-bit patterns; that's a known
	// property of BCH(40, 28, 2).
	if float64(wrongCount)/float64(total) < 0.95 {
		t.Errorf("only %d/%d triple errors detected/mis-corrected (< 95%%)", wrongCount, total)
	}
}

func TestBCHEDACSRandomRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(0xED4C5))
	for trial := 0; trial < 1024; trial++ {
		info := r.Uint32() & bchEDACSInfoMask
		cw := BCHEncodeEDACS(info)
		got, errs := BCHDecodeEDACS(cw)
		if errs != 0 || got != info {
			t.Errorf("trial %d: info=%#x decoded=(%#x, %d)", trial, info, got, errs)
		}
	}
}

func TestBCHEDACSSyndromeTableSanity(t *testing.T) {
	// syndromes[0] = x^0 mod g = 1.
	if bchEDACSSyndromes[0] != 1 {
		t.Errorf("syndromes[0] = %#x, want 1", bchEDACSSyndromes[0])
	}
	// syndromes[i] = 1 << i for i < 12 (since x^i has degree < 12).
	for i := 0; i < bchEDACSParityBits; i++ {
		want := uint16(1) << uint(i)
		if bchEDACSSyndromes[i] != want {
			t.Errorf("syndromes[%d] = %#x, want %#x", i, bchEDACSSyndromes[i], want)
		}
	}
	// syndromes[12] = x^12 mod g = g(x) - x^12 = low 12 bits of generator.
	want12 := bchEDACSGenerator & bchEDACSParityMask
	if bchEDACSSyndromes[12] != want12 {
		t.Errorf("syndromes[12] = %#x, want %#x", bchEDACSSyndromes[12], want12)
	}
	// All 40 entries must be unique within the table (a
	// requirement for single-bit-error correction).
	seen := map[uint16]int{}
	for i, s := range bchEDACSSyndromes {
		if prev, dup := seen[s]; dup {
			t.Errorf("duplicate syndrome %#x at positions %d and %d", s, prev, i)
		}
		seen[s] = i
	}
	// All entries must fit in 12 bits.
	for i, s := range bchEDACSSyndromes {
		if s>>bchEDACSParityBits != 0 {
			t.Errorf("syndrome[%d] = %#x exceeds 12 bits", i, s)
		}
	}
}

func TestBCHEDACSEncodedCodewordPassesItsOwnSyndrome(t *testing.T) {
	r := rand.New(rand.NewSource(0xC0DE))
	for trial := 0; trial < 64; trial++ {
		info := r.Uint32() & bchEDACSInfoMask
		cw := BCHEncodeEDACS(info)
		if s := computeBCHEDACSSyndrome(cw); s != 0 {
			t.Errorf("trial %d: encoded codeword %#x has nonzero syndrome %#x", trial, cw, s)
		}
	}
}
