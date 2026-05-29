package beast

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/storage"
)

// buildBEAST encodes one Mode-S frame as a BEAST envelope (frame
// marker + type + 6-byte timestamp + 1-byte signal + payload).
// 0x1A bytes inside the body are NOT escaped — callers pass
// hand-curated test vectors that don't contain 0x1A so escaping
// isn't needed.
func buildBEAST(t byte, ts uint64, signal byte, payload []byte) []byte {
	out := make([]byte, 0, 2+6+1+len(payload))
	out = append(out, 0x1A, t)
	for i := 5; i >= 0; i-- {
		out = append(out, byte(ts>>uint(i*8)))
	}
	out = append(out, signal)
	out = append(out, payload...)
	return out
}

// decodeHex panics on bad hex — tests pass hand-curated literals.
func decodeHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestReadFrameModeSLong(t *testing.T) {
	payload := decodeHex("8D4840D6202CC371C32CE0576098")
	wire := buildBEAST(0x33, 0x123456789ABC, 200, payload)
	r := bufio.NewReader(bytes.NewReader(wire))
	f, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if f.Type != 0x33 {
		t.Errorf("Type = 0x%02X, want 0x33", f.Type)
	}
	if f.Timestamp != 0x123456789ABC {
		t.Errorf("Timestamp = 0x%X, want 0x123456789ABC", f.Timestamp)
	}
	if f.Signal != 200 {
		t.Errorf("Signal = %d, want 200", f.Signal)
	}
	if !bytes.Equal(f.Payload, payload) {
		t.Errorf("Payload = %x, want %x", f.Payload, payload)
	}
}

func TestReadFrameModeSShort(t *testing.T) {
	payload := []byte{0x58, 0xAA, 0xBB, 0xCC, 0x00, 0x00, 0x00}
	wire := buildBEAST(0x32, 0, 100, payload)
	r := bufio.NewReader(bytes.NewReader(wire))
	f, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if f.Type != 0x32 || len(f.Payload) != 7 {
		t.Errorf("Type=0x%02X payloadLen=%d, want 0x32 / 7", f.Type, len(f.Payload))
	}
}

func TestReadFrameUnescapesStuffed1A(t *testing.T) {
	// Hand-craft a long frame whose payload contains 0x1A.
	// Build with escape sequences: each in-body 0x1A → 0x1A 0x1A.
	payload := []byte{0x1A, 0x00, 0x1A, 0x42, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	header := []byte{0x1A, 0x33}
	tsAndSignal := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xAA}
	// Apply byte-stuffing to body bytes (timestamp + signal +
	// payload) so any 0x1A in the body gets escaped.
	stuffed := []byte{}
	for _, b := range append(tsAndSignal, payload...) {
		stuffed = append(stuffed, b)
		if b == 0x1A {
			stuffed = append(stuffed, 0x1A)
		}
	}
	wire := append(header, stuffed...)
	r := bufio.NewReader(bytes.NewReader(wire))
	f, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(f.Payload, payload) {
		t.Errorf("Unstuffed payload = %x, want %x", f.Payload, payload)
	}
}

func TestReadFrameReturnsEOFOnCleanClose(t *testing.T) {
	r := bufio.NewReader(bytes.NewReader(nil))
	_, err := ReadFrame(r)
	if !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want io.EOF", err)
	}
}

func TestReadFrameSkipsGarbageUntilSync(t *testing.T) {
	// Pre-pad with non-0x1A garbage; ReadFrame should hunt for
	// the first 0x1A and frame from there.
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x12, 0x34}
	good := buildBEAST(0x32, 0, 0, []byte{0x58, 0, 0, 0, 0, 0, 0})
	wire := append(garbage, good...)
	r := bufio.NewReader(bytes.NewReader(wire))
	f, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if f.Type != 0x32 {
		t.Errorf("Type = 0x%02X, want 0x32", f.Type)
	}
}

func TestNewRejectsBadOptions(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	if _, err := New(Options{}); err == nil {
		t.Error("New without Bus: want error")
	}
	if _, err := New(Options{Bus: bus}); err == nil {
		t.Error("New without Addr: want error")
	}
}

// TestClientPublishesDecodedFrames runs a fake BEAST server on a
// loopback socket, feeds it the dump1090 canonical identification
// + airborne-position pair, and asserts the bus event arrives
// with the right ICAO + globally-decoded lat/lon.
func TestClientPublishesDecodedFrames(t *testing.T) {
	bus := events.NewBus(64)
	sub := bus.Subscribe()
	t.Cleanup(func() {
		sub.Close()
		bus.Close()
	})

	// Start a fake BEAST server that emits 3 frames then EOFs.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Identification frame (TC 4 → KLM1023 / ICAO 4840D6).
		_, _ = conn.Write(buildBEAST(0x33, 1, 200,
			decodeHex("8D4840D6202CC371C32CE0576098")))
		// CPR pair (ICAO 40621D, odd first then even).
		_, _ = conn.Write(buildBEAST(0x33, 2, 210,
			decodeHex("8D40621D58C386435CC412692AD6")))
		_, _ = conn.Write(buildBEAST(0x33, 3, 210,
			decodeHex("8D40621D58C382D690C8AC2863A7")))
	}()

	c, err := New(Options{
		Addr:           ln.Addr().String(),
		Bus:            bus,
		SourceName:     "test",
		ReconnectDelay: 100 * time.Millisecond,
		ReadDeadline:   200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	// Collect events; expect at least the identification + a
	// position with HasPosition=true.
	gotIdent := false
	gotPos := false
	deadline := time.After(2 * time.Second)
	for !gotIdent || !gotPos {
		select {
		case ev := <-sub.C:
			if ev.Kind != events.KindAircraftReport {
				continue
			}
			rep, ok := ev.Payload.(storage.AircraftReport)
			if !ok {
				continue
			}
			switch rep.Kind {
			case "ident":
				if rep.ICAO == 0x4840D6 && rep.Callsign == "KLM1023" {
					gotIdent = true
				}
			case "airborne-pos":
				if rep.ICAO == 0x40621D && rep.HasPosition {
					// Verify lat/lon match the canonical
					// dump1090 reference.
					if rep.Latitude < 52.25 || rep.Latitude > 52.26 {
						t.Errorf("Latitude = %f", rep.Latitude)
					}
					if rep.Longitude < 3.91 || rep.Longitude > 3.93 {
						t.Errorf("Longitude = %f", rep.Longitude)
					}
					gotPos = true
				}
			}
		case <-deadline:
			t.Fatalf("timeout — gotIdent=%v gotPos=%v stats=%+v",
				gotIdent, gotPos, c.Stats())
		}
	}
}

func TestPayloadLenSpotChecks(t *testing.T) {
	cases := map[byte]int{
		0x31: 2,
		0x32: 7,
		0x33: 14,
		0x00: 0,
		0xFF: 0,
	}
	for t1, want := range cases {
		if got := payloadLen(t1); got != want {
			t.Errorf("payloadLen(0x%02X) = %d, want %d", t1, got, want)
		}
	}
}
