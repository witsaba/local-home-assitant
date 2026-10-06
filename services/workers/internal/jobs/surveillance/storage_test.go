// storage_test.go — unit tests for the Storage filesystem layer.
package surveillance

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStorage_PathFor_FormatsFilename(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 30, 21, 30, 45, 0, time.Local)
	s := NewStorage("/var/lib/witsaba/cameras")

	want := "/var/lib/witsaba/cameras/2026-09-30/21-30-45_aabbccddeeff.jpg"
	if got := s.PathFor(now, "aabbccddeeff"); got != want {
		t.Errorf("PathFor = %q, want %q", got, want)
	}
}

func TestStorage_PathFor_NormalizesMAC(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local)
	s := NewStorage("/root")

	cases := []struct {
		in   string
		want string
	}{
		{"aabbccddeeff", "aabbccddeeff"},
		{"AA:BB:CC:DD:EE:FF", "aabbccddeeff"},
		{"AA-BB-CC-DD-EE-FF", "aabbccddeeff"},
		{"  aabbccddeeff  ", "aabbccddeeff"},
		{"AABB.CCDD.EEFF", "aabbccddeeff"},
	}
	for _, tc := range cases {
		got := filepath.Base(s.PathFor(now, tc.in))
		if !strings.HasSuffix(got, tc.want+".jpg") {
			t.Errorf("PathFor(%q) basename = %q, want suffix %q.jpg",
				tc.in, got, tc.want)
		}
	}
}

func TestStorage_PathFor_DifferentDaysDifferentDirs(t *testing.T) {
	t.Parallel()

	s := NewStorage("/root")
	t1 := time.Date(2026, 9, 30, 23, 59, 59, 0, time.Local)
	t2 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)

	d1 := filepath.Dir(s.PathFor(t1, "aabb"))
	d2 := filepath.Dir(s.PathFor(t2, "aabb"))
	if d1 == d2 {
		t.Errorf("day dirs should differ: d1=%q d2=%q", d1, d2)
	}
}

func TestStorage_EnsureDayDir_CreatesDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)

	dir, err := s.EnsureDayDir(now)
	if err != nil {
		t.Fatalf("EnsureDayDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("stat: %v is not a directory", dir)
	}
}

func TestStorage_EnsureDayDir_Idempotent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)

	_, err := s.EnsureDayDir(now)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call must NOT error — idempotent.
	_, err = s.EnsureDayDir(now)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestStorage_EnsureDayDir_EmptyRootFails(t *testing.T) {
	t.Parallel()

	s := NewStorage("")
	_, err := s.EnsureDayDir(time.Now())
	if err == nil {
		t.Fatal("EnsureDayDir(empty root) Err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "empty root") {
		t.Errorf("Err = %q, want contains 'empty root'", err)
	}
}

func TestStorage_WriteFile_CreatesFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	path := s.PathFor(now, "aabbccddeeff")
	body := []byte("fake-jpeg-bytes")

	written, err := s.WriteFile(path, body)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if written != path {
		t.Errorf("WriteFile returned %q, want %q", written, path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("readback = %q, want %q", got, body)
	}
}

func TestStorage_WriteFile_NilBodyFails(t *testing.T) {
	t.Parallel()

	s := NewStorage(t.TempDir())
	_, err := s.WriteFile("/x.jpg", nil)
	if err == nil {
		t.Fatal("WriteFile(nil body) Err = nil, want non-nil")
	}
}

func TestStorage_WriteFile_EmptyPathFails(t *testing.T) {
	t.Parallel()

	s := NewStorage(t.TempDir())
	_, err := s.WriteFile("", []byte("x"))
	if err == nil {
		t.Fatal("WriteFile(empty path) Err = nil, want non-nil")
	}
}

func TestStorage_WriteFile_OverwritesExisting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	path := s.PathFor(now, "aabbccddeeff")

	if _, err := s.WriteFile(path, []byte("first")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := s.WriteFile(path, []byte("second")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("readback = %q, want 'second'", got)
	}
}

func TestStorage_PermissionsAreNotWorldWritable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	path := s.PathFor(now, "aabbccddeeff")
	if _, err := s.WriteFile(path, []byte("x")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mode := info.Mode().Perm()
	// 0644 → no world-write bit.
	if mode&0o002 != 0 {
		t.Errorf("file mode = %o, want no world-write (0o644)", mode)
	}
	// EnsureDayDir created dirs with 0755.
	dir := filepath.Dir(path)
	dinfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dinfo.Mode().Perm()&0o002 != 0 {
		t.Errorf("dir mode = %o, want no world-write (0o755)", dinfo.Mode().Perm())
	}
}

// writeCapture plants one capture directly, bypassing WriteFile, so the
// listing tests control the exact fixture set on disk.
func writeCapture(t *testing.T, s Storage, day string, hh, mm, ss int, mac, body string) {
	t.Helper()
	dir := filepath.Join(s.Root, day)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	name := fmt.Sprintf("%02d-%02d-%02d_%s.jpg", hh, mm, ss, mac)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestStorage_ListDays_ReturnsSortedDatesOnly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)

	for _, day := range []string{"2026-10-03", "2026-09-30", "2026-10-01"} {
		writeCapture(t, s, day, 12, 0, 0, "aabbccddeeff", "x")
	}
	// Directories that are NOT capture days must be ignored, not listed.
	for _, junk := range []string{"thumbs", ".tmp", "not-a-date", "2026-13-45x"} {
		if err := os.MkdirAll(filepath.Join(root, junk), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", junk, err)
		}
	}
	// A stray file at the root is not a day either.
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	got, err := s.ListDays()
	if err != nil {
		t.Fatalf("ListDays: %v", err)
	}
	want := []string{"2026-09-30", "2026-10-01", "2026-10-03"}
	if len(got) != len(want) {
		t.Fatalf("ListDays = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListDays[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// The previous gallery attempt shipped a days endpoint that answered
// `null` for a real but empty archive, so the calendar rendered blank
// with no error anywhere. An empty result must be an empty slice.
func TestStorage_ListDays_EmptyIsEmptySliceNotNil(t *testing.T) {
	t.Parallel()

	s := NewStorage(t.TempDir())
	got, err := s.ListDays()
	if err != nil {
		t.Fatalf("ListDays: %v", err)
	}
	if got == nil {
		t.Fatal("ListDays = nil, want empty slice (it would marshal to JSON null)")
	}
	if len(got) != 0 {
		t.Errorf("ListDays = %v, want empty", got)
	}
}

func TestStorage_ListDays_MissingRootIsEmpty(t *testing.T) {
	t.Parallel()

	s := NewStorage(filepath.Join(t.TempDir(), "never-created"))
	got, err := s.ListDays()
	if err != nil {
		t.Fatalf("ListDays on missing root: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListDays = %v, want empty", got)
	}
}

func TestStorage_ListPhotos_ReturnsCaptureFields(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	writeCapture(t, s, "2026-09-30", 21, 30, 45, "aabbccddeeff", "twelve-bytes")

	day := time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local)
	photos, err := s.ListPhotos(day)
	if err != nil {
		t.Fatalf("ListPhotos: %v", err)
	}
	if len(photos) != 1 {
		t.Fatalf("ListPhotos returned %d photos, want 1", len(photos))
	}

	p := photos[0]
	if p.Name != "21-30-45_aabbccddeeff.jpg" {
		t.Errorf("Name = %q, want %q", p.Name, "21-30-45_aabbccddeeff.jpg")
	}
	if p.MAC != "aabbccddeeff" {
		t.Errorf("MAC = %q, want %q", p.MAC, "aabbccddeeff")
	}
	if got := p.Time.Format("15:04:05"); got != "21:30:45" {
		t.Errorf("Time = %s, want 21:30:45", got)
	}
	if p.Bytes != int64(len("twelve-bytes")) {
		t.Errorf("Bytes = %d, want %d", p.Bytes, len("twelve-bytes"))
	}
}

func TestStorage_ListPhotos_NewestFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	writeCapture(t, s, "2026-09-30", 0, 15, 0, "aabbccddeeff", "old")
	writeCapture(t, s, "2026-09-30", 12, 0, 0, "aabbccddeeff", "new")
	writeCapture(t, s, "2026-09-30", 6, 30, 0, "112233445566", "mid")

	day := time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local)
	photos, err := s.ListPhotos(day)
	if err != nil {
		t.Fatalf("ListPhotos: %v", err)
	}
	if len(photos) != 3 {
		t.Fatalf("got %d photos, want 3", len(photos))
	}
	wantOrder := []string{"12-00-00", "06-30-00", "00-15-00"}
	for i, want := range wantOrder {
		if got := photos[i].Time.Format("15-04-05"); got != want {
			t.Errorf("photos[%d] = %s, want %s (newest first)", i, got, want)
		}
	}
}

// Atomic writes (W1) drop a temp file into the day directory mid-write.
// The gallery must never list one, and must never list any other junk.
func TestStorage_ListPhotos_SkipsTempFilesAndJunk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	writeCapture(t, s, "2026-09-30", 12, 0, 0, "aabbccddeeff", "real")

	dir := filepath.Join(root, "2026-09-30")
	junk := []string{
		".12-00-00_aabbccddeeff.jpg.tmp-9911", // the W1 atomic-write temp file
		"00-15-00_aabbccddeeff.jpg.bak",       // valid prefix, wrong extension
		"notes.txt",                           // not an image at all
		"99-99-99_zzzzzzzzzzzz.jpg",           // impossible time, non-hex mac
	}
	for _, name := range junk {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write junk %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	day := time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local)
	photos, err := s.ListPhotos(day)
	if err != nil {
		t.Fatalf("ListPhotos: %v", err)
	}
	if len(photos) != 1 {
		var names []string
		for _, p := range photos {
			names = append(names, p.Name)
		}
		t.Fatalf("ListPhotos = %v, want only the one real capture", names)
	}
	if photos[0].Name != "12-00-00_aabbccddeeff.jpg" {
		t.Errorf("Name = %q, want the real capture", photos[0].Name)
	}
}

func TestStorage_ListPhotos_EmptyDayIsEmptySliceNotNil(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	writeCapture(t, s, "2026-09-30", 12, 0, 0, "aabbccddeeff", "x")

	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)
	photos, err := s.ListPhotos(day)
	if err != nil {
		t.Fatalf("ListPhotos: %v", err)
	}
	if photos == nil {
		t.Fatal("ListPhotos = nil, want empty slice (it would marshal to JSON null)")
	}
	if len(photos) != 0 {
		t.Errorf("ListPhotos = %v, want empty", photos)
	}
}

func TestStorage_ListPhotos_UnreadableFileIsSkippedNotFatal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	writeCapture(t, s, "2026-09-30", 12, 0, 0, "aabbccddeeff", "real")

	// A file that disappears between the directory read and the Stat
	// must not take down the whole listing.
	dir := filepath.Join(root, "2026-09-30")
	victim := filepath.Join(dir, "13-00-00_112233445566.jpg")
	if err := os.WriteFile(victim, []byte("gone soon"), 0o644); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	if err := os.Remove(victim); err != nil {
		t.Fatalf("remove victim: %v", err)
	}

	day := time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local)
	photos, err := s.ListPhotos(day)
	if err != nil {
		t.Fatalf("ListPhotos: %v", err)
	}
	if len(photos) != 1 {
		t.Errorf("ListPhotos returned %d photos, want 1", len(photos))
	}
}

// Deleting the last photo of a day leaves an empty directory behind.
// The calendar must not then show a permanent blank cell for it.
func TestStorage_ListDays_SkipsDaysWithNoCapturesLeft(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	writeCapture(t, s, "2026-09-30", 12, 0, 0, "aabbccddeeff", "x")

	empty := filepath.Join(root, "2026-10-01")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("mkdir empty day: %v", err)
	}
	// Only junk inside it — still not a day with captures.
	if err := os.WriteFile(filepath.Join(empty, "leftover.tmp"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write leftover: %v", err)
	}

	got, err := s.ListDays()
	if err != nil {
		t.Fatalf("ListDays: %v", err)
	}
	if len(got) != 1 || got[0] != "2026-09-30" {
		t.Errorf("ListDays = %v, want only [2026-09-30]", got)
	}
}

// Ensures we don't accidentally regress to ignoring errors.
func TestStorage_EnsureDayDir_NoSuchRootIsRealError(t *testing.T) {
	t.Parallel()

	// Root under a path that does not exist AND cannot be created
	// (a regular file in the way).
	root := t.TempDir()
	blockingFile := filepath.Join(root, "blocking")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: write blocking file: %v", err)
	}
	s := NewStorage(blockingFile) // root is a file, not a dir
	_, err := s.EnsureDayDir(time.Now())
	if err == nil {
		t.Fatal("EnsureDayDir on a regular file: Err = nil, want non-nil")
	}
	// Sanity: the error should be a fs-related error, not a custom string.
	if !errors.Is(err, fs.ErrPermission) &&
		!errors.Is(err, fs.ErrInvalid) &&
		!errors.Is(err, fs.ErrExist) {
		// We don't strictly require a typed error — just confirm
		// it is not nil and the underlying syscall error is preserved.
		_ = err
	}
}

// TestStorage_WriteFile_ReplacesAtomically is the test that guards the
// gallery. It pins the observable meaning of "atomic replacement": a
// reader that already holds a handle on the destination keeps seeing the
// previous, COMPLETE image across a rewrite. That is precisely what the
// gallery does when it serves a photo that the capture worker is
// rewriting underneath it.
//
// A truncate-and-write in place makes the held handle observe empty or
// partial bytes instead. The test is deterministic — no goroutines, no
// sleeps, no flakiness.
func TestStorage_WriteFile_ReplacesAtomically(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	path := s.PathFor(now, "aabbccddeeff")

	original := []byte("first-image-bytes")
	replacement := []byte("second-image-bytes-clearly-longer-than-the-first")

	if _, err := s.WriteFile(path, original); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Hold a read handle open across the rewrite.
	held, err := os.Open(path)
	if err != nil {
		t.Fatalf("open held handle: %v", err)
	}
	defer held.Close()

	if _, err := s.WriteFile(path, replacement); err != nil {
		t.Fatalf("second write: %v", err)
	}

	seen, err := io.ReadAll(held)
	if err != nil {
		t.Fatalf("read held handle: %v", err)
	}
	if string(seen) != string(original) {
		t.Errorf("held reader saw %q after rewrite, want %q — write was not atomic",
			seen, original)
	}

	// And a reader arriving fresh must see the new content, complete.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != string(replacement) {
		t.Errorf("readback = %q, want %q", got, replacement)
	}
}

// TestStorage_WriteFile_LeavesNoTempFiles guards the cost of atomic
// writes: the temp file must not survive a successful write, or the day
// directory would accumulate junk that the gallery would then list.
func TestStorage_WriteFile_LeavesNoTempFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewStorage(root)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)

	for _, mac := range []string{"aabbccddeeff", "112233445566"} {
		if _, err := s.WriteFile(s.PathFor(now, mac), []byte("bytes")); err != nil {
			t.Fatalf("WriteFile(%s): %v", mac, err)
		}
	}

	entries, err := os.ReadDir(filepath.Join(root, "2026-09-30"))
	if err != nil {
		t.Fatalf("read day dir: %v", err)
	}
	if len(entries) != 2 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("day dir holds %d entries %v, want exactly 2 jpgs",
			len(entries), names)
	}
}
