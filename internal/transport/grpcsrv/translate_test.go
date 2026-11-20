package grpcsrv

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/internal/llm"
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

// TestAProviderFailureIsNeverUnavailable pins G2 of the second UX audit. The
// wasm client reads every codes.Unavailable as "the transport dropped" and
// repaints the list header and the search dot, so a bad key on a Smart+ call
// made the whole app say the connection was gone while the server answered. A
// provider's failure, and the breaker being open, are application answers;
// each keeps its catalog key (the message is unchanged) and carries a code the
// tracker leaves alone.
func TestAProviderFailureIsNeverUnavailable(t *testing.T) {
	s := bareSmartServer()
	cases := []struct {
		name string
		err  error
		key  string
	}{
		{"article, provider failed", s.articleErr("fr", errors.New("provider returned 401")), "srv.articleTranslateFailed"},
		{"article, breaker open", s.articleErr("fr", llm.ErrCircuitOpen), "srv.smartUnavailable"},
		{"interface, provider failed", s.localeErr("fr", errors.New("provider returned 401")), "srv.translateFailed"},
		{"interface, breaker open", s.localeErr("fr", llm.ErrCircuitOpen), "srv.smartUnavailable"},
		{"theme, provider failed", s.themeErr(errors.New("provider returned 401"), "t"), "srv.themeFailed"},
		{"theme, not a palette", s.themeErr(design.ErrNotAPalette, "t"), "srv.themeNotAPalette"},
		{"theme, breaker open", s.themeErr(llm.ErrCircuitOpen, "t"), "srv.smartUnavailable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := status.Code(c.err); got == codes.Unavailable {
				t.Fatalf("code = Unavailable; the client would call this a dropped connection")
			}
			if got := detailKey(t, c.err); got != c.key {
				t.Errorf("key = %q, want %q", got, c.key)
			}
			// What the connection tracker will make of it, end to end.
			if v := data.Classify(c.err); v.Class != data.ClassApplication {
				t.Errorf("Classify = %v, want ClassApplication", v.Class)
			}
		})
	}
}

// G18 of the second UX audit: with a key the provider refuses, the interface
// language picker said "0 of 1,519 translated. Translating the rest…" and then
// just stopped at "0 of 1,519 translated." — the background fill's failure went
// to the log and nowhere the reader could see. The read after a failed fill now
// says "failed", and a request that asks for a fill starts another attempt
// instead of being told the old news.
func TestAFillThatFailedIsReportedOnTheNextRead(t *testing.T) {
	sc := ownerScope("admin")
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"message": "Incorrect API key provided"},
		}), nil
	})
	s := listModelsServer(t, sc, rt)
	ctx := context.Background()

	res, err := s.GetUIStrings(ctx, &pb.GetUIStringsRequest{Locale: "fr", Fill: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.GetFilling() {
		t.Fatalf("the fill did not start: reason=%q pending=%d", res.GetIdleReason(), res.GetPending())
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		res, err = s.GetUIStrings(ctx, &pb.GetUIStringsRequest{Locale: "fr"})
		if err != nil {
			t.Fatal(err)
		}
		if !res.GetFilling() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fill never stopped")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if res.GetIdleReason() != "failed" || res.GetPending() == 0 {
		t.Errorf("after a failed fill: reason=%q pending=%d, want failed and some pending", res.GetIdleReason(), res.GetPending())
	}
}
