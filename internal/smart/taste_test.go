package smart

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

var tasteNow = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

// tasteFixture builds a reader with n liked and n disliked items, titled
// "liked N" / "disliked N" so a test can tell which pool a returned title
// came from without re-deriving it.
func tasteFixture(t *testing.T, likedCount, dislikedCount int) (*store.ReaderRepo, store.Scope) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "taste.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := store.NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "u",
		Hash: "x", Role: "member", Now: tasteNow.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	feed, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:taste", FeedURL: "https://taste.example/rss",
		SiteURL: "https://taste.example/", Title: "Taste Feed",
	})
	if err != nil {
		t.Fatal(err)
	}

	addItem := func(title string, kind signals.Kind) {
		guid := idgen.New()
		if _, err := repo.IngestItems(ctx, feed.SourceID, []store.IngestItem{{
			GUID: guid, URL: "https://taste.example/" + guid,
			Title: title, Summary: "s", PublishedAt: tasteNow,
		}}); err != nil {
			t.Fatal(err)
		}
		items, _, err := repo.ListItems(ctx, sc, store.ListQuery{SourceID: feed.SourceID, Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		var itemID string
		for _, it := range items {
			if it.Title == title {
				itemID = it.ID
			}
		}
		if itemID == "" {
			t.Fatalf("could not find just-ingested item %q", title)
		}
		if _, err := repo.RecordEngagements(ctx, sc, []signals.Event{{
			ID: idgen.New(), ItemID: itemID, Kind: kind,
			Surface: "list", At: tasteNow.UnixMilli(),
		}}); err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < likedCount; i++ {
		addItem("liked "+idgen.New(), signals.Liked)
	}
	for i := 0; i < dislikedCount; i++ {
		addItem("disliked "+idgen.New(), signals.Disliked)
	}

	return repo, sc
}

// No liked/disliked verdicts at all is the common case (a fresh or
// lightly-used account) and must return cleanly, not error.
func TestTasteExamplesEmptyWithNoVerdicts(t *testing.T) {
	repo, sc := tasteFixture(t, 0, 0)
	pos, neg, err := TasteExamples(context.Background(), repo, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 0 || len(neg) != 0 {
		t.Errorf("pos=%v neg=%v, want both empty with no verdicts", pos, neg)
	}
}

// A pool at or under the cap is returned whole — no unnecessary shuffling of
// a reader with only two opinions on record.
func TestTasteExamplesStableWhenPoolAtOrUnderCap(t *testing.T) {
	repo, sc := tasteFixture(t, 2, 1)
	pos, neg, err := TasteExamples(context.Background(), repo, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 2 {
		t.Errorf("len(pos) = %d, want 2 (the whole pool, under the %d cap)", len(pos), MaxTasteSearchExamples)
	}
	if len(neg) != 1 {
		t.Errorf("len(neg) = %d, want 1", len(neg))
	}
}

// The whole point of Cam's ask (2026-08-01): repeated calls against a pool
// LARGER than the cap must return different selections often enough to prove
// the sampling is real randomization, not a fixed "first N" or "last N" that
// only looks random once.
func TestTasteExamplesVariesAcrossCallsWhenPoolExceedsCap(t *testing.T) {
	const poolSize = MaxTasteSearchExamples + 15
	repo, sc := tasteFixture(t, poolSize, poolSize)

	first, _, err := TasteExamples(context.Background(), repo, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != MaxTasteSearchExamples {
		t.Fatalf("len(first) = %d, want the cap %d", len(first), MaxTasteSearchExamples)
	}

	var sawDifferentSelection bool
	for attempt := 0; attempt < 20; attempt++ {
		next, _, err := TasteExamples(context.Background(), repo, sc)
		if err != nil {
			t.Fatal(err)
		}
		if !sameTitleSet(first, next) {
			sawDifferentSelection = true
			break
		}
	}
	if !sawDifferentSelection {
		t.Errorf("20 calls against a %d-item pool for a %d-item sample all returned the exact same set — "+
			"sampling does not look random", poolSize, MaxTasteSearchExamples)
	}
}

func sameTitleSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, t := range a {
		set[t] = true
	}
	for _, t := range b {
		if !set[t] {
			return false
		}
	}
	return true
}

// A duplicate Liked event on the same item (a re-open, a UI double-fire)
// must not make that item eligible to be counted, let alone sampled, twice.
func TestTasteExamplesDedupesRepeatedVerdictsOnTheSameItem(t *testing.T) {
	repo, sc := tasteFixture(t, 1, 0)
	// Fire a second Liked event on whatever item tasteFixture already liked.
	items, _, err := repo.ListItems(context.Background(), sc, store.ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("fixture produced %d items, want 1", len(items))
	}
	if _, err := repo.RecordEngagements(context.Background(), sc, []signals.Event{{
		ID: idgen.New(), ItemID: items[0].ID, Kind: signals.Liked,
		Surface: "list", At: tasteNow.Add(time.Minute).UnixMilli(),
	}}); err != nil {
		t.Fatal(err)
	}

	pos, _, err := TasteExamples(context.Background(), repo, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 {
		t.Errorf("pos = %v, want exactly 1 entry despite two Liked events on the same item", pos)
	}
}
