package widebandt2

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/sdr"
)

// mockDevice is a synchronous sdr.Device that emits a caller-supplied
// sequence of IQ chunks, then closes the stream. The test goroutine
// blocks on producing each chunk so the engine's loop is driven
// deterministically.
type mockDevice struct {
	chunks       [][]complex64
	chunkCh      chan []complex64
	streamErr    error
	centerFreqHz atomic.Uint32
	sampleRateHz atomic.Uint32
	startOnce    sync.Once
}

func newMockDevice(chunks [][]complex64) *mockDevice {
	return &mockDevice{chunks: chunks, chunkCh: make(chan []complex64, len(chunks)+1)}
}

func (m *mockDevice) Info() sdr.Info                { return sdr.Info{Driver: "mock", Serial: "MOCK1"} }
func (m *mockDevice) SetCenterFreq(hz uint32) error { m.centerFreqHz.Store(hz); return nil }
func (m *mockDevice) SetSampleRate(hz uint32) error { m.sampleRateHz.Store(hz); return nil }
func (m *mockDevice) SetGain(int) error             { return nil }
func (m *mockDevice) SetPPM(int) error              { return nil }
func (m *mockDevice) SetBiasTee(bool) error         { return nil }
func (m *mockDevice) Close() error                  { return nil }

func (m *mockDevice) StreamIQ(ctx context.Context) (<-chan []complex64, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	m.startOnce.Do(func() {
		go func() {
			defer close(m.chunkCh)
			for _, c := range m.chunks {
				select {
				case <-ctx.Done():
					return
				case m.chunkCh <- c:
				}
			}
		}()
	})
	return m.chunkCh, nil
}

func TestEngineNewRejectsMissingDevice(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	if _, err := New(Options{Bus: bus, SampleRateHz: 2_400_000, CenterFreqHz: 453_500_000,
		Channels: []ChannelConfig{{FrequencyHz: 453_125_000, SystemName: "x"}}}); err == nil {
		t.Errorf("expected error for missing Device")
	}
}

func TestEngineNewRejectsMissingBus(t *testing.T) {
	dev := newMockDevice(nil)
	if _, err := New(Options{Device: dev, SampleRateHz: 2_400_000, CenterFreqHz: 453_500_000,
		Channels: []ChannelConfig{{FrequencyHz: 453_125_000, SystemName: "x"}}}); err == nil {
		t.Errorf("expected error for missing Bus")
	}
}

func TestEngineNewRejectsEmptyChannels(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	dev := newMockDevice(nil)
	if _, err := New(Options{Device: dev, Bus: bus, SampleRateHz: 2_400_000, CenterFreqHz: 453_500_000}); err == nil {
		t.Errorf("expected error for empty channels")
	}
}

func TestEngineNewRejectsOutOfBandChannel(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	dev := newMockDevice(nil)
	_, err := New(Options{
		Device: dev, Bus: bus, SampleRateHz: 2_400_000, CenterFreqHz: 453_500_000,
		Channels: []ChannelConfig{
			{FrequencyHz: 470_000_000, SystemName: "x"}, // > 16 MHz away
		},
	})
	if err == nil {
		t.Errorf("expected error for out-of-band channel")
	}
}

func TestEngineStrategyAuto(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()

	t.Run("small fleet picks ddc", func(t *testing.T) {
		dev := newMockDevice(nil)
		e, err := New(Options{
			Device: dev, Bus: bus, SampleRateHz: 2_400_000, CenterFreqHz: 453_500_000,
			Channels: []ChannelConfig{
				{FrequencyHz: 453_125_000, SystemName: "x"},
				{FrequencyHz: 453_775_000, SystemName: "x"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if e.Strategy() != "auto(ddc)" {
			t.Errorf("strategy = %q, want auto(ddc)", e.Strategy())
		}
	})

	t.Run("large fleet picks polyphase", func(t *testing.T) {
		dev := newMockDevice(nil)
		// 7 channels exceeds strategyAutoThreshold, so auto picks
		// the channelizer. Frequencies are 200 kHz apart so they
		// occupy distinct bins (150 kHz bin width at M=16,
		// 2.4 MS/s).
		channels := []ChannelConfig{}
		for i := -3; i <= 3; i++ {
			channels = append(channels, ChannelConfig{
				FrequencyHz: uint32(int64(453_500_000) + int64(i)*200_000),
				SystemName:  "x",
			})
		}
		e, err := New(Options{
			Device: dev, Bus: bus, SampleRateHz: 2_400_000, CenterFreqHz: 453_500_000,
			Channels: channels,
		})
		if err != nil {
			t.Fatal(err)
		}
		if e.Strategy() != "auto(polyphase)" {
			t.Errorf("strategy = %q, want auto(polyphase)", e.Strategy())
		}
	})
}

func TestEngineStrategyExplicit(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	dev := newMockDevice(nil)
	cases := []struct {
		req, want string
	}{
		{"ddc", "ddc"},
		{"polyphase", "polyphase"},
	}
	for _, tc := range cases {
		e, err := New(Options{
			Device: dev, Bus: bus, SampleRateHz: 2_400_000, CenterFreqHz: 453_500_000,
			TunerStrategy: tc.req,
			Channels: []ChannelConfig{
				{FrequencyHz: 453_125_000, SystemName: "x"},
				{FrequencyHz: 453_775_000, SystemName: "x"},
			},
		})
		if err != nil {
			t.Fatalf("strategy %q: %v", tc.req, err)
		}
		if e.Strategy() != tc.want {
			t.Errorf("strategy %q: got %q, want %q", tc.req, e.Strategy(), tc.want)
		}
	}
}

func TestEngineRunSetsCenterFreqAndDrainsStream(t *testing.T) {
	bus := events.NewBus(64)
	defer bus.Close()

	// 4 silence chunks of 4800 wide-band samples each. The engine
	// must consume all of them then exit cleanly when the stream
	// closes.
	const chunkLen = 4800
	chunks := make([][]complex64, 4)
	for i := range chunks {
		chunks[i] = make([]complex64, chunkLen)
	}
	dev := newMockDevice(chunks)

	e, err := New(Options{
		Log:          slog.Default(),
		Device:       dev,
		Bus:          bus,
		SampleRateHz: 2_400_000,
		CenterFreqHz: 453_500_000,
		Channels: []ChannelConfig{
			{FrequencyHz: 453_125_000, SystemName: "regional-t2"},
			{FrequencyHz: 453_775_000, SystemName: "regional-t2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Errorf("Run: %v", err)
	}
	if got := dev.centerFreqHz.Load(); got != 453_500_000 {
		t.Errorf("device centre frequency = %d, want 453_500_000", got)
	}
	if got := len(e.Channels()); got != 2 {
		t.Errorf("Channels() len = %d, want 2", got)
	}
}

func TestEngineRunPropagatesStreamError(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	dev := newMockDevice(nil)
	dev.streamErr = errors.New("device dead")
	e, err := New(Options{
		Device: dev, Bus: bus, SampleRateHz: 2_400_000, CenterFreqHz: 453_500_000,
		Channels: []ChannelConfig{{FrequencyHz: 453_125_000, SystemName: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := e.Run(ctx); err == nil {
		t.Errorf("expected error from StreamIQ propagated, got nil")
	}
}
