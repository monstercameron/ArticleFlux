package pipeline

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
)

func newService(t *testing.T) *Service {
	t.Helper()
	lx, err := classify.Compile(lexicon.Categories())
	if err != nil {
		t.Fatalf("compiling the shipped taxonomy: %v", err)
	}
	return New(lx, classify.DefaultStrategy(), nil)
}

func analyzeOne(t *testing.T, s *Service, it Item) Analysis {
	t.Helper()
	out, err := s.Analyze(context.Background(), []Item{it})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d analyses for one item", len(out))
	}
	return out[0]
}

// TestReproducesExactly is the property the whole package rests on (§27.2c):
// ClearAnalysis then a re-run must produce the same rows, so nothing here may
// depend on a clock, on randomness, or on map iteration order.
func TestReproducesExactly(t *testing.T) {
	s := newService(t)
	items := []Item{
		{ID: "a", Title: "Ransomware group exploits a zero day in a VPN appliance",
			URL:     "https://example.com/security/2026/07/vpn-zero-day/",
			Summary: "The vulnerability allowed remote code execution before the patch landed.",
			Body:    strings.Repeat("The breach was disclosed by the vendor after a security advisory. ", 12)},
		{ID: "b", Title: "A week on the beaches of Java",
			Body: strings.Repeat("The island's volcanoes make a dramatic backdrop for the itinerary. ", 12)},
		{ID: "c", Title: "How to build your own NAS from scratch",
			Body: strings.Repeat("The motherboard and the drive bays are the two decisions that matter. ", 12)},
	}

	first, err := s.Analyze(context.Background(), items)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		again, err := s.Analyze(context.Background(), items)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d differed from the first:\n first: %+v\n again: %+v", i, first, again)
		}
	}
}

func TestEmptyBatch(t *testing.T) {
	s := newService(t)
	out, err := s.Analyze(context.Background(), nil)
	if err != nil || out != nil {
		t.Fatalf("empty batch returned %v, %v", out, err)
	}
}

func TestEveryItemGetsARowInOrder(t *testing.T) {
	s := newService(t)
	items := []Item{{ID: "x", Title: "One"}, {ID: "y", Title: "Two"}, {ID: "z", Title: "Three"}}
	out, err := s.Analyze(context.Background(), items)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(items) {
		t.Fatalf("%d analyses for %d items", len(out), len(items))
	}
	for i := range items {
		if out[i].ItemID != items[i].ID {
			t.Fatalf("position %d is %q, wanted %q", i, out[i].ItemID, items[i].ID)
		}
	}
}

func TestEnglishIsDetectedAndCategorised(t *testing.T) {
	s := newService(t)
	got := analyzeOne(t, s, Item{
		ID:      "en",
		Title:   "Emergency patch closes a zero day that was exploited in the wild",
		Summary: "The vulnerability allowed remote code execution and a breach is confirmed.",
	})
	if got.Lang != LangEnglish {
		t.Fatalf("lang was %q, wanted %q", got.Lang, LangEnglish)
	}
	if got.Primary != "security" {
		t.Fatalf("primary was %q, wanted security (scores %v)", got.Primary, got.CategoryScores)
	}
}

// TestNonEnglishIsLeftAlone is §27.13: an English-only lexicon must REFUSE
// rather than pick a category from whichever English words a foreign headline
// happens to share. A confidently wrong chip is the failure R23 names.
func TestNonEnglishIsLeftAlone(t *testing.T) {
	s := newService(t)
	got := analyzeOne(t, s, Item{
		ID:      "de",
		Title:   "Bundesregierung beschließt neue Regeln für die Netzbetreiber im Land",
		Summary: "Die Entscheidung wurde nach langen Verhandlungen zwischen den Ländern getroffen.",
	})
	if got.Lang == LangEnglish {
		t.Fatalf("German text was detected as English")
	}
	if got.Primary != "" || len(got.CategoryScores) != 0 {
		t.Fatalf("a non-English item was categorised as %q with scores %v",
			got.Primary, got.CategoryScores)
	}
}

// TestShortHeadlinesStillGetCategorised guards the asymmetry between "" and
// "und". A twelve-word headline is the most common shape in a feed, and a
// detector that declines to answer must not take the categoriser down with it.
func TestShortHeadlinesStillGetCategorised(t *testing.T) {
	s := newService(t)
	got := analyzeOne(t, s, Item{ID: "short", Title: "Kubernetes ransomware campaign"})
	if got.Lang == "und" {
		t.Fatalf("a short English headline was marked not-English")
	}
	if len(got.CategoryScores) == 0 {
		t.Fatalf("a short headline produced no scores at all")
	}
}

func TestGenreMarkers(t *testing.T) {
	s := newService(t)
	cases := []struct {
		want, title, url string
	}{
		{GenreRelease, "Patch notes for the 2.4 update", ""},
		{GenreTutorial, "How to build your own NAS", ""},
		{GenreTutorial, "Building a router", "https://example.com/how-to-build-a-router/"},
		{GenreInterview, "An interview with the compiler team", ""},
		{GenreReview, "Review: the new keyboard", ""},
		{GenreRoundup, "This week in Rust", ""},
		{GenreResearch, "A new study finds sleep matters", ""},
		{GenreOpinion, "The case against microservices", ""},
		{GenreAnalysis, "What it means for the grid", ""},
		{GenreNews, "Regulator fines the operator", ""},
	}
	for _, c := range cases {
		t.Run(c.want+"/"+c.title, func(t *testing.T) {
			got := analyzeOne(t, s, Item{ID: "g", Title: c.title, URL: c.url})
			if got.Genre != c.want {
				t.Fatalf("genre was %q, wanted %q", got.Genre, c.want)
			}
		})
	}
}

func TestKeyphrasesAndVectorArePopulated(t *testing.T) {
	s := newService(t)
	got := analyzeOne(t, s, Item{
		ID:    "k",
		Title: "Checkpoint starvation in the write ahead log",
		Body: strings.Repeat("The write ahead log grows when a checkpoint cannot run. "+
			"Checkpoint starvation is the usual cause of an oversized log file. ", 10),
	})
	if len(got.Keyphrases) == 0 {
		t.Fatalf("no keyphrases")
	}
	if len(got.Keyphrases) > MaxKeyphrases {
		t.Fatalf("%d keyphrases, over the %d cap", len(got.Keyphrases), MaxKeyphrases)
	}
	if len(got.Vector) == 0 {
		t.Fatalf("no vector")
	}
	// The pruning floor must actually be applied, or the largest column on the
	// row carries a tail of once-seen terms.
	for term, w := range got.Vector {
		if w < MinVectorWeight {
			t.Fatalf("term %q survived pruning at weight %v, floor is %v",
				term, w, MinVectorWeight)
		}
	}
}

func TestEntitiesAreCappedAndNormalised(t *testing.T) {
	s := newService(t)
	// Sentence case, with two real names in it. Title Case deliberately avoided:
	// textvec.Phrases refuses to answer for it, because capitalisation there is a
	// house style rather than a claim about particular words — and a test written
	// in Title Case measures that refusal instead of this analyzer.
	got := analyzeOne(t, s, Item{
		ID:      "e",
		Title:   "The new Nintendo Switch outsold every rival this quarter",
		Summary: "Owners of a Framework Laptop reported the same result last year.",
	})
	if len(got.Entities) == 0 {
		t.Fatalf("no entities extracted")
	}
	if len(got.Entities) > MaxEntities {
		t.Fatalf("%d entities, over the %d cap", len(got.Entities), MaxEntities)
	}
	seen := map[string]bool{}
	for _, e := range got.Entities {
		if e.Name != strings.ToLower(e.Name) {
			t.Fatalf("entity key %q is not lowercased", e.Name)
		}
		if seen[e.Name] {
			t.Fatalf("entity %q appeared twice", e.Name)
		}
		seen[e.Name] = true
		if e.Label == "" {
			t.Fatalf("entity %q has no display label", e.Name)
		}
	}
}

func TestEmptyItemProducesNoJunk(t *testing.T) {
	s := newService(t)
	got := analyzeOne(t, s, Item{ID: "empty"})
	if got.Primary != "" || len(got.CategoryScores) != 0 {
		t.Fatalf("an empty item was categorised: %+v", got)
	}
	if len(got.Keyphrases) != 0 || len(got.Entities) != 0 || len(got.Vector) != 0 {
		t.Fatalf("an empty item produced content: %+v", got)
	}
	if got.ItemID != "empty" {
		t.Fatalf("the item id was lost")
	}
}

// ---------------------------------------------------------------------------
// LexiconHash
// ---------------------------------------------------------------------------

func hashOf(t *testing.T, labels []classify.Label) string {
	t.Helper()
	lx, err := classify.Compile(labels)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return LexiconHash(lx)
}

func TestLexiconHashIsStable(t *testing.T) {
	a := hashOf(t, lexicon.Categories())
	b := hashOf(t, lexicon.Categories())
	if a != b || a == "" {
		t.Fatalf("hash was unstable across two compiles: %q vs %q", a, b)
	}
}

// TestLexiconHashIgnoresOrder is the one that matters operationally: moving a
// category up in Categories() must not invalidate every row in the database.
func TestLexiconHashIgnoresOrder(t *testing.T) {
	base := []classify.Label{
		{Slug: "a", Name: "A", Terms: []classify.Term{{Text: "alpha", Weight: 2}}},
		{Slug: "b", Name: "B", Terms: []classify.Term{{Text: "beta", Weight: 1.5}}},
	}
	swapped := []classify.Label{base[1], base[0]}
	if hashOf(t, base) != hashOf(t, swapped) {
		t.Fatalf("reordering the categories changed the hash, which would re-analyse " +
			"every row in the database for no behavioural change")
	}

	// And within a label: term order is authoring convenience, not behaviour.
	unsorted := []classify.Label{{Slug: "a", Name: "A", Terms: []classify.Term{
		{Text: "beta", Weight: 1}, {Text: "alpha", Weight: 2},
	}}}
	sorted := []classify.Label{{Slug: "a", Name: "A", Terms: []classify.Term{
		{Text: "alpha", Weight: 2}, {Text: "beta", Weight: 1},
	}}}
	if hashOf(t, unsorted) != hashOf(t, sorted) {
		t.Fatalf("reordering terms within a label changed the hash")
	}
}

func TestLexiconHashTracksWhatChangesAScore(t *testing.T) {
	base := []classify.Label{{Slug: "a", Name: "A", MinScore: 3,
		Terms:   []classify.Term{{Text: "alpha", Weight: 2, Requires: []string{"beta"}}},
		Exclude: []classify.Term{{Text: "gamma", Weight: 3}}}}

	// Deep copy, because a Label's Terms and Exclude are slice HEADERS: a shallow
	// copy shares the backing array, so mutating the copy mutates `base` too and
	// both hashes move together. The first version of this test did that and
	// reported four false failures against a hash that was working correctly —
	// which is worth leaving a note about, since the same trap is waiting for
	// anyone who adds a case here.
	clone := func(ts []classify.Term) []classify.Term {
		out := make([]classify.Term, len(ts))
		for i, t := range ts {
			out[i] = t
			out[i].Requires = append([]string(nil), t.Requires...)
		}
		return out
	}
	same := func(mut func(l *classify.Label)) bool {
		cp := base[0]
		cp.Terms = clone(base[0].Terms)
		cp.Exclude = clone(base[0].Exclude)
		mut(&cp)
		return hashOf(t, []classify.Label{cp}) == hashOf(t, base)
	}

	// A display name cannot change a score, so renaming "Film & TV" must not
	// re-analyse a database.
	if !same(func(l *classify.Label) { l.Name = "Renamed" }) {
		t.Errorf("renaming a category changed the hash")
	}
	if !same(func(l *classify.Label) { l.Prompt = "a new prompt" }) {
		t.Errorf("editing a Smart+ prompt changed the hash; it does not affect the free tier")
	}

	// Everything that CAN change a score must.
	if same(func(l *classify.Label) { l.Terms[0].Weight = 2.5 }) {
		t.Errorf("changing a term weight did not change the hash")
	}
	if same(func(l *classify.Label) { l.Terms[0].Text = "alphas" }) {
		t.Errorf("changing a term did not change the hash")
	}
	if same(func(l *classify.Label) { l.Terms[0].Requires = []string{"delta"} }) {
		t.Errorf("changing a guard did not change the hash")
	}
	if same(func(l *classify.Label) { l.MinScore = 4 }) {
		t.Errorf("changing a label floor did not change the hash")
	}
	if same(func(l *classify.Label) { l.Exclude[0].Weight = 5 }) {
		t.Errorf("changing an exclude weight did not change the hash")
	}
	if same(func(l *classify.Label) {
		l.Terms = append(l.Terms, classify.Term{Text: "epsilon"})
	}) {
		t.Errorf("adding a term did not change the hash")
	}
}

func TestNilLexiconHash(t *testing.T) {
	if LexiconHash(nil) != "" {
		t.Fatalf("a nil lexicon produced a hash")
	}
}

// TestVersionCoversEveryAnalyzer keeps the stamp honest: adding an analyzer
// without moving the version writes rows the staleness query cannot distinguish
// from complete ones, and the backfill would never revisit them.
func TestVersionCoversEveryAnalyzer(t *testing.T) {
	s := newService(t)
	want := []string{"lang", "category", "genre", "keyphrase", "entity", "vector"}
	if !reflect.DeepEqual(s.Analyzers(), want) {
		t.Fatalf("the analyzer set is %v, the version constant was reasoned about %v — "+
			"bump AnalyzerVersion and update this test together", s.Analyzers(), want)
	}
	if s.Version() <= AnalyzerVersion {
		t.Fatalf("Version() %d does not include the analyzers' own", s.Version())
	}
}
