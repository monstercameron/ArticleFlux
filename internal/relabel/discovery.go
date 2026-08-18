package relabel

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/propose"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/textvec"
)

// The discovery half (plan.md §27.3f, 0034).
//
// `internal/propose` decides what earns a category; this is what feeds it and
// what does something about the answer. The split is deliberate and it is the
// same one `internal/classify` and `internal/pipeline` have: the judgement is
// pure and exhaustively testable, and the part that touches a database is thin
// enough to read in one sitting.
//
// # Weekly, not quarter-hourly
//
// The labelling sweep runs every fifteen minutes because it is finishing work
// somebody is waiting for. This is the opposite: it proposes a permanent change
// to the reader's sidebar, and there is no version of "we found a new category
// for you" that is improved by arriving four times an hour. Weekly also means
// the corpus has meaningfully changed between runs, so a refused cluster is not
// re-refused with identical inputs six hundred times before anyone sees it.

const (
	// DiscoveryInterval is how often the pass CHECKS, not how often it runs.
	//
	// Hourly. The run itself is triggered by growth (see PileGrowthTrigger), so
	// this is only the resolution at which growth is noticed — a reader who has
	// just imported an OPML file gets a pass within the hour instead of within
	// the week.
	DiscoveryInterval = time.Hour

	// PileGrowthTrigger is how much the uncategorised pile must grow before the
	// pass runs again.
	//
	// # Why growth and not a schedule
	//
	// A clock says "a week has passed", which is not evidence about anything. It
	// fires a superlinear clustering pass over four thousand vectors to re-refuse
	// the same clusters when nothing has arrived, and it makes a reader who
	// imported two thousand articles an hour ago wait six days for a pass whose
	// input changed completely.
	//
	// A cluster that could clear the gate did not appear without articles
	// appearing first, so the pile is the honest trigger. Five hundred is roughly
	// three times `propose.Options.MinMembers`: enough new material that a
	// genuinely new subject could have reached admission size, and not so little
	// that the pass reconsiders an essentially unchanged corpus.
	PileGrowthTrigger = 500

	// MinDiscoveryGap is the rate limit that growth cannot override.
	//
	// Clustering is not free, and a bulk import can add tens of thousands of
	// articles in minutes — which without this would trigger the pass on every
	// tick while the backfill worked through them. Growth decides WHETHER, this
	// decides HOW OFTEN, and both are needed.
	MinDiscoveryGap = 6 * time.Hour

	// MaxDiscoveryGap forces a pass even on a pile that is not growing.
	//
	// A month. Growth is the trigger for a NEW subject arriving, but it is not the
	// only thing that changes the answer: the reader's own taxonomy moves, refusals
	// age out of relevance, and probation verdicts fall due. This is the floor that
	// keeps a quiet account from going indefinitely unreviewed.
	MaxDiscoveryGap = 30 * 24 * time.Hour

	// MinPileForDiscovery is how large the uncategorised pile must be before
	// this runs at all.
	//
	// Below a thousand genuine refusals there is no cluster that could clear
	// `propose.Options.MinMembers` and still leave a pile worth the name, so a
	// run would be a guaranteed no-op that still read four thousand vectors. It
	// also keeps a new instance quiet: an account two weeks old has an
	// uncategorised pile because it has barely any articles, not because its
	// taxonomy is wrong.
	MinPileForDiscovery = 1000

	// DiscoveryCorpusLimit bounds how much of the pile one run clusters.
	//
	// Two thousand, and the number is an OPERATIONAL limit rather than a quality
	// one. Agglomerative clustering here measures at roughly O(n^2.5):
	//
	//	n=500    126ms     5 MB
	//	n=1000   598ms    14 MB
	//	n=2000   3.4s     44 MB
	//	n=4000   21s     153 MB
	//
	// The target is a 2GB box that is also running nginx, SQLite and the analyze
	// pool, where a developer machine's 21 seconds is minutes of pegged CPU. And
	// the doubling buys nothing measurable: a real run over a 7,370-item pile at
	// n=4000 took 36 seconds and admitted zero clusters, with every refusal in
	// reach of the gate at half the corpus too.
	//
	// Newest-first, so what gets clustered is what the reader is accumulating now
	// rather than a two-year-old backlog — and the count that was dropped is
	// logged, because a silently truncated corpus reads as "we looked at
	// everything and found nothing".
	DiscoveryCorpusLimit = 2000

	// MaxDiscoveryDuration is how long one clustering pass may take before it is
	// reported as a problem.
	//
	// Not a cancellation — killing a pass halfway leaves the reader with nothing
	// and no explanation, and the pass is already bounded by the corpus limit
	// above. This is the tripwire that says the bound has stopped holding: a run
	// that takes minutes on a corpus sized to take seconds means the box is under
	// pressure or the limit has been raised without measuring, and both are
	// things somebody needs to see rather than infer from a slow site.
	MaxDiscoveryDuration = 90 * time.Second

	// CentroidSample bounds how many members define an existing category's
	// centroid for the novelty check.
	CentroidSample = 200

	// ProbationPeriod is how long a discovered category runs at a raised floor
	// before it graduates or is withdrawn.
	ProbationPeriod = 21 * 24 * time.Hour

	// ProbationMinClaims is how much a category must have filed by the end of
	// probation to keep its slot.
	//
	// A discovered category that claimed eleven articles in three weeks did not
	// earn a permanent place in the sidebar, however good its name looked. It
	// expires rather than being retired, and the distinction is recorded: expiry
	// means "too small", retirement means "wrong", and only the second is
	// evidence about the reader's taste.
	ProbationMinClaims = 25

	// MaxRemovalRate is the share of a category's assignments the reader may take
	// back off before it is withdrawn automatically.
	//
	// A fifth. This is the only signal available that means anything — a category
	// nobody removes is one nobody minds, and one being removed from a fifth of
	// what it claims is wrong about what it is for. Acting on it without asking
	// is the point: nobody is watching a background sweep, so a bad category that
	// waits for a click stays wrong indefinitely.
	MaxRemovalRate = 0.20
)

// minPile is the floor below which a run is not worth doing, defaulting to
// MinPileForDiscovery. A field rather than the constant directly so a test can
// exercise the pass without seeding a thousand rows per case — the THRESHOLD is
// covered where it belongs, in the pure gate.
func (s *Service) minPile() int {
	if s.pileFloor > 0 {
		return s.pileFloor
	}
	return MinPileForDiscovery
}

// DiscoveryResult is one run over one reader.
type DiscoveryResult struct {
	Pile      int
	Clustered int
	Created   []string
	Refused   map[string]int
	// Graduated and Retired are the probation ladder's outcomes this run.
	Graduated []string
	Retired   []string
	// Skipped is set when the pile was too small to look at.
	Skipped bool
	// Elapsed is how long the clustering itself took, so the caller can see the
	// cost rather than having to reproduce it.
	Elapsed time.Duration
}

// DiscoverFor runs one reader's discovery pass.
//
// Returns what it did rather than logging it here, so a test can assert on the
// decision instead of on a log line.
func (s *Service) DiscoverFor(ctx context.Context, sc store.Scope, opt propose.Options) (DiscoveryResult, error) {
	res := DiscoveryResult{Refused: map[string]int{}}

	// Probation first, and deliberately so: a category that is about to be
	// withdrawn must not still be counted as "known" by the novelty check below,
	// or its own cluster is refused as a duplicate of the thing being deleted.
	grad, ret, err := s.reviewProbation(ctx, sc)
	if err != nil {
		return res, err
	}
	res.Graduated, res.Retired = grad, ret

	docs, err := s.repo.UncategorisedForDiscovery(ctx, sc, DiscoveryCorpusLimit)
	if err != nil {
		return res, err
	}
	res.Pile = len(docs)
	if len(docs) < s.minPile() {
		res.Skipped = true
		return res, nil
	}

	known, err := s.knownCentroids(ctx, sc)
	if err != nil {
		return res, err
	}
	refused, err := s.refusedCentroids(ctx, sc)
	if err != nil {
		return res, err
	}

	pdocs := make([]propose.Doc, 0, len(docs))
	for _, d := range docs {
		pdocs = append(pdocs, propose.Doc{
			ItemID: d.ItemID, SourceID: d.SourceID,
			PublishedAt: d.PublishedAt, Vector: d.Vector,
		})
	}

	start := time.Now()
	out := propose.Propose(pdocs, known, refused, opt)
	res.Elapsed = time.Since(start)
	if res.Elapsed > MaxDiscoveryDuration {
		s.logf(ctx, slog.LevelWarn, "category discovery took longer than its budget",
			"user", sc.UserID, "docs", len(pdocs), "took", res.Elapsed,
			"budget", MaxDiscoveryDuration, "limit", DiscoveryCorpusLimit)
	}
	res.Clustered = out.Clustered
	for _, r := range out.Rejections {
		res.Refused[r.Reason]++
	}

	for _, c := range out.Proposals {
		id, err := s.create(ctx, sc, c)
		switch {
		case errors.Is(err, store.ErrCategoryExists), errors.Is(err, store.ErrTaxonomyFull):
			// Both are ordinary outcomes rather than failures. A full taxonomy is
			// the cap doing its job, and a name collision is two runs racing —
			// the loser skips, which is why store.CreateCategory distinguishes
			// these from a real error at all.
			res.Refused[reasonFor(err)]++
		case err != nil:
			return res, err
		default:
			res.Created = append(res.Created, id)
		}
	}
	return res, nil
}

func reasonFor(err error) string {
	if errors.Is(err, store.ErrTaxonomyFull) {
		return "taxonomy_full"
	}
	return "name_taken"
}

// create writes one proposal as a probationary category.
//
// The cluster's heaviest terms become the include list, and the members become
// the seed — which is what lets a later run recompute this category's centroid
// from stable ids rather than from a stored vector that drifts.
func (s *Service) create(ctx context.Context, sc store.Scope, c propose.Candidate) (string, error) {
	terms := make([]classify.Term, 0, len(c.Terms))
	for _, t := range c.Terms {
		// Weight 1.0 across the board, deliberately. The cluster says these terms
		// travel together; it says nothing about which is nearly conclusive alone,
		// and inventing a weight band from term frequency would be reading
		// precision into evidence that does not carry it. A person editing the
		// category afterwards is who should set those.
		terms = append(terms, classify.Term{Text: t, Weight: 1.0})
	}

	d, err := s.repo.CreateCategory(ctx, sc, store.CategorySpec{
		Name:    propose.SuggestName(c.Terms),
		Origin:  store.OriginDiscovered,
		State:   store.CategoryProbation,
		Include: terms,
		Seed:    c.Members,
	})
	if err != nil {
		return "", err
	}
	return d.ID, nil
}

// knownCentroids builds a centroid for every live category.
//
// Recomputed every run from current members rather than stored, for the reason
// `categories.seed_json` gives: TF-IDF is corpus-relative, so a centroid written
// down last month drifts against an IDF that has moved, and a novelty check
// against a drifted centroid starts admitting things it used to refuse.
func (s *Service) knownCentroids(ctx context.Context, sc store.Scope) ([]propose.Known, error) {
	members, err := s.repo.CategoryMembers(ctx, sc, CentroidSample)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, nil
	}

	deltas, err := s.repo.ListCategoryDeltas(ctx, sc)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(deltas))
	for _, d := range deltas {
		if d.State != store.CategoryRetired {
			names[d.Slug()] = d.Name
		}
	}

	out := make([]propose.Known, 0, len(members))
	for id, ids := range members {
		vecs, err := s.repo.VectorsFor(ctx, ids)
		if err != nil {
			return nil, err
		}
		c := centroid(vecs)
		if len(c) == 0 {
			continue
		}
		name := names[id]
		if name == "" {
			name = id
		}
		out = append(out, propose.Known{ID: id, Name: name, Centroid: c})
	}
	return out, nil
}

// refusedCentroids rebuilds the centroid of every cluster the reader declined.
//
// From the stored SEED ids, for the same drift reason — which is why
// `category_proposals` keeps them at all. A refusal that could not be recomputed
// would silently stop matching the cluster it was about, and the pass would start
// re-proposing things the reader has already said no to.
func (s *Service) refusedCentroids(ctx context.Context, sc store.Scope) ([]propose.Known, error) {
	props, err := s.repo.ListProposals(ctx, sc)
	if err != nil {
		return nil, err
	}
	out := make([]propose.Known, 0, len(props))
	for _, p := range props {
		if len(p.Seed) == 0 {
			continue
		}
		vecs, err := s.repo.VectorsFor(ctx, p.Seed)
		if err != nil {
			return nil, err
		}
		c := centroid(vecs)
		if len(c) == 0 {
			// Every seed item is gone — a retention sweep took them. The refusal
			// stands as a row but can no longer be matched by cosine, and that is
			// the honest outcome: there is nothing left to compare against.
			continue
		}
		out = append(out, propose.Known{ID: p.ID, Name: p.Name, Centroid: c})
	}
	return out, nil
}

// centroid is the mean of a set of vectors.
func centroid(vecs map[string]map[string]float64) textvec.Vector {
	if len(vecs) == 0 {
		return nil
	}
	out := make(textvec.Vector, 64)
	for _, v := range vecs {
		for term, w := range v {
			out[term] += w
		}
	}
	n := float64(len(vecs))
	for term := range out {
		out[term] /= n
	}
	return out
}

// reviewProbation graduates or withdraws every category whose probation is up.
//
// # Why this decides rather than asks
//
// The rest of this codebase asks: `tag_rules.auto` opens a dry run before a row
// is written, and §13.4 makes preview-equals-apply a rule. Those are all
// FOREGROUND — somebody clicked something and is waiting. This is not: it runs
// weekly in a goroutine nobody is watching, and a bad category that waits for a
// click stays wrong until somebody happens to look, which on a personal reader is
// possibly never.
//
// So the ladder is the safety, and it is decisive in both directions: a category
// earning its keep graduates without ceremony, and one the reader keeps
// correcting is withdrawn without one.
func (s *Service) reviewProbation(ctx context.Context, sc store.Scope) (graduated, retired []string, err error) {
	deltas, err := s.repo.ListCategoryDeltas(ctx, sc)
	if err != nil {
		return nil, nil, err
	}
	counts, err := s.repo.CategoryCounts(ctx, sc)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()

	for _, d := range deltas {
		if d.State != store.CategoryProbation {
			continue
		}
		if d.StateAt.IsZero() || now.Sub(d.StateAt) < ProbationPeriod {
			continue
		}

		removals, assignments, err := s.repo.CategoryRemovalRate(ctx, sc, d.ID, d.StateAt)
		if err != nil {
			return nil, nil, err
		}

		switch {
		case assignments > 0 && float64(removals)/float64(assignments) > MaxRemovalRate:
			if err := s.withdraw(ctx, sc, d, "retired"); err != nil {
				return nil, nil, err
			}
			retired = append(retired, d.ID)

		case counts[d.ID] < ProbationMinClaims:
			if err := s.withdraw(ctx, sc, d, "expired"); err != nil {
				return nil, nil, err
			}
			retired = append(retired, d.ID)

		default:
			if err := s.repo.SetCategoryState(ctx, sc, d.ID, store.CategoryActive); err != nil {
				return nil, nil, err
			}
			graduated = append(graduated, d.ID)
		}
	}
	return graduated, retired, nil
}

// withdraw retires a category AND records why, in that order.
//
// Both, always. Retiring without recording means the next weekly run re-finds the
// same cluster and proposes it again — which is `label_removals`' lesson at the
// taxonomy level, and the reason `category_proposals` exists.
func (s *Service) withdraw(ctx context.Context, sc store.Scope, d store.CategoryDelta, outcome string) error {
	name := d.Name
	if name == "" {
		name = d.ID
	}
	terms := make([]string, 0, len(d.Include))
	for _, t := range d.Include {
		terms = append(terms, t.Text)
	}
	if err := s.repo.RecordProposal(ctx, sc, name, d.Seed, terms, outcome); err != nil {
		return err
	}
	return s.repo.SetCategoryState(ctx, sc, d.ID, store.CategoryRetired)
}

// Discover runs the pass for every reader who has one to run.
func (s *Service) Discover(ctx context.Context) {
	scopes, err := s.repo.ScopesToRelabel(ctx)
	if err != nil {
		s.logf(ctx, slog.LevelWarn, "finding readers for category discovery", "err", err)
		return
	}
	// A reader with no `categories` row has never customised anything — but they
	// are exactly who discovery is FOR, so unlike the labelling sweep this cannot
	// use ScopesToRelabel alone. Everyone with a subscription is a candidate.
	all, err := s.repo.ScopesWithSubscriptions(ctx)
	if err != nil {
		s.logf(ctx, slog.LevelWarn, "finding readers for category discovery", "err", err)
		return
	}
	seen := make(map[string]bool, len(scopes))
	for _, sc := range scopes {
		seen[sc.UserID] = true
	}
	for _, sc := range all {
		if !seen[sc.UserID] {
			scopes = append(scopes, sc)
		}
	}

	for _, sc := range scopes {
		due, pile, err := s.discoveryDue(ctx, sc)
		if err != nil {
			s.logf(ctx, slog.LevelWarn, "checking whether discovery is due",
				"user", sc.UserID, "err", err)
			continue
		}
		if !due {
			continue
		}

		res, err := s.DiscoverFor(ctx, sc, propose.Defaults())
		if err != nil {
			s.logf(ctx, slog.LevelWarn, "category discovery failed",
				"user", sc.UserID, "err", err)
			continue
		}
		// Stamped even when the pass proposed nothing — especially then. The
		// stamp is what stops the next tick reconsidering an unchanged pile, so
		// recording only successful runs would mean a reader whose pile never
		// produces a candidate is clustered on every tick forever.
		if err := s.repo.RecordDiscoveryRun(ctx, sc, pile); err != nil {
			s.logf(ctx, slog.LevelWarn, "recording the discovery run", "user", sc.UserID, "err", err)
		}
		if res.Skipped {
			continue
		}
		if len(res.Created) == 0 && len(res.Retired) == 0 && len(res.Graduated) == 0 {
			// Refusals still get a line at debug: this is the number that says
			// whether the gate is working or merely closed, and a run reporting
			// nothing is indistinguishable from a run that did not happen.
			s.logf(ctx, slog.LevelDebug, "category discovery proposed nothing",
				"user", sc.UserID, "pile", res.Pile, "clustered", res.Clustered,
				"refused", res.Refused)
			continue
		}
		s.logf(ctx, slog.LevelInfo, "category discovery",
			"user", sc.UserID, "pile", res.Pile, "clustered", res.Clustered,
			"created", res.Created, "graduated", res.Graduated,
			"retired", res.Retired, "refused", res.Refused)
	}
}

// discoveryDue reports whether this reader's pile has changed enough to be worth
// clustering, and how big it is now.
//
// Counting the pile is one indexed query; clustering it is thousands of cosines
// over four thousand vectors. Asking the cheap question first is the whole reason
// this is a separate step rather than an early return inside DiscoverFor — and
// DiscoverFor deliberately does NOT consult it, so an explicit call (a test, a
// future "look now" button) always runs.
func (s *Service) discoveryDue(ctx context.Context, sc store.Scope) (bool, int, error) {
	pile, err := s.repo.UncategorisedCount(ctx, sc)
	if err != nil {
		return false, 0, err
	}
	if pile < s.minPile() {
		return false, pile, nil
	}

	last, lastPile, err := s.repo.DiscoveryState(ctx, sc)
	if err != nil {
		return false, pile, err
	}
	if last.IsZero() {
		return true, pile, nil
	}

	since := time.Since(last)
	if since < MinDiscoveryGap {
		return false, pile, nil
	}
	if since >= MaxDiscoveryGap {
		return true, pile, nil
	}
	return pile-lastPile >= PileGrowthTrigger, pile, nil
}

// RunDiscovery checks on a ticker until the context is cancelled.
//
// It does NOT run once at startup, which is the one place this deliberately
// differs from every other sweep here. A restart is not evidence about anybody's
// taxonomy, and a process that crash-looped would propose a category on every
// boot — MinDiscoveryGap would catch it, but relying on a rate limit to undo a
// scheduling mistake is not the same as not making it.
func (s *Service) RunDiscovery(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = DiscoveryInterval
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Discover(ctx)
		}
	}
}
