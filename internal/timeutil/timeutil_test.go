package timeutil

import (
	"testing"
	"time"
)

func TestClampPublished(t *testing.T) {
	seen := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	good := time.Date(2026, 7, 25, 9, 30, 0, 0, time.UTC)

	cases := []struct {
		name    string
		claimed time.Time
		want    time.Time
	}{
		{"a plausible date is believed", good, good},
		{"no date falls back to first seen", time.Time{}, seen},
		{"epoch zero is not a date", time.Unix(0, 0).UTC(), seen},
		{"the feed that claims 2087", time.Date(2087, 1, 1, 0, 0, 0, 0, time.UTC), seen},
		// A publisher in UTC+14 with a zone bug legitimately looks ahead of us.
		{"a few hours ahead is a timezone, not a lie",
			seen.Add(10 * time.Hour), seen.Add(10 * time.Hour)},
		{"a week ahead is a lie", seen.Add(7 * 24 * time.Hour), seen},
		{"1970 is below the floor", time.Date(1971, 5, 1, 0, 0, 0, 0, time.UTC), seen},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClampPublished(c.claimed, seen); !got.Equal(c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestWindowAcrossDST is the one that TODO 2.1 calls out: a 22:00-07:00 window
// must be correct on both a spring-forward and a fall-back date.
func TestWindowAcrossDST(t *testing.T) {
	w, err := ParseWindow("22:00-07:00", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	ny, _ := time.LoadLocation("America/New_York")

	cases := []struct {
		name string
		when time.Time
		want bool
	}{
		// 2026-03-08 is spring forward in the US: 02:00 -> 03:00.
		{"01:30 the night of spring forward", time.Date(2026, 3, 8, 1, 30, 0, 0, ny), true},
		{"03:30 after the gap", time.Date(2026, 3, 8, 3, 30, 0, 0, ny), true},
		{"08:00 the morning after", time.Date(2026, 3, 8, 8, 0, 0, 0, ny), false},
		// 2026-11-01 is fall back: 01:00-02:00 happens twice, and quiet hours
		// must be quiet both times through.
		{"first 01:30 on fall-back night", time.Date(2026, 11, 1, 1, 30, 0, 0, ny), true},
		{"midday is never quiet", time.Date(2026, 7, 1, 13, 0, 0, 0, ny), false},
		{"22:00 is the inclusive start", time.Date(2026, 7, 1, 22, 0, 0, 0, ny), true},
		{"07:00 is the exclusive end", time.Date(2026, 7, 1, 7, 0, 0, 0, ny), false},
		{"06:59 is still inside", time.Date(2026, 7, 1, 6, 59, 0, 0, ny), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := w.Contains(c.when); got != c.want {
				t.Errorf("Contains(%s) = %v, want %v", c.when, got, c.want)
			}
		})
	}
}

// The same instant is inside or outside depending on the reader's zone. This is
// the whole reason windows carry an IANA name.
func TestWindowIsZoneRelative(t *testing.T) {
	instant := time.Date(2026, 7, 1, 3, 0, 0, 0, time.UTC) // 23:00 in NY, 12:00 in Tokyo

	ny, _ := ParseWindow("22:00-07:00", "America/New_York")
	tokyo, _ := ParseWindow("22:00-07:00", "Asia/Tokyo")

	if !ny.Contains(instant) {
		t.Error("23:00 in New York should be inside quiet hours")
	}
	if tokyo.Contains(instant) {
		t.Error("noon in Tokyo should not be inside quiet hours")
	}
}

func TestNonWrappingAndEmptyWindows(t *testing.T) {
	utc := time.UTC
	day, err := ParseWindow("09:00-17:00", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if !day.Contains(time.Date(2026, 7, 1, 12, 0, 0, 0, utc)) {
		t.Error("noon should be inside 09:00-17:00")
	}
	if day.Contains(time.Date(2026, 7, 1, 20, 0, 0, 0, utc)) {
		t.Error("evening should be outside 09:00-17:00")
	}
	// An empty window is empty, not a whole day — the difference between
	// "notifications never" and "notifications never arrive".
	empty := Window{StartMin: 600, EndMin: 600, Zone: "UTC"}
	if empty.Contains(time.Date(2026, 7, 1, 10, 0, 0, 0, utc)) {
		t.Error("an empty window contains nothing")
	}
}

// A digest scheduled at 02:30 has no valid instant on a spring-forward date. It
// must skip to the next day, not silently fire an hour late.
func TestNextOccurrenceSkipsTheDSTGap(t *testing.T) {
	w := Window{Zone: "America/New_York"}
	ny, _ := time.LoadLocation("America/New_York")

	after := time.Date(2026, 3, 8, 0, 15, 0, 0, ny)
	got, ok := w.NextOccurrence(after, 2, 30)
	if !ok {
		t.Fatal("expected an occurrence within a week")
	}
	inNY := got.In(ny)
	if inNY.Day() != 9 {
		t.Errorf("02:30 does not exist on 2026-03-08; expected the 9th, got %s", inNY)
	}
	if inNY.Hour() != 2 || inNY.Minute() != 30 {
		t.Errorf("wall clock drifted: %s", inNY)
	}
}

func TestParseWindowRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "22:00", "22:00–07:00", "25:00-07:00", "2200-0700"} {
		if _, err := ParseWindow(s, "UTC"); err == nil {
			t.Errorf("ParseWindow(%q) should have failed", s)
		}
	}
	if _, err := ParseWindow("22:00-07:00", "Mars/Olympus"); err == nil {
		t.Error("an unknown zone should fail at parse time, not at evaluation time")
	}
}
