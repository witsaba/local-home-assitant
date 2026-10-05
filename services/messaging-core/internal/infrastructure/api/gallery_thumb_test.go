package api

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/gallery"
)

func thumbURL(date, tick, mac, w string) string {
	u := "/api/gallery/thumb?date=" + date + "&t=" + tick + "&mac=" + mac
	if w != "" {
		u += "&w=" + w
	}
	return u
}

func TestGalleryThumb_ServesGeneratedThumbnail(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, testDate, testTick, testMAC)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", thumbURL(testDate, testTick, testMAC, "320"), nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d (body %q)", rr.Code, http.StatusOK, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type: got %q, want image/jpeg", ct)
	}
	// A generated tile is immutable, so it is cacheable hard: this is what
	// keeps a 288-tile day from re-downloading on every view.
	if cc := rr.Header().Get("Cache-Control"); cc != "private, max-age=86400, immutable" {
		t.Errorf("Cache-Control: got %q, want the immutable form", cc)
	}
	if rr.Header().Get("ETag") == "" {
		t.Error("ETag: got empty, want a validator")
	}
	if _, err := jpeg.Decode(bytes.NewReader(rr.Body.Bytes())); err != nil {
		t.Errorf("body is not a decodable jpeg: %v", err)
	}
}

func TestGalleryThumb_DefaultWidthWhenOmitted(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, testDate, testTick, testMAC)

	withDefault := httptest.NewRecorder()
	h.ServeHTTP(withDefault, httptest.NewRequest("GET", thumbURL(testDate, testTick, testMAC, ""), nil))
	explicit := httptest.NewRecorder()
	h.ServeHTTP(explicit, httptest.NewRequest("GET", thumbURL(testDate, testTick, testMAC, "320"), nil))

	if withDefault.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", withDefault.Code, http.StatusOK)
	}
	// Omitting w must hit the same cache entry, not a separate one.
	if withDefault.Header().Get("ETag") != explicit.Header().Get("ETag") {
		t.Error("omitting w did not resolve to the canonical 320 thumbnail")
	}
}

func TestGalleryThumb_IfNoneMatchReturns304(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, testDate, testTick, testMAC)

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest("GET", thumbURL(testDate, testTick, testMAC, "320"), nil))
	etag := first.Header().Get("ETag")

	req := httptest.NewRequest("GET", thumbURL(testDate, testTick, testMAC, "320"), nil)
	req.Header.Set("If-None-Match", etag)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotModified {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusNotModified)
	}
}

func TestGalleryThumb_RejectsUnsupportedWidth(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, testDate, testTick, testMAC)

	// An unsupported width must be refused rather than clamped: a silently
	// narrowed tile looks like a page layout bug, not a client error.
	for _, w := range []string{"0", "-1", "123", "321", "99999", "abc", "320.5"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", thumbURL(testDate, testTick, testMAC, w), nil))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("w=%s: status got %d, want %d", w, rr.Code, http.StatusBadRequest)
		}
	}
}

func TestGalleryThumb_RejectsBadAddress(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, testDate, testTick, testMAC)

	cases := map[string]string{
		"traversal in date": thumbURL("../../etc", testTick, testMAC, "320"),
		"traversal in mac":  thumbURL(testDate, testTick, "..%2f..%2fetc", "320"),
		"uppercase mac":     thumbURL(testDate, testTick, "E08CFE3091B0", "320"),
		"impossible date":   thumbURL("2026-13-45", testTick, testMAC, "320"),
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

func TestGalleryThumb_MissingCaptureIs404(t *testing.T) {
	h, _ := newGalleryHandler(t)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", thumbURL(testDate, testTick, testMAC, "320"), nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// TestGalleryThumb_TruncatedCaptureIs503 proves a truncated source produces no
// tile at all, rather than a plausible-looking placeholder image for a moment
// that was never fully written.
func TestGalleryThumb_TruncatedCaptureIs503(t *testing.T) {
	h, root := newGalleryHandler(t)
	p := writeTestCapture(t, root, testDate, testTick, testMAC)

	full, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(p, full[:len(full)-8], 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", thumbURL(testDate, testTick, testMAC, "320"), nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}

	// Nothing may have been cached for a frame that cannot be decoded.
	thumbDir := filepath.Join(root, testDate, ".thumbs")
	if entries, err := os.ReadDir(thumbDir); err == nil {
		for _, e := range entries {
			t.Errorf("cache directory created for an incomplete capture: %s", e.Name())
		}
	}
}

func TestGalleryThumb_NoStoreReturns503(t *testing.T) {
	h := NewHandler(&mockRepo{}, nil, &mockLogger{})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", thumbURL(testDate, testTick, testMAC, "320"), nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestWriteFrameError_MapsSourceTooLarge(t *testing.T) {
	h := newTestHandler(t, &mockRepo{}, &mockLogger{})
	rr := httptest.NewRecorder()

	h.writeFrameError(rr, "test", testDate, testTick, testMAC,
		fmt.Errorf("%w: padded fixture", gallery.ErrSourceTooLarge))

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusRequestEntityTooLarge)
	}
}
