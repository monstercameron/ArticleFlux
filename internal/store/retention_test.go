package store

import (
	"context"
	"testing"
	"time"
)

// Retention, at the layer that actually deletes (TODO F36).
//
// The only property worth being certain about: an item somebody DID something
// with is never removed, however old it is. Everything else here is arithmetic;
// this one is somebody's reading.

func retentionFixture(t *testing.T) (*ReaderRepo, Scope, string, []string) {
	t.Helper()
	ctx := context.Background()
	db := openTest(t)
	repo := NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "alice", Hash: "x",
		Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	feed, _, err := repo.Subscribe(ctx, sc, NewSubscription{
		NaturalKey: "feed:old", FeedURL: "https://old.example/feed", Title: "Old feed",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Four articles, all two years old, so the window covers every one of them
	// and the only thing that can spare an item is a claim on it.
	old := time.Now().UTC().Add(-730 * 24 * time.Hour)
	var in []IngestItem
	for _, g := range []string{"plain", "starred", "noted", "rated"} {
		in = append(in, IngestItem{
			GUID: g, URL: "https://old.example/" + g, DupeKey: g,
			Title: "Article " + g, PublishedAt: old,
		})
	}
	if _, err := repo.IngestItems(ctx, feed.SourceID, in); err != nil {
		t.Fatal(err)
	}
	items, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 10})
	if err != nil || len(items) != 4 {
		t.Fatalf("seed: %v (%d items)", err, len(items))
	}
	ids := make([]string, 0, 4)
	byTitle := map[string]string{}
	for _, it := range items {
		ids = append(ids, it.ID)
		byTitle[it.Title] = it.ID
	}

	star := true
	if _, err := repo.SetItemState(ctx, sc, byTitle["Article starred"], StateChange{Starred: &star}); err != nil {
		t.Fatalf("star: %v", err)
	}
	if err := repo.SetNote(ctx, sc, byTitle["Article noted"], "worth keeping"); err != nil {
		t.Fatalf("note: %v", err)
	}
	rating := 1
	if _, err := repo.SetItemState(ctx, sc, byTitle["Article rated"], StateChange{Rating: &rating}); err != nil {
		t.Fatalf("rate: %v", err)
	}
	return repo, sc, feed.SourceID, ids
}

// The one that matters.
func TestASweepNeverRemovesWhatSomebodyKept(t *testing.T) {
	repo, sc, _, _ := retentionFixture(t)
	ctx := context.Background()

	res, err := repo.SweepItems(ctx, time.Now().UTC(), true)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Examined != 4 {
		t.Fatalf("examined %d, want all four", res.Examined)
	}
	if res.KeptPinned != 3 {
		t.Errorf("kept %d, want the starred, the annotated and the rated", res.KeptPinned)
	}
	if res.Removed != 1 {
		t.Errorf("removed %d, want only the untouched one", res.Removed)
	}

	left, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 3 {
		t.Fatalf("%d items survived, want 3", len(left))
	}
	for _, it := range left {
		if it.Title == "Article plain" {
			t.Error("the untouched article survived, so nothing was actually removed")
		}
	}
}

// A dry run has to be exactly that.
func TestADryRunCountsAndDeletesNothing(t *testing.T) {
	repo, sc, _, _ := retentionFixture(t)
	ctx := context.Background()

	res, err := repo.SweepItems(ctx, time.Now().UTC(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Examined != 4 || res.KeptPinned != 3 {
		t.Errorf("preview = %+v, want 4 examined and 3 pinned", res)
	}
	if res.Removed != 0 {
		t.Errorf("a dry run reported %d removed", res.Removed)
	}

	left, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 4 {
		t.Errorf("%d items left after a DRY RUN, want all four", len(left))
	}
}

// Nothing old enough means nothing happens — and it must not report otherwise.
func TestAWindowThatCoversNothingRemovesNothing(t *testing.T) {
	repo, _, _, _ := retentionFixture(t)

	res, err := repo.SweepItems(context.Background(),
		time.Now().UTC().Add(-10*365*24*time.Hour), true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Examined != 0 || res.Removed != 0 {
		t.Errorf("a ten-year window over two-year-old articles = %+v", res)
	}
}

// The ledger is what makes the policy auditable, and it has to survive a
// restart — that is the whole difference between an audit trail and a log line.
func TestTheLedgerRecordsWhatWasRemoved(t *testing.T) {
	repo, _, _, _ := retentionFixture(t)
	ctx := context.Background()

	res, err := repo.SweepItems(ctx, time.Now().UTC(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordSweep(ctx, "items", 90, res, "test"); err != nil {
		t.Fatalf("record: %v", err)
	}

	sweeps, err := repo.RecentSweeps(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sweeps) != 1 {
		t.Fatalf("ledger has %d rows", len(sweeps))
	}
	got := sweeps[0]
	if got.PolicyDays != 90 {
		t.Errorf("policy_days = %d — the policy in force is copied in, so a later "+
			"change cannot rewrite what this sweep did", got.PolicyDays)
	}
	if got.Removed != 1 || got.KeptPinned != 3 {
		t.Errorf("ledger row = %+v, want 1 removed and 3 kept", got)
	}
}
