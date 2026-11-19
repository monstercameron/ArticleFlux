//go:build js && wasm

package view

// Golden snapshots of the reading surface: the rail, the list, the article, the
// floating transport, the help sheet, the tab bar and the palette, each rendered
// to HTML for a spread of props and compared byte for byte with
// testdata/golden/<name>.html.
//
// # Why byte for byte
//
// About 180 Playwright specs, the delegated click and key handlers in
// client/platform, and the stylesheet all select on what these panes emit — the
// class names, the data-* and aria-* attributes, the element types and the
// order of children. None of those consumers run in this suite. A refactor that
// moves a helper into another file, swaps an append ladder for html.If, or
// takes an element onto GWC's compact attribute lane must leave every one of
// them seeing the same document, and the cheapest proof that it did is that the
// serialised document did not change by a single byte.
//
// So a failure here is not "update the golden". It is "name the difference and
// justify it" — and only then regenerate:
//
//	GOOS=js GOARCH=wasm go test -exec=... ./client/view -run TestGolden -update
//
// The SSR renderer sorts every element's attributes (see elementTag in
// testhelpers_test.go), so attribute ORDER is stable and a diff is always a real
// change of attribute, value, element or text.

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/golden/*.html from the current render")

type goldenCase struct {
	name  string
	build func(tr i18n.Runtime) ui.Node
}

func railGoldenCases() []goldenCase {
	base := func() railProps {
		return railProps{
			feeds:   fixtureFeeds(12),
			tags:    fixtureTags(),
			folders: fixtureFolders(),
			total:   36,
			ranked:  9,
		}
	}
	with := func(edit func(*railProps)) func(tr i18n.Runtime) ui.Node {
		return func(tr i18n.Runtime) ui.Node {
			p := base()
			edit(&p)
			return railPane(p)
		}
	}
	return []goldenCase{
		{"rail-all", with(func(p *railProps) {})},
		{"rail-feed-current", with(func(p *railProps) { p.sel = scope{SourceID: "src-2", Title: "Feed number 2"} })},
		{"rail-category-open", with(func(p *railProps) {
			p.openCats = "cat-1"
			p.sel = scope{FolderID: "cat-1", Title: "Tech"}
		})},
		{"rail-tag-current", with(func(p *railProps) { p.sel = scope{TagID: "t2", Title: "Long reads"} })},
		{"rail-myfeed", with(func(p *railProps) { p.sel = scope{MyFeed: true, Title: "My Feed"} })},
		{"rail-topics", with(func(p *railProps) {
			slugs := categorySlugs()
			p.topicUnread = map[string]int32{slugs[0]: 4, slugs[len(slugs)-1]: 1}
			p.uncatUnread = 3
			p.sel = scope{CategorySlug: slugs[0], Title: "x"}
		})},
		{"rail-filtered", with(func(p *railProps) { p.filter = "number 1"; p.filterSeed = "number 1" })},
		{"rail-unread-feeds-only", with(func(p *railProps) { p.unreadFeedsOnly = true })},
		{"rail-folded", with(func(p *railProps) {
			p.streamsClosed, p.feedsClosed, p.tagsClosed, p.catsClosed = true, true, true, true
		})},
		{"rail-closing", with(func(p *railProps) { p.closing = railCatKey("cat-0"); p.openCats = "cat-0" })},
		{"rail-loading-empty", func(tr i18n.Runtime) ui.Node { return railPane(railProps{loading: true}) }},
		{"rail-empty", func(tr i18n.Runtime) ui.Node { return railPane(railProps{}) }},
	}
}

func listGoldenCases() []goldenCase {
	items := fixtureItems(12)
	base := func() listProps {
		return listProps{
			items: items, connected: true, total: 40, unread: 120, hasMore: true,
			sel:       scope{Title: "All feeds"},
			iconHosts: iconHostsOf(fixtureFeeds(12)),
			conn:      data.Live,
		}
	}
	with := func(edit func(*listProps)) func(tr i18n.Runtime) ui.Node {
		return func(tr i18n.Runtime) ui.Node {
			p := base()
			edit(&p)
			return listPane(tr, p)
		}
	}
	return []goldenCase{
		{"list-connecting", func(tr i18n.Runtime) ui.Node { return listPane(tr, listProps{sel: scope{Title: "All feeds"}}) }},
		{"list-loading", func(tr i18n.Runtime) ui.Node {
			return listPane(tr, listProps{connected: true, loading: true, sel: scope{Title: "All feeds"}})
		}},
		{"list-empty-unread", func(tr i18n.Runtime) ui.Node {
			return listPane(tr, listProps{connected: true, unreadOnly: true, sel: scope{Title: "All feeds"}})
		}},
		{"list-empty-failed", func(tr i18n.Runtime) ui.Node {
			return listPane(tr, listProps{connected: true, loadFailed: true, sel: scope{Title: "Feed", SourceID: "s"}})
		}},
		{"list-all", with(func(p *listProps) {})},
		{"list-current-fresh", with(func(p *listProps) {
			p.current = items[3]
			p.fresh = map[string]bool{items[10].GetId(): true, items[11].GetId(): true}
		})},
		{"list-virtual-window", with(func(p *listProps) {
			p.viewport = 400
			p.scrollTop = ItemRowHeight * 3
			p.current = items[4]
		})},
		{"list-myfeed", with(func(p *listProps) { p.sel = scope{MyFeed: true, Title: "My Feed"}; p.ranked = 9 })},
		{"list-banners", with(func(p *listProps) {
			p.busy = "Marking…"
			p.undo = "tok"
			p.markAllArmed = true
			p.staleNote = "Cached 5m ago"
			p.conn = data.Down
			p.connFix, p.connFixLabel = "retry-now", "Retry"
			p.catSuggestName = "Tech"
			p.podcastBlocked = "voice.needsKey"
		})},
		{"list-search", with(func(p *listProps) {
			p.sel = scope{Search: "go", Title: "Search"}
			p.searchValue = "go"
			p.hasMore = false
		})},
		{"list-loading-more", with(func(p *listProps) { p.loadingMore = true })},
		{"list-unread-feed", with(func(p *listProps) {
			p.sel = scope{SourceID: "src-1", Title: "Feed number 1"}
			p.unreadOnly = true
			p.hasMore = false
			p.total = 0
		})},
	}
}

func articleGoldenCases() []goldenCase {
	with := func(edit func(*articleProps)) func(tr i18n.Runtime) ui.Node {
		return func(tr i18n.Runtime) ui.Node {
			p := fixtureArticle(30)
			edit(&p)
			return articlePane(tr, p)
		}
	}
	return []goldenCase{
		{"article-empty", func(tr i18n.Runtime) ui.Node { return articlePane(tr, articleProps{}) }},
		{"article-stream", with(func(p *articleProps) {})},
		{"article-clamped-loading-above", with(func(p *articleProps) {
			p.expanded = nil
			p.loadingAbove = true
			p.atEnd = false
			p.focus = true
		})},
		{"article-page-revisions", with(func(p *articleProps) {
			p.stream[0].StreamUrl = "/stream/a1?s=abc"
			p.pageOpen = map[string]bool{"a1": true}
			p.pageLive = map[string]bool{"a1": true}
			p.revisionsOpen = map[string]bool{"a1": true}
			p.revisions = map[string][]*pb.ItemRevision{"a1": {{Title: "Old title", ContentHtml: "<p>old</p>"}}}
			p.speakState = "paused"
			p.noteOpen = nil
		})},
		{"article-speak-error-start", with(func(p *articleProps) {
			p.speakState = "error"
			p.atStart = true
			p.atEnd = false
			p.loadingBelow = true
			p.speakSmart = false
		})},
		{"nowplaying-hidden-bar", func(tr i18n.Runtime) ui.Node {
			p := fixtureArticle(3)
			p.speakVisible = false
			p.speakState = "loading"
			return nowPlaying(tr, p)
		}},
		{"nowplaying-paused", func(tr i18n.Runtime) ui.Node {
			p := fixtureArticle(3)
			p.speakVisible = false
			p.speakState = "paused"
			return nowPlaying(tr, p)
		}},
	}
}

func chromeGoldenCases() []goldenCase {
	return []goldenCase{
		{"help-open", func(tr i18n.Runtime) ui.Node { return helpSheet(tr, true) }},
		{"help-closed", func(tr i18n.Runtime) ui.Node { return helpSheet(tr, false) }},
		{"tabbar-list", func(tr i18n.Runtime) ui.Node { return tabBar(tr, viewList, scope{Title: "All"}) }},
		{"tabbar-rail", func(tr i18n.Runtime) ui.Node { return tabBar(tr, viewRail, scope{Title: "All"}) }},
		{"tabbar-myfeed", func(tr i18n.Runtime) ui.Node { return tabBar(tr, viewList, scope{MyFeed: true}) }},
		{"palette-open", func(tr i18n.Runtime) ui.Node {
			entries := buildPalette(tr, fixtureFeeds(6), fixtureTags(), true)
			return palette(tr, paletteProps{open: true, query: "fe", active: 1, entries: filterPalette(entries, "fe")})
		}},
	}
}

func allGoldenCases() []goldenCase {
	var all []goldenCase
	all = append(all, railGoldenCases()...)
	all = append(all, listGoldenCases()...)
	all = append(all, articleGoldenCases()...)
	all = append(all, chromeGoldenCases()...)
	return all
}

// TestGolden is the refactor's safety net. See the file comment.
func TestGolden(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range allGoldenCases() {
		if seen[c.name] {
			t.Fatalf("duplicate golden name %q", c.name)
		}
		seen[c.name] = true
		t.Run(c.name, func(t *testing.T) {
			got := renderView(t, c.build)
			path := filepath.Join("testdata", "golden", c.name+".html")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("no golden for %s (run with -update to create it): %v", c.name, err)
			}
			if got != string(want) {
				t.Errorf("%s changed:\n%s", c.name, firstDifference(string(want), got))
			}
		})
	}
}

// firstDifference shows where two renders part company, with enough context
// either side to find the element — a 40KB string printed whole is not a diff
// anyone reads.
func firstDifference(want, got string) string {
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	from := i - 160
	if from < 0 {
		from = 0
	}
	clip := func(s string) string {
		to := i + 160
		if to > len(s) {
			to = len(s)
		}
		if from > len(s) {
			return ""
		}
		return s[from:to]
	}
	return "at byte " + strconv.Itoa(i) + " (want " + strconv.Itoa(len(want)) + " bytes, got " + strconv.Itoa(len(got)) + ")\n" +
		"want: …" + clip(want) + "…\n" +
		"got:  …" + clip(got) + "…"
}
