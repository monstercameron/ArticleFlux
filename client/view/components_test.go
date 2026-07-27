//go:build js && wasm

package view

import (
	"html"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// --- railPane ------------------------------------------------------------------

func TestRailPaneHappyPath(t *testing.T) {
	p := railProps{
		feeds: []*pb.Feed{
			{SourceId: "s1", Title: "The Verge", UnreadCount: 3},
			{SourceId: "s2", Title: "Ars Technica", FolderId: "cat-a"},
		},
		tags:    []*pb.Tag{{Id: "t1", Name: "golang", FeedCount: 1}},
		folders: []*pb.Folder{{Id: "cat-a", Name: "Tech"}},
		total:   5,
	}
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return railPane(p) })

	for _, want := range []string{
		"ArticleFlux",        // rail.mark, the masthead
		"2 feeds · 5 unread", // mastheadSub, composed from feedCount + unreadCount
		"STREAMS",            // bandStreams, upper-cased by railBandToggle
		"All feeds",          // the All stream's own label
		"The Verge",
		"Ars Technica",
		"golang", // the tag row (tagDisplay falls back to Name when Label is unset)
	} {
		if !strings.Contains(out, want) {
			t.Errorf("railPane output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// TestRailPaneCurrentStreamReflectsSelection checks that exactly the
// selected stream's row carries aria-current="true" — not "some row does",
// which a test that only greps for the substring `aria-current="true"`
// would too easily satisfy by accident.
func TestRailPaneCurrentStreamReflectsSelection(t *testing.T) {
	p := railProps{sel: scope{Unread: true}}
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return railPane(p) })

	allTag := elementTag(t, out, `data-source-id="`+streamAll+`"`)
	if strings.Contains(allTag, `aria-current="true"`) {
		t.Errorf("the All row is marked current while the selection is Unread: %s", allTag)
	}
	unreadTag := elementTag(t, out, `data-source-id="`+streamUnread+`"`)
	if !strings.Contains(unreadTag, `aria-current="true"`) {
		t.Errorf("the Unread row is not marked current while the selection is Unread: %s", unreadTag)
	}
}

func TestRailPaneStreamsClosedHidesStreamRows(t *testing.T) {
	p := railProps{streamsClosed: true}
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return railPane(p) })

	if strings.Contains(out, "All feeds") {
		t.Errorf("railPane with streamsClosed=true still rendered the stream rows:\n%s", out)
	}
	band := elementTag(t, out, `data-action="`+actStreams+`"`)
	if !strings.Contains(band, `aria-expanded="false"`) {
		t.Errorf("the Streams band toggle is not marked collapsed: %s", band)
	}
}

func TestRailPaneLoadingSkeletonBeforeFirstFeedArrives(t *testing.T) {
	p := railProps{loading: true}
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return railPane(p) })

	nav := elementTag(t, out, `class="pane pane-rail"`)
	if !strings.Contains(nav, `aria-busy="true"`) {
		t.Errorf("railPane while loading (no feeds yet) is not marked aria-busy: %s", nav)
	}
	if strings.Contains(out, "No feeds yet") {
		t.Errorf("railPane rendered the empty-no-feeds copy while a fetch is still in flight:\n%s", out)
	}
}

func TestRailPaneEmptyNoFeeds(t *testing.T) {
	p := railProps{} // no feeds, not loading
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return railPane(p) })
	if !strings.Contains(out, "No feeds yet") {
		t.Errorf("railPane with zero feeds (and not loading) should show the empty-no-feeds hint:\n%s", out)
	}
}

func TestRailPaneFilterWithNoMatches(t *testing.T) {
	p := railProps{
		feeds:  []*pb.Feed{{SourceId: "s1", Title: "The Verge"}, {SourceId: "s2", Title: "Ars Technica"}},
		filter: "zzz-nothing-matches-zzz",
	}
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return railPane(p) })
	want := "No feed matches \u201czzz-nothing-matches-zzz\u201d."
	if !strings.Contains(out, want) {
		t.Errorf("railPane output missing the no-match hint %q\n--- output ---\n%s", want, out)
	}
	if strings.Contains(out, "The Verge") || strings.Contains(out, "Ars Technica") {
		t.Errorf("railPane still rendered non-matching feed rows under an active filter:\n%s", out)
	}
}

// TestFeedRowUnreadBadgeIsPerFeed closes an audited blind spot: disabling
// feedRow's `if n := f.GetUnreadCount(); n > 0` branch entirely — so a feed
// draws its icon but never its own count — left the whole suite green,
// because TestRailPaneHappyPath's only unread assertion is the masthead's
// aggregate ("2 feeds · 5 unread"), which is composed independently and
// does not depend on any one row drawing a badge.
//
// This pins the badge to ONE feed's OWN row via buttonBlock keyed on
// data-source-id, so a badge drawn on some other row (or the aggregate
// living elsewhere in the markup) cannot satisfy it. It also pins the other
// half of the same branch: a zero-unread feed must draw no badge at all,
// because the design treats a zero count as deliberately undrawn — an
// "All feeds 0" badge would draw the eye to the one place nothing is
// happening.
func TestFeedRowUnreadBadgeIsPerFeed(t *testing.T) {
	p := railProps{
		feeds: []*pb.Feed{
			{SourceId: "s1", Title: "The Verge", UnreadCount: 3},
			{SourceId: "s2", Title: "Ars Technica", UnreadCount: 0},
		},
		total: 3,
	}
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return railPane(p) })

	unread := buttonBlock(t, out, `data-source-id="s1"`)
	if !strings.Contains(unread, `class="feed-count"`) || !strings.Contains(unread, ">3<") {
		t.Errorf("feed s1 (UnreadCount=3) should show its own %q badge inside its own row:\n%s", "3", unread)
	}

	zero := buttonBlock(t, out, `data-source-id="s2"`)
	if strings.Contains(zero, `class="feed-count"`) {
		t.Errorf("feed s2 (UnreadCount=0) should draw NO badge at all — a zero count is "+
			"deliberately not drawn:\n%s", zero)
	}
}

// --- listPane --------------------------------------------------------------

func TestListPaneNotYetConnected(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return listPane(tr, listProps{connected: false})
	})
	if !strings.Contains(out, "Connecting\u2026") {
		t.Errorf("listPane before the socket is up should show the connecting state:\n%s", out)
	}
}

func TestListPaneLoadingSkeleton(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return listPane(tr, listProps{connected: true, loading: true})
	})
	if !strings.Contains(out, "Loading articles") {
		t.Errorf("listPane on a first fetch in flight should show the loading skeleton:\n%s", out)
	}
	if strings.Contains(out, "No articles yet") {
		t.Errorf("listPane showed the EMPTY state while a fetch was still loading:\n%s", out)
	}
}

func TestListPaneRendersItems(t *testing.T) {
	items := []*pb.Item{
		{Id: "i1", Title: "Article One", SourceId: "s1", SourceTitle: "The Verge", WordCount: 500},
		{Id: "i2", Title: "Article Two", SourceId: "s2", SourceTitle: "Ars Technica", WordCount: 100, Rating: 1},
	}
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return listPane(tr, listProps{
			connected: true,
			items:     items,
			total:     2,
			sel:       scope{Title: "All feeds"},
		})
	})
	// The apostrophe is HTML-escaped by the SSR renderer (html.EscapeString),
	// so the literal source string "That's everything." never appears as-is
	// in the rendered markup — matching &#39; is what a real DOM would still
	// render as an apostrophe.
	for _, want := range []string{"Article One", "Article Two", "The Verge", "Ars Technica", "That&#39;s everything."} {
		if !strings.Contains(out, want) {
			t.Errorf("listPane output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// --- emptyList: KNOWN BUG — no case for Notes, a tag, or a category ------------

// TestEmptyListHasNoCaseForNotesTagOrCategory pins the confirmed defect:
// emptyList's switch covers search/unread/later/liked/disliked and then
// falls through to the "no articles at all" default for everything else,
// including Notes, a tag, and a category — three real, distinct scopes that
// get advice ("add a feed") which fits none of them. EXPECTED TO FAIL.
func TestEmptyListHasNoCaseForNotesTagOrCategory(t *testing.T) {
	genericHint := "Add a feed URL in the bar above, then hit Refresh."
	cases := []struct {
		name string
		sel  scope
	}{
		{"the Notes stream", scope{Notes: true}},
		{"a tag", scope{TagID: "tag-1"}},
		{"a category", scope{FolderID: "cat-1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := renderView(t, func(tr i18n.Runtime) ui.Node {
				return emptyList(tr, listProps{sel: c.sel})
			})
			if strings.Contains(out, genericHint) {
				t.Errorf("BUG: emptyList for %s falls through to the generic "+
					"no-articles-at-all hint (%q). emptyList has a dedicated "+
					"case for search/unread/later/liked/disliked but none for "+
					"Notes, a tag, or a category (client/view/panes.go).",
					c.name, genericHint)
			}
		})
	}
}

// TestEmptyNoArticlesHintPointsAtARemovedControl pins the confirmed defect:
// the catalog copy for the "no articles at all" hint tells the reader to use
// a bar above the list, but panes.go's own comments say that control was
// replaced by a dialog opened from the foot of the rail — there is no bar
// above anything in the current layout. EXPECTED TO FAIL.
func TestEmptyNoArticlesHintPointsAtARemovedControl(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return emptyList(tr, listProps{}) // zero-value scope -> the no-articles default
	})
	staleText := "Add a feed URL in the bar above, then hit Refresh."
	if !strings.Contains(out, staleText) {
		t.Skip("copy has changed since this was last verified; re-check whether the stale-control wording is still present")
	}
	t.Errorf("BUG: the no-articles empty state still says %q. panes.go's own "+
		"comment on railPane's head (\"The chosen design has NO top bar ... "+
		"adding a feed lives at the foot of the rail\") says this control no "+
		"longer exists where the hint says to look for it "+
		"(client/i18n/en_panes.go, key list.emptyNoArticlesHint).", staleText)
}

// TestEmptyListScopeGetsItsOwnCopy closes an audited blind spot: swapping the
// catalog KEYS so a Tag scope rendered the Category empty-state copy and a
// Category scope rendered the Tag copy — transposing two of emptyList's
// switch arms — left the whole suite green. The only existing assertion on
// those scopes (above) checks that the GENERIC no-articles hint is absent;
// swapped-but-still-scope-specific copy is not generic, so it slipped
// through untouched.
//
// emptyState's own doc comment says the shape is the point: every empty
// state is a heading plus a DIRECTION, and the direction is what a reader
// actually needed the empty state for. So this pins both halves, per scope —
// not just the three the auditor named (Notes/Tag/Category) but every scope
// emptyList switches on, since the same gap covers all of them identically.
//
// Both halves are looked up from the catalog by the exact key emptyList is
// documented (and, above, spot-checked) to use for that scope, rather than
// inlined as copy — that is what actually distinguishes "the Tag arm fired"
// from "some other arm fired and happened to produce different text", which
// a hardcoded string pasted at write time would not.
func TestEmptyListScopeGetsItsOwnCopy(t *testing.T) {
	tr := mustRuntime(t)
	ns := tr.NS("list")

	cases := []struct {
		name              string
		props             listProps
		titleKey, hintKey string
	}{
		{"a search", listProps{sel: scope{Search: "golang"}}, "emptySearch", "emptySearchHint"},
		{"My Feed", listProps{sel: scope{MyFeed: true}}, "emptyMyFeed", "emptyMyFeedHint"},
		{"unread-only", listProps{unreadOnly: true}, "emptyUnread", "emptyUnreadHint"},
		{"read later", listProps{sel: scope{Later: true}}, "emptyLater", "emptyLaterHint"},
		{"liked", listProps{sel: scope{Rating: 1}}, "emptyLiked", "emptyLikedHint"},
		{"disliked", listProps{sel: scope{Rating: -1}}, "emptyDisliked", "emptyDislikedHint"},
		{"the Notes stream", listProps{sel: scope{Notes: true}}, "emptyNotes", "emptyNotesHint"},
		{"a tag", listProps{sel: scope{TagID: "tag-1"}}, "emptyTag", "emptyTagHint"},
		{"a category", listProps{sel: scope{FolderID: "cat-1"}}, "emptyCategory", "emptyCategoryHint"},
		{"no scope at all", listProps{}, "emptyNoArticles", "emptyNoArticlesHint"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := renderView(t, func(tr i18n.Runtime) ui.Node { return emptyList(tr, c.props) })
			// html.EscapeString matches what the SSR renderer does to
			// html.Text content (see TestListPaneRendersItems above) — two of
			// these hints carry an apostrophe that would otherwise never
			// match the escaped &#39; the renderer actually emits.
			wantTitle := html.EscapeString(ns.T(c.titleKey))
			wantHint := html.EscapeString(ns.T(c.hintKey))
			if !strings.Contains(out, wantTitle) {
				t.Errorf("emptyList(%s) is missing its heading (list.%s = %q):\n%s",
					c.name, c.titleKey, wantTitle, out)
			}
			if !strings.Contains(out, wantHint) {
				t.Errorf("emptyList(%s) is missing its direction (list.%s = %q):\n%s",
					c.name, c.hintKey, wantHint, out)
			}
		})
	}
}

// --- articlePane -------------------------------------------------------------

func TestArticlePaneEmptyStream(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return articlePane(tr, articleProps{})
	})
	for _, want := range []string{"Pick something to read", "j and k move through the list, o opens the original."} {
		if !strings.Contains(out, want) {
			t.Errorf("articlePane(empty stream) missing %q:\n%s", want, out)
		}
	}
}

func TestArticlePaneLinksOnlyWhenTheItemHasAURL(t *testing.T) {
	stream := []*pb.Item{
		{Id: "no-url", Title: "No Link Title"},
		{Id: "has-url", Title: "Linked Title", Url: "https://example.com/a"},
	}
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return articlePane(tr, articleProps{stream: stream, atStart: true, atEnd: true})
	})

	if !strings.Contains(out, "No Link Title") || !strings.Contains(out, "Linked Title") {
		t.Fatalf("both article titles should render:\n%s", out)
	}
	if !strings.Contains(out, `href="https://example.com/a"`) {
		t.Errorf("the item with a URL should render an <a href>:\n%s", out)
	}

	// "no-url" is the FIRST item in the stream, so everything before
	// "has-url"'s own article element belongs to it (plus the pane's leading
	// nav, which also carries no href) — no "href=" should appear there.
	boundary := strings.Index(out, `data-article-id="has-url"`)
	if boundary < 0 {
		t.Fatalf("could not find the has-url article's marker:\n%s", out)
	}
	if strings.Contains(out[:boundary], "href=") {
		t.Errorf("found an href before the linked article even starts — the "+
			"no-url item appears to have rendered a link anyway:\n%s", out[:boundary])
	}
}

func TestArticlePaneStreamTopAndEndMarkers(t *testing.T) {
	stream := []*pb.Item{{Id: "i1", Title: "Only Article"}}
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return articlePane(tr, articleProps{stream: stream, atStart: true, atEnd: true})
	})
	if !strings.Contains(out, "The top of this feed.") {
		t.Errorf("atStart=true should render the top-of-feed marker:\n%s", out)
	}
	if !strings.Contains(out, "That&#39;s everything. Nothing left unread here.") {
		t.Errorf("atEnd=true should render the end-of-stream marker:\n%s", out)
	}
}

func TestArticlePaneFocusPerchReflectsFocusState(t *testing.T) {
	for _, focus := range []bool{false, true} {
		out := renderView(t, func(tr i18n.Runtime) ui.Node {
			return articlePane(tr, articleProps{focus: focus})
		})
		tag := elementTag(t, out, `data-action="`+actFocus+`"`)
		wantPressed := `aria-pressed="` + boolAttr(focus) + `"`
		if !strings.Contains(tag, wantPressed) {
			t.Errorf("focus=%v: focus button tag %s missing %s", focus, tag, wantPressed)
		}
	}
}

// --- helpSheet ---------------------------------------------------------------

// TestHelpSheetListsEveryGroup closes an audited blind spot: deleting the
// entire List-pane shortcut group (the {tr.T(..., "groupList"), []binding{...
// ↑ ↓ move-and-open, j k}} arm of helpSheet's groups slice) left this test
// passing, because it only checked that five substrings — "Ctrl", "K", "Esc",
// "Enter", "?" — appear ANYWHERE in the sheet. "Enter" and "Ctrl" both also
// occur in the Article group's "Ctrl Enter" binding, so a whole missing group
// left every one of those five substrings intact by coincidence.
//
// helpSheet is grouped by WHERE a key works rather than alphabetically (its
// own doc comment: that is the question a reader actually has), so the
// grouping is the feature under test, not decoration. This asserts all four
// group TITLES are present, plus — per group — a binding whose catalog text
// is unique to that one group, so a group emptied of its bindings but left
// with its title (a mutation the title-only check alone would miss) is also
// caught.
func TestHelpSheetListsEveryGroup(t *testing.T) {
	tr := mustRuntime(t)
	help := tr.NS("help")
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return helpSheet(tr, true) })

	cases := []struct {
		group          string // for the failure message only
		title, binding string
	}{
		{"groupAnywhere", help.T("groupAnywhere"), help.T("palette")},
		{"groupRail", help.T("groupRail"), help.T("moveFeeds")},
		{"groupList", help.T("groupList"), help.T("nextPrev")},
		{"groupArticle", help.T("groupArticle"), help.T("saveNote")},
	}
	for _, c := range cases {
		t.Run(c.group, func(t *testing.T) {
			if !strings.Contains(out, c.title) {
				t.Errorf("helpSheet is missing the %s group entirely — its title %q "+
					"does not appear anywhere in the sheet:\n%s", c.group, c.title, out)
			}
			if !strings.Contains(out, c.binding) {
				t.Errorf("helpSheet is missing %q, a binding unique to the %s group:\n%s",
					c.binding, c.group, out)
			}
		})
	}
}

// --- tabBar --------------------------------------------------------------------

func TestTabBarHomeIsCurrentOnTheReadingList(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return tabBar(tr, viewList, scope{}) })
	home := elementTag(t, out, `data-action="tab-home"`)
	if !strings.Contains(home, `aria-current="true"`) {
		t.Errorf("tab-home should be current when viewing the list with no Notes scope: %s", home)
	}
	feeds := elementTag(t, out, `data-action="tab-feeds"`)
	if !strings.Contains(feeds, `aria-current="false"`) {
		t.Errorf("tab-feeds should NOT be current while viewing the list: %s", feeds)
	}
}

func TestTabBarFeedsIsCurrentOnTheRail(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return tabBar(tr, viewRail, scope{}) })
	feeds := elementTag(t, out, `data-action="tab-feeds"`)
	if !strings.Contains(feeds, `aria-current="true"`) {
		t.Errorf("tab-feeds should be current while viewing the rail: %s", feeds)
	}
	home := elementTag(t, out, `data-action="tab-home"`)
	if !strings.Contains(home, `aria-current="false"`) {
		t.Errorf("tab-home should not be current while viewing the rail: %s", home)
	}
}

func TestTabBarNotesIsCurrentWhenScopeIsNotes(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return tabBar(tr, viewList, scope{Notes: true}) })
	notes := elementTag(t, out, `data-action="tab-notes"`)
	if !strings.Contains(notes, `aria-current="true"`) {
		t.Errorf("tab-notes should be current when the scope is Notes: %s", notes)
	}
	home := elementTag(t, out, `data-action="tab-home"`)
	if !strings.Contains(home, `aria-current="false"`) {
		t.Errorf("tab-home should not ALSO be current when the scope is Notes: %s", home)
	}
}

// --- settingsPane tabs -----------------------------------------------------

func TestSettingsPaneTabBarMarksExactlyTheActiveTab(t *testing.T) {
	for _, tc := range settingsTabs {
		tc := tc
		t.Run(string(tc.id), func(t *testing.T) {
			p := settingsProps{tab: tc.id}
			out := renderView(t, func(tr i18n.Runtime) ui.Node { return settingsPane(tr, p) })

			for _, other := range settingsTabs {
				tag := elementTag(t, out, `data-value="`+string(other.id)+`"`)
				want := `aria-current="` + boolAttr(other.id == tc.id) + `"`
				if !strings.Contains(tag, want) {
					t.Errorf("tab %q (active=%q) has tag %s, want to contain %s", other.id, tc.id, tag, want)
				}
			}
		})
	}
}

func TestSettingsPaneUnrecognisedTabFallsBackToReadingWithNoneCurrent(t *testing.T) {
	p := settingsProps{tab: settingsTab("not-a-real-tab")}
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return settingsPane(tr, p) })
	for _, tc := range settingsTabs {
		tag := elementTag(t, out, `data-value="`+string(tc.id)+`"`)
		if strings.Contains(tag, `aria-current="true"`) {
			t.Errorf("no real tab should be marked current for an unrecognised p.tab, but %q is: %s", tc.id, tag)
		}
	}
	// settingsPane's body switch falls back to settingsReading (the Go
	// `default:` arm) for any tab value its cases do not name — confirm that
	// body actually rendered instead of an empty panel.
	// fsGroup upper-cases its heading (the same treatment railBandToggle gives
	// section labels), so the catalog's mixed-case "What the list shows" is
	// rendered as "WHAT THE LIST SHOWS".
	if !strings.Contains(out, "WHAT THE LIST SHOWS") {
		t.Errorf("an unrecognised p.tab should still render the Reading body (settingsPane's default arm):\n%s", out)
	}
}
