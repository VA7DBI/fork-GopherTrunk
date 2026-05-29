// Package receiver wires the APRS pipeline together: HDLC framer
// reads bits, hands frame bodies to the AX.25 parser, the APRS
// info-field decoder turns each parsed frame into a typed packet,
// and the receiver publishes one events.KindAPRSPacket per
// successfully-parsed UI frame.
//
// The DSP layer above this package (1200 Bd Bell-202 AFSK
// demodulation + NRZI decode, see the planned follow-up PR) hands
// the receiver its bit stream. From bits down through bus event
// is what this package owns.
//
// One Receiver instance per APRS channel. The Push(bit) method is
// the single entry point — call it once per LSB-first wire bit.
// Pages flow onto the bus, are persisted by storage.APRSLog,
// reach the REST endpoint, and render on the /aprs web panel.
package receiver

import (
	"sync/atomic"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/radio/aprs"
	"github.com/MattCheramie/GopherTrunk/internal/radio/aprs/ax25"
	"github.com/MattCheramie/GopherTrunk/internal/radio/aprs/hdlc"
	"github.com/MattCheramie/GopherTrunk/internal/storage"
)

// Receiver consumes a bit stream and publishes APRS packets on the
// shared events bus. Zero-value Receivers are NOT usable — call
// New.
type Receiver struct {
	framer *hdlc.Framer
	bus    *events.Bus

	// dropBadFCS, when true, silently discards frames whose AX.25
	// CRC didn't match. Defaults to false — the receiver
	// publishes CRC-failed frames too, with FCSOK=false on the
	// payload, so the web panel can highlight them. Operators
	// who'd rather lose noise can flip it.
	dropBadFCS bool

	// dropNonUI, when true, discards non-UI frames (control byte
	// 0x03 + PID 0xF0 are the UI invariants APRS uses). Defaults
	// to false — we pass them through as TypeUnknown.
	dropNonUI bool

	// Counters surfaced for /metrics.
	framesIn     atomic.Uint64
	framesParsed atomic.Uint64
	framesFCSBad atomic.Uint64
	framesEmit   atomic.Uint64
}

// Options configures a Receiver.
type Options struct {
	// Bus is required — packets publish onto KindAPRSPacket.
	Bus *events.Bus

	// DropBadFCS silently discards CRC-failed frames when true.
	DropBadFCS bool

	// DropNonUI silently discards non-UI frames when true. APRS
	// always uses UI; setting this drops the rare non-UI noise
	// AX.25 implementations sometimes emit on the channel.
	DropNonUI bool
}

// New constructs a Receiver. Panics if opts.Bus is nil — receivers
// without a bus have nowhere to publish.
func New(opts Options) *Receiver {
	if opts.Bus == nil {
		panic("aprs/receiver: events.Bus is required")
	}
	return &Receiver{
		framer:     hdlc.New(),
		bus:        opts.Bus,
		dropBadFCS: opts.DropBadFCS,
		dropNonUI:  opts.DropNonUI,
	}
}

// Push feeds one LSB-first wire bit through the HDLC framer. If
// the bit completes a frame, the receiver parses it as AX.25 +
// APRS and publishes a storage.APRSPacket on the bus.
//
// Bits outside {0, 1} are clamped to 1 — matches the convention
// in pocsag.Syncer.Push.
func (r *Receiver) Push(bit byte) {
	body := r.framer.Push(bit)
	if body == nil {
		return
	}
	r.framesIn.Add(1)

	frame, err := ax25.Parse(body)
	if err != nil {
		return
	}
	r.framesParsed.Add(1)
	if !frame.FCSOK {
		r.framesFCSBad.Add(1)
		if r.dropBadFCS {
			return
		}
	}
	if r.dropNonUI && !frame.IsUI() {
		return
	}

	pkt := aprs.DecodeWithDst(frame.Info, []byte(frame.Dst.Callsign))
	msg := storage.APRSPacket{
		Src:     frame.Src.String(),
		Dst:     frame.Dst.String(),
		Path:    frame.PathString(),
		Type:    aprs.TypeString(pkt.Type),
		Body:    pkt.String(),
		RawInfo: string(frame.Info),
		FCSOK:   frame.FCSOK,
	}
	if pkt.Position != nil {
		msg.Latitude = pkt.Position.Latitude
		msg.Longitude = pkt.Position.Longitude
	}

	r.bus.Publish(events.Event{Kind: events.KindAPRSPacket, Payload: msg})
	r.framesEmit.Add(1)
}

// Stats reports cumulative counters for /metrics + debugging.
// Reading the fields atomically keeps Stats() lock-free.
type Stats struct {
	FramesIn      uint64 // raw frames the HDLC framer emitted
	FramesParsed  uint64 // frames the AX.25 parser accepted (structural OK)
	FramesBadFCS  uint64 // subset of Parsed that failed the CRC check
	FramesEmitted uint64 // events published to the bus
}

func (r *Receiver) Stats() Stats {
	return Stats{
		FramesIn:      r.framesIn.Load(),
		FramesParsed:  r.framesParsed.Load(),
		FramesBadFCS:  r.framesFCSBad.Load(),
		FramesEmitted: r.framesEmit.Load(),
	}
}
