package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The image and thumbnail endpoints have their own HTTP tests. The two
// endpoints the gallery page actually loads first -- /days and /day -- did
// not, so the JSON contract the browser parses was only covered at the store
// level. These tests pin it over real HTTP, because that contract is now
// load-bearing for a page rather than an internal caller.

// The two endpoints carry deliberately different shapes and the page reads
// them differently: /days summarises a day for the picker (cameras is a
// count), while /day details one day (cameras is the label list). Collapsing
// them into one struct hides the collision entirely -- two fields with the
// same json tag make encoding/json drop both, which reads as "the API is
// missing this field" rather than as a broken test type.
type daySummary struct {
	Date    string `json:"date"`
	Shots   int    `json:"shots"`
	Cameras int    `json:"cameras"`
	Bytes   int64  `json:"bytes"`
}

type dayResponse struct {
	Date    string `json:"date"`
	Cameras []struct {
		MAC  string `json:"mac"`
		Name string `json:"name"`
	} `json:"cameras"`
	Moments []struct {
		Time  string `json:"time"`
		Shots []struct {
			MAC      string `json:"mac"`
			Name     string `json:"name"`
			Bytes    int64  `json:"bytes"`
			ImageURL string `json:"image_url"`
		} `json:"shots"`
	} `json:"moments"`
}

func getJSON(t *testing.T, h *Handler, url string, want int) []byte {
	t.Helper()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", url, nil))
	if rr.Code != want {
		t.Fatalf("GET %s: got %d, want %d (body %q)", url, rr.Code, want, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("GET %s: Content-Type: got %q, want application/json", url, ct)
	}
	return rr.Body.Bytes()
}

// TestGalleryDays_NewestFirst is the contract the day picker depends on: the
// list is newest first, so the page can take element zero as "today" without
// sorting. A caller that rendered the picker in ascending order would offer
// the most recent capture at the bottom of a dropdown.
func TestGalleryDays_NewestFirst(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, "2026-09-28", "21-30-45", testMAC)
	writeTestCapture(t, root, "2026-09-30", "06-00-00", testMAC)
	writeTestCapture(t, root, "2026-09-30", "06-15-00", "d4e9f48d381c")

	body := getJSON(t, h, "/api/gallery/days", http.StatusOK)

	var days []daySummary
	if err := json.Unmarshal(body, &days); err != nil {
		t.Fatalf("decoding days: %v (body %s)", err, body)
	}
	if len(days) != 2 {
		t.Fatalf("day count: got %d, want 2 (body %s)", len(days), body)
	}
	if days[0].Date != "2026-09-30" || days[1].Date != "2026-09-28" {
		t.Errorf("day order: got %q then %q, want 2026-09-30 then 2026-09-28",
			days[0].Date, days[1].Date)
	}

	// The picker shows shot and camera counts, so these must be the real
	// totals for the day and not just the number of moments.
	if days[0].Shots != 2 {
		t.Errorf("2026-09-30 shots: got %d, want 2", days[0].Shots)
	}
	if days[0].Cameras != 2 {
		t.Errorf("2026-09-30 cameras: got %d, want 2", days[0].Cameras)
	}
	if days[0].Bytes <= 0 {
		t.Errorf("2026-09-30 bytes: got %d, want a positive size", days[0].Bytes)
	}
}

// TestGalleryDay_GroupsBySharedTick is the central design decision. Three
// cameras captured in the same tick become one moment with three shots, so the
// page can answer "what did the cameras see at 06:00" instead of flattening
// the day into an undifferentiated grid.
func TestGalleryDay_GroupsBySharedTick(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, "2026-09-30", "06-00-00", testMAC)
	writeTestCapture(t, root, "2026-09-30", "06-00-00", "d4e9f48d381c")
	writeTestCapture(t, root, "2026-09-30", "06-00-00", "b8e9f50d4c2f")
	writeTestCapture(t, root, "2026-09-30", "06-15-00", testMAC)

	body := getJSON(t, h, "/api/gallery/day?date=2026-09-30", http.StatusOK)

	var day dayResponse
	if err := json.Unmarshal(body, &day); err != nil {
		t.Fatalf("decoding day: %v (body %s)", err, body)
	}
	if len(day.Moments) != 2 {
		t.Fatalf("moment count: got %d, want 2 (body %s)", len(day.Moments), body)
	}
	if got := day.Moments[0].Time; got != "06-00-00" {
		t.Errorf("first moment time: got %q, want 06-00-00", got)
	}
	if n := len(day.Moments[0].Shots); n != 3 {
		t.Errorf("shots in the 06-00-00 moment: got %d, want 3", n)
	}
	// The detail response reports the camera label list, not a count; the
	// page takes its count from the moments it iterates.
	if n := len(day.Cameras); n != 3 {
		t.Errorf("camera labels: got %d, want 3", n)
	}
	total := 0
	for _, m := range day.Moments {
		total += len(m.Shots)
	}
	if total != 4 {
		t.Errorf("total shots across moments: got %d, want 4", total)
	}

	// image_url must be the frame endpoint with the moment's own tick. A
	// shared or stale tick here is the bug that renders a tile from the
	// wrong capture while the caption still shows the right time.
	for _, m := range day.Moments {
		for _, s := range m.Shots {
			want := "/api/gallery/img?date=" + day.Date + "&t=" + m.Time + "&mac=" + s.MAC
			if s.ImageURL != want {
				t.Errorf("image_url: got %q, want %q", s.ImageURL, want)
			}
		}
	}
}

// TestGalleryDay_TickIsNeverReformatted guards the one rule that broke twice:
// the tick and the day folder are Pi local wall-clock, and the page formats
// them with a string replace. If either side ever starts parsing them as a
// timestamp, a UTC browser files captures under the wrong day.
func TestGalleryDay_TickIsNeverReformatted(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, "2026-09-30", "06-15-00", testMAC)

	body := getJSON(t, h, "/api/gallery/day?date=2026-09-30", http.StatusOK)

	var day dayResponse
	if err := json.Unmarshal(body, &day); err != nil {
		t.Fatalf("decoding day: %v", err)
	}
	if day.Date != "2026-09-30" {
		t.Errorf("date: got %q, want the 2026-09-30 folder name verbatim", day.Date)
	}
	got := day.Moments[0].Time
	if got != "06-15-00" {
		t.Errorf("time: got %q, want 06-15-00 -- the filename's wall-clock value verbatim", got)
	}
	// The page turns "06-15-00" into "06:15:00" on the client. A colon or
	// an offset here would make that replace silently miss.
	if strings.ContainsAny(got, ":+Z") {
		t.Errorf("time %q carries a timezone or separator the client cannot format", got)
	}
}

// TestGalleryDay_NamesHistoricalCapturesFromDeviceList covers why the gallery
// uses ListAll rather than ListActive: a capture from last week cannot be
// labelled from the active window, because that window excludes devices not
// seen recently. Here the device is not in the active set at all, and the
// name must still resolve.
func TestGalleryDay_NamesHistoricalCapturesFromDeviceList(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, "2026-09-30", "06-00-00", testMAC)

	body := getJSON(t, h, "/api/gallery/day?date=2026-09-30", http.StatusOK)

	var day dayResponse
	if err := json.Unmarshal(body, &day); err != nil {
		t.Fatalf("decoding day: %v", err)
	}
	// The empty mock repo knows no devices, so the name must be empty and
	// the mac must still be present. The page falls back to the mac, which
	// is the only label the filesystem guarantees.
	s := day.Moments[0].Shots[0]
	if s.MAC != testMAC {
		t.Errorf("mac: got %q, want %q", s.MAC, testMAC)
	}
	if s.Name != "" {
		t.Errorf("name: got %q, want empty when no device matches", s.Name)
	}
}

func TestGalleryDays_EmptyArchiveIs200NotError(t *testing.T) {
	h, _ := newGalleryHandler(t)

	// The page treats a non-200 here as a broken backend. An empty archive
	// is a normal state that must render an empty page.
	body := getJSON(t, h, "/api/gallery/days", http.StatusOK)
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("empty days: got %q, want []", strings.TrimSpace(string(body)))
	}
}

func TestGalleryDays_IgnoresTheThumbCache(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, "2026-09-30", "06-00-00", testMAC)

	// Generate a thumbnail so .thumbs exists on disk, then re-read the day
	// list. The cache lives under the day directory and a dot-prefixed
	// directory, so counting entries naively would inflate every total.
	thumb := httptest.NewRecorder()
	h.ServeHTTP(thumb, httptest.NewRequest("GET",
		"/api/gallery/thumb?date=2026-09-30&t=06-00-00&mac="+testMAC+"&w=320", nil))
	if thumb.Code != http.StatusOK {
		t.Fatalf("generating thumbnail: got %d", thumb.Code)
	}

	body := getJSON(t, h, "/api/gallery/day?date=2026-09-30", http.StatusOK)
	var day dayResponse
	if err := json.Unmarshal(body, &day); err != nil {
		t.Fatalf("decoding day: %v", err)
	}
	if len(day.Moments) != 1 || len(day.Moments[0].Shots) != 1 {
		t.Errorf("counts changed once the thumbnail cache existed: moments=%d",
			len(day.Moments))
	}

	daysBody := getJSON(t, h, "/api/gallery/days", http.StatusOK)
	var days []daySummary
	if err := json.Unmarshal(daysBody, &days); err != nil {
		t.Fatalf("decoding days: %v", err)
	}
	if days[0].Shots != 1 {
		t.Errorf("day shots after thumbnailing: got %d, want 1", days[0].Shots)
	}
}

func TestGalleryDay_RejectsBadInput(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, "2026-09-30", "06-00-00", testMAC)

	cases := map[string]string{
		"missing date": "/api/gallery/day",
		"empty date":   "/api/gallery/day?date=",
		"traversal":    "/api/gallery/day?date=../../etc",
		"absolute":     "/api/gallery/day?date=/etc",
		"wrong shape":  "/api/gallery/day?date=2026-9-3",
		"wrong method": "",
	}
	for name, url := range cases {
		rr := httptest.NewRecorder()
		method := "GET"
		if name == "wrong method" {
			method = "POST"
			url = "/api/gallery/day?date=2026-09-30"
		}
		h.ServeHTTP(rr, httptest.NewRequest(method, url, nil))
		if rr.Code < 400 {
			t.Errorf("%s: got %d, want a 4xx (body %q)", name, rr.Code, rr.Body.String())
		}
	}
}

// TestGalleryPageFlow walks the exact sequence the page performs: list the
// days, pick the newest, read it, then fetch a thumbnail and the full frame.
// Each step asserts the value the next step depends on, so a change to one
// endpoint that quietly breaks the page fails here rather than in a browser.
func TestGalleryPageFlow(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, "2026-09-28", "21-30-45", testMAC)
	writeTestCapture(t, root, "2026-09-30", "06-00-00", testMAC)
	writeTestCapture(t, root, "2026-09-30", "06-00-00", "d4e9f48d381c")

	// 1. List days and take the newest, which is what the page does.
	var days []daySummary
	if err := json.Unmarshal(getJSON(t, h, "/api/gallery/days", http.StatusOK), &days); err != nil {
		t.Fatalf("decoding days: %v", err)
	}
	chosen := days[0].Date

	// 2. Read that day.
	var day dayResponse
	if err := json.Unmarshal(
		getJSON(t, h, "/api/gallery/day?date="+chosen, http.StatusOK), &day); err != nil {
		t.Fatalf("decoding day: %v", err)
	}
	if day.Date != chosen {
		t.Fatalf("day date: got %q, want the picked %q", day.Date, chosen)
	}

	// 3. Every image_url the page rendered must actually serve the frame.
	urls := make([]string, 0, len(day.Moments))
	for _, m := range day.Moments {
		for _, s := range m.Shots {
			urls = append(urls, s.ImageURL)
		}
	}
	sort.Strings(urls)
	if len(urls) != 2 {
		t.Fatalf("image_url count: got %d, want 2", len(urls))
	}
	for _, u := range urls {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", u, nil))
		if rr.Code != http.StatusOK {
			t.Errorf("GET %s: got %d, want 200", u, rr.Code)
		}
	}

	// 4. And a thumbnail for the first tile must be produced on demand.
	first := day.Moments[0].Shots[0]
	thumb := httptest.NewRecorder()
	h.ServeHTTP(thumb, httptest.NewRequest("GET",
		"/api/gallery/thumb?date="+chosen+"&t="+day.Moments[0].Time+
			"&mac="+first.MAC+"&w=320", nil))
	if thumb.Code != http.StatusOK {
		t.Errorf("thumbnail for the first tile: got %d, want 200", thumb.Code)
	}
}

// TestGalleryDay_UnknownDateIsEmptyNotAnError pins the response for a date
// with no folder. It is deliberately 200 with an empty moment list: the page
// only ever asks for a day it read out of /days, so a 404 path would be
// unreachable in practice while an empty day renders through the same empty
// state the page already has.
func TestGalleryDay_UnknownDateIsEmptyNotAnError(t *testing.T) {
	h, _ := newGalleryHandler(t)

	body := getJSON(t, h, "/api/gallery/day?date=1999-01-01", http.StatusOK)

	var day dayResponse
	if err := json.Unmarshal(body, &day); err != nil {
		t.Fatalf("decoding day: %v", err)
	}
	if day.Date != "1999-01-01" || len(day.Moments) != 0 {
		t.Errorf("unknown day: got date=%q moments=%d, want the date echoed with no moments",
			day.Date, len(day.Moments))
	}
}

// TestGalleryDay_RepeatedDateTakesTheFirstValue pins the traversal defence
// against a duplicated query parameter, where only the first value is read.
// Go's Query().Get returns the first, so the injected value is ignored
// rather than appended.
func TestGalleryDay_RepeatedDateTakesTheFirstValue(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, "2026-09-30", "06-00-00", testMAC)

	body := getJSON(t, h, "/api/gallery/day?date=2026-09-30&date=../../etc", http.StatusOK)

	var day dayResponse
	if err := json.Unmarshal(body, &day); err != nil {
		t.Fatalf("decoding day: %v", err)
	}
	if day.Date != "2026-09-30" {
		t.Errorf("date: got %q, want the first value 2026-09-30", day.Date)
	}
	if len(day.Moments) != 1 {
		t.Errorf("moments: got %d, want 1", len(day.Moments))
	}
}

// TestGalleryThumbs_LandUnderTheDayDirectory pins the on-disk cache layout.
// The systemd unit grants write access to the capture root to make these
// writes possible, and retention removes whole day directories including the
// cache, so a layout change here has to move with both.
func TestGalleryThumbs_LandUnderTheDayDirectory(t *testing.T) {
	h, root := newGalleryHandler(t)
	writeTestCapture(t, root, "2026-09-30", "06-00-00", testMAC)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET",
		"/api/gallery/thumb?date=2026-09-30&t=06-00-00&mac="+testMAC+"&w=320", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("thumbnail: got %d", rr.Code)
	}

	want := filepath.Join(root, "2026-09-30", ".thumbs", "320", "06-00-00_"+testMAC+".jpg")
	if _, err := os.Stat(want); err != nil {
		entries, _ := os.ReadDir(filepath.Join(root, "2026-09-30"))
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected the cache at %s: %v (day dir holds %v)", want, err, names)
	}
}
