package phase1

import (
	"errors"
	"testing"
)

func TestIdentifierUpdateRoundTrip700MHz(t *testing.T) {
	// Realistic 700/800 MHz Phase 1 fixture: ChannelID 1, base 851.0
	// MHz, 12.5 kHz spacing + bandwidth, -39 MHz transmit offset.
	in := IdentifierUpdate{
		ChannelID:   1,
		BandwidthHz: 12_500,
		SpacingHz:   12_500,
		TxOffsetHz:  -39_000_000,
		BaseHz:      851_000_000,
	}
	out := ParseIdentifierUpdate(AssembleIdentifierUpdate(in))
	if out != in {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

func TestIdentifierUpdateRoundTripPositiveOffset(t *testing.T) {
	// VHF-style fixture with a positive offset to exercise the sign
	// bit. Spacing is 6.25 kHz; bandwidth differs from spacing.
	in := IdentifierUpdate{
		ChannelID:   5,
		BandwidthHz: 6_250,
		SpacingHz:   6_250,
		TxOffsetHz:  5_000_000,
		BaseHz:      154_000_000,
	}
	out := ParseIdentifierUpdate(AssembleIdentifierUpdate(in))
	if out != in {
		t.Errorf("round-trip mismatch: got %+v want %+v", out, in)
	}
}

func TestIdentifierUpdateVUHFRoundTripNegativeOffset(t *testing.T) {
	// Realistic UHF P25 site fixture: ChannelID 1, base 460.0 MHz,
	// 12.5 kHz spacing + bandwidth, -5 MHz transmit offset.
	in := IdentifierUpdate{
		ChannelID:   1,
		BandwidthHz: 12_500,
		SpacingHz:   12_500,
		TxOffsetHz:  -5_000_000,
		BaseHz:      460_000_000,
	}
	out := ParseIdentifierUpdateVUHF(AssembleIdentifierUpdateVUHF(in))
	if out != in {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

func TestIdentifierUpdateVUHFRoundTripPositiveOffset(t *testing.T) {
	// 6.25 kHz narrowband VHF fixture with a positive offset to exercise
	// the sign bit.
	in := IdentifierUpdate{
		ChannelID:   3,
		BandwidthHz: 6_250,
		SpacingHz:   6_250,
		TxOffsetHz:  5_000_000,
		BaseHz:      154_000_000,
	}
	out := ParseIdentifierUpdateVUHF(AssembleIdentifierUpdateVUHF(in))
	if out != in {
		t.Errorf("round-trip mismatch: got %+v want %+v", out, in)
	}
}

func TestIdentifierUpdateVUHFUnknownBandwidthCodeMapsToZero(t *testing.T) {
	// On-air payload with BW code 0 (reserved per TIA-102.AABF Table 16).
	// Parser must surface BandwidthHz=0 rather than fabricating a value.
	var p [8]byte
	p[0] = 0x10 // ChannelID=1, BW code 0
	p[3] = 0x64 // STEP=100 → 12.5 kHz
	// FREQ=92_000_000 → 460 MHz at ×5 Hz
	freq5 := uint32(460_000_000 / 5)
	p[4] = byte(freq5 >> 24)
	p[5] = byte(freq5 >> 16)
	p[6] = byte(freq5 >> 8)
	p[7] = byte(freq5)

	out := ParseIdentifierUpdateVUHF(p)
	if out.BandwidthHz != 0 {
		t.Errorf("BandwidthHz = %d, want 0 for reserved BW code", out.BandwidthHz)
	}
	if out.SpacingHz != 12_500 {
		t.Errorf("SpacingHz = %d, want 12_500", out.SpacingHz)
	}
	if out.BaseHz != 460_000_000 {
		t.Errorf("BaseHz = %d, want 460_000_000", out.BaseHz)
	}
}

func TestBandPlanResolvesKnownChannel(t *testing.T) {
	bp := &BandPlan{}
	bp.Apply(IdentifierUpdate{
		ChannelID: 1,
		SpacingHz: 12_500,
		BaseHz:    851_000_000,
	})

	// Channel 0 → base. Channel 16 → base + 16 × 12.5 kHz = 851.2 MHz.
	if hz, err := bp.Frequency(1, 0); err != nil || hz != 851_000_000 {
		t.Errorf("Frequency(1,0) = %d, %v; want 851_000_000, nil", hz, err)
	}
	if hz, err := bp.Frequency(1, 16); err != nil || hz != 851_200_000 {
		t.Errorf("Frequency(1,16) = %d, %v; want 851_200_000, nil", hz, err)
	}
}

func TestBandPlanUnknownChannelID(t *testing.T) {
	bp := &BandPlan{}
	_, err := bp.Frequency(7, 0)
	if !errors.Is(err, ErrUnknownChannelID) {
		t.Errorf("Frequency on empty plan: err = %v, want ErrUnknownChannelID", err)
	}
	if bp.Known(7) {
		t.Error("Known(7) on empty plan should be false")
	}
}

func TestBandPlanReplacesSlotOnNewIdentifierUpdate(t *testing.T) {
	bp := &BandPlan{}
	bp.Apply(IdentifierUpdate{ChannelID: 2, SpacingHz: 12_500, BaseHz: 851_000_000})
	bp.Apply(IdentifierUpdate{ChannelID: 2, SpacingHz: 25_000, BaseHz: 852_000_000})

	hz, err := bp.Frequency(2, 4)
	if err != nil {
		t.Fatalf("Frequency: %v", err)
	}
	if hz != 852_100_000 {
		t.Errorf("hz = %d, want 852_100_000 (uses replaced slot)", hz)
	}
}

func TestBandPlanRejectsOverflow(t *testing.T) {
	bp := &BandPlan{}
	bp.Apply(IdentifierUpdate{ChannelID: 0, SpacingHz: 1_000_000, BaseHz: 4_000_000_000})
	if _, err := bp.Frequency(0, 1000); err == nil {
		t.Error("expected overflow error for >4.29 GHz resolved frequency")
	}
}
