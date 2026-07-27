package main

import (
	"context"
	"fmt"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// seedFrom copies real articles out of a live database (read-only) into a fresh
// one, through the REAL ingest path.
//
// It exists because the live database cannot currently be migrated — an
// already-applied migration was edited after the fact — and the point of this
// program is to prove the analysis path against real articles rather than
// against fixtures. Copying the rows forward gets the same evidence without
// touching the file that is in trouble.
func seedFrom(ctx context.Context, src string, repo *store.ReaderRepo, limit int) error {
	seed, err := store.LoadIngestSeed(ctx, src, limit)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t", Name: "T", UserID: "u", Username: "u",
		Hash: "x", Role: "member", Now: now,
	}); err != nil {
		return err
	}
	sc := store.Scope{TenantID: "t", UserID: "u", Role: "member"}

	bySource := map[string][]store.IngestItem{}
	urls := map[string]string{}
	for _, row := range seed {
		bySource[row.SourceTitle] = append(bySource[row.SourceTitle], row.Item)
		urls[row.SourceTitle] = row.FeedURL
	}

	total := 0
	for title, items := range bySource {
		feed, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
			NaturalKey: "feed:" + urls[title], FeedURL: urls[title], Title: title,
		})
		if err != nil {
			continue
		}
		res, err := repo.IngestItems(ctx, feed.SourceID, items)
		if err != nil {
			continue
		}
		total += res.New
	}
	fmt.Printf("seeded %d real articles from %d feeds\n", total, len(bySource))
	return nil
}
