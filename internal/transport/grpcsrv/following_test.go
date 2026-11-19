package grpcsrv

import (
	"context"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// "Do I already follow this?" is a question about the reader's own subscription,
// not about the server's sources. A feed another tenant reads exists on the
// server, and answering yes for it would tell the add-a-feed dialog that a feed
// this reader has never seen is one they already follow — and, worse, would be a
// way to ask whether anybody else does.
func TestFollowingAnswersFromTheReadersOwnSubscriptions(t *testing.T) {
	srv, a, b, _ := crossTenantFixture(t)
	ctx := context.Background()
	const mine, other = "https://a.example/feed", "https://a.example/not-followed"

	got, err := srv.svc.Following(ctx, a, mine, other)
	if err != nil {
		t.Fatalf("Following: %v", err)
	}
	// The fixture's natural key is "feed:a-only", not the URL's, so seed the one the
	// URL maps to before asking about it.
	if got[mine] || got[other] {
		t.Fatalf("before following: %v, want neither", got)
	}

	if _, _, err := srv.svc.SubscribeOnly(ctx, a, mine, "A again", "", ""); err != nil {
		t.Fatalf("SubscribeOnly: %v", err)
	}
	got, _ = srv.svc.Following(ctx, a, mine, other, mine+"/")
	if !got[mine] || got[other] {
		t.Errorf("after following: %v, want only %s", got, mine)
	}
	// The same feed under a spelling the natural key treats as equal.
	if feed.NaturalKey(mine+"/") == feed.NaturalKey(mine) && !got[mine+"/"] {
		t.Errorf("the trailing-slash spelling of a followed feed was not recognised: %v", got)
	}

	// Another reader on the same server: the source exists, the answer is no.
	gotB, err := srv.svc.Following(ctx, b, mine)
	if err != nil {
		t.Fatalf("Following as B: %v", err)
	}
	if gotB[mine] {
		t.Error("a feed only another tenant follows was reported as followed")
	}
	if _, err := srv.svc.Following(ctx, store.Scope{}, mine); err == nil {
		t.Error("Following without a scope succeeded")
	}
}
