package smart

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/schemaflux/v2"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Article translation (plan.md §10.5b): what leaves, what comes back, who may
// see it, and when it costs nothing.

const articleHTML = `<h2>Ein Titel</h2>` +
	`<p>Hallo <a href="https://pub.example/a?utm_source=feed">schöne Welt</a> und <code>rm -rf /</code>.</p>` +
	`<ul><li>Eins</li><li>Zwei</li></ul>` +
	`<p><img src="https://pub.example/i.png" alt="Bild"> 2026</p>`

type articleFixture struct {
	at       *ArticleTranslator
	fake     *fakeLLM
	repo     *store.ReaderRepo
	settings *store.SettingsRepo
	owner    store.Scope
	stranger store.Scope
	itemID   string
}

func newArticleFixture(t *testing.T, enabled bool) *articleFixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "a.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, nt := range []store.NewTenant{
		{TenantID: "t1", Name: "One", UserID: "u1", Username: "reader", Hash: "x", Role: "member", Now: now},
		{TenantID: "t2", Name: "Two", UserID: "u2", Username: "other", Hash: "x", Role: "admin", Now: now},
	} {
		if err := repo.CreateTenantAndUser(ctx, nt); err != nil {
			t.Fatal(err)
		}
	}
	f := &articleFixture{
		repo: repo, settings: store.NewSettingsRepo(db, nil),
		owner:    store.Scope{TenantID: "t1", UserID: "u1", Role: "member"},
		stranger: store.Scope{TenantID: "t2", UserID: "u2", Role: "admin"},
		fake:     &fakeLLM{configured: true},
	}
	sub, _, err := repo.Subscribe(ctx, f.owner, store.NewSubscription{
		NaturalKey: "feed:pub.example/rss", FeedURL: "https://pub.example/rss", Title: "Die Zeitung",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.IngestItems(ctx, sub.SourceID, []store.IngestItem{{
		GUID: "g1", URL: "https://pub.example/a", Title: "Guten Morgen", Author: "Erika Mustermann",
		ContentHTML: articleHTML, PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	items, _, err := repo.ListItems(ctx, f.owner, store.ListQuery{Limit: 1})
	if err != nil || len(items) == 0 {
		t.Fatalf("no item: %v", err)
	}
	f.itemID = items[0].ID
	if enabled {
		if err := f.settings.SetSystemValue(ctx, KeyTranslateArticles, "true", ""); err != nil {
			t.Fatal(err)
		}
	}
	f.at = NewArticleTranslator(f.fake, f.settings, repo)
	return f
}

// payloadOf recovers the TranslatePayload a request carried.
func payloadOf(t *testing.T, r schemaflux.Request) (llm.TranslatePayload, []byte) {
	t.Helper()
	in := r.System + r.User
	i := strings.Index(in, "Article:\n")
	if i < 0 {
		t.Fatalf("no payload marker in %q", in)
	}
	dec := json.NewDecoder(strings.NewReader(in[i+len("Article:\n"):]))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		t.Fatal(err)
	}
	var p llm.TranslatePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p, raw
}

// englishReply answers every segment with "EN[" + text + "]", except that it
// tries to smuggle markup into one.
func englishReply(t *testing.T) func(int, schemaflux.Request) (string, error) {
	return func(_ int, r schemaflux.Request) (string, error) {
		p, _ := payloadOf(t, r)
		type seg struct {
			ID   int    `json:"id"`
			Text string `json:"text"`
		}
		out := struct {
			Title    string `json:"title,omitempty"`
			Segments []seg  `json:"segments"`
		}{}
		if p.Title != "" {
			out.Title = "Good morning"
		}
		for _, s := range p.Segments {
			v := "EN[" + s.Text + "]"
			if s.Text == "Zwei" {
				v = `<script>alert(1)</script><a href="javascript:x">Two</a>`
			}
			out.Segments = append(out.Segments, seg{ID: s.ID, Text: v})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
}

func TestArticleStructureSurvivesAndModelMarkupIsNeverTrusted(t *testing.T) {
	f := newArticleFixture(t, true)
	answering(t, englishReply(t))

	got, err := f.at.Translate(context.Background(), f.owner, f.itemID, "en", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Good morning" || got.OriginalTitle != "Guten Morgen" {
		t.Errorf("titles: %q / %q", got.Title, got.OriginalTitle)
	}
	for _, want := range []string{"<h2>", "<ul>", "<li>", "EN[Ein Titel]", "EN[schöne Welt]",
		`href="https://pub.example/a?utm_source=feed"`, `src="https://pub.example/i.png"`,
		"<code>rm -rf /</code>", "2026"} {
		if !strings.Contains(got.BodyHTML, want) {
			t.Errorf("translated body lost %q:\n%s", want, got.BodyHTML)
		}
	}
	// The model's markup arrived as TEXT: escaped, inert, and the link it tried
	// to add does not exist.
	if strings.Contains(got.BodyHTML, "<script") || !strings.Contains(got.BodyHTML, "&lt;script&gt;") {
		t.Errorf("model markup was not reduced to escaped text:\n%s", got.BodyHTML)
	}
	if strings.Count(got.BodyHTML, "<a ") != strings.Count(articleHTML, "<a ") {
		t.Errorf("the link count changed:\n%s", got.BodyHTML)
	}
	if !strings.Contains(got.OriginalHTML, "schöne Welt") {
		t.Error("the original was not returned beside the translation")
	}
	// Whitespace around a segment is the original's, not the model's.
	if !strings.Contains(got.BodyHTML, `EN[Hallo] <a`) {
		t.Errorf("the space before the link was lost:\n%s", got.BodyHTML)
	}
}

func TestArticleEgressIsTextSegmentsOnly(t *testing.T) {
	f := newArticleFixture(t, true)
	prov := answering(t, englishReply(t))
	if _, err := f.at.Translate(context.Background(), f.owner, f.itemID, "fr", false); err != nil {
		t.Fatal(err)
	}
	if prov.CallCount() == 0 {
		t.Fatal("nothing was sent")
	}
	for i, r := range prov.Requests() {
		p, raw := payloadOf(t, r)
		if bad, err := llm.AuditTranslate(raw); err != nil || len(bad) != 0 {
			t.Errorf("request %d carried keys §10.5b does not permit: %v %v", i, bad, err)
		}
		whole := r.System + r.User
		for _, never := range []string{"https://", "pub.example", "utm_source", f.itemID,
			"Erika", "Die Zeitung", "<a", "<p>", "rm -rf", "u1", "t1"} {
			if strings.Contains(whole, never) {
				t.Errorf("request %d sent %q, which §10.5b forbids", i, never)
			}
		}
		for _, s := range p.Segments {
			if strings.ContainsAny(s.Text, "<>") {
				t.Errorf("a segment carried markup: %q", s.Text)
			}
		}
	}
}

func TestACachedArticleCostsNothingAndNeedsNoKeyOrConsent(t *testing.T) {
	f := newArticleFixture(t, true)
	prov := answering(t, englishReply(t))
	ctx := context.Background()
	if _, err := f.at.Translate(ctx, f.owner, f.itemID, "fr", false); err != nil {
		t.Fatal(err)
	}
	calls := prov.CallCount()

	// The key goes and the consent is withdrawn. A cached answer still sends
	// nothing, so it is still served.
	f.fake.configured = false
	_ = f.settings.SetSystemValue(ctx, KeyTranslateArticles, "false", "")
	got, err := f.at.Translate(ctx, f.owner, f.itemID, "fr", false)
	if err != nil || !got.Cached {
		t.Fatalf("cached: %+v, %v", got, err)
	}
	if prov.CallCount() != calls {
		t.Errorf("a cache hit made %d calls", prov.CallCount()-calls)
	}
}

func TestNoArticleLeavesWithoutConsentOrKey(t *testing.T) {
	f := newArticleFixture(t, false)
	prov := answering(t, englishReply(t))
	ctx := context.Background()
	if _, err := f.at.Translate(ctx, f.owner, f.itemID, "fr", false); !errors.Is(err, ErrTranslateOff) {
		t.Errorf("consent off: err = %v", err)
	}
	_ = f.settings.SetSystemValue(ctx, KeyTranslateArticles, "true", "")
	f.fake.configured = false
	if _, err := f.at.Translate(ctx, f.owner, f.itemID, "fr", false); !errors.Is(err, llm.ErrNotConfigured) {
		t.Errorf("no key: err = %v", err)
	}
	if prov.CallCount() != 0 {
		t.Errorf("%d calls without consent and key", prov.CallCount())
	}
}

func TestAReaderCannotTranslateAnArticleTheyCannotSee(t *testing.T) {
	f := newArticleFixture(t, true)
	prov := answering(t, englishReply(t))
	ctx := context.Background()
	if _, err := f.at.Translate(ctx, f.owner, f.itemID, "fr", false); err != nil {
		t.Fatal(err)
	}
	calls := prov.CallCount()
	// Another tenant — an ADMIN of theirs, which is no help across tenants —
	// can neither read the cached translation nor buy a new one.
	for _, force := range []bool{false, true} {
		if _, err := f.at.Translate(ctx, f.stranger, f.itemID, "fr", force); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("force=%v: a stranger got %v, want ErrNotFound", force, err)
		}
	}
	if prov.CallCount() != calls {
		t.Error("a stranger's request reached the model")
	}
}

func TestANoOpTranslationIsRefused(t *testing.T) {
	f := newArticleFixture(t, true)
	prov := answering(t, englishReply(t))
	ctx := context.Background()
	// The analyzer says English; asking for English is a no-op.
	if err := f.repo.UpsertAnalysis(ctx, []store.ItemAnalysis{{
		ItemID: f.itemID, Lang: "en", AnalyzedAt: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.at.Translate(ctx, f.owner, f.itemID, "en", false); !errors.Is(err, ErrSameLanguage) {
		t.Errorf("err = %v, want ErrSameLanguage", err)
	}
	if _, err := f.at.Translate(ctx, f.owner, f.itemID, "xx", false); !errors.Is(err, ErrUnsupportedLanguage) {
		t.Errorf("err = %v, want ErrUnsupportedLanguage", err)
	}
	if prov.CallCount() != 0 {
		t.Error("a refused translation reached the model")
	}
}

func TestAnEditedArticleIsTranslatedAgain(t *testing.T) {
	f := newArticleFixture(t, true)
	prov := answering(t, englishReply(t))
	ctx := context.Background()
	if _, err := f.at.Translate(ctx, f.owner, f.itemID, "fr", false); err != nil {
		t.Fatal(err)
	}
	calls := prov.CallCount()
	// A stale hash on the cached row is what a publisher's edit produces.
	row, err := f.repo.ItemTranslation(ctx, f.owner, f.itemID, "fr")
	if err != nil {
		t.Fatal(err)
	}
	row.SourceHash = "an-older-version"
	if err := f.repo.PutItemTranslation(ctx, f.owner, row); err != nil {
		t.Fatal(err)
	}
	got, err := f.at.Translate(ctx, f.owner, f.itemID, "fr", false)
	if err != nil || got.Cached {
		t.Fatalf("after an edit: cached=%v err=%v", got.Cached, err)
	}
	if prov.CallCount() == calls {
		t.Error("an edited article was served its old translation")
	}
}

func TestAnArticleRefusedByTheSpendCeilingIsNotCached(t *testing.T) {
	f := newArticleFixture(t, true)
	failing(t, llm.ErrOverBudget)
	ctx := context.Background()
	if _, err := f.at.Translate(ctx, f.owner, f.itemID, "fr", false); !errors.Is(err, llm.ErrOverBudget) {
		t.Fatalf("err = %v, want ErrOverBudget", err)
	}
	if _, err := f.repo.ItemTranslation(ctx, f.owner, f.itemID, "fr"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a refused translation left a cache row: %v", err)
	}
}
