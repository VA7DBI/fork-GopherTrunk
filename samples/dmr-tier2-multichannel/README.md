# One dongle, several DMR Tier II repeaters

This sample config monitors four conventional DMR Tier II repeaters
that all live inside a single 2.4 MHz IQ window around 453.5 MHz with
one RTL-SDR. No extra hardware, no hardware re-tune per call - the
daemon's internal channelizer (`internal/dsp/tuner`) extracts a
separate 48 kHz baseband stream per repeater and feeds an independent
T2 decoder for each.

## When to use this layout

- You have several DMR Tier II repeaters whose carriers cluster
  inside the IQ bandwidth of one dongle (typically ≤ 2.4 MHz of
  total spread).
- You only own one SDR or want to free your other dongles for
  other systems.
- All the repeaters belong to the same conventional system - i.e.
  you'd happily merge their call streams into one talkgroup CSV.

## What it doesn't do

- Trunked DMR (Tier III) grant chasing is not supported by the
  wideband role in v1. Add a separate `role: voice` dongle for
  trunked systems.
- Other protocols (P25, NXDN, TETRA, etc.) inside the wideband
  IQ are not decoded. Wideband is currently DMR Tier II only.
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

- DMR Tier III via wideband is planned (the engine and the DSP can
  do it - the voice-pool adapter is the remaining piece).
- Multi-protocol wideband (P25 + DMR + NXDN sharing one dongle) is
  not on the v1 roadmap.
