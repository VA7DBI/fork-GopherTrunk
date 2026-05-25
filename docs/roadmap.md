---
layout: page
title: Roadmap
description: What's still on the table for GopherTrunk
nav_group: Reference
---

# Roadmap

What's still on the table. Order isn't fixed; each item is
contained to its own package and lands independently. The historic
log of shipped work lives in
[`CHANGELOG.md`](https://github.com/MattCheramie/GopherTrunk/blob/main/CHANGELOG.md).

## DMR ARC4 / RC4 known-key voice decryption (issue #276)

DMR systems that protect voice with ARC4-based "Enhanced Privacy"
can be given known keys per system under
`trunking.systems[].encryption_keys`. The decode pipeline is
complete for *unencrypted* DMR voice — it decodes end-to-end to a
playable WAV via the DMR voice superframe decoder + AMBE+2
forward-error-correction
([`internal/radio/dmr/voice/`](https://github.com/MattCheramie/GopherTrunk/tree/main/internal/radio/dmr/voice)),
the composer DMR voice chain, and the AMBE+2 3600x2450 vocoder.

The dependency-free RC4 keystream generator
([`internal/crypto/rc4/`](https://github.com/MattCheramie/GopherTrunk/tree/main/internal/crypto/rc4))
is in place; the one remaining stage for *encrypted* calls is the
in-process RC4 descramble (PI-header parse + keystream
application). See [dmr-encryption.md](dmr-encryption.md) for the
full status, configuration guide, and out-of-band decode recipe.

Decryption is **known-key only** — no key recovery — matching the
SDRTrunk / DSD-FME / OP25 model.

## DVSI USB-3000 / AMBE-3003 hardware backend

The `Vocoder` + AMBE-3003 wire protocol + `voice.Vocoder`
interface conformance ship in
[`internal/voice/dvsi/`](https://github.com/MattCheramie/GopherTrunk/tree/main/internal/voice/dvsi)
behind `-tags dvsi`. CI exercises the wire protocol + Vocoder
plumbing through the scripted mock Transport and the
software-loopback Transport (`make test-dvsi`).

The USB / FTDI bulk-endpoint plumbing that talks to a physical
chip remains a stub returning `ErrNoDevice` — the recorder
fallback chain activates cleanly when no chip is connected. The
actual FTDI hardware integration lands when a DVSI USB-3000 is
available for round-trip testing.

## Vocoder level calibration (reference data)

The plumbing ships — comparison harness at
`internal/voice/calibrate`, per-vocoder testdata READMEs at
`internal/voice/{imbe,ambe2}/testdata/`, end-to-end recipe at
[voice-calibration.md](voice-calibration.md), and a one-off CLI
wrapper at `cmd/voice-calibrate`:

```sh
go run ./cmd/voice-calibrate -raw call.raw -ref-wav ref.wav -vocoder imbe
```

Operators just need to drop reference WAVs decoded by DSD-FME /
OP25 from the same `.raw` into testdata; the existing calibrate
tests run unguarded once both files are present.

AMBE+2 DTMF dual-tone synthesis (b₁ ∈ [128, 143]) is wired
against the ITU-T Q.23 4×4 matrix. Knox / call-alert pairs
(b₁ ∈ [144, 163]) are vendor-specific — operators with a
per-vendor reference register the (freqA, freqB) pair via
`ambe2.SetKnoxTone` or load a curated `ambe2.KnoxPreset` bundle
via `ambe2.RegisterPreset`.

## YSF on-air interleaver / puncture validation

The spec-level on-air codec ships in
`internal/radio/ysf/fich_trellis.go`'s `EncodeFICHOnAir` /
`DecodeFICHOnAir` per the MMDVMHost / DSDcc / Pi-Star reference
(puncture positions `{0, 1, 102, 103}`, column-major 10×10
interleave). Unit tests confirm every single-bit-flip is
Viterbi-corrected.

The remaining work is calibration against a real captured YSF
transmission — if the captured FICH fails CRC after on-air
decode, swap to the alternate schedule per
`samples/ysf/README.md`.
