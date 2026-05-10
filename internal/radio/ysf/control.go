package ysf

import (
	"log/slog"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

// LockState is the payload of cc.locked / cc.lost events emitted by
// the YSF state machine. Until the FICH decoder lands, the only
// information carried is the tuned frequency — site / call-sign /
// frame-type fields will accrete onto this struct as the FICH path
// stabilises. LockState satisfies trunking.LockedPayload so the
// hunter can consume it without importing this package.
type LockState struct {
	FrequencyHz uint32
}

// LockedFrequencyHz / LockedNAC implement trunking.LockedPayload.
// YSF doesn't have a P25-style NAC; LockedNAC returns 0 and the
// hunter treats it as a don't-care.
func (s LockState) LockedFrequencyHz() uint32 { return s.FrequencyHz }
func (s LockState) LockedNAC() uint16          { return 0 }

// ControlChannel ingests a stream of YSF dibits, runs the FSW
// detector, and publishes cc.locked the first time the sync word
// correlates on a freshly-tuned device. Lock events repeat with
// every fresh sync hit only when they would change state — once
// locked, repeated hits are suppressed until MarkLost runs.
//
// FICH decode lives behind a follow-up PR; until then a YSF lock
// just tells the orchestration layer "there's a transmission here"
// without telling it who or what.
type ControlChannel struct {
	bus    *events.Bus
	log    *slog.Logger
	det    *SyncDetector
	freqHz uint32
	locked bool
}

// NewControlChannel constructs a YSF control channel scanner.
// freqHz is the receive frequency the SDR is tuned to; it ends up
// on every cc.locked event.
func NewControlChannel(bus *events.Bus, log *slog.Logger, freqHz uint32) *ControlChannel {
	if log == nil {
		log = slog.Default()
	}
	return &ControlChannel{
		bus:    bus,
		log:    log,
		det:    NewSyncDetector(2),
		freqHz: freqHz,
	}
}

// Process consumes a window of dibits and runs sync detection.
// baseIdx is the absolute dibit index of dibits[0]. Returns the new
// baseIndex (matches the NXDN / P25 detector contracts).
func (c *ControlChannel) Process(dibits []uint8, baseIdx int) int {
	hits, next := c.det.Process(nil, dibits, baseIdx)
	for range hits {
		c.maybeLock()
	}
	return next
}

func (c *ControlChannel) maybeLock() {
	if c.locked {
		return
	}
	c.locked = true
	state := LockState{FrequencyHz: c.freqHz}
	c.bus.Publish(events.Event{Kind: events.KindCCLocked, Payload: state})
	c.log.Info("ysf cc locked", "freq", state.FrequencyHz)
}

// MarkLost publishes cc.lost and resets the locked flag. Wired up by
// the engine's watchdog when the FSW stops correlating.
func (c *ControlChannel) MarkLost() {
	if !c.locked {
		return
	}
	c.locked = false
	c.bus.Publish(events.Event{
		Kind:    events.KindCCLost,
		Payload: LockState{FrequencyHz: c.freqHz},
	})
}
