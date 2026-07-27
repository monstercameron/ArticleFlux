//go:build js && wasm

package view

// Shared render plumbing for every *_test.go in this package.
//
// client/view is entirely //go:build js && wasm (zero untagged files), so
// these tests only exist under GOOS=js GOARCH=wasm, executed against the
// compiled test binary via Node (see client/i18n/provider_test.go for the
// pattern this borrows, and scripts/make.ps1 / Makefile's `wasmtest` verb for
// the invocation).
//
// Every real pane calls i18n.UseI18n() as its first hook (see railPane's own
// comment on why: Runtime carries func fields, so it travels as a plain
// parameter rather than living on a props struct). Runtime's fields are
// unexported, so the ONLY way to get a real one — as opposed to a zero-value
// stand-in that would silently degrade every tr.T() call to "namespace.key" —
// is through that hook, inside a component mounted under i18n.Provider. That
// is what these two helpers set up.

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// mustRuntime returns a real Runtime, wired to the actual English catalog,
// for tests that call a plain helper (connLabel, readingTime, ...) directly
// rather than rendering a tree. The render itself is thrown away; only the
// Runtime captured by the closure is wanted.
func mustRuntime(t *testing.T) i18n.Runtime {
	t.Helper()
	var tr i18n.Runtime
	_, err := ui.RenderToString(i18n.Provider(i18n.ProviderProps{
		CurrentLocale:  i18n.DefaultLocale,
		FallbackLocale: i18n.DefaultLocale,
		Bundle:         i18n.Bundle(),
		Child: ui.CreateElement(func() ui.Node {
			tr = i18n.UseI18n()
			return html.Text("")
		}),
	}))
	if err != nil {
		t.Fatalf("render provider: %v", err)
	}
	return tr
}

// elementTag returns the full opening tag (e.g.
// `<button class="chip" data-action="x" aria-current="true">`) of the
// element whose serialized attributes contain marker, so a test can assert
// on attributes of ONE specific element among many similar ones without
// depending on the order elements appear relative to each other.
//
// This depends on GoWebComponents' SSR renderer sorting every attribute
// map alphabetically before writing it out (internal/runtime/ssr.go,
// writeSSRProps/writeSSRCompactAttrs both sort their keys) — confirmed by
// reading that source rather than assumed, because map iteration in Go is
// otherwise unordered and would make any such assertion flaky.
func elementTag(t *testing.T, html, marker string) string {
	t.Helper()
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatalf("marker %q not found in rendered output:\n%s", marker, html)
	}
	open := strings.LastIndex(html[:i], "<")
	closeOffset := strings.Index(html[i:], ">")
	if open < 0 || closeOffset < 0 {
		t.Fatalf("could not find the enclosing tag for %q", marker)
	}
	return html[open : i+closeOffset+1]
}

// buttonBlock returns the full markup of the <button ...>...</button> whose
// opening tag contains marker — open tag through its OWN matching close tag,
// rather than just the opening tag elementTag returns. That is what lets a
// test assert on something rendered INSIDE one specific element (a badge
// inside one feed's row, say) without also being satisfied by the same text
// sitting somewhere else on the page.
//
// This assumes buttons never nest here, which panes.go's own feedRow comment
// states as a deliberate constraint (a button inside a button is invalid
// HTML and browsers resolve it by hoisting one out) — so the first
// "</button>" after the opening tag is always the one that closes it.
func buttonBlock(t *testing.T, html, marker string) string {
	t.Helper()
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatalf("marker %q not found in rendered output:\n%s", marker, html)
	}
	open := strings.LastIndex(html[:i], "<button")
	const closeTag = "</button>"
	closeOffset := strings.Index(html[i:], closeTag)
	if open < 0 || closeOffset < 0 {
		t.Fatalf("could not find the enclosing <button>...</button> for %q", marker)
	}
	return html[open : i+closeOffset+len(closeTag)]
}

// renderView mounts build under a real i18n.Provider and renders it to a
// string through GWC's SSR path — the same path
// client/i18n/provider_test.go uses to prove the Provider wiring, applied
// here to the real panes rather than a stand-in probe.
func renderView(t *testing.T, build func(tr i18n.Runtime) ui.Node) string {
	t.Helper()
	out, err := ui.RenderToString(i18n.Provider(i18n.ProviderProps{
		CurrentLocale:  i18n.DefaultLocale,
		FallbackLocale: i18n.DefaultLocale,
		Bundle:         i18n.Bundle(),
		Child: ui.CreateElement(func() ui.Node {
			return build(i18n.UseI18n())
		}),
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}
