package phase2

// Synthesized-fixture encoders for the P25 Phase 2 superframe layer.
// These are the inverse of SuperframeDecoder / the ISCH + voice
// decoders and exist so unit and integration tests can build a
// well-formed dibit stream without a real over-the-air capture. They
// panic on misuse — they are test scaffolding, not a production
// transmit path (GopherTrunk is receive-only).

// IdleSubframe returns a DibitsPerSubframe-long all-zero sub-frame body
// for building synthesized superframe fixtures.
func IdleSubframe() []uint8 {
	return make([]uint8, DibitsPerSubframe)
}

// EncodeSuperframe concatenates 12 sub-frame dibit bodies into one
// DibitsPerSuperframe-long superframe stream and injects the 20-dibit
// outbound sync word at the head of sub-frame SyncSubframeIndex so a
// SuperframeDecoder can anchor on it. Each body must be exactly
// DibitsPerSubframe dibits; EncodeSuperframe panics otherwise.
func EncodeSuperframe(subframes [SubframesPerSuperframe][]uint8) []uint8 {
	out := make([]uint8, 0, DibitsPerSuperframe)
	for _, sub := range subframes {
		if len(sub) != DibitsPerSubframe {
			panic("p25/phase2: EncodeSuperframe sub-frame must be DibitsPerSubframe dibits")
		}
		out = append(out, sub...)
	}
	sync := OutboundSyncDibits()
	base := SyncSubframeIndex * DibitsPerSubframe
	copy(out[base:base+SyncDibits], sync)
	return out
}
