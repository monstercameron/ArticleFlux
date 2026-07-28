//go:build js && wasm

package platform

import (
	"strings"
	"syscall/js"
)

// The address bar, as five typed functions (§20.13b).
//
// # Why this exists at all, given what LaunchParam says
//
// LaunchParam's comment states that this application has no client-side routing
// and that it "is deliberately not the beginning of one". That was true, and the
// reason it was true — a URL and a saved preference would be two answers to the
// same question — turned out to be an argument for deciding the precedence rather
// than for having only one of them. A reader cannot link to a feed, cannot open an
// article in a second tab, and cannot press Back; and two tabs open at once fight
// over `read.kind` on the server, so the second window silently moves the first
// one's saved place. All four are worse than the ambiguity that was being avoided.
//
// The precedence is settled once, in client/view/route.go: a path other than the
// base path is an explicit destination and outranks the resume, a bare base path
// resumes from preferences exactly as it always has. Both keep being written, so
// A30 — where you were is account state, restored on any machine — survives intact.
//
// # Everything here is a fact, not a decision
//
// Same rule as the rest of this package: these read and write the browser's
// address and nothing more. What a path MEANS is client/view/route.go's business,
// and that file is a pure string codec with no syscall/js in it, which is what
// makes the grammar testable without a browser.

// Path is the document's current path, always with a leading slash.
//
// Empty only when there is no location at all, which is not a state a browser
// reaches — it is the same defensive read every other accessor here does, because
// a panic on this side of the boundary takes the whole wasm module down.
func Path() string {
	defer func() { _ = recover() }()
	loc := js.Global().Get("location")
	if !loc.Truthy() {
		return "/"
	}
	p := loc.Get("pathname")
	if p.Type() != js.TypeString || p.String() == "" {
		return "/"
	}
	return p.String()
}

// Query is the document's current query string, WITHOUT the leading "?".
//
// The whole string rather than one named parameter, unlike LaunchParam: the route
// codec parses it itself so that the same function can parse a path it was handed
// by a test, and a codec that reached back into the browser for half its input
// could not be tested without one.
func Query() string {
	defer func() { _ = recover() }()
	loc := js.Global().Get("location")
	if !loc.Truthy() {
		return ""
	}
	s := loc.Get("search")
	if s.Type() != js.TypeString {
		return ""
	}
	return strings.TrimPrefix(s.String(), "?")
}

// PushPath moves to a new address and adds a history entry, so Back returns to
// the one before it.
//
// For a change of PLACE — a different stream, feed, tag, category, search, the
// settings surface, or a dialog opening. Not for the article the reader has
// scrolled to: see ReplacePath.
func PushPath(p string) { writePath("pushState", p) }

// ReplacePath rewrites the address without adding a history entry.
//
// For a change WITHIN a place, which in this application means one thing and it
// is the reason this distinction has to exist at all: the reading pane is a
// continuous stream, so the current article changes on scroll (A28 — which
// article is being read is a scroll position, not a click). Pushing there would
// bury the entry the reader actually wants to go back to under one entry per
// article they scrolled past, and Back would stop meaning anything.
func ReplacePath(p string) { writePath("replaceState", p) }

func writePath(method, p string) {
	defer func() { _ = recover() }()
	hist := js.Global().Get("history")
	if !hist.Truthy() || !hist.Get(method).Truthy() {
		return
	}
	// js.Null() for the state object rather than a serialised route: the path IS
	// the state. Anything stored here would be a second copy that a reader
	// arriving from a bookmark — or from a link somebody sent them — would not
	// have, so the codec has to be able to work from the path alone, and storing
	// state as well would let it silently stop being able to.
	hist.Call(method, js.Null(), "", p)
}

// OnPopState reports the reader pressing Back or Forward.
//
// The callback receives nothing: the handler re-reads Path and Query, for the
// same reason writePath stores no state. A popstate event carries whatever was
// pushed with it, and what was pushed is deliberately nothing.
//
// `popstate` does not fire for a pushState the application itself performed,
// only for a genuine navigation, so this cannot loop with the writer — but the
// writer guards against re-writing an unchanged path anyway (see route.go's
// lastPath), because that guarantee is about pushState and not about the effect
// that calls it.
func OnPopState(fn func()) Listener {
	win := js.Global().Get("window")
	if !win.Truthy() {
		return Listener{}
	}
	f := js.FuncOf(func(js.Value, []js.Value) any {
		fn()
		return nil
	})
	win.Call("addEventListener", "popstate", f)
	return Listener{target: win, event: "popstate", fn: f}
}
