package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	p25phase1 "github.com/MattCheramie/GopherTrunk/internal/radio/p25/phase1"
	p25phase1rx "github.com/MattCheramie/GopherTrunk/internal/radio/p25/phase1/receiver"
	"github.com/MattCheramie/GopherTrunk/internal/trunking"
)

// runReplay is the entry point for `gophertrunk replay`. It runs an
// offline raw IQ capture file through the real P25 Phase 1 receiver +
// control-channel chain and prints lock / grant / decode-error events
// plus the per-frame NID-decoder diagnostics — including the
// at_boundary flag the bounded-search fix emits (issue #275). The
// effective-baud summary at EOF self-diagnoses a mislabeled capture
// (the symptom that made anakie_short.zip unusable as ground truth):
// if numDibits / (numSamples / sampleRate) ≠ ~4800, the file's true
// sample rate is not what the operator passed via -sample-rate.
//
// Usage:
//
//	gophertrunk replay -in <path> [-format u8|f32] [-sample-rate Hz]
//	                  [-demod c4fm|cqpsk] [-freq Hz]
//
// The decoder runs at debug log level so `nid corrected`, `nid parse
// failed`, and the new `at_boundary` field all surface in the output.
// Reuses the production receiver + control-channel constructors — what
// it decodes is what the daemon would decode, so a replay-lock
// implies an on-air lock and a replay-fail makes the offline capture a
// reproducible test fixture.
func runReplay(args []string) {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	in := fs.String("in", "", "raw IQ input file (required)")
	format := fs.String("format", "u8", "sample format: u8 (rtl_sdr 8-bit unsigned interleaved IQ) | f32 (GNU Radio cfile, interleaved float32)")
	sampleRate := fs.Float64("sample-rate", 2_400_000, "IQ sample rate in Hz")
	demod := fs.String("demod", "c4fm", "P25 Phase 1 demod mode: c4fm | cqpsk")
	freq := fs.Uint64("freq", 0, "informational only: the capture's nominal centre frequency in Hz")
	// Issue #275 bisect knob. The default ±6 grid is the production
	// value; widening to ±12/±18/±36 on a stubborn capture tells a
	// span-bounded failure (errs drop at the new optimum) from a
	// demod-quality-bounded one (errs stay at the BCH(63,16,11)
	// correction ceiling regardless of alignment). Both acceptance
	// tiers (BCH+parity + TSBK CRC) reject wrong alignments at any
	// span, so widening cannot manufacture a false lock.
	nidSearchSpan := fs.Int("nid-search-span", p25phase1.NIDSearchSpan, "NID-alignment search radius in dibits (default matches the production ccdecoder; widen to bisect a stubborn capture per issue #275)")
	// Issue #275 Phase B knob — after Phase A's widening ruled out
	// alignment, this surfaces what the NID-failure diag cannot: the
	// dibit-value histogram and the per-rotation FSW-correlation
	// landscape across the whole capture. Off by default since it
	// allocates an O(numDibits) buffer.
	diag := fs.Bool("diag", false, "print a demod-quality diagnostic report (dibit histogram + per-rotation FSW correlation landscape) at EOF")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `gophertrunk replay — decode a raw IQ capture file offline.

USAGE:
  gophertrunk replay -in <path> [-format u8|f32] [-sample-rate Hz] [-demod c4fm|cqpsk]

EXAMPLES:
  # rtl_sdr capture of a P25 control channel
  gophertrunk replay -in mt_anakie.bin -sample-rate 2048000 -demod c4fm

  # GNU Radio float32 cfile of an LSM simulcast site
  gophertrunk replay -in cbd.cfile -format f32 -sample-rate 960000 -demod cqpsk

FLAGS:`)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	if *in == "" {
		fmt.Fprintln(os.Stderr, "replay: -in is required")
		fs.Usage()
		os.Exit(2)
	}
	if *sampleRate <= 0 {
		fmt.Fprintln(os.Stderr, "replay: -sample-rate must be > 0")
		os.Exit(2)
	}
	demodMode, ok := p25phase1rx.ParseDemodMode(*demod)
	if !ok {
		fmt.Fprintf(os.Stderr, "replay: unknown -demod %q (want c4fm or cqpsk)\n", *demod)
		os.Exit(2)
	}
	if *nidSearchSpan <= 0 {
		fmt.Fprintln(os.Stderr, "replay: -nid-search-span must be > 0")
		os.Exit(2)
	}

	decode, bytesPerSample, err := pickSampleDecoder(*format)
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		os.Exit(2)
	}

	f, err := os.Open(*in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "replay: open %s: %v\n", *in, err)
		os.Exit(1)
	}
	defer f.Close()

	// Logger at debug so `nid corrected` (with at_boundary), `nid
	// parse failed` (with diag), and the FSW miss throttle all
	// surface — the diagnostic value the operator is here for.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	bus := events.NewBus(1024)
	defer bus.Close()
	sub := bus.Subscribe()
	defer sub.Close()

	// Mirror ccdecoder/pipelines.go: restrict the rotation set on the
	// C4FM path so the search cannot converge on a non-physical
	// rot 1 / rot 3 miscorrection (issue #275 post-#321).
	rotations := p25phase1.RotationsAll
	if demodMode == p25phase1rx.DemodC4FM {
		rotations = p25phase1.RotationsC4FM
	}
	cc := p25phase1.New(p25phase1.Options{
		Bus:           bus,
		Log:           logger,
		SystemName:    "replay",
		FrequencyHz:   uint32(*freq),
		Rotations:     rotations,
		NIDSearchSpan: *nidSearchSpan,
	})
	// Surface the active configuration the same way the ccdecoder
	// pipeline does, so the replay log line is directly comparable
	// to a daemon's startup line — and a non-default span (the
	// bisect knob) is visible without re-reading the command.
	fmt.Fprintf(os.Stderr, "replay: p25/phase1 configured  demod=%s  rotations=%v  nid_search_span=%d  nid_accept_errs=%d  nid_marginal_max=%d\n",
		*demod, rotations, *nidSearchSpan, p25phase1.NIDAcceptErrs, p25phase1.NIDMarginalMaxErrs)

	var dibitCount int64
	var diagAcc *iqDiag
	if *diag {
		diagAcc = &iqDiag{}
	}
	rxOpts := p25phase1rx.Options{
		SampleRateHz: *sampleRate,
		DeviationHz:  1800.0,
		DemodMode:    demodMode,
		DibitSink: func(dibits []uint8, baseIdx int) {
			dibitCount += int64(len(dibits))
			if diagAcc != nil {
				diagAcc.observe(dibits)
			}
			cc.Process(dibits, baseIdx)
		},
	}
	if diagAcc != nil {
		rxOpts.SoftSink = diagAcc.observeSoft
	}
	rx := p25phase1rx.New(rxOpts)

	// Drain bus events to stdout in the background so they print
	// interleaved with the decoder log going to stderr.
	doneEvents := make(chan struct{})
	var stats replayStats
	go func() {
		defer close(doneEvents)
		for ev := range sub.C {
			handleEvent(ev, &stats)
		}
	}()

	const chunkSamples = 8192
	buf := make([]byte, chunkSamples*bytesPerSample)
	samples := make([]complex64, chunkSamples)
	var totalSamples int64
	for {
		n, rerr := io.ReadFull(f, buf)
		if n > 0 {
			pairBytes := bytesPerSample
			pairs := n / pairBytes
			if pairs*pairBytes != n {
				fmt.Fprintf(os.Stderr, "replay: trailing %d bytes ignored (not a whole IQ pair at %d bytes/sample)\n", n-pairs*pairBytes, pairBytes)
			}
			if pairs > len(samples) {
				samples = make([]complex64, pairs)
			}
			decode(buf[:pairs*pairBytes], samples[:pairs])
			rx.Process(samples[:pairs])
			totalSamples += int64(pairs)
		}
		if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "replay: read %s: %v\n", *in, rerr)
			os.Exit(1)
		}
	}

	// Close the bus to release the event-drainer goroutine, then
	// wait for it to finish so the final stats are accurate.
	bus.Close()
	<-doneEvents

	printSummary(filepath.Base(*in), totalSamples, *sampleRate, dibitCount, stats)
	if diagAcc != nil {
		diagAcc.printReport(os.Stdout)
	}
}

// replayStats accumulates bus events seen during the replay so the
// EOF summary can render lock / grant / decode-error totals.
type replayStats struct {
	locked         bool
	nac            uint16
	grants         int
	grantFreqs     map[uint32]int
	decodeErrors   map[events.Stage]int
	otherEventKind map[events.Kind]int
}

func handleEvent(ev events.Event, s *replayStats) {
	switch ev.Kind {
	case events.KindCCLocked:
		if ls, ok := ev.Payload.(p25phase1.LockState); ok {
			s.locked = true
			s.nac = ls.NAC
			fmt.Printf("replay: cc.locked  nac=%#x  freq=%d  duid=%s\n", ls.NAC, ls.FrequencyHz, ls.DUID)
		}
	case events.KindGrant:
		if g, ok := ev.Payload.(trunking.Grant); ok {
			s.grants++
			if s.grantFreqs == nil {
				s.grantFreqs = make(map[uint32]int)
			}
			s.grantFreqs[g.FrequencyHz]++
			fmt.Printf("replay: grant      tg=%d  src=%d  ch=%d/%d  freq=%d  enc=%v  emer=%v\n",
				g.GroupID, g.SourceID, g.ChannelID, g.ChannelNum, g.FrequencyHz, g.Encrypted, g.Emergency)
		}
	case events.KindDecodeError:
		if de, ok := ev.Payload.(events.DecodeError); ok {
			if s.decodeErrors == nil {
				s.decodeErrors = make(map[events.Stage]int)
			}
			s.decodeErrors[de.Stage]++
		}
	default:
		if s.otherEventKind == nil {
			s.otherEventKind = make(map[events.Kind]int)
		}
		s.otherEventKind[ev.Kind]++
	}
}

// printSummary writes the EOF report — what the operator pastes into
// the GitHub issue alongside the live log. Includes the effective-baud
// self-diagnostic the anakie_short.zip mislabel taught us we need.
func printSummary(name string, samples int64, sampleRate float64, dibits int64, s replayStats) {
	fmt.Fprintln(os.Stdout, "----")
	duration := float64(samples) / sampleRate
	fmt.Fprintf(os.Stdout, "replay: %s — %d samples (%.2fs at %.0f Hz), %d dibits emitted\n",
		name, samples, duration, sampleRate, dibits)

	const expectedBaud = 4800.0
	if duration > 0 && dibits > 0 {
		effective := float64(dibits) / duration
		dev := (effective - expectedBaud) / expectedBaud * 100
		warning := ""
		if math.Abs(dev) > 2 {
			warning = "  (>2% — capture sample rate may not match -sample-rate)"
		}
		fmt.Fprintf(os.Stdout, "replay: effective baud %.1f (expected %.0f, deviation %+.1f%%)%s\n",
			effective, expectedBaud, dev, warning)
	}

	if s.locked {
		fmt.Fprintf(os.Stdout, "replay: locked  nac=%#x\n", s.nac)
	} else {
		fmt.Fprintln(os.Stdout, "replay: did NOT lock the control channel")
	}
	if s.grants > 0 {
		fmt.Fprintf(os.Stdout, "replay: %d grant(s) across %d frequencies\n", s.grants, len(s.grantFreqs))
	}
	if len(s.decodeErrors) > 0 {
		parts := make([]string, 0, len(s.decodeErrors))
		for stage, n := range s.decodeErrors {
			parts = append(parts, fmt.Sprintf("%s=%d", stage, n))
		}
		fmt.Fprintf(os.Stdout, "replay: decode errors: %s\n", strings.Join(parts, " "))
	}
}

// pickSampleDecoder maps the -format flag to a (decoder, bytes-per-IQ-pair)
// pair the read loop drives. u8 is the rtl_sdr default; f32 is GNU Radio's
// interleaved float32 cfile.
func pickSampleDecoder(format string) (func([]byte, []complex64), int, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "u8", "":
		return decodeU8Replay, 2, nil
	case "f32", "float32", "cfile":
		return decodeF32Replay, 8, nil
	default:
		return nil, 0, fmt.Errorf("unknown -format %q (want u8 or f32)", format)
	}
}

// decodeU8Replay converts rtl_sdr 8-bit unsigned interleaved IQ to
// complex64 in [-1, +1]. Mirrors internal/sdr/mock.go's decodeU8 but
// kept package-local so replay has no dependency on the mock SDR
// driver layer.
func decodeU8Replay(buf []byte, out []complex64) {
	n := len(buf) / 2
	for i := 0; i < n; i++ {
		ir := float32(buf[2*i]) - 127.5
		qr := float32(buf[2*i+1]) - 127.5
		out[i] = complex(ir/127.5, qr/127.5)
	}
}

// decodeF32Replay converts an interleaved-float32 GNU Radio cfile to
// complex64. The file is read as little-endian, matching the format
// gnuradio-companion emits on every platform GopherTrunk supports.
func decodeF32Replay(buf []byte, out []complex64) {
	n := len(buf) / 8
	for i := 0; i < n; i++ {
		ir := math.Float32frombits(binary.LittleEndian.Uint32(buf[8*i:]))
		qr := math.Float32frombits(binary.LittleEndian.Uint32(buf[8*i+4:]))
		out[i] = complex(ir, qr)
	}
}
