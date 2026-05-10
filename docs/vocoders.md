# Vocoders

Digital trunked-radio voice traffic is carried by one of two
DVSI-derived vocoders:

- **IMBE** — used by P25 Phase 1 LDU1/LDU2 voice frames. Core US patents
  (filed early-to-mid-1990s, 20-year term) have **expired**. The
  algorithm is implementable in pure Go without licence concerns;
  GopherTrunk ships a pure-Go decoder at `internal/voice/imbe`.
- **AMBE+2** — used by P25 Phase 2, DMR (Tier II / III), and NXDN. AMBE+2
  is **patent-encumbered**. DVSI sells hardware vocoders (USB-3000 /
  AMBE-3003) and licences software ports. Open-source software
  implementations (e.g. `mbelib`) implement the algorithm; the *code*
  is permissively licensed (mbelib is ISC) but the *patents* are the
  user's risk to evaluate.

Re-implementing AMBE+2 in pure Go does not change the patent posture —
the algorithm itself is what the patents cover, regardless of
implementation language. Operators in licence-restrictive jurisdictions
should evaluate with counsel before deploying. GopherTrunk's default
build ships the pure-Go AMBE+2 decoder default-on; the legal
responsibility for operating it falls on the deployer, not the
project.

## How GopherTrunk handles this

The `internal/voice` package defines a `Vocoder` interface and a
process-global `Registry`. Each backend registers a factory at `init()`
time. The set of factories present in a binary is determined by the
import set:

```go
type Vocoder interface {
    Name() string
    FrameSize() int
    Decode(frame []byte) ([]int16, error)
    Reset()
    Close() error
}
```

| Backend                  | Build tag    | Default? | Status                                          |
| ------------------------ | ------------ | -------- | ----------------------------------------------- |
| `null` (silence)         | none         | yes      | Always available                                |
| `imbe` (pure-Go, P25 P1) | none         | yes      | Producing intelligible audio; calibration TODO  |
| `ambe2` (pure-Go)        | none         | yes      | Producing audio; calibration TODO; tones → silence |
| `dvsi` (USB-3000 chip)   | `-tags dvsi` | **no**   | Hardware backend, planned                       |

The recorder always emits a raw-frame sidecar (`.raw` next to the WAV)
when configured, so users can run their own decoder on the captured
frames without trusting the in-binary vocoders. This is the escape
hatch for operators who want bit-exact mbelib / DSD-FME / OP25 output
or who prefer to defer the decoding choice to post-processing.

## Implementation notes

- `internal/voice/mbe/` is the shared MBE-family synthesis core:
  cross-frame log-amplitude prediction, voiced harmonic generator,
  unvoiced FFT excitation + §6.4 overlap-add window, §6.2 spectral
  enhancement, and a per-frame fast-attack / slow-release AGC.
  Consumed by both `imbe` and `ambe2`.
- `internal/voice/imbe/` holds the IMBE-specific front half: 88-bit
  unpack, Golay/Hamming FEC inverse, scrambler, PRBA/HOC + inverse
  DCTs producing the spectral residuals the shared core consumes.
- `internal/voice/ambe2/` holds the AMBE+2-specific front half:
  49-bit unpack, codebook lookups (auto-generated from
  szechyjs/mbelib's `ambe3600x2400_const.h` under ISC,
  regenerable via `scripts/gen-ambe2-tables.sh`), inverse DCTs
  producing the spectral residuals, and the cross-frame `gamma`
  bookkeeping AMBE+2 requires.

Both decoders share the same constructor surface
(`New()` / `NewWithSeed(seed)` / `NewWithConfig(seed, mbe.AGCConfig)`)
so operators can pin reproducibility for tests or tune the AGC for
their downstream chain.

## Why a plugin model

This is exactly what SDR# / OP25 / DSD do. The key benefits:

1. The default binary has no external library dependencies for voice
   (no CGO, no system shared library, no install scripts).
2. Users with DVSI hardware can opt in by building with `-tags dvsi`
   once that backend lands.
3. Captures contain raw frames so a researcher can defer the decoding
   choice to post-processing.

## Future work

- Absolute-level calibration against a DSD-FME or OP25 reference
  recording for both `imbe` and `ambe2` decoders; AGC per-frame gain
  tweaks if real frames show systematic level offset.
- Proper AMBE+2 tone-frame synthesis (single + dual sinewave) replacing
  the current silence path — the tone-index extraction is already
  preserved on `ambe2.Params.B1` / `B2` for the follow-up.
- DVSI USB-3000 / AMBE-3003 hardware backend (`-tags dvsi`).
- Optional Opus / FLAC re-encoding of the recorded WAVs to shrink
  long-running archives.
- Plain AMBE decoder for D-STAR voice (different algorithm from AMBE+2;
  same DVSI patent family).
