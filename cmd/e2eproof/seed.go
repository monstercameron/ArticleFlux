package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"

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
	db, err := driver.Open("file:"+src+"?mode=ro&_pragma=query_only(1)", fts5.Register)
	if err != nil {
		return err
	}
	defer db.Close()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t", Name: "T", UserID: "u", Username: "u",
		Hash: "x", Role: "member", Now: now,
	}); err != nil {
		return err
	}
	sc := store.Scope{TenantID: "t", UserID: "u", Role: "member"}

	rows, err := db.QueryContext(ctx, `
		SELECT ifnull(s.title,'Feed'), ifnull(s.feed_url,'https://x/'||s.id),
		       i.guid, ifnull(i.url,''), i.title, ifnull(i.summary,''),
		       ifnull(i.content_html,''), i.published_at, i.word_count
		  FROM items i JOIN sources s ON s.id = i.source_id
		 ORDER BY i.published_at DESC LIMIT ?`, limit)
	if err != nil {
		return err
	}
	defer rows.Close()

	bySource := map[string][]store.IngestItem{}
	urls := map[string]string{}
	for rows.Next() {
		var sTitle, sURL, guid, url, title, summary, body, pub string
		var wc int
		if err := rows.Scan(&sTitle, &sURL, &guid, &url, &title, &summary, &body, &pub, &wc); err != nil {
			return err
		}
		at, _ := time.Parse(time.RFC3339Nano, pub)
		bySource[sTitle] = append(bySource[sTitle], store.IngestItem{
			GUID: guid, URL: url, Title: title, Summary: summary,
			ContentHTML: body, PublishedAt: at, WordCount: wc,
		})
		urls[sTitle] = sURL
	}
	if err := rows.Err(); err != nil {
		return err
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
