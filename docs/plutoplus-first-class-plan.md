---
layout: page
title: Pluto Plus First-Class Plan
description: Engineering plan to take Pluto Plus from endpoint mode to a fully first-class SDR backend
nav_group: Reference
---

# Pluto Plus First-Class Implementation Plan

This plan defines how Pluto Plus becomes a fully supported, first-class
SDR in GopherTrunk with parity against existing production SDR backends
(device lifecycle, controls, diagnostics, test coverage, and operator docs).

## Current baseline (May 2026)

- Driver name: `plutoplus`.
- Working transport modes:
  - `transport: tcp` with explicit `addr`.
  - `transport: usb` with default endpoint `192.168.2.1:1234`.
- Implicit USB auto-probe exists for `gophertrunk sdr list` when
  no explicit `sdr.pluto_plus` entries are configured.
- Current wire model is rtl_tcp-compatible endpoint mode
  (header + commands + u8 IQ stream).

This is functional, but not yet first-class parity.

## Definition of done (first-class)

Pluto Plus is first-class when all of the following are true:

1. Discovery and identity
- Reliable local USB discovery on Linux/macOS/Windows.
- Stable serial/model identity in API snapshots and `sdr list`.
- No false positives from endpoint probing.

2. Controls and tuning parity
- Center frequency, sample rate, gain mode/manual gain, ppm and
  bias handling are deterministic and validated.
- Errors are explicit and actionable (not generic socket errors).
- Unsupported knobs degrade predictably (well-documented warnings).

3. Runtime resilience
- Reacquire/reconnect behavior matches pool expectations.
- Hot-unplug/replug recovers without daemon restart.
- Stream stall detection and bounded reconnect backoff are implemented.

4. Observability and diagnostics
- `sdr doctor` can diagnose Pluto transport and handshake issues.
- Structured logs include transport mode, endpoint, failure stage,
  and retry path.
- Metrics expose stream health (disconnects, reconnects, read errors).

5. Test coverage and release confidence
- Unit tests for protocol, reconnection, and edge/error paths.
- Integration tests with fake endpoint server.
- Hardware validation checklist run on at least one real Pluto Plus
  target per major OS family before release tagging.

6. Operator experience
- Config templates and docs include USB and TCP examples.
- Troubleshooting guide exists for common USB networking failures
  (Windows driver binding/firewall, Linux udev/NetworkManager, macOS).

## Architecture target

Use a layered backend model in `internal/sdr/plutoplus`:

- `Spec` and config mapping layer:
  - Parses `transport`, endpoint, and role/hints.
  - Produces canonical identity and effective endpoint.
- Connection/protocol layer:
  - Handshake, command encoding/decoding, IQ framing.
  - Centralizes read/write deadlines and retry policy.
- Device runtime layer:
  - Implements `sdr.Device` lifecycle and stream loop.
  - Emits structured diagnostics and typed error categories.
- Probe/discovery layer:
  - Explicit configured endpoints.
  - Implicit USB probe path with strict timeout and confidence checks.

This keeps future protocol upgrades isolated from daemon/pool wiring.

## Delivery phases

## Phase 1: Transport hardening (short-term)

Goal: make endpoint mode reliable under normal field failures.

Scope:
- Classify connection failures by stage:
  - dial timeout, handshake mismatch, stream EOF, stream stall.
- Add bounded reconnect strategy for stream breaks.
- Add explicit per-mode timeout defaults:
  - USB stricter/faster probe timeout.
  - Runtime read timeout and reconnect backoff knobs.
- Improve serial derivation and endpoint normalization.

Acceptance:
- Simulated endpoint failures recover or fail loudly with reason.
- No goroutine leaks in repeated open/close/reconnect tests.

## Phase 2: Device semantics parity (short-term)

Goal: align Pluto control semantics with pool/operator expectations.

Scope:
- Validate manual gain mapping and AGC mode behavior.
- Ensure PPM and center frequency changes during streaming are safe.
- Define and enforce unsupported-operation behavior (documented).
- Add per-setting integration tests against fake server command log.

Acceptance:
- Command sequencing is deterministic and tested.
- Runtime setting changes do not deadlock or wedge stream.

## Phase 3: Discovery and doctor integration (mid-term)

Goal: first-class discoverability and troubleshooting UX.

Scope:
- Extend `sdr doctor` with Pluto checks:
  - endpoint reachability,
  - handshake validity,
  - transport-mode guidance,
  - common remediation hints.
- Add explicit output for implicit USB probe decision path.
- Optionally add a `--verbose` probe trace for Pluto.

Acceptance:
- Operator can distinguish config errors from device/network errors
  without reading source.

## Phase 4: Runtime resilience + metrics (mid-term)

Goal: production-grade operations and observability.

Scope:
- Reconnect counters and last-error tracking metrics.
- Pool/watchdog integration tests for unplug/replug-like scenarios.
- Expose Pluto transport health through runtime snapshot/API.

Acceptance:
- Long-running daemon soak test keeps Pluto healthy under fault injection.

## Phase 5: Hardware qualification + release gate (final)

Goal: promote Pluto Plus status from endpoint-functional to production.

Scope:
- Real hardware test matrix:
  - Linux, Windows, macOS host paths.
  - USB mode and explicit TCP mode.
  - control-channel lock stability and voice-follow sessions.
- Publish operator runbook and known limits.
- Add release checklist gate requiring Pluto matrix sign-off.

Acceptance:
- Roadmap item can be marked complete.
- Hardware section status upgraded to production wording.

## Test strategy

1. Unit tests
- Endpoint resolution and transport parsing.
- Handshake validation and failure categorization.
- Command encoding and command order guarantees.

2. Integration tests
- Fake Pluto endpoint server with fault injection:
  - delayed header,
  - bad magic,
  - partial reads,
  - disconnect during stream,
  - reconnect success path.

3. Hardware tests
- Real-device smoke and soak profiles.
- Capture + replay validation for decode quality regressions.

## Risks and mitigations

- Risk: USB networking behavior differs by OS driver stack.
  - Mitigation: explicit per-OS doctor checks + docs.
- Risk: false-positive auto-probe in noisy networks.
  - Mitigation: strict handshake validation and short probe timeout.
- Risk: reconnect logic flaps under unstable links.
  - Mitigation: bounded exponential backoff + jitter + clear metrics.

## Work breakdown checklist

- [ ] Add typed Pluto error categories and structured log fields.
- [ ] Add reconnect manager for stream loop.
- [ ] Add `sdr doctor` Pluto diagnostics.
- [ ] Add runtime metrics for Pluto health.
- [ ] Add API/runtime snapshot fields for Pluto transport status.
- [ ] Expand fake-server integration tests for fault injection.
- [ ] Run and record hardware matrix results.
- [ ] Update docs status from endpoint mode to production.

## Suggested milestone commits

1. `sdr/plutoplus: harden endpoint handshake and error taxonomy`
2. `sdr/plutoplus: add reconnect loop and health metrics`
3. `cli/sdr-doctor: add plutoplus diagnostics and remediation hints`
4. `docs/plutoplus: publish operator runbook and hardware matrix`
