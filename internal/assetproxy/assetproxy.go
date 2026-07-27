// Package assetproxy fetches and caches the images an article points at, so
// they can be served from our own origin instead of the publisher's.
//
// This is §10.1a, and the reason it exists is narrower than "privacy": on a
// network that blocks the publisher, the server can reach the article and the
// *browser* cannot reach its images. The text arrives, the pictures hang, and
// the article is broken in a way that looks like a rendering bug. Everything
// here is in service of that one sentence.
//
// It is also what finishes an archive. §10.6 keeps `archived_html` so an
// article survives its publisher going away; an archive whose images still
// point at a dead origin has preserved a manifest of 404s.
//
// # What this package will not do
//
// It takes a URL and fetches it. It has no idea whether the caller was allowed
// to ask, and it must never be handed a URL that came from a request body — the
// authorization and the "this URL appeared in an item you can read" check both
// belong to the caller (§10.1b). What it does own is everything that makes the
// fetch safe once decided: the SSRF guard on every hop, a hard size cap, a
// content-type allowlist, and a disk cache so a popular image is fetched once
// rather than once per reader per render.
package assetproxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/netguard"
)

var (
	// ErrTooLarge is returned when a response exceeds the cap.
	ErrTooLarge = errors.New("assetproxy: asset is too large")
	// ErrNotAnImage is returned for a content type outside the allowlist.
	ErrNotAnImage = errors.New("assetproxy: not an image")
	// ErrUpstream is returned for a non-200 from the origin.
	ErrUpstream = errors.New("assetproxy: upstream refused")
)

// DefaultMaxBytes caps one asset.
//
// Eight megabytes is generous for an image and small enough that a hostile or
// broken origin cannot use one `<img>` to fill the disk. The cap is enforced
// while reading rather than from Content-Length, because Content-Length is a
// claim by the server we are defending against.
const DefaultMaxBytes = 8 << 20

// negativeTTL is how long a failure is remembered.
//
// The point is not to save bandwidth, it is to stop asking. An article with a
// dead image is re-read, and without this every read is another fetch of the
// same 404 — from a server the publisher may already be rate-limiting.
const negativeTTL = 6 * time.Hour

// Asset is one fetched image.
type Asset struct {
	Bytes       []byte
	ContentType string
	// ETag is ours, not the origin's: the content hash. The origin's validator
	// is useless to us here because the browser is talking to us, not to it,
	// and a hash we compute is one we can honour without a round trip.
	ETag string
}

// Options configure a Fetcher.
type Options struct {
	// Dir is the cache root. Created on demand.
	Dir string
	// AllowPrivate relaxes the SSRF guard for a self-hosted instance whose
	// feeds live on the same LAN. It does not relax the metadata-endpoint
	// rules; see netguard.
	AllowPrivate bool
	// MaxBytes caps one asset. Zero means DefaultMaxBytes.
	MaxBytes int64
	// Timeout bounds one fetch including redirects. Zero means 20s.
	Timeout time.Duration
	// UserAgent identifies us. A proxy that fetches anonymously is one a CDN
	// can only respond to by blocking.
	UserAgent string
}

// Fetcher fetches and caches assets.
type Fetcher struct {
	dir          string
	client       *http.Client
	maxBytes     int64
	allowPrivate bool
}

// New builds a Fetcher. A zero Dir disables caching, which is only sensible in
// tests — every real deployment wants the disk.
func New(opt Options) *Fetcher {
	if opt.MaxBytes <= 0 {
		opt.MaxBytes = DefaultMaxBytes
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 20 * time.Second
	}
	if opt.UserAgent == "" {
		opt.UserAgent = "ArticleFlux/1.0 (+asset proxy)"
	}
	return &Fetcher{
		dir: opt.Dir,
		client: netguard.Client(netguard.Options{
			AllowPrivate: opt.AllowPrivate,
			Timeout:      opt.Timeout,
			UserAgent:    opt.UserAgent,
			Purpose:      "asset",
		}),
		maxBytes:     opt.MaxBytes,
		allowPrivate: opt.AllowPrivate,
	}
}

// Get returns an asset, from cache when possible.
func (f *Fetcher) Get(ctx context.Context, rawURL string) (Asset, error) {
	key := cacheKey(rawURL)

	if a, ok := f.readCache(key); ok {
		return a, nil
	}
	if f.negativeHit(key) {
		return Asset{}, ErrUpstream
	}

	a, err := f.fetch(ctx, rawURL)
	if err != nil {
		f.markFailed(key)
		return Asset{}, err
	}
	f.writeCache(key, a)
	return a, nil
}

func (f *Fetcher) fetch(ctx context.Context, rawURL string) (Asset, error) {
	// The pre-check has to match the client's policy, and netguard.Get always
	// applies the strict one — so a permissive client reached through it would
	// still refuse the LAN address it was configured to allow. `feed` and
	// `extract` both branch exactly like this for the same reason; the third
	// copy is the point at which it should move into netguard.
	//
	// Either way this is only the early, legible rejection. The guarantee is
	// the dialer's Control, which runs after DNS on the address actually about
	// to be connected to, and which no amount of getting this branch wrong can
	// bypass.
	if f.allowPrivate {
		if err := netguard.CheckURLPermissive(rawURL); err != nil {
			return Asset{}, fmt.Errorf("assetproxy: %w", err)
		}
	} else if err := netguard.CheckURL(rawURL); err != nil {
		return Asset{}, fmt.Errorf("assetproxy: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Asset{}, fmt.Errorf("assetproxy: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return Asset{}, fmt.Errorf("assetproxy: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return Asset{}, fmt.Errorf("%w: %s", ErrUpstream, resp.Status)
	}

	// One byte past the cap, so "exactly at the limit" is a success and
	// "one over" is a legible error rather than a silent truncation. A
	// truncated image renders as a half-drawn picture, which is worse than
	// none: it looks like the publisher's fault.
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return Asset{}, fmt.Errorf("assetproxy: %w", err)
	}
	if int64(len(body)) > f.maxBytes {
		return Asset{}, ErrTooLarge
	}

	ct, ok := imageType(resp.Header.Get("Content-Type"), body)
	if !ok {
		return Asset{}, fmt.Errorf("%w: %q", ErrNotAnImage, resp.Header.Get("Content-Type"))
	}

	sum := sha256.Sum256(body)
	return Asset{Bytes: body, ContentType: ct, ETag: `"` + hex.EncodeToString(sum[:16]) + `"`}, nil
}

// imageType decides what this is, and refuses anything that is not an image.
//
// The declared type is preferred and the sniffed type is the fallback, because
// a surprising number of CDNs serve perfectly good JPEGs as
// `application/octet-stream`. That matters more than it sounds: we send
// `X-Content-Type-Options: nosniff`, so a browser handed octet-stream will
// refuse to render it — passing the origin's vagueness through would turn a
// working image into a broken one. Sniffing here, once, and then declaring the
// answer confidently is the whole reason the two can be combined safely.
func imageType(declared string, body []byte) (string, bool) {
	if mt := strings.ToLower(strings.TrimSpace(strings.SplitN(declared, ";", 2)[0])); strings.HasPrefix(mt, "image/") {
		return mt, true
	}
	sniffed := strings.SplitN(http.DetectContentType(body), ";", 2)[0]
	if strings.HasPrefix(sniffed, "image/") {
		return sniffed, true
	}
	return "", false
}

// --- cache -------------------------------------------------------------------
//
// One file per asset, holding a one-line header and then the bytes. Two files
// would mean two writes and a window where the metadata and the body disagree;
// one file with a header is written once and renamed into place atomically.
//
// The first two hex characters of the key become a subdirectory. A hundred
// thousand entries in a single directory is slow to enumerate on every
// filesystem this is likely to run on, and fanning out costs one MkdirAll.

func cacheKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])
}

func (f *Fetcher) path(key string) string {
	return filepath.Join(f.dir, key[:2], key)
}

func (f *Fetcher) readCache(key string) (Asset, bool) {
	if f.dir == "" {
		return Asset{}, false
	}
	b, err := os.ReadFile(f.path(key))
	if err != nil {
		return Asset{}, false
	}
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return Asset{}, false
	}
	head := string(b[:i])
	ct, etag, ok := strings.Cut(head, " ")
	if !ok {
		return Asset{}, false
	}
	return Asset{Bytes: b[i+1:], ContentType: ct, ETag: etag}, true
}

func (f *Fetcher) writeCache(key string, a Asset) {
	if f.dir == "" {
		return
	}
	dir := filepath.Dir(f.path(key))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, "tmp-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	_, werr := tmp.WriteString(a.ContentType + " " + a.ETag + "\n")
	if werr == nil {
		_, werr = tmp.Write(a.Bytes)
	}
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		return
	}
	// Best-effort throughout: a cache that fails to write must degrade to a
	// slower proxy, never to a failed request.
	if err := os.Rename(name, f.path(key)); err != nil {
		_ = os.Remove(name)
	}
}

func (f *Fetcher) failPath(key string) string { return f.path(key) + ".fail" }

func (f *Fetcher) negativeHit(key string) bool {
	if f.dir == "" {
		return false
	}
	st, err := os.Stat(f.failPath(key))
	if err != nil {
		return false
	}
	if time.Since(st.ModTime()) > negativeTTL {
		_ = os.Remove(f.failPath(key))
		return false
	}
	return true
}

func (f *Fetcher) markFailed(key string) {
	if f.dir == "" {
		return
	}
	p := f.failPath(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	// Truncating an existing marker refreshes its timestamp, which is the
	// whole state this file carries.
	if fh, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
		_ = fh.Close()
	}
}
