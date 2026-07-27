package derive

import (
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/textvec"
)

// corpusOf builds an IDF corpus over the same texts the items carry, which is what
// the real caller does — TFIDF against a corpus that never saw the documents gives
// every term the unseen-term weight and the similarities mean nothing.
func corpusOf(texts ...string) *textvec.Corpus {
	c := textvec.NewCorpus()
	for _, t := range texts {
		c.Add(t)
	}
	return c
}

func item(id, sourceID, title, summary string, published time.Time) store.Item {
	return store.Item{
		ID: id, SourceID: sourceID, Title: title, Summary: summary,
		PublishedAt: published.Format(time.RFC3339Nano),
	}
}

// The signal this whole function exists for: the same story from several sources.
//
// rank has weighted Corroboration since it was written and nothing ever set it, so
// "five of your feeds carried this" was unreachable prose. This asserts the count
// is real, counts OTHER sources rather than all of them, and survives the wording
// differences that make two outlets' versions of one announcement not identical.
func TestCorroborationCountsOtherSourcesCarryingTheSameStory(t *testing.T) {
	base := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)

	// One announcement, three outlets, three different headlines.
	a := "Qualcomm announces Snapdragon X2 Elite laptop processor"
	b := "Qualcomm unveils the Snapdragon X2 Elite processor for laptops"
	c := "Snapdragon X2 Elite: Qualcomm announces its new laptop processor"
	// An unrelated story that shares the field's vocabulary but not the story.
	d := "Framework laptop battery replacement guide and teardown"

	corpus := corpusOf(a, b, c, d)
	got := corroborate([]store.Item{
		item("i1", "s1", a, a, base),
		item("i2", "s2", b, b, base.Add(2*time.Hour)),
		item("i3", "s3", c, c, base.Add(3*time.Hour)),
		item("i4", "s4", d, d, base.Add(time.Hour)),
	}, corpus)

	// Every member of the story sees the other two sources.
	for _, id := range []string{"i1", "i2", "i3"} {
		if got[id].otherSources != 2 {
			t.Errorf("%s: otherSources = %d, want 2 — the corroboration signal is not being counted",
				id, got[id].otherSources)
		}
	}
	// The unrelated piece is corroborated by nobody, which is the half that proves
	// the threshold discriminates rather than grouping everything technical.
	if got["i4"].otherSources != 0 {
		t.Errorf("an unrelated story reported %d corroborating sources, want 0",
			got["i4"].otherSources)
	}
}

// The earliest member represents the story; the rest carry a duplicate penalty.
//
// Without this the megafeed's top slots are five rewrites of one announcement, which
// is the failure "one card per corroborated story" (§18.4) exists to prevent.
func TestTheEarliestMemberRepresentsTheStory(t *testing.T) {
	base := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	a := "Valkey 9 released with new cluster replication mode"
	b := "Valkey 9 is out, bringing a new cluster replication mode"

	corpus := corpusOf(a, b)
	// Deliberately passed newest-first, so a function that trusted input order
	// rather than publication time would pick the wrong representative.
	got := corroborate([]store.Item{
		item("late", "s2", b, b, base.Add(4*time.Hour)),
		item("first", "s1", a, a, base),
	}, corpus)

	if got["first"].duplicateOf != 0 {
		t.Errorf("the earliest member got a duplicate penalty of %v, want 0",
			got["first"].duplicateOf)
	}
	if got["late"].duplicateOf < SameStoryThreshold {
		t.Errorf("the later member's duplicateOf = %v, want >= %v so the dupe penalty applies",
			got["late"].duplicateOf, SameStoryThreshold)
	}
}

// One feed publishing twice about the same thing is a follow-up, not corroboration.
//
// Without the source check a single busy source manufactures its own supporting
// evidence, and "eleven sources carried this" would mean "one aggregator posted
// eleven times".
func TestOneSourceCannotCorroborateItself(t *testing.T) {
	base := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	a := "Linus Torvalds comments on the kernel scheduler rewrite"
	b := "Linus Torvalds comments again on the kernel scheduler rewrite"

	corpus := corpusOf(a, b)
	got := corroborate([]store.Item{
		item("i1", "same-source", a, a, base),
		item("i2", "same-source", b, b, base.Add(time.Hour)),
	}, corpus)

	if got["i1"].otherSources != 0 || got["i2"].otherSources != 0 {
		t.Errorf("a source corroborated itself: i1=%d i2=%d, want 0 and 0",
			got["i1"].otherSources, got["i2"].otherSources)
	}
	// And neither is suppressed as a duplicate of the other, because a follow-up
	// from a source you read is worth showing.
	if got["i2"].duplicateOf != 0 {
		t.Errorf("a same-source follow-up was penalised as a duplicate (%v)", got["i2"].duplicateOf)
	}
}

// Matching text outside the window is a recap, not the same story.
func TestTheStoryWindowBoundsCorroboration(t *testing.T) {
	base := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	a := "Nintendo Switch 2 launch date confirmed for June"
	b := "Nintendo Switch 2 launch date confirmed for June"

	corpus := corpusOf(a, b)
	got := corroborate([]store.Item{
		item("now", "s1", a, a, base),
		item("later", "s2", b, b, base.Add(SameStoryWindow+time.Hour)),
	}, corpus)

	if got["now"].otherSources != 0 || got["later"].otherSources != 0 {
		t.Errorf("identical text %v apart was treated as one story: %d and %d",
			SameStoryWindow+time.Hour, got["now"].otherSources, got["later"].otherSources)
	}
}

// Groups must not chain: A~B and B~C does not make A and C the same story.
//
// This is why the pass is pairwise against representatives rather than agglomerative.
// Transitive merging is how a "story" grows to forty loosely-related items, taking
// the whole page down with one duplicate penalty.
func TestStoriesDoNotChainTransitively(t *testing.T) {
	base := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	// Deliberately a chain: a shares with b, b shares with c, a shares little with c.
	a := "sqlite wal checkpoint fsync durability"
	b := "sqlite wal checkpoint btree page cache"
	c := "btree page cache vacuum index tuning"

	corpus := corpusOf(a, b, c)
	got := corroborate([]store.Item{
		item("a", "s1", a, a, base),
		item("b", "s2", b, b, base.Add(time.Hour)),
		item("c", "s3", c, c, base.Add(2*time.Hour)),
	}, corpus)

	// Whatever the thresholds decide about the individual pairs, no single story may
	// swallow all three — that is the runaway this structure prevents.
	if got["a"].otherSources >= 2 {
		t.Errorf("a story chained transitively: item a sees %d other sources",
			got["a"].otherSources)
	}
}

// Every kind named in deliberateKinds must actually exist in the registry.
//
// The switch this map replaced had cases for `starred` and `clicked_through`, neither
// of which is a kind — so two of its branches were dead and the click-through
// multiplier had never applied to anything. A typo in a string literal is invisible;
// this makes it a test failure.
func TestDeliberateKindsAreRealKinds(t *testing.T) {
	for k := range deliberateKinds {
		spec, ok := signals.Lookup(k)
		if !ok {
			t.Errorf("deliberateKinds names %q, which is not a signal kind", k)
			continue
		}
		if !spec.Affinity {
			t.Errorf("deliberateKinds names %q, which is excluded from affinity", k)
		}
	}
	// And the sign has to agree with the registry, or the re-rank contradicts the
	// taxonomy: a kind with a negative prior must not multiply the score up.
	for k, w := range deliberateKinds {
		spec, _ := signals.Lookup(k)
		if (spec.Prior < 0) != (w < 0) {
			t.Errorf("%q has prior %v but re-rank weight %v — the signs disagree",
				k, spec.Prior, w)
		}
	}
}
