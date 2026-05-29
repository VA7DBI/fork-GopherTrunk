package fleetync

import "fmt"

// MessageParser provides additional parsing and interpretation of FleetSync messages
type MessageParser struct {
	lastMessage *Message
}

// NewMessageParser creates a new message parser
func NewMessageParser() *MessageParser {
	return &MessageParser{}
}

// ParseStatusWord interprets a 32-bit status word into structured fields.
// Status word format (FleetSync standard):
//
//	[31:24] Subcommand (with flags for emergency, all-flag, priority)
//	[23:16] Command
//	[15:8]  To Unit ID (lower 8 bits)
//	[7:0]   From Unit ID (lower 8 bits)
func (p *MessageParser) ParseStatusWord(word uint32) *Message {
	msg := &Message{
		Command:    uint8((word >> 16) & 0xFF),
		Subcommand: uint8((word >> 24) & 0xFF),
		FromUnit:   uint16(word & 0xFFFF),
		ToUnit:     uint16((word >> 16) & 0xFFFF),
	}

	// Extract flags from subcommand byte
	msg.Emergency = (msg.Subcommand & 0x80) != 0
	msg.AllFlag = (msg.Subcommand & 0x40) != 0
	msg.Priority = (msg.Subcommand & 0x20) != 0

	p.lastMessage = msg
	return msg
}

// CommandName returns human-readable name for command code
func CommandName(cmd uint8) string {
	switch cmd {
	case CommandVoiceGrant:
		return "Voice Channel Grant"
	case CommandStatus:
		return "Status/Telemetry"
	case CommandEmergency:
		return "Emergency"
	case CommandAcknowledge:
		return "Acknowledgment"
	case CommandUnitCheck:
		return "Unit Check"
	case CommandAdjSite:
		return "Adjacent Site"
	case CommandSystemID:
		return "System ID"
	case CommandIdle:
		return "Idle"
	default:
		return fmt.Sprintf("Unknown (0x%02X)", cmd)
	}
}

// SubcommandName returns human-readable name for subcommand code
func SubcommandName(cmd uint8, subcmd uint8) string {
	// Strip flags
	base := subcmd & 0x1F

	switch cmd {
	case CommandVoiceGrant:
		switch base {
		case 0x00:
			return "Standard Grant"
		case 0x01:
			return "Extended Grant"
		case 0x02:
			return "Priority Grant"
		}
	case CommandStatus:
		switch base {
		case 0x00:
			return "Status Update"
		case 0x01:
			return "Telemetry Data"
		}
	case CommandEmergency:
		return "Emergency Signal"
	case CommandAcknowledge:
		switch base {
		case 0x00:
			return "Positive Ack"
		case 0x01:
			return "Negative Ack"
		}
	}

	return fmt.Sprintf("Unknown (0x%02X)", base)
}

// VersionString returns human-readable FleetSync version
func VersionString(v FSyncVersion) string {
	switch v {
	case VersionFleetSync1:
		return "FleetSync I"
	case VersionFleetSync2:
		return "FleetSync II"
	default:
		return "Unknown"
	}
}

// FormatMessage creates a human-readable string representation of a message
func FormatMessage(msg *Message) string {
	if msg == nil {
		return "(nil message)"
	}

	flags := ""
	if msg.Emergency {
		flags += " EMERGENCY"
	}
	if msg.AllFlag {
		flags += " BROADCAST"
	}
	if msg.Priority {
		flags += " PRIORITY"
	}

	return fmt.Sprintf(
		"%s | %s | Src: %d → Dst: %d | Cmd: %s | Sub: %s%s",
		msg.Timestamp.Format("15:04:05.000"),
		VersionString(msg.Version),
		msg.FromUnit, msg.ToUnit,
		CommandName(msg.Command),
		SubcommandName(msg.Command, msg.Subcommand),
		flags,
	)
}

// ValidateMessage performs basic sanity checks on parsed message
func ValidateMessage(msg *Message) error {
	if msg == nil {
		return fmt.Errorf("message is nil")
	}

	if msg.RawBytes == nil || len(msg.RawBytes) < 64 {
		return fmt.Errorf("raw message too short: %d bytes", len(msg.RawBytes))
	}

	if msg.Timestamp.IsZero() {
		return fmt.Errorf("timestamp not set")
	}

	// Version should be valid
	if msg.Version != VersionFleetSync1 && msg.Version != VersionFleetSync2 {
		return fmt.Errorf("invalid version: %d", msg.Version)
	}

	return nil
}

// ExtractPayloadFields attempts to parse payload based on command type
func (p *MessageParser) ExtractPayloadFields(msg *Message) map[string]interface{} {
	fields := make(map[string]interface{})

	if msg.Payload == nil || len(msg.Payload) == 0 {
		return fields
	}

	switch msg.Command {
	case CommandVoiceGrant:
		// Voice grant typically includes frequency information
		if len(msg.Payload) >= 2 {
			fields["frequency"] = uint16(msg.Payload[0])<<8 | uint16(msg.Payload[1])
		}

	case CommandStatus:
		// Status may include unit state, battery level, etc.
		if len(msg.Payload) >= 1 {
			fields["status_byte"] = msg.Payload[0]
		}

	case CommandEmergency:
		// Emergency may include reason code
		if len(msg.Payload) >= 1 {
			fields["emergency_code"] = msg.Payload[0]
		}
	}

	return fields
}

// DiffMessages compares two messages and returns differences
func DiffMessages(msg1, msg2 *Message) map[string]interface{} {
	diffs := make(map[string]interface{})

	if msg1 == nil || msg2 == nil {
		return diffs
	}

	if msg1.Command != msg2.Command {
		diffs["command"] = fmt.Sprintf("%02x → %02x", msg1.Command, msg2.Command)
	}

	if msg1.FromUnit != msg2.FromUnit {
		diffs["from_unit"] = fmt.Sprintf("%d → %d", msg1.FromUnit, msg2.FromUnit)
	}

	if msg1.ToUnit != msg2.ToUnit {
		diffs["to_unit"] = fmt.Sprintf("%d → %d", msg1.ToUnit, msg2.ToUnit)
	}

	if msg1.Emergency != msg2.Emergency {
		diffs["emergency"] = fmt.Sprintf("%v → %v", msg1.Emergency, msg2.Emergency)
	}

	if msg1.AllFlag != msg2.AllFlag {
		diffs["all_flag"] = fmt.Sprintf("%v → %v", msg1.AllFlag, msg2.AllFlag)
	}

	return diffs
}
