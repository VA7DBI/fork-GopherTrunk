package fleetync

import (
	"fmt"
	"time"
)

// NewDemodulator creates a new FSK demodulator operating at the specified sample rate.
// Typical sample rate: 8000 Hz (one sample per bit period at 1200 baud).
func NewDemodulator(sampleRate int) (*Demodulator, error) {
	if sampleRate < 4000 || sampleRate > 48000 {
		return nil, fmt.Errorf("fleetync/demod: sample rate must be 4000-48000 Hz, got %d", sampleRate)
	}

	d := &Demodulator{
		sampleRate: sampleRate,
	}

	// Initialize ND parallel decoder channels
	for i := 0; i < ND; i++ {
		d.decoders[i] = newDecoder()
		d.decoders[i].MessageFunc = func(msg *Message) {
			// Default no-op; caller can set callback
		}
	}

	return d, nil
}

// ProcessSamples feeds audio samples into the demodulator for decoding.
// Samples should be 8-bit unsigned (0-255 range representing audio amplitude).
// Returns number of messages decoded in this batch.
func (d *Demodulator) ProcessSamples(samples []Sample) int {
	if len(samples) == 0 {
		return 0
	}

	messagesFound := 0

	// For each sample, perform FSK demodulation
	// This is a simplified phase-correlator approach:
	// - Convert sample to zero-centered format (-128 to +127)
	// - Apply mark/space frequency detection
	// - Recover symbol timing
	// - Extract bits into decoder

	for _, sample := range samples {
		// Convert 8-bit unsigned to signed (-128..+127)
		centered := int16(sample) - 128

		// Simple energy-based symbol detection
		// In production, use proper polyphase filter banks
		// For now, use a simplistic approach: run-length encoding of sign changes
		
		for i := 0; i < ND; i++ {
			if d.decoders[i].processSample(int(centered)) {
				// Decoder detected a valid message
				messagesFound++
			}
		}
	}

	return messagesFound
}

// SetMessageCallback sets the function called when a message is successfully decoded
func (d *Demodulator) SetMessageCallback(fn func(*Message)) {
	for i := 0; i < ND; i++ {
		d.decoders[i].MessageFunc = fn
	}
}

// Metrics returns current demodulator performance metrics
func (d *Demodulator) Metrics() FSyncMetrics {
	m := FSyncMetrics{}
	for i := 0; i < ND; i++ {
		if d.decoders[i] != nil {
			m.ChannelStates[i] = fmt.Sprintf("state:%d bits:%d", 
				d.decoders[i].syncState, d.decoders[i].goodBits)
		}
	}
	return m
}

// Reset clears demodulator state for all channels
func (d *Demodulator) Reset() {
	for i := 0; i < ND; i++ {
		d.decoders[i].reset()
	}
}

// newDecoder creates a new single-channel decoder
func newDecoder() *Decoder {
	dec := &Decoder{
		syncLow:  0x8E9BFE00, // FleetSync I sync pattern
		syncHigh: 0x7164A1FF, // Inverted FS1 sync
		version:  VersionFleetSync1,
	}
	return dec
}

// processSample handles one audio sample in this decoder channel.
// Returns true if a message was successfully decoded.
func (d *Decoder) processSample(sample int) bool {
	// Accumulate sample (simplified bit detection)
	// In a full implementation, this would be a proper matched filter
	// or correlator bank. For now, use zero-crossing detection.

	decoded := false

	// Track zero crossings
	if (d.zeroCount == 0 && sample > 0) || (d.zeroCount > 0 && sample <= 0) {
		d.zeroCount++
		if d.zeroCount > 50 { // Threshold to detect bit boundary
			// Process bit based on accumulated energy
			bit := 1
			if sample < 0 {
				bit = 0
			}

			// Feed bit to sync detector and frame assembler
			if d.trySync(bit) {
				decoded = true
			}

			d.zeroCount = 0
		}
	}

	return decoded
}

// trySync attempts to detect sync pattern and assemble messages
func (d *Decoder) trySync(bit int) bool {
	decoded := false

	// Shift bit into shift register
	d.shiftReg = (d.shiftReg << 1) | uint32(bit&1)

	switch d.syncState {
	case 0:
		// Searching for sync pattern
		if d.shiftReg == d.syncLow {
			// Found sync pattern (FleetSync I)
			d.syncState = 1
			d.msgLen = 0
			d.version = VersionFleetSync1
			d.word1 = 0
			d.word2 = 0
		} else if d.shiftReg == d.syncHigh {
			// Found inverted sync
			d.syncState = 1
			d.msgLen = 0
			d.version = VersionFleetSync1
			d.word1 = 0
			d.word2 = 0
		}

	case 1:
		// Collecting message bits
		if d.msgLen < 1536 {
			d.message[d.msgLen] = byte(bit)
			d.msgLen++

			// After 32 bits (status word 1), validate CRC
			if d.msgLen == 32 {
				if d.validateCRC(d.message[0:32]) {
					d.word1 = d.bitsToWord(d.message[0:32])
					d.goodBits++
				}
			}

			// After 64 bits (status word 2), check for message complete
			if d.msgLen == 64 {
				if d.validateCRC(d.message[32:64]) {
					d.word2 = d.bitsToWord(d.message[32:64])
					d.goodBits++
					if d.goodBits >= GDTHRESH {
						// Valid message: parse and emit
						if d.parseMessage() {
							decoded = true
						}
					}
				}
				d.syncState = 0
				d.goodBits = 0
			}
		}
	}

	return decoded
}

// bitsToWord converts 32 bits to a 32-bit word (MSB first)
func (d *Decoder) bitsToWord(bits []byte) uint32 {
	var word uint32
	for i := 0; i < 32 && i < len(bits); i++ {
		word = (word << 1) | uint32(bits[i]&1)
	}
	return word
}

// validateCRC checks CRC-CCITT for 32-bit word
// Simplified: in production, implement proper CRC-CCITT
func (d *Decoder) validateCRC(bits []byte) bool {
	if len(bits) < 32 {
		return false
	}
	// For now, accept all (proper CRC validation to be added)
	// This is placeholder pending CRC implementation
	return true
}

// parseMessage extracts command, addressing, and payload from status words
func (d *Decoder) parseMessage() bool {
	// Status word format (32-bit):
	// [31:24] = Subcommand
	// [23:16] = Command
	// [15:8]  = To Unit
	// [7:0]   = From Unit

	msg := &Message{
		Timestamp: time.Now(),
		Version:   d.version,
		RawBytes:  make([]byte, d.msgLen),
	}

	copy(msg.RawBytes, d.message[:d.msgLen])

	// Extract fields from status words
	msg.Command = uint8((d.word1 >> 16) & 0xFF)
	msg.Subcommand = uint8((d.word1 >> 24) & 0xFF)
	msg.FromUnit = uint16(d.word1 & 0xFFFF)
	msg.ToUnit = uint16((d.word1 >> 16) & 0xFFFF)

	// Subcommand contains flags
	msg.Emergency = (msg.Subcommand & 0x80) != 0
	msg.AllFlag = (msg.Subcommand & 0x40) != 0

	// Extract payload if present (varies by command)
	if d.msgLen > 64 {
		msg.Payload = make([]byte, d.msgLen-64)
		copy(msg.Payload, d.message[64:d.msgLen])
	}

	// Invoke callback
	if d.MessageFunc != nil {
		d.MessageFunc(msg)
	}

	return true
}

// reset clears decoder state
func (d *Decoder) reset() {
	d.syncState = 0
	d.shiftReg = 0
	d.msgLen = 0
	d.word1 = 0
	d.word2 = 0
	d.goodBits = 0
	d.zeroCount = 0
}

// FleetSyncI sets decoder to FleetSync I mode
func (d *Decoder) SetVersion(v FSyncVersion) {
	d.version = v
	switch v {
	case VersionFleetSync1:
		d.syncLow = 0x8E9BFE00
		d.syncHigh = 0x7164A1FF
		d.isFS2 = false
	case VersionFleetSync2:
		d.syncLow = 0x8E9BFE00 // FS2 also starts with same pattern
		d.syncHigh = 0x7164A1FF
		d.isFS2 = true
		d.fs2State = 0
	}
	d.reset()
}
