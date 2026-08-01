package smart

import (
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/jsonsel"
)

// scrapejson.go's pure half had no test file at all — tryJSONRule and
// JSONProposal.Samples are reachable without a network seam, the same way
// tryRule and Proposal.Samples are in scrape_test.go. The retry/parse/
// provider-error paths of ProposeJSON itself are in scrapejson_llm_test.go.

const jsonListBody = `{"comic":{"chapters":[
	{"full_title":"Ch 3","chapter":3,"url":"/read/3"},
	{"full_title":"Ch 2","chapter":2,"url":"/read/2"},
	{"full_title":"Ch 1","chapter":1,"url":"/read/1"}
]}}`

func TestTryJSONRuleGoodRuleIsAccepted(t *testing.T) {
	res, problem := tryJSONRule(jsonsel.Rule{
		ItemsPath: "comic.chapters", TitlePath: "full_title", LinkPath: "url",
		IndexURL: "https://example.com/",
	}, []byte(jsonListBody))
	if problem != "" {
		t.Fatalf("a working rule was rejected: %s", problem)
	}
	if len(res.Items) != 3 {
		t.Fatalf("%d items, want 3", len(res.Items))
	}
}

func TestTryJSONRuleItemsPathFindsNothingIsRejected(t *testing.T) {
	_, problem := tryJSONRule(jsonsel.Rule{
		ItemsPath: "comic.genres", TitlePath: "name", LinkPath: "url",
	}, []byte(jsonListBody))
	if !strings.Contains(problem, "found no array of entries") {
		t.Errorf("problem = %q, want the no-array message", problem)
	}
}

// Entries exist but none produce a usable item (the title path points at a
// field none of them have).
func TestTryJSONRuleNoUsableItemsIsRejected(t *testing.T) {
	_, problem := tryJSONRule(jsonsel.Rule{
		ItemsPath: "comic.chapters", TitlePath: "nonexistent_field", LinkPath: "url",
	}, []byte(jsonListBody))
	if !strings.Contains(problem, "none produced a usable") {
		t.Errorf("problem = %q, want the 0-usable message", problem)
	}
}

func TestTryJSONRuleSingleItemIsRejected(t *testing.T) {
	_, problem := tryJSONRule(jsonsel.Rule{
		ItemsPath: "comic.chapters", TitlePath: "full_title", LinkPath: "url",
		IndexURL: "https://example.com/",
	}, []byte(`{"comic":{"chapters":[{"full_title":"Only one","url":"/read/1"}]}}`))
	if problem == "" {
		t.Fatal("a one-item rule was accepted")
	}
}

// Same failure scrape.go's allSame test names: a title path that reaches a
// field shared by every entry (the wrapper's own field, mistakenly given a
// per-entry-looking path) produces a feed where every item has one name.
func TestTryJSONRuleIdenticalTitlesAreRejected(t *testing.T) {
	body := `{"comic":{"chapters":[
		{"kind":"chapter","url":"/read/1"},
		{"kind":"chapter","url":"/read/2"},
		{"kind":"chapter","url":"/read/3"}
	]}}`
	_, problem := tryJSONRule(jsonsel.Rule{
		ItemsPath: "comic.chapters", TitlePath: "kind", LinkPath: "url",
		IndexURL: "https://example.com/",
	}, []byte(body))
	if !strings.Contains(problem, "same title") {
		t.Errorf("problem = %q, want the same-title message", problem)
	}
}

func TestTryJSONRuleRejectedSelectorsSurfaceTheCompileError(t *testing.T) {
	// Neither a link path nor a link template: jsonsel.Compile refuses this
	// rule outright, before Extract ever runs.
	_, problem := tryJSONRule(jsonsel.Rule{ItemsPath: "comic.chapters", TitlePath: "full_title"},
		[]byte(jsonListBody))
	if !strings.Contains(problem, "the paths were rejected") {
		t.Errorf("problem = %q, want a paths-rejected message", problem)
	}
}

func TestAllSameJSONWithFewerThanTwoItemsIsFalse(t *testing.T) {
	if allSameJSON(nil) {
		t.Error("allSameJSON(nil) = true")
	}
	if allSameJSON([]jsonsel.Item{{Title: "A"}}) {
		t.Error("allSameJSON of one item = true")
	}
}

// --- JSONProposal.Samples --------------------------------------------------------

func TestJSONProposalSamplesReturnsEverythingUnderTheCap(t *testing.T) {
	p := &JSONProposal{Items: []jsonsel.Item{{Title: "A"}, {Title: "B"}}}
	if got := p.Samples(); len(got) != 2 {
		t.Fatalf("got %d samples, want 2", len(got))
	}
}

func TestJSONProposalSamplesTruncatesAtTheCap(t *testing.T) {
	items := make([]jsonsel.Item, maxSamples+3)
	for i := range items {
		items[i] = jsonsel.Item{Title: string(rune('a' + i))}
	}
	p := &JSONProposal{Items: items}
	got := p.Samples()
	if len(got) != maxSamples {
		t.Fatalf("got %d samples, want exactly maxSamples (%d)", len(got), maxSamples)
	}
}
