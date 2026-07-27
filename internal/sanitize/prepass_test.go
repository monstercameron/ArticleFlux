package sanitize

import (
	"strings"
	"testing"
)

// Why HTML() still parses the document twice, and why the obvious fix does not
// work.
//
// # The optimisation this rejects
//
// HTML() parses twice. `prepare` parses the input, walks it hardening links and
// dropping tracking pixels, and renders it back to a string; the GWC engine then
// parses that string again and renders it a second time. Profiled on a
// 40-paragraph article, `prepare` is 80% of HTML() — four times the cost of the
// sanitizer it feeds.
//
// harden() acts on exactly three elements: `a`, `img` and `source`. A document
// with none of them is walked in full and changed in no way. So the obvious win
// is to look for those three tags in the raw text and, finding none, hand the
// input straight to the engine — skipping a whole parse and render on every feed
// summary that carries no link or picture, which is most of them.
//
// The argument for its safety is sound as far as it goes: html.Parse never
// invents an `a`, `img` or `source`, so a string containing none cannot produce
// one, so the walk provably does nothing.
//
// # Why it is wrong anyway
//
// Because `prepare` does something the walk has nothing to do with. Parsing a
// string and rendering it back NORMALISES it, and the engine downstream then
// parses the normalised form rather than the original. Skipping the round trip
// changes what the engine sees.
//
// A fuzzer found the divergence in under a second, on an input no reviewer would
// have thought to try:
//
//	input   " 0"        (one leading space, then a character)
//	today   "0"         the round trip drops the leading whitespace
//	skipped " 0"        the engine sees it and escapes it through
//
// html.Parse builds a DOCUMENT, and the HTML5 tree-construction rules discard
// whitespace-only text before the body content begins. The GWC engine parses a
// FRAGMENT in a div context, where that whitespace is ordinary text. Two
// different parsing modes, two different answers, and the round trip is
// silently converting between them.
//
// # Why that is enough to stop
//
// The difference found is a leading space on a list-row summary — cosmetic, and
// easy to argue is harmless. That argument is exactly the failure mode: the
// fuzzer found this one in 0.26 seconds and there is no reason to believe it is
// the only one, and the ones after it would be found by a reader rather than by
// a test. A sanitizer is the wrong place to accept a class of differences whose
// membership is unknown.
//
// The parse can still be collapsed — properly, by giving the engine a node-level
// entry point so the tree is built once and walked twice, instead of being
// serialised and re-parsed in between. That is an additive change to
// GoWebComponents' sanitize package rather than a trick here, and it is where
// this 80% actually goes.

// prepassIsPointless reports whether harden() can have any effect on this input.
//
// Kept, though nothing in the package calls it, because the test below has to
// select the same inputs the rejected optimisation would have selected. A
// demonstration that "skipping is unsafe" is worth nothing unless it skips
// exactly what the optimisation would have skipped.
func prepassIsPointless(s string) bool {
	for i := range len(s) {
		if s[i] != '<' {
			continue
		}
		rest := strings.TrimPrefix(s[i+1:], "/")
		if hasTagPrefix(rest, "a") || hasTagPrefix(rest, "img") || hasTagPrefix(rest, "source") {
			return false
		}
	}
	return true
}

// hasTagPrefix reports whether rest begins with the tag name followed by a
// character that can end a tag name — so `<article>` does not match `a`, and
// `<sourcecode>` does not match `source`. ASCII folding only; HTML tag names are
// ASCII.
func hasTagPrefix(rest, name string) bool {
	if len(rest) < len(name) {
		return false
	}
	for i := range len(name) {
		c := rest[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != name[i] {
			return false
		}
	}
	if len(rest) == len(name) {
		return true
	}
	switch rest[len(name)] {
	case ' ', '\t', '\n', '\r', '\f', '>', '/':
		return true
	}
	return false
}

// withoutPrepass is the rejected optimisation: straight to the engine.
func withoutPrepass(s string, p Policy) string {
	pol, ok := policies[p]
	if !ok {
		pol = policies[Public]
	}
	return pol.Sanitize(s)
}

// TestThePrePassCannotBeSkipped pins the counterexample.
//
// A comment saying "we tried this and it did not work" is a comment somebody
// deletes while re-trying it. This fails the moment the divergence stops
// existing — at which point the optimisation becomes available and this test is
// the thing that says so.
func TestThePrePassCannotBeSkipped(t *testing.T) {
	// The fuzzer's own find, verbatim.
	const input = " 0"

	if !prepassIsPointless(input) {
		t.Fatalf("%q contains no a/img/source, so the rejected optimisation would "+
			"have skipped the pre-pass on it; the triage disagrees", input)
	}

	with, without := HTML(input, Feed), withoutPrepass(input, Feed)
	if with == without {
		t.Fatalf("HTML(%q) and the engine alone now agree on %q.\n"+
			"The round-trip normalisation this test exists to document is gone, so "+
			"skipping the pre-pass for inputs with no a/img/source may now be safe. "+
			"Re-derive it with a fuzzer before believing it.", input, with)
	}
	// Stated exactly, so a CHANGE in how they differ is also a failure rather
	// than a pass on a different divergence.
	if with != "0" || without != " 0" {
		t.Errorf("the divergence moved: with pre-pass %q (want %q), without %q (want %q)",
			with, "0", without, " 0")
	}
}

// And the triage itself, which the test above depends on being right in the
// direction that matters: it may claim work exists when it does not, and must
// never claim there is none when a link needs hardening.
func TestPrepassTriageNeverMissesAnElementHardenActsOn(t *testing.T) {
	for _, in := range []string{
		`<a href="x">link</a>`,
		`<A HREF="x">upper</A>`,
		"<a\nhref=\"x\">newline after the name</a>",
		`<a>`,
		`<a/>`,
		`<img src=x>`,
		`<IMG SRC=x>`,
		`<source srcset="x">`,
		`<picture><source srcset="a"><img src="b"></picture>`,
		`text <a href="x">buried</a> in prose`,
		`<div><p><span><a href="x">nested</a></span></p></div>`,
	} {
		if prepassIsPointless(in) {
			t.Errorf("triage claimed there is nothing to harden in %q", in)
		}
	}

	for _, in := range []string{
		`<article>x</article>`,
		`<aside>x</aside>`,
		`<abbr>x</abbr>`,
		`<address>x</address>`,
		`<sourcecode>x</sourcecode>`,
		`<image>x</image>`, // legacy alias; harden() does not switch on it
		`plain text`,
		``,
	} {
		if !prepassIsPointless(in) {
			t.Errorf("triage found work in %q, which would cost a parse for nothing", in)
		}
	}
}
