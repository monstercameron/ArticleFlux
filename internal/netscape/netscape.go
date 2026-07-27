// Package netscape reads and writes the Netscape bookmark file format, and
// reads Chrome's JSON bookmarks (TODO 4.6, plan.md §15.7).
//
// # The format is not HTML, it only looks like it
//
// The Netscape bookmark file is thirty years old, has no specification, and is
// what every browser on earth imports. Its defining property is that the `<DT>`
// and `<P>` tags are never closed and the nesting is implied by document order
// rather than by the tree:
//
//	<DL><p>
//	    <DT><H3>Folder</H3>
//	    <DL><p>
//	        <DT><A HREF="...">Title</A>
//	    </DL><p>
//	    <DT><A HREF="...">Top level bookmark</A>
//	</DL><p>
//
// An HTML5 parser handles this correctly — it is exactly the kind of soup the
// spec's tree construction rules exist for — so this reads it with
// golang.org/x/net/html rather than with a regexp. Writing, on the other hand,
// deliberately emits the malformed shape: browsers parse it by convention rather
// than by specification, and a well-formed file with closed tags is a file some
// importers get wrong.
//
// # Why the export matters more than the import
//
// §15.7's rule is that data goes out in the format the destination actually
// accepts. An export nobody can import is a hostage note, and the acceptance bar
// for this package is that its output loads into a real browser — which is why
// the writer copies the exact byte shape browsers emit, down to the DOCTYPE
// comment and the non-standard "It will be read and overwritten" line.
package netscape

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Bookmark is one saved link.
type Bookmark struct {
	Title string
	URL   string
	// Folder is the path, e.g. []string{"Reading", "Go"}. Empty means top level.
	Folder []string
	// Tags come from the non-standard TAGS attribute, which Delicious
	// popularised and Pinboard, Raindrop and Firefox all still emit.
	Tags []string
	// AddedAt is zero when the file did not say.
	AddedAt time.Time
	// Description is the <DD> element that may follow an anchor.
	Description string
	// IconDataURL is the base64 favicon browsers embed. Kept on import so an
	// export round-trip does not silently strip icons, but never fetched.
	IconDataURL string
}

// Parse reads a Netscape bookmark file.
//
// Errors are returned only for input that is not a document at all. A bookmark
// file with three broken entries should yield the rest — someone exporting
// fifteen years of bookmarks from a dead service does not want the whole import
// refused over one malformed anchor.
func Parse(r io.Reader) ([]Bookmark, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("netscape: %w", err)
	}
	var out []Bookmark
	walk(doc, nil, &out)
	return out, nil
}

// walk descends the parsed tree, carrying the folder path down.
//
// The path is copied at each level rather than appended in place. Appending
// would let two sibling folders share a backing array, and the second one would
// overwrite the first's name in every bookmark already collected — a bug that
// only appears with more than one folder at the same depth, which is to say in
// every real bookmark file and no test fixture written carelessly.
func walk(n *html.Node, path []string, out *[]Bookmark) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch strings.ToLower(c.Data) {
		case "dt":
			// A <DT> holds either an <H3> naming a folder, or an <A>. The folder's
			// contents are the <DL> that follows, which — because <DT> is never
			// closed — the parser makes a CHILD of this <DT> rather than a
			// sibling. That is the one structural fact this whole function turns on.
			walkDT(c, path, out)
		case "dl", "body", "html", "p":
			walk(c, path, out)
		}
	}
}

func walkDT(dt *html.Node, path []string, out *[]Bookmark) {
	var folderName string
	for c := dt.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch strings.ToLower(c.Data) {
		case "h3":
			folderName = strings.TrimSpace(textOf(c))
		case "a":
			if b, ok := anchorToBookmark(c, path); ok {
				// A <DD> description follows the anchor as a sibling of the <DT>.
				if dd := nextElement(dt, "dd"); dd != nil {
					b.Description = strings.TrimSpace(textOf(dd))
				}
				*out = append(*out, b)
			}
		case "dl":
			child := path
			if folderName != "" {
				child = append(append([]string{}, path...), folderName)
			}
			walk(c, child, out)
		}
	}
}

func anchorToBookmark(a *html.Node, path []string) (Bookmark, bool) {
	b := Bookmark{Title: strings.TrimSpace(textOf(a))}
	if len(path) > 0 {
		b.Folder = append([]string{}, path...)
	}

	for _, attr := range a.Attr {
		switch strings.ToUpper(attr.Key) {
		case "HREF":
			b.URL = strings.TrimSpace(attr.Val)
		case "ADD_DATE":
			b.AddedAt = parseEpoch(attr.Val)
		case "TAGS":
			for _, tag := range strings.Split(attr.Val, ",") {
				if tag = strings.TrimSpace(tag); tag != "" {
					b.Tags = append(b.Tags, tag)
				}
			}
		case "ICON":
			b.IconDataURL = attr.Val
		}
	}

	if b.URL == "" {
		return Bookmark{}, false
	}
	// A bookmark with no title is legal and common — browsers write the URL as
	// the text when the page had no <title>. An empty row in a list is worse
	// than a URL, so fall back rather than dropping it.
	if b.Title == "" {
		b.Title = b.URL
	}
	// Browsers write javascript: bookmarklets into the same file. They are not
	// bookmarks, they are code, and importing one as a link produces an entry
	// that executes when clicked.
	if isScriptURL(b.URL) {
		return Bookmark{}, false
	}
	return b, true
}

func isScriptURL(u string) bool {
	trimmed := strings.ToLower(strings.TrimLeft(u, " \t\r\n\x00"))
	return strings.HasPrefix(trimmed, "javascript:") || strings.HasPrefix(trimmed, "data:")
}

// parseEpoch reads ADD_DATE, which is in whichever unit the exporter felt like.
//
// Four are in the wild, and they are discriminated by magnitude because nothing
// in the file says which is which. Present-day values, for scale:
//
//	unix seconds       1.8e9
//	unix milliseconds  1.8e12
//	unix microseconds  1.8e15
//	WebKit micros      1.3e16   (since 1601-01-01; Chrome)
//
// The gaps are three orders of magnitude apart, so the thresholds below are not
// close calls — but the ONE place they nearly touch is unix-µs against
// WebKit-µs, a single order of magnitude, which is why the boundary sits at 5e15
// rather than at a round number.
//
// Getting this wrong does not error. It produces a bookmark dated 1970, or 1601,
// or the year 425014 — all of which sort to one end of every list and read as
// data corruption rather than as a units bug.
func parseEpoch(s string) time.Time {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	switch {
	case n > 5e15: // microseconds since 1601
		return fromWebKit(n)
	case n > 1e14: // microseconds since 1970
		return time.UnixMicro(n).UTC()
	case n > 1e11: // milliseconds since 1970
		return time.UnixMilli(n).UTC()
	default:
		return time.Unix(n, 0).UTC()
	}
}

func nextElement(n *html.Node, tag string) *html.Node {
	for s := n.NextSibling; s != nil; s = s.NextSibling {
		if s.Type != html.ElementNode {
			continue
		}
		if strings.EqualFold(s.Data, tag) {
			return s
		}
		return nil
	}
	return nil
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(x *html.Node) {
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

// Write emits a Netscape bookmark file.
//
// The output is deliberately the malformed shape browsers emit — unclosed <DT>
// and <p>, uppercase tags, the DOCTYPE that is not a real doctype, and the
// "Do Not Edit!" comment. Browsers parse this by convention rather than by
// specification, and a tidied, well-formed file is one some importers get wrong.
// Matching what Firefox and Chrome themselves write is the only reliable way to
// be importable by both.
func Write(w io.Writer, bookmarks []Bookmark) error {
	var b strings.Builder
	b.WriteString("<!DOCTYPE NETSCAPE-Bookmark-file-1>\n")
	b.WriteString("<!-- This is an automatically generated file.\n")
	b.WriteString("     It will be read and overwritten.\n")
	b.WriteString("     DO NOT EDIT! -->\n")
	b.WriteString(`<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">` + "\n")
	b.WriteString("<TITLE>Bookmarks</TITLE>\n")
	b.WriteString("<H1>Bookmarks</H1>\n")
	b.WriteString("<DL><p>\n")

	writeTree(&b, buildTree(bookmarks), 1)

	b.WriteString("</DL><p>\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// node is a folder in the export tree.
type node struct {
	name     string
	children map[string]*node
	items    []Bookmark
}

func newNode(name string) *node {
	return &node{name: name, children: map[string]*node{}}
}

func buildTree(bookmarks []Bookmark) *node {
	root := newNode("")
	for _, bm := range bookmarks {
		cur := root
		for _, part := range bm.Folder {
			if part == "" {
				continue
			}
			child, ok := cur.children[part]
			if !ok {
				child = newNode(part)
				cur.children[part] = child
			}
			cur = child
		}
		cur.items = append(cur.items, bm)
	}
	return root
}

// writeTree emits folders before loose bookmarks, which is the order every
// browser writes and therefore the order that looks unremarkable on import.
func writeTree(b *strings.Builder, n *node, depth int) {
	indent := strings.Repeat("    ", depth)

	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		child := n.children[name]
		fmt.Fprintf(b, "%s<DT><H3>%s</H3>\n", indent, escape(name))
		fmt.Fprintf(b, "%s<DL><p>\n", indent)
		writeTree(b, child, depth+1)
		fmt.Fprintf(b, "%s</DL><p>\n", indent)
	}

	for _, bm := range n.items {
		b.WriteString(indent)
		b.WriteString(`<DT><A HREF="` + escape(bm.URL) + `"`)
		if !bm.AddedAt.IsZero() {
			fmt.Fprintf(b, ` ADD_DATE="%d"`, bm.AddedAt.Unix())
		}
		if len(bm.Tags) > 0 {
			b.WriteString(` TAGS="` + escape(strings.Join(bm.Tags, ",")) + `"`)
		}
		if bm.IconDataURL != "" {
			b.WriteString(` ICON="` + escape(bm.IconDataURL) + `"`)
		}
		b.WriteString(">" + escape(bm.Title) + "</A>\n")

		if bm.Description != "" {
			fmt.Fprintf(b, "%s<DD>%s\n", indent, escape(bm.Description))
		}
	}
}

// escape covers the five entities that matter in attribute and text position.
//
// html.EscapeString would also do, and is avoided for one reason: it escapes
// apostrophes to &#39;, which several older importers render literally. The
// conservative set is what browsers themselves emit.
var escaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
)

func escape(s string) string { return escaper.Replace(s) }
