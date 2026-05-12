package dstar

import (
	"log/slog"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

// TestProcess_DecodesHeaderAfterFrameSync feeds the Process adapter a
// bit stream consisting of a few warmup bits, the 32-bit Frame Sync,
// and 328 information bits encoding a valid group-call header. The
// state machine should publish events.KindCCLocked + an
// events.KindGrant on the bus.
func TestProcess_DecodesHeaderAfterFrameSync(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := New(Options{
		Bus:         bus,
		Log:         slog.Default(),
		SystemName:  "TestRepeater",
		FrequencyHz: 145_670_000,
	})

	stream := buildDStarHeaderStream("CQCQCQ  ", "MYCALL  ", "RPT2CALL", "RPT1CALL")
	if got := cc.Process(stream, 0); got != len(stream) {
		t.Errorf("Process returned %d, want %d", got, len(stream))
	}

	var sawLock, sawGrant bool
	deadline := time.After(200 * time.Millisecond)
DrainLoop:
	for {
		select {
		case ev := <-sub.C:
			switch ev.Kind {
			case events.KindCCLocked:
				sawLock = true
			case events.KindGrant:
				sawGrant = true
			}
			if sawLock && sawGrant {
				break DrainLoop
			}
		case <-deadline:
			break DrainLoop
		}
	}
	if !sawLock {
		t.Errorf("no KindCCLocked event observed")
	}
	if !sawGrant {
		t.Errorf("no KindGrant event observed (UR=CQCQCQ should fire)")
	}
}

// TestProcess_RejectsBadCRC feeds the Process adapter a header with a
// deliberately corrupted CRC. The state machine should silently drop
// the frame.
func TestProcess_RejectsBadCRC(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := New(Options{
		Bus:         bus,
		Log:         slog.Default(),
		SystemName:  "TestRepeater",
		FrequencyHz: 145_670_000,
	})

	stream := buildDStarHeaderStream("CQCQCQ  ", "MYCALL  ", "RPT2CALL", "RPT1CALL")
	// Flip the last CRC bit so verification fails.
	stream[len(stream)-1] ^= 1
	cc.Process(stream, 0)

	select {
	case ev := <-sub.C:
		t.Errorf("expected no events for bad-CRC frame, got %v", ev.Kind)
	default:
	}
}

// buildDStarHeaderStream assembles a bit stream: warmup + Frame Sync +
// 328 information bits encoding a valid Header (UR/MY1/RPT2/RPT1) with
// a computed CRC trailer.
func buildDStarHeaderStream(ur, my1, rpt2, rpt1 string) []byte {
	hdr := Header{
		Flag1: 0,
		Flag2: 0,
		Flag3: 0,
		RPT2:  rpt2,
		RPT1:  rpt1,
		UR:    ur,
		MY1:   my1,
		MY2:   "SUFX",
	}
	asm := AssembleHeader(hdr)
	hdr.CRC = ComputeCRC(asm[:39])
	asm = AssembleHeader(hdr)

	out := make([]byte, 0, 64+FrameSyncBits+HeaderBits)
	// Warmup: 64 ones — padding to ensure the Frame Sync isn't at
	// index 0. Can't use zeros or the alternating-01 pattern because
	// either one creates a near-match for the FrameSync (0x55555555,
	// i.e., 0101...01) within the detector's tolerance and fires a
	// false sync a few bits before the real one.
	for i := 0; i < 64; i++ {
		out = append(out, 1)
	}
	out = append(out, FrameSyncBitsSlice()...)
	for _, b := range asm {
		for i := 0; i < 8; i++ {
			out = append(out, (b>>uint(7-i))&1)
		}
	}
	return out
}
