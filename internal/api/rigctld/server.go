// Package rigctld implements a subset of Hamlib's rigctld TCP wire
// protocol so external amateur-radio tooling (Cloudlog, N1MM, GridTracker,
// PSTRotator, satellite trackers, logging programs) can read and set the
// frequency of one of GopherTrunk's SDRs over the network.
//
// rigctld is a text-line protocol — each line is one command, the
// server replies with one or more text lines terminated by an
// "RPRT <errno>\n" report code line ("RPRT 0" on success). Many
// clients use both the short single-character form (e.g. "F 851012500")
// and the long form ("set_freq 851012500"); both are accepted here.
//
// The implementation targets the ~10 commands real clients actually
// send. Anything else returns RPRT -1 (Hamlib's "command not
// supported" code), matching how real rigctld handles capability
// limits on minimal backends. The goal is interop with logging
// programs and frequency trackers — not full Hamlib coverage.
//
// GopherTrunk's "rig" is one SDR in the pool, identified by serial.
// The controller abstraction (Controller interface) lets the daemon
// wire whichever SDR makes sense (the control SDR, by default); the
// server itself stays free of sdr.Pool details so it can be unit-
// tested with a fake Controller.
package rigctld

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Controller is the subset of the daemon a rigctld server needs.
// Implementations hide whichever sdr.Device or iqtap.Broker the
// daemon decides to expose. Freq is in Hz; SetFreq returns an error
// when the device rejects the value or no rig is wired.
type Controller interface {
	Serial() string
	Freq() (uint32, error)
	SetFreq(hz uint32) error
}

// DefaultListenAddr is the address the server binds when callers
// don't override it. Matches the Hamlib rigctld default port.
const DefaultListenAddr = "127.0.0.1:4532"

// Server is one TCP listener that accepts rigctld clients. A daemon
// spawns one Server per logical rig; today GopherTrunk wires exactly
// one (the control SDR's frequency).
type Server struct {
	addr string
	ctrl Controller
	log  *slog.Logger

	mu       sync.Mutex
	listener net.Listener
	closed   bool
}

// New constructs a Server. addr defaults to DefaultListenAddr when
// empty. ctrl must be non-nil — the server doesn't run without
// something to control.
func New(addr string, ctrl Controller, log *slog.Logger) (*Server, error) {
	if ctrl == nil {
		return nil, errors.New("rigctld: Controller is required")
	}
	if log == nil {
		log = slog.Default()
	}
	if addr == "" {
		addr = DefaultListenAddr
	}
	return &Server{addr: addr, ctrl: ctrl, log: log}, nil
}

// Run binds the listener and serves until ctx cancels or Close fires.
// Returns the underlying listener error (typically net.ErrClosed on
// clean shutdown, which is converted to nil).
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("rigctld: listen %s: %w", s.addr, err)
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()
	s.log.Info("rigctld: listening", "addr", ln.Addr().String(), "rig_serial", s.ctrl.Serial())

	go func() {
		<-ctx.Done()
		_ = s.shutdown()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("rigctld: accept: %w", err)
		}
		go s.handle(ctx, conn)
	}
}

// BoundAddr reports the listener's bound address. Empty before Run
// has bound or after Close. Useful when the caller configured a
// ":0" port and needs the kernel-assigned value.
func (s *Server) BoundAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Close stops accepting and closes the listener. Existing connections
// drain naturally on their next read.
func (s *Server) Close() error { return s.shutdown() }

func (s *Server) shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.listener == nil {
		s.closed = true
		return nil
	}
	s.closed = true
	return s.listener.Close()
}

// handle services one client connection. rigctld is request/response:
// read a line, dispatch, write a response. Clients can pipeline
// commands but most send one at a time; we don't batch responses.
func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	s.log.Debug("rigctld: client connected", "remote", conn.RemoteAddr())

	br := bufio.NewReader(conn)
	bw := bufio.NewWriter(conn)
	for {
		// Per-line read timeout so a wedged client doesn't pin a
		// goroutine forever.
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		line, err := br.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				s.log.Debug("rigctld: read", "remote", conn.RemoteAddr(), "err", err)
			}
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if s.dispatch(line, bw) {
			return // client asked to quit
		}
		if err := bw.Flush(); err != nil {
			s.log.Debug("rigctld: write", "remote", conn.RemoteAddr(), "err", err)
			return
		}
	}
}

// dispatch parses and runs one command line. Returns true if the
// client requested termination (q / Q / exit).
func (s *Server) dispatch(line string, w *bufio.Writer) bool {
	cmd, args := splitCmd(line)
	switch cmd {
	case "q", "Q", "exit":
		return true

	case "F", "set_freq":
		if len(args) < 1 {
			rprt(w, -1)
			return false
		}
		hz, err := strconv.ParseUint(args[0], 10, 32)
		if err != nil {
			rprt(w, -1)
			return false
		}
		if err := s.ctrl.SetFreq(uint32(hz)); err != nil {
			s.log.Debug("rigctld: SetFreq", "hz", hz, "err", err)
			rprt(w, -8) // RIG_EPROTO — closest match for a backend refusal
			return false
		}
		rprt(w, 0)

	case "f", "get_freq":
		hz, err := s.ctrl.Freq()
		if err != nil {
			rprt(w, -8)
			return false
		}
		fmt.Fprintf(w, "%d\n", hz)

	case "M", "set_mode":
		// Scanner backend: accept any mode + passband, ack OK. Real
		// clients (Cloudlog, GridTracker) call this on connect with
		// "FM 12500" or "PKT 0" — refusing breaks their handshake
		// even though the value doesn't influence our pipeline.
		rprt(w, 0)

	case "m", "get_mode":
		// Hand back "FM" with a 12.5 kHz passband — a reasonable
		// proxy for what GopherTrunk is doing on the control channel.
		fmt.Fprintf(w, "FM\n12500\n")

	case "V", "set_vfo":
		// Single-VFO backend: any set_vfo to VFOA / Main / currVFO
		// succeeds, anything else is reported as unsupported.
		if len(args) < 1 {
			rprt(w, -1)
			return false
		}
		switch strings.ToUpper(args[0]) {
		case "VFOA", "MAIN", "CURRVFO":
			rprt(w, 0)
		default:
			rprt(w, -11) // RIG_ENTARGET — VFO not available
		}

	case "v", "get_vfo":
		fmt.Fprintf(w, "VFOA\n")

	case "T", "set_ptt":
		// RX-only backend. Accept set_ptt 0; reject set_ptt 1 — there
		// is no transmitter behind the SDR.
		if len(args) < 1 {
			rprt(w, -1)
			return false
		}
		v, err := strconv.Atoi(args[0])
		if err != nil {
			rprt(w, -1)
			return false
		}
		if v == 0 {
			rprt(w, 0)
		} else {
			rprt(w, -11) // RIG_ENTARGET — not a transmitter
		}

	case "t", "get_ptt":
		fmt.Fprintf(w, "0\n")

	case "chk_vfo":
		// rigctld -o 0 by default; clients that probe chk_vfo expect
		// "CHKVFO 0" when the daemon is single-VFO.
		fmt.Fprintf(w, "CHKVFO 0\n")

	case "dump_state":
		// Minimal but well-formed state dump. Real rigctl(1) clients
		// (Cloudlog) and Hamlib's own rigctl call this on connect to
		// learn capability bounds. The shape below matches a stripped-
		// down "Dummy" rig: protocol version 0, RX-only, single VFO,
		// 1 Hz-7 GHz tuning range, all-modes mask, zero TX ranges.
		writeDumpState(w)

	default:
		// Unknown command: rigctld returns RPRT -1, which clients
		// uniformly treat as "command not supported". This is the
		// expected behaviour for a minimal backend.
		rprt(w, -1)
	}
	return false
}

// writeDumpState emits the rigctld dump_state response body. The
// format is line-oriented and positional; rigctl(1) parses it as
// "protocol-version itu-region rx-range-list tx-range-list
// tuning-step-list filter-list max-rit max-xit max-ifshift
// announces preamps attenuators has-get-func has-set-func has-get-level
// has-set-level has-get-parm has-set-parm". A capability of 0 across
// the board is the safe stub Hamlib's dummy rig uses.
func writeDumpState(w *bufio.Writer) {
	const body = `0
2
1000 7000000000 0x1ff -1 -1 0x10000003 0x3
0 0 0 0 0 0 0
0 0 0 0 0 0 0
0 0
0 0
0
0
0
0


0x0
0x0
0x0
0x0
0x0
0x0
`
	_, _ = w.WriteString(body)
}

func rprt(w *bufio.Writer, code int) {
	fmt.Fprintf(w, "RPRT %d\n", code)
}

// splitCmd extracts the leading command token and the remaining
// space-separated arguments. rigctld treats "set_freq 851012500" and
// "F 851012500" as equivalent — both surface here as cmd="set_freq"
// or "F", args=["851012500"].
func splitCmd(line string) (string, []string) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}
