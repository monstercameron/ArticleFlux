package tagglyph

import (
	"testing"
	"unicode/utf8"
)

// The count is the specification, so it is asserted rather than commented. A
// catalogue that quietly became 49 during an edit compiles, renders, and is
// wrong.
func TestListIsFifty(t *testing.T) {
	if len(List) != 50 {
		t.Fatalf("catalogue has %d glyphs, want 50", len(List))
	}
}

// Two tags wearing the same mark defeats the entire point of wearing one.
func TestGlyphsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range List {
		if seen[g.Char] {
			t.Errorf("duplicate glyph %q (%s)", g.Char, g.Name)
		}
		seen[g.Char] = true
	}
}

// Names are the accessible label and the tooltip. A duplicate name is two
// controls a screen reader cannot tell apart even though they do different
// things.
func TestNamesAreDistinctAndPresent(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range List {
		if g.Name == "" {
			t.Errorf("glyph %q has no name", g.Char)
		}
		if g.Group == "" {
			t.Errorf("glyph %q (%s) has no group", g.Char, g.Name)
		}
		if seen[g.Name] {
			t.Errorf("duplicate name %q", g.Name)
		}
		seen[g.Name] = true
	}
}

// One rune each. A multi-rune entry would be a glyph plus a variation selector
// or a ZWJ sequence — which is how emoji get in, and they are what this
// catalogue exists to keep out.
func TestGlyphsAreSingleRunes(t *testing.T) {
	for _, g := range List {
		if n := utf8.RuneCountInString(g.Char); n != 1 {
			t.Errorf("glyph %q (%s) is %d runes, want 1", g.Char, g.Name, n)
		}
	}
}

func TestValid(t *testing.T) {
	if !Valid("") {
		t.Error(`Valid("") = false, want true — empty means "use the default"`)
	}
	if !Valid(List[0].Char) {
		t.Errorf("Valid(%q) = false, want true", List[0].Char)
	}
	// The cases that matter are the ones a hostile client sends: not a
	// near-miss, but markup and an emoji.
	for _, bad := range []string{"x", "<script>", "🙂", "◆◆", " ◆"} {
		if Valid(bad) {
			t.Errorf("Valid(%q) = true, want false", bad)
		}
	}
}

// Groups drives the picker's layout, so every glyph must fall into one of the
// groups it returns — an entry in a group Groups() omits is an entry the picker
// never draws.
func TestGroupsCoverEveryGlyph(t *testing.T) {
	n := 0
	for _, name := range Groups() {
		in := In(name)
		if len(in) == 0 {
			t.Errorf("group %q is empty", name)
		}
		n += len(in)
	}
	if n != len(List) {
		t.Errorf("groups cover %d glyphs, want %d", n, len(List))
	}
}

func TestName(t *testing.T) {
	if got := Name("★"); got != "Star" {
		t.Errorf(`Name("★") = %q, want "Star"`, got)
	}
	if got := Name("x"); got != "" {
		t.Errorf(`Name("x") = %q, want ""`, got)
	}
}
