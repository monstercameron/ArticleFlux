package extract

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The twelve pages the D7 bake-off was decided on, now serving as this package's
// regression fixtures. They are committed for the same reason the feed corpus is:
// extraction quality is the kind of thing that degrades one dependency bump at a
// time, and only a fixed set of pages can show it.

var fixedNow = time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)

type fixture struct {
	slug string
	body []byte
	url  string
}

func loadArticles(t *testing.T) []fixture {
	t.Helper()
	paths, _ := filepath.Glob("testdata/articles/*.html")
	if len(paths) == 0 {
		t.Fatal("no article fixtures")
	}
	sort.Strings(paths)
	var out []fixture
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		slug := strings.TrimSuffix(filepath.Base(p), ".html")
		f := fixture{slug: slug, body: b}
		if meta, err := os.ReadFile(filepath.Join("testdata/articles", slug+".meta")); err == nil {
			for _, line := range strings.Split(string(meta), "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(line), "url:"); ok {
					f.url = strings.TrimSpace(v)
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// minWords is the floor below which extraction has effectively failed. Set from
// the observed floor (the Verge column, ~430 words) with room underneath: this
// is a "the extractor collapsed" alarm, not a quality score.
const minWords = 200

func TestExtractsEveryFixture(t *testing.T) {
	for _, fx := range loadArticles(t) {
		t.Run(fx.slug, func(t *testing.T) {
			art, err := FromBytes(fx.body, "text/html", fx.url, fixedNow)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if art.Title == "" {
				t.Error("no title")
			}
			if art.WordCount < minWords {
				t.Errorf("%d words — extraction collapsed", art.WordCount)
			}
			if strings.TrimSpace(art.HTML) == "" {
				t.Error("no HTML")
			}
			if art.Excerpt == "" {
				t.Error("no excerpt")
			}
			// The page's own date is clamped exactly as a feed's is.
			if art.PublishedAt.After(fixedNow) {
				t.Errorf("published %s is in the future", art.PublishedAt)
			}
		})
	}
}

// The output goes straight into a reading pane, so it has to be safe there. This
// is the assertion that would catch a future change routing around the sanitizer
// — which is the plausible mistake, since readability's own output looks clean.
func TestExtractedHTMLIsSanitised(t *testing.T) {
	for _, fx := range loadArticles(t) {
		t.Run(fx.slug, func(t *testing.T) {
			art, err := FromBytes(fx.body, "text/html", fx.url, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			low := strings.ToLower(art.HTML)
			for _, bad := range []string{"<script", "javascript:", "onerror=", "onclick=", "<iframe", "<style", "<form"} {
				if strings.Contains(low, bad) {
					t.Errorf("%q survived into extracted HTML", bad)
				}
			}
			// Every link hardened, on real pages rather than on synthetic ones.
			if strings.Contains(low, "<a href") && !strings.Contains(low, "noopener") {
				t.Error("links were not hardened")
			}
		})
	}
}

// A page injecting script through the article body itself, rather than through
// chrome readability would have discarded anyway.
func TestMaliciousArticleBodyIsNeutralised(t *testing.T) {
	page := []byte(`<!doctype html><html><head><title>Post</title></head><body>
	<article>
	<h1>A post</h1>
	<p>` + strings.Repeat("Real article text that readability will keep because it is long enough to score. ", 40) + `</p>
	<p onclick="alert(1)">handler on a paragraph</p>
	<script>alert('xss')</script>
	<a href="javascript:alert(1)">a bad link</a>
	<img src=x onerror=alert(1)>
	<iframe src="//evil.tld"></iframe>
	</article></body></html>`)

	art, err := FromBytes(page, "text/html", "https://example.com/post", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	low := strings.ToLower(art.HTML)
	for _, bad := range []string{"<script", "javascript:", "onerror", "onclick", "<iframe"} {
		if strings.Contains(low, bad) {
			t.Errorf("%q survived: %s", bad, art.HTML)
		}
	}
	if !strings.Contains(art.Text, "Real article text") {
		t.Error("the actual article was lost")
	}
}

// The bug the feed corpus found, asserted here too: this package decodes before
// parsing for exactly the same reason, and nothing else would catch it if the
// call were dropped.
func TestNonUTF8PageDecodes(t *testing.T) {
	// "Café" with a Windows-1252 é (0xE9), which is illegal UTF-8.
	head := []byte(`<!doctype html><html><head><meta charset="windows-1252"><title>Caf`)
	page := append(head, 0xE9)
	page = append(page, []byte(`</title></head><body><article><p>`+
		strings.Repeat("Assez de texte pour que readability garde ce paragraphe entier. ", 40)+
		`Un caf`)...)
	page = append(page, 0xE9)
	page = append(page, []byte(` bien serr`)...)
	page = append(page, 0xE9)
	page = append(page, []byte(`.</p></article></body></html>`)...)

	art, err := FromBytes(page, "text/html", "https://example.fr/a", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(art.Text, "café") {
		t.Errorf("did not decode: %q", art.Text[:min(200, len(art.Text))])
	}
	if strings.ContainsRune(art.Text, '�') {
		t.Errorf("replacement characters in the text: %q", art.Text[:min(200, len(art.Text))])
	}
}

// A page with no article in it must say so rather than returning an empty
// success. Every consumer has a fallback — the feed's own content — but only if
// it is told to use it.
func TestNoArticleIsAnError(t *testing.T) {
	for _, page := range []string{
		`<!doctype html><html><body></body></html>`,
		`<!doctype html><html><body><nav><a href="/a">a</a><a href="/b">b</a></nav></body></html>`,
	} {
		if _, err := FromBytes([]byte(page), "text/html", "https://example.com/", fixedNow); err == nil {
			t.Errorf("expected an error for a page with no article: %q", page)
		}
	}
}

func TestExcerptCutsAtAWordBoundary(t *testing.T) {
	long := strings.Repeat("word ", 200)
	got := excerptFrom(long)
	if len(got) > 290 {
		t.Errorf("excerpt is %d chars", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("no ellipsis: %q", got)
	}
	if strings.Contains(got, "wor…") {
		t.Errorf("cut mid-word: %q", got)
	}
	short := "already short"
	if got := excerptFrom(short); got != short {
		t.Errorf("excerptFrom(%q) = %q", short, got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
