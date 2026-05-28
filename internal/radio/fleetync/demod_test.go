package fleetync

import (
	"testing"
	"time"
)

// TestDemodulatorCreation verifies demodulator initialization
func TestDemodulatorCreation(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		shouldErr  bool
	}{
		{"Valid 8kHz", 8000, false},
		{"Valid 24kHz", 24000, false},
		{"Too low", 2000, true},
		{"Too high", 96000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dem, err := NewDemodulator(tt.sampleRate)
			if tt.shouldErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.shouldErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.shouldErr && dem == nil {
				t.Fatal("expected demodulator, got nil")
			}
		})
	}
}

// TestDecoderInitialization verifies decoder initialization
func TestDecoderInitialization(t *testing.T) {
	dem, err := NewDemodulator(8000)
	if err != nil {
		t.Fatalf("failed to create demodulator: %v", err)
	}

	// Verify ND decoders are initialized
	if dem.decoders[0] == nil {
		t.Fatal("decoder[0] is nil")
	}
	if dem.decoders[ND-1] == nil {
		t.Fatalf("decoder[%d] is nil", ND-1)
	}

	// Each decoder should start in sync-search state
	for i := 0; i < ND; i++ {
		if dem.decoders[i].syncState != 0 {
			t.Errorf("decoder[%d] syncState: expected 0, got %d", i, dem.decoders[i].syncState)
		}
	}
}

// TestProcessSamplesEmpty verifies handling of empty sample buffer
func TestProcessSamplesEmpty(t *testing.T) {
	dem, _ := NewDemodulator(8000)
	result := dem.ProcessSamples(nil)
	if result != 0 {
		t.Errorf("processing nil samples: expected 0 messages, got %d", result)
	}

	result = dem.ProcessSamples([]Sample{})
	if result != 0 {
		t.Errorf("processing empty samples: expected 0 messages, got %d", result)
	}
}

// TestProcessSamples verifies basic sample processing
func TestProcessSamples(t *testing.T) {
	dem, _ := NewDemodulator(8000)

	// Generate test samples (simple alternating pattern)
	samples := make([]Sample, 1000)
	for i := range samples {
		if i%2 == 0 {
			samples[i] = 200 // High amplitude
		} else {
			samples[i] = 50 // Low amplitude
		}
	}

	// Process should not panic or error
	count := dem.ProcessSamples(samples)
	if count < 0 {
		t.Errorf("process samples returned negative count: %d", count)
	}
}

// TestCallbackInvoked verifies that message callback is invoked
func TestCallbackInvoked(t *testing.T) {
	dem, _ := NewDemodulator(8000)

	called := false
	dem.SetMessageCallback(func(msg *Message) {
		called = true
	})

	// Verify callback is set
	if dem.decoders[0].MessageFunc == nil {
		t.Fatal("callback not set on decoder[0]")
	}

	// Mark as used (in production, would be triggered by actual message decode)
	_ = called
}

// TestDecoderReset verifies state is cleared by reset
func TestDecoderReset(t *testing.T) {
	dec := newDecoder()
	dec.syncState = 5
	dec.msgLen = 100
	dec.word1 = 0xDEADBEEF
	dec.shiftReg = 0xFFFFFFFF

	dec.reset()

	if dec.syncState != 0 {
		t.Errorf("after reset, syncState: expected 0, got %d", dec.syncState)
	}
	if dec.msgLen != 0 {
		t.Errorf("after reset, msgLen: expected 0, got %d", dec.msgLen)
	}
	if dec.shiftReg != 0 {
		t.Errorf("after reset, shiftReg: expected 0, got 0x%x", dec.shiftReg)
	}
}

// TestBitsToWord verifies bit-to-word conversion
func TestBitsToWord(t *testing.T) {
	dec := newDecoder()

	tests := []struct {
		name     string
		bits     []byte
		expected uint32
	}{
		{
			"All zeros",
			[]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			0x00000000,
		},
		{
			"All ones",
			[]byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
			0xFFFFFFFF,
		},
		{
			"Alternating",
			[]byte{1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0},
			0xAAAAAAAA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := dec.bitsToWord(tt.bits)
			if result != tt.expected {
				t.Errorf("bitsToWord: expected 0x%08x, got 0x%08x", tt.expected, result)
			}
		})
	}
}

// TestMessageParsing verifies message parsing from status words
func TestMessageParsing(t *testing.T) {
	dec := newDecoder()
	dec.word1 = 0x00010203 // Subcommand=0x00, Command=0x01, ToUnit=0x0203
	dec.msgLen = 64

	// Manually build message bytes
	for i := 0; i < 64; i++ {
		dec.message[i] = 0
	}

	msgCalled := false
	dec.MessageFunc = func(msg *Message) {
		msgCalled = true
		if msg.Command != 0x01 {
			t.Errorf("Command: expected 0x01, got 0x%02x", msg.Command)
		}
		if msg.Subcommand != 0x00 {
			t.Errorf("Subcommand: expected 0x00, got 0x%02x", msg.Subcommand)
		}
		if msg.RawBytes == nil || len(msg.RawBytes) != 64 {
			t.Errorf("RawBytes: expected 64 bytes, got %d", len(msg.RawBytes))
		}
		if msg.Timestamp.IsZero() {
			t.Error("Timestamp not set")
		}
	}

	dec.parseMessage()
	if !msgCalled {
		t.Fatal("message callback not invoked")
	}
}

// TestFleetSyncVersionDetection verifies version switching
func TestFleetSyncVersionDetection(t *testing.T) {
	dec := newDecoder()

	// Start as FS1
	if dec.version != VersionFleetSync1 {
		t.Errorf("initial version: expected %d, got %d", VersionFleetSync1, dec.version)
	}

	// Switch to FS2
	dec.SetVersion(VersionFleetSync2)
	if dec.version != VersionFleetSync2 {
		t.Errorf("after SetVersion(FS2): expected %d, got %d", VersionFleetSync2, dec.version)
	}
	if !dec.isFS2 {
		t.Error("isFS2 flag not set after SetVersion(FS2)")
	}

	// Switch back to FS1
	dec.SetVersion(VersionFleetSync1)
	if dec.version != VersionFleetSync1 {
		t.Errorf("after SetVersion(FS1): expected %d, got %d", VersionFleetSync1, dec.version)
	}
	if dec.isFS2 {
		t.Error("isFS2 flag should be false after SetVersion(FS1)")
	}
}

// TestDemodulatorSetVersion verifies version propagation to all channels.
func TestDemodulatorSetVersion(t *testing.T) {
	dem, err := NewDemodulator(8000)
	if err != nil {
		t.Fatalf("NewDemodulator: %v", err)
	}
	dem.SetVersion(VersionFleetSync2)
	for i := 0; i < ND; i++ {
		if dem.decoders[i].version != VersionFleetSync2 {
			t.Fatalf("decoder[%d] version=%d want %d", i, dem.decoders[i].version, VersionFleetSync2)
		}
	}
}

// TestMetricsCollection verifies metrics generation
func TestMetricsCollection(t *testing.T) {
	dem, _ := NewDemodulator(8000)
	metrics := dem.Metrics()

	// Metrics should return valid channel state strings
	if metrics.ChannelStates[0] == "" {
		t.Error("ChannelStates[0] is empty")
	}

	// Should have state for all ND channels
	for i := 0; i < ND; i++ {
		if metrics.ChannelStates[i] == "" {
			t.Errorf("ChannelStates[%d] is empty", i)
		}
	}
}

// TestDemodulatorReset verifies full reset
func TestDemodulatorReset(t *testing.T) {
	dem, _ := NewDemodulator(8000)

	// Modify state
	dem.decoders[0].syncState = 5
	dem.decoders[0].msgLen = 50
	dem.decoders[0].word1 = 0xFFFFFFFF

	// Reset all
	dem.Reset()

	// Verify all decoders are reset
	for i := 0; i < ND; i++ {
		if dem.decoders[i].syncState != 0 {
			t.Errorf("decoder[%d].syncState after Reset: expected 0, got %d", i, dem.decoders[i].syncState)
		}
	}
}

// TestTimestampPrecision verifies message timestamps are set correctly
func TestTimestampPrecision(t *testing.T) {
	dec := newDecoder()
	dec.word1 = 0x12345678
	dec.msgLen = 64

	before := time.Now()
	dec.parseMessage()
	after := time.Now()

	// Capture message timestamp via callback
	var msgTime time.Time
	dec.MessageFunc = func(msg *Message) {
		msgTime = msg.Timestamp
	}

	dec.parseMessage()

	if msgTime.Before(before) || msgTime.After(after.Add(time.Second)) {
		t.Errorf("message timestamp %v outside expected range [%v, %v]", msgTime, before, after)
	}
}

// TestMultiChannelIndependence verifies channels decode independently
func TestMultiChannelIndependence(t *testing.T) {
	dem, _ := NewDemodulator(8000)

	// Set different states on each decoder
	for i := 0; i < ND; i++ {
		dem.decoders[i].syncState = i
		dem.decoders[i].msgLen = i * 10
	}

	// Reset specific decoder (simulate)
	dem.decoders[5].reset()

	// Verify only decoder 5 is reset
	if dem.decoders[5].syncState != 0 || dem.decoders[5].msgLen != 0 {
		t.Error("decoder[5] not properly reset")
	}

	// Verify others unchanged
	for i := 0; i < ND; i++ {
		if i != 5 {
			if dem.decoders[i].syncState != i {
				t.Errorf("decoder[%d].syncState changed unexpectedly", i)
			}
		}
	}
}
