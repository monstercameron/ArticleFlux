package app

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/assetproxy"
	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// assetTTL is how long a minted asset URL stays valid.
//
// Twelve hours is the shape of reading rather than a security number: you open
// an article, you come back to it after lunch, and the images still load
// without a refetch of the page. It is short enough that a URL which escapes
// into a log or a referrer is a capability to fetch one public image, for the
// rest of the day, and then nothing.
const assetTTL = 12 * time.Hour

// assetCacheMaxAge is what the browser is told. Deliberately shorter than the
// TTL: a browser holding a cached image past the point where its URL expires
// would be fine, but one that revalidates *after* expiry gets a 403 for a
// picture it already has, which reads as a broken article.
const assetCacheMaxAge = int(6 * time.Hour / time.Second)

// serveAsset re-serves one of an article's images from our own origin.
//
//	GET /asset?u=<base64url(url)>&e=<unix expiry>&s=<signature>
//
// §10.1a. The article text already arrives from us; its images do not, and on a
// network that blocks the publisher that is the difference between a readable
// page and a broken one.
//
// # Why this endpoint is not authenticated
//
// Every other private surface refuses a credential in the query string
// (`/speech` says so explicitly) because a URL lands in history, referrers and
// access logs. This one has no credential at all, and that is the same decision
// rather than its opposite: an `<img src>` cannot send an `Authorization`
// header, so the choice was never "header or query" — it was "a capability or
// nothing".
//
// So the URL *is* the capability: an HMAC over the target and an expiry, minted
// only by an authenticated RPC, for a URL that already appeared in an item the
// caller was allowed to read (§10.1b). What leaks if one escapes is the ability
// to fetch one publisher's image through this server until it expires. What
// does not leak is anything about the reader, because the capability carries no
// identity — which is also why it cannot be revoked individually, and why the
// TTL is hours rather than weeks.
func (a *App) serveAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if a.assets == nil {
		http.Error(w, "the asset proxy is not configured on this server", http.StatusNotImplemented)
		return
	}

	q := r.URL.Query()
	raw, err := base64.RawURLEncoding.DecodeString(q.Get("u"))
	if err != nil || len(raw) == 0 {
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	exp, err := strconv.ParseInt(q.Get("e"), 10, 64)
	if err != nil {
		http.Error(w, "bad expiry", http.StatusBadRequest)
		return
	}

	// Signature before expiry, and both before any work: an unsigned request
	// must not be able to tell the difference between "expired" and "never
	// valid", and must not cost a fetch either way.
	if !secret.VerifySignature(a.assetKey, assetMessage(string(raw), exp), q.Get("s")) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if time.Now().Unix() > exp {
		// 410 rather than 403: the client asked correctly and the answer used
		// to be yes. It also tells a stale page to reload rather than to
		// re-request, which is the actual fix.
		http.Error(w, "this link has expired", http.StatusGone)
		return
	}

	// Bounded: this runs inside a request, so a slow origin must not hold the
	// reader's connection open for the client's whole timeout.
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	asset, err := a.assets.Get(ctx, string(raw))
	if err != nil {
		a.log.Debug("asset proxy fetch failed", "err", err)
		switch {
		case errors.Is(err, assetproxy.ErrNotAnImage):
			http.Error(w, "not an image", http.StatusUnsupportedMediaType)
		case errors.Is(err, assetproxy.ErrTooLarge):
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		default:
			// The origin's own message can name internal hosts and paths, so it
			// goes to the log and never to the response (§22.11).
			http.Error(w, "could not fetch that image", http.StatusBadGateway)
		}
		return
	}

	h := w.Header()
	h.Set("Content-Type", asset.ContentType)
	h.Set("X-Content-Type-Options", "nosniff")
	// An SVG is a document that can carry script, and this endpoint will serve
	// one. Loaded through <img> a browser never runs it; navigated to directly
	// it would, and "directly" includes anyone who is handed the URL. The
	// sandbox directive is what closes that, and it costs nothing for the PNGs
	// that make up everything else.
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox; base-uri 'none'")
	// Private, not public: a shared cache holding these would serve one
	// reader's article images to whoever asked next.
	h.Set("Cache-Control", "private, max-age="+strconv.Itoa(assetCacheMaxAge))
	if asset.ETag != "" {
		h.Set("ETag", asset.ETag)
		if match := r.Header.Get("If-None-Match"); match != "" && match == asset.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	h.Set("Content-Length", strconv.Itoa(len(asset.Bytes)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(asset.Bytes)
}

// AssetURL mints a capability for one absolute URL.
//
// Returns "" when the proxy is off or the URL is not something this endpoint
// can serve, which is the signal `rewrite` uses to leave the markup alone.
func (a *App) AssetURL(rawURL string) string {
	if a.assets == nil || rawURL == "" {
		return ""
	}
	// Already ours: minting a capability for our own endpoint would nest the
	// proxy inside itself, and it is what makes a second rewrite pass a no-op.
	if strings.HasPrefix(rawURL, a.assetPrefix()) {
		return ""
	}
	exp := time.Now().Add(assetTTL).Unix()
	sig := secret.Sign(a.assetKey, assetMessage(rawURL, exp))
	return a.assetPrefix() +
		"?u=" + base64.RawURLEncoding.EncodeToString([]byte(rawURL)) +
		"&e=" + strconv.FormatInt(exp, 10) +
		"&s=" + sig
}

// assetPrefix is where minted URLs point.
//
// Relative by default, so an instance reached over a tunnel, on a LAN address
// and through a hostname all work without configuration — and so the URL is
// short, which matters when a page carries forty of them. D20's separate proxy
// hostname replaces this with an absolute prefix; the shape of the URL does not
// change, only its origin.
func (a *App) assetPrefix() string {
	if a.cfg.ProxyOrigin != "" {
		return strings.TrimRight(a.cfg.ProxyOrigin, "/") + "/asset"
	}
	return "/asset"
}

// assetMessage is what gets signed.
//
// The expiry is inside the signature rather than beside it, which is the whole
// point: a client that could edit `e` without invalidating `s` would hold a
// permanent capability.
func assetMessage(rawURL string, exp int64) string {
	return "asset\n" + rawURL + "\n" + strconv.FormatInt(exp, 10)
}

// loadOrCreateAssetKey returns the instance's proxy-signing key, creating it on
// first run.
//
// A file beside the database rather than a row in it: the key is needed to
// verify a request that may arrive before anything else is warm, it must
// survive a restart (or every URL on an open page dies with the process), and a
// backup of the data directory should carry it. 0600 is the whole access
// control, which is the same bet §7.2 already makes about filesystem access
// being proof of ownership.
func loadOrCreateAssetKey(dir string) ([]byte, error) {
	return loadOrCreateKeyFile(dir, "proxy.key")
}

// loadOrCreateKeyFile is the shared body. Separate keys live in separate files
// so that one being absent, unreadable or rotated does not take another feature
// with it — see App.speechKey for the case that made this worth splitting.
func loadOrCreateKeyFile(dir, name string) ([]byte, error) {
	path := filepath.Join(dir, name)
	b, err := os.ReadFile(path)
	if err == nil && len(b) >= 32 {
		return b, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key, err := secret.NewKey()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}
