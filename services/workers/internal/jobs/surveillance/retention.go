package surveillance

// retention.go — age-based pruning of the capture archive.
//
// The surveillance job is append-only: it writes ~288 frames a day for three
// cameras, and nothing has ever removed them. At the 15 minute default that is
// roughly 6.7 MB a day, or about 2.45 GB a year, on a device with 1 GB of RAM
// and a microSD card that is also taking the database. Retention is what turns
// the archive from an unbounded leak into a bounded window.
//
// # Why whole day directories
//
// Pruning removes an entire day directory or nothing at all. There is no
// partial day. A day folder is the unit the gallery reads, so deleting half of
// one would leave a day that looks selectable in the date picker and then
// renders as a half-empty, confusing grid.
//
// # Why the date filter is the safety property
//
// Only entries whose names parse as YYYY-MM-DD are ever considered, and only
// then is anything removed. That filter is the primary guard against a
// misconfigured root taking the filesystem with it: pointed at /, this code
// looks for directories named like dates and finds none, because those do not
// exist. The Lstat and root checks below are defence in depth on top of a
// property that already holds.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// dayLayout is the capture day directory format. It must stay in sync with
// Storage.dayDir.
const dayLayout = "2006-01-02"

// PruneResult reports what a prune removed. It is logged rather than returned
// to a caller because the job's own response to a prune is fixed: log it and
// carry on capturing.
type PruneResult struct {
	// Days is the number of day directories removed.
	Days int
	// Bytes is the size reclaimed. It is a lower bound: a file that
	// vanished between listing and stat is counted as zero.
	Bytes int64
	// Kept is the number of day directories left in place, which is
	// what makes a misconfigured retention window visible in the log
	// rather than only visible as missing history.
	Kept int
}

// PruneOlderThan removes day directories older than keepDays, relative to now.
//
// keepDays <= 0 disables pruning entirely and is a valid, supported setting:
// an operator who wants an unbounded archive sets 0 and gets none of this
// running. Making the disable a non-positive number rather than a separate
// boolean keeps the one config knob unambiguous.
//
// A failure to remove one day is logged by the caller and does not abort the
// sweep. A single unremovable directory -- a root-owned file from an earlier
// sudo, a mount point -- must not prevent every other day from being cleaned.
func (s Storage) PruneOlderThan(now time.Time, keepDays int) (PruneResult, error) {
	var res PruneResult

	if keepDays <= 0 {
		return res, nil
	}

	// Reject a root that is not a usable directory before reading
	// anything. Cheap, and it turns a misconfiguration into one clear
	// error instead of a confusing walk.
	root := filepath.Clean(s.Root)
	if root == "/" || root == "." {
		return res, fmt.Errorf("refusing to prune root %q", s.Root)
	}
	info, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			// Nothing has ever been captured. Not an error.
			return res, nil
		}
		return res, fmt.Errorf("stating capture root %q: %w", s.Root, err)
	}
	if !info.IsDir() {
		return res, fmt.Errorf("capture root %q is not a directory", s.Root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return res, fmt.Errorf("reading capture root %q: %w", s.Root, err)
	}

	// Dates are zero-padded and lexicographically sortable, so comparing
	// folder names compares calendar dates. The cutoff is computed with
	// AddDate rather than a fixed duration in hours so a day boundary is
	// never skipped or repeated by a DST shift.
	cutoff := now.AddDate(0, 0, -keepDays).Format(dayLayout)

	for _, e := range entries {
		name := e.Name()

		// The .thumbs cache and any operator-created directory fail
		// this test and are left alone.
		// Parse rather than match a regexp: a name like 2026-9-30
		// is not a real folder the writer ever produces, and parsing
		// rejects it.
		if _, err := time.Parse(dayLayout, name); err != nil {
			continue
		}

		path := filepath.Join(root, name)

		// Keep anything on or after the cutoff. Comparison is on the
		// validated name, so this cannot be skewed by a weird path.
		if name >= cutoff {
			res.Kept++
			continue
		}

		// Lstat, not Stat: a symlink to a directory would satisfy Stat
		// with IsDir, and RemoveAll on a symlink path removes the link
		// but any traversal through it is a risk. An entry that is a
		// symlink is skipped outright rather than removed, because a
		// symlink named like a date is something an operator put there
		// deliberately and is not ours to delete.
		fi, err := os.Lstat(path)
		if err != nil {
			// Vanished between ReadDir and Lstat, most likely another
			// prune. Count it as kept and move on.
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			res.Kept++
			continue
		}
		if !fi.IsDir() {
			// A file named like a date is not a day. Leave it.
			continue
		}

		// Measure before removing. After RemoveAll the tree is gone
		// and the reclaimed size is unknowable, so the log would report
		// zero forever.
		size := dirSize(path)

		if err := os.RemoveAll(path); err != nil {
			// Report but do not abort: one unremovable day must not
			// stop the rest of the archive from being reclaimed.
			return res, fmt.Errorf("removing day %q: %w", name, err)
		}
		res.Days++
		res.Bytes += size
	}

	return res, nil
}

// dirSize sums the regular-file sizes beneath a day directory.
//
// It walks the tree rather than trusting any recorded total, because there is
// no index: the filesystem is the only record of what a day contains. The
// .thumbs cache inside the day is counted too, which is the honest answer --
// reclaiming a day reclaims its cached tiles as well.
//
// A file that cannot be stat'd is skipped rather than failing the walk. This
// only ever produces a log line, and an inexact reclaimed-bytes figure is not
// worth aborting a prune over.
func dirSize(root string) int64 {
	var total int64
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
