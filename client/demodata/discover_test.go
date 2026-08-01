package demodata

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Discover (§18.7), through the same stubs as everything else here.
//
// The assertions are about the SHAPE of the argument the screen makes — every
// card explains itself, the gate refuses more than it accepts, a dismissal is
// permanent — rather than about which fixture came out on top. Pinning the order
// would be pinning internal/recommend's weights from a package that does not own
// them, and the scorer is allowed to be retuned without this failing.

func recs(t *testing.T, r pb.ReaderServiceClient) []*pb.Recommendation {
	t.Helper()
	res, err := r.ListRecommendations(context.Background(), &pb.ListRecommendationsRequest{})
	if err != nil {
		t.Fatalf("ListRecommendations: %v", err)
	}
	return res.GetRecommendations()
}

func TestDiscoverOpensWithSuggestions(t *testing.T) {
	_, r := newTest(t)
	list := recs(t, r)
	if len(list) == 0 {
		t.Fatal("Discover opens empty, which is the one thing the screen must not do on arrival")
	}
	for _, rec := range list {
		if rec.GetDomain() == "" || rec.GetFeedUrl() == "" {
			t.Errorf("%q has no address, so its subscribe button is a dead end", rec.GetTitle())
		}
		// The whole argument of the feature: an unexplained suggestion is worth
		// less than none.
		if rec.GetEvidence() == "" {
			t.Errorf("%s carries no evidence", rec.GetDomain())
		}
		if rec.GetScore() <= 0 {
			t.Errorf("%s scored %v", rec.GetDomain(), rec.GetScore())
		}
		if !strings.HasSuffix(rec.GetDomain(), ".example") {
			t.Errorf("%s is not under a reserved domain", rec.GetDomain())
		}
	}
}

// The evidence has to be checkable against the rail, or it is decoration: a
// reader who is told three writers they read linked somewhere can go and look.
func TestDiscoverEvidenceNamesTheFixturesOwnSources(t *testing.T) {
	_, r := newTest(t)
	var named int
	for _, rec := range recs(t, r) {
		for _, sf := range seedFeeds {
			if strings.Contains(rec.GetEvidence(), sf.title) {
				named++
			}
		}
	}
	if named == 0 {
		t.Error("no card's evidence names a feed in the rail, so none of it can be checked")
	}
}

// The health gate is the reason to trust the list, and a fixture where
// everything is accepted argues against the feature it demonstrates.
func TestDiscoverGateRefusesSomeCandidates(t *testing.T) {
	c := New(func() time.Time { return epoch })
	res := c.inst.score()
	if len(res.Rejected) == 0 {
		t.Fatal("nothing was refused; the fixtures mean to include a silent site, a firehose and one with no feed")
	}
	shown := map[string]bool{}
	for _, rec := range res.Recommendations {
		shown[rec.Domain] = true
	}
	for _, rej := range res.Rejected {
		if shown[rej.Domain] {
			t.Errorf("%s was both refused (%s) and shown", rej.Domain, rej.Reason)
		}
		if rej.Reason == "" {
			t.Errorf("%s was refused without a reason", rej.Domain)
		}
	}
}

func TestRefreshFindsMoreOnceAndThenHonestlyFindsNothing(t *testing.T) {
	_, r := newTest(t)
	first := len(recs(t, r))

	if _, err := r.RefreshRecommendations(context.Background(), &pb.RefreshRecommendationsRequest{}); err != nil {
		t.Fatalf("RefreshRecommendations: %v", err)
	}
	second := len(recs(t, r))
	if second <= first {
		t.Fatalf("Refresh found nothing: %d then %d", first, second)
	}

	if _, err := r.RefreshRecommendations(context.Background(), &pb.RefreshRecommendationsRequest{}); err != nil {
		t.Fatalf("second RefreshRecommendations: %v", err)
	}
	if third := len(recs(t, r)); third != second {
		t.Errorf("the second Refresh changed the list (%d → %d); it has nothing left to find", second, third)
	}
}

func TestAcceptSubscribesAndTheCardLeavesTheList(t *testing.T) {
	_, r := newTest(t)
	before := recs(t, r)
	target := before[0].GetDomain()

	res, err := r.AcceptRecommendation(context.Background(),
		&pb.AcceptRecommendationRequest{Domain: target})
	if err != nil {
		t.Fatalf("AcceptRecommendation: %v", err)
	}
	if res.GetFeed().GetId() == "" || !strings.Contains(res.GetFeed().GetFeedUrl(), target) {
		t.Fatalf("accepting %s returned %+v", target, res.GetFeed())
	}

	// It is in the rail...
	feeds, _ := r.ListFeeds(context.Background(), &pb.ListFeedsRequest{})
	var found bool
	for _, f := range feeds.GetFeeds() {
		if strings.Contains(f.GetFeedUrl(), target) {
			found = true
		}
	}
	if !found {
		t.Errorf("%s was accepted and is not in the feed list", target)
	}
	// ...and gone from the list, because the subscription is what removes it.
	for _, rec := range recs(t, r) {
		if rec.GetDomain() == target {
			t.Errorf("%s is still being suggested after being accepted", target)
		}
	}
}

func TestRejectIsPermanentEvenAcrossARefresh(t *testing.T) {
	_, r := newTest(t)
	target := recs(t, r)[0].GetDomain()

	if _, err := r.RejectRecommendation(context.Background(),
		&pb.RejectRecommendationRequest{Domain: target}); err != nil {
		t.Fatalf("RejectRecommendation: %v", err)
	}
	if _, err := r.RefreshRecommendations(context.Background(),
		&pb.RefreshRecommendationsRequest{}); err != nil {
		t.Fatalf("RefreshRecommendations: %v", err)
	}
	for _, rec := range recs(t, r) {
		if rec.GetDomain() == target {
			t.Fatal("a dismissed domain came back after a refresh; §18.7 says never again")
		}
	}
}

// A domain the screen is not offering cannot be accepted through the API,
// which is what stops a replayed click from subscribing to something the gate
// refused.
func TestAcceptRefusesADomainThatIsNotOnTheList(t *testing.T) {
	_, r := newTest(t)
	for _, domain := range []string{"", "wirefeed.example", "nothing-here.example"} {
		_, err := r.AcceptRecommendation(context.Background(),
			&pb.AcceptRecommendationRequest{Domain: domain})
		if err == nil {
			t.Errorf("accepting %q succeeded", domain)
			continue
		}
		if c := status.Code(err); c != codes.NotFound && c != codes.InvalidArgument {
			t.Errorf("accepting %q answered %v", domain, c)
		}
	}
}

func TestRejectNeedsADomain(t *testing.T) {
	_, r := newTest(t)
	_, err := r.RejectRecommendation(context.Background(), &pb.RejectRecommendationRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("rejecting nothing answered %v, want InvalidArgument", status.Code(err))
	}
}

// Nothing the reader already follows is ever suggested, which is the rejection
// that would be most obviously wrong on screen.
func TestSubscribedDomainsAreNeverSuggested(t *testing.T) {
	_, r := newTest(t)
	feeds, _ := r.ListFeeds(context.Background(), &pb.ListFeedsRequest{})
	for _, rec := range recs(t, r) {
		for _, f := range feeds.GetFeeds() {
			if strings.Contains(f.GetFeedUrl(), rec.GetDomain()) {
				t.Errorf("%s is suggested and already subscribed", rec.GetDomain())
			}
		}
	}
}
