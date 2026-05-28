package receiver

import (
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/radio/aprs/hdlc"
	"github.com/MattCheramie/GopherTrunk/internal/storage"
)

// msbFirstBitsToBytes is the inverse of bytesToMSBFirstBits — packs
// a one-bit-per-byte AIS bit slice into bytes (MSB-first). Used to
// build synthetic AIS frame bodies for end-to-end tests.
func msbFirstBitsToBytes(bits []byte) []byte {
	out := make([]byte, (len(bits)+7)/8)
	for i, b := range bits {
		if b != 0 {
			out[i/8] |= 1 << uint(7-(i%8))
		}
	}
	return out
}

// buildAISFrame assembles an AIS frame body suitable for HDLC-
// wrapping: 23 bytes of payload (168-bit AIS message) + 2 bytes of
// CRC-CCITT FCS. The caller supplies the MSB-first AIS payload
// bits; this helper packs them, appends the FCS, and returns the
// byte slice the HDLC framer would emit between two 0x7E flags.
func buildAISFrame(payloadBits []byte) []byte {
	payload := msbFirstBitsToBytes(payloadBits)
	fcs := crc16CCITT(payload)
	out := make([]byte, 0, len(payload)+2)
	out = append(out, payload...)
	out = append(out, byte(fcs&0xFF), byte(fcs>>8))
	return out
}

// wrapHDLC encodes the AIS frame body in HDLC framing: opening
// flag, bit-stuffed body, closing flag. Result is one byte per bit
// LSB-first, ready to feed through Receiver.Push.
func wrapHDLC(body []byte) []byte {
	const flag = hdlc.FlagPattern
	var bits []byte
	emitByte := func(b byte) {
		for i := 0; i < 8; i++ {
			bits = append(bits, (b>>i)&1)
		}
	}
	emitByte(flag)
	ones := 0
	for _, b := range body {
		for i := 0; i < 8; i++ {
			bit := (b >> i) & 1
			bits = append(bits, bit)
			if bit != 0 {
				ones++
				if ones == 5 {
					bits = append(bits, 0)
					ones = 0
				}
			} else {
				ones = 0
			}
		}
	}
	emitByte(flag)
	return bits
}

func pushAll(r *Receiver, bits []byte) {
	for _, b := range bits {
		r.Push(b)
	}
}

func newTestBus(t *testing.T) (*events.Bus, *events.Subscription) {
	t.Helper()
	bus := events.NewBus(8)
	sub := bus.Subscribe()
	t.Cleanup(func() {
		sub.Close()
		bus.Close()
	})
	return bus, sub
}

func awaitMessage(t *testing.T, sub *events.Subscription) storage.AISMessage {
	t.Helper()
	select {
	case ev, ok := <-sub.C:
		if !ok {
			t.Fatal("bus closed before message arrived")
		}
		if ev.Kind != events.KindAISMessage {
			t.Fatalf("got event %q, want ais.message", ev.Kind)
		}
		msg, ok := ev.Payload.(storage.AISMessage)
		if !ok {
			t.Fatalf("payload type = %T, want storage.AISMessage", ev.Payload)
		}
		return msg
	case <-time.After(time.Second):
		t.Fatal("no message arrived within 1s")
		return storage.AISMessage{}
	}
}

// aivdmPayloadToBits unpacks an AIVDM-formatted ASCII payload into
// the MSB-first bit slice. Duplicated from the parent package's
// test helper.
func aivdmPayloadToBits(payload string) []byte {
	out := make([]byte, 0, len(payload)*6)
	for _, c := range payload {
		v := int(c) - 48
		if v > 40 {
			v -= 8
		}
		v &= 0x3F
		for k := 5; k >= 0; k-- {
			out = append(out, byte((v>>uint(k))&1))
		}
	}
	return out
}

func TestNewPanicsWithoutBus(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("New(Options{}) without Bus: want panic")
		}
	}()
	_ = New(Options{})
}

// TestReceiverEmitsPositionMessage drives a synthetic AIS frame —
// the gpsd type-1 AIVDM canonical sample, bit-padded to 168 bits —
// through the HDLC framer + CRC validator + AIS parser, and asserts
// a KindAISMessage event lands on the bus with the expected MMSI
// and decoded lat/lon.
func TestReceiverEmitsPositionMessage(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus})

	payload := aivdmPayloadToBits("15M67FC000G?ufbE`FepT@3n00Sa")
	// AIVDM type 1 = 168 bits (28 chars × 6 = 168). Spec-aligned.
	if len(payload) != 168 {
		t.Fatalf("payload bits = %d, want 168", len(payload))
	}
	body := buildAISFrame(payload)
	bits := wrapHDLC(body)
	pushAll(r, bits)

	msg := awaitMessage(t, sub)
	if msg.MMSI != 366053209 {
		t.Errorf("MMSI = %d, want 366053209", msg.MMSI)
	}
	if msg.Type != "position-a" {
		t.Errorf("Type = %q, want position-a", msg.Type)
	}
	if !msg.FCSOK {
		t.Errorf("FCSOK = false on clean frame")
	}
	if !msg.HasPosition {
		t.Errorf("HasPosition = false on valid lat/lon")
	}
	if msg.Latitude < 37.8 || msg.Latitude > 37.81 {
		t.Errorf("Latitude = %f, want ≈ 37.802", msg.Latitude)
	}
}

func TestReceiverDropBadFCSOption(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus, DropBadFCS: true})

	payload := aivdmPayloadToBits("15M67FC000G?ufbE`FepT@3n00Sa")
	body := buildAISFrame(payload)
	// Corrupt the payload (flip the high bit of the MMSI's last
	// byte) so the CRC fails.
	body[4] ^= 0x80
	bits := wrapHDLC(body)
	pushAll(r, bits)

	select {
	case ev := <-sub.C:
		t.Errorf("got event %q when DropBadFCS=true; want silence", ev.Kind)
	case <-time.After(100 * time.Millisecond):
	}
	stats := r.Stats()
	if stats.FramesBadFCS != 1 {
		t.Errorf("FramesBadFCS = %d, want 1", stats.FramesBadFCS)
	}
	if stats.FramesEmitted != 0 {
		t.Errorf("FramesEmitted = %d, want 0", stats.FramesEmitted)
	}
}

func TestReceiverPublishesBadFCSByDefault(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus})

	payload := aivdmPayloadToBits("15M67FC000G?ufbE`FepT@3n00Sa")
	body := buildAISFrame(payload)
	body[4] ^= 0x80
	bits := wrapHDLC(body)
	pushAll(r, bits)

	msg := awaitMessage(t, sub)
	if msg.FCSOK {
		t.Error("FCSOK = true on corrupted frame")
	}
	stats := r.Stats()
	if stats.FramesBadFCS != 1 || stats.FramesEmitted != 1 {
		t.Errorf("stats = %+v, want BadFCS=1, Emitted=1", stats)
	}
}

func TestReceiverDropNonPositionOption(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus, DropNonPosition: true})

	// Type-5 static-voyage data — has Static but no Position.
	payload := aivdmPayloadToBits("55?MbV02;H;s<HtKR20EHE:0@T4@Dn2222222216L961O5Gf0NSQEp6ClRp8888888888880")
	// 70 chars × 6 = 420 bits; pad to byte boundary at 424 bits.
	for len(payload) < 424 {
		payload = append(payload, 0)
	}
	body := buildAISFrame(payload)
	bits := wrapHDLC(body)
	pushAll(r, bits)

	select {
	case ev := <-sub.C:
		t.Errorf("got event %q when DropNonPosition=true; want silence", ev.Kind)
	case <-time.After(100 * time.Millisecond):
	}
	stats := r.Stats()
	if stats.FramesParsed != 1 {
		t.Errorf("FramesParsed = %d, want 1 (frame parsed but dropped)", stats.FramesParsed)
	}
	if stats.FramesEmitted != 0 {
		t.Errorf("FramesEmitted = %d, want 0 (non-position drop)", stats.FramesEmitted)
	}
}

func TestReceiverDiscardsTooShortBodies(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus})

	// Garbage 18-byte body (below MinPayloadBytes = 23) wrapped
	// in HDLC framing. The HDLC framer's MinFrameBytes is 18; ours
	// is 23 so this gets discarded at the AIS receiver layer.
	garbage := make([]byte, 22)
	bits := wrapHDLC(garbage)
	pushAll(r, bits)

	select {
	case ev := <-sub.C:
		t.Errorf("got event %q for too-short body; want silence", ev.Kind)
	case <-time.After(100 * time.Millisecond):
	}
	stats := r.Stats()
	if stats.FramesIn != 1 {
		t.Errorf("FramesIn = %d, want 1", stats.FramesIn)
	}
	if stats.FramesTooShort != 1 {
		t.Errorf("FramesTooShort = %d, want 1", stats.FramesTooShort)
	}
}

func TestCRC16CCITTSelfConsistency(t *testing.T) {
	// CRC computed over a payload, when re-computed over (payload
	// + CRC bytes in little-endian) using the standard HDLC
	// magic-residue test, lands at 0xF0B8 — the same invariant
	// AX.25 has. Documents the shared algorithm and gives us a
	// regression anchor.
	payload := []byte("hello, world")
	fcs := crc16CCITT(payload)
	withFCS := append(payload, byte(fcs&0xFF), byte(fcs>>8))
	check := uint16(0xFFFF)
	for _, b := range withFCS {
		check ^= uint16(b)
		for i := 0; i < 8; i++ {
			if check&1 != 0 {
				check = (check >> 1) ^ 0x8408
			} else {
				check >>= 1
			}
		}
	}
	if check != 0xF0B8 {
		t.Errorf("CRC residue = 0x%04X, want 0xF0B8 (HDLC magic residue)", check)
	}
}

func TestBytesToMSBFirstBitsRoundTrip(t *testing.T) {
	// 0b10110011 = 0xB3 → MSB-first bits 1,0,1,1,0,0,1,1.
	got := bytesToMSBFirstBits([]byte{0xB3})
	want := []byte{1, 0, 1, 1, 0, 0, 1, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("bit[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestReceiverStatsCountAcrossMultipleFrames(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus})

	for i := 0; i < 3; i++ {
		payload := aivdmPayloadToBits("15M67FC000G?ufbE`FepT@3n00Sa")
		body := buildAISFrame(payload)
		bits := wrapHDLC(body)
		pushAll(r, bits)
	}

	for i := 0; i < 3; i++ {
		_ = awaitMessage(t, sub)
	}
	stats := r.Stats()
	if stats.FramesIn != 3 || stats.FramesParsed != 3 || stats.FramesEmitted != 3 {
		t.Errorf("stats = %+v", stats)
	}
}
