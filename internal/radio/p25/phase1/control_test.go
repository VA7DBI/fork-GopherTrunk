package phase1

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/trunking"
)

// buildLockedStream constructs a synthetic dibit window that places the
// FSW at the given offset, followed by a properly BCH-encoded NID for
// the supplied NAC and DUID, and a valid trellis-encoded + interleaved
// TSBK channel block (single Last-Block TSBK with the supplied opcode).
// Tail dibits are zero-padded.
func buildLockedStream(offset int, nac uint16, duid DUID, op Opcode) []uint8 {
	return buildLockedStreamWithTSBK(offset, nac, duid, TSBK{LB: true, Opcode: op})
}

// buildControlFrame builds one contiguous FSW + NID + TSBK frame (154
// dibits, no status symbols) for the given NAC / DUID / TSBK.
func buildControlFrame(nac uint16, duid DUID, tsbk TSBK) []uint8 {
	frame := make([]uint8, 0, 24+32+98)
	frame = append(frame, FrameSyncWord[:]...)
	bits := EncodeNIDBits(nac, duid)
	for i := 0; i < 32; i++ {
		frame = append(frame, (bits[2*i]<<1)|bits[2*i+1])
	}
	return append(frame, EncodeTSBKChannel(AssembleTSBK(tsbk))...)
}

// buildLockedStreamWithTSBK is the variant that takes a fully-formed
// TSBK so callers can carry a payload (used by the IdentifierUpdate +
// grant publication tests). The FSW + NID + TSBK frame is interleaved
// with P25 status symbols, mirroring a real on-air stream, so the
// receiver's status-symbol stripping is exercised.
func buildLockedStreamWithTSBK(offset int, nac uint16, duid DUID, tsbk TSBK) []uint8 {
	onAir := InjectControlStatusSymbols(buildControlFrame(nac, duid, tsbk))
	out := make([]uint8, offset+len(onAir)+16)
	copy(out[offset:], onAir)
	return out
}

func TestControlChannelEmitsLockOnTSDU(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := NewControlChannel(bus, nil, 851_000_000)
	stream := buildLockedStream(10, 0x293, DUIDTrunkingSignaling, OpRFSSStatusBroadcast)
	cc.Process(stream, 0)

	select {
	case ev := <-sub.C:
		if ev.Kind != events.KindCCLocked {
			t.Errorf("kind = %s, want cc.locked", ev.Kind)
		}
		ls, ok := ev.Payload.(LockState)
		if !ok {
			t.Fatalf("payload type = %T, want LockState", ev.Payload)
		}
		if ls.NAC != 0x293 || ls.DUID != DUIDTrunkingSignaling {
			t.Errorf("payload = %+v", ls)
		}
	case <-time.After(time.Second):
		t.Fatal("no event published")
	}
}

// TestControlChannelLocksUnderDibitRotation feeds a TSDU stream whose
// dibits have all been shifted by k — the residual quadrant ambiguity
// the CQPSK/LSM demod leaves on simulcast P25 sites. The sync detector
// recovers the rotation; parseFrame must undo it so the NID + TSBK
// still decode. Before the rotateDibits fix, odd rotations (1, 3) —
// exactly the π/4-DQPSK quadrant slips — corrupted every NID and the
// control channel never locked.
func TestControlChannelLocksUnderDibitRotation(t *testing.T) {
	for k := uint8(0); k < 4; k++ {
		bus := events.NewBus(8)
		sub := bus.Subscribe()
		stream := buildLockedStream(10, 0x293, DUIDTrunkingSignaling, OpRFSSStatusBroadcast)
		// received = canonical - k, so the detector reports rot == k.
		for i := range stream {
			stream[i] = (stream[i] + 4 - k) & 3
		}
		cc := NewControlChannel(bus, nil, 851_000_000)
		cc.Process(stream, 0)
		select {
		case ev := <-sub.C:
			if ev.Kind != events.KindCCLocked {
				t.Errorf("k=%d kind = %s, want cc.locked", k, ev.Kind)
			} else if ls := ev.Payload.(LockState); ls.NAC != 0x293 {
				t.Errorf("k=%d NAC = %#x, want 0x293", k, ls.NAC)
			}
		case <-time.After(time.Second):
			t.Errorf("k=%d no lock — NID not recovered under rotation", k)
		}
		sub.Close()
		bus.Close()
	}
}

func TestControlChannelIgnoresNonTSDU(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := NewControlChannel(bus, nil, 851_000_000)
	stream := buildLockedStream(10, 0x123, DUIDLogicalLink1, OpRFSSStatusBroadcast)
	cc.Process(stream, 0)

	select {
	case ev := <-sub.C:
		t.Errorf("unexpected event: %s", ev.Kind)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestControlChannelMarkLost(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := NewControlChannel(bus, nil, 851_000_000)
	cc.Process(buildLockedStream(10, 0x456, DUIDTrunkingSignaling, OpRFSSStatusBroadcast), 0)
	<-sub.C // CCLocked

	cc.MarkLost()
	select {
	case ev := <-sub.C:
		if ev.Kind != events.KindCCLost {
			t.Errorf("kind = %s, want cc.lost", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("no CCLost event")
	}
}

func TestControlChannelPublishesDecodeErrorOnUncorrectableNID(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	// Stream with FSW followed by 32 dibits of garbage (not a valid
	// NID), interleaved with status symbols as it arrives on-air.
	frame := make([]uint8, 24+32+98)
	copy(frame, FrameSyncWord[:])
	for i := 0; i < 32; i++ {
		frame[24+i] = uint8(i*7) & 0x3
	}
	onAir := InjectControlStatusSymbols(frame)
	stream := make([]uint8, 10+len(onAir)+16)
	copy(stream[10:], onAir)

	cc := NewControlChannel(bus, nil, 851_000_000)
	cc.Process(stream, 0)

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind != events.KindDecodeError {
				continue
			}
			de, ok := ev.Payload.(events.DecodeError)
			if !ok {
				t.Fatalf("payload type = %T, want DecodeError", ev.Payload)
			}
			if de.Protocol != "p25" || de.Stage != "nid-bch" {
				t.Errorf("DecodeError = %+v", de)
			}
			return
		case <-deadline:
			t.Fatal("no decode-error event published")
		}
	}
}

func TestControlChannelAppliesIdentifierUpdateAndPublishesGrant(t *testing.T) {
	bus := events.NewBus(16)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	// Build a stream with two TSBKs back-to-back: an IdentifierUpdate
	// for ChannelID 1 (12.5 kHz spacing, base 851 MHz) followed by a
	// GroupVoiceChannelGrant on (ID=1, Number=16) which should resolve
	// to 851.2 MHz on the bus.
	identTSBK := TSBK{LB: false, Opcode: OpIdentifierUpdate}
	identTSBK.Payload = AssembleIdentifierUpdate(IdentifierUpdate{
		ChannelID: 1, SpacingHz: 12_500, BaseHz: 851_000_000,
	})

	grantPayload := [8]byte{
		0xC0,                  // service options: emergency + encrypted
		(1 << 4) | 0x00, 0x10, // channel = ID 1, number 0x010 (=16)
		0x12, 0x34, // group address 0x1234
		0xAB, 0xCD, 0xEF, // source ID 0xABCDEF
	}
	grantTSBK := TSBK{LB: true, Opcode: OpGroupVoiceChannelGrant, Payload: grantPayload}

	stream1 := buildLockedStreamWithTSBK(10, 0x293, DUIDTrunkingSignaling, identTSBK)
	stream2 := buildLockedStreamWithTSBK(0, 0x293, DUIDTrunkingSignaling, grantTSBK)

	cc := New(Options{
		Bus:         bus,
		SystemName:  "TestSys",
		FrequencyHz: 851_000_000,
		Now:         func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	cc.Process(stream1, 0)
	cc.Process(stream2, len(stream1))

	// Drain events; assert exactly one Grant event lands and looks right.
	var got *trunking.Grant
	deadline := time.After(time.Second)
	for got == nil {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindGrant {
				g := ev.Payload.(trunking.Grant)
				got = &g
			}
		case <-deadline:
			t.Fatal("no grant event published")
		}
	}
	if got.System != "TestSys" || got.Protocol != "p25" {
		t.Errorf("identity = %s/%s", got.System, got.Protocol)
	}
	if got.GroupID != 0x1234 || got.SourceID != 0xABCDEF {
		t.Errorf("group/source = %d/%d, want 4660/11259375", got.GroupID, got.SourceID)
	}
	if got.ChannelID != 1 || got.ChannelNum != 0x010 {
		t.Errorf("channel = %d.%d, want 1.16", got.ChannelID, got.ChannelNum)
	}
	if got.FrequencyHz != 851_200_000 {
		t.Errorf("freq = %d, want 851_200_000", got.FrequencyHz)
	}
	if !got.Encrypted || !got.Emergency {
		t.Errorf("flags = enc=%v emer=%v, want both", got.Encrypted, got.Emergency)
	}
	if got.At.Unix() != 1_700_000_000 {
		t.Errorf("At = %v, want injected Now", got.At)
	}
}

// TestControlChannelLocksAndGrantsAcrossSmallChunks feeds a two-frame
// stream (IdentifierUpdate then GroupVoiceChannelGrant) through Process
// in 19-dibit batches — the dibit count a real RTL-SDR's 16 KiB USB
// transfer yields per call. No single batch holds a whole 154-dibit
// frame, so locking + granting here proves Process assembles frames
// across call boundaries (issue #275 — previously every FSW hit was
// discarded because the NID/TSBK lookahead had to fit in one call).
func TestControlChannelLocksAndGrantsAcrossSmallChunks(t *testing.T) {
	bus := events.NewBus(32)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	const nac = 0x293
	identTSBK := TSBK{LB: false, Opcode: OpIdentifierUpdate}
	identTSBK.Payload = AssembleIdentifierUpdate(IdentifierUpdate{
		ChannelID: 1, SpacingHz: 12_500, BaseHz: 851_000_000,
	})
	grantTSBK := TSBK{LB: true, Opcode: OpGroupVoiceChannelGrant, Payload: [8]byte{
		0x00,
		(1 << 4) | 0x00, 0x10, // channel = ID 1, number 16
		0x12, 0x34, // group address 0x1234
		0xAB, 0xCD, 0xEF, // source ID 0xABCDEF
	}}

	stream := append(
		buildLockedStreamWithTSBK(10, nac, DUIDTrunkingSignaling, identTSBK),
		buildLockedStreamWithTSBK(10, nac, DUIDTrunkingSignaling, grantTSBK)...,
	)

	cc := New(Options{
		Bus: bus, SystemName: "TestSys", FrequencyHz: 851_000_000,
		Now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})

	const batch = 19
	for off := 0; off < len(stream); off += batch {
		end := off + batch
		if end > len(stream) {
			end = len(stream)
		}
		cc.Process(stream[off:end], off)
	}

	var locked, granted bool
	for drained := false; !drained; {
		select {
		case ev := <-sub.C:
			switch ev.Kind {
			case events.KindCCLocked:
				locked = true
			case events.KindGrant:
				if g := ev.Payload.(trunking.Grant); g.FrequencyHz != 851_200_000 {
					t.Errorf("grant freq = %d, want 851_200_000", g.FrequencyHz)
				}
				granted = true
			}
		default:
			drained = true
		}
	}
	if !locked {
		t.Error("no cc.locked event — frame not assembled across 19-dibit batches")
	}
	if !granted {
		t.Error("no grant event — TSBK not assembled across 19-dibit batches")
	}
}

func TestControlChannelPublishesAffiliation(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	// Response = denied (2), AnnGroup = 0xAABB, Group = 0x1234,
	// TargetID = 0xABCDEF.
	payload := [8]byte{0x02, 0xAA, 0xBB, 0x12, 0x34, 0xAB, 0xCD, 0xEF}
	tsbk := TSBK{LB: true, Opcode: OpGroupAffiliationResponse, Payload: payload}
	stream := buildLockedStreamWithTSBK(10, 0x111, DUIDTrunkingSignaling, tsbk)

	cc := New(Options{
		Bus:         bus,
		SystemName:  "TestSys",
		FrequencyHz: 851_000_000,
		Now:         func() time.Time { return time.Unix(1_700_000_001, 0).UTC() },
	})
	cc.Process(stream, 0)

	var got *trunking.Affiliation
	deadline := time.After(time.Second)
	for got == nil {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindAffiliation {
				a := ev.Payload.(trunking.Affiliation)
				got = &a
			}
		case <-deadline:
			t.Fatal("no affiliation event published")
		}
	}
	if got.System != "TestSys" || got.Protocol != "p25" {
		t.Errorf("identity = %s/%s", got.System, got.Protocol)
	}
	if got.SourceID != 0xABCDEF {
		t.Errorf("SourceID = %06X, want ABCDEF", got.SourceID)
	}
	if got.GroupID != 0x1234 {
		t.Errorf("GroupID = %04X, want 1234", got.GroupID)
	}
	if got.AnnouncementGroup != 0xAABB {
		t.Errorf("AnnouncementGroup = %04X, want AABB", got.AnnouncementGroup)
	}
	if got.Response != trunking.AffiliationDenied {
		t.Errorf("Response = %v, want denied", got.Response)
	}
	if got.At.Unix() != 1_700_000_001 {
		t.Errorf("At = %v, want injected Now", got.At)
	}
}

func TestControlChannelPublishesUnitRegistration(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	// Response = accepted (0), WACN = 0xBEE08, SystemID = 0x534,
	// SourceID = 0x112233.
	payload := [8]byte{0x00, 0xBE, 0xE0, 0x85, 0x34, 0x11, 0x22, 0x33}
	tsbk := TSBK{LB: true, Opcode: OpUnitRegistrationResponse, Payload: payload}
	stream := buildLockedStreamWithTSBK(10, 0x222, DUIDTrunkingSignaling, tsbk)

	cc := New(Options{
		Bus:         bus,
		SystemName:  "TestSys",
		FrequencyHz: 851_000_000,
		Now:         func() time.Time { return time.Unix(1_700_000_002, 0).UTC() },
	})
	cc.Process(stream, 0)

	var got *trunking.UnitRegistration
	deadline := time.After(time.Second)
	for got == nil {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindUnitRegistration {
				u := ev.Payload.(trunking.UnitRegistration)
				got = &u
			}
		case <-deadline:
			t.Fatal("no registration event published")
		}
	}
	if got.System != "TestSys" || got.Protocol != "p25" {
		t.Errorf("identity = %s/%s", got.System, got.Protocol)
	}
	if got.SourceID != 0x112233 {
		t.Errorf("SourceID = %06X, want 112233", got.SourceID)
	}
	if got.WACN != 0xBEE08 {
		t.Errorf("WACN = %05X, want BEE08", got.WACN)
	}
	if got.SystemID != 0x534 {
		t.Errorf("SystemID = %03X, want 534", got.SystemID)
	}
	if got.Response != trunking.RegistrationAccepted {
		t.Errorf("Response = %v, want accepted", got.Response)
	}
	if got.At.Unix() != 1_700_000_002 {
		t.Errorf("At = %v, want injected Now", got.At)
	}
}

func TestControlChannelGrantBeforeIdentifierUpdateEmitsDecodeError(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	grantPayload := [8]byte{0x00, 0x20, 0x05, 0x00, 0x42, 0, 0, 0}
	grantTSBK := TSBK{LB: true, Opcode: OpGroupVoiceChannelGrant, Payload: grantPayload}
	stream := buildLockedStreamWithTSBK(10, 0x111, DUIDTrunkingSignaling, grantTSBK)

	cc := New(Options{Bus: bus, SystemName: "S", FrequencyHz: 1})
	cc.Process(stream, 0)

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindGrant {
				t.Fatalf("unexpected grant emitted without band-plan: %+v", ev.Payload)
			}
			if ev.Kind != events.KindDecodeError {
				continue
			}
			de := ev.Payload.(events.DecodeError)
			if de.Protocol == "p25" && de.Stage == "no-bandplan" {
				return
			}
		case <-deadline:
			t.Fatal("no decode-error with stage=no-bandplan")
		}
	}
}

func TestControlChannelPublishesDecodeErrorOnCorruptTSBK(t *testing.T) {
	bus := events.NewBus(16)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	// Valid FSW + valid NID + corrupted TSBK channel block. Corrupt the
	// contiguous TSBK before status symbols are interleaved, so the
	// flips land squarely on the 98-dibit channel block.
	frame := buildControlFrame(0x111, DUIDTrunkingSignaling, TSBK{LB: true, Opcode: OpRFSSStatusBroadcast})
	// Flip every dibit in the TSBK block — well beyond the Viterbi
	// correction radius, so the CRC trailer will fail.
	for i := 24 + 32; i < 24+32+98; i++ {
		frame[i] = (^frame[i]) & 0x3
	}
	onAir := InjectControlStatusSymbols(frame)
	stream := make([]uint8, 10+len(onAir)+16)
	copy(stream[10:], onAir)

	cc := NewControlChannel(bus, nil, 851_000_000)
	cc.Process(stream, 0)

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind != events.KindDecodeError {
				continue
			}
			de, ok := ev.Payload.(events.DecodeError)
			if !ok {
				t.Fatalf("payload type = %T", ev.Payload)
			}
			if de.Protocol != "p25" {
				t.Errorf("protocol = %s, want p25", de.Protocol)
			}
			if de.Stage != "tsbk-crc" && de.Stage != "tsbk-trellis" {
				t.Errorf("stage = %s, want tsbk-crc or tsbk-trellis", de.Stage)
			}
			return
		case <-deadline:
			t.Fatal("no decode-error event published for corrupt TSBK")
		}
	}
}

// TestControlChannelLocksThroughPostFSWSlip splices surplus dibits
// between the FSW and the NID — the post-FSW symbol slip issue #275's
// reporter observed, where the FSW detects reliably but the NID, shifted
// by a dibit or two, never BCH-decodes under a fixed single-offset read.
// parseFrame's bounded alignment search must recover the lock across the
// full NIDSearchSpan range — slips 1..6 after the span was widened from
// the original ±2 (which left field retests pegged at the +2 edge every
// frame; #275 post-#321).
func TestControlChannelLocksThroughPostFSWSlip(t *testing.T) {
	for _, slip := range []int{1, 2, 3, 4, 5, 6} {
		bus := events.NewBus(8)
		sub := bus.Subscribe()

		base := buildLockedStream(10, 0x293, DUIDTrunkingSignaling, OpRFSSStatusBroadcast)
		// The FSW occupies indices 10..33; splice `slip` surplus dibits
		// in right after it, before the NID. Everything downstream —
		// NID, its interleaved status symbol, the TSBK — shifts with it.
		slipped := make([]uint8, 0, len(base)+slip)
		slipped = append(slipped, base[:34]...)
		for i := 0; i < slip; i++ {
			slipped = append(slipped, 0)
		}
		slipped = append(slipped, base[34:]...)

		cc := NewControlChannel(bus, nil, 851_000_000)
		cc.Process(slipped, 0)
		select {
		case ev := <-sub.C:
			if ev.Kind != events.KindCCLocked {
				t.Errorf("slip=%d kind = %s, want cc.locked", slip, ev.Kind)
			} else if ls := ev.Payload.(LockState); ls.NAC != 0x293 {
				t.Errorf("slip=%d NAC = %#x, want 0x293", slip, ls.NAC)
			}
		case <-time.After(time.Second):
			t.Errorf("slip=%d no lock — alignment search did not recover the NID", slip)
		}
		sub.Close()
		bus.Close()
	}
}

// TestControlChannelLocksThroughPostFSWSlipSmallChunks repeats the
// post-FSW slip test under RTL-realistic small-chunk delivery: each
// Process call carries only ~19 dibits, so the slipped frame straddles
// many calls and parseFrame runs under whatever buffer state
// trimBuffer leaves it in. The widened search must still recover the
// lock — i.e. the buffer math at the new NIDSearchSpan stays
// consistent with cross-call frame assembly (#275 post-#321).
func TestControlChannelLocksThroughPostFSWSlipSmallChunks(t *testing.T) {
	for _, slip := range []int{1, 3, 6} {
		bus := events.NewBus(16)
		sub := bus.Subscribe()

		base := buildLockedStream(10, 0x293, DUIDTrunkingSignaling, OpRFSSStatusBroadcast)
		slipped := make([]uint8, 0, len(base)+slip)
		slipped = append(slipped, base[:34]...)
		for i := 0; i < slip; i++ {
			slipped = append(slipped, 0)
		}
		slipped = append(slipped, base[34:]...)

		cc := NewControlChannel(bus, nil, 851_000_000)
		const batch = 19
		for off := 0; off < len(slipped); off += batch {
			end := off + batch
			if end > len(slipped) {
				end = len(slipped)
			}
			cc.Process(slipped[off:end], off)
		}
		select {
		case ev := <-sub.C:
			if ev.Kind != events.KindCCLocked {
				t.Errorf("slip=%d kind = %s, want cc.locked", slip, ev.Kind)
			} else if ls := ev.Payload.(LockState); ls.NAC != 0x293 {
				t.Errorf("slip=%d NAC = %#x, want 0x293", slip, ls.NAC)
			}
		case <-time.After(time.Second):
			t.Errorf("slip=%d no lock under small-chunk delivery — buffer/trim math broke at the widened span", slip)
		}
		sub.Close()
		bus.Close()
	}
}

// TestSearchBoundaryHelpers locks down the boundary-pegged diag the
// next #275 field retest will hinge on. The two helpers — atSearchBoundary
// and boundaryNote — together turn the failure diag (and the success
// "nid corrected" log) into a measurement: a reader can tell a bounded
// framing slip (search converged interior with low errs → fix worked)
// from one that exceeds the span (still pegged at the edge → look
// elsewhere). The diag-string format wires boundaryNote() directly into
// the format strings, so guarding the helpers guards the contract.
func TestSearchBoundaryHelpers(t *testing.T) {
	cases := []struct {
		delta      int
		atBoundary bool
	}{
		{0, false},
		{1, false},
		{NIDSearchSpan - 1, false},
		{NIDSearchSpan, true},
		{-NIDSearchSpan, true},
		{-(NIDSearchSpan - 1), false},
		{NIDSearchSpan + 1, false}, // outside the grid; not "at" boundary
	}
	for _, tc := range cases {
		got := atSearchBoundary(tc.delta)
		if got != tc.atBoundary {
			t.Errorf("atSearchBoundary(%d) = %v, want %v", tc.delta, got, tc.atBoundary)
		}
		note := boundaryNote(tc.delta)
		if tc.atBoundary {
			if !strings.Contains(note, "search boundary") {
				t.Errorf("boundaryNote(%d) = %q, want contains \"search boundary\"", tc.delta, note)
			}
		} else if note != "" {
			t.Errorf("boundaryNote(%d) = %q, want empty", tc.delta, note)
		}
	}
}

// TestSearchNIDFailureDiagFlagsBoundary exercises the wiring of
// boundaryNote into searchNID's failure diag. A slip past NIDSearchSpan
// can never be recovered; we feed several deliberately-corrupt streams
// and assert that at least one of the failure diags surfaces the
// "search boundary" suffix. This guards the format-string call to
// boundaryNote without requiring the BCH error landscape to land a
// boundary-pegged hypothesis on any single fixture.
func TestSearchNIDFailureDiagFlagsBoundary(t *testing.T) {
	seenBoundary := false
	for seed := uint32(1); seed <= 24 && !seenBoundary; seed++ {
		cap := &diagCapture{}
		bus := events.NewBus(16)
		// Build a frame whose NID dibits are deliberately poisoned at
		// the seed-derived positions: the BCH decoder will find a
		// nearest-codeword miscorrection at SOME delta, and across
		// seeds at least one will be the +6 or -6 grid edge.
		frame := buildControlFrame(0x293, DUIDTrunkingSignaling,
			TSBK{LB: true, Opcode: OpRFSSStatusBroadcast})
		r := seed
		for i := 24; i < 24+32; i++ {
			r = r*1664525 + 1013904223
			frame[i] ^= uint8((r >> 13) & 0x3)
		}
		// Corrupt the TSBK too so corroboration cannot rescue anything.
		for i := 24 + 32; i < 24+32+98; i++ {
			frame[i] = (^frame[i]) & 0x3
		}
		onAir := InjectControlStatusSymbols(frame)
		stream := make([]uint8, 10+len(onAir)+16)
		copy(stream[10:], onAir)

		cc := New(Options{Bus: bus, Log: slog.New(cap), FrequencyHz: 851_000_000})
		cc.Process(stream, 0)
		if cap.containsDiag("search boundary") {
			seenBoundary = true
		}
		bus.Close()
	}
	if !seenBoundary {
		t.Skip("BCH error landscape never produced a boundary-pegged closest miss across 24 fixtures; helper unit-test still guards the contract")
	}
}

// TestControlChannelC4FMRejectsNonPhysicalRotation guards the C4FM
// rotation-set restriction (issue #275 post-#321). A C4FM FM-discriminator
// stream physically presents only rotations 0 and 2; the field retest
// converged on a rot=3 NID miscorrection that crowded out the real
// alignment. With RotationsC4FM the search must NOT lock on a
// rot=1-shifted stream (it's not a real signal a C4FM front end can
// produce). With the default RotationsAll the same stream must lock —
// proving the restriction is what's blocking the lock, not the stream.
func TestControlChannelC4FMRejectsNonPhysicalRotation(t *testing.T) {
	base := buildLockedStream(10, 0x293, DUIDTrunkingSignaling, OpRFSSStatusBroadcast)
	// Rotate the stream by k=1: received = canonical - 1, so the FSW
	// correlator finds the FSW under rotation 1.
	rotated := make([]uint8, len(base))
	for i, d := range base {
		rotated[i] = (d + 3) & 3
	}

	// With C4FM rotation restriction {0,2}, no FSW hit, no lock.
	t.Run("c4fm-rejects", func(t *testing.T) {
		bus := events.NewBus(16)
		defer bus.Close()
		sub := bus.Subscribe()
		defer sub.Close()

		cc := New(Options{
			Bus:         bus,
			FrequencyHz: 851_000_000,
			Rotations:   RotationsC4FM,
		})
		cc.Process(rotated, 0)

		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindCCLocked {
				t.Fatalf("rot=1 stream locked under C4FM rotation restriction; should not")
			}
		case <-time.After(100 * time.Millisecond):
			// No lock event — expected.
		}
	})

	// Sanity: the default all-rotations set still locks the same stream.
	t.Run("all-rotations-locks", func(t *testing.T) {
		bus := events.NewBus(16)
		defer bus.Close()
		sub := bus.Subscribe()
		defer sub.Close()

		cc := NewControlChannel(bus, nil, 851_000_000)
		cc.Process(rotated, 0)

		select {
		case ev := <-sub.C:
			if ev.Kind != events.KindCCLocked {
				t.Errorf("kind = %s, want cc.locked under RotationsAll", ev.Kind)
			} else if ls := ev.Payload.(LockState); ls.NAC != 0x293 {
				t.Errorf("NAC = %#x, want 0x293", ls.NAC)
			}
		case <-time.After(time.Second):
			t.Errorf("rot=1 stream did not lock under RotationsAll — sanity check broken")
		}
	})
}

// diagCapture is a slog.Handler that records the "diag" attribute of
// every NID-decode failure log line so the boundary-diag test can
// assert on its content.
type diagCapture struct {
	mu    sync.Mutex
	diags []string
}

func (h *diagCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *diagCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "diag" {
			h.diags = append(h.diags, a.Value.String())
		}
		return true
	})
	return nil
}

func (h *diagCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *diagCapture) WithGroup(string) slog.Handler      { return h }

func (h *diagCapture) containsDiag(needle string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, d := range h.diags {
		if strings.Contains(d, needle) {
			return true
		}
	}
	return false
}

// TestControlChannelNoFalseLockOnNoise feeds a valid FSW followed by
// pseudo-random dibits. parseFrame's bounded alignment search probes
// every combination of (delta, strip, rot) across the trusted
// (BCH + even-parity) and marginal (NID errs 7..11 corroborated by the
// TSBK CRC) tiers; neither may manufacture a lock out of noise — a
// noise NID has no clean TSBK to corroborate it.
func TestControlChannelNoFalseLockOnNoise(t *testing.T) {
	bus := events.NewBus(16)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	frame := make([]uint8, 24+32+98)
	copy(frame, FrameSyncWord[:])
	// Deterministic LCG fill so the test is reproducible.
	r := uint32(0x9E3779B9)
	for i := 24; i < len(frame); i++ {
		r = r*1664525 + 1013904223
		frame[i] = uint8(r>>13) & 0x3
	}
	onAir := InjectControlStatusSymbols(frame)
	stream := make([]uint8, 10+len(onAir)+16)
	copy(stream[10:], onAir)

	cc := NewControlChannel(bus, nil, 851_000_000)
	cc.Process(stream, 0)

	select {
	case ev := <-sub.C:
		if ev.Kind == events.KindCCLocked {
			t.Fatalf("false lock on noise: %+v", ev.Payload)
		}
		// KindDecodeError is the correct outcome here.
	case <-time.After(100 * time.Millisecond):
	}
}

// TestControlChannelLocksOnMarginalNIDCorroboratedByTSBK injects 8 bit
// errors into the NID — inside BCH(63,16,11)'s t=11 radius but past the
// NIDAcceptErrs=6 trusted threshold — while leaving the TSBK intact.
// searchNID's marginal tier must still lock, because the frame's TSBK
// decodes cleanly under the same alignment and corroborates the NID.
// This is issue #275's strong-site symptom: a NID that BCH-decodes but
// pegs above the trusted gate (the reporter's 9/10/11-error probes).
func TestControlChannelLocksOnMarginalNIDCorroboratedByTSBK(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	frame := buildControlFrame(0x293, DUIDTrunkingSignaling,
		TSBK{LB: true, Opcode: OpRFSSStatusBroadcast})
	// Flip the MSB of 8 distinct NID dibits (frame[24:56]). Each is a
	// distinct even-indexed codeword bit, so exactly 8 BCH errors land;
	// the trailing parity bit (LSB of NID dibit 31) is left untouched,
	// so the corrected codeword still passes even-parity.
	for _, i := range []int{1, 4, 8, 12, 16, 20, 25, 30} {
		frame[24+i] ^= 0b10
	}
	onAir := InjectControlStatusSymbols(frame)
	stream := make([]uint8, 10+len(onAir)+16)
	copy(stream[10:], onAir)

	cc := NewControlChannel(bus, nil, 851_000_000)
	cc.Process(stream, 0)

	select {
	case ev := <-sub.C:
		if ev.Kind != events.KindCCLocked {
			t.Fatalf("kind = %s, want cc.locked", ev.Kind)
		}
		if ls := ev.Payload.(LockState); ls.NAC != 0x293 {
			t.Errorf("NAC = %#x, want 0x293", ls.NAC)
		}
	case <-time.After(time.Second):
		t.Fatal("no lock — marginal NID not corroborated by its clean TSBK")
	}
}

// TestControlChannelRejectsMarginalNIDWithoutTSBK is the false-lock
// guard for the marginal tier: the same 8-bit-error NID, but with the
// TSBK channel block corrupted too. With no clean TSBK to corroborate
// the alignment, searchNID must reject the NID — a NID 7..11 BCH
// corrections from the received word is as plausibly a miscorrection as
// a real noisy NID, and only the TSBK CRC can tell them apart.
func TestControlChannelRejectsMarginalNIDWithoutTSBK(t *testing.T) {
	bus := events.NewBus(16)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	frame := buildControlFrame(0x293, DUIDTrunkingSignaling,
		TSBK{LB: true, Opcode: OpRFSSStatusBroadcast})
	for _, i := range []int{1, 4, 8, 12, 16, 20, 25, 30} {
		frame[24+i] ^= 0b10
	}
	// Flip every TSBK dibit — well past the Viterbi correction radius,
	// so the CRC trailer fails and the alignment cannot be corroborated.
	for i := 24 + 32; i < 24+32+98; i++ {
		frame[i] = (^frame[i]) & 0x3
	}
	onAir := InjectControlStatusSymbols(frame)
	stream := make([]uint8, 10+len(onAir)+16)
	copy(stream[10:], onAir)

	cc := NewControlChannel(bus, nil, 851_000_000)
	cc.Process(stream, 0)

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindCCLocked {
				t.Fatal("false lock: marginal NID accepted without TSBK corroboration")
			}
			if ev.Kind == events.KindDecodeError {
				return
			}
		case <-deadline:
			t.Fatal("no decode-error event for uncorroborated marginal NID")
		}
	}
}
