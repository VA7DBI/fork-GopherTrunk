package api

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/storage"
)

// FleetSyncProvider is the read surface the FleetSync log endpoints consume.
type FleetSyncProvider interface {
	RecentFleetSyncMessages(limit int) ([]storage.FleetSyncMessage, error)
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
		s.writeError(w, http.StatusServiceUnavailable, "fleetsync subsystem not enabled")
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	rows, err := s.fleetsync.RecentFleetSyncMessages(limit)
	if err != nil {
		s.log.Error("api: fleetsync messages", "err", err)
		s.writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]FleetSyncMessageDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, fleetSyncMessageToDTO(row))
	}
	writeJSON(w, http.StatusOK, out)
}
