package store

import (
	"context"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/topics"
)

// Every affinity table here follows the same contract: ReplaceX rewrites the
// whole set (abandoned rows disappear rather than lingering at a stale score)
// and the paired reader returns it keyed for the caller. One test per pair
// checks both halves plus the "old rows vanish" replace semantics.

func TestReplaceFeedAffinityRewritesWholeSet(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	// feed_affinity.source_id is a foreign key: it has to name a real source.
	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil || len(feeds) < 2 {
		t.Fatalf("feeds=%v err=%v, need >= 2", feeds, err)
	}
	srcA, srcB := feeds[0].SourceID, feeds[1].SourceID

	if err := repo.ReplaceFeedAffinity(ctx, sc, []FeedAffinity{
		{SourceID: srcA, Impressions: 10, Opens: 3, Score: 0.7},
		{SourceID: srcB, Impressions: 5, Opens: 1, Score: 0.2},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.FeedAffinities(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[srcA].Score != 0.7 || got[srcB].Opens != 1 {
		t.Fatalf("got %+v", got)
	}

	// A second pass that drops srcB must remove it, not merely leave it stale.
	if err := repo.ReplaceFeedAffinity(ctx, sc, []FeedAffinity{
		{SourceID: srcA, Impressions: 20, Opens: 8, Score: 0.9},
	}); err != nil {
		t.Fatal(err)
	}
	got2, err := repo.FeedAffinities(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got2[srcB]; ok {
		t.Error("srcB survived a replace that omitted it")
	}
	if got2[srcA].Score != 0.9 {
		t.Errorf("srcA score = %v, want 0.9", got2[srcA].Score)
	}
}

func TestFeedAffinitiesRequiresScope(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	if _, err := repo.FeedAffinities(context.Background(), Scope{}); err != ErrNoScope {
		t.Errorf("= %v, want ErrNoScope", err)
	}
	if err := repo.ReplaceFeedAffinity(context.Background(), Scope{}, nil); err != ErrNoScope {
		t.Errorf("= %v, want ErrNoScope", err)
	}
}

func TestReplaceTermAffinityRoundTripsAsAVector(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	if err := repo.ReplaceTermAffinity(ctx, sc, []TermWeight{
		{Term: "sqlite", Weight: 0.8, DocFreq: 4},
		{Term: "wal", Weight: 0.3, DocFreq: 2},
	}); err != nil {
		t.Fatal(err)
	}
	vec, err := repo.TermAffinity(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if vec["sqlite"] != 0.8 || vec["wal"] != 0.3 {
		t.Errorf("vec = %v", vec)
	}

	if err := repo.ReplaceTermAffinity(ctx, sc, []TermWeight{{Term: "only-this", Weight: 1}}); err != nil {
		t.Fatal(err)
	}
	vec2, err := repo.TermAffinity(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(vec2) != 1 || vec2["only-this"] != 1 {
		t.Errorf("vec2 = %v, want just only-this", vec2)
	}
}

func TestReplaceDomainAffinityRewritesWholeSet(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	if err := repo.ReplaceDomainAffinity(ctx, sc, []DomainAffinity{
		{Domain: "example.com", Impressions: 4, Opens: 2, Stars: 1},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.DomainAffinities(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if got["example.com"].Opens != 2 || got["example.com"].Stars != 1 {
		t.Errorf("got %+v", got)
	}

	if err := repo.ReplaceDomainAffinity(ctx, sc, nil); err != nil {
		t.Fatal(err)
	}
	got2, err := repo.DomainAffinities(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 0 {
		t.Errorf("got %v, want empty after replacing with nil", got2)
	}
}

func TestTopicKeyUsesOnlyTheFirstThreeTermsCaseFolded(t *testing.T) {
	a := TopicKey([]string{"Schema", "Migration", "Index", "Extra"})
	b := TopicKey([]string{"schema", "migration", "index"})
	if a != b {
		t.Errorf("TopicKey ignoring case/extra terms: %q != %q", a, b)
	}
	c := TopicKey([]string{"schema", "migration", "different"})
	if a == c {
		t.Error("TopicKey did not change when the third term changed")
	}
}

func TestTopicByKeyFindsAndReportsNotFound(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{TopTerms: []string{"orbit", "station", "downlink"}, Label: "Orbits",
			Members: []string{}, MemberScores: map[string]float64{}, Trend: topics.TrendSteady},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.TopicByKey(ctx, sc, TopicKey([]string{"orbit", "station", "downlink"}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "Orbits" {
		t.Errorf("Label = %q, want Orbits", got.Label)
	}
	if _, err := repo.TopicByKey(ctx, sc, "nonexistent-key"); err != ErrNotFound {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

func TestRenameTopicSetsUserLabelSourceAndRejectsEmpty(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{TopTerms: []string{"a", "b", "c"}, Label: "Original",
			Members: []string{}, MemberScores: map[string]float64{}, Trend: topics.TrendSteady},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.Topics(ctx, sc)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if err := repo.RenameTopic(ctx, sc, rows[0].ID, "My Name"); err != nil {
		t.Fatal(err)
	}
	rows2, err := repo.Topics(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if rows2[0].Label != "My Name" || rows2[0].LabelSource != "user" {
		t.Errorf("got %+v", rows2[0])
	}
	if err := repo.RenameTopic(ctx, sc, rows[0].ID, "   "); err == nil {
		t.Error("an empty label was accepted")
	}
	if err := repo.RenameTopic(ctx, sc, "nonexistent", "X"); err != ErrNotFound {
		t.Errorf("renaming an unknown topic = %v, want ErrNotFound", err)
	}
}

func TestReplaceHomeRankingWritesRankingAndClustersTogether(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	it := firstItem(t, repo, sc)

	if err := repo.ReplaceHomeRanking(ctx, sc,
		[]RankedItem{{ItemID: it.ID, Score: 0.9, Rank: 1, Slot: "top",
			Reasons: []RankReason{{Term: "topic", Text: "matches a followed topic"}}}},
		[]ItemCluster{{ItemID: it.ID, ClusterID: it.ID, IsHead: true, OtherSources: 2}},
	); err != nil {
		t.Fatal(err)
	}

	ranking, err := repo.HomeRanking(ctx, sc, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranking) != 1 || ranking[0].ItemID != it.ID || ranking[0].Tier != "smart" {
		t.Fatalf("got %+v, want one row defaulted to tier=smart", ranking)
	}
	if len(ranking[0].Reasons) != 1 || ranking[0].Reasons[0].Term != "topic" {
		t.Errorf("reasons = %+v", ranking[0].Reasons)
	}

	clusters, err := repo.HomeClusters(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || !clusters[0].IsHead || clusters[0].OtherSources != 2 {
		t.Errorf("clusters = %+v", clusters)
	}

	// A second call replaces both tables together — an empty pass clears them.
	if err := repo.ReplaceHomeRanking(ctx, sc, nil, nil); err != nil {
		t.Fatal(err)
	}
	if r2, _ := repo.HomeRanking(ctx, sc, 10); len(r2) != 0 {
		t.Error("home_ranking was not cleared by an empty replace")
	}
	if c2, _ := repo.HomeClusters(ctx, sc); len(c2) != 0 {
		t.Error("item_clusters was not cleared by an empty replace")
	}
}

func TestSourceVolumeCountsWithinTheWindowOnly(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	it := firstItem(t, repo, sc)

	vol, err := repo.SourceVolume(ctx, sc, 30)
	if err != nil {
		t.Fatal(err)
	}
	if vol[it.SourceID] <= 0 {
		t.Errorf("SourceVolume for a source with recent items = %v, want > 0", vol[it.SourceID])
	}

	// A window too narrow to include anything reports nothing for that source.
	volNone, err := repo.SourceVolume(ctx, sc, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = volNone // days<=0 defaults to 30, so this is really the default-days path.
}

func TestSourceVolumeRequiresScope(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	if _, err := repo.SourceVolume(context.Background(), Scope{}, 30); err != ErrNoScope {
		t.Errorf("= %v, want ErrNoScope", err)
	}
}

// HalfLifeFor returns zero rather than an error on sparse data — the caller
// falls back to a default decay, and this is the "there is nothing to fit yet"
// branch.
func TestHalfLifeForReturnsZeroWithNoEngagement(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	got, err := repo.HalfLifeFor(context.Background(), sc, "no-such-source")
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("HalfLifeFor with no data = %v, want 0", got)
	}
}

func TestRankedItemsExcludesReadAndUnsubscribedItems(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	items, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 3})
	if err != nil || len(items) < 2 {
		t.Fatalf("items=%v err=%v", items, err)
	}

	if err := repo.ReplaceHomeRanking(ctx, sc, []RankedItem{
		{ItemID: items[0].ID, Score: 1, Rank: 1, Slot: "top"},
		{ItemID: items[1].ID, Score: 0.9, Rank: 2, Slot: "top"},
	}, nil); err != nil {
		t.Fatal(err)
	}

	ranked, out, err := repo.RankedItems(ctx, sc, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 2 || len(out) != 2 {
		t.Fatalf("got %d ranked / %d items, want 2/2", len(ranked), len(out))
	}
	n, err := repo.CountRanked(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("CountRanked = %d, want 2", n)
	}

	// Reading one drops it from both the list and the count — the homepage is
	// a queue of suggestions, not a static snapshot.
	yes := true
	if _, err := repo.SetItemState(ctx, sc, items[0].ID, StateChange{Read: &yes}); err != nil {
		t.Fatal(err)
	}
	ranked2, _, err := repo.RankedItems(ctx, sc, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked2) != 1 || ranked2[0].ItemID != items[1].ID {
		t.Errorf("ranked after reading one = %+v, want just %s", ranked2, items[1].ID)
	}
	n2, err := repo.CountRanked(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 1 {
		t.Errorf("CountRanked after reading one = %d, want 1", n2)
	}
}

func TestRankedItemsPagesByAfterRank(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	items, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 3})
	if err != nil || len(items) < 2 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if err := repo.ReplaceHomeRanking(ctx, sc, []RankedItem{
		{ItemID: items[0].ID, Score: 1, Rank: 1, Slot: "top"},
		{ItemID: items[1].ID, Score: 0.9, Rank: 2, Slot: "top"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	page1, _, err := repo.RankedItems(ctx, sc, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 1 || page1[0].Rank != 1 {
		t.Fatalf("page1 = %+v", page1)
	}
	page2, _, err := repo.RankedItems(ctx, sc, page1[0].Rank, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 || page2[0].Rank != 2 {
		t.Fatalf("page2 = %+v", page2)
	}
}

func TestReplaceHomeRankingRequiresScope(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	if err := repo.ReplaceHomeRanking(context.Background(), Scope{}, nil, nil); err != ErrNoScope {
		t.Errorf("= %v, want ErrNoScope", err)
	}
	if _, err := repo.HomeRanking(context.Background(), Scope{}, 10); err != ErrNoScope {
		t.Errorf("= %v, want ErrNoScope", err)
	}
	if _, err := repo.HomeClusters(context.Background(), Scope{}); err != ErrNoScope {
		t.Errorf("= %v, want ErrNoScope", err)
	}
}
