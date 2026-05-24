package main

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/scanner/ccdecoder"
	"github.com/MattCheramie/GopherTrunk/internal/trunking"
)

// flakyIQSource is an IQSource that closes its channel n times
// (simulating n consecutive USB reaper deaths) and then returns a
// healthy never-closing channel — models the daemon's
// reopen-and-restart loop hitting a transient device fault.
type flakyIQSource struct {
	deaths   int32
	maxDeath int32
}

func (f *flakyIQSource) StreamIQ(ctx context.Context) (<-chan []complex64, error) {
	ch := make(chan []complex64)
	go func() {
		defer close(ch)
		select {
		case ch <- make([]complex64, 4):
		case <-ctx.Done():
			return
		}
		if atomic.AddInt32(&f.deaths, 1) <= f.maxDeath {
			// Simulate reaper death: close the channel mid-stream
			// without waiting on ctx.
			return
		}
		// Healthy stream — block on ctx.
		<-ctx.Done()
	}()
	return ch, nil
}

// TestRunCCDecoderWithRetry_RecoversAfterTransientDeath pins the
// issue-#345 restart loop: an IQ-stream death triggers a rebuild with
// backoff, and a subsequent healthy run keeps the loop alive (no
// fatal). Then ctx-cancel returns cleanly.
func TestRunCCDecoderWithRetry_RecoversAfterTransientDeath(t *testing.T) {
	// Shrink the backoff schedule so the test doesn't sit for 18s.
	oldBackoffs := ccDecoderRetryBackoffs
	ccDecoderRetryBackoffs = []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		50 * time.Millisecond,
	}
	defer func() { ccDecoderRetryBackoffs = oldBackoffs }()

	bus := events.NewBus(8)
	defer bus.Close()
	src := &flakyIQSource{maxDeath: 2}
	opts := ccdecoder.Options{
		Bus: bus, IQ: src, SampleRateHz: 48000,
		Log: slog.New(slog.DiscardHandler),
	}
	dec, err := ccdecoder.New(opts)
	if err != nil {
		t.Fatalf("ccdecoder.New: %v", err)
	}
	d := &Daemon{
		log:           slog.New(slog.DiscardHandler),
		bus:           bus,
		systems:       []trunking.System{},
		ccDecoder:     dec,
		ccDecoderOpts: opts,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.runCCDecoderWithRetry(ctx) }()

	// Give the loop time to weather both deaths, rebuild, and settle
	// into the healthy run.
	select {
	case err := <-done:
		t.Fatalf("loop exited early: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	// Confirm no fatal was recorded.
	if got := d.takeFatal(); got != nil {
		t.Errorf("recordFatal fired during transient deaths: %v", got)
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("loop exit err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("loop did not exit after ctx cancel")
	}
}

// TestRunCCDecoderWithRetry_FatalsAfterRetriesExhausted pins the
// terminal escalation: if the IQ stream dies more times than the
// backoff schedule covers (and never gets a healthy run in between),
// the loop records a fatal so the daemon exits non-zero.
func TestRunCCDecoderWithRetry_FatalsAfterRetriesExhausted(t *testing.T) {
	oldBackoffs := ccDecoderRetryBackoffs
	ccDecoderRetryBackoffs = []time.Duration{
		5 * time.Millisecond,
		5 * time.Millisecond,
	}
	defer func() { ccDecoderRetryBackoffs = oldBackoffs }()

	bus := events.NewBus(8)
	defer bus.Close()
	// maxDeath=999 means every reopen dies — no healthy window ever
	// hits, retries get exhausted.
	src := &flakyIQSource{maxDeath: 999}
	opts := ccdecoder.Options{
		Bus: bus, IQ: src, SampleRateHz: 48000,
		Log: slog.New(slog.DiscardHandler),
	}
	dec, err := ccdecoder.New(opts)
	if err != nil {
		t.Fatalf("ccdecoder.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &Daemon{
		log:           slog.New(slog.DiscardHandler),
		bus:           bus,
		systems:       []trunking.System{},
		ccDecoder:     dec,
		ccDecoderOpts: opts,
		cancel:        cancel,
	}

	got := d.runCCDecoderWithRetry(ctx)
	if !errors.Is(got, ccdecoder.ErrIQStreamClosed) {
		t.Errorf("Run = %v, want wrapped ErrIQStreamClosed", got)
	}
	if d.takeFatal() == nil {
		t.Error("expected recordFatal to fire after retries exhausted")
	}
}

// usbDisconnectIQSource models the exact failure pattern reported in
// issue #345 after the v0.2.1 retry shipped: the first StreamIQ call
// hands out a healthy channel that the caller-side reaper closes
// (mid-stream EOF), and every subsequent StreamIQ call returns the
// underlying USB-disconnect error directly — i.e. the rebuilt decoder
// hits a dead Tuner handle that rejects the ResetBuffer control
// transfer. Without the issue-#345 follow-up fix this open-time error
// is NOT classified as ErrIQStreamClosed, so the daemon's retry loop
// returns it as-is and (with essential=false) silently swallows it.
type usbDisconnectIQSource struct {
	opens atomic.Int32
}

func (u *usbDisconnectIQSource) StreamIQ(ctx context.Context) (<-chan []complex64, error) {
	n := u.opens.Add(1)
	if n == 1 {
		ch := make(chan []complex64)
		go func() {
			defer close(ch)
			select {
			case ch <- make([]complex64, 4):
			case <-ctx.Done():
				return
			}
			// Reaper death: close mid-stream without ctx-cancel.
		}()
		return ch, nil
	}
	// Every retry sees the device gone.
	return nil, errors.New("rtl2832u: write block=1 addr=0x2148 val=0x1002: usb: device disconnected")
}

// TestRunCCDecoderWithRetry_USBDisconnectEscalatesToFatal pins the
// issue-#345 follow-up: after the initial reaper-death triggers a
// rebuild and every subsequent StreamIQ open fails with
// `usb: device disconnected`, the retry loop must classify those
// open failures as ErrIQStreamClosed (so it stays in the backoff
// schedule rather than bailing out early) and, on exhaustion, fire
// recordFatal so the supervisor restarts the process. Pre-fix this
// test fails because runCCDecoderWithRetry returns the unwrapped
// open error after one attempt and never records a fatal.
func TestRunCCDecoderWithRetry_USBDisconnectEscalatesToFatal(t *testing.T) {
	oldBackoffs := ccDecoderRetryBackoffs
	ccDecoderRetryBackoffs = []time.Duration{
		5 * time.Millisecond,
		5 * time.Millisecond,
	}
	defer func() { ccDecoderRetryBackoffs = oldBackoffs }()

	bus := events.NewBus(8)
	defer bus.Close()
	src := &usbDisconnectIQSource{}
	opts := ccdecoder.Options{
		Bus: bus, IQ: src, SampleRateHz: 48000,
		Log: slog.New(slog.DiscardHandler),
	}
	dec, err := ccdecoder.New(opts)
	if err != nil {
		t.Fatalf("ccdecoder.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &Daemon{
		log:           slog.New(slog.DiscardHandler),
		bus:           bus,
		systems:       []trunking.System{},
		ccDecoder:     dec,
		ccDecoderOpts: opts,
		cancel:        cancel,
	}

	got := d.runCCDecoderWithRetry(ctx)
	if !errors.Is(got, ccdecoder.ErrIQStreamClosed) {
		t.Errorf("Run = %v, want wrapped ErrIQStreamClosed", got)
	}
	if d.takeFatal() == nil {
		t.Error("expected recordFatal to fire after USB-disconnect retries exhausted")
	}
	// Sanity: we should have seen at least one mid-stream death and
	// one open-time disconnect, proving the new classification covers
	// both shapes.
	if got, want := src.opens.Load(), int32(2); got < want {
		t.Errorf("StreamIQ opens = %d, want >= %d", got, want)
	}
}
