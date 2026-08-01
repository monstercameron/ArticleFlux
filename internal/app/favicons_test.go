package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/favicon"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// GET /favicon (§9's icon cache). AllowPrivateFeeds stays off in these fixtures
// so a fetch attempt against a loopback host is refused by netguard before any
// socket opens — deterministic and fast, unlike a real network round trip.

func faviconApp(t *testing.T) *App {
	t.Helper()
	a, err := Open(t.Context(), Config{
		DBPath: filepath.Join(t.TempDir(), "favicons.db"), DevMode: true, Log: testLogger(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestServeFaviconRejectsABadHost(t *testing.T) {
	a := faviconApp(t)
	for _, host := range []string{"", "has a space.example", "has/slash.example", "has\\backslash.example"} {
		rec := httptest.NewRecorder()
		a.serveFavicon(rec, httptest.NewRequest(http.MethodGet, "/favicon?host="+url.QueryEscape(host), nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("host=%q: status %d, want 400", host, rec.Code)
		}
	}
}

// A fresh cached row is served without a fetch, and a matching If-None-Match
// gets a 304 rather than the bytes again.
func TestServeFaviconServesAFreshRowAndHonoursETag(t *testing.T) {
	a := faviconApp(t)
	if err := a.Repo().PutFavicon(context.Background(), store.FaviconRow{
		Host: "cached.example", Bytes: pngBytes, ContentType: "image/png", ETag: `"abc"`,
	}); err != nil {
		t.Fatalf("PutFavicon: %v", err)
	}

	rec := httptest.NewRecorder()
	a.serveFavicon(rec, httptest.NewRequest(http.MethodGet, "/favicon?host=cached.example", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != len(pngBytes) {
		t.Errorf("got %d bytes, want %d — a fresh row must not be refetched", rec.Body.Len(), len(pngBytes))
	}
	if got := rec.Header().Get("Cache-Control"); got == "" {
		t.Error("missing Cache-Control")
	}

	req := httptest.NewRequest(http.MethodGet, "/favicon?host=cached.example", nil)
	req.Header.Set("If-None-Match", `"abc"`)
	rec2 := httptest.NewRecorder()
	a.serveFavicon(rec2, req)
	if rec2.Code != http.StatusNotModified {
		t.Errorf("status %d, want 304", rec2.Code)
	}
}

// A host cached with no bytes — the negative result — serves the transparent
// pixel rather than a 404, which lets the browser cache the absence for a
// month like any other answer.
func TestServeFaviconWithNoIconServesTheTransparentPixel(t *testing.T) {
	a := faviconApp(t)
	if err := a.Repo().PutFavicon(context.Background(), store.FaviconRow{Host: "noicon.example"}); err != nil {
		t.Fatalf("PutFavicon: %v", err)
	}

	rec := httptest.NewRecorder()
	a.serveFavicon(rec, httptest.NewRequest(http.MethodGet, "/favicon?host=noicon.example", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Body.Len() != len(transparentPNG) {
		t.Errorf("got %d bytes, want the %d-byte transparent pixel", rec.Body.Len(), len(transparentPNG))
	}
}

// A host with no cached row at all triggers a fetch attempt. AllowPrivateFeeds
// is off, so netguard refuses the loopback address before any connection is
// attempted, and the miss still resolves to a 200 with the transparent pixel —
// the failure is recorded rather than surfaced as an error to the reader.
func TestServeFaviconOnACacheMissAttemptsAFetchAndDegradesCleanly(t *testing.T) {
	a := faviconApp(t)

	rec := httptest.NewRecorder()
	a.serveFavicon(rec, httptest.NewRequest(http.MethodGet, "/favicon?host=127.0.0.1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 with a placeholder rather than an error", rec.Code)
	}
	if rec.Body.Len() != len(transparentPNG) {
		t.Errorf("got %d bytes, want the transparent pixel for a host that could not be fetched", rec.Body.Len())
	}

	row, err := a.Repo().GetFavicon(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatalf("GetFavicon: %v", err)
	}
	if row.Failures == 0 {
		t.Error("a failed fetch must be recorded, or the endpoint retries every publisher forever")
	}
}

// fetchFavicon on a failure keeps whatever icon was already cached rather than
// discarding it — a stale icon beats a blank one.
func TestFetchFaviconKeepsTheStaleIconOnFailure(t *testing.T) {
	a := faviconApp(t)
	a.icons = favicon.New(false) // AllowPrivateFeeds off: 127.0.0.1 is refused fast

	prev := store.FaviconRow{
		Host: "127.0.0.1", Bytes: []byte("old-icon"), ContentType: "image/x-icon",
		ETag: `"old"`, Failures: 2,
	}
	got := a.fetchFavicon(context.Background(), "127.0.0.1", prev)

	if got.Failures != 3 {
		t.Errorf("Failures = %d, want 3 (incremented once)", got.Failures)
	}
	if string(got.Bytes) != "old-icon" || got.ContentType != "image/x-icon" || got.ETag != `"old"` {
		t.Errorf("a failed fetch discarded the stale icon: %+v", got)
	}
	// And it must actually have been written back, or the next request pays
	// for the same fetch again with no memory of this one.
	stored, err := a.Repo().GetFavicon(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatalf("GetFavicon: %v", err)
	}
	if stored.Failures != 3 {
		t.Errorf("stored Failures = %d, want 3", stored.Failures)
	}
}

// A cache write that fails must not crash the fetch — it costs a repeated
// fetch next time, which is a smaller loss than a panicking favicon warmer.
func TestFetchFaviconSurvivesACacheWriteFailure(t *testing.T) {
	a := faviconApp(t)
	if err := a.DB().Close(); err != nil {
		t.Fatalf("closing the db early: %v", err)
	}
	// Must not panic even though PutFavicon will now fail.
	got := a.fetchFavicon(context.Background(), "127.0.0.1", store.FaviconRow{})
	if got.Host != "127.0.0.1" {
		t.Errorf("Host = %q", got.Host)
	}
}

// WarmFavicons with no subscriptions has nothing to warm and must return
// promptly rather than blocking on a source list that will never arrive.
func TestWarmFaviconsWithNoSubscriptionsIsANoop(t *testing.T) {
	a := faviconApp(t)
	done := make(chan struct{})
	go func() { a.WarmFavicons(context.Background(), 25); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("WarmFavicons did not return with no subscriptions to warm")
	}
}

// A host whose icon is already fresh must be skipped rather than refetched —
// warming is background best-effort work and must not undo a fetch nothing
// asked it to repeat.
func TestWarmFaviconsSkipsAnAlreadyFreshHost(t *testing.T) {
	a := faviconApp(t)
	sc, err := a.EnsureDevUser(context.Background(), "cam", "articleflux-warm-test")
	if err != nil {
		t.Fatalf("dev user: %v", err)
	}
	if _, _, err := a.Repo().Subscribe(context.Background(), sc, store.NewSubscription{
		NaturalKey: "feed:warm", FeedURL: "https://warm.example/feed", Title: "Warm fixture",
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := a.Repo().PutFavicon(context.Background(), store.FaviconRow{
		Host: "warm.example", Bytes: pngBytes, ContentType: "image/png", ETag: `"fresh"`,
	}); err != nil {
		t.Fatalf("PutFavicon: %v", err)
	}

	a.WarmFavicons(context.Background(), 25)

	row, err := a.Repo().GetFavicon(context.Background(), "warm.example")
	if err != nil {
		t.Fatalf("GetFavicon: %v", err)
	}
	if row.ETag != `"fresh"` {
		t.Errorf("ETag = %q, want the untouched fresh row — WarmFavicons refetched a host that did not need it", row.ETag)
	}
}

// A host that has already failed MaxFailures times must be skipped too — a
// site with no icon will not start working because the warmer asked again.
func TestWarmFaviconsSkipsAHostThatHasFailedTooManyTimes(t *testing.T) {
	a := faviconApp(t)
	sc, err := a.EnsureDevUser(context.Background(), "cam", "articleflux-warm-test")
	if err != nil {
		t.Fatalf("dev user: %v", err)
	}
	if _, _, err := a.Repo().Subscribe(context.Background(), sc, store.NewSubscription{
		NaturalKey: "feed:warmfail", FeedURL: "https://warmfail.example/feed", Title: "Warm-fail fixture",
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	// Stale (past TTL) but already at the failure ceiling.
	if err := a.Repo().PutFavicon(context.Background(), store.FaviconRow{
		Host: "warmfail.example", Failures: favicon.MaxFailures,
		FetchedAt: time.Now().UTC().Add(-60 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("PutFavicon: %v", err)
	}

	a.WarmFavicons(context.Background(), 25)

	row, err := a.Repo().GetFavicon(context.Background(), "warmfail.example")
	if err != nil {
		t.Fatalf("GetFavicon: %v", err)
	}
	if row.Failures != favicon.MaxFailures {
		t.Errorf("Failures = %d, want it left at the ceiling %d — a site that has failed "+
			"enough times must not be asked again", row.Failures, favicon.MaxFailures)
	}
}

// The limit bounds how many hosts get fetched in one pass — a cold start with
// many subscriptions must not open a socket per source.
func TestWarmFaviconsStopsAtTheLimit(t *testing.T) {
	a := faviconApp(t)
	sc, err := a.EnsureDevUser(context.Background(), "cam", "articleflux-warm-test")
	if err != nil {
		t.Fatalf("dev user: %v", err)
	}
	for i, host := range []string{"127.0.0.1", "127.0.0.2"} {
		if _, _, err := a.Repo().Subscribe(context.Background(), sc, store.NewSubscription{
			NaturalKey: "feed:limit" + host, FeedURL: "https://" + host + "/feed",
			SiteURL: "https://" + host + "/", Title: "Limit fixture " + host,
		}); err != nil {
			t.Fatalf("subscribe %d: %v", i, err)
		}
	}

	a.WarmFavicons(context.Background(), 1)

	fetched := 0
	for _, host := range []string{"127.0.0.1", "127.0.0.2"} {
		if _, err := a.Repo().GetFavicon(context.Background(), host); err == nil {
			fetched++
		}
	}
	if fetched != 1 {
		t.Errorf("WarmFavicons(limit=1) attempted %d hosts, want exactly 1", fetched)
	}
}
