// Package rewrite points a page's subresources somewhere else.
//
// It exists for the §10.1 proxy tiers. An article whose `<img src>` points at
// the publisher is an article whose images fail on a network that blocks the
// publisher: the *browser* resolves those URLs, from the reader's network, not
// from ours. The server already fetched the text successfully — rewriting the
// subresources to our own origin is what makes the rest of the page arrive by
// the same route. Tier 1a, tier 2 and tier 2r all need exactly this operation,
// which is why it is one package and not three.
//
// Pure by construction: bytes in, bytes out, no network, no database, and no
// opinion about what the replacement URL means. The caller supplies a function
// from absolute URL to replacement; this package's job is knowing every place a
// URL can hide.
//
// # The long tail is the whole point
//
// Everyone remembers `img@src`. The bugs live in the rest of it: `srcset`'s
// descriptor syntax, `url()` inside a background shorthand inside a `<style>`
// block, `@import` two levels deep in a fetched stylesheet, and `<base href>`
// quietly changing what every relative URL on the page resolves to. A rewriter
// that handles the first case and misses the others looks like it works and
// leaks every image to the origin — which, on a blocked network, is a broken
// article, and on any network is a per-reader tracking beacon.
package rewrite

import (
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Rewriter maps an absolute URL to its replacement.
//
// Returning "" means "leave this one alone", which is how a caller declines a
// scheme it cannot serve and how it stays idempotent — a URL that already
// points at the proxy must come back unchanged, or a second pass doubles the
// prefix.
type Rewriter func(absURL string) string

// Options configure a rewrite.
type Options struct {
	// Base resolves relative URLs. Required: without it a relative `src` cannot
	// be turned into anything a proxy could fetch, and silently skipping those
	// would leave exactly the URLs a feed is most likely to contain.
	Base *url.URL

	// Asset rewrites images, media, stylesheets and fonts — everything the
	// browser fetches on its own without being clicked. Nil disables it.
	Asset Rewriter

	// Link rewrites `<a href>`. Nil leaves links pointing at the origin, which
	// is the right default for tier 1a: the reader is reading our copy of the
	// article, and a link is a deliberate act of navigation. Tier 2 sets it so
	// clicking through stays inside the proxy.
	Link Rewriter

	// DropIntegrity removes `integrity` attributes. Proxying changes the bytes'
	// URL but not the bytes; what breaks SRI is that a *rewritten stylesheet*
	// genuinely no longer matches its hash. Left on, every proxied stylesheet
	// with SRI fails closed and the page renders unstyled.
	DropIntegrity bool

	// DropMetaCSP removes `<meta http-equiv="Content-Security-Policy">`. A
	// page's own CSP is written for the page's own origin and, once served from
	// ours, usually forbids the very assets we just rewrote.
	DropMetaCSP bool
}

// HTML rewrites every subresource URL in a document or fragment.
//
// Fragments are detected rather than configured: feed content arrives as a run
// of `<p>` and article extraction produces the same, while tiers 2 and 2r hand
// over whole documents. Parsing a fragment as a document silently wraps it in
// `<html><body>`, and rendering that back out injects markup the caller never
// had.
func HTML(doc string, opt Options) (string, error) {
	if opt.Base == nil {
		return "", fmt.Errorf("rewrite: Base is required")
	}

	if isDocument(doc) {
		root, err := html.Parse(strings.NewReader(doc))
		if err != nil {
			return "", fmt.Errorf("rewrite: %w", err)
		}
		base := effectiveBase(root, opt.Base)
		walk(root, opt, base)
		var b strings.Builder
		if err := html.Render(&b, root); err != nil {
			return "", fmt.Errorf("rewrite: %w", err)
		}
		return b.String(), nil
	}

	// DataAtom must be set as well as Data: ParseFragment rejects a context
	// node whose two halves disagree, and "body" without atom.Body is exactly
	// that disagreement.
	body := &html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"}
	nodes, err := html.ParseFragment(strings.NewReader(doc), body)
	if err != nil {
		return "", fmt.Errorf("rewrite: %w", err)
	}
	var b strings.Builder
	for _, n := range nodes {
		// A <base> inside a fragment is not something a feed should be able to
		// do — it would retarget relative URLs for the whole reading pane, not
		// just its own item. effectiveBase is deliberately not consulted here,
		// and element() drops the tag outright.
		//
		// The return value matters at this level and only at this level: a
		// top-level fragment node has no parent to be removed from, so a node
		// element() rejected is dropped by not rendering it.
		if !walk(n, opt, opt.Base) {
			continue
		}
		if err := html.Render(&b, n); err != nil {
			return "", fmt.Errorf("rewrite: %w", err)
		}
	}
	return b.String(), nil
}

// isDocument reports whether the input is a whole page rather than a fragment.
func isDocument(s string) bool {
	head := s
	if len(head) > 1024 {
		head = head[:1024]
	}
	head = strings.ToLower(head)
	return strings.Contains(head, "<!doctype") ||
		strings.Contains(head, "<html") ||
		strings.Contains(head, "<head") ||
		strings.Contains(head, "<body")
}

// effectiveBase applies a document's own <base href> and then removes it.
//
// Both halves matter. Honouring it is correctness — a page that declares
// `<base href="https://cdn.example/">` means every relative URL on it resolves
// there, and ignoring that rewrites them all to the wrong host. Removing it is
// also correctness: once the URLs are absolute and pointed at the proxy, a
// surviving <base> would retarget anything we missed straight back at the
// origin, which is the failure this package exists to prevent.
func effectiveBase(root *html.Node, fallback *url.URL) *url.URL {
	var found *url.URL
	var node *html.Node
	var scan func(*html.Node)
	scan = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "base" {
			if href, ok := attr(n, "href"); ok {
				if u, err := fallback.Parse(strings.TrimSpace(href)); err == nil {
					// one() only ever proxies http(s); a base that resolves to
					// any other scheme (or to no scheme at all resolving into
					// something non-http) can never produce a rewritable
					// absolute URL. Adopting it anyway does not just leave
					// that one attribute alone — it replaces `fallback`, the
					// one base capable of resolving relative URLs at all, so
					// every relative URL on the page (or after this point in
					// scan order) silently stops resolving. Falling through
					// to keep scanning (rather than stopping at the first
					// <base> found) mirrors HTML's own rule that only the
					// first <base href> counts, while still letting a later,
					// well-formed one be found if this one is bogus.
					switch strings.ToLower(u.Scheme) {
					case "http", "https":
						found, node = u, n
						return
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			scan(c)
		}
	}
	scan(root)
	if found == nil {
		return fallback
	}
	if node.Parent != nil {
		node.Parent.RemoveChild(node)
	}
	return found
}

// urlAttrs is every attribute that names a subresource the browser fetches
// without being asked.
//
// Three deliberate absences. `a@href` is navigation, and goes through
// Options.Link. `iframe@src`, `embed@src` and `object@data` are nested
// *documents*, not assets — an asset proxy that fetched one would hand back
// HTML from an endpoint whose content-type allowlist is images, which is a
// broken frame dressed up as a working feature; they belong to tier 2, which
// proxies documents on purpose. And `img@longdesc` is a link to a description
// page, not an image.
var urlAttrs = map[string][]string{
	"img":    {"src"},
	"source": {"src"},
	"video":  {"src", "poster"},
	"audio":  {"src"},
	"track":  {"src"},
	"input":  {"src"},
}

// walk rewrites a subtree, reporting whether the node itself survived.
func walk(n *html.Node, opt Options, base *url.URL) bool {
	// Children are collected first: the CSP and <base> cases remove nodes, and
	// mutating the sibling chain while iterating it skips whatever follows.
	var kids []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, c)
	}

	if n.Type == html.ElementNode {
		if !element(n, opt, base) {
			return false // dropped, and its children went with it
		}
	}

	for _, c := range kids {
		walk(c, opt, base)
	}
	return true
}

// element rewrites one node, reporting whether it survived.
func element(n *html.Node, opt Options, base *url.URL) bool {
	switch n.Data {
	case "base":
		// Always dropped, never honoured here. In a document effectiveBase has
		// already read and removed it; anything reaching this point is either a
		// second <base> (which the HTML spec ignores anyway) or one inside a
		// fragment, where honouring it would let a single feed item retarget
		// every relative URL in the reading pane.
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
		return false
	case "meta":
		if opt.DropMetaCSP {
			if v, ok := attr(n, "http-equiv"); ok &&
				strings.EqualFold(strings.TrimSpace(v), "content-security-policy") {
				if n.Parent != nil {
					n.Parent.RemoveChild(n)
				}
				return false
			}
		}
	case "link":
		// Only stylesheets and the icon family are subresources. `link
		// rel=canonical` and `rel=alternate` are metadata pointing at the
		// origin, and proxying them would be a lie about where this page lives.
		if rel, _ := attr(n, "rel"); isFetchedLinkRel(rel) {
			setURLAttr(n, "href", opt.Asset, base)
		}
		if opt.DropIntegrity {
			delAttr(n, "integrity")
		}
	case "script":
		// Scripts are dropped by the sanitize policy, not here — this package
		// has no security opinion. The integrity attribute still goes, so that
		// a caller who keeps scripts is not left with a hash over rewritten
		// bytes.
		if opt.DropIntegrity {
			delAttr(n, "integrity")
		}
	case "style":
		if opt.Asset != nil {
			if n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
				n.FirstChild.Data = CSS(n.FirstChild.Data, opt.Asset, base)
			}
		}
	case "a", "area":
		if opt.Link != nil {
			setURLAttr(n, "href", opt.Link, base)
		}
	}

	if attrs, ok := urlAttrs[n.Data]; ok {
		for _, a := range attrs {
			setURLAttr(n, a, opt.Asset, base)
		}
	}
	// srcset lives on both img and source and is the single most commonly
	// missed one, because it is a list rather than a URL.
	if n.Data == "img" || n.Data == "source" {
		setSrcset(n, opt.Asset, base)
	}
	// An inline style attribute can carry url() just as a <style> block can.
	if v, ok := attr(n, "style"); ok && opt.Asset != nil {
		setAttr(n, "style", CSS(v, opt.Asset, base))
	}
	return true
}

func isFetchedLinkRel(rel string) bool {
	for f := range strings.FieldsSeq(strings.ToLower(rel)) {
		switch f {
		case "stylesheet", "icon", "shortcut", "apple-touch-icon", "preload", "mask-icon":
			return true
		}
	}
	return false
}

// setURLAttr resolves and rewrites one attribute in place.
func setURLAttr(n *html.Node, name string, rw Rewriter, base *url.URL) {
	if rw == nil {
		return
	}
	raw, ok := attr(n, name)
	if !ok {
		return
	}
	if out := one(raw, rw, base); out != "" {
		setAttr(n, name, out)
	}
}

// one resolves a single reference and asks the rewriter for a replacement,
// returning "" when nothing should change.
func one(raw string, rw Rewriter, base *url.URL) string {
	ref := strings.TrimSpace(raw)
	if ref == "" || strings.HasPrefix(ref, "#") || !fetchable(ref) {
		return ""
	}
	abs, err := base.Parse(ref)
	if err != nil {
		return ""
	}
	// Only http(s) can be proxied. Anything else that survived fetchable() —
	// there should be none — is left exactly as the publisher wrote it.
	switch strings.ToLower(abs.Scheme) {
	case "http", "https":
	default:
		return ""
	}
	return rw(abs.String())
}

// fetchable rejects the schemes that are not network fetches at all.
//
// `data:` is the interesting one: it is already inline, proxying it would be
// strictly more bytes over two hops, and a long base64 image would blow the
// length of any URL we minted for it.
func fetchable(ref string) bool {
	i := strings.IndexByte(ref, ':')
	if i < 0 {
		return true // relative
	}
	// A colon inside a path segment is not a scheme: "foo:bar/baz" is relative
	// unless the part before the colon is a valid scheme, and a slash or
	// question mark before it settles the question.
	if j := strings.IndexAny(ref, "/?#"); j >= 0 && j < i {
		return true
	}
	switch strings.ToLower(ref[:i]) {
	case "http", "https":
		return true
	default:
		return false
	}
}

// setSrcset rewrites a srcset attribute, preserving its descriptors.
func setSrcset(n *html.Node, rw Rewriter, base *url.URL) {
	if rw == nil {
		return
	}
	raw, ok := attr(n, "srcset")
	if !ok {
		return
	}
	if out := Srcset(raw, rw, base); out != raw {
		setAttr(n, "srcset", out)
	}
}

// Srcset rewrites every candidate in a srcset attribute.
//
// The syntax is "URL descriptor, URL descriptor" where the descriptor is
// optional — so a naive `strings.Split(s, ",")` produces candidates whose URL
// half still has "2x" welded to it, and a naive `strings.Fields` loses the
// association between a URL and its descriptor entirely. Both mistakes render
// as "the image is missing at exactly one screen density", which nobody
// notices until it is the density they use.
func Srcset(raw string, rw Rewriter, base *url.URL) string {
	var out []string
	for _, cand := range splitSrcset(raw) {
		ref, desc := cand.url, cand.desc
		if rep := one(ref, rw, base); rep != "" {
			ref = rep
		}
		if desc != "" {
			out = append(out, ref+" "+desc)
		} else {
			out = append(out, ref)
		}
	}
	if len(out) == 0 {
		return raw
	}
	return strings.Join(out, ", ")
}

type candidate struct{ url, desc string }

// splitSrcset implements the WHATWG "parse a srcset attribute" shape.
//
// The subtlety worth keeping: a comma is only a separator when it is *trailing*
// the URL. `a.png,b.png` is therefore ONE candidate whose URL contains a comma —
// which looks like a bug and is what browsers do, so it is what we do. Getting
// this wrong in the other direction is worse than useless: we would rewrite two
// URLs the browser never asked for and skip the one it did.
func splitSrcset(raw string) []candidate {
	var out []candidate
	i, n := 0, len(raw)
	for i < n {
		for i < n && (isSpace(raw[i]) || raw[i] == ',') {
			i++
		}
		if i >= n {
			break
		}
		start := i
		for i < n && !isSpace(raw[i]) {
			i++
		}
		tok := raw[start:i]

		// Trailing commas end the candidate outright: there is no descriptor,
		// and whatever follows is the next URL rather than this one's width.
		if strings.HasSuffix(tok, ",") {
			if u := strings.TrimRight(tok, ","); u != "" {
				out = append(out, candidate{u, ""})
			}
			continue
		}

		for i < n && isSpace(raw[i]) {
			i++
		}
		start = i
		for i < n && raw[i] != ',' {
			i++
		}
		d := strings.TrimSpace(raw[start:i])
		if i < n {
			i++ // the comma
		}
		if tok != "" {
			out = append(out, candidate{tok, d})
		}
	}
	return out
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

// CSS rewrites every url() and @import in a stylesheet or style attribute.
//
// Deliberately a scanner rather than a parser. A real CSS parser would be the
// right tool for editing stylesheets; here the only question is where the URLs
// are, and a scanner cannot be defeated by a property it has never heard of —
// which matters, because the whole risk is a URL hiding in a shorthand nobody
// thought to enumerate.
func CSS(text string, rw Rewriter, base *url.URL) string {
	if rw == nil || text == "" {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))

	i, n := 0, len(text)
	for i < n {
		switch {
		case strings.HasPrefix(text[i:], "/*"):
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				b.WriteString(text[i:])
				return b.String()
			}
			b.WriteString(text[i : i+2+end+2])
			i += 2 + end + 2

		case hasFoldPrefix(text[i:], "url("):
			j := i + 4
			for j < n && isSpace(text[j]) {
				j++
			}
			q := byte(0)
			if j < n && (text[j] == '"' || text[j] == '\'') {
				q = text[j]
				j++
			}
			start := j
			for j < n && ((q != 0 && text[j] != q) || (q == 0 && text[j] != ')')) {
				j++
			}
			// j == n means the quote (if any) or, failing that, the ')'
			// never appeared before the text ran out: this is not a
			// complete url() token, just a prefix of one, and there is no
			// ')' anywhere in the remaining input to anchor a rewrite to.
			// Emitting the rest of the text unchanged and stopping —
			// exactly like the unterminated-comment case above — is what
			// keeps a second pass a no-op: manufacturing a ")" here would
			// turn an incomplete token into a well-formed one that the next
			// pass could then rewrite a second time (the bug this guards).
			if j >= n {
				b.WriteString(text[i:])
				return b.String()
			}
			ref := strings.TrimSpace(text[start:j])
			if q != 0 {
				j++ // closing quote
			}
			for j < n && isSpace(text[j]) {
				j++
			}
			// A quote that closed but is not followed by ')' before the text
			// ends (e.g. `url("x" ` or `url("x" foo`) is just as incomplete
			// as the unquoted case above, and gets the same treatment: leave
			// it untouched rather than inventing the missing punctuation.
			if j >= n || text[j] != ')' {
				b.WriteString(text[i:])
				return b.String()
			}
			j++ // the ')'
			b.WriteString("url(")
			b.WriteString(quoteCSS(replaceOr(ref, rw, base)))
			b.WriteString(")")
			i = j

		case hasFoldPrefix(text[i:], "@import"):
			j := i + len("@import")
			b.WriteString(text[i:j])
			for j < n && isSpace(text[j]) {
				b.WriteByte(text[j])
				j++
			}
			// `@import url(...)` falls through to the url() case on the next
			// iteration; only the bare-string form is handled here.
			if j < n && (text[j] == '"' || text[j] == '\'') {
				q := text[j]
				j++
				start := j
				for j < n && text[j] != q {
					j++
				}
				ref := text[start:j]
				if j < n {
					j++
				}
				b.WriteString(quoteCSS(replaceOr(ref, rw, base)))
			}
			i = j

		default:
			b.WriteByte(text[i])
			i++
		}
	}
	return b.String()
}

// replaceOr returns the rewritten URL, or the original when the rewriter
// declines. Keeping the original is what makes a second pass a no-op.
func replaceOr(ref string, rw Rewriter, base *url.URL) string {
	if rep := one(ref, rw, base); rep != "" {
		return rep
	}
	return ref
}

// quoteCSS always quotes. An unquoted url() token cannot contain parentheses,
// whitespace or quotes, and a proxy URL carries a base64 payload and a
// signature — quoting once here is cheaper than reasoning about which
// replacements happen to be safe bare.
func quoteCSS(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func hasFoldPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func attr(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}

func setAttr(n *html.Node, name, val string) {
	for i := range n.Attr {
		if n.Attr[i].Key == name {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: name, Val: val})
}

func delAttr(n *html.Node, name string) {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Key != name {
			out = append(out, a)
		}
	}
	n.Attr = out
}
