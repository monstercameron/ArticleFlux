//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The grammar, pinned from both ends.
//
// routeSegments and parseRoute are each other's inverse, and nothing in the
// compiler enforces that — which is the whole reason gwc/router's RouteContract
// was not used for half of it (see route.go). A codec whose two halves can
// disagree fails in the worst possible way: the address the reader copies is not
// the address that reopens what they were looking at, and nothing anywhere
// reports an error. So every case below is asserted in both directions, and
// TestRouteRoundTrip walks the whole table a second time to say so explicitly.

// routeCases is the grammar. One entry per shape the address bar can take.
var routeCases = []struct {
	name string
	path string
	// want is compared field by field against the parse of path. Titles are
	// checked separately (see TestParseRouteTitles) because a path carries an id
	// and never a name.
	want route
}{
	{"root is all feeds", "/", route{sel: scope{}}},
	{"unread", "/unread", route{sel: scope{Unread: true}}},
	{"liked", "/liked", route{sel: scope{Rating: 1}}},
	{"later", "/later", route{sel: scope{Later: true}}},
	{"notes", "/notes", route{sel: scope{Notes: true}}},
	{"my feed", "/myfeed", route{sel: scope{MyFeed: true}}},
	{"a feed", "/feed/01J2SRC", route{sel: scope{SourceID: "01J2SRC"}}},
	{"a tag", "/tag/01J2TAG", route{sel: scope{TagID: "01J2TAG"}}},
	{"a category", "/category/01J2CAT", route{sel: scope{FolderID: "01J2CAT"}}},
	{"a search", "/search?q=rust", route{sel: scope{Search: "rust"}}},

	{"an article in all", "/read/01J2ITEM", route{sel: scope{}, item: "01J2ITEM"}},
	{"an article in a feed", "/feed/01J2SRC/read/01J2ITEM",
		route{sel: scope{SourceID: "01J2SRC"}, item: "01J2ITEM"}},
	{"an article in unread", "/unread/read/01J2ITEM",
		route{sel: scope{Unread: true}, item: "01J2ITEM"}},
	{"an article in a search", "/search/read/01J2ITEM?q=rust",
		route{sel: scope{Search: "rust"}, item: "01J2ITEM"}},

	{"the add dialog over all", "/add", route{sel: scope{}, dlg: dialogAdd}},
	{"the add dialog over a feed", "/feed/01J2SRC/add",
		route{sel: scope{SourceID: "01J2SRC"}, dlg: dialogAdd}},
	{"the slideshow over all", "/slideshow", route{sel: scope{}, dlg: dialogShow}},
	{"the slideshow over a tag", "/tag/01J2TAG/slideshow",
		route{sel: scope{TagID: "01J2TAG"}, dlg: dialogShow}},
	{"the slideshow over an open article", "/unread/read/01J2ITEM/slideshow",
		route{sel: scope{Unread: true}, item: "01J2ITEM", dlg: dialogShow}},

	{"a feed's settings", "/feed/01J2SRC/settings",
		route{sel: scope{SourceID: "01J2SRC"}, dlg: dialogFeed, dlgID: "01J2SRC"}},
	{"a tag's settings", "/tag/01J2TAG/settings",
		route{sel: scope{TagID: "01J2TAG"}, dlg: dialogTag, dlgID: "01J2TAG"}},

	{"the settings surface", "/settings/appearance", route{sel: scope{}, tab: setAppearance}},
	{"the default settings tab", "/settings/reading", route{sel: scope{}, tab: setReading}},
}

// eqRoute compares two routes on everything the address carries. Title is
// excluded: parseRoute cannot know a feed's name from its id, which is what
// titleForScope exists to repair, and asserting on it here would be asserting
// that the codec does something it deliberately does not.
func eqRoute(a, b route) bool {
	as, av := scopeKind(a.sel)
	bs, bv := scopeKind(b.sel)
	return as == bs && av == bv &&
		a.item == b.item && a.tab == b.tab && a.dlg == b.dlg && a.dlgID == b.dlgID
}

func routeStr(r route) string {
	k, v := scopeKind(r.sel)
	return "kind=" + k + " value=" + v + " item=" + r.item +
		" tab=" + string(r.tab) + " dlg=" + string(r.dlg) + " dlgID=" + r.dlgID
}

// splitTarget separates the path from the query in a test case's address, since
// the browser hands them to parseRoute as two separate reads (location.pathname
// and location.search) rather than as one string.
func splitTarget(target string) (path, query string) {
	if i := strings.IndexByte(target, '?'); i >= 0 {
		return target[:i], target[i+1:]
	}
	return target, ""
}

func TestParseRoute(t *testing.T) {
	tr := mustRuntime(t)
	for _, tc := range routeCases {
		t.Run(tc.name, func(t *testing.T) {
			path, query := splitTarget(tc.path)
			got := parseRoute("/", path, query, tr)
			if !eqRoute(got, tc.want) {
				t.Errorf("parseRoute(%q)\n got %s\nwant %s", tc.path, routeStr(got), routeStr(tc.want))
			}
		})
	}
}

func TestRoutePath(t *testing.T) {
	for _, tc := range routeCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := routePath("/", tc.want); got != tc.path {
				t.Errorf("routePath(%s) = %q, want %q", routeStr(tc.want), got, tc.path)
			}
		})
	}
}

// TestRouteRoundTrip is the property the two tables above only imply: rendering
// a route and parsing the result must return the same route. It is stated
// separately because the two tables could both be edited to agree with each
// other and with nothing else.
func TestRouteRoundTrip(t *testing.T) {
	tr := mustRuntime(t)
	for _, tc := range routeCases {
		t.Run(tc.name, func(t *testing.T) {
			path, query := splitTarget(routePath("/", tc.want))
			back := parseRoute("/", path, query, tr)
			if !eqRoute(back, tc.want) {
				t.Errorf("round trip through %q\n got %s\nwant %s",
					routePath("/", tc.want), routeStr(back), routeStr(tc.want))
			}
		})
	}
}

// TestParseRouteDegradesToAll pins the rule at the top of route.go: an address
// this build does not understand opens the reader, it does not fail.
//
// Every entry here is a real way an address goes wrong — an older build's link,
// a hand-edited path, a kind with its argument missing, a paste that lost its
// query string.
func TestParseRouteDegradesToAll(t *testing.T) {
	tr := mustRuntime(t)
	bad := []string{
		"/not-a-stream",
		"/feed",               // a kind with no argument
		"/tag",                //
		"/category",           //
		"/feed/",              // the same, spelled with the separator
		"/search",             // a search with no query
		"/search?notq=rust",   // ... or the wrong parameter
		"/one/two/three",      // too deep for any shape
		"/feed/a/b/c/d",       //
		"/read",               // the article marker with no id
		"/unread/read",        //
		"/feed/x/notsettings", // a three-segment feed path that is not the dialog
	}
	for _, p := range bad {
		t.Run(p, func(t *testing.T) {
			path, query := splitTarget(p)
			got := parseRoute("/", path, query, tr)
			if k, _ := scopeKind(got.sel); k != kindAll {
				t.Errorf("parseRoute(%q) resolved to %s, want the All stream —\n"+
					"an address this build cannot read must open the reader, not a "+
					"wrong feed (route.go, \"Unknown paths degrade to All\")", p, routeStr(got))
			}
			if got.item != "" || got.tab != "" || got.dlg != dialogNone {
				t.Errorf("parseRoute(%q) = %s, want a bare All route", p, routeStr(got))
			}
		})
	}
}

// TestParseRouteSpellings: one place must have one address, or the reader gets a
// history entry for standing still. A trailing slash and a doubled separator are
// what a hand-edited address and a hand-written link look like.
func TestParseRouteSpellings(t *testing.T) {
	tr := mustRuntime(t)
	for _, p := range []string{"/unread", "/unread/", "//unread", "/unread//"} {
		got := parseRoute("/", p, "", tr)
		if k, _ := scopeKind(got.sel); k != kindUnread {
			t.Errorf("parseRoute(%q) = %s, want the unread stream", p, routeStr(got))
		}
	}
}

// TestRouteSearchEscaping: a search is reader-typed text, and the characters
// that break a naive encoding are exactly the ones people type.
//
// "a&b" is the case that matters most: split on "&" without decoding and the
// search silently becomes "a", which is a wrong answer that looks like a right
// one.
func TestRouteSearchEscaping(t *testing.T) {
	tr := mustRuntime(t)
	for _, q := range []string{"rust", "a&b", "a b", "50% off", "who?", "a/b", "π", "a=b"} {
		t.Run(q, func(t *testing.T) {
			r := route{sel: scope{Search: q, Title: q}}
			path, query := splitTarget(routePath("/", r))
			back := parseRoute("/", path, query, tr)
			if back.sel.Search != q {
				t.Errorf("search %q round-tripped through %q as %q",
					q, routePath("/", r), back.sel.Search)
			}
		})
	}
}

// TestRoutePathUnderBasePath: every address is built on the document's own base
// path, for the reason platform.BasePath documents — an absolute "/feed/x" is
// right on one deployment shape and leaves the application entirely on the other
// two.
func TestRoutePathUnderBasePath(t *testing.T) {
	tr := mustRuntime(t)
	const base = "/reader/"
	cases := []struct {
		r    route
		want string
	}{
		{route{sel: scope{}}, "/reader/"},
		{route{sel: scope{Unread: true}}, "/reader/unread"},
		{route{sel: scope{SourceID: "01J2SRC"}}, "/reader/feed/01J2SRC"},
		{route{sel: scope{}, tab: setAppearance}, "/reader/settings/appearance"},
	}
	for _, tc := range cases {
		got := routePath(base, tc.r)
		if got != tc.want {
			t.Errorf("routePath(%q, %s) = %q, want %q", base, routeStr(tc.r), got, tc.want)
		}
		path, query := splitTarget(got)
		if back := parseRoute(base, path, query, tr); !eqRoute(back, tc.r) {
			t.Errorf("under base %q, %q parsed back as %s, want %s",
				base, got, routeStr(back), routeStr(tc.r))
		}
	}
}

// TestSamePlace pins the push-versus-replace decision. Getting this backwards is
// not a cosmetic bug: pushing per article turns Back into a hundred presses
// through a stream the reader never chose to visit, and replacing on a scope
// change makes Back skip the feed they came from.
func TestSamePlace(t *testing.T) {
	feed := route{sel: scope{SourceID: "01J2SRC"}}
	cases := []struct {
		name string
		a, b route
		want bool
	}{
		{"the same place", feed, feed, true},
		{"a different article in the same feed — REPLACE",
			feed, route{sel: scope{SourceID: "01J2SRC"}, item: "01J2ITEM"}, true},
		{"a different feed — PUSH",
			feed, route{sel: scope{SourceID: "01J2OTHER"}}, false},
		{"a different stream — PUSH",
			feed, route{sel: scope{Unread: true}}, false},
		{"a dialog opening — PUSH",
			feed, route{sel: scope{SourceID: "01J2SRC"}, dlg: dialogAdd}, false},
		{"a settings tab change — PUSH",
			route{tab: setReading}, route{tab: setAppearance}, false},
		{"a different search — PUSH",
			route{sel: scope{Search: "rust"}}, route{sel: scope{Search: "go"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := samePlace(tc.a, tc.b); got != tc.want {
				t.Errorf("samePlace(%s, %s) = %v, want %v",
					routeStr(tc.a), routeStr(tc.b), got, tc.want)
			}
		})
	}
}

// TestParseRouteTitles: the streams name themselves from the catalog, so a
// URL-opened stream is labelled exactly as the rail and the palette label it —
// including in whatever language the reader chose.
func TestParseRouteTitles(t *testing.T) {
	tr := mustRuntime(t)
	cases := []struct{ path, want string }{
		{"/", tr.T("stream", "all")},
		{"/unread", tr.T("stream", "unread")},
		{"/liked", tr.T("stream", "liked")},
		{"/later", tr.T("stream", "later")},
		{"/notes", tr.T("stream", "notes")},
		{"/myfeed", tr.T("stream", "myFeed")},
		{"/search?q=rust", "rust"},
	}
	for _, tc := range cases {
		path, query := splitTarget(tc.path)
		if got := parseRoute("/", path, query, tr).sel.Title; got != tc.want {
			t.Errorf("parseRoute(%q).sel.Title = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestTitleForScope: a path carries an id and never a name, so a URL-seeded feed,
// tag or category opens with an empty header until the rail's data lands. This is
// the repair, and it must be a no-op for a scope that already has a title —
// otherwise a reader who resumed from preferences would have their saved header
// overwritten every time the feed list refreshed.
func TestTitleForScope(t *testing.T) {
	feeds := []*pb.Feed{{Id: "01J2SRC", Title: "Ars Technica"}}
	tags := []*pb.Tag{{Id: "01J2TAG", Name: "go"}, {Id: "01J2LBL", Name: "rust", Label: "Rust"}}
	folders := []*pb.Folder{{Id: "01J2CAT", Name: "Tech"}}

	cases := []struct {
		name string
		in   scope
		want string
	}{
		{"a feed is named from the feed list", scope{SourceID: "01J2SRC"}, "Ars Technica"},
		{"a tag is named from the tag list", scope{TagID: "01J2TAG"}, "go"},
		{"a renamed tag uses its override", scope{TagID: "01J2LBL"}, "Rust"},
		{"a category is named from the folder list", scope{FolderID: "01J2CAT"}, "Tech"},
		{"an existing title is left alone",
			scope{SourceID: "01J2SRC", Title: "what the reader saved"}, "what the reader saved"},
		{"an unknown id stays empty, to be filled when its list lands",
			scope{SourceID: "01J2GONE"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := titleForScope(tc.in, feeds, tags, folders).Title; got != tc.want {
				t.Errorf("titleForScope(%+v).Title = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestScopeKindCoversEveryStream is the guard on the bug that motivated pulling
// scopeKind out of rememberScope in the first place.
//
// My Feed had no branch there, so the ranked stream — the application's headline
// feature — was stored as "all", and a reader who left the app on My Feed came
// back to All every single time. The failure was silent from both ends: nothing
// logged, and All is a plausible enough place to land that it reads as the resume
// simply not being very good.
//
// Every scope this application can construct must classify as ITSELF and survive
// a trip through the stored vocabulary and back.
func TestScopeKindCoversEveryStream(t *testing.T) {
	tr := mustRuntime(t)
	cases := []struct {
		name string
		sc   scope
		want string
	}{
		{"all", scope{}, kindAll},
		{"unread", scope{Unread: true}, kindUnread},
		{"liked", scope{Rating: 1}, kindLiked},
		{"disliked", scope{Rating: -1}, kindDisliked},
		{"later", scope{Later: true}, kindLater},
		{"notes", scope{Notes: true}, kindNotes},
		{"my feed", scope{MyFeed: true}, kindMyFeed},
		{"a feed", scope{SourceID: "01J2SRC"}, kindFeed},
		{"a tag", scope{TagID: "01J2TAG"}, kindTag},
		{"a category", scope{FolderID: "01J2CAT"}, kindFolder},
		{"a search", scope{Search: "rust"}, kindSearch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, value := scopeKind(tc.sc)
			if kind != tc.want {
				t.Fatalf("scopeKind(%+v) = %q, want %q", tc.sc, kind, tc.want)
			}
			back, ok := scopeOf(kind, value, tc.sc.Title, tr)
			if !ok {
				t.Fatalf("scopeOf(%q, %q) reported the kind unknown — scopeKind and "+
					"scopeOf must cover the same vocabulary", kind, value)
			}
			if k2, v2 := scopeKind(back); k2 != kind || v2 != value {
				t.Errorf("%+v classified as (%q, %q) but rebuilt as (%q, %q)",
					tc.sc, kind, value, k2, v2)
			}
		})
	}
}

// TestResumeScopeAndPathAgree: the preference and the address are two encodings
// of one thing, and they now share a classifier and a builder so they cannot
// spell a stream two ways. This walks every kind through BOTH and checks the
// results describe the same scope.
func TestResumeScopeAndPathAgree(t *testing.T) {
	tr := mustRuntime(t)
	for _, tc := range routeCases {
		if tc.want.tab != "" || tc.want.dlg != dialogNone {
			// Settings and dialogs are addressed but not stored: the saved place is
			// a stream, not a screen. Nothing to compare.
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			kind, value := scopeKind(tc.want.sel)
			viaPrefs := resumeScope(map[string]string{
				"read.kind": kind, "read.value": value, "read.title": tc.want.sel.Title,
			}, tr)
			path, query := splitTarget(routePath("/", tc.want))
			viaPath := parseRoute("/", path, query, tr).sel

			pk, pv := scopeKind(viaPrefs)
			ak, av := scopeKind(viaPath)
			if pk != ak || pv != av {
				t.Errorf("kind %q resolves to (%q, %q) from preferences and (%q, %q) "+
					"from the address — one vocabulary, two answers", kind, pk, pv, ak, av)
			}
		})
	}
}

var _ = i18n.DefaultLocale

// TestBorrowTitle: a reload must not paint a blank header.
//
// An addressed scope has no name — a path carries "/feed/01J2…" and never "Ars
// Technica" — so it normally waits for the rail. On a RELOAD it does not have to:
// the reader is reloading the place they were already in, and `read.title` is
// already on hand before the first frame. Losing that is a visible flash on every
// refresh, which is the defect 8b.51 fixed for the resume and which routing must
// not reintroduce (reader.spec's "a reload paints the saved view" is the e2e half).
//
// The dangerous direction is the other one, and it is what most of these cases
// pin: borrowing a title from a DIFFERENT place would put one feed's name above
// another feed's articles. A header that lies is worse than a blank one.
func TestBorrowTitle(t *testing.T) {
	cases := []struct {
		name  string
		in    route
		saved map[string]string
		want  string
	}{
		{
			"the saved place is this place, so its name is borrowed",
			route{sel: scope{SourceID: "01J2SRC"}},
			map[string]string{"read.kind": "feed", "read.value": "01J2SRC", "read.title": "Ars Technica"},
			"Ars Technica",
		},
		{
			"a DIFFERENT feed's title is never borrowed",
			route{sel: scope{SourceID: "01J2SRC"}},
			map[string]string{"read.kind": "feed", "read.value": "01J2OTHER", "read.title": "Ars Technica"},
			"",
		},
		{
			"a different KIND's title is never borrowed",
			route{sel: scope{SourceID: "01J2SRC"}},
			map[string]string{"read.kind": "tag", "read.value": "01J2SRC", "read.title": "Go"},
			"",
		},
		{
			"a title already present is left alone",
			route{sel: scope{SourceID: "01J2SRC", Title: "from the address"}},
			map[string]string{"read.kind": "feed", "read.value": "01J2SRC", "read.title": "from the prefs"},
			"from the address",
		},
		{
			"nothing saved is simply nothing borrowed",
			route{sel: scope{SourceID: "01J2SRC"}},
			nil,
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := borrowTitle(tc.in, tc.saved).sel.Title; got != tc.want {
				t.Errorf("borrowTitle(%+v).sel.Title = %q, want %q", tc.in.sel, got, tc.want)
			}
		})
	}
}

// A settings address must not move the reader's saved place.
//
// The address for the settings surface carries no scope by construction
// (routeSegments returns `/settings/<tab>` and nothing else), so parseRoute can
// only answer with its default — All. Boot applied that default as though it had
// been asked for, and an addressed boot also WRITES the scope, so opening
// Settings while reading Liked and then reloading left the reader on All
// articles with A30's saved place overwritten on every machine they use.
//
// Back and Forward never had this bug: apply() treats a settings entry as a
// screen and leaves the place alone. This pins the same rule at boot.
func TestBootRouteKeepsThePlaceUnderSettings(t *testing.T) {
	tr := mustRuntime(t)
	saved := map[string]string{
		"read.kind": kindLiked, "read.title": "Liked", "read.item": "ITEM1",
	}

	got, addressed := bootRouteFor("/", "/settings/appearance", "", saved, tr)
	if !addressed {
		t.Error("a settings address is still an address; addressed = false")
	}
	if got.tab != setAppearance {
		t.Errorf("tab = %q, want %q — the address named the panel", got.tab, setAppearance)
	}
	if k, _ := scopeKind(got.sel); k != kindLiked {
		t.Errorf("scope = %q, want %q: the panel replaced the place underneath it, "+
			"which the prefs effect then writes back as the reader's saved place", k, kindLiked)
	}
	if got.item != "ITEM1" {
		t.Errorf("item = %q, want %q: closing the panel should land where it opened", got.item, "ITEM1")
	}
}

// A settings address obeys a fixed landing view, the same as a bare one.
//
// effectiveResumeScope, not resumeScope: a reader who has chosen "always open My
// Feed" gets that under the panel too, because the question the panel does not
// answer is exactly the question that preference exists to answer.
func TestBootRouteUnderSettingsHonoursFixedLanding(t *testing.T) {
	tr := mustRuntime(t)
	saved := map[string]string{
		"read.kind": kindLiked, "read.item": "ITEM1",
		"landing.mode": landingModeFixed, "landing.kind": kindMyFeed,
	}

	got, _ := bootRouteFor("/", "/settings/reading", "", saved, tr)
	if k, _ := scopeKind(got.sel); k != kindMyFeed {
		t.Errorf("scope = %q, want %q: a fixed landing view outranks the resume", k, kindMyFeed)
	}
	if got.item != "" {
		t.Errorf("item = %q, want empty: a fixed landing view opens on the list", got.item)
	}
}

// A feed's settings dialog DOES name a place, and keeps naming it.
//
// `/feed/<id>/settings` is the other shape with `settings` in it, and the carve
// out above must not swallow it: that address is about a specific feed, so its
// scope is the reader's request rather than a default standing in for one.
func TestBootRouteKeepsAnAddressedFeedSettings(t *testing.T) {
	tr := mustRuntime(t)
	saved := map[string]string{"read.kind": kindLiked}

	got, addressed := bootRouteFor("/", "/feed/SRC1/settings", "", saved, tr)
	if !addressed {
		t.Fatal("addressed = false")
	}
	if got.dlg != dialogFeed || got.dlgID != "SRC1" {
		t.Errorf("dialog = (%q, %q), want (%q, %q)", got.dlg, got.dlgID, dialogFeed, "SRC1")
	}
	if k, v := scopeKind(got.sel); k != kindFeed || v != "SRC1" {
		t.Errorf("scope = (%q, %q), want (%q, %q): the address named the feed", k, v, kindFeed, "SRC1")
	}
}

// A renamed feed renames its header, and nothing else moves.
//
// The scope carries the name it was captured with. That is right — a header
// must not blank while the rail is fetching — and stale the moment the feed is
// renamed: the rail said "Renamed Journal", the rows said "Renamed Journal",
// and the heading over them said "Big Journal", through a reload.
func TestRetitleScopeFollowsARename(t *testing.T) {
	// Id, not SourceId: scope.SourceID is matched against the SUBSCRIPTION id
	// (titleForScope compares f.GetId()), which the field's name does not say and
	// which cost this test a run to discover.
	feeds := []*pb.Feed{{Id: "src-1", SourceId: "source-1", Title: "Renamed Journal"}}
	tags := []*pb.Tag{{Id: "tag-1", Name: "morning read"}}
	folders := []*pb.Folder{{Id: "cat-1", Name: "Reading"}}

	got := retitleScope(scope{SourceID: "src-1", Title: "Big Journal"}, feeds, tags, folders)
	if got.Title != "Renamed Journal" {
		t.Errorf("title = %q, want the feed's CURRENT name", got.Title)
	}
	if got.SourceID != "src-1" {
		t.Errorf("SourceID = %q, want it untouched", got.SourceID)
	}

	if got := retitleScope(scope{TagID: "tag-1", Title: "old"}, feeds, tags, folders); got.Title != "morning read" {
		t.Errorf("tag title = %q, want %q", got.Title, "morning read")
	}
	if got := retitleScope(scope{FolderID: "cat-1", Title: "old"}, feeds, tags, folders); got.Title != "Reading" {
		t.Errorf("folder title = %q, want %q", got.Title, "Reading")
	}
}

// The two ways this could blank a header, which is worse than a stale one.
func TestRetitleScopeLeavesWhatItCannotName(t *testing.T) {
	feeds := []*pb.Feed{{Id: "src-1", Title: "Renamed Journal"}}

	// A stream names nothing in the rail. Retitling one would replace "Read
	// later" with whatever a lookup for the empty id returned, which is nothing.
	for _, s := range []scope{
		{Later: true, Title: "Read later"},
		{Unread: true, Title: "Unread"},
		{MyFeed: true, Title: "My Feed"},
		{Notes: true, Title: "Notes"},
		{Title: "All articles"},
	} {
		if got := retitleScope(s, feeds, nil, nil); got.Title != s.Title {
			t.Errorf("retitleScope(%q) = %q, want it untouched", s.Title, got.Title)
		}
	}

	// The rail has not arrived yet — the commonest frame of all, right after a
	// reload. A lookup that finds nothing must leave the captured name alone.
	if got := retitleScope(scope{SourceID: "src-9", Title: "Big Journal"}, nil, nil, nil); got.Title != "Big Journal" {
		t.Errorf("title = %q, want the captured name kept while the rail is empty", got.Title)
	}
}

// A feed is named by its SOURCE id, which is what a scope carries.
//
// Every writer of scope.SourceID in this client uses Feed.SourceId — the rail's
// rows, the item and article chips, the palette, feedByID, and scopeOf reading
// it back off the address. titleForScope compared Feed.Id, which is the
// subscription, and the two hold the same value on an unshared source: every
// subscription on a personal instance. Where a source IS shared they differ,
// and a header seeded from `/feed/<id>` — the shape every shared link has —
// would then come up blank beside a rail that named the feed perfectly well.
func TestTitleForScopeAcceptsEitherFeedID(t *testing.T) {
	feeds := []*pb.Feed{{Id: "sub-1", SourceId: "source-1", Title: "Big Journal"}}

	// The one that matters: a scope built anywhere in this client carries this.
	if got := titleForScope(scope{SourceID: "source-1"}, feeds, nil, nil); got.Title != "Big Journal" {
		t.Errorf("by source id: title = %q, want %q", got.Title, "Big Journal")
	}
	// And the subscription id still resolves, so a caller holding one is not
	// dropped on the floor.
	if got := titleForScope(scope{SourceID: "sub-1"}, feeds, nil, nil); got.Title != "Big Journal" {
		t.Errorf("by subscription id: title = %q, want %q", got.Title, "Big Journal")
	}
	// And an id belonging to neither still names nothing, rather than the first
	// feed in the list.
	if got := titleForScope(scope{SourceID: "sub-9"}, feeds, nil, nil); got.Title != "" {
		t.Errorf("unknown id: title = %q, want it left empty", got.Title)
	}
}
