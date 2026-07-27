// Package pipeline runs one analysis pass over each new item, whose result every
// other per-item feature reads instead of re-deriving (plan.md §27.2, A41,
// TODO 10.5).
//
// # The split that makes it affordable
//
// Items are global (A14): a source with 200 subscribers is fetched once and
// stored once. This inherits that. Everything here runs **once per item, for the
// whole instance**, and nothing in it may depend on which user is going to read
// the article.
//
// That is the decision most likely to be made backwards, because every other
// per-item feature in this codebase is correctly per-user — fan-out, rules,
// tagging, ranking. Writing this per-subscriber would work, pass its tests, and
// cost 200x on a popular feed, with the model reading the same article once per
// reader. Per-user labelling happens later, in fan-out, against the row this
// produces (§27.2a).
//
// # Everything here is derived
//
// `internal/derive`'s one rule extends to cover this package: ClearAnalysis then
// a re-run must reproduce the same rows, and a test asserts it. That property is
// what makes a wrong lexicon a five-minute fix rather than a migration, and it is
// the whole basis for §27.9's reclassification.
//
// It also constrains what an `Analyzer` may do: no clock beyond the timestamps
// the batch carries, no randomness, no reads of per-user state, and no
// dependence on the order items happen to arrive in.
//
// # What is NOT here yet
//
// The model half — the shared read and the `Contributor` registry (§27.2b,
// §27.4b) — is TODO 10.14. This package is the deterministic stage, and it is
// deliberately complete on its own: an instance with no API key runs exactly this
// and gets a finished feature.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/textvec"
)

// AnalyzerVersion is stamped on every row and bumped when this package's output
// would change for the same input.
//
// It is a code constant rather than a build stamp, so that a rebuild with no
// behavioural change does not invalidate every row in the database. Bumping it is
// a deliberate act by whoever changed the behaviour — which also makes the git
// history the answer to "why did everything re-analyse on Tuesday".
//
// Paired with, and not merged into, the lexicon hash: the analyzer can change
// without the lexicon (a scoring fix) and the lexicon can change without the
// analyzer (a term added), and the backfill wants to know which, because a
// lexicon change only invalidates the category scores.
const AnalyzerVersion = 1

// Item is one article entering the pipeline.
//
// A closed struct rather than the store's row type, so this package does not
// depend on the store and can be tested without a database. The caller projects.
//
// Body is PLAIN TEXT. Converting from HTML is the caller's job and is done once
// per item, rather than once per analyzer — six analyzers each calling
// `sanitize.Text` on the same 4,000 words is the kind of cost that only shows up
// under a full backfill.
type Item struct {
	ID          string
	Title       string
	URL         string
	Summary     string
	SourceTitle string
	Body        string
	WordCount   int
}

// Entity is one named thing found in an item.
type Entity struct {
	// Name is the normalised, lowercased matching key.
	Name string `json:"name"`
	// Label is the display form.
	Label string `json:"label"`
}

// Analysis is what one item's pass produces.
//
// Field-per-analyzer rather than a map, because the consumers are typed and a
// map would push every one of them into asserting on `any`. Adding an analyzer
// adds a field, which is a compile error at every consumer that needs to care and
// silence at every one that does not.
type Analysis struct {
	ItemID string

	// Lang is the detected language, or "" when the detector had too little to go
	// on. Only "en" gets categorised — see langAnalyzer.
	Lang string

	// Genre is the article's form: news, analysis, opinion, tutorial, release,
	// review, interview, roundup, research, announcement. Populated from day one
	// and surfaced by nothing in v1 (§27.1a).
	Genre string

	// CategoryScores is every default-taxonomy category that scored above zero.
	CategoryScores map[string]float64
	// Primary, Secondary and Ambiguous are the free tier's answer, carried so
	// that the escalation gate (§27.4a) does not have to re-derive them from the
	// scores and reach a different conclusion.
	Primary   string
	Secondary []string
	Ambiguous bool

	Keyphrases []string
	Entities   []Entity

	// Vector is the item's TERM-FREQUENCY vector — not TF-IDF. See the note on
	// vectorAnalyzer for why, and for what it means for internal/derive.
	Vector textvec.Vector
}

// Batch is a poll's worth of items and their in-progress analyses.
//
// Analyzers mutate `Out[i]` in place and run in a fixed order. A batch rather
// than an item at a time because some analyzers are cheaper across a group, and
// because the model stage (10.14) will want the grouping anyway.
type Batch struct {
	Items []Item
	Out   []Analysis
}

// Analyzer is one deterministic step.
//
// No I/O of any kind. An analyzer that needed the network or the database would
// break the reproduce-exactly property the package comment is built on, and it
// would make the throughput of a backfill depend on something other than CPU.
type Analyzer interface {
	// Name keys this analyzer in logs and in the drop list. Stable.
	Name() string
	// Version is bumped when this analyzer's output would change. It feeds
	// AnalyzerVersion; it is separate so that the reason for a re-analysis is
	// attributable to one step rather than to "something changed".
	Version() int
	// Analyze fills its own fields of every b.Out[i].
	Analyze(ctx context.Context, b *Batch) error
}

// Service runs the deterministic stage.
type Service struct {
	analyzers []Analyzer
	lexicon   *classify.Lexicon
	strategy  classify.Strategy
	hash      string
	log       *slog.Logger
}

// New wires the standard analyzer set, in the order they must run.
//
// Order is load-bearing in exactly one place and it is worth naming: the language
// detector runs first, and the category scorer reads its answer. Everything else
// is independent, and the fixed order exists so that two instances on the same
// build produce the same row rather than because the steps depend on each other.
func New(lx *classify.Lexicon, st classify.Strategy, log *slog.Logger) *Service {
	return &Service{
		lexicon:  lx,
		strategy: st,
		hash:     LexiconHash(lx),
		log:      log,
		analyzers: []Analyzer{
			langAnalyzer{},
			categoryAnalyzer{lexicon: lx, strategy: st},
			genreAnalyzer{},
			keyphraseAnalyzer{},
			entityAnalyzer{},
			vectorAnalyzer{},
		},
	}
}

// LexiconVersion returns the hash of the compiled lexicon, for the row stamp.
func (s *Service) LexiconVersion() string { return s.hash }

// Analyze runs every analyzer over a batch and returns one Analysis per item, in
// the order the items were given.
//
// # An analyzer error fails the whole batch
//
// Deliberately, and it is the opposite of what the model stage will do
// (§27.2b rule 2, where one contributor's bad slice must not cost the others
// their answers). The two are different because the failures are different: a
// contributor fails because a provider returned something unexpected, which is
// normal and recoverable, while an analyzer here has no I/O and can only fail
// because of a bug.
//
// Continuing past it would write a row stamped with the CURRENT version and
// missing a field — and the backfill selects on version, so that row would never
// be revisited. A silently incomplete row that the staleness query cannot see is
// strictly worse than a failed job that retries and then says why.
func (s *Service) Analyze(ctx context.Context, items []Item) ([]Analysis, error) {
	if len(items) == 0 {
		return nil, nil
	}
	b := &Batch{Items: items, Out: make([]Analysis, len(items))}
	for i := range items {
		b.Out[i].ItemID = items[i].ID
	}

	for _, a := range s.analyzers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := a.Analyze(ctx, b); err != nil {
			return nil, fmt.Errorf("pipeline: analyzer %q: %w", a.Name(), err)
		}
	}
	return b.Out, nil
}

// Version is the composite analyzer version: the package constant plus every
// analyzer's own.
//
// Summed rather than concatenated because the only thing anyone asks of it is
// "is this row from the same code as I am running", and a single integer answers
// that in an indexed column. The trade is that two analyzers changing in opposite
// directions in one release could collide; the constant above is the deliberate
// override for that case, and it is the reason it exists as well.
func (s *Service) Version() int {
	v := AnalyzerVersion
	for _, a := range s.analyzers {
		v += a.Version()
	}
	return v
}

// Analyzers lists the wired steps by name, for the status screen and for a test
// that asserts the set has not changed without the version moving.
func (s *Service) Analyzers() []string {
	out := make([]string, 0, len(s.analyzers))
	for _, a := range s.analyzers {
		out = append(out, a.Name())
	}
	return out
}
