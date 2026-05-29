---
layout: page
title: FleetSync
description: Kenwood FleetSync I/II decoder configuration and runtime behavior
nav_group: Reference
---

# FleetSync decoder

GopherTrunk can decode Kenwood FleetSync control/data bursts from
conventional channels and publish each decoded frame as a live event.

Current scope in this branch:

- FleetSync channel config (`fleetsync.channels`) is supported.
- The daemon can start one FleetSync receiver per configured channel.
- Receivers publish `events.KindFleetSyncMessage` payloads on the bus.
- SQLite-backed FleetSync message persistence is available when storage is enabled.
- Read-only API endpoints expose recent and per-message FleetSync logs.
- Core parser/demod packages ship with unit tests.

## Configuration

Add one or more FleetSync channels in config YAML:

```yaml
fleetsync:
  channels:
    - enabled: true
      name: "City Utilities FleetSync"
      serial: "00000002"
      frequency_hz: 451812500
      version: auto      # auto | fleetsync1 | fleetsync2
      baud_hz: 1200      # optional (must be 1200 when set)
```

Field reference:

- `enabled`: enable/disable the channel without deleting it.
- `name`: optional label for logs/ops context.
- `serial`: SDR serial to bind this channel to.
- `frequency_hz`: center frequency for this FleetSync channel.
- `version`: force decoder mode or let it auto-fallback.
- `baud_hz`: FleetSync signalling baud (currently 1200 only).

## Runtime behavior

For each enabled channel the daemon:

1. Finds the SDR IQ broker by `serial`.
2. Tunes to `frequency_hz`.
3. Runs IQ -> FM demod -> real resample (8 kHz) -> FleetSync decode.
4. Publishes a `fleetsync.message` event.

The payload is `internal/radio/fleetync.Message` and includes:

- timestamp
- FleetSync version
- command/subcommand
- source and destination addressing
- emergency/all/priority flags
- raw frame bytes and parsed payload bytes

## API

When `storage.path` is configured, the daemon persists FleetSync frames
to SQLite and exposes:

- `GET /api/v1/fleetsync/messages`
- `GET /api/v1/fleetsync/messages/{id}`
- `GET /api/v1/fleetsync/stats`

The list endpoint accepts optional query parameters:

- `limit`
- `source_unit`
- `destination_unit`
- `command` (decimal or `0xNN`)
- `since` / `until` (RFC3339)

The stats endpoint accepts the same filters (except `limit`) and
returns aggregate counters plus a per-command histogram.

It also includes a `runtime` section with live decoder telemetry:

- `messages_emitted`
- `total_samples`
- `total_messages_rx`
- `sync_errors`
- `crc_errors`
- `last_message_time`
- `message_rate`

And a `runtime.channels[]` per-receiver breakdown keyed by:

- `source`
- `messages_emitted`
- `total_samples`
- `total_messages_rx`
- `sync_errors`
- `crc_errors`
- `last_message_time`
- `message_rate`

Exporter health is included under `runtime.export`:

- `queued`
- `dropped`
- `last_event_at` (most recent export event accepted or dropped by the exporter)
- `last_send_at` (most recent successful backend delivery timestamp)
- `last_failure_at` (most recent terminal backend delivery failure timestamp)
- `telemetry_age_seconds` (seconds since the freshest export liveness timestamp)
- `queue_depth` (current messages waiting in exporter queue)
- `queue_capacity` (configured exporter queue size)
- `queue_utilization` (fraction of queue in use, from 0.0 to 1.0)
- `queue_utilization_last_60s_avg` (rolling 60-second average queue utilization)
- `queue_utilization_last_60s_peak` (rolling 60-second peak queue utilization)
- `dropped_by_source` (map of source label to dropped count)
- `dropped_per_minute_by_source` (map of source label to average drops/minute since exporter start)
- `sent_last_60s_total` (rolling total successful backend deliveries across all backends)
- `failed_last_60s_total` (rolling total failed backend deliveries across all backends)
- `success_rate_last_60s` (rolling success ratio across backend outcomes, 0.0 to 1.0)
- `failure_rate_last_60s` (rolling failure ratio across backend outcomes, 0.0 to 1.0)
- `retried_last_60s_total` (rolling total retry attempts across all backends)
- `retry_rate_last_60s` (rolling retry pressure ratio: retries divided by attempts in the last 60 seconds)
- `dropped_to_attempts_rate_last_60s` (rolling backpressure loss ratio: dropped messages divided by backend attempts in the last 60 seconds)
- `saturation_severity_last_60s` (weighted 0.0-1.0 severity score combining rolling queue utilization, dropped-to-attempts ratio, and retry rate)
- `saturation_state_last_60s` (severity bucket derived from score: `healthy`, `warning`, or `critical`)
- `dropped_last_60s_total` (total drops across all sources in the rolling last 60 seconds)
- `dropped_per_minute_last_60s_total` (rolling last-60s total drops normalized to per-minute)
- `dropped_last_60s_by_source` (map of source label to drops observed in the rolling last 60 seconds)
- `dropped_per_minute_last_60s_by_source` (rolling last-60s drops normalized to per-minute)
- `backends[]` with per backend:
- `name`
- `sent`
- `sent_last_60s`
- `success_rate_last_60s`
- `failed`
- `failed_last_60s`
- `failure_rate_last_60s`
- `attempts`
- `attempts_last_60s`
- `retried`
- `retried_last_60s`

## Export

FleetSync Epic 5 adds outbound export feeds under `broadcast.fleetsync`.
Two backend types are supported:

- `webhooks`: POST one JSON document per decoded frame
- `spool`: write one directory per decoded frame containing JSON + raw bytes

The export payload mirrors the API fields and includes:

- `received_at`
- `source`
- `version`, `command`, `subcommand`
- source and destination fleet/unit addressing
- `all_flag`, `emergency`, `priority`
- `payload_hex`, `raw_hex`

Source filters match the configured FleetSync channel `name` when set,
otherwise the SDR `serial`.

## Validation and errors

Config validation rejects:

- missing `serial` on enabled channels
- missing `frequency_hz` on enabled channels
- invalid `version` values
- unsupported `baud_hz` values

Validation errors are path-qualified (for example,
`fleetsync.channels[0]: serial required`).

## Notes

- FleetSync channels are non-essential services: if one channel cannot
  start, the daemon logs a warning and continues trunking operation.
- This decoder path is implemented as one receiver per configured
  channel; multi-channel-from-one-wideband-SDR is a follow-up item in
  the FleetSync plan.
