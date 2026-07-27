package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The bug this file exists for: every piece of the interest layer was built and
// unit-tested, and NONE of it ran. Nothing imported internal/jobs or
// internal/derive, so no pool was ever constructed, no derive job was ever
// enqueued, and a database with a thousand real engagement rows had zero rows in
// every derived table.
//
// That failure is invisible from inside the packages it breaks. derive's own
// tests pass — they call Handle directly. jobs' tests pass — they register their
// own handlers. The gap was exactly at the wiring, which is the one place neither
// suite looks, so the test for it has to live here and it has to go through the
// real Open → StartWorkers → DeriveDue path rather than calling the handler.
func TestDeriveRunsThroughThePool(t *testing.T) {
	ctx := t.Context()
	a, err := Open(ctx, Config{DBPath: filepath.Join(t.TempDir(), "derive.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	sc := seedReader(t, a)

	// Nothing derived yet: the point of the assertion is that the emptiness below
	// is caused by the absence of a run, not by an empty account.
	if n := countRanked(t, a, sc); n != 0 {
		t.Fatalf("home_ranking had %d rows before any derivation", n)
	}

	a.StartWorkers(ctx)
	a.DeriveDue(ctx)

	// The pool polls, so the job is picked up on the worker's schedule rather
	// than synchronously. Waiting for the OUTCOME rather than sleeping a fixed
	// duration: a fixed sleep is either flaky on a loaded CI box or slow on a
	// fast one, and this way the failure message says what never happened.
	waitUntil(t, 30*time.Second, func() bool { return countRanked(t, a, sc) > 0 })

	ranked, err := a.repo.HomeRanking(ctx, sc, 50)
	if err != nil {
		t.Fatalf("HomeRanking: %v", err)
	}
	if len(ranked) == 0 {
		t.Fatal("the pool ran and produced an empty homepage")
	}

	// Ordering is the product. A ranking that returns rows in an arbitrary order
	// is indistinguishable from no ranking at all, and `rank` is an indexed
	// column, so a regression here would be silent.
	for i := 1; i < len(ranked); i++ {
		if ranked[i-1].Rank > ranked[i].Rank {
			t.Fatalf("home_ranking came back out of order at %d: rank %d then %d",
				i, ranked[i-1].Rank, ranked[i].Rank)
		}
	}

	// The recall stage has to have produced something too, or the ranking above
	// is freshness-only and the interest layer is decorative.
	aff, err := a.repo.FeedAffinities(ctx, sc)
	if err != nil {
		t.Fatalf("FeedAffinities: %v", err)
	}
	if len(aff) == 0 {
		t.Fatal("no feed affinity was derived from engagements that clearly favour one feed")
	}
}

// My Feed reads the materialised ranking, in rank order, and drops what no longer
// belongs on it.
//
// The ordering assertion is the load-bearing one. RankedItems could have been written
// as HomeRanking plus ItemsByID, which returns no order at all — and the result would
// be a page that looks personalised, is arbitrary, and passes any test that only counts
// rows.
func TestMyFeedServesTheRankingInOrder(t *testing.T) {
	ctx := t.Context()
	a, err := Open(ctx, Config{DBPath: filepath.Join(t.TempDir(), "myfeed.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	sc := seedReader(t, a)
	a.StartWorkers(ctx)
	a.DeriveDue(ctx)
	waitUntil(t, 30*time.Second, func() bool { return countRanked(t, a, sc) > 0 })

	ranked, items, err := a.svc.ListRanked(ctx, sc, 0, 50)
	if err != nil {
		t.Fatalf("ListRanked: %v", err)
	}
	if len(ranked) == 0 {
		t.Fatal("My Feed is empty after a successful derivation")
	}
	if len(ranked) != len(items) {
		t.Fatalf("ranking and items disagree: %d vs %d", len(ranked), len(items))
	}
	for i := 1; i < len(ranked); i++ {
		if ranked[i-1].Rank >= ranked[i].Rank {
			t.Fatalf("out of rank order at %d: %d then %d", i, ranked[i-1].Rank, ranked[i].Rank)
		}
	}
	// The rows must line up with their items, or every reason line is attached to the
	// wrong article — a failure that looks like a bad ranker rather than a bad join.
	for i := range ranked {
		if ranked[i].ItemID != items[i].ID {
			t.Fatalf("row %d: ranking is for %s but the item is %s",
				i, ranked[i].ItemID, items[i].ID)
		}
	}
	// Every ranked row carries an explanation. §18.9 makes this the product, and an
	// unexplained pick is one the reader cannot correct.
	for _, r := range ranked {
		if len(r.Reasons) == 0 {
			t.Errorf("ranked item %s has no reason", r.ItemID)
		}
		if r.Tier == "" {
			t.Errorf("ranked item %s has no tier", r.ItemID)
		}
	}

	// Reading one removes it. A homepage that keeps offering what you have read is
	// broken in a way that is obvious to everyone except the code.
	before := len(ranked)
	if _, _, err := a.svc.SetItemState(ctx, sc, ranked[0].ItemID, store.StateChange{
		Read: boolPtr(true),
	}); err != nil {
		t.Fatalf("SetItemState: %v", err)
	}
	after, _, err := a.svc.ListRanked(ctx, sc, 0, 50)
	if err != nil {
		t.Fatalf("ListRanked after read: %v", err)
	}
	if len(after) != before-1 {
		t.Errorf("after reading one item My Feed had %d rows, want %d", len(after), before-1)
	}
	for _, r := range after {
		if r.ItemID == ranked[0].ItemID {
			t.Error("a read item is still on My Feed")
		}
	}
}

// A feed the reader took off their front page must not appear on it.
//
// in_megafeed has existed in the schema and in feed settings from the start, and the
// ranking ignored it — so the control was visibly present and did nothing.
func TestMyFeedHonoursTheMegafeedOptOut(t *testing.T) {
	ctx := t.Context()
	a, err := Open(ctx, Config{DBPath: filepath.Join(t.TempDir(), "optout.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	sc := seedReader(t, a)
	feeds, err := a.repo.ListFeeds(ctx, sc)
	if err != nil || len(feeds) == 0 {
		t.Fatalf("ListFeeds: %v (%d feeds)", err, len(feeds))
	}
	// Take the first feed off the front page.
	excluded := feeds[0].SourceID
	no := false
	if _, err := a.repo.UpdateFeedSettings(ctx, sc, excluded,
		store.FeedSettingsPatch{InMegafeed: &no}); err != nil {
		t.Fatalf("UpdateFeedSettings: %v", err)
	}

	a.StartWorkers(ctx)
	a.DeriveDue(ctx)
	waitUntil(t, 30*time.Second, func() bool { return countRanked(t, a, sc) > 0 })

	_, items, err := a.svc.ListRanked(ctx, sc, 0, 200)
	if err != nil {
		t.Fatalf("ListRanked: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("excluding one of two feeds emptied My Feed entirely")
	}
	for _, it := range items {
		if it.SourceID == excluded {
			t.Fatalf("item %s from an opted-out feed is on My Feed", it.ID)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

// A reader with no affinity-bearing engagement must not schedule a derivation.
//
// This is not tidiness: derive is a TF-IDF pass over ninety days of engaged
// items, and the poller calls DeriveDue every interval forever. Without the kind
// filter in ScopesToDerive, every account on the box gets a full derivation every
// fifteen minutes for the lifetime of the instance, including accounts nobody has
// opened since March.
func TestDeriveSkipsReadersWhoOnlyScrolled(t *testing.T) {
	ctx := t.Context()
	a, err := Open(ctx, Config{DBPath: filepath.Join(t.TempDir(), "skip.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	sc := seedReader(t, a)

	// R17's shape, at the scheduling layer: seen everything, marked it all read,
	// engaged with none of it. Neither kind moves affinity (signals.MovesAffinity), so a
	// derivation could not change a single number even if it ran.
	items := allItems(t, a, sc)
	record(t, a, sc, signals.Impression, items, time.Now().UTC())
	record(t, a, sc, signals.BulkRead, items, time.Now().UTC())

	a.deriveMu.Lock()
	a.lastDerive = time.Now().UTC().Add(-24 * time.Hour)
	a.deriveMu.Unlock()

	scopes, err := a.repo.ScopesToDerive(ctx, a.lastDerive)
	if err != nil {
		t.Fatalf("ScopesToDerive: %v", err)
	}
	if len(scopes) != 0 {
		t.Fatalf("scrolling and mark-all-read scheduled %d derivation(s), want 0", len(scopes))
	}

	// And one real signal is enough to schedule one, so the filter above is
	// selective rather than simply broken.
	record(t, a, sc, signals.Completed, items[:1], time.Now().UTC())
	scopes, err = a.repo.ScopesToDerive(ctx, a.lastDerive)
	if err != nil {
		t.Fatalf("ScopesToDerive: %v", err)
	}
	if len(scopes) != 1 {
		t.Fatalf("a completed read scheduled %d derivation(s), want 1", len(scopes))
	}
}

// The cursor must not advance past work that was never enqueued.
//
// Advancing first and failing second loses a whole window of engagements, and the
// symptom — a homepage stale by exactly one interval, occasionally — is close to
// unfindable in the wild.
func TestDeriveCursorAdvancesOnlyOnSuccess(t *testing.T) {
	ctx := t.Context()
	a, err := Open(ctx, Config{DBPath: filepath.Join(t.TempDir(), "cursor.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	sc := seedReader(t, a)

	// Five minutes ago, not "now". DeriveDue runs in under a millisecond, so an
	// engagement stamped at the current instant lands on the same millisecond the
	// cursor advances to — and the window boundary is inclusive on purpose (see
	// DeriveDue), so it would legitimately be seen twice. Backdating separates
	// "the cursor works" from that deliberate one-millisecond overlap.
	record(t, a, sc, signals.Completed, allItems(t, a, sc), time.Now().UTC().Add(-5*time.Minute))

	a.deriveMu.Lock()
	a.lastDerive = time.Now().UTC().Add(-time.Hour)
	before := a.lastDerive
	a.deriveMu.Unlock()

	a.DeriveDue(ctx)

	a.deriveMu.Lock()
	after := a.lastDerive
	a.deriveMu.Unlock()
	if !after.After(before) {
		t.Fatal("the cursor did not advance after a successful enqueue")
	}

	// A second pass finds nothing new, which is what stops the queue filling with
	// identical jobs on every tick.
	scopes, err := a.repo.ScopesToDerive(ctx, after)
	if err != nil {
		t.Fatalf("ScopesToDerive: %v", err)
	}
	if len(scopes) != 0 {
		t.Fatalf("the same engagements scheduled %d more derivation(s)", len(scopes))
	}
}

// seedReader creates an account with two feeds of clearly different subject
// matter and engagement that favours one, so a derivation has something to find.
func seedReader(t *testing.T, a *App) store.Scope {
	t.Helper()
	ctx := t.Context()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)

	if err := a.repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "reader",
		Hash: "x", Role: "member", Now: stamp,
	}); err != nil {
		t.Fatalf("CreateTenantAndUser: %v", err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	corpora := map[string][]string{
		"Systems": {
			"sqlite btree page cache write ahead log checkpoint",
			"sqlite wal fsync page cache btree durability",
			"btree page cache sqlite index vacuum fsync",
			"sqlite virtual table fts5 tokenizer index btree",
			"page cache wal checkpoint sqlite durability fsync",
			"sqlite index btree vacuum page cache wal",
		},
		"Racing": {
			"formula one aerodynamics downforce cornering tyre",
			"downforce aerodynamics tyre degradation cornering formula",
			"tyre compound degradation formula aerodynamics downforce",
			"cornering downforce formula tyre aerodynamics wing",
			"aerodynamics wing downforce tyre formula cornering",
			"formula tyre degradation cornering downforce wing",
		},
	}

	now := time.Now().UTC()
	var systems []string
	for title, texts := range corpora {
		feed, _, err := a.repo.Subscribe(ctx, sc, store.NewSubscription{
			NaturalKey: "feed:" + title,
			FeedURL:    "https://" + title + ".example/rss",
			SiteURL:    "https://" + title + ".example/",
			Title:      title,
		})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		var ingest []store.IngestItem
		for i, text := range texts {
			ingest = append(ingest, store.IngestItem{
				GUID:        title + "-" + itoa(i),
				URL:         "https://target-" + title + ".example/" + itoa(i),
				Title:       text,
				Summary:     text,
				ContentHTML: "<p>" + text + "</p>",
				PublishedAt: now.Add(-time.Duration(i+1) * 6 * time.Hour),
				WordCount:   400,
			})
		}
		if _, err := a.repo.IngestItems(ctx, feed.SourceID, ingest); err != nil {
			t.Fatalf("IngestItems: %v", err)
		}
		if title == "Systems" {
			items, _, err := a.repo.ListItems(ctx, sc, store.ListQuery{
				SourceID: feed.SourceID, Limit: 100,
			})
			if err != nil {
				t.Fatalf("ListItems: %v", err)
			}
			for _, it := range items {
				systems = append(systems, it.ID)
			}
		}
	}

	// Read, finished and liked the systems feed. Enough deliberate signal that
	// the precision stage has evidence, not just the passive recall stage.
	record(t, a, sc, signals.Impression, allItems(t, a, sc), now.Add(-48*time.Hour))
	record(t, a, sc, signals.Opened, systems, now.Add(-47*time.Hour))
	record(t, a, sc, signals.Completed, systems, now.Add(-46*time.Hour))
	record(t, a, sc, signals.Liked, systems[:3], now.Add(-45*time.Hour))
	return sc
}

func record(t *testing.T, a *App, sc store.Scope, kind signals.Kind, itemIDs []string, at time.Time) {
	t.Helper()
	var evs []signals.Event
	for _, id := range itemIDs {
		evs = append(evs, signals.Event{
			ID: idgen.New(), ItemID: id, Kind: kind,
			Surface: "list", At: at.UnixMilli(),
		})
	}
	if _, err := a.repo.RecordEngagements(t.Context(), sc, evs); err != nil {
		t.Fatalf("RecordEngagements: %v", err)
	}
}

func allItems(t *testing.T, a *App, sc store.Scope) []string {
	t.Helper()
	items, _, err := a.repo.ListItems(t.Context(), sc, store.ListQuery{Limit: 200})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

func countRanked(t *testing.T, a *App, sc store.Scope) int {
	t.Helper()
	rows, err := a.repo.HomeRanking(t.Context(), sc, 200)
	if err != nil {
		t.Fatalf("HomeRanking: %v", err)
	}
	return len(rows)
}

// waitUntil polls until cond holds, so the test waits on the outcome rather than
// on a duration guessed to be long enough.
func waitUntil(t *testing.T, limit time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("condition still false after %s", limit)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
