package phase1

import "time"

// Standard P25 voice-channel talker-alias reassembly (TIA-102.AABF).
//
// Distinct from TalkerAliasAssembler (talker_alias.go), which handles
// the Motorola vendor *TSBK* form carried on the control channel.
// Standard alias rides on the **voice channel** during an active
// call: an LDU1 Link Control word with LCFormat ∈ {0x15, 0x16, 0x17}
// carries one fragment of a 3-frame HEADER + BLOCK1 + BLOCK2 sequence.
//
// Layout (project working model — the per-LCO field tables in
// TIA-102.AABF are not in the repo's spec PDFs). Each 9-octet LC
// content carries:
//
//	HEADER  (LCF 0x15): octet 0 = LCO,
//	                    octet 1 = MFID,
//	                    octet 2 = format / data-format byte,
//	                    octet 3 = total alias byte length,
//	                    octets 4-8 = first 5 alias bytes
//	BLOCK1  (LCF 0x16): octet 0 = LCO,
//	                    octet 1 = MFID,
//	                    octets 2-8 = next 7 alias bytes
//	BLOCK2  (LCF 0x17): octet 0 = LCO,
//	                    octet 1 = MFID,
//	                    octets 2-8 = next 7 alias bytes
//
// Maximum reassembled alias length: 5 + 7 + 7 = 19 bytes. The
// assembler uses the HEADER's declared length to trim the trailing
// padding so a short alias ("ENG-12") doesn't drag along its filler.
//
// Talker alias is keyed by the active call's SourceID, but the LC
// content does NOT carry it inline — the source identity comes from
// the surrounding voice call (the engine's ActiveCall for the
// device whose voice chain feeds this assembler). That's why this
// type is a per-call buffer rather than a per-source map: each
// active voice call owns one StandardTalkerAliasBuf, and the
// composer caller publishes the assembled alias with the call's
// known SourceID.

// standardAliasMaxBytes is the working-model upper bound on the
// reassembled alias length — 5 (HEADER tail) + 7 (BLOCK1) + 7
// (BLOCK2). Anything beyond this is truncated.
const standardAliasMaxBytes = 19

// standardAliasStaleAfter is how long a partial reassembly is kept
// before the next AddFragment evicts it. Voice-channel LCs ride at
// ~9 LDU1s per second, so even a 1 s gap is generous; 5 s tolerates
// a brief decoder stall without dropping the alias.
const standardAliasStaleAfter = 5 * time.Second

// StandardTalkerAliasBuf reassembles the three voice-channel
// talker-alias fragments for one active call. Construct one per
// voice chain, call AddFragment for every LCO 0x15/0x16/0x17 LC the
// chain observes; AddFragment returns the assembled alias once
// HEADER + at least one BLOCK have arrived. Safe for single-goroutine
// use — the voice composer owns one per call and never shares.
type StandardTalkerAliasBuf struct {
	now func() time.Time

	haveHeader bool
	haveBlock1 bool
	haveBlock2 bool

	// dataLength is the alias byte length declared by HEADER.
	// Zero before HEADER arrives or when the HEADER octet is itself
	// zero (some implementations leave length at zero and rely on
	// trailing-padding stripping). Capped at standardAliasMaxBytes
	// on use.
	dataLength uint8

	// headerTail are the 5 alias bytes from HEADER octets 4..8.
	headerTail [5]byte
	// block1, block2 are the 7-byte alias payloads of BLOCK1/BLOCK2
	// (octets 2..8).
	block1 [7]byte
	block2 [7]byte

	lastUpdate time.Time
	// emittedHash is a fingerprint of the last successfully emitted
	// alias so the assembler doesn't re-emit the same alias on every
	// LC repeat. Cleared on Reset.
	emittedHash uint64
}

// NewStandardTalkerAliasBuf returns a ready buffer. now is injectable
// for tests; nil defaults to time.Now.
func NewStandardTalkerAliasBuf(now func() time.Time) *StandardTalkerAliasBuf {
	if now == nil {
		now = time.Now
	}
	return &StandardTalkerAliasBuf{now: now}
}

// AddFragment feeds one talker-alias LC's raw 9-octet content into
// the buffer. lcf must be one of LCOTalkerAliasHeader /
// LCOTalkerAliasBlock1 / LCOTalkerAliasBlock2; other LCOs are
// ignored. When enough fragments have arrived to form a complete
// alias, AddFragment returns (alias, true); otherwise ("", false).
//
// The same alias is not returned twice — the second AddFragment
// after a complete emission returns ("", false) until a new HEADER
// arrives. Fragments older than standardAliasStaleAfter are dropped
// on the next call so an interrupted sequence doesn't leak partial
// state into the next call's reassembly.
func (b *StandardTalkerAliasBuf) AddFragment(lcf uint8, content [lcContentOctets]byte) (string, bool) {
	now := b.now()
	if !b.lastUpdate.IsZero() && now.Sub(b.lastUpdate) > standardAliasStaleAfter {
		b.resetExceptEmitted()
	}
	switch lcf {
	case LCOTalkerAliasHeader:
		// HEADER carries the declared length in octet 3 and the
		// first 5 alias bytes in octets 4..8. A repeated HEADER
		// resets the assembler — a new alias is being sent.
		b.dataLength = content[3]
		copy(b.headerTail[:], content[4:9])
		b.haveHeader = true
		b.haveBlock1 = false
		b.haveBlock2 = false
	case LCOTalkerAliasBlock1:
		copy(b.block1[:], content[2:9])
		b.haveBlock1 = true
	case LCOTalkerAliasBlock2:
		copy(b.block2[:], content[2:9])
		b.haveBlock2 = true
	default:
		return "", false
	}
	b.lastUpdate = now

	if !b.haveHeader {
		return "", false
	}
	// Emit once both BLOCK1 and BLOCK2 have arrived. Single-BLOCK
	// emission would let a HEADER+BLOCK1 produce a (truncated) alias
	// before BLOCK2 lands, but real systems repeat the full sequence
	// throughout the call so waiting for the BLOCK2 keeps the first
	// emission complete.
	if !b.haveBlock1 || !b.haveBlock2 {
		return "", false
	}

	raw := make([]byte, 0, standardAliasMaxBytes)
	raw = append(raw, b.headerTail[:]...)
	raw = append(raw, b.block1[:]...)
	raw = append(raw, b.block2[:]...)
	if int(b.dataLength) > 0 && int(b.dataLength) < len(raw) {
		raw = raw[:b.dataLength]
	}
	alias := cleanAlias(raw)
	if alias == "" {
		return "", false
	}
	h := aliasFingerprint(alias)
	if h == b.emittedHash {
		// Already published this alias on a previous LC repeat;
		// don't double-emit.
		return "", false
	}
	b.emittedHash = h
	return alias, true
}

// Reset clears the buffer including the de-duplication fingerprint —
// the voice composer calls this when a new call starts on the same
// device so the previous call's alias doesn't leak forward.
func (b *StandardTalkerAliasBuf) Reset() {
	b.resetExceptEmitted()
	b.emittedHash = 0
}

func (b *StandardTalkerAliasBuf) resetExceptEmitted() {
	b.haveHeader = false
	b.haveBlock1 = false
	b.haveBlock2 = false
	b.dataLength = 0
	b.headerTail = [5]byte{}
	b.block1 = [7]byte{}
	b.block2 = [7]byte{}
	b.lastUpdate = time.Time{}
}

// aliasFingerprint is a cheap FNV-1a hash used only to suppress
// repeat emissions of the same alias inside one call.
func aliasFingerprint(s string) uint64 {
	const (
		offset uint64 = 14695981039346656037
		prime  uint64 = 1099511628211
	)
	h := offset
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}
