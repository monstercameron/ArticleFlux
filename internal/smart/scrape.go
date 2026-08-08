package smart

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/scrapesel"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/timeutil"

	schemaflux "github.com/monstercameron/schemaflux"
)

// Following a page that has no feed — plan.md §11 rung 5, §14.2.
//
// The model's job here is narrow and worth stating precisely, because a broader
// one is what makes AI features untrustworthy: it never sees an article, never
// summarises anything, and never produces content a reader will read. It looks
// at the STRUCTURE of one index page and answers a question about CSS selectors.
// Everything a reader eventually sees was extracted from the site's own HTML by
// internal/scrapesel, deterministically, from a rule that was checked against
// that page before anyone was shown it.
//
// That is the same "the LLM proposes, the parser disposes" rule §11 applies to
// feed discovery, and it has a sharper consequence here: a hallucinated selector
// cannot invent an article, it can only fail to match — and a rule that matches
// nothing is refused below rather than saved.
//
// # What leaves the machine
//
// A DISTILLED OUTLINE of the page, not the page. distill() drops scripts,
// styles, inline SVG, comments and most text, keeping the tag/class/id skeleton
// with short samples — which is both what the model needs and a fraction of the
// bytes. On a typical blog index this is 6-12 KB against 300-800 KB of HTML.
// Egress is still egress and the reader consents per request (§18.8); the point
// is that the smallest useful thing is what goes.

var (
	// ErrNoRule means the model answered and nothing it proposed survived being
	// run against the page. Distinct from a transport failure because the remedy
	// differs: this one is "this page cannot be followed this way", and retrying
	// it costs money to be told so again.
	ErrNoRule = errors.New("smart: no working rule for this page")

	// ErrNoList is the model saying there is nothing here to follow — an app
	// shell, a single article, a landing page.
	//
	// A first-class answer rather than a failure, because the alternative is
	// what a model does when "I cannot" is not an available reply: it finds the
	// most list-shaped thing in the markup, which on a page like that is the
	// navigation, and proposes selectors for a feed of menu items. Making the
	// honest answer expressible is what stops the plausible one.
	ErrNoList = errors.New("smart: the page has no list of entries on it")
)

// minItems is the floor for accepting a proposed rule.
//
// Two, not one. A rule matching exactly one item is the signature of a selector
// that latched onto the page's hero article or a singleton banner — it looks
// like success and produces a feed with one entry that never changes. Two is the
// smallest number that demonstrates the selector found a LIST.
const minItems = 2

// maxSamples is how many extracted items travel back to the client. Enough to
// judge the rule by, few enough that the response stays a preview.
const maxSamples = 6

// analyzeMaxTokens bounds the answer.
//
// The reply is eight short selectors and a sentence — perhaps 120 tokens. The
// bound is fifty times that because on a reasoning model this budget covers the
// THINKING TOO: the first attempt at 1200 came back `ErrTruncated` having spent
// every token reasoning and emitted nothing. That failure is indistinguishable
// from a bad prompt unless you know to look for it, which is why the number is
// generous and the reason is written down.
const analyzeMaxTokens = 6000

// analyzeEffort keeps the reasoning short.
//
// The question is structural — which container repeats, which key holds the
// title — and the evidence is in front of the model. Deliberation buys nothing
// here and is charged for by the token.
const analyzeEffort = "low"

// SiteAnalyzer proposes scrape rules.
type SiteAnalyzer struct {
	llm      llmClient
	settings *store.SettingsRepo
}

// NewSiteAnalyzer wires the analyzer to the one LLM client.
//
// c is the llmClient seam (see llmclient.go): production keeps passing a
// *llm.Client, tests pass a fake that never reaches the network.
func NewSiteAnalyzer(c llmClient, s *store.SettingsRepo) *SiteAnalyzer {
	return &SiteAnalyzer{llm: c, settings: s}
}

// Configured reports whether this instance can egress at all.
func (a *SiteAnalyzer) Configured(ctx context.Context) bool {
	return a != nil && a.llm != nil && a.llm.Configured(ctx)
}

// model is the instance's Smart+ model, or the built-in default.
//
// The nil check is not decoration. Every sibling here (Digest, Podcast,
// Interest, Classify, Theme) guards its settings repo the same way, and this
// type used to be the exception: it dereferenced the repo straight, which is
// safe only for as long as every caller passes a real one.
func (a *SiteAnalyzer) model(ctx context.Context) string {
	if a.settings == nil {
		return llm.DefaultModel
	}
	m, err := a.settings.SystemValue(ctx, store.KeySmartModel)
	if err != nil || strings.TrimSpace(m) == "" {
		return llm.DefaultModel
	}
	return strings.TrimSpace(m)
}

// Proposal is a rule that has already been run against the page it was written
// for, plus the evidence.
type Proposal struct {
	Rule    scrapesel.Rule
	Items   []scrapesel.Item
	Matched int
	Notes   string
}

// Propose reads one page and returns a rule that works on it, or ErrNoRule.
//
// Two attempts at most. The second is not a retry in the "maybe it will be
// luckier" sense — it is given the specific failure (nothing matched, no links,
// one item) as input, which is the difference between asking again and asking
// better. A third would be spending a reader's money on diminishing returns.
func (a *SiteAnalyzer) Propose(ctx context.Context, indexURL, pageHTML string) (*Proposal, error) {
	if a == nil || a.llm == nil {
		return nil, llm.ErrNotConfigured
	}
	if !a.llm.Configured(ctx) {
		return nil, llm.ErrNotConfigured
	}
	outline := distill(pageHTML)
	if strings.TrimSpace(outline) == "" {
		return nil, ErrNoRule
	}
	// The model is resolved by the bridge from this instance's setting now — a
	// typed operation has no field for one. See llm.Client.OpsContext.

	input := "Index URL: " + indexURL + "\n\nPage outline:\n" + outline
	var lastProblem string

	for attempt := 0; attempt < 2; attempt++ {
		in := input
		if lastProblem != "" {
			in = input + "\n\nYour previous answer did not work on this page: " +
				lastProblem + "\nPropose different selectors."
		}
		// Rebuilt on `Extracting` (plan P3.6). The answer shape is a fixed Go
		// type, so the schema is derived from it rather than kept in
		// `scrapeSchema()` beside it — one fewer thing that can drift from the
		// struct it describes.
		//
		// Still fed the DISTILLED outline, never raw HTML: `input` is assembled
		// upstream and that boundary is unchanged. The retry loop is also
		// unchanged, and it is this repo's own — it re-asks with the reason the
		// previous selectors failed against the real page, which is a different
		// thing from the library's repair loop (that re-asks for a well-SHAPED
		// answer; this re-asks for a WORKING one).
		answer, err := schemaflux.Extracting[scrapeAnswer](in).
			Steer(scrapeInstructions).
			Fast().
			Run(a.llm.OpsContext(ctx))
		if err != nil {
			return nil, err
		}

		// "There is no list here" — taken at its word, and NOT retried. The
		// second attempt exists to give a failed selector another go; asking
		// again after a considered "nothing to select" is paying twice for the
		// same sentence.
		if strings.TrimSpace(answer.ItemSelector) == "" {
			if n := strings.TrimSpace(answer.Notes); n != "" {
				return nil, fmt.Errorf("%w: %s", ErrNoList, n)
			}
			return nil, ErrNoList
		}

		rule := scrapesel.Rule{
			IndexURL:        indexURL,
			ItemSelector:    strings.TrimSpace(answer.ItemSelector),
			TitleSelector:   strings.TrimSpace(answer.TitleSelector),
			LinkSelector:    strings.TrimSpace(answer.LinkSelector),
			DateSelector:    strings.TrimSpace(answer.DateSelector),
			DateLayout:      strings.TrimSpace(answer.DateLayout),
			SummarySelector: strings.TrimSpace(answer.SummarySelector),
			ImageSelector:   strings.TrimSpace(answer.ImageSelector),
			AuthorSelector:  strings.TrimSpace(answer.AuthorSelector),
		}

		res, problem := tryRule(rule, pageHTML)
		if problem == "" {
			return &Proposal{
				Rule:    rule,
				Items:   res.Items,
				Matched: res.Matched,
				Notes:   strings.TrimSpace(answer.Notes),
			}, nil
		}
		lastProblem = problem
	}
	return nil, ErrNoRule
}

// tryRule compiles a rule, runs it, and returns the reason it is unacceptable.
//
// The empty string means "use it". Everything else is a sentence written to be
// fed back to the model, which is why the failures are specific: "0 of 14
// containers had a link" tells it what to change, where "it did not work" tells
// it to guess again.
// scrapeAnswer is the shape a proposal comes back in, and the schema is derived
// from it.
//
// It was an anonymous struct inside the loop with a hand-written
// `scrapeSchema()` beside it; the two had to agree and nothing checked that
// they did. The json tags are what the model reads, so they are the prompt as
// much as the type.
type scrapeAnswer struct {
	ItemSelector    string `json:"item_selector"`
	TitleSelector   string `json:"title_selector"`
	LinkSelector    string `json:"link_selector"`
	DateSelector    string `json:"date_selector"`
	DateLayout      string `json:"date_layout"`
	SummarySelector string `json:"summary_selector"`
	ImageSelector   string `json:"image_selector"`
	AuthorSelector  string `json:"author_selector"`
	Notes           string `json:"notes"`
}

func tryRule(r scrapesel.Rule, pageHTML string) (scrapesel.Result, string) {
	c, err := scrapesel.Compile(r)
	if err != nil {
		return scrapesel.Result{}, "the selectors were rejected: " + err.Error()
	}
	res, err := scrapesel.Extract(c, []byte(pageHTML), timeutil.Now())
	if err != nil {
		return res, "the page could not be extracted: " + err.Error()
	}
	switch {
	case res.Matched == 0:
		return res, "the item selector " + quote(r.ItemSelector) + " matched nothing on the page"
	case len(res.Items) == 0:
		return res, fmt.Sprintf(
			"the item selector matched %d containers but none produced a usable item (%s)",
			res.Matched, strings.Join(res.Problems, "; "))
	case len(res.Items) < minItems:
		return res, fmt.Sprintf(
			"the rule produced only %d item, which usually means the selector matched one "+
				"featured post rather than the list", len(res.Items))
	}
	// Titles that are all identical mean the title selector reached outside the
	// item container — a site-wide <h1>, typically — and every entry in the feed
	// would carry the site's name instead of its own.
	if allSame(res.Items) {
		return res, "every item came out with the same title " +
			quote(res.Items[0].Title) + ", so the title selector is matching outside the item"
	}
	return res, ""
}

func allSame(items []scrapesel.Item) bool {
	if len(items) < 2 {
		return false
	}
	first := items[0].Title
	for _, it := range items[1:] {
		if it.Title != first {
			return false
		}
	}
	return true
}

func quote(s string) string { return "“" + s + "”" }

// Samples trims a proposal's items to what the client is shown.
func (p *Proposal) Samples() []scrapesel.Item {
	if len(p.Items) <= maxSamples {
		return p.Items
	}
	return p.Items[:maxSamples]
}

// Age reports how old the newest sampled item is, which is the cheapest
// available check on whether a page is a live index or an archive nobody has
// touched since 2019. Zero when nothing carried a date.
func (p *Proposal) Age() time.Duration {
	var newest time.Time
	for _, it := range p.Items {
		if it.PublishedAt.After(newest) {
			newest = it.PublishedAt
		}
	}
	if newest.IsZero() {
		return 0
	}
	return timeutil.Now().Sub(newest)
}

func scrapeSchema() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"item_selector":    str,
			"title_selector":   str,
			"link_selector":    str,
			"date_selector":    str,
			"date_layout":      str,
			"summary_selector": str,
			"image_selector":   str,
			"author_selector":  str,
			"notes":            str,
		},
		// Strict mode requires every property in `required` and
		// additionalProperties:false, so the optional selectors are "required to
		// be present" and carry "" when the site does not have one. That is also
		// the honest encoding: "this page has no author on its index" is an
		// answer, and a missing key would be indistinguishable from a lapse.
		"required": []string{
			"item_selector", "title_selector", "link_selector", "date_selector",
			"date_layout", "summary_selector", "image_selector", "author_selector", "notes",
		},
		"additionalProperties": false,
	}
}

const scrapeInstructions = `You are writing a scraping rule for ArticleFlux, a feed reader, so that a page WITHOUT an RSS or Atom feed can be followed like one.

You get the URL of an index page and a DISTILLED OUTLINE of its HTML. Return CSS selectors that pull the list of entries out of that page.

READING THE OUTLINE

Each line is one element, indented by depth:

    tag#id.class.class[attr=value attr=value]
    "a short sample of the element's text"
    … x 11 more li.detail_list_item

Scripts, styles, SVG and most text are gone — the outline is structure, not content. The "… x N more" line is the strongest signal in it: it means N siblings shared that exact tag and classes, and a repeated block of that shape is almost always the list you are looking for.

WHAT COUNTS AS AN ENTRY

Anything a person would want to be told about when a new one appears: an article, a blog post, a chapter, an episode, a release, a changelog entry, a podcast episode, a video, an issue of a newsletter. "Article" below means whichever of those this page publishes.

THE SELECTOR LANGUAGE

- Plain CSS selectors, evaluated by Go's cascadia. Tag, class, id, descendant, child, attribute and :nth-child work. jQuery extensions do NOT: no :has(), no :contains(), no :visible, no :first (use :first-child).
- Append @attr to read an attribute instead of the text: "h2 a@href" is the anchor's href, "time@datetime" is the machine-readable date, "img@src" is the image address. Without @attr you get the element's text.
- item_selector selects the CONTAINER of one entry. Every other selector runs INSIDE a container match, so they must be relative to it and must not reach the page header, nav or footer.

RULES

1. item_selector, title_selector and link_selector are required — UNLESS there is no list on this page at all, in which case see NO LIST below. The rest are "" when the page does not carry them. Do not invent a selector for something that is not there; an empty string is a correct answer.
2. link_selector MUST end in @href. A link selector that reads text produces an unopenable item.
3. **Never select an id that belongs to ONE entry.** Outlines are full of them — li#episode_309, article#post-4471, div#chapter-88 — and a selector built on one matches exactly one thing today and nothing tomorrow. Use the shared class, or the list container plus a child: "ul#_listUl li" and "ul.chapters > li" are both good; "li#episode_309" is never right. An id is only safe on a CONTAINER of many entries.
4. Prefer stable, meaningful class names ("post", "entry", "chapter", "episode-item") over generated ones ("css-1x7ab2", "sc-fzXfMB"), and prefer semantic elements (article, li, time) over div soup. A rule survives a redesign only if it keys on what the page MEANS.
5. Choose the selector that matches the WHOLE list, not the featured item at the top and not the "start reading" button. If the outline shows one hero block and then a repeated block, the repeated block is the list.
6. DATES ARE THE FIELD MOST OFTEN GOT WRONG, and getting them wrong is silent: an unparseable date becomes "just now", so every old entry sorts to the top of the reader forever.
   - Prefer an attribute: "time@datetime" or any attribute holding an ISO 8601 value. Then leave date_layout "".
   - If the date is human text, set BOTH the selector and date_layout, where date_layout is a Go time layout written with the reference date "Mon Jan 2 15:04:05 MST 2006". Read the sample text in the outline and copy its shape:
       "Jun 1, 2026"      → "Jan 2, 2006"
       "01/06/2026"       → "02/01/2006"
       "2026-06-01 14:03" → "2006-01-02 15:04"
       "1 June 2026"      → "2 January 2006"
   - A relative date ("3 days ago", "hier") cannot be expressed as a layout. Leave BOTH the selector and the layout empty rather than guessing; the reader will use the time we first saw the entry, which is stable.
7. summary_selector is the entry's own excerpt or standfirst, not the article body and not a category label. Do not point it at view counts, like counts or comment counts.
8. Do not select navigation, pagination, related-posts rails, tag clouds, newsletter forms, "most popular" sidebars or a recommendation carousel. Those repeat too, and they are not the list.
9. notes: one short sentence naming what you keyed on, for a person deciding whether to trust this. No preamble, no restating the selectors.

NO LIST

Some pages have nothing to select, and saying so is a correct and useful answer. Return "" for item_selector, title_selector and link_selector, and say why in notes, when:

- the outline is an application shell — a div#app, div#root, a <router-view>, a "Loading" placeholder — and the entries are fetched by JavaScript after the page loads. There is nothing in this HTML to key on and no selector can find one.
- the page is a single article, an about page, a search form or a landing page rather than an index of entries.
- the only repeated blocks are navigation, tags or adverts.

A plausible-looking guess is worse than "no list here": it produces a feed of menu items that looks like it works.`
