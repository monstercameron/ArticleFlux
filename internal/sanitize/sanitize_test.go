package sanitize

import (
	"strings"
	"testing"
)

// allPolicies is what makes this an acceptance bar rather than a spot check:
// every vector below is run through every policy, so a policy added later
// inherits the whole corpus automatically and a policy loosened later has to
// survive it.
var allPolicies = []Policy{Feed, Newsletter, Archived, Public, Note, Snapshot}

// The XSS corpus. Each entry is a real technique rather than an invented one:
// they are the shapes that appear in payload lists, ordered roughly by how often
// they still work somewhere.
var xssCorpus = []struct {
	name string
	in   string
}{
	{"script element", `<script>alert(1)</script>`},
	{"script with attributes", `<script type="text/javascript" src="//evil.tld/x.js"></script>`},
	{"inline handler", `<div onclick="alert(1)">click</div>`},
	{"handler on an allowed tag", `<p onmouseover="alert(1)">hover</p>`},
	{"uppercase handler", `<p ONERROR="alert(1)">x</p>`},
	{"img onerror", `<img src=x onerror=alert(1)>`},
	{"javascript: href", `<a href="javascript:alert(1)">click</a>`},
	{"javascript: with whitespace", `<a href="  java&#115;cript:alert(1)">click</a>`},
	{"javascript: mixed case", `<a href="JaVaScRiPt:alert(1)">click</a>`},
	{"data: URL", `<a href="data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==">x</a>`},
	{"data: image src", `<img src="data:text/html,<script>alert(1)</script>">`},
	{"vbscript:", `<a href="vbscript:msgbox(1)">x</a>`},
	{"iframe", `<iframe src="//evil.tld"></iframe>`},
	{"iframe srcdoc", `<iframe srcdoc="&lt;script&gt;alert(1)&lt;/script&gt;"></iframe>`},
	{"object", `<object data="//evil.tld/x.swf"></object>`},
	{"embed", `<embed src="//evil.tld/x.swf">`},
	{"svg onload", `<svg onload="alert(1)"></svg>`},
	{"svg script child", `<svg><script>alert(1)</script></svg>`},
	{"math annotation", `<math><annotation-xml encoding="text/html"><script>alert(1)</script></annotation-xml></math>`},
	{"style block", `<style>body{background:url("javascript:alert(1)")}</style>`},
	{"style attribute", `<p style="background:url(javascript:alert(1))">x</p>`},
	{"expression in style", `<div style="width:expression(alert(1))">x</div>`},
	{"meta refresh", `<meta http-equiv="refresh" content="0;url=javascript:alert(1)">`},
	{"base tag", `<base href="//evil.tld/">`},
	{"form action", `<form action="javascript:alert(1)"><input type=submit></form>`},
	{"formaction on button", `<button formaction="javascript:alert(1)">x</button>`},
	{"template smuggling", `<template><script>alert(1)</script></template>`},
	{"noscript smuggling", `<noscript><p title="</noscript><script>alert(1)</script>">x</p></noscript>`},
	{"unclosed tag confusion", `<div><p>text<script>alert(1)`},
	{"null byte in scheme", "<a href=\"java\x00script:alert(1)\">x</a>"},
	{"entity-encoded handler", `<img src=x onerror=&#97;lert(1)>`},
	{"double-encoded", `<a href="&amp;#106;avascript:alert(1)">x</a>`},
	{"backtick attribute", "<img src=`x` onerror=alert(1)>"},
	{"malformed attribute quoting", `<img src="x"onerror="alert(1)">`},
	{"srcset with javascript", `<img srcset="javascript:alert(1) 1x">`},
	{"xlink href", `<svg><a xlink:href="javascript:alert(1)"><text>x</text></a></svg>`},
	{"nested script split", `<scr<script>ipt>alert(1)</scr</script>ipt>`},
	{"comment breakout", `<!--<img src="--><img src=x onerror=alert(1)//">`},
	{"cdata", `<![CDATA[<script>alert(1)</script>]]>`},
	{"marquee onstart", `<marquee onstart="alert(1)">x</marquee>`},
	{"details ontoggle", `<details ontoggle="alert(1)" open>x</details>`},
	{"video onerror", `<video><source onerror="alert(1)"></video>`},
	{"audio onerror", `<audio src=x onerror=alert(1)>`},
	{"input autofocus onfocus", `<input autofocus onfocus="alert(1)">`},
	{"select onfocus", `<select autofocus onfocus="alert(1)"><option>x</option></select>`},
	{"body onload", `<body onload="alert(1)">x</body>`},
	{"link stylesheet", `<link rel="stylesheet" href="//evil.tld/x.css">`},
	{"import in style", `<style>@import "//evil.tld/x.css";</style>`},
}

// forbidden are substrings that must not survive under any policy. Checking the
// output for these is cruder than parsing it, and that is deliberate: a parser
// in the test would share assumptions with the parser in the sanitizer, and a
// shared wrong assumption is exactly the bug this is trying to catch.
var forbidden = []string{
	"<script", "javascript:", "vbscript:", "onerror", "onclick", "onload",
	"onmouseover", "ontoggle", "onfocus", "onstart", "<iframe", "<object",
	"<embed", "<base", "<form", "srcdoc",
	"formaction", "expression(", "xlink:href",
}

// styling is the subset of `forbidden` that is a POLICY choice rather than a
// safety property: markup that brings its own presentation.
//
// Every prose policy refuses it, and Snapshot cannot, because a whole fetched
// page whose stylesheet has been removed is not the page — it is Reader mode
// with worse typography, delivered by a feature that promised a website
// (§10.1b). Splitting the list is the only honest way to say that; folding
// Snapshot in would have meant deleting assertions the other five need.
//
// What carries the weight for Snapshot instead:
//
//   - Everything in `forbidden` above still applies to it. Nothing that can
//     execute, navigate or collect input is exempted.
//   - Remote CSS is neutralised *upstream* rather than here:
//     `internal/pageproxy` rewrites every `url()` and `@import` to our own
//     proxy before this ever runs, and TestBaseHrefIsHonouredDespiteSanitize
//     DroppingIt pins that ordering. Sanitizing without rewriting first is not
//     a supported call, and there is no caller that does it.
//   - The output is served under `Content-Security-Policy: sandbox` with
//     `script-src 'none'`, in an opaque origin, so this walk is the inner of
//     two layers.
var styling = []string{"<style", "<link", "<meta", "@import"}

// tagsOnly returns everything between < and >, concatenated.
//
// Crude on purpose, and still cruder than parsing — the point of the substring
// checks is not to share a parser with the code under test. This narrows WHERE
// they look without changing WHAT they look for.
func tagsOnly(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			return b.String()
		}
		s = s[i:]
		j := strings.IndexByte(s, '>')
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:j+1])
		s = s[j+1:]
	}
}

func TestXSSCorpusIsNeutralisedUnderEveryPolicy(t *testing.T) {
	for _, c := range xssCorpus {
		for _, p := range allPolicies {
			t.Run(c.name+"/"+p.String(), func(t *testing.T) {
				got := strings.ToLower(HTML(c.in, p))
				bad := forbidden
				if p != Snapshot {
					bad = append(append([]string{}, forbidden...), styling...)
				}
				for _, b := range bad {
					if strings.Contains(got, b) {
						t.Errorf("%q survived\n  in:  %s\n  out: %s", b, c.in, got)
					}
				}
			})
		}
	}
}

// An unmapped policy value must fail closed. This is the case that turns a
// refactor — someone adds a constant and forgets the table — from a silent
// downgrade into the most restrictive behaviour available.
func TestUnknownPolicyFailsClosed(t *testing.T) {
	const bogus = Policy(9999)
	in := `<table><tr><td>cell</td></tr></table><p>text</p><img src="https://x/y.png">`
	got := HTML(in, bogus)
	if strings.Contains(got, "<table") || strings.Contains(got, "<img") {
		t.Errorf("an unmapped policy allowed more than Public does: %s", got)
	}
	if !strings.Contains(got, "text") {
		t.Errorf("failing closed should still keep text: %s", got)
	}
}

func TestPoliciesDifferWhereTheyShould(t *testing.T) {
	const withImage = `<p>Look at this</p><img src="https://cdn.example/photo.jpg" alt="a photo">`

	t.Run("feed keeps images because a review is mostly photographs", func(t *testing.T) {
		if !strings.Contains(HTML(withImage, Feed), "<img") {
			t.Error("Feed dropped an image")
		}
	})
	t.Run("newsletter drops images because a remote image is a read receipt", func(t *testing.T) {
		got := HTML(withImage, Newsletter)
		if strings.Contains(got, "<img") {
			t.Errorf("Newsletter kept an image: %s", got)
		}
		if !strings.Contains(got, "Look at this") {
			t.Errorf("Newsletter dropped the text too: %s", got)
		}
	})

	const withTable = `<table><tr><td>a</td></tr></table>`
	if !strings.Contains(HTML(withTable, Feed), "<table") {
		t.Error("Feed should keep tables")
	}
	if strings.Contains(HTML(withTable, Public), "<table") {
		t.Error("Public should not keep tables")
	}
}

func TestTrackingPixelsAreRemoved(t *testing.T) {
	cases := []struct {
		name string
		in   string
		gone bool
	}{
		{"1x1 by attribute", `<img src="https://mail.example/o.gif" width="1" height="1">`, true},
		{"zero by attribute", `<img src="https://mail.example/o.gif" width="0" height="0">`, true},
		{"named pixel", `<img src="https://mail.example/pixel.gif">`, true},
		{"named beacon", `<img src="https://mail.example/beacon?id=7">`, true},
		{"spacer gif", `<img src="https://old.example/img/spacer.gif">`, true},
		// The narrowness of the heuristic is the point: a real photograph that
		// happens to be served from a path containing "tracks" must survive.
		{"a real photo", `<img src="https://cdn.example/albums/soundtracks/cover.jpg" width="800" height="600">`, false},
		{"no dimensions at all", `<img src="https://cdn.example/photo.jpg">`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := HTML(c.in, Feed)
			has := strings.Contains(got, "<img")
			if c.gone && has {
				t.Errorf("pixel survived: %s", got)
			}
			if !c.gone && !has {
				t.Errorf("real image was removed: %s", got)
			}
		})
	}
}

func TestLinksAreHardened(t *testing.T) {
	cases := []struct{ name, in string }{
		{"plain link", `<a href="https://example.com/a">x</a>`},
		{"already targeted", `<a href="https://example.com/a" target="_blank">x</a>`},
		{"partial rel", `<a href="https://example.com/a" rel="noopener">x</a>`},
		{"unrelated rel", `<a href="https://example.com/a" rel="nofollow">x</a>`},
		{"self target", `<a href="https://example.com/a" target="_self">x</a>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := HTML(c.in, Feed)
			for _, want := range []string{"noopener", "noreferrer"} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %s: %s", want, got)
				}
			}
			// A rel the publisher set for their own reasons is kept.
			if strings.Contains(c.in, "nofollow") && !strings.Contains(got, "nofollow") {
				t.Errorf("dropped the publisher's own rel: %s", got)
			}
			// And it must not be doubled.
			if strings.Count(got, "noopener") != 1 {
				t.Errorf("noopener appears %d times: %s", strings.Count(got, "noopener"), got)
			}
		})
	}
}

func TestContentSurvivesSanitisation(t *testing.T) {
	in := `<h2>Heading</h2><p>Some <strong>bold</strong> and <em>italic</em> text with a
		<a href="https://example.com">link</a>.</p><blockquote>A quote.</blockquote>
		<ul><li>one</li><li>two</li></ul><pre><code>fmt.Println("hi")</code></pre>`
	got := HTML(in, Feed)
	for _, want := range []string{"Heading", "bold", "italic", "link", "A quote", "one", "two", "fmt.Println"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q from the output: %s", want, got)
		}
	}
	for _, want := range []string{"<h2", "<strong", "<em", "<blockquote", "<li", "<pre", "<code"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost the %s element: %s", want, got)
		}
	}
}

func TestTextStripsMarkup(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<p>Hello</p><p>world</p>`, "Hello world"},
		// The block-boundary space. Without it these weld into "Helloworld" and
		// the word count is quietly wrong.
		{`<div>Hello</div><div>world</div>`, "Hello world"},
		{`<p>a<br>b</p>`, "a b"},
		{`<script>alert(1)</script><p>safe</p>`, "safe"},
		{`<style>p{color:red}</style><p>safe</p>`, "safe"},
		{`<p>  lots   of    space  </p>`, "lots of space"},
		{`<ul><li>one</li><li>two</li></ul>`, "one two"},
		{`plain text, no markup`, "plain text, no markup"},
		{``, ""},
	}
	for _, c := range cases {
		if got := Text(c.in); got != c.want {
			t.Errorf("Text(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Sanitising twice must not change the result. Anything that fails this is
// rewriting its own output, which eventually eats real content.
func TestSanitisationIsIdempotent(t *testing.T) {
	inputs := []string{
		`<p>Hello <a href="https://x.tld">link</a></p>`,
		`<img src="https://cdn.example/p.jpg" alt="x">`,
		`<blockquote><p>quoted</p></blockquote>`,
	}
	for _, p := range allPolicies {
		for _, in := range inputs {
			once := HTML(in, p)
			twice := HTML(once, p)
			if once != twice {
				t.Errorf("policy %s not idempotent\n  in:    %s\n  once:  %s\n  twice: %s", p, in, once, twice)
			}
		}
	}
}

func FuzzHTMLNeverEmitsAScriptTag(f *testing.F) {
	for _, c := range xssCorpus {
		f.Add(c.in)
	}
	f.Fuzz(func(t *testing.T, in string) {
		for _, p := range allPolicies {
			got := strings.ToLower(HTML(in, p))
			// Tag checks are safe against the raw output: a literal "<script"
			// occurring in TEXT is escaped to "&lt;script" by the renderer, so
			// an unescaped one really is an element.
			for _, bad := range []string{"<script", "<iframe"} {
				if strings.Contains(got, bad) {
					t.Fatalf("policy %s emitted %q for input %q: %s", p, bad, in, got)
				}
			}
			// Attribute checks are NOT safe against the raw output, and the
			// fuzzer proved it twice: the inputs "JAVASCRiPt:0" and "onerror=0"
			// are prose, and prose containing those characters is not an attack.
			// Sanitizing text to text is correct; a test that fails on it is one
			// that gets silenced rather than believed.
			//
			// What matters is whether the string is REACHABLE as an attribute,
			// so the check looks only inside tags.
			markup := tagsOnly(got)
			for _, bad := range []string{"onerror=", "onload=", "onclick=", "javascript:"} {
				if strings.Contains(markup, bad) {
					t.Fatalf("policy %s emitted %q inside a tag for input %q: %s", p, bad, in, got)
				}
			}
		}
	})
}
