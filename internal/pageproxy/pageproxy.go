// Package pageproxy fetches a whole publisher page and prepares it to be served
// from our own origin.
//
// This is §10.1b — tier 2 of the rendering ladder, and the rung that answers
// "let me see the actual site" rather than "let me read the words". The article
// text already arrives through extraction and its images through
// `internal/assetproxy`; this is for the case where what you want is the page:
// its layout, its typography, the pull quotes, the charts that are only images
// in the first place.
//
// # The pipeline, and why it is in this order
//
//	fetch (guarded) → decode → rewrite → sanitize → cache the RAW copy
//
// **Rewrite before sanitize**, which looks backwards and is not. `<base href>`
// retargets every relative URL on a page, and it is not on any sanitize
// allowlist — sanitize first and the tag is gone before anything could honour
// it, so every relative URL silently resolves against the wrong host. Rewriting
// first consumes the base correctly and removes it. The cost is rewriting URLs
// inside `<script>` blocks that are dropped a moment later, which is a rounding
// error against parsing the document at all.
//
// **The cache holds the raw decoded page, not the finished one.** The finished
// HTML contains asset URLs with expiries in them; cache that for a week and the
// page comes back with every image dead. So the expensive part (the network) is
// cached and the cheap part (two parses) is redone per request with fresh
// capabilities. A page view is a deliberate act, not a hot path.
package pageproxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/charsetdec"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
	"github.com/monstercameron/ArticleFlux/internal/rewrite"
	"github.com/monstercameron/ArticleFlux/internal/sanitize"
)

var (
	// ErrNotHTML is returned when the origin served something that is not a page.
	ErrNotHTML = errors.New("pageproxy: not an HTML page")
	// ErrTooLarge is returned when a response exceeds the cap.
	ErrTooLarge = errors.New("pageproxy: page is too large")
	// ErrUpstream is returned for a non-200 from the origin.
	ErrUpstream = errors.New("pageproxy: upstream refused")
)

// DefaultMaxBytes caps one page's HTML.
//
// Four megabytes of *markup* — images are fetched separately and are not counted
// here. Real pages that exceed this are almost always a redirect loop rendered
// as a document or an error page with a stack trace in it.
const DefaultMaxBytes = 4 << 20

// cacheTTL is how long a fetched page is reused.
//
// Short, because unlike an image a page is a moving target: a front page is
// different in an hour, and the reader pressed this button to see the site as it
// is. Long enough that reopening the same article twice in a sitting is one
// fetch.
const cacheTTL = 30 * time.Minute

// Page is a prepared page.
type Page struct {
	// HTML is rewritten, sanitized and ready to serve.
	HTML string
	// FinalURL is where the fetch actually ended up, after redirects. It is what
	// relative URLs resolved against, and it is what the reader is shown as the
	// provenance of what they are looking at.
	FinalURL string
	// Title is the page's own <title>, for the tab.
	Title string
	// Fetched reports whether this came from the network rather than the cache.
	Fetched bool
}

// Options configure a Fetcher.
type Options struct {
	Dir          string
	AllowPrivate bool
	MaxBytes     int64
	Timeout      time.Duration
	UserAgent    string
}

// Fetcher fetches and prepares pages.
type Fetcher struct {
	dir          string
	client       *http.Client
	maxBytes     int64
	allowPrivate bool
}

// New builds a Fetcher.
func New(opt Options) *Fetcher {
	if opt.MaxBytes <= 0 {
		opt.MaxBytes = DefaultMaxBytes
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 25 * time.Second
	}
	if opt.UserAgent == "" {
		opt.UserAgent = "ArticleFlux/1.0 (+page proxy)"
	}
	return &Fetcher{
		dir: opt.Dir,
		client: netguard.Client(netguard.Options{
			AllowPrivate: opt.AllowPrivate,
			Timeout:      opt.Timeout,
			UserAgent:    opt.UserAgent,
		}),
		maxBytes:     opt.MaxBytes,
		allowPrivate: opt.AllowPrivate,
	}
}

// Get returns a page ready to serve.
//
// asset mints proxy URLs for the page's subresources; link does the same for
// its `<a href>`s, so clicking through stays inside the proxy. Either may be
// nil, and a nil `link` is the sensible default for an embedded view — the
// reader is looking at one page, not browsing.
func (f *Fetcher) Get(ctx context.Context, pageURL string, asset, link rewrite.Rewriter) (Page, error) {
	raw, final, ok := f.readCache(pageURL)
	fetched := false
	if !ok {
		var err error
		raw, final, err = f.fetch(ctx, pageURL)
		if err != nil {
			return Page{}, err
		}
		fetched = true
		f.writeCache(pageURL, raw, final)
	}

	base, err := url.Parse(final)
	if err != nil {
		return Page{}, fmt.Errorf("pageproxy: %w", err)
	}

	out, err := rewrite.HTML(string(raw), rewrite.Options{
		Base:          base,
		Asset:         asset,
		Link:          link,
		DropIntegrity: true,
		DropMetaCSP:   true,
	})
	if err != nil {
		return Page{}, fmt.Errorf("pageproxy: %w", err)
	}

	return Page{
		HTML:     sanitize.HTML(out, sanitize.Snapshot),
		FinalURL: final,
		Title:    title(raw),
		Fetched:  fetched,
	}, nil
}

func (f *Fetcher) fetch(ctx context.Context, pageURL string) ([]byte, string, error) {
	// Same policy branch as assetproxy and for the same reason: netguard.Get
	// applies the strict check regardless of how the client was built.
	if f.allowPrivate {
		if err := netguard.CheckURLPermissive(pageURL); err != nil {
			return nil, "", fmt.Errorf("pageproxy: %w", err)
		}
	} else if err := netguard.CheckURL(pageURL); err != nil {
		return nil, "", fmt.Errorf("pageproxy: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("pageproxy: %w", err)
	}
	// Asking for HTML specifically. Some origins content-negotiate, and the
	// default Go Accept header invites a JSON API response we cannot render.
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("pageproxy: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%w: %s", ErrUpstream, resp.Status)
	}

	ct := resp.Header.Get("Content-Type")
	if mt := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])); mt != "" &&
		mt != "text/html" && mt != "application/xhtml+xml" {
		return nil, "", fmt.Errorf("%w: %s", ErrNotHTML, mt)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("pageproxy: %w", err)
	}
	if int64(len(body)) > f.maxBytes {
		return nil, "", ErrTooLarge
	}

	// Decoded to UTF-8 here, once, so everything downstream is spared the
	// question. The `feed` corpus found this exact bug the hard way: decode late
	// and something re-decodes, decode twice and every accented character is
	// mojibake.
	decoded, _, derr := charsetdec.Decode(body, ct)
	if derr != nil {
		decoded = body
	}

	// The FINAL URL, not the requested one: relative references on a page that
	// redirected resolve against where it landed, and getting this wrong points
	// every asset at a host that no longer serves them.
	final := pageURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return decoded, final, nil
}

// title pulls the document title, for the browser tab.
//
// A scan rather than a parse: the document is parsed twice already and this is
// wanted before either of those, for the shell that wraps them.
func title(raw []byte) string {
	lower := bytes.ToLower(raw)
	i := bytes.Index(lower, []byte("<title"))
	if i < 0 {
		return ""
	}
	open := bytes.IndexByte(lower[i:], '>')
	if open < 0 {
		return ""
	}
	start := i + open + 1
	end := bytes.Index(lower[start:], []byte("</title"))
	if end < 0 {
		return ""
	}
	t := strings.TrimSpace(string(raw[start : start+end]))
	if len(t) > 200 {
		t = t[:200]
	}
	return t
}

// --- cache -------------------------------------------------------------------

func (f *Fetcher) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	h := hex.EncodeToString(sum[:])
	return filepath.Join(f.dir, h[:2], h)
}

func (f *Fetcher) readCache(pageURL string) ([]byte, string, bool) {
	if f.dir == "" {
		return nil, "", false
	}
	p := f.path(pageURL)
	st, err := os.Stat(p)
	if err != nil || time.Since(st.ModTime()) > cacheTTL {
		return nil, "", false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", false
	}
	final, body, ok := bytes.Cut(b, []byte("\n"))
	if !ok {
		return nil, "", false
	}
	return body, string(final), true
}

func (f *Fetcher) writeCache(pageURL string, body []byte, final string) {
	if f.dir == "" {
		return
	}
	p := f.path(pageURL)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "tmp-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	_, werr := tmp.WriteString(final + "\n")
	if werr == nil {
		_, werr = tmp.Write(body)
	}
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		return
	}
	if err := os.Rename(name, p); err != nil {
		_ = os.Remove(name)
	}
}
