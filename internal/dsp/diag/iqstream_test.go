package diag

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestNewRejectsBadOptions(t *testing.T) {
	if _, err := New(Options{InputRateSPS: 0}); err == nil {
		t.Error("zero InputRateSPS: want error")
	}
	if _, err := New(Options{InputRateSPS: 1000, TargetRateSPS: 5000}); err == nil {
		t.Error("TargetRateSPS > InputRateSPS: want error")
	}
}

func TestNewDefaultsTargetRate(t *testing.T) {
	d, err := New(Options{InputRateSPS: 2_048_000})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.TargetRateSPS() != DefaultDecimatedRateSPS {
		t.Errorf("TargetRateSPS = %d, want %d", d.TargetRateSPS(), DefaultDecimatedRateSPS)
	}
}

func TestDecimatorStrideMath(t *testing.T) {
	d, _ := New(Options{InputRateSPS: 2_048_000, TargetRateSPS: 2048})
	if d.Stride() != 1000 {
		t.Errorf("stride = %d, want 1000 (2.048M / 2048)", d.Stride())
	}
}

func TestRunEmitsFrameAfterChunksPerFrame(t *testing.T) {
	d, _ := New(Options{
		InputRateSPS:   1000,
		TargetRateSPS:  100,
		ChunksPerFrame: 2,
	})

	in := make(chan []complex64, 4)
	out := make(chan IQFrame, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var ts int64 = 1234567
	go func() {
		_ = d.Run(ctx, in, out, func() uint32 { return 850_000_000 }, func() int64 { return ts })
	}()

	// Stride is 10, so each 50-sample chunk yields 5 output points.
	chunk := make([]complex64, 50)
	for i := range chunk {
		chunk[i] = complex(0.5, 0.25)
	}
	in <- chunk
	// First chunk alone shouldn't emit (ChunksPerFrame=2).
	select {
	case <-out:
		t.Fatal("frame emitted after only 1 chunk; ChunksPerFrame=2")
	case <-time.After(50 * time.Millisecond):
	}
	in <- chunk
	select {
	case f := <-out:
		if len(f.Points) != 10 {
			t.Errorf("Points len = %d, want 10 (2 chunks * 5 strides)", len(f.Points))
		}
		if f.SampleRate != 100 {
			t.Errorf("SampleRate = %d, want 100", f.SampleRate)
		}
		if f.CenterHz != 850_000_000 {
			t.Errorf("CenterHz = %d, want 850_000_000", f.CenterHz)
		}
		if f.Points[0].I != 0.5 || f.Points[0].Q != 0.25 {
			t.Errorf("first point = (%f, %f), want (0.5, 0.25)", f.Points[0].I, f.Points[0].Q)
		}
		// 0.5^2 + 0.25^2 = 0.3125 → 10*log10(0.3125) ≈ -5.05 dBFS.
		if f.EnergyDBFS < -7 || f.EnergyDBFS > -3 {
			t.Errorf("EnergyDBFS = %f, want roughly -5 dBFS", f.EnergyDBFS)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no frame emitted within 500 ms")
	}
}

func TestRunPropagatesEnergyForDC(t *testing.T) {
	d, _ := New(Options{
		InputRateSPS:   1000,
		TargetRateSPS:  100,
		ChunksPerFrame: 1,
	})

	in := make(chan []complex64, 1)
	out := make(chan IQFrame, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = d.Run(ctx, in, out, func() uint32 { return 0 }, func() int64 { return 0 })
	}()

	// Unit-amplitude DC: 1+0i. |x|^2 = 1, so energy = 0 dBFS.
	chunk := make([]complex64, 100)
	for i := range chunk {
		chunk[i] = complex(1, 0)
	}
	in <- chunk

	select {
	case f := <-out:
		if math.Abs(float64(f.EnergyDBFS)) > 0.5 {
			t.Errorf("EnergyDBFS = %f, want ~0 dBFS", f.EnergyDBFS)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no frame")
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	d, _ := New(Options{InputRateSPS: 1000, TargetRateSPS: 100})

	in := make(chan []complex64)
	out := make(chan IQFrame, 1)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx, in, out, func() uint32 { return 0 }, func() int64 { return 0 })
	}()
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not exit after ctx cancel")
	}
}

func TestRunStopsOnInputClose(t *testing.T) {
	d, _ := New(Options{InputRateSPS: 1000, TargetRateSPS: 100})

	in := make(chan []complex64, 1)
	out := make(chan IQFrame, 1)

	done := make(chan error, 1)
	go func() {
		done <- d.Run(context.Background(), in, out, func() uint32 { return 0 }, func() int64 { return 0 })
	}()
	close(in)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run err = %v, want nil on input close", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not exit on input close")
	}
}

func TestSlowConsumerCountsDrops(t *testing.T) {
	d, _ := New(Options{
		InputRateSPS:   1000,
		TargetRateSPS:  100,
		ChunksPerFrame: 1,
	})

	in := make(chan []complex64, 16)
	// Tiny out channel that we deliberately don't drain.
	out := make(chan IQFrame, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = d.Run(ctx, in, out, func() uint32 { return 0 }, func() int64 { return 0 })
	}()

	// Pump 5 chunks without draining out. The first goes through, the
	// next four should drop.
	chunk := make([]complex64, 10)
	for i := 0; i < 5; i++ {
		in <- chunk
	}
	// Give the run loop a moment to process.
	time.Sleep(50 * time.Millisecond)
	if d.Dropped() == 0 {
		t.Error("Dropped count = 0 after deliberate backpressure; want > 0")
	}
}

func TestRunNilChannelsError(t *testing.T) {
	d, _ := New(Options{InputRateSPS: 1000})
	if err := d.Run(context.Background(), nil, make(chan IQFrame), nil, nil); err == nil {
		t.Error("Run with nil in: want error")
	}
	if err := d.Run(context.Background(), make(chan []complex64), nil, nil, nil); err == nil {
		t.Error("Run with nil out: want error")
	}
}
