package smart

import (
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// distill turns a page into the smallest thing a model can answer a structural
// question from.
//
// It exists for three reasons, in this order:
//
//  1. **Egress.** This is what leaves the machine (§18.8). An outline that has
//     dropped the prose has dropped almost everything a page is ABOUT while
//     keeping everything the question is about, which is the right trade to make
//     by default rather than one to leave to a prompt.
//  2. **Cost.** A blog index is 300-800 KB of HTML, most of it inline SVG,
//     JSON-LD, tracking script and framework attributes. The outline is 6-12 KB.
//  3. **Accuracy.** The signal for "this is the list of posts" is a REPEATED
//     STRUCTURE, and repetition is invisible in a wall of markup and obvious in
//     an outline that says "x 11 more". Collapsing siblings does not just save
//     bytes — it is the answer, written down.
//
// The output is deliberately not HTML. It cannot be pasted back into a browser,
// which removes any temptation to treat a model's echo of it as content.

const (
	// maxOutlineBytes caps what is sent. Reached only by genuinely enormous
	// pages, and the top of a page is where an index lives, so truncation costs
	// the footer rather than the answer.
	maxOutlineBytes = 48 << 10
	// maxDepth stops the walk. Beyond this is layout scaffolding — a div nested
	// eighteen deep is a grid cell, not a semantic container worth selecting.
	maxDepth = 16
	// sampleRuns is how many of a repeated block are shown before collapsing.
	// Two is enough to see that it repeats and that the second one differs.
	sampleRuns = 2
	// maxTextSample is how much text a leaf contributes. Long enough to tell a
	// headline from a byline, short enough that a page of prose does not
	// reconstitute itself in the outline.
	maxTextSample = 70
	// maxAttrValue truncates hrefs and srcs. A URL's shape is informative; its
	// query string is not.
	maxAttrValue = 60
)

// skipTags never appear in the outline. Script and style are the bulk of a
// modern page and carry nothing about structure; svg is thousands of path
// elements describing an icon.
var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "svg": true,
	"link": true, "meta": true, "template": true, "iframe": true, "path": true,
}

// keptAttrs are the attributes that help identify a selector target. Everything
// else — data-*, aria-*, framework hydration ids, inline styles — is noise at
// best and a fingerprint at worst.
var keptAttrs = map[string]bool{
	"href": true, "src": true, "datetime": true, "title": true,
	"itemprop": true, "property": true, "rel": true, "type": true,
}

func distill(pageHTML string) string {
	doc, err := html.Parse(strings.NewReader(pageHTML))
	if err != nil {
		return ""
	}
	var b strings.Builder
	body := findBody(doc)
	if body == nil {
		body = doc
	}
	writeNode(&b, body, 0)
	return b.String()
}

func findBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.Data == "body" {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findBody(c); got != nil {
			return got
		}
	}
	return nil
}

// writeNode emits one element and recurses, collapsing runs of siblings that
// share a signature.
func writeNode(b *strings.Builder, n *html.Node, depth int) {
	if b.Len() > maxOutlineBytes || depth > maxDepth {
		return
	}
	for c := n.FirstChild; c != nil; {
		if c.Type == html.TextNode {
			if s := sample(c.Data); s != "" {
				indent(b, depth)
				b.WriteString(`"` + s + `"` + "\n")
			}
			c = c.NextSibling
			continue
		}
		if c.Type != html.ElementNode || skipTags[c.Data] {
			c = c.NextSibling
			continue
		}

		// How many siblings from here share this element's signature. Counted
		// across the whole remaining run rather than pairwise, so a list of
		// twelve posts is "two shown, ten collapsed" and not six pairs.
		sig := signature(c)
		run := 1
		for s := c.NextSibling; s != nil; s = s.NextSibling {
			if s.Type == html.TextNode && strings.TrimSpace(s.Data) == "" {
				continue
			}
			if s.Type != html.ElementNode || signature(s) != sig {
				break
			}
			run++
		}

		shown := 0
		next := c
		for s := c; s != nil && shown < run; s = s.NextSibling {
			if s.Type != html.ElementNode || signature(s) != sig {
				continue
			}
			if shown < sampleRuns {
				indent(b, depth)
				b.WriteString(describe(s))
				b.WriteByte('\n')
				writeNode(b, s, depth+1)
			}
			shown++
			next = s.NextSibling
		}
		if run > sampleRuns {
			indent(b, depth)
			b.WriteString("… x " + strconv.Itoa(run-sampleRuns) + " more " + sig + "\n")
		}
		c = next
	}
}

// describe renders one element as `tag#id.class.class[attr=value]`.
func describe(n *html.Node) string {
	var b strings.Builder
	b.WriteString(n.Data)

	var classes []string
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "id":
			if v := strings.TrimSpace(a.Val); v != "" {
				b.WriteString("#" + v)
			}
		case "class":
			classes = strings.Fields(a.Val)
		}
	}
	// Three classes is enough to write a selector with; a utility-CSS page
	// carries forty per element and none of them mean anything.
	if len(classes) > 3 {
		classes = classes[:3]
	}
	for _, c := range classes {
		b.WriteString("." + c)
	}

	var attrs []string
	for _, a := range n.Attr {
		k := strings.ToLower(a.Key)
		if !keptAttrs[k] {
			continue
		}
		v := strings.TrimSpace(a.Val)
		if v == "" {
			continue
		}
		if len(v) > maxAttrValue {
			v = v[:maxAttrValue] + "…"
		}
		attrs = append(attrs, k+"="+v)
	}
	sort.Strings(attrs)
	if len(attrs) > 0 {
		b.WriteString("[" + strings.Join(attrs, " ") + "]")
	}
	return b.String()
}

// signature is what makes two siblings "the same kind of thing": the tag and its
// classes, without ids or attribute values. Two article cards differ in every
// href and every headline and are structurally identical, which is exactly the
// equivalence a repeated-block detector needs.
func signature(n *html.Node) string {
	var classes []string
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, "class") {
			classes = strings.Fields(a.Val)
		}
	}
	if len(classes) > 3 {
		classes = classes[:3]
	}
	if len(classes) == 0 {
		return n.Data
	}
	return n.Data + "." + strings.Join(classes, ".")
}

func sample(text string) string {
	s := strings.Join(strings.Fields(text), " ")
	if s == "" {
		return ""
	}
	if len(s) > maxTextSample {
		s = s[:maxTextSample] + "…"
	}
	// Quotes would close the sample early in the outline's own syntax.
	return strings.ReplaceAll(s, `"`, "'")
}

func indent(b *strings.Builder, depth int) {
	for i := 0; i < depth && i < maxDepth; i++ {
		b.WriteString("  ")
	}
}

// Outline is distill, exported.
//
// It exists for two callers that are not the analyser: the pagescan tool, which
// prints what the model would receive so a proposal that missed the list can be
// diagnosed rather than guessed at, and — eventually — a rule editor that wants
// to show the same thing. Both need to see EXACTLY what is sent, which is why
// this is the same function rather than a second one that drifts.
func Outline(pageHTML string) string { return distill(pageHTML) }
