package main

import "testing"

// The prose test is the whole of guard 5's judgement, and it had no test at all
// — which is how it came to demand that a shell command be translated while
// letting the three one-word commands beside it through. A heuristic that
// decides what must go in the catalog is worth pinning in both directions: what
// it must catch, and what it must leave alone.
func TestLooksLikeCopy(t *testing.T) {
	copyText := []string{
		"Sign in",
		"Loading…",
		"Read later",
		"No feeds yet.",
		"Mark all as read",
		// Dash cases the flag rule must not swallow: an em dash is not a hyphen,
		// a hyphen inside a word does not start one, and a bullet has nothing
		// after it. TestHasFlagWord pins the rest of that boundary directly,
		// because some of those strings are already excluded here for the
		// unrelated one-letter-run reason and would prove nothing.
		"Sign in — it's free",
		"a well-known feed",
		"- Everything else",
		// A path in prose is prose. This is the case the flag rule is
		// deliberately narrower than, and it stays in the catalog.
		"Stored under /var/lib",
	}
	for _, s := range copyText {
		if !looksLikeCopy(s) {
			t.Errorf("looksLikeCopy(%q) = false, want true — this is copy and belongs in the catalog", s)
		}
	}

	notCopy := []string{
		// Identifiers, classes, action ids, values, glyphs: the four kinds of
		// literal that legitimately live in client/view.
		"feed-row",
		"modal-keep",
		"sk-w-90",
		"true",
		"Enter",
		"▲ ▼",
		"1 2 3",
		// Command lines. The first is the exact literal from home.go's terminal
		// transcript that failed CI; a translated one is a command that does not
		// run.
		" init -db /var/lib/articleflux/articleflux.db -user cam",
		"go test -race ./...",
		"articleflux serve --addr 0.0.0.0:9000",
	}
	for _, s := range notCopy {
		if looksLikeCopy(s) {
			t.Errorf("looksLikeCopy(%q) = true, want false — this is not prose and cannot be translated", s)
		}
	}
}

// TestHasFlagWord pins the boundary of the flag rule on its own, at the level
// where a hyphen is either a flag or is not one. Going through looksLikeCopy for
// these would prove less than it appears to: "-5 items" is already not copy
// there because it has a single run of letters, so it would pass whether this
// rule fired or not.
func TestHasFlagWord(t *testing.T) {
	flags := []string{
		" init -db /var/lib/articleflux/articleflux.db -user cam",
		"go test -race ./...",
		"--addr 0.0.0.0:9000",
		"seed -n 40",
	}
	for _, s := range flags {
		if !hasFlagWord(s) {
			t.Errorf("hasFlagWord(%q) = false, want true", s)
		}
	}

	notFlags := []string{
		"Sign in — it's free", // em dash
		"a well-known feed",   // interior hyphen
		"-5 items",            // a digit follows, so it is a number
		"- Everything else",   // a bare bullet
		"Read later",
		"", // nothing at all
	}
	for _, s := range notFlags {
		if hasFlagWord(s) {
			t.Errorf("hasFlagWord(%q) = true, want false", s)
		}
	}
}
