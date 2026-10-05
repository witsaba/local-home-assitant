package gallery

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
)

// Thumbnail generation constants.
const (
	// DefaultThumbWidth is the canonical thumbnail longest edge. The page
	// asks for exactly this, and the cache is written at exactly this
	// size, so a day view generates one thumbnail per capture and no more.
	DefaultThumbWidth = 320

	// thumbQuality is the JPEG quality used for cached thumbnails. 70 is
	// the usual balance for a UI-sized image: at these dimensions the
	// difference between 70 and 90 is not visible in a grid, and the size
	// difference is roughly a third. The untouched full frame stays
	// available for anyone who needs the original.
	thumbQuality = 70

	// maxThumbSourceBytes caps what the thumbnailer will decode.
	//
	// image.Decode trusts the dimensions in the JPEG header, so a corrupt
	// or hostile file can claim an enormous canvas and allocate far more
	// than its own length. A real ESP32 capture at default quality is tens
	// of kilobytes, so a 2 MB ceiling is many times larger than any
	// legitimate frame while still bounding the damage. Anything above it
	// is refused rather than resized.
	maxThumbSourceBytes = 2 << 20

	// thumbDecodeSlots bounds how many captures are decoded at once.
	//
	// JPEG decode is CPU- and memory-hungry, and a page opening a day view
	// asks for every thumbnail on the day at once. Unbounded, a 288-shot
	// day would queue 288 decodes onto a Pi's four cores and the first
	// response would stall behind the rest. Two keeps the cores busy
	// without letting one page view starve the rest of the service.
	thumbDecodeSlots = 2
)

// allowedThumbWidths is the set of thumbnail sizes the server will produce.
//
// Restricting this to a small allowlist, rather than honouring any width the
// caller asks for, is what keeps the cache bounded: an arbitrary width would
// let a caller write unbounded variants of every capture to disk. A width
// outside the set is a client error rather than something to clamp, because a
// clamped width the caller cannot detect yields an image at the wrong size
// behind a 200 response.
var allowedThumbWidths = []int{160, 320, 640}

// IsAllowedThumbWidth reports whether the server will produce this width.
func IsAllowedThumbWidth(w int) bool {
	for _, allowed := range allowedThumbWidths {
		if w == allowed {
			return true
		}
	}
	return false
}

// AllowedThumbWidths returns the supported widths, for error messages and for
// the page to validate against before building a URL.
func AllowedThumbWidths() []int {
	out := make([]int, len(allowedThumbWidths))
	copy(out, allowedThumbWidths)
	return out
}

// ErrSourceTooLarge means a capture is above the decode cap.
var ErrSourceTooLarge = errors.New("capture is too large to thumbnail")

// thumbPath builds the on-disk path of one cached thumbnail.
//
// The width is a path component, not just a query parameter. A shared path
// would mean two sizes racing for a single file, and the loser would serve an
// image at the wrong dimensions behind a 200 response with no way for the
// caller to detect it. Keying on width caches each size independently, so the
// ETag always describes bytes the caller actually asked for.
func (s *Store) thumbPath(date, tick, mac string, width int) (string, error) {
	if err := validateAll(date, tick, mac); err != nil {
		return "", err
	}
	if !IsAllowedThumbWidth(width) {
		return "", fmt.Errorf("thumb width %d is not supported", width)
	}
	p := filepath.Join(s.root, date, thumbDirName, fmt.Sprint(width), tick+"_"+mac+tickSuffix)
	if !withinRoot(s.root, p) {
		return "", fmt.Errorf("resolved thumbnail path escapes the capture root")
	}
	return p, nil
}

// Thumb returns the cached thumbnail for one capture at one width, generating
// it on a miss.
//
// The returned Frame is a thumbnail rather than a capture, but it is opened
// through the same validated-open path so the handler serves both kinds
// identically.
//
// Concurrency: two simultaneous requests for the same uncached thumbnail both
// generate and both write. That is deliberate and safe. Each writes to its own
// temporary file and os.Rename is atomic, so a reader never observes a partial
// file, and the encoder is deterministic so every writer produces identical
// bytes. What differs between concurrent callers is the modification time,
// because the last rename wins, and with it the ETag: same pixels, different
// validator. That is harmless, since a cached thumbnail is served immutable and
// callers do not revalidate. A per-key lock would add shared state and a new
// failure mode -- a holder dying mid-generate -- to avoid work that is already
// cheap.
func (s *Store) Thumb(date, tick, mac string, width int) (*Frame, error) {
	path, err := s.thumbPath(date, tick, mac, width)
	if err != nil {
		return nil, err
	}

	// A hit costs one open and no decode. An error other than "absent" is
	// reported rather than retried, because regenerating over a permission
	// or I/O fault fails the same way and hides the real cause.
	if frame, err := openFrameAt(path); err == nil {
		return frame, nil
	} else if !errors.Is(err, ErrFrameNotFound) {
		return nil, err
	}

	if err := s.generateThumb(date, tick, mac, width, path); err != nil {
		return nil, err
	}
	return openFrameAt(path)
}

// generateThumb decodes a capture, scales it, and writes it to path atomically.
func (s *Store) generateThumb(date, tick, mac string, width int, path string) error {
	// The source goes through OpenFrame, so the completeness guard runs
	// first: a truncated capture gets no thumbnail, and a page never shows a
	// grid of plausible tiles for a moment that was never fully written.
	src, err := s.OpenFrame(date, tick, mac)
	if err != nil {
		return err
	}
	defer src.Close()

	if src.Size() > maxThumbSourceBytes {
		return fmt.Errorf("%w: %d bytes, over the %d-byte cap",
			ErrSourceTooLarge, src.Size(), maxThumbSourceBytes)
	}

	// Bound decode concurrency. The slot is taken before reading so queued
	// day-view requests do not all hold descriptors and buffers while they
	// wait.
	s.thumbSlots <- struct{}{}
	defer func() { <-s.thumbSlots }()

	decoded, err := decodeJPEG(src)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, resizeLongestEdge(decoded, width), &jpeg.Options{Quality: thumbQuality}); err != nil {
		return fmt.Errorf("encoding thumbnail: %w", err)
	}

	return writeFileAtomic(path, buf.Bytes())
}

// decodeJPEG reads and decodes a frame.
//
// Decoding through the already-validated handle rather than reopening by path
// keeps the size and mtime OpenFrame checked tied to the exact bytes decoded,
// so a concurrent retention prune cannot swap the file between the two steps.
func decodeJPEG(f *Frame) (image.Image, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("rewinding capture %q: %w", f.Path(), err)
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decoding capture %q: %w", f.Path(), err)
	}
	return img, nil
}

// resizeLongestEdge scales img so its longest edge is exactly width, keeping
// the aspect ratio.
//
// Scaling up is deliberately not attempted: when a capture is already narrower
// than the requested thumbnail, returning the original pixels is both cheaper
// and higher quality than an interpolated enlargement. An identity result is
// not a failure to refuse, it is the correct output.
func resizeLongestEdge(img image.Image, width int) image.Image {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return img
	}

	// Never scale up. A capture already narrower than the requested
	// thumbnail is returned untouched: interpolating adds no detail, costs
	// CPU and memory, and produces a file several times larger than the
	// original for no visible gain. An identity result here is the correct
	// output, not a failure to serve.
	if max(sw, sh) <= width {
		return img
	}

	tw, th := thumbDims(sw, sh, width)
	return resizeBox(img, tw, th)
}

// thumbDims computes the scaled size that puts width on the longest edge.
func thumbDims(sw, sh, width int) (int, int) {
	if sw >= sh {
		return width, max(1, sh*width/sw)
	}
	return max(1, sw*width/sh), width
}

// resizeBox scales src to exactly w x h by averaging every source pixel that
// falls inside each destination pixel's footprint.
//
// A box filter rather than nearest-neighbour, because this downscales a
// photographic image to grid size: nearest-neighbour drops three quarters of
// the source pixels and the result shimmers and aliases badly, which is
// glaring in a grid of small tiles. Averaging every covered pixel is one pass
// and gives the clean, slightly soft result the size is meant to have.
func resizeBox(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	for dy := 0; dy < h; dy++ {
		y0 := b.Min.Y + dy*sh/h
		y1 := b.Min.Y + (dy+1)*sh/h
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < w; dx++ {
			x0 := b.Min.X + dx*sw/w
			x1 := b.Min.X + (dx+1)*sw/w
			if x1 <= x0 {
				x1 = x0 + 1
			}

			var rs, gs, bs, as, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := src.At(sx, sy).RGBA()
					rs += uint64(cr)
					gs += uint64(cg)
					bs += uint64(cb)
					as += uint64(ca)
					n++
				}
			}
			if n == 0 {
				continue
			}

			i := dst.PixOffset(dx, dy)
			dst.Pix[i+0] = uint8(rs / n >> 8)
			dst.Pix[i+1] = uint8(gs / n >> 8)
			dst.Pix[i+2] = uint8(bs / n >> 8)
			dst.Pix[i+3] = uint8(as / n >> 8)
		}
	}
	return dst
}

// writeFileAtomic writes data to path via a temporary file and a rename.
//
// The rename is what makes this safe for the reader. A partially written
// thumbnail renamed into place is a corrupt file that looks complete, and the
// browser would cache it for as long as its Cache-Control allows. Writing to
// "<name>.<rand>.tmp" in the same directory and renaming means a reader sees
// either the previous file or the complete new one, never a half-written one.
// The temp file must share the destination directory because rename is only
// atomic within a single filesystem.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating thumbnail dir %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing %q: %w", tmpName, err)
	}
	// CreateTemp makes the file 0600. The cache is only ever read by the
	// process that wrote it, so 0600 is the tighter correct choice, set
	// explicitly rather than left to a umask guess.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("setting mode on %q: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing %q: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming into %q: %w", path, err)
	}
	return nil
}
