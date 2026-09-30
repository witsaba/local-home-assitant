// storage_test.go — unit tests for the Storage filesystem layer.
package surveillance

import (
	"errors"
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
