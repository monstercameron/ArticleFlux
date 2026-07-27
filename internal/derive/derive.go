// Package derive turns the raw engagement log into the interest layer
// (TODO 6.9, plan.md §18, D18).
//
// # The one rule this package exists to obey
//
// Everything it writes is derived. `ClearDerived` then a re-run must produce the
// same result, and a test asserts it. That property is what makes `engagements`
// the only irreplaceable table here: if a derivation is wrong, the repair is to
// throw the output away, which is only safe because none of it is a source of
// truth.
//
// # D18: two stages, in this order, for a reason
//
//  1. **Recall.** Feed, term and domain affinity, and topics, all from PASSIVE
//     signals — impressions, opens, dwell, completion. Volume lives here and it
//     is allowed to correlate with ease of consumption, because its only job is
//     to produce and order a candidate set without missing things.
//
//  2. **Precision.** `home_ranking`, re-ranked over that candidate set using the
//     DELIBERATE acts: A27 verdicts, notes, tags, click-throughs. These are
//     sparse precisely because they cost the reader something, and that cost is
//     what makes them evidence of worth rather than of stickiness.
//
// Folding the deliberate acts in as more weighted terms in one sum is the
// failure D18 rejected, and it is worth restating because the code would look
// simpler that way: a single linear score over dwell, completion and
// click-through converges on the most trivially clickable thing published that
// day, and it fails INVISIBLY — the page still looks full while it happens.
//
// # bulk_read is neutral, in both stages
//
// R17, and the reason it is checked rather than assumed: marking 143 items read
// is not 143 rejections, and a scorer that learns from it concludes the reader
// dislikes everything they subscribe to. `internal/signals` excludes it from
// affinity and `FeedSignals` excludes it in SQL; this package inherits both and
// a test asserts the end-to-end result.
package derive

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/rank"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/textvec"
	"github.com/monstercameron/ArticleFlux/internal/topics"
	"github.com/monstercameron/ArticleFlux/internal/urlnorm"
)

// Window is how far back a derivation looks.
//
// Ninety days. Long enough that a monthly essayist is still visible, short
// enough that last year's interests do not compete with this week's — §18.3's
// "interests expire", expressed as the only knob that can express it.
const Window = 90 * 24 * time.Hour

// MaxTerms bounds the stored term vocabulary.
//
// The TF-IDF vocabulary of a few hundred articles is tens of thousands of terms.
// Storing all of them makes term_affinity larger than `items` while the tail
// contributes nothing anyone would recognise as an interest.
const MaxTerms = 2000

// MaxRanked is how many items the materialised homepage holds.
const MaxRanked = 200

// Payload is a derive job.
type Payload struct {
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
}

// Service runs derivations.
type Service struct {
	repo *store.ReaderRepo
	log  *slog.Logger
}

// New builds the service.
func New(repo *store.ReaderRepo, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// Enqueue schedules a derivation for one user.
//
// Deduped per user: a poller running every fifteen minutes must not stack
// ninety-six identical derive jobs overnight while the first is still waiting.
func (s *Service) Enqueue(ctx context.Context, sc store.Scope) error {
	payload, err := json.Marshal(Payload{TenantID: sc.TenantID, UserID: sc.UserID})
	if err != nil {
		return err
	}
	_, err = s.repo.Enqueue(ctx, store.NewJob{
		Kind:     store.JobDerive,
		TenantID: sc.TenantID,
		Payload:  string(payload),
		// Below fan-out. Fan-out makes items visible; this decides their order,
		// and an item ranked late is still readable while an item not delivered
		// is not there at all.
		Priority:  1,
		DedupeKey: sc.UserID,
	})
	return err
}

// Handle is the job handler.
func (s *Service) Handle(ctx context.Context, job store.Job) error {
	var p Payload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return fmt.Errorf("derive: unreadable payload: %w", err)
	}
	sc := store.Scope{TenantID: p.TenantID, UserID: p.UserID}
	if !sc.Valid() {
		return fmt.Errorf("derive: payload names no user")
	}
	return s.Run(ctx, sc, time.Now().UTC())
}

// Result reports what a derivation produced.
type Result struct {
	Feeds   int
	Terms   int
	Domains int
	Topics  int
	Ranked  int
	// ColdStart is true when there was too little engagement for topics to mean
	// anything. §18.4 wants the UI to say so rather than present a confident
	// wrong answer.
	ColdStart bool
}

// Run derives everything for one user.
//
// `now` is a parameter so a replay over historical data produces the state that
// was true at the time, which is what makes the rebuild property testable.
func (s *Service) Run(ctx context.Context, sc store.Scope, now time.Time) error {
	_, err := s.RunReporting(ctx, sc, now)
	return err
}

// RunReporting is Run, returning what it did.
func (s *Service) RunReporting(ctx context.Context, sc store.Scope, now time.Time) (Result, error) {
	var res Result
	sinceMS := now.Add(-Window).UnixMilli()

	// ---- Stage 1: recall -------------------------------------------------

	feeds, err := s.deriveFeedAffinity(ctx, sc, sinceMS)
	if err != nil {
		return res, fmt.Errorf("derive: feed affinity: %w", err)
	}
	res.Feeds = len(feeds)

	engaged, err := s.engagedItems(ctx, sc, sinceMS)
	if err != nil {
		return res, fmt.Errorf("derive: engaged items: %w", err)
	}

	terms, corpus, vectors, err := s.deriveTermAffinity(ctx, sc, engaged)
	if err != nil {
		return res, fmt.Errorf("derive: term affinity: %w", err)
	}
	res.Terms = terms

	domains, err := s.deriveDomainAffinity(ctx, sc, engaged)
	if err != nil {
		return res, fmt.Errorf("derive: domain affinity: %w", err)
	}
	res.Domains = domains

	topicSet, suppressed, cold, err := s.deriveTopics(ctx, sc, engaged, vectors, now)
	if err != nil {
		return res, fmt.Errorf("derive: topics: %w", err)
	}
	res.Topics = len(topicSet)
	res.ColdStart = cold

	// ---- Stage 2: precision ---------------------------------------------
	//
	// Only now, and only reading what stage 1 wrote plus the deliberate acts.

	ranked, err := s.deriveHomeRanking(ctx, sc, feeds, topicSet, suppressed, corpus, now)
	if err != nil {
		return res, fmt.Errorf("derive: home ranking: %w", err)
	}
	res.Ranked = ranked

	if s.log != nil {
		s.log.Info("derived the interest layer",
			"user", sc.UserID, "feeds", res.Feeds, "terms", res.Terms,
			"domains", res.Domains, "topics", res.Topics, "ranked", res.Ranked,
			"cold_start", res.ColdStart)
	}
	return res, nil
}

// deriveFeedAffinity rolls the engagement log up per source.
func (s *Service) deriveFeedAffinity(ctx context.Context, sc store.Scope, sinceMS int64) ([]store.FeedAffinity, error) {
	sigs, err := s.repo.FeedSignals(ctx, sc, sinceMS)
	if err != nil {
		return nil, err
	}
	volume, err := s.repo.SourceVolume(ctx, sc, 30)
	if err != nil {
		return nil, err
	}

	out := make([]store.FeedAffinity, 0, len(sigs))
	for _, sig := range sigs {
		a := store.FeedAffinity{
			SourceID:     sig.SourceID,
			Impressions:  sig.Impressions,
			Opens:        sig.Opens,
			Favorites:    sig.Likes,
			Notes:        sig.Deliberate,
			Bounces:      sig.Bounces,
			VolumePerDay: volume[sig.SourceID],
		}
		if sig.Items > 0 {
			a.MedianDwellMS = float64(sig.DwellMS) / float64(sig.Items)
		}
		if sig.Opens > 0 {
			a.CompletionRate = float64(sig.Completes) / float64(sig.Opens)
		}
		if sig.LastEngagedAt > 0 {
			a.LastEngagedAt = time.UnixMilli(sig.LastEngagedAt).UTC().Format(time.RFC3339Nano)
		}

		half, err := s.repo.HalfLifeFor(ctx, sc, sig.SourceID)
		if err != nil {
			return nil, err
		}
		a.HalfLifeHours = half
		a.Score = feedScore(sig)
		out = append(out, a)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].SourceID < out[j].SourceID })
	if err := s.repo.ReplaceFeedAffinity(ctx, sc, out); err != nil {
		return nil, err
	}
	return out, nil
}

// feedScore turns a source's counts into a 0..1 affinity.
//
// The open rate is the backbone, because it is the one rate whose denominator is
// real — §18.1 built impressions specifically so this could be computed against
// something other than an invented number.
//
// Deliberate acts are added on top rather than averaged in: a note or a tag on
// one article out of fifty is strong evidence about the source, and averaging
// would let a low open rate erase it.
func feedScore(sig store.FeedSignal) float64 {
	if sig.Impressions == 0 {
		return 0
	}
	score := sig.OpenRate()

	// Bounces are informed rejections, and §18.1 weights them above opens for
	// that reason: someone who opened and left immediately told you more than
	// someone who never opened.
	if sig.Opens > 0 {
		score -= 0.5 * float64(sig.Bounces) / float64(sig.Opens)
	}
	if sig.Deliberate > 0 {
		score += 0.3 * math.Log1p(float64(sig.Deliberate))
	}
	if sig.Likes > 0 {
		score += 0.2 * math.Log1p(float64(sig.Likes))
	}
	if sig.Dislikes > 0 {
		score -= 0.3 * math.Log1p(float64(sig.Dislikes))
	}
	return clamp01(score)
}

// engagedItem is one item the reader actually engaged with.
type engagedItem struct {
	ItemID    string
	SourceID  string
	Text      string
	URL       string
	EngagedAt time.Time
	// Weight is how much this engagement counts, from the signal priors.
	Weight float64
}

// engagedItems collects what the reader engaged with in the window.
//
// Impressions are deliberately NOT enough to appear here. An item that scrolled
// past on screen is not evidence of interest, and including impressions would
// make the term vector a description of the reader's subscription list rather
// than of their reading.
func (s *Service) engagedItems(ctx context.Context, sc store.Scope, sinceMS int64) ([]engagedItem, error) {
	events, err := s.repo.EngagementsSince(ctx, sc, sinceMS, 20000)
	if err != nil {
		return nil, err
	}

	weights := map[string]float64{}
	when := map[string]time.Time{}
	for _, e := range events {
		if e.ItemID == "" {
			continue
		}
		w := affinityWeight(string(e.Kind))
		if w == 0 {
			// bulk_read and sync_read land here, which is R17's requirement: a
			// mark-all-read must not move a single number.
			continue
		}
		weights[e.ItemID] += w
		at := time.UnixMilli(e.At).UTC()
		if at.After(when[e.ItemID]) {
			when[e.ItemID] = at
		}
	}
	if len(weights) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(weights))
	for id := range weights {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	items, err := s.repo.ItemsByID(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make([]engagedItem, 0, len(items))
	for _, it := range items {
		body := it.ContentHTML
		if body == "" {
			body = it.Summary
		}
		out = append(out, engagedItem{
			ItemID:    it.ID,
			SourceID:  it.SourceID,
			Text:      it.Title + " " + body,
			URL:       it.URL,
			EngagedAt: when[it.ID],
			Weight:    weights[it.ID],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ItemID < out[j].ItemID })
	return out, nil
}

// affinityWeight maps a signal kind to its contribution.
//
// Positive for engagement, negative for informed rejection, and ZERO for the
// bulk kinds. The zero is the load-bearing one — see R17.
func affinityWeight(kind string) float64 {
	switch kind {
	case "opened", "dwell":
		return 1
	case "completed":
		return 2
	case "liked", "starred", "noted", "tagged", "clicked_through":
		return 3
	case "disliked":
		return -2
	case "bounced":
		return -1
	default:
		// Includes bulk_read, sync_read, impression and anything added later
		// without a considered weight. Silence is the safe default: a new signal
		// contributing an accidental weight is worse than one contributing none.
		return 0
	}
}

// deriveTermAffinity builds the TF-IDF vocabulary of what the reader reads.
func (s *Service) deriveTermAffinity(ctx context.Context, sc store.Scope,
	engaged []engagedItem) (int, *textvec.Corpus, map[string]textvec.Vector, error) {

	corpus := textvec.NewCorpus()
	for _, e := range engaged {
		corpus.Add(e.Text)
	}

	vectors := make(map[string]textvec.Vector, len(engaged))
	profile := textvec.Vector{}
	for _, e := range engaged {
		v := corpus.TFIDF(e.Text)
		vectors[e.ItemID] = v
		// Weighted by how much the reader engaged: an article they completed and
		// noted contributes more vocabulary than one they opened and left.
		profile.Add(v, e.Weight)
	}

	type tw struct {
		term string
		w    float64
	}
	all := make([]tw, 0, len(profile))
	for term, w := range profile {
		if w > 0 {
			all = append(all, tw{term, w})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].w != all[j].w {
			return all[i].w > all[j].w
		}
		return all[i].term < all[j].term
	})
	if len(all) > MaxTerms {
		all = all[:MaxTerms]
	}

	rows := make([]store.TermWeight, 0, len(all))
	for _, t := range all {
		rows = append(rows, store.TermWeight{Term: t.term, Weight: t.w, DocFreq: corpus.Docs()})
	}
	if err := s.repo.ReplaceTermAffinity(ctx, sc, rows); err != nil {
		return 0, nil, nil, err
	}
	return len(rows), corpus, vectors, nil
}

// deriveDomainAffinity rolls engagement up per TARGET domain (§18.6).
//
// The cheap signal nobody uses: aggregator items point elsewhere, so someone
// who keeps opening one research group's site through Hacker News is telling you
// about that group, and the aggregator is incidental.
func (s *Service) deriveDomainAffinity(ctx context.Context, sc store.Scope, engaged []engagedItem) (int, error) {
	byDomain := map[string]*store.DomainAffinity{}
	for _, e := range engaged {
		if e.URL == "" {
			continue
		}
		host := strings.TrimPrefix(urlnorm.Host(e.URL), "www.")
		if host == "" {
			continue
		}
		d, ok := byDomain[host]
		if !ok {
			d = &store.DomainAffinity{Domain: host}
			byDomain[host] = d
		}
		d.Opens++
		if e.Weight >= 3 {
			d.Stars++
		}
		if at := e.EngagedAt.Format(time.RFC3339Nano); at > d.LastAt {
			d.LastAt = at
		}
	}

	out := make([]store.DomainAffinity, 0, len(byDomain))
	for _, d := range byDomain {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })

	if err := s.repo.ReplaceDomainAffinity(ctx, sc, out); err != nil {
		return 0, err
	}
	return len(out), nil
}

// deriveTopics clusters what the reader engaged with.
//
// It returns a suppression flag per cluster, read back from storage rather than
// recomputed: "not an interest" is a decision the reader made, it survives
// derivation by design (see ReplaceTopics), and the only place it exists is the
// database. Reading it back after the write is what lets THIS pass honour it
// rather than the next one.
func (s *Service) deriveTopics(ctx context.Context, sc store.Scope, engaged []engagedItem,
	vectors map[string]textvec.Vector, now time.Time) ([]topics.Topic, []bool, bool, error) {

	docs := make([]topics.Doc, 0, len(engaged))
	for _, e := range engaged {
		docs = append(docs, topics.Doc{
			ItemID:    e.ItemID,
			Vector:    vectors[e.ItemID],
			EngagedAt: e.EngagedAt,
		})
	}

	res := topics.Build(docs, topics.Options{Now: now, Window: Window})
	if err := s.repo.ReplaceTopics(ctx, sc, res.Topics); err != nil {
		return nil, nil, false, err
	}

	stored, err := s.repo.Topics(ctx, sc)
	if err != nil {
		return nil, nil, false, err
	}
	suppressedBy := map[string]bool{}
	for _, row := range stored {
		suppressedBy[topicKey(row.TopTerms)] = row.Suppressed
	}
	flags := make([]bool, len(res.Topics))
	for i, t := range res.Topics {
		flags[i] = suppressedBy[topicKey(t.TopTerms)]
	}
	return res.Topics, flags, res.ColdStart, nil
}

// topicKey matches an in-memory cluster to its stored row.
//
// The first three terms, the same fingerprint ReplaceTopics preserves renames
// by. Using all of them would make the key change whenever the eighth-heaviest
// term shifted, which happens constantly.
func topicKey(terms []string) string {
	if len(terms) > 3 {
		terms = terms[:3]
	}
	return strings.ToLower(strings.Join(terms, "\x1f"))
}

// deriveHomeRanking is the precision stage.
func (s *Service) deriveHomeRanking(ctx context.Context, sc store.Scope,
	feeds []store.FeedAffinity, topicSet []topics.Topic, suppressed []bool,
	corpus *textvec.Corpus, now time.Time) (int, error) {

	// The candidate set is what recall produced: unread items from subscribed
	// feeds. Ranking read items would be re-ordering a page the reader has
	// already been through.
	candidates, _, err := s.repo.ListItems(ctx, sc, store.ListQuery{
		UnreadOnly: true,
		Limit:      MaxRanked * 3,
	})
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, s.repo.ReplaceHomeRanking(ctx, sc, nil)
	}

	byFeed := make(map[string]store.FeedAffinity, len(feeds))
	for _, f := range feeds {
		byFeed[f.SourceID] = f
	}
	domains, err := s.repo.DomainAffinities(ctx, sc)
	if err != nil {
		return 0, err
	}

	// The deliberate acts, read separately — this is the half that makes it a
	// re-rank rather than another weighted term.
	ids := make([]string, 0, len(candidates))
	for _, it := range candidates {
		ids = append(ids, it.ID)
	}
	if len(ids) > store.MaxItemSignalLookup {
		ids = ids[:store.MaxItemSignalLookup]
	}
	itemSignals, err := s.repo.ItemSignals(ctx, sc, ids)
	if err != nil {
		return 0, err
	}

	type scored struct {
		item store.Item
		res  rank.Result
	}
	out := make([]scored, 0, len(candidates))

	for _, it := range candidates {
		aff := byFeed[it.SourceID]
		host := strings.TrimPrefix(urlnorm.Host(it.URL), "www.")

		body := it.ContentHTML
		if body == "" {
			body = it.Summary
		}
		vec := corpus.TFIDF(it.Title + " " + body)
		topicIdx, topicScore := topics.Nearest(vec, topicSet)

		published, _ := time.Parse(time.RFC3339Nano, it.PublishedAt)
		rankItem := rank.Item{
			ID:           it.ID,
			SourceID:     it.SourceID,
			PublishedAt:  published,
			TopicScore:   topicScore,
			TargetDomain: host,
			ManualWeight: 1,
		}
		sig := rank.Signals{
			FeedAffinity:   aff.Score,
			DomainAffinity: domainScore(domains[host]),
			HalfLife:       time.Duration(aff.HalfLifeHours * float64(time.Hour)),
			VolumePerDay:   aff.VolumePerDay,
			Mode:           rank.ModeFull,
		}
		// A suppressed topic is a strong negative across its whole cluster
		// (§18.2), and it is precision-stage evidence: marking something "not an
		// interest" is a deliberate act.
		if topicIdx >= 0 && topicIdx < len(suppressed) && suppressed[topicIdx] {
			sig.NegativeAffinity = 1
		}

		r := rank.Score(rankItem, sig, rank.DefaultWeights(), now)
		if !r.Eligible {
			continue
		}
		r = applyDeliberate(r, itemSignals[it.ID])
		out = append(out, scored{item: it, res: r})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].res.Score != out[j].res.Score {
			return out[i].res.Score > out[j].res.Score
		}
		return out[i].item.ID < out[j].item.ID
	})
	if len(out) > MaxRanked {
		out = out[:MaxRanked]
	}

	rows := make([]store.RankedItem, 0, len(out))
	for i, sc := range out {
		reasons := make([]string, 0, len(sc.res.Reasons))
		for _, r := range sc.res.Reasons {
			reasons = append(reasons, r.Text)
		}
		rows = append(rows, store.RankedItem{
			ItemID:  sc.item.ID,
			Score:   sc.res.Score,
			Rank:    i + 1,
			Slot:    slotFor(i, len(out)),
			Reasons: reasons,
			Tier:    "smart",
		})
	}
	if err := s.repo.ReplaceHomeRanking(ctx, sc, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// applyDeliberate is the re-rank D18 asks for.
//
// It multiplies rather than adds, and that is the distinction from a weighted
// term: a deliberate act SCALES what recall already thought, so it cannot on its
// own promote something recall found uninteresting, and it cannot be diluted by
// the sum of a dozen passive terms either.
func applyDeliberate(r rank.Result, sig store.ItemSignal) rank.Result {
	if len(sig.Counts) == 0 {
		return r
	}
	factor := 1.0
	for kind, n := range sig.Counts {
		switch string(kind) {
		case "liked", "starred", "noted", "tagged":
			factor += 0.15 * float64(n)
		case "disliked":
			factor -= 0.4 * float64(n)
		case "clicked_through":
			factor += 0.1 * float64(n)
		}
	}
	if factor < 0.1 {
		factor = 0.1
	}
	if factor == 1.0 {
		return r
	}

	before := r.Score
	r.Score *= factor
	r.Reasons = append(r.Reasons, rank.Reason{
		Term:  "deliberate",
		Text:  deliberateText(factor),
		Delta: r.Score - before,
	})
	return r
}

func deliberateText(factor float64) string {
	if factor < 1 {
		return "you marked things like this as not for you"
	}
	return "you have engaged deliberately with this before"
}

// slotFor assigns §18.4's three slots.
//
// Top ~70%, Explore ~20%, Clusters ~10%. Explore exists because pure affinity
// converges to a monoculture and fails invisibly — the page still looks full.
func slotFor(i, total int) string {
	switch {
	case float64(i) < float64(total)*0.7:
		return "top"
	case float64(i) < float64(total)*0.9:
		return "explore"
	default:
		return "cluster_head"
	}
}

func domainScore(d store.DomainAffinity) float64 {
	if d.Opens == 0 {
		return 0
	}
	return clamp01(math.Log1p(float64(d.Opens+2*d.Stars)) / math.Log1p(20))
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
