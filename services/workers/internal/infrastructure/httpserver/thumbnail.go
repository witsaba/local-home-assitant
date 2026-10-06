// thumbnail.go — on-disk thumbnail cache for the gallery day grid.
//
// Why a cache at all: a day holds up to 96 photos per camera at
// 800x600. Opening a day and shipping originals means the browser
// downloads megabytes before anything appears. A thumbnail is roughly
// an eighth of the pixels, and once generated it is reused forever.
//
// Where it lives: a "thumbs" directory SIBLING of the capture root, so
// for the default ~/.witsaba/cameras it is ~/.witsaba/thumbs. That is
// not arbitrary — the systemd unit sets ProtectHome=read-only with
// ReadWritePaths=$HOME/.witsaba, so a cache anywhere else under $HOME
// would be denied at write time. Deriving it from the archive root
// keeps it inside the writable subtree by construction.
//
// Only the standard library is used. image/jpeg decodes, and the
// downscale below is a plain box filter, so this adds no dependency to
// a service that has no web framework either.
package httpserver

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/witsaba/local-home-assitant/services/workers/internal/jobs/surveillance"
)

// thumbnailWidth is the cached thumbnail width in pixels. The capture
// is 800x600, so this is a 2.5x reduction. Height follows the source
// aspect ratio.
const thumbnailWidth = 320

// thumbQuality is the JPEG quality of the cached thumbnail. Lower than
// the capture on purpose: this image is shown at 320px wide, and a
// smaller file is the entire point of having a thumbnail.
const thumbQuality = 82

// thumbnailer generates and caches thumbnails under a fixed root.
//
// It is safe for concurrent use. Two goroutines racing to generate the
// same thumbnail both write complete files to distinct temp names and
// rename into place, so the last rename wins and no reader ever sees a
// partial file. That is the same guarantee W1 gave the capture writer.
type thumbnailer struct {
	root  string
	width int
}

// newThumbnailer returns a thumbnailer caching under root.
func newThumbnailer(root string, width int) *thumbnailer {
	return &thumbnailer{root: root, width: width}
}

// defaultThumbRoot derives the cache root as a sibling "thumbs"
// directory of the capture archive. See the package comment for why it
// is derived rather than configured separately.
func defaultThumbRoot(archiveRoot string) string {
	return filepath.Join(filepath.Dir(archiveRoot), "thumbs")
}

// pathFor returns the cache path for a photo.
//
// The cache is a SECOND filesystem surface, so it is gated with the same
// component validation as the archive. A name that would escape the
// archive cannot steer a write out of the cache root either.
func (t *thumbnailer) pathFor(day, name string) (string, error) {
	if !surveillance.ValidCaptureDay(day) {
		return "", fmt.Errorf("%w: %q", errBadDay, day)
	}
	if !surveillance.ValidCaptureName(name) {
		return "", fmt.Errorf("%w: %q", errBadName, name)
	}
	return filepath.Join(t.root, day, name), nil
}

// get returns the cache path for a photo, generating the thumbnail from
// srcPath first if it is not already cached.
//
// The returned path is the cache entry; callers serve that file.
func (t *thumbnailer) get(day, name, srcPath string) (string, error) {
	cachePath, err := t.pathFor(day, name)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(cachePath); err == nil {
		return cachePath, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return "", fmt.Errorf("thumb mkdir: %w", err)
	}

	f, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("thumb open source: %w", err)
	}
	src, _, err := image.Decode(f)
	_ = f.Close()
	if err != nil {
		// A corrupt capture must not leave a cache entry behind: the
		// next request would serve that broken file forever.
		return "", fmt.Errorf("thumb decode: %w", err)
	}

	if err := writeJPEGAtomic(cachePath, downscale(src, t.width)); err != nil {
		return "", err
	}
	return cachePath, nil
}

// drop removes a cached thumbnail. Removing an absent entry is not an
// error: deleting a photo whose thumbnail was never generated must
// still succeed.
func (t *thumbnailer) drop(day, name string) error {
	cachePath, err := t.pathFor(day, name)
	if err != nil {
		return err
	}
	if err := os.Remove(cachePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// downscale box-filters src down to the given width, preserving aspect.
// Integer arithmetic throughout — no float rounding drift on the box
// boundaries, which matters because a rounding error here shows up as a
// one-pixel seam down the thumbnail.
func downscale(src image.Image, width int) image.Image {
	b := src.Bounds()
	srcW, srcH := b.Dx(), b.Dy()

	// Never upscale. An already-small capture is returned untouched.
	if srcW <= width || width < 1 {
		return src
	}
	dstH := int(int64(srcH) * int64(width) / int64(srcW))
	if dstH < 1 {
		dstH = 1
	}

	// Normalize to RGBA at the origin so the inner loop indexes bytes
	// directly instead of paying an interface call per sample. For an
	// 800x600 source that is ~480k calls saved per generation.
	rgba, ok := src.(*image.RGBA)
	if !ok || !rgba.Rect.Min.Eq(image.Point{}) {
		rgba = image.NewRGBA(image.Rect(0, 0, srcW, srcH))
		draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, dstH))
	for y := 0; y < dstH; y++ {
		y0 := int(int64(y) * int64(srcH) / int64(dstH))
		y1 := int(int64(y+1) * int64(srcH) / int64(dstH))
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < width; x++ {
			x0 := int(int64(x) * int64(srcW) / int64(width))
			x1 := int(int64(x+1) * int64(srcW) / int64(width))
			if x1 <= x0 {
				x1 = x0 + 1
			}

			var r, g, bl, a, n uint32
			for sy := y0; sy < y1; sy++ {
				base := rgba.PixOffset(x0, sy)
				for o := base; o < base+(x1-x0)*4; o += 4 {
					r += uint32(rgba.Pix[o])
					g += uint32(rgba.Pix[o+1])
					bl += uint32(rgba.Pix[o+2])
					a += uint32(rgba.Pix[o+3])
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o] = uint8(r / n)
			dst.Pix[o+1] = uint8(g / n)
			dst.Pix[o+2] = uint8(bl / n)
			dst.Pix[o+3] = uint8(a / n)
		}
	}
	return dst
}

// writeJPEGAtomic encodes img to path via a temp file in the same
// directory and renames it into place, so a concurrent reader never
// observes a partial thumbnail. Mirrors the capture writer from W1.
func writeJPEGAtomic(path string, img image.Image) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("thumb create temp: %w", err)
	}
	tmpName := tmp.Name()
	discard := func() { _ = os.Remove(tmpName) }

	if err := jpeg.Encode(tmp, img, &jpeg.Options{Quality: thumbQuality}); err != nil {
		_ = tmp.Close()
		discard()
		return fmt.Errorf("thumb encode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		discard()
		return fmt.Errorf("thumb sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		discard()
		return fmt.Errorf("thumb close: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		discard()
		return fmt.Errorf("thumb chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		discard()
		return fmt.Errorf("thumb rename: %w", err)
	}
	return nil
}
