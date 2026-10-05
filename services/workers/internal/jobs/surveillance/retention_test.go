package surveillance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
)

// writeDay creates a day directory containing n small JPEGs, and returns the
// day path.
func writeDay(t *testing.T, root, date string, n int) string {
	t.Helper()

	dir := filepath.Join(root, date)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", date, err)
	}
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, "12-00-0"+string(rune('0'+i))+"_e08cfe3091b0.jpg")
		if err := os.WriteFile(p, make([]byte, 512), 0o644); err != nil {
			t.Fatalf("writing frame in %s: %v", date, err)
		}
	}
	return dir
}

func exists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Lstat(p)
	return err == nil
}

func TestPruneOlderThan_RemovesOldDaysKeepsRecent(t *testing.T) {
	root := t.TempDir()
	s := NewStorage(root)

	old := writeDay(t, root, "2026-08-01", 3)   // well past 30 days
	mid := writeDay(t, root, "2026-09-01", 3)   // inside the window
	newer := writeDay(t, root, "2026-09-28", 3) // inside the window

	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	res, err := s.PruneOlderThan(now, 30)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}

	if exists(t, old) {
		t.Error("the stale day survived the prune")
	}
	if !exists(t, mid) {
		t.Error("a day inside the retention window was removed")
	}
	if !exists(t, newer) {
		t.Error("the newest day was removed")
	}
	if res.Days != 1 {
		t.Errorf("Days: got %d, want 1", res.Days)
	}
	if res.Kept != 2 {
		t.Errorf("Kept: got %d, want 2", res.Kept)
	}
	if res.Bytes == 0 {
		t.Error("Bytes: got 0, want the reclaimed size; it must be measured before removal")
	}
}

// TestPruneOlderThan_BoundaryIsInclusive pins the off-by-one that matters:
// a day exactly keepDays old is still inside the window.
func TestPruneOlderThan_BoundaryIsInclusive(t *testing.T) {
	root := t.TempDir()
	s := NewStorage(root)

	// now minus 30 days is 2026-08-31; that day is exactly at the cutoff.
	onCutoff := writeDay(t, root, "2026-08-31", 1)
	oneBefore := writeDay(t, root, "2026-08-30", 1)

	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	if _, err := s.PruneOlderThan(now, 30); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}

	if !exists(t, onCutoff) {
		t.Error("the day exactly at the cutoff was removed; the window must be inclusive")
	}
	if exists(t, oneBefore) {
		t.Error("the day before the cutoff survived")
	}
}

func TestPruneOlderThan_ZeroDisables(t *testing.T) {
	for _, days := range []int{0, -1, -30} {
		root := t.TempDir()
		s := NewStorage(root)
		day := writeDay(t, root, "2020-01-01", 2)

		res, err := s.PruneOlderThan(time.Now(), days)
		if err != nil {
			t.Fatalf("keepDays=%d: %v", days, err)
		}
		if res.Days != 0 {
			t.Errorf("keepDays=%d: removed %d days, want 0", days, res.Days)
		}
		if !exists(t, day) {
			t.Errorf("keepDays=%d: pruned a day; 0 and negatives must disable retention", days)
		}
	}
}

func TestPruneOlderThan_IgnoresNonDayEntries(t *testing.T) {
	root := t.TempDir()
	s := NewStorage(root)

	// Directories and files that are not captures must survive untouched.
	mustSurvive := []string{".thumbs", "2026", "not-a-date", "2026-9-3"}
	for _, name := range mustSurvive {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}
	// A FILE named like a day is not a day.
	if err := os.WriteFile(filepath.Join(root, "2026-08-01"), []byte("x"), 0o644); err != nil {
		t.Fatalf("creating day-named file: %v", err)
	}

	if _, err := s.PruneOlderThan(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), 30); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}

	for _, name := range mustSurvive {
		if !exists(t, filepath.Join(root, name)) {
			t.Errorf("%q was removed; only real day directories may be pruned", name)
		}
	}
	if !exists(t, filepath.Join(root, "2026-08-01")) {
		t.Error("a FILE named like a day was removed")
	}
}

// TestPruneOlderThan_KeepsSymlinkedDay is the traversal guard. A symlink
// named like a day points outside the root; RemoveAll on a path that traverses
// it would delete the target's contents, so the entry must be skipped.
func TestPruneOlderThan_KeepsSymlinkedDay(t *testing.T) {
	outside := t.TempDir()
	precious := filepath.Join(outside, "keep-me.txt")
	if err := os.WriteFile(precious, []byte("important"), 0o644); err != nil {
		t.Fatalf("writing target: %v", err)
	}

	root := t.TempDir()
	s := NewStorage(root)
	link := filepath.Join(root, "2026-08-01")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	res, err := s.PruneOlderThan(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), 30)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}

	if !exists(t, link) {
		t.Error("the symlink was removed; it is something an operator placed deliberately")
	}
	if !exists(t, precious) {
		t.Error("the symlink target was deleted; the prune followed a link out of the root")
	}
	if res.Days != 0 {
		t.Errorf("Days: got %d, want 0", res.Days)
	}
}

func TestPruneOlderThan_KeepsFutureDays(t *testing.T) {
	root := t.TempDir()
	s := NewStorage(root)
	future := writeDay(t, root, "2027-01-01", 1)

	// A clock skewed ahead, or a Pi whose date was wrong, must not cause
	// the prune to eat yesterday's captures.
	if _, err := s.PruneOlderThan(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 30); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if !exists(t, future) {
		t.Error("a day newer than now was removed")
	}
}

func TestPruneOlderThan_MissingRootIsNotAnError(t *testing.T) {
	s := NewStorage(filepath.Join(t.TempDir(), "never-created"))

	res, err := s.PruneOlderThan(time.Now(), 30)
	if err != nil {
		t.Errorf("a root that does not exist must not be a tick failure: %v", err)
	}
	if res.Days != 0 {
		t.Errorf("Days: got %d, want 0", res.Days)
	}
}

func TestPruneOlderThan_RefusesDangerousRoot(t *testing.T) {
	for _, root := range []string{"/", ".", ""} {
		s := NewStorage(root)
		if _, err := s.PruneOlderThan(time.Now(), 30); err == nil {
			t.Errorf("root %q: got nil error, want a refusal to prune", root)
		}
	}
}

func TestPruneOlderThan_RemovesThumbCacheWithTheDay(t *testing.T) {
	root := t.TempDir()
	s := NewStorage(root)

	day := writeDay(t, root, "2026-08-01", 1)
	// messaging-core generates this cache lazily inside the day folder.
	thumbs := filepath.Join(day, ".thumbs", "320")
	if err := os.MkdirAll(thumbs, 0o755); err != nil {
		t.Fatalf("creating thumb cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(thumbs, "12-00-00_e08cfe3091b0.jpg"), make([]byte, 128), 0o600); err != nil {
		t.Fatalf("writing thumb: %v", err)
	}

	if _, err := s.PruneOlderThan(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), 30); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}

	if exists(t, day) {
		t.Error("the day survived; its thumbnail cache must be reclaimed with it")
	}
}

func TestPruneOlderThan_ReportsBytesAcrossDays(t *testing.T) {
	root := t.TempDir()
	s := NewStorage(root)

	writeDay(t, root, "2026-08-01", 4)
	writeDay(t, root, "2026-08-02", 4)

	res, err := s.PruneOlderThan(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), 30)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if res.Days != 2 {
		t.Errorf("Days: got %d, want 2", res.Days)
	}
	if want := int64(8 * 512); res.Bytes != want {
		t.Errorf("Bytes: got %d, want %d", res.Bytes, want)
	}
}

// TestRunPrunesBeforeCapturing proves the prune is actually wired into the
// tick, not merely implemented and never called.
func TestRunPrunesBeforeCapturing(t *testing.T) {
	root := t.TempDir()
	stale := writeDay(t, root, "2026-08-01", 2)

	// The repo returns no cameras, so the tick writes nothing itself and
	// any change on disk is the prune's doing.
	j := newTestJob(t, &fakeDevicesRepo{}, &fakeClock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)}, nil, root, Window{})
	j.SetRetentionDays(30)

	if err := j.Run(context.Background(), func(types.DiscoveryEvent) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exists(t, stale) {
		t.Error("the tick did not prune the stale day")
	}
}

// TestRunWithZeroCamerasStillPrunes pins the ordering: pruning happens before
// the repo is consulted, so a day with no cameras online still gets cleaned.
func TestRunWithZeroCamerasStillPrunes(t *testing.T) {
	root := t.TempDir()
	stale := writeDay(t, root, "2026-08-01", 2)

	j := newTestJob(t, &fakeDevicesRepo{}, &fakeClock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)}, nil, root, Window{})
	j.SetRetentionDays(30)

	if err := j.Run(context.Background(), func(types.DiscoveryEvent) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exists(t, stale) {
		t.Error("pruning was skipped because no cameras were found")
	}
}

// TestRunWithRetentionDisabledKeepsEverything proves the default in the test
// harness and the production default cannot silently turn into "delete all".
func TestRunWithRetentionDisabledKeepsEverything(t *testing.T) {
	root := t.TempDir()
	stale := writeDay(t, root, "2000-01-01", 2)

	j := newTestJob(t, &fakeDevicesRepo{}, &fakeClock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)}, nil, root, Window{})

	if err := j.Run(context.Background(), func(types.DiscoveryEvent) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !exists(t, stale) {
		t.Error("a job with retention unset deleted a two-decade-old day; unset must mean disabled")
	}
}

// unremovableDay makes a day directory that RemoveAll cannot delete, by
// stripping its write permission so the sweep cannot unlink its contents.
// Returns a restore func because t.TempDir cleanup also needs the write bit
// back.
//
// This only means anything as a non-root user: root ignores the permission
// bits and would remove the day anyway, so the caller skips instead of
// asserting something false.
func unremovableDay(t *testing.T, root, date string) func() {
	t.Helper()

	path := writeDay(t, root, date, 2)
	if err := os.Chmod(path, 0o555); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	return func() { _ = os.Chmod(path, 0o755) }
}

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are ignored, so an " +
			"unremovable directory cannot be simulated")
	}
}

// TestPruneOlderThan_KeepsGoingPastAnUnremovableDay is the case the comment
// in retention.go always claimed and the code did not do: one day that cannot
// be removed must not stop the days after it from being reclaimed. Retention
// exists to bound disk growth, so skipping the rest of the archive over one
// stubborn directory defeats the purpose.
func TestPruneOlderThan_KeepsGoingPastAnUnremovableDay(t *testing.T) {
	skipIfRoot(t)

	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	root := t.TempDir()

	// ReadDir returns entries sorted by name, so the unremovable day is
	// visited first and the sweep must continue past it to reach the rest.
	restore := unremovableDay(t, root, "2026-08-01")
	defer restore()
	writeDay(t, root, "2026-08-02", 3)
	writeDay(t, root, "2026-08-03", 4)
	writeDay(t, root, "2026-09-30", 5) // inside the window, must be kept

	s := Storage{Root: root}
	res, err := s.PruneOlderThan(now, 30)

	if err == nil {
		t.Error("PruneOlderThan returned no error; the unremovable day was not reported")
	}
	if res.Days != 2 {
		t.Errorf("days removed: got %d, want 2 -- the sweep must continue past the failure", res.Days)
	}
	// Days+Kept equals the four day directories on disk: two removed, one
	// failed to remove, one inside the window.
	if res.Kept != 2 {
		t.Errorf("kept: got %d, want 2 (the unremovable day and the in-window day)", res.Kept)
	}
	if res.Days+res.Kept != 4 {
		t.Errorf("days+kept: got %d, want 4 so the log accounts for every day directory",
			res.Days+res.Kept)
	}
	if res.Bytes == 0 {
		t.Error("bytes reclaimed: got 0, want the two removable days measured")
	}

	for _, gone := range []string{"2026-08-02", "2026-08-03"} {
		if _, err := os.Stat(root + string(os.PathSeparator) + gone); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed, stat returned %v", gone, err)
		}
	}
	if _, err := os.Stat(root + string(os.PathSeparator) + "2026-08-01"); err != nil {
		t.Errorf("the unremovable day should still be there, stat returned %v", err)
	}
}

// TestRunSurvivesAnUnremovableDay proves a retention failure never costs us
// captures. A failing prune that aborts the tick would turn a disk problem
// into a security problem.
func TestRunSurvivesAnUnremovableDay(t *testing.T) {
	skipIfRoot(t)

	root := t.TempDir()
	restore := unremovableDay(t, root, "2026-08-01")
	defer restore()

	j := newTestJob(t, &fakeDevicesRepo{},
		&fakeClock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)}, nil, root, Window{})
	j.SetRetentionDays(30)

	// The prune genuinely fails here: the day exists, is stale, and cannot be
	// removed. The previous version of this test pointed Storage at a
	// missing directory, which is a deliberate no-op, so it proved nothing.
	if err := j.Run(context.Background(), func(types.DiscoveryEvent) {}); err != nil {
		t.Fatalf("Run returned %v; a prune failure must not fail the tick", err)
	}
}
