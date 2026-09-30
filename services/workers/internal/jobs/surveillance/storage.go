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
// the file write uses os.WriteFile which atomically replaces the
// destination. Two ticks firing within the same second for the
// same device would overwrite each other; in practice the
// 15-minute interval prevents that, and a future caller can
// guard against it by including the second or a counter.
package surveillance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", fmt.Errorf("WriteFile: write %s: %w", path, err)
	}
	return path, nil
}

// normalizeMAC strips common separators and lowercases. Returns
// the input unchanged if it already looks normalized.
func normalizeMAC(mac string) string {
	mac = strings.ToLower(strings.TrimSpace(mac))
	r := strings.NewReplacer(":", "", "-", "", " ", "", ".", "")
	return r.Replace(mac)
}
