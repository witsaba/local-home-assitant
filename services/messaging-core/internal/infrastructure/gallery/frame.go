package gallery

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Sentinel errors for a failed capture lookup. The handler maps these onto
// distinct HTTP statuses, so they are matched with errors.Is rather than by
// string comparison.
var (
	// ErrFrameNotFound means no capture exists at the validated address.
	// It is distinct from a bad address: the path was well formed, the
	// file simply is not there.
	ErrFrameNotFound = errors.New("no such capture")

	// ErrFrameIncomplete means the file exists but is not a complete JPEG.
	// This is what a capture interrupted mid-write leaves on disk, and it
	// is the one condition in this package that is expected to resolve on
	// its own: the next tick writes a fresh, whole frame.
	ErrFrameIncomplete = errors.New("capture is incomplete")
)

// JPEG structure constants used by the completeness guard.
const (
	jpegMarker = 0xFF // Every JPEG marker begins with this byte.
	jpegSOI    = 0xD8 // Start Of Image: the first two bytes of every JPEG.
	jpegEOI    = 0xD9 // End Of Image: the last two bytes of every complete JPEG.

	// minJPEGBytes is the size floor for a plausible frame. A real capture
	// is tens of kilobytes, so 125 bytes only has to separate a genuine
	// image from an empty or stub file, and it stays far below any real
	// frame so the floor can never reject a good one.
	minJPEGBytes = 125
)

// Frame is an opened, validated capture file.
//
// It streams from the descriptor instead of holding bytes: a day view can
// touch hundreds of frames and a full-resolution capture is tens to hundreds
// of kilobytes, so buffering is what would make the gallery the largest
// memory consumer in the service. Read and Seek are forwarded to the
// descriptor so a *Frame satisfies io.ReadSeeker, which is what lets the
// handler hand it to http.ServeContent and get range requests, conditional
// requests and Content-Type detection for free.
type Frame struct {
	file    *os.File
	path    string
	size    int64
	modTime time.Time
}

// OpenFrame validates the address, opens the capture, and refuses anything
// that is not a complete JPEG.
//
// The completeness check is not defensive decoration. odd/tasks/gallery.md
// records that the camera firmware's Storage.WriteFile can return before the
// frame is fully flushed, which leaves the newest file on a day short. A
// truncated JPEG decodes to a partial image in some viewers and to nothing
// in others, so serving it produces a page that looks broken in a way the
// operator cannot diagnose. Refusing it turns that into an explicit
// "incomplete capture" the page can render as a placeholder.
func (s *Store) OpenFrame(date, tick, mac string) (*Frame, error) {
	p, err := s.framePath(date, tick, mac)
	if err != nil {
		return nil, err
	}
	return openFrameAt(p)
}

// openFrameAt opens one already-resolved path under the capture root and
// applies the completeness guard.
//
// It is separate from OpenFrame so the thumbnail cache, whose paths are
// generated rather than derived from a client address, go through exactly
// the same open-and-validate path. One guard with two callers cannot drift
// into two different definitions of "a usable image".
func openFrameAt(p string) (*Frame, error) {
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrFrameNotFound
		}
		return nil, fmt.Errorf("opening capture %q: %w", p, err)
	}

	// Stat through the descriptor rather than by path: it cannot race with
	// a rename, so the size and mtime that become the ETag describe the
	// exact bytes this handle will serve.
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stating capture %q: %w", p, err)
	}

	fr := &Frame{file: f, path: p, size: info.Size(), modTime: info.ModTime()}
	if err := fr.checkComplete(); err != nil {
		f.Close()
		return nil, err
	}
	return fr, nil
}

// checkComplete verifies that the frame carries both JPEG boundary markers.
//
// The End Of Image test is deliberately strict: it requires the final two
// bytes to be the marker. A JPEG with trailing padding or a concatenated
// Exif thumbnail could in principle fail it, and that failure mode is a
// valid image shown as a placeholder, which is visible and harmless. The
// looser alternative -- scanning a window for the marker -- would let a file
// truncated in its last few hundred bytes pass, which is the exact case worth
// catching.
func (f *Frame) checkComplete() error {
	if f.size < minJPEGBytes {
		return fmt.Errorf("%w: %d bytes, under the %d-byte floor",
			ErrFrameIncomplete, f.size, minJPEGBytes)
	}

	// ReadAt rather than Read: it takes an explicit offset, so it leaves
	// the descriptor position at zero for the caller's streaming read and
	// is safe against a concurrent seek on the same handle.
	var head, tail [2]byte
	if _, err := f.file.ReadAt(head[:], 0); err != nil {
		return fmt.Errorf("%w: unreadable start of image", ErrFrameIncomplete)
	}
	if head[0] != jpegMarker || head[1] != jpegSOI {
		return fmt.Errorf("%w: missing JPEG start-of-image marker", ErrFrameIncomplete)
	}
	if _, err := f.file.ReadAt(tail[:], f.size-2); err != nil {
		return fmt.Errorf("%w: unreadable end of image", ErrFrameIncomplete)
	}
	if tail[0] != jpegMarker || tail[1] != jpegEOI {
		return fmt.Errorf("%w: missing JPEG end-of-image marker", ErrFrameIncomplete)
	}
	return nil
}

// ETag is the weak validator for this frame.
//
// Size and modification time uniquely identify the bytes for a capture file,
// which is written once and never modified: a later mtime with the same size
// is a different frame at the same address, not a changed one, and the weak
// prefix correctly declines to promise byte equality. The handler sets this
// before calling http.ServeContent, which then uses it to answer
// If-None-Match without any hand-written comparison.
func (f *Frame) ETag() string {
	return fmt.Sprintf(`W/"%d-%d"`, f.size, f.modTime.UnixNano())
}

// Path is the on-disk location of the frame. It is used only to derive the
// filename http.ServeContent infers Content-Type from.
func (f *Frame) Path() string { return f.path }

// Size is the frame length in bytes.
func (f *Frame) Size() int64 { return f.size }

// ModTime is the frame's modification time, used for Last-Modified.
func (f *Frame) ModTime() time.Time { return f.modTime }

// Name is the filename with extension, which is what ServeContent needs to
// choose image/jpeg without sniffing content.
func (f *Frame) Name() string { return filepath.Base(f.path) }

// Read forwards to the descriptor.
func (f *Frame) Read(p []byte) (int, error) { return f.file.Read(p) }

// Seek forwards to the descriptor, making *Frame an io.ReadSeeker.
func (f *Frame) Seek(offset int64, whence int) (int64, error) {
	return f.file.Seek(offset, whence)
}

// Close releases the descriptor. It is safe to call more than once.
func (f *Frame) Close() error { return f.file.Close() }

// Compile-time proof that a Frame can be streamed by net/http.
var _ io.ReadSeeker = (*Frame)(nil)
