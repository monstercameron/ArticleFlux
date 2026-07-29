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
