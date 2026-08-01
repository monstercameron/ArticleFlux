package store

import "testing"

// "Steer by rejection" (Cam, 2026-08-01; §18.7, migration 0031): a
// dismissal's topic_label must round-trip through ReplaceRecommendations and
// DismissRecommendation, and DismissedTopics must count only labelled,
// dismissed rows — the fact internal/recommend's topicPenalty depends on.
func TestDismissedTopicsCountsOnlyLabelledDismissals(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := t.Context()

	if err := repo.ReplaceRecommendations(ctx, sc, []StoredRecommendation{
		{Domain: "npu-one.example", Score: 3, Rung: 4, Evidence: "[]", TopicLabel: "NPU Inference"},
		{Domain: "npu-two.example", Score: 2.5, Rung: 4, Evidence: "[]", TopicLabel: "NPU Inference"},
		{Domain: "cooking.example", Score: 3, Rung: 4, Evidence: "[]", TopicLabel: "Cooking"},
		// Rungs 1-2 typically carry no topic label at all (0031's own doc
		// comment) — this row is what that looks like, and it must not be
		// counted under an empty-string key once dismissed.
		{Domain: "outlink-only.example", Score: 4, Rung: 1, Evidence: "[]"},
	}); err != nil {
		t.Fatal(err)
	}

	for _, d := range []string{"npu-one.example", "npu-two.example", "outlink-only.example"} {
		if err := repo.DismissRecommendation(ctx, sc, d); err != nil {
			t.Fatal(err)
		}
	}
	// cooking.example is left as 'new' — an undismissed topic must not appear.

	got, err := repo.DismissedTopics(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if got["NPU Inference"] != 2 {
		t.Errorf(`DismissedTopics()["NPU Inference"] = %d, want 2`, got["NPU Inference"])
	}
	if _, ok := got["Cooking"]; ok {
		t.Errorf("DismissedTopics() contains %q, want it absent — it was never dismissed", "Cooking")
	}
	if _, ok := got[""]; ok {
		t.Error(`DismissedTopics() contains the empty-string key — an unlabelled dismissal ` +
			"was counted as a topic, which is exactly what the migration's WHERE clause exists to prevent")
	}
}

// A recommendation's topic_label must survive the read paths that hand it
// back to the caller, not just the write — Recommendations() feeds the
// Discover list and RecommendationByDomain feeds Accept, and either one
// silently dropping the label would make it invisible without failing loudly.
func TestTopicLabelRoundTripsThroughReadPaths(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := t.Context()

	if err := repo.ReplaceRecommendations(ctx, sc, []StoredRecommendation{
		{Domain: "labelled.example", FeedURL: "https://labelled.example/feed",
			Score: 3, Rung: 4, Evidence: "[]", TopicLabel: "Distributed Systems"},
	}); err != nil {
		t.Fatal(err)
	}

	list, err := repo.Recommendations(ctx, sc, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].TopicLabel != "Distributed Systems" {
		t.Errorf("Recommendations() = %+v, want one row with TopicLabel = %q", list, "Distributed Systems")
	}

	rec, ok, err := repo.RecommendationByDomain(ctx, sc, "labelled.example")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rec.TopicLabel != "Distributed Systems" {
		t.Errorf("RecommendationByDomain() = %+v, ok=%v, want TopicLabel = %q", rec, ok, "Distributed Systems")
	}
}
