package attrsel

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in       string
		sel      string
		attr     string
		wantText bool
	}{
		{"h2 a@href", "h2 a", "href", false},
		{"article.post", "article.post", "", true},
		{"  h2 a @ href  ", "h2 a", "href", false},
		{"img@src", "img", "src", false},
		{"time@datetime", "time", "datetime", false},
		{"div@data-published-at", "div", "data-published-at", false},
		{"use@xlink:href", "use", "xlink:href", false},
		{"html@xml:lang", "html", "xml:lang", false},
		// HTML attribute names are ASCII case-insensitive, so a recipe may shout.
		{"a@HREF", "a", "href", false},
		// Descendant, child and pseudo selectors pass through untouched.
		{"main > article:first-child h1", "main > article:first-child h1", "", true},
		{"ul li:nth-child(2n+1) a@href", "ul li:nth-child(2n+1) a", "href", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := Parse(c.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", c.in, err)
			}
			if got.Selector != c.sel || got.Attr != c.attr {
				t.Errorf("Parse(%q) = {%q, %q}, want {%q, %q}",
					c.in, got.Selector, got.Attr, c.sel, c.attr)
			}
			if got.TextContent() != c.wantText {
				t.Errorf("TextContent() = %v, want %v", got.TextContent(), c.wantText)
			}
		})
	}
}

// The reason the split is on the LAST unquoted '@' rather than the first: '@'
// occurs inside real attribute selectors, and cutting there produces a selector
// cascadia rejects with a confusing error.
func TestAtInsideASelectorIsNotTheSeparator(t *testing.T) {
	cases := []struct{ in, sel, attr string }{
		{`[data-user="a@b.com"]`, `[data-user="a@b.com"]`, ""},
		{`[data-user="a@b.com"]@href`, `[data-user="a@b.com"]`, "href"},
		{`a[href='mailto:x@y.z']`, `a[href='mailto:x@y.z']`, ""},
		{`a[href='mailto:x@y.z']@href`, `a[href='mailto:x@y.z']`, "href"},
		// Unquoted but bracketed: depth counts too.
		{`[data-x=a@b]`, `[data-x=a@b]`, ""},
		{`[data-x=a@b] a@href`, `[data-x=a@b] a`, "href"},
		// An escaped quote inside a quoted value must not end the quote.
		{`[title="say \"hi\" to x@y"]@href`, `[title="say \"hi\" to x@y"]`, "href"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := Parse(c.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", c.in, err)
			}
			if got.Selector != c.sel || got.Attr != c.attr {
				t.Errorf("Parse(%q) = {%q, %q}, want {%q, %q}",
					c.in, got.Selector, got.Attr, c.sel, c.attr)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"", ErrEmpty},
		{"   ", ErrEmpty},
		{"a@", ErrEmptyAttr},
		{"a@   ", ErrEmptyAttr},
		{"@href", ErrEmptySel},
		{"   @href", ErrEmptySel},
		{"a@href name", ErrBadAttrName},
		{"a@hr>ef", ErrBadAttrName},
		{"a@hr ef", ErrBadAttrName},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			_, err := Parse(c.in)
			if !errors.Is(err, c.want) {
				t.Errorf("Parse(%q) = %v, want %v", c.in, err, c.want)
			}
		})
	}
}

func TestStringRoundTrips(t *testing.T) {
	for _, s := range []string{"h2 a@href", "article.post", `[data-user="a@b.com"]@src`} {
		e, err := Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Parse(e.String())
		if err != nil {
			t.Fatalf("re-parsing %q: %v", e.String(), err)
		}
		if again != e {
			t.Errorf("round trip changed %q into %q", e, again)
		}
	}
}
