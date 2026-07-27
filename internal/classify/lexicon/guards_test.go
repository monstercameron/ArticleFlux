package lexicon

import (
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/classify"
)

// TestGuardTerms is TODO 10.2's acceptance bar, and it is behavioural rather
// than structural (plan.md §27.3c).
//
// `TestAmbiguousWordsAreGuarded` in taxonomy_test.go checks that a guard EXISTS.
// That is not the same claim and it is much weaker: a guard list can exist and
// still be the wrong list, or be attached to a term the item never matches, or
// be defeated by a second unguarded term in the same category. This file scores
// real sentences and asserts where they land.
//
// # Why these ten and not others
//
// Every one is a measured failure mode of lexical classification, not a
// hypothetical. They are the headlines that make a reader stop trusting the
// chips — "Apple picking season" under Hardware is not a small error, it is the
// error that gets the feature turned off (R23). The corpus test (T24, TODO 10.3)
// measures the aggregate; this measures the specific cases the aggregate is
// allowed to average away.
//
// # The assertion is "not assigned at all", not "not primary"
//
// A secondary category renders a chip too. An article about an orchard carrying
// a faint Hardware chip is the same failure as one filed under Hardware, just
// quieter — and quieter failures are the ones that survive.

type guardCase struct {
	name string
	// wrong is the reading the lexicon must NOT produce.
	wrong string
	// right is the category the correct reading must land in. Empty means the
	// item is genuinely unsortable and must get no primary at all.
	right   string
	title   string
	summary string
	body    string
}

func TestGuardTerms(t *testing.T) {
	lx, err := classify.Compile(Categories())
	if err != nil {
		t.Fatalf("the shipped taxonomy does not compile: %v", err)
	}
	st := classify.DefaultStrategy()

	cases := []guardCase{
		{
			name:  "apple the fruit is not hardware",
			wrong: "hardware",
			title: "Apple picking season opens at the orchard",
			body: "The apple harvest looks strong this year. Growers say the cider " +
				"press will run through October, and the farm stand opens on Saturday.",
		},
		{
			name:  "apple the company is hardware",
			wrong: "food",
			right: "hardware",
			title: "Apple ships a new iPhone with a faster chip",
			body: "The iPhone was announced in Cupertino. macOS gains the same silicon " +
				"later this year, and the M-series soc moves to a smaller process node.",
		},
		{
			name:  "the amazon is a rainforest, not a company",
			wrong: "business",
			right: "climate",
			title: "Deforestation in the Amazon slows for a third year",
			body: "Conservation groups credit enforcement. The rainforest absorbs carbon " +
				"and its biodiversity is among the richest on earth; drought remains a risk.",
		},
		{
			name:  "amazon the company is business",
			wrong: "climate",
			right: "business",
			title: "Amazon reports quarterly revenue above expectations",
			body: "The retailer's cloud unit drove growth. Its ceo said hiring would slow " +
				"after last year's layoffs, and the earnings beat lifted the stock.",
		},
		{
			// The load-bearing half is `wrong`. That this lands in travel is the
			// happy outcome and not the point — the point is that the island does
			// not reach Software.
			name:  "a beach in Java is not software",
			wrong: "software",
			right: "travel",
			title: "A week on the beaches of Java",
			body: "The island's volcanoes make a dramatic backdrop. Our itinerary took in " +
				"a national park, three hostels and a scenic drive along the south coast.",
		},
		{
			name:  "java the language is software",
			wrong: "travel",
			right: "software",
			title: "Java 26 lands with virtual threads on by default",
			body: "The jvm release changes the default for the compiler. Maven and gradle " +
				"builds need no change, and the programming language keeps source compatibility.",
		},
		{
			name:  "the rust belt is not software",
			wrong: "software",
			title: "Rust Belt towns bet on a new factory",
			body: "The plant will employ 900 people. Local hiring has lagged since the " +
				"steel mills closed, and the state offered a tax break to land it.",
		},
		{
			name:  "rust the language is software",
			wrong: "work",
			right: "software",
			title: "Rust 1.99 stabilises the new borrow checker",
			body: "Cargo gains a workspace flag. The compiler team says the crate ecosystem " +
				"needs no change, and memory safe code compiles unmodified.",
		},
		{
			name:  "the snake is not software",
			wrong: "software",
			title: "A python swallowed a heat lamp at the zoo",
			body: "Keepers say the snake is recovering. The reptile house reopens on Friday " +
				"and the species is native to the region.",
		},
		{
			name:  "python the language is software",
			wrong: "science",
			right: "software",
			title: "Python 3.16 removes the global interpreter lock",
			body: "The runtime change lands after a long deprecation. Pip and the standard " +
				"library are unaffected; the programming language keeps its syntax.",
		},
		{
			// `science` and `health` are both defensible here and the lexicon
			// picks science, because "meta analysis" is a research-methods term
			// and it is in the title. Asserted as science rather than argued
			// about: the guard family under test is *the word* meta against *the
			// company* Meta, and `wrong` is what carries this case.
			name:  "a meta-analysis is not the company",
			wrong: "business",
			right: "science",
			title: "A meta analysis of statin trials finds a smaller effect",
			body: "The peer reviewed study pooled 41 clinical trial results. Researchers " +
				"say the diagnosis threshold may need revisiting; the fda has not commented.",
		},
		{
			name:  "meta the company is business",
			wrong: "science",
			right: "business",
			title: "Meta reports advertising revenue growth",
			body: "The company said its quarterly earnings beat guidance. Facebook and " +
				"instagram drove the increase, and the ceo confirmed hiring plans.",
		},
		{
			name:  "mercury in retrograde is not space",
			wrong: "space",
			title: "Mercury is in retrograde and everyone has an opinion",
			body: "Astrology apps report a surge in downloads whenever the phrase trends. " +
				"Believers blame it for missed trains and unanswered texts.",
		},
		{
			name:  "mercury the planet is space",
			wrong: "culture",
			right: "space",
			title: "A probe enters orbit around Mercury",
			body: "The spacecraft will map the planet's surface. Its instruments survived " +
				"the launch and the agency says the payload is healthy.",
		},
		{
			name:  "nikola tesla is not the car company",
			wrong: "business",
			title: "The Tesla coil that lit a room without wires",
			body: "The inventor's 1899 experiments in Colorado Springs are the subject of " +
				"a new museum exhibition, curated from his surviving notebooks.",
		},
		{
			name:  "tesla the company is business",
			wrong: "culture",
			right: "business",
			title: "Tesla misses quarterly delivery targets",
			body: "The carmaker said its revenue fell short. Its ceo blamed a factory " +
				"shutdown, and the stock dropped after the earnings call.",
		},
		{
			name:  "patch notes are gaming, not security",
			wrong: "security",
			right: "gaming",
			title: "Patch notes for the new season land ahead of the update",
			body: "The developer nerfed two weapons and fixed a speedrun exploit. Steam " +
				"players get the update first; the console build follows next week.",
		},
		{
			name:  "a security patch is security",
			wrong: "gaming",
			right: "security",
			title: "Emergency patch closes a zero day exploited in the wild",
			body: "The vulnerability allowed remote code execution. Administrators should " +
				"apply the update immediately; a breach at one victim is already confirmed.",
		},
		{
			name:  "an interview with a director is not a job story",
			wrong: "work",
			right: "filmtv",
			title: "An interview with the director of this year's best picture nominee",
			body: "We spoke about the screenplay, the cast, and why the film sat in " +
				"development for six years before a studio picked it up.",
		},
		{
			name:  "a job interview is work",
			wrong: "filmtv",
			right: "work",
			title: "The interview loop is broken and everyone knows it",
			body: "Six rounds, a take home exercise and a panel. Hiring managers say the " +
				"process filters for stamina; candidates call it unpaid labor.",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := lx.Score(classify.Item{Title: c.title, Summary: c.summary, Body: c.body}, st)

			if c.wrong != "" && r.Has(c.wrong) {
				where := "secondary"
				if r.Primary == c.wrong {
					where = "PRIMARY"
				}
				t.Errorf("the wrong reading won: %q assigned as %s\n  title:  %q\n  scores: %s\n  why:    %s",
					c.wrong, where, c.title, top(r), why(r, c.wrong))
			}

			switch c.right {
			case "":
				if r.Primary != "" {
					t.Errorf("an unsortable item was filed under %q\n  title:  %q\n  scores: %s",
						r.Primary, c.title, top(r))
				}
			default:
				if r.Primary != c.right {
					t.Errorf("primary was %q, wanted %q\n  title:  %q\n  scores: %s\n  why:    %s",
						r.Primary, c.right, c.title, top(r), why(r, r.Primary))
				}
			}
		})
	}
}

// top renders the leading scores, so a failure says what beat what rather than
// only that it failed.
func top(r classify.Result) string {
	var b strings.Builder
	for i, s := range r.Scores {
		if i == 4 {
			break
		}
		if i > 0 {
			b.WriteString(" · ")
		}
		b.WriteString(s.Slug)
		b.WriteString(" ")
		b.WriteString(round(s.Value))
	}
	if b.Len() == 0 {
		return "(nothing scored)"
	}
	if r.Ambiguous {
		b.WriteString("  [ambiguous]")
	}
	return b.String()
}

// why names the terms behind a label, which is what turns a failing guard test
// into a one-line lexicon fix instead of an afternoon.
func why(r classify.Result, slug string) string {
	if slug == "" {
		return "(no primary)"
	}
	for _, s := range r.Scores {
		if s.Slug != slug {
			continue
		}
		var b strings.Builder
		for i, m := range s.Matches {
			if i == 5 {
				b.WriteString(", …")
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(m.Term)
			b.WriteString("@")
			b.WriteString(m.Field.String())
			b.WriteString(" ")
			b.WriteString(round(m.Value))
		}
		return b.String()
	}
	return "(not scored)"
}

func round(f float64) string {
	neg := ""
	if f < 0 {
		neg, f = "-", -f
	}
	whole := int(f)
	frac := int((f-float64(whole))*10 + 0.5)
	if frac == 10 {
		whole++
		frac = 0
	}
	return neg + itoa(whole) + "." + itoa(frac)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
