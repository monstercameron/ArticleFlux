// Package feed fetches and normalises feeds.
//
// gofeed handles the parsing (D1: RSS 0.9x–2.0, RDF, Atom 0.3/1.0, JSON Feed,
// plus the dc:, content:, media: and itunes: namespaces). What it does not do is
// the normalisation this application needs on top: clamping dates feeds lie
// about, computing a cross-source duplicate key, deriving a stable guid when the
// feed omits one, and turning "the fetch failed" into a health signal rather than
// an error return nobody reads.
//
// Every fetch goes through netguard. A feed URL is user-supplied and this server
// sits inside a network the user cannot otherwise reach, so "subscribe to
// http://169.254.169.254/..." is a credential-exfiltration attack delivered
// through the application's main feature.
package feed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/monstercameron/ArticleFlux/internal/charsetdec"
	"github.com/monstercameron/ArticleFlux/internal/feeddate"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
	"github.com/monstercameron/ArticleFlux/internal/timeutil"
	"github.com/monstercameron/ArticleFlux/internal/urlnorm"
)

// MaxBodyBytes bounds a feed download.
//
// 32 MiB, raised from 8 after 8 rejected danluu.com/atom.xml — a real, popular
// feed that ships the full text of every post ever written and is legitimately
// ~9 MB. The cap exists to stop a misconfigured server streaming gigabytes into
// memory, not to have an opinion about how much someone has written.
//
// The whole body is buffered rather than streamed because gofeed parses from a
// reader but charset detection has to see the head before decoding can start,
// and a feed is parsed once every thirty minutes. 32 MiB × six concurrent polls
// is a bounded 192 MiB worst case, which is the number that actually matters.
const MaxBodyBytes = 32 << 20

// UserAgent identifies us. A reader that fetches anonymously is one a publisher
// can only respond to by blocking.
const UserAgent = "ArticleFlux/0.1 (+https://github.com/monstercameron/ArticleFlux)"

// Parsed is a fetched and normalised feed.
type Parsed struct {
	Title       string
	Description string
	SiteURL     string
	IconURL     string
	Language    string
	// Author is the channel's own author, used as the fallback for items that
	// declare none.
	Author string
	Items  []Item

	// ETag and LastModified carry conditional-GET state back to the caller.
	ETag         string
	LastModified string
	// NotModified is true when the server answered 304 and there is nothing to
	// process. Honouring this is the difference between being a good citizen and
	// being rate-limited off a publisher's server.
	NotModified bool
}

// Item is one normalised entry.
type Item struct {
	GUID        string
	URL         string
	DupeKey     string
	Title       string
	Author      string
	Summary     string
	ContentHTML string
	PublishedAt time.Time
	ImageURL    string
	WordCount   int
}

// Fetcher fetches feeds. Reuse one: it holds a connection pool.
type Fetcher struct {
	client       *http.Client
	parser       *gofeed.Parser
	allowPrivate bool
}

// Config configures a Fetcher.
type Config struct {
	// AllowPrivateAddresses disables the SSRF guard.
	//
	// **This is a real security relaxation and it is off by default.** With it
	// on, any user who can add a feed can make the server fetch
	// http://169.254.169.254/ or a router admin page and read the result — which
	// is the exact attack netguard exists to stop.
	//
	// It exists because a self-hosted instance may genuinely need it: a feed on
	// the same LAN, a Miniflux or FreshRSS instance on the next box, an internal
	// status page. That is a legitimate use, and it is the operator's decision
	// to make rather than one to smuggle in as a default.
	AllowPrivateAddresses bool
}

// NewFetcher returns a Fetcher whose transport is SSRF-guarded.
func NewFetcher() *Fetcher { return New(Config{}) }

// New returns a Fetcher with explicit configuration.
func New(cfg Config) *Fetcher {
	f := &Fetcher{
		parser:       gofeed.NewParser(),
		allowPrivate: cfg.AllowPrivateAddresses,
	}
	// Even in permissive mode the request goes through netguard, just with a
	// narrower deny list. The earlier version swapped in http.DefaultTransport,
	// which turned "let me reach my LAN" into "no SSRF protection at all" — and
	// since the dev server sets this automatically on a loopback bind, that was
	// the configuration most people would actually run.
	f.client = netguard.Client(netguard.Options{
		Timeout:      30 * time.Second,
		DialTimeout:  10 * time.Second,
		UserAgent:    UserAgent,
		AllowPrivate: cfg.AllowPrivateAddresses,
		Purpose:      "feed",
	})
	return f
}

// Conditional carries the state from the last successful fetch.
type Conditional struct {
	ETag         string
	LastModified string
}

var (
	// ErrNotAFeed means the bytes fetched were not a feed.
	ErrNotAFeed = errors.New("feed: not a recognisable feed")
	// ErrTooLarge means the body exceeded MaxBodyBytes.
	ErrTooLarge = errors.New("feed: body too large")
)

// Fetch retrieves and parses a feed.
func (f *Fetcher) Fetch(ctx context.Context, feedURL string, cond Conditional) (*Parsed, error) {
	if !f.allowPrivate {
		if err := netguard.CheckURL(feedURL); err != nil {
			return nil, err
		}
	} else if err := netguard.CheckURLPermissive(feedURL); err != nil {
		// Private addresses are allowed here; link-local, the metadata endpoint,
		// multicast and reserved space are not, under any policy.
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept",
		"application/atom+xml, application/rss+xml, application/feed+json, application/xml;q=0.9, */*;q=0.8")
	if cond.ETag != "" {
		req.Header.Set("If-None-Match", cond.ETag)
	}
	if cond.LastModified != "" {
		req.Header.Set("If-Modified-Since", cond.LastModified)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotModified {
		return &Parsed{NotModified: true, ETag: cond.ETag, LastModified: cond.LastModified}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("feed: HTTP %d", resp.StatusCode)
	}

	// Read one byte past the limit so exceeding it is detectable rather than
	// silently truncating a feed into a parse error.
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxBodyBytes {
		return nil, ErrTooLarge
	}

	out, err := f.ParseBytes(body, resp.Header.Get("Content-Type"), feedURL, timeutil.Now())
	if err != nil {
		return nil, err
	}
	out.ETag = resp.Header.Get("ETag")
	out.LastModified = resp.Header.Get("Last-Modified")
	return out, nil
}

// ParseBytes decodes and parses a feed body that has already been retrieved.
//
// Split out of Fetch so the committed corpus (TODO 4.2) exercises the real
// decode-parse-normalise path rather than a test-local reimplementation of it. A
// fixture test that re-implements the pipeline proves the fixtures parse; it does
// not prove the application parses them, and those come apart the first time
// Fetch changes.
func (f *Fetcher) ParseBytes(body []byte, contentType, feedURL string, now time.Time) (*Parsed, error) {
	// Decode before parsing: a Windows-1252 or Shift-JIS feed handed to an XML
	// parser as raw bytes produces mojibake, not an error, so nothing would
	// notice.
	decoded, _, err := charsetdec.Decode(body, contentType)
	if err != nil {
		return nil, err
	}

	parsed, err := f.parser.Parse(strings.NewReader(string(decoded)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotAFeed, err)
	}
	return Normalize(parsed, feedURL, now), nil
}

// Normalize converts a gofeed result into our shape. Separate from Fetch so it
// is testable against fixtures without a network.
func Normalize(f *gofeed.Feed, feedURL string, now time.Time) *Parsed {
	p := &Parsed{
		Title:       strings.TrimSpace(f.Title),
		Description: strings.TrimSpace(f.Description),
		SiteURL:     f.Link,
		Language:    f.Language,
	}
	if p.Title == "" {
		// A feed with no title is unusable in a sidebar. The host is a better
		// placeholder than an empty row, and the user can rename it.
		p.Title = urlnorm.Host(feedURL)
	}
	if f.Image != nil {
		p.IconURL = f.Image.URL
	}
	if len(f.Authors) > 0 && f.Authors[0] != nil {
		p.Author = strings.TrimSpace(f.Authors[0].Name)
	}

	for _, it := range f.Items {
		p.Items = append(p.Items, normalizeItem(it, feedURL, p.Author, now))
	}
	return p
}

func normalizeItem(it *gofeed.Item, feedURL, feedAuthor string, now time.Time) Item {
	content := firstNonEmpty(it.Content, it.Description)
	summarySrc := firstNonEmpty(it.Description, it.Content)

	// Strip the body once and use it for both the word count and — when the
	// summary comes from the same field — the summary.
	//
	// The two lines this replaces read `summarize(firstNonEmpty(Description,
	// Content))` and `countWords(content)`, and both begin with stripTags. When
	// an entry carries only ONE of the two fields, which is most of the corpus,
	// those two expressions resolve to the same string and it was stripped
	// twice: two passes over the whole body, two builders, two entity replaces,
	// for one document.
	//
	// The equality test is a string comparison in the source and a pointer
	// comparison at run time — firstNonEmpty returns one of its arguments
	// unchanged, so when both resolve to the same field the two headers are
	// identical and == answers without looking at the bytes.
	strippedContent := stripTags(content)
	strippedSummary := strippedContent
	if summarySrc != content {
		strippedSummary = stripTags(summarySrc)
	}

	out := Item{
		GUID:        stableGUID(it, feedURL),
		URL:         strings.TrimSpace(it.Link),
		Title:       strings.TrimSpace(it.Title),
		Summary:     summarizeText(strippedSummary),
		ContentHTML: content,
		WordCount:   countWordsText(strippedContent),
	}
	if out.Title == "" {
		// Untitled entries are legal in Atom and common in microblog feeds. The
		// summary is what a person would read anyway.
		out.Title = truncate(stripTags(out.Summary), 80)
	}
	if len(it.Authors) > 0 && it.Authors[0] != nil {
		out.Author = strings.TrimSpace(it.Authors[0].Name)
	}
	if out.Author == "" {
		// Fall back to the channel's author. Podcast feeds are the common case:
		// the corpus's Changelog fixture carries one <itunes:author> at the
		// channel and none on any of its 1,012 episodes, so an item-only reading
		// shows no author anywhere in the application. A single-author blog is the
		// same shape.
		//
		// This is a normalisation decision rather than a parser one, which is why
		// it lives here: gofeed reports exactly what the document said, correctly.
		out.Author = feedAuthor
	}
	if it.Image != nil {
		out.ImageURL = it.Image.URL
	}
	if out.URL != "" {
		out.DupeKey = urlnorm.DupeKey(out.URL)
	}

	// Date resolution, in order of trust: gofeed's own parse, then our
	// broken-date layouts, then first-seen. Every branch runs through
	// ClampPublished, so a feed claiming 2087 cannot pin itself to the top of
	// every list forever.
	var claimed time.Time
	switch {
	case it.PublishedParsed != nil:
		claimed = *it.PublishedParsed
	case it.UpdatedParsed != nil:
		claimed = *it.UpdatedParsed
	case it.Published != "":
		claimed, _ = feeddate.Parse(it.Published)
	case it.Updated != "":
		claimed, _ = feeddate.Parse(it.Updated)
	}
	out.PublishedAt = timeutil.ClampPublished(claimed, now)
	return out
}

// stableGUID derives an identifier that survives re-polling.
//
// The order matters. A feed's own <guid> is authoritative when present. Failing
// that the link is stable. Failing both — which happens in hand-rolled feeds —
// hashing title+content is the last resort: it is stable as long as the entry is
// unchanged, and an edited entry becoming a "new" item is a far better failure
// than every entry being new on every poll.
func stableGUID(it *gofeed.Item, feedURL string) string {
	if g := strings.TrimSpace(it.GUID); g != "" {
		return g
	}
	if l := strings.TrimSpace(it.Link); l != "" {
		// ItemKey, not Norm: Norm drops the fragment, and a linkblog that
		// publishes a day's entries as anchors on one page has nothing else to
		// tell them apart. See urlnorm.ItemKey.
		return urlnorm.ItemKey(l)
	}
	h := sha256.Sum256([]byte(feedURL + "\x00" + it.Title + "\x00" + it.Description))
	return "sha256:" + hex.EncodeToString(h[:16])
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// summarize produces list-row text: tags stripped, whitespace collapsed, cut at
// a sentence-ish boundary.
// summaryLen is how much of an entry a list row shows.
const summaryLen = 280

func summarize(html string) string { return summarizeText(stripTags(html)) }

// summarizeText is summarize's second half, taking text that has already had its
// markup stripped.
//
// Split out so normalizeItem — which needs the stripped body anyway, for the
// word count — does not strip the same string twice.
//
// # Why it stops early
//
// This was `strings.Join(strings.Fields(text), " ")` followed by truncate, which
// is correct and does an amount of work proportional to the WHOLE article to
// produce 280 characters. On a full-content feed that is a 50KB body turned into
// a slice of nine thousand string headers, joined into a second 50KB string, and
// then discarded except for its first paragraph. Across the 27-feed corpus,
// strings.FieldsFunc alone accounted for 224MB of the 162MB-per-parse
// allocation profile's largest single entry.
//
// Collapsing incrementally and stopping once there is more text than truncate
// can use produces a byte-identical result, because truncate reads at most
// s[:summaryLen] and otherwise only asks whether s is longer than that. One byte
// past the limit answers both questions.
func summarizeText(text string) string {
	return truncate(collapseSpace(text, summaryLen+1), summaryLen)
}

// collapseSpace joins the whitespace-separated fields of s with single spaces,
// stopping once the result exceeds max bytes.
//
// It may return slightly more than max — the field that crosses the line is
// appended whole rather than cut, because cutting it would mean deciding where,
// and no caller wants that decision made here. What it guarantees is the part
// that matters: the bytes it returns are the same bytes strings.Join(
// strings.Fields(s), " ") would have produced in those positions.
func collapseSpace(s string, max int) string {
	var b strings.Builder
	b.Grow(min(len(s), max+64))
	for f := range strings.FieldsSeq(s) {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(f)
		if b.Len() > max {
			break
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	// Prefer a word boundary, so a row never ends mid-word.
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:") + "…"
}

// stripTags removes markup without parsing. This output is never rendered as
// HTML — it is plain text for a list row and for word counting — so a real
// parser would buy nothing. Anything that IS rendered goes through the sanitizer
// instead.
func stripTags(s string) string {
	var b strings.Builder
	// The output is the input minus its markup, so the input's length is an
	// upper bound and never a wild one — feed HTML is mostly text. Without this
	// the builder doubles from 8 bytes, which for a 50KB body is a dozen
	// reallocations and copies of everything written so far.
	b.Grow(len(s))
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return unescapeBasic(b.String())
}

// unescapeBasic resolves the five entities that actually appear in feed text.
// A full entity table is not needed for a list row.
var basicEntities = strings.NewReplacer(
	"&amp;", "&", "&lt;", "<", "&gt;", ">",
	"&quot;", `"`, "&#39;", "'", "&apos;", "'", "&nbsp;", " ",
	"&mdash;", "—", "&ndash;", "–", "&hellip;", "…", "&rsquo;", "'",
)

// unescapeBasic resolves those entities, and returns s untouched when there are
// none to resolve.
//
// strings.Replacer.Replace allocates a working buffer and a result string on
// every call, whether or not it changed anything — it does not have a
// "nothing matched" fast path. Every entity above starts with '&', so one
// IndexByte answers whether the call can possibly do anything, and most stripped
// feed text contains no ampersand at all.
func unescapeBasic(s string) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	return basicEntities.Replace(s)
}

func countWords(html string) int { return countWordsText(stripTags(html)) }

// countWordsText counts the fields of already-stripped text.
//
// FieldsSeq rather than len(strings.Fields(...)): the slice version allocates
// one string header per word — nine thousand of them for a long article — and
// every one of them is thrown away by the len() around it.
func countWordsText(text string) int {
	n := 0
	for range strings.FieldsSeq(text) {
		n++
	}
	return n
}

// NaturalKey is the deduplication key for a source (A14): two tenants adding the
// same feed URL converge on one row and one poll.
//
// Note what this is NOT used for: kind='mailbox' sources key per-user
// (`mailbox:<mailbox_id>:<sender>`), because global keying would merge two
// people's private mail into a single row.
func NaturalKey(feedURL string) string {
	return "feed:" + urlnorm.DupeKey(feedURL)
}
