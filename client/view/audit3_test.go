//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// Audit 3's defects that a render can pin without a browser. The ones that are
// about a pointer, a Tab key or an address bar live in e2e/audit3.spec.mjs.

func TestBareAuthorDropsALeadingBy(t *testing.T) {
	for in, want := range map[string]string{
		"by abnercoimbre": "abnercoimbre",
		"By Ada":          "Ada",
		"  by   Grace ":   "Grace",
		"Bytes Weekly":    "Bytes Weekly",
		"by":              "by",
		"":                "",
	} {
		if got := bareAuthor(in); got != want {
			t.Errorf("bareAuthor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHasWebScheme(t *testing.T) {
	for in, want := range map[string]bool{
		"https://a.example/feed": true,
		"http://a.example":       true,
		"notaurlatall":           false,
		"ftp://a.example":        false,
		"a.example/feed":         false,
		"":                       false,
	} {
		if got := hasWebScheme(in); got != want {
			t.Errorf("hasWebScheme(%q) = %v, want %v", in, got, want)
		}
	}
}

// H19: the topic rows wait until something is filed under one.
func TestTopicsHeld(t *testing.T) {
	some := map[string]int32{"software": 3}
	none := map[string]int32{"software": 0, "ai": 0}
	for _, c := range []struct {
		name string
		p    railProps
		want bool
	}{
		{"counts not landed yet", railProps{topicUnread: nil, total: 5}, true},
		{"a topic holds an unread article", railProps{topicUnread: some, total: 5}, true},
		{"caught up, nothing unread anywhere", railProps{topicUnread: none, total: 0}, true},
		{"unread articles and none of them labelled", railProps{topicUnread: none, total: 5}, false},
	} {
		if got := topicsHeld(c.p); got != c.want {
			t.Errorf("%s: topicsHeld = %v, want %v", c.name, got, c.want)
		}
	}
	tr := mustRuntime(t)
	hidden := renderView(t, func(i18n.Runtime) ui.Node {
		return html.Div(html.Props{}, topicRows(tr, railProps{topicUnread: none, total: 5, uncatUnread: 5})...)
	})
	if strings.Contains(hidden, "data-category-slug") || !strings.Contains(hidden, "topic-dot-none") {
		t.Errorf("with nothing filed the labels must be absent and Uncategorised present:\n%s", hidden)
	}
	// The topic being viewed stays, so an address never strands the reader.
	kept := renderView(t, func(i18n.Runtime) ui.Node {
		return html.Div(html.Props{}, topicRows(tr, railProps{topicUnread: none, total: 5, sel: scope{CategorySlug: "ai"}})...)
	})
	if !strings.Contains(kept, `data-category-slug="ai"`) || strings.Contains(kept, `data-category-slug="software"`) {
		t.Errorf("only the viewed topic should remain:\n%s", kept)
	}
}

// H16: a tab waiting on a call it cannot make says so instead of drawing bars.
func TestSettingsLoadingSaysItIsWaitingWhenOffline(t *testing.T) {
	for _, conn := range []data.ConnState{data.Offline, data.Down} {
		out := renderView(t, func(tr i18n.Runtime) ui.Node {
			return html.Div(html.Props{}, settingsLoading(tr, conn)...)
		})
		if strings.Contains(out, "sk-line") || !strings.Contains(out, "fill in by itself") {
			t.Errorf("conn %s: want the waiting sentence and no skeleton, got:\n%s", conn, out)
		}
	}
	live := renderView(t, func(tr i18n.Runtime) ui.Node {
		return html.Div(html.Props{}, settingsLoading(tr, data.Live)...)
	})
	if !strings.Contains(live, "sk-line") {
		t.Errorf("a live connection keeps the skeleton:\n%s", live)
	}
}

// H20: an empty instance's article pane must not tell the reader to press j and
// k through a list with nothing in it.
func TestEmptyArticlePaneOnAnEmptyInstanceDoesNotPointAtTheList(t *testing.T) {
	tr := mustRuntime(t)
	empty := renderView(t, func(i18n.Runtime) ui.Node { return articlePane(tr, articleProps{listEmpty: true}) })
	if strings.Contains(empty, "j and k") || !strings.Contains(empty, "Nothing to read yet") {
		t.Errorf("an empty instance's pane:\n%s", empty)
	}
	picked := renderView(t, func(i18n.Runtime) ui.Node { return articlePane(tr, articleProps{}) })
	if !strings.Contains(picked, "Pick something to read") {
		t.Errorf("a pane with a list behind it keeps the pick prompt:\n%s", picked)
	}
}

// H11 and H14: one Tab stop, decided in the markup.
func TestListRowsAndGlyphCellsAreRovingTabStops(t *testing.T) {
	if got := itemRowAttrs(fixtureItems(1)[0], -1)["tabindex"]; got != "-1" {
		t.Errorf("an article row's tabindex = %v, want -1 (the list is one stop, set after render)", got)
	}
	tr := mustRuntime(t)
	for _, current := range []string{"", "★", "not-a-glyph"} {
		out := renderView(t, func(i18n.Runtime) ui.Node { return tagGlyphPicker(tr, "t1", current) })
		if n := strings.Count(out, `tabindex="0"`); n != 1 {
			t.Errorf("glyph picker with current %q has %d Tab stops, want 1", current, n)
		}
		if strings.Count(out, `tabindex="-1"`) < 40 {
			t.Errorf("glyph picker with current %q: the other cells must be tabindex -1", current)
		}
	}
	chosen := renderView(t, func(i18n.Runtime) ui.Node { return tagGlyphPicker(tr, "t1", "★") })
	if !strings.Contains(chosen, `data-value="★"`) {
		t.Fatalf("fixture glyph ★ is not in the set; pick another:\n%s", chosen)
	}
}

// H26: the sign-in, recovery and setup forms carry no inline event handler.
func TestFormsCarryNoInlineSubmitHandler(t *testing.T) {
	tr := mustRuntime(t)
	out := renderView(t, func(i18n.Runtime) ui.Node {
		return recoverCard(recoverCardProps{tr: tr})
	})
	if strings.Contains(out, "onsubmit") {
		t.Errorf("an inline onsubmit survives (the page's CSP refuses it):\n%s", out)
	}
}
