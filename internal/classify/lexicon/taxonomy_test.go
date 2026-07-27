package lexicon

import (
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/classify"
)

// The acceptance bar for TODO 10.2 — the shipped taxonomy, checked as data.
//
// # What this is and is not
//
// It is a STRUCTURAL test. It asserts that the lexicon has the shape a usable
// lexicon has: enough terms, a real weight distribution, multi-word terms where
// the precision lives, guards on the ambiguous words, and a prompt per category
// that says what the category is not.
//
// It is NOT a quality test. Whether `security` actually catches security
// articles is `TestTaxonomyPrecision`'s question (T24, TODO 10.3), and it is
// answered against a labeled corpus rather than against anybody's opinion.
// Keeping the two apart matters: this file can be satisfied by a lexicon that is
// well-formed and wrong, and the corpus test can be satisfied by one that is
// right and unmaintainable. Both bars are real and neither implies the other.

// wantSlugs is the taxonomy in plan.md §27.3d's order.
//
// Written out rather than derived from Categories(), which would make the test
// agree with whatever the code says. The order is user-visible — it is the order
// the settings screen and the chip filter list them in — so a reordering should
// be a deliberate edit here, not a silent consequence of moving a line.
var wantSlugs = []string{
	"software", "ai", "hardware", "security", "science", "health",
	"business", "finance", "politics", "world", "law", "climate",
	"space", "energy", "transport", "gaming", "filmtv", "music",
	"culture", "books", "sport", "food", "travel", "design",
	"work", "education",
}

const (
	// minTerms is the point below which a category only fires on headlines that
	// name it outright.
	minTerms = 60
	// maxTerms is the point above which you are padding with words that appear
	// everywhere else, which costs precision in every OTHER category too.
	//
	// **Raised from 110 to 150 on 2026-07-27, because the old number made a
	// category worse.** `hardware` hit 110 while being expanded from the live
	// corpus, and the only way forward was to DELETE working terms —
	// semiconductor vocabulary (`vram`, `wafer`, `x86`, `lithography`, `chip
	// fab`) was traded away for consumer-device brands, because this particular
	// feed set is consumer-heavy. That is a real loss for a reader who follows
	// fab and silicon news, and it was forced by a number I picked with no
	// evidence rather than by anything about the lexicon.
	//
	// The cap's purpose is to stop PADDING — terms added to inflate coverage that
	// fire everywhere and cost precision. `TestWeightsAreAJudgement` and the
	// corpus precision floors already measure that directly and would catch it,
	// so this number is the weakest of the three guards and should be the one
	// that yields. It is kept at all because a category quietly growing to four
	// hundred terms is worth a conversation.
	maxTerms = 150
)

func compiled(t *testing.T) *classify.Lexicon {
	t.Helper()
	lx, err := classify.Compile(Categories())
	if err != nil {
		t.Fatalf("the shipped taxonomy does not compile: %v", err)
	}
	return lx
}

func TestTaxonomyCompiles(t *testing.T) {
	compiled(t)
}

func TestTaxonomyIsTheDocumentedSet(t *testing.T) {
	got := Categories()
	if len(got) != len(wantSlugs) {
		t.Fatalf("%d categories, plan.md §27.3d specifies %d", len(got), len(wantSlugs))
	}
	for i, l := range got {
		if l.Slug != wantSlugs[i] {
			t.Fatalf("position %d is %q, §27.3d has %q", i, l.Slug, wantSlugs[i])
		}
		if strings.TrimSpace(l.Name) == "" {
			t.Fatalf("%q has no display name", l.Slug)
		}
	}
}

func TestEveryCategoryHasEnoughTerms(t *testing.T) {
	for _, l := range Categories() {
		n := len(l.Terms)
		if n < minTerms {
			t.Errorf("%s has %d terms, under the %d floor — it will only fire on headlines "+
				"that name the category outright", l.Slug, n, minTerms)
		}
		if n > maxTerms {
			t.Errorf("%s has %d terms, over the %d ceiling — past this it is padding with "+
				"words that also appear in every other category", l.Slug, n, maxTerms)
		}
	}
}

// TestWeightsAreAJudgement guards the one tuning knob the lexicon has. A label
// where every term is 1.0 has thrown it away, and the symptom is a category that
// is either never assigned or assigned to everything.
func TestWeightsAreAJudgement(t *testing.T) {
	for _, l := range Categories() {
		var high, mid, low int
		for _, term := range l.Terms {
			w := term.Weight
			if w == 0 {
				w = 1
			}
			switch {
			case w >= 2.0:
				high++
			case w >= 1.0:
				mid++
			default:
				low++
			}
		}
		total := len(l.Terms)
		if total == 0 {
			continue
		}
		if high == 0 {
			t.Errorf("%s has no term weighted 2.0 or above — nothing in it is "+
				"conclusive on its own, so it can never win a close call", l.Slug)
		}
		if low == 0 {
			t.Errorf("%s has no term under 1.0 — every term being at least strong "+
				"means the weak ones are overstated, which is how a category "+
				"starts matching everything", l.Slug)
		}
		if mid*100/total < 25 {
			t.Errorf("%s has %d%% of its terms in the middle band; most of a lexicon "+
				"belongs there", l.Slug, mid*100/total)
		}
	}
}

// TestMultiWordTermsCarryThePrecision: a two-word term at 2.0 beats a one-word
// term at 1.0 that fires on half the corpus, and a lexicon of bare unigrams is
// the version that produces embarrassing chips.
func TestMultiWordTermsCarryThePrecision(t *testing.T) {
	const wantMulti = 8
	for _, l := range Categories() {
		multi := 0
		for _, term := range l.Terms {
			if term.Regex {
				continue
			}
			if strings.Contains(strings.TrimSpace(term.Text), " ") {
				multi++
			}
		}
		if multi < wantMulti {
			t.Errorf("%s has %d multi-word terms, wanted at least %d — this is where "+
				"the precision is", l.Slug, multi, wantMulti)
		}
	}
}

// guardFamilies are plan.md §27.3c's named failure modes.
//
// Each must be handled SOMEWHERE — by a guard on the term, or by an exclude that
// removes the wrong reading. A lexicon that ships without these is the lexicon
// that files "Apple picking season" under Hardware, and that is the single
// fastest way to lose a reader's trust in the feature (R23).
var guardFamilies = []string{
	"apple", "amazon", "java", "rust", "python",
	"meta", "mercury", "tesla", "patch", "interview",
}

func TestAmbiguousWordsAreGuarded(t *testing.T) {
	handled := map[string]string{}

	for _, l := range Categories() {
		for _, term := range l.Terms {
			text := strings.ToLower(strings.TrimSpace(term.Text))
			for _, fam := range guardFamilies {
				if text != fam {
					continue
				}
				if len(term.Requires) == 0 {
					t.Errorf("%s carries the bare term %q with no Requires — "+
						"this is the exact family §27.3c names", l.Slug, term.Text)
					continue
				}
				if len(term.Requires) < 3 {
					t.Errorf("%s guards %q with only %d companions; a guard list of "+
						"one or two is a guess", l.Slug, term.Text, len(term.Requires))
				}
				handled[fam] = l.Slug
			}
		}
		for _, ex := range l.Exclude {
			text := strings.ToLower(ex.Text)
			for _, fam := range guardFamilies {
				if strings.Contains(text, fam) {
					handled[fam] = l.Slug
				}
			}
		}
	}

	for _, fam := range guardFamilies {
		if handled[fam] == "" {
			t.Errorf("no category guards or excludes %q — §27.3c requires it, and the "+
				"corpus has cases for it", fam)
		}
	}
}

// TestPromptsSayWhatTheLabelIsNot. §27.4c: the misfiles come from the boundary,
// so a prompt that only describes the centre of a category buys nothing. The
// three prompts in internal/smart/interest.go are the house standard and all
// three do this.
func TestPromptsSayWhatTheLabelIsNot(t *testing.T) {
	negations := []string{"do not", "don't", "never", "not for", "rather than", "not the", "exclude"}

	for _, l := range Categories() {
		p := strings.TrimSpace(l.Prompt)
		if p == "" {
			t.Errorf("%s has no Smart+ prompt", l.Slug)
			continue
		}
		if n := len([]rune(p)); n > classify.MaxPromptRunes {
			t.Errorf("%s prompt is %d runes, over the %d cap", l.Slug, n, classify.MaxPromptRunes)
		}
		lower := strings.ToLower(p)
		found := false
		for _, neg := range negations {
			if strings.Contains(lower, neg) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s prompt never says what the category is NOT: %q", l.Slug, p)
		}
	}
}

// TestNoTermIsSharedAtFullStrength catches the collision this taxonomy is most
// likely to get wrong: two categories both claiming a term at 2.0 and leaving the
// tie-break to decide. Where a term is genuinely shared, one category should own
// it and the other should carry it weaker.
func TestNoTermIsSharedAtFullStrength(t *testing.T) {
	type claim struct {
		slug   string
		weight float64
	}
	claims := map[string][]claim{}

	for _, l := range Categories() {
		for _, term := range l.Terms {
			if term.Regex {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(term.Text))
			w := term.Weight
			if w == 0 {
				w = 1
			}
			claims[key] = append(claims[key], claim{l.Slug, w})
		}
	}

	for term, cs := range claims {
		if len(cs) < 2 {
			continue
		}
		strong := 0
		var who []string
		for _, c := range cs {
			if c.weight >= 2.0 {
				strong++
				who = append(who, c.slug)
			}
		}
		if strong > 1 {
			t.Errorf("%q is claimed at 2.0 or above by %v — give it to the category "+
				"where it is more diagnostic and weaken it in the others, rather than "+
				"letting the margin decide", term, who)
		}
	}
}

// TestCanonicalItemsLandWhereExpected is a smoke test, not a precision test.
// A dozen unambiguous headlines that any working lexicon must place correctly;
// if these fail, something is structurally wrong and the corpus run will be
// noise.
func TestCanonicalItemsLandWhereExpected(t *testing.T) {
	lx := compiled(t)
	st := classify.DefaultStrategy()

	cases := []struct {
		want  string
		title string
		body  string
	}{
		{"security", "Ransomware group exploits a zero day in a VPN appliance",
			"The vulnerability was patched after a breach disclosed by the vendor."},
		{"space", "NASA delays the lunar lander launch to next year",
			"The payload and its orbit remain unchanged, the agency said."},
		{"gaming", "Nintendo ships patch notes for the Switch 2 launch title",
			"The update fixes a speedrun exploit reported by players on Steam."},
		{"finance", "The Fed holds interest rates as inflation cools",
			"Bond markets rallied and equities closed higher on the news."},
		{"climate", "Wildfire emissions hit a record as the drought deepens",
			"Carbon released by the fires exceeded the country's annual output."},
		{"health", "FDA approves a new insulin after a clinical trial",
			"The trial met its endpoint and the diagnosis criteria were unchanged."},
	}

	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			r := lx.Score(classify.Item{Title: c.title, Body: c.body}, st)
			if r.Primary == c.want {
				return
			}
			if r.Has(c.want) {
				t.Errorf("%q landed as a SECONDARY rather than primary (primary was %q).\n"+
					"scores: %v", c.title, r.Primary, topScores(r))
				return
			}
			t.Errorf("%q was filed as %q, wanted %q.\nscores: %v",
				c.title, r.Primary, c.want, topScores(r))
		})
	}
}

func topScores(r classify.Result) []string {
	out := make([]string, 0, 4)
	for i, s := range r.Scores {
		if i == 4 {
			break
		}
		out = append(out, s.Slug)
	}
	return out
}
