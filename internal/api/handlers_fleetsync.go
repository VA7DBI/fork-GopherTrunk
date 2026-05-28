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
