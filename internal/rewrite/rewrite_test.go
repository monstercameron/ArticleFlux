package rewrite

import (
	"net/url"
	"strings"
	"testing"
)

// prox is the rewriter under test everywhere below: it prefixes, and it
// declines anything it has already prefixed. That second half is what makes a
// second pass a no-op, and it is the shape every real caller has to have.
func prox(abs string) string {
	if strings.HasPrefix(abs, "https://proxy.test/a?u=") {
		return ""
	}
	return "https://proxy.test/a?u=" + url.QueryEscape(abs)
}

func mustBase(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("base %q: %v", raw, err)
	}
	return u
}

func run(t *testing.T, in string, opt Options) string {
	t.Helper()
	out, err := HTML(in, opt)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	return out
}

func TestRewritesTheObviousAndTheEasilyMissed(t *testing.T) {
	base := mustBase(t, "https://pub.example/posts/one/")
	opt := Options{Base: base, Asset: prox, DropIntegrity: true, DropMetaCSP: true}

	cases := []struct {
		name string
		in   string
		want []string // substrings that must appear
		not  []string // substrings that must not
	}{
		{
			name: "relative img src resolves against the base",
			in:   `<p><img src="pic.png"></p>`,
			want: []string{url.QueryEscape("https://pub.example/posts/one/pic.png")},
			not:  []string{`src="pic.png"`},
		},
		{
			name: "protocol-relative takes the base scheme",
			in:   `<img src="//cdn.example/x.jpg">`,
			want: []string{url.QueryEscape("https://cdn.example/x.jpg")},
		},
		{
			name: "root-relative",
			in:   `<img src="/static/a.png">`,
			want: []string{url.QueryEscape("https://pub.example/static/a.png")},
		},
		{
			name: "srcset keeps its descriptors",
			in:   `<img srcset="a.png 1x, b.png 2x" src="a.png">`,
			want: []string{" 1x", " 2x", url.QueryEscape("https://pub.example/posts/one/b.png")},
			not:  []string{`srcset="a.png`},
		},
		{
			name: "srcset with width descriptors and odd spacing",
			in:   `<img srcset="  a.png   400w ,b.png 800w  ">`,
			want: []string{" 400w", " 800w"},
		},
		{
			name: "srcset with no descriptor at all",
			in:   `<img srcset="a.png, b.png">`,
			want: []string{url.QueryEscape("https://pub.example/posts/one/a.png"), url.QueryEscape("https://pub.example/posts/one/b.png")},
			not:  []string{`srcset="a.png`},
		},
		{
			// Spec behaviour, not a bug: a comma only separates candidates when
			// it trails the URL, so this is one candidate with a comma in it.
			// Browsers fetch it exactly that way; matching them is the point.
			name: "srcset comma with no space is one candidate",
			in:   `<img srcset="a.png,b.png">`,
			want: []string{url.QueryEscape("https://pub.example/posts/one/a.png,b.png")},
		},
		{
			name: "source inside picture",
			in:   `<picture><source srcset="w.webp"><img src="f.jpg"></picture>`,
			want: []string{url.QueryEscape("https://pub.example/posts/one/w.webp")},
		},
		{
			name: "video poster and source",
			in:   `<video poster="p.jpg" src="v.mp4"></video>`,
			want: []string{url.QueryEscape("https://pub.example/posts/one/p.jpg"), url.QueryEscape("https://pub.example/posts/one/v.mp4")},
		},
		{
			name: "url() inside a style block",
			in:   `<style>.a{background:url(bg.png) no-repeat}</style>`,
			want: []string{url.QueryEscape("https://pub.example/posts/one/bg.png")},
			not:  []string{"url(bg.png)"},
		},
		{
			name: "url() inside a style attribute",
			in:   `<div style="background-image:url('x.png')"></div>`,
			want: []string{url.QueryEscape("https://pub.example/posts/one/x.png")},
		},
		{
			name: "bare @import string",
			in:   `<style>@import "more.css";</style>`,
			want: []string{url.QueryEscape("https://pub.example/posts/one/more.css")},
		},
		{
			name: "@import url() form",
			in:   `<style>@import url(more.css);</style>`,
			want: []string{url.QueryEscape("https://pub.example/posts/one/more.css")},
		},
		{
			name: "stylesheet link",
			in:   `<link rel="stylesheet" href="s.css" integrity="sha384-xyz">`,
			want: []string{url.QueryEscape("https://pub.example/posts/one/s.css")},
			not:  []string{"integrity"},
		},
		{
			name: "canonical link is metadata, not a subresource",
			in:   `<link rel="canonical" href="https://pub.example/posts/one/">`,
			want: []string{`href="https://pub.example/posts/one/"`},
			not:  []string{"proxy.test"},
		},
		{
			name: "meta CSP is dropped",
			in:   `<meta http-equiv="Content-Security-Policy" content="default-src 'self'"><p>x</p>`,
			not:  []string{"Content-Security-Policy"},
		},
		{
			name: "data: URIs are already inline",
			in:   `<img src="data:image/png;base64,AAAA">`,
			want: []string{"data:image/png;base64,AAAA"},
			not:  []string{"proxy.test"},
		},
		{
			name: "fragment-only href is not a fetch",
			in:   `<a href="#foot">x</a>`,
			want: []string{`href="#foot"`},
		},
		{
			name: "mailto is left alone",
			in:   `<img src="mailto:x@y.z">`,
			not:  []string{"proxy.test"},
		},
		{
			name: "links are untouched when Link is nil",
			in:   `<a href="/other">x</a>`,
			want: []string{`href="/other"`},
			not:  []string{"proxy.test"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := run(t, tc.in, opt)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("unexpected %q in:\n%s", n, got)
				}
			}
		})
	}
}

// A second pass must change nothing. Without this, every re-render of a cached
// article doubles the proxy prefix, and the failure shows up as a 404 on an
// image that worked yesterday.
func TestSecondPassIsANoOp(t *testing.T) {
	opt := Options{Base: mustBase(t, "https://pub.example/p/"), Asset: prox,
		DropIntegrity: true, DropMetaCSP: true}
	in := `<div><img src="a.png" srcset="a.png 1x, b.png 2x">` +
		`<style>.x{background:url(c.png)}</style>` +
		`<div style="background:url('d.png')"></div></div>`

	once := run(t, in, opt)
	twice := run(t, once, opt)
	if once != twice {
		t.Errorf("not idempotent:\nfirst:  %s\nsecond: %s", once, twice)
	}
}

// <base href> retargets every relative URL on the page. Honouring it is
// correctness; removing it afterwards is the security half — a surviving <base>
// points anything we missed straight back at the origin.
func TestBaseHrefIsHonouredThenRemoved(t *testing.T) {
	opt := Options{Base: mustBase(t, "https://pub.example/p/"), Asset: prox}
	in := `<!doctype html><html><head><base href="https://cdn.example/assets/"></head>` +
		`<body><img src="pic.png"></body></html>`

	got := run(t, in, opt)
	if !strings.Contains(got, url.QueryEscape("https://cdn.example/assets/pic.png")) {
		t.Errorf("base href not honoured:\n%s", got)
	}
	if strings.Contains(got, "<base") {
		t.Errorf("base element survived:\n%s", got)
	}
}

// A <base> inside feed content would retarget the reading pane, not just its
// own item. Fragments therefore ignore it.
func TestFragmentIgnoresBase(t *testing.T) {
	opt := Options{Base: mustBase(t, "https://pub.example/p/"), Asset: prox}
	got := run(t, `<base href="https://evil.example/"><img src="pic.png">`, opt)
	if !strings.Contains(got, url.QueryEscape("https://pub.example/p/pic.png")) {
		t.Errorf("fragment base was honoured, it must not be:\n%s", got)
	}
}

// A fragment must not grow an <html><body> wrapper on the way through.
func TestFragmentStaysAFragment(t *testing.T) {
	opt := Options{Base: mustBase(t, "https://pub.example/p/"), Asset: prox}
	got := run(t, `<p>hello</p>`, opt)
	if strings.Contains(got, "<html") || strings.Contains(got, "<body") {
		t.Errorf("fragment was wrapped as a document: %s", got)
	}
	if got != "<p>hello</p>" {
		t.Errorf("fragment changed: %s", got)
	}
}

func TestDocumentStaysADocument(t *testing.T) {
	opt := Options{Base: mustBase(t, "https://pub.example/p/"), Asset: prox}
	got := run(t, `<!doctype html><html><body><p>hi</p></body></html>`, opt)
	if !strings.Contains(got, "<html") {
		t.Errorf("document lost its shell: %s", got)
	}
}

func TestLinkRewriterIsSeparateFromAssets(t *testing.T) {
	opt := Options{
		Base:  mustBase(t, "https://pub.example/p/"),
		Asset: prox,
		Link:  func(abs string) string { return "https://proxy.test/page?u=" + url.QueryEscape(abs) },
	}
	got := run(t, `<a href="/next"><img src="t.png"></a>`, opt)
	if !strings.Contains(got, "proxy.test/page?u=") {
		t.Errorf("link not rewritten: %s", got)
	}
	if !strings.Contains(got, "proxy.test/a?u=") {
		t.Errorf("asset not rewritten: %s", got)
	}
}

func TestNilAssetRewriterLeavesEverythingAlone(t *testing.T) {
	opt := Options{Base: mustBase(t, "https://pub.example/p/")}
	in := `<img src="a.png"><style>.x{background:url(b.png)}</style>`
	got := run(t, in, opt)
	if strings.Contains(got, "proxy.test") {
		t.Errorf("rewrote with no rewriter: %s", got)
	}
	if !strings.Contains(got, "url(b.png)") && !strings.Contains(got, `url("b.png")`) {
		t.Errorf("css mangled with no rewriter: %s", got)
	}
}

func TestBaseIsRequired(t *testing.T) {
	if _, err := HTML(`<img src="a.png">`, Options{Asset: prox}); err == nil {
		t.Fatal("expected an error with no Base")
	}
}

func TestCSSComments(t *testing.T) {
	base := mustBase(t, "https://pub.example/p/")
	// A URL inside a comment is not fetched, so rewriting it is harmless — but
	// the scanner must not lose its place and mangle what follows.
	got := CSS(`/* url(ignored.png) */ .a{background:url(real.png)}`, prox, base)
	if !strings.Contains(got, url.QueryEscape("https://pub.example/p/real.png")) {
		t.Errorf("real url missed: %s", got)
	}
	if !strings.Contains(got, "/* url(ignored.png) */") {
		t.Errorf("comment mangled: %s", got)
	}
}

func TestCSSUnterminatedCommentDoesNotHang(t *testing.T) {
	got := CSS(`.a{} /* never closed`, prox, mustBase(t, "https://pub.example/"))
	if !strings.Contains(got, "never closed") {
		t.Errorf("lost the tail: %s", got)
	}
}

func TestSrcsetSplitting(t *testing.T) {
	cases := []struct {
		in   string
		want []candidate
	}{
		{"a.png", []candidate{{"a.png", ""}}},
		{"a.png 1x", []candidate{{"a.png", "1x"}}},
		{"a.png 1x, b.png 2x", []candidate{{"a.png", "1x"}, {"b.png", "2x"}}},
		// One candidate, per spec: the comma is not trailing the URL.
		{"a.png,b.png", []candidate{{"a.png,b.png", ""}}},
		{"a.png, b.png", []candidate{{"a.png", ""}, {"b.png", ""}}},
		{"  a.png   400w ,  b.png 800w ", []candidate{{"a.png", "400w"}, {"b.png", "800w"}}},
		{"", nil},
	}
	for _, tc := range cases {
		got := splitSrcset(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("%q: got %d candidates, want %d (%v)", tc.in, len(got), len(tc.want), got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q: candidate %d = %v, want %v", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}
