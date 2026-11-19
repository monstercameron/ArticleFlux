package grpcsrv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// cache_only, the member-readable consent flag, and the image proxy on a
// translation (§10.5b, §10.1a). The property under test throughout is the egress
// one: a cache-only lookup reaches no provider, whatever the key and the consent
// say, and spends nothing.

// countingTransport answers every request with a failure and counts it. Any
// count above zero is a request that was addressed to the provider.
type countingTransport struct{ n atomic.Int64 }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.n.Add(1)
	return nil, errors.New("countingTransport: no provider in this test")
}

// translationFixture is a SmartServer over a real database, one item the
// scope can see, and an llm client whose every outbound request is counted.
type translationFixture struct {
	s        *SmartServer
	repo     *store.ReaderRepo
	settings *store.SettingsRepo
	rt       *countingTransport
	sc       store.Scope
	itemID   string
	key      *atomic.Value // the OpenAI key the client resolves per call
}

const (
	fixtureItemURL  = "https://pub.example/a"
	fixtureItemHTML = `<p>Hallo Welt</p><img src="/pics/a.png">`
)

func newTranslationFixture(t *testing.T, role string) *translationFixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "tr.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	sc := ownerScope(role)
	repo := store.NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: sc.TenantID, Name: "Test", UserID: sc.UserID, Username: "u",
		Hash: "x", Role: role, Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	key := &atomic.Value{}
	key.Store("")
	rt := &countingTransport{}
	client := llm.NewWithTransport(func(context.Context) string { return key.Load().(string) }, rt)
	settings := store.NewSettingsRepo(db, nil)
	s := NewSmartServer(settings, client, smart.NewTranslator(client, settings),
		fixedScope(sc), slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.WithTheming(nil, repo)

	sub, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:pub.example/rss", FeedURL: "https://pub.example/rss",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.IngestItems(ctx, sub.SourceID, []store.IngestItem{{
		GUID: "g", URL: fixtureItemURL, Title: "Hallo", ContentHTML: fixtureItemHTML,
		PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	items, _, err := repo.ListItems(ctx, sc, store.ListQuery{Limit: 1})
	if err != nil || len(items) == 0 {
		t.Fatal(err)
	}
	return &translationFixture{s: s, repo: repo, settings: settings, rt: rt, sc: sc,
		itemID: items[0].ID, key: key}
}

// seedCached writes a cached French translation for the fixture's item as it
// stands. The source hash is computed the way internal/smart's articleHash
// computes it — title, NUL, sanitised body — because a row with any other hash
// is, correctly, a stale one. If that function changes, the hit tests below
// fail rather than pass vacuously: a stale row reads as a miss.
func (f *translationFixture) seedCached(t *testing.T) {
	t.Helper()
	orig := sanitize.HTML(fixtureItemHTML, sanitize.Feed)
	h := sha256.New()
	h.Write([]byte("Hallo"))
	h.Write([]byte{0})
	h.Write([]byte(orig))
	if err := f.repo.PutItemTranslation(context.Background(), f.sc, store.ItemTranslation{
		ItemID: f.itemID, Lang: "fr", SourceLang: "de",
		SourceHash: hex.EncodeToString(h.Sum(nil))[:32],
		Title:      "Bonjour", BodyHTML: `<p>Bonjour le monde</p><img src="/pics/a.png">`,
		Model: "test",
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *translationFixture) setConsent(t *testing.T, on bool) {
	t.Helper()
	v := "false"
	if on {
		v = "true"
	}
	if err := f.settings.SetSystemValue(context.Background(), smart.KeyTranslateArticles, v, ""); err != nil {
		t.Fatal(err)
	}
}

func (f *translationFixture) assertNothingLeft(t *testing.T) {
	t.Helper()
	if n := f.rt.n.Load(); n != 0 {
		t.Errorf("%d request(s) reached the provider", n)
	}
	if u := f.s.llm.Usage(); u.Requests != 0 || u.InputTokens != 0 {
		t.Errorf("usage moved: %+v", u)
	}
	if c := f.s.llm.Cost(); c.USD != 0 || c.Priced != 0 || c.Unpriced != 0 {
		t.Errorf("spend moved: %+v", c)
	}
}

func TestCacheOnlyAMissIsNotCachedAndReachesNoProvider(t *testing.T) {
	// The worst case for the promise: a key, consent on, force asked for. A
	// lookup that fell through to Translate here would be one request on the
	// counter.
	f := newTranslationFixture(t, "member")
	f.key.Store("sk-test-key")
	f.setConsent(t, true)

	res, err := f.s.TranslateArticle(context.Background(), &pb.TranslateArticleRequest{
		ItemId: f.itemID, Locale: "fr", CacheOnly: true, Force: true,
	})
	if err != nil {
		t.Fatalf("a cache miss is an answer, not an error: %v", err)
	}
	if !res.GetNotCached() || res.GetContentHtml() != "" || res.GetCached() {
		t.Errorf("miss: not_cached=%v cached=%v body=%q", res.GetNotCached(), res.GetCached(), res.GetContentHtml())
	}
	if res.GetItemId() != f.itemID || res.GetLocale() != "fr" {
		t.Errorf("miss names %q/%q", res.GetItemId(), res.GetLocale())
	}
	f.assertNothingLeft(t)

	// And the control: the same request without cache_only DOES try the
	// provider, so the counter above is measuring something.
	_, _ = f.s.TranslateArticle(context.Background(), &pb.TranslateArticleRequest{ItemId: f.itemID, Locale: "fr"})
	if f.rt.n.Load() == 0 {
		t.Fatal("the control request never reached the transport; the zero above proves nothing")
	}
}

func TestCacheOnlyAHitNeedsNoKeyAndNoConsent(t *testing.T) {
	f := newTranslationFixture(t, "member")
	f.seedCached(t)
	f.setConsent(t, false) // and no key: the fixture's key starts empty

	res, err := f.s.TranslateArticle(context.Background(), &pb.TranslateArticleRequest{
		ItemId: f.itemID, Locale: "fr", CacheOnly: true,
	})
	if err != nil {
		t.Fatalf("cached hit: %v", err)
	}
	if res.GetNotCached() || !res.GetCached() || res.GetTitle() != "Bonjour" ||
		!strings.Contains(res.GetContentHtml(), "Bonjour le monde") {
		t.Errorf("hit: %+v", res)
	}
	if res.GetOriginalTitle() != "Hallo" || !strings.Contains(res.GetOriginalContentHtml(), "Hallo Welt") {
		t.Errorf("the original did not ride along: %q / %q", res.GetOriginalTitle(), res.GetOriginalContentHtml())
	}
	f.assertNothingLeft(t)
}

func TestCacheOnlyIsNotRateLimited(t *testing.T) {
	f := newTranslationFixture(t, "member")
	for i := 0; i < 50; i++ {
		res, err := f.s.TranslateArticle(context.Background(), &pb.TranslateArticleRequest{
			ItemId: f.itemID, Locale: "fr", CacheOnly: true,
		})
		if err != nil || !res.GetNotCached() {
			t.Fatalf("lookup %d: %v %v", i, res, err)
		}
	}
	f.assertNothingLeft(t)
}

func TestCacheOnlyStillChecksVisibility(t *testing.T) {
	f := newTranslationFixture(t, "member")
	_, err := f.s.TranslateArticle(context.Background(), &pb.TranslateArticleRequest{
		ItemId: "not-theirs", Locale: "fr", CacheOnly: true,
	})
	if codeOf(err).String() != "NotFound" {
		t.Errorf("an invisible item through cache_only: %v", err)
	}
}

func TestTranslatedArticleImagesGoThroughTheProxy(t *testing.T) {
	f := newTranslationFixture(t, "member")
	f.seedCached(t)
	f.s.WithAssetProxy(stubMint)
	ctx := context.Background()

	res, err := f.s.TranslateArticle(ctx, &pb.TranslateArticleRequest{ItemId: f.itemID, Locale: "fr", CacheOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	// Relative to the ITEM's URL, as GetItem resolves it — not the feed's.
	want := `/asset?u=` + "https%3A%2F%2Fpub.example%2Fpics%2Fa.png"
	for name, body := range map[string]string{
		"translation": res.GetContentHtml(), "original": res.GetOriginalContentHtml(),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s was not proxied: %s", name, body)
		}
		if strings.Contains(body, `src="/pics/a.png"`) {
			t.Errorf("%s still points at the publisher: %s", name, body)
		}
	}

	// The reader's own opt-out is honoured here as on GetItem.
	if err := f.repo.SetPrefs(ctx, f.sc, store.Prefs{proxyImagesPref: "false"}); err != nil {
		t.Fatal(err)
	}
	res, err = f.s.TranslateArticle(ctx, &pb.TranslateArticleRequest{ItemId: f.itemID, Locale: "fr", CacheOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.GetContentHtml(), "/asset?u=") || !strings.Contains(res.GetContentHtml(), `/pics/a.png`) {
		t.Errorf("opted out, yet proxied: %s", res.GetContentHtml())
	}
}

func TestAMemberCanReadWhetherArticleTranslationIsOn(t *testing.T) {
	f := newTranslationFixture(t, "member")
	ctx := context.Background()
	read := func() bool {
		t.Helper()
		res, err := f.s.ListLanguages(ctx, &pb.ListLanguagesRequest{})
		if err != nil {
			t.Fatalf("a member could not list languages: %v", err)
		}
		if len(res.GetLanguages()) != len(smart.Languages) {
			t.Errorf("got %d languages, want %d", len(res.GetLanguages()), len(smart.Languages))
		}
		return res.GetTranslateArticlesEnabled()
	}
	if read() {
		t.Error("fresh instance reports article translation on")
	}
	f.setConsent(t, true)
	// Consent without a key: a press would be refused, so this says off.
	if read() {
		t.Error("consent with no key reports on")
	}
	f.key.Store("sk-test-key")
	if !read() {
		t.Error("consent and a key reports off")
	}
	f.setConsent(t, false)
	if read() {
		t.Error("a key without consent reports on")
	}
	f.assertNothingLeft(t)
}
