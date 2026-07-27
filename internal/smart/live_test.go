package smart

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The one leg of this feature that unit tests cannot reach: the model call.
//
// Skipped unless AF_LIVE=1 and a key is present — it spends money and depends on
// somebody else's site being up, neither of which belongs in CI. Run it by hand
// when the prompt changes:
//
//	AF_LIVE=1 OPENAI_API_KEY=$(...) go test ./internal/smart/ -run Live -v
//
// What it proves is the thing that cannot be argued about: given the real shape
// of a real page's data, does the prompt produce paths that the extractor
// accepts and that yield the entries a reader wants?
func TestLiveProposeJSON(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if os.Getenv("AF_LIVE") != "1" || key == "" {
		t.Skip("set AF_LIVE=1 and OPENAI_API_KEY to run this against the model")
	}

	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "live.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		page = "https://hni-scantrad.net/comics/hajime-no-ippo"
		api  = "https://hni-scantrad.net/api/comics/hajime-no-ippo"
	)
	res, err := http.Get(api)
	if err != nil {
		t.Skipf("the site is not reachable: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()

	a := NewSiteAnalyzer(
		llm.New(func(context.Context) string { return key }),
		store.NewSettingsRepo(db, nil),
	)
	prop, err := a.ProposeJSON(context.Background(), page, api, "comic.chapters", body)
	if err != nil {
		t.Fatalf("ProposeJSON: %v", err)
	}

	t.Logf("items  %s", prop.Rule.ItemsPath)
	t.Logf("title  %s", prop.Rule.TitlePath)
	t.Logf("link   %s%s", prop.Rule.LinkPath, prop.Rule.LinkTemplate)
	t.Logf("date   %s", prop.Rule.DatePath)
	t.Logf("id     %s", prop.Rule.IDPath)
	t.Logf("notes  %s", prop.Notes)
	t.Logf("found  %d entries; first: %q → %s",
		prop.Found, prop.Items[0].Title, prop.Items[0].URL)

	// The proposal already passed tryJSONRule to get here. These assert the
	// things a passing gate does not: that the entries are the CHAPTERS and not
	// some other array, and that the links go somewhere.
	if prop.Found < 100 {
		t.Errorf("found %d entries, expected the chapter archive", prop.Found)
	}
	if !strings.HasPrefix(prop.Items[0].URL, "https://hni-scantrad.net/") {
		t.Errorf("first link = %q", prop.Items[0].URL)
	}
	if prop.Items[0].PublishedAt.IsZero() {
		t.Error("no date was extracted")
	}
}
