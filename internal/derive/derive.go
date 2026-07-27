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
	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/signals"
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
	// Dwell is scored in a second pass, because Classify needs the article's word
	// count and that is not known until the items are fetched below. The longest
	// single observation is kept rather than the sum: attentive time on one article
	// across two sittings is one reading of it, and adding them up would let a
	// reader who left a tab open outscore one who read three pieces.
	dwellMS := map[string]int64{}
	seen := map[string]bool{}
	for _, e := range events {
		if e.ItemID == "" {
			continue
		}
		// The weight comes from signals.Lookup — the registry — and not from a
		// switch written out here. That distinction was a real bug, and it is worth
		// recording because the broken version read as perfectly reasonable code.
		//
		// The first version was a hand-written switch over ten kind strings. It
		// silently discarded most of what the client actually sends, and on a real
		// database of 977 engagements it cost:
		//
		//   - `reread`, prior 1.2 — the highest of any non-verdict kind, described in
		//     signals.go as "rare, and one of the strongest" — present on 41 distinct
		//     items, contributing NOTHING.
		//   - `chose`, prior 0.6, "picked this over the others on screen" — 28 items,
		//     also nothing.
		//   - `clicked_out`, prior 1.0 — nothing. The switch instead had a case for
		//     `clicked_through`, which is not a kind that exists, and one for
		//     `starred`, which is not either. Two of its ten branches were dead.
		//
		// So the reader's most frequent deliberate signals were being dropped while
		// the code looked correct. Spec.Doc exists to be "kept next to the weight so
		// the two cannot drift apart"; a second copy of the table defeats that.
		//
		// R17 now holds by construction rather than by a remembered case: BulkRead and
		// SyncRead carry Affinity=false, so they are refused before a prior is read.
		//
		// Every affinity-bearing observation marks the item as engaged, even when its
		// own contribution is zero. Dwell is the case that matters: its prior is 0.0
		// by design — "classify with Classify, do not use raw" — and gating membership
		// on a non-zero weight would drop a dwell-only item before the second pass
		// could score it at all.
		spec, ok := signals.Lookup(e.Kind)
		if !ok || !spec.Affinity {
			// bulk_read and sync_read land here, which is R17's requirement: a
			// mark-all-read must not move a single number.
			continue
		}
		seen[e.ItemID] = true
		if e.Kind == signals.Dwell {
			if ms := int64(e.Value); ms > dwellMS[e.ItemID] {
				dwellMS[e.ItemID] = ms
			}
		} else {
			weights[e.ItemID] += spec.Prior
		}
		at := time.UnixMilli(e.At).UTC()
		if at.After(when[e.ItemID]) {
			when[e.ItemID] = at
		}
	}
	if len(seen) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	items, err := s.repo.ItemsByID(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make([]engagedItem, 0, len(items))
	for _, it := range items {
		w := weights[it.ID] + dwellWeight(dwellMS[it.ID], it.WordCount)
		// A net-zero or net-negative item is not evidence of interest, and the
		// vectoriser must not learn its vocabulary. The check is here rather than in
		// the loop above because dwell can move an item in either direction: a
		// bounce-length dwell on an item that was merely opened nets out negative,
		// which is exactly the informed rejection §18.1 asks for.
		if w <= 0 {
			continue
		}
		out = append(out, engagedItem{
			ItemID:    it.ID,
			SourceID:  it.SourceID,
			Text:      itemText(it.Title, it.ContentHTML, it.Summary),
			URL:       it.URL,
			EngagedAt: when[it.ID],
			Weight:    w,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ItemID < out[j].ItemID })
	return out, nil
}

// itemText is the plain-language text of an item, for the vectoriser.
//
// # Why this exists, and what it cost to not have it
//
// The first version fed `Title + " " + ContentHTML` straight into TF-IDF. On a
// real database — a thousand engagements over thirty feeds — the top of the
// derived vocabulary came out as:
//
//	https img href comments com lobste www jpg src nbsp
//
// Those are an HTML document's furniture, not a reader's interests. Every one of
// them appears in most articles, so IDF cannot suppress them the way it suppresses
// ordinary common words: they are genuinely distinctive of the documents the
// reader ENGAGED with, because every document has them.
//
// The visible damage was in the topic labels, which are built from a cluster's top
// terms and came out as "Nbsp · Font · 6f6f6f" — a hex colour lifted from a style
// attribute, presented to the reader as a thing they are interested in. §18.2 says
// a model you can correct is one you will trust and one that just asserts things
// about you is one you will resent; a topic named after a colour code is the
// second kind, and no amount of renaming in Trends fixes a vocabulary that never
// contained the real words.
//
// sanitize.Text parses and takes the text nodes, which drops tags and attributes,
// skips script and style outright, and decodes entities — so `&nbsp;` becomes a
// space rather than the token `nbsp`. It costs one HTML parse per engaged item in
// a background job, which is the right place to spend it.
//
// Summary is the fallback and is also HTML in practice: feeds put markup in
// <description> constantly, so it gets the same treatment rather than being
// trusted because it is short.
func itemText(title, contentHTML, summary string) string {
	body := contentHTML
	if body == "" {
		body = summary
	}
	return title + " " + sanitize.Text(body)
}

// dwellWeight scores a dwell observation, which its prior cannot.
//
// signals.Classify turns milliseconds plus the article's length into a judgement —
// Glance, Bounce, Skim or Read — and that judgement is the signal. The mapping
// back to a weight is deliberately aligned with the neighbouring priors rather
// than invented: a Read is worth about what Completed is worth (0.8), a Skim about
// what an Opened is (0.3), and a Bounce carries the same negative as the explicit
// Bounced kind (-1.0), because they are the same event observed two ways.
//
// A Glance scores zero rather than negative. Below GlanceFloorMS no conclusion is
// drawn at all, and treating "the row went past" as a rejection is how a scorer
// learns that the reader dislikes everything they subscribe to.
//
// words == 0 means the length is unknown, which is common for a truncated feed. It
// returns the Skim weight rather than guessing in either direction: crediting a
// full read on no evidence inflates every short item, and scoring it negative
// punishes the reader for the publisher's excerpt policy.
func dwellWeight(dwellMS int64, words int) float64 {
	if dwellMS <= 0 {
		return 0
	}
	if words <= 0 {
		return 0.3
	}
	switch signals.Classify(dwellMS, words, 0) {
	case signals.Read:
		return 0.8
	case signals.Skim:
		return 0.3
	case signals.Bounce:
		return -1.0
	default:
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

	// One pass over the candidates buys both of the story-level signals: how many
	// OTHER sources carried this, and whether it is a near-duplicate of something
	// already on the page. See corroborate.
	stories := corroborate(candidates, corpus)

	for _, it := range candidates {
		aff := byFeed[it.SourceID]
		host := strings.TrimPrefix(urlnorm.Host(it.URL), "www.")

		vec := corpus.TFIDF(itemText(it.Title, it.ContentHTML, it.Summary))
		topicIdx, topicScore := topics.Nearest(vec, topicSet)

		published, _ := time.Parse(time.RFC3339Nano, it.PublishedAt)
		st := stories[it.ID]
		rankItem := rank.Item{
			ID:              it.ID,
			SourceID:        it.SourceID,
			PublishedAt:     published,
			TopicScore:      topicScore,
			TargetDomain:    host,
			ManualWeight:    1,
			Corroboration:   st.otherSources,
			SimilarToRecent: st.duplicateOf,
			Skips:           skipCount(itemSignals[it.ID]),
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

// SameStoryThreshold is the cosine similarity at which two items are treated as
// the same story rather than merely the same subject.
//
// Much tighter than topics.DefaultThreshold (0.18), and the gap between the two is
// the point: 0.18 groups "articles about phone batteries", which is a topic, while
// this has to group "articles about THIS phone's battery announcement", which is a
// story. Set it near the topic threshold and every tech article collapses into one
// cluster, the corroboration count becomes "how many feeds I subscribe to", and the
// duplicate penalty suppresses most of the page.
//
// 0.45 on TF-IDF over title plus summary, and that number is measured rather than
// guessed. The cross-source pair distribution over 200 real unread candidates from
// thirty feeds, within the window:
//
//	0.05–0.20   400 pairs   ← shared field vocabulary; "two tech articles"
//	0.20–0.30     9
//	0.30–0.40    10
//	0.40–0.45     3
//	0.45–0.50     1
//	0.50–0.60     1
//	0.60–0.70     1
//	0.70+         3         ← max 1.000: literal syndication, same text twice
//
// Ninety-three percent of pairs are below 0.20 and the top of the distribution is
// nearly empty, so almost anywhere in the sparse middle "works". 0.45 sits above
// the 0.40–0.45 shoulder and keeps the six genuine matches, including the three
// identical ones an aggregator produced by re-publishing a feed already subscribed
// to. Dropping to 0.30 would admit thirteen more pairs that are the same SUBJECT,
// at which point the duplicate penalty starts suppressing real coverage — which is
// the expensive direction of error, because it removes articles silently.
const SameStoryThreshold = 0.45

// SameStoryWindow is how far apart two items may be published and still be the
// same story.
//
// Seventy-two hours. A story is carried by other outlets within a day or two; past
// that, matching text is a follow-up, a recap or an anniversary piece, and calling
// it a duplicate would suppress genuinely new coverage. It also stops a feed's
// archive from corroborating its own back catalogue.
const SameStoryWindow = 72 * time.Hour

// story is what the corroboration pass concludes about one item.
type story struct {
	// otherSources is how many OTHER subscribed sources carried this story.
	otherSources int
	// duplicateOf is 0 for the item chosen to represent the story and the measured
	// similarity for every other member, which is what feeds rank's duplicate
	// penalty.
	duplicateOf float64
}

// corroborate finds items that are the same story from different sources.
//
// # Why this exists
//
// rank.Item has carried Corroboration and SimilarToRecent from the beginning, the
// scorer weights both (0.6 and 0.9 by default), rank.Reason has prose for the
// corroboration case, and internal/rank's own tests exercise all of it. Nothing
// ever set either field. They were zero on every item in production, so the branch
// was unreachable and the two most legible statements the ranker can make —
// "five of your feeds carried this" and "this is the same story you already have" —
// could never be made.
//
// # One pass, two signals
//
// Both signals are the same question asked twice: which candidates are the same
// story? Grouping once and reading the answer two ways is cheaper than two passes
// and, more importantly, keeps them consistent — a version that counted
// corroboration on one grouping and deduplicated on another could promote an item
// for being widely carried and then suppress it for being a duplicate of itself.
//
// # Choosing the representative
//
// The EARLIEST published member represents the story. That rewards the outlet that
// carried it first rather than whichever copy happens to sort well, which is the
// behaviour a reader would defend if asked. It also makes the choice stable: it
// does not depend on scores that have not been computed yet, so the grouping is
// pure and the whole function is testable without a database.
//
// # Cost
//
// Pairwise, so O(n²) in the candidate count — MaxRanked*3 = 600 at the ceiling,
// which is 180,000 cosine calls over sparse pruned vectors in a background job. The
// alternative, agglomerative clustering, costs the same asymptotically and gives
// transitive groups, which is wrong here: A near B and B near C does not make A the
// same story as C, and chained merges are how a "story" grows to forty items.
func corroborate(candidates []store.Item, corpus *textvec.Corpus) map[string]story {
	type cand struct {
		id        string
		sourceID  string
		published time.Time
		vec       textvec.Vector
	}
	items := make([]cand, 0, len(candidates))
	for _, it := range candidates {
		// Title plus summary, not the full body. A story is identified by what it
		// announces, and two outlets' full articles diverge in ways that dilute the
		// signal exactly where it needs to be sharpest. A missing summary falls back
		// to the title alone rather than to the body, for the same reason.
		text := it.Title
		if it.Summary != "" {
			text += " " + sanitize.Text(it.Summary)
		}
		vec := corpus.TFIDF(text)
		if len(vec) == 0 {
			continue
		}
		published, _ := time.Parse(time.RFC3339Nano, it.PublishedAt)
		items = append(items, cand{id: it.ID, sourceID: it.SourceID, published: published, vec: vec})
	}

	// Deterministic order, so the representative of a tie is always the same item
	// and a re-derivation produces an identical result. The rebuild property in
	// TestDerivedStateRebuildsIdentically depends on this.
	sort.Slice(items, func(i, j int) bool {
		if !items[i].published.Equal(items[j].published) {
			return items[i].published.Before(items[j].published)
		}
		return items[i].id < items[j].id
	})

	out := make(map[string]story, len(items))
	// group[i] is the index of the item representing i's story. Because items are
	// sorted by publication, the representative is always the earliest member.
	group := make([]int, len(items))
	sim := make([]float64, len(items))
	for i := range items {
		group[i] = i
	}
	for i := range items {
		if group[i] != i {
			// Already a member of an earlier story. Not used as a representative,
			// which is what keeps groups from chaining transitively.
			continue
		}
		for j := i + 1; j < len(items); j++ {
			if group[j] != j {
				continue
			}
			if items[j].published.Sub(items[i].published) > SameStoryWindow {
				// Sorted by publication, so everything after j is further away too.
				break
			}
			if items[i].sourceID == items[j].sourceID {
				// The same feed publishing twice about one thing is a follow-up, not
				// corroboration. Counting it would let one busy source manufacture
				// its own supporting evidence.
				continue
			}
			if c := textvec.Cosine(items[i].vec, items[j].vec); c >= SameStoryThreshold {
				group[j] = i
				sim[j] = c
			}
		}
	}

	// Distinct other sources per representative.
	sources := make([]map[string]bool, len(items))
	for i := range items {
		rep := group[i]
		if sources[rep] == nil {
			sources[rep] = map[string]bool{}
		}
		sources[rep][items[i].sourceID] = true
	}
	for i := range items {
		rep := group[i]
		others := len(sources[rep]) - 1
		if others < 0 {
			others = 0
		}
		out[items[i].id] = story{otherSources: others, duplicateOf: sim[i]}
	}
	return out
}

// SkipMinImpressions is how many times an item must have been on screen before
// passing it over counts as a skip.
//
// Three. One impression is the denominator and says nothing (R17). Two is a list
// that was scrolled and scrolled back — the same glance, twice. Three separate
// exposures is the point at which "I have seen this and not chosen it" is a fairer
// reading than "it has not had its turn yet".
const SkipMinImpressions = 3

// skipCount derives §18.1's never-emitted `skipped` signal from the impression log.
//
// # Why this is derived here rather than sent by the client
//
// The taxonomy defines Skipped as "visible repeatedly ACROSS SESSIONS and still
// passed over". A browser tab cannot see across sessions — it sees the one it is —
// so a client-side emitter would either report every scroll-past as a skip or need
// its own history of what it had already reported. The engagement log already holds
// the whole picture, and deriving it keeps the rule that `engagements` is the only
// irreplaceable table: change this function, re-derive, and the answer changes with
// no migration and no lost data.
//
// # The two guards that keep it from being a blunt impression penalty
//
// R17 is the thing at risk here. Impressions are the most numerous rows in the table
// by a wide margin — 189 of 977 distinct items on a real database — and a scorer
// that reads them as rejection concludes the reader dislikes everything they
// subscribe to.
//
//  1. ANY affinity-bearing engagement disqualifies a skip. Opened, dwelt on, liked,
//     even bounced off: all of those are the reader ACTING on the item, and an item
//     that was acted on was not skipped. Bounced already carries its own negative,
//     so counting it here as well would penalise one decision twice.
//
//  2. The exposures must span more than one sitting. Forty impressions inside one
//     session is one scroll through a long list, not forty rejections. The span test
//     uses signals.SessionGapMS, the same thirty-minute boundary SameSession uses, so
//     "what counts as a separate sitting" has one definition.
//
// FirstAt and LastAt are min/max across every kind, not impressions specifically.
// That is exact rather than approximate for the items this function can return a
// non-zero count for: guard 1 means such an item has impressions and NOTHING else,
// so the overall span is the impression span.
func skipCount(sig store.ItemSignal) int {
	impressions := sig.Counts[signals.Impression]
	if impressions < SkipMinImpressions {
		return 0
	}
	for kind, n := range sig.Counts {
		if n <= 0 || kind == signals.Impression {
			continue
		}
		if signals.MovesAffinity(kind) {
			return 0
		}
	}
	if sig.LastAt-sig.FirstAt < signals.SessionGapMS {
		return 0
	}
	// The count is exposures BEYOND the threshold, so an item at exactly the
	// threshold gets the smallest possible penalty rather than a step change.
	return impressions - SkipMinImpressions + 1
}

// deliberateKinds are the acts that cost the reader something, and their re-rank
// multipliers per occurrence.
//
// Sparse by nature — that sparsity is the whole argument for the precision stage.
// A verdict, a note or a tag is evidence of WORTH; dwell and completion are evidence
// of stickiness, and D18 keeps them apart so the page cannot converge on whatever is
// most trivially clickable.
//
// `later` is here at a low weight because putting something aside is a real choice
// that costs a press, but it is a promise rather than a verdict — a saved article is
// not yet a read one. `selected` likewise: highlighting text means engagement with
// the argument, and the signal records only the LENGTH, never the content (§18.8).
//
// NotInterested is deliberately absent: it is a source- and topic-level instruction,
// applied through topic suppression in the caller, not an item multiplier. Applying
// it here as well would count one press twice.
var deliberateKinds = map[signals.Kind]float64{
	signals.Liked:        0.15,
	signals.Noted:        0.15,
	signals.Tagged:       0.15,
	signals.ClickedOut:   0.10,
	signals.SearchOpened: 0.10,
	signals.Later:        0.05,
	signals.Selected:     0.05,
	signals.Disliked:     -0.40,
}

// applyDeliberate is the re-rank D18 asks for.
//
// It multiplies rather than adds, and that is the distinction from a weighted
// term: a deliberate act SCALES what recall already thought, so it cannot on its
// own promote something recall found uninteresting, and it cannot be diluted by
// the sum of a dozen passive terms either.
//
// # Which kinds count as deliberate
//
// The set is named here rather than taken from signals.Lookup, and that is a real
// distinction from affinityWeight rather than the same mistake twice: D18's split
// is not "positive versus negative", it is DELIBERATE versus PASSIVE, and the
// registry does not record which is which. A prior says how much a signal is worth;
// it does not say whether the reader chose to spend something to produce it. Dwell
// and reread have priors and are passive — they happen while reading. Liking,
// noting, tagging and following a link out cost an action.
//
// It is still checked against the registry: deliberateKinds names real kinds, and a
// test asserts every one of them exists. The first version of this switch had cases
// for `starred` and `clicked_through`, neither of which is a kind — so the
// click-through multiplier had never once applied.
func applyDeliberate(r rank.Result, sig store.ItemSignal) rank.Result {
	if len(sig.Counts) == 0 {
		return r
	}
	factor := 1.0
	for kind, n := range sig.Counts {
		if w, ok := deliberateKinds[kind]; ok {
			factor += w * float64(n)
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
