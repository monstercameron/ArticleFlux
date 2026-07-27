// Package extract turns a fetched web page into a readable article: sanitised
// HTML and plain text (TODO 4.4, D7).
//
// # Five consumers, and why that matters
//
// Reader mode renders the HTML. Bookmark archiving stores it. Offline packs ship
// it. Ranking reads the text. TTS speaks the text. That is why extraction is
// Phase 1 rather than Phase 3, and it is also why this package returns BOTH
// representations from one pass — deriving text from HTML at four call sites
// produces four subtly different word counts, and the one used for ranking would
// be the one nobody checked.
//
// # D7: go-shiori/go-readability
//
// Chosen 2026-07-26 after a twelve-page bake-off against go-trafilatura and
// go-domdistiller (`internal/extract/bakeoff`, a separate module so the losers
// do not sit in this one's dependency graph forever). All three worked on all
// twelve pages; the decision came down to two things a character count hides:
//
//   - Text quality. Trafilatura's plain-text serialiser inserts spaces around
//     inline elements: "The Room , Troll 2 , or Fateful Findings". Across the
//     twelve pages that is 199 artefacts against readability's 5. Three of the
//     five consumers above read the text, and one of them reads it out loud.
//   - Content retention. On a photo-heavy hardware review, readability kept 34
//     images and trafilatura kept 8. Losing three quarters of the photographs in
//     a review of a physical object is losing the article.
//
// Readability's one apparent weakness — markup bulk, 5.8x text size on WordPress
// against trafilatura's 1.4x — turned out to be 38 KB of `srcset` attributes,
// which is data rather than cruft, and which the sanitizer's attribute allowlist
// removes anyway. The measurement that looked decisive was measuring something
// that does not survive to storage.
//
// Recorded weakness: readability is weakest on documentation-shaped pages (the
// MDN fixture keeps some navigation scaffolding). Feeds rarely carry those, and
// the cost is cosmetic rather than a lost article.
package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	nurl "net/url"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
	"golang.org/x/net/html"

	"github.com/monstercameron/ArticleFlux/internal/charsetdec"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/timeutil"
)

// MaxBodyBytes bounds a page download.
//
// Smaller than the feed limit (32 MiB) on purpose: a feed is a whole archive and
// legitimately large, while a single article page that exceeds 8 MiB of HTML is
// not an article. The largest page in the corpus is 394 KB.
const MaxBodyBytes = 8 << 20

// UserAgent identifies us when fetching a page for reader mode.
const UserAgent = "ArticleFlux/0.1 (+https://github.com/monstercameron/ArticleFlux)"

// MinWords is the floor below which extraction is reported as failure.
//
// The number is a judgement, and the direction of the error matters more than
// the value. Below the floor the caller falls back to the feed's own content,
// which for a short post is usually the complete post anyway — so a false
// negative costs nothing. Above it, a nav-only page renders as an article
// consisting of the words "Home About Contact", which is the outcome worth
// avoiding. Twenty-five words is short enough that a genuine one-paragraph link
// post clears it and long enough that a navigation bar does not.
const MinWords = 25

// Article is an extracted page.
type Article struct {
	Title    string
	Byline   string
	SiteName string
	Language string

	// HTML is sanitised under sanitize.Archived and safe to render.
	HTML string
	// Text is plain text for ranking, search, word counts and TTS.
	Text string
	// Excerpt is a short lead-in for a list row.
	Excerpt string

	ImageURL  string
	WordCount int

	// PublishedAt is the page's own claim, clamped like a feed's. Zero when the
	// page makes no claim — the caller decides whether to fall back to the feed's
	// date, because only the caller knows whether it has one.
	PublishedAt time.Time

	// CanonicalURL is the URL actually fetched, after redirects.
	CanonicalURL string
}

var (
	// ErrNoContent means the page parsed but no article was found in it. A
	// section index or a login wall does this, and it is a normal outcome rather
	// than a failure — the caller falls back to the feed's own content.
	ErrNoContent = errors.New("extract: no article content found")
	// ErrTooLarge means the body exceeded MaxBodyBytes.
	ErrTooLarge = errors.New("extract: body too large")
)

// Extractor fetches and extracts. Reuse one: it holds a connection pool.
type Extractor struct {
	client       *http.Client
	allowPrivate bool
}

// Config configures an Extractor.
type Config struct {
	// AllowPrivateAddresses disables the SSRF guard. See feed.Config for the
	// argument; it applies identically here, and more sharply — extraction
	// fetches a URL that arrived inside someone else's feed, so the attacker
	// does not even need an account to choose it.
	AllowPrivateAddresses bool
	// Timeout bounds one page fetch. Zero means 20s.
	Timeout time.Duration
}

// New returns an Extractor whose transport is SSRF-guarded.
func New(cfg Config) *Extractor {
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	return &Extractor{
		allowPrivate: cfg.AllowPrivateAddresses,
		client: netguard.Client(netguard.Options{
			Timeout:      cfg.Timeout,
			DialTimeout:  10 * time.Second,
			UserAgent:    UserAgent,
			AllowPrivate: cfg.AllowPrivateAddresses,
			Purpose:      "extract",
		}),
	}
}

// Fetch retrieves pageURL and extracts it.
func (e *Extractor) Fetch(ctx context.Context, pageURL string) (*Article, error) {
	if !e.allowPrivate {
		if err := netguard.CheckURL(pageURL); err != nil {
			return nil, err
		}
	} else if err := netguard.CheckURLPermissive(pageURL); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("extract: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxBodyBytes {
		return nil, ErrTooLarge
	}

	// The URL after redirects, not the one asked for: relative links in the
	// article resolve against where the document actually came from.
	final := pageURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}

	art, err := FromBytes(body, resp.Header.Get("Content-Type"), final, timeutil.Now())
	if err != nil {
		return nil, err
	}
	art.CanonicalURL = final
	return art, nil
}

// FromBytes extracts an already-retrieved page.
//
// Separated from Fetch for the same reason feed.ParseBytes is: the fixtures test
// the real path rather than a copy of it, and the copy is what drifts.
func FromBytes(body []byte, contentType, pageURL string, now time.Time) (*Article, error) {
	// Decode first. Readability parses HTML, and an HTML parser handed
	// Windows-1252 bytes produces mojibake rather than an error — the same class
	// of bug the feed corpus caught between charsetdec and gofeed.
	decoded, _, err := charsetdec.Decode(body, contentType)
	if err != nil {
		return nil, err
	}

	// Parse the DOM ourselves and hand readability a document, rather than
	// handing it a reader.
	//
	// readability.FromReader goes through go-shiori/dom.Parse, which runs
	// STATISTICAL charset detection (gogs/chardet) over the bytes and re-decodes
	// on its guess. Given correct UTF-8 that is mostly ASCII with a few accented
	// words — French, German, anything Latin — chardet reads the sparse two-byte
	// sequences as Latin-1 and decodes a second time: "café" becomes "cafÃ©".
	//
	// This is the same double-decode the feed corpus found between charsetdec and
	// gofeed, arriving through a third door, and it is the reason the fix is
	// structural rather than another retag: charsetdec can repair a declaration,
	// but it cannot stop a library from ignoring the declaration and guessing.
	// The only reliable answer is to not give anyone downstream a second chance
	// to decode. We own the decode; everything after it gets a DOM.
	//
	// Note that it is also encoding-dependent in a way that hides it in testing:
	// a short page has too little signal for chardet to be confident and comes
	// through fine, so the failure only appears at realistic article length.
	doc, err := html.Parse(strings.NewReader(string(decoded)))
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	parsed, _ := nurl.Parse(pageURL)
	art, err := readability.FromDocument(doc, parsed)
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}

	// Readability returns content rather than an error when it finds no article:
	// on a section index or a login wall it hands back the navigation, which is
	// non-empty and worthless. Treating that as success gives every consumer a
	// reading pane containing the words "Home About Contact" and no explanation.
	text := sanitize.Text(art.Content)
	if len(strings.Fields(text)) < MinWords {
		return nil, ErrNoContent
	}

	out := &Article{
		Title:        strings.TrimSpace(art.Title),
		Byline:       strings.TrimSpace(art.Byline),
		SiteName:     strings.TrimSpace(art.SiteName),
		Language:     strings.TrimSpace(art.Language),
		HTML:         sanitize.HTML(art.Content, sanitize.Archived),
		Text:         text,
		Excerpt:      strings.TrimSpace(art.Excerpt),
		ImageURL:     strings.TrimSpace(art.Image),
		CanonicalURL: pageURL,
	}
	out.WordCount = len(strings.Fields(out.Text))

	// The page's own date, clamped exactly as a feed's is. A page claiming 2087
	// is no more believable than a feed claiming it.
	if art.PublishedTime != nil && !art.PublishedTime.IsZero() {
		out.PublishedAt = timeutil.ClampPublished(*art.PublishedTime, now)
	}

	if out.Excerpt == "" {
		out.Excerpt = excerptFrom(out.Text)
	}
	return out, nil
}

// excerptFrom cuts a list-row lead-in at a word boundary.
func excerptFrom(text string) string {
	const limit = 280
	if len(text) <= limit {
		return text
	}
	cut := text[:limit]
	if i := strings.LastIndexByte(cut, ' '); i > limit/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:") + "…"
}
