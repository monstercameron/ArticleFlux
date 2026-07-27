// Package sanitize wraps GWC's sanitizer with the named policies this
// application actually needs (TODO 2.9, plan.md §21).
//
// GWC's Sanitize is the engine and it is not reimplemented here: it owns the
// parse, the allowlist walk, the URL scheme check and the mutation-XSS
// hardening, and all of that is hard to get right once, let alone twice. What it
// does not have is an opinion about *where the HTML came from*, and that turns
// out to be the whole question.
//
// Four sources, four different threat models:
//
//	Feed        publisher markup. Images are content — a hardware review is
//	            mostly photographs. Formatting is content. Trust is "a stranger
//	            whose feed you chose to subscribe to".
//
//	Newsletter  the strictest, and the only one where a remote image is an
//	            attack rather than a picture. A tracking pixel in an email
//	            reports back that you opened it, when, how often, and from which
//	            IP — to a sender you may only have given an address to once. The
//	            reader is the thing standing between the two, so remote images
//	            are dropped outright rather than proxied: proxying still tells
//	            the sender the message was opened.
//
//	Archived    our own extraction output, already through readability. Trusted
//	            more, but not trusted — it is still publisher bytes that passed
//	            through a parser.
//
//	Public      a shared excerpt rendered for someone who never subscribed to
//	            anything. Least trust, least markup: text and emphasis only.
//
// A single policy tuned to sit between these would be too strict for feeds
// (dropping the photographs a review consists of) and too loose for newsletters
// (leaving the pixel in). That is the argument for naming them.
package sanitize

import (
	"strings"

	gwc "github.com/monstercameron/GoWebComponents/v5/sanitize"
	"golang.org/x/net/html"
)

// Policy names the trust context of a piece of HTML.
type Policy int

const (
	// Feed is publisher markup arriving over RSS/Atom.
	Feed Policy = iota
	// Newsletter is markup arriving by email. Remote images are dropped.
	Newsletter
	// Archived is our own extraction output.
	Archived
	// Public is an excerpt rendered for an unauthenticated viewer.
	Public
	// Note is the user's own text. Still sanitized — the user may paste.
	Note
)

func (p Policy) String() string {
	switch p {
	case Feed:
		return "feed"
	case Newsletter:
		return "newsletter"
	case Archived:
		return "archived"
	case Public:
		return "public"
	case Note:
		return "note"
	}
	return "unknown"
}

// set is a small helper so the policy tables below read as lists rather than as
// map literals with `: true` on every line.
func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

var (
	// Inline formatting every policy allows. Removing these does not make
	// anything safer; it only makes quoted text stop looking quoted.
	inlineTags = []string{"a", "b", "strong", "i", "em", "u", "s", "sub", "sup",
		"code", "br", "span", "mark", "small", "abbr", "time", "q", "cite"}

	blockTags = []string{"p", "div", "blockquote", "pre", "hr",
		"h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "dl", "dt", "dd"}

	tableTags = []string{"table", "thead", "tbody", "tfoot", "tr", "td", "th", "caption"}

	mediaTags = []string{"img", "figure", "figcaption", "picture"}
)

// policies is built once.
//
// Every entry allowlists "rel" and "target" even though neither comes from the
// publisher: hardenLink discards whatever they sent and writes its own values.
// They have to be on the allowlist anyway, because the GWC walk runs afterwards
// and would otherwise strip the hardening a moment after it was applied. Policy values are documented as immutable and shared
// across goroutines, so they are constructed at init and never mutated.
var policies = map[Policy]gwc.Policy{
	Feed: {
		AllowedTags:       set(append(append(append(append([]string{}, inlineTags...), blockTags...), tableTags...), mediaTags...)...),
		AllowedAttributes: set("href", "src", "alt", "title", "colspan", "rowspan", "datetime", "cite", "width", "height", "rel", "target"),
		AllowedURLSchemes: set("http", "https", "mailto"),
	},
	Newsletter: {
		// No media tags at all. See the package comment: in email a remote
		// image is a read receipt, and the only reliable way not to send one is
		// not to make the request.
		AllowedTags:       set(append(append(append([]string{}, inlineTags...), blockTags...), tableTags...)...),
		AllowedAttributes: set("href", "alt", "title", "colspan", "rowspan", "datetime", "rel", "target"),
		AllowedURLSchemes: set("http", "https", "mailto"),
	},
	Archived: {
		AllowedTags:       set(append(append(append(append([]string{}, inlineTags...), blockTags...), tableTags...), mediaTags...)...),
		AllowedAttributes: set("href", "src", "alt", "title", "colspan", "rowspan", "datetime", "width", "height", "rel", "target"),
		AllowedURLSchemes: set("http", "https", "mailto"),
	},
	Public: {
		// Text and emphasis. An excerpt shown to a stranger does not need
		// tables, and every element not on this list is one fewer thing that can
		// be made to behave unexpectedly in a context we do not control.
		AllowedTags:       set("p", "br", "b", "strong", "i", "em", "u", "code", "blockquote", "a"),
		AllowedAttributes: set("href", "title", "rel", "target"),
		AllowedURLSchemes: set("http", "https"),
	},
	Note: {
		AllowedTags:       set(append(append([]string{}, inlineTags...), blockTags...)...),
		AllowedAttributes: set("href", "title", "rel", "target"),
		AllowedURLSchemes: set("http", "https", "mailto"),
	},
}

// HTML sanitizes s under the named policy.
//
// The result is safe to render. "Safe" here means the GWC guarantee — no script
// execution vector survives — plus this package's additions: link hardening on
// every policy, and tracking-pixel removal on Newsletter and Feed.
func HTML(s string, p Policy) string {
	pol, ok := policies[p]
	if !ok {
		// An unmapped policy value is a programming error, and the safe way to
		// fail is closed. Public is the most restrictive policy there is.
		pol = policies[Public]
	}

	// Pre-pass on the parse tree, before the allowlist walk. The allowlist
	// cannot express "an img whose dimensions are 1x1" or "a link that must gain
	// rel=noopener" — those are decisions about values, not about names.
	pre, err := prepare(s, p)
	if err != nil {
		// Unparseable input: sanitize the raw string anyway rather than
		// returning it. A parse failure is not a reason to hand the caller
		// something unsafe.
		return pol.Sanitize(s)
	}
	return pol.Sanitize(pre)
}

// Text strips all markup and returns readable plain text.
//
// Used for list rows, search indexing, word counts and TTS. This output is never
// rendered as HTML, which is why it is allowed to be this blunt.
func Text(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return strings.Join(strings.Fields(s), " ")
	}
	var b strings.Builder
	collectText(doc, &b)
	return strings.Join(strings.Fields(b.String()), " ")
}

func collectText(n *html.Node, b *strings.Builder) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script", "style", "template", "noscript":
			return
		}
	}
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectText(c, b)
		// Block boundaries become spaces, or "</p><p>" welds two words together
		// and the word count is wrong in a way nobody ever notices.
		if c.Type == html.ElementNode && isBlock(c.Data) {
			b.WriteString(" ")
		}
	}
}

func isBlock(tag string) bool {
	switch tag {
	case "p", "div", "br", "li", "tr", "td", "th", "h1", "h2", "h3", "h4", "h5", "h6",
		"blockquote", "pre", "section", "article", "header", "footer", "figcaption":
		return true
	}
	return false
}

// prepare runs the value-dependent edits the allowlist cannot express.
func prepare(s string, p Policy) (string, error) {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return "", err
	}
	walk(doc, p)
	var b strings.Builder
	if err := html.Render(&b, doc); err != nil {
		return "", err
	}
	return b.String(), nil
}

func walk(n *html.Node, p Policy) {
	// Collect first: harden may remove n's children, and mutating a sibling
	// chain while iterating it skips nodes.
	var kids []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, c)
	}
	for _, c := range kids {
		walk(c, p)
	}
	if n.Type == html.ElementNode {
		harden(n, p)
	}
}

func harden(n *html.Node, p Policy) {
	switch n.Data {
	case "a":
		hardenLink(n)
	case "img":
		if isTrackingPixel(n) && n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
	}
}

// hardenLink makes every link safe to click and quiet to follow.
//
// target=_blank without rel=noopener hands the opened page a window.opener
// handle to the tab it came from, which is enough to navigate this application
// somewhere else — a phishing primitive that costs the attacker one attribute in
// their own feed. noreferrer additionally stops the reader from telling every
// publisher which article the reader was on.
func hardenLink(n *html.Node) {
	var rel, target string
	out := n.Attr[:0]
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "rel":
			rel = a.Val
			continue
		case "target":
			target = a.Val
			continue
		}
		out = append(out, a)
	}
	n.Attr = out

	if target == "" {
		target = "_blank"
	}
	n.Attr = append(n.Attr,
		html.Attribute{Key: "target", Val: target},
		html.Attribute{Key: "rel", Val: mergeRel(rel)},
	)
}

func mergeRel(existing string) string {
	need := []string{"noopener", "noreferrer"}
	have := strings.Fields(strings.ToLower(existing))
	for _, n := range need {
		found := false
		for _, h := range have {
			if h == n {
				found = true
				break
			}
		}
		if !found {
			have = append(have, n)
		}
	}
	return strings.Join(have, " ")
}

// isTrackingPixel recognises an image that exists to report a read.
//
// The heuristic is deliberately narrow — width or height declared as 1 or 0, or
// a src whose path names itself. Anything cleverer starts deleting real images,
// and a reader that silently eats the photographs is a worse product than one
// that leaks an occasional pixel. The strong protection is Newsletter's, which
// drops <img> entirely; this is the backstop for the other policies.
func isTrackingPixel(n *html.Node) bool {
	var w, h, src string
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "width":
			w = strings.TrimSpace(a.Val)
		case "height":
			h = strings.TrimSpace(a.Val)
		case "src":
			src = strings.ToLower(a.Val)
		}
	}
	if (w == "1" || w == "0") && (h == "1" || h == "0") {
		return true
	}
	for _, marker := range []string{"/pixel.", "/track", "/open.gif", "/beacon", "spacer.gif", "1x1."} {
		if strings.Contains(src, marker) {
			return true
		}
	}
	return false
}
