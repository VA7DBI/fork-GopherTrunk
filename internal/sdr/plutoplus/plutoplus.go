// Package plutoplus implements an sdr.Driver for Pluto Plus receivers
// exposed over a TCP IQ endpoint compatible with the rtl_tcp wire shape
// (12-byte RTL0 header + command channel + u8 IQ stream).
package plutoplus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/sdr"
)

const driverName = "plutoplus"

var magic = [4]byte{'R', 'T', 'L', '0'}

const (
	cmdSetFreq         uint8 = 0x01
	cmdSetSampleRate   uint8 = 0x02
	cmdSetGainMode     uint8 = 0x03
	cmdSetGain         uint8 = 0x04
	cmdSetFreqCorrPPM  uint8 = 0x05
	cmdSetBiasTee      uint8 = 0x0e
	defaultProductName       = "Pluto Plus"
)

const DefaultConnectTimeout = 3 * time.Second
const autoProbeTimeout = 250 * time.Millisecond

type Stage string

const (
	StageDial      Stage = "dial"
	StageHandshake Stage = "handshake"
	StageCommand   Stage = "command"
	StageStream    Stage = "stream"
)

// TransportError classifies Pluto endpoint failures by operation stage.
type TransportError struct {
	Stage Stage
	Addr  string
	Op    string
	Err   error
}

func (e *TransportError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Op != "" {
		return fmt.Sprintf("plutoplus: %s %s (%s): %v", e.Stage, e.Op, e.Addr, e.Err)
	}
	return fmt.Sprintf("plutoplus: %s (%s): %v", e.Stage, e.Addr, e.Err)
}

func (e *TransportError) Unwrap() error { return e.Err }

func isStage(err error, stage Stage) bool {
	var te *TransportError
	if !errors.As(err, &te) {
		return false
	}
	return te.Stage == stage
}

const (
	TransportTCP = "tcp"
	TransportUSB = "usb"
)

// Pluto boards connected over USB commonly expose a USB-Ethernet
// interface at 192.168.2.1. The Pluto Plus endpoint process can
// listen on 1234 with an rtl_tcp-compatible wire shape.
var DefaultUSBAddr = "192.168.2.1:1234"

// Spec declares one Pluto Plus endpoint.
type Spec struct {
	Addr           string
	Serial         string
	Role           string
	Transport      string
	ConnectTimeout time.Duration
}

// Driver implements sdr.Driver for Pluto Plus.
type Driver struct {
	specs []Spec
	log   *slog.Logger
}

func New(specs []Spec, log *slog.Logger) *Driver {
	if log == nil {
		log = slog.Default()
	}
	return &Driver{specs: specs, log: log}
}

func (d *Driver) Name() string { return driverName }

func (d *Driver) Enumerate() ([]sdr.Info, error) {
	if len(d.specs) == 0 {
		spec := Spec{Transport: TransportUSB}
		addr, err := resolveAddr(spec)
		if err != nil {
			return nil, nil
		}
		if err := probeEndpoint(addr, autoProbeTimeout); err != nil {
			// USB Pluto is optional; absence should look like "no device"
			// instead of surfacing as a noisy enumerate error.
			return nil, nil
		}
		return []sdr.Info{{
			Driver:       driverName,
			Index:        0,
			Serial:       serialFor(spec, 0),
			Manufacturer: "Analog Devices",
			Product:      productName(spec),
			TunerName:    "AD936x",
			Gains:        standardGainLadder(),
		}}, nil
	}

	out := make([]sdr.Info, 0, len(d.specs))
	for i, spec := range d.specs {
		addr, err := resolveAddr(spec)
		if err != nil {
			continue
		}
		out = append(out, sdr.Info{
			Driver:       driverName,
			Index:        i,
			Serial:       serialFor(spec, i),
			Manufacturer: "Analog Devices",
			Product:      productName(spec),
			TunerName:    "AD936x",
			Gains:        standardGainLadder(),
		})
		d.log.Debug("plutoplus: enumerate", "transport", normalizeTransport(spec.Transport), "addr", addr)
	}
	return out, nil
}

func (d *Driver) Open(idx int) (sdr.Device, error) {
	if len(d.specs) == 0 {
		if idx != 0 {
			return nil, fmt.Errorf("plutoplus: index %d out of range", idx)
		}
		spec := Spec{Transport: TransportUSB}
		return d.openSpec(0, spec)
	}

	if idx < 0 || idx >= len(d.specs) {
		return nil, fmt.Errorf("plutoplus: index %d out of range", idx)
	}
	return d.openSpec(idx, d.specs[idx])
}

func (d *Driver) openSpec(idx int, spec Spec) (sdr.Device, error) {
	addr, err := resolveAddr(spec)
	if err != nil {
		return nil, fmt.Errorf("plutoplus: spec[%d]: %w", idx, err)
	}
	timeout := spec.ConnectTimeout
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	conn, err := dialAndHandshake(addr, timeout)
	if err != nil {
		d.log.Warn("plutoplus: open failed", "transport", normalizeTransport(spec.Transport), "addr", addr, "err", err)
		return nil, err
	}

	info := sdr.Info{
		Driver:       driverName,
		Index:        idx,
		Serial:       serialFor(spec, idx),
		Manufacturer: "Analog Devices",
		Product:      productName(spec),
		TunerName:    "AD936x",
		Gains:        standardGainLadder(),
	}
	d.log.Info("plutoplus: connected", "transport", normalizeTransport(spec.Transport), "addr", addr, "serial", info.Serial)
	return &device{addr: addr, info: info, conn: conn, log: d.log}, nil
}

func dialAndHandshake(addr string, timeout time.Duration) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, &TransportError{Stage: StageDial, Addr: addr, Op: "connect", Err: err}
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		conn.Close()
		return nil, &TransportError{Stage: StageHandshake, Addr: addr, Op: "set-read-deadline", Err: err}
	}
	var hdr [12]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		conn.Close()
		return nil, &TransportError{Stage: StageHandshake, Addr: addr, Op: "read-header", Err: err}
	}
	if hdr[0] != magic[0] || hdr[1] != magic[1] || hdr[2] != magic[2] || hdr[3] != magic[3] {
		conn.Close()
		return nil, &TransportError{
			Stage: StageHandshake,
			Addr:  addr,
			Op:    "validate-header-magic",
			Err:   fmt.Errorf("server header magic = %q, want %q", hdr[:4], magic[:]),
		}
	}
	_ = conn.SetReadDeadline(time.Time{})
	return conn, nil
}

func probeEndpoint(addr string, timeout time.Duration) error {
	conn, err := dialAndHandshake(addr, timeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

type device struct {
	addr string
	info sdr.Info
	log  *slog.Logger

	mu     sync.Mutex
	conn   net.Conn
	closed bool
}

func (d *device) Info() sdr.Info { return d.info }

func (d *device) SetCenterFreq(hz uint32) error { return d.sendCmd(cmdSetFreq, hz) }
func (d *device) SetSampleRate(hz uint32) error { return d.sendCmd(cmdSetSampleRate, hz) }

func (d *device) SetGain(tenthDB int) error {
	if tenthDB < 0 {
		return d.sendCmd(cmdSetGainMode, 0)
	}
	if err := d.sendCmd(cmdSetGainMode, 1); err != nil {
		return err
	}
	return d.sendCmd(cmdSetGain, uint32(tenthDB))
}

func (d *device) SetPPM(ppm int) error {
	return d.sendCmd(cmdSetFreqCorrPPM, uint32(int32(ppm)))
}

func (d *device) SetBiasTee(enable bool) error {
	var v uint32
	if enable {
		v = 1
	}
	return d.sendCmd(cmdSetBiasTee, v)
}

func (d *device) sendCmd(op uint8, param uint32) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.conn == nil {
		return fmt.Errorf("plutoplus: device closed")
	}
	var pkt [5]byte
	pkt[0] = op
	pkt[1] = byte(param >> 24)
	pkt[2] = byte(param >> 16)
	pkt[3] = byte(param >> 8)
	pkt[4] = byte(param)
	_ = d.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := d.conn.Write(pkt[:]); err != nil {
		return &TransportError{Stage: StageCommand, Addr: d.addr, Op: fmt.Sprintf("cmd-0x%02x", op), Err: err}
	}
	_ = d.conn.SetWriteDeadline(time.Time{})
	return nil
}

func (d *device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	if d.conn != nil {
		return d.conn.Close()
	}
	return nil
}

func (d *device) StreamIQ(ctx context.Context) (<-chan []complex64, error) {
	d.mu.Lock()
	if d.closed || d.conn == nil {
		d.mu.Unlock()
		return nil, fmt.Errorf("plutoplus: device closed")
	}
	conn := d.conn
	d.mu.Unlock()

	const chunkSamples = 8192
	out := make(chan []complex64, 8)
	go func() {
		defer close(out)
		buf := make([]byte, chunkSamples*2)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			n, err := io.ReadFull(conn, buf)
			if err != nil {
				if err != io.EOF && err != io.ErrUnexpectedEOF {
					d.log.Debug("plutoplus: stream read", "addr", d.addr, "err", (&TransportError{Stage: StageStream, Addr: d.addr, Op: "read-iq", Err: err}).Error())
				}
				if n == 0 {
					return
				}
			}
			if n > 0 {
				samples := convertU8(buf[:n])
				select {
				case <-ctx.Done():
					return
				case out <- samples:
				}
			}
			if err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()
	return out, nil
}

func convertU8(buf []byte) []complex64 {
	n := len(buf) / 2
	out := make([]complex64, n)
	for i := 0; i < n; i++ {
		iv := float32(buf[2*i]) - 127.5
		qv := float32(buf[2*i+1]) - 127.5
		out[i] = complex(iv/127.5, qv/127.5)
	}
	return out
}

func serialFor(spec Spec, idx int) string {
	if spec.Serial != "" {
		return spec.Serial
	}
	addr, err := resolveAddr(spec)
	if err != nil {
		return fmt.Sprintf("plutoplus-%s-%02d", normalizeTransport(spec.Transport), idx)
	}
	return fmt.Sprintf("plutoplus-%s-%02d", sanitizeAddr(addr), idx)
}

func normalizeTransport(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return TransportTCP
	}
	return s
}

func resolveAddr(spec Spec) (string, error) {
	t := normalizeTransport(spec.Transport)
	addr := strings.TrimSpace(spec.Addr)
	switch t {
	case TransportTCP:
		if addr == "" {
			return "", fmt.Errorf("addr is required for transport %q", TransportTCP)
		}
		return addr, nil
	case TransportUSB:
		if addr == "" {
			return DefaultUSBAddr, nil
		}
		return addr, nil
	default:
		return "", fmt.Errorf("unsupported transport %q (want %q or %q)", t, TransportTCP, TransportUSB)
	}
}

func productName(spec Spec) string {
	if normalizeTransport(spec.Transport) == TransportUSB {
		return defaultProductName + " USB"
	}
	return defaultProductName
}

func sanitizeAddr(addr string) string {
	out := make([]byte, 0, len(addr))
	for i := 0; i < len(addr); i++ {
		c := addr[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '-' || c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

func standardGainLadder() []int {
	return []int{0, 9, 14, 27, 37, 77, 87, 125, 144, 157, 166, 197, 207, 229, 254, 280, 297, 328, 338, 364, 372, 386, 402, 421, 434, 439, 445, 480, 496}
}
