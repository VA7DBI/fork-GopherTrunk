package broadcast

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// WebhookConfig configures one JSON call-export sink.
type WebhookConfig struct {
	// Name is an optional log label; defaults to "webhook".
	Name string
	// URL is the webhook endpoint that receives the completed call.
	URL string
	// Systems restricts the sink to these trunking-system names.
	// Empty streams every system.
	Systems []string
}

type webhookBackend struct {
	systemFilter
	name     string
	endpoint string
	http     *http.Client
}

// NewWebhook builds a JSON webhook call-export backend.
func NewWebhook(cfg WebhookConfig, hc *http.Client) (Backend, error) {
	if cfg.URL == "" {
		return nil, errors.New("broadcast/webhook: url is required")
	}
	name := cfg.Name
	if name == "" {
		name = "webhook"
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &webhookBackend{
		systemFilter: newSystemFilter(cfg.Systems),
		name:         name,
		endpoint:     cfg.URL,
		http:         hc,
	}, nil
}

func (b *webhookBackend) Name() string { return b.name }

type webhookPayload struct {
	System          string   `json:"system"`
	Protocol        string   `json:"protocol"`
	Talkgroup       uint32   `json:"talkgroup"`
	TalkgroupLabel  string   `json:"talkgroup_label,omitempty"`
	Source          uint32   `json:"source"`
	FrequencyHz     uint32   `json:"frequency_hz"`
	Encrypted       bool     `json:"encrypted"`
	AlgorithmID     uint8    `json:"algorithm_id,omitempty"`
	KeyID           uint16   `json:"key_id,omitempty"`
	Emergency       bool     `json:"emergency"`
	PatchedGroups   []uint32 `json:"patched_groups,omitempty"`
	StartedAt       string   `json:"started_at"`
	EndedAt         string   `json:"ended_at"`
	DurationMs      int64    `json:"duration_ms"`
	SampleRate      int      `json:"sample_rate"`
	AudioFilename   string   `json:"audio_filename"`
	AudioMPEGBase64 string   `json:"audio_mpeg_base64"`
}

// Send posts the completed call as JSON, including the MP3 payload.
func (b *webhookBackend) Send(ctx context.Context, c *Call) error {
	audio, err := c.MP3()
	if err != nil {
		return fmt.Errorf("%s: encode mp3: %w", b.name, err)
	}
	payload := webhookPayload{
		System:          c.System,
		Protocol:        c.Protocol,
		Talkgroup:       c.Talkgroup,
		TalkgroupLabel:  c.TalkgroupLabel,
		Source:          c.Source,
		FrequencyHz:     c.FrequencyHz,
		Encrypted:       c.Encrypted,
		AlgorithmID:     c.AlgorithmID,
		KeyID:           c.KeyID,
		Emergency:       c.Emergency,
		PatchedGroups:   c.PatchedGroups,
		StartedAt:       c.StartedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		EndedAt:         c.EndedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		DurationMs:      c.Duration().Milliseconds(),
		SampleRate:      c.SampleRate,
		AudioFilename:   audioFilename(c, "mp3"),
		AudioMPEGBase64: base64.StdEncoding.EncodeToString(audio),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%s: marshal webhook payload: %w", b.name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: post: %w", b.name, err)
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: HTTP %d: %s", b.name, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}