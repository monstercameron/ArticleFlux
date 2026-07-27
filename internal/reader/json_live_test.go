package reader

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/jsonsel"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The live proof for §11.2b, against the site that motivated it.
//
// Skipped unless AF_LIVE=1: it fetches somebody's real API, and nothing in CI
// should depend on a stranger's server being up. Run it by hand when the JSON
// path is touched —
//
//	AF_LIVE=1 go test ./internal/reader/ -run Live -v
//
// What it proves is the whole chain minus the model: discovery finds the
// endpoint, the rule extracts entries, the subscription is written, items land
// in the database with real titles and dates, and a second poll adds nothing
// because every entry is already known.
func TestLiveJSONSubscribeAndPoll(t *testing.T) {
	if os.Getenv("AF_LIVE") != "1" {
		t.Skip("set AF_LIVE=1 to run this against the live site")
	}
	svc, repo, sc := testService(t)
	ctx := context.Background()
	const page = "https://hni-scantrad.net/comics/hajime-no-ippo"

	// The rule a model would propose, written by hand so the test does not need
	// an API key. Every path here came out of the shape the analyser sends.
	rule := jsonsel.Rule{
		DataURL:   "https://hni-scantrad.net/api/comics/hajime-no-ippo",
		ItemsPath: "comic.chapters",
		TitlePath: "full_title",
		LinkPath:  "url",
		DatePath:  "published_on",
		IDPath:    "slug_lang_vol_ch_sub",
	}

	f, n, err := svc.SubscribeJSON(ctx, sc, page, "Hajime no Ippo", "", rule)
	if err != nil {
		t.Fatalf("SubscribeJSON: %v", err)
	}
	if n < 100 {
		t.Fatalf("ingested %d chapters, expected the archive", n)
	}
	t.Logf("subscribed: %s — %d chapters", f.Title, n)

	items, _, err := repo.ListItems(ctx, sc, store.ListQuery{Limit: 5})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no items in the database")
	}
	newest := items[0]
	t.Logf("newest: %q  %s  %s", newest.Title, newest.PublishedAt, newest.URL)
	if !strings.HasPrefix(newest.URL, "https://hni-scantrad.net/read/") {
		t.Errorf("link was not resolved to the reader page: %q", newest.URL)
	}
	if newest.PublishedAt == "" {
		t.Error("no published date survived")
	}

	// The stored rule is what the poller will read an hour from now.
	stored, err := repo.ScrapeRuleFor(ctx, f.SourceID)
	if err != nil {
		t.Fatalf("ScrapeRuleFor: %v", err)
	}
	if stored.Kind != "json" || stored.DataURL != rule.DataURL {
		t.Fatalf("stored rule = %+v", stored)
	}

	// Polling again finds the same archive and adds nothing: "title != exist"
	// is decided on the guid, so a re-poll of an unchanged site is free.
	res, err := svc.Refresh(ctx, sc, nil)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res.NewItems != 0 {
		t.Errorf("a second poll of an unchanged site produced %d new items", res.NewItems)
	}
	if len(res.Errors) > 0 {
		t.Errorf("poll errors: %v", res.Errors)
	}
}
