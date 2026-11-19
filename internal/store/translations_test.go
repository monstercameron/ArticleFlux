package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// §10.5b: the translation cache is global per item and access-checked per
// scope. These pin both halves.

func TestItemTranslationIsInvisibleAcrossTenants(t *testing.T) {
	db := openTest(t)
	repo, a, b, bIDs := twoTenants(t, db)
	ctx := context.Background()
	item := bIDs["itemID"]

	if err := repo.PutItemTranslation(ctx, b, ItemTranslation{
		ItemID: item, Lang: "fr", SourceHash: "h1", Title: leakMarker + "-fr",
		BodyHTML: "<p>secret</p>",
	}); err != nil {
		t.Fatal(err)
	}
	// B reads its own.
	got, err := repo.ItemTranslation(ctx, b, item, "fr")
	if err != nil || got.Title != leakMarker+"-fr" || got.SourceHash != "h1" {
		t.Fatalf("B's own translation: %+v, %v", got, err)
	}

	// A cannot read it, and the refusal is the same as for a translation that
	// does not exist — "forbidden" would confirm somebody reads that item.
	if _, err := repo.ItemTranslation(ctx, a, item, "fr"); !errors.Is(err, ErrNotFound) {
		t.Errorf("A read B's private item translation: err = %v", err)
	}
	if _, _, err := repo.ItemLanguage(ctx, a, item); !errors.Is(err, ErrNotFound) {
		t.Errorf("A read B's private item language: err = %v", err)
	}
	// Nor overwrite it.
	if err := repo.PutItemTranslation(ctx, a, ItemTranslation{
		ItemID: item, Lang: "fr", Title: "planted",
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("A wrote a translation against B's item: err = %v", err)
	}
	if got, _ := repo.ItemTranslation(ctx, b, item, "fr"); got.Title != leakMarker+"-fr" {
		t.Errorf("B's translation was overwritten: %q", got.Title)
	}
	if _, err := repo.ItemTranslation(ctx, Scope{}, item, "fr"); !errors.Is(err, ErrNoScope) {
		t.Errorf("an empty scope: err = %v, want ErrNoScope", err)
	}
}

func TestItemTranslationIsSharedBetweenReadersOfTheSameItem(t *testing.T) {
	db := openTest(t)
	repo, a, b, _ := twoTenants(t, db)
	ctx := context.Background()

	// An item on the source BOTH tenants subscribe to.
	items, _, err := repo.ListItems(ctx, a, ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var shared string
	for _, it := range items {
		shared = it.ID
	}
	if shared == "" {
		src, _, err := repo.Subscribe(ctx, a, NewSubscription{
			NaturalKey: "feed:shared.example/rss", FeedURL: "https://shared.example/rss",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.IngestItems(ctx, src.SourceID, []IngestItem{{
			GUID: "shared-1", URL: "https://shared.example/1", Title: "Hallo Welt",
			ContentHTML: "<p>Hallo</p>", PublishedAt: time.Now().UTC(),
		}}); err != nil {
			t.Fatal(err)
		}
		items, _, _ = repo.ListItems(ctx, a, ListQuery{Limit: 10})
		shared = items[0].ID
	}

	if err := repo.PutItemTranslation(ctx, a, ItemTranslation{
		ItemID: shared, Lang: "en", SourceHash: "h", Title: "Hello world", Model: "m",
	}); err != nil {
		t.Fatal(err)
	}
	// B subscribes to the same global source, so B is served A's translation —
	// paid once per instance — and nothing in the row says A asked.
	got, err := repo.ItemTranslation(ctx, b, shared, "en")
	if err != nil || got.Title != "Hello world" {
		t.Fatalf("B could not reuse the shared translation: %+v, %v", got, err)
	}
	var cols []string
	rows, err := db.Read.QueryContext(ctx, `SELECT name FROM pragma_table_info('item_translations')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		cols = append(cols, n)
	}
	for _, c := range cols {
		if strings.Contains(c, "user") || strings.Contains(c, "tenant") {
			t.Errorf("item_translations carries %q — the cache must not record who asked", c)
		}
	}
}
