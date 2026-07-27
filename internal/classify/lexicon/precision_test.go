package lexicon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/classify"
)

// T24 — the corpus ratchet (plan.md §27.11, TODO 10.3).
//
// # Why this test is the one that matters
//
// Every other test in this package checks that the lexicon is well-FORMED. This
// is the only one that checks it is any GOOD, and it is the only thing standing
// between a taxonomy that improves and one that decays a well-meaning term at a
// time. A term added without a corpus case that motivates it is a guess; this is
// where the guess gets caught.
//
// **The floors only go up.** When a change improves a number, raise the floor in
// the same commit — a ratchet nobody tightens is a threshold, and a threshold
// drifts down. `TestTaxonomyPrecisionReport` prints how much headroom each metric
// has so that tightening is a two-minute job rather than an investigation.
//
// # What the corpus is
//
// `testdata/corpus.jsonl` — 302 items, 249 of them real articles pulled from a
// live feed database and 53 written to cover categories the (tech-heavy) feed set
// has no examples of. 46 of them have NO correct category, which is the group
// most likely to be skimped on and the only way to measure false assignment at
// all. See §27.11.
//
// # The metric that decides whether anyone trusts this
//
// Not accuracy. **False assignment** — the share of genuinely unsortable items
// that got a chip anyway. R23 is the risk that a wrong chip is worse than no
// chip, and accuracy over the items that HAVE an answer is exactly the number
// that hides it: a classifier that labels everything confidently can score well
// on accuracy while being unusable.

// corpusItem is one labeled article.
type corpusItem struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Summary       string   `json:"summary"`
	Source        string   `json:"source"`
	Body          string   `json:"body"`
	GoldPrimary   string   `json:"gold_primary"`
	GoldSecondary []string `json:"gold_secondary"`
	GoldTags      []string `json:"gold_tags"`
	Note          string   `json:"note"`
}

func loadCorpus(t *testing.T) []corpusItem {
	t.Helper()
	f, err := os.Open("testdata/corpus.jsonl")
	if err != nil {
		t.Fatalf("opening the corpus: %v", err)
	}
	defer f.Close()

	var out []corpusItem
	sc := bufio.NewScanner(f)
	// Bodies run to a few hundred words, which is past bufio's default line cap.
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var it corpusItem
		if err := json.Unmarshal([]byte(raw), &it); err != nil {
			t.Fatalf("corpus line %d is not valid JSON: %v", line, err)
		}
		out = append(out, it)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading the corpus: %v", err)
	}
	if len(out) < 250 {
		t.Fatalf("the corpus has only %d items; §27.11 specifies a few hundred, and a "+
			"corpus small enough to pass by luck is not a ratchet", len(out))
	}
	return out
}

// TestCorpusIsWellFormed checks the fixture before anything measures against it.
// A corpus with a typo'd slug silently lowers the score for a category that is
// working, and the investigation starts in the wrong place.
func TestCorpusIsWellFormed(t *testing.T) {
	corpus := loadCorpus(t)
	known := map[string]bool{}
	for _, l := range Categories() {
		known[l.Slug] = true
	}

	ids := map[string]bool{}
	unsortable := 0
	withSecondary := 0

	for _, it := range corpus {
		if it.ID == "" {
			t.Errorf("an item has no id: %q", it.Title)
		}
		if ids[it.ID] {
			t.Errorf("duplicate id %q", it.ID)
		}
		ids[it.ID] = true
		if strings.TrimSpace(it.Title) == "" {
			t.Errorf("%s has no title", it.ID)
		}

		if it.GoldPrimary == "" {
			unsortable++
			if len(it.GoldSecondary) > 0 {
				t.Errorf("%s has no primary but %d secondaries", it.ID, len(it.GoldSecondary))
			}
			continue
		}
		if !known[it.GoldPrimary] {
			t.Errorf("%s has unknown gold_primary %q", it.ID, it.GoldPrimary)
		}
		if len(it.GoldSecondary) > 0 {
			withSecondary++
		}
		if len(it.GoldSecondary) > 2 {
			t.Errorf("%s has %d secondaries, max 2", it.ID, len(it.GoldSecondary))
		}
		for _, s := range it.GoldSecondary {
			if !known[s] {
				t.Errorf("%s has unknown gold_secondary %q", it.ID, s)
			}
			if s == it.GoldPrimary {
				t.Errorf("%s repeats its primary %q as a secondary", it.ID, s)
			}
		}
	}

	// The unsortable group is the one that decays first, because it is the only
	// group nobody enjoys writing.
	if unsortable < 40 {
		t.Errorf("only %d items have no correct category; §27.11 wants at least 40, and "+
			"without them false assignment cannot be measured at all", unsortable)
	}
	if withSecondary < 5 {
		t.Errorf("only %d items carry a gold_secondary, so the secondary path is untested",
			withSecondary)
	}
}

// ---------------------------------------------------------------------------
// The ratchet
// ---------------------------------------------------------------------------

// Aggregate floors. Raise them whenever a change beats them.
//
// **Measured 2026-07-27 on the 302-item corpus** with the shipped lexicon and
// `DefaultStrategy`. Each floor sits a little under its measurement, because the
// corpus will grow and a floor set exactly at the measurement fails the next time
// somebody adds five hard items — which teaches people to lower floors, the one
// habit this test cannot survive.
const (
	// minAccuracy: of items that HAVE a correct category, the share whose primary
	// we got exactly right. Measured 0.594 after the 2026-07-27 corpus-driven
	// expansion (was 0.578 at the first calibration).
	minAccuracy = 0.57

	// minTopHit: the share where the correct category came back as the primary OR
	// a secondary. Softer than accuracy and it measures something different — a
	// gold answer sitting in second place is a lexicon that knows what the article
	// is about and disagrees about the section, which is a much smaller problem
	// than one that has no idea. Measured 0.637 (was 0.613).
	minTopHit = 0.61

	// maxFalseAssignment: of items with NO correct category, the share we gave one
	// to anyway. **The R23 metric**, and the one to drive down first when there is
	// a choice, because it is what a reader experiences as the feature being WRONG
	// rather than as the feature being incomplete. Measured 0.130 — six of the
	// forty-six unsortable items — unchanged across the expansion, which is the
	// result that mattered: 110 terms were added and precision did not move.
	maxFalseAssignment = 0.16

	// minPrecision is the per-category floor, applied only where the corpus has
	// enough support to mean anything. Measured minimum 0.600 (science, software).
	minPrecision = 0.55

	// minRecall applies to every category NOT in weakRecall below.
	minRecall = 0.45

	// minSupport is the corpus support below which per-category numbers move in
	// whole articles, and asserting on them tests the corpus rather than the
	// lexicon.
	minSupport = 8
)

// weakRecall names the categories that do not clear minRecall today, with the
// value each one measured.
//
// # Why a named list rather than a lower global floor
//
// A single floor low enough for `politics` at 0.125 would assert nothing about
// the other twenty-five, and lowering `minRecall` to accommodate three categories
// is how a ratchet becomes a threshold. This way the general bar stays where it
// should be and the exceptions are *visible, enumerated, and individually
// ratcheted* — each may not get worse, and the list may only shrink.
//
// **Deleting an entry when a category starts clearing minRecall is part of
// fixing it.** An entry that stays after the problem is gone is a permanently
// lowered bar for that category.
//
// What these three have in common is diagnostic: their vocabulary is diffuse.
// A politics article is about a named bill, a named person and a named country,
// and the words that make it politics — "election", "parliament" — often appear
// once each rather than in the clusters a term-count classifier needs. The
// intended fix is a per-label `MinScore` on exactly these (see the note on
// `classify.DefaultStrategy`), not a lower bar for everybody.
// **`transport` was removed 2026-07-27** after `electric vehicles` took its
// recall from 0.375 to 0.500. That deletion is the ratchet doing its job, and it
// is worth recording how close it came to not happening: the agent that mined the
// term added it, saw this test fail with "remove it from weakRecall", could not
// edit a test file, and REVERTED the improvement. The rule is right and the
// consequence was that a real coverage gain was thrown away for twenty minutes —
// so anyone who sees that failure should read it as "you have fixed something,
// now delete a line", never as "back this out".
var weakRecall = map[string]float64{
	"politics": 0.125,
	"science":  0.273,
}

type counts struct{ tp, fp, fn int }

func (c counts) precision() float64 {
	if c.tp+c.fp == 0 {
		return 1
	}
	return float64(c.tp) / float64(c.tp+c.fp)
}

func (c counts) recall() float64 {
	if c.tp+c.fn == 0 {
		return 1
	}
	return float64(c.tp) / float64(c.tp+c.fn)
}

type scoreboard struct {
	perCategory map[string]*counts
	support     map[string]int

	graded      int
	correct     int
	topHit      int
	unsorted    int
	falseAssign int

	// missed records the worst confusions, for the report.
	confusion map[string]int
}

func grade(t *testing.T) (*scoreboard, []corpusItem, []classify.Result) {
	t.Helper()
	corpus := loadCorpus(t)
	lx, err := classify.Compile(Categories())
	if err != nil {
		t.Fatalf("the shipped taxonomy does not compile: %v", err)
	}
	st := classify.DefaultStrategy()

	sb := &scoreboard{
		perCategory: map[string]*counts{},
		support:     map[string]int{},
		confusion:   map[string]int{},
	}
	for _, l := range Categories() {
		sb.perCategory[l.Slug] = &counts{}
	}

	results := make([]classify.Result, len(corpus))
	for i, it := range corpus {
		r := lx.Score(classify.Item{
			Title:       it.Title,
			URL:         it.URL,
			Summary:     it.Summary,
			SourceTitle: it.Source,
			Body:        it.Body,
		}, st)
		results[i] = r

		if it.GoldPrimary == "" {
			sb.unsorted++
			if r.Primary != "" {
				sb.falseAssign++
				sb.confusion["(none)→"+r.Primary]++
				if c := sb.perCategory[r.Primary]; c != nil {
					c.fp++
				}
			}
			continue
		}

		sb.graded++
		sb.support[it.GoldPrimary]++

		switch {
		case r.Primary == it.GoldPrimary:
			sb.correct++
			sb.topHit++
			sb.perCategory[it.GoldPrimary].tp++
		default:
			sb.perCategory[it.GoldPrimary].fn++
			if r.Primary != "" {
				if c := sb.perCategory[r.Primary]; c != nil {
					c.fp++
				}
				sb.confusion[it.GoldPrimary+"→"+r.Primary]++
			} else {
				sb.confusion[it.GoldPrimary+"→(none)"]++
			}
			if r.Has(it.GoldPrimary) {
				sb.topHit++
			}
		}
	}
	return sb, corpus, results
}

func TestTaxonomyPrecision(t *testing.T) {
	sb, _, _ := grade(t)

	accuracy := ratio(sb.correct, sb.graded)
	topHit := ratio(sb.topHit, sb.graded)
	falseAssign := ratio(sb.falseAssign, sb.unsorted)

	t.Logf("graded %d · accuracy %.3f · top-hit %.3f | unsortable %d · false assignment %.3f",
		sb.graded, accuracy, topHit, sb.unsorted, falseAssign)

	if accuracy < minAccuracy {
		t.Errorf("accuracy %.3f is under the %.2f floor (%d of %d correct)",
			accuracy, minAccuracy, sb.correct, sb.graded)
	}
	if topHit < minTopHit {
		t.Errorf("top-hit %.3f is under the %.2f floor", topHit, minTopHit)
	}
	if falseAssign > maxFalseAssignment {
		t.Errorf("false assignment %.3f is over the %.2f ceiling (%d of %d unsortable items "+
			"got a chip) — this is the R23 metric and it is the one a reader experiences",
			falseAssign, maxFalseAssignment, sb.falseAssign, sb.unsorted)
	}

	slugs := make([]string, 0, len(sb.perCategory))
	for s := range sb.perCategory {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)

	for _, slug := range slugs {
		c := sb.perCategory[slug]
		if sb.support[slug] < minSupport {
			continue
		}
		if p := c.precision(); p < minPrecision {
			t.Errorf("%s precision %.3f under the %.2f floor (%d right, %d wrong)",
				slug, p, minPrecision, c.tp, c.fp)
		}

		r := c.recall()
		known, isWeak := weakRecall[slug]
		switch {
		case !isWeak:
			if r < minRecall {
				t.Errorf("%s recall %.3f under the %.2f floor (%d found, %d missed). "+
					"If this category is genuinely diffuse, give it a per-label MinScore "+
					"rather than adding it to weakRecall — the list is for what is already "+
					"broken, not a place to put new breakage", slug, r, minRecall, c.tp, c.fn)
			}
		case r < known-0.001:
			t.Errorf("%s recall %.3f is worse than its recorded %.3f — a category on the "+
				"weak list may not degrade further", slug, r, known)
		case r >= minRecall:
			// The ratchet's other half, and the half that is usually forgotten: an
			// exception that outlives the problem is a permanently lowered bar.
			t.Errorf("%s now clears the %.2f recall floor at %.3f — remove it from "+
				"weakRecall in this commit", slug, minRecall, r)
		}
	}

	for slug := range weakRecall {
		if _, ok := sb.perCategory[slug]; !ok {
			t.Errorf("weakRecall names %q, which is not a category", slug)
		}
	}
}

// TestTaxonomyPrecisionReport never fails. It prints the table.
//
// Separate from the ratchet so that a failing run shows the ONE number that
// broke rather than sixty lines of context, and so that `-run Report` is the way
// to see where the headroom is when it is time to tighten the floors. A ratchet
// with no report is a ratchet nobody tightens.
func TestTaxonomyPrecisionReport(t *testing.T) {
	sb, _, _ := grade(t)

	var b strings.Builder
	b.WriteString("\n  category      support  prec   recall\n")
	b.WriteString("  ------------  -------  -----  ------\n")

	slugs := make([]string, 0, len(sb.perCategory))
	for s := range sb.perCategory {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		c := sb.perCategory[slug]
		mark := " "
		if sb.support[slug] < minSupport {
			mark = "*"
		}
		fmt.Fprintf(&b, "  %-12s %s%6d  %.3f  %.3f\n",
			slug, mark, sb.support[slug], c.precision(), c.recall())
	}
	b.WriteString("  (* under the support threshold; not asserted)\n\n")

	fmt.Fprintf(&b, "  accuracy          %.3f  (floor %.2f, headroom %+.3f)\n",
		ratio(sb.correct, sb.graded), minAccuracy, ratio(sb.correct, sb.graded)-minAccuracy)
	fmt.Fprintf(&b, "  top-hit           %.3f  (floor %.2f, headroom %+.3f)\n",
		ratio(sb.topHit, sb.graded), minTopHit, ratio(sb.topHit, sb.graded)-minTopHit)
	fmt.Fprintf(&b, "  false assignment  %.3f  (ceiling %.2f, headroom %+.3f)\n",
		ratio(sb.falseAssign, sb.unsorted), maxFalseAssignment,
		maxFalseAssignment-ratio(sb.falseAssign, sb.unsorted))

	type conf struct {
		pair string
		n    int
	}
	var cs []conf
	for k, v := range sb.confusion {
		cs = append(cs, conf{k, v})
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].n != cs[j].n {
			return cs[i].n > cs[j].n
		}
		return cs[i].pair < cs[j].pair
	})
	b.WriteString("\n  worst confusions (gold→predicted):\n")
	for i, c := range cs {
		if i == 12 {
			break
		}
		fmt.Fprintf(&b, "    %-34s %d\n", c.pair, c.n)
	}
	t.Log(b.String())
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
