---
layout: page
title: Constellation panel
description: Live IQ scatter for visually identifying signal shape — PSK, FSK, AM, noise — without launching a separate SDR receiver
nav_group: Operate
---

# Constellation panel

The **Constellation panel** (web `/constellation`) renders a live
2D scatter of the IQ samples coming off whichever SDR you pick.
It's the "what does this signal *look* like" view — useful for
identifying modulation shape, checking demod / equalizer health,
and confirming that the SDR is actually receiving something you
care about before committing to a deeper decode pipeline.

## What you see

The X axis is the in-phase (I) component, the Y axis is the
quadrature (Q) component, both normalized to ±1. Brighter dots are
more recent. Reference rings show |z| = 0.5 and |z| = 1.0 so you
can eyeball amplitude.

Common shapes:

| Shape | Likely signal |
| --- | --- |
| One bright dot off-centre | DC bias / unmodulated carrier |
| Two clusters at ±0.5 + 0i | BPSK |
| Four clusters in a square | QPSK / π/4-DQPSK |
| Two horizontal arcs | 2-FSK |
| Four arcs (top + bottom of unit circle) | C4FM / 4-FSK |
| Diffuse rotating cluster, amplitude-modulated | AM voice |
| Diffuse circle around the origin | Wideband noise / nothing on this frequency |
| Spiral expanding outward | Strong frequency offset (re-tune or fix PPM) |

## How it works

- The panel opens a WebSocket to `WS /api/v1/diag/iq?device=...&rate=2000`.
- The daemon decimates the SDR's full-rate IQ stream by a stride
  (`input_rate / target_rate`) — no anti-alias filter; the goal is
  visualization, not faithful spectral reconstruction.
- Frames arrive every ~50 ms with ~100 points each; the panel
  keeps a rolling buffer of the last 2000 points and repaints the
  canvas each frame.
- Energy (the average power of the *pre*-decimation chunk in
  dBFS) is stamped on every frame and shown in the tuning line —
  helps tell "signal present" apart from "wideband noise that
  happens to look circular."

## Limitations

- **Not a demodulator.** This is a sample-domain view of the raw
  IQ — useful as a sanity check, not as a decoder. To actually
  decode a signal, configure it as a `trunking.systems` entry or
  `scanner.conventional` channel and let the per-protocol
  pipeline take over.
- **Decimation is brutal.** The stride decimator throws away
  spectral content above `target_rate / 2`. Wideband signals
  appear smeared. The constellation is most useful for symbol-
  domain signals that have already been narrow-channelized — e.g.
  pointed at a single FM repeater or P25 voice channel — rather
  than a 2.4 MHz wideband capture.
- **Single-SDR-at-a-time.** Each WS subscriber runs its own
  decimator; CPU scales linearly with the number of open panels.
  Fine for the single-operator deployments GopherTrunk targets.

## Implementation

| Path | Role |
| --- | --- |
| `internal/dsp/diag/iqstream.go` | `Decimator` — decimates IQ chunks by stride, computes per-frame energy, batches points into wire frames |
| `internal/api/diag.go` | `DiagProvider` interface + `WS /api/v1/diag/iq` handler |
| `cmd/gophertrunk/diag_provider.go` | Daemon-side `diagProvider` — wires the decimator to the iqtap broker for each WS subscriber |
| `web/src/api/diag.ts` | Typed client with auto-reconnect / backoff |
| `web/src/panels/Constellation.tsx` | Canvas scatter renderer |

The decimator runs on top of the iqtap broker (PR #365), so it
fans out from the same IQ source the trunking decoder is reading
without disturbing decode. Multiple panels open against the same
SDR don't double the SDR's CPU cost — they share the broker
subscription up to the broker's drop-on-full bound.
