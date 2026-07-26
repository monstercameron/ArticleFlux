package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

const rss2 = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">
  <channel>
    <title>Example Blog</title>
    <link>https://example.com</link>
    <description>Notes</description>
    <item>
      <title>Speculative decoding without a draft model</title>
      <link>https://example.com/posts/spec?utm_source=rss</link>
      <guid isPermaLink="false">post-1</guid>
      <pubDate>Sun, 26 Jul 2026 12:30:00 +0000</pubDate>
      <description>&lt;p&gt;n-gram proposals verified in the same batch.&lt;/p&gt;</description>
      <content:encoded><![CDATA[<p>The full article body with rather more words in it than the summary carries.</p>]]></content:encoded>
    </item>
    <item>
      <title>The write lock is not your problem</title>
      <link>https://example.com/posts/wal</link>
      <pubDate>Sat, 25 Jul 2026 09:00:00 +0000</pubDate>
      <description>WAL and a single serialized writer.</description>
    </item>
  </channel>
</rss>`

const atom = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Example</title>
  <link href="https://atom.example/"/>
  <entry>
    <title>An entry</title>
    <link href="https://atom.example/e/1"/>
    <id>urn:uuid:1</id>
    <updated>2026-07-26T12:30:00Z</updated>
    <summary>Short summary.</summary>
  </entry>
</feed>`

func parse(t *testing.T, body, url string) *Parsed {
	t.Helper()
	f, err := gofeed.NewParser().Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return Normalize(f, url, time.Date(2026, 7, 26, 18, 0, 0, 0, time.UTC))
}

func TestNormalizeRSS(t *testing.T) {
	p := parse(t, rss2, "https://example.com/feed.xml")

	if p.Title != "Example Blog" {
		t.Errorf("title = %q", p.Title)
	}
	if len(p.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(p.Items))
	}

	it := p.Items[0]
	if it.GUID != "post-1" {
		t.Errorf("guid = %q, want the feed's own guid", it.GUID)
	}
	// content:encoded is the article; description is the summary. Taking the
	// shorter one as content would lose the body.
	if !strings.Contains(it.ContentHTML, "full article body") {
		t.Errorf("content = %q, want content:encoded", it.ContentHTML)
	}
	if strings.Contains(it.Summary, "<p>") {
		t.Errorf("summary %q still contains markup; it is rendered as text", it.Summary)
	}
	if !strings.Contains(it.Summary, "n-gram proposals") {
		t.Errorf("summary = %q", it.Summary)
	}
	// Tracking parameters must not reach the duplicate key, or the same article
	// from two feeds counts twice.
	if strings.Contains(it.DupeKey, "utm_source") {
		t.Errorf("dupe key = %q, want tracking parameters stripped", it.DupeKey)
	}
	if it.WordCount == 0 {
		t.Error("word count should be computed from the content")
	}
	want := time.Date(2026, 7, 26, 12, 30, 0, 0, time.UTC)
	if !it.PublishedAt.Equal(want) {
		t.Errorf("published = %v, want %v", it.PublishedAt, want)
	}
}

func TestNormalizeAtom(t *testing.T) {
	p := parse(t, atom, "https://atom.example/feed")
	if len(p.Items) != 1 {
		t.Fatalf("got %d items", len(p.Items))
	}
	if p.Items[0].GUID != "urn:uuid:1" {
		t.Errorf("guid = %q", p.Items[0].GUID)
	}
}

// An entry with no guid must still be stable across polls, or every poll
// re-inserts the whole feed.
func TestGUIDFallsBackToTheLink(t *testing.T) {
	p := parse(t, rss2, "https://example.com/feed.xml")
	it := p.Items[1]
	if it.GUID != "https://example.com/posts/wal" {
		t.Errorf("guid = %q, want the normalised link", it.GUID)
	}
	// Re-parsing the same bytes must produce the same guid.
	again := parse(t, rss2, "https://example.com/feed.xml")
	if again.Items[1].GUID != it.GUID {
		t.Error("guid is not stable across polls")
	}
}

func TestGUIDFallsBackToAContentHash(t *testing.T) {
	noID := `<rss version="2.0"><channel><title>T</title>
	  <item><title>Only a title</title><description>and a body</description></item>
	</channel></rss>`
	a := parse(t, noID, "https://x.example/feed")
	b := parse(t, noID, "https://x.example/feed")
	if a.Items[0].GUID == "" {
		t.Fatal("an item with no guid and no link still needs one")
	}
	if a.Items[0].GUID != b.Items[0].GUID {
		t.Error("the hashed guid must be stable, or every poll re-inserts the feed")
	}
	if !strings.HasPrefix(a.Items[0].GUID, "sha256:") {
		t.Errorf("guid = %q", a.Items[0].GUID)
	}
}

// A feed claiming 2087 would pin itself to the top of every list forever,
// because published_at is the sort key for the whole application.
func TestFutureDatesAreClamped(t *testing.T) {
	body := `<rss version="2.0"><channel><title>T</title>
	  <item><title>From the future</title><link>https://x.example/1</link>
	  <pubDate>Sat, 01 Jan 2087 00:00:00 +0000</pubDate></item>
	</channel></rss>`
	now := time.Date(2026, 7, 26, 18, 0, 0, 0, time.UTC)
	p := parse(t, body, "https://x.example/feed")
	if !p.Items[0].PublishedAt.Equal(now) {
		t.Errorf("published = %v, want it clamped to now (%v)", p.Items[0].PublishedAt, now)
	}
}

func TestUntitledEntriesGetATitle(t *testing.T) {
	body := `<rss version="2.0"><channel><title>T</title>
	  <item><link>https://x.example/1</link>
	  <description>A microblog post with no title at all, which is legal.</description></item>
	</channel></rss>`
	p := parse(t, body, "https://x.example/feed")
	if p.Items[0].Title == "" {
		t.Error("an untitled entry needs something renderable in a list row")
	}
	if strings.Contains(p.Items[0].Title, "<") {
		t.Errorf("derived title %q contains markup", p.Items[0].Title)
	}
}

func TestUntitledFeedFallsBackToItsHost(t *testing.T) {
	body := `<rss version="2.0"><channel><link>https://x.example</link>
	  <item><title>a</title></item></channel></rss>`
	p := parse(t, body, "https://feeds.x.example/rss")
	if p.Title != "feeds.x.example" {
		t.Errorf("title = %q, want the host as a placeholder", p.Title)
	}
}

func TestFetchHonoursNotModified(t *testing.T) {
	var gotINM string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotINM = r.Header.Get("If-None-Match")
		if gotINM == `W/"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `W/"abc"`)
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rss2))
	}))
	defer srv.Close()

	// httptest binds loopback, which the guard refuses by design. This is the
	// documented escape hatch, exercised here exactly as a self-hosted operator
	// with a LAN feed would use it.
	f := New(Config{AllowPrivateAddresses: true})
	f.client = srv.Client()

	p, err := f.Fetch(context.Background(), srv.URL, Conditional{})
	if err != nil {
		t.Fatal(err)
	}
	if p.NotModified {
		t.Error("a first fetch is never 304")
	}
	if p.ETag != `W/"abc"` {
		t.Errorf("etag = %q", p.ETag)
	}

	p2, err := f.Fetch(context.Background(), srv.URL, Conditional{ETag: p.ETag})
	if err != nil {
		t.Fatal(err)
	}
	if !p2.NotModified {
		t.Error("the second fetch should be 304 — not honouring this gets us " +
			"rate-limited off a publisher's server")
	}
	if len(p2.Items) != 0 {
		t.Error("a 304 carries no items")
	}
}

// A feed URL is user-supplied, and the server can reach hosts the user cannot.
func TestFetchRefusesInternalAddresses(t *testing.T) {
	f := NewFetcher()
	for _, u := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:9000/",
		"file:///etc/passwd",
	} {
		if _, err := f.Fetch(context.Background(), u, Conditional{}); err == nil {
			t.Errorf("Fetch(%q) should have been refused", u)
		}
	}
}

func TestFetchRejectsNonFeeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>not a feed</body></html>"))
	}))
	defer srv.Close()

	f := New(Config{AllowPrivateAddresses: true})
	f.client = srv.Client()
	if _, err := f.Fetch(context.Background(), srv.URL, Conditional{}); err == nil {
		t.Error("an HTML page is not a feed")
	}
}

func TestFetchBoundsTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		chunk := strings.Repeat("x", 1<<20)
		for i := 0; i < 10; i++ {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	f := New(Config{AllowPrivateAddresses: true})
	f.client = srv.Client()
	_, err := f.Fetch(context.Background(), srv.URL, Conditional{})
	if err == nil {
		t.Error("an oversized body should be refused, not buffered")
	}
}

func TestNaturalKeyDeduplicates(t *testing.T) {
	// A14: two tenants adding the same feed differently must converge on one row.
	a := NaturalKey("https://example.com/feed.xml")
	b := NaturalKey("https://www.example.com/feed.xml?utm_source=x")
	if a != b {
		t.Errorf("%q != %q — the same feed would be polled twice", a, b)
	}
	if a == NaturalKey("https://other.example/feed.xml") {
		t.Error("different feeds must not collide")
	}
}
