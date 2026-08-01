package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// TestEgressKeysWereNotWidened is the guard that makes the two-list split real
// rather than a convention (§27.4e, classify.go).
//
// The tempting way to ship classification was to add `article` and `body` to
// `EgressKeys`. That would have made a body legal in a RANK payload too — and the
// entire argument for §18.8's types-as-enforcement is that the boundary must be
// impossible to widen by accident from somewhere else. This test is what stops
// the next feature from doing it.
func TestEgressKeysWereNotWidened(t *testing.T) {
	mustNotBeInterestKeys := []string{
		"article", "body", "labels", "categories", "prompt", "slug", "source",
	}
	for _, k := range mustNotBeInterestKeys {
		if EgressKeys[k] {
			t.Errorf("EgressKeys contains %q. §18.8's list is the INTEREST layer's and "+
				"classification has its own (ClassifyKeys) — adding it here makes it "+
				"legal in a rank payload, which is the failure the split prevents", k)
		}
	}

	// And the original §18.8 list is still exactly what it was, plus the
	// 2026-07-31 amendment (taste-calibration examples, plan.md §18.8).
	for _, k := range []string{"candidates", "id", "title", "summary", "profile",
		"topics", "label", "terms", "sources", "want",
		"positive_examples", "negative_examples"} {
		if !EgressKeys[k] {
			t.Errorf("EgressKeys lost %q", k)
		}
	}
	if len(EgressKeys) != 12 {
		t.Errorf("EgressKeys has %d entries, §18.8 (as amended 2026-07-31) specifies 12 — "+
			"a key was added or removed without this test being considered", len(EgressKeys))
	}
}

// TestNoAllowlistAdmitsAForbiddenKey is a statement about the BOUNDARY rather
// than about today's payloads: an allowlist describes what currently ships, and a
// later amendment could quietly admit something it should not. This checks every
// list in the package against the enumerated never-list.
func TestNoAllowlistAdmitsAForbiddenKey(t *testing.T) {
	lists := map[string]map[string]bool{
		"EgressKeys":   EgressKeys,
		"ClassifyKeys": ClassifyKeys,
	}
	for name, list := range lists {
		for k := range list {
			if ForbiddenKeys[k] {
				t.Errorf("%s admits %q, which is on ForbiddenKeys", name, k)
			}
		}
	}
	// The never-list has to actually name the things §27.4e says are forbidden,
	// or it is decoration.
	for _, k := range []string{"url", "feed_url", "user_id", "tenant_id", "note",
		"read_at", "dwell", "published_at", "author", "folder"} {
		if !ForbiddenKeys[k] {
			t.Errorf("ForbiddenKeys is missing %q, which §27.4e names explicitly", k)
		}
	}
}

// TestClassifyPayloadCarriesOnlyWhatIsPermitted is the §27.4e enforcement test.
// It audits the ASSEMBLED body, not the template.
func TestClassifyPayloadCarriesOnlyWhatIsPermitted(t *testing.T) {
	p := Shared(
		ArticleText{
			Title:   "Emergency patch closes a zero day",
			Summary: "The vulnerability allowed remote code execution.",
			Source:  "Ars Technica",
			Body:    "Administrators should apply the update immediately.",
		},
		[]LabelHint{
			{Slug: "security", Name: "Security & Privacy", Prompt: "Assign for the security OF systems. Do not assign for national security."},
			{Slug: "software", Name: "Software & Development"},
		},
		ClassifyWant{Category: 1, Secondary: 2, Tags: 5},
	)

	bad, err := AuditClassify(mustJSON(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("the classify payload carried keys §27.4e does not permit: %v", bad)
	}

	// The same body must FAIL the interest-layer audit, which is the proof that
	// the two boundaries are genuinely different rather than one list with two
	// names.
	bad, err = AuditEgress(mustJSON(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) == 0 {
		t.Fatalf("a classify payload passed the §18.8 interest-layer audit, so the two " +
			"allowlists are not actually distinct")
	}
}

// TestSharedCannotCarryAUserVocabulary. The instance-wide read serves every
// subscriber, so one reader's private taxonomy appearing in it would shape every
// other reader's labels and would leave the machine on behalf of a job they never
// consented to (§27.4d).
func TestSharedCannotCarryAUserVocabulary(t *testing.T) {
	p := Shared(ArticleText{Title: "x"}, []LabelHint{{Slug: "security"}},
		ClassifyWant{Category: 1, Tags: 5})
	if len(p.Labels.Tags) != 0 {
		t.Fatalf("Shared() produced a tag vocabulary: %v", p.Labels.Tags)
	}

	// Checked structurally, not by searching the JSON text. `want.tags` is a
	// COUNT — "return up to five tags" — and it is legitimately present; only
	// `labels.tags`, the vocabulary, must be absent. A substring match on `"tags"`
	// conflates the two and fails on a correct payload, which is what the first
	// version of this test did.
	var decoded struct {
		Labels map[string]json.RawMessage `json:"labels"`
	}
	if err := json.Unmarshal(mustJSON(t, p), &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded.Labels["tags"]; ok {
		t.Fatalf("the shared payload serialised a labels.tags vocabulary: %s", decoded.Labels["tags"])
	}
	if _, ok := decoded.Labels["categories"]; !ok {
		t.Fatalf("the shared payload carried no category vocabulary at all")
	}
}

func TestTrimCapsTheBody(t *testing.T) {
	long := strings.Repeat("word ", MaxClassifyBodyWords+500)
	p, rep := ClassifyPayload{Article: ArticleText{Body: long}}.Trim()

	if got := len(strings.Fields(p.Article.Body)); got != MaxClassifyBodyWords {
		t.Fatalf("body kept %d words, cap is %d", got, MaxClassifyBodyWords)
	}
	if rep.BodyWordsDropped != 500 {
		t.Fatalf("reported %d words dropped, expected 500", rep.BodyWordsDropped)
	}
	if rep.Empty() {
		t.Fatalf("a trim that cut 500 words reported nothing — 'no silent caps' is the rule")
	}

	// A short body is untouched and reports nothing.
	short, rep2 := ClassifyPayload{Article: ArticleText{Body: "three little words"}}.Trim()
	if short.Article.Body != "three little words" || !rep2.Empty() {
		t.Fatalf("a body under the cap was modified: %q %+v", short.Article.Body, rep2)
	}
}

func TestTrimCapsAPromptAndSaysWhich(t *testing.T) {
	p := ClassifyPayload{Labels: LabelSet{Categories: []LabelHint{
		{Slug: "ok", Prompt: "short"},
		{Slug: "toolong", Prompt: strings.Repeat("x", MaxLabelPromptRunes+50)},
	}}}
	got, rep := p.Trim()

	if n := len([]rune(got.Labels.Categories[1].Prompt)); n != MaxLabelPromptRunes {
		t.Fatalf("prompt kept %d runes, cap is %d", n, MaxLabelPromptRunes)
	}
	if len(rep.PromptsTruncated) != 1 || rep.PromptsTruncated[0] != "toolong" {
		t.Fatalf("truncation report was %v, wanted [toolong]", rep.PromptsTruncated)
	}
	if got.Labels.Categories[0].Prompt != "short" {
		t.Fatalf("a prompt under the cap was touched")
	}
}

// TestTrimDropsWholeLabels: a half-described label is worse than an absent one,
// because the model weighs it against fully-described neighbours and it loses for
// the wrong reason.
func TestTrimDropsWholeLabels(t *testing.T) {
	var cats []LabelHint
	for i := range 40 {
		cats = append(cats, LabelHint{
			Slug:   string(rune('a'+i%26)) + "cat",
			Name:   "A category name",
			Prompt: strings.Repeat("y", MaxLabelPromptRunes),
		})
	}
	p := ClassifyPayload{Labels: LabelSet{Categories: cats}}
	got, rep := p.Trim()

	if labelsRunes(got.Labels) > MaxLabelsBlockRunes {
		t.Fatalf("labels block is %d runes, cap is %d",
			labelsRunes(got.Labels), MaxLabelsBlockRunes)
	}
	if len(rep.LabelsDropped) == 0 {
		t.Fatalf("labels were dropped without being reported")
	}
	// Whole labels only: every survivor keeps its full prompt.
	for _, h := range got.Labels.Categories {
		if len([]rune(h.Prompt)) != MaxLabelPromptRunes {
			t.Fatalf("%s survived with a partial prompt (%d runes)", h.Slug, len([]rune(h.Prompt)))
		}
	}
	// Tags go before categories, since the caller orders by priority.
	if len(got.Labels.Categories) == 0 {
		t.Fatalf("every category was dropped")
	}
}

func TestTrimTerminatesWhenNothingIsLeftToDrop(t *testing.T) {
	// A body far over the block cap with no labels at all must not spin: the
	// block cap only governs labels, and the loop has to notice there is nothing
	// left it can remove.
	p := ClassifyPayload{Article: ArticleText{Body: strings.Repeat("z ", 100)}}
	done := make(chan struct{})
	go func() {
		p.Trim()
		close(done)
	}()
	<-done
}

func TestAuditClassifyRejectsSmuggledKeys(t *testing.T) {
	// A hand-built body, which is exactly the path the types cannot protect and
	// the audit exists for.
	raw := []byte(`{"article":{"title":"t","url":"https://example.com/x","author":"A"},
	                "want":{"category":1}}`)
	bad, err := AuditClassify(raw)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, k := range bad {
		found[k] = true
	}
	for _, k := range []string{"url", "author"} {
		if !found[k] {
			t.Errorf("AuditClassify did not flag %q: got %v", k, bad)
		}
	}
}

func TestAuditsRejectNonJSON(t *testing.T) {
	if _, err := AuditClassify([]byte("not json")); err == nil {
		t.Fatalf("AuditClassify accepted non-JSON")
	}
	if _, err := AuditEgress([]byte("not json")); err == nil {
		t.Fatalf("AuditEgress accepted non-JSON")
	}
}
