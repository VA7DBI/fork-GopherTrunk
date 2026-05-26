// Package pocsag decodes POCSAG paging traffic — the dominant wireline
// FSK paging protocol used by fire / EMS dispatch (Knox tone-out boxes
// often forward POCSAG text to crews), commercial paging services
// (now mostly dead in NA but very alive in EU / JP / hospital paging),
// and amateur DAPNET.
//
// The implementation is split:
//
//   - codeword.go (this file): POCSAG 32-bit codeword struct, parity
//     validation, BCH(31,21) wrapping, address / message decode.
//   - batch.go: batch framing — sync codeword detection (FSC =
//     0x7CD215D8), 16-codeword batch carve-up, frame-slot index
//     resolution.
//   - message.go: numeric (5 BCD per codeword) and alphanumeric (7-bit
//     packed across codewords) message reassembly.
//
// The DSP wiring (FM demod → bit slicer → sync detector) lives in a
// follow-up; the package shape here lets a caller hand in a bit
// stream and get pages back. That keeps the protocol layer testable
// without IQ fixtures.
package pocsag

import (
	"github.com/MattCheramie/GopherTrunk/internal/radio/framing"
)

// WordType distinguishes the two codeword shapes POCSAG defines.
type WordType uint8

const (
	WordTypeAddress WordType = iota
	WordTypeMessage
	WordTypeIdle // 0x7A89C197 — filler in unused frame slots
	WordTypeSync // 0x7CD215D8 — batch boundary marker
)

// Function is the 2-bit "function code" sent in an address codeword.
// Pagers traditionally map A/B/C/D to tone, beep + numeric, beep +
// alpha, etc. Modern fire/EMS POCSAG systems use the function code
// to distinguish dispatch types ("structure fire" vs. "MVA" vs.
// "EMS rendezvous"); the meaning is operator-specific and lives in
// the talkgroup alias DB, not here.
type Function uint8

// String returns the conventional A/B/C/D label.
func (f Function) String() string {
	if f > 3 {
		return "?"
	}
	return string([]byte{'A' + byte(f)})
}

// On-wire constants from CCIR Recommendation 584.
const (
	// SyncCodeword (Frame Synchronization Codeword) appears at the
	// start of every batch. Receivers slip until a 32-bit window
	// matches this pattern (or its bit-inverse — the wire data may
	// be polarity-inverted by the FM demod).
	SyncCodeword uint32 = 0x7CD215D8

	// IdleCodeword fills frame slots that don't carry a real address
	// or message. Receivers MUST treat this exact pattern as "skip"
	// — it is not a valid address-or-message codeword (it has the
	// address flag bit but doesn't correspond to a real RIC).
	IdleCodeword uint32 = 0x7A89C197

	// CodewordBits is the on-wire size of one POCSAG codeword.
	CodewordBits = 32

	// BatchCodewords is how many non-sync codewords follow each FSC.
	BatchCodewords = 16

	// FrameCodewords is how many codewords are in each "frame slot"
	// — POCSAG batches are 8 frames × 2 codewords.
	FrameCodewords = 2

	// FramesPerBatch is the number of frame slots in one batch.
	FramesPerBatch = 8

	// BatchBits is the on-wire size of one POCSAG batch (sync + 16
	// codewords).
	BatchBits = CodewordBits * (1 + BatchCodewords)
)

// Codeword wraps one 32-bit POCSAG codeword. After Decode the Type
// distinguishes address / message / idle / sync; CorrectedErrors
// reports how many bit errors BCH had to correct (0 = clean,
// 1-2 = corrected, -1 = uncorrectable: the codeword arrived too
// damaged to trust).
type Codeword struct {
	// Raw is the 32-bit codeword as received from the wire (after
	// any polarity inversion). bit 31 = flag (MSB on the wire).
	Raw uint32

	// Type is the decoded codeword shape. WordTypeAddress and
	// WordTypeMessage are the only ones that carry data.
	Type WordType

	// Address is the 18-bit RIC bits (positions 30..13 of the raw
	// codeword). Combined with the frame-slot index (0..7) by the
	// batch layer to produce the full 21-bit RIC the paging
	// network uses.
	Address uint32

	// Func is the 2-bit function code (positions 12..11 of an
	// address codeword).
	Func Function

	// MessageBits is the 20-bit message field (positions 30..11 of
	// a message codeword), MSB-first within the uint32's low 20
	// bits.
	MessageBits uint32

	// CorrectedErrors is the BCH(31,21) error count (0..2) or -1
	// when the codeword is uncorrectable. Callers should treat -1
	// as "drop this codeword."
	CorrectedErrors int

	// ParityOK is true when the trailing overall-even-parity bit
	// matches the bits above it. False indicates a single bit flip
	// in the parity position; BCH cannot tell where but the
	// codeword is degraded.
	ParityOK bool
}

// Decode parses one 32-bit POCSAG codeword, running BCH(31,21)
// correction over the 31 non-parity bits and validating the
// trailing overall-parity bit. The returned Codeword's Type
// reports what was decoded.
func Decode(raw uint32) Codeword {
	cw := Codeword{Raw: raw}

	if raw == SyncCodeword {
		cw.Type = WordTypeSync
		cw.ParityOK = true
		return cw
	}
	if raw == IdleCodeword {
		cw.Type = WordTypeIdle
		cw.ParityOK = true
		return cw
	}

	// Strip the trailing parity bit; BCH runs over the high 31
	// bits.
	body31 := raw >> 1
	corrected, errs := framing.BCHDecode31_21(body31)
	cw.CorrectedErrors = errs

	// Re-encode to get the clean 31-bit codeword for the parity
	// re-check (we want parity over what the air *should* have
	// looked like, not over the noisy received bits).
	clean := framing.BCHEncode31_21(corrected)
	expectParity := framing.BCH3121ParityBit(clean)
	cw.ParityOK = expectParity == byte(raw&1)

	// `corrected` carries the 21 info bits in its low bits. The
	// MSB of those 21 bits is the address/message flag.
	flag := (corrected >> 20) & 1
	if flag == 0 {
		cw.Type = WordTypeAddress
		// info layout (21 bits): flag=0 | 18-bit address | 2-bit function
		cw.Address = (corrected >> 2) & 0x3FFFF
		cw.Func = Function(corrected & 0x3)
	} else {
		cw.Type = WordTypeMessage
		// info layout (21 bits): flag=1 | 20-bit message field
		cw.MessageBits = corrected & 0xFFFFF
	}
	return cw
}

// Encode builds a 32-bit POCSAG codeword from the supplied fields.
// flag must be 0 (address) or 1 (message). info is the 20-bit
// payload (18-bit address + 2-bit function for address codewords;
// 20-bit message field for message codewords). Used by the tests
// to round-trip synthetic codewords.
func Encode(flag uint8, info20 uint32) uint32 {
	infoFull := (uint32(flag&1) << 20) | (info20 & 0xFFFFF)
	cw31 := framing.BCHEncode31_21(infoFull)
	parity := uint32(framing.BCH3121ParityBit(cw31))
	return (cw31 << 1) | parity
}

// EncodeAddress is the canonical builder for an address codeword.
// address18 carries the 18-bit address portion (the RIC with its
// bottom 3 frame-slot bits already stripped). fn is the 2-bit
// function code.
func EncodeAddress(address18 uint32, fn Function) uint32 {
	info20 := ((address18 & 0x3FFFF) << 2) | uint32(fn&0x3)
	return Encode(0, info20)
}

// EncodeMessage is the canonical builder for a message codeword.
// payload20 is the 20-bit message field — the next 20 bits of the
// numeric / alphanumeric stream.
func EncodeMessage(payload20 uint32) uint32 {
	return Encode(1, payload20&0xFFFFF)
}
