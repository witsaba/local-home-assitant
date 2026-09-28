// Package api provides the HTTP REST API server for the messaging-core service.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/devices"
)

// maxActiveAge is the default lookback window for active devices.
const maxActiveAge = time.Minute

// Handler serves the messaging-core HTTP REST API.
type Handler struct {
	repo  devices.DeviceRepository
	log   ports.Logger
	mux   *http.ServeMux
}

// NewHandler returns a Handler wired with the given DeviceRepository.
func NewHandler(repo devices.DeviceRepository, log ports.Logger) *Handler {
	h := &Handler{repo: repo, log: log}
	h.mux = http.NewServeMux()
	h.mux.HandleFunc("GET /api/devices/active", h.listActive)
	return h
}

// ServeHTTP dispatches to the internal mux.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// deviceResponse is the JSON shape returned by GET /api/devices/active.
type deviceResponse struct {
	MAC          string `json:"mac"`
	Name         string `json:"name"`
	FW           string `json:"fw"`
	Chip         string `json:"chip"`
	LastSourceIP string `json:"last_source_ip,omitempty"`
	LastSeenAt   string `json:"last_seen_at"`
}

// listActive handles GET /api/devices/active.
// It returns devices whose last_seen_at is within the last 60 seconds.
func (h *Handler) listActive(w http.ResponseWriter, r *http.Request) {
	devs, err := h.repo.ListActive(r.Context(), maxActiveAge)
	if err != nil {
		h.log.Error("GET /api/devices/active: ListActive failed",
			ports.Field{Key: "err", Value: err.Error()},
		)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	out := make([]deviceResponse, 0, len(devs))
	for _, d := range devs {
		rsp := deviceResponse{
			MAC:        d.MAC,
			Name:       d.Name,
			FW:         d.FW,
			Chip:       d.Chip,
			LastSeenAt: d.LastSeenAt.Format(time.RFC3339),
		}
		if d.LastSourceIP != nil {
			rsp.LastSourceIP = d.LastSourceIP.String()
		}
		out = append(out, rsp)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(out); err != nil {
		h.log.Error("GET /api/devices/active: json encode failed",
			ports.Field{Key: "err", Value: err.Error()},
		)
	}
}
