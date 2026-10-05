package api

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/devices"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/gallery"
)

// encodeJPEG renders a deterministic gradient at the requested size.
//
// The bytes come from the standard library encoder rather than a hand-written
// fixture, because the truncation guard checks for the real JPEG boundary
// markers and a hand-rolled fake would only agree with the guard by accident.
func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, colorFor(x, y))
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encoding test jpeg: %v", err)
	}
	return buf.Bytes()
}

// writeCaptureAt writes raw bytes to the on-disk address of one capture.
func writeCaptureAt(t *testing.T, root, date, tick, mac string, data []byte) string {
	t.Helper()

	dir := filepath.Join(root, date)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating day dir: %v", err)
	}
	p := filepath.Join(dir, tick+"_"+mac+".jpg")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("writing capture: %v", err)
	}
	return p
}

// writeTestCapture writes a 64x48 frame and returns its path.
func writeTestCapture(t *testing.T, root, date, tick, mac string) string {
	t.Helper()
	return writeCaptureAt(t, root, date, tick, mac, encodeJPEG(t, 64, 48))
}

// newGalleryHandler returns a handler over a temporary capture root, plus
// that directory.
//
// The store is built over the same root the test writes into. This cannot go
// through the shared newTestHandler helper, because each t.TempDir() call
// returns a distinct directory: the handler would then read an empty archive
// while the test populated a different one, and every request would 404 for
// reasons that have nothing to do with the code under test.
func newGalleryHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	root := t.TempDir()
	return NewHandler(&mockRepo{}, gallery.NewStore(root), &mockLogger{}), root
}

// colorFor paints a deterministic gradient so every generated frame is
// distinct from the others and compresses to a realistic, non-uniform size.
func colorFor(x, y int) color.RGBA {
	return color.RGBA{R: uint8(x * 4), G: uint8(y * 5), B: uint8((x + y) * 3), A: 0xFF}
}

const (
	testDate = "2026-09-30"
	testTick = "21-30-45"
	testMAC  = "e08cfe3091b0"
)

func imgURL(date, tick, mac string) string {
	return "/api/gallery/img?date=" + date + "&t=" + tick + "&mac=" + mac
}

func TestGalleryImage_ServesWholeFrame(t *testing.T) {
	h, root := newGalleryHandler(t)
	p := writeTestCapture(t, root, testDate, testTick, testMAC)
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading back capture: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", imgURL(testDate, testTick, testMAC), nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d (body %q)", rr.Code, http.StatusOK, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type: got %q, want %q", ct, "image/jpeg")
	}
	if !bytes.Equal(rr.Body.Bytes(), want) {
		t.Errorf("body: got %d bytes, want the %d stored bytes", rr.Body.Len(), len(want))
	}
	if got := rr.Header().Get("Last-Modified"); got == "" {
		t.Error("Last-Modified: got empty, want the frame mtime")
	}
	if got := rr.Header().Get("ETag"); got == "" {
		t.Error("ETag: got empty, want a weak validator")
	}
}

func TestGalleryImage_ETagIsWeakSizeAndMtime(t *testing.T) {
	h, root := newGalleryHandler(t)
	p := writeTestCapture(t, root, testDate, testTick, testMAC)

	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", imgURL(testDate, testTick, testMAC), nil))

	want := fmt.Sprintf(`W/"%d-%d"`, info.Size(), info.ModTime().UnixNano())
	if got := rr.Header().Get("ETag"); got != want {
		t.Errorf("ETag: got %q, want %q", got, want)
	}
}

// TestGalleryImage_IfNoneMatchReturns304 is the caching contract: a repeat
// view of the same day must not re-transfer image bytes.
func TestGalleryImage_IfNoneMatchReturns304(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, testDate, testTick, testMAC)

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest("GET", imgURL(testDate, testTick, testMAC), nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the first response")
	}

	req := httptest.NewRequest("GET", imgURL(testDate, testTick, testMAC), nil)
	req.Header.Set("If-None-Match", etag)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotModified {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusNotModified)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body: got %d bytes on a 304, want none", rr.Body.Len())
	}
}

// TestGalleryImage_StaleETagReturns200 guards the other direction: a client
// holding a validator for different bytes must be sent the new frame rather
// than a 304 it would wrongly reuse.
func TestGalleryImage_StaleETagReturns200(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, testDate, testTick, testMAC)

	req := httptest.NewRequest("GET", imgURL(testDate, testTick, testMAC), nil)
	req.Header.Set("If-None-Match", `W/"1-1"`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

// TestGalleryImage_TruncatedCaptureIsRefused is the guard the firmware
// truncation risk requires: a frame that exists but has no end-of-image
// marker must not be served as a broken image.
func TestGalleryImage_TruncatedCaptureIsRefused(t *testing.T) {
	h, root := newGalleryHandler(t)
	p := writeTestCapture(t, root, testDate, testTick, testMAC)

	full, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading capture: %v", err)
	}
	// Chop the tail, which is exactly what a write interrupted before
	// flush completes looks like.
	if err := os.WriteFile(p, full[:len(full)-8], 0o644); err != nil {
		t.Fatalf("truncating capture: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", imgURL(testDate, testTick, testMAC), nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	if got := rr.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After: got empty, want a hint that the next tick fixes it")
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want a JSON error rather than image bytes", ct)
	}
}

func TestGalleryImage_EmptyCaptureIsRefused(t *testing.T) {
	h, root := newGalleryHandler(t)
	dir := filepath.Join(root, testDate)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating day dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, testTick+"_"+testMAC+".jpg"), nil, 0o644); err != nil {
		t.Fatalf("writing empty capture: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", imgURL(testDate, testTick, testMAC), nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestGalleryImage_MissingCaptureIs404(t *testing.T) {
	h, _ := newGalleryHandler(t)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", imgURL(testDate, testTick, testMAC), nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// TestGalleryImage_RejectsBadComponents covers each validator, because
// each one is the only thing standing between a query string and the
// filesystem.
func TestGalleryImage_RejectsBadComponents(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, testDate, testTick, testMAC)

	cases := map[string]string{
		"traversal in date":  "/api/gallery/img?date=../../etc&t=" + testTick + "&mac=" + testMAC,
		"traversal in tick":  "/api/gallery/img?date=" + testDate + "&t=../../../etc&mac=" + testMAC,
		"traversal in mac":   "/api/gallery/img?date=" + testDate + "&t=" + testTick + "&mac=../../../../etc",
		"non-hex mac":        "/api/gallery/img?date=" + testDate + "&t=" + testTick + "&mac=zzzzzzzzzzzz",
		"uppercase mac":      "/api/gallery/img?date=" + testDate + "&t=" + testTick + "&mac=E08CFE3091B0",
		"impossible date":    "/api/gallery/img?date=2026-13-45&t=" + testTick + "&mac=" + testMAC,
		"impossible time":    "/api/gallery/img?date=" + testDate + "&t=99-99-99&mac=" + testMAC,
		"missing everything": "/api/gallery/img",
	}

	for name, url := range cases {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest("GET", url, nil))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
	}
}

// TestGalleryImage_RangeRequestIsHonoured proves the frame is streamed and
// not buffered whole: a byte range returns that range, which only works
// against a real io.ReadSeeker.
func TestGalleryImage_RangeRequestIsHonoured(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, testDate, testTick, testMAC)

	req := httptest.NewRequest("GET", imgURL(testDate, testTick, testMAC), nil)
	req.Header.Set("Range", "bytes=0-9")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusPartialContent {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusPartialContent)
	}
	if rr.Body.Len() != 10 {
		t.Errorf("body: got %d bytes, want 10", rr.Body.Len())
	}
}

// TestGalleryImage_NoStoreReturns503 documents that a deployment without a
// capture root fails the gallery without taking the device list down.
func TestGalleryImage_NoStoreReturns503(t *testing.T) {
	h := NewHandler(&mockRepo{}, nil, &mockLogger{})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", imgURL(testDate, testTick, testMAC), nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

// TestGalleryImage_DoesNotAffectDeviceList is the regression guard for the
// handler signature change: the existing route must still work when a
// gallery store is present.
func TestGalleryImage_DoesNotAffectDeviceList(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	repo := &mockRepo{devs: []*devices.Device{{
		MAC: testMAC, Name: "kitchen-cam", LastSeenAt: now,
	}}}

	h := newTestHandler(t, repo, &mockLogger{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/devices/active", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

// TestOpenFrame_ETagChangesWhenBytesChange pins the validator to the bytes,
// not just the address: overwriting a frame must invalidate the old ETag or
// the browser would keep showing the old image from cache.
func TestOpenFrame_ETagChangesWhenBytesChange(t *testing.T) {
	root := t.TempDir()
	store := gallery.NewStore(root)
	writeTestCapture(t, root, testDate, testTick, testMAC)

	first, err := store.OpenFrame(testDate, testTick, testMAC)
	if err != nil {
		t.Fatalf("OpenFrame: %v", err)
	}
	firstETag := first.ETag()
	first.Close()

	// Re-encode at a different size rather than appending bytes: the result
	// stays a valid JPEG, and the size change is exactly what must move the
	// validator.
	writeCaptureAt(t, root, testDate, testTick, testMAC, encodeJPEG(t, 80, 60))

	second, err := store.OpenFrame(testDate, testTick, testMAC)
	if err != nil {
		t.Fatalf("OpenFrame after rewrite: %v", err)
	}
	defer second.Close()

	if second.ETag() == firstETag {
		t.Errorf("ETag stayed %q across a content change; it must track the bytes", firstETag)
	}
}
