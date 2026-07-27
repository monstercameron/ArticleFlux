package feeddate

import "testing"

// FuzzParse feeds arbitrary strings through Parse — every date this package
// ever sees came out of a feed a stranger controls. The only universal
// invariant is "never panics"; failure is a normal, expected outcome (the
// caller falls back to first-seen), so this does not assert success or
// failure on any particular input, only that the function returns.
//
// A UTC invariant is checked on the success path: Parse's contract is that a
// date with no zone reads as UTC rather than server-local (see the package
// comment), and every returned time.Time must be in that zone regardless of
// which layout matched.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"2026-07-26T12:30:00Z", "Sun, 26 Jul 2026 12:30:00 +0000",
		"Sun, 6 Jul 2026 12:30:00 +0000", "Sun, 26 Jul 2026 12:30:00 -0000",
		"Sun, 26 Jul 2026 12:30:00 GMT+0000", "Sun, 26 Jul 2026 08:30:00 EDT",
		"2026-07-26", "26 Jul 2026 12:30:00 +0000",
		"Sun, 26 Jul 2026 12:30:00 +0000 (UTC)", "", "not a date",
		"Sun, 99 Xxx 2026", "0000-00-00", "01/02/2006",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Parse(%q) panicked: %v", s, r)
			}
		}()
		got, err := Parse(s)
		if err != nil {
			return
		}
		if got.Location() != nil && got.Location().String() != "UTC" {
			t.Fatalf("Parse(%q) = %v, not in UTC", s, got)
		}
	})
}
