package rigctld

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeController is a minimal Controller for tests. Tracks SetFreq
// calls and lets tests inject errors.
type fakeController struct {
	mu       sync.Mutex
	freq     atomic.Uint32
	setErr   error
	getErr   error
	setCalls atomic.Int32
}

func (f *fakeController) Serial() string {
	return "test-rig"
}

func (f *fakeController) Freq() (uint32, error) {
	f.mu.Lock()
	err := f.getErr
	f.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return f.freq.Load(), nil
}

func (f *fakeController) SetFreq(hz uint32) error {
	f.setCalls.Add(1)
	f.mu.Lock()
	err := f.setErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	f.freq.Store(hz)
	return nil
}

// startServer spawns a Server bound to a kernel-assigned port and
// returns it plus the bound address. Cleanup runs Close + waits for
// the Run goroutine to exit.
func startServer(t *testing.T, ctrl Controller) (*Server, string) {
	t.Helper()
	s, err := New("127.0.0.1:0", ctrl, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	// Wait briefly for the listener to bind so BoundAddr is non-empty.
	deadline := time.Now().Add(time.Second)
	for s.BoundAddr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("server did not bind within 1s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	addr := s.BoundAddr()

	t.Cleanup(func() {
		cancel()
		_ = s.Close()
		select {
		case <-runErr:
		case <-time.After(time.Second):
		}
	})
	return s, addr
}

// roundTrip dials the server, sends a single line, and returns the
// server's response (lines accumulated until either an RPRT line
// arrives or the connection idles for 100 ms).
func roundTrip(t *testing.T, addr, cmd string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte(cmd + "\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Read with a per-line timeout. rigctld replies are short; stop
	// after the first RPRT line, the first non-RPRT line followed by
	// a brief idle, or 2s of total time.
	br := bufio.NewReader(conn)
	var lines []string
	for {
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		line, err := br.ReadString('\n')
		if line != "" {
			lines = append(lines, strings.TrimRight(line, "\r\n"))
			if strings.HasPrefix(line, "RPRT ") {
				return strings.Join(lines, "\n")
			}
		}
		if err != nil {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func TestNewRequiresController(t *testing.T) {
	if _, err := New("", nil, nil); err == nil {
		t.Fatal("New with nil controller: want error, got nil")
	}
}

func TestSetFreqShortAndLongForms(t *testing.T) {
	ctrl := &fakeController{}
	_, addr := startServer(t, ctrl)

	if got := roundTrip(t, addr, "F 851012500"); got != "RPRT 0" {
		t.Errorf("F 851012500 → %q, want RPRT 0", got)
	}
	if ctrl.freq.Load() != 851_012_500 {
		t.Errorf("ctrl.freq = %d, want 851012500", ctrl.freq.Load())
	}

	if got := roundTrip(t, addr, "set_freq 462562500"); got != "RPRT 0" {
		t.Errorf("set_freq → %q, want RPRT 0", got)
	}
	if ctrl.freq.Load() != 462_562_500 {
		t.Errorf("ctrl.freq = %d, want 462562500", ctrl.freq.Load())
	}
}

func TestGetFreqReturnsCurrentValue(t *testing.T) {
	ctrl := &fakeController{}
	ctrl.freq.Store(853_500_000)
	_, addr := startServer(t, ctrl)

	got := roundTrip(t, addr, "f")
	if got != "853500000" {
		t.Errorf("f → %q, want 853500000", got)
	}
}

func TestSetFreqBackendErrorReturnsRPRTNeg8(t *testing.T) {
	ctrl := &fakeController{setErr: errBackend()}
	_, addr := startServer(t, ctrl)

	got := roundTrip(t, addr, "F 100000000")
	if got != "RPRT -8" {
		t.Errorf("F when backend fails → %q, want RPRT -8", got)
	}
}

func TestSetFreqBadArgReturnsRPRTNeg1(t *testing.T) {
	ctrl := &fakeController{}
	_, addr := startServer(t, ctrl)

	if got := roundTrip(t, addr, "F notanumber"); got != "RPRT -1" {
		t.Errorf("F notanumber → %q, want RPRT -1", got)
	}
	if got := roundTrip(t, addr, "F"); got != "RPRT -1" {
		t.Errorf("F (no arg) → %q, want RPRT -1", got)
	}
}

func TestModeAndVFOQueries(t *testing.T) {
	_, addr := startServer(t, &fakeController{})

	if got := roundTrip(t, addr, "m"); got != "FM\n12500" {
		t.Errorf("m → %q, want \"FM\\n12500\"", got)
	}
	if got := roundTrip(t, addr, "M FM 12500"); got != "RPRT 0" {
		t.Errorf("M → %q, want RPRT 0", got)
	}
	if got := roundTrip(t, addr, "v"); got != "VFOA" {
		t.Errorf("v → %q, want VFOA", got)
	}
	if got := roundTrip(t, addr, "V VFOA"); got != "RPRT 0" {
		t.Errorf("V VFOA → %q, want RPRT 0", got)
	}
	if got := roundTrip(t, addr, "V VFOB"); got != "RPRT -11" {
		t.Errorf("V VFOB (unsupported) → %q, want RPRT -11", got)
	}
}

func TestPTTRejectsTransmit(t *testing.T) {
	_, addr := startServer(t, &fakeController{})
	if got := roundTrip(t, addr, "t"); got != "0" {
		t.Errorf("get_ptt → %q, want 0", got)
	}
	if got := roundTrip(t, addr, "T 0"); got != "RPRT 0" {
		t.Errorf("set_ptt 0 → %q, want RPRT 0", got)
	}
	if got := roundTrip(t, addr, "T 1"); got != "RPRT -11" {
		t.Errorf("set_ptt 1 → %q, want RPRT -11 (RX-only backend)", got)
	}
}

func TestChkVFOReportsSingleVFO(t *testing.T) {
	_, addr := startServer(t, &fakeController{})
	if got := roundTrip(t, addr, "chk_vfo"); got != "CHKVFO 0" {
		t.Errorf("chk_vfo → %q, want CHKVFO 0", got)
	}
}

func TestUnknownCommandReturnsRPRTNeg1(t *testing.T) {
	_, addr := startServer(t, &fakeController{})
	if got := roundTrip(t, addr, "unsupported_xyzzy"); got != "RPRT -1" {
		t.Errorf("unknown cmd → %q, want RPRT -1", got)
	}
}

func TestQuitTerminatesConnection(t *testing.T) {
	_, addr := startServer(t, &fakeController{})
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("q\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// After q the server closes the connection; subsequent Read
	// should return EOF immediately.
	buf := make([]byte, 16)
	_, err = conn.Read(buf)
	if err == nil {
		t.Error("connection still open after q, want EOF")
	}
}

func TestDumpStateContainsExpectedShape(t *testing.T) {
	_, addr := startServer(t, &fakeController{})
	got := roundTrip(t, addr, "dump_state")
	// First line is the rigctld protocol version (must be 0). Second
	// line is the ITU region (2). Anything else and Hamlib clients
	// reject the handshake.
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("dump_state body too short: %q", got)
	}
	if lines[0] != "0" {
		t.Errorf("dump_state line 0 = %q, want \"0\" (protocol version)", lines[0])
	}
	if lines[1] != "2" {
		t.Errorf("dump_state line 1 = %q, want \"2\" (ITU region)", lines[1])
	}
}

func TestMultipleCommandsOnOneConnection(t *testing.T) {
	ctrl := &fakeController{}
	_, addr := startServer(t, ctrl)

	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	// Pipeline two commands and read both responses.
	if _, err := conn.Write([]byte("F 100000000\nf\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	br := bufio.NewReader(conn)
	first, _ := br.ReadString('\n')
	second, _ := br.ReadString('\n')
	if strings.TrimRight(first, "\r\n") != "RPRT 0" {
		t.Errorf("first response = %q, want RPRT 0", first)
	}
	if strings.TrimRight(second, "\r\n") != "100000000" {
		t.Errorf("second response = %q, want 100000000", second)
	}
}

// errBackend returns a sentinel error used to simulate a controller
// rejecting SetFreq (e.g. SDR returned an error). Kept as a helper so
// the test signal of the error matters more than its exact type.
type backendErr struct{}

func (backendErr) Error() string { return "backend failure" }

func errBackend() error { return backendErr{} }
