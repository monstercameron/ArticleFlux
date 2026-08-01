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
	"log"
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
		log.Fatalf("open %s: %v", *dbPath, err)
	}
	defer func() { _ = db.Close() }()

	repo := store.NewReaderRepo(db)

	// A limit far past any realistic local instance's item count — this is a
	// one-shot backfill, not a paged sweep, and RecentItemIDs' own comment
	// says 200 is its DEFAULT, not a ceiling.
	ids, err := repo.RecentItemIDs(ctx, 1_000_000)
	if err != nil {
		log.Fatalf("RecentItemIDs: %v", err)
	}
	fmt.Printf("found %d items\n", len(ids))

	var (
		scanned, withContent, mined, linkRows, skipped int
	)
	start := time.Now()

	for i := 0; i < len(ids); i += batchSize {
		end := min(i+batchSize, len(ids))
		batch := ids[i:end]

		items, err := repo.ItemsByID(ctx, batch)
		if err != nil {
			log.Fatalf("ItemsByID: %v", err)
		}

		// mustHaveSource associates each item with the source id RecordOutlinks
		// needs. ItemsByID already carries SourceID (internal/store.Item), so no
		// second query is required.
		for _, it := range items {
			scanned++
			if it.URL == "" || it.ContentHTML == "" {
				skipped++
				continue
			}
			withContent++

			links := outlinks.Extract(it.ContentHTML, it.URL, outlinks.Options{})
			if len(links) == 0 {
				continue
			}
			mined++
			linkRows += len(links)

			if *dryRun {
				continue
			}
			if err := repo.RecordOutlinks(ctx, it.ID, it.SourceID, links); err != nil {
				log.Printf("RecordOutlinks(%s): %v", it.ID, err)
			}
		}
		fmt.Printf("  ...%d/%d scanned\n", scanned, len(ids))
	}

	mode := "wrote"
	if *dryRun {
		mode = "would have written"
	}
	fmt.Printf("done in %s: scanned=%d withContent=%d skippedNoContent=%d itemsMined=%d %s linkRows=%d\n",
		time.Since(start).Round(time.Millisecond), scanned, withContent, skipped, mined, mode, linkRows)
}
