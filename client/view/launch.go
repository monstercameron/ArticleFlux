//go:build js && wasm

package view

import (
	"strings"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
)

// What an installed app was opened FOR (§20.24).
//
// # This is not the beginning of client-side routing
//
// The application resumes from a PREFERENCE, not from a URL (§20.13, A30): the
// scope, its argument and the open article are server-side state, so the same
// reader lands in the same place on a laptop and a phone. A URL that also decided
// what to show would be a second answer to a question that already has one, and
// the two would disagree the first time somebody bookmarked a link.
//
// A manifest has no other channel. `shortcuts` and `share_target` say what they
// want in the query string because that is all a launcher can send, so this reads
// three named parameters, acts on them once, and then removes them from the
// address — which is what keeps them an INSTRUCTION rather than a location.
//
// # A launch outranks the resume, and only for this launch
//
// Somebody who long-pressed the icon and chose "Unread" asked for unread five
// hundred milliseconds ago; the saved view is what they were doing yesterday. The
// explicit, more recent request wins — and it is not written back to the
// preference, because a shortcut is a visit rather than a decision about where
// this reader lives.

// The parameter names. `view` and `add` are this application's own, named in
// web/manifest.webmanifest; `url`, `text` and `title` are the share target's, and
// those three are named by the Web Share Target spec rather than by us.
const (
	paramView  = "view"
	paramAdd   = "add"
	paramURL   = "url"
	paramText  = "text"
	paramTitle = "title"
)

// The values `view` takes. Deliberately a closed set — an unknown one is ignored
// rather than guessed at, so a stale shortcut from an older manifest opens the
// app normally instead of opening nothing.
const (
	launchMyFeed = "myfeed"
	launchUnread = "unread"
	launchLater  = "later"
	launchLiked  = "liked"
	launchNotes  = "notes"
)

// launch is one opening of the application, as its address described it.
type launch struct {
	// view names a stream to open, or is empty.
	view string
	// add is the "Add a feed" shortcut, or a share that carried an address.
	add bool
	// url is the address that was shared, if one could be found.
	url string
	// title is what the sharing app called it. Carried because the add-feed
	// dialog has a title field and a shared page usually knows its own name;
	// unused if the address turns out to be a feed that names itself.
	title string
}

func (l launch) empty() bool {
	return l.view == "" && !l.add && l.url == "" && l.title == ""
}

// readLaunch reads the address this document was opened at.
func readLaunch() launch {
	l := launch{
		view:  strings.ToLower(strings.TrimSpace(platform.LaunchParam(paramView))),
		title: strings.TrimSpace(platform.LaunchParam(paramTitle)),
	}
	l.url = sharedURL(
		platform.LaunchParam(paramURL),
		platform.LaunchParam(paramText),
	)
	l.add = l.url != "" || platform.LaunchParam(paramAdd) != ""
	return l
}

// sharedURL finds the address in a share.
//
// # Why `text` is searched at all
//
// The Web Share Target spec has a `url` field and a great many Android apps do
// not use it: a browser's share sheet, most reader apps and every "share this
// article" button in a social client put the address in `text`, usually as
// `Some Headline https://example.com/x`. A share target that only reads `url`
// therefore works when you share from the address bar and silently does nothing
// when you share from anywhere else, which is the harder half to discover.
//
// The first http(s) token wins. Not a URL parser: what is needed is "which of
// these words is the link", and a real parse would accept `mailto:` and
// `javascript:` — the second of which is a scheme this application must never
// hand to a subscribe field.
func sharedURL(url, text string) string {
	if u := strings.TrimSpace(url); isHTTPURL(u) {
		return u
	}
	for _, word := range strings.Fields(text) {
		// Trailing punctuation is what a sentence leaves on a pasted link.
		word = strings.TrimRight(word, ".,;:)]}\"'")
		if isHTTPURL(word) {
			return word
		}
	}
	return ""
}

func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

// scope resolves a `view` parameter to the stream it names.
//
// The titles come from the same catalog keys the rail and the command palette
// use, so a shortcut opens a stream labelled exactly as it is labelled
// everywhere else — including in whatever language the reader chose.
func (l launch) scope(tr i18n.Runtime) (scope, bool) {
	switch l.view {
	case launchMyFeed:
		return scope{Title: tr.T("stream", "myFeed"), MyFeed: true}, true
	case launchUnread:
		return scope{Title: tr.T("stream", "unread"), Unread: true}, true
	case launchLater:
		return scope{Title: tr.T("stream", "later"), Later: true}, true
	case launchLiked:
		return scope{Title: tr.T("stream", "liked"), Rating: 1}, true
	case launchNotes:
		return scope{Title: tr.T("stream", "notes"), Notes: true}, true
	}
	return scope{}, false
}
