package sanitize

import (
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Fuzzing the sanitizer (§10.2, the security review of 2026-07-27).
//
// # Why this file was missing and should not have been
//
// Ten packages in this tree carry a fuzz target — feed parsing, charset
// decoding, mail headers, the SSRF guard. `sanitize` had none, and it is the one
// that matters most: it is the ONLY thing standing between hostile publisher
// HTML and the reader's DOM, on an origin that holds the session token. Every
// other parser here fails by returning wrong data. This one fails by executing
// somebody else's code.
//
// # The invariant
//
// Whatever goes in, the output must not be able to run. Not "should be escaped",
// not "usually safe" — must not contain a script element, an event-handler
// attribute, a javascript: or data:text/html URL, or any of the tags whose whole
// job is to load and execute something else.
//
// Stated as an invariant over ARBITRARY input rather than as a list of payloads,
// because a payload list only ever proves the payloads on it. The seeds below
// are the attacks worth naming; the fuzzer is what covers the ones nobody
// thought of, and mutation XSS in particular is a class where the winning input
// is one no human writes.

// findExecutable RE-PARSES sanitized output and reports anything that could run.
//
// # Why this parses instead of matching patterns
//
// The first version of this used regular expressions, and the fuzzer broke it in
// 2.4 seconds with `<A srC=onAA=>`. The sanitizer's output for that is
// `<a src="onAA=">` — completely inert, because the `on…` text is the VALUE of
// `src`, not an attribute name. A regex looking for `on[a-z]+=` inside a tag
// cannot tell those apart, so it reported a hole that was not there.
//
// That matters beyond the false positive. A security test nobody trusts gets
// muted, and the way this one would have been muted is by loosening the pattern
// until it stopped complaining — which is exactly how a check stops catching the
// real thing. Parsing the output with the same HTML5 parser a browser
// implements answers the question properly: what ELEMENTS and ATTRIBUTES does
// this actually become?
//
// It is also stricter. `<sc<script>ript>` and entity-encoded schemes defeat
// pattern matching and cannot defeat a parser, because by then the question is
// about nodes rather than about text.
func findExecutable(t *testing.T, out string) []string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(out))
	if err != nil {
		// x/net/html does not fail on malformed input by design, so this is a
		// genuine surprise rather than a bad payload.
		return []string{"output did not parse: " + err.Error()}
	}

	// Elements whose entire purpose is to load or execute something else.
	forbiddenTags := map[string]bool{
		"script": true, "iframe": true, "object": true, "embed": true,
		"applet": true, "base": true, "frame": true, "frameset": true,
		"foreignobject": true,
	}
	// Attributes carrying a URL the browser will fetch or navigate to.
	urlAttrs := map[string]bool{
		"href": true, "src": true, "action": true, "formaction": true,
		"xlink:href": true, "data": true, "poster": true, "background": true,
	}

	var found []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			name := strings.ToLower(n.Data)
			if forbiddenTags[name] {
				found = append(found, "element <"+name+">")
			}
			// <meta charset> is legitimate on a whole page, which Snapshot
			// serves. <meta http-equiv=refresh> is a navigation primitive.
			if name == "meta" {
				for _, a := range n.Attr {
					if strings.EqualFold(a.Key, "http-equiv") {
						found = append(found, "<meta http-equiv> (navigation)")
					}
				}
			}
			for _, a := range n.Attr {
				key := strings.ToLower(a.Key)
				if a.Namespace != "" {
					key = strings.ToLower(a.Namespace) + ":" + key
				}
				// An event handler is decided by the attribute NAME. This is the
				// distinction the regex could not make.
				if strings.HasPrefix(key, "on") && len(key) > 2 {
					found = append(found, "event handler "+key+" on <"+name+">")
				}
				if key == "srcdoc" {
					found = append(found, "srcdoc on <"+name+">")
				}
				if urlAttrs[key] && isExecutableURL(a.Val) {
					found = append(found, key+"=\""+a.Val+"\" on <"+name+"> is an executable URL")
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return found
}

// isExecutableURL normalises a URL the way a browser does before reading its
// scheme, then asks whether that scheme runs code.
//
// Browsers strip leading and embedded whitespace and C0 control characters —
// including NUL, tab and newline — before parsing the scheme, which is why
// `jav&#x09;ascript:` and "java\x00script:" are live attacks and a naive
// HasPrefix check is not enough.
func isExecutableURL(raw string) bool {
	var b strings.Builder
	for _, r := range raw {
		if r > 0x20 && r != 0x7f {
			b.WriteRune(r)
		}
	}
	s := strings.ToLower(b.String())
	switch {
	case strings.HasPrefix(s, "javascript:"):
		return true
	case strings.HasPrefix(s, "vbscript:"):
		return true
	case strings.HasPrefix(s, "data:text/html"):
		return true
	case strings.HasPrefix(s, "data:image/svg+xml"):
		// An SVG is a document that can carry script. Loaded through <img> a
		// browser will not run it, but this is about what the markup permits,
		// not about where it happens to be used today.
		return true
	}
	return false
}

// seeds are the attacks worth naming, and each is a real technique rather than a
// variation on the same one.
var seeds = []string{
	`<script>alert(1)</script>`,
	`<img src=x onerror=alert(1)>`,
	`<a href="javascript:alert(1)">x</a>`,
	`<a href="jav&#x09;ascript:alert(1)">x</a>`,         // entity-split scheme
	`<a href=" javascript:alert(1)">x</a>`,              // leading space
	`<a href="JaVaScRiPt:alert(1)">x</a>`,               // case
	`<a href="java` + "\x00" + `script:alert(1)">x</a>`, // NUL inside the scheme
	`<iframe src="data:text/html,<script>alert(1)</script>"></iframe>`,
	`<iframe srcdoc="&lt;script&gt;alert(1)&lt;/script&gt;"></iframe>`,
	`<svg><script>alert(1)</script></svg>`,
	`<svg><foreignObject><script>alert(1)</script></foreignObject></svg>`,
	`<math><mtext><table><mglyph><style><img src=x onerror=alert(1)>`, // mXSS classic
	`<noscript><p title="</noscript><img src=x onerror=alert(1)>">`,   // mXSS via noscript
	`<template><script>alert(1)</script></template>`,
	`<base href="https://evil.example/">`,
	`<meta http-equiv="refresh" content="0;url=https://evil.example">`,
	`<object data="javascript:alert(1)"></object>`,
	`<embed src="data:text/html,<script>alert(1)</script>">`,
	`<form action="javascript:alert(1)"><button formaction="javascript:alert(1)">x`,
	`<style>@import 'https://evil.example/x.css';</style>`,
	`<div style="background:url(javascript:alert(1))">x</div>`,
	`<img srcset="javascript:alert(1) 1x">`,
	`<a href="&#106;avascript:alert(1)">x</a>`,
	`<xss style="behavior:url(#default#time2)">`,
	`<p>ordinary <b>prose</b> with a <a href="https://example.com">link</a>.</p>`,
	``,
	`<`,
	`<<<<<<script>`,
	strings.Repeat(`<div>`, 200) + `<script>alert(1)</script>`,
}

// FuzzHTMLNeverEmitsExecutableMarkup is the invariant, across every policy.
//
// Every policy, not just the widest one: a payload blocked by Feed and permitted
// by Snapshot would be a hole in the tier that renders WHOLE FETCHED PAGES,
// which is the most hostile input this application accepts.
func FuzzHTMLNeverEmitsExecutableMarkup(f *testing.F) {
	for _, s := range seeds {
		f.Add(s)
	}
	policies := []Policy{Feed, Newsletter, Archived, Public, Note, Snapshot}

	f.Fuzz(func(t *testing.T, in string) {
		for _, p := range policies {
			out := HTML(in, p)
			for _, bad := range findExecutable(t, out) {
				t.Errorf("policy %v emitted %s\n  in:  %q\n  out: %q",
					p, bad, truncate(in), truncate(out))
			}
		}
	})
}

// FuzzHTMLIsIdempotent: sanitizing twice must equal sanitizing once.
//
// This is not tidiness. A sanitizer whose output changes on a second pass is one
// whose output is not in its own accepted language — and that is the signature of
// mutation XSS, where the browser's parser re-reads the output differently from
// how the sanitizer wrote it. If f(f(x)) != f(x), some parser somewhere disagrees
// with this one, and that disagreement is the bug.
func FuzzHTMLIsIdempotent(f *testing.F) {
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		for _, p := range []Policy{Feed, Public, Note, Snapshot} {
			once := HTML(in, p)
			twice := HTML(once, p)
			if a, b := structure(once), structure(twice); a != b {
				t.Errorf("policy %v is not structurally idempotent — the output re-parses "+
					"into different markup\n  in:    %q\n  once:  %q\n  twice: %q\n"+
					"  shape once:  %s\n  shape twice: %s",
					p, truncate(in), truncate(once), truncate(twice), truncate(a), truncate(b))
			}
		}
	})
}

// structure reduces sanitized output to the shape a browser would build from it:
// element names and their attributes, in order, with text collapsed to a marker.
//
// # Why the comparison is structural rather than byte-for-byte
//
// The first version compared the two passes as strings, and the fuzzer found
// `<a00> 0000…` — an unknown element dropped on pass one, leaving a leading
// space that pass two trims. The bytes differ; nothing about the MARKUP does.
//
// That distinction is the whole point, and it is not the same as loosening the
// check until it stops complaining. The security property is that re-parsing
// yields the same ELEMENTS AND ATTRIBUTES — because mutation XSS is precisely
// the case where it does not, where a browser reads the sanitizer's output as
// different nodes than the sanitizer believed it wrote. Whitespace has no
// bearing on that, and asserting on it would have meant this test failing for
// cosmetic reasons until somebody deleted it.
func structure(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return "unparseable: " + err.Error()
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.ElementNode:
			b.WriteString("<" + strings.ToLower(n.Data))
			// Sorted, because attribute ORDER is presentation and only the set
			// of name/value pairs is semantics.
			attrs := make([]string, 0, len(n.Attr))
			for _, a := range n.Attr {
				attrs = append(attrs, strings.ToLower(a.Key)+"="+a.Val)
			}
			sort.Strings(attrs)
			for _, a := range attrs {
				b.WriteString(" " + a)
			}
			b.WriteString(">")
		case html.TextNode:
			// Presence, not content: collapsed so that trimming, entity
			// normalisation and whitespace folding do not read as a structural
			// change. Text cannot execute; elements and attributes can.
			if strings.TrimSpace(n.Data) != "" {
				b.WriteString("#text")
			}
		case html.CommentNode:
			b.WriteString("#comment")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			b.WriteString("</" + strings.ToLower(n.Data) + ">")
		}
	}
	walk(doc)
	return b.String()
}

// FuzzTextNeverEmitsMarkup checks the property Text() can actually promise.
//
// # The invariant this does NOT assert, and why
//
// It first asserted that the output contains no markup, and the fuzzer answered
// with `<<A>A>`, which flattens to the literal string `<A>`: the parser reads
// the leading `<` as text, `<A>` as an element, and `A>` as text, so the two
// TEXT nodes concatenate into something that looks like a tag.
//
// That is not a defect, and "escape it" would be the wrong fix. Text() extracts
// PLAIN TEXT, and plain text legitimately contains angle brackets — an article
// about `5 < 10` must survive. A function cannot be both a faithful text
// extractor and a producer of HTML-safe strings; those are different jobs, and
// conflating them is how `&lt;` ends up displayed to readers.
//
// The safety of the value therefore belongs to its CONSUMER, and the consumers
// are correct: the sole render path is `html.Text(it.GetSummary())` in
// client/view/panes.go, which builds a DOM text node, and a text node cannot be
// markup by construction. What this asserts is the part Text() genuinely owns.
func FuzzTextNeverEmitsMarkup(f *testing.F) {
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := Text(in)

		// 1. Script and style CONTENT must never surface. A summary that quietly
		//    contained a publisher's JavaScript would be both nonsense to read
		//    and, in any consumer that ever interpolates rather than escapes, a
		//    payload waiting for one.
		for _, leak := range []string{"alert(1)", "@import", "behavior:url"} {
			if strings.Contains(out, leak) {
				t.Errorf("Text leaked script/style content %q\n  in:  %q\n  out: %q",
					leak, truncate(in), truncate(out))
			}
		}

		// 2. Stable: extracting twice must not discover new text. A second pass
		//    finding more than the first would mean the first left markup behind
		//    that the parser later re-read as content.
		if again := Text(out); again != out {
			t.Errorf("Text is not stable\n  in:    %q\n  once:  %q\n  twice: %q",
				truncate(in), truncate(out), truncate(again))
		}
	})
}

// TestKnownPayloadsAreNeutralised runs the named attacks as an ordinary test, so
// they are checked on every `go test` rather than only when somebody runs the
// fuzzer with -fuzz.
//
// The fuzz corpus and the regression suite being the same list is deliberate:
// one place to add an attack when a new one is learned.
func TestKnownPayloadsAreNeutralised(t *testing.T) {
	for _, p := range []Policy{Feed, Newsletter, Archived, Public, Note, Snapshot} {
		for _, in := range seeds {
			out := HTML(in, p)
			for _, bad := range findExecutable(t, out) {
				t.Errorf("policy %v emitted %s\n  in:  %q\n  out: %q",
					p, bad, truncate(in), truncate(out))
			}
		}
	}
}

func truncate(s string) string {
	const max = 220
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
