---
layout: page
title: Hardware setup
description: RTL-SDR, HackRF and Airspy dongles, udev rules, DVB blacklist, and supported USB chipsets
nav_group: Install
---

# Hardware Setup

GopherTrunk ships with pure-Go drivers for three SDR families — no
`librtlsdr`, `libhackrf`, `libairspy`, `libusb`, or C toolchain on
the build host. All three drivers share the same pure-Go USB
transport (USBDEVFS on Linux, IOKit on macOS, WinUSB on Windows).

## Supported devices

| Family | Driver | USB IDs | Status |
| --- | --- | --- | --- |
| **RTL-SDR** (RTL2832U + R820T / R820T2 / R828D / E4000 / FC0012 / FC0013 / FC2580) | `rtlsdr` | `0x0bda:0x2838` | Production — on-air-validated across Linux / macOS / Windows. |
| **HackRF One** (also Jawbreaker / Rad1o) | `hackrf` | `0x1d50:0x6089` · `0x1d50:0x604b` · `0x1d50:0xcc15` | Wire-protocol-complete; on-air validation against attached hardware is the documented follow-up. |
| **Airspy R2 / Airspy Mini** | `airspy` | `0x1d50:0x60a1` | Wire-protocol-complete; on-air validation against attached hardware is the documented follow-up. |

The HackRF and Airspy drivers speak the documented libhackrf /
libairspy USB vendor protocols directly (transceiver / receiver
mode, frequency, sample rate, LNA / VGA / mixer / amp / bias-tee
gains, bulk-IN sample reaper with real-time decode of HackRF int8 IQ
and Airspy INT16_IQ into `complex64`). Their wire protocols are
exercised by unit tests against `usb.MockTransport`. SDRPlay, USRP
and BladeRF require vendor C libraries and are out of scope for the
zero-CGO build.

### RTL-SDR tested combinations

| Device | Tuner | Notes |
| --- | --- | --- |
| **NooElec NESDR Smart v5** | R820T2 | 0.5 ppm TCXO, software-controllable bias-tee. Use `bias_tee: true` in config to power an external LNA via the SMA. |
| NooElec NESDR Smart (v4 and earlier) | R820T2 | TCXO; no bias-tee on early units. |
| Generic RTL-SDR Blog v3 / v4 | R820T2 / R828D | Bias-tee on most units. |
| Plain RTL2832U DVB-T sticks | R820T | No TCXO; expect a few ppm offset — set `ppm:` in config after measuring. |

If you have a v5 (or any modern dongle with a bias-tee) and want to
power an LNA, the config snippet looks like:

```yaml
sdr:
  devices:
    - serial: "00000001"      # whatever `gophertrunk sdr list` shows
      role: control            # or voice / auto
      ppm: 0                   # 0 is fine for TCXO-equipped units
      gain: "auto"             # or a numeric tenths-of-dB string like "496"
      bias_tee: true           # 5V on the SMA — only enable if you want it
```

## Linux

No package install is needed for the build itself; the driver only
needs USB-device permissions at runtime.

Add a udev rule so non-root processes can claim each dongle. One
file per family is fine:

```
# /etc/udev/rules.d/20-rtlsdr.rules
SUBSYSTEM=="usb", ATTRS{idVendor}=="0bda", ATTRS{idProduct}=="2838", MODE="0666"

# /etc/udev/rules.d/21-hackrf.rules
SUBSYSTEM=="usb", ATTRS{idVendor}=="1d50", ATTRS{idProduct}=="6089", MODE="0666"
SUBSYSTEM=="usb", ATTRS{idVendor}=="1d50", ATTRS{idProduct}=="604b", MODE="0666"
SUBSYSTEM=="usb", ATTRS{idVendor}=="1d50", ATTRS{idProduct}=="cc15", MODE="0666"

# /etc/udev/rules.d/22-airspy.rules
SUBSYSTEM=="usb", ATTRS{idVendor}=="1d50", ATTRS{idProduct}=="60a1", MODE="0666"
```

Reload udev (`sudo udevadm control --reload && sudo udevadm trigger`) and
unplug/replug the dongle.

Blacklist the kernel's DVB driver — only matters for RTL-SDR, but
the file is harmless to ship even on HackRF / Airspy-only hosts:

```
# /etc/modprobe.d/blacklist-dvb_usb_rtl28xxu.conf
blacklist dvb_usb_rtl28xxu
```

See [`install-linux.md`]({{ '/install-linux.html' | relative_url }})
for the full step-by-step including systemd service setup.

## macOS

IOKit lets user-space claim USB devices without rebinding the kernel
driver, so there's no kext to install and no driver swap to perform.
Plug in the dongle and run `gophertrunk sdr list`. See
[`install-macos.md`]({{ '/install-macos.html' | relative_url }}) for
the full step-by-step including the Gatekeeper bypass and launchd
service setup.

## Windows

Bind each dongle to **WinUSB** with Zadig (bundled in the Windows
installer — Start Menu → GopherTrunk → "Install RTL-SDR driver
(Zadig)") once per device. The same Zadig walkthrough also works
for HackRF and Airspy — pick the device in the dropdown, choose
the WinUSB driver, click Replace. See [`install-windows.md`]({{ '/install-windows.html' | relative_url }})
for the click-by-click walkthrough.

## Verifying the build

```sh
make build
./bin/gophertrunk sdr list
```

You should see one row per attached dongle with index, serial,
tuner type (e.g. `R820T2` / `R828D` for RTL-SDR, `MAX2839+MAX5864`
for HackRF, `R820T` for Airspy), and the supported gain values. The
`Driver` column reads `rtlsdr`, `hackrf`, or `airspy` for each row.

## Capturing IQ for replay

GopherTrunk has two replay paths.

**Wideband baseband replay** — record the full IQ stream of any
attached tuner with a `baseband.record` config entry (see
[`opt-in-features.md`]({{ '/opt-in-features.html' | relative_url }})),
then mount the resulting two-channel 16-bit WAV (or one of
SDRtrunk's baseband recordings — same layout) as a virtual tuner via
`baseband.replay`. The replay loops on EOF, so a short capture
becomes a continuous source.

**Raw cfile mock driver** — the legacy mock driver replays raw u8-IQ
files (`.cfile` format) generated with `gqrx`, `csdr`, or any tool
that produces interleaved unsigned-8-bit samples. Drop `.cfile`
files under `testdata/iq/` to use them through the mock driver. The
baseband replay path above is the preferred option for new work;
the cfile mock stays around for the existing integration tests.
