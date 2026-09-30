// Package surveillance implements the periodic-background surveillance
// job. Every Interval() minutes it enumerates online witsaba devices,
// calls GET /capture on each, and persists the resulting JPEG under
// the configured root directory (default ~/.witsaba/cameras/).
//
// The package owns three seams that the tests depend on:
//
//   - Clock     — the time source; defaults to time.Now() in
//                 production and to a fake clock in unit tests.
//   - HTTP client — supplied by the Job; defaults to a fresh
//                 http.Client per tick with DisableKeepAlives: true.
//   - Storage   — the filesystem layer; defaults to disk but tests
//                 can swap in a tmpfs-backed implementation.
//
// The surveillance job does NOT emit DiscoveryEvents — the
// discovery job already populates the witsaba.devices table. The
// surveillance job only reads it. Keeping the shared event channel
// clean of non-discovery events is intentional.
package surveillance

import "time"

// Clock abstracts the time source so tests can pin "now" to
// deterministic boundary values for the flash window. The default
// implementation is realTimeClock{}, used by NewJob.
type Clock interface {
	// Now returns the current local time. Workers run on the Pi
	// which has a properly configured local timezone; we never
	// call UTC() here on purpose so the human operator's mental
	// model (17:45 == "after dusk") matches the code.
	Now() time.Time
}

// realTimeClock is the production Clock — defers to time.Now.
type realTimeClock struct{}

func (realTimeClock) Now() time.Time { return time.Now() }

// Window describes the half-open hour-of-day range during which
// the worker passes ?flash=1 to the camera. The window is
// intentionally NOT timezone-aware: we run on the Pi and rely on
// the operator having set the timezone correctly via /etc/localtime.
//
// Minutes are within the hour so we never have to deal with
// "1:30 AM on the second day" semantics. The start minute defaults
// to 45 (17:45 → 5:45 inclusive of the 17:00–17:44 half-hour
// boundary). The end minute defaults to 45 (the 5:45–6:00 half
// hour is the bright zone).
type Window struct {
	// StartHour is 0–23. Flash is on when the current hour is
	// greater than StartHour OR we are inside the EndHour "morning"
	// slot.
	StartHour int
	// StartMinute defaults to 45 in NewWindow. The window starts
	// at (StartHour, StartMinute) local time.
	StartMinute int
	// EndHour is 0–23. Must be less than StartHour (the window
	// crosses midnight). 5 with EndMinute 45 means "up to and
	// including 05:44:59 local time".
	EndHour int
	// EndMinute defaults to 45 in NewWindow. The window ends at
	// (EndHour, EndMinute) — i.e. the window is "EndHour:EndMinute"
	// exclusive.
	EndMinute int
}

// NewWindow returns a sensible default: 17:45 → 05:45. The user's
// standing direction is "flash on between 17:45 and 05:45 local
// time"; we treat that as a compile-time constant here and let the
// operator override via env vars in T7 if they need to.
func NewWindow() Window {
	return Window{
		StartHour:   17,
		StartMinute: 45,
		EndHour:     5,
		EndMinute:   45,
	}
}

// Contains reports whether the given local time falls inside the
// flash window. The window is interpreted as:
//
//	[start at (StartHour, StartMinute), end at (EndHour, EndMinute))
//
// where the start is later in the day than the end (i.e. crosses
// midnight). 17:45 → 05:45 means "from 17:45 today to 05:45
// tomorrow morning".
//
// Boundary semantics: 17:45:00 IS inside the window. 05:45:00 IS
// NOT inside the window (it's the first instant outside). The
// operator direction was "between 17:45 and 5:45" — we read that
// as half-open at both ends for the evening, closed-open for the
// morning, matching the typical CCTV dusk-to-dawn convention.
func (w Window) Contains(t time.Time) bool {
	h, m, _ := t.Clock() // local time components
	total := h*60 + m
	start := w.StartHour*60 + w.StartMinute
	end := w.EndHour*60 + w.EndMinute

	// Cross-midnight window: start > end. Either inside the
	// evening half (total >= start) or inside the morning half
	// (total < end).
	if start > end {
		return total >= start || total < end
	}
	// Same-day window (rare; included for completeness).
	return total >= start && total < end
}

// CaptureRequest is what the worker hands to captureOne. One
// CaptureRequest per device per tick. MAC and SourceIP come from
// the witsaba.devices row read by the Job; Flash is computed by
// the Job via Window.Contains(now); Timeout is the per-request
// HTTP timeout (default 10 s).
type CaptureRequest struct {
	MAC      string
	SourceIP string
	Flash    bool
	Timeout  time.Duration
}

// CaptureResult is what captureOne returns. On a successful HTTP
// 200, Body holds the JPEG bytes and Err is nil. On any other
// outcome, Body is nil and Err is non-nil. StatusCode is the
// HTTP status, or 0 if the request never reached the server.
type CaptureResult struct {
	MAC        string
	SourceIP   string
	Flash      bool
	StatusCode int
	Body       []byte
	Elapsed    time.Duration
	Err        error
}
