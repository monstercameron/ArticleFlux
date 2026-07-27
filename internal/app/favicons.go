package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/favicon"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// faviconMaxAge is what the browser is told. Thirty days, matching the server's
// own TTL — telling the browser to cache for less would mean re-requesting an
// icon we would only serve from cache anyway.
const faviconMaxAge = int(favicon.TTL / time.Second)

// transparentPNG is a 1x1 transparent PNG, served for hosts with no icon.
//
// A 200 with a blank pixel rather than a 404, because a 404 is not cacheable in
// the same way and the browser will re-ask on every render of every row. The
// negative result is a result, and it should cost one request per month like any
// other.
var transparentPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0, 0, 0, 0x0d, 'I', 'H', 'D', 'R',
	0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89,
	0, 0, 0, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
	0x0d, 0x0a, 0x2d, 0xb4,
	0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

// serveFavicon returns a cached site icon, fetching it on a miss.
//
// GET /favicon?host=example.com
//
// Fetch-on-miss rather than fetch-on-subscribe: a subscription list imported in
// bulk would otherwise make 144 outbound requests before showing anything, and
// most of those icons are for feeds the user will not look at today. The warmer
// fills the rest in the background.
func (a *App) serveFavicon(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("host")))
	if host == "" || len(host) > 255 || strings.ContainsAny(host, `/\ `) {
		http.Error(w, "bad host", http.StatusBadRequest)
		return
	}

	row, err := a.repo.GetFavicon(r.Context(), host)
	fresh := err == nil && time.Since(row.FetchedAt) < favicon.TTL

	if !fresh && (err != nil || row.Failures < favicon.MaxFailures) {
		// Bounded: this runs inside a request, so a slow site must not hold the
		// connection open for the client's whole timeout.
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		row = a.fetchFavicon(ctx, host, row)
	}

	// Cached for a month by the browser too, so a returning reader makes no
	// request at all for icons it already has.
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(faviconMaxAge)+", immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Defense in depth, matching serveAsset (internal/app/asset.go): the
	// content-type this endpoint serves is a label we ourselves derived by
	// sniffing bytes at fetch time (internal/favicon.sniffAllowedImage), not
	// something the remote site gets to choose — but this header costs
	// nothing and means a mistake in that trust chain, now or in a future
	// change, still cannot turn into a document loaded with this origin's
	// authority. Harmless for the PNG/ICO/GIF/JPEG/WebP bytes that make up
	// everything this endpoint actually serves.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox; base-uri 'none'")

	if len(row.Bytes) == 0 {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(transparentPNG)
		return
	}
	if row.ETag != "" {
		w.Header().Set("ETag", row.ETag)
		if r.Header.Get("If-None-Match") == row.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	ct := row.ContentType
	if ct == "" {
		ct = "image/png"
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(row.Bytes)
}

// fetchFavicon fetches and stores an icon, recording failure as a real outcome.
func (a *App) fetchFavicon(ctx context.Context, host string, prev store.FaviconRow) store.FaviconRow {
	icon, err := a.icons.Fetch(ctx, "https://"+host)
	next := store.FaviconRow{Host: host, FetchedAt: time.Now().UTC()}
	if err != nil {
		// Failure is recorded, not discarded: it is what stops us asking a site
		// with no icon on every single page load.
		next.Failures = prev.Failures + 1
		// Keep whatever we had; a stale icon beats a blank one.
		next.Bytes, next.ContentType, next.ETag = prev.Bytes, prev.ContentType, prev.ETag
	} else {
		next.Bytes, next.ContentType, next.ETag = icon.Bytes, icon.ContentType, icon.ETag
	}
	if perr := a.repo.PutFavicon(ctx, next); perr != nil {
		a.log.Warn("favicon cache write", "host", host, "err", perr)
	}
	return next
}

// WarmFavicons fetches icons for subscribed sources that have none or whose
// cache has expired.
//
// Background and bounded. Icons are a nicety: they must never compete with feed
// polling for the connection budget, and a cold start with 150 sources should
// not open 150 sockets.
func (a *App) WarmFavicons(ctx context.Context, limit int) {
	hosts, err := a.repo.SourceHosts(ctx, limit)
	if err != nil {
		a.log.Warn("favicon warm", "err", err)
		return
	}
	seen := map[string]bool{}
	done := 0
	for _, raw := range hosts {
		h := favicon.Host(raw)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true

		row, err := a.repo.GetFavicon(ctx, h)
		if err == nil && time.Since(row.FetchedAt) < favicon.TTL {
			continue
		}
		if err == nil && row.Failures >= favicon.MaxFailures {
			continue
		}
		if !errors.Is(err, store.ErrNotFound) && err != nil {
			continue
		}

		fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		a.fetchFavicon(fctx, h, row)
		cancel()

		done++
		if done >= limit {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(150 * time.Millisecond):
			// Paced deliberately. A burst of 150 requests from one IP is what a
			// CDN treats as an attack, and there is no hurry: these are icons.
		}
	}
}
