package fleetync

import (
	"strings"
	"testing"
	"time"
)

// TestMessageParserCreation verifies parser initialization
func TestMessageParserCreation(t *testing.T) {
	p := NewMessageParser()
	if p == nil {
		t.Fatal("parser is nil")
	}
	if p.lastMessage != nil {
		t.Error("lastMessage should be nil initially")
	}
}

// TestParseStatusWord verifies status word parsing
func TestParseStatusWord(t *testing.T) {
	p := NewMessageParser()

	tests := []struct {
		name           string
		word           uint32
		expectCmd      uint8
		expectSubcmd   uint8
		expectFromUnit uint16
		expectToUnit   uint16
		expectEmerg    bool
		expectAll      bool
		expectPriority bool
	}{
		{
			"Simple message",
			0x04030201, // subcommand=0x04, command=0x03, tounit/fromunit=0x0201
			0x03, 0x04, 0x0201, 0x0403,
			false, false, false,
		},
		{
			"With emergency flag",
			0x84030201,
			0x03, 0x84, 0x0201, 0x0403,
			true, false, false,
		},
		{
			"With broadcast flag",
			0x44030201,
			0x03, 0x44, 0x0201, 0x0403,
			false, true, false,
		},
		{
			"With priority flag",
			0x24030201,
			0x03, 0x24, 0x0201, 0x0403,
			false, false, true,
		},
		{
			"All flags set",
			0xE4030201,
			0x03, 0xE4, 0x0201, 0x0403,
			true, true, true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := p.ParseStatusWord(tt.word)

			if msg.Command != tt.expectCmd {
				t.Errorf("Command: expected 0x%02x, got 0x%02x", tt.expectCmd, msg.Command)
			}
			if msg.Subcommand != tt.expectSubcmd {
				t.Errorf("Subcommand: expected 0x%02x, got 0x%02x", tt.expectSubcmd, msg.Subcommand)
			}
			if msg.FromUnit != tt.expectFromUnit {
				t.Errorf("FromUnit: expected %d, got %d", tt.expectFromUnit, msg.FromUnit)
			}
			if msg.Emergency != tt.expectEmerg {
				t.Errorf("Emergency: expected %v, got %v", tt.expectEmerg, msg.Emergency)
			}
			if msg.AllFlag != tt.expectAll {
				t.Errorf("AllFlag: expected %v, got %v", tt.expectAll, msg.AllFlag)
			}
			if msg.Priority != tt.expectPriority {
				t.Errorf("Priority: expected %v, got %v", tt.expectPriority, msg.Priority)
			}
		})
	}
}

// TestCommandName returns human-readable command names
func TestCommandName(t *testing.T) {
	tests := []struct {
		cmd    uint8
		expect string
	}{
		{CommandVoiceGrant, "Voice Channel Grant"},
		{CommandStatus, "Status/Telemetry"},
		{CommandEmergency, "Emergency"},
		{CommandAcknowledge, "Acknowledgment"},
		{CommandUnitCheck, "Unit Check"},
		{CommandAdjSite, "Adjacent Site"},
		{CommandSystemID, "System ID"},
		{CommandIdle, "Idle"},
		{0xFF, "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expect, func(t *testing.T) {
			name := CommandName(tt.cmd)
			if !strings.Contains(name, "Unknown") && name != tt.expect {
				t.Errorf("expected %q, got %q", tt.expect, name)
			}
		})
	}
}

// TestSubcommandName returns human-readable subcommand names
func TestSubcommandName(t *testing.T) {
	tests := []struct {
		cmd    uint8
		subcmd uint8
		check  string
	}{
		{CommandVoiceGrant, 0x00, "Standard Grant"},
		{CommandVoiceGrant, 0x01, "Extended Grant"},
		{CommandStatus, 0x00, "Status Update"},
		{CommandEmergency, 0x00, "Emergency Signal"},
		{CommandAcknowledge, 0x00, "Positive Ack"},
		{CommandAcknowledge, 0x01, "Negative Ack"},
	}

	for _, tt := range tests {
		t.Run(tt.check, func(t *testing.T) {
			name := SubcommandName(tt.cmd, tt.subcmd)
			if !strings.Contains(name, tt.check) {
				t.Errorf("expected %q in result, got %q", tt.check, name)
			}
		})
	}
}

// TestVersionString returns human-readable version names
func TestVersionString(t *testing.T) {
	tests := []struct {
		version FSyncVersion
		expect  string
	}{
		{VersionFleetSync1, "FleetSync I"},
		{VersionFleetSync2, "FleetSync II"},
		{99, "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expect, func(t *testing.T) {
			name := VersionString(tt.version)
			if name != tt.expect {
				t.Errorf("expected %q, got %q", tt.expect, name)
			}
		})
	}
}

// TestFormatMessage generates human-readable message string
func TestFormatMessage(t *testing.T) {
	msg := &Message{
		Timestamp:  time.Date(2026, 5, 28, 15, 4, 5, 0, time.UTC),
		Version:    VersionFleetSync1,
		Command:    CommandVoiceGrant,
		Subcommand: 0x00,
		FromUnit:   123,
		ToUnit:     456,
		Emergency:  false,
		AllFlag:    false,
		Priority:   false,
	}

	formatted := FormatMessage(msg)

	if !strings.Contains(formatted, "15:04:05") {
		t.Errorf("timestamp not in formatted output: %s", formatted)
	}
	if !strings.Contains(formatted, "FleetSync I") {
		t.Errorf("version not in formatted output: %s", formatted)
	}
	if !strings.Contains(formatted, "123") {
		t.Errorf("from unit not in formatted output: %s", formatted)
	}
	if !strings.Contains(formatted, "456") {
		t.Errorf("to unit not in formatted output: %s", formatted)
	}
}

// TestFormatMessageNil handles nil message
func TestFormatMessageNil(t *testing.T) {
	result := FormatMessage(nil)
	if result != "(nil message)" {
		t.Errorf("expected '(nil message)', got %q", result)
	}
}

// TestFormatMessageWithFlags includes emergency/broadcast flags
func TestFormatMessageWithFlags(t *testing.T) {
	msg := &Message{
		Timestamp:  time.Now(),
		Version:    VersionFleetSync1,
		Command:    CommandEmergency,
		Subcommand: 0x80,
		FromUnit:   100,
		ToUnit:     200,
		Emergency:  true,
		AllFlag:    true,
		Priority:   true,
	}

	formatted := FormatMessage(msg)

	if !strings.Contains(formatted, "EMERGENCY") {
		t.Errorf("EMERGENCY flag missing: %s", formatted)
	}
	if !strings.Contains(formatted, "BROADCAST") {
		t.Errorf("BROADCAST flag missing: %s", formatted)
	}
	if !strings.Contains(formatted, "PRIORITY") {
		t.Errorf("PRIORITY flag missing: %s", formatted)
	}
}

// TestValidateMessage checks message sanity
func TestValidateMessage(t *testing.T) {
	tests := []struct {
		name      string
		msg       *Message
		shouldErr bool
	}{
		{
			"Valid message",
			&Message{
				Version:   VersionFleetSync1,
				Timestamp: time.Now(),
				RawBytes:  make([]byte, 64),
			},
			false,
		},
		{
			"Nil message",
			nil,
			true,
		},
		{
			"Missing raw bytes",
			&Message{
				Version:   VersionFleetSync1,
				Timestamp: time.Now(),
				RawBytes:  nil,
			},
			true,
		},
		{
			"Raw bytes too short",
			&Message{
				Version:   VersionFleetSync1,
				Timestamp: time.Now(),
				RawBytes:  make([]byte, 32),
			},
			true,
		},
		{
			"Zero timestamp",
			&Message{
				Version:   VersionFleetSync1,
				Timestamp: time.Time{},
				RawBytes:  make([]byte, 64),
			},
			true,
		},
		{
			"Invalid version",
			&Message{
				Version:   99,
				Timestamp: time.Now(),
				RawBytes:  make([]byte, 64),
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMessage(tt.msg)
			if tt.shouldErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.shouldErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestExtractPayloadFields parses command-specific payload data
func TestExtractPayloadFields(t *testing.T) {
	p := NewMessageParser()

	tests := []struct {
		name     string
		cmd      uint8
		payload  []byte
		checkKey string
	}{
		{
			"Voice grant with frequency",
			CommandVoiceGrant,
			[]byte{0x12, 0x34, 0x56},
			"frequency",
		},
		{
			"Status with status byte",
			CommandStatus,
			[]byte{0xFF},
			"status_byte",
		},
		{
			"Emergency with code",
			CommandEmergency,
			[]byte{0x02},
			"emergency_code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &Message{
				Command: tt.cmd,
				Payload: tt.payload,
			}

			fields := p.ExtractPayloadFields(msg)
			if _, ok := fields[tt.checkKey]; !ok {
				t.Errorf("expected key %q in fields, got: %v", tt.checkKey, fields)
			}
		})
	}
}

// TestExtractPayloadFieldsNilPayload handles nil payload
func TestExtractPayloadFieldsNilPayload(t *testing.T) {
	p := NewMessageParser()
	msg := &Message{
		Command: CommandVoiceGrant,
		Payload: nil,
	}

	fields := p.ExtractPayloadFields(msg)
	if len(fields) > 0 {
		t.Errorf("expected empty fields for nil payload, got %v", fields)
	}
}

// TestDiffMessages compares two messages
func TestDiffMessages(t *testing.T) {
	msg1 := &Message{
		Command:   CommandVoiceGrant,
		FromUnit:  100,
		ToUnit:    200,
		Emergency: false,
	}

	msg2 := &Message{
		Command:   CommandStatus,
		FromUnit:  100,
		ToUnit:    201,
		Emergency: true,
	}

	diffs := DiffMessages(msg1, msg2)

	if _, ok := diffs["command"]; !ok {
		t.Error("expected 'command' diff")
	}
	if _, ok := diffs["to_unit"]; !ok {
		t.Error("expected 'to_unit' diff")
	}
	if _, ok := diffs["emergency"]; !ok {
		t.Error("expected 'emergency' diff")
	}
	if _, ok := diffs["from_unit"]; ok {
		t.Error("unexpected 'from_unit' diff (should be same)")
	}
}

// TestDiffMessagesIdentical returns empty diffs for identical messages
func TestDiffMessagesIdentical(t *testing.T) {
	msg1 := &Message{
		Command:  CommandVoiceGrant,
		FromUnit: 100,
		ToUnit:   200,
	}

	msg2 := &Message{
		Command:  CommandVoiceGrant,
		FromUnit: 100,
		ToUnit:   200,
	}

	diffs := DiffMessages(msg1, msg2)
	if len(diffs) > 0 {
		t.Errorf("expected no diffs for identical messages, got %v", diffs)
	}
}

// TestLastMessage tracks previous message
func TestLastMessage(t *testing.T) {
	p := NewMessageParser()

	msg := p.ParseStatusWord(0x12345678)
	if p.lastMessage != msg {
		t.Error("lastMessage not updated after parsing")
	}

	msg2 := p.ParseStatusWord(0x87654321)
	if p.lastMessage != msg2 {
		t.Error("lastMessage not updated to new message")
	}
	if p.lastMessage == msg {
		t.Error("lastMessage should be msg2, not msg")
	}
}
