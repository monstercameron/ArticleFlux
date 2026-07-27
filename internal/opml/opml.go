// Package opml reads and writes OPML subscription lists.
//
// OPML is how people leave one reader and arrive at another, so it is the
// migration path in both directions and neither half is optional: an importer
// without an exporter is a roach motel.
//
// The specification is loose to the point of being advisory. Real exports from
// FreshRSS, Feedly, Inoreader, NewsBlur and Google Reader disagree about
// nesting, about which attribute holds the title, and about whether `type` is
// even present. This parser is written against what those files actually contain
// rather than against the document.
package opml

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// Document is a parsed OPML file.
type Document struct {
	Title string
	Feeds []Feed
	// Folders are the outline groups that contained feeds, in document order.
	Folders []string
}

// Feed is one subscription.
type Feed struct {
	// Title is the user's name for the feed. Never empty after parsing: an
	// untitled row is unusable in a sidebar, so the host stands in.
	Title string
	// FeedURL is the xmlUrl. A row without one is not a subscription.
	FeedURL string
	SiteURL string
	// Folder is the enclosing outline's text, or "" at the top level.
	Folder string
}

// outline is the recursive OPML element.
//
// Both `text` and `title` are captured because exporters disagree: the
// specification says `text`, Google Reader wrote `title`, and several tools
// write both with different values.
type outline struct {
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr"`
	Type     string    `xml:"type,attr"`
	XMLURL   string    `xml:"xmlUrl,attr"`
	HTMLURL  string    `xml:"htmlUrl,attr"`
	Outlines []outline `xml:"outline"`
}

type opmlDoc struct {
	XMLName xml.Name `xml:"opml"`
	Head    struct {
		Title string `xml:"title"`
	} `xml:"head"`
	Body struct {
		Outlines []outline `xml:"outline"`
	} `xml:"body"`
}

// ErrNotOPML means the bytes were not an OPML document.
var ErrNotOPML = errors.New("opml: not an OPML document")

// MaxSize bounds an import. A 144-feed export is ~32 KB; 8 MiB is four orders of
// magnitude of headroom and still refuses a file that would exhaust memory.
const MaxSize = 8 << 20

// Parse reads an OPML document.
//
// Two shapes have to work, and both occur:
//
//   - Flat: every feed directly under <body>.
//   - Wrapped: everything under a single unnamed group, which is what FreshRSS
//     exports (a top-level "Subscriptions" outline). Treating that wrapper as a
//     folder would put every feed in one folder called "Subscriptions", which is
//     not what the user had.
func Parse(r io.Reader) (*Document, error) {
	var doc opmlDoc
	dec := xml.NewDecoder(io.LimitReader(r, MaxSize))
	// Exports are not always UTF-8, and several tools emit undeclared entities.
	dec.Strict = false
	dec.CharsetReader = charsetPassthrough
	if err := dec.Decode(&doc); err != nil {
		return nil, ErrNotOPML
	}

	out := &Document{Title: strings.TrimSpace(doc.Head.Title)}
	tops := doc.Body.Outlines

	// Unwrap a single top-level group that contains only groups/feeds and has no
	// feed of its own. That is the FreshRSS "Subscriptions" wrapper.
	if len(tops) == 1 && tops[0].XMLURL == "" && len(tops[0].Outlines) > 0 {
		tops = tops[0].Outlines
	}

	seenFolder := map[string]bool{}
	var walk func(items []outline, folder string)
	walk = func(items []outline, folder string) {
		for _, o := range items {
			url := strings.TrimSpace(o.XMLURL)
			if url != "" {
				out.Feeds = append(out.Feeds, Feed{
					Title:   title(o, url),
					FeedURL: url,
					SiteURL: strings.TrimSpace(o.HTMLURL),
					Folder:  folder,
				})
				continue
			}
			// No xmlUrl: this is a folder. Its own text names it.
			name := strings.TrimSpace(firstNonEmpty(o.Text, o.Title))
			if len(o.Outlines) == 0 {
				// A leaf with neither a URL nor children is noise, not a folder.
				continue
			}
			if name != "" && !seenFolder[name] {
				seenFolder[name] = true
				out.Folders = append(out.Folders, name)
			}
			// Nested folders are flattened to their immediate parent's name.
			// ArticleFlux caps folder depth, and a two-level path is what every
			// reader shows anyway.
			walk(o.Outlines, name)
		}
	}
	walk(tops, "")

	if len(out.Feeds) == 0 {
		return nil, ErrNotOPML
	}
	return out, nil
}

// Write serialises a subscription list.
//
// Feeds are grouped under their folder so the file round-trips through other
// readers, which is the only reason to export at all.
func Write(w io.Writer, doc *Document) error {
	byFolder := map[string][]Feed{}
	var order []string
	for _, f := range doc.Feeds {
		if _, seen := byFolder[f.Folder]; !seen {
			order = append(order, f.Folder)
		}
		byFolder[f.Folder] = append(byFolder[f.Folder], f)
	}

	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString("<opml version=\"2.0\">\n  <head>\n    <title>")
	b.WriteString(escape(orDefault(doc.Title, "ArticleFlux")))
	b.WriteString("</title>\n  </head>\n  <body>\n")

	// Top-level feeds first, then folders — the order a person expects to read.
	for _, folder := range order {
		if folder != "" {
			continue
		}
		for _, f := range byFolder[folder] {
			writeFeed(&b, f, "    ")
		}
	}
	for _, folder := range order {
		if folder == "" {
			continue
		}
		b.WriteString("    <outline text=\"" + escape(folder) + "\">\n")
		for _, f := range byFolder[folder] {
			writeFeed(&b, f, "      ")
		}
		b.WriteString("    </outline>\n")
	}

	b.WriteString("  </body>\n</opml>\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeFeed(b *strings.Builder, f Feed, indent string) {
	b.WriteString(indent)
	b.WriteString(`<outline type="rss" text="` + escape(f.Title) + `"`)
	b.WriteString(` xmlUrl="` + escape(f.FeedURL) + `"`)
	if f.SiteURL != "" {
		b.WriteString(` htmlUrl="` + escape(f.SiteURL) + `"`)
	}
	b.WriteString("/>\n")
}

// title picks the best available name, falling back to the feed's host.
//
// An untitled row is unusable in a sidebar, and OPML files from scripted exports
// routinely omit both attributes.
func title(o outline, feedURL string) string {
	if t := strings.TrimSpace(firstNonEmpty(o.Text, o.Title)); t != "" {
		return t
	}
	if i := strings.Index(feedURL, "://"); i >= 0 {
		host := feedURL[i+3:]
		if j := strings.IndexAny(host, "/?#"); j >= 0 {
			host = host[:j]
		}
		return strings.TrimPrefix(host, "www.")
	}
	return feedURL
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// escape writes XML attribute-safe text. encoding/xml's own escaper turns
// newlines into character references, which is correct but produces diffs no one
// can read; these five are what actually need escaping in an attribute.
var attrEscaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;",
)

func escape(s string) string { return attrEscaper.Replace(s) }

// charsetPassthrough accepts any declared encoding rather than failing.
//
// Exports declare encodings they do not use, and an OPML file is almost entirely
// ASCII attribute values — refusing to parse one because of a wrong declaration
// would lose someone's whole subscription list over a header.
func charsetPassthrough(_ string, input io.Reader) (io.Reader, error) {
	return input, nil
}
