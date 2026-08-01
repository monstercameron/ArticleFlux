package store

import (
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/outlinks"
	"github.com/monstercameron/ArticleFlux/internal/signals"
)

func TestRecordOutlinksStoresLinksAndSkipsEmpty(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 1)

	err := repo.RecordOutlinks(ctx, ids[0], "src1", []outlinks.Link{
		{Host: "blog.example", URL: "https://blog.example/post"},
		{Host: "blog.example", URL: "https://blog.example/post2"},
		// Missing host or url must be skipped rather than written as junk rows.
		{Host: "", URL: "https://nohost.example/x"},
		{Host: "nourl.example", URL: ""},
	})
	if err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM outlinks WHERE item_id = ?`, ids[0]).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("stored %d outlinks, want 2 (the two with both host and url)", n)
	}
}

// Re-extraction must REPLACE, not accumulate — otherwise a revision or a
// re-run of the harvester doubles every link and inflates the domain evidence.
func TestRecordOutlinksReplacesOnReExtraction(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 1)

	if err := repo.RecordOutlinks(ctx, ids[0], "src1", []outlinks.Link{
		{Host: "old.example", URL: "https://old.example/a"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordOutlinks(ctx, ids[0], "src1", []outlinks.Link{
		{Host: "new.example", URL: "https://new.example/b"},
	}); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM outlinks WHERE item_id = ?`, ids[0]).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d outlink rows after re-extraction, want 1", n)
	}
	var domain string
	if err := db.Read.QueryRowContext(ctx, `SELECT target_domain FROM outlinks WHERE item_id = ?`, ids[0]).Scan(&domain); err != nil {
		t.Fatal(err)
	}
	if domain != "new.example" {
		t.Errorf("target_domain = %q, want new.example (old one replaced)", domain)
	}
}

func TestRecordOutlinksWithNoLinksClearsExisting(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 1)
	if err := repo.RecordOutlinks(ctx, ids[0], "src1", []outlinks.Link{
		{Host: "old.example", URL: "https://old.example/a"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordOutlinks(ctx, ids[0], "src1", nil); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM outlinks WHERE item_id = ?`, ids[0]).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d outlinks remain after recording an empty link set, want 0", n)
	}
}

// OutlinkCandidates is the join that makes this feature per-reader: outlinks
// are global, engagement is per user, and the intersection must not leak one
// tenant's reading into a recommendation for another.
func TestOutlinkCandidatesJoinsOutlinksWithOwnEngagement(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := t.Context()
	it := firstItem(t, repo, sc)

	if err := repo.RecordOutlinks(ctx, it.ID, it.SourceID, []outlinks.Link{
		{Host: "referenced.example", URL: "https://referenced.example/a"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecordEngagements(ctx, sc, []signals.Event{
		{ID: "eng1", ItemID: it.ID, Kind: signals.Opened, Surface: signals.SurfaceList, At: time.Now().UnixMilli()},
	}); err != nil {
		t.Fatal(err)
	}

	out, err := repo.OutlinkCandidates(ctx, sc, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range out {
		if e.Domain == "referenced.example" {
			found = true
			if e.LinkCount < 1 {
				t.Errorf("LinkCount = %d, want >= 1", e.LinkCount)
			}
		}
	}
	if !found {
		t.Errorf("candidates = %+v, missing referenced.example", out)
	}
}
