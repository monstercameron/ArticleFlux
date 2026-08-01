package store

import (
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/rules"
)

// fanoutFixture is one tenant/user subscribed to one source with a folder and
// a feed tag, plus items ready for fan-out.
type fanoutFixture struct {
	db    *DB
	repo  *ReaderRepo
	sc    Scope
	items []string
}

func seedFanout(t *testing.T, n int) *fanoutFixture {
	t.Helper()
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	now := time.Now().UTC().Format(time.RFC3339)

	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "alice",
		Hash: "h", Role: "member", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	for _, s := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO sources (id,natural_key,feed_url,title,language,created_at) VALUES ('src1','feed:a','https://a.example/feed','Alpha','en',?)`, []any{now}},
		{`INSERT INTO folders (id,tenant_id,user_id,name,depth,created_at) VALUES ('f1','t1','u1','Tech',0,?)`, []any{now}},
		{`INSERT INTO tags (id,tenant_id,user_id,name,created_at) VALUES ('tag1','t1','u1','favorites',?)`, []any{now}},
		{`INSERT INTO subscriptions (id,tenant_id,user_id,source_id,folder_id,created_at) VALUES ('sub1','t1','u1','src1','f1',?)`, []any{now}},
		{`INSERT INTO feed_tags (tenant_id,user_id,tag_id,source_id,added_at) VALUES ('t1','u1','tag1','src1',?)`, []any{now}},
	} {
		if _, err := db.Write.ExecContext(ctx, s.q, s.args...); err != nil {
			t.Fatalf("seed %.60s: %v", s.q, err)
		}
	}

	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := "item" + string(rune('a'+i))
		if _, err := db.Write.ExecContext(ctx, `
			INSERT INTO items (id,source_id,guid,title,summary,published_at,first_seen_at,word_count)
			VALUES (?,?,?,?,?,?,?,?)`,
			id, "src1", "g"+id, "Test Article "+id, "summary", now, now, 100); err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	return &fanoutFixture{db: db, repo: repo, sc: sc, items: ids}
}

func TestSubscribersOfAggregatesFolderAndTags(t *testing.T) {
	f := seedFanout(t, 1)
	subs, err := f.repo.SubscribersOf(t.Context(), "src1")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d subscribers, want 1", len(subs))
	}
	s := subs[0]
	if s.TenantID != "t1" || s.UserID != "u1" || s.SourceID != "src1" {
		t.Errorf("got %+v", s)
	}
	if s.SourceName != "Alpha" {
		t.Errorf("SourceName = %q, want Alpha", s.SourceName)
	}
	if s.FolderName != "Tech" {
		t.Errorf("FolderName = %q, want Tech", s.FolderName)
	}
	if s.Lang != "en" {
		t.Errorf("Lang = %q, want en", s.Lang)
	}
	if len(s.Tags) != 1 || s.Tags[0] != "favorites" {
		t.Errorf("Tags = %v, want [favorites]", s.Tags)
	}
}

func TestSubscribersOfEmptyForUnsubscribedSource(t *testing.T) {
	f := seedFanout(t, 1)
	subs, err := f.repo.SubscribersOf(t.Context(), "no-such-source")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 0 {
		t.Errorf("got %d subscribers for a nonexistent source, want 0", len(subs))
	}
}

func TestItemsByIDLoadsRequestedRowsOnly(t *testing.T) {
	f := seedFanout(t, 3)
	got, err := f.repo.ItemsByID(t.Context(), []string{f.items[0], f.items[2]})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2", len(got))
	}
	seen := map[string]bool{}
	for _, it := range got {
		seen[it.ID] = true
	}
	if !seen[f.items[0]] || !seen[f.items[2]] {
		t.Errorf("wrong items returned: %v", got)
	}
}

func TestItemsByIDEmptyInputReturnsNil(t *testing.T) {
	f := seedFanout(t, 1)
	got, err := f.repo.ItemsByID(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got %v, want nil for an empty id list", got)
	}
}

// createRuleRow inserts a real row via CreateRule and returns rule with that
// row's id filled in. rule_hits.rule_id has a foreign key on rules(id), so
// any rule whose actions will actually fire (and thus write a hit) needs a
// row to reference — an in-memory-only rules.Rule is fine for a rule that
// never matches, but not for one that does.
func createRuleRow(t *testing.T, f *fanoutFixture, rule rules.Rule) rules.Rule {
	t.Helper()
	id, err := f.repo.CreateRule(t.Context(), f.sc, rule)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	rule.ID = id
	return rule
}

func markReadRule() rules.Rule {
	return rules.Rule{
		Name: "mark-read-tech", Enabled: true,
		Match: rules.Match{Conditions: []rules.Condition{
			{Field: rules.FieldTitle, Op: rules.OpContains, Value: "Test"},
		}},
		Actions: []rules.Action{{Kind: rules.ActionMarkRead}},
	}
}

// The main path: a rule that matches marks the item read and records exactly
// one hit.
func TestFanoutItemsAppliesMarkReadAndRecordsOneHit(t *testing.T) {
	f := seedFanout(t, 1)
	ctx := t.Context()
	subs, err := f.repo.SubscribersOf(ctx, "src1")
	if err != nil || len(subs) != 1 {
		t.Fatalf("subs=%v err=%v", subs, err)
	}
	items, err := f.repo.ItemsByID(ctx, f.items)
	if err != nil {
		t.Fatal(err)
	}
	ruleItems := make([]rules.Item, len(items))
	for i, it := range items {
		ruleItems[i] = rules.Item{Title: it.Title, WordCount: it.WordCount}
	}

	rule := createRuleRow(t, f, markReadRule())
	res, err := f.repo.FanoutItems(ctx, subs[0], []rules.Rule{rule}, ruleItems, f.items, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Delivered != 1 || res.Read != 1 || res.Hits != 1 {
		t.Errorf("res = %+v, want Delivered=1 Read=1 Hits=1", res)
	}

	got, err := f.repo.GetItem(ctx, f.sc, f.items[0])
	if err != nil {
		t.Fatal(err)
	}
	if !got.Read {
		t.Error("item was not marked read")
	}

	n, err := f.repo.CountRuleHits(ctx, f.sc, rule.ID, f.items[0])
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("rule_hits count = %d, want 1", n)
	}
}

// This is the core invariant the file's long comment on upsertState exists to
// protect: once a rule has fired once for an (item,user), a REDELIVERY of the
// same rule/item must not re-apply mark-read over a reader's own "mark
// unread" — alreadyFired must gate the coalesce, not just skip the row insert.
func TestFanoutItemsRedeliveryDoesNotResurrectAClearedReadFlag(t *testing.T) {
	f := seedFanout(t, 1)
	ctx := t.Context()
	subs, _ := f.repo.SubscribersOf(ctx, "src1")
	items, _ := f.repo.ItemsByID(ctx, f.items)
	ruleItems := []rules.Item{{Title: items[0].Title, WordCount: items[0].WordCount}}
	rs := []rules.Rule{createRuleRow(t, f, markReadRule())}

	if _, err := f.repo.FanoutItems(ctx, subs[0], rs, ruleItems, f.items, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The reader explicitly un-reads it.
	no := false
	if _, err := f.repo.SetItemState(ctx, f.sc, f.items[0], StateChange{Read: &no}); err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.GetItem(ctx, f.sc, f.items[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.Read {
		t.Fatal("SetItemState did not clear read")
	}

	// A second fan-out run — a reclaimed job, a retry — over the SAME rule and
	// item must not mark it read again.
	if _, err := f.repo.FanoutItems(ctx, subs[0], rs, ruleItems, f.items, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err = f.repo.GetItem(ctx, f.sc, f.items[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.Read {
		t.Error("a redelivered rule resurrected a read flag the reader explicitly cleared")
	}
	// And no second rule_hits row was written for the redelivery.
	n, err := f.repo.CountRuleHits(ctx, f.sc, rs[0].ID, f.items[0])
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("rule_hits count after a redelivery = %d, want 1 (recorded once)", n)
	}
}

func TestFanoutItemsAppliesTagAndCreatesTagOnFirstUse(t *testing.T) {
	f := seedFanout(t, 1)
	ctx := t.Context()
	subs, _ := f.repo.SubscribersOf(ctx, "src1")
	items, _ := f.repo.ItemsByID(ctx, f.items)
	ruleItems := []rules.Item{{Title: items[0].Title, WordCount: items[0].WordCount}}

	rule := createRuleRow(t, f, rules.Rule{Name: "auto-tag", Enabled: true,
		Match: rules.Match{Conditions: []rules.Condition{
			{Field: rules.FieldTitle, Op: rules.OpContains, Value: "Test"},
		}},
		Actions: []rules.Action{{Kind: rules.ActionTag, Value: "auto-applied"}},
	})
	res, err := f.repo.FanoutItems(ctx, subs[0], []rules.Rule{rule}, ruleItems, f.items, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Tagged != 1 {
		t.Errorf("Tagged = %d, want 1", res.Tagged)
	}
	tagMap, err := f.repo.ItemTags(ctx, f.sc, f.items)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tag := range tagMap[f.items[0]] {
		if tag.Name == "auto-applied" {
			found = true
		}
	}
	if !found {
		t.Errorf("applied tag not found: %v", tagMap[f.items[0]])
	}

	// Re-running must not double the tag (ON CONFLICT DO NOTHING).
	if _, err := f.repo.FanoutItems(ctx, subs[0], []rules.Rule{rule}, ruleItems, f.items, time.Now()); err != nil {
		t.Fatal(err)
	}
	tagMap2, err := f.repo.ItemTags(ctx, f.sc, f.items)
	if err != nil {
		t.Fatal(err)
	}
	if len(tagMap2[f.items[0]]) != 1 {
		t.Errorf("%d tags after a redelivery, want 1 (no duplicate)", len(tagMap2[f.items[0]]))
	}
}

func TestFanoutItemsMuteAndUnmuteByRule(t *testing.T) {
	f := seedFanout(t, 2)
	ctx := t.Context()
	subs, _ := f.repo.SubscribersOf(ctx, "src1")
	items, _ := f.repo.ItemsByID(ctx, f.items)
	ruleItems := make([]rules.Item, len(items))
	for i, it := range items {
		ruleItems[i] = rules.Item{Title: it.Title, WordCount: it.WordCount}
	}
	rule := createRuleRow(t, f, rules.Rule{Name: "mute-all", Enabled: true,
		Match: rules.Match{Conditions: []rules.Condition{
			{Field: rules.FieldTitle, Op: rules.OpContains, Value: "Test"},
		}},
		Actions: []rules.Action{{Kind: rules.ActionMute}},
	})
	res, err := f.repo.FanoutItems(ctx, subs[0], []rules.Rule{rule}, ruleItems, f.items, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Muted != 2 {
		t.Errorf("Muted = %d, want 2", res.Muted)
	}
	muted, ruleIDs, err := f.repo.MutedItems(ctx, f.sc, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(muted) != 2 {
		t.Fatalf("MutedItems returned %d, want 2", len(muted))
	}
	for _, rid := range ruleIDs {
		if rid != rule.ID {
			t.Errorf("muted_by_rule_id = %q, want %q", rid, rule.ID)
		}
	}

	n, err := f.repo.UnmuteByRule(ctx, f.sc, rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("UnmuteByRule restored %d, want 2", n)
	}
	mutedAfter, _, err := f.repo.MutedItems(ctx, f.sc, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(mutedAfter) != 0 {
		t.Errorf("%d items still muted after UnmuteByRule", len(mutedAfter))
	}
}

// A rule with no matching conditions delivers the item (the row is written so
// unread counts work) but takes no action.
func TestFanoutItemsWithNoMatchingRuleStillDeliversTheItem(t *testing.T) {
	f := seedFanout(t, 1)
	ctx := t.Context()
	subs, _ := f.repo.SubscribersOf(ctx, "src1")
	items, _ := f.repo.ItemsByID(ctx, f.items)
	ruleItems := []rules.Item{{Title: items[0].Title, WordCount: items[0].WordCount}}

	nonMatching := rules.Rule{ID: "rule-nomatch", Name: "never", Enabled: true,
		Match: rules.Match{Conditions: []rules.Condition{
			{Field: rules.FieldTitle, Op: rules.OpContains, Value: "nonexistent-phrase-xyz"},
		}},
		Actions: []rules.Action{{Kind: rules.ActionMarkRead}},
	}
	res, err := f.repo.FanoutItems(ctx, subs[0], []rules.Rule{nonMatching}, ruleItems, f.items, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Delivered != 1 || res.Read != 0 || res.Hits != 0 {
		t.Errorf("res = %+v, want Delivered=1 Read=0 Hits=0", res)
	}
	got, err := f.repo.GetItem(ctx, f.sc, f.items[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.Read {
		t.Error("a non-matching rule marked the item read")
	}
}

// set_home_weight is deliberately NOT gated by alreadyFired (see the comment
// on upsertState): every run recomputes it fresh, so a redelivery must still
// pick up a NEW weight rather than freezing at whatever the first run wrote.
func TestFanoutItemsSetHomeWeightRecomputesOnEveryRun(t *testing.T) {
	f := seedFanout(t, 1)
	ctx := t.Context()
	subs, _ := f.repo.SubscribersOf(ctx, "src1")
	items, _ := f.repo.ItemsByID(ctx, f.items)
	ruleItems := []rules.Item{{Title: items[0].Title, WordCount: items[0].WordCount}}

	weight := func(v string) rules.Rule {
		return createRuleRow(t, f, rules.Rule{Name: "weight-" + v, Enabled: true,
			Match: rules.Match{Conditions: []rules.Condition{
				{Field: rules.FieldTitle, Op: rules.OpContains, Value: "Test"},
			}},
			Actions: []rules.Action{{Kind: rules.ActionSetHomeWeight, Value: v}},
		})
	}
	readWeight := func() float64 {
		var w float64
		if err := f.db.Read.QueryRowContext(ctx,
			`SELECT home_weight FROM user_item_state WHERE user_id=? AND item_id=?`,
			f.sc.UserID, f.items[0]).Scan(&w); err != nil {
			t.Fatal(err)
		}
		return w
	}

	r1 := weight("2.5")
	if _, err := f.repo.FanoutItems(ctx, subs[0], []rules.Rule{r1}, ruleItems, f.items, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := readWeight(); got != 2.5 {
		t.Fatalf("home_weight after first run = %v, want 2.5", got)
	}

	// A second, DIFFERENT matching rule (a rule edit in effect) must overwrite
	// the weight rather than leaving the first run's value frozen.
	r2 := weight("0.1")
	if _, err := f.repo.FanoutItems(ctx, subs[0], []rules.Rule{r2}, ruleItems, f.items, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := readWeight(); got != 0.1 {
		t.Errorf("home_weight after second run = %v, want 0.1 (recomputed, not frozen)", got)
	}
}

func TestFanoutItemsRejectsMismatchedLengths(t *testing.T) {
	f := seedFanout(t, 1)
	ctx := t.Context()
	subs, _ := f.repo.SubscribersOf(ctx, "src1")
	_, err := f.repo.FanoutItems(ctx, subs[0], nil,
		[]rules.Item{{Title: "a"}, {Title: "b"}}, f.items, time.Now())
	if err == nil {
		t.Error("FanoutItems accepted mismatched items/itemIDs lengths")
	}
}
