// thumbnail_test.go — unit tests for the on-disk thumbnail cache.
package httpserver

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// makeJPEG returns a JPEG of the given size whose pixels are a coarse
// gradient, so a downscale produces something with visible structure.
func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(x * 255 / w),
				G: uint8(y * 255 / h),
				B: uint8((x + y) % 256),
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return buf.Bytes()
}

// newThumbFixture returns a thumbnailer and a seeded capture.
func newThumbFixture(t *testing.T, srcW, srcH int) (*thumbnailer, string, string, string) {
	t.Helper()

	archive := t.TempDir()
	cache := t.TempDir()

	day, name := "2026-10-06", "12-00-00_aabbccddeeff.jpg"
	body := makeJPEG(t, srcW, srcH)

	dir := filepath.Join(archive, day)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatalf("seed capture: %v", err)
	}

	return newThumbnailer(cache, thumbnailWidth), archive, day, name
}

func TestThumbnail_GeneratesAndDownscales(t *testing.T) {
	t.Parallel()

	th, archive, day, name := newThumbFixture(t, 800, 600)

	out, err := th.get(day, name, filepath.Join(archive, day, name))
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open thumb: %v", err)
	}
	defer f.Close()

	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("format = %q, want jpeg", format)
	}
	if cfg.Width != thumbnailWidth {
		t.Errorf("width = %d, want %d", cfg.Width, thumbnailWidth)
	}
	// Aspect ratio must be preserved: 800x600 -> width x 0.75
	wantH := thumbnailWidth * 600 / 800
	if cfg.Height != wantH {
		t.Errorf("height = %d, want %d (aspect preserved)", cfg.Height, wantH)
	}
}

// The whole point of the cache: the second request must not redo the
// work. Asserted by mutating the cached file and seeing the mutation
// served back.
func TestThumbnail_SecondCallIsServedFromCache(t *testing.T) {
	t.Parallel()

	th, archive, day, name := newThumbFixture(t, 800, 600)
	src := filepath.Join(archive, day, name)

	first, err := th.get(day, name, src)
	if err != nil {
		t.Fatalf("first get: %v", err)
	}

	// Poison the cache entry. A regeneration would overwrite this.
	if err := os.WriteFile(first, []byte("CACHE-SENTINEL"), 0o644); err != nil {
		t.Fatalf("poison cache: %v", err)
	}

	second, err := th.get(day, name, src)
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	if second != first {
		t.Errorf("cache path changed: %q then %q", first, second)
	}
	got, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if string(got) != "CACHE-SENTINEL" {
		t.Error("cache was regenerated — the second call should reuse the cached file")
	}
}

// The cache must live under its own root, never inside the archive, and
// never anywhere the systemd unit would deny writes to.
func TestThumbnail_CacheLivesUnderCacheRootNotArchive(t *testing.T) {
	t.Parallel()

	th, archive, day, name := newThumbFixture(t, 800, 600)
	out, err := th.get(day, name, filepath.Join(archive, day, name))
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if !strings.HasPrefix(out, th.root+string(os.PathSeparator)) {
		t.Errorf("thumb %q is not under the cache root %q", out, th.root)
	}
	if strings.HasPrefix(out, archive+string(os.PathSeparator)) {
		t.Errorf("thumb %q was written inside the capture archive", out)
	}
	want := filepath.Join(th.root, day, name)
	if out != want {
		t.Errorf("thumb path = %q, want %q", out, want)
	}
}

// The cache is a second filesystem surface. A hostile name must not be
// able to steer a write outside the cache root.
func TestThumbnail_RejectsHostileNames(t *testing.T) {
	t.Parallel()

	th := newThumbnailer(t.TempDir(), thumbnailWidth)

	cases := []struct{ day, name string }{
		{"2026-10-06", "../../../../.ssh/authorized_keys"},
		{"../../etc", "12-00-00_aabbccddeeff.jpg"},
		{"2026-10-06", "/etc/passwd"},
		{"2026-10-06", "12-00-00_aabbccddeeff.jpg.bak"},
		{"2026-10-06", "12-00-00_aabbccddeeff"},
		{"2026-10-06", ""},
		{"", "12-00-00_aabbccddeeff.jpg"},
	}
	for _, tc := range cases {
		if _, err := th.pathFor(tc.day, tc.name); err == nil {
			t.Errorf("pathFor(%q, %q) Err = nil, want non-nil", tc.day, tc.name)
		}
	}
}

// A corrupted or truncated source must not leave a half-written cache
// entry behind: the next request would serve that broken file forever.
func TestThumbnail_CorruptSourceLeavesNoCacheEntry(t *testing.T) {
	t.Parallel()

	th, archive, day, name := newThumbFixture(t, 800, 600)
	src := filepath.Join(archive, day, name)
	if err := os.WriteFile(src, []byte("this is not a jpeg"), 0o644); err != nil {
		t.Fatalf("corrupt source: %v", err)
	}

	if _, err := th.get(day, name, src); err == nil {
		t.Fatal("get on a corrupt source: Err = nil, want non-nil")
	}

	cachePath := filepath.Join(th.root, day, name)
	if _, err := os.Stat(cachePath); err == nil {
		t.Error("a cache entry was left behind for a corrupt source")
	}

	// No stray temp files either.
	if entries, err := os.ReadDir(filepath.Join(th.root, day)); err == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), ".tmp-") {
				t.Errorf("stray temp file left behind: %s", e.Name())
			}
		}
	}
}

func TestThumbnail_MissingSourceIsAnError(t *testing.T) {
	t.Parallel()

	th, archive, day, name := newThumbFixture(t, 800, 600)
	// Remove the capture after seeding.
	if err := os.Remove(filepath.Join(archive, day, name)); err != nil {
		t.Fatalf("remove capture: %v", err)
	}

	if _, err := th.get(day, name, filepath.Join(archive, day, name)); err == nil {
		t.Fatal("get on a missing source: Err = nil, want non-nil")
	}
}

// Concurrent requests for the same uncached photo must not corrupt the
// cache or serve a partial file. This is the same torn-read hazard W1
// fixed on the capture side.
func TestThumbnail_ConcurrentGetsAreSafe(t *testing.T) {
	t.Parallel()

	th, archive, day, name := newThumbFixture(t, 800, 600)
	src := filepath.Join(archive, day, name)

	const workers = 8
	var wg sync.WaitGroup
	results := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = th.get(day, name, src)
		}(i)
	}
	wg.Wait()

	for i, err := range results {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}

	// Whatever landed must be a complete, decodable JPEG.
	out, err := th.pathFor(day, name)
	if err != nil {
		t.Fatalf("pathFor: %v", err)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	defer f.Close()
	if _, err := jpeg.Decode(f); err != nil {
		t.Errorf("cached thumb is not a complete JPEG: %v", err)
	}
}

// An already-smaller image must not be upscaled back up.
func TestThumbnail_DoesNotUpscale(t *testing.T) {
	t.Parallel()

	th, archive, day, name := newThumbFixture(t, 64, 48)
	out, err := th.get(day, name, filepath.Join(archive, day, name))
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.Width != 64 {
		t.Errorf("width = %d, want the original 64 (no upscaling)", cfg.Width)
	}
}

func TestThumbnail_DropRemovesCacheEntry(t *testing.T) {
	t.Parallel()

	th, archive, day, name := newThumbFixture(t, 800, 600)
	out, err := th.get(day, name, filepath.Join(archive, day, name))
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if err := th.drop(day, name); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("cache entry survived drop")
	}
}

// Dropping an absent entry is not an error: deleting a photo whose
// thumbnail was never generated must still succeed.
func TestThumbnail_DropIsIdempotent(t *testing.T) {
	t.Parallel()

	th := newThumbnailer(t.TempDir(), thumbnailWidth)
	for i := 0; i < 2; i++ {
		if err := th.drop("2026-10-06", "12-00-00_aabbccddeeff.jpg"); err != nil {
			t.Fatalf("drop #%d: %v", i+1, err)
		}
	}
}
