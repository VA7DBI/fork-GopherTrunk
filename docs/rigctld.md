---
layout: page
title: rigctld TCP integration
description: Expose the daemon's tuning to external Hamlib clients (Cloudlog, GridTracker, satellite trackers, logging programs)
nav_group: Operate
---

# rigctld TCP integration

GopherTrunk can expose the **control SDR's frequency** over the
[Hamlib `rigctld` TCP wire
protocol](https://hamlib.sourceforge.net/manuals/4.5/rigctld.1.html)
so external amateur-radio tooling can read it (for logging) and
set it (for satellite Doppler correction, scheduled retunes, etc.)
without learning GopherTrunk's REST API.

Compatible with anything that already speaks rigctld:

- **Cloudlog** / **GridTracker** / **N1MM+** for logging
- **PSTRotator** for tracker integration
- **gpredict** for satellite Doppler
- The reference **`rigctl(1)`** CLI for ad-hoc scripting

## Quick start

Add a one-liner to `config.yaml`:

```yaml
api:
  rigctld: "127.0.0.1:4532"
```

Restart the daemon, then verify with the standard Hamlib CLI:

```sh
$ rigctl -m 2 -r 127.0.0.1:4532
Rig command: f
851012500
Rig command: F 851037500
Rig command: f
851037500
Rig command: q
```

(`-m 2` selects the "NET rigctl" backend — that's what every
rigctld client uses to talk to a remote rigctld server.)

## What's wired

| Command | Behaviour |
| --- | --- |
| `F` / `set_freq <hz>` | Tunes the **control SDR** to `<hz>`. Internally routes through the iqtap broker so the change survives a USB-disconnect cycle. |
| `f` / `get_freq` | Returns the last-set centre frequency in Hz. |
| `M` / `set_mode <mode> <pb>` | Accepted, no-op. Real clients call this on connect with `FM 12500` / `PKT 0`; the value doesn't influence GopherTrunk's pipeline. |
| `m` / `get_mode` | Returns `FM\n12500\n` — a reasonable proxy for what the daemon is doing on the control channel. |
| `V` / `set_vfo VFOA` | Accepted (single-VFO backend). Other VFO names return `RPRT -11`. |
| `v` / `get_vfo` | Returns `VFOA`. |
| `T` / `set_ptt 0` | Accepted. `set_ptt 1` returns `RPRT -11` — GopherTrunk is RX-only. |
| `t` / `get_ptt` | Returns `0`. |
| `chk_vfo` | Returns `CHKVFO 0` (single-VFO daemon). |
| `dump_state` | Minimal but well-formed capability dump so handshake-on-connect clients (Cloudlog, rigctl) don't choke. |
| `q` / `Q` / `exit` | Terminates the connection. |
| Anything else | Returns `RPRT -1` (Hamlib's "command not supported"). |

## Which SDR is the "rig"?

The rigctld server controls **the SDR with `role: control` in the
pool**. If no control SDR is configured (or none is attached at
startup) the server logs a warning and skips wiring itself — the
daemon keeps running, the trunking pipeline is unaffected, but
external rigctld clients will get connection refused on the
configured port until a control SDR comes up and the daemon is
restarted.

When the control SDR USB-disconnects and is re-acquired by the pool
watchdog, the rigctld server keeps working: tuning goes through the
iqtap broker which transparently retargets the fresh handle.

## Security

`rigctld` is **plaintext, unauthenticated** TCP. Treat it like
SMTP or unauthenticated Redis: bind only to interfaces you trust,
or front it with an SSH / WireGuard / Tailscale tunnel.

- **Loopback only (default-safe):** `rigctld: "127.0.0.1:4532"` —
  same-host clients only.
- **LAN exposure:** `rigctld: "0.0.0.0:4532"` — every device on
  your network can retune the SDR. Acceptable on a trusted home
  network, **not** on a shared / coworking / hostel LAN.
- **Tunnelled remote:** Bind to loopback on the daemon host and
  forward over SSH from your laptop:
  ```sh
  ssh -L 4532:localhost:4532 daemon-host
  rigctl -m 2 -r 127.0.0.1:4532
  ```

## Diagnostics

The daemon logs `rigctld: listening addr=... rig_serial=...` on
successful bind. A bind failure ("port already in use" — typically
a real Hamlib rigctld already running on 4532) is non-fatal: the
trunking pipeline continues, but the rigctld endpoint is
unavailable. Either stop the conflicting service or pick a
different port (`rigctld: "127.0.0.1:14532"` then point clients
at the new port with `rigctl -r 127.0.0.1:14532`).

## Implementation notes

- The rigctld backend is a `Controller` over the iqtap broker (the
  same primitive the live spectrum panel taps). SetFreq mutations
  funnel through the broker so the broker's `CenterHz()` cache
  stays current — this is what feeds the spectrum panel's
  frequency-axis labels after an external rigctld retune.
- Only the ~10 commands real clients send are implemented. Adding
  more is mechanical (`internal/api/rigctld/server.go`'s `dispatch`
  is a switch table). Rotctld (`rotctld` rotor control) is out
  of scope.
- The server is wired only when `api.rigctld` is set AND a
  control SDR exists in the pool — a daemon configured without a
  tuner doesn't pretend to be a controllable rig.
