---
layout: page
title: POCSAG paging decoder
description: Protocol layer for decoding POCSAG (CCIR 584) wireline FSK pager traffic — fire / EMS dispatch, commercial paging, amateur DAPNET
nav_group: Reference
---

# POCSAG paging decoder

GopherTrunk now decodes the **POCSAG** (Post Office Code
Standardisation Advisory Group, CCIR Recommendation 584) wireline
FSK pager protocol — the dominant pager protocol globally and the
one most fire / EMS departments use for tone-out dispatch
forwarding. The first PR lands the protocol layer (codeword parsing,
BCH(31,21), batch carve-up, numeric + alphanumeric message
reassembly) with thorough unit tests. The DSP wiring (FM demod →
bit slicer → sync detector → batch decoder → bus event) is a
focused follow-up PR.

## What's here today

- **`internal/radio/framing/bch_pocsag.go`** — BCH(31,21) encoder
  + brute-force minimum-Hamming-distance decoder, plus the
  trailing-parity helper POCSAG uses to stretch the code to 32
  wire bits. Generator polynomial: g(x) = x^10 + x^9 + x^8 + x^6
  + x^5 + x^3 + 1 (CCIR 584 §3.2.1).
- **`internal/radio/pager/pocsag/codeword.go`** — 32-bit codeword
  struct, sync (`0x7CD215D8`) + idle (`0x7A89C197`) pattern
  recognition, address/message/function decoding, parity check
  on the trailing bit.
- **`internal/radio/pager/pocsag/batch.go`** — batch carve-up
  (sync + 16 codewords × 8 frame slots), frame-slot index
  resolution, full-RIC reconstruction (18-bit address codeword
  field + 3-bit slot index → 21-bit pager address).
- **`internal/radio/pager/pocsag/message.go`** — numeric (5 BCD
  digits per codeword, with the CCIR 584 extended-character
  table: 0-9, *, U, space, -, ), ( ) and alphanumeric (7-bit
  LSB-first ASCII packed across 20-bit message fields)
  decoders. Trailing space-padding is trimmed.

## What's pending

- **DSP integration.** The FM-demodulated bit stream → POCSAG
  syncer → batch decoder pipeline (the equivalent of the existing
  P25 / DMR per-protocol `Process(stream, baseIdx)` adapters)
  lands in a follow-up PR. The protocol layer is ready to consume
  packed bits the moment the DSP layer exists.
- **Bus event + storage.** Once the DSP wiring exists, a new
  `events.KindPagerMessage` plus a `pager_log` SQLite table (mirroring
  `call_log` / `location_log`) ship alongside.
- **Web + TUI panel.** Renders the live pager message stream with
  RIC, function, and decoded text columns. Will subscribe to the
  same SSE stream the rest of the panels use.
- **FLEX.** A separate, higher-rate (1600 / 3200 / 6400 sps)
  Motorola pager protocol that shares the operator workflow but
  needs its own FEC (Reed-Muller + two-of-three majority decoder)
  and frame structure. Documented as a planned follow-up; the
  framework added here is the foundation.

## Testing

The protocol layer has 13 unit tests covering:

- BCH(31,21) round-trip (encode → decode = no errors), single-
  bit and double-bit error correction, triple-bit rejection
- Sync + idle codeword recognition
- Address + message codeword round-trips (encode → wire → decode
  reproduces the original fields)
- Single-bit error correction at every codeword position
- Parity-bit flip detection
- Frame-slot mapping (word index → slot index → RIC reconstruction)
- Batch carve-up with a synthetic address + message at a known slot
- Numeric BCD decode (including the CCIR 584 extended symbols,
  trailing-space trimming, LSB-first nibble order)
- Alphanumeric ASCII reassembly (7-bit LSB-first, character
  straddling 20-bit boundaries)
- Mixed-codeword slices (address + message words in one buffer
  — DecodeNumeric / DecodeAlpha ignore non-message codewords)

## Why now

Most operator workflows that already use GopherTrunk for trunked
voice want the local fire / EMS pager traffic alongside —
dispatch goes out on the trunked system AND on a paging
frequency, and seeing the pager text helps confirm "this
specific tone-out matched these specific crews."

Building on the iqtap broker (PR #365), the eventual DSP
pipeline will tap the same IQ stream the trunking decoder reads
on a separate broker subscriber, so adding POCSAG decode doesn't
double the USB / CPU cost of the SDR.

## References

- CCIR Recommendation 584-1, "Standard Codes and Format for
  International Radio Paging"
- [sdrtrunk POCSAG decoder](https://github.com/DSheirer/sdrtrunk)
  (Java) — sanity reference for the BCH polynomial choice and
  the LSB-first bit-order quirks
- [multimon-ng](https://github.com/EliasOenal/multimon-ng) (C) —
  the BCH lookup table + numeric BCD table used by most
  open-source POCSAG decoders
