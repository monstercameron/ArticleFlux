// Command backfilloutlinks is a one-shot fix for the gap Cam found
// (2026-08-01): internal/recommend's rung 1 (outlink mining, §18.7) depends
// entirely on the `outlinks` table, and nothing in the ingest pipeline wrote
// to it before internal/reader.Service.mineOutlinks was added — every item
// ingested before that fix has real content_html sitting in the database with
// no outlinks ever extracted from it. This walks every existing item once and
// mines it, so a reader's PAST reading/liking/disliking history becomes
// immediately usable by Discover instead of only future articles.
//
// Safe to re-run: RecordOutlinks REPLACES a given item's rows rather than
// appending, so running this twice does not double anything.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/outlinks"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// batchSize keeps each ItemsByID call well under SQLite's bound-parameter
// ceiling (~999) while still moving through a large table in a handful of
// round trips rather than one per item.
const batchSize = 500

func main() {
	dbPath := flag.String("db", "articleflux.db", "path to the SQLite database")
	dryRun := flag.Bool("dry-run", false, "report what would be mined without writing anything")
	flag.Parse()

	ctx := context.Background()
	db, err := store.Open(store.Options{Path: *dbPath})
	if err != nil {
		fmt.Fprintf(os.Stderr, "backfilloutlinks: open %s: %v\n", *dbPath, err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	s, err := run(ctx, store.NewReaderRepo(db), *dryRun, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "backfilloutlinks:", err)
		os.Exit(1)
	}
	fmt.Print(s.String())
}

// stats is what one pass did, which is the only thing a caller wants back.
//
// Returned rather than printed from inside run, for the reason precompress's
// does: the counters are the evidence the backfill worked, and a function that
// only prints them can be run but not checked.
type stats struct {
	Scanned     int
	WithContent int
	Skipped     int
	Mined       int
	LinkRows    int
	DryRun      bool
	Elapsed     time.Duration
}

func (s stats) String() string {
	mode := "wrote"
	if s.DryRun {
		mode = "would have written"
	}
	return fmt.Sprintf("done in %s: scanned=%d withContent=%d skippedNoContent=%d itemsMined=%d %s linkRows=%d\n",
		s.Elapsed.Round(time.Millisecond), s.Scanned, s.WithContent, s.Skipped, s.Mined, mode, s.LinkRows)
}

// run mines every item in the database once.
//
// Takes the repository rather than a path so a test can drive it against a
// fixture, and reports progress to a writer rather than to stdout so that
// writer can be discarded. A nil progress writer is fine.
//
// An error from the store ends the pass. A failure to RECORD one item's links
// does not: the backfill is re-runnable by design, and stopping the whole walk
// because one row would not write means the operator re-runs from the start for
// a row that will fail again.
func run(ctx context.Context, repo *store.ReaderRepo, dryRun bool, progress io.Writer) (stats, error) {
	if progress == nil {
		progress = io.Discard
	}
	s := stats{DryRun: dryRun}
	start := time.Now()

	// A limit far past any realistic local instance's item count — this is a
	// one-shot backfill, not a paged sweep, and RecentItemIDs' own comment
	// says 200 is its DEFAULT, not a ceiling.
	ids, err := repo.RecentItemIDs(ctx, 1_000_000)
	if err != nil {
		return s, fmt.Errorf("RecentItemIDs: %w", err)
	}
	fmt.Fprintf(progress, "found %d items\n", len(ids))

	for i := 0; i < len(ids); i += batchSize {
		end := min(i+batchSize, len(ids))
		batch := ids[i:end]

		items, err := repo.ItemsByID(ctx, batch)
		if err != nil {
			return s, fmt.Errorf("ItemsByID: %w", err)
		}

		// Each item carries the source id RecordOutlinks needs. ItemsByID
		// already returns SourceID (internal/store.Item), so no second query is
		// required.
		for _, it := range items {
			s.Scanned++
			if it.URL == "" || it.ContentHTML == "" {
				s.Skipped++
				continue
			}
			s.WithContent++

			links := outlinks.Extract(it.ContentHTML, it.URL, outlinks.Options{})
			if len(links) == 0 {
				continue
			}
			s.Mined++
			s.LinkRows += len(links)

			if dryRun {
				continue
			}
			if err := repo.RecordOutlinks(ctx, it.ID, it.SourceID, links); err != nil {
				fmt.Fprintf(progress, "  !! RecordOutlinks(%s): %v\n", it.ID, err)
			}
		}
		fmt.Fprintf(progress, "  ...%d/%d scanned\n", s.Scanned, len(ids))
	}

	s.Elapsed = time.Since(start)
	return s, nil
}
