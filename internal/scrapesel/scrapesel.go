// Package scrapesel turns a scrape rule and a page of HTML into items
// (TODO 4.7, plan.md §14.2).
//
// Pure: rule plus bytes in, normalised items out. No fetching, no robots.txt
// check, no storage — 6.8 does those. Keeping the extraction pure is what lets
// the rule editor preview against a saved page, which is the same argument as
// the rules engine's: a scrape rule you cannot dry-run is a scrape rule nobody
// will write, because the alternative is editing selectors against a live site
// and guessing.
//
// # A scraped site is a source like any other
//
// The output is `feed.Item`-shaped on purpose. A site without a feed still
// becomes a `sources` row with kind='scrape', so ingest, dedup, health, rules,
// ranking and search all work on it unchanged. Nothing downstream needs to know
// a source was scraped, and that is the whole reason this package exists rather
// than a parallel pipeline.
//
// # The failure mode this is designed around
//
// A site redesign silently breaks a scrape rule, and the result looks exactly
// like "they stopped publishing". Distinguishing those is the difference between
// a nudge that says "your rule for X stopped matching" and a feed that quietly
// goes dead. So Extract reports how many item nodes it saw and how many it could
// use, and 6.8 counts consecutive empty results — the RULE_BROKEN trigger the
// `scrape_rules.empty_polls` column exists for.
package scrapesel

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"

	"github.com/monstercameron/ArticleFlux/internal/attrsel"
	"github.com/monstercameron/ArticleFlux/internal/feeddate"
	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/timeutil"
	"github.com/monstercameron/ArticleFlux/internal/urlnorm"
)

// Rule describes how to turn one site's index page into items.
//
// The selectors are attrsel syntax (2.8): `h2 a@href` is a selector plus an
// attribute target, which cascadia has no concept of and which is needed for
// almost every link and image.
type Rule struct {
	// IndexURL is the page to scrape. Used here only to resolve relative links.
	IndexURL string
	// ItemSelector matches each item's container. Everything else is evaluated
	// relative to a match of this.
	ItemSelector string
	// Title, Link are required.
	TitleSelector string
	LinkSelector  string
	// The rest are optional; an empty selector means "this site does not
	// expose it", not "look harder".
	DateSelector    string
	DateLayout      string
	SummarySelector string
	ImageSelector   string
	AuthorSelector  string
}

// Item is one scraped entry, in the shape the rest of the pipeline expects.
type Item struct {
	GUID        string
	URL         string
	DupeKey     string
	Title       string
	Author      string
	Summary     string
	ContentHTML string
	PublishedAt time.Time
	ImageURL    string
}

// Result reports what happened, not just what was produced.
type Result struct {
	Items []Item
	// Matched is how many nodes ItemSelector found.
	Matched int
	// Skipped counts nodes that matched the container but yielded no usable
	// item — no link, or no title.
	Skipped int
	// Problems describes what went wrong per skipped node, capped. This is what
	// the rule editor shows, and it is the difference between "0 items" and
	// "12 containers matched but none had a link — check your link selector".
	Problems []string
}

// Compiled is a validated rule, ready to run against many pages.
type Compiled struct {
	rule    Rule
	base    *url.URL
	item    cascadia.Selector
	title   field
	link    field
	dateSel field
	summary field
	image   field
	author  field
}

type field struct {
	set  bool
	sel  cascadia.Selector
	attr string
}

// Compile validates a rule and pre-parses its selectors.
//
// Separate from Extract because the same rule runs against a page every poll
// forever, and re-parsing eight selectors each time is work done for no reason.
// It is also where a rule is told it is wrong: Extract must never fail on a page
// (a site can serve anything), so all the refusable errors live here.
func Compile(r Rule) (*Compiled, error) {
	c := &Compiled{rule: r}

	if strings.TrimSpace(r.ItemSelector) == "" {
		return nil, fmt.Errorf("scrapesel: an item selector is required")
	}
	if strings.TrimSpace(r.TitleSelector) == "" {
		return nil, fmt.Errorf("scrapesel: a title selector is required")
	}
	if strings.TrimSpace(r.LinkSelector) == "" {
		return nil, fmt.Errorf("scrapesel: a link selector is required")
	}

	// The item selector is a plain CSS selector — an attribute target on it
	// would be meaningless, since it selects a container rather than a value.
	sel, err := cascadia.Compile(strings.TrimSpace(r.ItemSelector))
	if err != nil {
		return nil, fmt.Errorf("scrapesel: item selector: %w", err)
	}
	c.item = sel

	if r.IndexURL != "" {
		if c.base, err = url.Parse(r.IndexURL); err != nil {
			return nil, fmt.Errorf("scrapesel: index URL: %w", err)
		}
	}

	for _, f := range []struct {
		name string
		raw  string
		dst  *field
	}{
		{"title", r.TitleSelector, &c.title},
		{"link", r.LinkSelector, &c.link},
		{"date", r.DateSelector, &c.dateSel},
		{"summary", r.SummarySelector, &c.summary},
		{"image", r.ImageSelector, &c.image},
		{"author", r.AuthorSelector, &c.author},
	} {
		if strings.TrimSpace(f.raw) == "" {
			continue
		}
		expr, err := attrsel.Parse(f.raw)
		if err != nil {
			return nil, fmt.Errorf("scrapesel: %s selector: %w", f.name, err)
		}
		sel, err := cascadia.Compile(expr.Selector)
		if err != nil {
			return nil, fmt.Errorf("scrapesel: %s selector %q: %w", f.name, expr.Selector, err)
		}
		*f.dst = field{set: true, sel: sel, attr: expr.Attr}
	}

	// A link selector that reads text rather than an attribute is almost
	// certainly a mistake — it produces the anchor's label as a URL. Refusing it
	// at authoring time beats a feed of unopenable items.
	if c.link.attr == "" {
		return nil, fmt.Errorf(
			"scrapesel: the link selector %q reads text, not a URL; it probably needs @href",
			r.LinkSelector)
	}
	return c, nil
}

// MaxItems bounds one extraction.
//
// A selector matching `div` on a large page can produce thousands of "items",
// and the cost of that is not the extraction — it is the ingest, the fan-out and
// the unread count. A rule this wrong should be visibly wrong at 200 rather than
// quietly expensive at 20,000.
const MaxItems = 200

// Extract runs a compiled rule against a page.
//
// Never returns an error for content reasons. A site can serve anything and a
// poll that fails is a health signal (§14.2's empty_polls), not an exception —
// the caller needs the counts to tell a broken rule apart from a quiet site.
func Extract(c *Compiled, page []byte, now time.Time) (Result, error) {
	doc, err := html.Parse(strings.NewReader(string(page)))
	if err != nil {
		return Result{}, fmt.Errorf("scrapesel: %w", err)
	}

	var res Result
	for _, node := range cascadia.QueryAll(doc, c.item) {
		res.Matched++
		if len(res.Items) >= MaxItems {
			continue
		}

		item, problem := c.itemFrom(node, now)
		if problem != "" {
			res.Skipped++
			if len(res.Problems) < 5 {
				res.Problems = append(res.Problems, problem)
			}
			continue
		}
		res.Items = append(res.Items, item)
	}
	return res, nil
}

func (c *Compiled) itemFrom(node *html.Node, now time.Time) (Item, string) {
	link := c.read(node, c.link)
	if link == "" {
		return Item{}, "a container matched but had no link"
	}
	link = c.resolve(link)
	if link == "" {
		return Item{}, "a link could not be resolved against the index URL"
	}

	title := strings.TrimSpace(collapse(c.read(node, c.title)))
	if title == "" {
		return Item{}, "a container matched but had no title"
	}

	it := Item{
		Title: title,
		URL:   link,
		// Identity is the normalised link, and it must keep the fragment for the
		// reason urlnorm.ItemKey exists: a one-page site distinguishes its
		// entries by anchor and nothing else. The feed corpus found this the
		// hard way.
		GUID:    urlnorm.ItemKey(link),
		DupeKey: urlnorm.DupeKey(link),
	}

	if c.summary.set {
		// The summary selector points at a content BLOCK, so its markup is
		// wanted — an excerpt reduced to plain text loses its emphasis, its
		// links and its paragraph breaks, and the reading pane then renders a
		// wall. readInner returns the inner HTML for that reason, where every
		// other field reads text or an attribute.
		//
		// It is publisher HTML from a page we chose to fetch, so it goes through
		// the same policy feed content does.
		raw := c.readInner(node, c.summary)
		it.ContentHTML = sanitize.HTML(raw, sanitize.Feed)
		it.Summary = truncate(collapse(sanitize.Text(raw)), 280)
	}
	if c.author.set {
		it.Author = strings.TrimSpace(collapse(c.read(node, c.author)))
	}
	if c.image.set {
		it.ImageURL = c.resolve(c.read(node, c.image))
	}

	it.PublishedAt = c.resolveDate(node, now)
	return it, ""
}

// resolveDate resolves the published time, falling through to first-seen.
//
// Never the zero time: an item stamped year 1 sorts to the bottom of every list
// forever and is functionally deleted. Same rule as the feed parser, and worth
// repeating here because a scraped site is far more likely to have an
// unparseable date than an RSS feed is.
func (c *Compiled) resolveDate(node *html.Node, now time.Time) time.Time {
	if !c.dateSel.set {
		return now
	}
	raw := strings.TrimSpace(collapse(c.read(node, c.dateSel)))
	if raw == "" {
		return now
	}

	// An explicit layout wins: a site writing "01/02/2006" is ambiguous, and the
	// rule author is the only one who knows whether that is January or February.
	if c.rule.DateLayout != "" {
		if t, err := time.Parse(c.rule.DateLayout, raw); err == nil {
			return timeutil.ClampPublished(t, now)
		}
	}
	if t, err := feeddate.Parse(raw); err == nil {
		return timeutil.ClampPublished(t, now)
	}
	return now
}

// read pulls a value out of a node using a compiled field.
func (c *Compiled) read(node *html.Node, f field) string {
	if !f.set {
		return ""
	}
	// The container itself may be the match — `article a@href` inside an item
	// whose container IS the anchor. QueryAll only searches descendants, so
	// without this a perfectly reasonable rule silently finds nothing.
	target := cascadia.Query(node, f.sel)
	if target == nil && f.sel.Match(node) {
		target = node
	}
	if target == nil {
		return ""
	}
	if f.attr == "" {
		return textOf(target)
	}
	for _, a := range target.Attr {
		if strings.EqualFold(a.Key, f.attr) {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

// readInner returns a matched node's inner HTML rather than its text.
//
// Only the summary field uses it. When the selector names an attribute the
// attribute still wins, because `div.body@data-summary` is an unambiguous
// request for a string.
func (c *Compiled) readInner(node *html.Node, f field) string {
	if !f.set {
		return ""
	}
	if f.attr != "" {
		return c.read(node, f)
	}
	target := cascadia.Query(node, f.sel)
	if target == nil && f.sel.Match(node) {
		target = node
	}
	if target == nil {
		return ""
	}
	var b strings.Builder
	for child := target.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&b, child); err != nil {
			// Render only fails on a malformed tree, which x/net/html does not
			// produce. Falling back to text keeps a broken node from silently
			// emptying the item.
			return textOf(target)
		}
	}
	return b.String()
}

// resolve makes a URL absolute against the index page.
//
// Scraped sites use relative links constantly, and a relative URL stored as an
// item's identity is both unopenable and non-unique across sites.
func (c *Compiled) resolve(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.IsAbs() {
		if u.Scheme != "http" && u.Scheme != "https" {
			// javascript: and data: links appear in navigation. They are not
			// articles, and one stored as an item is an entry that runs code.
			return ""
		}
		return u.String()
	}
	if c.base == nil {
		return ""
	}
	return c.base.ResolveReference(u).String()
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(x *html.Node) {
		if x.Type == html.ElementNode {
			switch x.Data {
			case "script", "style", "template", "noscript":
				return
			}
		}
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return b.String()
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:") + "…"
}
