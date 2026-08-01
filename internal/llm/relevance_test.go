package llm

import "testing"

// TestRelevancePayloadCarriesOnlyWhatIsPermitted is the §18.8 enforcement
// test for the "2 posts reviewed" gate (Cam, 2026-07-31), mirroring
// TestClassifyPayloadCarriesOnlyWhatIsPermitted: audit the assembled body,
// not the template, and prove it fails the OTHER boundary's audit too — the
// two allowlists must stay genuinely separate.
func TestRelevancePayloadCarriesOnlyWhatIsPermitted(t *testing.T) {
	p := RelevancePayload{
		Topic: "distributed systems, database internals",
		Samples: []RelevanceSample{
			{Title: "Consensus without a coordinator", Summary: "A walkthrough of leaderless replication."},
			{Title: "Why your WAL fsyncs matter", Summary: "Durability tradeoffs in embedded stores."},
		},
		PositiveExamples: []string{"Consensus without a coordinator"},
		NegativeExamples: []string{"10 Racing Facts You Won't Believe"},
	}

	bad, err := AuditRelevance(mustJSON(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("the relevance payload carried keys not on RelevanceKeys: %v", bad)
	}

	bad, err = AuditClassify(mustJSON(t, p))
	if err == nil && len(bad) == 0 {
		t.Fatal("a relevance payload passed the classify audit — the two allowlists are not distinct")
	}
}

// RelevanceKeys must not admit anything on ForbiddenKeys, same as every other
// list in the package (TestNoAllowlistAdmitsAForbiddenKey's companion).
func TestRelevanceKeysAdmitsNoForbiddenKey(t *testing.T) {
	for k := range RelevanceKeys {
		if ForbiddenKeys[k] {
			t.Errorf("RelevanceKeys admits %q, which is on ForbiddenKeys", k)
		}
	}
}

// The gate is "2 posts reviewed" — Trim enforces that literally, not just by
// convention at the call site.
func TestRelevanceTrimCapsSamplesAndSummaries(t *testing.T) {
	longSummary := ""
	for range 400 {
		longSummary += "word "
	}
	p := RelevancePayload{
		Topic: "topic",
		Samples: []RelevanceSample{
			{Title: "one", Summary: longSummary},
			{Title: "two", Summary: "short"},
			{Title: "three", Summary: "should be dropped entirely"},
		},
	}

	trimmed, rep := p.Trim()

	if len(trimmed.Samples) != MaxRelevanceSamples {
		t.Errorf("trimmed.Samples has %d entries, want %d", len(trimmed.Samples), MaxRelevanceSamples)
	}
	if rep.SamplesDropped != 1 {
		t.Errorf("rep.SamplesDropped = %d, want 1", rep.SamplesDropped)
	}
	if rep.SummaryWordsDropped == 0 {
		t.Error("rep.SummaryWordsDropped = 0, want the 400-word summary to have been capped")
	}
	if rep.Empty() {
		t.Error("rep.Empty() = true, but the payload was trimmed")
	}
}

func TestRelevanceTrimCapsTheExamples(t *testing.T) {
	over := make([]string, MaxRelevanceExamples+2)
	for i := range over {
		over[i] = "title"
	}
	p := RelevancePayload{Topic: "topic", PositiveExamples: over, NegativeExamples: over[:MaxRelevanceExamples+1]}

	trimmed, rep := p.Trim()
	if len(trimmed.PositiveExamples) != MaxRelevanceExamples {
		t.Errorf("trimmed.PositiveExamples has %d entries, want %d", len(trimmed.PositiveExamples), MaxRelevanceExamples)
	}
	if len(trimmed.NegativeExamples) != MaxRelevanceExamples {
		t.Errorf("trimmed.NegativeExamples has %d entries, want %d", len(trimmed.NegativeExamples), MaxRelevanceExamples)
	}
	if rep.PositiveDropped != 2 {
		t.Errorf("rep.PositiveDropped = %d, want 2", rep.PositiveDropped)
	}
	if rep.NegativeDropped != 1 {
		t.Errorf("rep.NegativeDropped = %d, want 1", rep.NegativeDropped)
	}
	if rep.Empty() {
		t.Error("rep.Empty() = true, but examples were trimmed")
	}
}

func TestRelevanceTrimNoopWhenWithinCaps(t *testing.T) {
	p := RelevancePayload{
		Topic:   "short topic",
		Samples: []RelevanceSample{{Title: "one", Summary: "short"}},
	}
	_, rep := p.Trim()
	if !rep.Empty() {
		t.Errorf("rep = %+v, want Empty() for a payload within every cap", rep)
	}
}
