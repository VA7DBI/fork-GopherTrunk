package pocsag

import "testing"

func TestDecodeRecognisesSyncAndIdle(t *testing.T) {
	if got := Decode(SyncCodeword); got.Type != WordTypeSync {
		t.Errorf("Decode(sync) type = %v, want WordTypeSync", got.Type)
	}
	if got := Decode(IdleCodeword); got.Type != WordTypeIdle {
		t.Errorf("Decode(idle) type = %v, want WordTypeIdle", got.Type)
	}
}

func TestEncodeAddressRoundTrip(t *testing.T) {
	for _, c := range []struct {
		addr18 uint32
		fn     Function
	}{
		{0, 0},
		{1, 1},
		{0x3FFFF, 3},
		{0x12345, 2},
		{0x00ABC, 0},
	} {
		cw := EncodeAddress(c.addr18, c.fn)
		got := Decode(cw)
		if got.Type != WordTypeAddress {
			t.Errorf("Decode(EncodeAddress(0x%x, %v)) type = %v, want WordTypeAddress",
				c.addr18, c.fn, got.Type)
		}
		if got.Address != c.addr18 {
			t.Errorf("addr = 0x%x, want 0x%x", got.Address, c.addr18)
		}
		if got.Func != c.fn {
			t.Errorf("func = %v, want %v", got.Func, c.fn)
		}
		if got.CorrectedErrors != 0 {
			t.Errorf("clean encode → CorrectedErrors = %d, want 0",
				got.CorrectedErrors)
		}
		if !got.ParityOK {
			t.Errorf("clean encode → ParityOK = false")
		}
	}
}

func TestEncodeMessageRoundTrip(t *testing.T) {
	for _, payload := range []uint32{
		0x00000, 0xFFFFF, 0x12345, 0xAAAAA, 0x55555,
	} {
		cw := EncodeMessage(payload)
		got := Decode(cw)
		if got.Type != WordTypeMessage {
			t.Errorf("Decode(EncodeMessage(0x%x)) type = %v, want WordTypeMessage",
				payload, got.Type)
		}
		if got.MessageBits != payload {
			t.Errorf("msg = 0x%x, want 0x%x", got.MessageBits, payload)
		}
	}
}

func TestDecodeCorrectsSingleBitError(t *testing.T) {
	clean := EncodeAddress(0x12345, FunctionFromByte('B'))
	for bit := 0; bit < 32; bit++ {
		flipped := clean ^ (1 << uint(bit))
		got := Decode(flipped)
		if got.Type != WordTypeAddress {
			t.Errorf("bit-flip at %d: type = %v, want WordTypeAddress",
				bit, got.Type)
		}
		if got.Address != 0x12345 {
			t.Errorf("bit-flip at %d: address = 0x%x, want 0x12345",
				bit, got.Address)
		}
	}
}

func TestDecodeFlagsParityFailure(t *testing.T) {
	clean := EncodeAddress(0x100, 0)
	flipped := clean ^ 1 // toggle parity bit only
	got := Decode(flipped)
	if got.Type != WordTypeAddress {
		t.Errorf("type = %v, want WordTypeAddress (BCH unaffected by parity-only flip)", got.Type)
	}
	if got.ParityOK {
		t.Errorf("ParityOK = true after parity-bit flip; want false")
	}
}

func TestFrameSlotMapping(t *testing.T) {
	// 16 codewords → 8 slots → wordIdx / 2 == slot.
	for i := 0; i < 16; i++ {
		if got := FrameSlot(i); got != i/2 {
			t.Errorf("FrameSlot(%d) = %d, want %d", i, got, i/2)
		}
	}
}

func TestReconstructRIC(t *testing.T) {
	// 18-bit addr 0x12345 in slot 5 → ((0x12345 << 3) | 5).
	got := ReconstructRIC(0x12345, 5)
	want := uint32((0x12345 << 3) | 5)
	if got != want {
		t.Errorf("ReconstructRIC(0x12345, 5) = 0x%x, want 0x%x", got, want)
	}
}

func TestPackCodewordMSBFirst(t *testing.T) {
	// 0x80000001 = bit 31 + bit 0 set.
	bits := make([]byte, 32)
	bits[0] = 1  // MSB
	bits[31] = 1 // LSB
	if got := packCodeword(bits); got != 0x80000001 {
		t.Errorf("packCodeword(MSB+LSB) = 0x%x, want 0x80000001", got)
	}
}

// FunctionFromByte is a tiny test helper that maps 'A'/'B'/'C'/'D'
// to the Function value.
func FunctionFromByte(c byte) Function {
	if c < 'A' || c > 'D' {
		return 0
	}
	return Function(c - 'A')
}

func TestFunctionString(t *testing.T) {
	for i, want := range []string{"A", "B", "C", "D"} {
		if got := Function(i).String(); got != want {
			t.Errorf("Function(%d).String() = %q, want %q", i, got, want)
		}
	}
	if got := Function(99).String(); got != "?" {
		t.Errorf("Function(99).String() = %q, want \"?\"", got)
	}
}
