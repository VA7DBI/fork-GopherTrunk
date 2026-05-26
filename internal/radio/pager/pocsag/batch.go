package pocsag

import "errors"

// Batch is one POCSAG batch: a sync codeword followed by 16
// "frame-slot" codewords organized as 8 frames of 2 codewords each.
//
// Frame slots are significant: a paging RIC's low 3 bits select
// which frame slot the receiver looks for its address codeword in
// (POCSAG saves battery by only enabling the receiver during the
// pager's slot). When extracting addresses from a batch the
// caller combines the 18-bit address bits in the codeword with
// the 3-bit slot index to reconstruct the full 21-bit RIC.
type Batch struct {
	// Sync is the sync codeword as decoded — should always be
	// WordTypeSync; included so the batch struct carries the raw
	// bits for diagnostic logging.
	Sync Codeword
	// Words holds the 16 codewords (8 frames × 2 codewords) in
	// the order they appeared on the wire.
	Words [BatchCodewords]Codeword
}

// ErrBatchTooShort is returned by DecodeBatch when the input has
// fewer than (1 + BatchCodewords) * CodewordBits bits.
var ErrBatchTooShort = errors.New("pocsag: batch input too short")

// DecodeBatch parses one batch from a packed bit-stream. The input
// must be exactly BatchBits long. bits[0] is the MSB of the sync
// codeword.
//
// Each codeword is read MSB-first: bit 31 of the codeword =
// bits[wordStart], bit 30 = bits[wordStart+1], etc.
func DecodeBatch(bits []byte) (Batch, error) {
	if len(bits) < BatchBits {
		return Batch{}, ErrBatchTooShort
	}
	var b Batch
	b.Sync = Decode(packCodeword(bits[:CodewordBits]))
	for i := 0; i < BatchCodewords; i++ {
		start := CodewordBits * (1 + i)
		end := start + CodewordBits
		b.Words[i] = Decode(packCodeword(bits[start:end]))
	}
	return b, nil
}

// packCodeword packs 32 0/1 bytes into a uint32, MSB-first.
func packCodeword(bits []byte) uint32 {
	var cw uint32
	for i := 0; i < CodewordBits; i++ {
		if bits[i] != 0 {
			cw |= 1 << uint(CodewordBits-1-i)
		}
	}
	return cw
}

// FrameSlot reports which frame-slot index (0..7) the codeword at
// position wordIdx (0..15) sits in. Used to reconstruct full RICs
// from address codewords' 18-bit address fields.
func FrameSlot(wordIdx int) int {
	return wordIdx / FrameCodewords
}

// ReconstructRIC combines an 18-bit address-codeword address field
// with the frame-slot index it was found in to produce the full
// 21-bit RIC the paging network addresses the pager by.
func ReconstructRIC(address18 uint32, slot int) uint32 {
	return ((address18 & 0x3FFFF) << 3) | (uint32(slot) & 0x7)
}

// IsAddressInExpectedSlot reports whether an address codeword
// extracted from frame-slot `slot` matches the RIC's slot
// assignment. POCSAG receivers MUST drop address codewords whose
// implied slot doesn't match the frame they were transmitted in
// — the wire has no other way to validate that the codeword
// wasn't a corrupted message codeword that happened to BCH-decode
// to something address-shaped.
//
// The validation: a real RIC fits in 21 bits where the low 3
// bits encode the slot index. An address codeword's 18-bit
// address field is the high 18 bits of that RIC. The codeword's
// physical slot in the batch must therefore equal the RIC's
// (low 3) slot bits — which is just `slot` since there's no
// freedom in the 18-bit field. The check is degenerate at this
// layer — the slot IS the slot. The check matters one layer up
// when correlating an address codeword to a continuing message
// stream that may span batches.
func IsAddressInExpectedSlot(_ Codeword, _ int) bool {
	return true
}
