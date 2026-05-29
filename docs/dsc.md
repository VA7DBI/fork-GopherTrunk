---
layout: page
title: DSC / Marine Distress
description: Decoded marine DSC pipeline — events bus, SQLite log, REST endpoint, web panel
nav_group: Reference
---

# DSC / Marine Distress

GopherTrunk decodes **Digital Selective Calling** messages —
the SOLAS-mandated digital signalling that triggers distress /
urgency / safety / routine calls on marine VHF channel 70
(156.525 MHz) and the medium / high-frequency DSC channels
(2.187.5, 8.414.5, 12.577, 16.804.5 kHz). DSC is what fires every
GMDSS distress alert; the routine calls broadcast the working
voice channel two stations are about to switch to. A coast-guard
MMSI lighting up the channel-70 stream is near-instant visibility
into search-and-rescue activity.

This page documents the **pipeline scaffolding**: what's wired,
what's persisted, what's queryable, and what the web panel
renders. The DSP layer (1200 Bd FSK at 1300 / 2100 Hz tones →
10-bit symbol assembly → BCH check + DX/RX redundancy → 7-bit
symbol stream to `dsc.Decode`) is tracked separately under
"What's pending" below.

## What's wired

### Events bus
- `events.KindDSCMessage` — published once per decoded DSC
  sequence. Payload is a `storage.DSCMessage` carrying source
  MMSI + format (distress / all-ships / individual / ...) +
  category (distress / urgency / safety / routine) + nature
  (for distress alerts) + position (for distress alerts that
  included one) + time UTC.

### Storage
- New SQLite table `dsc_log` (append-only migration alongside
  `vessel_log`, `aprs_log`, `pager_log`):
  - `received_at` (nanoseconds), `format`, `category`,
    `self_mmsi`, `target_mmsi`, `nature`, `time_utc`,
    `latitude`, `longitude`, `has_position`, `body`, `raw_hex`
  - Indexes on `(received_at)` and `(self_mmsi, received_at)`
- `storage.DSCLog` bus subscriber writes one row per
  `KindDSCMessage` event. Mirrors the `VesselLog` / `APRSLog`
  lifecycle — subscribes at construction so events published
  before `Run()` begins aren't lost.

### REST
- `GET /api/v1/dsc/messages?limit=N` — most recent sequences,
  newest first. Default 200, max 5000. Returns 503 when the
  storage layer isn't wired (daemon started without
  `storage.path`).

### Web panel
- `/dsc` — live list with columns:
  - Received (HH:MM:SS, daemon clock)
  - Format (distress / all-ships / individual / group / ...)
  - Category (distress / urgency / safety / routine)
  - Self MMSI
  - Target / Nature (target MMSI for individual / group calls;
    distress nature for distress alerts)
  - Body (one-line summary; distress alerts include the time
    UTC on a second line)
  - Lat / Lon (em-dash for non-position-bearing messages)
- Polls every 5 s. Rows tint by category: distress = red,
  urgency = orange, safety = blue, routine = default.

## Protocol layer (`internal/radio/dsc`)

Pure-Go DSC message parser. The bit-stream layer above
(separate PR) handles sync detection, BCH(10,7) syndrome check,
and the DX / RX redundancy merge — by the time symbols reach
`dsc.Decode` each entry is one 7-bit value 0..127.

- **BCH(10,7) codec** (`bch.go`) — encode + check helpers
  for the CRC-3 wrapper ITU-R M.493-15 §3.4 specifies. Generator
  polynomial `g(x) = x³ + x + 1` (binary `1011`). The code's
  minimum Hamming distance is **2**, not 3, so the syndrome
  reliably **detects** single-bit errors but doesn't reliably
  **correct** them at this layer — DSC achieves the actual
  correction via DX/RX redundancy (each character is sent
  twice on the wire and the receiver compares the two streams).
- **MMSI codec** (`codec.go`) — 5 symbols × 2 decimal digits
  per symbol decode to a 9-digit MMSI (the 10th digit is the
  format-extension nibble and ignored).
- **Position codec** (`codec.go`) — 5 symbols carrying a
  10-digit position string `Q.DD.MM.DDD.MM` where `Q` is the
  quadrant (0 = NE, 1 = NW, 2 = SE, 3 = SW). The all-9s sentinel
  for "position unknown" surfaces as `HasPosition = false`.
- **Type dispatch** (`dsc.go`) — recognises every numbered
  format (112 = Distress, 116 = AllShips, 114 = Group, 120 =
  Individual, 102 = Geographic, 123 = AutoIndividual) and
  decodes the format-specific payload:
  - **Distress**: self-MMSI + nature of distress + position +
    time UTC.
  - **Non-distress**: target MMSI + category + self-MMSI;
    remaining fields stay on `RawSymbols` for the follow-up
    per-format parser.

Spec references:
- ITU-R M.493-15 (Recommendation, 2019) — DSC message format,
  symbol table, BCH(10,7) check, nature-of-distress codes.
- ITU-R M.541 — operational use, station identification,
  category routing.

## What's pending

- **DSP receiver.** 1200 Bd FSK (mark 1300 Hz, space 2100 Hz)
  on channel 70 (VHF) or one of the HF DSC channels. Pipeline:
  iqtap broker subscriber → FM demod → FFSK discriminator
  (reusing `internal/dsp/demod/ffsk` with DSC's tone
  frequencies) → symbol-time recovery → 10-bit symbol assembly
  → BCH syndrome check → DX / RX redundancy merge → 7-bit
  symbol stream into `dsc.Decode`. Pattern matches the APRS /
  AIS DSP frontends.
- **Multi-frame protocol.** A few DSC sequence types span
  multiple slots when transmitted (Auto-Individual ACK chains,
  multi-recipient calls). The single-frame parser covers the
  operational majority; the multi-frame path needs a per-MMSI
  buffer plus a sequence reassembler.
## Live map

Distress alerts that included a position render as red,
oversized markers on the shared Leaflet map at the top of
`/dsc` — the larger radius + distress-red colour pull the
operator's eye immediately. Nature of distress ("fire /
explosion", "sinking", etc.) appears in the marker tooltip.
The same `<PositionMap>` component renders on `/aprs`, `/ais`,
and `/adsb`.

## References

- ITU-R M.493-15 (2019) "Digital selective-calling system for
  use in the maritime mobile service" —
  `https://www.itu.int/rec/R-REC-M.493`
- ITU-R M.541 — operational guidance.
