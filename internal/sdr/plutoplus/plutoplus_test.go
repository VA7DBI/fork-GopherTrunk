package plutoplus

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type fakeServer struct {
	t  *testing.T
	ln net.Listener

	mu   sync.Mutex
	cmds []cmdRecord
	feed chan []byte
	conn net.Conn
}

type cmdRecord struct {
	Op    uint8
	Param uint32
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	s := &fakeServer{t: t, ln: ln, feed: make(chan []byte, 32)}
	go s.run()
	t.Cleanup(s.close)
	return s
}

func (s *fakeServer) Addr() string { return s.ln.Addr().String() }

func (s *fakeServer) Feed(b []byte) { s.feed <- b }

func (s *fakeServer) Commands() []cmdRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]cmdRecord, len(s.cmds))
	copy(out, s.cmds)
	return out
}

func (s *fakeServer) close() {
	_ = s.ln.Close()
	s.mu.Lock()
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.mu.Unlock()
}

func (s *fakeServer) run() {
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	var hdr [12]byte
	copy(hdr[:4], magic[:])
	binary.BigEndian.PutUint32(hdr[4:8], 5)
	binary.BigEndian.PutUint32(hdr[8:12], 29)
	if _, err := conn.Write(hdr[:]); err != nil {
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 5)
		for {
			if _, err := io.ReadFull(conn, buf); err != nil {
				return
			}
			s.mu.Lock()
			s.cmds = append(s.cmds, cmdRecord{Op: buf[0], Param: binary.BigEndian.Uint32(buf[1:])})
			s.mu.Unlock()
		}
	}()

	for {
		select {
		case <-done:
			return
		case b, ok := <-s.feed:
			if !ok {
				_ = conn.Close()
				return
			}
			if _, err := conn.Write(b); err != nil {
				return
			}
		}
	}
}

func TestName(t *testing.T) {
	if got := New(nil, nil).Name(); got != driverName {
		t.Fatalf("Name() = %q, want %q", got, driverName)
	}
}

func TestEnumerateNoSpecsAbsentPlutoIsSilent(t *testing.T) {
	infos, err := New(nil, nil).Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("len(infos) = %d, want 0", len(infos))
	}
}

func TestEnumerateConfiguredSpecs(t *testing.T) {
	infos, err := New([]Spec{{Addr: "127.0.0.1:1234", Serial: "PLUTO-1"}}, nil).Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("len(infos) = %d, want 1", len(infos))
	}
	if infos[0].Driver != driverName || infos[0].Serial != "PLUTO-1" {
		t.Fatalf("info = %+v", infos[0])
	}
}

func TestEnumerateUSBDefaultAddr(t *testing.T) {
	infos, err := New([]Spec{{Transport: TransportUSB, Serial: "PLUTO-USB-1"}}, nil).Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("len(infos) = %d, want 1", len(infos))
	}
	if infos[0].Product != defaultProductName+" USB" {
		t.Fatalf("product = %q, want %q", infos[0].Product, defaultProductName+" USB")
	}
}

func TestResolveAddrUSBDefault(t *testing.T) {
	got, err := resolveAddr(Spec{Transport: TransportUSB})
	if err != nil {
		t.Fatalf("resolveAddr: %v", err)
	}
	if got != DefaultUSBAddr {
		t.Fatalf("resolveAddr usb default = %q, want %q", got, DefaultUSBAddr)
	}
}

func TestResolveAddrInvalidTransport(t *testing.T) {
	if _, err := resolveAddr(Spec{Transport: "bluetooth", Addr: "x"}); err == nil {
		t.Fatal("resolveAddr invalid transport: want error")
	}
}

func TestOpenAndClose(t *testing.T) {
	srv := newFakeServer(t)
	d := New([]Spec{{Addr: srv.Addr(), Serial: "PLUTO-001"}}, nil)
	dev, err := d.Open(0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if dev.Info().Serial != "PLUTO-001" {
		t.Fatalf("Info().Serial = %q", dev.Info().Serial)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpenUSBTransportWithExplicitAddr(t *testing.T) {
	srv := newFakeServer(t)
	d := New([]Spec{{Transport: TransportUSB, Addr: srv.Addr(), Serial: "PLUTO-USB-001"}}, nil)
	dev, err := d.Open(0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if dev.Info().Product != defaultProductName+" USB" {
		t.Fatalf("Info().Product = %q, want %q", dev.Info().Product, defaultProductName+" USB")
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpenNoSpecsUsesImplicitUSB(t *testing.T) {
	srv := newFakeServer(t)
	withDefaultUSBAddr(t, srv.Addr())
	d := New(nil, nil)
	dev, err := d.Open(0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if dev.Info().Product != defaultProductName+" USB" {
		t.Fatalf("Info().Product = %q, want %q", dev.Info().Product, defaultProductName+" USB")
	}
	_ = dev.Close()
}

func withDefaultUSBAddr(t *testing.T, addr string) {
	t.Helper()
	prev := DefaultUSBAddr
	DefaultUSBAddr = addr
	t.Cleanup(func() { DefaultUSBAddr = prev })
}

func TestCommands(t *testing.T) {
	srv := newFakeServer(t)
	d := New([]Spec{{Addr: srv.Addr(), Serial: "PLUTO-001"}}, nil)
	dev, err := d.Open(0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dev.Close()

	if err := dev.SetCenterFreq(851012500); err != nil {
		t.Fatalf("SetCenterFreq: %v", err)
	}
	if err := dev.SetSampleRate(2048000); err != nil {
		t.Fatalf("SetSampleRate: %v", err)
	}
	if err := dev.SetGain(320); err != nil {
		t.Fatalf("SetGain(manual): %v", err)
	}
	if err := dev.SetGain(-1); err != nil {
		t.Fatalf("SetGain(auto): %v", err)
	}
	if err := dev.SetPPM(10); err != nil {
		t.Fatalf("SetPPM: %v", err)
	}
	if err := dev.SetBiasTee(true); err != nil {
		t.Fatalf("SetBiasTee: %v", err)
	}

	waitFor(t, 500*time.Millisecond, func() bool { return len(srv.Commands()) >= 7 })
	got := srv.Commands()
	want := []cmdRecord{
		{cmdSetFreq, 851012500},
		{cmdSetSampleRate, 2048000},
		{cmdSetGainMode, 1},
		{cmdSetGain, 320},
		{cmdSetGainMode, 0},
		{cmdSetFreqCorrPPM, uint32(int32(10))},
		{cmdSetBiasTee, 1},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cmd[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestStreamIQ(t *testing.T) {
	srv := newFakeServer(t)
	d := New([]Spec{{Addr: srv.Addr(), Serial: "PLUTO-001"}}, nil)
	dev, err := d.Open(0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dev.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := dev.StreamIQ(ctx)
	if err != nil {
		t.Fatalf("StreamIQ: %v", err)
	}

	body := make([]byte, 8192*2)
	for i := 0; i < len(body); i += 2 {
		body[i] = 127
		body[i+1] = 128
	}
	srv.Feed(body)

	select {
	case v := <-ch:
		if len(v) != 8192 {
			t.Fatalf("len(samples) = %d, want 8192", len(v))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no IQ chunk received")
	}
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
