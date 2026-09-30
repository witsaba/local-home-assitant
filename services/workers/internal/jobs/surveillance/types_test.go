// types_test.go — unit tests for Window.Contains and related helpers.
package surveillance

import (
	"testing"
	"time"
)

func TestWindow_NewWindow_DefaultsAreRight(t *testing.T) {
	t.Parallel()

	w := NewWindow()
	if w.StartHour != 17 || w.StartMinute != 45 {
		t.Errorf("default start = %d:%02d, want 17:45", w.StartHour, w.StartMinute)
	}
	if w.EndHour != 5 || w.EndMinute != 45 {
		t.Errorf("default end = %d:%02d, want 05:45", w.EndHour, w.EndMinute)
	}
}

func TestWindow_Contains_BoundarySemantics(t *testing.T) {
	t.Parallel()

	w := NewWindow() // 17:45 → 05:45
	cases := []struct {
		name string
		h, m int
		want bool
	}{
		// Bright zone (no flash).
		{"12:00 noon", 12, 0, false},
		{"13:30", 13, 30, false},
		{"16:00", 16, 0, false},
		{"17:30 — 15min before start", 17, 30, false},
		{"17:44 — one minute before start", 17, 44, false},
		{"17:44:59.999", 17, 44, false},

		// Evening transition (flash on).
		{"17:45 — exact start, inclusive", 17, 45, true},
		{"17:46", 17, 46, true},
		{"18:00", 18, 0, true},
		{"23:59", 23, 59, true},
		{"00:00 — midnight", 0, 0, true},
		{"02:30", 2, 30, true},
		{"04:00", 4, 0, true},
		{"05:30 — 15min before end", 5, 30, true},
		{"05:44 — one minute before end", 5, 44, true},
		{"05:44:59 — last instant inside", 5, 44, true},

		// Morning transition (flash off).
		{"05:45 — exact end, exclusive", 5, 45, false},
		{"05:46", 5, 46, false},
		{"06:00", 6, 0, false},
		{"08:00", 8, 0, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Date is irrelevant; we only inspect h:m. Use a
			// fixed reference date and let the local-clock helper
			// extract the components.
			now := time.Date(2026, 9, 30, tc.h, tc.m, 0, 0, time.Local)
			got := w.Contains(now)
			if got != tc.want {
				t.Errorf("Window{17:45->05:45}.Contains(%02d:%02d) = %v, want %v",
					tc.h, tc.m, got, tc.want)
			}
		})
	}
}

func TestWindow_Contains_SameDayWindow(t *testing.T) {
	t.Parallel()

	// Edge case: window does NOT cross midnight (e.g. 09:00 → 17:00).
	// Documents the inclusive-exclusive interpretation.
	w := Window{StartHour: 9, StartMinute: 0, EndHour: 17, EndMinute: 0}
	cases := []struct {
		h, m int
		want bool
	}{
		{8, 59, false},
		{9, 0, true},
		{12, 0, true},
		{16, 59, true},
		{17, 0, false}, // exclusive end
		{17, 1, false},
	}
	for _, tc := range cases {
		now := time.Date(2026, 9, 30, tc.h, tc.m, 0, 0, time.Local)
		if got := w.Contains(now); got != tc.want {
			t.Errorf("Window{09:00->17:00}.Contains(%02d:%02d) = %v, want %v",
				tc.h, tc.m, got, tc.want)
		}
	}
}

func TestShouldFlash_DelegatesToWindow(t *testing.T) {
	t.Parallel()

	w := NewWindow()
	midnight := time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local)
	noon := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)

	if !shouldFlash(midnight, w) {
		t.Error("shouldFlash(midnight) = false, want true")
	}
	if shouldFlash(noon, w) {
		t.Error("shouldFlash(noon) = true, want false")
	}
}

// fakeClock implements Clock for tests.
type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time { return f.now }

func TestRealTimeClock_NowIsReasonable(t *testing.T) {
	t.Parallel()

	c := realTimeClock{}
	before := time.Now().Add(-time.Second)
	got := c.Now()
	after := time.Now().Add(time.Second)
	if got.Before(before) || got.After(after) {
		t.Errorf("realTimeClock.Now() = %v, want within [%v, %v]",
			got, before, after)
	}
}

func TestFakeClock_Now(t *testing.T) {
	t.Parallel()

	want := time.Date(2026, 9, 30, 21, 30, 0, 0, time.Local)
	c := &fakeClock{now: want}
	if got := c.Now(); !got.Equal(want) {
		t.Errorf("fakeClock.Now() = %v, want %v", got, want)
	}
}
