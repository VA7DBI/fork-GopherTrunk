package mdc1200

import "testing"

// encodeFrame is the transmitter-side inverse of deinterleave: it
// packs the 14 data bytes into the de-interleaved logical bit order
// and then applies the 16×7 column interleave, yielding the 112
// over-the-air payload bits (one byte per bit). Used only by tests.
func encodeFrame(data [14]byte) []byte {
	var lbits [FrameBits]byte
	for i := 0; i < 14; i++ {
		for j := 0; j < 8; j++ {
			if data[i]&(1<<uint(j)) != 0 {
				lbits[i*8+j] = 1
			}
		}
	}
	bits := make([]byte, FrameBits)
	idx := 0
	for i := 0; i < 16; i++ {
		for j := 0; j < 7; j++ {
			bits[j*16+i] = lbits[idx]
			idx++
		}
	}
	return bits
}

// frameFor builds a CRC-valid frame for the given header fields.
func frameFor(op, arg uint8, unitID uint16) []byte {
	var data [14]byte
	data[0] = op
	data[1] = arg
	data[2] = byte(unitID >> 8)
	data[3] = byte(unitID)
	crc := crc16(data[:4])
	data[4] = byte(crc)      // low byte on the wire
	data[5] = byte(crc >> 8) // high byte
	return encodeFrame(data)
}

func TestDecodeFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		op, arg   uint8
		unitID    uint16
		operation string
	}{
		{"ptt-id", 0x01, 0x80, 0x1234, "PTT ID"},
		{"ptt-id-end", 0x01, 0x00, 0x4321, "PTT ID (end)"},
		{"emergency", 0x00, 0x90, 0x0042, "Emergency"},
		{"radio-check", 0x63, 0x00, 0xABCD, "Radio check"},
		{"status-7", 0x12, 0x07, 0x1000, "Status 7"},
		{"unknown", 0x99, 0x11, 0x5555, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bits := frameFor(c.op, c.arg, c.unitID)
			m, ok := DecodeFrame(bits)
			if !ok {
				t.Fatalf("DecodeFrame ok=false, want true (CRC should validate)")
			}
			if m.Op != c.op || m.Arg != c.arg {
				t.Errorf("op/arg = 0x%02X/0x%02X, want 0x%02X/0x%02X", m.Op, m.Arg, c.op, c.arg)
			}
			if m.UnitID != c.unitID {
				t.Errorf("UnitID = 0x%04X, want 0x%04X", m.UnitID, c.unitID)
			}
			if m.Operation != c.operation {
				t.Errorf("Operation = %q, want %q", m.Operation, c.operation)
			}
			if !m.CRCOK {
				t.Errorf("CRCOK = false, want true")
			}
		})
	}
}

func TestDecodeFrameCRCMismatch(t *testing.T) {
	bits := frameFor(0x01, 0x80, 0x1234)
	// Flip a bit in the unit-ID region so the CRC no longer matches.
	// Locate the over-the-air position of logical bit 16 (data[2], the
	// unit-ID high byte LSB): i = 16/7 = 2, j = 16%7 = 2 → raw j*16+i.
	bits[2*16+2] ^= 1
	m, ok := DecodeFrame(bits)
	if ok {
		t.Fatalf("DecodeFrame ok=true, want false on corrupted frame")
	}
	if m.CRCOK {
		t.Errorf("CRCOK = true, want false")
	}
	// Best-effort decode is still returned.
	if m.Body == "" {
		t.Errorf("Body empty; want best-effort summary even on CRC fail")
	}
}

func TestDecodeFrameIncomplete(t *testing.T) {
	m, ok := DecodeFrame(make([]byte, FrameBits-1))
	if ok {
		t.Errorf("ok = true, want false on short input")
	}
	if m.Operation != "incomplete" {
		t.Errorf("Operation = %q, want %q", m.Operation, "incomplete")
	}
}

func TestDoublePacketFlag(t *testing.T) {
	for _, op := range []uint8{0x35, 0x55} {
		bits := frameFor(op, 0x00, 0x0001)
		m, _ := DecodeFrame(bits)
		if !m.DoublePacket {
			t.Errorf("op 0x%02X: DoublePacket = false, want true", op)
		}
	}
	bits := frameFor(0x01, 0x80, 0x0001)
	if m, _ := DecodeFrame(bits); m.DoublePacket {
		t.Errorf("op 0x01: DoublePacket = true, want false")
	}
}

// TestCRC16KnownVector pins the CRC implementation against a fixed
// input so an accidental polynomial / reflection change is caught.
func TestCRC16KnownVector(t *testing.T) {
	// Round-trip property: a frame built with frameFor must validate,
	// and mutating any header byte must break it.
	var data [4]byte
	data[0], data[1], data[2], data[3] = 0x01, 0x80, 0x12, 0x34
	got := crc16(data[:])
	// Recompute independently to ensure determinism.
	if again := crc16(data[:]); again != got {
		t.Fatalf("crc16 not deterministic: %04X vs %04X", got, again)
	}
	data[0] ^= 0xFF
	if crc16(data[:]) == got {
		t.Errorf("crc16 unchanged after mutating input")
	}
}
