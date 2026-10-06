// server_test.go — unit tests for the workers HTTP gallery surface.
package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/witsaba/local-home-assitant/services/workers/internal/jobs/surveillance"
)

// newTestHandler returns a Handler over a temp capture root seeded with
// captures on the given days. day -> number of photos.
func newTestHandler(t *testing.T, days map[string]int) (*Handler, string) {
	t.Helper()

	root := t.TempDir()
	store := surveillance.NewStorage(root)

	for day, n := range days {
		d, err := time.ParseInLocation("2006-01-02", day, time.Local)
		if err != nil {
			t.Fatalf("bad fixture day %q: %v", day, err)
		}
		for i := 0; i < n; i++ {
			mac := "aabbccddeeff"
			if i%2 == 1 {
				mac = "112233445566"
			}
			at := time.Date(d.Year(), d.Month(), d.Day(), 12, i, 0, 0, time.Local)
			if _, err := store.WriteFile(store.PathFor(at, mac), []byte("jpeg-bytes")); err != nil {
				t.Fatalf("seed %s: %v", day, err)
			}
		}
	}
	return NewHandler(root, zap.NewNop()), root
}

// do issues a request against the handler and returns the recorder.
func do(t *testing.T, h *Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHandler_Healthz(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, nil)
	rec := do(t, h, http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("body = %q, want status ok", rec.Body.String())
	}
}

// --- /api/gallery/days -------------------------------------------------

func TestHandler_Days_ReturnsEveryDateInRangeWithCount(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, map[string]int{
		"2026-10-05": 2,
		"2026-10-06": 3,
		"2026-10-07": 0, // never captured
	})

	rec := do(t, h, http.MethodGet, "/api/gallery/days?from=2026-10-05&to=2026-10-07")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	var got []dayCount
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (body %q)", err, rec.Body.String())
	}

	// Every date in the range must appear, including the empty one —
	// that is what lets the week strip render seven cells.
	if len(got) != 3 {
		t.Fatalf("got %d days %+v, want 3", len(got), got)
	}
	wantCounts := map[string]int{"2026-10-05": 2, "2026-10-06": 3, "2026-10-07": 0}
	for _, d := range got {
		if want, ok := wantCounts[d.Date]; !ok {
			t.Errorf("unexpected date %q", d.Date)
		} else if d.Count != want {
			t.Errorf("date %q count = %d, want %d", d.Date, d.Count, want)
		}
	}
}

// A day with nothing captured still appears, with count 0 — that is what
// lets the week strip render seven cells. The body must be a JSON array,
// never null: the previous attempt answered `null` here and the calendar
// rendered as a silent blank.
func TestHandler_Days_UncapturedDayIsCountZeroNotNull(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, nil)
	rec := do(t, h, http.MethodGet, "/api/gallery/days?from=2026-10-05&to=2026-10-05")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); !strings.HasPrefix(body, "[") {
		t.Errorf("body = %q, want a JSON array — null renders a blank calendar", body)
	}
	var got []dayCount
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Date != "2026-10-05" || got[0].Count != 0 {
		t.Errorf("got %+v, want exactly one entry for 2026-10-05 with count 0", got)
	}
}

func TestHandler_Days_DefaultsToTodayWhenParamsAbsent(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, nil)
	rec := do(t, h, http.MethodGet, "/api/gallery/days")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []dayCount
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d days, want exactly today", len(got))
	}
	if want := time.Now().Format("2006-01-02"); got[0].Date != want {
		t.Errorf("date = %q, want today %q", got[0].Date, want)
	}
}

func TestHandler_Days_RejectsHostileAndMalformedParams(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, nil)
	cases := []struct{ name, query string }{
		{"traversal", "from=../../etc&to=2026-10-07"},
		{"absolute", "from=/etc&to=2026-10-07"},
		{"bad shape", "from=20261005&to=2026-10-07"},
		{"impossible month", "from=2026-13-45&to=2026-10-07"},
		{"nul byte", "from=2026-10-0%006&to=2026-10-07"},
		{"reversed range", "from=2026-10-07&to=2026-10-05"},
		{"range too wide", "from=2020-01-01&to=2026-10-07"},
		{"empty from", "from=&to=2026-10-07"},
	}
	for _, tc := range cases {
		rec := do(t, h, http.MethodGet, "/api/gallery/days?"+tc.query)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %q)", tc.name, rec.Code, rec.Body.String())
		}
	}
}

// --- /api/gallery/day --------------------------------------------------

func TestHandler_Day_ReturnsPhotosWithFields(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, map[string]int{"2026-10-06": 2})
	rec := do(t, h, http.MethodGet, "/api/gallery/day?date=2026-10-06")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	var got []photo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d photos, want 2", len(got))
	}

	// Newest first.
	if got[0].Time <= got[1].Time {
		t.Errorf("photos not newest-first: %q then %q", got[0].Time, got[1].Time)
	}
	for _, p := range got {
		if p.Name == "" || p.MAC == "" || p.Time == "" {
			t.Errorf("photo has an empty field: %+v", p)
		}
		if !strings.HasSuffix(p.Name, ".jpg") {
			t.Errorf("name = %q, want a .jpg", p.Name)
		}
		if p.Bytes != int64(len("jpeg-bytes")) {
			t.Errorf("bytes = %d, want %d", p.Bytes, len("jpeg-bytes"))
		}
	}
}

// Regression guard. An earlier gallery attempt declared one Go struct
// field as both `int` and `string[]` under a single json tag;
// encoding/json silently dropped BOTH and the UI just looked empty
// with no error. This pins that every field actually survives.
func TestHandler_Day_JSONCarriesEveryField(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, map[string]int{"2026-10-06": 1})
	rec := do(t, h, http.MethodGet, "/api/gallery/day?date=2026-10-06")

	var raw []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("got %d photos, want 1", len(raw))
	}
	for _, key := range []string{"name", "mac", "time", "bytes"} {
		if _, ok := raw[0][key]; !ok {
			t.Errorf("json is missing key %q — a duplicate struct tag would drop it silently. body: %s",
				key, rec.Body.String())
		}
	}
}

func TestHandler_Day_MissingDayIs200EmptyArray(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, map[string]int{"2026-10-06": 1})
	rec := do(t, h, http.MethodGet, "/api/gallery/day?date=2026-01-01")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("body = %q, want %q", got, "[]")
	}
}

func TestHandler_Day_RejectsMalformedDate(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, nil)
	cases := []string{
		"date=../../etc",
		"date=2026-10-06/../../etc",
		"date=/etc",
		"date=2026-13-45",
		"date=20261006",
		"date=2026-10-06x",
		"date=",
		"date=2026-10-06%20",
		"",
	}
	for _, q := range cases {
		rec := do(t, h, http.MethodGet, "/api/gallery/day?"+q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q: status = %d, want 400 (body %q)", q, rec.Code, rec.Body.String())
		}
	}
}

// --- /api/gallery/img --------------------------------------------------

func TestHandler_Img_ServesTheJpeg(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, map[string]int{"2026-10-06": 1})
	name := "12-00-00_aabbccddeeff.jpg"
	rec := do(t, h, http.MethodGet, "/api/gallery/img?date=2026-10-06&name="+name)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", got)
	}
	if rec.Body.String() != "jpeg-bytes" {
		t.Errorf("body = %q, want the stored bytes", rec.Body.String())
	}
}

func TestHandler_Img_MissingFileIs404(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, map[string]int{"2026-10-06": 1})
	rec := do(t, h, http.MethodGet, "/api/gallery/img?date=2026-10-06&name=13-45-00_112233445566.jpg")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// This is the test that matters most in the whole feature. A name that
// looks superficially right must still never escape the capture root.
func TestHandler_Img_RejectsHostileNames(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, map[string]int{"2026-10-06": 1})
	cases := []struct{ name, query string }{
		{"dotdot file", "date=2026-10-06&name=../../../../.ssh/authorized_keys"},
		{"dotdot day", "date=../..&name=12-00-00_aabbccddeeff.jpg"},
		{"absolute name", "date=2026-10-06&name=/etc/passwd"},
		{"absolute day", "date=/etc&name=12-00-00_aabbccddeeff.jpg"},
		{"valid prefix trailing junk", "date=2026-10-06&name=12-00-00_aabbccddeeff.jpg.bak"},
		{"uppercase mac", "date=2026-10-06&name=12-00-00_AABBCCDDEEFF.jpg"},
		{"short mac", "date=2026-10-06&name=12-00-00_aabb.jpg"},
		{"no extension", "date=2026-10-06&name=12-00-00_aabbccddeeff"},
		{"nested path", "date=2026-10-06&name=sub/12-00-00_aabbccddeeff.jpg"},
		{"encoded traversal", "date=2026-10-06&name=..%2f..%2fetc%2fpasswd"},
		{"missing name", "date=2026-10-06"},
		{"missing date", "name=12-00-00_aabbccddeeff.jpg"},
	}
	for _, tc := range cases {
		rec := do(t, h, http.MethodGet, "/api/gallery/img?"+tc.query)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %q)", tc.name, rec.Code, rec.Body.String())
		}
	}
}

// A filesystem attacker who can drop a symlink into the capture root
// must not be able to make the gallery serve a file from outside it.
func TestHandler_Img_SymlinkPointingOutsideRootIsRefused(t *testing.T) {
	t.Parallel()

	h, root := newTestHandler(t, map[string]int{"2026-10-06": 1})

	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	// Looks exactly like a capture. Is not one.
	link := filepath.Join(root, "2026-10-06", "13-00-00_aabbccddeeff.jpg")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/gallery/img?date=2026-10-06&name=13-00-00_aabbccddeeff.jpg")
	if rec.Code == http.StatusOK {
		t.Fatalf("status = 200 — served a symlink target from outside the root: %q",
			rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "TOP-SECRET") {
		t.Errorf("secret leaked in body: %q", rec.Body.String())
	}
}

// A symlinked DAY directory must not become a way out either.
func TestHandler_Img_SymlinkedDayDirectoryIsRefused(t *testing.T) {
	t.Parallel()

	h, root := newTestHandler(t, map[string]int{"2026-10-06": 1})

	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "12-00-00_aabbccddeeff.jpg"),
		[]byte("OUTSIDE"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	link := filepath.Join(root, "2026-10-09")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	rec := do(t, h, http.MethodGet,
		"/api/gallery/img?date=2026-10-09&name=12-00-00_aabbccddeeff.jpg")
	if rec.Code == http.StatusOK {
		t.Fatalf("status = 200 — served through a symlinked day directory: %q",
			rec.Body.String())
	}
}

// --- method handling ---------------------------------------------------

func TestHandler_RejectsWrongMethods(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, map[string]int{"2026-10-06": 1})
	cases := []struct{ method, target string }{
		{http.MethodPost, "/api/gallery/days"},
		{http.MethodPost, "/api/gallery/day?date=2026-10-06"},
		{http.MethodPut, "/api/gallery/img?date=2026-10-06&name=12-00-00_aabbccddeeff.jpg"},
		{http.MethodDelete, "/api/gallery/days"},
	}
	for _, tc := range cases {
		rec := do(t, h, tc.method, tc.target)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want 405", tc.method, tc.target, rec.Code)
		}
	}
}

// Every response, including errors, must be JSON. The frontend has a
// single api() helper that always parses JSON, so a plain-text error
// page turns a clean error into an unhandled rejection.
func TestHandler_ErrorsAreJSON(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t, nil)
	rec := do(t, h, http.MethodGet, "/api/gallery/day?date=../..")
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if !json.Valid(rec.Body.Bytes()) {
		t.Errorf("body is not valid JSON: %q", rec.Body.String())
	}
}

// TestServe_BindsServesAndDrains exercises the real listener, not just
// the handler. It is the only test that proves Serve binds a port,
// answers on it, and shuts down cleanly when its context is cancelled —
// which is exactly what the systemd unit depends on.
func TestServe_BindsServesAndDrains(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := surveillance.NewStorage(root)
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	if _, err := store.WriteFile(store.PathFor(at, "aabbccddeeff"), []byte("real-jpeg")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Bind :0 to learn a free port, then release it for Serve to claim.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, NewHandler(root, zap.NewNop()), addr, 5*time.Second, zap.NewNop())
	}()

	base := "http://" + addr
	// Give the listener a moment to come up.
	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.Get(base + RouteHealth)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("listener never answered on %s: %v", addr, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", resp.StatusCode)
	}

	// A real image fetch over the wire.
	imgResp, err := http.Get(base + RouteImage + "?date=2026-10-06&name=12-00-00_aabbccddeeff.jpg")
	if err != nil {
		t.Fatalf("image fetch: %v", err)
	}
	body, _ := io.ReadAll(imgResp.Body)
	imgResp.Body.Close()
	if imgResp.StatusCode != http.StatusOK || string(body) != "real-jpeg" {
		t.Errorf("image = %d %q, want 200 %q", imgResp.StatusCode, body, "real-jpeg")
	}

	// Cancelling must drain and return nil, not hang.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil on clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return within 10s of cancellation")
	}

	if _, err := http.Get(base + RouteHealth); err == nil {
		t.Error("listener still answering after shutdown")
	}
}
