package feeddate

import (
	"testing"
	"time"
)

// A corpus of real broken dates, which is TODO 2.5's bar. Every one of these
// shapes has been observed in a production feed.
func TestParseRealCorpus(t *testing.T) {
	cases := []struct {
		in   string
		want string // RFC3339 in UTC
	}{
		// The specifications, when honoured.
		{"2026-07-26T12:30:00Z", "2026-07-26T12:30:00Z"},
		// Fractional seconds are kept, not truncated — two items published in the
		// same second still need a defined order.
		{"2026-07-26T12:30:00.123Z", "2026-07-26T12:30:00.123Z"},
		{"2026-07-26T08:30:00-04:00", "2026-07-26T12:30:00Z"},
		{"Sun, 26 Jul 2026 12:30:00 +0000", "2026-07-26T12:30:00Z"},
		{"Sun, 26 Jul 2026 08:30:00 -0400", "2026-07-26T12:30:00Z"},

		// Single-digit day — the most common deviation by a wide margin.
		{"Sun, 6 Jul 2026 12:30:00 +0000", "2026-07-06T12:30:00Z"},
		{"Sun,  6 Jul 2026 12:30:00 +0000", "2026-07-06T12:30:00Z"},

		// "-0000" is RFC 2822 for "zone unknown", not UTC. Every other reader
		// treats it as UTC and so do we.
		{"Sun, 26 Jul 2026 12:30:00 -0000", "2026-07-26T12:30:00Z"},

		// A numeric offset wearing a name.
		{"Sun, 26 Jul 2026 12:30:00 GMT+0000", "2026-07-26T12:30:00Z"},
		{"Sun, 26 Jul 2026 12:30:00 UTC+0000", "2026-07-26T12:30:00Z"},

		// Named zones Go cannot resolve without a location database.
		{"Sun, 26 Jul 2026 08:30:00 EDT", "2026-07-26T12:30:00Z"},
		{"Sun, 26 Jul 2026 07:30:00 CDT", "2026-07-26T12:30:00Z"},
		{"Sun, 26 Jul 2026 05:30:00 PDT", "2026-07-26T12:30:00Z"},
		{"Sun, 26 Jul 2026 13:30:00 BST", "2026-07-26T12:30:00Z"},
		{"Sun, 26 Jul 2026 18:00:00 IST", "2026-07-26T12:30:00Z"},

		// No zone at all: read as UTC, never as server-local.
		{"Sun, 26 Jul 2026 12:30:00", "2026-07-26T12:30:00Z"},
		{"2026-07-26 12:30:00", "2026-07-26T12:30:00Z"},
		{"2026-07-26T12:30:00", "2026-07-26T12:30:00Z"},

		// No seconds.
		{"Sun, 26 Jul 2026 12:30 +0000", "2026-07-26T12:30:00Z"},

		// Date only.
		{"2026-07-26", "2026-07-26T00:00:00Z"},
		{"Sun, 26 Jul 2026", "2026-07-26T00:00:00Z"},
		{"July 26, 2026", "2026-07-26T00:00:00Z"},
		{"2026/07/26", "2026-07-26T00:00:00Z"},

		// No weekday.
		{"26 Jul 2026 12:30:00 +0000", "2026-07-26T12:30:00Z"},

		// A trailing RFC 2822 comment.
		{"Sun, 26 Jul 2026 12:30:00 +0000 (UTC)", "2026-07-26T12:30:00Z"},

		// Leading and trailing whitespace, which XML parsers hand through.
		{"  2026-07-26T12:30:00Z  ", "2026-07-26T12:30:00Z"},

		// WordPress-style SQL datetime with an offset.
		{"2026-07-26 08:30:00 -0400", "2026-07-26T12:30:00Z"},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := Parse(c.in)
			if err != nil {
				t.Fatalf("Parse(%q) failed: %v", c.in, err)
			}
			want, err := time.Parse(time.RFC3339, c.want)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(want) {
				t.Errorf("Parse(%q) = %s, want %s", c.in, got.Format(time.RFC3339), c.want)
			}
			if got.Location() != time.UTC {
				t.Errorf("Parse(%q) returned %v, not UTC", c.in, got.Location())
			}
		})
	}
}

// Failure is routine, not exceptional: the caller falls back to first-seen.
// What matters is that it fails rather than inventing a date.
func TestParseRejectsGarbage(t *testing.T) {
	for _, s := range []string{
		"", "   ", "not a date", "yesterday", "Sun, 99 Xxx 2026", "0000-00-00",
	} {
		if got, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) should have failed, got %s", s, got)
		}
	}
}

// MustParse is the fixture helper — it must return the same instant Parse
// does on the success path.
func TestMustParseReturnsOnSuccess(t *testing.T) {
	got := MustParse("2026-07-26T12:30:00Z")
	want, _ := time.Parse(time.RFC3339, "2026-07-26T12:30:00Z")
	if !got.Equal(want) {
		t.Errorf("MustParse = %s, want %s", got, want)
	}
}

// MustParse exists for fixtures, where an unparseable literal is a typo in the
// test, not routine feed garbage — it must panic loudly rather than return a
// zero time that silently passes downstream assertions.
func TestMustParsePanicsOnGarbage(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustParse did not panic on unparseable input")
		}
	}()
	MustParse("not a date")
}

// A loose layout must never steal a match from a precise one — that is what the
// ordering in `layouts` is for.
func TestPreciseLayoutsWinOverLooseOnes(t *testing.T) {
	got, err := Parse("2026-07-26T12:30:45.678Z")
	if err != nil {
		t.Fatal(err)
	}
	if got.Second() != 45 {
		t.Errorf("seconds lost to a looser layout: %s", got.Format(time.RFC3339))
	}
}
