package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/MattCheramie/GopherTrunk/internal/api"
	"github.com/MattCheramie/GopherTrunk/internal/dsp/spectrum"
	"github.com/MattCheramie/GopherTrunk/internal/sdr"
	"github.com/MattCheramie/GopherTrunk/internal/sdr/iqtap"
)

// spectrumProvider implements api.SpectrumProvider on top of the
// daemon's iqtap broker map. Each open stream subscribes a fresh
// observer to the requested device's broker and runs a per-request
// spectrum.Producer that reads IQ chunks, runs windowed FFTs at the
// negotiated frame rate, and writes api.SpectrumFrame onto the
// returned channel. A cleanup func tears down the subscription and
// the producer's goroutine.
//
// Per-request producers (one FFT plan per WS client) keep the design
// simple and let each client pick its own FFT size / frame rate. CPU
// scales linearly with #clients, which is fine for the single- or
// few-operator deployments GopherTrunk targets — a typical browser
// session sums to one open stream at any time. A future optimization
// could share a producer across clients with the same (device, size,
// fps) tuple, but that adds enough bookkeeping to defer until profile
// data shows the wins.
type spectrumProvider struct {
	pool    *sdr.Pool
	brokers map[string]*iqtap.Broker
	log     *slog.Logger
}

func newSpectrumProvider(pool *sdr.Pool, brokers map[string]*iqtap.Broker, log *slog.Logger) *spectrumProvider {
	if log == nil {
		log = slog.Default()
	}
	return &spectrumProvider{pool: pool, brokers: brokers, log: log}
}

// Devices walks the broker map and reports each device's current
// tuning. Brokers know the most-recent SetCenterFreq / SetSampleRate
// because every setter funnels through them; the pool entry supplies
// driver / role / product metadata.
func (p *spectrumProvider) Devices() []api.SpectrumDevice {
	if p == nil || p.pool == nil || len(p.brokers) == 0 {
		return nil
	}
	entries := p.pool.Entries()
	out := make([]api.SpectrumDevice, 0, len(entries))
	for _, e := range entries {
		br, ok := p.brokers[e.Info.Serial]
		if !ok {
			continue
		}
		out = append(out, api.SpectrumDevice{
			Serial:       e.Info.Serial,
			Driver:       e.Info.Driver,
			Product:      e.Info.Product,
			Role:         e.Role.String(),
			CenterHz:     br.CenterHz(),
			SampleRateHz: br.SampleRateHz(),
		})
	}
	return out
}

// Tune programs the named SDR's centre frequency. Routes through
// the iqtap broker so the broker's CenterHz cache stays current
// (the spectrum + constellation frame stamps follow), the change
// survives pool.Reacquire, and external rigctld clients see the
// same view.
func (p *spectrumProvider) Tune(serial string, centerHz uint32) error {
	if p == nil {
		return errors.New("spectrum: provider not wired")
	}
	br, ok := p.brokers[serial]
	if !ok {
		return fmt.Errorf("spectrum: serial %q is not a known SDR", serial)
	}
	if err := br.SetCenterFreq(centerHz); err != nil {
		return fmt.Errorf("spectrum: tune %s to %d Hz: %w", serial, centerHz, err)
	}
	return nil
}

// OpenStream subscribes to the named device's broker and starts a
// producer goroutine that converts IQ to spectrum frames at the
// negotiated rate. The returned cleanup func MUST be called by the
// HTTP handler on disconnect — it closes the iqtap subscription and
// cancels the producer's context so its goroutine exits.
func (p *spectrumProvider) OpenStream(ctx context.Context, serial string, fftSize int, fps float64) (<-chan api.SpectrumFrame, func(), error) {
	if p == nil {
		return nil, nil, errors.New("spectrum: provider not wired")
	}
	br, ok := p.brokers[serial]
	if !ok {
		return nil, nil, fmt.Errorf("spectrum: serial %q is not a known SDR", serial)
	}
	prod, err := spectrum.New(spectrum.Options{
		FFTSize:      fftSize,
		FrameRate:    fps,
		CenterFreqHz: br.CenterHz(),
		SampleRateHz: br.SampleRateHz(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("spectrum: %w", err)
	}

	sub := br.Subscribe()
	internalOut := make(chan spectrum.Frame, 8)
	wireOut := make(chan api.SpectrumFrame, 8)

	streamCtx, cancel := context.WithCancel(ctx)

	go func() {
		defer close(internalOut)
		_ = prod.Run(streamCtx, sub.C, internalOut)
	}()

	go func() {
		defer close(wireOut)
		for {
			select {
			case <-streamCtx.Done():
				return
			case f, ok := <-internalOut:
				if !ok {
					return
				}
				// Refresh stamp in case the device retuned mid-stream.
				prod.SetCenter(br.CenterHz())
				prod.SetSampleRate(br.SampleRateHz())
				select {
				case wireOut <- api.SpectrumFrame{
					TimestampNs:  f.Timestamp.UnixNano(),
					CenterHz:     f.CenterHz,
					SampleRateHz: f.SampleRate,
					Bins:         f.Bins,
				}:
				case <-streamCtx.Done():
					return
				}
			}
		}
	}()

	cleanup := func() {
		cancel()
		sub.Close()
	}
	return wireOut, cleanup, nil
}
