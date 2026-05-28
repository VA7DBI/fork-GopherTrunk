package main

import (
	"log/slog"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/config"
	"github.com/MattCheramie/GopherTrunk/internal/sdr"
	"github.com/MattCheramie/GopherTrunk/internal/trunking"
)

// fakeWidebandDriver enumerates a single streamingFakeDevice under a
// fixed serial so a test can open a pool entry without real hardware.
type fakeWidebandDriver struct{ dev *streamingFakeDevice }

func (f *fakeWidebandDriver) Name() string { return "fakewb" }

func (f *fakeWidebandDriver) Enumerate() ([]sdr.Info, error) {
	return []sdr.Info{{Driver: "fakewb", Index: 0, Serial: f.dev.serial, Gains: []int{0}}}, nil
}

func (f *fakeWidebandDriver) Open(idx int) (sdr.Device, error) { return f.dev, nil }

// TestWidebandOnlyConfigPopulatesVoicePool locks issue #422: a topology
// with a single role:wideband dongle (no physical role:voice SDR) and
// voice_taps > 0 must yield a non-empty voice pool. The bug was that
// buildVirtualVoiceTuners ran *after* the voice pool and composer map
// were constructed, so every grant dropped with "no voice SDR".
func TestWidebandOnlyConfigPopulatesVoicePool(t *testing.T) {
	const serial = "wb-issue422"
	const centerHz = 859_837_500
	const sampleRate = 2_400_000
	const taps = 8

	// Register a driver exposing our serial. Strict mode (engaged below)
	// means any drivers leaked in by other tests in this package are
	// skipped, so the global registry's additive nature is harmless.
	sdr.Register(&fakeWidebandDriver{dev: newStreamingFake(serial)})

	log := slog.New(slog.DiscardHandler)
	cfg := config.Config{
		SDR: config.SDRConfig{
			SampleRate: sampleRate,
			Devices: []config.DeviceConfig{{
				Serial:       serial,
				Role:         "wideband",
				CenterFreqHz: centerHz,
				VoiceTaps:    taps,
			}},
		},
	}

	d := &Daemon{pool: sdr.NewPool(log)}
	if err := d.pool.OpenWith(sdr.PoolOpenOptions{
		SampleRateHz: sampleRate,
		Hints:        []sdr.Hint{{Serial: serial, Role: sdr.RoleWideband}},
		Strict:       true,
	}); err != nil {
		t.Fatalf("pool open: %v", err)
	}
	d.wrapIQBrokers(cfg, log)

	if err := d.buildVirtualVoiceTuners(cfg, log); err != nil {
		t.Fatalf("buildVirtualVoiceTuners: %v", err)
	}

	if got := len(d.virtualVoiceTuners); got != taps {
		t.Fatalf("virtualVoiceTuners = %d, want %d", got, taps)
	}
	if got := len(d.collectVoiceDevices()); got != taps {
		t.Fatalf("collectVoiceDevices = %d, want %d (wideband-only pool must not be empty)", got, taps)
	}
	if got := len(d.virtualVoiceMap()); got != taps {
		t.Fatalf("virtualVoiceMap = %d, want %d", got, taps)
	}

	// End-to-end angle: an in-window grant frequency from the issue must
	// resolve to a tap; a clearly out-of-window one must not.
	pool := trunking.NewVoicePool(d.collectVoiceDevices())
	if free := pool.FindFreeForFrequency(859_237_500); free == nil {
		t.Error("FindFreeForFrequency(859.2375 MHz, in-window) = nil, want a wideband tap")
	}
	if free := pool.FindFreeForFrequency(900_000_000); free != nil {
		t.Errorf("FindFreeForFrequency(900 MHz, out-of-window) = %q, want nil", free.Serial)
	}
}
