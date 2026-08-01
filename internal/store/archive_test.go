package store

import (
	"errors"
	"testing"
	"time"
)

// seedArchiveItems creates n items on one source, for tests that only need
// item_id foreign-key targets.
func seedArchiveItems(t *testing.T, db *DB, n int) []string {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO sources (id,natural_key,feed_url,title,created_at) VALUES ('src1','feed:a','https://a.example/feed','A',?)`, now); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := "item" + string(rune('a'+i))
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO items (id,source_id,guid,title,url,published_at,first_seen_at)
			 VALUES (?,?,?,?,?,?,?)`,
			id, "src1", "g"+id, "Title "+id, "https://a.example/"+id, now, now); err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	return ids
}

func TestPutArchiveThenGetArchiveRoundTrips(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 1)

	if err := repo.PutArchive(ctx, Archive{
		ItemID: ids[0], HTML: "<p>hi</p>", Text: "hi", Bytes: 42, Reason: "on_read",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetArchive(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.HTML != "<p>hi</p>" || got.Text != "hi" || got.Bytes != 42 || got.Reason != "on_read" {
		t.Errorf("got %+v", got)
	}
	if got.OriginDead {
		t.Error("a fresh archive reports OriginDead")
	}

	has, err := repo.HasArchive(ctx, ids[0])
	if err != nil || !has {
		t.Errorf("HasArchive = %v, %v; want true, nil", has, err)
	}
}

// PutArchive upserts: re-archiving (a revision, a re-run) must replace the
// content rather than erroring or duplicating the row.
func TestPutArchiveReplacesOnConflict(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 1)

	if err := repo.PutArchive(ctx, Archive{ItemID: ids[0], HTML: "old", Bytes: 10, Reason: "on_read"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutArchive(ctx, Archive{ItemID: ids[0], HTML: "new", Bytes: 20, Reason: "distress"}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetArchive(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.HTML != "new" || got.Bytes != 20 || got.Reason != "distress" {
		t.Errorf("got %+v, want the replaced values", got)
	}
	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM item_archives`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d rows after a re-archive, want 1", n)
	}
}

func TestGetArchiveNotFound(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	if _, err := repo.GetArchive(t.Context(), "nonexistent"); !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

func TestHasArchiveFalseWhenAbsent(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	has, err := repo.HasArchive(t.Context(), "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("HasArchive true for a row that was never archived")
	}
}

// GetArchive's own read must not evict itself: last_read_at is what eviction
// orders by, and a read that raced its own eviction would be a self-defeating
// cache.
func TestGetArchiveRecordsLastRead(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 1)
	if err := repo.PutArchive(ctx, Archive{ItemID: ids[0], HTML: "x", Bytes: 5, Reason: "on_read"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetArchive(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	var lastRead string
	if err := db.Read.QueryRowContext(ctx,
		`SELECT ifnull(last_read_at,'') FROM item_archives WHERE item_id = ?`, ids[0]).Scan(&lastRead); err != nil {
		t.Fatal(err)
	}
	if lastRead == "" {
		t.Error("GetArchive did not stamp last_read_at")
	}
}

// MarkOriginDead must create a row from nothing, so the UI can offer a Wayback
// link even for an item that was never archived.
func TestMarkOriginDeadCreatesARowWithNoArchive(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 1)

	if err := repo.MarkOriginDead(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetArchive(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if !got.OriginDead {
		t.Error("MarkOriginDead did not set OriginDead")
	}
	if got.HTML != "" {
		t.Errorf("HTML = %q, want empty (no archive content was ever written)", got.HTML)
	}
}

func TestMarkOriginDeadOnExistingArchivePreservesContent(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 1)
	if err := repo.PutArchive(ctx, Archive{ItemID: ids[0], HTML: "kept", Bytes: 5, Reason: "on_read"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkOriginDead(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetArchive(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.HTML != "kept" {
		t.Errorf("HTML = %q, MarkOriginDead must not clobber existing content", got.HTML)
	}
	if !got.OriginDead {
		t.Error("OriginDead was not set on an existing archive")
	}
}

func TestUnarchivedItemsListsOnlyThoseWithoutAnArchive(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 3)
	if err := repo.PutArchive(ctx, Archive{ItemID: ids[0], HTML: "x", Bytes: 1, Reason: "on_read"}); err != nil {
		t.Fatal(err)
	}
	out, err := repo.UnarchivedItems(ctx, "src1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d unarchived items, want 2", len(out))
	}
	for _, id := range out {
		if id == ids[0] {
			t.Error("UnarchivedItems listed an item that already has an archive")
		}
	}
}

// The one rule eviction may not break (§10.6): an origin-dead archive must
// never be dropped, even when it is the largest and least-recently-read row
// in the table — i.e. even when every OTHER heuristic says evict it first.
func TestEvictArchivesNeverDropsAnOriginDeadRow(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 2)

	// The dead-origin archive: large, never read — the profile eviction would
	// normally pick first.
	if err := repo.PutArchive(ctx, Archive{ItemID: ids[0], HTML: "big", Bytes: 10_000, Reason: "distress"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkOriginDead(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	// A small, evictable archive.
	if err := repo.PutArchive(ctx, Archive{ItemID: ids[1], HTML: "small", Bytes: 10, Reason: "on_read"}); err != nil {
		t.Fatal(err)
	}

	freed, n, err := repo.EvictArchives(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || freed != 10 {
		t.Errorf("evicted n=%d freed=%d, want n=1 freed=10 (only the small, live-origin row)", n, freed)
	}
	if has, _ := repo.HasArchive(ctx, ids[0]); !has {
		t.Fatal("the origin-dead archive was evicted — this must never happen (§10.6)")
	}
	if has, _ := repo.HasArchive(ctx, ids[1]); has {
		t.Error("the evictable archive is still present")
	}
}

func TestEvictArchivesZeroTargetIsANoOp(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 1)
	if err := repo.PutArchive(ctx, Archive{ItemID: ids[0], HTML: "x", Bytes: 100, Reason: "on_read"}); err != nil {
		t.Fatal(err)
	}
	freed, n, err := repo.EvictArchives(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if freed != 0 || n != 0 {
		t.Errorf("freed=%d n=%d, want 0,0 for a non-positive target", freed, n)
	}
	if has, _ := repo.HasArchive(ctx, ids[0]); !has {
		t.Error("EvictArchives(0) evicted something")
	}
}

func TestEvictArchivesStopsOnceTargetIsMet(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 3)
	for i, id := range ids {
		if err := repo.PutArchive(ctx, Archive{ItemID: id, HTML: "x", Bytes: 100, Reason: "on_read"}); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	freed, n, err := repo.EvictArchives(ctx, 150)
	if err != nil {
		t.Fatal(err)
	}
	// The loop breaks once accumulated freed >= target: one 100-byte row is not
	// enough, two are (200 >= 150), so exactly 2 rows should go.
	if n != 2 || freed != 200 {
		t.Errorf("n=%d freed=%d, want n=2 freed=200", n, freed)
	}
}

func TestArchiveStatsCountsBytesAndOriginDeadSeparately(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	ids := seedArchiveItems(t, db, 2)
	if err := repo.PutArchive(ctx, Archive{ItemID: ids[0], HTML: "x", Bytes: 30, Reason: "on_read"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutArchive(ctx, Archive{ItemID: ids[1], HTML: "y", Bytes: 70, Reason: "distress"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkOriginDead(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}

	st, err := repo.ArchiveStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Count != 2 {
		t.Errorf("Count = %d, want 2", st.Count)
	}
	if st.Bytes != 100 {
		t.Errorf("Bytes = %d, want 100", st.Bytes)
	}
	if st.OriginDead != 1 {
		t.Errorf("OriginDead = %d, want 1", st.OriginDead)
	}
	if st.UnevictableBytes != 70 {
		t.Errorf("UnevictableBytes = %d, want 70", st.UnevictableBytes)
	}
}

func TestArchiveStatsOnEmptyTable(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	st, err := repo.ArchiveStats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.Count != 0 || st.Bytes != 0 || st.OriginDead != 0 || st.UnevictableBytes != 0 {
		t.Errorf("empty-table stats = %+v, want all zero", st)
	}
}
