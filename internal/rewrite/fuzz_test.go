package rewrite

import (
	"net/url"
	"testing"
)

// FuzzHTML feeds arbitrary strings through HTML as both a fragment (feed
// content, article extraction) and a whole document (tier 2/2r), which is
// every shape this package is ever handed. Two invariants matter:
//
//   - Never panics, on markup a stranger's publisher produced.
//   - Idempotence: a second pass changes nothing. This is not just a nice
//     property — TestSecondPassIsANoOp pins it for a handful of fixed cases,
//     and the package comment explains why it matters (a cached article
//     re-rendered through the proxy must not double the prefix each time).
//     Fuzzing generalises that fixed-case test to arbitrary markup instead of
//     only the shapes a person thought to write down.
//
// This DID find a real bug on first run: a <base href> with a non-http(s)
// scheme is pinned as a failing seed corpus entry here and explained in
// TestBaseHrefWithNonHTTPSchemeDoesNotSuppressRewriting in rewrite_test.go.
func FuzzHTML(f *testing.F) {
	seeds := []string{
		`<p><img src="pic.png"></p>`,
		`<img srcset="a.png 1x, b.png 2x, c.png">`,
		`<style>.a{background:url(bg.png)}</style>`,
		`<div style="background:url('x.png')"></div>`,
		`<link rel="stylesheet" href="s.css" integrity="sha384-x">`,
		`<!doctype html><html><head><base href="https://cdn.example/"></head><body><img src="a.png"></body></html>`,
		`<base href="https://evil.example/"><img src="a.png">`,
		`<meta http-equiv="Content-Security-Policy" content="default-src 'self'">`,
		`<img src="data:image/png;base64,AAAA">`,
		`<a href="#foot">x</a>`,
		`<img src="mailto:x@y.z">`,
		`<style>@import "more.css"; @import url(more2.css);</style>`,
		`<style>/* unterminated`,
		``,
		`<`,
		`<img srcset=",,,,">`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	base := mustBaseForFuzz("https://pub.example/p/")
	opt := Options{Base: base, Asset: prox,
		Link:          func(abs string) string { return "https://proxy.test/page?u=" + url.QueryEscape(abs) },
		DropIntegrity: true, DropMetaCSP: true}

	f.Fuzz(func(t *testing.T, in string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("HTML(%q) panicked: %v", in, r)
			}
		}()
		once, err := HTML(in, opt)
		if err != nil {
			// Base is always set here, so an error means html.Parse or
			// ParseFragment rejected the input — x/net/html does not do that
			// for arbitrary byte strings, so this is worth seeing rather than
			// silently skipping.
			t.Fatalf("HTML(%q) returned an error: %v", in, err)
		}
		twice, err := HTML(once, opt)
		if err != nil {
			t.Fatalf("second pass on %q returned an error: %v", once, err)
		}
		if once != twice {
			t.Fatalf("not idempotent:\nin:     %q\nfirst:  %q\nsecond: %q", in, once, twice)
		}
	})
}

// mustBase panics rather than calling t.Fatalf, so it can be used to build a
// package-level fixture for the fuzz seed corpus outside of a test function.
func mustBaseForFuzz(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
