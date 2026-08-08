package grpcsrv

import (
	"context"
	"testing"
	"time"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// CountUnreadByCategory is the rail's counts, and it was the one reader RPC with
// no test at any layer above the repository. The store's own tests are thorough
// about the numbers; what they cannot see is everything this method adds — the
// int→int32 narrowing, the uncategorised count that rides along in the same
// response, and the scope the counts are taken under.
//
// The last of those is the reason this is worth a test rather than a line in
// the authz map. This is the authorization boundary, and a count is a disclosure
// like any other: "how many unread security articles does this instance have"
// is a question about somebody's reading, and answering it across a tenant edge
// leaks it just as surely as returning the articles would.

// The counts arrive per label, with the labels that scored nothing present and
// zero — the rail renders every label it asked for, and a missing key is
// indistinguishable from a zero by the time it reaches the client's map.
func TestCountUnreadByCategoryAnswersEveryLabelItWasAsked(t *testing.T) {
	srv, repo, sc := newCategoryServer(t)
	ctx := context.Background()

	items, _, err := srv.svc.ListItems(ctx, sc, store.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("fixture gave %d items, want 2", len(items))
	}
	if err := repo.UpsertAnalysis(ctx, []store.ItemAnalysis{
		{ItemID: items[0].ID, AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{"security": 9}, AnalyzedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatalf("UpsertAnalysis: %v", err)
	}

	res, err := srv.CountUnreadByCategory(ctx, &pb.CountUnreadByCategoryRequest{
		Slugs: []string{"security", "gardening"},
	})
	if err != nil {
		t.Fatalf("CountUnreadByCategory: %v", err)
	}
	if got := res.GetCounts()["security"]; got != 1 {
		t.Errorf("security = %d, want 1", got)
	}
	n, ok := res.GetCounts()["gardening"]
	if !ok || n != 0 {
		t.Errorf("gardening = (%d, present=%v), want (0, true) — the rail renders "+
			"every label it asked for", n, ok)
	}

	// The uncategorised count rides in the SAME response. It is fetched here
	// rather than behind a second RPC because the rail renders it as one more
	// row in the same group, and a number that arrives on its own round trip
	// arrives after the group it belongs to has already been laid out.
	if res.GetUncategorised() != 1 {
		t.Errorf("uncategorised = %d, want 1 (the second fixture item carries no label)",
			res.GetUncategorised())
	}
}

// Reading an article takes it out of its label's count and out of the
// uncategorised count alike — the number is of UNREAD articles, and this is the
// path the rail actually refreshes on.
func TestCountUnreadByCategoryFollowsTheReadState(t *testing.T) {
	srv, repo, sc := newCategoryServer(t)
	ctx := context.Background()

	items, _, err := srv.svc.ListItems(ctx, sc, store.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if err := repo.UpsertAnalysis(ctx, []store.ItemAnalysis{
		{ItemID: items[0].ID, AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{"security": 9}, AnalyzedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatalf("UpsertAnalysis: %v", err)
	}

	read := true
	for _, it := range items {
		if _, err := repo.SetItemState(ctx, sc, it.ID, store.StateChange{Read: &read}); err != nil {
			t.Fatalf("SetItemState: %v", err)
		}
	}

	res, err := srv.CountUnreadByCategory(ctx, &pb.CountUnreadByCategoryRequest{
		Slugs: []string{"security"},
	})
	if err != nil {
		t.Fatalf("CountUnreadByCategory: %v", err)
	}
	if got := res.GetCounts()["security"]; got != 0 {
		t.Errorf("security = %d after reading everything, want 0", got)
	}
	if res.GetUncategorised() != 0 {
		t.Errorf("uncategorised = %d after reading everything, want 0", res.GetUncategorised())
	}
}

// No slugs is not an error and not "all of them": the client that sends an empty
// list is asking for no labels, and inventing the whole taxonomy would turn a
// cheap call into twenty-six counts. The uncategorised number still comes back,
// because it is not one of the labels.
func TestCountUnreadByCategoryWithNoSlugsStillReportsTheUncategorised(t *testing.T) {
	srv, _, _ := newCategoryServer(t)

	res, err := srv.CountUnreadByCategory(context.Background(), &pb.CountUnreadByCategoryRequest{})
	if err != nil {
		t.Fatalf("CountUnreadByCategory: %v", err)
	}
	if len(res.GetCounts()) != 0 {
		t.Errorf("got %d counts for an empty slug list: %v", len(res.GetCounts()), res.GetCounts())
	}
	if res.GetUncategorised() != 2 {
		t.Errorf("uncategorised = %d, want 2 — nothing in the fixture is classified yet",
			res.GetUncategorised())
	}
}

// The disclosure test. A count taken under the wrong scope is a leak of somebody
// else's reading, and it is a leak that would never look like one: the rail
// would simply show a larger number.
func TestCountUnreadByCategoryDoesNotCountAnotherTenantsArticles(t *testing.T) {
	srv, repo, _ := newCategoryServer(t)
	ctx := context.Background()

	// A second tenant, on a feed of its own so the two are not sharing a source.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t2", Name: "Other", UserID: "u2", Username: "other",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatalf("create second tenant: %v", err)
	}
	other := store.Scope{TenantID: "t2", UserID: "u2", Role: "superadmin"}
	f2, _, err := repo.Subscribe(ctx, other, store.NewSubscription{
		NaturalKey: "feed:b", FeedURL: "https://b.example/feed", Title: "Beta Journal",
	})
	if err != nil {
		t.Fatalf("subscribe as the second tenant: %v", err)
	}
	ing, err := repo.IngestItems(ctx, f2.SourceID, []store.IngestItem{
		{GUID: "b1", Title: "Their security article", PublishedAt: time.Now()},
		{GUID: "b2", Title: "Their unclassified article", PublishedAt: time.Now()},
	})
	if err != nil {
		t.Fatalf("ingest for the second tenant: %v", err)
	}
	if err := repo.UpsertAnalysis(ctx, []store.ItemAnalysis{
		{ItemID: ing.NewIDs[0], AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{"security": 9}, AnalyzedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatalf("UpsertAnalysis: %v", err)
	}

	// The server's scope function answers with tenant one, always — this is the
	// first tenant asking, with the other's rows sitting in the same database.
	res, err := srv.CountUnreadByCategory(ctx, &pb.CountUnreadByCategoryRequest{
		Slugs: []string{"security"},
	})
	if err != nil {
		t.Fatalf("CountUnreadByCategory: %v", err)
	}
	if got := res.GetCounts()["security"]; got != 0 {
		t.Errorf("security = %d for a tenant with nothing classified — the other "+
			"tenant's article was counted", got)
	}
	if res.GetUncategorised() != 2 {
		t.Errorf("uncategorised = %d, want the 2 rows this tenant owns; the other "+
			"tenant contributed 2 more", res.GetUncategorised())
	}
}

// A scope that cannot be resolved is refused before any counting happens. This
// is the branch every RPC in this file opens with, and on this one it is the
// difference between "not signed in" and instance-wide counts.
func TestCountUnreadByCategoryRefusesAnUnresolvableScope(t *testing.T) {
	srv, _, _ := newCategoryServer(t)
	broken := NewReaderServer(srv.svc, func(context.Context) (store.Scope, error) {
		return store.Scope{}, store.ErrNoScope
	})

	if _, err := broken.CountUnreadByCategory(context.Background(),
		&pb.CountUnreadByCategoryRequest{Slugs: []string{"security"}}); err == nil {
		t.Fatal("counts were returned with no scope to take them under")
	}
}

// The service methods the RPC delegates to had no direct caller in a test
// either. Thin as they are, an argument dropped on the way through — the slug
// list, or the scope — is invisible from either side alone.
func TestServiceUnreadCountsPassTheirArgumentsThrough(t *testing.T) {
	srv, repo, sc := newCategoryServer(t)
	ctx := context.Background()
	svc := reader.New(repo, nil)

	items, _, err := srv.svc.ListItems(ctx, sc, store.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if err := repo.UpsertAnalysis(ctx, []store.ItemAnalysis{
		{ItemID: items[0].ID, AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{"security": 9}, AnalyzedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatalf("UpsertAnalysis: %v", err)
	}

	counts, err := svc.UnreadByCategory(ctx, sc, []string{"security"})
	if err != nil {
		t.Fatalf("UnreadByCategory: %v", err)
	}
	if counts["security"] != 1 {
		t.Errorf("security = %d, want 1", counts["security"])
	}
	none, err := svc.UnreadUncategorised(ctx, sc)
	if err != nil {
		t.Fatalf("UnreadUncategorised: %v", err)
	}
	if none != 1 {
		t.Errorf("uncategorised = %d, want 1", none)
	}
}
