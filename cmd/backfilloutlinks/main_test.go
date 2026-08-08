package main

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The backfill had no test because it had no seam: everything lived in `main`,
// talked to a hardcoded database and exited on error. That is the shape TODO.md
// describes for the tools left dead after the coverage campaign, and the reason
// it matters here more than for the other one-shots is that this one WRITES —
// it walks every item on a real instance and replaces rows. A one-shot data fix
// gets run once, in anger, against the only copy of the data.
//
// The two properties worth holding are the ones its own doc comment claims:
// re-running does not double anything, and -dry-run touches nothing.

// fixture builds an instance with three items: two carrying content with
// outbound links, and one carrying none.
func fixture(t *testing.T) (*store.DB, *store.ReaderRepo) {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "backfill.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}

	f, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:a", FeedURL: "https://source.example/feed", Title: "Source",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := repo.IngestItems(ctx, f.SourceID, []store.IngestItem{
		{
			GUID: "g1", Title: "Two outbound links", URL: "https://source.example/one",
			ContentHTML: `<p>see <a href="https://alpha.example/x">alpha</a> and ` +
				`<a href="https://beta.example/y">beta</a></p>`,
			PublishedAt: time.Now(),
		},
		{
			GUID: "g2", Title: "One outbound link", URL: "https://source.example/two",
			ContentHTML: `<p>only <a href="https://alpha.example/z">alpha</a></p>`,
			PublishedAt: time.Now().Add(-time.Hour),
		},
		{
			// The skip case: an item the extractor cannot work on. Every instance
			// has these — a summary-only feed — and counting them as failures
			// would make the report read like the backfill was broken.
			GUID: "g3", Title: "No content at all", URL: "https://source.example/three",
			PublishedAt: time.Now().Add(-2 * time.Hour),
		},
	}); err != nil {
		t.Fatal(err)
	}
	return db, repo
}

func outlinkRows(t *testing.T, db *store.DB) int {
	t.Helper()
	var n int
	if err := db.Read.QueryRow(`SELECT count(*) FROM outlinks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTheBackfillMinesEveryItemThatHasContent(t *testing.T) {
	db, repo := fixture(t)

	s, err := run(context.Background(), repo, false, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if s.Scanned != 3 {
		t.Errorf("scanned = %d, want the 3 items in the fixture", s.Scanned)
	}
	if s.WithContent != 2 {
		t.Errorf("withContent = %d, want 2", s.WithContent)
	}
	if s.Skipped != 1 {
		t.Errorf("skipped = %d, want the one item with no content", s.Skipped)
	}
	if s.Mined != 2 {
		t.Errorf("itemsMined = %d, want 2", s.Mined)
	}
	if s.LinkRows != 3 {
		t.Errorf("linkRows = %d, want 3 (two links plus one)", s.LinkRows)
	}
	// The counters are a report; the table is the point. A pass that counts
	// correctly and writes nothing would satisfy every assertion above.
	if got := outlinkRows(t, db); got != 3 {
		t.Errorf("the outlinks table holds %d rows, want 3 — the backfill counted "+
			"work it did not do", got)
	}
}

// The claim in the command's own doc comment: safe to re-run. RecordOutlinks
// replaces rather than appends, and if that ever changed, an operator who ran
// the backfill twice would double every piece of "linked here N times" evidence
// Discover shows — which climbs on its own and looks like a real signal.
func TestRunningTheBackfillTwiceDoesNotDoubleAnything(t *testing.T) {
	db, repo := fixture(t)
	ctx := context.Background()

	if _, err := run(ctx, repo, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	first := outlinkRows(t, db)

	if _, err := run(ctx, repo, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if second := outlinkRows(t, db); second != first {
		t.Errorf("a second pass took the table from %d rows to %d", first, second)
	}
}

// -dry-run reports exactly what the real pass would do and writes nothing. A
// dry run that under-reports is worse than none: it is the number the operator
// decides on.
func TestDryRunReportsTheSameWorkAndWritesNothing(t *testing.T) {
	db, repo := fixture(t)
	ctx := context.Background()

	dry, err := run(ctx, repo, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if got := outlinkRows(t, db); got != 0 {
		t.Fatalf("-dry-run wrote %d rows", got)
	}

	wet, err := run(ctx, repo, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Mined != wet.Mined || dry.LinkRows != wet.LinkRows {
		t.Errorf("dry run predicted mined=%d linkRows=%d, the real pass did mined=%d linkRows=%d",
			dry.Mined, dry.LinkRows, wet.Mined, wet.LinkRows)
	}
	if !dry.DryRun || wet.DryRun {
		t.Error("the report does not say which mode it ran in")
	}
	if !strings.Contains(dry.String(), "would have written") {
		t.Errorf("a dry run's report reads as though it wrote: %q", dry.String())
	}
	if !strings.Contains(wet.String(), "wrote") {
		t.Errorf("a real pass's report does not say it wrote: %q", wet.String())
	}
}

// An empty instance is a fresh install, and the pass has to complete on one
// rather than dividing by a count it does not have.
func TestTheBackfillOnAnEmptyInstanceDoesNothingQuietly(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "empty.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	s, err := run(ctx, store.NewReaderRepo(db), false, io.Discard)
	if err != nil {
		t.Fatalf("run on an empty instance: %v", err)
	}
	if s.Scanned != 0 || s.Mined != 0 {
		t.Errorf("an empty instance reported %+v", s)
	}
}

// A closed database is the operator pointing at the wrong file, or a disk that
// went away mid-pass. It has to come back as an error rather than as a report
// saying nothing needed doing.
func TestAFailingStoreIsReportedRatherThanCountedAsNoWork(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "closed.db")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := run(ctx, repo, false, io.Discard); err == nil {
		t.Error("a closed database produced a clean report")
	}
}
