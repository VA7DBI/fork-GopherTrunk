package broadcast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SpoolConfig configures one local file-queue export sink.
type SpoolConfig struct {
	// Name is an optional log label; defaults to "spool".
	Name string
	// Dir is the root directory for queued call exports.
	Dir string
	// Systems restricts the sink to these trunking-system names.
	// Empty streams every system.
	Systems []string
}

type spoolBackend struct {
	systemFilter
	name string
	dir  string
}

// NewSpool builds a local spool backend that writes one directory per call.
func NewSpool(cfg SpoolConfig, _ any) (Backend, error) {
	if cfg.Dir == "" {
		return nil, errors.New("broadcast/spool: dir is required")
	}
	name := cfg.Name
	if name == "" {
		name = "spool"
	}
	return &spoolBackend{
		systemFilter: newSystemFilter(cfg.Systems),
		name:         name,
		dir:          cfg.Dir,
	}, nil
}

func (b *spoolBackend) Name() string { return b.name }

type spoolPayload struct {
	System         string   `json:"system"`
	Protocol       string   `json:"protocol"`
	Talkgroup      uint32   `json:"talkgroup"`
	TalkgroupLabel string   `json:"talkgroup_label,omitempty"`
	Source         uint32   `json:"source"`
	FrequencyHz    uint32   `json:"frequency_hz"`
	Encrypted      bool     `json:"encrypted"`
	AlgorithmID    uint8    `json:"algorithm_id,omitempty"`
	KeyID          uint16   `json:"key_id,omitempty"`
	Emergency      bool     `json:"emergency"`
	PatchedGroups  []uint32 `json:"patched_groups,omitempty"`
	StartedAt      string   `json:"started_at"`
	EndedAt        string   `json:"ended_at"`
	DurationMs     int64    `json:"duration_ms"`
	SampleRate     int      `json:"sample_rate"`
	AudioFilename  string   `json:"audio_filename"`
}

// Send writes the completed call to disk as a small queue entry.
func (b *spoolBackend) Send(_ context.Context, c *Call) error {
	audio, err := c.MP3()
	if err != nil {
		return fmt.Errorf("%s: encode mp3: %w", b.name, err)
	}
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		return fmt.Errorf("%s: mkdir spool dir: %w", b.name, err)
	}
	entryDir := filepath.Join(b.dir, fmt.Sprintf("call-%d-tg%d-src%d", c.StartedAt.UTC().UnixNano(), c.Talkgroup, c.Source))
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		return fmt.Errorf("%s: mkdir entry dir: %w", b.name, err)
	}
	audioName := audioFilename(c, "mp3")
	payload := spoolPayload{
		System:         c.System,
		Protocol:       c.Protocol,
		Talkgroup:      c.Talkgroup,
		TalkgroupLabel: c.TalkgroupLabel,
		Source:         c.Source,
		FrequencyHz:    c.FrequencyHz,
		Encrypted:      c.Encrypted,
		AlgorithmID:    c.AlgorithmID,
		KeyID:          c.KeyID,
		Emergency:      c.Emergency,
		PatchedGroups:  c.PatchedGroups,
		StartedAt:      c.StartedAt.UTC().Format(time.RFC3339Nano),
		EndedAt:        c.EndedAt.UTC().Format(time.RFC3339Nano),
		DurationMs:     c.Duration().Milliseconds(),
		SampleRate:     c.SampleRate,
		AudioFilename:  audioName,
	}
	meta, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: marshal spool metadata: %w", b.name, err)
	}
	if err := os.WriteFile(filepath.Join(entryDir, "call.mp3"), audio, 0o644); err != nil {
		return fmt.Errorf("%s: write mp3: %w", b.name, err)
	}
	if err := os.WriteFile(filepath.Join(entryDir, "call.json"), meta, 0o644); err != nil {
		return fmt.Errorf("%s: write metadata: %w", b.name, err)
	}
	return nil
}