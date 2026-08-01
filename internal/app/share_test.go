package app

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// `/pub/:slug` (§7.8b, TODO F29).
//
// The properties that matter here are the ones a mistake would make permanent:
// what leaves the building (excerpt only), who can ask (anyone with the
// address), what happens when the owner changes their mind (revocation and
// rotation), and whether a crawler is invited (it is not).

// sharedInstance is an app with one subscribed feed, one article, and that
// feed's folder published.
func sharedInstance(t *testing.T) (*App, *httptest.Server, store.Share, store.Scope) {
	t.Helper()
	a, err := Open(t.Context(), Config{
		DBPath:       filepath.Join(t.TempDir(), "share.db"),
		PollInterval: 0,
		DevMode:      true,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	sc, err := a.EnsureDevUser(t.Context(), "cam", "articleflux-share-test")
	if err != nil {
		t.Fatalf("dev user: %v", err)
	}

	folder, err := a.Repo().CreateFolder(t.Context(), sc, "Systems")
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	feed, _, err := a.Repo().Subscribe(t.Context(), sc, store.NewSubscription{
		NaturalKey: "feed:pub", FeedURL: "https://pub.example/feed",
		Title: "Pub fixture", FolderID: folder.ID,
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if _, err := a.Repo().IngestItems(t.Context(), feed.SourceID, []store.IngestItem{{
		GUID: "g1", URL: "https://pub.example/one", DupeKey: "d1",
		Title:   "The article that was shared",
		Summary: "<p>A summary the publisher wrote.</p>",
		// The full text, which must NEVER appear in the feed.
		ContentHTML: "<p>THE-WHOLE-ARTICLE-BODY, extracted and stored.</p>",
		PublishedAt: time.Now().UTC().Add(-time.Hour),
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	sh, err := a.Repo().CreateShare(t.Context(), sc, store.ShareFolder, folder.ID, "My systems reading")
	if err != nil {
		t.Fatalf("share: %v", err)
	}

	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return a, srv, sh, sc
}

func TestAPublishedScopeIsAFeedAnybodyCanRead(t *testing.T) {
	_, srv, sh, _ := sharedInstance(t)

	// No credential of any kind. That is the entire point: the reader
	// subscribing to this will never sign in.
	res, err := http.Get(srv.URL + "/pub/" + sh.Slug)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/atom+xml") {
		t.Errorf("content type = %q, which no feed reader will recognise", ct)
	}

	var feed struct {
		Title   string `xml:"title"`
		Entries []struct {
			Title   string `xml:"title"`
			Summary string `xml:"summary"`
		} `xml:"entry"`
	}
	if err := xml.NewDecoder(res.Body).Decode(&feed); err != nil {
		t.Fatalf("the feed is not parseable XML: %v", err)
	}
	if feed.Title != "My systems reading" {
		t.Errorf("feed title = %q", feed.Title)
	}
	if len(feed.Entries) != 1 {
		t.Fatalf("got %d entries, want the one article", len(feed.Entries))
	}
	if !strings.Contains(feed.Entries[0].Summary, "A summary the publisher wrote") {
		t.Errorf("the entry carries no summary: %q", feed.Entries[0].Summary)
	}
}

// The one thing that must never leak: the full text.
//
// §7.8b makes this permanent rather than a default — republishing somebody
// else's article under your own name is a licensing decision, and an RSS
// endpoint should not be making it on a reader's behalf.
func TestAPublishedFeedNeverCarriesTheFullArticle(t *testing.T) {
	_, srv, sh, _ := sharedInstance(t)

	res, err := http.Get(srv.URL + "/pub/" + sh.Slug)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body := make([]byte, 64*1024)
	n, _ := res.Body.Read(body)
	if strings.Contains(string(body[:n]), "THE-WHOLE-ARTICLE-BODY") {
		t.Error("the extracted article body was published; the feed is excerpt-only " +
			"and that is a licensing decision, not a setting")
	}
}

// A crawler is the one visitor that turns "hard to find" into "listed on
// Google" without anybody meaning to.
func TestAShareTellsCrawlersToStayAwayUnlessInvited(t *testing.T) {
	a, srv, sh, sc := sharedInstance(t)

	res, err := http.Get(srv.URL + "/pub/" + sh.Slug)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got := res.Header.Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Errorf("X-Robots-Tag = %q on a share nobody opted in for", got)
	}

	if err := a.Repo().SetShareIndexable(context.Background(), sc, sh.ID, true); err != nil {
		t.Fatal(err)
	}
	res2, err := http.Get(srv.URL + "/pub/" + sh.Slug)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if got := res2.Header.Get("X-Robots-Tag"); strings.Contains(got, "noindex") {
		t.Errorf("X-Robots-Tag = %q after the owner opted in", got)
	}
}

// Revocation and rotation have to be real from the outside, not just in the
// database — an unpublished scope must be unreachable rather than unlinked.
func TestRevokingAndRotatingChangeWhatTheWorldCanReach(t *testing.T) {
	a, srv, sh, sc := sharedInstance(t)

	next, err := a.Repo().RotateShare(context.Background(), sc, sh.ID)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	old, err := http.Get(srv.URL + "/pub/" + sh.Slug)
	if err != nil {
		t.Fatal(err)
	}
	old.Body.Close()
	if old.StatusCode != http.StatusNotFound {
		t.Errorf("the old address answered %d after a rotation", old.StatusCode)
	}

	live, err := http.Get(srv.URL + "/pub/" + next)
	if err != nil {
		t.Fatal(err)
	}
	live.Body.Close()
	if live.StatusCode != http.StatusOK {
		t.Fatalf("the new address answered %d", live.StatusCode)
	}

	if err := a.Repo().RevokeShare(context.Background(), sc, sh.ID); err != nil {
		t.Fatal(err)
	}
	gone, err := http.Get(srv.URL + "/pub/" + next)
	if err != nil {
		t.Fatal(err)
	}
	gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Errorf("a revoked share answered %d — unpublished has to mean unreachable", gone.StatusCode)
	}
}

// A feed reader polls this forever, so it has to be able to ask "anything new?"
// cheaply.
func TestAFeedReaderCanPollWithoutRefetching(t *testing.T) {
	_, srv, sh, _ := sharedInstance(t)

	first, err := http.Get(srv.URL + "/pub/" + sh.Slug)
	if err != nil {
		t.Fatal(err)
	}
	first.Body.Close()
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag, so every poll re-downloads the whole feed")
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/pub/"+sh.Slug, nil)
	req.Header.Set("If-None-Match", etag)
	second, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusNotModified {
		t.Errorf("a conditional poll got %d, want 304", second.StatusCode)
	}
}

// An address that never existed and one that was revoked answer identically.
// Any difference is a free oracle for "was this ever real", which is the one
// question somebody holding a leaked URL would like answered.
func TestAnUnknownAddressLooksExactlyLikeARevokedOne(t *testing.T) {
	_, srv, _, _ := sharedInstance(t)

	res, err := http.Get(srv.URL + "/pub/nosuchsharenosuchshare00")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d for an address that never existed", res.StatusCode)
	}
}

// Only GET and HEAD are meaningful for a feed. Anything else must name what it
// accepts rather than just refusing.
func TestServePublicShareRejectsOtherMethods(t *testing.T) {
	_, srv, sh, _ := sharedInstance(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/pub/"+sh.Slug, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", res.StatusCode)
	}
	if got := res.Header.Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q", got)
	}
}

// An empty slug or one that tries to smuggle a second path segment must 404
// before ever reaching the store.
func TestServePublicShareRejectsAMalformedSlug(t *testing.T) {
	_, srv, _, _ := sharedInstance(t)

	for _, path := range []string{"/pub/", "/pub/one/two"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, res.StatusCode)
		}
	}
}

// A HEAD request answers with the same headers and no body, so a poller can
// check freshness without paying for the document.
func TestServePublicShareHeadCarriesNoBody(t *testing.T) {
	_, srv, sh, _ := sharedInstance(t)

	req, _ := http.NewRequest(http.MethodHead, srv.URL+"/pub/"+sh.Slug, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if len(body) != 0 {
		t.Errorf("HEAD returned %d bytes of body", len(body))
	}
	if res.Header.Get("ETag") == "" {
		t.Error("HEAD is missing the ETag a conditional GET would need")
	}
}

// publicURL follows the request, not a hardcoded host — a self-hosted app
// whose feed always said "http://localhost" would be wrong for everyone but
// its author.
func TestPublicURLReflectsTheRequestScheme(t *testing.T) {
	a := &App{}

	plain := httptest.NewRequest(http.MethodGet, "http://reader.example/pub/x", nil)
	plain.Host = "reader.example"
	if got := a.publicURL(plain); got != "http://reader.example" {
		t.Errorf("plain = %q", got)
	}

	forwarded := httptest.NewRequest(http.MethodGet, "http://reader.example/pub/x", nil)
	forwarded.Host = "reader.example"
	forwarded.Header.Set("X-Forwarded-Proto", "https")
	if got := a.publicURL(forwarded); got != "https://reader.example" {
		t.Errorf("forwarded = %q, want https since the proxy said so", got)
	}
}

// The feed's `updated` tracks the newest item rather than the share's creation
// time, so a subscriber's reader can tell a share is still alive.
func TestBuildShareFeedUpdatedTracksTheNewestItem(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	ps := store.PublishedShare{Share: store.Share{Slug: "abc", Title: "T", CreatedAt: created}}
	items := []store.Item{{ID: "i1", PublishedAt: newer.Format(time.RFC3339Nano)}}

	feed := buildShareFeed(ps, items, "https://reader.example")
	if feed.Updated != newer.UTC().Format(time.RFC3339) {
		t.Errorf("Updated = %q, want the newest item's date %q", feed.Updated, newer.UTC().Format(time.RFC3339))
	}

	// An item older than the share's creation must not move `updated` backwards.
	older := created.Add(-time.Hour)
	itemsOld := []store.Item{{ID: "i2", PublishedAt: older.Format(time.RFC3339Nano)}}
	feedOld := buildShareFeed(ps, itemsOld, "https://reader.example")
	if feedOld.Updated != created.UTC().Format(time.RFC3339) {
		t.Errorf("Updated = %q, an older item must not move it back", feedOld.Updated)
	}
}

// An entry carries its author when the item has one — bylines are part of what
// makes a shared feed worth reading.
func TestBuildShareFeedIncludesTheAuthorWhenPresent(t *testing.T) {
	ps := store.PublishedShare{Share: store.Share{Slug: "abc", Title: "T", CreatedAt: time.Now().UTC()}}
	items := []store.Item{{ID: "i1", Title: "Piece", Author: "J. Writer"}}
	feed := buildShareFeed(ps, items, "https://reader.example")
	if len(feed.Entries) != 1 {
		t.Fatalf("got %d entries", len(feed.Entries))
	}
	if feed.Entries[0].Author == nil || feed.Entries[0].Author.Name != "J. Writer" {
		t.Errorf("author = %+v, want J. Writer", feed.Entries[0].Author)
	}

	unattributed := buildShareFeed(ps, []store.Item{{ID: "i2", Title: "No byline"}}, "https://reader.example")
	if unattributed.Entries[0].Author != nil {
		t.Errorf("author = %+v, want none for an item with no byline", unattributed.Entries[0].Author)
	}
}

// A timestamp that will not parse falls back to the share's creation time
// rather than "now" — an entry whose date moves on every fetch looks new on
// every poll, which is how a reader is notified about the same article forever.
func TestAtomTimeFallsBackOnAnUnparseableTimestamp(t *testing.T) {
	fallback := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if got := atomTime("not a timestamp", fallback); got != fallback.UTC().Format(time.RFC3339) {
		t.Errorf("atomTime = %q, want the fallback", got)
	}
	good := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if got := atomTime(good.Format(time.RFC3339Nano), fallback); got != good.UTC().Format(time.RFC3339) {
		t.Errorf("atomTime = %q, want the parsed value", got)
	}
}
