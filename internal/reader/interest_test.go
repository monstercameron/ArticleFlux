package reader

import (
	"context"
	"fmt"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/topics"
)

// InterestProfile assembles four independent store reads into one answer, and
// the thing worth proving is that they compose correctly — the factor
// histogram counts each PICK once even when a reason repeats, ColdStart
// tracks whether any pick actually cited a topic, and the two "how many
// exist" counters (TopicCount/FactorBase vs the capped slices) stay honest.
//
// Seeded directly through the store's Replace* writers rather than through a
// real derivation: internal/derive is deliberately not a reader dependency
// (interest.go's own package comment), so this is the same seam the real
// deriver writes through.
func TestInterestProfileComposesTopicsEntitiesFeedsAndFactors(t *testing.T) {
	svc, repo, sc, sourceID, items := seedThreeItems(t)
	ctx := context.Background()

	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{
			Label: "Testing", TopTerms: []string{"test", "case", "cover"},
			Members: []string{items[0].ID, items[1].ID},
			MemberScores: map[string]float64{
				items[0].ID: 0.9, items[1].ID: 0.7,
			},
			Trend: topics.TrendRising,
		},
	}); err != nil {
		t.Fatalf("ReplaceTopics: %v", err)
	}
	topicRows, err := repo.Topics(ctx, sc)
	if err != nil {
		t.Fatalf("Topics: %v", err)
	}
	if len(topicRows) != 1 {
		t.Fatalf("seeded 1 topic, store has %d", len(topicRows))
	}
	topicID := topicRows[0].ID

	if err := repo.ReplaceEntityAffinity(ctx, sc, []store.Entity{
		{Name: "widget co", Label: "Widget Co", Kind: store.EntityPhrase, Weight: 5, Mentions: 3},
	}); err != nil {
		t.Fatalf("ReplaceEntityAffinity: %v", err)
	}
	if err := repo.ReplaceFeedAffinity(ctx, sc, []store.FeedAffinity{
		{SourceID: sourceID, Opens: 4, Impressions: 10, Score: 1.5, VolumePerDay: 0.4},
	}); err != nil {
		t.Fatalf("ReplaceFeedAffinity: %v", err)
	}

	// Two ranked picks: one citing the topic (twice, over two reasons with the
	// SAME term — that must count as one item for that term, not two), one
	// citing only a feed reason and no topic at all.
	if err := repo.ReplaceHomeRanking(ctx, sc, []store.RankedItem{
		{
			ItemID: items[0].ID, Score: 3, Rank: 1, Slot: "top", TopicID: topicID,
			Reasons: []store.RankReason{{Term: "topic", Text: "a"}, {Term: "topic", Text: "b"}},
		},
		{
			ItemID: items[1].ID, Score: 2, Rank: 2, Slot: "top",
			Reasons: []store.RankReason{{Term: "feed", Text: "c"}},
		},
	}, nil); err != nil {
		t.Fatalf("ReplaceHomeRanking: %v", err)
	}

	p, err := svc.InterestProfile(ctx, sc)
	if err != nil {
		t.Fatalf("InterestProfile: %v", err)
	}

	if p.TopicCount != 1 || len(p.Topics) != 1 {
		t.Errorf("TopicCount=%d len(Topics)=%d, want 1 and 1", p.TopicCount, len(p.Topics))
	}
	if p.EntityCount != 1 || len(p.Entities) != 1 {
		t.Errorf("EntityCount=%d len(Entities)=%d, want 1 and 1", p.EntityCount, len(p.Entities))
	}
	if p.FeedCount != 1 || len(p.Feeds) != 1 {
		t.Fatalf("FeedCount=%d len(Feeds)=%d, want 1 and 1", p.FeedCount, len(p.Feeds))
	}
	if p.Feeds[0].SourceID != sourceID || !p.Feeds[0].OnMyFeed {
		t.Errorf("Feeds[0] = %+v, want source %q and OnMyFeed true", p.Feeds[0], sourceID)
	}

	if p.Ranked != 2 {
		t.Errorf("Ranked = %d, want 2", p.Ranked)
	}
	if p.FactorBase != 2 {
		t.Errorf("FactorBase = %d, want 2", p.FactorBase)
	}
	if p.ColdStart {
		t.Error("ColdStart = true, but one ranked pick cited a topic")
	}

	var topicFactor, feedFactor *ProfileFactor
	for i := range p.Factors {
		switch p.Factors[i].Term {
		case "topic":
			topicFactor = &p.Factors[i]
		case "feed":
			feedFactor = &p.Factors[i]
		}
	}
	if topicFactor == nil || topicFactor.Items != 1 {
		t.Errorf("topic factor = %+v, want Items=1 (deduped per item, not per reason)", topicFactor)
	}
	if feedFactor == nil || feedFactor.Items != 1 {
		t.Errorf("feed factor = %+v, want Items=1", feedFactor)
	}
}

// ColdStart is the "still learning" signal, and it is a rule about the
// ranked page specifically — not about how many topics exist. A page whose
// picks landed entirely on feed/freshness reasons, with a topic sitting
// unused in the sidebar, must still read as cold start.
func TestInterestProfileColdStartWhenNoRankedPickCitesATopic(t *testing.T) {
	svc, repo, sc, _, items := seedThreeItems(t)
	ctx := context.Background()

	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{Label: "Unused", TopTerms: []string{"a", "b", "c"}, Members: []string{items[0].ID},
			Trend: topics.TrendSteady},
	}); err != nil {
		t.Fatalf("ReplaceTopics: %v", err)
	}
	if err := repo.ReplaceHomeRanking(ctx, sc, []store.RankedItem{
		{ItemID: items[0].ID, Score: 1, Rank: 1, Slot: "top", Reasons: []store.RankReason{{Term: "fresh"}}},
	}, nil); err != nil {
		t.Fatalf("ReplaceHomeRanking: %v", err)
	}

	p, err := svc.InterestProfile(ctx, sc)
	if err != nil {
		t.Fatalf("InterestProfile: %v", err)
	}
	if !p.ColdStart {
		t.Error("ColdStart = false, want true — no ranked pick cited a topic")
	}
}

// With nothing derived at all, InterestProfile must return a usable empty
// answer rather than erroring — a reader who has just subscribed opens this
// screen before any derivation has run.
func TestInterestProfileEmptyBeforeAnyDerivation(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	p, err := svc.InterestProfile(context.Background(), sc)
	if err != nil {
		t.Fatalf("InterestProfile: %v", err)
	}
	if p.TopicCount != 0 || p.EntityCount != 0 || p.FeedCount != 0 || p.Ranked != 0 {
		t.Errorf("expected an all-zero profile, got %+v", p)
	}
	if !p.ColdStart {
		t.Error("ColdStart = false with nothing derived, want true")
	}
}

// profileFeeds caps the list at MaxProfileFeeds but reports the TRUE total —
// the number the summary line means by "competing" (profileFeeds's own
// comment) — and it skips an affinity row for a feed this reader has since
// unsubscribed rather than naming it by a bare id.
func TestProfileFeedsCapsButCountsTheTrueTotal(t *testing.T) {
	svc, repo, sc, firstSource, _ := seedThreeItems(t)
	ctx := context.Background()

	const extra = MaxProfileFeeds + 2 // one more subscribed feed than the cap allows, plus one unsubscribed
	affinities := []store.FeedAffinity{{SourceID: firstSource, Score: 1}}
	var lastSubscribed string
	for i := 0; i < extra; i++ {
		f, _, err := svc.SubscribeOnly(ctx, sc,
			fmt.Sprintf("https://feed-%d.example/rss", i), fmt.Sprintf("Feed %d", i), "", "")
		if err != nil {
			t.Fatalf("SubscribeOnly(%d): %v", i, err)
		}
		affinities = append(affinities, store.FeedAffinity{SourceID: f.SourceID, Score: float64(i + 2)})
		lastSubscribed = f.SourceID
	}
	// One affinity row for a source this reader has since unsubscribed from —
	// A22 keeps the source row itself, so the row is real (satisfies the FK)
	// but must still be skipped, not shown under a bare id.
	ghost, _, err := svc.SubscribeOnly(ctx, sc, "https://ghost.example/rss", "Ghost", "", "")
	if err != nil {
		t.Fatalf("SubscribeOnly(ghost): %v", err)
	}
	if err := svc.Unsubscribe(ctx, sc, ghost.SourceID); err != nil {
		t.Fatalf("Unsubscribe(ghost): %v", err)
	}
	affinities = append(affinities, store.FeedAffinity{SourceID: ghost.SourceID, Score: 999})

	if err := repo.ReplaceFeedAffinity(ctx, sc, affinities); err != nil {
		t.Fatalf("ReplaceFeedAffinity: %v", err)
	}

	p, err := svc.InterestProfile(ctx, sc)
	if err != nil {
		t.Fatalf("InterestProfile: %v", err)
	}
	wantTotal := 1 + extra // firstSource plus every SubscribeOnly'd feed; the ghost is excluded
	if p.FeedCount != wantTotal {
		t.Errorf("FeedCount = %d, want %d (the ghost source must not be counted)", p.FeedCount, wantTotal)
	}
	if len(p.Feeds) != MaxProfileFeeds {
		t.Fatalf("len(Feeds) = %d, want the cap of %d", len(p.Feeds), MaxProfileFeeds)
	}
	// Sorted by score descending, so the highest-scoring subscribed feed —
	// the last one added, at extra+1 — must lead.
	if p.Feeds[0].SourceID != lastSubscribed {
		t.Errorf("top feed = %q, want the highest-scoring one %q", p.Feeds[0].SourceID, lastSubscribed)
	}
	for _, f := range p.Feeds {
		if f.SourceID == ghost.SourceID {
			t.Error("an unsubscribed source's affinity row was shown")
		}
	}
}

// citesATopic is the primitive ColdStart is built on: true the moment any
// row carries a non-empty TopicID, false for an empty ranking or one whose
// picks are all topic-less.
func TestCitesATopic(t *testing.T) {
	cases := []struct {
		name   string
		ranked []store.RankedItem
		want   bool
	}{
		{"empty", nil, false},
		{"none cite", []store.RankedItem{{ItemID: "a"}, {ItemID: "b"}}, false},
		{"one cites", []store.RankedItem{{ItemID: "a"}, {ItemID: "b", TopicID: "t1"}}, true},
	}
	for _, c := range cases {
		if got := citesATopic(c.ranked); got != c.want {
			t.Errorf("%s: citesATopic = %v, want %v", c.name, got, c.want)
		}
	}
}

// SteerLevel.weights is a pure mapping and the one place "never" gets its
// special case: the multiplier resets to normal rather than remembering
// whatever it was before, so undoing a "never" does not surprise the reader
// with a stale weight (interest.go's own comment).
func TestSteerLevelWeights(t *testing.T) {
	cases := []struct {
		level          SteerLevel
		wantSteer      float64
		wantSuppressed bool
		wantOK         bool
	}{
		{SteerMore, store.SteerMore, false, true},
		{SteerNormal, store.SteerNormal, false, true},
		{SteerLess, store.SteerLess, false, true},
		{SteerNever, store.SteerNormal, true, true},
		{SteerLevel("sideways"), 0, false, false},
		{SteerLevel(""), 0, false, false},
	}
	for _, c := range cases {
		steer, suppressed, ok := c.level.weights()
		if steer != c.wantSteer || suppressed != c.wantSuppressed || ok != c.wantOK {
			t.Errorf("weights(%q) = (%v,%v,%v), want (%v,%v,%v)",
				c.level, steer, suppressed, ok, c.wantSteer, c.wantSuppressed, c.wantOK)
		}
	}
}

// SteerTopic resolves its target by fingerprint, not by a stored id, so it
// keeps working across a rebuild that reassigned ids — the whole reason for
// TopicKey's existence (interest.go's long comment on SteerTopic).
func TestSteerTopicByKeyAndRebuildHook(t *testing.T) {
	svc, repo, sc, _, items := seedThreeItems(t)
	ctx := context.Background()
	terms := []string{"alpha", "beta", "gamma"}
	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{Label: "Greek", TopTerms: terms, Members: []string{items[0].ID}, Trend: topics.TrendSteady},
	}); err != nil {
		t.Fatalf("ReplaceTopics: %v", err)
	}
	key := store.TopicKey(terms)

	var fired []store.Scope
	svc.WithRankPrefHook(func(s store.Scope) { fired = append(fired, s) })

	rebuilt, err := svc.SteerTopic(ctx, sc, key, SteerLess)
	if err != nil {
		t.Fatalf("SteerTopic: %v", err)
	}
	if !rebuilt {
		t.Error("SteerTopic reported no rebuild requested, want true with a hook installed")
	}
	if len(fired) != 1 {
		t.Fatalf("hook fired %d times, want 1", len(fired))
	}

	rows, err := repo.Topics(ctx, sc)
	if err != nil {
		t.Fatalf("Topics: %v", err)
	}
	if rows[0].Steer != store.SteerLess {
		t.Errorf("stored steer = %v, want SteerLess (%v)", rows[0].Steer, store.SteerLess)
	}

	// "Never" resets the multiplier AND suppresses.
	if _, err := svc.SteerTopic(ctx, sc, key, SteerNever); err != nil {
		t.Fatalf("SteerTopic(never): %v", err)
	}
	rows, err = repo.Topics(ctx, sc)
	if err != nil {
		t.Fatalf("Topics: %v", err)
	}
	if !rows[0].Suppressed {
		t.Error("topic not suppressed after SteerNever")
	}
	if rows[0].Steer != store.SteerNormal {
		t.Errorf("steer after SteerNever = %v, want it reset to SteerNormal", rows[0].Steer)
	}
}

func TestSteerTopicRejectsAnUnknownLevelOrEmptyKey(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	ctx := context.Background()
	if _, err := svc.SteerTopic(ctx, sc, "some-key", SteerLevel("bogus")); err == nil {
		t.Error("an unknown steer level was accepted")
	}
	if _, err := svc.SteerTopic(ctx, sc, "   ", SteerMore); err == nil {
		t.Error("an empty (whitespace) topic key was accepted")
	}
}

// A key that no longer matches any cluster — the rebuild-raced-the-press
// case the long comment on SteerTopic describes — must fail rather than
// silently steering nothing.
func TestSteerTopicOnAVanishedKeyFails(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	if _, err := svc.SteerTopic(context.Background(), sc, "no-such-fingerprint", SteerMore); err == nil {
		t.Error("SteerTopic on an unknown fingerprint returned no error")
	}
}

// SteerEntity normalises case and whitespace the same way the derivation
// stores it, so a client that echoes back the display label instead of the
// key still addresses the right row.
func TestSteerEntityNormalisesNameAndFiresTheHook(t *testing.T) {
	svc, repo, sc, _, _ := seedThreeItems(t)
	ctx := context.Background()
	if err := repo.ReplaceEntityAffinity(ctx, sc, []store.Entity{
		{Name: "pro max", Label: "Pro Max", Kind: store.EntityPhrase, Weight: 37, Mentions: 2},
	}); err != nil {
		t.Fatalf("ReplaceEntityAffinity: %v", err)
	}

	n := 0
	svc.WithRankPrefHook(func(store.Scope) { n++ })
	rebuilt, err := svc.SteerEntity(ctx, sc, "  Pro Max  ", SteerNever)
	if err != nil {
		t.Fatalf("SteerEntity: %v", err)
	}
	if !rebuilt || n != 1 {
		t.Errorf("rebuilt=%v hookCalls=%d, want true and 1", rebuilt, n)
	}

	ents, err := repo.AllEntities(ctx, sc, 10)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(ents) != 1 || !ents[0].Suppressed {
		t.Fatalf("entities = %+v, want the one row suppressed", ents)
	}
}

func TestSteerEntityRejectsAnUnknownLevelOrEmptyName(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	ctx := context.Background()
	if _, err := svc.SteerEntity(ctx, sc, "widget", SteerLevel("bogus")); err == nil {
		t.Error("an unknown steer level was accepted")
	}
	if _, err := svc.SteerEntity(ctx, sc, "   ", SteerMore); err == nil {
		t.Error("an empty (whitespace) entity name was accepted")
	}
}

// rebuild's own contract: it reports whether anything is listening, and a
// service with no hook installed — every test above this file, and every
// deployment before the deriver was wired — still works, it just does not
// schedule anything.
func TestRebuildReportsFalseWithNoHookInstalled(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	if svc.rebuild(sc) {
		t.Error("rebuild() = true with no hook installed")
	}
}
