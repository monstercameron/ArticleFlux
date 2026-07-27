package grpcsrv

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The transport half of §27's category read path: proving that a category
// resolved by store.CategoriesFor actually reaches the wire item, through the
// same ListItems and GetItem RPCs a real client calls, rather than only
// through the repository the RPC layer sits on top of.

func newCategoryServer(t *testing.T) (*ReaderServer, *store.ReaderRepo, store.Scope) {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := store.NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}

	f, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:a", FeedURL: "https://a.example/feed", Title: "Alpha Journal",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	ing, err := repo.IngestItems(ctx, f.SourceID, []store.IngestItem{
		{GUID: "g1", Title: "A CVE in the wild", PublishedAt: time.Now()},
		{GUID: "g2", Title: "Nothing scores here", PublishedAt: time.Now().Add(-time.Hour)},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(ing.NewIDs) != 2 {
		t.Fatalf("ingested %d items, want 2", len(ing.NewIDs))
	}

	svc := reader.New(repo, nil)
	srv := NewReaderServer(svc, func(context.Context) (store.Scope, error) { return sc, nil })
	return srv, repo, sc
}

func TestListItemsPopulatesCategoryFromStoredScores(t *testing.T) {
	srv, repo, _ := newCategoryServer(t)
	ctx := context.Background()

	feeds, err := srv.ListFeeds(ctx, &pb.ListFeedsRequest{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	items, _, err := srv.svc.ListItems(ctx, store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"},
		store.ListQuery{SourceID: feeds.GetFeeds()[0].GetSourceId(), Limit: 10})
	if err != nil {
		t.Fatalf("seed ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	// items[0] is "A CVE in the wild" (newest, published_at DESC): clears the
	// floor. items[1] is "Nothing scores here": nothing does.
	if err := repo.UpsertAnalysis(ctx, []store.ItemAnalysis{
		{ItemID: items[0].ID, AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{"security": 8.0}, AnalyzedAt: time.Now().UTC()},
		{ItemID: items[1].ID, AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{"security": 0.5}, AnalyzedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatalf("UpsertAnalysis: %v", err)
	}

	res, err := srv.ListItems(ctx, &pb.ListItemsRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(res.GetItems()) != 2 {
		t.Fatalf("got %d wire items, want 2", len(res.GetItems()))
	}
	byID := map[string]*pb.Item{}
	for _, it := range res.GetItems() {
		byID[it.GetId()] = it
	}
	if got := byID[items[0].ID].GetCategory(); got != "security" {
		t.Errorf("cleared item Category = %q, want %q", got, "security")
	}
	if got := byID[items[1].ID].GetCategory(); got != "" {
		t.Errorf("unsorted item Category = %q, want empty", got)
	}
}

func TestGetItemPopulatesCategory(t *testing.T) {
	srv, repo, sc := newCategoryServer(t)
	ctx := context.Background()

	items, _, err := srv.svc.ListItems(ctx, sc, store.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("seed ListItems: %v", err)
	}
	target := items[0]
	if err := repo.UpsertAnalysis(ctx, []store.ItemAnalysis{
		{ItemID: target.ID, AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{"security": 8.0}, AnalyzedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatalf("UpsertAnalysis: %v", err)
	}

	res, err := srv.GetItem(ctx, &pb.GetItemRequest{Id: target.ID})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got := res.GetItem().GetCategory(); got != "security" {
		t.Errorf("Category = %q, want %q", got, "security")
	}
}

func TestGetItemModelPrimarySetsByModel(t *testing.T) {
	srv, repo, sc := newCategoryServer(t)
	ctx := context.Background()

	items, _, err := srv.svc.ListItems(ctx, sc, store.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("seed ListItems: %v", err)
	}
	target := items[0]
	if err := repo.UpsertAnalysis(ctx, []store.ItemAnalysis{
		{ItemID: target.ID, AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{"security": 8.0},
			ModelPrimary:   "business", AnalyzedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatalf("UpsertAnalysis: %v", err)
	}

	res, err := srv.GetItem(ctx, &pb.GetItemRequest{Id: target.ID})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got := res.GetItem().GetCategory(); got != "business" {
		t.Errorf("Category = %q, want the model's %q", got, "business")
	}
	if !res.GetItem().GetCategoryByModel() {
		t.Error("CategoryByModel = false for a model verdict")
	}
}
