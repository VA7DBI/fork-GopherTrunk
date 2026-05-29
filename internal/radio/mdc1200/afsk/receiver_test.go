package afsk

import (
	"context"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

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
	r, err := New(Options{InputRateHz: 96_000, Bus: bus})
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
	r, err := New(Options{InputRateHz: 96_000, Bus: bus})
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
	r, _ := New(Options{InputRateHz: 96_000, Bus: bus})
	if err := r.Process(context.Background(), nil); err == nil {
		t.Error("Process with nil input: want error")
	}
}

func TestReceiverProcessReturnsNilOnInputClose(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	r, err := New(Options{InputRateHz: 96_000, Bus: bus})
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
	r, err := New(Options{InputRateHz: 96_000, Bus: bus})
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
	chunk := make([]complex64, 9600)
	in <- chunk
	close(in)
	<-done
	if got := r.Stats().IQSamplesSeen; got != uint64(len(chunk)) {
		t.Errorf("IQSamplesSeen = %d, want %d", got, len(chunk))
	}
}
