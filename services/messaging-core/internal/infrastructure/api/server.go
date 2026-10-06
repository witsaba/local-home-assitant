// Package api provides the HTTP REST API server for the messaging-core service.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/devices"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/gallery"
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
//	0, 3, 3, 3, 3, 3, 3, 3, 1, 0, 2, 2, 2, 2, 2, 2, 2, 0, 3, ...
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
	store *gallery.Store
	log   ports.Logger
	mux   *http.ServeMux
}

// NewHandler returns a Handler wired with the given DeviceRepository.
//
// store is the capture archive. It may be nil, in which case the gallery
// routes answer 503: a deployment with no capture root configured is a
// configuration gap to report, not a reason to fail the whole API, and the
// device list must keep working either way.
func NewHandler(repo devices.DeviceRepository, store *gallery.Store, log ports.Logger) *Handler {
	h := &Handler{repo: repo, store: store, log: log}
	h.mux = http.NewServeMux()
	h.mux.HandleFunc("GET /api/devices/active", h.listActive)
	h.mux.HandleFunc("GET /api/gallery/days", h.galleryDays)
	h.mux.HandleFunc("GET /api/gallery/day", h.galleryDay)
	h.mux.HandleFunc("DELETE /api/gallery/day", h.deleteGalleryDay)
	h.mux.HandleFunc("GET /api/gallery/img", h.galleryImage)
	h.mux.HandleFunc("GET /api/gallery/thumb", h.galleryThumb)
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

// galleryUnavailable reports whether the gallery routes can serve a request.
//
// A nil store means no capture root was configured at startup. That is a
// deployment gap, so it is reported as 503 with a reason a human can act on
// rather than as a 500, and the device routes keep working.
func (h *Handler) galleryUnavailable(w http.ResponseWriter, route string) bool {
	if h.store == nil {
		h.log.Warn("gallery request with no capture store configured",
			ports.Field{Key: "route", Value: route},
		)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "gallery is not configured on this server",
		})
		return true
	}
	return false
}

// writeJSON writes a JSON body with the given status code.
func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// galleryImage serves one full-resolution capture.
//
// The response is streamed straight from the file descriptor through
// http.ServeContent, which is the whole reason this handler is short: that
// function already implements If-None-Match, If-Modified-Since, Range,
// Accept-Ranges and Content-Type from the extension, and it does all of it
// without reading the frame into memory. Setting the ETag beforehand is
// enough for it to use that as the validator.
func (h *Handler) galleryImage(w http.ResponseWriter, r *http.Request) {
	const route = "GET /api/gallery/img"
	if h.galleryUnavailable(w, route) {
		return
	}

	q := r.URL.Query()
	date, tick, mac := q.Get("date"), q.Get("t"), q.Get("mac")

	// Each component is validated before the address is used, and a
	// malformed one is a 400 rather than a 404: the client sent something
	// it could have formatted correctly, and a 404 would send it hunting
	// for a capture that never existed.
	if err := gallery.ValidateDate(date); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := gallery.ValidateTick(tick); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := gallery.ValidateMAC(mac); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	frame, err := h.store.OpenFrame(date, tick, mac)
	if err != nil {
		h.writeFrameError(w, route, date, tick, mac, err)
		return
	}
	defer frame.Close()

	// A completed capture never changes, but the newest one on a day can be
	// rewritten by a capture that was interrupted and retried, so the
	// client is told to revalidate. The ETag makes revalidation cheap:
	// an unchanged frame costs a 304 and no image bytes.
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	w.Header().Set("ETag", frame.ETag())

	http.ServeContent(w, r, frame.Name(), frame.ModTime(), frame)
}

// galleryThumb serves one generated thumbnail, creating it on first request.
//
// The page asks for gallery.DefaultThumbWidth and nothing else, so a day view
// generates exactly one thumbnail per capture. Generation happens here rather
// than in the workers service on purpose: the worker would have to decode and
// scale every capture for every camera whether or not anyone ever looks at
// that day, and the gallery is read far more rarely than it is written.
func (h *Handler) galleryThumb(w http.ResponseWriter, r *http.Request) {
	const route = "GET /api/gallery/thumb"
	if h.galleryUnavailable(w, route) {
		return
	}

	q := r.URL.Query()
	date, tick, mac := q.Get("date"), q.Get("t"), q.Get("mac")

	if err := gallery.ValidateDate(date); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := gallery.ValidateTick(tick); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := gallery.ValidateMAC(mac); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	width, err := thumbWidthParam(q.Get("w"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	thumb, err := h.store.Thumb(date, tick, mac, width)
	if err != nil {
		h.writeFrameError(w, route, date, tick, mac, err)
		return
	}
	defer thumb.Close()

	// Unlike a full frame, a thumbnail is immutable: it is derived from a
	// capture that already passed the completeness guard, and nothing
	// rewrites it afterwards. That is what makes a day of grid tiles cost
	// one request each, and a reload costs nothing. The ETag is still set so
	// a client that does revalidate gets a 304 rather than the bytes.
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	w.Header().Set("ETag", thumb.ETag())

	http.ServeContent(w, r, thumb.Name(), thumb.ModTime(), thumb)
}

// thumbWidthParam parses the optional w parameter.
//
// An absent width is the canonical size, which keeps the common URL short and
// means a page that does not care about tile density still gets the cached
// tiles every other client is already using. A present but unsupported width
// is a 400 rather than something to clamp, because a silently narrowed tile
// looks like a layout bug on the page and not like a request it got wrong.
func thumbWidthParam(raw string) (int, error) {
	if raw == "" {
		return gallery.DefaultThumbWidth, nil
	}
	w, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("width %q is not a number", raw)
	}
	if !gallery.IsAllowedThumbWidth(w) {
		return 0, fmt.Errorf("width %d is not supported; use one of %v",
			w, gallery.AllowedThumbWidths())
	}
	return w, nil
}

// writeFrameError maps a failed capture lookup onto a response.
//
// An incomplete capture is 503 with Retry-After rather than 404 or 500
// because it is a real condition with a known cause and a known resolution:
// the firmware returned from writing a frame that had not finished flushing.
// The next tick writes a whole file at the same address, so this is a
// temporary unavailability of one image rather than a missing resource or a
// server fault. Retry-After stops a page full of such frames from being
// re-requested in a tight loop while the day is still being written.
func (h *Handler) writeFrameError(w http.ResponseWriter, route, date, tick, mac string, err error) {
	switch {
	case errors.Is(err, gallery.ErrFrameNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "no capture at that date, time and camera",
		})

	case errors.Is(err, gallery.ErrSourceTooLarge):
		// 413 is the honest status: the referenced file is real and
		// readable, it is simply larger than this server will decode. No
		// thumbnail is written, and a full frame can still be fetched.
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": "that capture is too large to thumbnail",
		})

	case errors.Is(err, gallery.ErrFrameIncomplete):
		h.log.Warn("refusing to serve an incomplete capture",
			ports.Field{Key: "route", Value: route},
			ports.Field{Key: "date", Value: date},
			ports.Field{Key: "t", Value: tick},
			ports.Field{Key: "mac", Value: mac},
			ports.Field{Key: "err", Value: err.Error()},
		)
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "that capture is incomplete and will be replaced by the next tick",
		})

	default:
		h.log.Error("capture lookup failed",
			ports.Field{Key: "route", Value: route},
			ports.Field{Key: "date", Value: date},
			ports.Field{Key: "t", Value: tick},
			ports.Field{Key: "mac", Value: mac},
			ports.Field{Key: "err", Value: err.Error()},
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "could not read that capture",
		})
	}
}

// galleryDays handles GET /api/gallery/days. It returns every day folder in
// the capture archive, newest first, and drives the date picker. The JSON
// shape is owned by the gallery package, not restated here.
func (h *Handler) galleryDays(w http.ResponseWriter, r *http.Request) {
	if h.galleryUnavailable(w, "GET /api/gallery/days") {
		return
	}

	days, err := h.store.ListDays()
	if err != nil {
		h.log.Error("GET /api/gallery/days: ListDays failed",
			ports.Field{Key: "err", Value: err.Error()},
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "could not read the capture archive",
		})
		return
	}

	// A missing root yields an empty slice from the store, but never a nil
	// one, so this encodes [] rather than null.
	writeJSON(w, http.StatusOK, days)
}

// galleryDay handles GET /api/gallery/day?date=YYYY-MM-DD.
//
// The date is validated before any path is built, and an invalid one is a
// 400 rather than a 404: the client sent something malformed, and telling it
// "not found" would send it looking for a day that never existed.
func (h *Handler) galleryDay(w http.ResponseWriter, r *http.Request) {
	if h.galleryUnavailable(w, "GET /api/gallery/day") {
		return
	}

	date := r.URL.Query().Get("date")
	if err := gallery.ValidateDate(date); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}

	names, err := h.cameraNames(r)
	if err != nil {
		h.log.Error("GET /api/gallery/day: ListAll failed",
			ports.Field{Key: "date", Value: date},
			ports.Field{Key: "err", Value: err.Error()},
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "could not resolve camera names",
		})
		return
	}

	day, err := h.store.Day(date, names)
	if err != nil {
		// ValidateDate already ran, so any error here is a filesystem
		// fault or the response size cap, both of which are server-side.
		h.log.Error("GET /api/gallery/day: Day failed",
			ports.Field{Key: "date", Value: date},
			ports.Field{Key: "err", Value: err.Error()},
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "could not read that day",
		})
		return
	}

	writeJSON(w, http.StatusOK, day)
}

// cameraNames maps MAC to display name for every device the stack has ever
// seen.
//
// ListAll is used rather than ListActive on purpose: ListActive drops a
// camera once it has been unseen for maxActiveAge, so a gallery day from
// last week would come back as bare MACs for anything currently offline.
// A MAC with no row keeps an empty name and the page falls back to the MAC;
// the API never invents a name for a capture.
func (h *Handler) cameraNames(r *http.Request) (map[string]string, error) {
	devs, err := h.repo.ListAll(r.Context())
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(devs))
	for _, d := range devs {
		names[d.MAC] = d.Name
	}
	return names, nil
}

// deleteGalleryDay handles DELETE /api/gallery/day?date=YYYY-MM-DD.
//
// The date is validated before any path is used. A missing day is a 404, not
// a 500, because the operator may have already deleted it manually. The
// operation is permanent: there is no trash or undo, matching the design
// decision recorded in the gallery ODD document.
func (h *Handler) deleteGalleryDay(w http.ResponseWriter, r *http.Request) {
	const route = "DELETE /api/gallery/day"
	if h.galleryUnavailable(w, route) {
		return
	}

	date := r.URL.Query().Get("date")
	if err := gallery.ValidateDate(date); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}

	if err := h.store.RemoveDay(date); err != nil {
		if errors.Is(err, gallery.ErrDayNotFound) {
			h.log.Warn("DELETE /api/gallery/day: day not found",
				ports.Field{Key: "date", Value: date},
			)
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "no such day",
			})
			return
		}
		h.log.Error("DELETE /api/gallery/day: RemoveDay failed",
			ports.Field{Key: "date", Value: date},
			ports.Field{Key: "err", Value: err.Error()},
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "could not remove that day",
		})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
