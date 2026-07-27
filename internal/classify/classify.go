// Package classify scores an article against a label set (plan.md §27.3, TODO 10.1).
//
// # Pure, and that is the whole design
//
// `(Item, *Lexicon, Strategy) → Result`. No database, no clock, no network, no
// logging, no side effects. It is the same shape as `internal/rules` and it is
// pure for the same reason §13.4 gives about rules: **the settings screen's live
// preview must be the same code as the apply.** A preview that is a second
// implementation promising to behave identically is a preview that will one day
// lie about what a term is going to do, which is worse than showing nothing.
//
// # A category is a place; a tag is a claim
//
// Both are `Label`s here and the machinery is identical — the difference is
// vocabulary size, threshold, and what the caller does with the answer (§27.1).
// Categories are ~26, generic, and comparable to each other. Tags are open,
// focused, and need more evidence before they are worth asserting. That is a
// `Strategy`, not a second scorer.
//
// # Refusing is an answer
//
// `Result.Primary` is empty when nothing cleared the floor, and that is the
// common, correct outcome for a large fraction of any real feed. An item with no
// chip costs nothing; a wrong chip costs trust (R23). `topics.MinMembers`,
// `topics.ColdStart` and the discovery ladder's `ErrNoRule` all make the same
// choice, and this is the surface where guessing is most visible.
//
// # What this package does NOT do
//
// It does not know about users, tenants, categories-as-rows, tags, jobs, or the
// model. It scores text against labels. `internal/pipeline` owns everything else,
// and keeping that line is what lets the throughput test (T24's neighbour) be a
// statement about the scorer rather than about SQLite.
package classify

import "strings"

// Field is where in an item a term matched.
//
// A closed set rather than a string, because the multipliers in `Strategy` are
// indexed by it and a typo in a field name would silently score zero.
type Field uint8

const (
	// FieldTitle is the headline.
	FieldTitle Field = iota
	// FieldURL is the address, scanned as words — `/technology/` in a path is a
	// publisher telling you their own section.
	FieldURL
	// FieldSummary is the feed's own description.
	FieldSummary
	// FieldSource is the feed's title, plus the folder it is filed under.
	FieldSource
	// FieldBody is the article text.
	FieldBody

	numFields = 5
)

// String names a field for the reason line and for test failures.
func (f Field) String() string {
	switch f {
	case FieldTitle:
		return "title"
	case FieldURL:
		return "url"
	case FieldSummary:
		return "summary"
	case FieldSource:
		return "source"
	case FieldBody:
		return "body"
	}
	return "unknown"
}

// Item is the subset of an article the scorer can see.
//
// A struct rather than an interface, for the reason `rules.Item` is one: the
// field set is closed by §27.3b, and an interface would invite a caller to
// compute a field lazily — at which point a "pure" scorer is running arbitrary
// caller code and the preview stops being reproducible.
//
// Body is PLAIN TEXT, not HTML. A term matching against raw markup hits link
// hrefs, CSS class names and image filenames, none of which is what the person
// who wrote the term meant and all of which are invisible when they check the
// article. `sanitize.Text` is the converter; this package does not do it, so that
// the cost is paid once per item in the pipeline rather than once per label set.
type Item struct {
	Title       string
	URL         string
	Summary     string
	SourceTitle string
	// FolderName joins SourceTitle in FieldSource. A reader who filed a feed under
	// "Security" has said something about its articles, and it is the same kind of
	// weak, capped evidence the feed's own title is.
	FolderName string
	Body       string
}

// Strategy is every tunable in the scorer.
//
// It is a value, passed in, and never read from a global: the settings screen
// previews a modified strategy against real items before saving it (§27.6), and
// a package-level default would make that preview mutate the running instance.
type Strategy struct {
	// FieldWeights multiplies a term's weight by where it matched. Index by Field.
	FieldWeights [numFields]float64

	// MinScore is the floor a label must clear to be assigned at all. A label's
	// own MinScore overrides this.
	MinScore float64

	// Margin is how far ahead of the runner-up the top label must be before the
	// answer is considered settled. Falling short does NOT block the assignment —
	// it sets Result.Ambiguous, which is what §27.4a escalates to the model.
	//
	// This is a correction to §27.3b as first drafted, and it is worth stating
	// because the original reads more cautious and is worse: requiring the margin
	// for the primary meant a CVE in a game engine, scoring 8.0 security against
	// 7.0 software, produced NO category at all. Two strong signals disagreeing
	// about the section is not the same as no signal, and collapsing them to
	// Unsorted throws away a confident read of what the article is about in order
	// to avoid choosing between two correct answers.
	Margin float64

	// MaxSecondary bounds the extra labels carried alongside the primary.
	MaxSecondary int

	// SourceCap bounds the TOTAL contribution of terms that matched only in the
	// feed's title or folder.
	//
	// Uncapped, a feed named "Ars Technica" biases every one of its articles
	// toward hardware, which is usually right and is exactly how the politics
	// piece on Ars gets misfiled. The cap keeps the feed's identity as a tiebreak
	// rather than as evidence.
	SourceCap float64

	// UseBody includes the article text. Off is a real configuration — it is the
	// middle lever in §27.12's cost list — and it costs precision on long-form.
	UseBody bool

	// BodyWords truncates the body. Beyond a couple of thousand words is comment
	// furniture, related-links blocks and the site's own footer, which is
	// vocabulary from every category at once.
	BodyWords int
}

// DefaultStrategy is the shipped configuration.
//
// # MinScore, calibrated 2026-07-27 against the 302-item corpus
//
// It shipped as a placeholder and it survived the calibration, which is worth
// writing down with the table rather than leaving as a number that looks
// unexamined. `TestCalibrationSweep` in internal/classify/lexicon produces this:
//
//	MinScore  accuracy  top-hit  false-assign  refused
//	    1.00     0.691    0.789         0.391    0.125
//	    1.50     0.668    0.738         0.326    0.172
//	    2.00     0.641    0.691         0.261    0.211
//	    2.50     0.621    0.664         0.261    0.254
//	    3.00     0.578    0.613         0.130    0.305   ← shipped
//	    3.50     0.527    0.551         0.130    0.363
//
// Two things in that table decided it.
//
// **2.25 and 2.50 are strictly dominated by 2.00** — identical false assignment,
// worse accuracy. The curve is not smooth, so "pick the middle" would have landed
// on a setting that is worse than one of its neighbours in every dimension.
//
// **The real choice is 2.00 against 3.00, and it is close to one-for-one.**
// Moving to 2.00 buys about sixteen more correct chips across the corpus and
// costs about fourteen more wrong ones. R23's premise is exactly that this trade
// is a losing one: a wrong chip costs more trust than a right chip earns, because
// the reader who sees "Apple picking season" under Hardware stops reading the
// chips entirely. Refusing is also not a dead end — an unsorted item is precisely
// what §27.4a escalates to the model, so the higher floor routes work rather than
// discarding it.
//
// The cost is real and is recorded rather than glossed: at 3.00 the free tier
// declines to place **30%** of the articles that have a correct answer. The fix
// for that is not a lower global bar — it is per-label `MinScore` on the
// categories whose vocabulary is genuinely diffuse (politics, science, transport
// are the measured ones), which is what `Label.MinScore` exists for and what the
// weak-recall ratchet in `precision_test.go` tracks.
//
// Margin does not gate anything, so it cannot appear in that table. Its effect is
// on escalation volume: 1.35 flags **4.6%** of the corpus ambiguous.
func DefaultStrategy() Strategy {
	var fw [numFields]float64
	// A word in a headline was chosen; a word in the body may be an aside. The
	// URL sits second because a publisher's own path segment — /technology/,
	// /markets/ — is the single highest-precision signal available for free, and
	// it is short enough that a false positive is rare.
	fw[FieldTitle] = 3.0
	fw[FieldURL] = 2.0
	fw[FieldSummary] = 2.0
	fw[FieldSource] = 1.0
	fw[FieldBody] = 1.0

	return Strategy{
		FieldWeights: fw,
		MinScore:     3.0,
		Margin:       1.35,
		MaxSecondary: 2,
		SourceCap:    1.5,
		UseBody:      true,
		BodyWords:    2000,
	}
}

// weight returns the multiplier for a field, defaulting to 1 so a zero-valued
// Strategy scores rather than silently returning nothing for every item.
func (s Strategy) weight(f Field) float64 {
	if int(f) >= numFields || s.FieldWeights[f] == 0 {
		return 1
	}
	return s.FieldWeights[f]
}

// Match is one term's contribution, kept for the reason line (§18.9).
//
// Explainability is the product: "matched `ransomware` in the title" is a reason
// a reader can act on, and a bare chip is not. It is also the only practical way
// to debug a lexicon — the settings screen shows exactly which term pulled an
// article into a category, which is what turns "this is wrong" into "remove that
// term".
type Match struct {
	// Term is the lexicon entry as authored.
	Term string
	// Field is the best field it matched in — the one whose multiplier was used.
	Field Field
	// Value is what it contributed, after the field multiplier. Negative for an
	// exclude.
	Value float64
}

// Score is one label's outcome.
type Score struct {
	Slug string
	// Value is the final score, after excludes, the source cap and length
	// normalisation.
	Value float64
	// Matches are the contributing terms, largest absolute contribution first.
	Matches []Match
}

// Result is the outcome of scoring one item against one lexicon.
type Result struct {
	// Primary is the assigned label's slug, or "" when nothing cleared its floor.
	// Empty is a correct and common answer — see the package comment.
	Primary string
	// Secondary are the additional labels that cleared their floor, best first.
	Secondary []string
	// Ambiguous is set when the runner-up was within Margin of the primary. This
	// is the escalation signal (§27.4a): the free tier has an answer and is not
	// confident in it, which is a different state from having no answer.
	Ambiguous bool
	// Scores are every label that scored above zero, best first, tie-broken by
	// slug so the order is stable across runs and across database row order.
	Scores []Score
}

// Explain returns the terms behind the primary assignment, best first.
//
// Nil when there is no primary, which the caller must handle: an unsorted item
// has no reason line because it has nothing to explain.
func (r Result) Explain() []Match {
	if r.Primary == "" {
		return nil
	}
	for _, s := range r.Scores {
		if s.Slug == r.Primary {
			return s.Matches
		}
	}
	return nil
}

// Has reports whether a slug was assigned, primary or secondary.
func (r Result) Has(slug string) bool {
	if r.Primary == slug {
		return true
	}
	for _, s := range r.Secondary {
		if s == slug {
			return true
		}
	}
	return false
}

// normalise turns an authored term into its lookup key.
//
// Through the SAME scanner the item text goes through, which is the property the
// whole index depends on: a term written "Zero-Day" and a headline containing
// "zero day" must produce the same key, and the only way to be sure of that
// without maintaining two normalisers is to run both sides through one function.
//
// Returns "" for a term that scans to nothing.
func normalise(term string) string {
	words := scanWords(term)
	if len(words) == 0 {
		return ""
	}
	return strings.Join(words, " ")
}
