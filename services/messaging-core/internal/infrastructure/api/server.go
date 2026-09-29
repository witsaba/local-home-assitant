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
//
// It must be comfortably GREATER than the discovery worker's scan interval
// (DISCOVERY_INTERVAL_SECONDS, default 60s). When the two are equal the list
// flaps: every device is re-probed at interval + the time it takes the pool to
// reach it inside the scan, so its age crosses the cutoff just before its own
// refresh lands and the API drops it. Measured on the Pi at 60s/60s, sampling
// /api/devices/active every 5s:
//
//   0, 3, 3, 3, 3, 3, 3, 3, 1, 0, 2, 2, 2, 2, 2, 2, 2, 0, 3, ...
//
// The devices were present the entire time -- discovery reported all three
// every cycle with upsert_ok=true. 3x the default interval absorbs one or two
// missed scans, so a single slow cycle no longer empties the list.
//
// If DISCOVERY_INTERVAL_SECONDS is raised, raise this too. testActiveWindow
// ExceedsDiscoveryInterval asserts the relationship holds.
const maxActiveAge = 3 * time.Minute

// defaultDiscoveryInterval mirrors the workers default
// (DISCOVERY_INTERVAL_SECONDS). Only used by the test that documents the
// coupling above.
const defaultDiscoveryInterval = time.Minute

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
// It returns devices whose last_seen_at is within maxActiveAge.
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
