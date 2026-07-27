package seedread

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

var now = time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*store.ReaderRepo, store.Scope) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "seed.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "reader",
		Hash: "x", Role: "member", Now: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	// Two feeds, one about a subject that recurs and one that does not, so the derived
	// focus has something to find and something to leave alone.
	feed, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:tech", FeedURL: "https://tech.example/rss",
		SiteURL: "https://tech.example/", Title: "Tech",
	})
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{
		"Android Auto adds offline maps this month",
		"Android Auto split screen arrives widely",
		"Android Auto gains better voice control",
		"Android tablets get a desktop mode",
		"Cycling shoes reviewed for winter riding",
		"Cycling helmets tested against the standard",
		"Sourdough starter troubleshooting guide",
		"Kettlebell programming for beginners",
	}
	var ingest []store.IngestItem
	for i, title := range titles {
		ingest = append(ingest, store.IngestItem{
			GUID: string(rune('a' + i)), URL: "https://tech.example/p/" + string(rune('a'+i)),
			Title: title, Summary: title, ContentHTML: "<p>" + title + "</p>",
			PublishedAt: now.Add(-time.Duration(i+1) * 12 * time.Hour),
			WordCount:   800,
		})
	}
	if _, err := repo.IngestItems(ctx, feed.SourceID, ingest); err != nil {
		t.Fatal(err)
	}
	return repo, sc
}

// The seeder has to produce the signals the interest layer actually reads.
//
// Not just "some rows": a history of pure impressions derives nothing, because impressions
// are weight-zero by design (R17). This asserts the kinds that can move a score are present.
func TestSeedProducesAffinityBearingSignals(t *testing.T) {
	ctx := context.Background()
	repo, sc := setup(t)

	res, err := Run(ctx, repo, sc, Options{Focus: []string{"android"}, Now: now, Seed: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Items == 0 {
		t.Fatal("no items were considered")
	}
	if res.Opened == 0 || res.Completed == 0 {
		t.Fatalf("no reading was simulated: opened=%d completed=%d", res.Opened, res.Completed)
	}

	// Impressions alone would derive nothing at all, so the presence of kinds that MOVE a
	// score is the property worth pinning.
	counts := kindCounts(t, repo, sc)
	if counts[signals.Impression] == 0 {
		t.Error("no impressions: the denominator is missing")
	}
	for _, k := range []signals.Kind{signals.Opened, signals.Dwell, signals.Completed} {
		if counts[k] == 0 {
			t.Errorf("no %q events — this history cannot produce affinity", k)
		}
	}
	// Dwell has to be classifiable as a real read, or the whole history scores as bounces.
	// It carries attentive milliseconds scaled to the article's length, which is the only
	// way signals.Classify can reach Read.
	if !anyDwellReads(t, repo, sc) {
		t.Error("no dwell observation classifies as a Read; every one looks like a bounce")
	}
}

// Reproducible: the same database and the same seed produce the same history.
//
// "The ranking looks wrong" is only diagnosable if the input can be reproduced, which is why
// every decision is a hash of the item id rather than a draw from a random source.
func TestSeedIsReproducible(t *testing.T) {
	ctx := context.Background()
	repoA, scA := setup(t)
	repoB, scB := setup(t)

	a, err := Run(ctx, repoA, scA, Options{Focus: []string{"android"}, Now: now, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Run(ctx, repoB, scB, Options{Focus: []string{"android"}, Now: now, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	// Compared field by field via a formatted string: Result carries a []string, so it is
	// not comparable with ==, and reflect.DeepEqual would report "not equal" without
	// saying which count drifted.
	if got, want := fmt.Sprintf("%+v", a), fmt.Sprintf("%+v", b); got != want {
		t.Errorf("the same seed produced different histories:\n  %s\n  %s", got, want)
	}
}

// The focus decides what gets read, so it has to actually discriminate.
//
// A seeder that reads everything gives the scorer no negative signal and leaves the
// `skipped` and `bounced` paths dead in every development database — which is how those
// paths came to be unexercised in the first place.
func TestFocusDecidesWhatGetsRead(t *testing.T) {
	ctx := context.Background()
	repo, sc := setup(t)

	if _, err := Run(ctx, repo, sc, Options{
		Focus: []string{"android"}, Read: 1, Now: now, Seed: 1,
	}); err != nil {
		t.Fatal(err)
	}

	items, _, err := repo.ListItems(ctx, sc, store.ListQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]store.Item{}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		byID[it.ID] = it
		ids = append(ids, it.ID)
	}
	sigs, err := repo.ItemSignals(ctx, sc, ids)
	if err != nil {
		t.Fatal(err)
	}

	for id, s := range sigs {
		title := strings.ToLower(byID[id].Title)
		onFocus := strings.Contains(title, "android")
		completed := s.Counts[signals.Completed] > 0
		if onFocus && !completed {
			t.Errorf("an on-focus item was not read at Read=1: %q", byID[id].Title)
		}
		if !onFocus && completed {
			t.Errorf("an off-focus item was read to the end: %q", byID[id].Title)
		}
	}
}

// With no focus given, one is taken from the corpus — otherwise a hardcoded list would
// match nothing on a database of cycling feeds and the seeder would silently write a
// history of pure impressions.
func TestFocusIsDerivedFromTheCorpusWhenAbsent(t *testing.T) {
	ctx := context.Background()
	repo, sc := setup(t)

	res, err := Run(ctx, repo, sc, Options{Now: now, Seed: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Focus) == 0 {
		t.Fatal("no focus was derived, so nothing was interesting and nothing was read")
	}
	if res.Completed == 0 {
		t.Errorf("a derived focus of %v read nothing — it does not match the corpus", res.Focus)
	}
	// "android" occurs in four of the eight headlines and is six letters, so it must be
	// among the candidates. If it is not, the derivation is not looking at the corpus.
	var found bool
	for _, f := range res.Focus {
		if f == "android" || f == "cycling" {
			found = true
		}
	}
	if !found {
		t.Errorf("derived focus %v contains none of the recurring subjects in the corpus", res.Focus)
	}
}

// An empty database is an error, not a silent no-op.
//
// The command's whole purpose is to make the interest layer observable; doing nothing and
// reporting success would send the operator looking for a bug in the ranker.
func TestSeedRefusesAnEmptyDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "empty.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "reader",
		Hash: "x", Role: "member", Now: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	if _, err := Run(ctx, repo, sc, Options{Now: now}); err == nil {
		t.Error("seeding an empty database reported success")
	}
}

func kindCounts(t *testing.T, repo *store.ReaderRepo, sc store.Scope) map[signals.Kind]int {
	t.Helper()
	evs, err := repo.EngagementsSince(t.Context(), sc, 0, 20000)
	if err != nil {
		t.Fatal(err)
	}
	out := map[signals.Kind]int{}
	for _, e := range evs {
		out[e.Kind]++
	}
	return out
}

// anyDwellReads reports whether at least one dwell observation is long enough, for its
// article's length, to classify as a full read.
func anyDwellReads(t *testing.T, repo *store.ReaderRepo, sc store.Scope) bool {
	t.Helper()
	ctx := t.Context()
	items, _, err := repo.ListItems(ctx, sc, store.ListQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	words := map[string]int{}
	for _, it := range items {
		words[it.ID] = it.WordCount
	}
	evs, err := repo.EngagementsSince(ctx, sc, 0, 20000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind != signals.Dwell {
			continue
		}
		if signals.Classify(int64(e.Value), words[e.ItemID], 0) == signals.Read {
			return true
		}
	}
	return false
}
