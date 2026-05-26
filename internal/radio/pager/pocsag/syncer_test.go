package pocsag

import (
	"testing"
)

// feedCodewords pushes the 32-bit codewords' bits through the syncer
// MSB-first and returns any pages emitted.
func feedCodewords(t *testing.T, s *Syncer, cws []uint32) []Page {
	t.Helper()
	var out []Page
	for _, cw := range cws {
		for i := 0; i < CodewordBits; i++ {
			bit := byte((cw >> uint(CodewordBits-1-i)) & 1)
			pages := s.Push(bit)
			out = append(out, pages...)
		}
	}
	return out
}

func TestSyncerLocksOnSyncCodeword(t *testing.T) {
	s := NewSyncer()
	if s.Locked() {
		t.Fatal("syncer locked before any bits fed")
	}
	for i := 0; i < CodewordBits; i++ {
		bit := byte((SyncCodeword >> uint(CodewordBits-1-i)) & 1)
		s.Push(bit)
	}
	if !s.Locked() {
		t.Error("syncer did not lock after sync codeword")
	}
}

func TestSyncerEmitsPageForAddressMessagePair(t *testing.T) {
	const (
		addr18 uint32   = 0x12345
		fn     Function = 2 // 'C' — alpha by convention
	)
	// Build a synthetic batch: sync + address at word 6 + 1 message
	// + idle to terminate + idle padding.
	addrCW := EncodeAddress(addr18, fn)
	// Pad the message with NULs so the alpha decode produces ""
	// — we're testing the syncer, not the decoder.
	msgCW := EncodeMessage(0)

	cws := []uint32{SyncCodeword}
	for i := 0; i < BatchCodewords; i++ {
		switch i {
		case 6:
			cws = append(cws, addrCW)
		case 7:
			cws = append(cws, msgCW)
		case 8:
			// Idle terminates the page.
			cws = append(cws, IdleCodeword)
		default:
			cws = append(cws, IdleCodeword)
		}
	}

	s := NewSyncer()
	pages := feedCodewords(t, s, cws)

	if len(pages) != 1 {
		t.Fatalf("emitted %d pages, want 1", len(pages))
	}
	p := pages[0]
	wantRIC := uint32((addr18 << 3) | 3) // slot 3 (word 6/2)
	if p.RIC != wantRIC {
		t.Errorf("RIC = 0x%x, want 0x%x", p.RIC, wantRIC)
	}
	if p.Func != fn {
		t.Errorf("Func = %v, want %v", p.Func, fn)
	}
	if p.Encoding != EncodingAlpha {
		t.Errorf("Encoding = %v, want EncodingAlpha (function C)", p.Encoding)
	}
}

func TestSyncerNumericEncodingForFunctionB(t *testing.T) {
	const (
		addr18 uint32   = 0x100
		fn     Function = 1 // 'B' — numeric by convention
	)
	addrCW := EncodeAddress(addr18, fn)
	// 5 digit-nibble payload "12345" with LSB-first nibble encoding.
	var payload uint32
	digits := "12345"
	for j := 0; j < 5; j++ {
		nib := encodeDigitForSyncerTest(digits[j])
		payload |= reverseNibble(uint32(nib)) << uint(16-4*j)
	}
	msgCW := EncodeMessage(payload)

	cws := []uint32{SyncCodeword}
	for i := 0; i < BatchCodewords; i++ {
		switch i {
		case 0:
			cws = append(cws, addrCW)
		case 1:
			cws = append(cws, msgCW)
		case 2:
			cws = append(cws, IdleCodeword)
		default:
			cws = append(cws, IdleCodeword)
		}
	}

	s := NewSyncer()
	pages := feedCodewords(t, s, cws)
	if len(pages) != 1 {
		t.Fatalf("emitted %d pages, want 1", len(pages))
	}
	if pages[0].Encoding != EncodingNumeric {
		t.Errorf("Encoding = %v, want EncodingNumeric (function B)", pages[0].Encoding)
	}
	if pages[0].Text != "12345" {
		t.Errorf("Text = %q, want %q", pages[0].Text, "12345")
	}
}

// encodeDigitForSyncerTest mirrors the test helper in message_test.go.
// Kept local so syncer_test.go doesn't depend on the message_test.go
// file's lexical visibility (both tests live in package pocsag).
func encodeDigitForSyncerTest(c byte) byte {
	for i, want := range numericTable {
		if want == c {
			return byte(i)
		}
	}
	return 0xC
}

func TestSyncerHandlesPolarityInverted(t *testing.T) {
	addrCW := EncodeAddress(0x100, 1)
	cws := []uint32{SyncCodeword, addrCW}
	for i := 0; i < BatchCodewords-1; i++ {
		cws = append(cws, IdleCodeword)
	}

	s := NewSyncer()
	// Feed bit-inverted stream.
	var pages []Page
	for _, cw := range cws {
		for i := 0; i < CodewordBits; i++ {
			bit := byte((cw >> uint(CodewordBits-1-i)) & 1)
			pages = append(pages, s.Push(bit^1)...)
		}
	}
	if !s.Locked() && len(pages) == 0 {
		// Locked goes false again after the batch ends; assert that
		// a page made it out instead.
	}
	if len(pages) != 1 {
		t.Fatalf("inverted-polarity feed emitted %d pages, want 1", len(pages))
	}
}

func TestSyncerFlushReleasesInProgressPage(t *testing.T) {
	addrCW := EncodeAddress(0x100, 1)
	msgCW := EncodeMessage(0)
	// Build a batch where the page isn't terminated by an idle —
	// the address + message are followed by more idles but flushPage
	// always fires on the batch boundary because flushPage is
	// triggered by the next address/idle. Validate Flush() releases
	// the page when the operator stops the stream mid-page.
	cws := []uint32{SyncCodeword, addrCW, msgCW}
	for i := 0; i < BatchCodewords-2; i++ {
		cws = append(cws, IdleCodeword)
	}
	s := NewSyncer()
	pages := feedCodewords(t, s, cws)
	// The idle codewords inside the batch will already have flushed
	// the page; Flush should return nil.
	if len(pages) != 1 {
		t.Fatalf("emitted %d pages from batch with idle terminator, want 1", len(pages))
	}
	if p := s.Flush(); p != nil {
		t.Errorf("Flush after page emission returned %+v, want nil", p)
	}

	// Now test the opposite: a page in-flight with no terminator.
	s2 := NewSyncer()
	addrOnly := []uint32{SyncCodeword, addrCW}
	for i := 0; i < BatchCodewords-1; i++ {
		addrOnly = append(addrOnly, msgCW)
	}
	pages2 := feedCodewords(t, s2, addrOnly)
	// Batch boundary doesn't flush; only an address / idle terminator
	// or operator-driven Flush does. The whole batch is message after
	// the address, with no terminator → no emission.
	if len(pages2) != 0 {
		t.Errorf("emitted %d pages without terminator, want 0 (page in-progress)", len(pages2))
	}
	if p := s2.Flush(); p == nil {
		t.Error("Flush on in-progress page returned nil")
	}
}
