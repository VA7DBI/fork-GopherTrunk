package phase2

import (
	"log/slog"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/radio/framing"
)

// TestProcessLocksOnFirstMACPDUAfterSync: build a dibit stream of
// 30 padding dibits + 20 outbound sync dibits + 72 dibits whose
// raw bits form a MAC PDU (opcode = OpMACPTT, no payload). The
// state machine should publish a KindCCLocked since
// OpMACPTT.IsIdle() returns false.
func TestProcessLocksOnFirstMACPDUAfterSync(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := New(Options{
		Bus:         bus,
		Log:         slog.Default(),
		SystemName:  "Sys",
		FrequencyHz: 851_062_500,
	})

	pdu := MACPDU{Opcode: OpMACPTT, Payload: make([]byte, 17)}
	pduBytes := AssembleMACPDU(pdu)
	if len(pduBytes) != 18 {
		t.Fatalf("AssembleMACPDU = %d bytes, want 18", len(pduBytes))
	}
	pduBits := framing.UnpackBitsMSB(pduBytes, 144)
	pduDibits := framing.BitsToDibits(pduBits)
	if len(pduDibits) != 72 {
		t.Fatalf("MAC PDU dibits = %d, want 72", len(pduDibits))
	}

	stream := make([]uint8, 30)
	stream = append(stream, OutboundSyncDibits()...)
	stream = append(stream, pduDibits...)

	cc.Process(stream, 0)

	var sawLock bool
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindCCLocked {
				ls, _ := ev.Payload.(LockState)
				if ls.FrequencyHz != 851_062_500 {
					t.Errorf("LockState.FrequencyHz = %d", ls.FrequencyHz)
				}
				sawLock = true
			}
		default:
			if !sawLock {
				t.Errorf("Process did not publish a KindCCLocked")
			}
			return
		}
	}
}

// TestProcessHandlesMACPDUSpanningCalls confirms that a sync +
// MAC PDU split across two Process calls still drives Ingest.
func TestProcessHandlesMACPDUSpanningCalls(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := New(Options{Bus: bus, Log: slog.Default(), SystemName: "Sys"})

	pdu := MACPDU{Opcode: OpMACPTT, Payload: make([]byte, 17)}
	pduBits := framing.UnpackBitsMSB(AssembleMACPDU(pdu), 144)
	pduDibits := framing.BitsToDibits(pduBits)

	chunk1 := make([]uint8, 30)
	chunk1 = append(chunk1, OutboundSyncDibits()...)
	cc.Process(chunk1, 0)
	cc.Process(pduDibits, len(chunk1))

	var sawLock bool
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.KindCCLocked {
				sawLock = true
			}
		default:
			if !sawLock {
				t.Errorf("Process did not publish a KindCCLocked across the chunk boundary")
			}
			return
		}
	}
}

func TestProcessIgnoresGarbageWithoutSync(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	cc := New(Options{Bus: bus, Log: slog.Default(), SystemName: "Sys"})

	garbage := make([]uint8, 2000)
	for i := range garbage {
		garbage[i] = uint8(i % 4)
	}
	cc.Process(garbage, 0)

	select {
	case ev := <-sub.C:
		t.Errorf("unexpected event from garbage stream: %v", ev.Kind)
	default:
	}
}

func TestSyncDetectorReset(t *testing.T) {
	det := NewSyncDetector(OutboundSyncDibits(), 0)
	junk := make([]uint8, 100)
	det.Process(nil, junk, 0)
	det.Reset()
	if det.primed != 0 {
		t.Errorf("post-Reset primed = %d, want 0", det.primed)
	}
	if det.pos != 0 {
		t.Errorf("post-Reset pos = %d, want 0", det.pos)
	}
}
