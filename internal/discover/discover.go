// Package discover answers "can I follow this page?" — plan.md §11, the
// subscribe ladder, deterministic first and the model last.
//
// The ladder, in the order it is climbed here:
//
//  1. <link rel="alternate"> on the page               ~60-70% of sites
//  2. a short list of conventional paths                ~20% more
//  3. what the reader typed, if it parses as a feed     (they pasted the feed)
//  4. the model reads the page and proposes a feed      — not here; §11 rung 4
//  5. the model writes a SCRAPE RULE                    — internal/smart
//
// **The parser disposes.** Nothing on rungs 1-3 is offered on the strength of a
// URL or a `type=` attribute: every candidate is fetched and run through the
// real feed parser, and a candidate that yields zero items is not returned. That
// is the whole reason this package fetches at all rather than returning links —
// a `<link rel="alternate">` pointing at a 404 is extremely common, and offering
// it produces a subscription that never has anything in it and a reader who
// blames the reader.
//
// # Why the page fetch lives here rather than in internal/extract
//
// Extraction wants an ARTICLE out of a page and reports failure when there is
// not one; discovery and scraping want the page's MARKUP, index pages
// especially — which is exactly the input readability is built to reject. Two
// different questions about the same bytes, so this owns the raw fetch and
// extract keeps owning the readable one.
package discover

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/monstercameron/ArticleFlux/internal/charsetdec"
	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
	"github.com/monstercameron/ArticleFlux/internal/recommend"
)

// MaxSamples is how many posts are kept off a candidate's own feed. Two: the
// smallest number that lets a relevance check tell "this post happened to
// mention it once" from "this is what the site is about" apart from a single
// post, without turning the validating fetch into a scrape of the whole feed.
const MaxSamples = 2

// UserAgent identifies us, with a contact URL, per §14.2's guardrails. An
// anonymous scraper is the one that gets a server's IP banned for every tenant
// on it.
const UserAgent = "ArticleFlux/0.1 (+https://github.com/monstercameron/ArticleFlux)"

// MaxBodyBytes bounds one page. Index pages are bigger than articles — a busy
// homepage with inlined SVG runs to a couple of megabytes — and smaller than a
// feed archive.
const MaxBodyBytes = 8 << 20

// probeTimeout bounds one candidate check. Short on purpose: probing runs a
// handful of URLs that mostly 404, and a site that takes eight seconds to say
// "no" must not hold up the four that already said it.
const probeTimeout = 8 * time.Second

var (
	// ErrNotHTML means the address returned something this package cannot read
	// as a page.
	ErrNotHTML = errors.New("discover: not an HTML page")
	// ErrTooLarge means the body exceeded MaxBodyBytes.
	ErrTooLarge = errors.New("discover: page too large")
	// ErrNotJSON means an endpoint that used to answer with JSON no longer does
	// — an error page, a login redirect, an API that moved.
	ErrNotJSON = errors.New("discover: the response is not JSON")
)

// Page is a fetched HTML page, kept in every form its consumers need.
//
// The bytes, the DOM and the decoded string all travel together because the
// three consumers want three different things — the scrape extractor wants the
// DOM, the model wants a distilled string, and the caller wants to know where
// the page actually came from — and re-parsing 400 KB of HTML twice to avoid
// one struct is a bad trade.
type Page struct {
	// URL is the address after redirects. Relative links resolve against it,
	// not against what was typed.
	URL   string
	Title string
	HTML  string
	Doc   *html.Node
}

// Candidate is a feed that was found and parsed.
type Candidate struct {
	URL   string
	Title string
	Items int
	// How is "declared" or "probed" — see the proto comment. A guess and a
	// declaration are different claims and the reader is told which is which.
	How string
	// Samples are up to MaxSamples posts pulled from this same fetch, for
	// recommendjob's relevance check. Never re-fetched separately — the parser
	// already read them disposing of the feed body, so keeping a couple costs
	// nothing extra.
	Samples []recommend.Sample
}

// Fetcher fetches pages. Reuse one: it holds a connection pool and the robots
// cache.
type Fetcher struct {
	client       *http.Client
	feeds        *feed.Fetcher
	allowPrivate bool

	mu     sync.Mutex
	robots map[string]robotsEntry
}

// Config configures a Fetcher.
type Config struct {
	// AllowPrivateAddresses relaxes the SSRF guard, exactly as feed.Config does
	// and for the same reason. Off by default; the dev server turns it on for a
	// loopback bind so local fixtures work.
	AllowPrivateAddresses bool
	Timeout               time.Duration
}

// New returns a Fetcher whose transport is SSRF-guarded.
func New(cfg Config) *Fetcher {
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	return &Fetcher{
		allowPrivate: cfg.AllowPrivateAddresses,
		client: netguard.Client(netguard.Options{
			Timeout:      cfg.Timeout,
			DialTimeout:  10 * time.Second,
			UserAgent:    UserAgent,
			AllowPrivate: cfg.AllowPrivateAddresses,
			Purpose:      "discover",
		}),
		feeds:  feed.New(feed.Config{AllowPrivateAddresses: cfg.AllowPrivateAddresses}),
		robots: map[string]robotsEntry{},
	}
}

// Page fetches one page and parses it.
func (f *Fetcher) Page(ctx context.Context, pageURL string) (*Page, error) {
	body, final, ctype, err := f.get(ctx, pageURL,
		"text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	if err != nil {
		return nil, err
	}
	// An index page in Windows-1252 is not exotic, and an HTML parser handed
	// undeclared bytes produces mojibake rather than an error — the same
	// double-decode trap internal/extract documents at length.
	decoded, _, err := charsetdec.Decode(body, ctype)
	if err != nil {
		return nil, err
	}
	text := string(decoded)
	doc, err := html.Parse(strings.NewReader(text))
	if err != nil {
		return nil, ErrNotHTML
	}
	return &Page{URL: final, Title: pageTitle(doc), HTML: text, Doc: doc}, nil
}

// Feeds climbs rungs 1-3 and returns what actually parsed.
//
// Ordered by strength of evidence rather than by discovery order: a feed the
// page declares outranks one found at a guessed path, and among equals the one
// with more items comes first. The reader picks from a list whose first entry is
// almost always the right answer.
func (f *Fetcher) Feeds(ctx context.Context, rawURL string) (*Page, []Candidate, error) {
	// Rung 3 first, cheaply: if what was typed IS a feed, none of the rest of
	// this matters. It costs one fetch that would happen anyway.
	if p, err := f.feeds.Fetch(ctx, rawURL, feed.Conditional{}); err == nil && p != nil {
		return nil, []Candidate{{
			URL: rawURL, Title: p.Title, Items: len(p.Items), How: "declared",
			Samples: sampleItems(p.Items),
		}}, nil
	}

	page, err := f.Page(ctx, rawURL)
	if err != nil {
		return nil, nil, err
	}

	seen := map[string]bool{}
	var out []Candidate
	for _, u := range declaredFeeds(page) {
		if seen[u] {
			continue
		}
		seen[u] = true
		if c, ok := f.check(ctx, u, "declared"); ok {
			out = append(out, c)
		}
	}
	// Probing only when the page declared nothing usable. A site that says where
	// its feed is has answered the question, and hammering six more paths on it
	// is six requests spent to disagree with the publisher.
	if len(out) == 0 {
		for _, u := range probeURLs(page.URL) {
			if seen[u] {
				continue
			}
			seen[u] = true
			if c, ok := f.check(ctx, u, "probed"); ok {
				out = append(out, c)
				// One is enough from a probe list whose entries are aliases of
				// each other far more often than they are different feeds.
				break
			}
		}
	}
	return page, out, nil
}

// check fetches a candidate and parses it. The bool is "offer this".
func (f *Fetcher) check(ctx context.Context, u, how string) (Candidate, bool) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	p, err := f.feeds.Fetch(ctx, u, feed.Conditional{})
	if err != nil || p == nil || len(p.Items) == 0 {
		// Zero items is a rejection, not a pass with a caveat. An empty feed is
		// indistinguishable from a wrong URL at this point, and offering it
		// produces a subscription that never fills.
		return Candidate{}, false
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = u
	}
	return Candidate{
		URL: u, Title: title, Items: len(p.Items), How: how,
		Samples: sampleItems(p.Items),
	}, true
}

// sampleItems keeps the first MaxSamples items' title and summary, and
// nothing else off a feed's items — no URL, no GUID, no author, no dates.
// What recommendjob passes to Smart+ is scoped again at that boundary
// (internal/llm/relevance.go), but the fewer fields that travel this far, the
// fewer that can be forgotten and passed on later.
func sampleItems(items []feed.Item) []recommend.Sample {
	if len(items) > MaxSamples {
		items = items[:MaxSamples]
	}
	out := make([]recommend.Sample, 0, len(items))
	for _, it := range items {
		out = append(out, recommend.Sample{
			Title:   strings.TrimSpace(it.Title),
			Summary: strings.TrimSpace(it.Summary),
		})
	}
	return out
}

// declaredFeeds reads <link rel="alternate"> — rung 1.
//
// Both `rel="alternate"` and the sloppier `rel="alternate stylesheet"`-style
// multi-value rel are handled, because the attribute is a space-separated set
// and treating it as a string misses real feeds on real sites.
func declaredFeeds(p *Page) []string {
	base, err := url.Parse(p.URL)
	if err != nil {
		return nil
	}
	var out []string
	walk(p.Doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "link" {
			return
		}
		var rel, typ, href string
		for _, a := range n.Attr {
			switch strings.ToLower(a.Key) {
			case "rel":
				rel = strings.ToLower(a.Val)
			case "type":
				typ = strings.ToLower(strings.TrimSpace(a.Val))
			case "href":
				href = strings.TrimSpace(a.Val)
			}
		}
		if href == "" || !hasWord(rel, "alternate") || !feedTypes[typ] {
			return
		}
		if abs, err := base.Parse(href); err == nil {
			out = append(out, abs.String())
		}
	})
	return out
}

// feedTypes are the content types a declaration must claim.
//
// A closed set rather than "anything with 'xml' in it": `application/xml` is
// also how a sitemap and an XSLT stylesheet announce themselves, and following
// those is a request spent on something that can never be a feed.
var feedTypes = map[string]bool{
	"application/rss+xml":   true,
	"application/atom+xml":  true,
	"application/feed+json": true,
	"application/json":      true,
	"text/xml":              true,
}

// probeURLs are rung 2: the conventional paths, in the order they are worth
// trying.
//
// Kept short deliberately. Every entry is a request to somebody else's server,
// and the tail of this list — /feeds/posts/default, /?feed=rss2 — buys a
// fraction of a percent for a permanent cost in politeness. The long tail is
// what rung 4 is for.
func probeURLs(pageURL string) []string {
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	root := &url.URL{Scheme: u.Scheme, Host: u.Host}
	paths := []string{"/feed", "/rss", "/feed.xml", "/rss.xml", "/atom.xml", "/index.xml"}

	var out []string
	// The typed path first: a reader on example.com/blog wants that blog's feed,
	// and example.com/feed is the site-wide one, which is a different thing.
	if dir := strings.TrimSuffix(u.Path, "/"); dir != "" && dir != "/" {
		for _, p := range paths {
			out = append(out, root.String()+dir+p)
		}
	}
	for _, p := range paths {
		out = append(out, root.String()+p)
	}
	return out
}

// get performs one guarded, size-capped GET and returns the body, the final URL
// and the content type.
func (f *Fetcher) get(ctx context.Context, rawURL, accept string) ([]byte, string, string, error) {
	if f.allowPrivate {
		if err := netguard.CheckURLPermissive(rawURL); err != nil {
			return nil, "", "", err
		}
	} else if err := netguard.CheckURL(rawURL); err != nil {
		return nil, "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("Accept", accept)

	res, err := f.client.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		_ = res.Body.Close()
	}()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, "", "", fmt.Errorf("discover: HTTP %d", res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(body) > MaxBodyBytes {
		return nil, "", "", ErrTooLarge
	}
	final := rawURL
	if res.Request != nil && res.Request.URL != nil {
		final = res.Request.URL.String()
	}
	return body, final, res.Header.Get("Content-Type"), nil
}

// pageTitle is the document's <title>, trimmed and collapsed.
func pageTitle(doc *html.Node) string {
	var title string
	walk(doc, func(n *html.Node) {
		if title != "" || n.Type != html.ElementNode || n.Data != "title" {
			return
		}
		if n.FirstChild != nil {
			title = strings.Join(strings.Fields(n.FirstChild.Data), " ")
		}
	})
	if len(title) > 200 {
		title = title[:200]
	}
	return title
}

func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}

// hasWord reports whether a space-separated attribute contains a token.
func hasWord(attr, word string) bool {
	for _, f := range strings.Fields(attr) {
		if f == word {
			return true
		}
	}
	return false
}
