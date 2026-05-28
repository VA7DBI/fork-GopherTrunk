// Package fleetync provides decoder/demodulator for Kenwood FleetSync (I and II)
// radio signaling formats. FleetSync is a 1200-baud narrow-band data system
// that transmits metadata independently of voice traffic.
//
// Reference implementation: https://github.com/russinnes/fsync-mdc1200-decode
package fleetync

import "time"

// Sample format: 8-bit unsigned integers at 8 kHz sampling rate
type Sample uint8

// Command types for FleetSync messages
const (
	CommandVoiceGrant    = 0x00
	CommandStatus        = 0x01
	CommandEmergency     = 0x02
	CommandAcknowledge   = 0x03
	CommandUnitCheck     = 0x04
	CommandAdjSite       = 0x05
	CommandSystemID      = 0x06
	CommandIdle          = 0x07
)

// ND is the number of parallel decode channels (decoders per demodulator)
const ND = 10

// GDTHRESH is the "good bits" threshold for sync validation
const GDTHRESH = 3

// FSyncVersion represents FleetSync variant
type FSyncVersion uint8

const (
	VersionFleetSync1 FSyncVersion = 1
	VersionFleetSync2 FSyncVersion = 2
)

// Message represents a decoded FleetSync status word
type Message struct {
	Timestamp    time.Time
	Version      FSyncVersion
	Command      uint8
	Subcommand   uint8
	FromFleet    uint8
	FromUnit     uint16
	ToFleet      uint8
	ToUnit       uint16
	AllFlag      bool
	Emergency    bool
	Priority     bool
	Payload      []byte
	RawBytes     []byte
}

// Demodulator performs FSK demodulation on 8kHz audio samples
// and extracts bit stream for downstream decoder processing.
type Demodulator struct {
	sampleRate int
	decoders   [ND]*Decoder
}

// Decoder processes a bit stream and performs frame sync,
// bit assembly, and CRC validation for one channel.
type Decoder struct {
	version      FSyncVersion
	syncState    int
	shiftReg     uint32
	syncLow      uint32
	syncHigh     uint32
	word1        uint32
	word2        uint32
	msgLen       int
	message      [1536]byte
	isFS2        bool
	fs2State     int
	fs2Word1     uint32
	fs2Word2     uint32
	goodBits     int
	zeroCount    int

	// Callback invoked on successful message decode
	MessageFunc func(*Message)
}

// FSyncMetrics holds performance and diagnostic information
type FSyncMetrics struct {
	TotalSamples      int64
	TotalMessagesRx   int64
	SyncErrors        int64
	CRCErrors         int64
	ChannelStates     [ND]string
	LastMessageTime   time.Time
	MessageRate       float64 // messages per second
}

// DemodulatorConfig specifies demodulator parameters
type DemodulatorConfig struct {
	SampleRate  int
	Version     FSyncVersion
	Frequency   uint32
	Deviation   uint32
	Gain        float32
	CenterFreq  float32
}
