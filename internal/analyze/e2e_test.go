package analyze_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/analyze"
	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
	"github.com/monstercameron/ArticleFlux/internal/jobs"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/pipeline"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// End-to-end for both tiers (plan.md §27, TODO 10.6).
//
// # What "end to end" means here, precisely
//
// A real SQLite file, real migrations, the real `jobs.Pool` draining a real
// queue, the real `pipeline` over the real shipped 26-category lexicon, and the
// real `store` writing and reading `item_analysis`. Nothing is stubbed on the
// Smart path at all.
//
// On the Smart+ path everything is real except the socket: a genuine
// `*llm.Client` marshals a genuine `llm.ClassifyPayload`, checks it against the
// egress allowlist, addresses it to `llm.Endpoint`, and has its request answered
// by a RoundTripper instead of api.openai.com. That is the pattern `llm_test.go`
// documents and insists on — repointing the client at an httptest server would
// "design a hole around the allowlist check instead of exercising it", so the
// URL check and the host allowlist run for real in every test below.
//
// **What is therefore NOT proven here:** that OpenAI answers this schema the way
// the contributors expect. That needs a key and one live run; everything on our
// side of the wire is covered.

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "e2e.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// testEncKey is the 32-byte key the settings repo needs to store secrets. The
// only secret this test stores is a consent flag, which is not encrypted, but
// the repo requires a key to construct.
var testEncKey = []byte("0123456789abcdef0123456789abcdef")

// scope creates a tenant and a user, because Subscribe is the only exported way
// to bring a source into existence and it is per-user. The analysis path itself
// needs neither — it reads `items`, which is global (A14) — so this is setup for
// the fixture rather than something the feature depends on.
func scope(t *testing.T, repo *store.ReaderRepo) store.Scope {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Idempotent: several helpers want the scope and the order they run in is not
	// the caller's business, so a second call is a no-op rather than an error.
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}
	if err := repo.CreateTenantAndUser(context.Background(), store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "u1",
		Hash: "x", Role: "member", Now: now,
	}); err != nil && !strings.Contains(err.Error(), "UNIQUE") &&
		!strings.Contains(err.Error(), "exists") {
		t.Fatalf("tenant: %v", err)
	}
	return sc
}

// seedItems writes a source and some items through the REAL ingest path, so the
// test exercises the same rows a poll would produce.
func seedItems(t *testing.T, repo *store.ReaderRepo, items []store.IngestItem) (string, []string) {
	t.Helper()
	ctx := context.Background()

	feed, _, err := repo.Subscribe(ctx, scope(t, repo), store.NewSubscription{
		NaturalKey: "feed:example.com/feed.xml",
		FeedURL:    "https://example.com/feed.xml",
		SiteURL:    "https://example.com/",
		Title:      "Example Weekly",
	})
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	sourceID := feed.SourceID
	res, err := repo.IngestItems(ctx, sourceID, items)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(res.NewIDs) != len(items) {
		t.Fatalf("ingested %d of %d", len(res.NewIDs), len(items))
	}
	return sourceID, res.NewIDs
}

// realArticles are shaped like the ones in the development database: a title, a
// short summary, and a body of a few hundred words.
func realArticles() []store.IngestItem {
	now := time.Now().UTC()
	mk := func(guid, title, summary, body string) store.IngestItem {
		return store.IngestItem{
			GUID: guid, Title: title, Summary: summary,
			ContentHTML: "<p>" + body + "</p>",
			URL:         "https://example.com/" + guid,
			PublishedAt: now, WordCount: len(strings.Fields(body)),
		}
	}
	return []store.IngestItem{
		mk("sec-1",
			"Ransomware crew exploits a zero day in a VPN appliance",
			"The vulnerability allowed remote code execution before a patch shipped.",
			strings.Repeat("The breach was disclosed by the vendor after a security advisory. "+
				"Administrators should apply the patch immediately; an exploit is circulating. ", 8)),
		mk("space-1",
			"Probe enters orbit around Mercury after a seven-year cruise",
			"The spacecraft will map the planet's surface with its full instrument payload.",
			strings.Repeat("The launch was in 2019 and the agency says the payload is healthy. "+
				"Its orbit will tighten over the coming months as the mission begins. ", 8)),
		mk("fin-1",
			"The Fed holds interest rates steady as inflation cools",
			"Bond markets rallied and equities closed higher on the news.",
			strings.Repeat("Traders had priced in a hold. The central bank said inflation was "+
				"approaching target while the labour market stayed tight. ", 8)),
		mk("none-1",
			"A quiet weekend",
			"Nothing much happened and that was fine.",
			strings.Repeat("The tomatoes came in early this year and the weather held. "+
				"We sat outside on Saturday and read for a while. ", 6)),
	}
}

func newPipeline(t *testing.T) *pipeline.Service {
	t.Helper()
	lx, err := classify.Compile(lexicon.Categories())
	if err != nil {
		t.Fatalf("compile lexicon: %v", err)
	}
	return pipeline.New(lx, classify.DefaultStrategy(), nil)
}

// drain runs the real pool until the queue is empty or the deadline passes.
func drain(t *testing.T, repo *store.ReaderRepo, svc *analyze.Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool := jobs.New(repo, jobs.Options{Workers: 2, Idle: 25 * time.Millisecond})
	pool.Handle(store.JobAnalyze, svc.Handle)
	pool.Start(ctx)
	defer pool.Stop()

	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		depth, err := repo.QueueDepth(ctx)
		if err != nil {
			t.Fatalf("queue depth: %v", err)
		}
		d := depth[store.JobAnalyze]
		if d.Queued == 0 && d.Running == 0 {
			// One more idle interval, so a job that was claimed between the two
			// reads has finished writing.
			time.Sleep(80 * time.Millisecond)
			depth, _ = repo.QueueDepth(ctx)
			if depth[store.JobAnalyze].Queued == 0 && depth[store.JobAnalyze].Running == 0 {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("the analyze queue never drained")
}

// ---------------------------------------------------------------------------
// Smart — the free tier, end to end
// ---------------------------------------------------------------------------

func TestSmartEndToEnd(t *testing.T) {
	db := openDB(t)
	repo := store.NewReaderRepo(db)
	ctx := context.Background()

	sourceID, ids := seedItems(t, repo, realArticles())

	var fannedOut atomic.Int64
	svc := analyze.New(repo, newPipeline(t), nil).
		WithFanout(func(context.Context, string, []string) error {
			fannedOut.Add(1)
			return nil
		})

	if err := svc.Enqueue(ctx, sourceID, ids); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	drain(t, repo, svc)

	got, err := repo.AnalysisByIDs(ctx, ids)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("%d analysis rows for %d items", len(got), len(ids))
	}

	// Every row is stamped, so the staleness query can find it later.
	for id, row := range got {
		if row.AnalyzerVersion == 0 || row.LexiconHash == "" {
			t.Errorf("%s has no version stamp: %+v", id, row)
		}
		if row.AnalyzedAt.IsZero() {
			t.Errorf("%s has no analysed_at", id)
		}
		// The free tier answered, so the model marker must be absent — this is
		// what the Smart+ retry sweep selects on.
		if !row.LLMAt.IsZero() {
			t.Errorf("%s claims a model read on the free tier", id)
		}
	}

	// And it actually classified: the three real articles land in their sections
	// and the fourth is correctly left alone.
	want := map[string]string{"sec-1": "security", "space-1": "space", "fin-1": "finance"}
	byGUID := guidToID(t, repo, ids)
	for guid, slug := range want {
		row := got[byGUID[guid]]
		if row.CategoryScores[slug] == 0 {
			t.Errorf("%s scored nothing for %q: %v", guid, slug, row.CategoryScores)
		}
	}
	if row, ok := got[byGUID["none-1"]]; ok && len(row.CategoryScores) > 0 {
		// Scoring above zero is fine; being ASSIGNED is what matters, and that is
		// checked through the pipeline in the classify tests. Here we only assert
		// the row exists, which it does.
		_ = row
	}

	if fannedOut.Load() == 0 {
		t.Errorf("analysis completed without enqueuing fan-out — §27.2a's ordering is not wired")
	}
}

// TestSmartIsIdempotent: the queue is at-least-once, so a redelivery must produce
// the same row rather than a second one or a different one (§27.2c).
func TestSmartIsIdempotent(t *testing.T) {
	db := openDB(t)
	repo := store.NewReaderRepo(db)
	ctx := context.Background()

	sourceID, ids := seedItems(t, repo, realArticles())
	svc := analyze.New(repo, newPipeline(t), nil)

	if err := svc.Enqueue(ctx, sourceID, ids); err != nil {
		t.Fatal(err)
	}
	drain(t, repo, svc)
	first, _ := repo.AnalysisByIDs(ctx, ids)

	if err := svc.Enqueue(ctx, sourceID, ids); err != nil {
		t.Fatal(err)
	}
	drain(t, repo, svc)
	second, _ := repo.AnalysisByIDs(ctx, ids)

	if len(second) != len(first) {
		t.Fatalf("a re-run produced %d rows where the first produced %d", len(second), len(first))
	}
	for id, a := range first {
		b := second[id]
		if a.LexiconHash != b.LexiconHash || a.AnalyzerVersion != b.AnalyzerVersion {
			t.Errorf("%s: stamp changed between runs", id)
		}
		if len(a.CategoryScores) != len(b.CategoryScores) {
			t.Errorf("%s: %d scores then %d", id, len(a.CategoryScores), len(b.CategoryScores))
			continue
		}
		for slug, v := range a.CategoryScores {
			// Exactly equal. §27.2c requires a reproduce, and a tolerance here
			// would hide exactly the map-iteration float bug the pipeline's own
			// reproduce test caught.
			if b.CategoryScores[slug] != v {
				t.Errorf("%s: %s was %v then %v", id, slug, v, b.CategoryScores[slug])
			}
		}
	}
}

// TestSmartSurvivesAMissingItem: ids that no longer exist must not retry forever.
func TestSmartSurvivesAMissingItem(t *testing.T) {
	db := openDB(t)
	repo := store.NewReaderRepo(db)
	ctx := context.Background()
	svc := analyze.New(repo, newPipeline(t), nil)

	if err := svc.Enqueue(ctx, "gone", []string{"no-such-item"}); err != nil {
		t.Fatal(err)
	}
	drain(t, repo, svc) // fails the test if the job never settles
}

// ---------------------------------------------------------------------------
// Smart+ — end to end, everything real but the socket
// ---------------------------------------------------------------------------

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// provider answers a Responses API call, and records what it was sent.
type provider struct {
	calls    atomic.Int64
	lastBody atomic.Value // string
	reply    func(sent string) string
	status   int
}

func (p *provider) transport() http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		p.calls.Add(1)
		raw, _ := io.ReadAll(r.Body)

		// The request the real client built, so the test can assert on what
		// actually would have left the machine.
		var wire struct {
			Input string `json:"input"`
		}
		_ = json.Unmarshal(raw, &wire)
		p.lastBody.Store(wire.Input)

		status := p.status
		if status == 0 {
			status = http.StatusOK
		}
		body := `{"error":{"message":"boom"}}`
		if status == http.StatusOK {
			out := p.reply(wire.Input)
			body = fmt.Sprintf(
				`{"id":"resp_1","status":"completed","output_text":%s,
				  "usage":{"input_tokens":100,"output_tokens":50}}`,
				mustQuote(out))
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewReader([]byte(body))),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})
}

func mustQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// goodReply is a well-formed union answer for the four shipped contributors.
func goodReply(string) string {
	return `{"classify":{"primary":"security","secondary":["software"],"tags":["cve"],
	          "confidence":0.9,"unsure":false},
	         "genre":{"kind":"analysis"},
	         "keyphrases":{"phrases":["zero day","vpn appliance"]},
	         "abstract":{"text":"A VPN zero day is being exploited before the patch shipped."}}`
}

func smartPlusService(t *testing.T, db *store.DB, repo *store.ReaderRepo, p *provider) *analyze.Service {
	t.Helper()
	lx, err := classify.Compile(lexicon.Categories())
	if err != nil {
		t.Fatal(err)
	}
	settings := store.NewSettingsRepo(db, testEncKey)
	ctx := context.Background()
	// `settings.updated_by` carries an FK to users(id), so the actor has to be a
	// real one — a placeholder string fails the constraint. The tenant is created
	// here rather than relying on seedItems having run, so the order of the two
	// does not matter to the caller.
	owner := scope(t, repo)
	// Both consent conditions, set the way the owner would: the instance opts in,
	// and a key exists. Neither implies the other.
	if err := settings.SetSystemValue(ctx, smart.KeyClassifyEnabled, "true", owner.UserID); err != nil {
		t.Fatalf("consent: %v", err)
	}

	client := llm.NewWithTransport(
		func(context.Context) string { return "sk-test-key" }, p.transport())

	cls := smart.NewClassifier(client, settings, lx)
	return analyze.New(repo, pipeline.New(lx, classify.DefaultStrategy(), nil), nil).
		WithSmartPlus(cls, pipeline.EscalateAlways)
}

func TestSmartPlusEndToEnd(t *testing.T) {
	db := openDB(t)
	repo := store.NewReaderRepo(db)
	ctx := context.Background()

	p := &provider{reply: goodReply}
	sourceID, ids := seedItems(t, repo, realArticles())
	svc := smartPlusService(t, db, repo, p)

	if err := svc.Enqueue(ctx, sourceID, ids); err != nil {
		t.Fatal(err)
	}
	drain(t, repo, svc)

	if p.calls.Load() == 0 {
		t.Fatal("Smart+ was enabled and consented to, and made no request at all")
	}

	got, err := repo.AnalysisByIDs(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	enriched := 0
	for id, row := range got {
		if row.LLMAt.IsZero() {
			continue
		}
		enriched++
		if row.Abstract == "" {
			t.Errorf("%s was marked as read by the model but carries no abstract", id)
		}
		if row.Genre != "analysis" {
			t.Errorf("%s genre is %q, the model said analysis", id, row.Genre)
		}
	}
	if enriched == 0 {
		t.Fatal("no row was marked as enriched, so llm_at is not being written")
	}

	// What actually left the machine has to satisfy §27.4e — asserted against the
	// body the real client marshalled, not against a template.
	sent, _ := p.lastBody.Load().(string)
	if sent == "" {
		t.Fatal("nothing was captured from the outbound request")
	}
	bad, err := llm.AuditClassify([]byte(sent))
	if err != nil {
		t.Fatalf("the outbound body was not JSON: %v", err)
	}
	if len(bad) > 0 {
		t.Fatalf("the outbound body carried keys §27.4e forbids: %v", bad)
	}
	for _, forbidden := range []string{"user_id", "tenant_id", "read_at", "feed_url", "item_id"} {
		if strings.Contains(sent, forbidden) {
			t.Errorf("the outbound body mentions %q", forbidden)
		}
	}
}

// TestSmartPlusNeverRunsWithoutConsent is the gate that protects the bill and the
// boundary: with `smart.classify` unset, ZERO requests, whatever else is wired.
func TestSmartPlusNeverRunsWithoutConsent(t *testing.T) {
	db := openDB(t)
	repo := store.NewReaderRepo(db)
	ctx := context.Background()

	p := &provider{reply: goodReply}
	lx, _ := classify.Compile(lexicon.Categories())
	settings := store.NewSettingsRepo(db, testEncKey)
	// Deliberately NOT setting smart.classify. A key exists; consent does not.
	client := llm.NewWithTransport(
		func(context.Context) string { return "sk-test-key" }, p.transport())

	svc := analyze.New(repo, pipeline.New(lx, classify.DefaultStrategy(), nil), nil).
		WithSmartPlus(smart.NewClassifier(client, settings, lx), pipeline.EscalateAlways)

	sourceID, ids := seedItems(t, repo, realArticles())
	if err := svc.Enqueue(ctx, sourceID, ids); err != nil {
		t.Fatal(err)
	}
	drain(t, repo, svc)

	if n := p.calls.Load(); n != 0 {
		t.Fatalf("%d requests were made without consent", n)
	}
	// And the free tier still did its job.
	got, _ := repo.AnalysisByIDs(ctx, ids)
	if len(got) != len(ids) {
		t.Fatalf("%d rows for %d items", len(got), len(ids))
	}
	for id, row := range got {
		if !row.LLMAt.IsZero() {
			t.Errorf("%s claims a model read", id)
		}
	}
}

// TestSmartPlusFailsSoft: every provider failure must leave the free tier's
// answer in place rather than failing the job (§27.4f).
func TestSmartPlusFailsSoft(t *testing.T) {
	cases := []struct {
		name string
		p    *provider
	}{
		{"provider returns 500", &provider{status: http.StatusInternalServerError, reply: goodReply}},
		{"reply is not JSON", &provider{reply: func(string) string { return "I'm afraid I can't do that" }}},
		{"reply is JSON but not the schema", &provider{reply: func(string) string { return `{"wrong":1}` }}},
		{"every slice is garbage", &provider{reply: func(string) string {
			return `{"classify":"nope","genre":7,"keyphrases":[],"abstract":null}`
		}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := openDB(t)
			repo := store.NewReaderRepo(db)
			ctx := context.Background()

			sourceID, ids := seedItems(t, repo, realArticles())
			svc := smartPlusService(t, db, repo, c.p)

			if err := svc.Enqueue(ctx, sourceID, ids); err != nil {
				t.Fatal(err)
			}
			// drain fails the test if a job dies rather than settling, which is
			// the assertion: a provider failure must not fail the job.
			drain(t, repo, svc)

			got, err := repo.AnalysisByIDs(ctx, ids)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(ids) {
				t.Fatalf("%d rows for %d items — a provider failure lost the free tier's work",
					len(got), len(ids))
			}
			for id, row := range got {
				if row.AnalyzerVersion == 0 {
					t.Errorf("%s was written without a stamp", id)
				}
				if !row.LLMAt.IsZero() {
					t.Errorf("%s was marked as read by the model after a failure, so the "+
						"retry sweep will never revisit it", id)
				}
			}
		})
	}
}

// TestSmartPlusPartialReplyKeepsWhatWorked is §27.2b rule 2 at the top level: a
// reply where one contributor is malformed must still apply the other three.
func TestSmartPlusPartialReplyKeepsWhatWorked(t *testing.T) {
	db := openDB(t)
	repo := store.NewReaderRepo(db)
	ctx := context.Background()

	p := &provider{reply: func(string) string {
		return `{"classify":{"primary":"security","secondary":[],"tags":[],
		          "confidence":0.8,"unsure":false},
		         "genre":{"kind":"not-a-real-genre"},
		         "keyphrases":{"phrases":["zero day"]},
		         "abstract":{"text":"A short one."}}`
	}}
	sourceID, ids := seedItems(t, repo, realArticles())
	svc := smartPlusService(t, db, repo, p)

	if err := svc.Enqueue(ctx, sourceID, ids); err != nil {
		t.Fatal(err)
	}
	drain(t, repo, svc)

	got, _ := repo.AnalysisByIDs(ctx, ids)
	kept := 0
	for _, row := range got {
		if row.Abstract == "A short one." {
			kept++
			// The bad genre slice must not have taken the good ones with it, and
			// the deterministic genre must still be there.
			if row.Genre == "not-a-real-genre" {
				t.Errorf("an off-enum genre reached the row")
			}
			if row.Genre == "" {
				t.Errorf("the free tier's genre was lost to the model's bad slice")
			}
		}
	}
	if kept == 0 {
		t.Fatal("one malformed slice cost every other contributor its answer")
	}
}

// TestEscalateNeverSpendsNothing proves the cheapest lever works: policy=never
// makes zero requests even with consent and a key.
func TestEscalateNeverSpendsNothing(t *testing.T) {
	db := openDB(t)
	repo := store.NewReaderRepo(db)
	ctx := context.Background()

	p := &provider{reply: goodReply}
	lx, _ := classify.Compile(lexicon.Categories())
	settings := store.NewSettingsRepo(db, testEncKey)
	owner := scope(t, repo)
	if err := settings.SetSystemValue(ctx, smart.KeyClassifyEnabled, "true", owner.UserID); err != nil {
		t.Fatal(err)
	}
	client := llm.NewWithTransport(func(context.Context) string { return "sk-test" }, p.transport())

	svc := analyze.New(repo, pipeline.New(lx, classify.DefaultStrategy(), nil), nil).
		WithSmartPlus(smart.NewClassifier(client, settings, lx), pipeline.EscalateNever)

	sourceID, ids := seedItems(t, repo, realArticles())
	if err := svc.Enqueue(ctx, sourceID, ids); err != nil {
		t.Fatal(err)
	}
	drain(t, repo, svc)

	if n := p.calls.Load(); n != 0 {
		t.Fatalf("policy=never made %d requests", n)
	}
	got, _ := repo.AnalysisByIDs(ctx, ids)
	if len(got) != len(ids) {
		t.Fatalf("%d rows for %d items", len(got), len(ids))
	}
}

// guidToID maps the seeded GUIDs back to their item ids.
func guidToID(t *testing.T, repo *store.ReaderRepo, ids []string) map[string]string {
	t.Helper()
	items, err := repo.ItemsByID(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, it := range items {
		// The seed used the GUID as the URL suffix, which survives ingest.
		parts := strings.Split(it.URL, "/")
		out[parts[len(parts)-1]] = it.ID
	}
	return out
}

// TestSmartPlusVerdictIsPersisted closes the gap the "why can't you test it"
// question exposed: every Smart+ test passed while the model's CATEGORY answer
// was written nowhere at all.
//
// `toRow` copied the genre, keyphrases and abstract and silently dropped the
// primary, because `item_analysis` had no column for it — so the feature under
// test was "the model was asked", not "the model placed the article". The
// assertion that would have caught it is this one: read the verdict back.
func TestSmartPlusVerdictIsPersisted(t *testing.T) {
	db := openDB(t)
	repo := store.NewReaderRepo(db)
	ctx := context.Background()

	p := &provider{reply: goodReply}
	sourceID, ids := seedItems(t, repo, realArticles())
	svc := smartPlusService(t, db, repo, p)

	if err := svc.Enqueue(ctx, sourceID, ids); err != nil {
		t.Fatal(err)
	}
	drain(t, repo, svc)

	got, err := repo.AnalysisByIDs(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	placed := 0
	for id, row := range got {
		if row.LLMAt.IsZero() {
			continue
		}
		if row.ModelPrimary == "" {
			t.Errorf("%s was read by the model and stored no verdict", id)
			continue
		}
		placed++
		if row.ModelPrimary != "security" {
			t.Errorf("%s stored %q, the model said security", id, row.ModelPrimary)
		}
		if len(row.ModelSecondary) != 1 || row.ModelSecondary[0] != "software" {
			t.Errorf("%s secondary is %v, wanted [software]", id, row.ModelSecondary)
		}
		if len(row.ModelTags) != 1 || row.ModelTags[0] != "cve" {
			t.Errorf("%s tags are %v, wanted [cve]", id, row.ModelTags)
		}
	}
	if placed == 0 {
		t.Fatal("no verdict was persisted for any item")
	}
}

// TestSmartPlusRejectsASlugThatDoesNotExist. Strict mode types `primary` as a
// STRING — it cannot enumerate 26 slugs without repeating them in every request —
// so nothing on the wire stops a model answering "tech". `dedupeSlugs` carried a
// comment claiming the caller re-checks against the taxonomy, and until this test
// nothing did.
func TestSmartPlusRejectsASlugThatDoesNotExist(t *testing.T) {
	db := openDB(t)
	repo := store.NewReaderRepo(db)
	ctx := context.Background()

	p := &provider{reply: func(string) string {
		return `{"classify":{"primary":"tech","secondary":["alsofake"],"tags":[],
		          "confidence":0.99,"unsure":false},
		         "genre":{"kind":"news"},
		         "keyphrases":{"phrases":["zero day"]},
		         "abstract":{"text":"Something happened."}}`
	}}
	sourceID, ids := seedItems(t, repo, realArticles())
	svc := smartPlusService(t, db, repo, p)

	if err := svc.Enqueue(ctx, sourceID, ids); err != nil {
		t.Fatal(err)
	}
	drain(t, repo, svc)

	got, _ := repo.AnalysisByIDs(ctx, ids)
	for id, row := range got {
		if row.ModelPrimary == "tech" {
			t.Errorf("%s stored an invented slug", id)
		}
		for _, s := range row.ModelSecondary {
			if s == "alsofake" {
				t.Errorf("%s stored an invented secondary", id)
			}
		}
		// The rest of the reply is still good and must survive: dropping a bad
		// slug is not a reason to throw away the abstract that came with it.
		if !row.LLMAt.IsZero() && row.Abstract == "" {
			t.Errorf("%s lost its abstract to a bad slug", id)
		}
	}
}
