package phase1

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/trunking"
)

// ControlChannel consumes a stream of P25 Phase 1 dibits (already
// symbol-time-recovered and mapped via SymbolToDibit) and emits
// trunking events onto an events.Bus.
//
// Pipeline: dibit window → FSW detect → NID parse (BCH(63,16,11) +
// even-parity check) → if DUID is TSDU and the buffer holds enough
// dibits, deinterleave + Viterbi-decode the next 98-dibit TSBK block,
// validate the CRC trailer, and dispatch on the parsed opcode.
//
//   - OpIdentifierUpdate (0x3D) populates the band-plan slot for its
//     Channel ID.
//   - OpGroupVoiceChannelGrant (0x00) parses the channel/group/source
//     payload, looks up the frequency in the band plan, and publishes
//     a trunking.Grant with Protocol="p25" on the bus.
//
// CCLocked / CCLost events fan out on the first corrected NID with a
// TSDU DUID. Uncorrectable NIDs and TSBK CRC failures publish
// KindDecodeError; a grant whose Channel ID has no IdentifierUpdate
// yet publishes KindDecodeError with stage="no-bandplan" so the
// metric counter surfaces the gap.
type ControlChannel struct {
	bus        *events.Bus
	log        *slog.Logger
	det        *SyncDetector
	systemName string
	freqHz     uint32
	bandPlan   *BandPlan
	now        func() time.Time
	locked     bool
	lastNAC    uint16
	// lastNoHitsAt throttles the "no FSW hits" debug log so the chunk-rate
	// emission doesn't flood at debug level. See Process for the rationale.
	lastNoHitsAt time.Time

	// buf accumulates dibits across Process calls so a frame whose
	// FSW + NID + TSBK straddles IQ-chunk boundaries is still
	// assembled; bufBase is the absolute dibit index of buf[0].
	// pending holds FSW hits whose NID + TSBK has not been fully
	// buffered yet. See Process — this is the fix for issue #275,
	// where a live SDR's small IQ chunks delivered far fewer than a
	// frame's worth of dibits per call.
	buf     []uint8
	bufBase int
	pending []pendingHit

	// aliasAsm reassembles multi-fragment vendor talker-alias TSBKs
	// into a radio's display name. Self-synchronised (its own mutex).
	aliasAsm *TalkerAliasAssembler

	// netModel accumulates the site's status-broadcast TSBKs into a
	// queryable system-topology snapshot. Self-synchronised.
	netModel NetworkModel
}

// NetworkSnapshot returns the system topology accumulated from the
// site's Network / RFSS / Secondary-CC / Adjacent-Site status TSBKs.
func (c *ControlChannel) NetworkSnapshot() NetworkConfig {
	return c.netModel.Snapshot()
}

// pendingHit is an FSW match awaiting enough buffered dibits to decode
// its NID + TSBK. end is the absolute dibit index of the FSW's last
// dibit; rot is the cyclic rotation the sync detector matched under.
type pendingHit struct {
	end int
	rot uint8
}

// Options configure a ControlChannel.
type Options struct {
	Bus         *events.Bus
	Log         *slog.Logger
	SystemName  string
	FrequencyHz uint32
	BandPlan    *BandPlan // optional; a new empty BandPlan is used if nil
	Now         func() time.Time
}

// New constructs a ControlChannel from Options. SystemName ends up on
// every trunking.Grant the channel publishes; the daemon passes it
// through from config.
func New(opts Options) *ControlChannel {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	bp := opts.BandPlan
	if bp == nil {
		bp = &BandPlan{}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &ControlChannel{
		bus:        opts.Bus,
		log:        log,
		det:        NewSyncDetector(4),
		systemName: opts.SystemName,
		freqHz:     opts.FrequencyHz,
		bandPlan:   bp,
		now:        now,
		aliasAsm:   NewTalkerAliasAssembler(now),
	}
}

// NewControlChannel keeps the legacy positional constructor working —
// it's used by the existing FEC/decode tests that don't care about
// grant publication. New callers should use New(Options{...}).
func NewControlChannel(bus *events.Bus, log *slog.Logger, freqHz uint32) *ControlChannel {
	return New(Options{Bus: bus, Log: log, FrequencyHz: freqHz})
}

// LockState is the payload of CCLocked / CCLost events. It satisfies
// trunking.LockedPayload so the hunter can consume it without
// importing this package (which would create an import cycle now that
// phase1 publishes trunking.Grant events).
type LockState struct {
	FrequencyHz uint32
	NAC         uint16
	DUID        DUID
}

// LockedFrequencyHz / LockedNAC implement trunking.LockedPayload.
func (s LockState) LockedFrequencyHz() uint32 { return s.FrequencyHz }
func (s LockState) LockedNAC() uint16         { return s.NAC }

// noHitsThrottle bounds how often Process emits its "no FSW hits" debug
// log when the sync detector is finding nothing in successive chunks.
// Issue #275 surfaced because that state produced zero logs at all —
// operators couldn't tell whether the IQ pipeline was alive but
// unsynchronized or wholly silent. Throttling at 2 s keeps the signal
// visible without flooding.
const noHitsThrottle = 2 * time.Second

// p25StatusStride is the on-air dibit period of P25's interleaved
// status symbols: 35 data dibits followed by one 2-bit status symbol
// (TIA-102.BAAA — one status symbol per 70 information bits, the same
// cadence ldu.go's LDUStatusInterval encodes for voice frames).
// Counting on-air dibits from the FSW's first dibit, a status symbol
// falls wherever the index mod p25StatusStride is p25StatusStride-1.
const p25StatusStride = 36

// frameLookahead is the number of on-air dibits that must follow the
// FSW for a full frame to decode: the 32-dibit NID plus the 98-dibit
// TSBK channel block — 130 data dibits — plus the 4 status symbols
// interleaved into that span at the p25StatusStride cadence. Process
// defers an FSW hit until this many dibits (plus nidSearchSpan, so the
// +delta end of the alignment search stays in-buffer) have
// accumulated, so frame assembly no longer depends on the IQ chunking.
const frameLookahead = 130 + 4

// nidSearchSpan bounds the parseFrame NID-alignment search: the NID is
// probed at the FSW-derived start index plus a delta in
// [-nidSearchSpan, +nidSearchSpan]. ±2 dibits absorbs a single
// post-FSW symbol slip or an off-by-one in the NID framing — issue
// #275, where the field symptom was a reliably-detected FSW followed
// by an always-uncorrectable NID, i.e. NID dibits that were sound but
// mis-aligned, which a fixed single-offset read cannot recover. It is
// deliberately small: the BCH(63,16,11) + even-parity + DUID
// acceptance gate is what rejects wrong alignments, and a wider span
// only adds chances for a miscorrection to slip through it.
const nidSearchSpan = 2

// nidAcceptErrs is the highest BCH-corrected error count parseFrame
// will treat as a genuine NID. The code corrects up to 11, but a
// window that only resolves with 7+ corrections is far more likely a
// miscorrection of a misaligned guess than a real noisy NID, so the
// search stays well below the t=11 ceiling.
const nidAcceptErrs = 6

// Process consumes a window of dibits and runs detection/parsing.
// baseIdx is the absolute dibit index of dibits[0]. Returns the
// absolute index one past the last consumed dibit.
//
// Dibits are accumulated into an internal buffer that spans calls, so
// a frame whose FSW + NID + TSBK straddles several Process calls is
// still assembled. This matters on live hardware: a 16 KiB RTL-SDR USB
// transfer carries only ~19 P25 symbols — far short of the 154-dibit
// frame — so without cross-call buffering every FSW hit was discarded
// and the control channel never locked (issue #275).
func (c *ControlChannel) Process(dibits []uint8, baseIdx int) int {
	hits, rots, next := c.det.ProcessWithRotation(nil, nil, dibits, baseIdx)
	if len(hits) == 0 && len(dibits) > 0 && !c.locked {
		now := c.now()
		if now.Sub(c.lastNoHitsAt) >= noHitsThrottle {
			c.log.Debug("p25/phase1: no FSW hits in chunk",
				"system", c.systemName, "freq_hz", c.freqHz, "dibits", len(dibits))
			c.lastNoHitsAt = now
		}
	}

	// Accumulate the new dibits. The receiver hands them over in
	// contiguous, in-order batches, so buf stays a faithful copy of
	// the dibit stream from bufBase onward.
	if len(c.buf) == 0 {
		c.bufBase = baseIdx
	}
	c.buf = append(c.buf, dibits...)
	for i, h := range hits {
		c.pending = append(c.pending, pendingHit{end: h, rot: rots[i]})
	}

	// Parse every pending FSW hit whose full frame has now been
	// buffered; keep the rest for a later call once more dibits land.
	kept := c.pending[:0]
	for _, ph := range c.pending {
		// FSW ends at absolute index ph.end; the 32-dibit NID
		// immediately follows.
		nidStart := ph.end + 1 - c.bufBase
		if nidStart < 0 {
			continue // buffer already trimmed past this hit — drop it
		}
		if nidStart+frameLookahead+nidSearchSpan > len(c.buf) {
			kept = append(kept, ph) // not enough buffered yet
			continue
		}
		c.parseFrame(c.buf, nidStart, ph.rot)
	}
	c.pending = kept
	c.trimBuffer()
	return next
}

// parseFrame decodes the NID + TSBK of one FSW hit. buf[nidStart:]
// must hold at least frameLookahead+nidSearchSpan dibits — the caller
// guarantees it. fswRot is the FSW-search rotation: the sync detector
// matched after adding fswRot mod 4 to each input dibit; it seeds the
// search and breaks ties so a clean frame binds deterministically.
//
// Issue #275: a reliably-detected FSW was followed by a NID that never
// BCH-decoded — the NID dibits were individually sound (the FSW would
// not correlate otherwise) but mis-aligned by the post-FSW framing.
// parseFrame therefore does not trust a single fixed read: searchNID
// probes a bounded grid of alignment hypotheses (NID start ±
// nidSearchSpan, status symbols stripped or not, all four dibit
// rotations) and accepts the one whose NID clears the BCH(63,16,11) +
// even-parity gate with the fewest corrections. That gate is strong
// enough that a wrong alignment cannot realistically be locked on, so
// the search self-validates instead of guessing.
func (c *ControlChannel) parseFrame(buf []uint8, nidStart int, fswRot uint8) {
	best, found, diag := c.searchNID(buf, nidStart, fswRot)
	if !found {
		c.log.Debug("nid parse failed", "system", c.systemName,
			"freq_hz", c.freqHz, "diag", diag)
		c.bus.Publish(events.Event{
			Kind:    events.KindDecodeError,
			Payload: events.DecodeError{Protocol: "p25", Stage: events.StageNIDBCH},
		})
		return
	}
	if best.errs > 0 {
		c.log.Debug("nid corrected", "errs", best.errs, "nac", best.nid.NAC,
			"rot", best.rot, "delta", best.delta, "strip", best.strip)
	}
	if best.nid.DUID != DUIDTrunkingSignaling {
		// Some non-control DUID — record but don't lock.
		c.log.Debug("non-control DUID", "duid", best.nid.DUID, "nac", best.nid.NAC)
		return
	}
	if !c.locked || c.lastNAC != best.nid.NAC {
		c.locked = true
		c.lastNAC = best.nid.NAC
		c.bus.Publish(events.Event{
			Kind:    events.KindCCLocked,
			Payload: LockState{FrequencyHz: c.freqHz, NAC: best.nid.NAC, DUID: best.nid.DUID},
		})
		c.log.Info("control channel locked", "nac", best.nid.NAC, "freq", c.freqHz,
			"rot", best.rot, "delta", best.delta)
	}

	// The channel TSBK occupies the 98 data dibits after the NID,
	// gathered under the same alignment the NID search settled on.
	fswStart := nidStart + best.delta - len(FrameSyncWord)
	tsbkChannel, _ := gatherFrameDibits(buf, best.tsbkStart, 98, fswStart, best.strip)
	tsbk, metric, err := DecodeTSBKChannel(rotateDibits(tsbkChannel, best.rot))
	if err != nil {
		c.log.Debug("tsbk decode failed", "err", err, "metric", metric, "nac", best.nid.NAC)
		stage := events.StageTSBKTrellis
		if errors.Is(err, CRCError) {
			stage = events.StageTSBKCRC
		}
		c.bus.Publish(events.Event{
			Kind:    events.KindDecodeError,
			Payload: events.DecodeError{Protocol: "p25", Stage: stage},
		})
		return
	}
	c.dispatchTSBK(tsbk, best.nid.NAC, metric)
}

// nidGuess is one evaluated NID-alignment hypothesis: the NID read from
// buf starting at nidStart+delta, with interleaved status symbols
// stripped (strip) or taken contiguously, and the dibit alphabet
// rotated by rot. tsbkStart is the index the matching TSBK gather
// begins at.
type nidGuess struct {
	delta     int
	strip     bool
	rot       uint8
	nid       NID
	errs      int
	tsbkStart int
}

// searchNID evaluates the bounded grid of NID-alignment hypotheses for
// one FSW hit and returns the one whose NID BCH-decodes with a valid
// even-parity bit and the fewest corrected errors (errs ≤
// nidAcceptErrs). found is false when no hypothesis clears that gate;
// diag then summarises the closest miss for the failure log, turning a
// silent reject into a measurement (e.g. a low closest-miss errs at a
// non-zero delta points straight at a symbol slip).
func (c *ControlChannel) searchNID(buf []uint8, nidStart int, fswRot uint8) (nidGuess, bool, string) {
	var best nidGuess
	found := false
	closestMiss := -1 // lowest BCH errs of a parity-rejected near-miss
	var missAt nidGuess
	uncorrectable, tried := 0, 0

	for delta := -nidSearchSpan; delta <= nidSearchSpan; delta++ {
		start := nidStart + delta
		if start < 0 || start+frameLookahead > len(buf) {
			continue
		}
		fswStart := start - len(FrameSyncWord)
		for _, strip := range [...]bool{true, false} {
			rawNID, tsbkStart := gatherFrameDibits(buf, start, 32, fswStart, strip)
			for rot := uint8(0); rot < 4; rot++ {
				tried++
				nid, errs, err := NIDFromDibits(rotateDibits(rawNID, rot))
				if err != nil {
					if errs < 0 {
						uncorrectable++
					} else if closestMiss < 0 || errs < closestMiss {
						closestMiss = errs
						missAt = nidGuess{delta: delta, strip: strip, rot: rot, errs: errs}
					}
					continue
				}
				if errs > nidAcceptErrs {
					// A parity-valid codeword this far from the received
					// word is a miscorrection of a bad alignment, not a
					// real NID — treat it as a near-miss, not a decode.
					if closestMiss < 0 || errs < closestMiss {
						closestMiss = errs
						missAt = nidGuess{delta: delta, strip: strip, rot: rot, errs: errs}
					}
					continue
				}
				cand := nidGuess{delta: delta, strip: strip, rot: rot,
					nid: nid, errs: errs, tsbkStart: tsbkStart}
				if !found || betterNID(cand, best, fswRot) {
					best, found = cand, true
				}
			}
		}
	}
	if found {
		return best, true, ""
	}
	if closestMiss >= 0 {
		return best, false, fmt.Sprintf(
			"no NID within accept gate over %d guesses; closest errs=%d at delta=%d strip=%v rot=%d",
			tried, missAt.errs, missAt.delta, missAt.strip, missAt.rot)
	}
	return best, false, fmt.Sprintf(
		"no NID within accept gate over %d guesses; all %d BCH-uncorrectable", tried, uncorrectable)
}

// betterNID reports whether candidate a should replace the current
// best b: fewer BCH corrections wins; ties break toward the most
// canonical alignment (see nidRank) so a clean frame binds to delta=0,
// status-stripped, rot=fswRot exactly as the pre-search code did.
func betterNID(a, b nidGuess, fswRot uint8) bool {
	if a.errs != b.errs {
		return a.errs < b.errs
	}
	return nidRank(a, fswRot) < nidRank(b, fswRot)
}

// nidRank scores how canonical an alignment hypothesis is — lower is
// more canonical. The FSW-reported rotation, a zero start delta and
// status-stripped framing are the expected spec alignment; anything
// else only wins on a strictly lower error count.
func nidRank(g nidGuess, fswRot uint8) int {
	r := 0
	if g.rot != fswRot {
		r += 32
	}
	d := g.delta
	if d < 0 {
		d = -d
	}
	r += 4 * d
	if !g.strip {
		r += 2
	}
	if g.delta < 0 {
		r++
	}
	return r
}

// trimBuffer drops dibits no pending hit needs any more. With no
// pending hits the whole buffer is released; otherwise everything
// before the earliest pending hit's NID is dropped — keeping buf
// bounded to roughly one frame.
func (c *ControlChannel) trimBuffer() {
	keep := len(c.buf)
	for _, ph := range c.pending {
		if s := ph.end + 1 - c.bufBase; s >= 0 && s < keep {
			keep = s
		}
	}
	if keep > 0 {
		c.buf = append(c.buf[:0], c.buf[keep:]...)
		c.bufBase += keep
	}
}

// rotateDibits returns a copy of src with the FSW-search rotation
// undone. The sync detector reported that adding `rot` mod 4 to each
// received dibit reproduced the canonical FrameSyncWord, so the
// canonical NID / TSBK dibits are recovered the same way — by adding
// `rot` mod 4. rot=0 short-circuits to avoid the copy.
func rotateDibits(src []uint8, rot uint8) []uint8 {
	if rot == 0 {
		return src
	}
	out := make([]uint8, len(src))
	for i, d := range src {
		out[i] = (d + rot) & 3
	}
	return out
}

// gatherDataDibits copies count P25 data dibits out of buf starting at
// index start, skipping the status symbols interleaved at the
// p25StatusStride cadence. fswStart is the index of the frame's first
// FSW dibit, against which the status-symbol phase is measured (it may
// be negative if the buffer was trimmed past the FSW — only the
// distance from it matters, and start is always well past it). It
// returns the gathered dibits and the index one past the last dibit
// consumed, so the next field gathers from there. The caller
// guarantees buf holds enough dibits (see frameLookahead).
func gatherDataDibits(buf []uint8, start, count, fswStart int) ([]uint8, int) {
	out := make([]uint8, 0, count)
	i := start
	for len(out) < count {
		if (i-fswStart)%p25StatusStride != p25StatusStride-1 {
			out = append(out, buf[i])
		}
		i++
	}
	return out, i
}

// gatherFrameDibits copies count data dibits out of buf starting at
// index start. When strip is true the status symbols interleaved at
// the p25StatusStride cadence (phase measured from fswStart) are
// skipped via gatherDataDibits; when false the dibits are taken
// contiguously — the parseFrame alignment search tries both, since a
// status-phase error and a missing status symbol are among issue
// #275's candidate framing faults. It returns the gathered dibits and
// the index one past the last dibit consumed.
func gatherFrameDibits(buf []uint8, start, count, fswStart int, strip bool) ([]uint8, int) {
	if strip {
		return gatherDataDibits(buf, start, count, fswStart)
	}
	out := make([]uint8, count)
	copy(out, buf[start:start+count])
	return out, start + count
}

// InjectControlStatusSymbols interleaves P25 status symbols into a
// contiguous control-channel dibit stream (FSW + NID + TSBK …),
// producing the on-air dibit stream a real transmitter emits: one
// status symbol after every p25StatusStride-1 data dibits (TIA-102.BAAA
// — one status symbol per 70 information bits). It is the inverse of
// the status-symbol stripping parseFrame applies via gatherDataDibits,
// and exists to build spec-faithful synthetic streams for tests — the
// receiver harness, integration tests — that would otherwise feed the
// decoder an unrealistic status-free stream.
func InjectControlStatusSymbols(stream []uint8) []uint8 {
	const dataRun = p25StatusStride - 1
	out := make([]uint8, 0, len(stream)+len(stream)/dataRun+1)
	for i, d := range stream {
		out = append(out, d)
		if i%dataRun == dataRun-1 {
			// Cycle the status value through all four symbols so a
			// test cannot pass by depending on a fixed status value —
			// the receiver must skip them by position.
			out = append(out, uint8((i/dataRun)&3))
		}
	}
	return out
}

// dispatchTSBK routes a successfully-CRC'd TSBK to the right opcode
// handler. Unknown opcodes are still useful for diagnostics — they're
// logged at debug but not republished, since they're the bulk of what
// a busy site emits and would drown signal in noise.
func (c *ControlChannel) dispatchTSBK(t TSBK, nac uint16, metric int) {
	// Manufacturer-specific TSBKs are decoded in the vendor's opcode
	// namespace (Motorola patch/regroup, Harris regroup, talker alias)
	// — see tsbk_vendor.go.
	if t.IsVendorMFID() {
		c.dispatchVendorTSBK(t, nac)
		return
	}
	switch t.Opcode {
	case OpIdentifierUpdate:
		u := ParseIdentifierUpdate(t.Payload)
		c.bandPlan.Apply(u)
		c.log.Debug("p25: identifier update",
			"nac", nac, "id", u.ChannelID,
			"base_hz", u.BaseHz, "spacing_hz", u.SpacingHz,
			"tx_offset_hz", u.TxOffsetHz)
	case OpGroupVoiceChannelGrant:
		c.publishGroupGrant(ParseGroupVoiceChannelGrant(t.Payload), nac)
	case OpGroupVoiceChannelUpdate:
		u := ParseGroupVoiceChannelUpdate(t.Payload)
		c.publishVoiceGrant(voiceGrant{
			groupID: uint32(u.GroupAddressA), channelID: u.ChannelAID,
			channelNumber: u.ChannelANumber,
		}, nac)
		if u.GroupAddressB != 0 {
			c.publishVoiceGrant(voiceGrant{
				groupID: uint32(u.GroupAddressB), channelID: u.ChannelBID,
				channelNumber: u.ChannelBNumber,
			}, nac)
		}
	case OpGroupVoiceChannelUpdateExpl:
		c.publishGroupGrant(ParseGroupVoiceChannelUpdateExplicit(t.Payload), nac)
	case OpUnitToUnitVoiceChannelGrant:
		g := ParseUnitToUnitVoiceChannelGrant(t.Payload)
		c.publishVoiceGrant(voiceGrant{
			groupID: g.TargetID, sourceID: g.SourceID,
			channelID: g.ChannelID, channelNumber: g.ChannelNumber,
		}, nac)
	case OpTelephoneInterconnectGrant:
		g := ParseTelephoneInterconnectGrant(t.Payload)
		c.publishVoiceGrant(voiceGrant{
			groupID: g.TargetID, channelID: g.ChannelID,
			channelNumber: g.ChannelNumber, serviceOptions: g.ServiceOptions,
		}, nac)
	case OpSNDCPDataChannelGrant:
		g := ParseSNDCPDataChannelGrant(t.Payload)
		c.publishVoiceGrant(voiceGrant{
			groupID: g.TargetID, channelID: g.ChannelID,
			channelNumber: g.ChannelNumber, serviceOptions: g.ServiceOptions,
			dataCall: true,
		}, nac)
	case OpNetworkStatusBroadcast:
		c.netModel.ApplyNetworkStatus(ParseNetworkStatusBroadcast(t.Payload))
	case OpRFSSStatusBroadcast:
		c.netModel.ApplyRFSSStatus(ParseRFSSStatusBroadcast(t.Payload))
	case OpSecondaryControlChannel:
		c.netModel.ApplySecondaryControlChannel(ParseSecondaryControlChannelBroadcast(t.Payload))
	case OpAdjacentSiteStatusBroadcast:
		c.netModel.ApplyAdjacentSite(ParseAdjacentSiteStatusBroadcast(t.Payload))
	case OpGroupAffiliationResponse:
		c.publishAffiliation(ParseGroupAffiliationResponse(t.Payload), nac)
	case OpUnitRegistrationResponse:
		c.publishUnitRegistration(ParseUnitRegistrationResponse(t.Payload), nac)
	default:
		c.log.Debug("tsbk decoded",
			"opcode", t.Opcode, "lb", t.LB, "metric", metric, "nac", nac)
	}
}

// dispatchVendorTSBK routes a manufacturer-specific TSBK (Motorola or
// Harris MFID) through the vendor accessors in tsbk_vendor.go and
// publishes the trunking event it carries.
func (c *ControlChannel) dispatchVendorTSBK(t TSBK, nac uint16) {
	if pg, ok := t.AsMotorolaPatchGroup(); ok {
		members := make([]uint32, len(pg.Patched))
		for i, m := range pg.Patched {
			members[i] = uint32(m)
		}
		c.publishPatch(uint32(pg.SuperGroup), members, "motorola", true)
		return
	}
	if super, ok := t.AsMotorolaPatchDelete(); ok {
		c.publishPatch(uint32(super), nil, "motorola", false)
		return
	}
	if hr, ok := t.AsHarrisRegroup(); ok {
		c.publishPatch(uint32(hr.RegroupGroup), nil, "harris", true)
		return
	}
	if f, ok := t.AsTalkerAliasFragment(); ok {
		if alias, src, done := c.aliasAsm.Add(f); done {
			c.publishTalkerAlias(src, alias)
		}
		return
	}
	c.log.Debug("p25: vendor tsbk", "mfid", t.MFID, "opcode", t.Opcode, "nac", nac)
}

// publishPatch publishes an events.KindPatch for a vendor patch /
// dynamic-regroup TSBK so the engine can attribute later grants on the
// super-group to its member talkgroups. add=false cancels a patch.
func (c *ControlChannel) publishPatch(superGroup uint32, members []uint32, vendor string, add bool) {
	c.bus.Publish(events.Event{
		Kind: events.KindPatch,
		Payload: trunking.Patch{
			System:     c.systemName,
			Protocol:   "p25",
			SuperGroup: superGroup,
			Members:    members,
			Vendor:     vendor,
			Add:        add,
			At:         c.now(),
		},
	})
	c.log.Debug("p25: patch",
		"system", c.systemName, "vendor", vendor,
		"super", superGroup, "members", members, "add", add)
}

// publishTalkerAlias publishes an events.KindTalkerAlias once a radio's
// display name has been fully reassembled from its fragment TSBKs.
func (c *ControlChannel) publishTalkerAlias(sourceID uint32, alias string) {
	c.bus.Publish(events.Event{
		Kind: events.KindTalkerAlias,
		Payload: trunking.TalkerAlias{
			System:   c.systemName,
			Protocol: "p25",
			SourceID: sourceID,
			Alias:    alias,
			At:       c.now(),
		},
	})
	c.log.Debug("p25: talker alias",
		"system", c.systemName, "src", sourceID, "alias", alias)
}

// voiceGrant is the protocol-internal shape publishVoiceGrant resolves
// and publishes. It generalises the several P25 grant TSBKs (group,
// group-update, unit-to-unit, telephone, SNDCP data) over one path.
type voiceGrant struct {
	groupID        uint32 // talkgroup, or destination unit / phone target
	sourceID       uint32
	channelID      uint8
	channelNumber  uint16
	serviceOptions uint8
	dataCall       bool
}

// publishVoiceGrant resolves a grant's channel through the band plan
// and publishes a trunking.Grant on the bus. If the channel ID hasn't
// been seen yet, a `decode.error` with stage="no-bandplan" is
// published instead — the engine can't do anything with a
// zero-frequency grant, and surfacing this lets operators see when a
// site's IdentifierUpdate cadence is too slow for their capture.
func (c *ControlChannel) publishVoiceGrant(g voiceGrant, nac uint16) {
	freq, err := c.bandPlan.Frequency(g.channelID, g.channelNumber)
	if err != nil {
		c.log.Debug("p25: grant before identifier update",
			"nac", nac, "id", g.channelID, "num", g.channelNumber, "err", err)
		c.bus.Publish(events.Event{
			Kind:    events.KindDecodeError,
			Payload: events.DecodeError{Protocol: "p25", Stage: events.StageNoBandPlan},
		})
		return
	}
	so := ServiceOptions(g.serviceOptions)
	c.bus.Publish(events.Event{
		Kind: events.KindGrant,
		Payload: trunking.Grant{
			System:      c.systemName,
			Protocol:    "p25",
			GroupID:     g.groupID,
			SourceID:    g.sourceID,
			FrequencyHz: freq,
			ChannelID:   g.channelID,
			ChannelNum:  g.channelNumber,
			Encrypted:   so.Encrypted(),
			Emergency:   so.Emergency(),
			DataCall:    g.dataCall,
			At:          c.now(),
		},
	})
	c.log.Debug("p25: grant",
		"system", c.systemName, "nac", nac,
		"tg", g.groupID, "src", g.sourceID,
		"id", g.channelID, "num", g.channelNumber, "freq_hz", freq,
		"enc", so.Encrypted(), "emer", so.Emergency(), "data", g.dataCall)
}

// publishGroupGrant publishes a standard group voice grant (opcode
// 0x00 / 0x03) via publishVoiceGrant.
func (c *ControlChannel) publishGroupGrant(g GroupVoiceChannelGrant, nac uint16) {
	c.publishVoiceGrant(voiceGrant{
		groupID:        uint32(g.GroupAddress),
		sourceID:       g.SourceID,
		channelID:      g.ChannelID,
		channelNumber:  g.ChannelNumber,
		serviceOptions: g.ServiceOptions,
	}, nac)
}

// publishAffiliation publishes a trunking.Affiliation on the bus when
// the site issues a Group Affiliation Response (opcode 0x28). No
// band-plan resolution is needed — affiliations don't carry channel
// info.
func (c *ControlChannel) publishAffiliation(g GroupAffiliationResponse, nac uint16) {
	c.bus.Publish(events.Event{
		Kind: events.KindAffiliation,
		Payload: trunking.Affiliation{
			System:            c.systemName,
			Protocol:          "p25",
			SourceID:          g.TargetID,
			GroupID:           uint32(g.GroupAddress),
			AnnouncementGroup: uint32(g.AnnouncementGroup),
			Response:          trunking.AffiliationResponse(g.Response),
			At:                c.now(),
		},
	})
	c.log.Debug("p25: affiliation",
		"system", c.systemName, "nac", nac,
		"src", g.TargetID, "tg", g.GroupAddress,
		"ann", g.AnnouncementGroup, "rsp", g.Response)
}

// publishUnitRegistration publishes a trunking.UnitRegistration on the
// bus when the site issues a Unit Registration Response (opcode 0x2C).
func (c *ControlChannel) publishUnitRegistration(u UnitRegistrationResponse, nac uint16) {
	c.bus.Publish(events.Event{
		Kind: events.KindUnitRegistration,
		Payload: trunking.UnitRegistration{
			System:   c.systemName,
			Protocol: "p25",
			SourceID: u.SourceID,
			WACN:     u.WACN,
			SystemID: u.SystemID,
			Response: trunking.RegistrationResponse(u.Response),
			At:       c.now(),
		},
	})
	c.log.Debug("p25: registration",
		"system", c.systemName, "nac", nac,
		"src", u.SourceID, "wacn", u.WACN, "sysid", u.SystemID,
		"rsp", u.Response)
}

// MarkLost publishes a CCLost event for the current frequency and resets
// the locked flag. Loss tracking is intentionally simple — the engine /
// hunter pair owns the watchdog logic and calls this when the control
// channel goes silent.
func (c *ControlChannel) MarkLost() {
	if !c.locked {
		return
	}
	c.locked = false
	// Drop accumulated dibits + pending hits so a re-acquisition
	// starts from a clean slate.
	c.buf = c.buf[:0]
	c.pending = c.pending[:0]
	c.bus.Publish(events.Event{
		Kind:    events.KindCCLost,
		Payload: LockState{FrequencyHz: c.freqHz, NAC: c.lastNAC},
	})
}
