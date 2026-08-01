package grpcsrv

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/recommendjob"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// A server with no repo wired (WithRecommendations never called) must not
// panic — every other RPC on this server degrades the same way when its
// optional dependency is absent (categorizer, mintSpeech, ...).
func TestListRecommendationsWithNoRepoWiredReturnsEmpty(t *testing.T) {
	srv, _, _ := subscribeFixture(t)

	res, err := srv.ListRecommendations(context.Background(), &pb.ListRecommendationsRequest{})
	if err != nil {
		t.Fatalf("ListRecommendations: %v", err)
	}
	if len(res.GetRecommendations()) != 0 {
		t.Errorf("recommendations = %v, want none with no repo wired", res.GetRecommendations())
	}
}

func TestRefreshRecommendationsWithNoRecommenderWiredIsUnavailable(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	_, err := srv.RefreshRecommendations(context.Background(), &pb.RefreshRecommendationsRequest{})
	if status.Code(err) != codes.Unavailable {
		t.Errorf("code = %v, want Unavailable when no recommender is wired", status.Code(err))
	}
}

// The end-to-end loop: a stored recommendation is listed, accepting it
// subscribes to its validated feed URL and removes it from the open list, and
// a second accept (now gone) is NotFound rather than a duplicate subscribe.
func TestAcceptRecommendationSubscribesAndClearsIt(t *testing.T) {
	srv, sc, repo, base := subscribeNetFixture(t)
	srv.WithRecommendations(repo, nil)
	ctx := context.Background()
	feedURL := base + "/source-feed"

	if err := repo.ReplaceRecommendations(ctx, sc, []store.StoredRecommendation{
		{Domain: "example.com", FeedURL: feedURL, Title: "Example", Score: 3, Rung: 1,
			Evidence: `["3 writers you read linked here"]`},
	}); err != nil {
		t.Fatalf("seed recommendation: %v", err)
	}

	listed, err := srv.ListRecommendations(ctx, &pb.ListRecommendationsRequest{})
	if err != nil {
		t.Fatalf("ListRecommendations: %v", err)
	}
	if len(listed.GetRecommendations()) != 1 {
		t.Fatalf("listed %d recommendations, want 1", len(listed.GetRecommendations()))
	}
	if got := listed.GetRecommendations()[0].GetEvidence(); got != "3 writers you read linked here" {
		t.Errorf("evidence = %q, want the unwrapped sentence", got)
	}

	res, err := srv.AcceptRecommendation(ctx, &pb.AcceptRecommendationRequest{Domain: "example.com"})
	if err != nil {
		t.Fatalf("AcceptRecommendation: %v", err)
	}
	if res.GetFeed().GetFeedUrl() != feedURL {
		t.Errorf("subscribed feed url = %q, want %q", res.GetFeed().GetFeedUrl(), feedURL)
	}

	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil || len(feeds) != 1 {
		t.Fatalf("ListFeeds = %v, %v, want exactly 1 subscribed feed", feeds, err)
	}

	afterAccept, err := srv.ListRecommendations(ctx, &pb.ListRecommendationsRequest{})
	if err != nil {
		t.Fatalf("ListRecommendations after accept: %v", err)
	}
	if len(afterAccept.GetRecommendations()) != 0 {
		t.Errorf("recommendations after accept = %v, want the accepted one gone from the open list",
			afterAccept.GetRecommendations())
	}

	if _, err := srv.AcceptRecommendation(ctx, &pb.AcceptRecommendationRequest{Domain: "example.com"}); status.Code(err) != codes.NotFound {
		t.Errorf("second accept code = %v, want NotFound — the recommendation is no longer open", status.Code(err))
	}
}

func TestAcceptRecommendationRequiresADomain(t *testing.T) {
	srv, _, repo := subscribeFixture(t)
	srv.WithRecommendations(repo, nil)
	_, err := srv.AcceptRecommendation(context.Background(), &pb.AcceptRecommendationRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument for an empty domain", status.Code(err))
	}
}

// Rejection is permanent (§18.7): the domain must not be listed again even
// though nothing new was ever scored for it, and recommendjob's own
// DismissedDomains read (tested in recommendjob_test.go) is what makes that
// stick across a rescore — this test only proves the RPC records it at all.
func TestRejectRecommendationIsPermanent(t *testing.T) {
	srv, sc, repo := subscribeFixture(t)
	srv.WithRecommendations(repo, nil)
	ctx := context.Background()

	if err := repo.ReplaceRecommendations(ctx, sc, []store.StoredRecommendation{
		{Domain: "spam.example", FeedURL: "https://spam.example/feed", Title: "Spam",
			Score: 1, Rung: 1, Evidence: `["linked once"]`},
	}); err != nil {
		t.Fatalf("seed recommendation: %v", err)
	}

	if _, err := srv.RejectRecommendation(ctx, &pb.RejectRecommendationRequest{Domain: "spam.example"}); err != nil {
		t.Fatalf("RejectRecommendation: %v", err)
	}

	listed, err := srv.ListRecommendations(ctx, &pb.ListRecommendationsRequest{})
	if err != nil {
		t.Fatalf("ListRecommendations: %v", err)
	}
	if len(listed.GetRecommendations()) != 0 {
		t.Errorf("recommendations = %v, want the rejected domain gone", listed.GetRecommendations())
	}

	dismissed, err := repo.DismissedDomains(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if !dismissed["spam.example"] {
		t.Error("spam.example is not in DismissedDomains after rejection — a rescore would re-suggest it")
	}
}

// RefreshRecommendations, wired to a real recommendjob.Service, must actually
// reach the job queue — not just return success.
func TestRefreshRecommendationsEnqueuesAJob(t *testing.T) {
	srv, sc, repo := subscribeFixture(t)
	rec := recommendjob.New(repo, nil, nil, nil, nil)
	srv.WithRecommendations(repo, rec)

	if _, err := srv.RefreshRecommendations(context.Background(), &pb.RefreshRecommendationsRequest{}); err != nil {
		t.Fatalf("RefreshRecommendations: %v", err)
	}

	depth, err := repo.QueueDepth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if depth[store.JobRecommend].Queued != 1 {
		t.Errorf("JobRecommend queue depth = %+v, want 1 queued after RefreshRecommendations", depth[store.JobRecommend])
	}
	_ = sc
}
