package outlinks

import (
	"testing"
)

const article = `
<html><body>
  <nav><a href="https://elsewhere.com/nav">Home</a></nav>
  <header><a href="https://elsewhere.com/header">About</a></header>
  <article>
    <p>I've been reading <a href="https://sqlite.org/wal.html">the WAL design doc</a>
       and <a href="https://danluu.com/latency/">Dan Luu on latency</a>.</p>
    <p>Also see <a href="/other-post">my other post</a> and
       <a href="https://blog.example.com/x">our blog</a>.</p>
    <p><a href="https://sqlite.org/wal.html">the WAL design doc again</a></p>
    <p><a href="https://sponsor.example/deal" rel="nofollow sponsored">a deal</a></p>
    <p><a href="mailto:me@example.com">email me</a>
       <a href="javascript:void(0)">click</a>
       <a href="#top">top</a></p>
    <p><a href="https://twitter.com/someone">@someone</a></p>
  </article>
  <div class="related"><a href="https://elsewhere.com/related">Related post</a></div>
  <footer><a href="https://elsewhere.com/footer">Privacy</a></footer>
</body></html>`

func TestExtractFindsEditorialLinksOnly(t *testing.T) {
	links := Extract(article, "https://example.com/post", Options{})

	got := map[string]int{}
	for _, l := range links {
		got[l.Host] = l.Count
	}

	// Kept: genuine editorial links in the body.
	for _, want := range []string{"sqlite.org", "danluu.com"} {
		if _, ok := got[want]; !ok {
			t.Errorf("%s should have been extracted; got %v", want, got)
		}
	}
	// A repeated link is counted, not duplicated.
	if got["sqlite.org"] != 2 {
		t.Errorf("sqlite.org count = %d, want 2", got["sqlite.org"])
	}

	// Dropped, each for its own reason.
	for host, why := range map[string]string{
		"elsewhere.com":   "nav/header/footer/related are furniture",
		"twitter.com":     "share targets are not recommendations",
		"sponsor.example": "rel=sponsored is the author saying it is not an endorsement",
	} {
		if _, ok := got[host]; ok {
			t.Errorf("%s should have been dropped (%s); got %v", host, why, got)
		}
	}
}

// A relative link is by definition a self-link, and a subdomain of the article's
// own host is still the same publisher.
func TestSelfLinksAreDropped(t *testing.T) {
	links := Extract(article, "https://example.com/post", Options{})
	for _, l := range links {
		if l.Host == "example.com" || l.Host == "blog.example.com" {
			t.Errorf("self-link survived: %+v", l)
		}
	}
}

func TestNonWebSchemesAreDropped(t *testing.T) {
	links := Extract(`<a href="mailto:x@y.z">mail</a><a href="tel:+123">call</a>
	                  <a href="data:text/html,x">data</a>`,
		"https://example.com/", Options{})
	if len(links) != 0 {
		t.Errorf("non-web schemes are not outbound links, got %+v", links)
	}
}

func TestFurnitureAnchorTextIsDropped(t *testing.T) {
	// The link points somewhere real, but the anchor says it is navigation.
	for _, anchor := range []string{"Read more", "Share", "Subscribe", "click here", ""} {
		h := `<article><a href="https://real.example/x">` + anchor + `</a></article>`
		if links := Extract(h, "https://example.com/", Options{}); len(links) != 0 {
			t.Errorf("anchor %q should be treated as furniture, got %+v", anchor, links)
		}
	}
}

func TestURLsAreNormalised(t *testing.T) {
	h := `<article><a href="https://sqlite.org/wal.html?utm_source=rss#s2">wal design notes</a></article>`
	links := Extract(h, "https://example.com/", Options{})
	if len(links) != 1 {
		t.Fatalf("want 1 link, got %+v", links)
	}
	if links[0].URL != "https://sqlite.org/wal.html" {
		t.Errorf("URL = %q, want the tracking parameters and fragment gone", links[0].URL)
	}
}

// The signal that actually matters: a domain linked once from twenty articles
// beats one linked twenty times from a single article.
func TestHostsAggregates(t *testing.T) {
	links := []Link{
		{Host: "sqlite.org", Count: 5},
		{Host: "sqlite.org", Count: 1},
		{Host: "danluu.com", Count: 1},
	}
	h := Hosts(links)
	if h["sqlite.org"] != 2 {
		t.Errorf("sqlite.org = %d distinct links, want 2", h["sqlite.org"])
	}
	if h["danluu.com"] != 1 {
		t.Errorf("danluu.com = %d, want 1", h["danluu.com"])
	}
}

func TestMaxPerDocBoundsALinkDump(t *testing.T) {
	var h string
	for i := 0; i < 200; i++ {
		h += `<article><a href="https://site` + string(rune('a'+i%26)) +
			string(rune('a'+i/26)) + `.example/x">a real article title</a></article>`
	}
	links := Extract(h, "https://example.com/", Options{MaxPerDoc: 10})
	if len(links) != 10 {
		t.Errorf("got %d links, want the MaxPerDoc bound of 10", len(links))
	}
}

func TestIncludeChromeOptOut(t *testing.T) {
	h := `<article><a href="https://twitter.com/someone">a thread about databases</a></article>`
	if links := Extract(h, "https://example.com/", Options{}); len(links) != 0 {
		t.Errorf("chrome host should be excluded by default, got %+v", links)
	}
	links := Extract(h, "https://example.com/", Options{IncludeChrome: true})
	if len(links) != 1 {
		t.Errorf("IncludeChrome should keep it, got %+v", links)
	}
}

func TestChromeMatchesSubdomains(t *testing.T) {
	h := `<article><a href="https://cdn.twitter.com/x">a thread about databases</a></article>`
	if links := Extract(h, "https://example.com/", Options{}); len(links) != 0 {
		t.Errorf("a subdomain of a chrome host should also be excluded, got %+v", links)
	}
}

func TestGarbageDoesNotPanic(t *testing.T) {
	for _, h := range []string{"", "<a", "<a href=>x</a>", "<<<>>>", "<a href='ht tp://x'>y</a>"} {
		_ = Extract(h, "https://example.com/", Options{})
	}
	_ = Extract(article, "", Options{})
	_ = Extract(article, "::::", Options{})
}
