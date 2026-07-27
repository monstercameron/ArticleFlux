package smart

import (
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/scrapesel"
)

// What is tested here is the half that does not call OpenAI: the distiller that
// decides what leaves the machine, and the gate that decides whether an answer
// is allowed to become a subscription.
//
// That gate is the whole safety argument for the feature — "the LLM proposes,
// the parser disposes" is only true if the disposing is real — so it is tested
// against the answers a model actually gives wrong, not against a happy path.

const index = `<!doctype html>
<html><head><title>Field Notes</title></head><body>
<header><h1>Field Notes</h1><nav><a href="/">Home</a><a href="/about">About</a></nav></header>
<main>
  <article class="post">
    <h2><a href="/posts/one">Rewriting the poller</a></h2>
    <time datetime="2026-07-20T09:00:00Z">20 July</time>
    <p class="excerpt">Backoff, jitter and the one bug that mattered.</p>
  </article>
  <article class="post">
    <h2><a href="/posts/two">A week of SQLite</a></h2>
    <time datetime="2026-07-22T09:00:00Z">22 July</time>
    <p class="excerpt">WAL, one writer, and why it is enough.</p>
  </article>
  <article class="post">
    <h2><a href="/posts/three">Reading the room</a></h2>
    <time datetime="2026-07-24T09:00:00Z">24 July</time>
    <p class="excerpt">Ranking without a recommender.</p>
  </article>
</main>
<footer><p>© 2026</p></footer>
</body></html>`

func TestGoodRuleIsAccepted(t *testing.T) {
	res, problem := tryRule(scrapesel.Rule{
		IndexURL:        "https://notes.example/",
		ItemSelector:    "article.post",
		TitleSelector:   "h2 a",
		LinkSelector:    "h2 a@href",
		DateSelector:    "time@datetime",
		SummarySelector: "p.excerpt",
	}, index)
	if problem != "" {
		t.Fatalf("a working rule was rejected: %s", problem)
	}
	if len(res.Items) != 3 {
		t.Fatalf("%d items, want 3", len(res.Items))
	}
	if res.Items[0].URL != "https://notes.example/posts/one" {
		t.Errorf("relative link was not resolved: %q", res.Items[0].URL)
	}
}

// The failure mode this rejects is specific and common: a selector that latches
// onto the page's <h1> instead of the entry's own heading. Every item comes out
// with the site's name and the feed is useless — but it EXTRACTS, so nothing
// short of this check notices.
func TestIdenticalTitlesAreRejected(t *testing.T) {
	_, problem := tryRule(scrapesel.Rule{
		IndexURL:      "https://notes.example/",
		ItemSelector:  "article.post",
		TitleSelector: "h1",
		LinkSelector:  "h2 a@href",
	}, `<html><body><h1>Field Notes</h1>` + strings.Repeat(
		`<article class="post"><h1>Field Notes</h1><h2><a href="/a">A</a></h2></article>`, 3) +
		`</body></html>`)
	if problem == "" {
		t.Fatal("a rule producing three identical titles was accepted")
	}
	if !strings.Contains(problem, "same title") {
		t.Errorf("problem = %q, want it to name the cause", problem)
	}
}

// One item is the signature of a selector that found the hero post rather than
// the list. It looks like success and produces a feed with one entry forever.
func TestSingleItemIsRejected(t *testing.T) {
	_, problem := tryRule(scrapesel.Rule{
		IndexURL:      "https://notes.example/",
		ItemSelector:  "header",
		TitleSelector: "h1",
		LinkSelector:  "nav a@href",
	}, index)
	if problem == "" {
		t.Fatal("a one-item rule was accepted")
	}
}

func TestSelectorThatMatchesNothingIsRejected(t *testing.T) {
	_, problem := tryRule(scrapesel.Rule{
		IndexURL:      "https://notes.example/",
		ItemSelector:  "div.entry-card",
		TitleSelector: "h2",
		LinkSelector:  "a@href",
	}, index)
	if !strings.Contains(problem, "matched nothing") {
		t.Errorf("problem = %q, want it to say the selector matched nothing", problem)
	}
}

// A link selector that reads text produces items nobody can open. scrapesel
// refuses it at compile time; this is here so the refusal keeps arriving as a
// rejected proposal rather than as a panic or a saved rule.
func TestLinkWithoutHrefIsRejected(t *testing.T) {
	_, problem := tryRule(scrapesel.Rule{
		IndexURL:      "https://notes.example/",
		ItemSelector:  "article.post",
		TitleSelector: "h2 a",
		LinkSelector:  "h2 a",
	}, index)
	if problem == "" {
		t.Fatal("a link selector reading text was accepted")
	}
}

// The outline is what leaves the machine. Two properties matter: the prose is
// gone, and the repetition — the actual answer to "where is the list" — is
// visible.
func TestDistillKeepsStructureAndDropsProse(t *testing.T) {
	out := distill(index)
	if out == "" {
		t.Fatal("empty outline")
	}
	if !strings.Contains(out, "article.post") {
		t.Errorf("the repeated container is missing from the outline:\n%s", out)
	}
	if !strings.Contains(out, "x 1 more article.post") {
		t.Errorf("repeated siblings were not collapsed:\n%s", out)
	}
	if strings.Contains(out, "<article") {
		t.Error("the outline is HTML; it should not be pasteable back into a browser")
	}
	if len(out) >= len(index) {
		t.Errorf("outline (%d bytes) is not smaller than the page (%d)", len(out), len(index))
	}
}

func TestDistillDropsScriptsAndStyles(t *testing.T) {
	out := distill(`<html><body>
		<script>var secret = "do not send this";</script>
		<style>.x{color:red}</style>
		<article class="post"><h2><a href="/a">A</a></h2></article>
		<article class="post"><h2><a href="/b">B</a></h2></article>
	</body></html>`)
	if strings.Contains(out, "do not send this") {
		t.Error("script contents reached the outline")
	}
	if strings.Contains(out, "color:red") {
		t.Error("stylesheet contents reached the outline")
	}
	if !strings.Contains(out, "article.post") {
		t.Errorf("structure was lost:\n%s", out)
	}
}

// A page that is one long article rather than a list has no repeated block. The
// distiller must still produce something rather than nothing, because "no list
// here" is an answer the model has to be able to give.
func TestDistillHandlesAPageWithNoList(t *testing.T) {
	out := distill(`<html><body><article><h1>One essay</h1>` +
		strings.Repeat(`<p>Paragraphs of prose that carry no structure at all.</p>`, 40) +
		`</article></body></html>`)
	if strings.TrimSpace(out) == "" {
		t.Fatal("a prose page distilled to nothing")
	}
	if len(out) > 4000 {
		t.Errorf("a 40-paragraph essay produced %d bytes of outline", len(out))
	}
}
