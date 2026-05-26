---
layout: page
title: APRS / AX.25
description: Decoded APRS packet pipeline — events bus, SQLite log, REST endpoint, web panel
nav_group: Reference
---

# APRS / AX.25

GopherTrunk surfaces decoded **APRS** (Automatic Packet Reporting
System) packets through the same bus / SQLite / REST / web-panel
stack the POCSAG pager pipeline uses. APRS is the amateur-radio
metadata bus carrying position beacons, SAR coordination, weather,
status messages, and direct text messaging — useful alongside
trunked-voice scanning for emergency-comms-adjacent operators.

This page documents the **pipeline scaffolding**: what's wired,
what's persisted, what's queryable, and what the web panel
renders. The protocol parsers (AX.25 frame layout, APRS info-field
sub-types) and the DSP layer (1200 Bd Bell-202 AFSK → HDLC
de-stuff → frame delivery) are tracked separately under "What's
pending" below.

## What's wired

### Events bus
- `events.KindAPRSPacket` — published once per decoded frame.
  Payload is a `storage.APRSPacket` carrying the AX.25 envelope
  (src + dst + path) plus the APRS sub-type + decoded summary +
  optional lat/lon.

### Storage
- New SQLite table `aprs_log` (append-only migration alongside
  `pager_log`, `call_log`, etc.):
  - `received_at` (nanoseconds), `src`, `dst`, `path`, `type`,
    `body`, `latitude`, `longitude`, `raw_info`, `fcs_ok`
  - Indexes on `(received_at)` and `(src, received_at)`
- `storage.APRSLog` bus subscriber writes one row per
  `KindAPRSPacket` event. Mirrors the `PagerLog` lifecycle —
  subscribes at construction so events published before `Run()`
  begins aren't lost.

### REST
- `GET /api/v1/aprs/packets?limit=N` — most recent packets,
  newest first. Default 200, max 5000. Returns 503 when the
  storage layer isn't wired (daemon started without
  `storage.path`).

### Web panel
- `/aprs` — live list with columns:
  - Received (HH:MM:SS, daemon clock)
  - Src → Dst (with optional digipeater path on a second line)
  - Type (position / message / status / bulletin / weather /
    telemetry / object / mic-e / unknown)
  - Body (one-line summary)
  - Lat / Lon (for position-bearing types; em-dash otherwise)
- Polls every 5 s. Frames with `fcs_ok = false` highlight in
  yellow as a marginal-signal indicator.

## What's pending

- **AX.25 frame parser + APRS info-field decoder.** Pure-Go
  parsers that turn bit-de-stuffed AX.25 frames into the
  `events.KindAPRSPacket` payload this pipeline expects. Mirrors
  the POCSAG protocol-layer split (#372 → #373) — the bus / log
  / REST / UI ship in their own PR (this one) so the protocol
  layer can be reviewed against its own concerns. Until that
  ships, no events fire and the panel stays at the empty state.
- **DSP receiver.** 1200 Bd Bell-202 AFSK demodulation, HDLC
  bit-stuffing reversal, 0x7E flag-delimited frame extraction.
  Plugs into the AX.25 parser the moment both pieces land.
  Pattern matches the POCSAG receiver (#378): iqtap broker
  subscriber → narrowband FM demod → bit slicer → frame
  delivery → bus event.
- **Mic-E decoder.** Compressed lat/lon format common on mobile
  trackers (Kenwood TH-D74, Yaesu FT-3D). ~200 LOC for base-91
  unpacking + speed / course / altitude decode.
- **Live map.** Position-bearing types have lat/lon — a
  Leaflet / MapLibre overlay on top of `/aprs` showing the most
  recent station fixes is an obvious next step once the panel
  has real traffic.

## References

- APRS Protocol Reference 1.0.1 (1998-08-07) — `http://www.aprs.org/doc/APRS101.PDF`
- AX.25 Link Access Protocol v2.2 (TAPR / ARRL, 1998) — `http://www.ax25.net/AX25.2.2-Jul%2098-2.pdf`
- aprs.fi parser source — real-world variant cross-check
