package grpcsrv

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The transport half of on-the-fly translation (§22.16c, §10.5b): who may read,
// who may spend, and what each refusal says. The translation behaviour itself is
// internal/smart's tests.

func TestGetUIStringsIsReadableByAMemberButOnlyAnOwnerMayFill(t *testing.T) {
	member := ownerScope("member")
	s, _ := smartConfigServer(t, nil, member, fixedScope(member))
	res, err := s.GetUIStrings(context.Background(), &pb.GetUIStringsRequest{
		Locale: "fr", Keys: []string{"login.submit"}, Fill: true,
	})
	if err != nil {
		t.Fatalf("a member could not read the cache: %v", err)
	}
	if res.GetPending() != 1 || res.GetFilling() || res.GetIdleReason() != "not_owner" {
		t.Errorf("member fill: pending=%d filling=%v reason=%q", res.GetPending(), res.GetFilling(), res.GetIdleReason())
	}

	owner := ownerScope("admin")
	s, _ = smartConfigServer(t, nil, owner, fixedScope(owner))
	res, err = s.GetUIStrings(context.Background(), &pb.GetUIStringsRequest{Locale: "fr", Fill: true})
	if err != nil {
		t.Fatal(err)
	}
	// No key on this server: nothing may be sent, and the client is told why.
	if res.GetFilling() || res.GetIdleReason() != "no_key" {
		t.Errorf("owner without a key: filling=%v reason=%q", res.GetFilling(), res.GetIdleReason())
	}
}

func TestGetUIStringsRefusesAnUnofferedLocale(t *testing.T) {
	member := ownerScope("member")
	s, _ := smartConfigServer(t, nil, member, fixedScope(member))
	_, err := s.GetUIStrings(context.Background(), &pb.GetUIStringsRequest{Locale: "xx"})
	if status.Code(err) != codes.InvalidArgument || detailKey(t, err) != "srv.unsupportedLanguage" {
		t.Errorf("err = %v (%s)", err, detailKey(t, err))
	}
	if _, err := s.GetUIStrings(context.Background(), &pb.GetUIStringsRequest{Locale: "fr"}); err != nil {
		t.Errorf("an offered locale with an empty cache: %v", err)
	}
}

func TestGetUIStringsNeedsASignedInReader(t *testing.T) {
	s, _ := smartConfigServer(t, nil, store.Scope{}, nil)
	if _, err := s.GetUIStrings(context.Background(), &pb.GetUIStringsRequest{Locale: "fr"}); status.Code(err) != codes.Unauthenticated {
		t.Errorf("err = %v, want Unauthenticated", err)
	}
}

func TestTranslateArticleConsentRoundTripsAndDefaultsOff(t *testing.T) {
	owner := ownerScope("admin")
	s, _ := smartConfigServer(t, nil, owner, fixedScope(owner))
	ctx := context.Background()
	cfg, err := s.GetSmartConfig(ctx, &pb.GetSmartConfigRequest{})
	if err != nil || cfg.GetTranslateArticlesEnabled() {
		t.Fatalf("fresh instance: enabled=%v err=%v, want off", cfg.GetTranslateArticlesEnabled(), err)
	}
	on := true
	res, err := s.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{TranslateArticlesEnabled: &on})
	if err != nil || !res.GetConfig().GetTranslateArticlesEnabled() {
		t.Fatalf("enable: %v %v", res, err)
	}
	// An unrelated save leaves it alone.
	res, err = s.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{Model: "some-model"})
	if err != nil || !res.GetConfig().GetTranslateArticlesEnabled() {
		t.Fatalf("an unrelated save changed the consent: %v %v", res, err)
	}
}

// articleServer is smartConfigServer plus a reader repo and one item the given
// scope subscribes to.
func articleServer(t *testing.T, sc store.Scope) (*SmartServer, string) {
	t.Helper()
	s, db := smartConfigServer(t, nil, sc, fixedScope(sc))
	repo := store.NewReaderRepo(db)
	s.WithTheming(nil, repo)
	ctx := context.Background()
	sub, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:pub.example/rss", FeedURL: "https://pub.example/rss",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.IngestItems(ctx, sub.SourceID, []store.IngestItem{{
		GUID: "g", URL: "https://pub.example/a", Title: "Hallo", ContentHTML: "<p>Hallo Welt</p>",
		PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	items, _, err := repo.ListItems(ctx, sc, store.ListQuery{Limit: 1})
	if err != nil || len(items) == 0 {
		t.Fatal(err)
	}
	return s, items[0].ID
}

func TestTranslateArticleRefusals(t *testing.T) {
	member := ownerScope("member")
	s, item := articleServer(t, member)
	ctx := context.Background()

	// No key on this server: refused with the key's own message, nothing sent.
	_, err := s.TranslateArticle(ctx, &pb.TranslateArticleRequest{ItemId: item, Locale: "fr"})
	if detailKey(t, err) != "srv.smartNoKey" {
		t.Errorf("no key: %v (%s)", err, detailKey(t, err))
	}
	// An item the reader cannot see is NotFound — never a permission error.
	_, err = s.TranslateArticle(ctx, &pb.TranslateArticleRequest{ItemId: "not-theirs", Locale: "fr"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("an invisible item: %v", err)
	}
	_, err = s.TranslateArticle(ctx, &pb.TranslateArticleRequest{ItemId: item, Locale: "xx"})
	if detailKey(t, err) != "srv.unsupportedLanguage" {
		t.Errorf("unsupported: %v (%s)", err, detailKey(t, err))
	}
}

func TestTranslateArticleWithNoReaderRepoIsOff(t *testing.T) {
	member := ownerScope("member")
	s, _ := smartConfigServer(t, nil, member, fixedScope(member))
	_, err := s.TranslateArticle(context.Background(), &pb.TranslateArticleRequest{ItemId: "x", Locale: "fr"})
	if detailKey(t, err) != "srv.articleTranslateOff" {
		t.Errorf("err = %v (%s)", err, detailKey(t, err))
	}
}
