package classify

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/textvec"
)

// MaxTermWords is the longest term that can ever match.
//
// Three. The item's token stream is indexed as 1-, 2- and 3-grams, so a
// four-word term would be authored, stored, shown in the settings screen, and
// never fire — which is the worst kind of failure this package can have, because
// the reader concludes the feature is broken rather than that the term is too
// long. `Compile` refuses it instead.
const MaxTermWords = 3

// MaxUserRegex bounds how many regular expressions one label set may carry
// (§27.3a).
//
// Go's RE2 has no catastrophic backtracking, so a hostile pattern costs linear
// time rather than the ReDoS this would otherwise be. The cap is therefore about
// COST, not safety: each pattern is a full scan of the title, summary and URL,
// and thirty-two of them is already far past what an escape hatch should be
// carrying.
const MaxUserRegex = 32

// Term is one lexicon entry.
//
// Matched case-insensitively against the item's token stream as a 1-, 2- or
// 3-gram, so "machine learning" fires only when the two words are adjacent
// *within a sentence* — the scanner's sentence boundaries are honoured, because
// "…about Java. Islands in the…" is not a term.
type Term struct {
	// Text is the term as authored, or an RE2 pattern when Regex is set.
	Text string

	// Weight defaults to 1.0 when zero.
	//
	// Three bands, and the middle one should hold most of a lexicon: 2.0–3.0 for a
	// term that is nearly conclusive alone (`ransomware`, `box office`), 1.0–1.8
	// for strong but shared (`compiler`, `emissions`), 0.4–0.9 for merely
	// consistent (`platform`, `report`). A lexicon where everything is 1.0 has
	// thrown away the only tuning knob it has.
	Weight float64

	// Requires is a guard: this term scores nothing unless at least one of these
	// appears somewhere in the item, in any field.
	//
	// This is the mechanism §27.3c is about, and it is the difference between a
	// classifier people trust and one they turn off in a week. `apple` scores for
	// hardware only alongside `iphone`, `macos`, `cupertino` and friends; alone it
	// is a fruit. Guards are checked against the whole item rather than the same
	// field, because the companion evidence is usually in the body while the
	// ambiguous word is in the headline.
	Requires []string

	// Regex treats Text as an RE2 pattern, matched against the title, summary and
	// URL only — never the body, where the cost is unbounded and the precision is
	// worst.
	Regex bool
}

// Label is one category or one tag definition.
type Label struct {
	Slug string
	Name string
	// Terms are the positive evidence.
	Terms []Term
	// Exclude are negative: each match subtracts its weight × field multiplier,
	// floored at zero for the label as a whole.
	//
	// Symmetric with Terms rather than a separate veto mechanism, deliberately. A
	// veto list plus a subtract list is two things to reason about and two places
	// to look when a label misfires; one signed mechanism covers both, because an
	// exclude at weight 3.0 or more already outweighs anything a single positive
	// term can contribute.
	Exclude []Term
	// MinScore overrides the strategy floor for this label. Zero inherits.
	MinScore float64
	// Prompt is the Smart+ per-label instruction (§27.4c). Capped at
	// MaxPromptRunes and required to say what the label is NOT.
	Prompt string
}

// MaxPromptRunes caps a per-label prompt (§27.4c).
//
// The failure this prevents is silent and expensive: a reader tunes ten prompts,
// the eleventh pushes the request past the model's attention, and the earlier ones
// quietly stop working. A cap that is enforced at authoring time is the only kind
// that helps.
const MaxPromptRunes = 240

// Lexicon is a compiled label set: the index, built once and reused across every
// item in a batch.
//
// Compiling is what makes the scorer O(tokens) rather than O(labels × terms) —
// see the note on `index`. It is immutable after Compile and safe for concurrent
// use, which is what lets one lexicon serve every worker in the pool.
type Lexicon struct {
	labels []Label

	// index maps an n-gram key to every term that wants it.
	//
	// This is the decision §27.3a is about. The obvious implementation is a
	// `[]*regexp.Regexp` per label matched against each item, which at 26
	// categories × ~70 terms is ~1,800 scans per article and eleven million a day
	// on a modest feed set — for the tier that is supposed to be the cheap one.
	//
	// Inverting it costs one map and makes the per-item work one pass over the
	// token stream, independent of how many labels exist. Adding the 27th category
	// costs nothing per item, which is the property that lets a reader define
	// forty of their own without thinking about it.
	index map[string][]posting

	// regexes are the escape hatch, kept out of the index because they cannot be
	// looked up. Scanned linearly, which is exactly why MaxUserRegex exists.
	regexes []compiledRegex

	// guards holds the normalised Requires sets, parallel to the postings that
	// reference them. Normalised at compile time so the hot path is a map lookup
	// rather than a scan of the authored strings.
	guards [][]string

	// wanted is every key this lexicon could ever care about — term keys and
	// guard keys both.
	//
	// It exists so the scorer can ask "is this n-gram interesting" with ONE
	// alloc-free map lookup and drop it otherwise. Without it, the scorer would
	// have to materialise a string for every 1-, 2- and 3-gram of a 2,000-word
	// body just to discover that ~99.9% of them match nothing: six thousand
	// allocations per article, which is the difference between a scorer that runs
	// in the poll's shadow and one that becomes the poll.
	wanted map[string]struct{}

	// maxN is the longest term in the set, so the scorer stops generating n-grams
	// nobody asked for. A lexicon with no multi-word terms never builds a bigram.
	maxN int
}

// posting is one term's claim on an n-gram.
type posting struct {
	label int32
	// term indexes into Terms, or into Exclude when negative is set.
	term     int32
	negative bool
	// guard indexes into Lexicon.guards, or -1 for an unguarded term.
	guard int32
}

type compiledRegex struct {
	label    int32
	term     int32
	negative bool
	re       *regexp.Regexp
	weight   float64
	text     string
}

// Labels returns the compiled label set, in the order it was compiled.
func (lx *Lexicon) Labels() []Label { return lx.labels }

// Len reports how many labels are in the set.
func (lx *Lexicon) Len() int {
	if lx == nil {
		return 0
	}
	return len(lx.labels)
}

// Compile builds the index.
//
// It is strict, and it is strict for the reason `rules.Validate` is separate from
// `rules.Evaluate`: the two have opposite obligations. Scoring must never fail on
// one bad item out of a thousand, and authoring must fail loudly on the one bad
// term — because a term that silently never matches is indistinguishable from a
// term that is simply wrong, and the reader will spend an afternoon on the
// difference.
func Compile(labels []Label) (*Lexicon, error) {
	lx := &Lexicon{
		labels: make([]Label, 0, len(labels)),
		index:  make(map[string][]posting, len(labels)*80),
		wanted: make(map[string]struct{}, len(labels)*100),
		maxN:   1,
	}

	seenSlug := make(map[string]bool, len(labels))
	guardKey := make(map[string]int32)
	regexCount := 0

	for _, l := range labels {
		slug := strings.TrimSpace(l.Slug)
		if slug == "" {
			return nil, fmt.Errorf("classify: a label needs a slug")
		}
		if seenSlug[slug] {
			// Two labels with one slug is not a warning. Everything downstream —
			// the assignment row, the removal ledger, the reason line — keys on the
			// slug, so the second one would silently inherit the first one's
			// history.
			return nil, fmt.Errorf("classify: duplicate label slug %q", slug)
		}
		seenSlug[slug] = true
		if n := len([]rune(l.Prompt)); n > MaxPromptRunes {
			return nil, fmt.Errorf("classify: %q prompt is %d characters, max %d",
				slug, n, MaxPromptRunes)
		}

		idx := int32(len(lx.labels))
		lx.labels = append(lx.labels, l)

		if err := lx.addTerms(idx, slug, l.Terms, false, guardKey, &regexCount); err != nil {
			return nil, err
		}
		if err := lx.addTerms(idx, slug, l.Exclude, true, guardKey, &regexCount); err != nil {
			return nil, err
		}
	}

	// Deterministic posting order. Two labels claiming the same n-gram must be
	// visited in the same order every run, or two items with identical text can
	// produce different `Matches` ordering — which is invisible until a golden
	// test compares reason lines.
	for key := range lx.index {
		ps := lx.index[key]
		sort.SliceStable(ps, func(i, j int) bool {
			if ps[i].label != ps[j].label {
				return ps[i].label < ps[j].label
			}
			if ps[i].negative != ps[j].negative {
				return !ps[i].negative
			}
			return ps[i].term < ps[j].term
		})
	}
	return lx, nil
}

// MustCompile is Compile for a lexicon that ships with the build.
//
// The shipped taxonomy is a compile-time artefact (§27.3f) and an instance that
// cannot compile it is misbuilt, not misconfigured. Panicking at init is how that
// is discovered by the person who broke it rather than by a reader whose chips
// stopped appearing.
func MustCompile(labels []Label) *Lexicon {
	lx, err := Compile(labels)
	if err != nil {
		panic(err)
	}
	return lx
}

func (lx *Lexicon) addTerms(label int32, slug string, terms []Term, negative bool,
	guardKey map[string]int32, regexCount *int) error {

	seen := make(map[string]bool, len(terms))

	for i, t := range terms {
		weight := t.Weight
		if weight == 0 {
			weight = 1
		}
		if weight < 0 {
			// Negation is expressed by which list a term is in, not by its sign. A
			// negative weight in Terms would be a second, invisible way to say
			// Exclude, and the two would eventually disagree.
			return fmt.Errorf("classify: %q term %q has a negative weight; use Exclude",
				slug, t.Text)
		}

		if t.Regex {
			*regexCount++
			if *regexCount > MaxUserRegex {
				return fmt.Errorf("classify: more than %d regex terms; %q is over the cap",
					MaxUserRegex, t.Text)
			}
			// Case-insensitive, matching `rules.compile`'s choice and for its
			// reason: a person writing a pattern for "Rust" means the language
			// whether or not a headline shouts it.
			re, err := regexp.Compile("(?i)" + t.Text)
			if err != nil {
				return fmt.Errorf("classify: %q regex %q: %w", slug, t.Text, err)
			}
			lx.regexes = append(lx.regexes, compiledRegex{
				label: label, term: int32(i), negative: negative,
				re: re, weight: weight, text: t.Text,
			})
			continue
		}

		key := normalise(t.Text)
		if key == "" {
			return fmt.Errorf("classify: %q has a term that scans to nothing: %q", slug, t.Text)
		}
		n := strings.Count(key, " ") + 1
		if n > MaxTermWords {
			return fmt.Errorf("classify: %q term %q is %d words, max %d — it could never match",
				slug, t.Text, n, MaxTermWords)
		}
		if seen[key] {
			// Duplicates are not merged. The second one would be dead weight the
			// settings screen still displays and the reader still edits, which is
			// the same failure mode as a term that is too long.
			return fmt.Errorf("classify: %q has %q twice", slug, t.Text)
		}
		seen[key] = true
		if n > lx.maxN {
			lx.maxN = n
		}

		guard := int32(-1)
		if len(t.Requires) > 0 {
			g := make([]string, 0, len(t.Requires))
			for _, r := range t.Requires {
				if k := normalise(r); k != "" && strings.Count(k, " ")+1 <= MaxTermWords {
					g = append(g, k)
					if gn := strings.Count(k, " ") + 1; gn > lx.maxN {
						// A guard is looked up in the same n-gram set the terms
						// are, so a three-word guard forces trigram generation
						// exactly as a three-word term does.
						lx.maxN = gn
					}
				}
			}
			if len(g) == 0 {
				return fmt.Errorf("classify: %q term %q has a guard that scans to nothing", slug, t.Text)
			}
			sort.Strings(g)
			ck := strings.Join(g, "\x00")
			id, ok := guardKey[ck]
			if !ok {
				id = int32(len(lx.guards))
				lx.guards = append(lx.guards, g)
				guardKey[ck] = id
			}
			guard = id
		}

		lx.index[key] = append(lx.index[key], posting{
			label: label, term: int32(i), negative: negative, guard: guard,
		})
	}
	return nil
}

// scanWords is the one place text becomes words, for terms and for items alike.
//
// `textvec.Scan` rather than `textvec.Tokenize`: the interest layer's filter is
// wrong here in both directions — it drops everything under three characters,
// which is `ai`, `ev`, `ui`, `5g` and `f1`, and it drops stopwords, which turns
// the term "war on drugs" into "war drugs" and the term "state of the art" into
// a phrase nobody wrote. See `textvec/scan.go` for why the split is shared and
// the filter is not.
func scanWords(s string) []string {
	toks := textvec.Scan(s)
	if len(toks) == 0 {
		return nil
	}
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		out = append(out, t.Text)
	}
	return out
}
