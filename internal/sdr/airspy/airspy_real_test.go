package airspy

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/sdr"
)

const (
	airspyRealEnv       = "GOPHERTRUNK_AIRSPY_REAL"
	airspyRealSerialEnv = "GOPHERTRUNK_AIRSPY_REAL_SERIAL"
	airspyRealHzEnv     = "GOPHERTRUNK_AIRSPY_REAL_CENTER_HZ"
	airspyRealRateEnv   = "GOPHERTRUNK_AIRSPY_REAL_RATE_HZ"
	airspyRealGainEnv   = "GOPHERTRUNK_AIRSPY_REAL_GAIN_TENTH_DB"
	airspyRealBiasEnv   = "GOPHERTRUNK_AIRSPY_REAL_BIAS_TEE"
)

func TestRealHardware_OpenConfigureStream(t *testing.T) {
	requireRealAirspy(t)

	centerHz := mustEnvUint32(t, airspyRealHzEnv, 144_390_000)
	rateHz := mustEnvUint32(t, airspyRealRateEnv, 2_500_000)
	gainTenthDB := mustEnvInt(t, airspyRealGainEnv, -1) // default auto AGC
	serialHint := strings.TrimSpace(os.Getenv(airspyRealSerialEnv))

	drv := New(nil)
	infos, err := drv.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(infos) == 0 {
		t.Fatalf("Enumerate returned no devices (set %s only when hardware is attached)", airspyRealEnv)
	}

	idx := 0
	if serialHint != "" {
		match := -1
		for i := range infos {
			if infos[i].Serial == serialHint || strings.Contains(infos[i].Serial, serialHint) {
				match = i
				break
			}
		}
		if match < 0 {
			t.Fatalf("no enumerated Airspy serial matched %q; found: %v", serialHint, collectSerials(infos))
		}
		idx = match
	}

	dev, err := drv.Open(idx)
	if err != nil {
		t.Fatalf("Open(%d): %v", idx, err)
	}
	t.Cleanup(func() { _ = dev.Close() })

	if err := dev.SetCenterFreq(centerHz); err != nil {
		t.Fatalf("SetCenterFreq(%d): %v", centerHz, err)
	}
	if err := dev.SetSampleRate(rateHz); err != nil {
		t.Fatalf("SetSampleRate(%d): %v", rateHz, err)
	}
	if err := dev.SetGain(gainTenthDB); err != nil {
		t.Fatalf("SetGain(%d): %v", gainTenthDB, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	iq, err := dev.StreamIQ(ctx)
	if err != nil {
		t.Fatalf("StreamIQ: %v", err)
	}

	select {
	case chunk, ok := <-iq:
		if !ok {
			t.Fatal("StreamIQ channel closed before first packet")
		}
		if len(chunk) == 0 {
			t.Fatal("StreamIQ produced an empty packet")
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for IQ packet: %v", ctx.Err())
	}
}

func TestRealHardware_BiasTeeToggle(t *testing.T) {
	requireRealAirspy(t)
	if !envBool(airspyRealBiasEnv) {
		t.Skipf("set %s=1 to run real Airspy bias-tee toggle validation", airspyRealBiasEnv)
	}

	serialHint := strings.TrimSpace(os.Getenv(airspyRealSerialEnv))

	drv := New(nil)
	infos, err := drv.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(infos) == 0 {
		t.Fatalf("Enumerate returned no devices (set %s only when hardware is attached)", airspyRealEnv)
	}

	idx := 0
	if serialHint != "" {
		match := -1
		for i := range infos {
			if infos[i].Serial == serialHint || strings.Contains(infos[i].Serial, serialHint) {
				match = i
				break
			}
		}
		if match < 0 {
			t.Fatalf("no enumerated Airspy serial matched %q; found: %v", serialHint, collectSerials(infos))
		}
		idx = match
	}

	dev, err := drv.Open(idx)
	if err != nil {
		t.Fatalf("Open(%d): %v", idx, err)
	}
	t.Cleanup(func() {
		// Best-effort safety reset: leave bias-tee off when the test exits.
		_ = dev.SetBiasTee(false)
		_ = dev.Close()
	})

	if err := dev.SetBiasTee(true); err != nil {
		t.Fatalf("SetBiasTee(true): %v", err)
	}
	if err := dev.SetBiasTee(false); err != nil {
		t.Fatalf("SetBiasTee(false): %v", err)
	}
}

func requireRealAirspy(t *testing.T) {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(airspyRealEnv))
	if v == "" || v == "0" || strings.EqualFold(v, "false") {
		t.Skipf("set %s=1 to run real Airspy hardware validation", airspyRealEnv)
	}
	if testing.Short() {
		t.Skip("skipping real Airspy hardware validation in -short mode")
	}
}

func mustEnvUint32(t *testing.T, key string, fallback uint32) uint32 {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		t.Fatalf("%s=%q is not a valid unsigned integer: %v", key, raw, err)
	}
	return uint32(v)
}

func mustEnvInt(t *testing.T, key string, fallback int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s=%q is not a valid integer: %v", key, raw, err)
	}
	return v
}

func envBool(key string) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return false
	}
	if v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") {
		return true
	}
	return false
}

func collectSerials(infos []sdr.Info) []string {
	out := make([]string, 0, len(infos))
	for _, i := range infos {
		out = append(out, i.Serial)
	}
	return out
}
