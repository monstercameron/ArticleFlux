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

// A closed database is the one realistic way ListItems fails mid-run — not a fabricated
// error, since a store already closed underneath a caller is an ordinary lifecycle bug this
// package cannot paper over.
func TestRunPropagatesAListItemsError(t *testing.T) {
	ctx := context.Background()
	repo, sc := setup(t)
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "unused.db")})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	closedRepo := store.NewReaderRepo(db)
	if _, err := Run(ctx, closedRepo, sc, Options{Now: now}); err == nil {
		t.Error("Run against a closed database reported success")
	}
	_ = repo // repo/sc from setup only establish a comparable scope shape; the query itself is on closedRepo
}

// Zero Options.Now anchors the run to the real clock rather than erroring or silently no-oping.
func TestRunDefaultsNowToTheRealClockWhenZero(t *testing.T) {
	ctx := context.Background()
	repo, sc := setup(t)
	res, err := Run(ctx, repo, sc, Options{Focus: []string{"android"}, Seed: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Items == 0 {
		t.Fatal("no items were considered with a zero Now")
	}
}

// An item published after the anchor "now" has not happened yet from the simulated reader's
// perspective and must not be walked at all — a seeder that reads the future would corrupt
// the reproducibility the whole package exists to provide.
func TestFutureDatedItemsAreSkipped(t *testing.T) {
	ctx := context.Background()
	repo, sc := setup(t)

	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil || len(feeds) == 0 {
		t.Fatalf("ListFeeds: %v (feeds=%d)", err, len(feeds))
	}
	future := store.IngestItem{
		GUID: "future-1", URL: "https://tech.example/p/future",
		Title: "Android Auto announces a feature from next year", Summary: "s", ContentHTML: "<p>s</p>",
		PublishedAt: now.Add(365 * 24 * time.Hour), WordCount: 800,
	}
	if _, err := repo.IngestItems(ctx, feeds[0].SourceID, []store.IngestItem{future}); err != nil {
		t.Fatal(err)
	}

	res, err := Run(ctx, repo, sc, Options{Focus: []string{"android"}, Now: now, Seed: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The fixture has 8 items already in the past; the future one must not raise that count.
	if res.Items != 8 {
		t.Errorf("Items = %d, want 8; a future-dated item was walked", res.Items)
	}

	items, _, err := repo.ListItems(ctx, sc, store.ListQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var futureID string
	for _, it := range items {
		if it.Title == future.Title {
			futureID = it.ID
		}
	}
	if futureID == "" {
		t.Fatal("the future item was not even stored")
	}
	sigs, err := repo.ItemSignals(ctx, sc, []string{futureID})
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := sigs[futureID]; ok && len(s.Counts) > 0 {
		t.Errorf("the future item received engagement signals: %+v", s.Counts)
	}
}

// The switch in Run has four branches: opened, interesting-but-skipped, bounced, and neither
// (silent). Titles below are chosen so their fnv hash lands in each branch deterministically —
// pinned rather than looped-until-lucky, so the test says exactly why each one lands where it
// does.
func TestRunEngagementBranchesAreAllReachable(t *testing.T) {
	ctx := context.Background()
	repo, sc := setup(t)
	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil || len(feeds) == 0 {
		t.Fatalf("ListFeeds: %v", err)
	}

	titles := []string{
		"Gizmos guide entry 3",      // interesting, roll 0.0194 < 0.6: opened, and hashMod(like,4)==0
		"Gizmos manual part 1",      // interesting, roll 0.2107 < 0.6: opened, and hashMod(reread,5)==0
		"Gizmos explained volume 0", // interesting, roll 0.6799 >= 0.6: skipped, not opened
		"Gadget review number 7 today", // not interesting, hashMod(bounce,12)==0: bounced
	}
	var ingest []store.IngestItem
	for i, title := range titles {
		ingest = append(ingest, store.IngestItem{
			GUID: fmt.Sprintf("branch-%d", i), URL: fmt.Sprintf("https://tech.example/p/branch-%d", i),
			Title: title, Summary: title, ContentHTML: "<p>" + title + "</p>",
			PublishedAt: now.Add(-time.Duration(i+1) * time.Hour), WordCount: 800,
		})
	}
	if _, err := repo.IngestItems(ctx, feeds[0].SourceID, ingest); err != nil {
		t.Fatal(err)
	}

	res, err := Run(ctx, repo, sc, Options{Focus: []string{"gizmos"}, Now: now, Seed: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Liked == 0 {
		t.Error("expected the pinned like-branch title to produce a Liked event")
	}
	if res.Reread == 0 {
		t.Error("expected the pinned reread-branch title to produce a Reread event")
	}
	if res.Skipped == 0 {
		t.Error("expected the pinned interesting-but-not-read title to count as skipped")
	}
	if res.Bounced == 0 {
		t.Error("expected the pinned off-focus title to bounce")
	}
}

// The paging loop over ListItems (bounded by store.MaxLimit per page) and the batching loop in
// write (bounded by store.MaxEngagementBatch) are both single-page/single-batch in every other
// test in this file, which is exactly the bug the package doc warns about: "seeded a history
// over 200 of 3,848 items and reported success". This corpus is sized past both limits.
func TestSeedHandlesLargeCorporaAcrossPagesAndBatches(t *testing.T) {
	if testing.Short() {
		t.Skip("large-corpus seeding is slow under -short")
	}
	ctx := context.Background()
	repo, sc := setup(t)
	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil || len(feeds) == 0 {
		t.Fatalf("ListFeeds: %v", err)
	}

	const n = 260 // > store.MaxLimit (200), so ListItems needs at least two pages
	var ingest []store.IngestItem
	for i := 0; i < n; i++ {
		ingest = append(ingest, store.IngestItem{
			GUID: fmt.Sprintf("big-%d", i), URL: fmt.Sprintf("https://tech.example/p/big-%d", i),
			Title:       fmt.Sprintf("Widgetcraft roundup entry number %d", i),
			Summary:     "s", ContentHTML: "<p>s</p>",
			PublishedAt: now.Add(-time.Duration(i+100) * time.Hour), WordCount: 800,
		})
	}
	if _, err := repo.IngestItems(ctx, feeds[0].SourceID, ingest); err != nil {
		t.Fatal(err)
	}

	res, err := Run(ctx, repo, sc, Options{Focus: []string{"widgetcraft"}, Read: 1, Now: now, Seed: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The 8 items from setup() plus the n new ones; every one of the new ones is
	// interesting and Read=1, so paging correctness shows up directly in the count.
	if want := 8 + n; res.Items != want {
		t.Errorf("Items = %d, want %d — the paging loop stopped early", res.Items, want)
	}
	if res.Completed < n {
		t.Errorf("Completed = %d, want at least %d", res.Completed, n)
	}

	counts := kindCounts(t, repo, sc)
	var total int
	for _, c := range counts {
		total += c
	}
	if total <= store.MaxEngagementBatch {
		t.Fatalf("only %d engagements were recorded; the batching loop was never exercised past one call", total)
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

// words is the tokenizer every hash decision is keyed off of, so its boundary handling — case
// folding, digit handling, and what counts as a separator — is worth pinning directly rather
// than only indirectly through Run.
func TestWords(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"punctuation only", "!!! --- ...", nil},
		{"mixed case folds to lower", "Android AUTO", []string{"android", "auto"}},
		{"digits are kept inside a word", "gpt5 launches", []string{"gpt5", "launches"}},
		{"unicode letters are treated as separators", "café résumé", []string{"caf", "r", "sum"}},
		{"single trailing word with no separator after it", "trailingword", []string{"trailingword"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := words(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("words(%q) = %v, want %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("words(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestMatchesFocus(t *testing.T) {
	cases := []struct {
		name  string
		title string
		focus []string
		want  bool
	}{
		{"no focus at all never matches", "Android news today", nil, false},
		{"matches one of several focus terms", "A story about cycling gear", []string{"android", "cycling"}, true},
		{"case-insensitive via the same tokenizer", "ANDROID auto update", []string{"android"}, true},
		{"no overlap", "Sourdough starter guide", []string{"android", "cycling"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchesFocus(c.title, c.focus); got != c.want {
				t.Errorf("matchesFocus(%q, %v) = %v, want %v", c.title, c.focus, got, c.want)
			}
		})
	}
}

// hashMod and hashUnit are what make a seeded history reproducible; the property that matters
// is determinism (same input, same output) plus hashMod's defensive n<=0 case, which Run never
// triggers since it only ever calls it with fixed positive divisors.
func TestHashModAndHashUnit(t *testing.T) {
	if hashMod("x", "salt", 0) != 0 {
		t.Error("hashMod with n<=0 should return 0 rather than dividing by zero")
	}
	if hashMod("x", "salt", -5) != 0 {
		t.Error("hashMod with a negative n should return 0")
	}
	if a, b := hashMod("same-key", "s", 12), hashMod("same-key", "s", 12); a != b {
		t.Errorf("hashMod is not deterministic: %d != %d", a, b)
	}
	if a, b := hashUnit("same-key", "s", 7), hashUnit("same-key", "s", 7); a != b {
		t.Errorf("hashUnit is not deterministic: %v != %v", a, b)
	}
	// A different seed is expected to (usually) move the roll — this is what lets two dev
	// databases run different simulated readers over the same corpus.
	if hashUnit("k", "read", 1) == hashUnit("k", "read", 2) {
		t.Error("two different seeds produced the exact same roll for the same key (extremely unlikely, check the seed mixing)")
	}
	for i := 0; i < 200; i++ {
		if u := hashUnit(fmt.Sprintf("item-%d", i), "read", uint64(i)); u < 0 || u >= 1 {
			t.Fatalf("hashUnit out of [0,1): %v", u)
		}
	}
}

// deriveFocus's two edge branches: a corpus with no word appearing at least three times (no
// focus can be derived), and a corpus with more candidates than the FocusTerms*4 window (the
// window must be capped rather than scanning everything).
func TestDeriveFocusEdgeCases(t *testing.T) {
	t.Run("no repeated substantial words yields nil", func(t *testing.T) {
		items := []store.Item{
			{Title: "Alpha unique headline"},
			{Title: "Bravo distinct story"},
			{Title: "Charlie singular report"},
		}
		if got := deriveFocus(items, 1); got != nil {
			t.Errorf("deriveFocus = %v, want nil", got)
		}
	})
	t.Run("short words under six letters never become candidates", func(t *testing.T) {
		items := []store.Item{
			{Title: "cat dog cat dog"},
			{Title: "cat dog"},
			{Title: "cat dog"},
		}
		if got := deriveFocus(items, 1); got != nil {
			t.Errorf("deriveFocus = %v, want nil; every candidate word is under six letters", got)
		}
	})
	t.Run("window is capped past FocusTerms*4 candidates", func(t *testing.T) {
		// 24 distinct >=6-letter words, each appearing exactly 3 times: comfortably more
		// than the FocusTerms*4 (16) window.
		var items []store.Item
		for w := 0; w < 24; w++ {
			word := fmt.Sprintf("keyword%02d", w)
			for n := 0; n < 3; n++ {
				items = append(items, store.Item{Title: fmt.Sprintf("Story about %s today", word)})
			}
		}
		got := deriveFocus(items, 3)
		if len(got) != FocusTerms {
			t.Fatalf("deriveFocus returned %d terms, want %d", len(got), FocusTerms)
		}
		gotB := deriveFocus(items, 11)
		if fmt.Sprintf("%v", got) == fmt.Sprintf("%v", gotB) {
			t.Error("two different seeds over a wide candidate pool picked the identical focus set")
		}
	})
}
