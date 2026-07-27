//go:build js && wasm

package view

import (
	"testing"
)

// --- filterPalette: the documented three-tier ranking --------------------------

// TestFilterPaletteTierOrder pins the doc comment on filterPalette exactly:
// whole-label prefix, then word prefix, then substring, and within a tier the
// shorter label wins. Every entry here is chosen so it lands in exactly one
// tier for the query "pol", so a regression that merges or reorders the
// tiers changes this order.
func TestFilterPaletteTierOrder(t *testing.T) {
	all := []paletteEntry{
		{ID: "policy", Label: "Policy Today"},          // whole-label prefix, len 12
		{ID: "pol", Label: "Pol"},                      // whole-label prefix, len 3 (shortest)
		{ID: "androidpolice", Label: "Android Police"}, // word prefix ("Police" starts with "Pol")
		{ID: "interpol", Label: "Interpol News"},       // substring only (inside "Interpol")
		{ID: "none", Label: "Something Else"},          // no match at all
	}
	got := filterPalette(all, "pol")

	wantIDs := []string{"pol", "policy", "androidpolice", "interpol"}
	if len(got) != len(wantIDs) {
		t.Fatalf("filterPalette returned %d entries %v, want %d: %v", len(got), idsOf(got), len(wantIDs), wantIDs)
	}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Errorf("position %d: got ID %q, want %q (full order: %v, want %v)", i, got[i].ID, id, idsOf(got), wantIDs)
		}
	}
}

func idsOf(entries []paletteEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}
	return out
}

// TestFilterPaletteIsNotFuzzy pins the doc comment's explicit non-goal: a
// subsequence match ("cme" appears in order inside "Chrome") must NOT match,
// because fuzzy/subsequence scoring "makes almost everything match almost
// everything at 150 feeds."
func TestFilterPaletteIsNotFuzzy(t *testing.T) {
	all := []paletteEntry{{ID: "chrome", Label: "Chrome"}}
	got := filterPalette(all, "cme") // subsequence of "Chrome", not a substring
	if len(got) != 0 {
		t.Errorf("filterPalette(%v, \"cme\") = %v, want no matches (subsequence matching is explicitly not supported)", all, idsOf(got))
	}
}

func TestFilterPaletteSubstringNotAtWordStart(t *testing.T) {
	all := []paletteEntry{{ID: "interpol", Label: "Interpol News"}}
	got := filterPalette(all, "pol")
	if len(got) != 1 || got[0].ID != "interpol" || got[0].Score != 2 {
		t.Errorf("filterPalette(Interpol News, \"pol\") = %+v, want one substring-tier (Score=2) match", got)
	}
}

func TestFilterPaletteEmptyQueryExcludesCommandsAndCapsAtTwentyFour(t *testing.T) {
	all := make([]paletteEntry, 0, 30)
	for i := 0; i < 30; i++ {
		all = append(all, paletteEntry{ID: "stream-like", Kind: paletteFeed, Label: "Feed"})
	}
	all = append(all, paletteEntry{ID: "should-be-excluded", Kind: paletteCommand, Label: "Refresh"})

	got := filterPalette(all, "")
	if len(got) != 24 {
		t.Fatalf("filterPalette(all, \"\") returned %d entries, want 24 (the cap)", len(got))
	}
	for _, e := range got {
		if e.Kind == paletteCommand {
			t.Errorf("filterPalette(all, \"\") included a command entry %+v; empty-query results should exclude commands", e)
		}
	}
}

func TestFilterPaletteCapsPopulatedQueryAtTwentyFour(t *testing.T) {
	all := make([]paletteEntry, 0, 30)
	for i := 0; i < 30; i++ {
		all = append(all, paletteEntry{ID: "x", Label: "Politics Daily"})
	}
	got := filterPalette(all, "pol")
	if len(got) != 24 {
		t.Errorf("filterPalette with 30 matching entries returned %d, want the 24-entry cap", len(got))
	}
}

func TestFilterPaletteQueryIsTrimmedAndCaseInsensitive(t *testing.T) {
	all := []paletteEntry{{ID: "x", Label: "The Verge"}}
	for _, q := range []string{"  the  ", "THE", "The", "the"} {
		got := filterPalette(all, q)
		if len(got) != 1 {
			t.Errorf("filterPalette(%v, %q) = %v, want a single match (query should be trimmed and lower-cased)", all, q, idsOf(got))
		}
	}
}

func TestFilterPaletteNoMatchesReturnsEmptyNotAll(t *testing.T) {
	all := []paletteEntry{{ID: "x", Label: "The Verge"}, {ID: "y", Label: "Ars Technica"}}
	got := filterPalette(all, "zzz-nonexistent-zzz")
	if len(got) != 0 {
		t.Errorf("filterPalette with a query matching nothing returned %v, want empty", idsOf(got))
	}
}

// --- wordPrefix ----------------------------------------------------------------

func TestWordPrefix(t *testing.T) {
	cases := []struct {
		name string
		s, q string
		want bool
	}{
		{"matches at the very start of the string", "police report", "pol", true},
		{"matches after a space", "android police", "pol", true},
		{"matches after a hyphen", "android-police", "pol", true},
		{"matches after an en dash", "android–police", "pol", true},
		{"matches after an em dash", "android—police", "pol", true},
		{"matches after a colon", "site:police", "pol", true},
		{"matches after a slash", "site/police", "pol", true},
		{"matches after a dot", "site.police", "pol", true},
		{"matches after a pipe", "site|police", "pol", true},
		{"matches after an open paren", "site(police", "pol", true},
		{"matches after an open bracket", "site[police", "pol", true},
		{"does not match mid-word", "interpol", "pol", false},
		{"does not match when q is not a prefix of any word", "android police", "xyz", false},
		{"empty query matches (every position is a prefix of \"\")", "android police", "", true},
		{"empty s never matches a non-empty query", "", "pol", false},
		{"whole string equal to query matches", "pol", "pol", true},
		{"query longer than the whole string does not match", "pol", "police", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wordPrefix(c.s, c.q); got != c.want {
				t.Errorf("wordPrefix(%q, %q) = %v, want %v", c.s, c.q, got, c.want)
			}
		})
	}
}

// --- buildPalette / paletteStreams: KNOWN BUG — the disliked stream is unreachable

// TestPaletteNeverOffersTheDislikedStream pins a confirmed product defect:
// scope.Rating is only ever set to +1 in this codebase (see reader.go), never
// -1, and the palette's own list of destinations — where a reader would have
// to go to reach it if the rail does not offer it (see panes.go's railPane,
// which explicitly excludes it: "Liked is a stream; disliked is not") —
// likewise never lists one. streamDisliked exists as a constant and
// subDisliked/emptyDisliked/emptyDislikedHint exist as catalog copy, but
// nothing in this codebase ever constructs a scope reaching that state. This
// test is EXPECTED TO FAIL until that is fixed; it is not a test bug.
func TestPaletteNeverOffersTheDislikedStream(t *testing.T) {
	tr := mustRuntime(t)

	streams := paletteStreams(tr)
	for _, e := range streams {
		if e.ID == streamDisliked {
			return // found it: the bug is fixed, nothing more to check.
		}
	}

	full := buildPalette(tr, nil, nil)
	for _, e := range full {
		if e.ID == streamDisliked {
			return
		}
	}

	t.Errorf("BUG: neither paletteStreams nor buildPalette ever offers an entry "+
		"with ID %q (streamDisliked). scope.Rating is never set to -1 anywhere "+
		"in client/view, so the disliked stream — which has its own catalog "+
		"copy (list.subDisliked, list.emptyDisliked, list.emptyDislikedHint) "+
		"and its own constant (streamDisliked) — is unreachable from the UI. "+
		"paletteStreams returned: %v", streamDisliked, idsOf(streams))
}
