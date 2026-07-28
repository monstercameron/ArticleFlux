package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPlanEgressCarriesNoBody is the §18.8/§27.4e-shaped enforcement test
// TODO 11.14 names. It checks both directions: a payload built through the
// type is clean, and a hand-built body carrying the forbidden shapes is
// caught by name.
func TestPlanEgressCarriesNoBody(t *testing.T) {
	p := PlanPayload{
		TargetMinutes: 20,
		Candidates: []PlanCandidate{
			{ID: 1, Cluster: 1, Title: "A zero-day patch ships", Category: "tech",
				Genre: "news", Abstract: "A vendor shipped an emergency fix.", AirtimeHint: "LEAD"},
			{ID: 2, Title: "Markets close mixed", Category: "markets"},
		},
	}
	bad, err := AuditPlan(mustJSON(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("a plan payload built through the type carried keys the allowlist does not permit: %v", bad)
	}

	// The path the types cannot protect: a hand-built body smuggling exactly
	// the shapes §18.8 forbids — the article's own text, a URL, and a
	// database id rather than a per-request ordinal.
	raw := []byte(`{"candidates":[{"id":1,"title":"t","url":"https://example.com/x",` +
		`"content_html":"<p>the whole article</p>","item_id":42,"feed_url":"https://feed.example/"}]}`)
	bad, err = AuditPlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, k := range bad {
		found[k] = true
	}
	for _, k := range []string{"url", "content_html", "item_id", "feed_url"} {
		if !found[k] {
			t.Errorf("AuditPlan did not flag %q: got %v", k, bad)
		}
	}
}

// TestPlanKeysAdmitNoForbiddenKey is the same statement classify_test.go
// makes for EgressKeys and ClassifyKeys, carried to the third list: an
// allowlist describes today's payload, and this checks the boundary itself
// against the enumerated never-list so a later amendment cannot quietly admit
// one of them.
func TestPlanKeysAdmitNoForbiddenKey(t *testing.T) {
	for k := range PlanKeys {
		if ForbiddenKeys[k] {
			t.Errorf("PlanKeys admits %q, which is on ForbiddenKeys", k)
		}
	}
}

// TestPlanCandidateIDIsAnOrdinalNotADatabaseID: the id that crosses the
// boundary is this request's ordinal, exactly as Candidate.ID is for the
// interest re-rank — never item_id, which would let a provider correlate a
// planner's picks with a later request.
func TestPlanCandidateIDIsAnOrdinalNotADatabaseID(t *testing.T) {
	body := mustJSON(t, PlanPayload{Candidates: []PlanCandidate{{ID: 7, Title: "x"}}})
	var decoded struct {
		Candidates []map[string]any `json:"candidates"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Candidates) != 1 {
		t.Fatalf("expected one candidate, got %d", len(decoded.Candidates))
	}
	if _, ok := decoded.Candidates[0]["id"]; !ok {
		t.Fatalf("a plan candidate carries no id field")
	}
	if _, ok := decoded.Candidates[0]["item_id"]; ok {
		t.Fatalf("a plan candidate carries item_id, which would be the database id")
	}
}

func TestPlanTrimCapsCandidatesAndAbstracts(t *testing.T) {
	var cands []PlanCandidate
	for i := 0; i < MaxPlanCandidates+10; i++ {
		cands = append(cands, PlanCandidate{ID: i + 1, Title: "t"})
	}
	got := PlanPayload{Candidates: cands}.Trim()
	if len(got.Candidates) != MaxPlanCandidates {
		t.Fatalf("Trim kept %d candidates, cap is %d", len(got.Candidates), MaxPlanCandidates)
	}

	long := strings.Repeat("x", MaxAbstractRunes+100)
	got2 := PlanPayload{Candidates: []PlanCandidate{{ID: 1, Abstract: long}}}.Trim()
	if n := len([]rune(got2.Candidates[0].Abstract)); n != MaxAbstractRunes {
		t.Fatalf("abstract kept %d runes, cap is %d", n, MaxAbstractRunes)
	}

	// A payload under both caps is untouched.
	small := PlanPayload{TargetMinutes: 10, Candidates: []PlanCandidate{{ID: 1, Abstract: "short"}}}
	got3 := small.Trim()
	if got3.Candidates[0].Abstract != "short" || got3.TargetMinutes != 10 {
		t.Fatalf("a payload under the caps was modified: %+v", got3)
	}
}

// TestPlanAuditRejectsNonJSON matches the same guard classify.go's audits
// carry — a hand-built body that is not JSON at all must error rather than
// silently report "nothing bad found".
func TestPlanAuditRejectsNonJSON(t *testing.T) {
	if _, err := AuditPlan([]byte("not json")); err == nil {
		t.Fatalf("AuditPlan accepted non-JSON")
	}
}
