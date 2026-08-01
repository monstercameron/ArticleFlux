package llm

import "testing"

// TestWebSearchPayloadCarriesOnlyWhatIsPermitted is the §18.8 enforcement
// test for rung 5 (Cam, 2026-08-01), mirroring
// TestRelevancePayloadCarriesOnlyWhatIsPermitted: audit the assembled body,
// not the template, and prove it fails the OTHER boundaries' audits too —
// every allowlist in this package must stay genuinely separate.
func TestWebSearchPayloadCarriesOnlyWhatIsPermitted(t *testing.T) {
	p := WebSearchPayload{
		Topic:            "distributed systems, database internals",
		PositiveExamples: []string{"Consensus without a coordinator"},
		NegativeExamples: []string{"10 Racing Facts You Won't Believe"},
	}

	bad, err := AuditWebSearch(mustJSON(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("the web-search payload carried keys not on WebSearchKeys: %v", bad)
	}

	// Not cross-checked against AuditRelevance: RelevanceKeys legitimately
	// permits "topic" too (RelevancePayload has its own Topic field), so a
	// topic-only body trivially satisfies it — that overlap is correct, not
	// a sign the boundaries have merged. ClassifyKeys has no "topic" entry
	// at all, so that audit is the one that actually proves this payload's
	// shape is foreign to a different boundary.
	bad, err = AuditClassify(mustJSON(t, p))
	if err == nil && len(bad) == 0 {
		t.Fatal("a web-search payload passed the classify audit — the allowlists are not distinct")
	}
}

// WebSearchKeys must not admit anything on ForbiddenKeys, same as every
// other list in the package.
func TestWebSearchKeysAdmitsNoForbiddenKey(t *testing.T) {
	for k := range WebSearchKeys {
		if ForbiddenKeys[k] {
			t.Errorf("WebSearchKeys admits %q, which is on ForbiddenKeys", k)
		}
	}
}

// WebSearchKeys is exactly {topic, positive_examples, negative_examples} —
// rung 5 sends topic terms plus taste-calibration titles (Cam, 2026-08-01)
// and nothing else (§18.8). A test that just checks "not forbidden" would
// pass a WebSearchKeys that grew a fourth, unreviewed field; this pins the
// whole set.
func TestWebSearchKeysIsExactlyTopicAndExamples(t *testing.T) {
	want := map[string]bool{"topic": true, "positive_examples": true, "negative_examples": true}
	if len(WebSearchKeys) != len(want) {
		t.Fatalf("WebSearchKeys = %v, want exactly %v", WebSearchKeys, want)
	}
	for k := range want {
		if !WebSearchKeys[k] {
			t.Errorf("WebSearchKeys is missing %q", k)
		}
	}
}

func TestWebSearchTrimCapsTheExamples(t *testing.T) {
	over := make([]string, MaxWebSearchExamples+3)
	for i := range over {
		over[i] = "title"
	}
	p := WebSearchPayload{Topic: "topic", PositiveExamples: over, NegativeExamples: over[:MaxWebSearchExamples+1]}

	trimmed, rep := p.Trim()
	if len(trimmed.PositiveExamples) != MaxWebSearchExamples {
		t.Errorf("trimmed.PositiveExamples has %d entries, want %d", len(trimmed.PositiveExamples), MaxWebSearchExamples)
	}
	if len(trimmed.NegativeExamples) != MaxWebSearchExamples {
		t.Errorf("trimmed.NegativeExamples has %d entries, want %d", len(trimmed.NegativeExamples), MaxWebSearchExamples)
	}
	if rep.PositiveDropped != 3 {
		t.Errorf("rep.PositiveDropped = %d, want 3", rep.PositiveDropped)
	}
	if rep.NegativeDropped != 1 {
		t.Errorf("rep.NegativeDropped = %d, want 1", rep.NegativeDropped)
	}
	if rep.Empty() {
		t.Error("rep.Empty() = true, but examples were trimmed")
	}
}

func TestWebSearchTrimCapsTheTopic(t *testing.T) {
	long := ""
	for range 400 {
		long += "x"
	}
	p := WebSearchPayload{Topic: long}

	trimmed, rep := p.Trim()
	if len([]rune(trimmed.Topic)) != MaxWebSearchTopicRunes {
		t.Errorf("trimmed.Topic has %d runes, want %d", len([]rune(trimmed.Topic)), MaxWebSearchTopicRunes)
	}
	if !rep.TopicTruncated {
		t.Error("rep.TopicTruncated = false, want true")
	}
	if rep.Empty() {
		t.Error("rep.Empty() = true, but the topic was truncated")
	}
}

func TestWebSearchTrimNoopWhenWithinCap(t *testing.T) {
	p := WebSearchPayload{Topic: "short topic"}
	_, rep := p.Trim()
	if !rep.Empty() {
		t.Errorf("rep = %+v, want Empty() for a topic within the cap", rep)
	}
}

// The schema's own maxItems is the real enforcement of MaxWebSearchCandidates
// — WebSearchInstructions only hints at the model. This pins the schema
// value so the two cannot silently drift apart (see WebSearchInstructions's
// own doc comment on why the number is spelled out rather than interpolated).
func TestWebSearchSchemaCapsCandidateCount(t *testing.T) {
	props, ok := WebSearchSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("WebSearchSchema.properties is not a map")
	}
	candidates, ok := props["candidates"].(map[string]any)
	if !ok {
		t.Fatal("WebSearchSchema.properties.candidates is not a map")
	}
	max, ok := candidates["maxItems"].(int)
	if !ok || max != MaxWebSearchCandidates {
		t.Errorf("candidates.maxItems = %v, want %d", candidates["maxItems"], MaxWebSearchCandidates)
	}
}
