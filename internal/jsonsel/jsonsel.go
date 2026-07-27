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
			Author:  strings.TrimSpace(text(walk(entry, c.author))),
			Summary: strings.TrimSpace(text(walk(entry, c.summary))),
			DupeKey: urlnorm.DupeKey(abs),
		}
		if img := c.absolute(strings.TrimSpace(text(walk(entry, c.image)))); img != "" {
			it.ImageURL = img
		}
		if d := strings.TrimSpace(text(walk(entry, c.date))); d != "" {
			// The same parser the feed pipeline uses, so a date in an API and a
			// date in an Atom entry cannot be interpreted differently.
			if t, err := feeddate.Parse(d); err == nil {
				it.PublishedAt = t
			}
		}
		if it.PublishedAt.IsZero() {
			// First seen, like a feed entry with no date. Stable afterwards:
			// published_at is never rewritten on re-ingest.
			it.PublishedAt = now
		}

		// Identity, in order of how well it survives the site editing itself:
		// an explicit id, then the URL, then the title. The last is the weakest
		// and is why IDPath is worth setting when a response has one.
		switch {
		case len(c.id) > 0 && text(walk(entry, c.id)) != "":
			it.GUID = "id:" + text(walk(entry, c.id))
		default:
			it.GUID = abs
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
