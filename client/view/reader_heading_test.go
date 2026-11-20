//go:build js && wasm

package view

// G27: the heading outline of the reader. Markup-level; the same outline read out
// of a real browser's accessibility tree is in e2e/audit2.spec.mjs.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

var headingTag = regexp.MustCompile(`<h([1-6])([^>]*)>`)

// outlineLevels is the level a screen reader announces for each heading, in DOM
// order: the element's own level unless aria-level overrides it.
func outlineLevels(out string) []string {
	var levels []string
	for _, m := range headingTag.FindAllStringSubmatch(out, -1) {
		level := m[1]
		if a := regexp.MustCompile(`aria-level="(\d)"`).FindStringSubmatch(m[2]); a != nil {
			level = a[1]
		}
		levels = append(levels, level)
	}
	return levels
}

func TestReaderOutlineHasOneLevelOneAndArticlesAreLevelTwo(t *testing.T) {
	p := fixtureArticle(3)
	// A second loaded article, so the stream really holds more than one title.
	p.bodies["a2"] = p.stream[1]
	p.stream[1].ContentHtml = fixtureBody(2)

	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		sel := scope{Title: "Rust Blog"}
		return html.Div(html.Props{Class: "shell"},
			readerHeading(tr, listTitle(tr, listProps{sel: sel})),
			html.Div(html.Props{Class: "panes"},
				listPane(tr, listProps{sel: sel, connected: true, feedsKnown: true, feeds: 3}),
				articlePane(tr, p),
			))
	})

	got := strings.Join(outlineLevels(out), "")
	if !strings.HasPrefix(got, "12") {
		t.Fatalf("outline = %q; want the page's level 1 first, then the list's level 2", got)
	}
	if n := strings.Count(got, "1"); n != 1 {
		t.Errorf("outline = %q holds %d level-1 headings, want exactly one", got, n)
	}
	// Every article title is still an <h1> element (the CSS and e2e selectors say
	// `.article h1`) but announced as level 2.
	titles := strings.Count(out, `class="article"`)
	if titles < 2 {
		t.Fatalf("fixture drew %d articles, want a stream of at least two", titles)
	}
	if h1s := strings.Count(out, "<h1"); h1s != titles+1 {
		t.Errorf("%d <h1> elements for %d articles; want one per article plus the page heading", h1s, titles)
	}
	if !strings.Contains(out, "ArticleFlux — Rust Blog") {
		t.Errorf("the page heading does not name the app and the scope:\n%s", out)
	}
}

func TestReaderHeadingNamesTheScopeAndFallsBackToTheApp(t *testing.T) {
	for scopeName, want := range map[string]string{
		"Rust Blog": "ArticleFlux — Rust Blog",
		"":          ">ArticleFlux<",
	} {
		out := renderView(t, func(tr i18n.Runtime) ui.Node { return readerHeading(tr, scopeName) })
		if !strings.Contains(out, want) || !strings.Contains(out, `class="vh"`) {
			t.Errorf("readerHeading(%q) = %s, want %q in the hidden-heading utility", scopeName, out, want)
		}
	}
}
