package gmsk

import (
	"context"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

func TestNRZIDecoderInitialPlaceholder(t *testing.T) {
	d := NewNRZIDecoder()
	if got := d.Decode(0); got != 1 {
		t.Errorf("first Decode(0) = %d, want 1 (placeholder)", got)
	}
	if got := d.Decode(1); got != 0 {
		t.Errorf("transition 0→1 = %d, want 0", got)
	}
}

func TestNRZIDecoderTransitionVsHold(t *testing.T) {
	d := NewNRZIDecoder()
	_ = d.Decode(0) // seed
	cases := []struct {
		raw  byte
		want byte
		name string
	}{
		{raw: 0, want: 1, name: "0→0 hold"},
		{raw: 1, want: 0, name: "0→1 transition"},
		{raw: 1, want: 1, name: "1→1 hold"},
		{raw: 0, want: 0, name: "1→0 transition"},
	}
	for _, c := range cases {
		if got := d.Decode(c.raw); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestNRZIDecoderResetClears(t *testing.T) {
	d := NewNRZIDecoder()
	_ = d.Decode(1)
	_ = d.Decode(0)
	d.Reset()
	if got := d.Decode(0); got != 1 {
		t.Errorf("after Reset, first Decode = %d, want 1 (placeholder)", got)
	}
}

func TestReceiverNewRejectsBadOptions(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()

	if _, err := New(Options{}); err == nil {
		t.Error("New without Bus: want error")
	}
	if _, err := New(Options{Bus: bus}); err == nil {
		t.Error("New without InputRateHz: want error")
	}
}

func TestReceiverNewSetsUpInner(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	r, err := New(Options{InputRateHz: 768_000, Bus: bus})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r.Inner() == nil {
		t.Error("Inner() = nil, want non-nil orchestrator")
	}
}

func TestReceiverPropagatesContextCancel(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	r, err := New(Options{InputRateHz: 768_000, Bus: bus})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	in := make(chan []complex64)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- r.Process(ctx, in) }()
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Process err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Process did not exit after ctx cancel")
	}
}

func TestReceiverNilInputErrors(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	r, _ := New(Options{InputRateHz: 768_000, Bus: bus})
	if err := r.Process(context.Background(), nil); err == nil {
		t.Error("Process with nil input: want error")
	}
}

func TestReceiverProcessReturnsNilOnInputClose(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	r, err := New(Options{InputRateHz: 768_000, Bus: bus})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	in := make(chan []complex64)
	done := make(chan error, 1)
	go func() { done <- r.Process(context.Background(), in) }()
	close(in)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Process on closed input = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Process did not exit after input close")
	}
}

func TestReceiverStatsCountIQSamples(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	r, err := New(Options{InputRateHz: 768_000, Bus: bus})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := make(chan []complex64, 1)
	done := make(chan struct{})
	go func() {
		_ = r.Process(ctx, in)
		close(done)
	}()
	chunk := make([]complex64, 76_800) // 100 ms of IQ at 768 ksps
	in <- chunk
	close(in)
	<-done
	if got := r.Stats().IQSamplesSeen; got != uint64(len(chunk)) {
		t.Errorf("IQSamplesSeen = %d, want %d", got, len(chunk))
	}
}
