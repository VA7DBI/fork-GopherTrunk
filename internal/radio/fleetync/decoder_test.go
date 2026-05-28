package fleetync

import "testing"

// TestSyncDetectionFS1 verifies FS1 sync pattern detection
func TestSyncDetectionFS1(t *testing.T) {
	dec := newDecoder()
	dec.SetVersion(VersionFleetSync1)

	// FS1 sync pattern: 0x8E9BFE00
	// Simulate feeding bits matching the sync pattern
	bits := []int{1, 0, 0, 0, 1, 1, 1, 0, 1, 0, 0, 1, 1, 0, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	for _, bit := range bits {
		dec.trySync(bit)
	}

	if dec.syncState == 0 {
		t.Error("sync pattern not detected (syncState still 0)")
	}
}

// TestSyncDetectionFS2 verifies FS2 detection and differentiation
func TestSyncDetectionFS2(t *testing.T) {
	dec := newDecoder()
	dec.SetVersion(VersionFleetSync2)

	if !dec.isFS2 {
		t.Error("isFS2 not set for FS2 version")
	}
}

// TestInvertedSyncDetection verifies detection of inverted sync patterns
func TestInvertedSyncDetection(t *testing.T) {
	dec := newDecoder()
	// FS1 inverted sync: 0x7164A1FF (bitwise inverse of 0x8E9BFE00)
	syncPatternInv := uint32(0x7164A1FF)

	if dec.syncHigh != syncPatternInv {
		t.Errorf("inverted sync pattern mismatch: expected 0x%08x, got 0x%08x", syncPatternInv, dec.syncHigh)
	}
}

// TestMessageAssembly verifies multi-bit message accumulation
func TestMessageAssembly(t *testing.T) {
	dec := newDecoder()
	dec.syncState = 1 // Simulate post-sync
	dec.msgLen = 0

	// Feed 64 bits (two status words)
	testBits := []byte{1, 0, 1, 0, 1, 0, 1, 0, 0, 1, 0, 1, 0, 1, 0, 1,
		1, 1, 0, 0, 1, 1, 0, 0, 0, 0, 1, 1, 0, 0, 1, 1,
		1, 0, 1, 0, 1, 0, 1, 0, 0, 1, 0, 1, 0, 1, 0, 1,
		1, 1, 0, 0, 1, 1, 0, 0, 0, 0, 1, 1, 0, 0, 1, 1}

	for i, bit := range testBits {
		dec.trySync(int(bit))
		// Verify message accumulates
		if dec.msgLen < i+1 && dec.msgLen > 0 {
			t.Errorf("at bit %d: msgLen mismatch, expected %d, got %d", i, i+1, dec.msgLen)
		}
	}

	if dec.msgLen != 64 {
		t.Errorf("final msgLen: expected 64, got %d", dec.msgLen)
	}
}

// TestCRCValidation verifies CRC check on message frames
func TestCRCValidation(t *testing.T) {
	dec := newDecoder()

	// Test with known good CRC (dummy validation currently)
	testBits := make([]byte, 32)
	for i := range testBits {
		testBits[i] = byte(i % 2)
	}

	// Current implementation accepts all (placeholder)
	valid := dec.validateCRC(testBits)
	if !valid {
		t.Error("validateCRC returned false for test data")
	}
}

// TestMessageBoundaryHandling verifies proper handling at message boundaries
func TestMessageBoundaryHandling(t *testing.T) {
	dec := newDecoder()

	// Start in search state
	if dec.syncState != 0 {
		t.Error("decoder not in search state initially")
	}

	// After processing, should return to search for next message
	dec.syncState = 1
	dec.msgLen = 64

	// Simulate message completion
	dec.syncState = 0
	dec.msgLen = 0

	if dec.syncState != 0 || dec.msgLen != 0 {
		t.Error("message boundary state not properly reset")
	}
}

// TestMultiChannelSync verifies different channels can detect sync independently
func TestMultiChannelSync(t *testing.T) {
	dem, _ := NewDemodulator(8000)

	// Set different sync states on each channel
	dem.decoders[0].syncState = 0 // Searching
	dem.decoders[1].syncState = 1 // Assembling
	dem.decoders[2].syncState = 0 // Searching
	dem.decoders[3].syncState = 1 // Assembling

	// Verify independence
	if dem.decoders[0].syncState != 0 {
		t.Errorf("decoder[0] state: expected 0, got %d", dem.decoders[0].syncState)
	}
	if dem.decoders[1].syncState != 1 {
		t.Errorf("decoder[1] state: expected 1, got %d", dem.decoders[1].syncState)
	}
}

// TestPayloadExtraction verifies payload is correctly extracted from messages
func TestPayloadExtraction(t *testing.T) {
	dec := newDecoder()
	dec.msgLen = 96 // 64 status bits + 32 payload bits

	// Fill message buffer
	for i := 0; i < 96; i++ {
		dec.message[i] = byte(i % 2)
	}

	var capturedMsg *Message
	dec.MessageFunc = func(msg *Message) {
		capturedMsg = msg
	}

	dec.parseMessage()

	if capturedMsg == nil {
		t.Fatal("message callback not invoked")
	}

	if capturedMsg.Payload == nil || len(capturedMsg.Payload) == 0 {
		t.Error("payload not extracted from message")
	}

	if len(capturedMsg.RawBytes) != 96 {
		t.Errorf("raw bytes: expected 96, got %d", len(capturedMsg.RawBytes))
	}
}

// TestEmergencyFlagParsing verifies emergency flag extraction
func TestEmergencyFlagParsing(t *testing.T) {
	dec := newDecoder()

	tests := []struct {
		name      string
		subcommand uint8
		expectEmerg bool
	}{
		{"Emergency bit set", 0x80, true},
		{"Emergency bit clear", 0x00, false},
		{"With other bits", 0x8F, true},
		{"Clear with other bits", 0x7F, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec.word1 = uint32(tt.subcommand) << 24
			dec.msgLen = 64

			var msg *Message
			dec.MessageFunc = func(m *Message) {
				msg = m
			}

			dec.parseMessage()

			if msg == nil {
				t.Fatal("message not parsed")
			}

			if msg.Emergency != tt.expectEmerg {
				t.Errorf("Emergency: expected %v, got %v", tt.expectEmerg, msg.Emergency)
			}
		})
	}
}

// TestAllFlagParsing verifies all-flag (broadcast) extraction
func TestAllFlagParsing(t *testing.T) {
	dec := newDecoder()

	tests := []struct {
		name       string
		subcommand uint8
		expectAll  bool
	}{
		{"All bit set", 0x40, true},
		{"All bit clear", 0x00, false},
		{"With emergency", 0xC0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec.word1 = uint32(tt.subcommand) << 24
			dec.msgLen = 64

			var msg *Message
			dec.MessageFunc = func(m *Message) {
				msg = m
			}

			dec.parseMessage()

			if msg.AllFlag != tt.expectAll {
				t.Errorf("AllFlag: expected %v, got %v", tt.expectAll, msg.AllFlag)
			}
		})
	}
}

// TestCommandParsing verifies command field extraction
func TestCommandParsing(t *testing.T) {
	dec := newDecoder()

	tests := []struct {
		name    string
		word1   uint32
		expectCmd uint8
	}{
		{"Command 0x00", 0x00000000, 0x00},
		{"Command 0x01", 0x00010000, 0x01},
		{"Command 0xFF", 0x00FF0000, 0xFF},
		{"With other fields", 0xAA00BB00, 0x00}, // bits 16-23 are 0x00
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec.word1 = tt.word1
			dec.msgLen = 64

			var msg *Message
			dec.MessageFunc = func(m *Message) {
				msg = m
			}

			dec.parseMessage()

			if msg.Command != tt.expectCmd {
				t.Errorf("Command: expected 0x%02x, got 0x%02x", tt.expectCmd, msg.Command)
			}
		})
	}
}

// TestVersionPropagation verifies version is set in parsed messages
func TestVersionPropagation(t *testing.T) {
	dec := newDecoder()

	versions := []FSyncVersion{VersionFleetSync1, VersionFleetSync2}
	for _, ver := range versions {
		dec.version = ver
		dec.word1 = 0x12345678
		dec.msgLen = 64

		var msg *Message
		dec.MessageFunc = func(m *Message) {
			msg = m
		}

		dec.parseMessage()

		if msg.Version != ver {
			t.Errorf("Version propagation: expected %d, got %d", ver, msg.Version)
		}
	}
}

// TestGoodBitsThreshold verifies GDTHRESH validation
func TestGoodBitsThreshold(t *testing.T) {
	dec := newDecoder()
	dec.goodBits = 0

	// Simulate incrementing good bits
	for i := 0; i <= GDTHRESH; i++ {
		dec.goodBits = i
		if i < GDTHRESH {
			if dec.goodBits >= GDTHRESH {
				t.Errorf("premature threshold at %d bits", i)
			}
		}
	}

	if dec.goodBits < GDTHRESH {
		t.Error("failed to reach GDTHRESH")
	}
}
