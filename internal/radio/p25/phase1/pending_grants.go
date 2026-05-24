package phase1

import "time"

// pendingGrantTTL bounds how long a grant whose channel ID lacked an
// IdentifierUpdate at arrival is held in the deferred queue. P25 voice
// grants address very short live windows — a queued grant older than
// this describes a call that's already underway on a voice channel
// without us, so resolving it after the fact buys no operational
// benefit. The bound also caps memory exposure to a runaway site.
const pendingGrantTTL = 5 * time.Second

// pendingGrantSlotCap is the per-channel-ID ring capacity. Four covers
// the legitimate "burst of grants arrived a few hundred ms before the
// matching IDEN_UP" shape without growing unboundedly when a site
// genuinely never broadcasts an IDEN_UP for that ID.
const pendingGrantSlotCap = 4

// pendingGrant is one queued voice grant awaiting its IdentifierUpdate.
type pendingGrant struct {
	g   voiceGrant
	nac uint16
	at  time.Time
}

// pendingGrants holds voice grants whose channel ID had no
// IdentifierUpdate when the grant was dispatched, one bounded ring per
// channel ID. The control channel calls add() on a BandPlan miss and
// drain() after a successful BandPlan.Apply for that ID; entries older
// than pendingGrantTTL are dropped on drain. Not safe for concurrent
// use — the control channel reads/writes from a single goroutine, the
// same constraint BandPlan already documents.
type pendingGrants struct {
	slots [16][]pendingGrant
}

// add appends g to channelID's ring. When the ring is full the oldest
// entry is dropped to make room — a stuck channel ID can't grow memory
// unbounded. channelID outside [0,15] is silently ignored to match
// BandPlan.Apply's contract.
func (p *pendingGrants) add(channelID uint8, g voiceGrant, nac uint16, now time.Time) {
	if int(channelID) >= len(p.slots) {
		return
	}
	entry := pendingGrant{g: g, nac: nac, at: now}
	ring := p.slots[channelID]
	if len(ring) >= pendingGrantSlotCap {
		ring = append(ring[:0], ring[1:]...)
	}
	p.slots[channelID] = append(ring, entry)
}

// drain returns and clears every entry for channelID whose age is
// within pendingGrantTTL. Expired entries are dropped silently. The
// returned slice is fresh per call so the caller may safely retain it.
func (p *pendingGrants) drain(channelID uint8, now time.Time) []pendingGrant {
	if int(channelID) >= len(p.slots) {
		return nil
	}
	ring := p.slots[channelID]
	p.slots[channelID] = nil
	if len(ring) == 0 {
		return nil
	}
	out := make([]pendingGrant, 0, len(ring))
	for _, e := range ring {
		if now.Sub(e.at) <= pendingGrantTTL {
			out = append(out, e)
		}
	}
	return out
}
