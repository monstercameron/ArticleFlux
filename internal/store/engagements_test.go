package store

import (
	"context"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/signals"
)

// firstItem returns one seeded item and the source it belongs to.
func firstItem(t *testing.T, repo *ReaderRepo, sc Scope) Item {
	t.Helper()
	items, _, err := repo.ListItems(context.Background(), sc, ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("fixture produced no items")
	}
	return items[0]
}

func TestEngagementsRoundTrip(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	it := firstItem(t, repo, sc)
	now := time.Now().UnixMilli()

	evs := []signals.Event{
		{ID: "e1", ItemID: it.ID, Kind: signals.Impression, Surface: signals.SurfaceList, At: now},
		{ID: "e2", ItemID: it.ID, Kind: signals.Opened, Surface: signals.SurfaceList, At: now + 100},
		{ID: "e3", ItemID: it.ID, Kind: signals.Dwell, Value: 42_000, Surface: signals.SurfaceReader, At: now + 200},
		{ID: "e4", ItemID: it.ID, Kind: signals.ScrollDepth, Value: 0.9, Surface: signals.SurfaceReader, At: now + 300},
	}
	n, err := repo.RecordEngagements(ctx, sc, evs)
	if err != nil {
		t.Fatalf("RecordEngagements: %v", err)
	}
	if n != 4 {
		t.Fatalf("wrote %d, want 4", n)
	}

	got, err := repo.ItemSignals(ctx, sc, []string{it.ID})
	if err != nil {
		t.Fatal(err)
	}
	sig, ok := got[it.ID]
	if !ok {
		t.Fatal("no rollup for the item just written")
	}
	if sig.Counts[signals.Opened] != 1 {
		t.Errorf("opens = %d, want 1", sig.Counts[signals.Opened])
	}
	if sig.DwellMS != 42_000 {
		t.Errorf("dwell = %d, want 42000", sig.DwellMS)
	}
	if sig.MaxDepth != 0.9 {
		t.Errorf("depth = %v, want 0.9", sig.MaxDepth)
	}
	// The client never sent a source id; the server resolved it from the item so
	// a client cannot invent an attribution.
	if sig.SourceID != it.SourceID {
		t.Errorf("source = %q, want %q resolved from the item", sig.SourceID, it.SourceID)
	}
}

// The outbox retries anything it could not confirm, so this is the normal case
// and not an edge case.
func TestEngagementReplayIsDropped(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	it := firstItem(t, repo, sc)
	now := time.Now().UnixMilli()

	batch := []signals.Event{
		{ID: "dup", ItemID: it.ID, Kind: signals.Dwell, Value: 60_000, Surface: signals.SurfaceReader, At: now},
	}
	if _, err := repo.RecordEngagements(ctx, sc, batch); err != nil {
		t.Fatal(err)
	}
	n, err := repo.RecordEngagements(ctx, sc, batch)
	if err != nil {
		t.Fatalf("a replay must not error: %v", err)
	}
	if n != 0 {
		t.Errorf("replay wrote %d rows, want 0", n)
	}

	sig, err := repo.ItemSignals(ctx, sc, []string{it.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := sig[it.ID].DwellMS; got != 60_000 {
		t.Errorf("dwell = %d after a replay, want 60000 — double-counted dwell is the "+
			"difference between 'read carefully' and 'left the tab open'", got)
	}
}

func TestEngagementClockIsClamped(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	it := firstItem(t, repo, sc)
	now := time.Now().UnixMilli()
	future := now + 30*24*3600*1000

	if _, err := repo.RecordEngagements(ctx, sc, []signals.Event{
		{ID: "e1", ItemID: it.ID, Kind: signals.Opened, Surface: signals.SurfaceList, At: future},
	}); err != nil {
		t.Fatal(err)
	}

	evs, err := repo.EngagementsSince(ctx, sc, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	if evs[0].At > now+signals.MaxSkewMS {
		t.Errorf("at = %d, want clamped to about %d — a client claiming next month "+
			"would otherwise sit in every recency window forever", evs[0].At, now)
	}
}

// The row that would quietly destroy weeks of signal.
//
// This has to construct the fixture so the exclusion is actually load-bearing,
// not merely present. A bulk_read or sync_read sharing an item_id with a real
// `opened`/`impression` row proves nothing: that item_id is already in
// count(DISTINCT item_id) because of the row that was never meant to be
// excluded, so the SAME assertions pass whether or not the `kind NOT IN
// (...)` clause does anything at all. Two more items are seeded that carry
// ONLY a bulk_read or ONLY a sync_read row and nothing else — a real shape,
// since both are single events a mark-all-read or a syncing client can emit
// for an item nobody individually opened — and Items/LastEngagedAt are
// checked precisely because those two are the aggregates a leaked row would
// move: DISTINCT item_id would gain an id that should never have entered the
// count, and max(at) would jump to whichever excluded row's timestamp is
// latest.
func TestFeedSignalsExcludeBulkReads(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	it := firstItem(t, repo, sc)

	all, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var sibling1, sibling2 string
	for _, other := range all {
		if other.SourceID != it.SourceID || other.ID == it.ID {
			continue
		}
		if sibling1 == "" {
			sibling1 = other.ID
		} else if sibling2 == "" {
			sibling2 = other.ID
			break
		}
	}
	if sibling1 == "" || sibling2 == "" {
		t.Fatal("fixture's first source needs at least 3 items for this test")
	}

	now := time.Now().UnixMilli()

	evs := []signals.Event{
		// The only real engagement: it.ID, at now/now+1.
		{ID: "i1", ItemID: it.ID, Kind: signals.Impression, Surface: signals.SurfaceList, At: now},
		{ID: "o1", ItemID: it.ID, Kind: signals.Opened, Surface: signals.SurfaceList, At: now + 1},
		// A backlog item swept by mark-all-read, never individually opened.
		// Far later timestamp: if this leaks past the exclusion, max(at) moves.
		{ID: "b1", ItemID: sibling1, Kind: signals.BulkRead, Surface: signals.SurfaceList, At: now + 1000},
		// A third-party client auto-marking a different backlog item, same idea.
		{ID: "s1", ItemID: sibling2, Kind: signals.SyncRead, Surface: signals.SurfaceSyncAPI, At: now + 2000},
	}
	if _, err := repo.RecordEngagements(ctx, sc, evs); err != nil {
		t.Fatal(err)
	}

	feeds, err := repo.FeedSignals(ctx, sc, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range feeds {
		if f.SourceID != it.SourceID {
			continue
		}
		found = true
		if f.Impressions != 1 || f.Opens != 1 {
			t.Errorf("impressions=%d opens=%d, want 1/1", f.Impressions, f.Opens)
		}
		if f.Items != 1 {
			t.Errorf("items=%d, want 1 — the bulk_read/sync_read-only items must not "+
				"enter count(DISTINCT item_id)", f.Items)
		}
		if f.LastEngagedAt != now+1 {
			t.Errorf("last_engaged_at=%d, want %d (the `opened` row) — a bulk_read or "+
				"sync_read timestamp leaked into max(at)", f.LastEngagedAt, now+1)
		}
		if f.OpenRate() != 1 {
			t.Errorf("open rate = %v, want 1", f.OpenRate())
		}
	}
	if !found {
		t.Fatal("no rollup for the seeded source")
	}

	// The raw log still holds them — they are excluded from affinity, not from
	// history, which is what makes the formula re-derivable.
	raw, err := repo.EngagementsSince(ctx, sc, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 4 {
		t.Errorf("raw log has %d events, want all 4 kept", len(raw))
	}
}

func TestEngagementsAreTenantScoped(t *testing.T) {
	db := openTest(t)
	repoA, scA := seedReader(t, db)
	ctx := context.Background()
	itA := firstItem(t, repoA, scA)

	if _, err := repoA.RecordEngagements(ctx, scA, []signals.Event{
		{ID: "a1", ItemID: itA.ID, Kind: signals.Liked, Surface: signals.SurfaceReader,
			At: time.Now().UnixMilli()},
	}); err != nil {
		t.Fatal(err)
	}

	// A second tenant over the same global item (A14) must see none of it.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repoA.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t2", Name: "Other", UserID: "u2", Username: "other",
		Hash: "x", Role: "member", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	scB := Scope{TenantID: "t2", UserID: "u2", Role: "member"}

	got, err := repoA.ItemSignals(ctx, scB, []string{itA.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("tenant B saw %d of tenant A's rollups over a shared item", len(got))
	}
	n, err := repoA.CountEngagements(ctx, scB)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("tenant B counted %d of tenant A's engagements", n)
	}
}

func TestRecordEngagementsRejectsUnscopedAndOversized(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	if _, err := repo.RecordEngagements(ctx, Scope{}, []signals.Event{{ID: "x"}}); err != ErrNoScope {
		t.Errorf("unscoped write = %v, want ErrNoScope", err)
	}
	big := make([]signals.Event, MaxEngagementBatch+1)
	if _, err := repo.RecordEngagements(ctx, sc, big); err == nil {
		t.Error("an oversized batch should be rejected, not silently truncated")
	}
}
