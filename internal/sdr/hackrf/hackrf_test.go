package hackrf

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/sdr"
	"github.com/MattCheramie/GopherTrunk/internal/sdr/rtlsdr/usb"
)

// withDevice returns a Device whose USB transport is a freshly-built
// MockTransport so each test can populate its own Script.
func withDevice(t *testing.T) (*Device, *usb.MockTransport) {
	t.Helper()
	mt := usb.NewMockTransport()
	return &Device{t: mt, info: sdr.Info{Driver: driverName, Serial: "test"}}, mt
}

func TestDriverEnumerateAndOpen(t *testing.T) {
	enum := &usb.MockEnumerator{
		Devices: []usb.Descriptor{
			{Bus: 1, Address: 7, VID: vidHackRF, PID: pidHackRFOne, Serial: "ABC", Product: "HackRF One", Path: "mock/1"},
		},
	}
	drv := New(enum)
	infos, err := drv.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(infos) != 1 || infos[0].Serial != "ABC" || infos[0].Driver != driverName {
		t.Fatalf("Enumerate = %+v", infos)
	}
	dev, err := drv.Open(0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if dev.Info().Serial != "ABC" {
		t.Fatalf("Info.Serial = %q, want ABC", dev.Info().Serial)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestDriverOpenRejectsBadIndex(t *testing.T) {
	enum := &usb.MockEnumerator{Devices: nil}
	drv := New(enum)
	if _, err := drv.Open(0); err == nil {
		t.Fatal("Open on empty driver should error")
	}
}

func TestSetCenterFreqEncoding(t *testing.T) {
	dev, mt := withDevice(t)
	mt.Script = []usb.CtrlExchange{{
		BRequest: reqSetFreq, WValue: 0, WIndex: 0,
		Data: encodeFreq(851_012_500),
	}}
	if err := dev.SetCenterFreq(851_012_500); err != nil {
		t.Fatalf("SetCenterFreq: %v", err)
	}
	if mt.Err != nil {
		t.Fatalf("transport error: %v", mt.Err)
	}
}

func encodeFreq(hz uint32) []byte {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint32(buf[0:4], hz/1_000_000)
	binary.LittleEndian.PutUint32(buf[4:8], hz%1_000_000)
	return buf
}

func TestSetSampleRateProgramsFilter(t *testing.T) {
	dev, mt := withDevice(t)
	rateBytes := make([]byte, 8)
	binary.LittleEndian.PutUint32(rateBytes[0:4], 8_000_000)
	binary.LittleEndian.PutUint32(rateBytes[4:8], 1)
	mt.Script = []usb.CtrlExchange{
		{BRequest: reqSampleRateSet, Data: rateBytes},
		{BRequest: reqBasebandFilterBwSet, WValue: uint16(6_000_000 & 0xFFFF), WIndex: uint16(6_000_000 >> 16)},
	}
	if err := dev.SetSampleRate(8_000_000); err != nil {
		t.Fatalf("SetSampleRate: %v", err)
	}
	if mt.Err != nil {
		t.Fatalf("transport error: %v", mt.Err)
	}
}

func TestSplitGain(t *testing.T) {
	cases := []struct {
		tenthDB          int
		wantLNA, wantVGA int
	}{
		{-1, defaultLNAGainDB, defaultVGAGainDB},
		{0, 0, 0},
		{160, 16, 0},   // 16 dB → all in LNA
		{180, 16, 2},   // 16 dB LNA + 2 dB VGA
		{300, 24, 6},   // 24 + 6 = 30 dB
		{900, 40, 50},  // clamped: 40 dB LNA, 50 dB VGA
		{1500, 40, 62}, // both saturated
	}
	for _, c := range cases {
		lna, vga, amp := splitGain(c.tenthDB)
		if lna != c.wantLNA || vga != c.wantVGA || amp {
			t.Errorf("splitGain(%d) = (%d,%d,%v), want (%d,%d,false)",
				c.tenthDB, lna, vga, amp, c.wantLNA, c.wantVGA)
		}
	}
}

func TestSetGainIssuesAMPLNAVGA(t *testing.T) {
	dev, mt := withDevice(t)
	mt.Script = []usb.CtrlExchange{
		{BRequest: reqAmpEnable, WValue: 0},
		{In: true, BRequest: reqSetLNAGain, WIndex: 16, Reply: []byte{1}, N: 1},
		{In: true, BRequest: reqSetVGAGain, WIndex: 20, Reply: []byte{1}, N: 1},
	}
	if err := dev.SetGain(-1); err != nil {
		t.Fatalf("SetGain: %v", err)
	}
	if mt.Err != nil {
		t.Fatalf("transport error: %v", mt.Err)
	}
}

func TestSetBiasTeeRoundTrips(t *testing.T) {
	dev, mt := withDevice(t)
	mt.Script = []usb.CtrlExchange{
		{BRequest: reqAntennaEnable, WValue: 1},
		{BRequest: reqAntennaEnable, WValue: 0},
	}
	if err := dev.SetBiasTee(true); err != nil {
		t.Fatalf("SetBiasTee(on): %v", err)
	}
	if err := dev.SetBiasTee(false); err != nil {
		t.Fatalf("SetBiasTee(off): %v", err)
	}
	if mt.Err != nil {
		t.Fatalf("transport: %v", mt.Err)
	}
}

func TestDecodeInt8IQ(t *testing.T) {
	// Three samples: (+127, -127), (0, 0), (-128, +64).
	buf := []byte{127, 0xFF - 126, 0, 0, 0x80, 64}
	got := decodeInt8IQ(buf)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if real(got[0]) <= 0.9 || imag(got[0]) >= -0.9 {
		t.Errorf("sample 0 = (%f,%f); expected near (+1,-1)", real(got[0]), imag(got[0]))
	}
	if real(got[1]) != 0 || imag(got[1]) != 0 {
		t.Errorf("sample 1 = (%f,%f); expected (0,0)", real(got[1]), imag(got[1]))
	}
	if real(got[2]) >= -0.9 || imag(got[2]) < 0.4 || imag(got[2]) > 0.6 {
		t.Errorf("sample 2 = (%f,%f); expected near (-1, +0.5)", real(got[2]), imag(got[2]))
	}
}

func TestStreamIQFlipsModeAndStopsOnCancel(t *testing.T) {
	dev, mt := withDevice(t)
	mt.Script = []usb.CtrlExchange{
		{BRequest: reqSetTransceiverMode, WValue: transceiverModeReceive},
		{BRequest: reqSetTransceiverMode, WValue: transceiverModeOff},
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := dev.StreamIQ(ctx)
	if err != nil {
		t.Fatalf("StreamIQ: %v", err)
	}
	cancel()
	// Drain until the chain shuts down.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := <-ch; !ok {
			break
		}
	}
	if mt.Err != nil {
		t.Fatalf("transport error: %v", mt.Err)
	}
}
