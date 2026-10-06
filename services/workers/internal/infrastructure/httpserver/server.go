// Package httpserver provides the HTTP surface of the workers service.
//
// The workers binary is otherwise a background job host with no listener
// at all. This package adds one, currently serving the gallery: listing
// the captured days, listing one day's photos, and serving a photo's
// bytes.
//
// It deliberately uses only the standard library. The service has no web
// framework dependency today and five routes are not a reason to add one.
// The stdlib ServeMux also gives method-aware routing
// ("GET /path"), so a POST to a read route answers 405 rather than
// silently reading.
//
// The listener binds loopback only. nginx is the single LAN-facing door,
// which is what lets the gallery be reachable from a phone on the local
// network without the gallery server itself ever being exposed.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/witsaba/local-home-assitant/services/workers/internal/jobs/surveillance"
)

const (
	// maxRangeDays caps the days= range. The week strip asks for seven.
	// The cap stops a single request from walking the entire archive.
	maxRangeDays = 31

	// readHeaderTimeout guards against a slow client holding a worker
	// open before it has sent a request line.
	readHeaderTimeout = 5 * time.Second
)

// Route prefixes. Kept as constants so main.go and the nginx config can
// be checked against one source of truth.
const (
	RouteHealth = "/healthz"
	RouteDays   = "/api/gallery/days"
	RouteDay    = "/api/gallery/day"
	RouteImage  = "/api/gallery/img"
)

// Handler serves the gallery API.
type Handler struct {
	store surveillance.Storage
	log   *zap.Logger
	mux   *http.ServeMux
}

// NewHandler returns a Handler serving the gallery for the capture root.
func NewHandler(root string, log *zap.Logger) *Handler {
	h := &Handler{
		store: surveillance.NewStorage(root),
		log:   log,
		mux:   http.NewServeMux(),
	}
	h.mux.HandleFunc("GET "+RouteHealth, h.healthz)
	h.mux.HandleFunc("GET "+RouteDays, h.listDays)
	h.mux.HandleFunc("GET "+RouteDay, h.listDay)
	h.mux.HandleFunc("GET "+RouteImage, h.serveImage)
	return h
}

// ServeHTTP dispatches to the internal mux.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// dayCount is the JSON shape of one cell in the week strip.
//
// It deliberately carries exactly one field per JSON tag. An earlier
// gallery attempt declared a field as both an int and a string slice
// under one tag, and encoding/json silently dropped both, leaving a
// calendar that rendered blank with no error anywhere.
type dayCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// photo is the JSON shape of one photo in a day view.
type photo struct {
	Name  string `json:"name"`
	MAC   string `json:"mac"`
	Time  string `json:"time"`
	Bytes int64  `json:"bytes"`
}

// apiError is the JSON shape of every error response.
type apiError struct {
	Error string `json:"error"`
}

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// listDays handles GET /api/gallery/days?from=&to=.
//
// It answers one entry per date in the range, INCLUDING dates with no
// captures, because the week strip needs seven cells to render whether
// or not anything was captured. Ranges default to today.
func (h *Handler) listDays(w http.ResponseWriter, r *http.Request) {
	from, to, ok := parseRange(r)
	if !ok {
		writeError(w, http.StatusBadRequest,
			"from and to must be YYYY-MM-DD, from must not be after to, and the range must not exceed 31 days")
		return
	}

	// make(..., 0, n) so an empty range marshals as [] and never as null.
	out := make([]dayCount, 0, maxRangeDays)
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		photos, err := h.store.ListPhotos(day)
		if err != nil {
			// One unreadable day must not blank the whole calendar.
			h.log.Warn("gallery: list day failed",
				zap.String("date", day.Format("2006-01-02")),
				zap.Error(err))
		}
		out = append(out, dayCount{
			Date:  day.Format("2006-01-02"),
			Count: len(photos),
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// listDay handles GET /api/gallery/day?date=.
func (h *Handler) listDay(w http.ResponseWriter, r *http.Request) {
	day, err := parseDay(r.URL.Query().Get("date"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "date must be a single YYYY-MM-DD capture day")
		return
	}

	photos, err := h.store.ListPhotos(day)
	if err != nil {
		h.log.Error("gallery: ListPhotos failed",
			zap.String("date", day.Format("2006-01-02")),
			zap.Error(err))
		writeError(w, http.StatusInternalServerError, "could not list that day")
		return
	}

	out := make([]photo, 0, len(photos))
	for _, p := range photos {
		out = append(out, photo{
			Name:  p.Name,
			MAC:   p.MAC,
			Time:  p.Time.Format(time.RFC3339),
			Bytes: p.Bytes,
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// serveImage handles GET /api/gallery/img?date=&name=.
//
// The name arrives from a request and is turned into a filesystem path,
// so this is the most dangerous handler in the service. It is gated by
// resolveCapture, which validates both components against the capture
// patterns AND proves the resolved path is still inside the capture
// root, so neither a traversal string nor a symlink planted in the root
// can read a file outside it.
func (h *Handler) serveImage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	path, err := resolveCapture(h.store.Root, q.Get("date"), q.Get("name"))
	switch {
	case errors.Is(err, errBadDay), errors.Is(err, errBadName):
		writeError(w, http.StatusBadRequest,
			"date must be YYYY-MM-DD and name must be HH-MM-SS_<12 hex>.jpg")
		return
	case errors.Is(err, errOutsideRoot):
		h.log.Warn("gallery: refused a path resolving outside the capture root",
			zap.String("date", q.Get("date")),
			zap.String("name", q.Get("name")))
		writeError(w, http.StatusForbidden, "refused")
		return
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, "no such photo")
		return
	case err != nil:
		h.log.Error("gallery: resolve failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "could not read that photo")
		return
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "no such photo")
			return
		}
		h.log.Error("gallery: open failed", zap.String("path", path), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "could not read that photo")
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read that photo")
		return
	}

	// no-cache, not no-store: the browser may hold the bytes but must
	// revalidate, so a photo the operator deletes stops being served
	// promptly while unchanged photos still answer 304.
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

// --- parameter validation ---------------------------------------------

var (
	errBadDay      = errors.New("invalid capture day")
	errBadName     = errors.New("invalid capture name")
	errOutsideRoot = errors.New("path resolves outside the capture root")
)

// resolveCapture turns a request-supplied day and name into an absolute
// path proven to sit inside root.
//
// Two independent gates:
//
//  1. Each component must match the capture pattern. Neither pattern can
//     match a separator, a dot segment, or a NUL, so the joined path is
//     inside root by construction.
//  2. Both sides are resolved through EvalSymlinks and the result is
//     required to be under the resolved root. This catches a symlink
//     planted inside the capture root — including a symlinked DAY
//     directory — which gate one alone cannot see.
//
// Resolving the root as well matters on its own: a temp dir or /var on
// macOS is reached through a symlink, so comparing a resolved file
// against an unresolved root would reject every legitimate request.
func resolveCapture(root, day, name string) (string, error) {
	if !surveillance.ValidCaptureDay(day) {
		return "", errBadDay
	}
	if !surveillance.ValidCaptureName(name) {
		return "", errBadName
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}

	real, err := filepath.EvalSymlinks(filepath.Join(root, day, name))
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(real, realRoot+string(os.PathSeparator)) {
		return "", errOutsideRoot
	}
	return real, nil
}

// parseDay validates a YYYY-MM-DD capture day and returns it at midnight
// local time.
func parseDay(v string) (time.Time, error) {
	if !surveillance.ValidCaptureDay(v) {
		return time.Time{}, errBadDay
	}
	t, err := time.ParseInLocation("2006-01-02", v, time.Local)
	if err != nil {
		return time.Time{}, errBadDay
	}
	return t, nil
}

// parseRange reads from/to, defaulting both to today, and rejects a
// reversed or oversized range.
// An absent parameter means "today"; a parameter that is PRESENT BUT
// EMPTY is a client bug and is rejected. Treating the two the same way
// would make `date=` and `from=` behave inconsistently.
func parseRange(r *http.Request) (time.Time, time.Time, bool) {
	q := r.URL.Query()

	to := startOfToday()
	if q.Has("to") {
		parsed, err := parseDay(q.Get("to"))
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
		to = parsed
	}

	from := to
	if q.Has("from") {
		parsed, err := parseDay(q.Get("from"))
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
		from = parsed
	}

	if from.After(to) {
		return time.Time{}, time.Time{}, false
	}
	// Inclusive of both endpoints, so 1..31 days spans 32 entries.
	if to.Sub(from) > (maxRangeDays-1)*24*time.Hour {
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func startOfToday() time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.Local)
}

// --- responses ---------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	// Nothing useful can be done about a write failure here; the status
	// line has already gone out.
	_ = json.NewEncoder(w).Encode(payload)
}

// writeError always answers JSON, never the plain text http.Error
// produces. The frontend has a single api() helper that always parses
// JSON, so a text error body would turn a clean failure into an
// unhandled rejection.
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, apiError{Error: msg})
}

// --- lifecycle ---------------------------------------------------------

// Serve runs the listener on addr until ctx is cancelled, then drains
// in-flight requests within shutdownTimeout.
//
// It returns nil on a clean shutdown. A bind failure is returned to the
// caller rather than logged and swallowed, because a workers process
// whose gallery cannot bind has a silently missing gallery.
func Serve(ctx context.Context, h *Handler, addr string, shutdownTimeout time.Duration, log *zap.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	log.Info("gallery: http listening",
		zap.String("addr", addr),
		zap.String("root", h.store.Root),
	)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("gallery: shutdown: %w", err)
	}
	return <-errCh
}
