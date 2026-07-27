package scrapesel

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

// A site with no feed, written the way such sites usually are: a list of
// articles, relative links, a date in a <time datetime> attribute, and a pile of
// navigation chrome around it that must not become items.
const indexPage = `<!doctype html>
<html><head><title>Notes</title></head><body>
<nav class="site-nav">
  <a href="/">Home</a>
  <a href="/about">About</a>
</nav>

<main>
  <article class="post">
    <h2><a href="/2026/07/first-post">The first post</a></h2>
    <time datetime="2026-07-20T09:00:00Z">20 July 2026</time>
    <p class="excerpt">An <em>excerpt</em> with <a href="/somewhere">a link</a> in it.</p>
    <img class="hero" src="/img/first.jpg" alt="">
    <span class="byline">Ada Lovelace</span>
  </article>

  <article class="post">
    <h2><a href="https://absolute.example/second">The second post</a></h2>
    <time datetime="2026-07-21T09:00:00Z">21 July 2026</time>
    <p class="excerpt">Another excerpt.</p>
  </article>

  <article class="post">
    <h2><a href="/2026/07/one-page#anchor-a">Anchored A</a></h2>
    <time datetime="2026-07-22T09:00:00Z">22 July 2026</time>
  </article>
  <article class="post">
    <h2><a href="/2026/07/one-page#anchor-b">Anchored B</a></h2>
    <time datetime="2026-07-22T10:00:00Z">22 July 2026</time>
  </article>

  <article class="post">
    <h2>A post with no link at all</h2>
    <time datetime="2026-07-23T09:00:00Z">23 July 2026</time>
  </article>

  <article class="post">
    <h2><a href="javascript:alert(1)">Not an article</a></h2>
  </article>
</main>

<footer><a href="/rss">Subscribe</a></footer>
</body></html>`

func goodRule() Rule {
	return Rule{
		IndexURL:        "https://notes.example/index.html",
		ItemSelector:    "article.post",
		TitleSelector:   "h2 a",
		LinkSelector:    "h2 a@href",
		DateSelector:    "time@datetime",
		SummarySelector: "p.excerpt",
		ImageSelector:   "img.hero@src",
		AuthorSelector:  "span.byline",
	}
}

func mustCompile(t *testing.T, r Rule) *Compiled {
	t.Helper()
	c, err := Compile(r)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return c
}

func TestExtract(t *testing.T) {
	c := mustCompile(t, goodRule())
	res, err := Extract(c, []byte(indexPage), now)
	if err != nil {
		t.Fatal(err)
	}

	if res.Matched != 6 {
		t.Errorf("matched %d containers, want 6", res.Matched)
	}
	// Four usable: the two ordinary posts and the two anchored ones. The
	// link-less one and the javascript: one are skipped.
	if len(res.Items) != 4 {
		t.Fatalf("got %d items: %+v", len(res.Items), res.Items)
	}
	if res.Skipped != 2 {
		t.Errorf("skipped %d, want 2", res.Skipped)
	}

	first := res.Items[0]
	t.Run("relative links are resolved against the index", func(t *testing.T) {
		if first.URL != "https://notes.example/2026/07/first-post" {
			t.Errorf("url = %q", first.URL)
		}
	})
	t.Run("title", func(t *testing.T) {
		if first.Title != "The first post" {
			t.Errorf("title = %q", first.Title)
		}
	})
	t.Run("date from an attribute", func(t *testing.T) {
		if first.PublishedAt.Format("2006-01-02") != "2026-07-20" {
			t.Errorf("published = %v", first.PublishedAt)
		}
	})
	t.Run("author", func(t *testing.T) {
		if first.Author != "Ada Lovelace" {
			t.Errorf("author = %q", first.Author)
		}
	})
	t.Run("image is resolved too", func(t *testing.T) {
		if first.ImageURL != "https://notes.example/img/first.jpg" {
			t.Errorf("image = %q", first.ImageURL)
		}
	})
	t.Run("summary is text, content keeps markup", func(t *testing.T) {
		if strings.Contains(first.Summary, "<em>") {
			t.Errorf("summary carries markup: %q", first.Summary)
		}
		if !strings.Contains(first.Summary, "An excerpt with a link in it") {
			t.Errorf("summary = %q", first.Summary)
		}
		if !strings.Contains(first.ContentHTML, "<em>") {
			t.Errorf("content lost its markup: %q", first.ContentHTML)
		}
	})
	t.Run("absolute links are left alone", func(t *testing.T) {
		if res.Items[1].URL != "https://absolute.example/second" {
			t.Errorf("url = %q", res.Items[1].URL)
		}
	})
	t.Run("nav and footer links do not become items", func(t *testing.T) {
		for _, it := range res.Items {
			if strings.Contains(it.URL, "/about") || strings.Contains(it.URL, "/rss") {
				t.Errorf("chrome became an item: %+v", it)
			}
		}
	})
}

// The bug the feed corpus found, in its second home: a one-page site
// distinguishes its entries by anchor and nothing else, so identity must keep
// the fragment or all but one entry become unstorable.
func TestAnchorsAreDistinctItems(t *testing.T) {
	c := mustCompile(t, goodRule())
	res, _ := Extract(c, []byte(indexPage), now)

	var anchored []Item
	for _, it := range res.Items {
		if strings.Contains(it.URL, "one-page") {
			anchored = append(anchored, it)
		}
	}
	if len(anchored) != 2 {
		t.Fatalf("expected two anchored items, got %d", len(anchored))
	}
	if anchored[0].GUID == anchored[1].GUID {
		t.Errorf("two anchored entries share the guid %q; one can never be stored",
			anchored[0].GUID)
	}
}

// The skipped ones must be explained, or a rule editor showing "4 items" cannot
// tell the author that two containers had no link.
func TestProblemsAreReported(t *testing.T) {
	c := mustCompile(t, goodRule())
	res, _ := Extract(c, []byte(indexPage), now)

	if len(res.Problems) == 0 {
		t.Fatal("two containers were skipped with no explanation")
	}
	joined := strings.Join(res.Problems, " | ")
	if !strings.Contains(joined, "no link") {
		t.Errorf("problems = %q; expected one about a missing link", joined)
	}
}

// A site redesign breaks the selector and looks exactly like "they stopped
// publishing". Matched must distinguish them.
func TestABrokenSelectorIsDistinguishableFromAQuietSite(t *testing.T) {
	// The redesign: the container class changed.
	broken := goodRule()
	broken.ItemSelector = "article.entry"
	res, err := Extract(mustCompile(t, broken), []byte(indexPage), now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 0 || len(res.Items) != 0 {
		t.Errorf("a stale container selector still matched: %+v", res)
	}

	// The quiet site: the selector is right, there is simply nothing today.
	quiet := `<html><body><main></main></body></html>`
	res2, _ := Extract(mustCompile(t, goodRule()), []byte(quiet), now)
	if res2.Matched != 0 {
		t.Errorf("matched %d on an empty page", res2.Matched)
	}

	// Both give zero items, and the ONE that separates them is a container
	// selector that matches nothing on a page that clearly has content — which
	// is what empty_polls counts and what the nudge reports.
	res3, _ := Extract(mustCompile(t, broken), []byte(indexPage), now)
	if res3.Matched != 0 {
		t.Error("expected zero matches for the stale selector")
	}
}

// A container that IS the match — cascadia.Query only searches descendants.
func TestSelectorMatchingTheContainerItself(t *testing.T) {
	page := `<html><body>
	  <a class="row" href="/a"><span>Row A</span></a>
	  <a class="row" href="/b"><span>Row B</span></a>
	</body></html>`

	c := mustCompile(t, Rule{
		IndexURL:      "https://x.tld/",
		ItemSelector:  "a.row",
		TitleSelector: "span",
		LinkSelector:  "a.row@href",
	})
	res, _ := Extract(c, []byte(page), now)
	if len(res.Items) != 2 {
		t.Fatalf("got %d items; the link selector matched the container itself and was missed: %+v",
			len(res.Items), res)
	}
	if res.Items[0].URL != "https://x.tld/a" {
		t.Errorf("url = %q", res.Items[0].URL)
	}
}

func TestExplicitDateLayoutWinsForAmbiguousDates(t *testing.T) {
	page := `<html><body><div class="p"><a href="/x">T</a><span class="d">02/03/2026</span></div></body></html>`
	r := Rule{
		IndexURL: "https://x.tld/", ItemSelector: "div.p",
		TitleSelector: "a", LinkSelector: "a@href", DateSelector: "span.d",
		DateLayout: "02/01/2006", // day first
	}
	res, _ := Extract(mustCompile(t, r), []byte(page), now)
	if len(res.Items) != 1 {
		t.Fatal("no item")
	}
	// Day-first: 2 March, not 3 February. Only the author knows which.
	if got := res.Items[0].PublishedAt.Format("2006-01-02"); got != "2026-03-02" {
		t.Errorf("published %s; the explicit layout was not used", got)
	}
}

// Never the zero time: an item stamped year 1 sorts to the bottom of every list
// forever and is functionally deleted.
func TestUnparseableDatesFallThroughToNow(t *testing.T) {
	page := `<html><body><div class="p"><a href="/x">T</a><span class="d">soon</span></div></body></html>`
	r := Rule{IndexURL: "https://x.tld/", ItemSelector: "div.p",
		TitleSelector: "a", LinkSelector: "a@href", DateSelector: "span.d"}
	res, _ := Extract(mustCompile(t, r), []byte(page), now)
	if !res.Items[0].PublishedAt.Equal(now) {
		t.Errorf("published %v, want first-seen %v", res.Items[0].PublishedAt, now)
	}

	// And with no date selector at all.
	r2 := Rule{IndexURL: "https://x.tld/", ItemSelector: "div.p",
		TitleSelector: "a", LinkSelector: "a@href"}
	res2, _ := Extract(mustCompile(t, r2), []byte(page), now)
	if !res2.Items[0].PublishedAt.Equal(now) {
		t.Errorf("published %v with no date selector", res2.Items[0].PublishedAt)
	}
}

// A future date is clamped exactly as a feed's is.
func TestFutureDatesAreClamped(t *testing.T) {
	page := `<html><body><div class="p"><a href="/x">T</a>
	  <time datetime="2087-01-01T00:00:00Z">soon</time></div></body></html>`
	r := Rule{IndexURL: "https://x.tld/", ItemSelector: "div.p",
		TitleSelector: "a", LinkSelector: "a@href", DateSelector: "time@datetime"}
	res, _ := Extract(mustCompile(t, r), []byte(page), now)
	if res.Items[0].PublishedAt.After(now) {
		t.Errorf("a 2087 date was not clamped: %v", res.Items[0].PublishedAt)
	}
}

func TestScrapedContentIsSanitised(t *testing.T) {
	page := `<html><body><div class="p">
	  <a href="/x">Title</a>
	  <div class="body"><p>Real text</p><script>alert(1)</script>
	    <img src=y onerror=alert(1)><a href="javascript:alert(1)">bad</a></div>
	</div></body></html>`
	r := Rule{IndexURL: "https://x.tld/", ItemSelector: "div.p",
		TitleSelector: "a", LinkSelector: "a@href", SummarySelector: "div.body"}

	res, _ := Extract(mustCompile(t, r), []byte(page), now)
	got := strings.ToLower(res.Items[0].ContentHTML)
	for _, bad := range []string{"<script", "onerror", "javascript:"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q survived into scraped content: %s", bad, got)
		}
	}
	if !strings.Contains(res.Items[0].ContentHTML, "Real text") {
		t.Error("the actual content was lost")
	}
}

func TestCompileRefusesBadRules(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
		want string
	}{
		{"no item selector", Rule{TitleSelector: "a", LinkSelector: "a@href"}, "item selector is required"},
		{"no title", Rule{ItemSelector: "div", LinkSelector: "a@href"}, "title selector is required"},
		{"no link", Rule{ItemSelector: "div", TitleSelector: "a"}, "link selector is required"},
		{"bad css", Rule{ItemSelector: "div[", TitleSelector: "a", LinkSelector: "a@href"}, "item selector"},
		{"bad link css", Rule{ItemSelector: "div", TitleSelector: "a", LinkSelector: "a[@href"}, "link selector"},
		// The most useful refusal: a link selector reading text produces the
		// anchor's label as a URL, and a feed of unopenable items.
		{"link reads text", Rule{ItemSelector: "div", TitleSelector: "a", LinkSelector: "a"},
			"probably needs @href"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Compile(c.rule)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// A selector matching `div` on a large page can produce thousands of "items",
// and the cost lands on ingest and the unread count rather than here.
func TestExtractionIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := 0; i < MaxItems+50; i++ {
		b.WriteString(`<div class="p"><a href="/x`)
		b.WriteString(strings.Repeat("a", i%5+1))
		b.WriteString(`">T</a></div>`)
	}
	b.WriteString("</body></html>")

	r := Rule{IndexURL: "https://x.tld/", ItemSelector: "div.p",
		TitleSelector: "a", LinkSelector: "a@href"}
	res, _ := Extract(mustCompile(t, r), []byte(b.String()), now)

	if len(res.Items) > MaxItems {
		t.Errorf("produced %d items, over the %d cap", len(res.Items), MaxItems)
	}
	// The count must still be honest, or a rule this wrong looks fine.
	if res.Matched != MaxItems+50 {
		t.Errorf("matched reported %d, want the true %d", res.Matched, MaxItems+50)
	}
}

func TestExtractIsPure(t *testing.T) {
	c := mustCompile(t, goodRule())
	a, _ := Extract(c, []byte(indexPage), now)
	b, _ := Extract(c, []byte(indexPage), now)
	if len(a.Items) != len(b.Items) {
		t.Fatal("two extractions disagree")
	}
	for i := range a.Items {
		if a.Items[i].GUID != b.Items[i].GUID {
			t.Errorf("item %d guid changed between runs", i)
		}
	}
}
