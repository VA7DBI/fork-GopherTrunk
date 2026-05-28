// Package receiver wires the AIS pipeline together: HDLC framer
// reads bits, validates the trailing CRC-CCITT, unpacks the
// payload bytes into the MSB-first bit slice the AIS message
// parser expects, and publishes one events.KindAISMessage per
// successfully-parsed message.
//
// The DSP layer above this package (9600 Bd GMSK demodulation +
// NRZI decode, see internal/radio/ais/gmsk) hands the receiver its
// bit stream. From bits down through bus event is what this
// package owns.
//
// One Receiver instance per AIS channel. The Push(bit) method is
// the single entry point — call it once per LSB-first wire bit.
// Decoded messages flow onto the bus, are persisted by
// storage.VesselLog, reach the REST endpoint, and render on the
// /ais web panel.
package receiver

import (
	"sync/atomic"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/radio/ais"
	"github.com/MattCheramie/GopherTrunk/internal/radio/aprs/hdlc"
	"github.com/MattCheramie/GopherTrunk/internal/storage"
)

// MinPayloadBytes is the smallest valid AIS frame body (after the
// HDLC framer has stripped flags). 168 bits of message + 16 bits
// of CRC = 184 bits = 23 bytes. We accept short frames slightly
// under that and let the parser surface them as TypeUnknown.
const MinPayloadBytes = 23

// Receiver consumes a stream of HDLC bits and publishes AIS
// messages on the shared events bus. Zero-value Receivers are NOT
// usable — call New.
type Receiver struct {
	framer *hdlc.Framer
	bus    *events.Bus

	// dropBadFCS, when true, silently discards frames whose AIS
	// CRC didn't match. Defaults to false — the receiver
	// publishes CRC-failed frames too, with FCSOK=false on the
	// payload, so the web panel can highlight them. Operators
	// who'd rather lose noise can flip it.
	dropBadFCS bool

	// dropNonPosition, when true, discards messages whose payload
	// doesn't carry a usable position. Useful for operators who
	// only want vessel tracks and not the type-4 base-station
	// chatter / type-22 channel-management broadcasts that
	// dominate quiet channels.
	dropNonPosition bool

	// Counters surfaced for /metrics.
	framesIn       atomic.Uint64
	framesParsed   atomic.Uint64
	framesFCSBad   atomic.Uint64
	framesEmitted  atomic.Uint64
	framesTooShort atomic.Uint64
}

// Options configures a Receiver.
type Options struct {
	// Bus is required — messages publish onto KindAISMessage.
	Bus *events.Bus

	// DropBadFCS silently discards CRC-failed messages when true.
	DropBadFCS bool

	// DropNonPosition silently discards messages whose payload
	// doesn't carry a usable position. Defaults to false —
	// static-data and base-station messages still surface.
	DropNonPosition bool
}

// New constructs a Receiver. Panics if opts.Bus is nil — receivers
// without a bus have nowhere to publish.
func New(opts Options) *Receiver {
	if opts.Bus == nil {
		panic("ais/receiver: events.Bus is required")
	}
	return &Receiver{
		framer:          hdlc.New(),
		bus:             opts.Bus,
		dropBadFCS:      opts.DropBadFCS,
		dropNonPosition: opts.DropNonPosition,
	}
}

// Push feeds one LSB-first wire bit through the HDLC framer. If
// the bit completes a frame, the receiver validates the CRC,
// unpacks the payload into AIS bits, parses it, and publishes a
// storage.AISMessage on the bus.
//
// Bits outside {0, 1} are clamped to 1 — matches the convention
// in hdlc.Framer.Push.
func (r *Receiver) Push(bit byte) {
	body := r.framer.Push(bit)
	if body == nil {
		return
	}
	r.framesIn.Add(1)
	if len(body) < MinPayloadBytes {
		r.framesTooShort.Add(1)
		return
	}

	// CRC-CCITT FCS validation. Same algorithm as AX.25: reflected
	// polynomial 0x8408, init 0xFFFF, final XOR 0xFFFF. The
	// trailing 2 bytes of body are the FCS, little-endian.
	payload := body[:len(body)-2]
	want := uint16(body[len(body)-2]) | uint16(body[len(body)-1])<<8
	fcsOK := crc16CCITT(payload) == want

	if !fcsOK {
		r.framesFCSBad.Add(1)
		if r.dropBadFCS {
			return
		}
	}

	// Unpack payload bytes MSB-first into a one-bit-per-byte
	// slice — AIS spec convention for field reads (see
	// ais.readBitsUint). Wire ordering is LSB-first within each
	// byte (the HDLC framer assembled that way), so the byte
	// values are correct; we just lay their bits out MSB-first
	// for the parser.
	bits := bytesToMSBFirstBits(payload)
	msg := ais.Decode(bits)
	r.framesParsed.Add(1)

	if r.dropNonPosition && (msg.Position == nil || !msg.Position.HasPosition) {
		return
	}

	out := storage.AISMessage{
		MMSI:   msg.MMSI,
		Type:   ais.TypeString(msg.Type),
		Body:   msg.String(),
		RawHex: msg.RawHex,
		FCSOK:  fcsOK,
	}
	if msg.Position != nil {
		out.Latitude = msg.Position.Latitude
		out.Longitude = msg.Position.Longitude
		out.SpeedOverGround = msg.Position.SpeedOverGround
		out.CourseOverGround = msg.Position.CourseOverGround
		out.Heading = msg.Position.Heading
		out.HasPosition = msg.Position.HasPosition
	}
	if msg.Static != nil {
		out.VesselName = msg.Static.Name
		out.Callsign = msg.Static.Callsign
		out.Destination = msg.Static.Destination
		out.ShipType = msg.Static.ShipType
		out.IMO = msg.Static.IMO
	}

	r.bus.Publish(events.Event{Kind: events.KindAISMessage, Payload: out})
	r.framesEmitted.Add(1)
}

// bytesToMSBFirstBits expands a byte slice into the one-bit-per-
// byte form the ais package's parsers expect. MSB-first per byte
// — bit 0 of the output is the high bit of byte 0.
func bytesToMSBFirstBits(bytes []byte) []byte {
	out := make([]byte, len(bytes)*8)
	for i, b := range bytes {
		for k := 0; k < 8; k++ {
			if b&(1<<uint(7-k)) != 0 {
				out[i*8+k] = 1
			}
		}
	}
	return out
}

// crc16CCITT computes the HDLC FCS (CRC-CCITT, reflected
// polynomial 0x8408, init 0xFFFF, final XOR 0xFFFF). Same routine
// AX.25 uses — AIS inherits the HDLC link-layer conventions verbatim
// per ITU-R M.1371-5 §4.2.
func crc16CCITT(data []byte) uint16 {
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

// Stats reports cumulative counters for /metrics + debugging.
// Reading the fields atomically keeps Stats() lock-free.
type Stats struct {
	FramesIn       uint64 // raw frames the HDLC framer emitted
	FramesParsed   uint64 // frames the AIS parser accepted (length OK)
	FramesBadFCS   uint64 // subset of Parsed that failed the CRC check
	FramesEmitted  uint64 // events published to the bus
	FramesTooShort uint64 // frames discarded for under-length
}

func (r *Receiver) Stats() Stats {
	return Stats{
		FramesIn:       r.framesIn.Load(),
		FramesParsed:   r.framesParsed.Load(),
		FramesBadFCS:   r.framesFCSBad.Load(),
		FramesEmitted:  r.framesEmitted.Load(),
		FramesTooShort: r.framesTooShort.Load(),
	}
}
