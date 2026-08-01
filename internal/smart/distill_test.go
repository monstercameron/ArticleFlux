package smart

import (
	"strings"
	"testing"
)

// distill.go's structural tests (dropping prose, collapsing repeats, the
// app-shell detector) live in scrape_test.go alongside the `index` fixture
// they share with scrape.go's own tests. This file is the smaller internals
// those tests do not happen to exercise: the per-element formatting caps.

// describe() caps classes at three — a utility-CSS element carries dozens and
// none of them help write a selector.
func TestDescribeCapsClassesAtThree(t *testing.T) {
	out := distill(`<html><body>` +
		strings.Repeat(`<article class="post one two three four five"><a href="/x">A</a></article>`, 2) +
		`</body></html>`)
	if strings.Count(out, ".four") > 0 || strings.Count(out, ".five") > 0 {
		t.Errorf("more than three classes reached the outline:\n%s", out)
	}
	if !strings.Contains(out, ".post.one.two") {
		t.Errorf("the first three classes are missing:\n%s", out)
	}
}

// An attribute with only whitespace for a value contributes nothing — kept
// out entirely rather than rendered as `href=`.
func TestDescribeDropsAttrsThatAreBlank(t *testing.T) {
	out := distill(`<html><body>` +
		strings.Repeat(`<article class="post"><a href="   " title="Real Title">A</a></article>`, 2) +
		`</body></html>`)
	if strings.Contains(out, "href=") {
		t.Errorf("a blank href reached the outline:\n%s", out)
	}
	if !strings.Contains(out, "title=Real Title") {
		t.Errorf("a real attribute was dropped alongside the blank one:\n%s", out)
	}
}

// A long href is truncated rather than shown in full — the shape matters,
// the query string does not.
func TestDescribeTruncatesALongAttrValue(t *testing.T) {
	long := "/articles/" + strings.Repeat("x", maxAttrValue+40)
	out := distill(`<html><body>` +
		strings.Repeat(`<article class="post"><a href="`+long+`">A</a></article>`, 2) +
		`</body></html>`)
	if strings.Contains(out, long) {
		t.Errorf("a %d-byte href reached the outline in full", len(long))
	}
	if !strings.Contains(out, "…") {
		t.Errorf("a truncated attribute did not carry the ellipsis marker:\n%s", out)
	}
}

// signature() feeds the repeat-collapsing logic and caps classes the same
// way describe() does, so two elements differing only past the third class
// are still recognised as the same repeated block.
func TestSignatureCapsClassesAtThreeForGrouping(t *testing.T) {
	out := distill(`<html><body>` +
		`<article class="post a b c unique-1"><a href="/1">One</a></article>` +
		`<article class="post a b c unique-2"><a href="/2">Two</a></article>` +
		`<article class="post a b c unique-3"><a href="/3">Three</a></article>` +
		`</body></html>`)
	if !strings.Contains(out, "more") {
		t.Errorf("three elements differing only past the third class did not collapse:\n%s", out)
	}
}

// sample() truncates long text nodes rather than reproducing the prose —
// the outline is a structural summary, not a copy.
func TestSampleTruncatesLongText(t *testing.T) {
	long := strings.Repeat("word ", maxTextSample)
	out := distill(`<html><body><article class="post"><p>` + long + `</p></article>` +
		`<article class="post"><p>` + long + `</p></article></body></html>`)
	if strings.Contains(out, long) {
		t.Error("a long text sample reached the outline unclipped")
	}
	if !strings.Contains(out, "…") {
		t.Errorf("a truncated text sample did not carry the ellipsis marker:\n%s", out)
	}
}

// writeNode's own depth guard: past maxDepth the walk stops rather than
// describing scaffolding div-in-div-in-div nesting forever.
func TestDistillStopsAtMaxDepth(t *testing.T) {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := 0; i < maxDepth+10; i++ {
		b.WriteString(`<div class="wrap">`)
	}
	b.WriteString("deeply nested text")
	for i := 0; i < maxDepth+10; i++ {
		b.WriteString("</div>")
	}
	b.WriteString("</body></html>")
	out := distill(b.String())
	// Not asserting an exact bound — only that runaway nesting did not
	// produce an entry per level, which is what "stops" means here.
	if strings.Count(out, "div.wrap") > maxDepth+2 {
		t.Errorf("nesting past maxDepth was not capped: %d div.wrap lines",
			strings.Count(out, "div.wrap"))
	}
}

// --- ClientRendered internals ----------------------------------------------------

// Script and style content inside an app shell must not count toward the
// "almost no text" measurement, or a shell with a large inline script would
// be mistaken for a page with real content.
func TestClientRenderedIgnoresScriptAndStyleText(t *testing.T) {
	page := `<html><body><div id="root"><router-view></router-view></div>` +
		`<script>` + strings.Repeat("var x = 'padding'; ", 200) + `</script>` +
		`<style>` + strings.Repeat(".a{color:red} ", 200) + `</style>` +
		`</body></html>`
	if !ClientRendered(page) {
		t.Error("a script/style-padded app shell was not detected")
	}
}

// The other three framework markers ClientRendered checks for, alongside
// the id-based appShellMarkers already covered elsewhere.
func TestClientRenderedDetectsFrameworkHydrationAttrs(t *testing.T) {
	for _, attr := range []string{`data-reactroot=""`, `ng-app="myApp"`, `data-server-rendered="true"`} {
		page := `<html><body><div ` + attr + `><p>Loading</p></div></body></html>`
		if !ClientRendered(page) {
			t.Errorf("a shell carrying %s was not detected", attr)
		}
	}
}
