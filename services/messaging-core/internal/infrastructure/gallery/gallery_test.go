package gallery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFrame creates a capture file of the given size in a day folder.
func writeFrame(t *testing.T, root, date, tick, mac string, size int) string {
	t.Helper()
	dir := filepath.Join(root, date)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	p := filepath.Join(dir, tick+"_"+mac+tickSuffix)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestValidateDate(t *testing.T) {
	valid := []string{"2026-10-05", "2024-02-29", "1970-01-01"}
	for _, v := range valid {
		if err := ValidateDate(v); err != nil {
			t.Errorf("ValidateDate(%q): want nil, got %v", v, err)
		}
	}

	invalid := []struct {
		raw  string
		note string
	}{
		{"", "empty"},
		{"2026-10", "no day part"},
		{"2026/10/05", "path separators"},
		{"2026-10-05/../../etc", "traversal attempt"},
		{"2026-13-45", "matches the regex but is not a real date"},
		{"2023-02-29", "not a leap year"},
		{"2026-10-05T00:00:00Z", "carries a time"},
		{"../2026-10-05", "leading traversal"},
		{"2026-1-5", "unpadded"},
	}
	for _, c := range invalid {
		if err := ValidateDate(c.raw); err == nil {
			t.Errorf("ValidateDate(%q): want an error (%s), got nil", c.raw, c.note)
		}
	}
}

func TestValidateTick(t *testing.T) {
	valid := []string{"00-00-00", "14-15-00", "23-59-59"}
	for _, v := range valid {
		if err := ValidateTick(v); err != nil {
			t.Errorf("ValidateTick(%q): want nil, got %v", v, err)
		}
	}

	invalid := []struct {
		raw  string
		note string
	}{
		{"", "empty"},
		{"14-15", "no seconds"},
		{"14:15:00", "colons"},
		{"14-15-60", "matches the regex but 60s is not a clock time"},
		{"24-00-00", "hour out of range"},
		{"14-15-00x", "trailing junk"},
		{"14-15-00/../..", "traversal attempt"},
	}
	for _, c := range invalid {
		if err := ValidateTick(c.raw); err == nil {
			t.Errorf("ValidateTick(%q): want an error (%s), got nil", c.raw, c.note)
		}
	}
}

func TestValidateMAC(t *testing.T) {
	if err := ValidateMAC("d4e9f48d381c"); err != nil {
		t.Errorf("ValidateMAC(valid): got %v, want nil", err)
	}

	invalid := []string{
		"",
		"D4E9F48D381C",     // uppercase: filenames are normalized lowercase
		"d4e9f48d381",      // 11 chars
		"d4e9f48d381cd",    // 13 chars
		"d4-e9-f4-8d-38",   // colon-separated
		"../../../etc",      // traversal attempt
		"d4e9f48d381c.jpg", // extension smuggled in
		"d4e9f48d381c;",    // shell metacharacter
	}
	for _, v := range invalid {
		if err := ValidateMAC(v); err == nil {
			t.Errorf("ValidateMAC(%q): want an error, got nil", v)
		}
	}
}

// TestFramePathStaysInsideRoot is the traversal defense. The validators make
// escape unreachable, so the withinRoot guard is asserted independently: if
// someone later widens a pattern, this test still fails.
func TestFramePathStaysInsideRoot(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)

	hostile := []struct{ date, tick, mac string }{
		{"../../etc", "00-00-00", "d4e9f48d381c"},
		{"2026-10-05", "../../../etc/passwd", "d4e9f48d381c"},
		{"2026-10-05", "00-00-00", "../../../../etc/shadow"},
		{"..", "00-00-00", "d4e9f48d381c"},
		{"/etc", "00-00-00", "d4e9f48d381c"},
	}
	for _, c := range hostile {
		p, err := s.framePath(c.date, c.tick, c.mac)
		if err != nil {
			// The validators rejected it before any path was built,
			// which is the intended outcome.
			continue
		}
		// If a hostile input ever does resolve, the redundant
		// withinRoot guard must still keep it contained.
		if !withinRoot(root, p) {
			t.Errorf("framePath(%q,%q,%q) returned %q which escapes the root", c.date, c.tick, c.mac, p)
		}
		t.Errorf("framePath(%q,%q,%q): want an error, got path %q", c.date, c.tick, c.mac, p)
	}
}

func TestWithinRoot(t *testing.T) {
	cases := []struct {
		root, path string
		want       bool
	}{
		{"/root/cameras", "/root/cameras", true},
		{"/root/cameras", "/root/cameras/2026-10-05/a.jpg", true},
		{"/root/cameras", "/root/cameras/../etc/passwd", false},
		{"/root/cameras", "/etc/passwd", false},
		// The prefix trap: a sibling that shares the name prefix must
		// not be accepted.
		{"/root/cameras", "/root/cameras-evil/x.jpg", false},
		{"/root/cameras", "/root/camerasx", false},
	}
	for _, c := range cases {
		if got := withinRoot(c.root, c.path); got != c.want {
			t.Errorf("withinRoot(%q, %q): got %v, want %v", c.root, c.path, got, c.want)
		}
	}
}

func TestImageURL(t *testing.T) {
	got := ImageURL("2026-10-05", "14-15-00", "d4e9f48d381c")
	want := "/api/gallery/img?date=2026-10-05&t=14-15-00&mac=d4e9f48d381c"
	if got != want {
		t.Errorf("ImageURL: got %q, want %q", got, want)
	}
}

func TestThumbURL(t *testing.T) {
	got := ThumbURL("2026-10-05", "14-15-00", "d4e9f48d381c", 320)
	want := "/api/gallery/thumb?date=2026-10-05&t=14-15-00&mac=d4e9f48d381c&w=320"
	if got != want {
		t.Errorf("ThumbURL: got %q, want %q", got, want)
	}
}

func TestListDays_MissingRootIsEmptyNotAnError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	s := NewStore(root)

	days, err := s.ListDays()
	if err != nil {
		t.Fatalf("ListDays on a missing root: want nil error, got %v", err)
	}
	if len(days) != 0 {
		t.Fatalf("len(days): got %d, want 0", len(days))
	}
}

func TestListDays_EmptyRoot(t *testing.T) {
	s := NewStore(t.TempDir())
	days, err := s.ListDays()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(days) != 0 {
		t.Fatalf("len(days): got %d, want 0", len(days))
	}
}

func TestListDays_SortsNewestFirst(t *testing.T) {
	root := t.TempDir()
	writeFrame(t, root, "2026-10-03", "09-00-00", "d4e9f48d381c", 10)
	writeFrame(t, root, "2026-10-05", "09-00-00", "d4e9f48d381c", 10)
	writeFrame(t, root, "2026-10-04", "09-00-00", "d4e9f48d381c", 10)
	s := NewStore(root)

	days, err := s.ListDays()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"2026-10-05", "2026-10-04", "2026-10-03"}
	if len(days) != len(want) {
		t.Fatalf("len(days): got %d, want %d", len(days), len(want))
	}
	for i, w := range want {
		if days[i].Date != w {
			t.Errorf("days[%d].Date: got %q, want %q", i, days[i].Date, w)
		}
	}
}

func TestListDays_IgnoresNonDateFolders(t *testing.T) {
	root := t.TempDir()
	writeFrame(t, root, "2026-10-05", "09-00-00", "d4e9f48d381c", 10)
	// The thumbnail cache directory, an operator folder, and a loose file.
	if err := os.MkdirAll(filepath.Join(root, "2026-10-05", thumbDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(root)

	days, err := s.ListDays()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(days) != 1 || days[0].Date != "2026-10-05" {
		t.Fatalf("days: got %+v, want only 2026-10-05", days)
	}
}

func TestListDays_SkipsFramesInsideThumbs(t *testing.T) {
	root := t.TempDir()
	writeFrame(t, root, "2026-10-05", "09-00-00", "d4e9f48d381c", 100)
	thumbs := filepath.Join(root, "2026-10-05", thumbDirName)
	if err := os.MkdirAll(thumbs, 0o755); err != nil {
		t.Fatal(err)
	}
	// A cached thumbnail must never be counted as a capture.
	if err := os.WriteFile(filepath.Join(thumbs, "09-00-00_d4e9f48d381c.jpg"), make([]byte, 5), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(root)

	days, err := s.ListDays()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(days) != 1 {
		t.Fatalf("len(days): got %d, want 1", len(days))
	}
	if days[0].Shots != 1 {
		t.Errorf("Shots: got %d, want 1 (a cached thumbnail is not a capture)", days[0].Shots)
	}
	if days[0].Bytes != 100 {
		t.Errorf("Bytes: got %d, want 100 (cached thumbnail bytes must not count)", days[0].Bytes)
	}
}

func TestListDays_CountsCamerasDistinctly(t *testing.T) {
	root := t.TempDir()
	writeFrame(t, root, "2026-10-05", "09-00-00", "d4e9f48d381c", 10)
	writeFrame(t, root, "2026-10-05", "09-00-00", "e08cfe3091b0", 10)
	writeFrame(t, root, "2026-10-05", "09-15-00", "d4e9f48d381c", 10)
	s := NewStore(root)

	days, err := s.ListDays()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if days[0].Shots != 3 {
		t.Errorf("Shots: got %d, want 3", days[0].Shots)
	}
	if days[0].Cameras != 2 {
		t.Errorf("Cameras: got %d, want 2 (distinct MACs across the day)", days[0].Cameras)
	}
	if days[0].Bytes != 30 {
		t.Errorf("Bytes: got %d, want 30", days[0].Bytes)
	}
}

func TestListDays_SkipsStrayFiles(t *testing.T) {
	root := t.TempDir()
	writeFrame(t, root, "2026-10-05", "09-00-00", "d4e9f48d381c", 10)
	dayDir := filepath.Join(root, "2026-10-05")
	// Files a capture never produces: uppercase MAC, no separator, wrong
	// extension, a .part from an interrupted write.
	for _, name := range []string{
		"09-15-00_D4E9F48D381C.jpg",
		"09-15-00d4e9f48d381c.jpg",
		"09-15-00_d4e9f48d381c.png",
		"09-15-00_d4e9f48d381c.jpg.part",
		"README",
	} {
		if err := os.WriteFile(filepath.Join(dayDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := NewStore(root)

	days, err := s.ListDays()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if days[0].Shots != 1 {
		t.Errorf("Shots: got %d, want 1 (stray files are not captures)", days[0].Shots)
	}
}

func TestDay_GroupsByMoment(t *testing.T) {
	root := t.TempDir()
	writeFrame(t, root, "2026-10-05", "09-00-00", "d4e9f48d381c", 10)
	writeFrame(t, root, "2026-10-05", "09-00-00", "e08cfe3091b0", 20)
	writeFrame(t, root, "2026-10-05", "09-15-00", "d4e9f48d381c", 30)
	s := NewStore(root)

	day, err := s.Day("2026-10-05", map[string]string{"d4e9f48d381c": "Garage"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(day.Moments) != 2 {
		t.Fatalf("len(Moments): got %d, want 2", len(day.Moments))
	}
	// Chronological, and the zero-padded filename order matches time order.
	if day.Moments[0].Time != "09-00-00" || day.Moments[1].Time != "09-15-00" {
		t.Errorf("Moments: got [%s %s], want [09-00-00 09-15-00]", day.Moments[0].Time, day.Moments[1].Time)
	}
	if len(day.Moments[0].Shots) != 2 {
		t.Fatalf("first moment shots: got %d, want 2 (one per camera)", len(day.Moments[0].Shots))
	}
	if day.Moments[0].Shots[0].Bytes != 10 || day.Moments[0].Shots[1].Bytes != 20 {
		t.Errorf("first moment sizes: got %d,%d want 10,20",
			day.Moments[0].Shots[0].Bytes, day.Moments[0].Shots[1].Bytes)
	}
}

func TestDay_NamesComeFromTheCaller(t *testing.T) {
	root := t.TempDir()
	writeFrame(t, root, "2026-10-05", "09-00-00", "d4e9f48d381c", 10)
	writeFrame(t, root, "2026-10-05", "09-00-00", "e08cfe3091b0", 10)
	s := NewStore(root)

	// Only one camera is known to the database, which is the real
	// situation: the other is offline and has aged out of ListActive.
	day, err := s.Day("2026-10-05", map[string]string{"d4e9f48d381c": "Garage"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(day.Cameras) != 2 {
		t.Fatalf("len(Cameras): got %d, want 2", len(day.Cameras))
	}
	byMAC := map[string]Camera{}
	for _, c := range day.Cameras {
		byMAC[c.MAC] = c
	}
	if byMAC["d4e9f48d381c"].Name != "Garage" {
		t.Errorf("known camera name: got %q, want %q", byMAC["d4e9f48d381c"].Name, "Garage")
	}
	if byMAC["e08cfe3091b0"].Name != "" {
		t.Errorf("unknown camera name: got %q, want empty (never invent one)", byMAC["e08cfe3091b0"].Name)
	}
}

func TestDay_UnknownDayIsEmptyNotAnError(t *testing.T) {
	s := NewStore(t.TempDir())
	day, err := s.Day("2020-01-01", nil)
	if err != nil {
		t.Fatalf("Day on a missing folder: want nil error, got %v", err)
	}
	if len(day.Moments) != 0 {
		t.Fatalf("len(Moments): got %d, want 0", len(day.Moments))
	}
	if day.Cameras == nil || day.Moments == nil {
		t.Error("slices must be non-nil so the JSON is [] and not null")
	}
}

func TestDay_RejectsMalformedDate(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, bad := range []string{"", "2026-13-45", "../../etc", "2026-10-05/../.."} {
		if _, err := s.Day(bad, nil); err == nil {
			t.Errorf("Day(%q): want an error, got nil", bad)
		}
	}
}

func TestDay_ImageURLIsPresent(t *testing.T) {
	root := t.TempDir()
	writeFrame(t, root, "2026-10-05", "09-00-00", "d4e9f48d381c", 10)
	s := NewStore(root)

	day, err := s.Day("2026-10-05", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	url := day.Moments[0].Shots[0].ImageURL
	if !strings.Contains(url, "date=2026-10-05") || !strings.Contains(url, "t=09-00-00") {
		t.Errorf("ImageURL: got %q, want it to carry the date and tick", url)
	}
}
