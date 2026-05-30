package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/api"
	"github.com/MattCheramie/GopherTrunk/internal/config"
	"github.com/MattCheramie/GopherTrunk/internal/sdr"
	"github.com/MattCheramie/GopherTrunk/internal/sdr/plutoplus"
	"github.com/MattCheramie/GopherTrunk/internal/voice/player"
)

// runtimeSnapshot wraps the daemon config + a few process-global
// registries into the api.RuntimeProvider shape. The DTO is rebuilt
// on every /api/v1/runtime call since the daemon config is immutable
// at runtime; cost is a handful of slice allocs.
type runtimeSnapshot struct {
	cfg     *config.Config
	version string
	metrics bool
	daemon  *Daemon // for StartupWarnings, ConfigPath, ...
}

// vocoderProtocolMap is the canonical mapping the daemon hands to
// each protocol's voice decoder at construction time. Surfaced here
// so the TUI's Vocoders tab matches what the engine actually loads.
// Keep in sync with the per-protocol pipeline factories in
// internal/radio/*/pipeline.go.
var vocoderProtocolMap = map[string]string{
	"p25":        "imbe",
	"p25-phase2": "ambe2",
	"dmr":        "ambe2",
	"nxdn":       "ambe2",
	"dpmr":       "ambe2",
	"tetra":      "ambe2",
	"ysf":        "ambe2",
	"edacs":      "ambe2",
	"motorola":   "—",
	"ltr":        "—",
	"mpt1327":    "—",
}

type plutoHealthThresholds struct {
	RecentWindow      time.Duration
	UnstableFailures  uint64
	DegradedFailures  uint64
	DegradedReconnect uint64
}

func (r *runtimeSnapshot) Runtime() api.RuntimeDTO {
	cfg := r.cfg
	dto := api.RuntimeDTO{
		HTTPAddr:       cfg.API.HTTPAddr,
		GRPCAddr:       cfg.API.GRPCAddr,
		AllowMutations: cfg.API.AllowMutations,
		LogLevel:       cfg.Log.Level,
		LogFormat:      cfg.Log.Format,
		Version:        r.version,

		StorageDBPath:  cfg.Storage.Path,
		StorageCCCache: cfg.Storage.CCCacheFile,

		RetentionCallLogDays: cfg.Retention.CallLogDays,
		RetentionFilesDays:   cfg.Retention.FilesDays,

		RecordingDir:        cfg.Recordings.Dir,
		RecordingSampleRate: int(cfg.Recordings.SampleRate),
		RecordingWriteRaw:   cfg.Recordings.WriteRaw,
		RecordingEQEnabled:  cfg.Recordings.Equalizer.Enabled,
		RecordingEQTaps:     cfg.Recordings.Equalizer.Taps,

		AudioEnabled:    cfg.Audio.Enabled,
		AudioDevice:     cfg.Audio.Device,
		AudioSampleRate: int(cfg.Audio.SampleRate),
		AudioBufferMs:   cfg.Audio.BufferMs,
		AudioBackends:   player.ListDevices(),

		SDRSampleRate: int(cfg.SDR.SampleRate),
		SDRBackends:   sdrBackendNames(),

		ScannerScanMode:          cfg.Scanner.ScanMode,
		ScannerCCHuntEnabled:     cfg.Scanner.CCHunt.Enabled,
		ScannerCCHuntDwellMs:     cfg.Scanner.CCHunt.DwellMs,
		ScannerCCHuntBackoffMs:   cfg.Scanner.CCHunt.BackoffMs,
		ScannerCCMaxBackoffMs:    cfg.Scanner.CCHunt.MaxBackoffMs,
		ScannerManualTuneEnabled: cfg.Scanner.ManualTuneEnabled,

		MetricsEnabled: r.metrics,
		VocoderMap:     vocoderProtocolMap,
	}
	pm := plutoplus.RuntimeMetricsSnapshot()
	thresholds := resolvePlutoHealthThresholds(cfg)
	healthClass, recentFailure := classifyPlutoHealth(pm, time.Now().UTC(), thresholds)
	dominantStage, _ := dominantPlutoFailureStage(pm)
	breakdown := plutoFailureBreakdown(pm)
	dto.PlutoRuntime = api.PlutoRuntimeDTO{
		Reconnects:        pm.Reconnects,
		ReconnectFailures: pm.ReconnectFailures,
		DialFailures:      pm.DialFailures,
		HandshakeFailures: pm.HandshakeFailures,
		CommandFailures:   pm.CommandFailures,
		StreamFailures:    pm.StreamFailures,
		UnknownFailures:   pm.UnknownFailures,
		LastFailureAt:     pm.LastFailureAt,
		HealthClass:       healthClass,
		RecentFailure:     recentFailure,
		DominantStage:     dominantStage,
		FailureBreakdown:  breakdown,
		RemediationHint:   plutoRemediationHint(dominantStage, recentFailure),
	}
	if cfg.Recordings.Equalizer.StepSize != 0 {
		dto.RecordingEQStepSize = formatFloat(float64(cfg.Recordings.Equalizer.StepSize))
	}
	if d, err := time.ParseDuration(cfg.Retention.Interval); err == nil {
		dto.RetentionInterval = d
	}
	for _, prof := range cfg.ToneOut.Profiles {
		cooldown, _ := time.ParseDuration(prof.Cooldown)
		dto.ToneProfiles = append(dto.ToneProfiles, api.ToneProfileDTO{
			Name:      prof.Name,
			AlphaTag:  prof.AlphaTag,
			Cooldown:  cooldown,
			ToneCount: len(prof.Tones),
		})
	}
	if r.daemon != nil {
		dto.ConfigPath = r.daemon.ConfigPath()
		dto.StartupWarnings = r.daemon.StartupWarnings()
		if ferr, fat, src := r.daemon.FatalStatus(); ferr != nil {
			dto.LastFatalError = ferr.Error()
			dto.LastFatalAt = fat
			dto.LastFatalComponent = src
			dto.LastFatalClass, dto.LastFatalHint = classifyFatal(src, ferr.Error())
		}
	}
	return dto
}

func classifyFatal(component, msg string) (class string, hint string) {
	lmsg := strings.ToLower(msg)
	switch {
	case strings.Contains(lmsg, "another gophertrunk is running") || strings.Contains(lmsg, ".gophertrunk.lock"):
		return "instance_lock", "Another instance is holding the config lock; stop the other process or remove a stale .gophertrunk.lock file."
	case strings.Contains(lmsg, "address already in use") || (strings.Contains(lmsg, "bind") && (component == "http" || component == "grpc" || component == "rigctld")):
		return "bind_conflict", "A listener port is already in use; change the configured address/port or stop the conflicting process."
	case strings.Contains(lmsg, "permission denied"):
		return "permission_denied", "Check filesystem/device permissions and rerun with an account that can access configured paths/devices."
	case strings.Contains(lmsg, "iq stream") || strings.Contains(lmsg, "device disconnected") || strings.Contains(lmsg, "usb"):
		return "sdr_disconnect", "SDR stream dropped unexpectedly; check USB cabling/power, then restart or allow supervisor restart."
	default:
		return "essential_component_failure", "An essential daemon component failed; inspect LastFatalComponent and logs for stack-specific details."
	}
}

func sdrBackendNames() []string {
	drivers := sdr.Drivers()
	out := make([]string, 0, len(drivers))
	for _, d := range drivers {
		out = append(out, d.Name())
	}
	return out
}

// formatFloat is a tiny helper to avoid pulling strconv into the API
// DTO encoding path. 6 significant digits is enough for step_size
// constants like 1e-4.
func formatFloat(v float64) string {
	// We deliberately go via fmt — strconv has no shorthand for "drop
	// trailing zeroes" without going through ParseFloat round-trips.
	const eps = 1e-12
	s := ""
	switch {
	case v == 0:
		return "0"
	case v < eps:
		s = fmt.Sprintf("%g", v)
	default:
		s = fmt.Sprintf("%g", v)
	}
	return s
}

func classifyPlutoHealth(pm plutoplus.RuntimeMetrics, now time.Time, thresholds plutoHealthThresholds) (string, bool) {
	failures := plutoFailureTotal(pm)
	recent := plutoFailuresRecent(pm, now, thresholds.RecentWindow)
	switch {
	case failures >= thresholds.UnstableFailures && recent:
		return "unstable", true
	case (failures >= thresholds.DegradedFailures && recent) || (pm.Reconnects >= thresholds.DegradedReconnect && recent):
		return "degraded", true
	case failures > 0:
		return "historical", false
	default:
		return "stable", false
	}
}

func plutoFailuresRecent(pm plutoplus.RuntimeMetrics, now time.Time, recentWindow time.Duration) bool {
	if pm.LastFailureAt.IsZero() {
		return plutoFailureTotal(pm) > 0
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if pm.LastFailureAt.After(now) {
		return true
	}
	return now.Sub(pm.LastFailureAt) <= recentWindow
}

func resolvePlutoHealthThresholds(cfg *config.Config) plutoHealthThresholds {
	out := plutoHealthThresholds{
		RecentWindow:      10 * time.Minute,
		UnstableFailures:  5,
		DegradedFailures:  1,
		DegradedReconnect: 3,
	}
	if cfg == nil {
		return out
	}
	ph := cfg.SDR.PlutoHealth
	if d, err := time.ParseDuration(ph.RecentWindow); err == nil && d > 0 {
		out.RecentWindow = d
	}
	if ph.UnstableFailures > 0 {
		out.UnstableFailures = uint64(ph.UnstableFailures)
	}
	if ph.DegradedFailures > 0 {
		out.DegradedFailures = uint64(ph.DegradedFailures)
	}
	if ph.DegradedReconnects > 0 {
		out.DegradedReconnect = uint64(ph.DegradedReconnects)
	}
	return out
}

func plutoFailureTotal(pm plutoplus.RuntimeMetrics) uint64 {
	return pm.ReconnectFailures + pm.DialFailures + pm.HandshakeFailures + pm.CommandFailures + pm.StreamFailures + pm.UnknownFailures
}

func dominantPlutoFailureStage(pm plutoplus.RuntimeMetrics) (string, uint64) {
	maxStage := ""
	maxCount := uint64(0)
	stages := []struct {
		name  string
		count uint64
	}{
		{name: "dial", count: pm.DialFailures},
		{name: "handshake", count: pm.HandshakeFailures},
		{name: "command", count: pm.CommandFailures},
		{name: "stream", count: pm.StreamFailures},
		{name: "unknown", count: pm.UnknownFailures},
	}
	for _, s := range stages {
		if s.count > maxCount {
			maxCount = s.count
			maxStage = s.name
		}
	}
	return maxStage, maxCount
}

func plutoFailureBreakdown(pm plutoplus.RuntimeMetrics) string {
	parts := make([]string, 0, 5)
	if pm.DialFailures > 0 {
		parts = append(parts, fmt.Sprintf("dial %d", pm.DialFailures))
	}
	if pm.HandshakeFailures > 0 {
		parts = append(parts, fmt.Sprintf("handshake %d", pm.HandshakeFailures))
	}
	if pm.CommandFailures > 0 {
		parts = append(parts, fmt.Sprintf("command %d", pm.CommandFailures))
	}
	if pm.StreamFailures > 0 {
		parts = append(parts, fmt.Sprintf("stream %d", pm.StreamFailures))
	}
	if pm.UnknownFailures > 0 {
		parts = append(parts, fmt.Sprintf("unknown %d", pm.UnknownFailures))
	}
	return strings.Join(parts, "  ·  ")
}

func plutoRemediationHint(stage string, recent bool) string {
	if !recent {
		return ""
	}
	switch stage {
	case "dial":
		return "check Pluto endpoint address/USB transport and device power"
	case "handshake":
		return "verify RTL-TCP compatibility and firmware behavior on connect"
	case "command":
		return "inspect tuner command sequence and Pluto command responses"
	case "stream":
		return "check USB/network stability and host performance under load"
	case "unknown":
		return "inspect daemon logs for plutoplus transport error details"
	default:
		return ""
	}
}
