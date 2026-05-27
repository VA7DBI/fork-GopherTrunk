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

## Protocol layer (`internal/radio/aprs`)

Pure-Go AX.25 frame parser + APRS info-field decoder that turn
bit-de-stuffed AX.25 frames into the `events.KindAPRSPacket`
payload the bus / log / REST / UI scaffolding above expects.

- **`internal/radio/aprs/ax25`** — AX.25 UI-frame parser:
  7-byte address packing with the spec's bit-shifted ASCII
  callsigns, up to 8 digipeater path entries, HDLC CRC-16-CCITT
  validation via the standard 0x8408 reflected-bit polynomial,
  conventional `W1AW-9` / `WIDE2-1*` display helpers. HBit
  handling is only meaningful for path entries; dst + src use
  that bit position for the C-bit (Command/Response indicator),
  which the APRS console convention ignores.
- **`internal/radio/aprs`** — info-field decoder. Recognised
  packet types (data-type indicator → decoded fields):
  - `!` / `=` — position without timestamp; messaging flag set
    on `=`
  - `/` / `@` — position with raw 7-char timestamp; messaging
    flag set on `@`
  - `:` — message (9-char trimmed addressee, body, optional
    `{seqno}`, `ack` / `rej` short-form) or bulletin
    (`BLN`-prefixed addressee → `Bulletin` struct)
  - `>` — status (free-text)
  - `;` / `_` / `T#` / Mic-E — type-tagged with payloads
    stashed for follow-up decoders
  - anything else — `TypeUnknown`, raw bytes preserved
- Position parsing covers both hemispheres (N/S, E/W), the
  spec's "ambiguity space" convention (low-precision digits as
  space — treated as 0), and the standard `DDMM.hhH` /
  `DDDMM.hhH` hundredths-of-a-minute encoding.

## Bit-stream pipeline (`internal/radio/aprs/hdlc` + `receiver`)

Once a DSP layer produces LSB-first wire bits, the following
glue turns them into operator-visible packets — no daemon changes
required to wire it in (the receiver is a `Push(bit byte)`
function).

- **`internal/radio/aprs/hdlc`** — bit-stream framer. Tracks the
  sliding-flag detector (HDLC's 0x7E delimiter), reverses the
  bit-stuffing (after 5 consecutive 1s, drop the next 0),
  resyncs on shared-flag packing, aborts on 7-or-more-1s runs
  (the HDLC abort sequence). Emits one fully-formed AX.25 frame
  body per (flag, ..., flag) sequence. Note: HDLC framing is
  the layer below the AX.25 frame parser — the bit-stuffing
  reversal happens here, not in `ax25`.
- **`internal/radio/aprs/receiver`** — orchestrator. Threads
  bits through the framer, hands frame bodies to `ax25.Parse`,
  the info field to `aprs.Decode`, and publishes one
  `events.KindAPRSPacket` per successfully-decoded UI frame.
  Bus payload is `storage.APRSPacket` carrying the AX.25
  envelope, the APRS sub-type label, the decoded summary
  string, and (for position-bearing types) lat/lon. The
  receiver counts frames in / parsed / CRC-failed / emitted
  for future `/metrics` surfacing. Options expose
  `DropBadFCS` and `DropNonUI` toggles for operators who'd
  rather lose marginal frames than see them on the panel.

## What's pending

- **DSP receiver.** 1200 Bd Bell-202 AFSK demodulation +
  NRZI decoding to produce the LSB-first wire bits the HDLC
  framer expects. Pattern matches the POCSAG receiver (#378):
  iqtap broker subscriber → narrowband FM demod → bit slicer →
  `receiver.Push(bit)`. Once that ships, the end-to-end
  pipeline goes live.
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
