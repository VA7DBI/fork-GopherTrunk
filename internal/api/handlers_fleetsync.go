package api

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/storage"
)

// FleetSyncProvider is the read surface the FleetSync log endpoints consume.
type FleetSyncProvider interface {
	ListFleetSyncMessages(filter storage.FleetSyncFilter) ([]storage.FleetSyncMessage, error)
	GetFleetSyncMessage(id int64) (storage.FleetSyncMessage, error)
	FleetSyncStats(filter storage.FleetSyncFilter) (storage.FleetSyncStats, error)
	FleetSyncRuntimeStats() FleetSyncRuntimeStatsDTO
}

// FleetSyncMessageDTO is the JSON wire shape for FleetSync log endpoints.
type FleetSyncMessageDTO struct {
	ID         int64     `json:"id"`
	ReceivedAt time.Time `json:"received_at"`
	Version    uint8     `json:"version"`
	Command    uint8     `json:"command"`
	Subcommand uint8     `json:"subcommand"`
	FromFleet  uint8     `json:"from_fleet"`
	FromUnit   uint16    `json:"from_unit"`
	ToFleet    uint8     `json:"to_fleet"`
	ToUnit     uint16    `json:"to_unit"`
	AllFlag    bool      `json:"all_flag"`
	Emergency  bool      `json:"emergency"`
	Priority   bool      `json:"priority"`
	PayloadHex string    `json:"payload_hex"`
	RawHex     string    `json:"raw_hex"`
}

// FleetSyncStatsDTO is the JSON wire shape for FleetSync stats.
type FleetSyncStatsDTO struct {
	Total     int64                          `json:"total"`
	Emergency int64                          `json:"emergency"`
	Priority  int64                          `json:"priority"`
	FirstSeen time.Time                      `json:"first_seen"`
	LastSeen  time.Time                      `json:"last_seen"`
	Commands  []storage.FleetSyncCommandStat `json:"commands"`
	Runtime   FleetSyncRuntimeStatsDTO       `json:"runtime"`
}

// FleetSyncRuntimeStatsDTO captures live decoder/receiver telemetry.
type FleetSyncRuntimeStatsDTO struct {
	MessagesEmitted uint64                            `json:"messages_emitted"`
	TotalSamples    int64                             `json:"total_samples"`
	TotalMessagesRx int64                             `json:"total_messages_rx"`
	SyncErrors      int64                             `json:"sync_errors"`
	CRCErrors       int64                             `json:"crc_errors"`
	LastMessageTime time.Time                         `json:"last_message_time"`
	MessageRate     float64                           `json:"message_rate"`
	Channels        []FleetSyncRuntimeChannelStatsDTO `json:"channels,omitempty"`
	Export          FleetSyncExportRuntimeStatsDTO    `json:"export"`
}

// FleetSyncRuntimeChannelStatsDTO is per-receiver live telemetry.
type FleetSyncRuntimeChannelStatsDTO struct {
	Source          string    `json:"source"`
	MessagesEmitted uint64    `json:"messages_emitted"`
	TotalSamples    int64     `json:"total_samples"`
	TotalMessagesRx int64     `json:"total_messages_rx"`
	SyncErrors      int64     `json:"sync_errors"`
	CRCErrors       int64     `json:"crc_errors"`
	LastMessageTime time.Time `json:"last_message_time"`
	MessageRate     float64   `json:"message_rate"`
}

// FleetSyncExportRuntimeStatsDTO is exporter/back-end health telemetry.
type FleetSyncExportRuntimeStatsDTO struct {
	Queued                          int                              `json:"queued"`
	Dropped                         int                              `json:"dropped"`
	QueueDepth                      int                              `json:"queue_depth"`
	QueueCapacity                   int                              `json:"queue_capacity"`
	QueueUtilization                float64                          `json:"queue_utilization"`
	DroppedBySource                 map[string]int                   `json:"dropped_by_source,omitempty"`
	DroppedPerMinuteBySource        map[string]float64               `json:"dropped_per_minute_by_source,omitempty"`
	SentLast60sTotal                int                              `json:"sent_last_60s_total,omitempty"`
	FailedLast60sTotal              int                              `json:"failed_last_60s_total,omitempty"`
	SuccessRateLast60s              float64                          `json:"success_rate_last_60s,omitempty"`
	FailureRateLast60s              float64                          `json:"failure_rate_last_60s,omitempty"`
	DroppedLast60sTotal             int                              `json:"dropped_last_60s_total,omitempty"`
	DroppedPerMinuteLast60sTotal    float64                          `json:"dropped_per_minute_last_60s_total,omitempty"`
	DroppedLast60sBySource          map[string]int                   `json:"dropped_last_60s_by_source,omitempty"`
	DroppedPerMinuteLast60sBySource map[string]float64               `json:"dropped_per_minute_last_60s_by_source,omitempty"`
	Backends                        []FleetSyncExportBackendStatsDTO `json:"backends,omitempty"`
}

// FleetSyncExportBackendStatsDTO captures per-backend delivery counters.
type FleetSyncExportBackendStatsDTO struct {
	Name               string  `json:"name"`
	Sent               int     `json:"sent"`
	SentLast60s        int     `json:"sent_last_60s,omitempty"`
	SuccessRateLast60s float64 `json:"success_rate_last_60s,omitempty"`
	Failed             int     `json:"failed"`
	FailedLast60s      int     `json:"failed_last_60s,omitempty"`
	FailureRateLast60s float64 `json:"failure_rate_last_60s,omitempty"`
	Attempts           int     `json:"attempts"`
	AttemptsLast60s    int     `json:"attempts_last_60s,omitempty"`
	Retried            int     `json:"retried"`
	RetriedLast60s     int     `json:"retried_last_60s,omitempty"`
}

func fleetSyncMessageToDTO(m storage.FleetSyncMessage) FleetSyncMessageDTO {
	return FleetSyncMessageDTO{
		ID:         m.ID,
		ReceivedAt: m.ReceivedAt,
		Version:    m.Version,
		Command:    m.Command,
		Subcommand: m.Subcommand,
		FromFleet:  m.FromFleet,
		FromUnit:   m.FromUnit,
		ToFleet:    m.ToFleet,
		ToUnit:     m.ToUnit,
		AllFlag:    m.AllFlag,
		Emergency:  m.Emergency,
		Priority:   m.Priority,
		PayloadHex: strings.ToUpper(hex.EncodeToString(m.Payload)),
		RawHex:     strings.ToUpper(hex.EncodeToString(m.RawBytes)),
	}
}

// handleFleetSyncMessages answers GET /api/v1/fleetsync/messages.
func (s *Server) handleFleetSyncMessages(w http.ResponseWriter, r *http.Request) {
	if s.fleetsync == nil {
		writeError(w, http.StatusServiceUnavailable, "fleetsync subsystem not enabled")
		return
	}
	filter, err := parseFleetSyncFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.fleetsync.ListFleetSyncMessages(filter)
	if err != nil {
		s.log.Error("api: fleetsync messages", "err", err)
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]FleetSyncMessageDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, fleetSyncMessageToDTO(row))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleFleetSyncMessage answers GET /api/v1/fleetsync/messages/{id}.
func (s *Server) handleFleetSyncMessage(w http.ResponseWriter, r *http.Request) {
	if s.fleetsync == nil {
		writeError(w, http.StatusServiceUnavailable, "fleetsync subsystem not enabled")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	row, err := s.fleetsync.GetFleetSyncMessage(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		s.log.Error("api: fleetsync message", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, fleetSyncMessageToDTO(row))
}

// handleFleetSyncStats answers GET /api/v1/fleetsync/stats.
func (s *Server) handleFleetSyncStats(w http.ResponseWriter, r *http.Request) {
	if s.fleetsync == nil {
		writeError(w, http.StatusServiceUnavailable, "fleetsync subsystem not enabled")
		return
	}
	filter, err := parseFleetSyncFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter.Limit = 0 // stats endpoint is aggregate-only.
	stats, err := s.fleetsync.FleetSyncStats(filter)
	if err != nil {
		s.log.Error("api: fleetsync stats", "err", err)
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, FleetSyncStatsDTO{
		Total:     stats.Total,
		Emergency: stats.Emergency,
		Priority:  stats.Priority,
		FirstSeen: stats.FirstSeen,
		LastSeen:  stats.LastSeen,
		Commands:  stats.Commands,
		Runtime:   s.fleetsync.FleetSyncRuntimeStats(),
	})
}

func parseFleetSyncFilter(r *http.Request) (storage.FleetSyncFilter, error) {
	q := r.URL.Query()
	filter := storage.FleetSyncFilter{Limit: 200}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return filter, errors.New("invalid limit")
		}
		filter.Limit = n
	}
	if v := q.Get("source_unit"); v != "" {
		n, err := strconv.ParseUint(v, 10, 16)
		if err != nil {
			return filter, errors.New("invalid source_unit")
		}
		u := uint16(n)
		filter.SourceUnit = &u
	}
	if v := q.Get("destination_unit"); v != "" {
		n, err := strconv.ParseUint(v, 10, 16)
		if err != nil {
			return filter, errors.New("invalid destination_unit")
		}
		u := uint16(n)
		filter.DestinationUnit = &u
	}
	if v := q.Get("command"); v != "" {
		n, err := parseUint8(v)
		if err != nil {
			return filter, errors.New("invalid command")
		}
		filter.Command = &n
	}
	if v := q.Get("since"); v != "" {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return filter, errors.New("invalid since")
		}
		filter.Since = ts
	}
	if v := q.Get("until"); v != "" {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return filter, errors.New("invalid until")
		}
		filter.Until = ts
	}
	if !filter.Since.IsZero() && !filter.Until.IsZero() && filter.Until.Before(filter.Since) {
		return filter, errors.New("invalid range")
	}
	return filter, nil
}

func parseUint8(v string) (uint8, error) {
	base := 10
	value := strings.TrimSpace(strings.ToLower(v))
	if strings.HasPrefix(value, "0x") {
		base = 16
		value = strings.TrimPrefix(value, "0x")
	}
	n, err := strconv.ParseUint(value, base, 8)
	if err != nil {
		return 0, err
	}
	return uint8(n), nil
}
