package trunking

import (
	"fmt"
	"time"
)

// Grant is the protocol-agnostic voice-channel grant payload published on
// the events bus by P25/DMR/NXDN control-channel decoders. The trunking
// engine subscribes to events of kind events.KindGrant and dispatches
// them through the priority + voice-device pool.
//
// FrequencyHz must be filled in by the protocol layer (P25 derives it from
// IdentifierUpdate band-plan TSBKs, DMR/NXDN from the configured System).
// If FrequencyHz is zero, the engine logs and drops the grant.
type Grant struct {
	System      string // System name, matches trunking.System.Name
	Protocol    string // "p25" / "dmr" / "nxdn"
	GroupID     uint32 // talkgroup or destination subscriber address
	SourceID    uint32 // originator (subscriber unit)
	FrequencyHz uint32 // voice channel frequency
	ChannelID   uint8  // raw channel ID (P25 band-plan ID, DMR LCN high)
	ChannelNum  uint16 // raw channel number within the ID
	Encrypted   bool
	Emergency   bool
	// AlgorithmID and KeyID carry the encryption parameters the
	// protocol's privacy header advertises (the DMR PI header, etc.).
	// They are meaningful only when Encrypted is true and stay zero
	// until a privacy header has been parsed. Persisted to the call
	// log so an operator can see which key a recorded call needs.
	AlgorithmID uint8
	KeyID       uint16
	DataCall    bool // false = voice call (default)
	// ProVoice marks the grant as an EDACS ProVoice (digital) call. The
	// vocoder is patent + trade-secret encumbered so we cannot ship a
	// built-in decoder; the recorder treats this flag as a directive to
	// emit a `.raw` frame sidecar regardless of its global WriteRaw
	// setting, so researchers can decode out-of-band.
	ProVoice bool
	// PatchedGroups, when non-empty, lists the member talkgroups of a
	// patch / dynamic-regroup super-group: the call on GroupID is
	// physically the shared traffic of these groups. The engine fills
	// it from its PatchRegistry so the call can be attributed to every
	// member. Empty for an ordinary (non-patched) grant.
	PatchedGroups []uint32
	At            time.Time
}

// String renders a one-line summary of a Grant for log output.
func (g Grant) String() string {
	flags := ""
	if g.Encrypted {
		flags += "E"
		// Append ALGID / KID once the in-call signalling has surfaced
		// them so the log line is self-describing for operators
		// triaging encrypted traffic. Zero values are suppressed
		// because a Phase 1 grant publishes before the LDU2 lands.
		if g.AlgorithmID != 0 || g.KeyID != 0 {
			flags += fmt.Sprintf("(alg=0x%02X,key=0x%04X)", g.AlgorithmID, g.KeyID)
		}
	}
	if g.Emergency {
		flags += "!"
	}
	if g.DataCall {
		flags += "D"
	}
	if g.ProVoice {
		flags += "P"
	}
	return fmt.Sprintf("%s/%s tg=%d src=%d freq=%d %s", g.System, g.Protocol, g.GroupID, g.SourceID, g.FrequencyHz, flags)
}

// EndReason classifies why a call ended; carried in CallEnd events so the
// API layer can surface the cause to UIs.
type EndReason uint8

const (
	EndReasonUnknown    EndReason = iota
	EndReasonNormal               // CC announced channel release / talk-off
	EndReasonTimeout              // engine watchdog fired (no recent activity)
	EndReasonPreempted            // higher-priority grant kicked us off
	EndReasonLockout              // talkgroup is locked out by policy
	EndReasonNoVoiceSDR           // every Voice-role SDR was busy
	EndReasonError
	EndReasonManual // operator ended the call via API / TUI
)

func (r EndReason) String() string {
	switch r {
	case EndReasonNormal:
		return "normal"
	case EndReasonTimeout:
		return "timeout"
	case EndReasonPreempted:
		return "preempted"
	case EndReasonLockout:
		return "lockout"
	case EndReasonNoVoiceSDR:
		return "no-voice-sdr"
	case EndReasonError:
		return "error"
	case EndReasonManual:
		return "manual"
	default:
		return "unknown"
	}
}

// CallStart is the payload of an events.KindCallStart event. The engine
// publishes this once a Voice device has been retuned to the grant's
// frequency; downstream pipelines (the demod composer, the recorder)
// subscribe and start consuming IQ.
type CallStart struct {
	Grant        Grant
	Talkgroup    *TalkGroup // resolved via the engine's TalkgroupDB; nil if unknown
	DeviceSerial string     // which Voice SDR is following the call
	StartedAt    time.Time
}

// CallEnd is the payload of an events.KindCallEnd event.
type CallEnd struct {
	Grant        Grant
	Talkgroup    *TalkGroup
	DeviceSerial string
	StartedAt    time.Time
	EndedAt      time.Time
	Reason       EndReason
}

// Duration returns how long the call ran.
func (c CallEnd) Duration() time.Duration { return c.EndedAt.Sub(c.StartedAt) }

// CallComplete is the payload of an events.KindCallComplete event. The
// recorder publishes it once a call's WAV has been flushed and closed,
// so the outbound-streaming subsystem (internal/broadcast) can read the
// finished file and upload it to call aggregators. AudioPath is the
// absolute or working-directory-relative path to the .wav the recorder
// wrote; SampleRate is its PCM rate in Hz.
type CallComplete struct {
	Grant        Grant
	Talkgroup    *TalkGroup
	DeviceSerial string
	StartedAt    time.Time
	EndedAt      time.Time
	Reason       EndReason
	AudioPath    string
	SampleRate   uint32
}

// Duration returns how long the call ran.
func (c CallComplete) Duration() time.Duration { return c.EndedAt.Sub(c.StartedAt) }

// CallEncryption is the payload of an events.KindCallEncryption event.
// It is published by the voice composer when an in-call Encryption Sync
// is recovered (P25 Phase 1 LDU2 carries it; the grant TSBK has only
// the encrypted flag). DeviceSerial keys the update to a specific
// active call; the engine backfills the bound ActiveCall.Grant's
// AlgorithmID / KeyID and republishes the event with System / Protocol
// / GroupID populated so SSE + TUI consumers can patch their live
// view without re-deriving the call's identity.
//
// MessageIndicator carries the 72-bit per-call cryptographic sync
// vector — not surfaced in any DTO today, but plumbed through for
// future key-discovery tooling.
type CallEncryption struct {
	DeviceSerial     string
	System           string // filled in by the engine on republish
	Protocol         string
	GroupID          uint32
	AlgorithmID      uint8
	KeyID            uint16
	MessageIndicator [9]byte
	At               time.Time
}
