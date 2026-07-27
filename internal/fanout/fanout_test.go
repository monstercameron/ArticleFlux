package fanout

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/rules"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

type fixture struct {
	repo    *store.ReaderRepo
	svc     *Service
	ctx     context.Context
	alice   store.Scope
	bob     store.Scope
	source  string
	itemIDs []string
}

// Two tenants subscribed to ONE global source, which is the configuration §13.2
// is about. Isolation is uninteresting when nothing overlaps.
func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "fanout.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	for _, tt := range []store.NewTenant{
		{TenantID: "ta", Name: "A", UserID: "alice", Username: "alice", Hash: "x", Role: "member", Now: now},
		{TenantID: "tb", Name: "B", UserID: "bob", Username: "bob", Hash: "x", Role: "member", Now: now},
	} {
		if err := repo.CreateTenantAndUser(ctx, tt); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{
		repo:  repo,
		svc:   New(repo, nil),
		ctx:   ctx,
		alice: store.Scope{TenantID: "ta", UserID: "alice", Role: "member"},
		bob:   store.Scope{TenantID: "tb", UserID: "bob", Role: "member"},
	}

	sub := store.NewSubscription{
		NaturalKey: "feed:news.example/rss",
		FeedURL:    "https://news.example/rss",
		SiteURL:    "https://news.example/",
		Title:      "News",
	}
	feed, _, err := repo.Subscribe(ctx, f.alice, sub)
	if err != nil {
		t.Fatal(err)
	}
	f.source = feed.SourceID
	if _, _, err := repo.Subscribe(ctx, f.bob, sub); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.IngestItems(ctx, f.source, []store.IngestItem{
		{GUID: "g1", URL: "https://news.example/1", Title: "Rust 2.0 is here",
			Summary: "A big release", ContentHTML: "<p>ownership and lifetimes</p>",
			PublishedAt: time.Now().UTC(), WordCount: 900},
		{GUID: "g2", URL: "https://news.example/2", Title: "Python news",
			Summary: "Something else", ContentHTML: "<p>unrelated</p>",
			PublishedAt: time.Now().UTC(), WordCount: 300},
	}); err != nil {
		t.Fatal(err)
	}

	items, _, err := repo.ListItems(ctx, f.alice, store.ListQuery{Limit: 10})
	if err != nil || len(items) != 2 {
		t.Fatalf("expected 2 items, got %d (%v)", len(items), err)
	}
	for _, it := range items {
		f.itemIDs = append(f.itemIDs, it.ID)
	}
	return f
}

func (f *fixture) run(t *testing.T) {
	t.Helper()
	payload, err := json.Marshal(Payload{SourceID: f.source, ItemIDs: f.itemIDs})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Handle(f.ctx, store.Job{Kind: store.JobFanout, Payload: string(payload)}); err != nil {
		t.Fatalf("fanout: %v", err)
	}
}

func muteRust(t *testing.T, f *fixture, s store.Scope) string {
	t.Helper()
	id, err := f.repo.CreateRule(f.ctx, s, rules.Rule{
		Name: "mute rust", Enabled: true,
		Match:   rules.Match{Conditions: []rules.Condition{{Field: rules.FieldTitle, Op: rules.OpContains, Value: "rust"}}},
		Actions: []rules.Action{{Kind: rules.ActionMute}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// §13.2's bug, and the reason fan-out is per subscriber. Alice mutes something;
// Bob must still see it. Evaluating once at ingest gets this wrong, and the
// symptom lands in an account that has no rule at all.
func TestOneUsersMuteDoesNotHideAnItemFromAnother(t *testing.T) {
	f := setup(t)
	muteRust(t, f, f.alice)
	f.run(t)

	aliceMuted, _, err := f.repo.MutedItems(f.ctx, f.alice, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceMuted) != 1 {
		t.Fatalf("alice has %d muted items, want 1", len(aliceMuted))
	}
	if aliceMuted[0].Title != "Rust 2.0 is here" {
		t.Errorf("alice muted the wrong item: %q", aliceMuted[0].Title)
	}

	bobMuted, _, err := f.repo.MutedItems(f.ctx, f.bob, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobMuted) != 0 {
		t.Errorf("BOB has %d muted items — alice's rule reached another account", len(bobMuted))
	}

	// And the item itself is untouched. Mute is not delete (§13.3).
	items, _, err := f.repo.ListItems(f.ctx, f.bob, store.ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Errorf("bob sees %d items, want both", len(items))
	}
}

// §13.3: a muted item is stored and flagged, so unmuting works and a MUTED view
// can show what a rule is eating.
func TestMuteIsRecoverable(t *testing.T) {
	f := setup(t)
	ruleID := muteRust(t, f, f.alice)
	f.run(t)

	if muted, _, _ := f.repo.MutedItems(f.ctx, f.alice, 10); len(muted) != 1 {
		t.Fatalf("expected one muted item, got %d", len(muted))
	}

	n, err := f.repo.UnmuteByRule(f.ctx, f.alice, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("unmuted %d, want 1", n)
	}
	if muted, _, _ := f.repo.MutedItems(f.ctx, f.alice, 10); len(muted) != 0 {
		t.Errorf("%d items still muted after undoing the rule", len(muted))
	}
}

func TestActionsAreApplied(t *testing.T) {
	f := setup(t)
	if _, err := f.repo.CreateRule(f.ctx, f.alice, rules.Rule{
		Name: "long rust posts", Enabled: true,
		Match: rules.Match{Conditions: []rules.Condition{
			{Field: rules.FieldTitle, Op: rules.OpContains, Value: "rust"},
			{Field: rules.FieldWordCount, Op: rules.OpGT, Value: "500"},
		}},
		Actions: []rules.Action{
			{Kind: rules.ActionStar},
			{Kind: rules.ActionTag, Value: "deep-dive"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	f.run(t)

	items, _, err := f.repo.ListItems(f.ctx, f.alice, store.ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var starred int
	for _, it := range items {
		if it.Starred {
			starred++
			if it.Title != "Rust 2.0 is here" {
				t.Errorf("starred the wrong item: %q", it.Title)
			}
		}
	}
	if starred != 1 {
		t.Errorf("%d items starred, want 1", starred)
	}

	tags, err := f.repo.ListTags(f.ctx, f.alice)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tag := range tags {
		if tag.Name == "deep-dive" {
			found = true
		}
	}
	if !found {
		t.Errorf("the rule's tag was not created: %+v", tags)
	}
}

// The queue is at-least-once — a reclaimed job, a retry, a retroactive apply —
// so fan-out running twice must not undo what a person did in between.
func TestReRunningDoesNotUndoTheReadersWork(t *testing.T) {
	f := setup(t)
	f.run(t)

	// The reader marks something read and stars it themselves.
	if _, err := f.repo.SetItemState(f.ctx, f.alice, f.itemIDs[0], store.StateChange{
		Read: boolPtr(true), Starred: boolPtr(true),
	}); err != nil {
		t.Fatal(err)
	}

	// Fan-out runs again, with no rules at all.
	f.run(t)

	items, _, err := f.repo.ListItems(f.ctx, f.alice, store.ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ID != f.itemIDs[0] {
			continue
		}
		if !it.Read {
			t.Error("re-running fan-out marked a read item unread")
		}
		if !it.Starred {
			t.Error("re-running fan-out un-starred an item the reader starred")
		}
	}
}

// Every delivered item gets a state row whether or not a rule matched. Without
// it the item is not known to the user at all, and unread counts and paging both
// depend on the row existing.
func TestEveryItemGetsStateEvenWithNoRules(t *testing.T) {
	f := setup(t)
	f.run(t)

	for _, s := range []store.Scope{f.alice, f.bob} {
		items, _, err := f.repo.ListItems(f.ctx, s, store.ListQuery{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 {
			t.Errorf("%s sees %d items, want 2", s.UserID, len(items))
		}
	}
}

// §13 promises the reader can find out why something happened, and §13.5 wants
// "this rule has not matched in six months".
func TestHitsAndCountersAreRecorded(t *testing.T) {
	f := setup(t)
	ruleID := muteRust(t, f, f.alice)
	f.run(t)

	_, stats, err := f.repo.ListRules(f.ctx, f.alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("got %d rules", len(stats))
	}
	if stats[0].MatchCount != 1 {
		t.Errorf("match count = %d, want 1", stats[0].MatchCount)
	}
	if stats[0].LastMatchedAt == "" {
		t.Error("last matched is empty, so §13.5 cannot report a dead rule")
	}

	muted, ruleIDs, err := f.repo.MutedItems(f.ctx, f.alice, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(muted) != 1 || ruleIDs[0] != ruleID {
		t.Errorf("the muted item does not name the rule that muted it: %v", ruleIDs)
	}
}

// A rule matching on content must see TEXT, not markup — otherwise "contains
// rust" matches a link to rust-lang.org in a footer and the reader cannot see
// why.
func TestContentIsMatchedAsTextNotMarkup(t *testing.T) {
	f := setup(t)
	if _, err := f.repo.CreateRule(f.ctx, f.alice, rules.Rule{
		Name: "markup should not match", Enabled: true,
		Match: rules.Match{Conditions: []rules.Condition{
			{Field: rules.FieldContent, Op: rules.OpContains, Value: "<p>"},
		}},
		Actions: []rules.Action{{Kind: rules.ActionMute}},
	}); err != nil {
		t.Fatal(err)
	}
	f.run(t)

	if muted, _, _ := f.repo.MutedItems(f.ctx, f.alice, 10); len(muted) != 0 {
		t.Errorf("%d items matched on raw markup; rules see text", len(muted))
	}
}

// A source with no subscribers is normal — unsubscribing never deactivates a
// global source (A22), so the row outlives its last reader.
func TestNoSubscribersIsNotAnError(t *testing.T) {
	f := setup(t)
	if err := f.repo.Unsubscribe(f.ctx, f.alice, f.source); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Unsubscribe(f.ctx, f.bob, f.source); err != nil {
		t.Fatal(err)
	}
	f.run(t)
}

func TestMalformedPayloadIsRejected(t *testing.T) {
	f := setup(t)
	err := f.svc.Handle(f.ctx, store.Job{Kind: store.JobFanout, Payload: "{not json"})
	if err == nil {
		t.Error("a malformed payload was accepted")
	}
}

func boolPtr(b bool) *bool { return &b }
