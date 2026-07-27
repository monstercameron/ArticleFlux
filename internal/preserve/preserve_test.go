package preserve

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/extract"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// fakeSite stands in for the web. It can be killed mid-test, which is the whole
// scenario this package exists for and the ticket's acceptance bar.
type fakeSite struct {
	mu    sync.Mutex
	dead  bool
	calls int
}

func (f *fakeSite) kill() {
	f.mu.Lock()
	f.dead = true
	f.mu.Unlock()
}

func (f *fakeSite) Fetch(_ context.Context, url string) (*extract.Article, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.dead {
		return nil, fmt.Errorf("dial tcp: no such host")
	}
	return &extract.Article{
		Title: "Archived " + url,
		HTML:  "<p>The full article body from " + url + ", which the feed only teased.</p>",
		Text:  "The full article body from " + url + ", which the feed only teased.",
	}, nil
}

type fixture struct {
	repo   *store.ReaderRepo
	svc    *Service
	site   *fakeSite
	ctx    context.Context
	scope  store.Scope
	source string
	items  []string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "preserve.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)
	now := time.Now().UTC()

	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "u",
		Hash: "x", Role: "member", Now: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	feed, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:doomed",
		FeedURL:    "https://doomed.example/rss",
		SiteURL:    "https://doomed.example/",
		Title:      "Doomed",
	})
	if err != nil {
		t.Fatal(err)
	}

	// A feed that publishes teasers — the case where losing the origin loses the
	// article, and the one §10.6 is written around.
	var ingest []store.IngestItem
	for i := 0; i < 5; i++ {
		ingest = append(ingest, store.IngestItem{
			GUID:        fmt.Sprintf("g%d", i),
			URL:         fmt.Sprintf("https://doomed.example/%d", i),
			Title:       fmt.Sprintf("Article %d", i),
			Summary:     "A teaser.",
			ContentHTML: "<p>A teaser. Continue reading&hellip;</p>",
			PublishedAt: now.Add(-time.Duration(i) * time.Hour),
			WordCount:   6,
		})
	}
	if _, err := repo.IngestItems(ctx, feed.SourceID, ingest); err != nil {
		t.Fatal(err)
	}

	items, _, err := repo.ListItems(ctx, sc, store.ListQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{repo: repo, site: &fakeSite{}, ctx: ctx, scope: sc, source: feed.SourceID}
	f.svc = New(repo, f.site, nil)
	for _, it := range items {
		f.items = append(f.items, it.ID)
	}
	return f
}

func (f *fixture) sweep(t *testing.T) error {
	t.Helper()
	payload, err := json.Marshal(Payload{SourceID: f.source, Reason: ReasonDistress})
	if err != nil {
		t.Fatal(err)
	}
	return f.svc.Handle(f.ctx, store.Job{Kind: store.JobPreserve, Payload: string(payload)})
}

// The ticket's acceptance bar: killing the fixture feed mid-test leaves its
// items still readable.
func TestKillingTheSiteLeavesArchivedItemsReadable(t *testing.T) {
	f := setup(t)

	// The source starts failing. §10.6's distress sweep runs while the article
	// URLs still resolve — that window is the entire opportunity.
	if err := f.sweep(t); err != nil {
		t.Fatalf("the sweep failed while the site was up: %v", err)
	}

	// Now the site goes away completely.
	f.site.kill()

	// Every item is still readable, in full, from the archive.
	for _, id := range f.items {
		a, err := f.repo.GetArchive(f.ctx, id)
		if err != nil {
			t.Fatalf("item %s is not readable after the site died: %v", id, err)
		}
		if !strings.Contains(a.Text, "The full article body") {
			t.Errorf("item %s archived the teaser rather than the article: %q", id, a.Text)
		}
	}
}

// A sweep that starts after the site is already gone must not fail loudly for
// every item, and must record what it learned.
func TestSweepingADeadSiteRecordsThatTheOriginIsGone(t *testing.T) {
	f := setup(t)
	f.site.kill()

	// It returns an error — the caller should see that a sweep achieved nothing —
	// but it must have tried every item rather than stopping at the first.
	if err := f.sweep(t); err == nil {
		t.Error("a sweep of a dead site reported success")
	}
	if f.site.calls < len(f.items) {
		t.Errorf("the sweep stopped after %d of %d items; on a dying site most will "+
			"fail and the few that succeed are the point", f.site.calls, len(f.items))
	}

	// And each item now knows its origin is dead, which is what makes the UI
	// offer a Wayback link rather than a broken one.
	for _, id := range f.items {
		a, err := f.repo.GetArchive(f.ctx, id)
		if err != nil {
			t.Fatalf("no row for %s: %v", id, err)
		}
		if !a.OriginDead {
			t.Errorf("item %s was not marked origin-dead", id)
		}
	}
}

// §10.6's rule, and the one eviction may never break: an archive whose origin is
// dead is the only copy that exists anywhere.
func TestEvictionCanNeverDropAnArchiveWhoseOriginIsDead(t *testing.T) {
	f := setup(t)
	if err := f.sweep(t); err != nil {
		t.Fatal(err)
	}

	// Two of them lose their origin.
	doomed := f.items[:2]
	for _, id := range doomed {
		if err := f.repo.MarkOriginDead(f.ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	// Ask eviction to free far more than exists — the disk-emergency case.
	freed, n, err := f.repo.EvictArchives(f.ctx, 1<<40)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("eviction dropped nothing at all, so it proves nothing")
	}
	t.Logf("evicted %d archives, %d bytes", n, freed)

	for _, id := range doomed {
		a, err := f.repo.GetArchive(f.ctx, id)
		if err != nil {
			t.Errorf("eviction destroyed the only remaining copy of %s", id)
			continue
		}
		if !a.OriginDead {
			t.Errorf("%s lost its origin-dead flag", id)
		}
	}

	// The rest are gone, which is what makes the test above meaningful.
	stats, err := f.repo.ArchiveStats(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Count != len(doomed) {
		t.Errorf("%d archives remain, want only the %d irreplaceable ones", stats.Count, len(doomed))
	}
	if stats.OriginDead != len(doomed) {
		t.Errorf("origin-dead count = %d", stats.OriginDead)
	}
}

// Eviction orders by last read, so an old archive someone still opens outlives a
// newer one nobody has touched.
func TestEvictionPrefersTheUnread(t *testing.T) {
	f := setup(t)
	if err := f.sweep(t); err != nil {
		t.Fatal(err)
	}

	// Read one of them, which records the read.
	read := f.items[0]
	if _, err := f.repo.GetArchive(f.ctx, read); err != nil {
		t.Fatal(err)
	}

	// Free about half. Asking for everything would evict everything, which is
	// correct behaviour and would say nothing about the ORDER — the property
	// under test is which ones go first, not whether the target is honoured.
	stats, _ := f.repo.ArchiveStats(f.ctx)
	if _, n, err := f.repo.EvictArchives(f.ctx, stats.Bytes/2); err != nil {
		t.Fatal(err)
	} else if n == 0 || n == len(f.items) {
		t.Fatalf("evicted %d of %d; the test needs a partial eviction to say anything",
			n, len(f.items))
	}

	if _, err := f.repo.GetArchive(f.ctx, read); err != nil {
		t.Error("eviction dropped the archive that had been read in favour of unread ones")
	}
}

// Re-running a sweep must not refetch what is already archived: on a struggling
// site that is the difference between polite and a crawl.
func TestArchivingIsIdempotent(t *testing.T) {
	f := setup(t)
	if err := f.sweep(t); err != nil {
		t.Fatal(err)
	}
	first := f.site.calls

	if err := f.sweep(t); err != nil {
		t.Fatal(err)
	}
	if f.site.calls != first {
		t.Errorf("a second sweep made %d more requests to a site already archived",
			f.site.calls-first)
	}
}

// The eager tier's judgement, tested without a network because that is the part
// with a decision in it.
func TestShouldArchiveAtIngest(t *testing.T) {
	cases := []struct {
		name     string
		affinity float64
		words    int
		policy   string
		want     bool
	}{
		{"never means never, whatever else is true", 1.0, 10, "never", false},
		{"always means always", 0.0, 5000, "always", true},
		// The combination that matters: a teaser from a feed the reader engages
		// with is where losing the origin loses the article.
		{"truncated content from a followed feed", 0.5, 40, "", true},
		{"truncated content from a feed nobody reads", 0.05, 40, "", false},
		{"full content from a feed nobody reads", 0.05, 3000, "", false},
		{"full content from a much-loved feed", 0.9, 3000, "", true},
		{"unknown word count is not treated as truncated", 0.5, 0, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := ShouldArchiveAtIngest(c.affinity, c.words, c.policy)
			if got != c.want {
				t.Errorf("= %v, want %v", got, c.want)
			}
			if got && reason == "" {
				t.Error("archiving was chosen with no reason recorded")
			}
		})
	}
}

// One timeout is noise — a phone tethering, a publisher restarting nginx — and
// sweeping on it would mean crawling a site every time its TLS terminator
// hiccups.
func TestDistressNeedsMoreThanOneFailure(t *testing.T) {
	for failures, want := range map[int]bool{0: false, 1: false, 2: false, 3: true, 10: true} {
		if got := SweepOnDistress(failures); got != want {
			t.Errorf("SweepOnDistress(%d) = %v, want %v", failures, got, want)
		}
	}
}

// A source that keeps failing must not queue a sweep on every poll, or a
// struggling site gets swept by us every fifteen minutes.
func TestDistressSweepsAreDedupedPerSource(t *testing.T) {
	f := setup(t)
	for i := 0; i < 12; i++ {
		if err := f.svc.EnqueueDistressSweep(f.ctx, f.source); err != nil {
			t.Fatal(err)
		}
	}
	depth, err := f.repo.QueueDepth(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth[store.JobPreserve].Queued != 1 {
		t.Errorf("%d sweeps queued for one failing source, want 1", depth[store.JobPreserve].Queued)
	}
}

func TestItemsWithNoURLAreSkippedQuietly(t *testing.T) {
	f := setup(t)
	// An item with no link cannot be archived and is not a failure.
	if _, err := f.repo.IngestItems(f.ctx, f.source, []store.IngestItem{{
		GUID: "nolink", Title: "No link", Summary: "x", PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := f.sweep(t); err != nil {
		t.Errorf("an item with no URL made the sweep fail: %v", err)
	}
}
