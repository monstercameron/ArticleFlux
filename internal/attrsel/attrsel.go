// Package attrsel parses a selector-plus-attribute expression like `h2 a@href`.
//
// We own this because cassidy/cascadia — the CSS selector library everyone uses —
// implements CSS selectors, and CSS has no syntax for "and then take this
// attribute". CSS selects elements; it never extracts values. So the `@attr`
// suffix is ours to define and ours to parse (§14.2).
//
// It exists for scraped sources: a site with no feed gets a recipe saying "each
// item is `article.post`, its title is `h2 a`, its link is `h2 a@href`". The
// selector half is handed to cascadia unchanged; only the suffix is ours.
package attrsel

import (
	"errors"
	"fmt"
	"strings"
)

// Expr is a parsed expression.
type Expr struct {
	// Selector is the CSS selector, passed to cascadia verbatim.
	Selector string
	// Attr is the attribute to read, or "" meaning "take the text content".
	Attr string
}

// TextContent reports whether this expression extracts text rather than an
// attribute.
func (e Expr) TextContent() bool { return e.Attr == "" }

func (e Expr) String() string {
	if e.Attr == "" {
		return e.Selector
	}
	return e.Selector + "@" + e.Attr
}

var (
	ErrEmpty       = errors.New("attrsel: empty expression")
	ErrEmptyAttr   = errors.New("attrsel: '@' with no attribute name")
	ErrEmptySel    = errors.New("attrsel: '@' with no selector")
	ErrBadAttrName = errors.New("attrsel: invalid attribute name")
)

// Parse splits an expression into its selector and attribute.
//
// The split is on the **last** '@' rather than the first, because '@' is legal
// inside a CSS attribute selector: `a[href@=x]` is not valid CSS, but
// `[data-user="a@b.com"]` is entirely ordinary and appears in real recipes.
// Splitting on the first '@' would cut that in half and produce a selector that
// cascadia rejects with a confusing error.
//
// An '@' inside quotes is skipped entirely, which is what makes the above work.
func Parse(s string) (Expr, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Expr{}, ErrEmpty
	}

	i := lastUnquotedAt(s)
	if i < 0 {
		return Expr{Selector: s}, nil
	}

	sel := strings.TrimSpace(s[:i])
	attr := strings.TrimSpace(s[i+1:])
	if sel == "" {
		return Expr{}, ErrEmptySel
	}
	if attr == "" {
		return Expr{}, ErrEmptyAttr
	}
	if !validAttrName(attr) {
		return Expr{}, fmt.Errorf("%w: %q", ErrBadAttrName, attr)
	}
	// Attribute names are ASCII case-insensitive in HTML, and normalising here
	// means a recipe saying "@HREF" matches an element parsed as "href".
	return Expr{Selector: sel, Attr: strings.ToLower(attr)}, nil
}

// MustParse is for fixtures and defaults.
func MustParse(s string) Expr {
	e, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return e
}

// lastUnquotedAt finds the last '@' that is not inside a quoted string and not
// inside a bracketed attribute selector.
//
// Bracket depth matters as much as quoting: `[data-x=a@b]` is unquoted but the
// '@' is still part of the selector, not our suffix.
func lastUnquotedAt(s string) int {
	var quote byte
	depth := 0
	found := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++ // skip the escaped character
				continue
			}
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '[':
			depth++
		case c == ']':
			if depth > 0 {
				depth--
			}
		case c == '@' && depth == 0:
			found = i
		}
	}
	return found
}

// validAttrName accepts what HTML attribute names actually look like. Permissive
// on purpose — `data-*`, `aria-*`, `xml:lang` and `xlink:href` all occur — but it
// still rejects whitespace and the structural characters that would mean the
// author meant something else.
func validAttrName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '-', c == '_', c == ':', c == '.':
		default:
			return false
		}
	}
	return true
}
