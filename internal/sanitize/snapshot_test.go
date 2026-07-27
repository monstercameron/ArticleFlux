package sanitize

import (
	"strings"
	"testing"
	"time"
)

// The Snapshot policy is the only hand-written walk in this package — every
// other policy delegates the tree work to GWC. These are the tests that keep
// that exception honest: the corpus and the fuzzer in sanitize_test.go prove it
// is safe, and these prove it is *useful*, which is the other half of why it
// exists. A snapshot sanitizer that passes every security test and strips the
// stylesheet has failed at its job.

func TestSnapshotKeepsWhatAPageNeedsToRender(t *testing.T) {
	in := `<!doctype html><html><head>
<title>Piece</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<link rel="stylesheet" href="/s.css">
<style>.hero{color:red;background:url("/bg.png")}</style>
</head><body class="theme-dark">
<main id="content" class="wrap" style="max-width:62em">
<h1 class="headline">Title</h1>
<figure><img src="/a.png" srcset="/a.png 1x, /a2.png 2x" alt="A"><figcaption>Cap</figcaption></figure>
<table><tr><td colspan="2">cell</td></tr></table>
</main></body></html>`

	got := HTML(in, Snapshot)
	for _, want := range []string{
		"<style", "<link", `class="theme-dark"`, `id="content"`, "max-width:62em",
		`class="headline"`, "srcset=", `colspan="2"`, "<figcaption", "viewport",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q — the page will not render as itself:\n%s", want, got)
		}
	}
}

func TestSnapshotDropsWhatCanAct(t *testing.T) {
	in := `<html><body>
<script>alert(1)</script>
<form action="/steal"><input name="pw" type="password"><button>Go</button></form>
<div onclick="alert(2)" onmouseover="alert(3)">x</div>
<a href="javascript:alert(4)">link</a>
<a href="data:text/html,<script>alert(5)</script>">data link</a>
<img src="data:image/svg+xml,<svg onload=alert(6)>">
<iframe src="//evil.tld"></iframe>
<object data="//evil.tld"></object>
<meta http-equiv="refresh" content="0;url=//evil.tld">
<noscript><img src="x" onerror="alert(7)"></noscript>
<template><script>alert(8)</script></template>
<p>Kept</p>
</body></html>`

	got := strings.ToLower(HTML(in, Snapshot))
	for _, gone := range []string{
		"<script", "<form", "<input", "<button", "onclick", "onmouseover",
		"javascript:", "data:text/html", "data:image/svg", "<iframe", "<object",
		"refresh", "<noscript", "<template", "onerror", "onload",
	} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived:\n%s", gone, got)
		}
	}
	if !strings.Contains(got, "kept") {
		t.Errorf("real content was lost:\n%s", got)
	}
}

// An unknown tag must lose the tag and keep the words. Dropping the subtree
// would delete content; keeping the tag is what let `scr<script` through.
func TestSnapshotUnwrapsUnknownElements(t *testing.T) {
	got := HTML(`<zzz><p>words</p></zzz>`, Snapshot)
	if !strings.Contains(got, "words") {
		t.Errorf("unwrapping lost the content: %s", got)
	}
	if strings.Contains(got, "<zzz") {
		t.Errorf("unknown tag survived: %s", got)
	}
}

// <style> is a raw text element: html.Render writes its contents back without
// escaping, so a `<` that goes in comes out and can re-open the parser.
func TestSnapshotStripsAngleBracketsFromCSS(t *testing.T) {
	got := HTML(`<style>a{}</style><script>alert(1)</script>`, Snapshot)
	if strings.Contains(got, "<script") {
		t.Errorf("raw text broke out: %s", got)
	}
	got2 := HTML("<style>x{}\x3c/style\x3e<script>alert(1)</script>", Snapshot)
	if strings.Contains(strings.ToLower(got2), "<script") {
		t.Errorf("raw text broke out: %s", got2)
	}
}

// An attribute that means nothing on an element must not ride along on it —
// that is how an unchecked value ends up somewhere nobody thought to check.
func TestSnapshotScopesElementSpecificAttributes(t *testing.T) {
	got := HTML(`<a content="javascript:alert(1)" href="/ok">x</a>`, Snapshot)
	if strings.Contains(strings.ToLower(got), "javascript:") {
		t.Errorf("content= survived on an anchor: %s", got)
	}
	if !strings.Contains(got, `href="/ok"`) {
		t.Errorf("the real href was lost: %s", got)
	}
}

func TestSnapshotAllowsInlineImagesButNotInlineDocuments(t *testing.T) {
	ok := HTML(`<img src="data:image/png;base64,AAAA">`, Snapshot)
	if !strings.Contains(ok, "data:image/png") {
		t.Errorf("an inline PNG should survive: %s", ok)
	}
	for _, bad := range []string{
		`<a href="data:text/html,<b>x</b>">l</a>`,
		`<img src="data:image/svg+xml,<svg/onload=alert(1)>">`,
	} {
		got := strings.ToLower(HTML(bad, Snapshot))
		if strings.Contains(got, "data:text/html") || strings.Contains(got, "data:image/svg") {
			t.Errorf("an inline document survived: %s", got)
		}
	}
}

// This walk recurses over attacker-controlled input. A quadratic blowup here is
// a denial of service on a self-hosted box with one CPU to spare, and it would
// arrive as "the reader hangs when I open a page" rather than as a crash.
func TestSnapshotDoesNotBlowUpOnPathologicalInput(t *testing.T) {
	cases := map[string]string{
		"repeated invalid utf8": strings.Repeat("\xf1", 200_000),
		"deep nesting":          strings.Repeat("<div>", 20_000) + "x" + strings.Repeat("</div>", 20_000),
		"many attributes":       "<div " + strings.Repeat(`data-x="y" `, 20_000) + ">x</div>",
		"huge style":            "<style>" + strings.Repeat("a{color:red}", 40_000) + "</style>",
		"unclosed style":        "<style>" + strings.Repeat("<", 100_000),
		"many unknown tags":     strings.Repeat("<zzz>", 20_000) + "x",
	}
	for name, in := range cases {
		start := time.Now()
		_ = HTML(in, Snapshot)
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s took %v on %d bytes", name, d, len(in))
		}
	}
}
