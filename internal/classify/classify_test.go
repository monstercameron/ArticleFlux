package classify

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// testLexicon is a deliberately small label set. It is hand-built rather than
// imported from internal/classify/lexicon so that a failure here is a statement
// about the scorer and never about the shipped taxonomy — the two have separate
// tests for a reason, and a scorer test that breaks when someone adds a term to
// `security` is a scorer test nobody will trust.
func testLexicon(t *testing.T) *Lexicon {
	t.Helper()
	lx, err := Compile([]Label{
		{
			Slug: "software", Name: "Software",
			Terms: []Term{
				{Text: "kubernetes", Weight: 2.0},
				{Text: "compiler", Weight: 1.4},
				{Text: "open source", Weight: 1.2},
				{Text: "ai", Weight: 0.6},
				{Text: "rust", Weight: 1.6, Requires: []string{"cargo", "borrow checker", "rustc"}},
			},
			Exclude: []Term{{Text: "rust belt", Weight: 3.0}},
		},
		{
			Slug: "security", Name: "Security",
			Terms: []Term{
				{Text: "ransomware", Weight: 2.4},
				{Text: "zero day", Weight: 2.2},
				{Text: "kubernetes", Weight: 0.8},
				{Text: `cve-\d{4}-\d{4,}`, Weight: 2.5, Regex: true},
			},
		},
		{
			Slug: "hardware", Name: "Hardware",
			Terms: []Term{
				{Text: "apple", Weight: 1.5, Requires: []string{"iphone", "macos", "cupertino"}},
				{Text: "npu", Weight: 2.0},
			},
		},
		{
			Slug: "travel", Name: "Travel",
			Terms: []Term{
				{Text: "java islands", Weight: 2.0},
				{Text: "national park", Weight: 2.0},
			},
		},
	})
	if err != nil {
		t.Fatalf("compiling the test lexicon: %v", err)
	}
	return lx
}

// loose is a strategy with the floor dropped, for the tests whose subject is the
// arithmetic rather than the threshold. Using DefaultStrategy everywhere would
// make every assertion below depend on a number that is explicitly still being
// calibrated (see DefaultStrategy's comment).
func loose() Strategy {
	st := DefaultStrategy()
	st.MinScore = 0.01
	return st
}

func scoreOf(r Result, slug string) float64 {
	for _, s := range r.Scores {
		if s.Slug == slug {
			return s.Value
		}
	}
	return 0
}

func TestCompileRejectsWhatCouldNeverWork(t *testing.T) {
	cases := []struct {
		name   string
		labels []Label
		want   string
	}{
		{
			"duplicate slug",
			[]Label{{Slug: "a", Terms: []Term{{Text: "x"}}}, {Slug: "a", Terms: []Term{{Text: "y"}}}},
			"duplicate label slug",
		},
		{
			"term too long to ever match",
			[]Label{{Slug: "a", Terms: []Term{{Text: "one two three four"}}}},
			"could never match",
		},
		{
			"duplicate term",
			[]Label{{Slug: "a", Terms: []Term{{Text: "rust"}, {Text: "Rust"}}}},
			"twice",
		},
		{
			"negative weight instead of Exclude",
			[]Label{{Slug: "a", Terms: []Term{{Text: "x", Weight: -1}}}},
			"use Exclude",
		},
		{
			"unparseable regex",
			[]Label{{Slug: "a", Terms: []Term{{Text: "([a-z", Regex: true}}}},
			"error parsing regexp",
		},
		{
			"term that scans to nothing",
			[]Label{{Slug: "a", Terms: []Term{{Text: "   ---   "}}}},
			"scans to nothing",
		},
		{
			"prompt over the cap",
			[]Label{{Slug: "a", Terms: []Term{{Text: "x"}}, Prompt: strings.Repeat("x", MaxPromptRunes+1)}},
			"prompt is",
		},
		{
			"no slug",
			[]Label{{Slug: "", Terms: []Term{{Text: "x"}}}},
			"needs a slug",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Compile(c.labels)
			if err == nil {
				t.Fatalf("compiled %s without complaint", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error was %q, wanted it to mention %q", err, c.want)
			}
		})
	}
}

func TestRegexCapIsEnforced(t *testing.T) {
	terms := make([]Term, 0, MaxUserRegex+1)
	for i := 0; i <= MaxUserRegex; i++ {
		terms = append(terms, Term{Text: fmt.Sprintf("pat%d", i), Regex: true})
	}
	if _, err := Compile([]Label{{Slug: "a", Terms: terms}}); err == nil {
		t.Fatalf("compiled %d regex terms with a cap of %d", len(terms), MaxUserRegex)
	}
}

// TestDeterministic is the guard on the property everything else assumes: the
// same input scores identically every time, regardless of map iteration order.
// Ten runs rather than two, because a map-order dependency shows up
// probabilistically and a two-run test would pass most of the time — which is
// worse than not having it.
func TestDeterministic(t *testing.T) {
	lx := testLexicon(t)
	it := Item{
		Title:   "Kubernetes ransomware campaign hits open source registries",
		URL:     "https://example.com/security/2026/07/k8s-ransomware/",
		Summary: "A zero day in a popular compiler plugin was used.",
		Body:    strings.Repeat("kubernetes clusters and open source tooling. ", 40),
	}
	first := lx.Score(it, loose())
	for i := range 10 {
		got := lx.Score(it, loose())
		if got.Primary != first.Primary || got.Ambiguous != first.Ambiguous {
			t.Fatalf("run %d: primary %q/%v, first was %q/%v",
				i, got.Primary, got.Ambiguous, first.Primary, first.Ambiguous)
		}
		if len(got.Scores) != len(first.Scores) {
			t.Fatalf("run %d: %d scores, first had %d", i, len(got.Scores), len(first.Scores))
		}
		for j := range got.Scores {
			if got.Scores[j].Slug != first.Scores[j].Slug ||
				math.Abs(got.Scores[j].Value-first.Scores[j].Value) > 1e-12 {
				t.Fatalf("run %d position %d: %v, first was %v",
					i, j, got.Scores[j], first.Scores[j])
			}
			for k := range got.Scores[j].Matches {
				if got.Scores[j].Matches[k] != first.Scores[j].Matches[k] {
					t.Fatalf("run %d: match order moved: %v vs %v",
						i, got.Scores[j].Matches[k], first.Scores[j].Matches[k])
				}
			}
		}
	}
}

// TestTwoCompilesAgree catches an index that depends on the order Compile
// happened to walk, which a single-lexicon determinism test cannot see.
func TestTwoCompilesAgree(t *testing.T) {
	a, b := testLexicon(t), testLexicon(t)
	it := Item{Title: "A zero day in Kubernetes", Body: "compiler open source"}
	ra, rb := a.Score(it, loose()), b.Score(it, loose())
	if ra.Primary != rb.Primary || len(ra.Scores) != len(rb.Scores) {
		t.Fatalf("two compiles disagreed: %+v vs %+v", ra, rb)
	}
}

// TestShortTokensSurvive is the regression test for the reason
// `textvec.Scan` exists at all: `textvec.Tokenize` drops everything under three
// characters, which silently deletes `ai`, `ev`, `ui`, `5g` and `f1` from every
// lexicon that uses them.
func TestShortTokensSurvive(t *testing.T) {
	lx := testLexicon(t)
	r := lx.Score(Item{Title: "AI everywhere"}, loose())
	if scoreOf(r, "software") == 0 {
		t.Fatalf("the term \"ai\" did not match a title containing AI: %+v", r.Scores)
	}
}

// TestSentenceBoundaryBlocksPhrases: "…runs on Java. Islands…" must not
// manufacture the term "java islands".
func TestSentenceBoundaryBlocksPhrases(t *testing.T) {
	lx := testLexicon(t)

	across := lx.Score(Item{Title: "It runs on Java. Islands of the Pacific are lovely."}, loose())
	if scoreOf(across, "travel") != 0 {
		t.Fatalf("a phrase was built across a sentence boundary: %+v", across.Scores)
	}

	within := lx.Score(Item{Title: "The Java Islands trip report"}, loose())
	if scoreOf(within, "travel") == 0 {
		t.Fatalf("an adjacent phrase within one sentence did not match: %+v", within.Scores)
	}
}

// TestGuardTermsNeedTheirCompanion is §27.3c in miniature — the mechanism that
// decides whether the feature is trusted or turned off.
func TestGuardTermsNeedTheirCompanion(t *testing.T) {
	lx := testLexicon(t)

	fruit := lx.Score(Item{
		Title: "Apple picking season is here",
		Body:  "The orchard opens on Saturday and the apple harvest looks strong.",
	}, loose())
	if scoreOf(fruit, "hardware") != 0 {
		t.Fatalf("apple-the-fruit scored for hardware: %+v", fruit.Scores)
	}

	company := lx.Score(Item{
		Title: "Apple ships a new iPhone",
		Body:  "The iPhone launch was announced in Cupertino.",
	}, loose())
	if scoreOf(company, "hardware") == 0 {
		t.Fatalf("apple-the-company did not score for hardware: %+v", company.Scores)
	}

	// The guard is satisfied from ANYWHERE in the item, which is the common real
	// shape: the ambiguous word is in the headline and the confirmation is in the
	// body.
	split := lx.Score(Item{Title: "Apple had a good quarter", Body: "macOS shipped on time."}, loose())
	if scoreOf(split, "hardware") == 0 {
		t.Fatalf("a guard satisfied from the body did not count: %+v", split.Scores)
	}
}

func TestExcludeSubtracts(t *testing.T) {
	lx := testLexicon(t)
	plain := lx.Score(Item{Title: "Rust and the borrow checker", Body: "cargo build"}, loose())
	if scoreOf(plain, "software") == 0 {
		t.Fatalf("guarded rust did not score: %+v", plain.Scores)
	}
	belt := lx.Score(Item{
		Title: "Rust Belt towns and the borrow checker of public finance",
		Body:  "cargo rail traffic through the Rust Belt",
	}, loose())
	if scoreOf(belt, "software") >= scoreOf(plain, "software") {
		t.Fatalf("the rust belt exclude did not subtract: %v vs %v",
			scoreOf(belt, "software"), scoreOf(plain, "software"))
	}
}

// TestRepetitionDoesNotAccumulate is the long-article defence. A term counts
// once no matter how often it appears, so a 4,000-word piece that says
// "kubernetes" twenty times cannot outscore a short one that is about it.
func TestRepetitionDoesNotAccumulate(t *testing.T) {
	lx := testLexicon(t)
	st := loose()
	st.UseBody = true

	one := lx.Score(Item{Body: "kubernetes"}, st)
	many := lx.Score(Item{Body: strings.Repeat("kubernetes ", 40)}, st)

	// The raw contribution is identical; only the length divisor differs, and it
	// must make the long one score LOWER rather than higher.
	if scoreOf(many, "software") > scoreOf(one, "software") {
		t.Fatalf("repetition raised the score: %v vs %v",
			scoreOf(many, "software"), scoreOf(one, "software"))
	}
}

func TestFieldMultipliersRankEvidence(t *testing.T) {
	lx := testLexicon(t)
	st := loose()

	inTitle := lx.Score(Item{Title: "Kubernetes"}, st)
	inBody := lx.Score(Item{Body: "kubernetes"}, st)
	if scoreOf(inTitle, "software") <= scoreOf(inBody, "software") {
		t.Fatalf("a title match did not beat a body match: %v vs %v",
			scoreOf(inTitle, "software"), scoreOf(inBody, "software"))
	}

	// A term in both fields is scored at the title multiplier, not the sum.
	//
	// Compared as a band rather than for equality: the two-field item has a body,
	// so the length divisor applies to it and not to the title-only one. That
	// gap is a fraction of a percent for a one-word body and it is real — the
	// assertion that matters is that the score is the TITLE's, nowhere near the
	// title plus the body.
	both := lx.Score(Item{Title: "Kubernetes", Body: "kubernetes"}, st)
	title, sum := scoreOf(inTitle, "software"), scoreOf(inTitle, "software")+scoreOf(inBody, "software")
	if got := scoreOf(both, "software"); got > title || got < title*0.98 {
		t.Fatalf("a term in two fields scored %v; wanted its title score %v, not the sum %v",
			got, title, sum)
	}
}

func TestSourceContributionIsCapped(t *testing.T) {
	lx := testLexicon(t)
	st := loose()
	st.SourceCap = 1.0

	r := lx.Score(Item{
		SourceTitle: "Kubernetes Weekly",
		FolderName:  "Open Source",
	}, st)
	if got := scoreOf(r, "software"); got > st.SourceCap+1e-9 {
		t.Fatalf("source contribution %v exceeded the cap %v", got, st.SourceCap)
	}
	if scoreOf(r, "software") == 0 {
		t.Fatalf("the source field contributed nothing at all: %+v", r.Scores)
	}
}

// TestRefusingIsAnAnswer: an off-topic item gets no primary, not a default one.
func TestRefusingIsAnAnswer(t *testing.T) {
	lx := testLexicon(t)
	r := lx.Score(Item{
		Title:   "A quiet weekend in the garden",
		Summary: "Nothing much happened and that was fine.",
		Body:    "The tomatoes came in early this year and the weather held.",
	}, DefaultStrategy())

	if r.Primary != "" {
		t.Fatalf("an off-topic item was filed under %q: %+v", r.Primary, r.Scores)
	}
	if len(r.Secondary) != 0 {
		t.Fatalf("no primary but %d secondaries: %v", len(r.Secondary), r.Secondary)
	}
	if r.Explain() != nil {
		t.Fatalf("an unsorted item produced a reason line: %v", r.Explain())
	}
}

// TestBelowFloorIsUnsorted checks the floor itself rather than the vocabulary: a
// single weak match must not be enough.
func TestBelowFloorIsUnsorted(t *testing.T) {
	lx := testLexicon(t)
	st := DefaultStrategy()
	st.MinScore = 5.0

	r := lx.Score(Item{Body: "one mention of kubernetes"}, st)
	if r.Primary != "" {
		t.Fatalf("a %v score cleared a floor of %v", scoreOf(r, "software"), st.MinScore)
	}
}

// TestAmbiguityIsFlaggedNotRefused is the correction recorded on
// Strategy.Margin: two strong signals disagreeing about the section must still
// produce a primary, flagged for escalation, rather than collapsing to Unsorted.
func TestAmbiguityIsFlaggedNotRefused(t *testing.T) {
	lx, err := Compile([]Label{
		{Slug: "a", Terms: []Term{{Text: "alpha", Weight: 2.0}}},
		{Slug: "b", Terms: []Term{{Text: "beta", Weight: 1.9}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := loose()
	st.Margin = 1.35

	r := lx.Score(Item{Title: "alpha and beta"}, st)
	if r.Primary == "" {
		t.Fatalf("two close scores produced no primary at all: %+v", r.Scores)
	}
	if !r.Ambiguous {
		t.Fatalf("two scores within the margin were not flagged ambiguous: %+v", r.Scores)
	}

	clear := lx.Score(Item{Title: "alpha alpha", Body: "alpha"}, st)
	if clear.Ambiguous {
		t.Fatalf("a single-label item was flagged ambiguous: %+v", clear.Scores)
	}
}

func TestSecondaryRespectsTheCap(t *testing.T) {
	lx := testLexicon(t)
	st := loose()
	st.MaxSecondary = 1

	r := lx.Score(Item{
		Title:   "Kubernetes zero day: ransomware in the NPU driver",
		Summary: "An open source compiler and an npu are involved.",
	}, st)
	if r.Primary == "" {
		t.Fatalf("nothing was assigned: %+v", r.Scores)
	}
	if len(r.Secondary) > 1 {
		t.Fatalf("MaxSecondary=1 produced %d: %v", len(r.Secondary), r.Secondary)
	}
	for _, s := range r.Secondary {
		if s == r.Primary {
			t.Fatalf("the primary %q also appeared as a secondary", s)
		}
	}
}

func TestRegexTermMatches(t *testing.T) {
	lx := testLexicon(t)
	r := lx.Score(Item{Title: "CVE-2026-12345 lands in the wild"}, loose())
	if scoreOf(r, "security") == 0 {
		t.Fatalf("the CVE pattern did not match: %+v", r.Scores)
	}
	// The body is deliberately out of scope for regex.
	body := lx.Score(Item{Title: "A quiet day", Body: "CVE-2026-12345"}, loose())
	if scoreOf(body, "security") != 0 {
		t.Fatalf("a regex matched the body, which it must never scan: %+v", body.Scores)
	}
}

func TestExplainNamesTheTermsThatDidIt(t *testing.T) {
	lx := testLexicon(t)
	r := lx.Score(Item{Title: "Ransomware hits a Kubernetes cluster"}, loose())
	if r.Primary != "security" {
		t.Fatalf("primary was %q, wanted security: %+v", r.Primary, r.Scores)
	}
	ex := r.Explain()
	if len(ex) == 0 {
		t.Fatalf("no reason line for an assigned item")
	}
	if ex[0].Term != "ransomware" {
		t.Fatalf("the strongest match was %q, wanted ransomware: %+v", ex[0].Term, ex)
	}
	if ex[0].Field != FieldTitle {
		t.Fatalf("the match field was %v, wanted title", ex[0].Field)
	}
}

func TestBodyCanBeTurnedOff(t *testing.T) {
	lx := testLexicon(t)
	st := loose()
	st.UseBody = false
	r := lx.Score(Item{Body: strings.Repeat("kubernetes ransomware ", 20)}, st)
	if len(r.Scores) != 0 {
		t.Fatalf("body scored with UseBody=false: %+v", r.Scores)
	}
}

func TestEmptyItemAndEmptyLexicon(t *testing.T) {
	lx := testLexicon(t)
	if r := lx.Score(Item{}, DefaultStrategy()); r.Primary != "" || len(r.Scores) != 0 {
		t.Fatalf("an empty item produced %+v", r)
	}
	var nilLx *Lexicon
	if r := nilLx.Score(Item{Title: "anything"}, DefaultStrategy()); r.Primary != "" {
		t.Fatalf("a nil lexicon produced %+v", r)
	}
	empty, err := Compile(nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := empty.Score(Item{Title: "anything"}, DefaultStrategy()); r.Primary != "" {
		t.Fatalf("an empty lexicon produced %+v", r)
	}
}

func TestZeroStrategyStillScores(t *testing.T) {
	// A zero Strategy is what a caller gets by forgetting DefaultStrategy, and it
	// must degrade to something sane rather than silently scoring every item at
	// zero — which would look exactly like a broken lexicon.
	lx := testLexicon(t)
	r := lx.Score(Item{Title: "Kubernetes and ransomware everywhere"}, Strategy{MinScore: 0.01})
	if len(r.Scores) == 0 {
		t.Fatalf("a zero strategy scored nothing")
	}
}

func TestTruncWords(t *testing.T) {
	long := strings.Repeat("word ", 5000)
	got := len(strings.Fields(truncWords(long, 100)))
	if got > 101 {
		t.Fatalf("truncWords kept %d words for a limit of 100", got)
	}
	if got < 90 {
		t.Fatalf("truncWords kept only %d words for a limit of 100", got)
	}
	if truncWords("short text", 100) != "short text" {
		t.Fatalf("truncWords cut text that was already under the limit")
	}
}
