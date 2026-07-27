package classify

import (
	"math"
	"sort"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/textvec"
)

// fieldBit is a field's place in the presence bitmask.
func fieldBit(f Field) uint8 { return 1 << uint(f) }

// lengthPivot is the body length at which normalisation starts to bite.
//
// Four hundred words. Below it an article is a link post and the divisor is
// close to 1; a 4,000-word feature divides by about 3.4. See `lengthNorm` for
// what this is and — more importantly — what it is not.
const lengthPivot = 400.0

// Score runs one item against the lexicon.
//
// # Distinct terms once, at their best field
//
// A term contributes exactly ONCE no matter how often it appears, at the highest
// field multiplier it was seen in. This is the measure that actually protects the
// ranking, and it replaces the log-damped repetition count the spec first
// sketched: without it, a 4,000-word article mentioning "kubernetes" eleven times
// outscores a 300-word one that is *about* Kubernetes, and long-form silently
// wins every category. Counting each term once removes the effect entirely rather
// than attenuating it, and it needs no tuning constant to do so.
//
// # Length normalisation changes the threshold, not the ranking
//
// The divisor is the same for every label, so it cannot reorder them. That is not
// a defect — it is the entire job. `MinScore` has to mean the same thing on a
// 300-word link post and a 4,000-word feature, and without normalisation the long
// piece clears any fixed floor on breadth alone while the short one never clears
// it at all. Anyone reading this expecting the divisor to affect which category
// wins should stop here: it never does, and a change that made it do so would be
// a change to what the floor means.
func (lx *Lexicon) Score(it Item, st Strategy) Result {
	if lx == nil || len(lx.labels) == 0 {
		return Result{}
	}

	present, bodyWords := lx.presence(it, st)
	accs := lx.accumulate(present)
	lx.applyRegex(it, st, accs)

	norm := lengthNorm(bodyWords, st)
	return assemble(lx, accs, st, norm)
}

// presence records, for every n-gram the lexicon cares about, which fields it
// appeared in.
//
// One map for the whole item rather than one per field: a term's best field is
// the only thing that matters downstream, and a bitmask answers that in one word
// per key. It is also what makes a guard check a single lookup — a guard asks
// "anywhere in the item", which is exactly "any bit set".
func (lx *Lexicon) presence(it Item, st Strategy) (map[string]uint8, int) {
	present := make(map[string]uint8, 64)

	lx.addField(present, it.Title, FieldTitle, 0)
	lx.addField(present, it.URL, FieldURL, 0)
	lx.addField(present, it.Summary, FieldSummary, 0)

	// The feed's title and the folder the reader filed it under are one field.
	// They are the same kind of evidence — a statement about the feed rather than
	// about the article — and they share the cap for that reason.
	src := it.SourceTitle
	if it.FolderName != "" {
		src = src + ". " + it.FolderName
	}
	lx.addField(present, src, FieldSource, 0)

	bodyWords := 0
	if st.UseBody && it.Body != "" {
		limit := st.BodyWords
		if limit <= 0 {
			limit = DefaultStrategy().BodyWords
		}
		bodyWords = lx.addField(present, it.Body, FieldBody, limit)
	}
	return present, bodyWords
}

// addField generates the 1-, 2- and 3-grams of one field and records the ones the
// lexicon wants. Returns how many words it scanned.
//
// The map lookup is `lx.wanted[string(buf)]`, which the compiler turns into a
// lookup with no allocation — the reason the key is assembled into a reused byte
// slice rather than with strings.Join. On a 2,000-word body that is the
// difference between six thousand allocations per article and roughly a dozen.
func (lx *Lexicon) addField(present map[string]uint8, text string, f Field, wordLimit int) int {
	if text == "" {
		return 0
	}
	if wordLimit > 0 {
		text = truncWords(text, wordLimit)
	}
	toks := textvec.Scan(text)
	if len(toks) == 0 {
		return 0
	}
	bit := fieldBit(f)

	// Sized for the longest key a lexicon may hold; grows on the rare overflow.
	buf := make([]byte, 0, 64)

	for i := range toks {
		buf = append(buf[:0], toks[i].Text...)
		if _, ok := lx.wanted[string(buf)]; ok {
			present[string(buf)] |= bit
		}
		for n := 2; n <= lx.maxN && i+n <= len(toks); n++ {
			// A sentence boundary ends every n-gram that would cross it. Without
			// this, "…runs on Java. Islands in the Pacific…" manufactures the term
			// "java islands", and every headline followed by a byline becomes a
			// phrase.
			if toks[i+n-1].BreakBefore {
				break
			}
			buf = append(buf, ' ')
			buf = append(buf, toks[i+n-1].Text...)
			if _, ok := lx.wanted[string(buf)]; ok {
				present[string(buf)] |= bit
			}
		}
	}
	return len(toks)
}

// termAcc is one term's contribution to one label.
type termAcc struct {
	// bestField is the highest-multiplier field this term was seen in.
	bestField Field
	// sourceOnly marks a term whose only evidence was the feed's own title or
	// folder, which is what the source cap applies to.
	sourceOnly bool
	seen       bool
}

// labelAcc accumulates one label's evidence.
type labelAcc struct {
	terms    map[int32]*termAcc
	excludes map[int32]*termAcc
}

// accumulate walks the present n-grams and books each term's best field.
//
// Map iteration order is deliberately not a problem here: every operation is a
// max or a set-insert, both commutative, and `assemble` sorts before returning.
// A scorer whose answer depended on map order would be non-deterministic in a way
// that only shows up as a flaky golden test months later.
func (lx *Lexicon) accumulate(present map[string]uint8) map[int32]*labelAcc {
	accs := make(map[int32]*labelAcc, 8)

	for key, fields := range present {
		postings, ok := lx.index[key]
		if !ok {
			// A guard key that is not itself anyone's term.
			continue
		}
		for _, p := range postings {
			if p.guard >= 0 && !guardSatisfied(lx.guards[p.guard], present) {
				continue
			}
			acc := accs[p.label]
			if acc == nil {
				acc = &labelAcc{terms: map[int32]*termAcc{}, excludes: map[int32]*termAcc{}}
				accs[p.label] = acc
			}
			bucket := acc.terms
			if p.negative {
				bucket = acc.excludes
			}
			ta := bucket[p.term]
			if ta == nil {
				ta = &termAcc{}
				bucket[p.term] = ta
			}
			mergeFields(ta, fields)
		}
	}
	return accs
}

// mergeFields records the best field a term was seen in.
//
// Best is by multiplier RANK rather than by the strategy's numbers, because the
// numbers are user-tunable and a reader who sets body above title has said
// something about weighting, not about which evidence is more specific. The rank
// is fixed: title, then url, then summary, then body, and source last because it
// is a statement about the feed rather than about the article.
func mergeFields(ta *termAcc, fields uint8) {
	order := [...]Field{FieldTitle, FieldURL, FieldSummary, FieldBody, FieldSource}
	for _, f := range order {
		if fields&fieldBit(f) == 0 {
			continue
		}
		if !ta.seen {
			ta.seen = true
			ta.bestField = f
			ta.sourceOnly = f == FieldSource
			return
		}
		return
	}
}

// guardSatisfied reports whether any of a term's required companions is present.
//
// Any rather than all: a guard list is a set of alternative confirmations —
// `apple` alongside any one of iphone, macos, cupertino — and requiring all of
// them would mean the term never fires.
func guardSatisfied(guard []string, present map[string]uint8) bool {
	for _, g := range guard {
		if present[g] != 0 {
			return true
		}
	}
	return false
}

// applyRegex runs the escape-hatch patterns.
//
// Title, summary and URL only, joined once. Never the body: the cost is
// unbounded there and the precision is at its worst, and a pattern that needs the
// body is a pattern that should have been a term.
func (lx *Lexicon) applyRegex(it Item, st Strategy, accs map[int32]*labelAcc) {
	if len(lx.regexes) == 0 {
		return
	}
	subject := it.Title + "\n" + it.Summary + "\n" + it.URL
	for _, cr := range lx.regexes {
		if !cr.re.MatchString(subject) {
			continue
		}
		acc := accs[cr.label]
		if acc == nil {
			acc = &labelAcc{terms: map[int32]*termAcc{}, excludes: map[int32]*termAcc{}}
			accs[cr.label] = acc
		}
		bucket := acc.terms
		if cr.negative {
			bucket = acc.excludes
		}
		// Scored at the title multiplier. A regex is authored for a specific,
		// high-precision shape — a CVE id, an RFC number — and the field it
		// happened to land in says less than the fact that it matched at all.
		bucket[cr.term] = &termAcc{seen: true, bestField: FieldTitle}
	}
}

// lengthNorm is the threshold calibration described on Score.
func lengthNorm(bodyWords int, st Strategy) float64 {
	if !st.UseBody || bodyWords <= 0 {
		return 1
	}
	return 1 + math.Log(1+float64(bodyWords)/lengthPivot)
}

// assemble turns the accumulators into the sorted, thresholded answer.
func assemble(lx *Lexicon, accs map[int32]*labelAcc, st Strategy, norm float64) Result {
	var res Result
	if norm <= 0 {
		norm = 1
	}

	for idx, acc := range accs {
		label := lx.labels[idx]
		var positive, sourceBucket float64
		matches := make([]Match, 0, len(acc.terms)+len(acc.excludes))

		for ti, ta := range acc.terms {
			t := label.Terms[ti]
			v := termWeight(t) * st.weight(ta.bestField)
			if ta.sourceOnly {
				sourceBucket += v
			} else {
				positive += v
			}
			matches = append(matches, Match{Term: t.Text, Field: ta.bestField, Value: v})
		}

		// The cap is applied to the bucket, not per term, because the thing being
		// bounded is how much the feed's identity may say about one article — and
		// six weak source matches are the same overreach as one strong one.
		cap := st.SourceCap
		if cap <= 0 {
			cap = DefaultStrategy().SourceCap
		}
		if sourceBucket > cap {
			sourceBucket = cap
		}
		total := positive + sourceBucket

		for ti, ta := range acc.excludes {
			t := label.Exclude[ti]
			v := termWeight(t) * st.weight(ta.bestField)
			total -= v
			matches = append(matches, Match{Term: t.Text, Field: ta.bestField, Value: -v})
		}

		if total <= 0 {
			// Floored at zero for the label as a whole rather than per term: a
			// label excluded into negative territory is simply absent, and a
			// ranking that distinguished degrees of absence would be inventing
			// information.
			continue
		}
		total /= norm

		// Largest absolute contribution first, then alphabetically, so the reason
		// line reads the same on every recomputation.
		sort.SliceStable(matches, func(i, j int) bool {
			ai, aj := math.Abs(matches[i].Value), math.Abs(matches[j].Value)
			if ai != aj {
				return ai > aj
			}
			return matches[i].Term < matches[j].Term
		})
		res.Scores = append(res.Scores, Score{Slug: label.Slug, Value: total, Matches: matches})
	}

	// Best first, tie-broken by slug. The tiebreak is what makes two items with
	// identical text produce identical answers regardless of map iteration order.
	sort.SliceStable(res.Scores, func(i, j int) bool {
		if res.Scores[i].Value != res.Scores[j].Value {
			return res.Scores[i].Value > res.Scores[j].Value
		}
		return res.Scores[i].Slug < res.Scores[j].Slug
	})

	if len(res.Scores) == 0 {
		return res
	}

	floor := func(slug string) float64 {
		for _, l := range lx.labels {
			if l.Slug == slug && l.MinScore > 0 {
				return l.MinScore
			}
		}
		if st.MinScore > 0 {
			return st.MinScore
		}
		return DefaultStrategy().MinScore
	}

	top := res.Scores[0]
	if top.Value < floor(top.Slug) {
		// Nothing cleared its floor. No primary, and therefore no secondary — the
		// item is Unsorted, which is a complete answer and not a failure.
		return res
	}
	res.Primary = top.Slug

	margin := st.Margin
	if margin <= 0 {
		margin = DefaultStrategy().Margin
	}
	if len(res.Scores) > 1 && res.Scores[1].Value*margin >= top.Value {
		// The free tier has an answer and is not confident in it. That is a
		// different state from having no answer, and it is the only thing the
		// margin does — see the note on Strategy.Margin.
		res.Ambiguous = true
	}

	maxSecondary := st.MaxSecondary
	if maxSecondary == 0 {
		maxSecondary = DefaultStrategy().MaxSecondary
	}
	for _, s := range res.Scores[1:] {
		if len(res.Secondary) >= maxSecondary {
			break
		}
		if s.Value < floor(s.Slug) {
			break
		}
		res.Secondary = append(res.Secondary, s.Slug)
	}
	return res
}

func termWeight(t Term) float64 {
	if t.Weight == 0 {
		return 1
	}
	return t.Weight
}

// truncWords cuts text to roughly n words without tokenising it.
//
// Cheap on purpose: this runs before the scanner so that a 50,000-word page —
// which the extractor does occasionally produce — costs a scan of its first two
// thousand words rather than of all of it. Counting transitions into a run of
// letters is close enough to a word count for a truncation nobody measures.
func truncWords(s string, n int) string {
	if n <= 0 {
		return s
	}
	count := 0
	inWord := false
	for i, r := range s {
		isWord := r == '\'' || r == '-' || isAlnum(r)
		switch {
		case isWord && !inWord:
			inWord = true
			count++
			if count > n {
				return s[:i]
			}
		case !isWord:
			inWord = false
		}
	}
	return s
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r > 127
}

// Slugs returns the label slugs in compile order, for tests and for the settings
// screen's ordering.
func (lx *Lexicon) Slugs() []string {
	if lx == nil {
		return nil
	}
	out := make([]string, 0, len(lx.labels))
	for _, l := range lx.labels {
		out = append(out, l.Slug)
	}
	return out
}

// Prompt returns a label's Smart+ instruction, or "" when it has none.
func (lx *Lexicon) Prompt(slug string) string {
	if lx == nil {
		return ""
	}
	for _, l := range lx.labels {
		if l.Slug == slug {
			return strings.TrimSpace(l.Prompt)
		}
	}
	return ""
}
