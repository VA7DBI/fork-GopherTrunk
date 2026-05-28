package receiver

import (
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/radio/aprs/ax25"
	"github.com/MattCheramie/GopherTrunk/internal/radio/aprs/hdlc"
	"github.com/MattCheramie/GopherTrunk/internal/storage"
)

// encodeAddress is duplicated from ax25's test helper — we can't
// import an internal package's _test.go file across packages, so
// recreate the minimal wire-format helper here.
func encodeAddress(callsign string, ssid uint8, last, hOrC bool) []byte {
	out := make([]byte, 7)
	for len(callsign) < 6 {
		callsign += " "
	}
	for i := 0; i < 6; i++ {
		out[i] = callsign[i] << 1
	}
	ssidByte := byte(0b01100000)
	ssidByte |= (ssid & 0x0F) << 1
	if hOrC {
		ssidByte |= 0x80
	}
	if last {
		ssidByte |= 0x01
	}
	out[6] = ssidByte
	return out
}

// hdlcFCS mirrors the ax25 package's CRC algorithm.
func hdlcFCS(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

// buildAX25Body assembles a complete AX.25 frame body suitable for
// HDLC-wrapping. Helper for the receiver tests; produces a UI
// frame with the standard 0x03/0xF0 control+PID.
func buildAX25Body(dstCall string, dstSSID uint8, srcCall string, srcSSID uint8, info []byte) []byte {
	var buf []byte
	buf = append(buf, encodeAddress(dstCall, dstSSID, false, false)...)
	buf = append(buf, encodeAddress(srcCall, srcSSID, true, true)...)
	buf = append(buf, 0x03, 0xF0)
	buf = append(buf, info...)
	fcs := hdlcFCS(buf)
	buf = append(buf, byte(fcs&0xFF), byte(fcs>>8))
	return buf
}

// wrapHDLC encodes the AX.25 body in HDLC framing: opening flag,
// bit-stuffed body, closing flag. Result is one byte per bit
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

// pushAll feeds the bit stream through the receiver, then returns
// the receiver's stats.
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

func awaitPacket(t *testing.T, sub *events.Subscription) storage.APRSPacket {
	t.Helper()
	select {
	case ev, ok := <-sub.C:
		if !ok {
			t.Fatal("bus closed before packet arrived")
		}
		if ev.Kind != events.KindAPRSPacket {
			t.Fatalf("got event %q, want aprs.packet", ev.Kind)
		}
		msg, ok := ev.Payload.(storage.APRSPacket)
		if !ok {
			t.Fatalf("payload type = %T, want storage.APRSPacket", ev.Payload)
		}
		return msg
	case <-time.After(time.Second):
		t.Fatal("no packet arrived within 1s")
		return storage.APRSPacket{}
	}
}

func TestNewPanicsWithoutBus(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("New(Options{}) without Bus: want panic")
		}
	}()
	_ = New(Options{})
}

func TestReceiverEmitsPositionPacket(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus})

	info := []byte("!4903.50N/07201.75W-Test")
	body := buildAX25Body("APRS", 0, "W1AW", 9, info)
	bits := wrapHDLC(body)
	pushAll(r, bits)

	pkt := awaitPacket(t, sub)
	if pkt.Src != "W1AW-9" {
		t.Errorf("Src = %q, want W1AW-9", pkt.Src)
	}
	if pkt.Type != "position" {
		t.Errorf("Type = %q, want position", pkt.Type)
	}
	if !pkt.FCSOK {
		t.Errorf("FCSOK = false on clean frame")
	}
	if pkt.Latitude < 49 || pkt.Latitude > 50 {
		t.Errorf("Latitude = %f, want ≈ 49", pkt.Latitude)
	}
	if pkt.Longitude > -71 || pkt.Longitude < -73 {
		t.Errorf("Longitude = %f, want ≈ -72", pkt.Longitude)
	}
}

// TestReceiverEmitsMicEPacket exercises the Mic-E decode path: the
// orchestrator hands the AX.25 destination callsign to
// aprs.DecodeWithDst, so a Mic-E info field arrives with lat/lon
// populated even though the position lives half in the destination
// address. Verifies the bus event carries the decoded position so
// the storage layer + /aprs panel get usable coordinates.
func TestReceiverEmitsMicEPacket(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus})

	// Dst encodes lat 33° 25.64' N, +100° offset, W hemisphere,
	// M3 Returning. Info encodes lon 112° 09.18' W, speed 20 kn,
	// course 251°, car symbol on the primary table.
	dst := "S32UVT"
	info := []byte{
		'`',     // DTI: Mic-E current GPS data
		12 + 28, // lon deg (raw 12, +100° offset → 112)
		9 + 28,  // lon min
		18 + 28, // lon hun
		2 + 28,  // sp
		2 + 28,  // dc
		51 + 28, // se
		'>',     // symbol code (car)
		'/',     // symbol table (primary)
	}
	body := buildAX25Body(dst, 0, "W1AW", 9, info)
	bits := wrapHDLC(body)
	pushAll(r, bits)

	pkt := awaitPacket(t, sub)
	if pkt.Type != "mic-e" {
		t.Errorf("Type = %q, want mic-e", pkt.Type)
	}
	if pkt.Latitude < 33.4 || pkt.Latitude > 33.5 {
		t.Errorf("Latitude = %f, want ≈ 33.42", pkt.Latitude)
	}
	if pkt.Longitude > -112.1 || pkt.Longitude < -112.2 {
		t.Errorf("Longitude = %f, want ≈ -112.15", pkt.Longitude)
	}
}

func TestReceiverEmitsMessagePacket(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus})

	info := []byte(":W1AW-1   :Hello world{42}")
	body := buildAX25Body("APRS", 0, "W2XYZ", 0, info)
	bits := wrapHDLC(body)
	pushAll(r, bits)

	pkt := awaitPacket(t, sub)
	if pkt.Type != "message" {
		t.Errorf("Type = %q, want message", pkt.Type)
	}
	if pkt.Src != "W2XYZ" {
		t.Errorf("Src = %q", pkt.Src)
	}
}

func TestReceiverDropBadFCSOption(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus, DropBadFCS: true})

	// Build a valid frame, then corrupt one byte of the info
	// field so the CRC fails.
	info := []byte("!4903.50N/07201.75W-Test")
	body := buildAX25Body("APRS", 0, "W1AW", 9, info)
	// Flip a bit in the info section (after the 16-byte address
	// chain + control + PID = 16 bytes prefix).
	body[18] ^= 0x80
	bits := wrapHDLC(body)
	pushAll(r, bits)

	select {
	case ev := <-sub.C:
		t.Errorf("got event %q when DropBadFCS=true; want silence", ev.Kind)
	case <-time.After(100 * time.Millisecond):
		// Expected — frame was dropped.
	}
	stats := r.Stats()
	if stats.FramesBadFCS != 1 {
		t.Errorf("FramesBadFCS = %d, want 1", stats.FramesBadFCS)
	}
	if stats.FramesEmitted != 0 {
		t.Errorf("FramesEmitted = %d, want 0 (CRC drop)", stats.FramesEmitted)
	}
}

func TestReceiverPublishesBadFCSByDefault(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus})

	info := []byte("!4903.50N/07201.75W-Test")
	body := buildAX25Body("APRS", 0, "W1AW", 9, info)
	body[18] ^= 0x80
	bits := wrapHDLC(body)
	pushAll(r, bits)

	pkt := awaitPacket(t, sub)
	if pkt.FCSOK {
		t.Error("FCSOK = true on corrupted frame")
	}
	stats := r.Stats()
	if stats.FramesBadFCS != 1 || stats.FramesEmitted != 1 {
		t.Errorf("stats = %+v, want BadFCS=1, Emitted=1", stats)
	}
}

func TestReceiverStatsCountAcrossMultipleFrames(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus})

	for i := 0; i < 3; i++ {
		body := buildAX25Body("APRS", 0, "W1AW", uint8(i), []byte("!4903.50N/07201.75W-"))
		bits := wrapHDLC(body)
		pushAll(r, bits)
	}

	for i := 0; i < 3; i++ {
		_ = awaitPacket(t, sub)
	}
	stats := r.Stats()
	if stats.FramesIn != 3 || stats.FramesParsed != 3 || stats.FramesEmitted != 3 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestReceiverDropsStructurallyInvalidBodies(t *testing.T) {
	bus, sub := newTestBus(t)
	r := New(Options{Bus: bus})

	// Build a body with 18 bytes of garbage that doesn't end
	// with a complete address chain. The HDLC framer accepts it
	// (MinFrameBytes = 18); the AX.25 parser then rejects the
	// malformed address chain. Verifies the receiver counts the
	// framer's emit but doesn't publish.
	garbage := make([]byte, ax25.MinFrameBytes)
	bits := wrapHDLC(garbage)
	pushAll(r, bits)

	// FramesIn counts the HDLC-level emit; FramesParsed should
	// be 0 because the AX.25 parser will reject the malformed
	// address chain.
	select {
	case ev := <-sub.C:
		t.Errorf("got event %q for malformed frame; want silence", ev.Kind)
	case <-time.After(100 * time.Millisecond):
	}
	stats := r.Stats()
	if stats.FramesIn != 1 {
		t.Errorf("FramesIn = %d, want 1", stats.FramesIn)
	}
	if stats.FramesParsed != 0 {
		t.Errorf("FramesParsed = %d, want 0 (malformed address chain)", stats.FramesParsed)
	}
}
