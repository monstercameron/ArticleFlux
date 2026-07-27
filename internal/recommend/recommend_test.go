package recommend

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

func healthy() Health {
	return Health{
		HasFeed:      true,
		Reachable:    true,
		LastPostAt:   now.Add(-3 * 24 * time.Hour),
		PostsPerWeek: 2,
	}
}

func good(domain string) Candidate {
	return Candidate{
		Domain:  domain,
		FeedURL: "https://" + domain + "/feed",
		Title:   domain,
		Rung:    RungOutlinks,
		Health:  healthy(),
		Evidence: Evidence{
			LinkCount: 6, DistinctSources: 3, EngagementWeight: 2.5,
			TopicScore: 0.6, TopicLabel: "Npu Inference",
		},
	}
}

func domains(rs []Recommendation) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Domain)
	}
	return out
}

func rejectionFor(res Result, domain string) string {
	for _, r := range res.Rejected {
		if r.Domain == domain {
			return r.Reason
		}
	}
	return ""
}

// The ticket's acceptance bar: a dead site and a firehose are both refused, and
// every survivor carries a human-readable evidence string.
func TestHealthGateRefusesDeadSitesAndFirehoses(t *testing.T) {
	dead := good("dead.example")
	dead.Health.LastPostAt = now.Add(-400 * 24 * time.Hour)

	firehose := good("firehose.example")
	firehose.Health.PostsPerWeek = 700 // a hundred a day

	res := Score([]Candidate{good("live.example"), dead, firehose}, nil, Thresholds{}, now)

	if got := domains(res.Recommendations); len(got) != 1 || got[0] != "live.example" {
		t.Errorf("recommendations = %v, want only the live one", got)
	}
	if r := rejectionFor(res, "dead.example"); !strings.Contains(r, "silent since") {
		t.Errorf("dead site rejected as %q", r)
	}
	if r := rejectionFor(res, "firehose.example"); !strings.Contains(r, "a day") {
		t.Errorf("firehose rejected as %q", r)
	}
	for _, rec := range res.Recommendations {
		if strings.TrimSpace(rec.Evidence) == "" {
			t.Errorf("%s has no evidence string", rec.Domain)
		}
	}
}

func TestEveryGateReason(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Candidate)
		state  State
		want   string
	}{
		{"already subscribed", nil, State{Subscribed: true}, "already subscribed"},
		{"dismissed", nil, State{Dismissed: true}, "dismissed before"},
		{"muted", nil, State{Muted: true}, "muted"},
		{"unreachable", func(c *Candidate) { c.Health.Reachable = false }, State{}, "did not respond"},
		{"no feed", func(c *Candidate) { c.Health.HasFeed = false }, State{}, "no feed"},
		{"aggregator", func(c *Candidate) { c.Health.IsAggregator = true }, State{}, "aggregator"},
		{"undated", func(c *Candidate) { c.Health.LastPostAt = time.Time{} }, State{}, "no dated posts"},
		{"silent", func(c *Candidate) { c.Health.LastPostAt = now.Add(-400 * 24 * time.Hour) },
			State{}, "silent since"},
		{"firehose", func(c *Candidate) { c.Health.PostsPerWeek = 500 }, State{}, "a day"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cand := good("x.example")
			if c.mutate != nil {
				c.mutate(&cand)
			}
			known := map[string]State{"x.example": c.state}
			res := Score([]Candidate{cand}, known, Thresholds{}, now)

			if len(res.Recommendations) != 0 {
				t.Fatalf("candidate survived: %+v", res.Recommendations)
			}
			if r := rejectionFor(res, "x.example"); !strings.Contains(r, c.want) {
				t.Errorf("rejected as %q, want something containing %q", r, c.want)
			}
		})
	}
}

// §18.7: dismissals are remembered per domain and never re-suggested. A
// recommender that re-asks is one the reader learns to ignore entirely.
func TestDismissalsAreNeverReSuggested(t *testing.T) {
	strong := good("dismissed.example")
	strong.Evidence.LinkCount = 99
	strong.Evidence.DistinctSources = 20
	strong.Evidence.StarredViaAggregator = 15

	known := map[string]State{"dismissed.example": {Dismissed: true}}
	res := Score([]Candidate{strong}, known, Thresholds{}, now)
	if len(res.Recommendations) != 0 {
		t.Error("a dismissed domain came back, however strong the evidence")
	}
}

// Three writers linking once each beats one writer linking six times. Without
// that the scorer just finds whoever links most.
func TestDistinctSourcesBeatRawVolume(t *testing.T) {
	spread := good("spread.example")
	spread.Evidence = Evidence{LinkCount: 3, DistinctSources: 3, EngagementWeight: 1.5}

	concentrated := good("concentrated.example")
	concentrated.Evidence = Evidence{LinkCount: 6, DistinctSources: 1, EngagementWeight: 1.5}

	res := Score([]Candidate{spread, concentrated}, nil, Thresholds{}, now)
	if len(res.Recommendations) < 2 {
		t.Fatalf("expected both, got %v", domains(res.Recommendations))
	}
	if res.Recommendations[0].Domain != "spread.example" {
		t.Errorf("ranked %v first; three writers linking once should beat one linking six times",
			res.Recommendations[0].Domain)
	}
}

// A link inside something skimmed is not the same evidence as a link inside
// something read twice and starred.
func TestEngagementWithTheLinkingArticleMatters(t *testing.T) {
	engaged := good("engaged.example")
	engaged.Evidence = Evidence{LinkCount: 4, DistinctSources: 2, EngagementWeight: 4}

	skimmed := good("skimmed.example")
	skimmed.Evidence = Evidence{LinkCount: 4, DistinctSources: 2, EngagementWeight: 0.2}

	res := Score([]Candidate{engaged, skimmed}, nil, Thresholds{}, now)
	if res.Recommendations[0].Domain != "engaged.example" {
		t.Errorf("ranked %v first, ignoring engagement with the linking article",
			res.Recommendations[0].Domain)
	}
}

// The evidence string is the product. It has to read as a sentence a person
// would accept, not as a debug dump.
func TestEvidenceReadsAsASentence(t *testing.T) {
	c := good("example.org")
	c.Evidence = Evidence{
		LinkCount: 11, DistinctSources: 3, EngagementWeight: 3,
		StarredViaAggregator: 4, AggregatorName: "Hacker News",
		TopicScore: 0.7, TopicLabel: "Npu Inference",
	}
	c.Health.PostsPerWeek = 7

	res := Score([]Candidate{c}, nil, Thresholds{}, now)
	if len(res.Recommendations) != 1 {
		t.Fatal("candidate did not survive")
	}
	got := res.Recommendations[0].Evidence

	// This is the §18.7 example sentence, assembled from real fields.
	for _, want := range []string{
		"3 writers you read linked here 11 times",
		"you saved 4 of its articles via Hacker News",
		"matches your Npu Inference reading",
		"posts ~7 a week",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence %q is missing %q", got, want)
		}
	}
}

func TestEvidenceSingularAndPlural(t *testing.T) {
	c := good("solo.example")
	c.Evidence = Evidence{LinkCount: 1, DistinctSources: 1, EngagementWeight: 1,
		StarredViaAggregator: 1, AggregatorName: "Lobsters"}
	res := Score([]Candidate{c}, nil, Thresholds{MinScore: -1}, now)
	got := res.Recommendations[0].Evidence

	if !strings.Contains(got, "1 writer you read linked here once") {
		t.Errorf("singular link phrasing wrong: %q", got)
	}
	if !strings.Contains(got, "you saved 1 of its article via Lobsters") {
		t.Errorf("singular article phrasing wrong: %q", got)
	}
}

func TestCadencePhrasing(t *testing.T) {
	cases := []struct {
		perWeek float64
		want    string
	}{
		{700, "posts ~100 a day"},
		{21, "posts ~3 a day"},
		{7, "posts ~7 a week"},
		{2, "posts a few times a week"},
		{0.9, "posts ~weekly"},
		{0.25, "posts ~monthly"},
		{0.05, "posts rarely"},
	}
	for _, c := range cases {
		if got := cadence(c.perWeek); got != c.want {
			t.Errorf("cadence(%v) = %q, want %q", c.perWeek, got, c.want)
		}
	}
}

// §18.7's anti-filter-bubble guardrail. Adjacent candidates score lower by
// construction, so a plain top-N drops them every time and the guardrail becomes
// a comment rather than a behaviour.
func TestAdjacentCandidatesGetReservedSlots(t *testing.T) {
	var cands []Candidate
	for i := 0; i < 20; i++ {
		c := good(string(rune('a'+i)) + ".example")
		c.Evidence.LinkCount = 20 - i
		c.Evidence.DistinctSources = 8
		cands = append(cands, c)
	}
	adj := good("adjacent.example")
	adj.Evidence = Evidence{LinkCount: 1, DistinctSources: 1, EngagementWeight: 0.2, Adjacent: true}
	cands = append(cands, adj)

	res := Score(cands, nil, Thresholds{MaxResults: 6, AdjacentSlots: 1, MinScore: -1}, now)
	if len(res.Recommendations) != 6 {
		t.Fatalf("got %d results, want 6", len(res.Recommendations))
	}

	found := false
	for _, r := range res.Recommendations {
		if r.Adjacent {
			found = true
		}
	}
	if !found {
		t.Errorf("the adjacent candidate was crowded out: %v", domains(res.Recommendations))
	}
}

func TestAdjacentIsLabelledInTheEvidence(t *testing.T) {
	adj := good("adjacent.example")
	adj.Evidence.Adjacent = true
	res := Score([]Candidate{adj}, nil, Thresholds{}, now)
	if !strings.Contains(res.Recommendations[0].Evidence, "different from what you usually read") {
		t.Errorf("adjacent candidate not labelled: %q", res.Recommendations[0].Evidence)
	}
}

func TestResultsAreDeterministic(t *testing.T) {
	cands := []Candidate{good("c.example"), good("a.example"), good("b.example")}
	first := domains(Score(cands, nil, Thresholds{}, now).Recommendations)

	reversed := []Candidate{cands[2], cands[1], cands[0]}
	second := domains(Score(reversed, nil, Thresholds{}, now).Recommendations)

	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Errorf("input order changed the output:\n  %v\n  %v", first, second)
	}
}

func TestWwwAndCaseAreNormalised(t *testing.T) {
	c := good("WWW.Example.COM")
	known := map[string]State{"example.com": {Subscribed: true}}
	res := Score([]Candidate{c}, known, Thresholds{}, now)
	if len(res.Recommendations) != 0 {
		t.Error("www.Example.COM was not recognised as the subscribed example.com")
	}
}

func TestWeakCandidatesAreRejectedWithAReason(t *testing.T) {
	weak := good("weak.example")
	weak.Evidence = Evidence{LinkCount: 1}

	res := Score([]Candidate{weak}, nil, Thresholds{MinScore: 2.0}, now)
	if len(res.Recommendations) != 0 {
		t.Error("a weak candidate survived a high MinScore")
	}
	if r := rejectionFor(res, "weak.example"); !strings.Contains(r, "not enough evidence") {
		t.Errorf("rejected as %q", r)
	}
}

func TestEmptyInput(t *testing.T) {
	res := Score(nil, nil, Thresholds{}, now)
	if len(res.Recommendations) != 0 || len(res.Rejected) != 0 {
		t.Errorf("empty input produced %+v", res)
	}
}
