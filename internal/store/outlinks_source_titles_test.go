package store

import (
	"context"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/outlinks"
	"github.com/monstercameron/ArticleFlux/internal/signals"
)

// "Say the fucking source" (Cam, 2026-08-01): OutlinkCandidates must resolve
// and name the actual linking feeds, not just count them — seedReader's two
// fixture feeds are literally titled "Alpha Journal" and "Beta Notes" for
// exactly this test to use.
func TestOutlinkCandidatesNamesTheLinkingSources(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	items, _, err := repo.ListItems(ctx, sc, ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	bySource := map[string]Item{}
	for _, it := range items {
		if _, ok := bySource[it.SourceID]; !ok {
			bySource[it.SourceID] = it
		}
	}
	if len(bySource) != 2 {
		t.Fatalf("fixture has %d distinct sources, want 2", len(bySource))
	}

	now := time.Now().UnixMilli()
	i := 0
	for _, it := range bySource {
		if err := repo.RecordOutlinks(ctx, it.ID, it.SourceID, []outlinks.Link{
			{Host: "shared.example", URL: "https://shared.example/a"},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.RecordEngagements(ctx, sc, []signals.Event{
			{ID: "eng-src-" + it.SourceID, ItemID: it.ID, Kind: signals.Opened,
				Surface: signals.SurfaceList, At: now + int64(i)},
		}); err != nil {
			t.Fatal(err)
		}
		i++
	}

	out, err := repo.OutlinkCandidates(ctx, sc, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got OutlinkEvidence
	found := false
	for _, e := range out {
		if e.Domain == "shared.example" {
			got = e
			found = true
		}
	}
	if !found {
		t.Fatalf("shared.example missing from candidates: %+v", out)
	}
	if got.DistinctSources != 2 {
		t.Errorf("DistinctSources = %d, want 2", got.DistinctSources)
	}

	want := map[string]bool{"Alpha Journal": true, "Beta Notes": true}
	if len(got.SourceTitles) != 2 {
		t.Fatalf("SourceTitles = %v, want both fixture feed titles", got.SourceTitles)
	}
	for _, title := range got.SourceTitles {
		if !want[title] {
			t.Errorf("SourceTitles contains unexpected title %q", title)
		}
		delete(want, title)
	}
	if len(want) != 0 {
		t.Errorf("SourceTitles is missing %v", want)
	}
}

// SourceTitles must stay capped at sourceTitleCap even when more sources than
// that linked the same domain — an uncapped list is exactly the "everything
// dumped on screen" failure the evidence sentence exists to avoid.
func TestOutlinkCandidatesCapsSourceTitles(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}

	const n = 5 // more than sourceTitleCap (3)
	nowMS := time.Now().UnixMilli()
	for i := 0; i < n; i++ {
		key := "feed:" + string(rune('a'+i))
		feed, _, err := repo.Subscribe(ctx, sc, NewSubscription{
			NaturalKey: key,
			FeedURL:    "https://" + string(rune('a'+i)) + ".example/feed",
			Title:      "Feed " + string(rune('A'+i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.IngestItems(ctx, feed.SourceID, []IngestItem{
			{GUID: feed.SourceID + "-1", Title: "post", PublishedAt: time.Now()},
		}); err != nil {
			t.Fatal(err)
		}
		items, _, err := repo.ListItems(ctx, sc, ListQuery{SourceID: feed.SourceID, Limit: 1})
		if err != nil || len(items) == 0 {
			t.Fatal("no item for", feed.SourceID)
		}
		if err := repo.RecordOutlinks(ctx, items[0].ID, feed.SourceID, []outlinks.Link{
			{Host: "capped.example", URL: "https://capped.example/x"},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.RecordEngagements(ctx, sc, []signals.Event{
			{ID: "eng-cap-" + feed.SourceID, ItemID: items[0].ID, Kind: signals.Opened,
				Surface: signals.SurfaceList, At: nowMS + int64(i)},
		}); err != nil {
			t.Fatal(err)
		}
	}

	out, err := repo.OutlinkCandidates(ctx, sc, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range out {
		if e.Domain == "capped.example" {
			if e.DistinctSources != n {
				t.Errorf("DistinctSources = %d, want %d", e.DistinctSources, n)
			}
			if len(e.SourceTitles) != sourceTitleCap {
				t.Errorf("SourceTitles has %d entries, want the cap of %d: %v",
					len(e.SourceTitles), sourceTitleCap, e.SourceTitles)
			}
			return
		}
	}
	t.Fatalf("capped.example missing from candidates: %+v", out)
}
