package gallery

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const (
	tDate = "2026-09-30"
	tTick = "21-30-45"
	tMAC  = "e08cfe3091b0"
)

// encodeTestJPEG renders a deterministic noise-ish gradient at the given
// size. Real encoder output is required: the completeness guard checks the
// actual JPEG boundary markers, so a hand-written fixture would only agree
// with the guard by accident.
func encodeTestJPEG(t *testing.T, w, h int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(x*7 + y*3),
				G: uint8(y*5 + x),
				B: uint8((x ^ y) * 2),
				A: 0xFF,
			})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encoding test jpeg: %v", err)
	}
	return buf.Bytes()
}

func writeTestFrame(t *testing.T, root string, data []byte) string {
	t.Helper()

	dir := filepath.Join(root, tDate)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating day dir: %v", err)
	}
	p := filepath.Join(dir, tTick+"_"+tMAC+".jpg")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("writing frame: %v", err)
	}
	return p
}

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	return NewStore(root), root
}

func TestThumb_GeneratesOnMissAndServesOnHit(t *testing.T) {
	store, root := newTestStore(t)
	writeTestFrame(t, root, encodeTestJPEG(t, 640, 480))

	frame, err := store.Thumb(tDate, tTick, tMAC, DefaultThumbWidth)
	if err != nil {
		t.Fatalf("Thumb: %v", err)
	}
	defer frame.Close()

	cfg, format, err := image.DecodeConfig(frame)
	if err != nil {
		t.Fatalf("decoding generated thumb: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("format: got %q, want jpeg", format)
	}
	if long := max(cfg.Width, cfg.Height); long != DefaultThumbWidth {
		t.Errorf("longest edge: got %d, want %d", long, DefaultThumbWidth)
	}
	// Aspect ratio must survive the downscale: 640x480 becomes 320x240, so
	// the width is the longest edge and the height is 3/4 of it.
	if got, want := cfg.Height, DefaultThumbWidth*480/640; got != want {
		t.Errorf("height: got %d, want %d (480/640 aspect preserved)", got, want)
	}
}

func TestThumb_CachesUnderWidthSubdirectory(t *testing.T) {
	store, root := newTestStore(t)
	writeTestFrame(t, root, encodeTestJPEG(t, 640, 480))

	for _, w := range []int{160, 320, 640} {
		frame, err := store.Thumb(tDate, tTick, tMAC, w)
		if err != nil {
			t.Fatalf("Thumb(%d): %v", w, err)
		}
		frame.Close()
	}

	// Each width is cached under its own directory so two sizes cannot
	// serve each other's bytes.
	for _, w := range []int{160, 320, 640} {
		p := filepath.Join(root, tDate, thumbDirName, itoa(w), tTick+"_"+tMAC+".jpg")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("width %d not cached at %s: %v", w, p, err)
		}
	}
}

func itoa(n int) string {
	return string(rune('0'+n/100)) + string(rune('0'+(n/10)%10)) + string(rune('0'+n%10))
}

// TestThumb_SameWidthReturnsIdenticalETag proves the hit path returns the
// cached bytes rather than regenerating: a second request must present the
// same validator, which is what lets the browser keep the tile.
func TestThumb_SameWidthReturnsIdenticalETag(t *testing.T) {
	store, root := newTestStore(t)
	writeTestFrame(t, root, encodeTestJPEG(t, 640, 480))

	first, err := store.Thumb(tDate, tTick, tMAC, DefaultThumbWidth)
	if err != nil {
		t.Fatalf("first Thumb: %v", err)
	}
	firstETag := first.ETag()
	first.Close()

	second, err := store.Thumb(tDate, tTick, tMAC, DefaultThumbWidth)
	if err != nil {
		t.Fatalf("second Thumb: %v", err)
	}
	defer second.Close()

	if second.ETag() != firstETag {
		t.Errorf("ETag changed across a cache hit: %q then %q; the hit path regenerated",
			firstETag, second.ETag())
	}
}

func TestThumb_LeavesNoTempFiles(t *testing.T) {
	store, root := newTestStore(t)
	writeTestFrame(t, root, encodeTestJPEG(t, 640, 480))

	if _, err := store.Thumb(tDate, tTick, tMAC, DefaultThumbWidth); err != nil {
		t.Fatalf("Thumb: %v", err)
	}

	// A failed or interrupted write must not leave debris next to the
	// cache: the day folder is scanned by the day view, and stray files
	// there are indistinguishable from a fault later.
	dayDir := filepath.Join(root, tDate)
	err := filepath.WalkDir(dayDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(p) == ".tmp" {
			t.Errorf("temp file left behind: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking day dir: %v", err)
	}
}

func TestThumb_TruncatedSourceIsRefused(t *testing.T) {
	store, root := newTestStore(t)
	full := encodeTestJPEG(t, 640, 480)
	writeTestFrame(t, root, full[:len(full)-8])

	_, err := store.Thumb(tDate, tTick, tMAC, DefaultThumbWidth)
	if !errors.Is(err, ErrFrameIncomplete) {
		t.Errorf("err: got %v, want ErrFrameIncomplete", err)
	}
}

func TestThumb_MissingSourceIsNotFound(t *testing.T) {
	store, _ := newTestStore(t)

	if _, err := store.Thumb(tDate, tTick, tMAC, DefaultThumbWidth); !errors.Is(err, ErrFrameNotFound) {
		t.Errorf("err: got %v, want ErrFrameNotFound", err)
	}
}

// TestThumb_OversizeSourceIsRefusedBeforeDecode uses a padded file that
// passes the marker guard but exceeds the decode cap. The point is that the
// cap is checked on the size alone, before any decode, so a file with a
// hostile header can never reach the decoder.
func TestThumb_OversizeSourceIsRefusedBeforeDecode(t *testing.T) {
	store, root := newTestStore(t)

	real := encodeTestJPEG(t, 640, 480)
	oversize := make([]byte, maxThumbSourceBytes+1)
	copy(oversize, real)
	// Re-assert the EOI marker at the end so the size guard is what
	// rejects this, not the completeness guard.
	oversize[len(oversize)-2] = jpegMarker
	oversize[len(oversize)-1] = jpegEOI
	writeTestFrame(t, root, oversize)

	if _, err := store.Thumb(tDate, tTick, tMAC, DefaultThumbWidth); !errors.Is(err, ErrSourceTooLarge) {
		t.Errorf("err: got %v, want ErrSourceTooLarge", err)
	}
}

func TestThumb_RejectsUnsupportedWidth(t *testing.T) {
	store, root := newTestStore(t)
	writeTestFrame(t, root, encodeTestJPEG(t, 640, 480))

	for _, w := range []int{0, -1, 1, 321, 1000, 100000} {
		if _, err := store.Thumb(tDate, tTick, tMAC, w); err == nil {
			t.Errorf("width %d: got nil error, want a rejection so the cache cannot fragment", w)
		}
	}
}

func TestThumb_RejectsBadAddress(t *testing.T) {
	store, root := newTestStore(t)
	writeTestFrame(t, root, encodeTestJPEG(t, 640, 480))

	cases := map[string]struct{ date, tick, mac string }{
		"traversal in date": {"../../etc", tTick, tMAC},
		"traversal in tick": {tDate, "../../../etc", tMAC},
		"traversal in mac":  {tDate, tTick, "../../../../etc"},
		"uppercase mac":     {tDate, tTick, "E08CFE3091B0"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Thumb(c.date, c.tick, c.mac, DefaultThumbWidth); err == nil {
				t.Error("got nil error, want the address rejected before any filesystem access")
			}
		})
	}
}

// TestThumb_ConcurrentFirstRequests is the atomic-write contract. A day view
// fires every tile at once, so the first view of a day produces a burst of
// simultaneous misses for the same path. Run under -race, this fails if the
// temp-file-and-rename path is not in fact atomic.
func TestThumb_ConcurrentFirstRequests(t *testing.T) {
	store, root := newTestStore(t)
	writeTestFrame(t, root, encodeTestJPEG(t, 640, 480))

	const n = 8
	var wg sync.WaitGroup
	bodies := make([][]byte, n)
	errs := make([]error, n)

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			frame, err := store.Thumb(tDate, tTick, tMAC, DefaultThumbWidth)
			if err != nil {
				errs[i] = err
				return
			}
			defer frame.Close()
			bodies[i], err = io.ReadAll(frame)
			if err != nil {
				errs[i] = err
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Thumb %d: %v", i, err)
		}
	}
	// The guarantee is identical bytes, not an identical ETag: every writer
	// renames its own temp file, so the last rename wins and callers may
	// legitimately observe different modification times. What must hold is
	// that no caller ever saw a partial or divergent file.
	for i := 1; i < n; i++ {
		if !bytes.Equal(bodies[i], bodies[0]) {
			t.Errorf("caller %d got %d bytes, caller 0 got %d; concurrent generation diverged",
				i, len(bodies[i]), len(bodies[0]))
		}
	}
}

func TestResizeLongestEdge_PreservesAspect(t *testing.T) {
	cases := []struct{ sw, sh, wantW, wantH int }{
		{640, 480, 320, 240},
		{480, 640, 240, 320},
		{1000, 1000, 320, 320},
		{1920, 1080, 320, 180},
	}
	for _, c := range cases {
		w, h := thumbDims(c.sw, c.sh, 320)
		if w != c.wantW || h != c.wantH {
			t.Errorf("thumbDims(%d,%d,320) = %dx%d, want %dx%d",
				c.sw, c.sh, w, h, c.wantW, c.wantH)
		}
	}
}

// TestResizeLongestEdge_SmallerThanWidthIsIdentity documents that a capture
// narrower than the requested thumbnail is returned untouched rather than
// upscaled, because interpolation adds no detail and costs work.
func TestResizeLongestEdge_SmallerThanWidthIsIdentity(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 80))
	got := resizeLongestEdge(src, 320)

	b := got.Bounds()
	if b.Dx() != 100 || b.Dy() != 80 {
		t.Errorf("got %dx%d, want the original 100x80", b.Dx(), b.Dy())
	}
}

func TestWriteFileAtomic_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "thumb.jpg")

	if err := writeFileAtomic(p, []byte("first")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeFileAtomic(p, []byte("second")); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("got %q, want the replacement", got)
	}
}

func TestWriteFileAtomic_PreservesMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "thumb.jpg")
	if err := writeFileAtomic(p, []byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// The cache is only read by the process that wrote it, so it should not
	// be group or world readable.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode: got %o, want 600", perm)
	}
}
