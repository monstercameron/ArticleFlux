//go:build js && wasm

package platform

import (
	"syscall/js"
	"testing"
)

// TestResolveTopmostFallback_QueriesTheScrollingPaneNotTheDelegationRoot is
// the regression test for the "jumping down the list" flake in
// e2e/reader.spec.mjs: row 1 (the article a jump seeds above its target,
// which must stay unread) intermittently read data-read=true.
//
// Root cause: OnTopmostChild's on-demand refresh (wired to releaseFocus in
// reader.go, called the instant a jump's scrollTop is assigned) fell back to
// `querySelector(rootSelector)` — "#app", the whole shell — whenever no real
// scroll event had yet set `el` to the actual scrolling pane. "#app" never
// scrolls, so `frame`'s scrollTop/clientHeight/scrollHeight read as a
// permanently-unscrolled container and its topmost calculation degenerates to
// "whichever article is first in DOM order" — the article seeded ABOVE the
// jump's target — reported as topmost at the exact moment the guard that
// would have suppressed it has just been lowered. That reports it read.
//
// The real scroll event that would eventually correct `el` to the true
// scrolling pane (".pane-article") has not necessarily fired yet by then —
// scroll events dispatch on the next rendering opportunity, not synchronously
// with the scrollTop assignment — so this is a genuine race, not a fixed
// ordering. It reproduced roughly 1 run in 3 in the browser.
//
// This test isolates the one-line decision responsible — which selector the
// fallback resolves against — so the failure is provable deterministically,
// without depending on requestAnimationFrame timing or a real scroll event
// ever firing. Mutation-tested: reverting resolveTopmostFallback to query
// rootSelector instead of matchSelector turns this red.
func TestResolveTopmostFallback_QueriesTheScrollingPaneNotTheDelegationRoot(t *testing.T) {
	global := js.Global()
	objectCtor := global.Get("Object")

	// A fake `document.querySelector` that tags back which selector it was
	// asked for, the same way adapters_wasm_test.go in GWC fakes document
	// methods for this exact harness: a js.FuncOf set as a plain JS object's
	// property, invoked through `.Call()` — never through `.Invoke()` on the
	// bare js.Func, which is documented elsewhere in this package
	// (keydown_wasm_test.go) to hang this test runner.
	query := js.FuncOf(func(_ js.Value, args []js.Value) any {
		got := ""
		if len(args) > 0 {
			got = args[0].String()
		}
		marker := objectCtor.New()
		marker.Set("queriedSelector", got)
		return marker
	})
	t.Cleanup(query.Release)

	doc := objectCtor.New()
	doc.Set("querySelector", query)

	const rootSelector = "#app"
	const matchSelector = ".pane-article"

	got := resolveTopmostFallback(doc, rootSelector, matchSelector)

	if want, have := matchSelector, got.Get("queriedSelector").String(); have != want {
		t.Fatalf("resolveTopmostFallback queried %q, want %q (the scrolling pane "+
			"frame() actually measures) — querying %q instead is the #app-shell "+
			"fallback that reproduced the false read-1 flake", have, want, rootSelector)
	}
}
