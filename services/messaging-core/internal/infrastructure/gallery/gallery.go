// Package gallery reads the periodic surveillance captures that the
// workers service writes to disk, and exposes them for browsing.
//
// # Where the data comes from
//
// The surveillance job in services/workers writes one JPEG per camera per
// tick to <root>/<YYYY-MM-DD>/<HH-MM-SS>_<mac>.jpg, where the timestamp is
// the scheduler tick start in Pi local time and is therefore shared by every
// camera in that tick. odd/tasks/surveillance-worker.md recorded "no index in
// Postgres, the filesystem is the index" as a deliberate non-goal, and this
// package honors that: a day view is a single ReadDir of one folder plus a
// regexp. There is no manifest to keep in sync and no row to migrate.
//
// # Path safety
//
// No caller-supplied string ever reaches the filesystem. Dates, tick times
// and MACs are matched against strict patterns and then parsed, and the path
// is constructed from the validated pieces. withinRoot is a second, redundant
// check on the result. Both exist because a gallery endpoint is the most
// tempting traversal target in the service, and the cost of the belt-and-
// braces check is a few nanoseconds compared to the cost of getting it wrong.
package gallery

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Layout constants. The day folder and the tick prefix must stay in sync with
// surveillance.storage.PathFor; thumbDirName must stay in sync with the
// retention prune that owns it.
const (
	dayLayout  = "2006-01-02"
	tickLayout = "15-04-05"
	tickSuffix = ".jpg"

	// thumbDirName is the per-day thumbnail cache directory, created
	// lazily by the thumbnail endpoint. It is a dot-directory so that
	// *.jpg globs cannot match inside it, and it lives inside the day
	// folder so the retention prune removes it for free.
	thumbDirName = ".thumbs"
)

// Strict patterns. A tick or MAC accepted here is used verbatim in a path,
// so the character classes are deliberately narrow: digits, dashes and
// lowercase hex only. No dots, no separators, no uppercase, no percent.
var (
	dayRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	tickRe = regexp.MustCompile(`^\d{2}-\d{2}-\d{2}$`)
	macRe  = regexp.MustCompile(`^[0-9a-f]{12}$`)

	// frameRe parses a capture filename back into its two components.
	frameRe = regexp.MustCompile(`^(\d{2}-\d{2}-\d{2})_([0-9a-f]{12})\.jpg$`)
)

// ValidateDate checks that raw is a well-formed calendar date.
//
// Both layers are required and neither is redundant: dayRe alone accepts
// 2026-13-45, and time.Parse alone is what makes a syntactically valid but
// non-existent day fail. A day that cannot be parsed must never reach the
// filesystem, because it would produce a path that silently matches nothing.
func ValidateDate(raw string) error {
	if !dayRe.MatchString(raw) {
		return fmt.Errorf("date %q must match YYYY-MM-DD", raw)
	}
	if _, err := time.Parse(dayLayout, raw); err != nil {
		return fmt.Errorf("date %q is not a real calendar date", raw)
	}
	return nil
}

// ValidateTick checks that raw is a well-formed HH-MM-SS tick time.
func ValidateTick(raw string) error {
	if !tickRe.MatchString(raw) {
		return fmt.Errorf("time %q must match HH-MM-SS", raw)
	}
	if _, err := time.Parse(tickLayout, raw); err != nil {
		return fmt.Errorf("time %q is not a real clock time", raw)
	}
	return nil
}

// ValidateMAC checks that raw is a 12-character lowercase hex MAC.
func ValidateMAC(raw string) error {
	if !macRe.MatchString(raw) {
		return fmt.Errorf("mac %q must be 12 lowercase hex characters", raw)
	}
	return nil
}

// Store reads a capture tree. It is read-only: no method here creates,
// modifies or removes a capture. The thumbnail cache in U3 is the single
// intended exception and lives in its own type.
type Store struct {
	root string
}

// NewStore returns a Store rooted at dir. The directory is not required to
// exist at construction; Days treats a missing root as an empty archive,
// because "no captures yet" is a normal state for a fresh install, not an
// error worth surfacing as a 500.
func NewStore(dir string) *Store {
	return &Store{root: dir}
}

// Root returns the configured capture root.
func (s *Store) Root() string { return s.root }

// validateAll validates the three components of a capture address in one
// call so a handler cannot forget one.
func validateAll(date, tick, mac string) error {
	if err := ValidateDate(date); err != nil {
		return err
	}
	if err := ValidateTick(tick); err != nil {
		return err
	}
	return ValidateMAC(mac)
}

// framePath builds the on-disk path of one full frame.
//
// The three inputs are validated before the path is assembled, so the result
// is root/<digits>/<digits>_<hex>.jpg by construction. withinRoot then
// re-checks the result: it should be unreachable, which is the point of
// having it.
func (s *Store) framePath(date, tick, mac string) (string, error) {
	if err := validateAll(date, tick, mac); err != nil {
		return "", err
	}
	p := filepath.Join(s.root, date, tick+"_"+mac+tickSuffix)
	if !withinRoot(s.root, p) {
		return "", fmt.Errorf("resolved path escapes the capture root")
	}
	return p, nil
}

// thumbPath builds the on-disk path of one cached thumbnail. The .thumbs
// directory is not created here; the caller does that only on a cache miss.
func (s *Store) thumbPath(date, tick, mac string) (string, error) {
	if err := validateAll(date, tick, mac); err != nil {
		return "", err
	}
	p := filepath.Join(s.root, date, thumbDirName, tick+"_"+mac+tickSuffix)
	if !withinRoot(s.root, p) {
		return "", fmt.Errorf("resolved thumbnail path escapes the capture root")
	}
	return p, nil
}

// withinRoot reports whether p is root or lives beneath it.
//
// filepath.Clean on both sides removes any ".." before the comparison, so
// the prefix test is a real containment test rather than a string match.
// The separator is appended to the root before testing so that a sibling
// directory sharing a name prefix -- /root/cameras-evil versus /root/cameras
// -- cannot pass.
func withinRoot(root, p string) bool {
	cleanRoot := filepath.Clean(root)
	cleanP := filepath.Clean(p)
	if cleanP == cleanRoot {
		return true
	}
	return strings.HasPrefix(cleanP, cleanRoot+string(filepath.Separator))
}

// DaySummary is one day folder in the archive.
type DaySummary struct {
	// Date is the YYYY-MM-DD folder name.
	Date string `json:"date"`
	// Shots is the number of capture files in the day.
	Shots int `json:"shots"`
	// Cameras is the number of distinct MACs seen in the day.
	Cameras int `json:"cameras"`
	// Bytes is the summed size of those files.
	Bytes int64 `json:"bytes"`
}

// ListDays returns the day folders present under the root, newest first.
//
// Directories that do not parse as a date are ignored rather than reported:
// the cache directory and any operator-created folder are not part of the
// archive, and a gallery should not render them as broken days. A day with
// no readable frames is still listed, with Shots 0, because the folder
// existing is meaningful on its own.
func (s *Store) ListDays() ([]DaySummary, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		// A missing root is an empty archive, not a failure. Every
		// other error -- a permission problem, a root that is a file
		// rather than a directory -- is surfaced, because silently
		// returning an empty archive would look identical to "no
		// captures yet" and hide a real fault.
		if os.IsNotExist(err) {
			return []DaySummary{}, nil
		}
		return nil, fmt.Errorf("reading capture root %q: %w", s.root, err)
	}

	var out []DaySummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if ValidateDate(name) != nil {
			continue
		}
		sum, err := s.summarizeDay(name)
		if err != nil {
			// One unreadable day must not blank the whole picker.
			// Skip it and keep going; the day simply does not
			// appear.
			continue
		}
		out = append(out, sum)
	}

	sortNewestFirst(out)
	return out, nil
}

// summarizeDay counts the frames in one day folder.
func (s *Store) summarizeDay(date string) (DaySummary, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, date))
	if err != nil {
		return DaySummary{}, err
	}

	sum := DaySummary{Date: date}
	macs := make(map[string]struct{})
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := frameRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		macs[m[2]] = struct{}{}

		// A frame that vanishes between ReadDir and Stat is a
		// concurrent retention prune, not a fault. Count the shot
		// with an unknown size rather than failing the day.
		if info, err := e.Info(); err == nil {
			sum.Bytes += info.Size()
		}
		sum.Shots++
	}
	sum.Cameras = len(macs)
	return sum, nil
}

// sortNewestFirst orders day summaries by descending date. The date layout
// is lexicographically sortable, so a string compare is a date compare.
func sortNewestFirst(days []DaySummary) {
	for i := 1; i < len(days); i++ {
		for j := i; j > 0 && days[j-1].Date < days[j].Date; j-- {
			days[j-1], days[j] = days[j], days[j-1]
			continue
		}
	}
}

// Camera is one device in the archive, with its display name if known.
type Camera struct {
	MAC  string `json:"mac"`
	Name string `json:"name"`
}

// Shot is one captured frame within a moment.
type Shot struct {
	MAC      string `json:"mac"`
	Name     string `json:"name"`
	Bytes    int64  `json:"bytes"`
	ImageURL string `json:"image_url"`
}

// Moment is every frame captured at one scheduler tick. Because the tick
// timestamp is stamped on each camera in the tick, the filename prefix is
// the moment key and no extra grouping state is needed.
type Moment struct {
	Time  string `json:"time"`
	Shots []Shot `json:"shots"`
}

// Day is the full response for one day.
type Day struct {
	Date    string   `json:"date"`
	Cameras []Camera `json:"cameras"`
	Moments []Moment `json:"moments"`
}

// maxDayShots bounds a single day response.
//
// 288 shots is one day at the 15 minute default for three cameras, and a
// day at 5 minutes for two. The cap is a defense against a misconfigured
// interval writing tens of thousands of files into one folder, which would
// make the response expensive to build on a Pi. It is reported as an error
// rather than silently truncated, because a partial day that looks complete
// is worse than an explicit refusal.
const maxDayShots = 4096

// Day returns the archive for one day, newest moment last.
//
// nameByMAC supplies display names for archived frames. The map is read
// only, and a MAC that is absent keeps an empty name: the API never invents
// one, because a plausible-looking fake name is worse than an honest
// fallback to the MAC in the UI.
func (s *Store) Day(date string, nameByMAC map[string]string) (*Day, error) {
	if err := ValidateDate(date); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(filepath.Join(s.root, date))
	if err != nil {
		if os.IsNotExist(err) {
			// A day that has not happened yet is a normal answer,
			// not a 404. The page uses an empty state for it.
			return &Day{Date: date, Cameras: []Camera{}, Moments: []Moment{}}, nil
		}
		return nil, fmt.Errorf("reading day %q: %w", date, err)
	}

	// ReadDir returns entries sorted by filename, and the filename
	// starts with the zero-padded tick time, so a single pass in
	// directory order yields chronological moments. The explicit group
	// preserves that order while collapsing cameras into moments.
	var order []string
	grouped := make(map[string][]Shot)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := frameRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		tick, mac := m[1], m[2]

		shot := Shot{MAC: mac, Name: nameByMAC[mac], ImageURL: ImageURL(date, tick, mac)}
		// A frame pruned between ReadDir and Info still counts as a
		// shot; only its size is unknown.
		if info, err := e.Info(); err == nil {
			shot.Bytes = info.Size()
		}
		if _, seen := grouped[tick]; !seen {
			order = append(order, tick)
		}
		grouped[tick] = append(grouped[tick], shot)
	}

	total := 0
	for _, shots := range grouped {
		total += len(shots)
	}
	if total > maxDayShots {
		return nil, fmt.Errorf("day %q holds %d frames, over the %d cap", date, total, maxDayShots)
	}

	day := &Day{Date: date, Cameras: []Camera{}, Moments: make([]Moment, 0, len(order))}
	seenMAC := make(map[string]struct{}, len(grouped))
	for _, tick := range order {
		day.Moments = append(day.Moments, Moment{Time: tick, Shots: grouped[tick]})
		for _, s := range grouped[tick] {
			if _, dup := seenMAC[s.MAC]; dup {
				continue
			}
			seenMAC[s.MAC] = struct{}{}
			day.Cameras = append(day.Cameras, Camera{MAC: s.MAC, Name: s.Name})
		}
	}
	return day, nil
}

// ImageURL is the API path of one full frame. It is exported because the
// response is serialized from this package and the URL shape is part of the
// public contract the page is written against.
func ImageURL(date, tick, mac string) string {
	return "/api/gallery/img?date=" + date + "&t=" + tick + "&mac=" + mac
}

// ThumbURL is the API path of one thumbnail.
//
// The width is a requested longest edge, not a contract: the server may
// return a smaller image when re-encoding cannot reach it exactly, so a
// client must treat the result as a hint rather than a guarantee. The
// server is the only thing that decides the final size, which is what keeps
// an arbitrary caller from selecting an expensive encode.
func ThumbURL(date, tick, mac string, width int) string {
	return "/api/gallery/thumb?date=" + date + "&t=" + tick + "&mac=" + mac + "&w=" + strconv.Itoa(width)
}
