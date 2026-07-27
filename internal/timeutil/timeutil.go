// Package timeutil holds the time rules the rest of ArticleFlux depends on.
//
// Three of them, and each exists because getting it wrong is silent:
//
//   - Everything stored is UTC. A feed reader ingests timestamps from strangers;
//     the only sane policy is to normalise once at the edge.
//   - Wall-clock windows (quiet hours, digest times, idle triggers) resolve
//     against an IANA zone, never a fixed offset. "22:00–07:00" means 22:00 local
//     on both sides of a DST boundary, which a UTC offset cannot express.
//   - Feeds lie about dates. ClampPublished is the guard.
package timeutil

import (
	"errors"
	"time"
)

// Now is the clock. Tests replace it; nothing else should.
var Now = func() time.Time { return time.Now().UTC() }

// UTC normalises to UTC, which is the only form that gets stored (§22.9).
func UTC(t time.Time) time.Time { return t.UTC() }

// MaxSkew is how far into the future a feed may claim an item was published
// before we stop believing it.
//
// One day, not one hour: a publisher in UTC+14 with a timezone bug can look ~14h
// ahead of a reader in UTC, and treating that as a lie would clamp every item
// from an entire hemisphere. Beyond a day it is a bug in their feed, not a zone.
const MaxSkew = 24 * time.Hour

// MinPublished is the floor. Feeds emit epoch-zero for "no date" often enough
// that a 1970 item at the top of a list is a recognisable failure mode.
var MinPublished = time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)

// ClampPublished decides what published_at should be, given what the feed
// claimed and when we first saw the item.
//
// The rule: a claimed date is believed only if it is plausible. Implausible
// means zero, before MinPublished, or more than MaxSkew after firstSeen — the
// three shapes that actually occur (missing date, epoch zero, and the feed that
// claims 2087). In every implausible case the answer is firstSeen, because "when
// we saw it" is the one timestamp we can vouch for.
//
// This matters more than it looks: published_at is the sort key for every list in
// the application (§6.5), so one item claiming 2087 pins itself to the top of
// somebody's feed forever.
func ClampPublished(claimed, firstSeen time.Time) time.Time {
	firstSeen = firstSeen.UTC()
	if claimed.IsZero() {
		return firstSeen
	}
	claimed = claimed.UTC()
	if claimed.Before(MinPublished) {
		return firstSeen
	}
	if claimed.After(firstSeen.Add(MaxSkew)) {
		return firstSeen
	}
	return claimed
}

// Window is a wall-clock range in a named zone, e.g. quiet hours 22:00–07:00 in
// America/New_York. It may wrap midnight; that is the common case.
type Window struct {
	StartMin int    // minutes past local midnight, 0..1439
	EndMin   int    // exclusive
	Zone     string // IANA name
}

// ParseWindow reads "HH:MM-HH:MM" in an IANA zone.
func ParseWindow(s, zone string) (Window, error) {
	if len(s) != 11 || s[5] != '-' {
		return Window{}, errors.New(`window must look like "22:00-07:00"`)
	}
	start, err := parseHM(s[:5])
	if err != nil {
		return Window{}, err
	}
	end, err := parseHM(s[6:])
	if err != nil {
		return Window{}, err
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return Window{}, err
	}
	return Window{StartMin: start, EndMin: end, Zone: zone}, nil
}

func parseHM(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Contains reports whether an instant falls inside the window, evaluated in the
// window's own zone.
//
// The DST cases this has to survive, and why the implementation converts to local
// wall-clock rather than doing arithmetic on UTC:
//
//   - Spring forward: 02:00–03:00 local does not exist. An instant simply never
//     has that wall-clock reading, so a window covering it is never entered —
//     correct, and it falls out for free.
//   - Fall back: 01:00–02:00 local happens twice. Both occurrences read the same
//     wall-clock time, so both are in the window — also correct. Quiet hours
//     should be quiet both times through.
func (w Window) Contains(t time.Time) bool {
	loc, err := time.LoadLocation(w.Zone)
	if err != nil {
		return false
	}
	lt := t.In(loc)
	min := lt.Hour()*60 + lt.Minute()
	if w.StartMin == w.EndMin {
		return false // an empty window is empty, not a whole day
	}
	if w.StartMin < w.EndMin {
		return min >= w.StartMin && min < w.EndMin
	}
	// Wraps midnight.
	return min >= w.StartMin || min < w.EndMin
}

// NextOccurrence returns the next instant at or after `after` whose local
// wall-clock reading is hour:minute in the window's zone. This is what schedules
// a daily digest.
//
// It searches forward day by day rather than adding 24h, because a day is not
// always 24 hours. On a spring-forward date a 02:30 digest has no valid instant;
// the search skips to the next day rather than silently firing at 03:30.
func (w Window) NextOccurrence(after time.Time, hour, minute int) (time.Time, bool) {
	loc, err := time.LoadLocation(w.Zone)
	if err != nil {
		return time.Time{}, false
	}
	lt := after.In(loc)
	for i := 0; i < 8; i++ {
		d := lt.AddDate(0, 0, i)
		cand := time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, loc)
		// If the wall-clock time does not exist, time.Date normalises it into a
		// different reading. Rejecting the mismatch is what skips the gap.
		if cand.Hour() != hour || cand.Minute() != minute {
			continue
		}
		if cand.After(after) {
			return cand.UTC(), true
		}
	}
	return time.Time{}, false
}
