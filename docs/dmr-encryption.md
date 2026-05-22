# DMR encryption (ARC4 / RC4 "Enhanced Privacy")

GopherTrunk can be configured with **known decryption keys** for DMR
systems that protect voice with ARC4/RC4-based "Enhanced Privacy".
This page covers how to configure keys, what the decode pipeline does
today, what is not yet implemented, and how to decode captured frames
out-of-band.

This is **known-key** support only: the operator supplies a key they
are authorized to hold. GopherTrunk performs **no key recovery** of
any kind — it is the same model used by SDRTrunk, DSD-FME and OP25.
Only monitor systems you are legally permitted to monitor.

## Configuration

Encryption keys are declared per trunking system, under
`trunking.systems[].encryption_keys`:

```yaml
trunking:
  systems:
    - name: "Example-DMR"
      protocol: dmr
      control_channels: [451_000_000]
      talkgroup_file: "/etc/gophertrunk/talkgroups-dmr.csv"
      encryption_keys:
        - key_id: 1            # matches the key ID in the privacy header
          algorithm: rc4       # only "rc4" / "arc4" is accepted today
          key: "0123456789"    # hex; whitespace and a "0x" prefix are ignored
```

Each entry has three fields:

| Field | Meaning |
| --- | --- |
| `key_id` | The key identifier the radios advertise in the privacy header. A system that rotates between several keys resolves to the right one by ID. |
| `algorithm` | The cipher. Only `rc4` (alias `arc4`) is accepted. `aes` / `des` are rejected at config-load with an explicit "not supported yet" error so the schema can grow later without a config break. |
| `key` | The raw key, hex-encoded. Surrounding whitespace, internal spaces and an optional `0x`/`0X` prefix are tolerated. 1–32 bytes. |

The config is validated when the daemon loads it: an unknown
algorithm, malformed hex, an over-length key, or a duplicate `key_id`
within one system is a hard error.

## What the pipeline does today

A DMR voice call flows through these stages — each has shipped and is
covered by unit + integration tests:

1. **Control-channel decode** — the DMR Tier II / Tier III decoders
   emit a `trunking.Grant`. The grant's `Encrypted` flag is read from
   the Full Link Control `ServiceOptions` bit, so an encrypted call is
   known as such before voice even starts.
2. **Voice superframe decode** (`internal/radio/dmr/voice`) — the
   composer runs an IQ → DMR receiver → superframe-decoder chain on
   the granted voice channel, locking onto the A–F voice superframe
   and extracting its eighteen 72-bit on-air AMBE+2 frames.
3. **AMBE+2 forward-error-correction** (`internal/radio/dmr/voice`,
   `ambefec.go`) — each 72-bit on-air frame is deinterleaved into its
   C0..C3 sub-vectors, C0/C1 are Golay(23,12)-corrected, C1 is
   descrambled with its C0-seeded pseudo-random sequence, and the
   49-bit vocoder payload is assembled.
4. **Capture** — the FEC-decoded 49-bit AMBE+2 frames are written to
   the call's `.raw` sidecar, next to the (currently silent) `.wav`,
   under the usual recordings layout.

A dependency-free RC4 keystream generator
(`internal/crypto/rc4`, verified against the canonical RC4 and
RFC 6229 test vectors) is in the tree, ready for the descramble step.

**Net result:** every DMR voice call — encrypted or not — is captured
as a `.raw` file of standard 49-bit AMBE+2 frames, and the call log
records whether it was flagged encrypted. When a call is encrypted,
the composer logs a clear line so the operator understands why its
audio is unintelligible.

## What is not yet implemented

Two stages remain before an encrypted DMR call decodes to playable
audio **inside GopherTrunk**:

- **In-process RC4 descramble.** This needs the PI-header parse
  (algorithm ID, key ID and the per-superframe Message Indicator) plus
  the exact rule for building the RC4 key from the configured key and
  the Message Indicator, and the per-frame keystream application. The
  RC4 *cipher* is already implemented; the DMR-specific *application*
  of it is the missing piece. It is intentionally not guessed: there
  is no permissively-licensed reference implementation to port from
  (the existing ones are GPL), and the project has no encrypted-DMR
  capture to validate an implementation against. Contributors who can
  supply a capture + known key should open an issue.
- **In-process AMBE+2 vocoder → WAV.** The `.raw` frames are standard
  49-bit AMBE+2 payloads but DMR uses the AMBE **3600x2450** parameter
  layout, whereas the bundled `internal/voice/ambe2` decoder was
  ported from the **3600x2400** variant. Rendering DMR voice to PCM
  needs a 2450 parameter-decode path; until then the `.wav` for a DMR
  voice call is silent and the `.raw` sidecar is the deliverable.

## Decoding the `.raw` sidecar out-of-band

The `.raw` file is a flat concatenation of 7-byte frames, each holding
one FEC-decoded 49-bit AMBE+2 voice frame (MSB-first, 49 bits + 7 bits
of zero padding). This is a standard AMBE+2 frame format and can be
fed to an external AMBE decoder (an mbelib-based tool, DSD-FME, or
DVSI hardware) to produce audio.

For an encrypted call with no in-process descramble, the `.raw` holds
the *encrypted* AMBE+2 frames; an external decoder with the key — or
GopherTrunk once the descramble lands — is needed for intelligible
audio.

## References

The AMBE+2 FEC implementation is ported, with bit layouts preserved
1:1, from two ISC-licensed projects (attribution in
`THIRD_PARTY_LICENSES.md`):

- **mbelib** (`ambe3600x2450.c`, `ecc.c`) — the C0/C1 Golay(23,12)
  error correction, the C1 descramble pseudo-random sequence, and the
  C0..C3 → 49-bit payload assembly.
- **szechyjs/dsd** (`dmr_const.h`, `dmr_voice.c`) — the 72-bit on-air
  → C0..C3 deinterleave schedule.

See also [`docs/vocoders.md`](vocoders.md) for the IMBE / AMBE+2
licensing landscape and the vocoder plugin model.
