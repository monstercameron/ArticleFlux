// Package jsonsel turns a JSON rule and an API response into items.
//
// It is scrapesel's sibling, for the half of the web that scrapesel cannot see:
// a page whose entries are fetched by JavaScript has nothing in its HTML to
// select, but the request that JavaScript makes is usually a plain GET returning
// plain JSON, and that JSON is BETTER than the rendered page would have been.
// A chapter list rendered into a DOM gives you strings; the response behind it
// gives you a chapter number as a number, a published-at as a timestamp, a
// canonical URL, and a stable id — all the types the rendering threw away.
//
// # Why not render the page instead
//
// Executing a site's JavaScript server-side would bypass every SSRF rule this
// application has: netguard lives in Go's transport, and a headless browser's
// network stack does not consult it. That is the hole plan.md §21 exists to
// close, and it is a bad trade for a self-hosted reader whose whole promise is
// that it is a small process on your own machine. Rendering stays available as a
// later rung behind an operator's explicit opt-in (§11.2b); this is the rung
// that costs one GET and executes nothing.
//
// # Pure, like scrapesel
//
// Rule plus bytes in, normalised items out. No fetching, no storage. That is
// what lets a rule be dry-run against a saved response, which is the same
// argument the HTML side makes: a rule you cannot try is a rule nobody writes.
package jsonsel

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/feeddate"
	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/timeutil"
	"github.com/monstercameron/ArticleFlux/internal/urlnorm"
)

// Rule describes how to turn one API response into items.
//
// The paths are dotted: `comic.chapters` walks two keys; `teams.0.name` indexes
// an array on the way. Deliberately not JSONPath — the expression language is
// the part of a rule a person has to be able to read at a glance in a preview,
// and `$..[?(@.type=='post')]` is not that. Everything these sites need is a
// walk down named keys.
type Rule struct {
	// IndexURL is the PAGE the reader typed. Relative links resolve against it,
	// and it is what the sidebar shows as the source's address.
	IndexURL string
	// DataURL is where the JSON actually comes from. Kept separate because they
	// are different addresses with different lifetimes: a site can move its API
	// without moving its page.
	DataURL string

	// ItemsPath is the array of entries, from the root. Required.
	ItemsPath string
	// TitlePath and LinkPath are relative to one entry. Title is required; a
	// link is required unless LinkTemplate supplies one.
	TitlePath string
	LinkPath  string
	// LinkTemplate builds a URL from an entry's fields when the response has no
	// usable one: "https://example.com/read/{slug}/ch/{chapter}". Every {field}
	// is a path relative to the entry, resolved the same way.
	LinkTemplate string

	// IDPath is the entry's stable identity. Optional and worth setting when it
	// exists: it is what makes "have I seen this?" survive a retitled entry.
	IDPath string

	DatePath    string
	SummaryPath string
	ImagePath   string
	AuthorPath  string
}

// Item is one entry, in the shape the rest of the pipeline expects — the same
// shape scrapesel produces, on purpose. Nothing downstream should be able to
// tell how a source was extracted.
type Item struct {
	GUID        string
	URL         string
	DupeKey     string
	Title       string
	Author      string
	Summary     string
	PublishedAt time.Time
	ImageURL    string
}

// Result reports what happened, not just what came out.
type Result struct {
	Items []Item
	// Found is how many entries the items path yielded.
	Found int
	// Skipped counts entries that produced nothing usable — no title, or no
	// link — and Problems says why, capped. This is the difference between "0
	// items" and "170 entries, none with a link: check link_path".
	Skipped  int
	Problems []string
}

// MaxItems bounds one extraction, for the reason scrapesel's does: a path
// pointing at the wrong array can produce thousands of "items", and the cost is
// not the extraction but the ingest and the unread count.
const MaxItems = 200

// maxProblems caps the diagnostic list. Ten identical complaints say the same
// thing as one.
const maxProblems = 5

// Compiled is a validated rule.
type Compiled struct {
	rule  Rule
	base  *url.URL
	items []string
	// The per-entry paths, pre-split so extraction is a walk rather than a parse.
	title, link, id, date, summary, image, author []string
	tmplParts                                     []tmplPart
}

// tmplPart is one piece of a link template: literal text, or a field to look up.
type tmplPart struct {
	literal string
	path    []string
}

// Compile validates a rule and pre-splits its paths.
//
// Separate from Extract for scrapesel's reason: the same rule runs against a
// response every hour forever, and every refusable error belongs here, where a
// person is looking, rather than in the poller, where nobody is.
func Compile(r Rule) (*Compiled, error) {
	c := &Compiled{rule: r}
	if strings.TrimSpace(r.ItemsPath) == "" {
		return nil, fmt.Errorf("jsonsel: an items path is required")
	}
	if strings.TrimSpace(r.TitlePath) == "" {
		return nil, fmt.Errorf("jsonsel: a title path is required")
	}
	if strings.TrimSpace(r.LinkPath) == "" && strings.TrimSpace(r.LinkTemplate) == "" {
		return nil, fmt.Errorf("jsonsel: a link path or a link template is required")
	}
	if r.IndexURL != "" {
		var err error
		if c.base, err = url.Parse(r.IndexURL); err != nil {
			return nil, fmt.Errorf("jsonsel: index URL: %w", err)
		}
	}
	c.items = splitPath(r.ItemsPath)
	c.title = splitPath(r.TitlePath)
	c.link = splitPath(r.LinkPath)
	c.id = splitPath(r.IDPath)
	c.date = splitPath(r.DatePath)
	c.summary = splitPath(r.SummaryPath)
	c.image = splitPath(r.ImagePath)
	c.author = splitPath(r.AuthorPath)

	if t := strings.TrimSpace(r.LinkTemplate); t != "" {
		parts, err := parseTemplate(t)
		if err != nil {
			return nil, err
		}
		c.tmplParts = parts
	}
	return c, nil
}

// Extract runs a compiled rule against a response body.
//
// Never returns an error for CONTENT reasons — a site can serve anything, and a
// poll that finds nothing is a health signal rather than an exception. It does
// return one for a body that is not JSON at all, because that is a different
// fact: the endpoint changed, or something is answering with an error page.
func Extract(c *Compiled, body []byte, now time.Time) (Result, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return Result{}, fmt.Errorf("jsonsel: the response is not JSON: %w", err)
	}

	node := walk(root, c.items)
	arr, ok := node.([]any)
	if !ok {
		// A path that lands on an object rather than an array is the common
		// authoring mistake, and saying so beats "0 items".
		if node == nil {
			return Result{Problems: []string{
				"the items path " + quote(c.rule.ItemsPath) + " found nothing"}}, nil
		}
		return Result{Problems: []string{
			"the items path " + quote(c.rule.ItemsPath) + " is not an array"}}, nil
	}

	res := Result{Found: len(arr)}
	for _, raw := range arr {
		if len(res.Items) >= MaxItems {
			break
		}
		entry, ok := raw.(map[string]any)
		if !ok {
			res.Skipped++
			addProblem(&res, "an entry is not an object")
			continue
		}

		title := strings.TrimSpace(text(walk(entry, c.title)))
		if title == "" {
			res.Skipped++
			addProblem(&res, "an entry has no title at "+quote(c.rule.TitlePath))
			continue
		}

		link := strings.TrimSpace(text(walk(entry, c.link)))
		if link == "" && len(c.tmplParts) > 0 {
			link = render(c.tmplParts, entry)
		}
		abs := c.absolute(link)
		if abs == "" {
			res.Skipped++
			addProblem(&res, "an entry has no usable link")
			continue
		}

		it := Item{
			Title:   title,
			URL:     abs,
			Author:  collapse(strings.TrimSpace(text(walk(entry, c.author)))),
			Summary: summaryOf(text(walk(entry, c.summary))),
			DupeKey: urlnorm.DupeKey(abs),
		}
		if img := c.absolute(strings.TrimSpace(text(walk(entry, c.image)))); img != "" {
			it.ImageURL = img
		}
		// The same parser AND the same clamp the feed pipeline uses, so a date in
		// an API and a date in an Atom entry cannot be interpreted differently.
		//
		// The clamp was the half this was missing. The comment already claimed
		// parity with the feed path, and the parser alone does not give it:
		// internal/feed, internal/extract, internal/mailparse and
		// internal/scrapesel all run their result through ClampPublished and this
		// was the one source that did not, so two dates that are the same string
		// were stored differently depending on which door they came through.
		//
		// What got through, and why the falsy-looking case is the dangerous one:
		//
		//   - A date PAST now+MaxSkew pinned the entry to the top of every list
		//     forever. That is the failure ClampPublished's own doc names.
		//   - A date before MinPublished (1990) was stored as-is. Epoch-zero is
		//     not exotic here — timeutil says feeds emit it for "no date" often
		//     enough to need a floor — and `1970-01-01T00:00:00Z` PARSES, so
		//     `IsZero()` is false and the guard below never saw it. An item
		//     stamped 1970 sorts to the bottom of every list and is deleted by
		//     the first retention sweep, because SweepItems deletes
		//     `WHERE published_at < cut`. Silently, and on the next cycle.
		//
		// ClampPublished folds in the empty and unparseable cases too — both
		// leave `claimed` zero and come back as first-seen — which is what the
		// separate IsZero branch here used to do.
		var claimed time.Time
		if d := strings.TrimSpace(text(walk(entry, c.date))); d != "" {
			claimed, _ = feeddate.Parse(d)
		}
		// First seen when there is nothing trustworthy to use. Stable afterwards:
		// published_at is never rewritten on re-ingest.
		it.PublishedAt = timeutil.ClampPublished(claimed, now)

		// Identity, in order of how well it survives the site editing itself:
		// an explicit id, then the URL, then the title. The last is the weakest
		// and is why IDPath is worth setting when a response has one.
		switch {
		case len(c.id) > 0 && text(walk(entry, c.id)) != "":
			it.GUID = "id:" + text(walk(entry, c.id))
		default:
			// ItemKey, not the raw URL — the same derivation internal/feed and
			// internal/scrapesel use for their URL fallback.
			//
			// This is IDENTITY. `items` carries `UNIQUE(source_id, guid)` and
			// ingest looks a row up by exactly that pair, so a guid that differs
			// between two polls is not a near-miss: it is a second article. The
			// raw address is a bad identity because three things about it move
			// without the entry changing, and ItemKey removes all three:
			//
			//   - Query ORDER. stripQuery sorts what it keeps, so ?b=2&a=1 and
			//     ?a=1&b=2 are one key. A JSON API assembling a link from a map
			//     has no reason to emit a stable order, and nothing about the
			//     response would look wrong when it does not.
			//   - Tracking parameters. `ref`, `source`, `s` and `share` are in
			//     the strip list and are exactly the kind an API attaches to the
			//     links it hands out.
			//   - A trailing slash.
			//
			// Any of those changing produced a NEW item on every poll, forever,
			// for that source. DupeKey above did not save it: that index is not
			// unique and is for cross-source suppression, not ingest identity.
			//
			// One-time effect worth knowing: on the next poll after this change,
			// an existing entry whose stored guid is a raw URL that ItemKey would
			// rewrite ingests once as new. That is the same set of entries that
			// was re-ingesting on EVERY poll, so this trades an unbounded
			// duplication for a single one.
			it.GUID = urlnorm.ItemKey(abs)
		}
		res.Items = append(res.Items, it)
	}
	return res, nil
}

// absolute resolves a link against the page's own address.
func (c *Compiled) absolute(raw string) string {
	if raw == "" {
		return ""
	}
	if c.base == nil {
		if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
			return raw
		}
		return ""
	}
	u, err := c.base.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

// walk follows a dotted path from a node. A numeric segment indexes an array.
func walk(node any, path []string) any {
	for _, seg := range path {
		switch v := node.(type) {
		case map[string]any:
			node = v[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(v) {
				return nil
			}
			node = v[i]
		default:
			return nil
		}
		if node == nil {
			return nil
		}
	}
	return node
}

// text renders a JSON scalar as a string.
//
// Numbers matter here: a chapter number arrives as a float64 and must render as
// "1515" rather than "1515.000000", because it ends up in a title and in a URL.
func text(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case json.Number:
		return t.String()
	case nil:
		return ""
	default:
		return ""
	}
}

func splitPath(p string) []string {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	out := strings.Split(p, ".")
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	return out
}

// parseTemplate splits "…/read/{slug}/ch/{chapter}" into literals and lookups.
func parseTemplate(t string) ([]tmplPart, error) {
	var parts []tmplPart
	for {
		open := strings.IndexByte(t, '{')
		if open < 0 {
			if t != "" {
				parts = append(parts, tmplPart{literal: t})
			}
			return parts, nil
		}
		close := strings.IndexByte(t[open:], '}')
		if close < 0 {
			return nil, fmt.Errorf("jsonsel: link template has an unclosed {")
		}
		close += open
		if open > 0 {
			parts = append(parts, tmplPart{literal: t[:open]})
		}
		field := strings.TrimSpace(t[open+1 : close])
		if field == "" {
			return nil, fmt.Errorf("jsonsel: link template has an empty {}")
		}
		parts = append(parts, tmplPart{path: splitPath(field)})
		t = t[close+1:]
	}
}

func render(parts []tmplPart, entry map[string]any) string {
	var b strings.Builder
	for _, p := range parts {
		if p.path == nil {
			b.WriteString(p.literal)
			continue
		}
		v := text(walk(entry, p.path))
		if v == "" {
			// A template with a missing field would produce a URL with a hole in
			// it, which is worse than no URL: it 404s and looks like the site
			// broke.
			return ""
		}
		b.WriteString(v)
	}
	return b.String()
}

func addProblem(r *Result, s string) {
	for _, p := range r.Problems {
		if p == s {
			return
		}
	}
	if len(r.Problems) < maxProblems {
		r.Problems = append(r.Problems, s)
	}
}

func quote(s string) string { return "“" + s + "”" }

// summaryOf renders an entry's summary field the way this pipeline's siblings do.
//
// # Why a JSON string still needs the HTML treatment
//
// This package's own doc says items come out in "the same shape scrapesel
// produces, on purpose. Nothing downstream should be able to tell how a source
// was extracted." Summary was the field where that stopped being true: scrapesel
// stores `truncate(collapse(sanitize.Text(raw)), 280)` and internal/feed stores
// `summarizeText(stripTags(...))`, and this stored whatever the API said, whole.
//
// Three consequences, in the order they bite:
//
//   - MARKUP SHOWS. A `"description"` carrying `<p>Chapter 12 is out</p>` is
//     ordinary in an API response, and the item list renders the summary with
//     `html.Text` — escaped, correctly, because it is supposed to be text. So the
//     reader saw the tags. sanitize.Text is what the other two paths use to make
//     it text, and it parses rather than pattern-matching, so entities and
//     malformed markup come out right.
//   - NO CEILING. Plenty of APIs return the whole article in the summary field.
//     Stored whole, it is carried on every list query and rendered into a row
//     built for two lines.
//   - WHITESPACE. JSON strings keep their newlines and tabs; collapse is what
//     turns them into one line, as it does for scrapesel.
//
// 280 matches scrapesel deliberately rather than by coincidence: two ingest
// paths feeding one list should not disagree about how long a row's text is.
func summaryOf(raw string) string {
	return truncate(collapse(sanitize.Text(raw)), 280)
}

// collapse and truncate mirror scrapesel's, which is the point — see summaryOf.
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
