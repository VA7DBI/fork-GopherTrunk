package pocsag

import "testing"

// unpackCodewordToBits is the inverse of packCodeword — emits 32
// MSB-first bytes for the supplied codeword. Test helper.
func unpackCodewordToBits(cw uint32) []byte {
	bits := make([]byte, 32)
	for i := 0; i < 32; i++ {
		if cw&(1<<uint(31-i)) != 0 {
			bits[i] = 1
		}
	}
	return bits
}

func TestDecodeBatchTooShort(t *testing.T) {
	if _, err := DecodeBatch(make([]byte, BatchBits-1)); err != ErrBatchTooShort {
		t.Errorf("DecodeBatch(short) = %v, want ErrBatchTooShort", err)
	}
}

func TestDecodeBatchAllIdle(t *testing.T) {
	// Sync + 16 idle codewords — the "empty" batch every POCSAG
	// transmitter sends between real pages.
	var bits []byte
	bits = append(bits, unpackCodewordToBits(SyncCodeword)...)
	for i := 0; i < BatchCodewords; i++ {
		bits = append(bits, unpackCodewordToBits(IdleCodeword)...)
	}
	batch, err := DecodeBatch(bits)
	if err != nil {
		t.Fatalf("DecodeBatch: %v", err)
	}
	if batch.Sync.Type != WordTypeSync {
		t.Errorf("sync.Type = %v, want WordTypeSync", batch.Sync.Type)
	}
	for i, w := range batch.Words {
		if w.Type != WordTypeIdle {
			t.Errorf("word[%d].Type = %v, want WordTypeIdle", i, w.Type)
		}
	}
}

func TestDecodeBatchWithAddressAndMessage(t *testing.T) {
	// Synthetic batch: address at slot 3 (word 6 + 7 reserved for
	// the page), message at word 7. Verifies wire-order parsing
	// and frame-slot resolution.
	const (
		addr18     uint32   = 0x12345
		fn         Function = 1 // 'B'
		msgPayload uint32   = 0x0ABCD
	)

	var bits []byte
	bits = append(bits, unpackCodewordToBits(SyncCodeword)...)
	for i := 0; i < BatchCodewords; i++ {
		var cw uint32
		switch i {
		case 6: // slot 3, first half
			cw = EncodeAddress(addr18, fn)
		case 7: // slot 3, second half
			cw = EncodeMessage(msgPayload)
		default:
			cw = IdleCodeword
		}
		bits = append(bits, unpackCodewordToBits(cw)...)
	}
	batch, err := DecodeBatch(bits)
	if err != nil {
		t.Fatalf("DecodeBatch: %v", err)
	}
	if batch.Words[6].Type != WordTypeAddress {
		t.Fatalf("word[6].Type = %v, want WordTypeAddress", batch.Words[6].Type)
	}
	if batch.Words[6].Address != addr18 {
		t.Errorf("address = 0x%x, want 0x%x", batch.Words[6].Address, addr18)
	}
	if batch.Words[6].Func != fn {
		t.Errorf("func = %v, want %v", batch.Words[6].Func, fn)
	}
	if batch.Words[7].Type != WordTypeMessage {
		t.Fatalf("word[7].Type = %v, want WordTypeMessage", batch.Words[7].Type)
	}
	if batch.Words[7].MessageBits != msgPayload {
		t.Errorf("msg = 0x%x, want 0x%x", batch.Words[7].MessageBits, msgPayload)
	}

	// Slot 3 = word 6 / 2.
	if got := FrameSlot(6); got != 3 {
		t.Errorf("FrameSlot(6) = %d, want 3", got)
	}
	ric := ReconstructRIC(addr18, FrameSlot(6))
	wantRIC := uint32((addr18 << 3) | 3)
	if ric != wantRIC {
		t.Errorf("RIC = 0x%x, want 0x%x", ric, wantRIC)
	}
}
