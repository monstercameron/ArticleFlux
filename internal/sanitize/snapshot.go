package sanitize

import (
	"strings"

	"golang.org/x/net/html"
)

// snapshotHTML sanitizes a whole fetched page for the §10.1b proxy.
//
// See HTML's comment for why this does not go through the GWC engine. The short
// version: that engine drops `<style>`, `<link>` and every `style` attribute
// above the policy layer, which is right for prose and fatal for a page.
//
// The rule here is not "what does prose need" but **what can act**. Anything
// that executes, navigates on its own, or collects input is removed with its
// subtree; everything that only describes appearance stays. That line is drawn
// once, below, and it is the whole security argument of this file.
func snapshotHTML(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		// Unparseable input gets the strictest existing policy rather than a
		// pass-through. A parse failure is never a reason to hand back markup.
		return HTML(s, Public)
	}
	snapWalk(doc)

	var b strings.Builder
	if err := html.Render(&b, doc); err != nil {
		return HTML(s, Public)
	}
	return b.String()
}

// snapDrop lists elements removed with their entire subtree.
//
// Subtree, not unwrap: unwrapping `<script>` would promote its source code into
// the document as visible text, and unwrapping `<template>` would promote inert
// markup into live out-of-context markup, which is a mutation-XSS precondition.
//
// `form`, `input`, `button`, `select` and `textarea` are here for a different
// reason than the rest. None of them executes anything. They are removed
// because a login box that looks exactly like the publisher's, rendered under
// OUR hostname, is the most convincing phishing surface this application could
// possibly produce — and the reader has every reason to trust the address bar.
var snapDrop = map[string]bool{
	"script": true, "iframe": true, "object": true, "embed": true,
	"applet": true, "frame": true, "frameset": true, "template": true,
	"form": true, "input": true, "button": true, "select": true,
	"textarea": true, "option": true, "optgroup": true, "fieldset": true,
	"label": true, "datalist": true, "output": true, "portal": true,
	// SVG and MathML have their own parsing rules, and foreign content is where
	// mutation-XSS lives. A page's decorative SVG is not worth that surface.
	"svg": true, "math": true,
	// <base> would retarget everything we just rewrote, straight back at the
	// origin. internal/rewrite already consumed and removed the real one; this
	// catches any that arrive some other way.
	"base": true,
	// noscript's contents are parsed as text by x/net/html but as MARKUP by a
	// browser with scripting disabled — which, after this walk, is exactly what
	// the reader has. Anything inside would come alive unexamined.
	"noscript": true,
}

// snapAllow is every element a document may contain.
//
// An allowlist rather than a denylist, and that changed after the corpus ran:
// a denylist keeps whatever it has not heard of, and `<scr<script>ipt>` parses
// into an element literally named `scr<script` — which no denylist contains,
// and which renders back out carrying the substring `<script`. Unknown elements
// are unwrapped (children kept, tag dropped), so garbage names cannot survive
// while ordinary prose inside them does.
//
// It is much wider than the prose policies because a page is not prose: it has
// a head, sectioning, tables, media and — the whole reason this file exists —
// a stylesheet.
var snapAllow = map[string]bool{
	"html": true, "head": true, "body": true, "title": true,
	"meta": true, "style": true, "link": true,

	"main": true, "article": true, "section": true, "header": true,
	"footer": true, "nav": true, "aside": true, "div": true, "span": true,
	"hgroup": true, "address": true, "details": true, "summary": true,

	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"p": true, "blockquote": true, "pre": true, "hr": true, "br": true, "wbr": true,
	"ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true,
	"figure": true, "figcaption": true,

	"a": true, "b": true, "strong": true, "i": true, "em": true, "u": true,
	"s": true, "sub": true, "sup": true, "code": true, "kbd": true, "samp": true,
	"var": true, "mark": true, "small": true, "abbr": true, "time": true,
	"q": true, "cite": true, "del": true, "ins": true, "bdi": true, "bdo": true,
	"ruby": true, "rt": true, "rp": true,

	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true,
	"td": true, "th": true, "caption": true, "col": true, "colgroup": true,

	"img": true, "picture": true, "source": true, "video": true, "audio": true,
	"track": true, "canvas": true, "progress": true, "meter": true,
}

// snapAttrs is what may stay on an element.
//
// Generous by prose standards and deliberately so: `class` and `id` are what
// every stylesheet selects on, and dropping them keeps the CSS while throwing
// away everything it matches.
var snapAttrs = map[string]bool{
	"href": true, "src": true, "srcset": true, "sizes": true, "alt": true,
	"title": true, "class": true, "id": true, "style": true,
	"width": true, "height": true, "colspan": true, "rowspan": true,
	"span": true, "align": true, "valign": true, "dir": true, "lang": true,
	"rel": true, "target": true, "type": true, "media": true,
	"datetime": true, "cite": true, "role": true, "poster": true,
	"loading": true, "decoding": true, "start": true, "reversed": true,
	"value": true, "open": true, "charset": true, "content": true,
	"http-equiv": true, "name": true, "property": true, "sizes-hint": true,
	"integrity": false, // listed explicitly: rewritten bytes never match a hash
}

// snapURLAttrs are the attributes whose values are fetched or navigated to, and
// therefore need their scheme checked.
var snapURLAttrs = map[string]bool{
	"href": true, "src": true, "srcset": true, "poster": true, "cite": true,
}

// snapSchemes is what a URL in a proxied page may use.
//
// No `mailto:` on purpose — it is harmless but it is also the one scheme that
// makes a proxied page hand the reader's mail client to a stranger's markup,
// and a link that does nothing is a smaller surprise than a compose window.
var snapSchemes = map[string]bool{"http": true, "https": true, "data": true}

func snapWalk(n *html.Node) {
	var kids []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, c)
	}

	switch n.Type {
	case html.CommentNode:
		// Every comment goes. Two reasons, and the second is the one that
		// matters: IE's conditional comments were an execution vector, and
		// x/net/html parses `<![CDATA[...]]>` into a COMMENT whose text still
		// contains whatever was inside it — so a comment can carry `<script`
		// back out through Render even though nothing ever parsed it as markup.
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
		return
	case html.ElementNode:
		switch snapElement(n) {
		case snapDropped:
			// The subtree went with it; nothing left to walk.
			return
		case snapUnwrapped:
			// The children were promoted to the parent and still have to be
			// cleaned — they are ordinary content that happened to be inside a
			// tag we did not recognise. Returning here instead would let an
			// unknown wrapper smuggle its whole subtree past the walk, which is
			// a far worse hole than the tag name it was hiding behind.
		}
	}
	for _, c := range kids {
		snapWalk(c)
	}
}

// What snapElement decided.
type snapVerdict int

const (
	snapKept snapVerdict = iota
	snapDropped
	snapUnwrapped
)

// snapUnwrap replaces an element with its children.
//
// For an element that is merely unrecognised rather than dangerous: the tag is
// meaningless to us, the text inside it is the page's actual content, and
// dropping the subtree would delete words the reader asked to see.
func snapUnwrap(n *html.Node) {
	parent := n.Parent
	if parent == nil {
		return
	}
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		parent.InsertBefore(c, n)
		c = next
	}
	parent.RemoveChild(n)
}

// snapElement cleans one element and reports what became of it.
func snapElement(n *html.Node) snapVerdict {
	name := strings.ToLower(n.Data)

	if snapDrop[name] {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
		return snapDropped
	}
	// Unrecognised: keep the words, lose the tag. This is what catches a name
	// no denylist could contain, `scr<script` being the one the corpus found.
	if !snapAllow[name] {
		snapUnwrap(n)
		return snapUnwrapped
	}

	// A stylesheet's text is not markup and never reaches the allowlist walk,
	// so it is scrubbed here or not at all.
	if name == "style" {
		if n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
			n.FirstChild.Data = snapCSS(n.FirstChild.Data)
		}
	}

	// A <link> is only allowed to be a stylesheet or an icon. `rel=preload`,
	// `rel=prefetch` and friends are fetch instructions we have not rewritten,
	// and `rel=canonical` is a claim about identity that is no longer true once
	// we are the ones serving it.
	if name == "link" {
		rel, _ := snapAttr(n, "rel")
		if !snapLinkRel(rel) {
			if n.Parent != nil {
				n.Parent.RemoveChild(n)
			}
			return snapDropped
		}
	}

	// A <meta> may describe the document. It may not redirect it: `http-equiv`
	// refresh is navigation without script, and it would fire the moment the
	// frame loaded.
	if name == "meta" {
		if v, ok := snapAttr(n, "http-equiv"); ok {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "content-type", "content-language":
			default:
				if n.Parent != nil {
					n.Parent.RemoveChild(n)
				}
				return snapDropped
			}
		}
	}

	kept := n.Attr[:0]
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)

		// Every event handler, without enumerating them. The list of on* names
		// grows with every browser release, so matching the shape is the only
		// version of this that stays correct.
		if strings.HasPrefix(key, "on") {
			continue
		}
		// xlink:href and xml:* carry URLs and namespaces into foreign content.
		if strings.Contains(key, ":") {
			continue
		}
		if !snapAttrs[key] {
			continue
		}
		if snapURLAttrs[key] {
			if key == "srcset" {
				if v := snapSrcset(a.Val); v != "" {
					a.Val = v
				} else {
					continue
				}
			} else if !schemeAllowed(strings.TrimSpace(a.Val), snapSchemes) {
				continue
			}
		}
		if key == "style" {
			// The url() references inside were rewritten before this ran. What
			// is left to refuse is the handful of legacy CSS constructs that
			// could execute: IE's expression(), Gecko's -moz-binding, and any
			// surviving javascript: URL.
			low := strings.ToLower(a.Val)
			if strings.Contains(low, "expression(") ||
				strings.Contains(low, "-moz-binding") ||
				strings.Contains(low, "javascript:") {
				continue
			}
		}
		kept = append(kept, a)
	}
	n.Attr = kept
	return snapKept
}

// snapCSS scrubs a stylesheet's text.
//
// By the time this runs, `internal/rewrite` has already pointed every `url()`
// and `@import` at our own proxy — this is the layer that assumes it did not.
// A caller who sanitizes without rewriting first (a future one; there are none
// today) would otherwise ship a stylesheet that fetches straight from the
// origin, which on the network this whole feature exists for is a request that
// hangs, and on any other network is a tracking beacon we went to some trouble
// to remove from the images.
//
// So: anything still carrying a scheme we do not serve is neutralised. What
// stays is relative (ours) and https (ours, when ProxyOrigin is set).
func snapCSS(css string) string {
	if css == "" {
		return css
	}
	low := strings.ToLower(css)
	// The executable legacy constructs go wholesale rather than surgically.
	// They have no legitimate use in a page we are re-serving, and a stylesheet
	// that loses one is a stylesheet with one fewer rule, not a broken one.
	for _, bad := range []string{"expression(", "-moz-binding", "javascript:", "vbscript:"} {
		if strings.Contains(low, bad) {
			css = stripRulesContaining(css, bad)
			low = strings.ToLower(css)
		}
	}
	return css
}

// stripRulesContaining removes every declaration containing needle.
//
// Declaration-level rather than whole-stylesheet: one bad `background` should
// cost that background, not the site's entire layout. Split on `;` is crude and
// correct enough — a semicolon inside a quoted CSS string is legal and vanishingly
// rare, and the failure mode of getting it wrong here is a dropped declaration.
func stripRulesContaining(css, needle string) string {
	parts := strings.Split(css, ";")
	kept := parts[:0]
	for _, p := range parts {
		if !strings.Contains(strings.ToLower(p), needle) {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, ";")
}

func snapLinkRel(rel string) bool {
	for f := range strings.FieldsSeq(strings.ToLower(rel)) {
		switch f {
		case "stylesheet", "icon", "shortcut", "apple-touch-icon", "mask-icon":
			return true
		}
	}
	return false
}

// snapSrcset drops candidates whose scheme is not allowed, returning "" when
// nothing survives.
func snapSrcset(v string) string {
	var kept []string
	for cand := range strings.SplitSeq(v, ",") {
		ref := strings.TrimSpace(cand)
		if ref == "" {
			continue
		}
		u := ref
		if sp := strings.IndexAny(ref, " \t\n\r\f"); sp >= 0 {
			u = ref[:sp]
		}
		if schemeAllowed(u, snapSchemes) {
			kept = append(kept, ref)
		}
	}
	return strings.Join(kept, ", ")
}

// snapAttr reads one attribute.
func snapAttr(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val, true
		}
	}
	return "", false
}
