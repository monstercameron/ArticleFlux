//go:build js && wasm

package view

// Do the panes Reader mounts as components actually skip renders that change
// nothing they show?
//
// railmemo_test.go pins the predicate GWC decides that with (reflect.DeepEqual
// over the props). These go one step further for the panes made components in
// the reader refactor — the article pane, the shortcut sheet and the tab bar —
// and mount them in GWC's own test fixture under a parent that re-renders for
// an unrelated reason, the way Reader re-renders on every scroll frame of the
// list. A render count that moves on that re-render is the regression: the pane
// is being rebuilt at frame rate again, with nothing on screen to show for it.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/testkit/render"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// TestMemoisedPanePropsHoldNoFuncs is the structural half: a func anywhere in a
// memoised pane's props — a bare ui.Handler, a callback, a Runtime — never
// compares equal under DeepEqual, so the pane would re-render on every parent
// render while looking memoised. railProps reaches its one handler through a
// ui.Ref for exactly this reason; nothing else may carry one at all.
func TestMemoisedPanePropsHoldNoFuncs(t *testing.T) {
	refType := reflect.TypeOf(ui.Ref[ui.Handler]{})
	var walk func(path string, ty reflect.Type, seen map[reflect.Type]bool)
	walk = func(path string, ty reflect.Type, seen map[reflect.Type]bool) {
		if ty == refType || seen[ty] {
			return
		}
		seen[ty] = true
		defer delete(seen, ty)
		switch ty.Kind() {
		case reflect.Func, reflect.Interface, reflect.Chan:
			t.Errorf("%s is a %s: it defeats the memo bailout", path, ty)
		case reflect.Struct:
			// Generated protobuf messages carry runtime bookkeeping that is not
			// ours to police and compares by pointer before anything else.
			if ty.PkgPath() != "" && ty.PkgPath() != reflect.TypeOf(scope{}).PkgPath() {
				return
			}
			for i := 0; i < ty.NumField(); i++ {
				f := ty.Field(i)
				walk(path+"."+f.Name, f.Type, seen)
			}
		case reflect.Slice, reflect.Array, reflect.Pointer:
			walk(path+"[]", ty.Elem(), seen)
		case reflect.Map:
			walk(path+"[key]", ty.Key(), seen)
			walk(path+"[val]", ty.Elem(), seen)
		}
	}
	for _, v := range []any{articleProps{}, helpSheetProps{}, tabBarProps{}, railProps{}} {
		ty := reflect.TypeOf(v)
		walk(ty.Name(), ty, map[reflect.Type]bool{})
	}
}

// TestMemoisedPanesSkipUnrelatedRenders is the behavioural half, through the
// real reconciler.
func TestMemoisedPanesSkipUnrelatedRenders(t *testing.T) {
	art := fixtureArticle(3)
	renders := map[string]int{}

	// Each probe is the real pane behind a counter — same component shape, same
	// props type — so what is measured is GWC's decision for these props, and
	// what is rendered is the real markup.
	articleProbe := func(p articleProps) ui.Node { renders["article"]++; return articleView(p) }
	helpProbe := func(p helpSheetProps) ui.Node { renders["help"]++; return helpSheetView(p) }
	tabProbe := func(p tabBarProps) ui.Node { renders["tabs"]++; return tabBarView(p) }

	var bumpUnrelated, bumpSpeak func()
	parent := func() ui.Node {
		// Stand-ins for the two kinds of Reader state: one no pane reads (the
		// list's scroll offset), and one the article pane does read.
		scrollTop := ui.UseState(0.0)
		speakState := ui.UseState("playing")
		bumpUnrelated = func() { scrollTop.Set(scrollTop.Get() + 96) }
		bumpSpeak = func() { speakState.Set("paused") }

		// Built the way Reader builds them: a fresh struct literal every render,
		// from state that has not changed.
		p := art
		p.speakState = speakState.Get()
		return html.Div(html.Props{},
			html.Text(""),
			ui.CreateElement(articleProbe, p),
			ui.CreateElement(tabProbe, tabBarProps{active: viewList, sel: scope{Title: "All feeds"}}),
			ui.CreateElement(helpProbe, helpSheetProps{open: false}),
		)
	}

	f := render.New(t)
	f.Render(i18n.Provider(i18n.ProviderProps{
		CurrentLocale:  i18n.DefaultLocale,
		FallbackLocale: i18n.DefaultLocale,
		Bundle:         i18n.Bundle(),
		Child:          ui.CreateElement(parent),
	}))
	want := map[string]int{"article": 1, "help": 1, "tabs": 1}
	if !reflect.DeepEqual(renders, want) {
		t.Fatalf("first mount: renders = %v, want %v", renders, want)
	}

	ui.PostAsync(bumpUnrelated)
	f.Flush()
	if !reflect.DeepEqual(renders, want) {
		t.Errorf("an unrelated parent render re-rendered a memoised pane: renders = %v, want %v", renders, want)
	}

	ui.PostAsync(bumpSpeak)
	f.Flush()
	want["article"] = 2
	if !reflect.DeepEqual(renders, want) {
		t.Errorf("a change the article pane shows did not reach it, or reached the others: renders = %v, want %v", renders, want)
	}
}

// TestRailRowFollowsItsFeedThroughAMemoisedRender is the other side of the
// feed-row memo: skipping unchanged rows must not skip the CHANGED one. One
// feed's unread count moves the way adjustUnread moves it — a new slice with
// that one feed cloned — and the mounted rail's row for it must say the new
// number while its neighbour still says its old one.
func TestRailRowFollowsItsFeedThroughAMemoisedRender(t *testing.T) {
	feeds0 := fixtureFeeds(12)
	var setFeeds func([]*pb.Feed)
	parent := func() ui.Node {
		feeds := ui.UseState(feeds0)
		setFeeds = feeds.Set
		return ui.CreateElement(railPane, railProps{feeds: feeds.Get(), folders: fixtureFolders(), openCats: "cat-1"})
	}
	f := render.New(t)
	f.Render(i18n.Provider(i18n.ProviderProps{
		CurrentLocale:  i18n.DefaultLocale,
		FallbackLocale: i18n.DefaultLocale,
		Bundle:         i18n.Bundle(),
		Child:          ui.CreateElement(parent),
	}))
	labels := func() map[string][]string {
		out := map[string][]string{}
		for _, b := range f.AllByTag("button") {
			if id := b.Attr("data-source-id"); id != "" && b.Attr("aria-label") != "" {
				out[id] = append(out[id], b.Attr("aria-label"))
			}
		}
		return out
	}
	before := labels()
	// src-1 is filed under cat-1 (open), so it is drawn twice: both copies
	// must follow.
	if len(before["src-1"]) != 2 {
		t.Fatalf("fixture drift: expected src-1 in the feed list and in open cat-1, got %v", before["src-1"])
	}

	next := append([]*pb.Feed(nil), feeds0...)
	c := cloneFeed(next[1])
	c.UnreadCount = 42
	next[1] = c
	ui.PostAsync(func() { setFeeds(next) })
	f.Flush()

	after := labels()
	for _, l := range after["src-1"] {
		if !strings.Contains(l, "42") {
			t.Errorf("src-1's row did not follow its feed: %q", l)
		}
	}
	if !reflect.DeepEqual(after["src-2"], before["src-2"]) {
		t.Errorf("an untouched row changed: %v -> %v", before["src-2"], after["src-2"])
	}
}
