//go:build js && wasm

package view

import (
	"net/url"
	"strings"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The address bar's grammar (§20.13b).
//
// # What changed, and why the old note was wrong
//
// launch.go still opens with "this is not the beginning of client-side routing",
// and the reasoning it gives — a URL and a saved preference would be two answers
// to one question, and they would disagree the first time somebody bookmarked a
// link — was a reason to DECIDE THE PRECEDENCE, not a reason to have only one of
// them. What the single answer actually cost:
//
//   - No link to a feed, a tag, a search or an article. Nothing in this
//     application could be sent to anybody, including to yourself tomorrow.
//   - Back and Forward did nothing. Back left the reader entirely, from a screen
//     they had navigated four levels into.
//   - No opening a row in a second tab, which is the one gesture every reader
//     already knows.
//   - Two tabs FOUGHT. Both wrote `read.kind` to the same server preference on
//     every navigation, so opening a second window silently moved the first
//     one's saved place — A30 breaking A30.
//
// So both are written and the precedence is settled here, once: **a path other
// than the base path is an explicit destination and outranks the resume; a bare
// base path resumes from preferences exactly as it always has** (see bootRoute).
// Preferences keep being written on every navigation, so a reader still lands
// where they left off when they open the app fresh on another machine — which is
// what A30 is actually for.
//
// # Why not gwc/router
//
// GWC ships a real router — NewHistoryRouter, Register, guards, loaders,
// UseRoute, OnNavigate, and a pure RouteContract for reverse routing — and it was
// evaluated before any of this was written. It is a **route-to-component**
// router, and this application is one mounted tree in which the address mirrors
// state INSIDE a single component. Three specifics decided it:
//
//   - Both Navigate and NavigateReplace end in renderCurrentRoute, which does
//     RenderTo(target, element) with an incremented renderVersion. Every URL write
//     would re-render the route element — here, Reader — discarding the loaded
//     list, the fetched bodies and every note draft.
//   - renderCurrentRoute also calls focusRouteContent and wraps the swap in a view
//     transition. This application writes the address on SCROLL, because which
//     article is being read is a scroll position and not a click (A28), so that is
//     a focus steal and a view transition per article scrolled past — against
//     §20.14's roving focus, which is the thing that makes the reader keyboard-
//     navigable at all.
//   - There is no location-only mode. OnNavigate fires from publishLocation, which
//     runs only inside renderCurrentRoute, so the location stream cannot be had
//     without letting the router own the tree.
//
// RouteContract on its own is pure and would build paths nicely, but the matcher
// behind it (matchRoutePattern) is unexported, so it offers the BUILD half and not
// the PARSE half. Taking half would leave routeSegments and parseRoute able to
// drift, and two halves of a codec that can disagree is the one defect a codec
// must not have. They are written as each other's inverse here and pinned that way
// by a round-trip test.
//
// # This file is a pure string codec
//
// No syscall/js, no state handles, no i18n beyond the titles it is handed a
// Runtime for. A path in, a route out; a route in, a path out. That is what makes
// the grammar testable without a browser (route_test.go), and it is the same
// split platform/history_wasm.go describes from the other side: that file knows
// how to write an address and nothing about what one means.
//
// # The grammar
//
//	/                                   All feeds
//	/unread /liked /later /notes /myfeed
//	/feed/<sourceID>
//	/tag/<tagID>
//	/category/<folderID>
//	/search?q=<text>
//	/settings/<tab>
//	<place>/read/<itemID>               an article open in that place
//	<place>/add                         the add-a-feed dialog
//	<place>/slideshow                   the slideshow
//	/feed/<sourceID>/settings           that feed's settings
//	/tag/<tagID>/settings               that tag's settings
//
// Two shapes of dialog and the difference is deliberate. `add` and `slideshow`
// are about nothing in particular, so they hang off wherever the reader is and
// closing one leaves them there. The two settings panels are ABOUT a specific
// feed or tag — openFeedSettings does not change the scope, and a link to a
// dialog is a link to the thing it configures — so those name their subject and
// take over the place for as long as they are open. Closing re-derives the
// address from state, which still holds the scope underneath, so the reader lands
// back where they were either way.
//
// # Unknown paths degrade to All
//
// The same rule resumeScope applies to an unrecognised `read.kind`, for the same
// reason: a path this version of the client has stopped understanding — a link
// from an older build, a truncated paste, somebody's typo — must open the reader
// on something, and All is the only answer that is never wrong about the reader's
// data. A 404 screen would be a worse lie, because the address is not a resource.

// The scope vocabulary, shared by the saved preference (`read.kind`) and the
// path. One set of words rather than two, so a stream cannot be spelled one way
// in the URL and another in the resume and quietly diverge — which is exactly the
// class of bug that made My Feed unresumable before this file existed (see
// scopeKind).
const (
	kindAll      = "all"
	kindUnread   = "unread"
	kindLiked    = "liked"
	kindDisliked = "disliked"
	kindLater    = "later"
	kindNotes    = "notes"
	kindMyFeed   = "myfeed"
	kindFeed     = "feed"
	kindTag      = "tag"
	kindFolder   = "folder"
	kindSearch   = "search"
)

// The path segments that are not simply the kind.
//
// `category` rather than `folder` because that is the word on screen — the rail
// band, the dialog and the palette all say Categories, and an address is read by
// people. The stored kind stays `folder` because changing it would strand every
// existing reader's saved place for the sake of a word nobody sees.
const (
	segCategory = "category"
	segSettings = "settings"
	segRead     = "read"
)

// dialog is which overlay is open, at most one.
type dialog string

const (
	dialogNone dialog = ""
	dialogAdd  dialog = "add"
	dialogShow dialog = "slideshow"
	// These two carry a subject in dlgID and are emitted as `<kind>/<id>/settings`
	// rather than as their own segment, so their constant values are identifiers
	// here and never appear in an address.
	dialogFeed dialog = "feed-settings"
	dialogTag  dialog = "tag-settings"
)

// route is everywhere the reader can be, as one value.
//
// It is deliberately NOT the whole of Reader's state. Pane widths, folded rail
// sections, the unread-only filter, the theme and every draft are settings or
// session scratch — they belong to the reader, not to the address, and putting
// them in a link would mean sending somebody your preferences along with the
// article you wanted them to read. What is here is what a person would point at.
type route struct {
	// sel is the place: which stream, feed, tag, category or search.
	sel scope
	// item is the article open in that place, or "".
	item string
	// tab is the settings surface's tab, or "" when settings is not open.
	tab settingsTab
	// dlg is the overlay, and dlgID its subject for the two that have one.
	dlg   dialog
	dlgID string
}

// scopeKind classifies a scope into the stored/addressed vocabulary.
//
// Extracted from Reader's rememberScope so the preference and the path cannot
// disagree about what a scope IS. They did: rememberScope had no branch for
// MyFeed and none for a negative rating, so the ranked stream — the application's
// headline feature — was saved as "all" and a reader who left the app on My Feed
// came back to All every time. One classifier means a stream added in one place
// is added in both.
//
// The order of the branches is load-bearing where scopes overlap: a search within
// a feed is a search, and MyFeed replaces the query rather than narrowing it (see
// scope.MyFeed), so both outrank a SourceID that may still be set beside them.
func scopeKind(sc scope) (kind, value string) {
	switch {
	case sc.Search != "":
		return kindSearch, sc.Search
	case sc.MyFeed:
		return kindMyFeed, ""
	case sc.Notes:
		return kindNotes, ""
	case sc.Later:
		return kindLater, ""
	case sc.Rating > 0:
		return kindLiked, ""
	case sc.Rating < 0:
		return kindDisliked, ""
	case sc.TagID != "":
		return kindTag, sc.TagID
	case sc.FolderID != "":
		return kindFolder, sc.FolderID
	case sc.SourceID != "":
		return kindFeed, sc.SourceID
	case sc.Unread:
		return kindUnread, ""
	}
	return kindAll, ""
}

// scopeOf is scopeKind's inverse: a kind and its argument back to a scope.
//
// Shared by resumeScope (which reads it out of preferences) and parseRoute (which
// reads it out of a path), so the two cannot drift. `title` is what the caller
// already knows — the saved `read.title`, or nothing at all when the answer came
// from a URL, which is the case titleFor exists to repair.
//
// ok is false for a kind this build does not recognise, and for one whose
// argument is missing: `/feed/` names no feed, and resolving it to "the feed
// whose id is the empty string" would show an empty list under a blank header
// rather than admitting the address said nothing.
func scopeOf(kind, value, title string, tr i18n.Runtime) (scope, bool) {
	switch kind {
	case kindAll:
		return scope{Title: tr.T("stream", "all")}, true
	case kindUnread:
		return scope{Title: tr.T("stream", "unread"), Unread: true}, true
	case kindLiked:
		return scope{Title: tr.T("stream", "liked"), Rating: 1}, true
	case kindDisliked:
		// Named but not advertised: no rail row, and reachable only by address or by
		// the palette. See the note beside the catalog key for why naming it was the
		// lesser of the two evils.
		return scope{Title: tr.T("stream", "disliked"), Rating: -1}, true
	case kindLater:
		return scope{Title: tr.T("stream", "later"), Later: true}, true
	case kindNotes:
		return scope{Title: tr.T("stream", "notes"), Notes: true}, true
	case kindMyFeed:
		return scope{Title: tr.T("stream", "myFeed"), MyFeed: true}, true
	case kindFeed:
		if value == "" {
			return scope{}, false
		}
		return scope{SourceID: value, Title: title}, true
	case kindTag:
		if value == "" {
			return scope{}, false
		}
		return scope{TagID: value, Title: title}, true
	case kindFolder:
		if value == "" {
			return scope{}, false
		}
		return scope{FolderID: value, Title: title}, true
	case kindSearch:
		if value == "" {
			return scope{}, false
		}
		// A search names itself. There is nothing else it could be called, and
		// unlike a feed or a tag the client never has to look the name up.
		return scope{Search: value, Title: value}, true
	}
	return scope{}, false
}

// routePath renders a route as an address, relative to the document's base path.
//
// base is platform.BasePath — "/" normally, "/reader/" behind a proxy that mounts
// the app under a path. Every address is built on it for the same reason every
// asset URL is (see BasePath): an absolute "/feed/x" is correct on exactly one
// deployment shape and leaves the application entirely on the other two.
func routePath(base string, r route) string {
	segs, query := routeSegments(r)
	if base == "" {
		base = "/"
	}
	p := strings.TrimSuffix(base, "/") + "/" + strings.Join(segs, "/")
	// A trailing slash on the base path itself, and nowhere else: "/" is the
	// document, "/unread/" is the same place spelled differently, and two
	// spellings of one place is how a reader gets two history entries for
	// standing still.
	if len(segs) == 0 {
		p = base
	}
	if query != "" {
		p += "?" + query
	}
	return p
}

// routeSegments is routePath's body, split out so the tests can assert on the
// pieces rather than on string surgery.
func routeSegments(r route) (segs []string, query string) {
	// The subject-naming dialogs take over the address while they are open. Not a
	// suffix on the current place, because openFeedSettings does not change the
	// scope — a reader can be reading All and configuring one feed — and
	// "/unread/feed-settings/<id>" would put a feed id in an address twice to
	// describe a dialog that is about the feed and not about unread.
	switch {
	case r.dlg == dialogFeed && r.dlgID != "":
		return []string{kindFeed, r.dlgID, segSettings}, ""
	case r.dlg == dialogTag && r.dlgID != "":
		return []string{kindTag, r.dlgID, segSettings}, ""
	}
	// The settings surface likewise. It REPLACES the reading panes rather than
	// floating over them (pane == viewSettings), so an address that still named a
	// feed would describe a screen the feed is not on.
	if r.tab != "" {
		return []string{segSettings, string(r.tab)}, ""
	}

	kind, value := scopeKind(r.sel)
	switch kind {
	case kindAll:
		// No segment: All is the document root, which is what makes the bare
		// address the one that resumes.
	case kindFeed, kindTag:
		segs = []string{kind, url.PathEscape(value)}
	case kindFolder:
		segs = []string{segCategory, url.PathEscape(value)}
	case kindSearch:
		// The query string, not a path segment. A search is arbitrary text — it
		// contains spaces, slashes and question marks — and percent-encoding all of
		// that into a path produces an address nobody can read and some proxies
		// normalise. `?q=` is where every search on the web lives.
		segs = []string{kindSearch}
		query = "q=" + url.QueryEscape(value)
	default:
		segs = []string{kind}
	}

	if r.item != "" {
		// `read/<id>` rather than appending the id bare, so "/feed/<id>/<id>" cannot
		// be mistaken for a two-segment place and so `settings` stays unambiguous as
		// a final segment.
		segs = append(segs, segRead, url.PathEscape(r.item))
	}
	switch r.dlg {
	case dialogAdd, dialogShow:
		segs = append(segs, string(r.dlg))
	}
	return segs, query
}

// parseRoute reads an address back into a route.
//
// Total: every input produces a route, because there is no failure this can
// usefully report. An address is not a resource here — it is a request to look at
// something — so the only two outcomes are "the reader gets what they asked for"
// and "the reader gets the reader". See the note at the top of this file.
func parseRoute(base, path, query string, tr i18n.Runtime) route {
	all, _ := scopeOf(kindAll, "", "", tr)
	r := route{sel: all}

	segs := pathSegments(base, path)

	// The suffix dialogs come off the end first, so what remains is a place with
	// an optional article — which is the only shape the rest of this has to know.
	if n := len(segs); n > 0 {
		switch dialog(segs[n-1]) {
		case dialogAdd:
			r.dlg, segs = dialogAdd, segs[:n-1]
		case dialogShow:
			r.dlg, segs = dialogShow, segs[:n-1]
		}
	}

	// The subject-naming dialogs, which are a whole address rather than a suffix.
	if len(segs) == 3 && segs[2] == segSettings && (segs[0] == kindFeed || segs[0] == kindTag) {
		if id := unescape(segs[1]); id != "" {
			if sc, ok := scopeOf(segs[0], id, "", tr); ok {
				r.sel = sc
				r.dlgID = id
				if segs[0] == kindFeed {
					r.dlg = dialogFeed
				} else {
					r.dlg = dialogTag
				}
				return r
			}
		}
		return r
	}

	// The settings surface. An unknown tab falls back to the default tab rather
	// than to the reading screen: somebody who asked for /settings/<something>
	// asked for settings, and the tab is the part that may have been renamed
	// between builds.
	if len(segs) >= 1 && segs[0] == segSettings {
		r.tab = setReading
		if len(segs) >= 2 {
			if t := settingsTab(segs[1]); knownSettingsTab(t) {
				r.tab = t
			}
		}
		return r
	}

	// The article, off the end of whatever place precedes it.
	if n := len(segs); n >= 2 && segs[n-2] == segRead {
		r.item = unescape(segs[n-1])
		segs = segs[:n-2]
	}

	switch len(segs) {
	case 0:
		// Already All.
	case 1:
		if segs[0] == kindSearch {
			if q := queryParam(query, "q"); q != "" {
				if sc, ok := scopeOf(kindSearch, q, "", tr); ok {
					r.sel = sc
				}
			}
			break
		}
		if sc, ok := scopeOf(segs[0], "", "", tr); ok && !kindTakesValue(segs[0]) {
			r.sel = sc
		}
	case 2:
		kind := segs[0]
		if kind == segCategory {
			kind = kindFolder
		}
		if kindTakesValue(kind) {
			if sc, ok := scopeOf(kind, unescape(segs[1]), "", tr); ok {
				r.sel = sc
			}
		}
	}
	return r
}

// kindTakesValue reports whether a kind is meaningless without an argument. It is
// what stops "/feed" resolving to a scope with an empty SourceID — which is not
// "no feed selected", it is a query for the feed whose id is "".
func kindTakesValue(kind string) bool {
	switch kind {
	case kindFeed, kindTag, kindFolder, kindSearch:
		return true
	}
	return false
}

func knownSettingsTab(t settingsTab) bool {
	for _, e := range settingsTabs {
		if e.id == t {
			return true
		}
	}
	return false
}

// pathSegments strips the base path and splits what is left.
//
// Empty segments are dropped rather than preserved, so "/unread/", "//unread" and
// "/unread" are one address. They arrive: a trailing slash is what a browser adds
// when somebody edits an address by hand, and a doubled slash is what string
// concatenation produces in every link anyone will ever write by hand.
func pathSegments(base, path string) []string {
	if base != "" && base != "/" {
		// Case-sensitively, and only as a prefix: the base is derived from this very
		// document's own address (BasePath), so it either matches exactly or the
		// caller has handed us something from somewhere else.
		if trimmed := strings.TrimPrefix(path, strings.TrimSuffix(base, "/")); trimmed != path {
			path = trimmed
		}
	}
	var out []string
	for _, s := range strings.Split(path, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// queryParam reads one parameter out of a raw query string.
//
// url.ParseQuery rather than a hand-split, because the value is reader-typed text
// that has been percent-encoded and may contain the separators — a search for
// "a&b" round-trips only if it is decoded properly. A malformed query yields
// whatever ParseQuery could recover, which for the one parameter this reads is
// the right answer more often than discarding the lot.
func queryParam(query, name string) string {
	v, err := url.ParseQuery(query)
	if err != nil && len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(v.Get(name))
}

// unescape decodes one path segment, keeping the raw text when it will not
// decode. A stray "%" in an address is a typo, not a reason to lose the id.
func unescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// samePlace reports whether two routes describe the same PLACE, which is the
// whole push-versus-replace decision (see platform.PushPath).
//
// Everything except the open article. The reading pane is a continuous stream, so
// `current` changes as the reader scrolls (A28) — and one history entry per
// article scrolled past would bury the entry they actually want under a hundred
// they never chose. Scrolling rewrites the address; navigating adds to the
// history.
func samePlace(a, b route) bool {
	ak, av := scopeKind(a.sel)
	bk, bv := scopeKind(b.sel)
	return ak == bk && av == bv &&
		a.tab == b.tab && a.dlg == b.dlg && a.dlgID == b.dlgID
}

// titleForScope names a scope that arrived from a URL.
//
// A path carries an id, never a name — "/feed/01J2…" says which feed and not what
// it is called — where the saved place carries `read.title` precisely so that the
// header is right on the first frame. So a URL-seeded scope opens with an empty
// title, and this fills it in once the rail's data lands.
//
// It returns the scope unchanged when the title is already set, which is what
// makes it safe to call from an effect that runs on every render: a reader who
// arrived from a preference, or one whose feed list has already landed, pays a
// comparison and nothing else.
func titleForScope(sc scope, feeds []*pb.Feed, tags []*pb.Tag, folders []*pb.Folder) scope {
	if sc.Title != "" {
		return sc
	}
	switch {
	case sc.SourceID != "":
		for _, f := range feeds {
			if f.GetId() == sc.SourceID {
				sc.Title = f.GetTitle()
				return sc
			}
		}
	case sc.TagID != "":
		for _, t := range tags {
			if t.GetId() == sc.TagID {
				// The override if there is one, the tag's own name otherwise —
				// the same precedence the rail renders with, so a renamed tag is
				// renamed in the header too.
				if l := t.GetLabel(); l != "" {
					sc.Title = l
					return sc
				}
				sc.Title = t.GetName()
				return sc
			}
		}
	case sc.FolderID != "":
		for _, f := range folders {
			if f.GetId() == sc.FolderID {
				sc.Title = f.GetName()
				return sc
			}
		}
	}
	return sc
}
