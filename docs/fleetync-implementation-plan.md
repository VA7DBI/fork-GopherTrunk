# Kenwood FleetSync Decoder/Demodulator Implementation Plan

## Overview

This document outlines the implementation strategy for adding Kenwood FleetSync (I and II) decoder/demodulator support to GopherTrunk. FleetSync is a Kenwood-developed signaling format for radio communications that carries metadata about calls independently of voice traffic.

### FleetSync Specification Basics

**FleetSync I & II**: A 1200-baud narrow-band data system that transmits:
- Command/Subcommand pairs
- From Fleet / From Unit (source)
- To Fleet / To Unit (destination)
- All-flag (broadcast indicator)
- Payload data
- Emergency/Priority indicators

**Key Characteristics**:
- Operates independently on dedicated frequencies or in-band alongside voice
- Narrow bandwidth (~8 kHz)
- Multiple channels simultaneously (differs between FS1 and FS2)
- 8-bit unsigned audio sample format at 8 kHz sampling rate
- Outputs JSON-formatted messages with decoded metadata

### Integration Architecture

FleetSync will integrate into GopherTrunk following established patterns:

```
Audio Input (IQ → FM Demod → 8kHz audio samples)
    ↓
FleetSync Demodulator (internal/radio/fleetync/demod.go)
    ↓
FleetSync Decoder (internal/radio/fleetync/decoder.go)
    ↓
Message Parser (internal/radio/fleetync/message.go)
    ↓
Event Publisher (events.KindFleetyncMessage)
    ↓
TUI / Broadcast / Storage subscribers
```

Similar to APRS and Paging (POCSAG) subsystems, FleetSync will:
1. Accept config entries pinning SDRs to frequencies
2. Demodulate audio from pinned tuners
3. Publish decoded events to the bus
4. Support TUI display, storage, and broadcast backends

---

## Epics

### Epic 1: FleetSync Core Demodulation & Decoding Engine

**Objective**: Implement base FleetSync I/II demodulation and decoding logic, ported from reference implementation.

**Milestones**:
1. **M1.1**: Implement FSK demodulator with polyphase filters and phase correlator
   - Symbol timing recovery at 1200 baud
   - Frequency detection (mark/space distinction)
   - Tests: unit tests for filter coefficients, symbol detection

2. **M1.2**: Implement FleetSync I frame synchronization
   - Sync pattern detection (0x8E9BFE00 for FS1)
   - Multi-channel support (10 parallel detection channels)
   - Tests: sync detection with known test vectors

3. **M1.3**: Implement FleetSync II frame synchronization
   - Extended sync patterns (FS2 variant)
   - Differentiation from FS1
   - Tests: FS2-specific test vectors

4. **M1.4**: Implement bit/frame assembly and CRC validation
   - Message accumulation (multiple frames per call)
   - CRC-CCITT validation
   - Tests: known message CRC validation

5. **M1.5**: Implement status word parsing
   - Command/Subcommand extraction
   - Source unit (fleet/ID) parsing
   - Destination unit parsing
   - Payload extraction
   - Tests: parse known status word formats

**Deliverables**:
- `internal/radio/fleetync/demod.go` (~400 lines)
- `internal/radio/fleetync/decoder.go` (~300 lines)
- `internal/radio/fleetync/message.go` (~150 lines)
- `internal/radio/fleetync/types.go` (type definitions)
- `internal/radio/fleetync/demod_test.go` (95%+ coverage)
- `internal/radio/fleetync/decoder_test.go` (95%+ coverage)

### Epic 2: GopherTrunk Integration & Configuration

**Objective**: Wire FleetSync decoder into GopherTrunk's config, startup, and event systems.

**Milestones**:
1. **M2.1**: Add FleetSync config structures to config.go
   - `FleetSyncConfig` with channels array
   - Per-channel: serial, frequency_hz, name (optional)
   - Tests: config parsing YAML

2. **M2.2**: Add FleetSync events to event system
   - `events.KindFleetyncMessage` event type
   - Message structure with metadata, timestamps
   - Tests: event marshaling/unmarshaling

3. **M2.3**: Implement startup wiring in cmd/gophertrunk/
   - Parse FleetSync config from YAML
   - Initialize demodulators per channel
   - Attach to scanner/tuner system
   - Tests: integration tests with mock tuners

4. **M2.4**: Add example config entries to config.example.yaml
   - Common FleetSync frequencies
   - Configuration documentation

**Deliverables**:
- `config.go` modifications (FleetSyncChannelConfig struct)
- `cmd/gophertrunk/fleetync.go` (startup & wiring, ~200 lines)
- `config.example.yaml` additions
- `internal/events/events.go` (KindFleetyncMessage)
- `cmd/gophertrunk/fleetync_test.go`

### Epic 3: TUI Display & Real-Time Monitoring

**Objective**: Add FleetSync message display to the TUI, matching the pattern of other decoders.

**Milestones**:
1. **M3.1**: Create FleetSync view in TUI
   - Recent messages table (last 50-100 messages)
   - Columns: timestamp, src (fleet/unit), dest, cmd, subcommand, status
   - Tests: UI component tests with mock events

2. **M3.2**: Add filtering/search capability
   - Filter by source unit
   - Filter by destination unit
   - Filter by command/subcommand
   - Tests: filter logic tests

3. **M3.3**: Add details view for selected messages
   - Full payload hex dump
   - Interpreted fields (emergency, priority)
   - Raw status word
   - Tests: details rendering

**Deliverables**:
- `internal/tui/fleetync.go` (~250 lines)
- `internal/tui/fleetync_test.go`
- Updated TUI main view with FleetSync section

### Epic 4: Persistent Storage & Querying

**Objective**: Store decoded FleetSync messages in database for historical analysis and pattern detection.

**Milestones**:
1. **M4.1**: Design storage schema
   - fleetync_messages table
   - Columns: timestamp, src_fleet, src_unit, dst_fleet, dst_unit, command, subcommand, payload, raw_message
   - Indexes for query performance

2. **M4.2**: Implement database operations
   - Insert message
   - Query by time range
   - Query by unit ID
   - Tests: SQLite integration tests

3. **M4.3**: Wire storage subscriber to event bus
   - Persist incoming events
   - Handle write failures gracefully
   - Tests: event-to-storage integration

4. **M4.4**: Create REST API endpoints
   - `/api/v1/fleetync/messages` (paginated list, query filters)
   - `/api/v1/fleetync/messages/{id}` (single message)
   - Tests: endpoint integration tests

**Deliverables**:
- `internal/storage/fleetync.go` (~200 lines)
- `internal/storage/fleetync_test.go`
- API endpoint implementations
- API tests

### Epic 5: Broadcast Backend Integration

**Objective**: Allow FleetSync messages to be exported to external systems (webhooks, files, etc.).

**Milestones**:
1. **M5.1**: Create FleetSync broadcast message format
   - JSON structure mirroring call-export format
   - Metadata + payload encoding
   - Tests: format validation

2. **M5.2**: Implement webhook integration
   - POST to configured webhooks on message decode
   - Retry logic leveraging broadcast.Manager
   - System filtering support
   - Tests: HTTP mock tests

3. **M5.3**: Implement file spool backend
   - Write JSON + raw bytes to disk
   - Directory structure: `fleetync-{nanosecond}-cmd{cmd}-unit{unit}`
   - Tests: filesystem write tests

4. **M5.4**: Documentation and examples
   - Webhook payload documentation
   - Integration examples (IFTTT, custom handlers)

**Deliverables**:
- Modifications to broadcast system for FleetSync events
- Example broadcast configurations
- Documentation

### Epic 6: Advanced Features & Optimization

**Objective**: Performance optimization, error handling, and extended capabilities.

**Milestones**:
1. **M6.1**: Performance optimization
   - Profile demodulator under load
   - Optimize filter implementations
   - Multi-channel CPU efficiency
   - Tests: load/stress tests

2. **M6.2**: Error handling & recovery
   - Graceful degradation on malformed messages
   - Channel state recovery
   - Detailed error logging
   - Tests: error condition handling

3. **M6.3**: Extended unit ID mapping
   - Support for unit name databases
   - Alias mapping (agency/unit → display name)
   - Tests: mapping lookup tests

4. **M6.4**: Analytics & reporting
   - Message frequency statistics
   - Unit activity heatmaps
   - Command histograms
   - Tests: analytics aggregation tests

**Deliverables**:
- Performance profiles & optimization report
- Analytics dashboard components
- Error handling improvements

---

## Implementation Sequence & Timeline

### Phase 1: Foundation (Weeks 1-2)
- **Sprint 1**: Epic 1 - M1.1 through M1.3 (FSK demodulation and FS1/FS2 sync)
- **Sprint 2**: Epic 1 - M1.4 through M1.5 (Frame assembly and message parsing)

### Phase 2: Integration (Week 3)
- **Sprint 3**: Epic 2 - M2.1 through M2.4 (Config, events, startup wiring)

### Phase 3: Visibility (Week 4)
- **Sprint 4**: Epic 3 - M3.1 through M3.3 (TUI implementation)

### Phase 4: Persistence (Week 5)
- **Sprint 5**: Epic 4 - M4.1 through M4.4 (Storage and API)

### Phase 5: Export (Week 6)
- **Sprint 6**: Epic 5 - M5.1 through M5.4 (Broadcast integration)

### Phase 6: Polish (Week 7+)
- **Sprint 7**: Epic 6 - Advanced features and optimization

---

## Testing Strategy

Each milestone includes:
- **Unit Tests**: 95%+ coverage of package functionality
- **Integration Tests**: Component interactions (e.g., demod → decoder)
- **End-to-End Tests**: Full pipeline from config → TUI display
- **Test Data**: Known test vectors from reference implementation
- **Regression Tests**: Ensure no regression on other radio systems

**Test Organization**:
```
internal/radio/fleetync/
├── demod.go
├── demod_test.go         # Unit tests for FSK demodulation
├── decoder.go
├── decoder_test.go       # Unit tests for sync/frame/assembly
├── message.go
├── message_test.go       # Unit tests for status word parsing
└── types.go

cmd/gophertrunk/
└── fleetync_test.go      # Integration tests (config → startup)

internal/storage/
└── fleetync_test.go      # Storage integration tests

internal/tui/
└── fleetync_test.go      # TUI rendering tests
```

**Test Fixtures**:
- Reference test vectors from fsync-mdc1200-decode project
- Synthetic audio samples for known messages
- Mock event subscriptions

---

## Documentation Deliverables

1. **Architecture Document** (this plan)
2. **User Guide**: Configuring FleetSync channels
   - Frequency selection guidance
   - Audio level tuning
   - Troubleshooting decoder issues
3. **Developer Guide**: FleetSync message format, state machine, decoder algorithm
4. **API Documentation**: REST endpoints, event formats
5. **Example Configs**: Common FleetSync frequencies and configurations
6. **Decoder Implementation Notes**: Porting decisions, optimization rationale

---

## Success Criteria

- ✓ Both FleetSync I and II formats decode correctly
- ✓ Multiple channels (≥10) decode simultaneously without interference
- ✓ TUI displays messages with correct metadata
- ✓ Messages persist to SQLite with query performance <100ms
- ✓ REST API serves messages with filtering
- ✓ Broadcast backends (webhook/file) export successfully
- ✓ 95%+ test coverage on all new code
- ✓ Full documentation and examples
- ✓ No regression on existing radio systems (DMR, P25, LTR, NXDN, etc.)
- ✓ Frequent, detailed commits (minimum 15-20 commits throughout)

---

## References

- **Reference Implementation**: https://github.com/russinnes/fsync-mdc1200-decode
- **GopherTrunk Architecture**: See internal/radio/{dmr,p25,ltr,nxdn}
- **Existing Integrations**: APRS (internal/aprs), Paging (internal/paging)

---

## Appendix: FleetSync Message Format

### Status Word (32-bit)
```
[15:0]   Address (source fleet/unit)
[23:16]  Command
[31:24]  Subcommand
         Emergency flag (bit in subcommand)
         All-flag (broadcast, bit in subcommand)
```

### Common Commands
- `0x00`: Voice Channel Grant
- `0x01`: Status/Telemetry
- `0x02`: Emergency
- `0x03`: Acknowledgment
- (Additional commands per Kenwood documentation)

### Payload Formats
- Varies by command/subcommand
- Typically 0-8 bytes
- May be empty (status-only)

---

## Known Non-Goals

- ~~MDC1200 (Motorola) decoder~~ (separate implementation if needed)
- ~~Encoding/Transmission~~ (receive-only for initial implementation)
- ~~Integration with Motorola SmartZone~~ (separate system)
- ~~Real-time voice demodulation alongside FleetSync~~ (handled separately)
