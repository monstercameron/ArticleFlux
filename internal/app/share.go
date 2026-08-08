package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// `/pub/:slug` — a published scope, as Atom (§7.8b, TODO F29).
//
// The whole point is that this needs no client of ours. A folder or a tag comes
// out as a feed at an unguessable address, and whoever holds that address
// subscribes to it in whatever reader they already use. No account, no API key,
// no server of ours in the middle.
//
// # Excerpt-only, and this is not a limitation to fix later
//
// The feed carries titles, links and the publisher's own summary — never the
// extracted full text. Republishing somebody else's article under your name is a
// licensing decision, not a technical one, and it is not one an RSS endpoint
// should make on a reader's behalf. What a subscriber gets is a pointer, which
// is exactly what the original feed gave us.
//
// # Unguessable is not secret
//
// 128 bits of address is enough that nobody finds it by trying. It is not enough
// to survive being pasted into a public issue tracker, so the response says
// `X-Robots-Tag: noindex` unless the owner opted in — a crawler is the one
// visitor that turns "hard to find" into "listed on Google" without anybody
// meaning to.

// shareItems is how many entries a published feed carries.
//
// Fifty. A feed reader polls for what changed rather than for history, and a
// larger window costs every subscriber bytes on every poll to deliver articles
// they already have.
const shareItems = 50

// shareMaxAge is the cache window advertised to subscribers and intermediaries.
//
// Fifteen minutes. Feed readers poll on their own schedule regardless, and this
// is aimed at the case that actually costs something: a share passed around at
// work, where twenty readers poll the same address and a proxy can answer most
// of it.
const shareMaxAge = 15 * time.Minute

// servePublicShare answers a published scope.
func (a *App) servePublicShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "GET or HEAD only", http.StatusMethodNotAllowed)
		return
	}
	slug := strings.TrimPrefix(r.URL.Path, "/pub/")
	if slug == "" || strings.Contains(slug, "/") {
		http.NotFound(w, r)
		return
	}

	ps, err := a.repo.ShareBySlug(r.Context(), slug)
	if err != nil {
		// 404 for revoked, unknown and malformed alike. Any distinction is a
		// free oracle for "did this address ever exist", which is the one
		// question an attacker with a leaked URL would like answered.
		http.NotFound(w, r)
		return
	}

	sources, err := a.repo.ShareSources(r.Context(), ps)
	if err != nil {
		a.log.ErrorContext(r.Context(), "reading a share's sources", "share", ps.ID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var items []store.Item
	if len(sources) > 0 {
		// The OWNER's scope, taken from the share row rather than from anything
		// the request said — the request has no identity at all.
		sc := store.Scope{TenantID: ps.TenantID, UserID: ps.UserID}
		items, _, err = a.repo.ListItems(r.Context(), sc, store.ListQuery{
			SourceIDs: sources, Limit: shareItems,
		})
		if err != nil {
			a.log.ErrorContext(r.Context(), "reading a share's items", "share", ps.ID, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	feed := buildShareFeed(ps, items, a.publicURL(r))
	body, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		a.log.ErrorContext(r.Context(), "rendering a share", "share", ps.ID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	body = append([]byte(xml.Header), body...)

	// Conditional GET, because a feed reader polls this every fifteen minutes
	// forever. The ETag is over the RENDERED BYTES rather than over the newest
	// timestamp: a title edit, an item disappearing from the window, or the
	// share being renamed all change the document without moving any date, and
	// an ETag that missed those would serve a stale feed indefinitely.
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(shareMaxAge.Seconds())))
	if !ps.Indexable {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}
	if len(items) > 0 {
		// `Item.PublishedAt` is the stored RFC3339 string rather than a time —
		// the store keeps timestamps as text so SQLite can compare them — so it
		// is parsed here rather than assumed. An unparseable one simply omits
		// the header: a feed reader that gets no Last-Modified polls normally,
		// which is a smaller loss than a header that lies.
		if at, err := time.Parse(time.RFC3339Nano, items[0].PublishedAt); err == nil {
			w.Header().Set("Last-Modified", at.UTC().Format(http.TimeFormat))
		}
	}
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}

// publicURL is the address this instance is reachable at, for the feed's own
// self-link.
//
// Derived from the request rather than configured, for the reason TunnelURL
// gives on the client: a hardcoded host is the single most common reason a
// self-hosted app works for its author and nobody else.
func (a *App) publicURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// --- Atom -------------------------------------------------------------------

type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Links   []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr,omitempty"`
}

type atomEntry struct {
	Title   string     `xml:"title"`
	ID      string     `xml:"id"`
	Updated string     `xml:"updated"`
	Links   []atomLink `xml:"link"`
	Author  *atomName  `xml:"author,omitempty"`
	Summary *atomText  `xml:"summary,omitempty"`
}

type atomName struct {
	Name string `xml:"name"`
}

// atomText carries the excerpt as escaped HTML.
//
// `type="html"` with the markup escaped, rather than a CDATA block or
// `type="xhtml"`: escaped HTML is the form every reader in the world handles
// identically, and the alternatives are where feed parsers disagree.
type atomText struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

// buildShareFeed renders a published scope.
func buildShareFeed(ps store.PublishedShare, items []store.Item, base string) atomFeed {
	self := base + "/pub/" + ps.Slug
	updated := ps.CreatedAt
	if len(items) > 0 {
		if at, err := time.Parse(time.RFC3339Nano, items[0].PublishedAt); err == nil && at.After(updated) {
			updated = at
		}
	}

	feed := atomFeed{
		Title: ps.Title,
		// The tag URI is built from the SLUG, so rotating an address makes a new
		// feed identity — which is honest: a rotated share is deliberately not
		// the same feed the old subscribers had.
		ID:      "urn:articleflux:share:" + ps.Slug,
		Updated: updated.UTC().Format(time.RFC3339),
		Links: []atomLink{
			{Rel: "self", Href: self, Type: "application/atom+xml"},
		},
	}

	for _, it := range items {
		e := atomEntry{
			Title: it.Title,
			ID:    "urn:articleflux:item:" + it.ID,
			// Normalised to RFC3339 rather than passed through: the column is
			// text, and Atom's `updated` is one of the few fields feed readers
			// genuinely parse rather than display.
			Updated: atomTime(it.PublishedAt, ps.CreatedAt),
		}
		if it.URL != "" {
			e.Links = append(e.Links, atomLink{Rel: "alternate", Href: it.URL, Type: "text/html"})
		}
		if it.Author != "" {
			e.Author = &atomName{Name: it.Author}
		}
		// The publisher's own summary, sanitized — never `ContentHTML`, and
		// never the extracted article. See the excerpt note at the top.
		if sum := strings.TrimSpace(it.Summary); sum != "" {
			e.Summary = &atomText{Type: "html", Body: sanitize.HTML(sum, sanitize.Feed)}
		}
		feed.Entries = append(feed.Entries, e)
	}
	return feed
}

// atomTime renders a stored timestamp, falling back when it cannot be parsed.
//
// The fallback is the SHARE's creation time rather than "now": an entry whose
// date moves every time the feed is fetched looks new on every poll, which is
// how a reader ends up being notified about the same article forever.
func atomTime(stored string, fallback time.Time) string {
	if at, err := time.Parse(time.RFC3339Nano, stored); err == nil {
		return at.UTC().Format(time.RFC3339)
	}
	return fallback.UTC().Format(time.RFC3339)
}
