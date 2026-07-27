// Package outlinks extracts and normalises the links out of an article body.
//
// This is §18.7 rung 1, and it is the best recommendation signal available for
// the price of a URL parse. The reasoning: the articles you read contain links,
// and the domains an author links to are the domains that author considers worth
// reading. Aggregate that across everything a user reads and you have a ranked
// list of sites they have never subscribed to but are already, indirectly,
// reading — with no model, no embedding, and no third-party call.
//
// The hard part is not extraction. It is telling an editorial link apart from
// site furniture: nav bars, share buttons, "related posts", and the author's own
// social profiles all appear as <a href> in the same document.
package outlinks

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"github.com/monstercameron/ArticleFlux/internal/urlnorm"
)

// Link is one outbound link with the context needed to judge it.
type Link struct {
	URL    string // normalised via urlnorm.Norm
	Host   string // registrable-ish host, no www.
	Anchor string // trimmed anchor text
	// Count is how many times this URL appeared in the document. A link repeated
	// five times is usually furniture, not emphasis.
	Count int
}

// chrome are hosts that are almost never an editorial recommendation. They are
// share targets, platform furniture, and license badges.
//
// Excluding them is a judgement call, and it is the right one: a reader who
// links to a Wikipedia article is citing, not recommending a subscription, and
// "twitter.com" as a top recommendation would be noise on every profile.
var chrome = map[string]bool{
	"twitter.com": true, "x.com": true, "facebook.com": true,
	"linkedin.com": true, "reddit.com": true, "news.ycombinator.com": true,
	"pinterest.com": true, "instagram.com": true, "threads.net": true,
	"mastodon.social": true, "bsky.app": true, "t.me": true, "whatsapp.com": true,
	"youtube.com": true, "youtu.be": true, "vimeo.com": true,
	"creativecommons.org": true, "w3.org": true, "schema.org": true,
	"gravatar.com": true, "feedburner.com": true, "feeds.feedburner.com": true,
	"addtoany.com": true, "sharethis.com": true, "buffer.com": true,
	"paypal.com": true, "patreon.com": true, "ko-fi.com": true,
	"amazon.com": true, "amzn.to": true, // affiliate links, not recommendations
	"wikipedia.org": true, "en.wikipedia.org": true, // citation, not subscription
	"archive.org": true, "web.archive.org": true,
	"doi.org": true, "arxiv.org": true,
}

// furnitureAnchors are anchor texts that mark a link as navigation regardless of
// where it points.
var furnitureAnchors = map[string]bool{
	"": true, "share": true, "tweet": true, "post": true, "email": true,
	"print": true, "home": true, "next": true, "previous": true, "prev": true,
	"read more": true, "continue reading": true, "more": true, "permalink": true,
	"subscribe": true, "rss": true, "comments": true, "reply": true,
	"back": true, "top": true, "menu": true, "search": true, "contact": true,
	"privacy": true, "terms": true, "about": true, "advertisement": true,
	"click here": true, "here": true, "link": true, "source": true, "via": true,
}

// Options tune extraction.
type Options struct {
	// SelfHost is the article's own host. Self-links are navigation within the
	// site being read and carry no recommendation signal.
	SelfHost string
	// IncludeChrome disables the chrome-host filter. Off by default.
	IncludeChrome bool
	// MaxPerDoc bounds how many distinct links one document may contribute, so a
	// link-dump post cannot dominate a profile. Zero means 50.
	MaxPerDoc int
}

// Extract pulls the editorial outbound links from an HTML fragment.
//
// base resolves relative hrefs. Without it a relative link is unresolvable and
// is dropped, which is correct: a relative link is by definition a self-link.
func Extract(htmlFrag, base string, opt Options) []Link {
	if opt.MaxPerDoc == 0 {
		opt.MaxPerDoc = 50
	}
	baseURL, _ := url.Parse(base)
	selfHost := opt.SelfHost
	if selfHost == "" && baseURL != nil {
		selfHost = urlnorm.Host(base)
	}

	doc, err := html.Parse(strings.NewReader(htmlFrag))
	if err != nil {
		return nil
	}

	byURL := map[string]*Link{}
	var order []string

	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inNav bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "nav", "header", "footer", "aside":
				// Structural furniture. Everything inside is navigation.
				inNav = true
			case "a":
				if !inNav {
					if l, ok := consider(n, baseURL, selfHost, opt); ok {
						if existing, seen := byURL[l.URL]; seen {
							existing.Count++
						} else {
							cp := l
							byURL[l.URL] = &cp
							order = append(order, l.URL)
						}
					}
				}
			}
			if hasFurnitureClass(n) {
				inNav = true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inNav)
		}
	}
	walk(doc, false)

	out := make([]Link, 0, len(order))
	for _, u := range order {
		if len(out) >= opt.MaxPerDoc {
			break
		}
		out = append(out, *byURL[u])
	}
	return out
}

// Hosts collapses links to distinct hosts with counts. This is what actually
// feeds the recommendation table — a domain linked once from twenty articles is
// a far stronger signal than one linked twenty times from a single article.
func Hosts(links []Link) map[string]int {
	out := map[string]int{}
	for _, l := range links {
		out[l.Host]++
	}
	return out
}

func consider(n *html.Node, base *url.URL, selfHost string, opt Options) (Link, bool) {
	href := attr(n, "href")
	if href == "" || strings.HasPrefix(href, "#") {
		return Link{}, false
	}
	// rel="nofollow ugc sponsored" is the author saying this is not an
	// endorsement. Taking them at their word is both correct and free.
	rel := strings.ToLower(attr(n, "rel"))
	for _, bad := range []string{"nofollow", "ugc", "sponsored"} {
		if strings.Contains(rel, bad) {
			return Link{}, false
		}
	}

	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return Link{}, false
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		// mailto:, javascript:, tel:, data: — none are outbound links.
		return Link{}, false
	}

	host := urlnorm.Host(u.String())
	if host == "" || host == selfHost || strings.HasSuffix(host, "."+selfHost) {
		return Link{}, false
	}
	if !opt.IncludeChrome && isChrome(host) {
		return Link{}, false
	}

	anchor := strings.ToLower(strings.Join(strings.Fields(text(n)), " "))
	if furnitureAnchors[anchor] {
		return Link{}, false
	}

	return Link{
		URL:    urlnorm.Norm(u.String()),
		Host:   host,
		Anchor: anchor,
		Count:  1,
	}, true
}

// isChrome matches the host or any parent domain, so "cdn.twitter.com" is
// excluded along with "twitter.com".
func isChrome(host string) bool {
	if chrome[host] {
		return true
	}
	for i := 0; i < len(host); i++ {
		if host[i] == '.' && chrome[host[i+1:]] {
			return true
		}
	}
	return false
}

// furnitureClasses mark a container as site chrome. Class-name matching is
// heuristic, but these names are near-universal across CMS themes.
var furnitureClasses = []string{
	"nav", "menu", "share", "sharing", "social", "related", "sidebar",
	"footer", "header", "breadcrumb", "pagination", "widget", "promo",
	"advert", "sponsor", "byline", "tags", "meta",
}

func hasFurnitureClass(n *html.Node) bool {
	c := strings.ToLower(attr(n, "class")) + " " + strings.ToLower(attr(n, "id"))
	if strings.TrimSpace(c) == "" {
		return false
	}
	for _, f := range furnitureClasses {
		if strings.Contains(c, f) {
			return true
		}
	}
	return false
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}

func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
