// storage.go — filesystem persistence for the surveillance job.
//
// Layout (under Root):
//
//	<Root>/
//	  2026-09-30/
//	    00-15-00_aabbccddeeff.jpg
//	    00-15-00_112233445566.jpg
//	    00-30-00_aabbccddeeff.jpg
//	    ...
//	  2026-10-01/
//	    ...
//
// One folder per day (the day boundary is the worker's local
// time, NOT UTC — a tick at 23:59 lands in today/, the next tick
// at 00:14 lands in tomorrow/). One file per (device, tick) —
// the filename is "<HH-MM-SS>_<mac>.jpg".
//
// Idempotency: the directory is created with os.MkdirAll (so
// restarting the worker does not error on existing dirs), and
// the file write goes through writeFileAtomic, which writes a
// temp file and renames it over the destination so a concurrent
// reader never observes a half-written image. Two ticks firing
// within the same second for the same device would overwrite
// each other; in practice the 15-minute interval prevents that,
// and a future caller can guard against it by including the
// second or a counter.
package surveillance

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Storage is the filesystem layer. The worker holds one Storage
// value configured with the root directory; tests can swap in a
// tmpfs-backed Root. The methods are safe to call concurrently
// because the underlying filesystem ops are atomic at the
// per-file granularity that this package uses.
type Storage struct {
	// Root is the absolute path under which day subdirectories
	// are created. The constructor (NewStorage) does NOT
	// create the root itself — that's the operator's job (the
	// install script creates ~/.witsaba/cameras/ on first
	// boot). This means a misconfigured Root will surface as a
	// permission error on the first write, not silently create
	// a typo'd parent.
	Root string
}

// NewStorage returns a Storage rooted at the given absolute path.
// The path is NOT validated — operations fail at first use if
// the path is not writable.
func NewStorage(root string) Storage {
	return Storage{Root: root}
}

// dayDir returns "<Root>/<YYYY-MM-DD>" for the given local time.
// The format matches what the user expects to see when they
// `ls` the directory; we do not zero-pad hours because hours
// never appear in this path component.
func (s Storage) dayDir(t time.Time) string {
	return filepath.Join(s.Root, t.Format("2006-01-02"))
}

// PathFor returns the absolute path where the JPEG for the given
// device/tick should be written. The returned path is purely
// computed — neither the parent directory nor the file is
// created. Use EnsureDayDir first.
//
// MAC normalization: the input is lowercased and stripped of
// colons/spaces/dashes, so "AA:BB:CC:DD:EE:FF", "aabbccddeeff",
// and "AA-BB-CC-DD-EE-FF" all collapse to "aabbccddeeff". The
// witsaba /whoami endpoint already returns 12-char lowercase
// hex with no separators, so this normalization is purely a
// safety net for future code paths that might pass through
// differently-formatted MAC strings.
func (s Storage) PathFor(t time.Time, mac string) string {
	normalized := normalizeMAC(mac)
	return filepath.Join(
		s.Root,
		t.Format("2006-01-02"),
		fmt.Sprintf("%s_%s.jpg", t.Format("15-04-05"), normalized),
	)
}

// EnsureDayDir creates the day directory for the given local
// time if it does not already exist. Idempotent.
//
// Returns the absolute path of the day directory on success.
// Does NOT create the file — callers do that with WriteFile.
func (s Storage) EnsureDayDir(t time.Time) (string, error) {
	if strings.TrimSpace(s.Root) == "" {
		return "", errors.New("EnsureDayDir: empty root")
	}
	dir := s.dayDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("EnsureDayDir: mkdir %s: %w", dir, err)
	}
	return dir, nil
}

// WriteFile persists the JPEG body at the given absolute path.
// Mode is 0644 — readable by the witsaba admin user, writable
// only by the owner (the worker process). The directory is
// created if missing so a caller that builds a path from
// PathFor does not have to call EnsureDayDir separately.
//
// Returns the absolute path written on success.
func (s Storage) WriteFile(path string, body []byte) (string, error) {
	if path == "" {
		return "", errors.New("WriteFile: empty path")
	}
	if body == nil {
		return "", errors.New("WriteFile: nil body")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("WriteFile: mkdir %s: %w", dir, err)
	}
	if err := writeFileAtomic(path, body, 0o644); err != nil {
		return "", fmt.Errorf("WriteFile: write %s: %w", path, err)
	}
	return path, nil
}

// writeFileAtomic writes body to path so that a concurrent reader ever
// only ever sees either the complete previous file or the complete new
// one, never a partially written image.
//
// The sequence: write to a temp file in the SAME directory (so the
// rename stays inside one filesystem, which is what makes it atomic),
// fsync the file so its bytes reach the disk, close it, rename it over
// the destination, then fsync the directory so the rename is durable.
//
// os.WriteFile cannot be used here. It opens the destination with
// O_TRUNC and writes in place, so a reader watching that file sees it
// drop to zero length and then grow back to full. The gallery serves
// these images straight from disk, so that window would hand a browser
// a truncated JPEG. This replaces an earlier comment here that claimed
// os.WriteFile was atomic; it is not, and the stdlib documents no such
// guarantee.
func writeFileAtomic(path string, body []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()

	// From here on, every failure path must not leave the temp file
	// behind: a leftover would be picked up by the gallery listing.
	discard := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		discard()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		discard()
		return fmt.Errorf("sync temp: %w", err)
	}
	// Close before the rename so the destination can never become
	// visible without its data already flushed.
	if err := tmp.Close(); err != nil {
		discard()
		return fmt.Errorf("close temp: %w", err)
	}
	// CreateTemp always makes 0600; the archive contract is 0644.
	if err := os.Chmod(tmpName, perm); err != nil {
		discard()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		discard()
		return fmt.Errorf("rename: %w", err)
	}

	// Best-effort durability for the rename itself. A failure here
	// leaves the file contents correct, so it is not fatal.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// normalizeMAC strips common separators and lowercases. Returns
// the input unchanged if it already looks normalized.
func normalizeMAC(mac string) string {
	mac = strings.ToLower(strings.TrimSpace(mac))
	r := strings.NewReplacer(":", "", "-", "", " ", "", ".", "")
	return r.Replace(mac)
}

// --- read side ---------------------------------------------------------
//
// Everything above is the write path. Everything below is what the
// gallery reads: which days exist, and which photos each day holds. The
// filesystem stays the index — no manifest, no database row, nothing to
// migrate and nothing to corrupt.

// Photo is one captured image found on disk.
//
// Name is the on-disk filename. It is also what the gallery passes back
// when it wants the image served or removed, so a file is always
// addressed by exactly the string that was read out of the directory.
// MAC and Time are parsed out of that same name rather than recomputed,
// so they cannot disagree with what is on disk.
type Photo struct {
	Name  string
	MAC   string
	Time  time.Time
	Bytes int64
}

const (
	dayLayout         = "2006-01-02"
	captureTimeLayout = "15-04-05"
)

// captureNameRE matches the exact shape of a capture filename:
// "HH-MM-SS_<12 lowercase hex>.jpg", where the hex is the MAC written by
// PathFor.
//
// This is the first line of defence for the HTTP layer, where the day
// and the name arrive from a request. Anything that does not match is
// not a capture: it must never be listed, served, or deleted. It is also
// what keeps the ".<name>.tmp-*" file that writeFileAtomic creates
// mid-write out of the gallery.
var captureNameRE = regexp.MustCompile(`^([0-9]{2}-[0-9]{2}-[0-9]{2})_([0-9a-f]{12})\.jpg$`)

// dayRE matches a capture day directory, "YYYY-MM-DD".
var dayRE = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// ValidCaptureDay reports whether name is a capture day directory. The
// HTTP layer uses this to reject a date parameter before it is ever
// joined onto the capture root.
func ValidCaptureDay(name string) bool { return dayRE.MatchString(name) }

// ValidCaptureName reports whether name is a capture filename. The HTTP
// layer uses this to reject a name parameter before it is ever joined
// onto the capture root — this is what stops a traversal payload or a
// crafted name from ever reaching the filesystem.
func ValidCaptureName(name string) bool { return captureNameRE.MatchString(name) }

// ListDays returns the capture days that still hold at least one photo,
// oldest first.
//
// A root that does not exist yet — an archive that has never run, or a
// directory that was moved — is reported as empty rather than as an
// error, because the gallery needs a truthful empty calendar rather than
// a failure page. A day directory left behind after its last photo was
// deleted is skipped, so the calendar never grows a permanent blank
// cell.
//
// ISO dates sort lexicographically, which is also chronological order.
func (s Storage) ListDays() ([]string, error) {
	days := []string{}

	if strings.TrimSpace(s.Root) == "" {
		return nil, errors.New("ListDays: empty root")
	}

	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return days, nil
		}
		return nil, fmt.Errorf("ListDays: read %s: %w", s.Root, err)
	}

	for _, e := range entries {
		if !e.IsDir() || !ValidCaptureDay(e.Name()) {
			continue
		}
		// One unreadable day must not empty the whole calendar.
		photos, err := s.ListPhotos(mustParseDay(e.Name()))
		if err != nil {
			continue
		}
		if len(photos) > 0 {
			days = append(days, e.Name())
		}
	}

	sort.Strings(days)
	return days, nil
}

// ListPhotos returns the photos captured on the given local day, newest
// first — the order the operator expects when they open a day.
//
// Only filenames matching the capture pattern are returned. A temporary
// file from an in-flight atomic write, a backup, and anything else that
// is not a capture are skipped rather than reported as errors.
//
// A day with no directory is an empty day, not an error: navigating to
// a date that was never captured should show "no photos captured on
// this day", not a failure.
func (s Storage) ListPhotos(day time.Time) ([]Photo, error) {
	photos := []Photo{}

	if strings.TrimSpace(s.Root) == "" {
		return nil, errors.New("ListPhotos: empty root")
	}

	dir := s.dayDir(day)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return photos, nil
		}
		return nil, fmt.Errorf("ListPhotos: read %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := captureNameRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}

		// A file can vanish between the directory read and the Info
		// call — the gallery deleting it, or retention pruning it.
		// Skip it rather than failing the whole day.
		info, err := e.Info()
		if err != nil {
			continue
		}

		when, err := time.ParseInLocation(
			dayLayout+" "+captureTimeLayout,
			day.Format(dayLayout)+" "+m[1],
			time.Local,
		)
		if err != nil {
			continue
		}

		photos = append(photos, Photo{
			Name:  e.Name(),
			MAC:   m[2],
			Time:  when,
			Bytes: info.Size(),
		})
	}

	sort.Slice(photos, func(i, j int) bool {
		return photos[i].Time.After(photos[j].Time)
	})
	return photos, nil
}

// mustParseDay turns a "YYYY-MM-DD" directory name that has already
// matched dayRE into a time.Time. If it somehow fails, the zero time
// simply matches no real photo.
func mustParseDay(name string) time.Time {
	t, err := time.ParseInLocation(dayLayout, name, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ErrDayNotFound is returned by RemoveDay when the requested day folder
// does not exist. It is distinct from a permission error so the handler
// can return a 404 rather than a 500.
var ErrDayNotFound = errors.New("day folder not found")

// RemoveDay deletes the entire capture folder for one day, including
// any in-flight atomic-write temp files.
//
// The day is converted to its on-disk path through dayDir, which uses
// the same date layout as ListDays and WriteFile, so the result is
// always inside the capture root. Lstat (not Stat) is what stops a
// symlink at the day path from being followed: a symlinked day is
// reported as such and refused before any removal is attempted.
//
// EvalSymlinks on both sides is the belt-and-braces containment check:
// the validated day path should already be under root, but a symlink
// planted at the day path (caught by Lstat above) might still resolve
// to somewhere outside, and the comparison must use the same kind of
// resolved path on both sides.
//
// The thumbnail cache lives as a sibling "thumbs" directory of the
// capture root, not inside the day folder, so it is NOT removed by
// RemoveDay; thumbnails for deleted days become orphans that the next
// access simply fails to regenerate. That is acceptable: a deleted day
// has no photos to thumb, and the cache is bounded by the archive.
//
// Returns ErrDayNotFound when the day does not exist, so the handler
// can answer 404 rather than 500.
func (s Storage) RemoveDay(day time.Time) error {
	if strings.TrimSpace(s.Root) == "" {
		return errors.New("RemoveDay: empty root")
	}
	dir := s.dayDir(day)

	// Lstat so a symlink is reported instead of followed. A day path
	// that resolves to a file or to a symlink pointing somewhere else
	// must not be removed.
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrDayNotFound
		}
		return fmt.Errorf("RemoveDay: stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("RemoveDay: %s is not a directory", dir)
	}

	// Belt-and-braces containment check.
	realRoot, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		return fmt.Errorf("RemoveDay: resolve root: %w", err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("RemoveDay: resolve %s: %w", dir, err)
	}
	if !strings.HasPrefix(real, realRoot+string(os.PathSeparator)) {
		return fmt.Errorf("RemoveDay: %s escapes the capture root", real)
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("RemoveDay: remove %s: %w", dir, err)
	}
	return nil
}
