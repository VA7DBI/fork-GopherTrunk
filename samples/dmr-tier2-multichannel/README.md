# One dongle, several DMR repeaters

This sample config monitors four conventional DMR Tier II repeaters
that all live inside a single 2.4 MHz IQ window around 453.5 MHz with
one RTL-SDR. No extra hardware, no hardware re-tune per call - the
daemon's internal channelizer (`internal/dsp/tuner`) extracts a
separate 48 kHz baseband stream per repeater and feeds an independent
DMR decoder for each.

## When to use this layout

- You have several DMR Tier II repeaters whose carriers cluster
  inside the IQ bandwidth of one dongle (typically ≤ 2.4 MHz of
  total spread).
- You have a DMR Tier III trunked control channel inside the same
  IQ window — the wideband engine can decode the T3 CC alongside
  any T2 carriers. The commented-out section in `config.yaml`
  shows how. (Voice grants from T3 still need a `role: voice`
  SDR; the wideband dongle decodes the CC only.)
- You only own one SDR or want to free your other dongles for
  other systems.

## What it doesn't do

- Decode DMR Tier III voice grants directly on the wideband
  dongle. Grants from the T3 CC publish on the bus; the daemon's
  existing voice pool retunes a `role: voice` SDR to follow them.
  A virtual voice pool that allocates a tuner tap per grant is
  planned as a follow-up so T3-on-one-dongle becomes possible.
- Other protocols (P25, NXDN, TETRA, etc.) inside the wideband
  IQ. Wideband is currently DMR-only.
- The wideband dongle is dedicated to this layout - it can't
  double as a voice-pool member or a control-channel hunter.

## Tuning

Edit `config.yaml`:

1. **`serial`** - run `gophertrunk sdr list` and copy your dongle's
   serial. The daemon binds the channel list to the device by serial.
2. **`center_freq_hz`** - the centre frequency the dongle sits on.
   Pick a value that puts every `channels[].frequency_hz` inside
   `center ± sample_rate/2`, with a 5 % guard at each edge (so
   ±1.08 MHz at the default 2.4 MS/s rate). The config validator
   rejects out-of-band channels at startup.
3. **`channels`** - one entry per repeater you want to decode. Each
   one references the system you declared in `trunking.systems`.
4. **`tuner_strategy`** (optional) - `auto` (default) picks DDC for
   ≤ 6 channels and a polyphase channelizer above that. Force `ddc`
   or `polyphase` to override.

## Running

```sh
go run ./cmd/gophertrunk run -config samples/dmr-tier2-multichannel/config.yaml
```

The startup log line `widebandt2: starting` confirms the engine came
up; `center_freq_hz`, `channels`, and `strategy` fields show what was
wired. `cc.locked` and `grant` events fire per repeater frequency as
calls arrive.

## Limits and follow-ups

- DMR Tier III voice on the wideband dongle is planned (the engine
  and DSP can do it; the missing piece is a virtual voice pool that
  allocates a tuner tap per grant instead of retuning a physical
  dongle). Today T3 voice grants from a wideband CC route to a
  physical `role: voice` SDR.
- Multi-protocol wideband (P25 + DMR + NXDN sharing one dongle) is
  not on the near-term roadmap.
