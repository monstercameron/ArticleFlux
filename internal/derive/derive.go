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
	// plus is the Smart+ tier. Nil on every instance without an API key, which is the
	// normal case and the one the free tier is built to be good enough for.
	plus Enhancer
}

// Enhancer is the Smart+ half of the interest layer (§10.5, §18.8).
//
// # Why an interface here rather than an import
//
// This package must not depend on internal/llm or internal/smart. Two reasons, and the
// second is the one that matters.
//
// The interest layer has to work with no API key at all — free Smart is the product, and
// a paid tier is an addition to it, not the thing that makes it function. An import would
// make that a runtime nil-check in a dozen places instead of a structural fact.
//
// And the egress boundary (§18.8) is enforced by TYPES in internal/llm: the payload shapes
// have fields only for what may leave the machine, so building the request is the
// enforcement. If this package assembled those payloads it would become a second place
// that has to be audited, and the whole argument for that design is that there should be
// one. The adapter in internal/smart builds them; this package hands over plain data and
// never sees the wire.
//
// Every method may return an error, and every caller treats an error as "no Smart+ this
// time" and continues with the free-tier answer. A model that is down, rate-limited or
// unconfigured must degrade the ranking, never fail the derivation.
type Enhancer interface {
	// RerankCandidates reorders the strongest candidates and returns the indexes it
	// wants, best first. It may return fewer than it was given, and any index it omits
	// keeps its free-tier position behind the ones it chose.
	//
	// Indexes rather than ids: what crosses the boundary is an ordinal (see
	// llm.Candidate), so the caller's slice position is the only shared vocabulary.
	RerankCandidates(ctx context.Context, cands []Candidate, prof ProfileHint, want int) ([]int, error)

	// ExtractEntities names the brands, products and organisations in a set of
	// headlines. Titles only — no bodies, no URLs, no dates.
	ExtractEntities(ctx context.Context, titles []string) ([]NamedEntity, error)

	// LabelTopic writes a human label for a cluster from its top terms. The
	// deterministic label is passed as the fallback the model is improving on.
	LabelTopic(ctx context.Context, terms []string, fallback string) (string, error)
}

// Candidate is one item offered for Smart+ re-ranking. Title and summary only, matching
// what §18.8 permits to leave.
type Candidate struct {
	Title   string
	Summary string
}

// ProfileHint is the derived description of the reader that Smart+ is allowed to see:
// topic labels with their terms, and source titles. Never a URL, never a log.
type ProfileHint struct {
	Topics  []TopicHint
	Sources []string
}

// TopicHint is one interest as the provider sees it.
type TopicHint struct {
	Label string
	Terms []string
}

// NamedEntity is one extracted brand, product or organisation.
type NamedEntity struct {
	// Name is the normalised, lowercased matching key.
	Name string
	// Label is the display form, as the model wrote it.
	Label string
}

// scoredItem is one candidate with its free-tier score.
//
// Package level rather than local to deriveHomeRanking, because applySmartPlus reorders
// exactly this slice. Passing it as a parameter is what keeps the Smart+ pass unable to
// change any score — it can permute the slice and nothing else.
type scoredItem struct {
	item store.Item
	res  rank.Result
}

// entityAcc accumulates the evidence for one named thing across engaged items.
type entityAcc struct {
	label    string
	weight   float64
	mentions int
	// sources is not a threshold — requiring two of them cost twelve real entities out of
	// fourteen on real data (see the note above EntityMinMentions). It is kept because it
	// is the natural evidence for a future "covered by three of your feeds" line, and
	// because dropping it would make that measurement have to be rediscovered.
	sources map[string]bool
	last    time.Time
	// llm marks an entity the model found. Recorded so removing the API key visibly
	// narrows the list rather than silently changing what the rows mean.
	llm bool
}

// New builds the service.
func New(repo *store.ReaderRepo, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// WithSmartPlus adds the paid tier.
//
// Separate from New so the free path is the DEFAULT rather than a nil case someone
// remembered to handle: every test, every instance without a key, and every code path that
// does not opt in gets a Service that cannot make a network call at all.
//
// Being wired is NOT consent — see SmartPlusPrefKey. Every call is additionally gated on a
// per-user preference that defaults to off.
func (s *Service) WithSmartPlus(e Enhancer) *Service {
	s.plus = e
	return s
}

// SmartPlusPrefKey is the per-user opt-in for Smart+ ranking. Absent or anything but
// "true" means off.
//
// # Default-off, and why this needed fixing rather than shipping
//
// The first version attached the enhancer in internal/app and called it on every
// derivation. It worked, and it was wrong for a reason that has nothing to do with
// correctness: this spends the reader's money. A derivation fires after every poll and
// after every batch of engagements, so an instance with a key in its environment was
// making paid API calls on a schedule nobody agreed to, for a feature nobody asked for.
//
// It follows the precedent `tts.smartPlus` set for exactly this shape of decision: a
// feature that egresses data AND bills someone must never become active because of a
// default. Wiring is capability; the preference is consent, and they are deliberately two
// different things.
//
// A THIRD opt-in rather than a mode of the voice ones, and for their stated reason: the
// voice sends the article you are reading, and this sends forty titles plus a description
// of your interests. Someone who consented to one has not consented to the other, and a
// reader watching their bill should be able to tell them apart on the settings screen.
const SmartPlusPrefKey = "feed.smartPlus"

// plusFor returns the enhancer this derivation may use: the wired one when this reader has
// opted in, and nil otherwise.
//
// # Resolved once per run and threaded down, rather than checked at each call site
//
// Service is shared by every user on the instance, so the answer cannot be cached on the
// struct — that would be both a data race and a consent leak, with one reader's opt-in
// deciding another's. And checking at each of the three call sites would read the
// preference three times per derivation and, worse, allow the three to DISAGREE if the
// reader toggled it mid-run: entities extracted under consent, the ranking not.
//
// So it is a value: nil means every downstream nil-check already written does the right
// thing, which is why the three helpers take an Enhancer rather than a bool.
//
// Read fresh every derivation rather than at boot, so switching it off takes effect on the
// next run instead of at the next restart. That matters most in the direction that costs
// money: a reader who turns it off because of the bill must not have to restart a server to
// be believed.
//
// A read error is OFF. The safe direction for a consent flag that cannot be read is to
// spend nothing.
func (s *Service) plusFor(ctx context.Context, sc store.Scope) Enhancer {
	if s.plus == nil {
		return nil
	}
	prefs, err := s.repo.GetPrefs(ctx, sc)
	if err != nil || prefs[SmartPlusPrefKey] != "true" {
		return nil
	}
	return s.plus
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
	// Entities is how many brands, products and organisations survived
	// EntityMinMentions. Logged separately from Terms because it answers a different
	// question — "can this instance name what you follow" — and a vocabulary of two
	// thousand terms with zero entities is a specific, diagnosable state.
	Entities int
	Topics   int
	Ranked   int
	// ColdStart is true when there was too little engagement for topics to mean
	// anything. §18.4 wants the UI to say so rather than present a confident
	// wrong answer.
	ColdStart bool
	// SmartPlus reports whether the paid tier was permitted on this run.
	//
	// Logged, because "why did my ranking not change" and "why am I being billed" are both
	// questions an operator answers from the log, and the answer to both is this flag. It
	// reflects CONSENT, not availability: a wired enhancer with the preference off reads
	// false, which is the distinction that matters.
	SmartPlus bool
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

	// Consent, resolved once for the whole run. Nil unless this reader has explicitly
	// turned Smart+ on — being WIRED is a capability, not permission to spend their money.
	plus := s.plusFor(ctx, sc)
	res.SmartPlus = plus != nil

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

	// Recall, not precision: entities describe what the reader follows, which is a
	// candidate-generation fact. It reads `engaged` and nothing that stage 2 writes, so
	// its position here is a real ordering constraint rather than a convenience.
	entities, err := s.deriveEntities(ctx, sc, plus, engaged)
	if err != nil {
		return res, fmt.Errorf("derive: entities: %w", err)
	}
	res.Entities = entities

	topicSet, suppressed, cold, err := s.deriveTopics(ctx, sc, plus, engaged, vectors, now)
	if err != nil {
		return res, fmt.Errorf("derive: topics: %w", err)
	}
	res.Topics = len(topicSet)
	res.ColdStart = cold

	// ---- Stage 2: precision ---------------------------------------------
	//
	// Only now, and only reading what stage 1 wrote plus the deliberate acts.

	ranked, err := s.deriveHomeRanking(ctx, sc, plus, feeds, topicSet, suppressed, corpus, now)
	if err != nil {
		return res, fmt.Errorf("derive: home ranking: %w", err)
	}
	res.Ranked = ranked

	if s.log != nil {
		s.log.Info("derived the interest layer",
			"user", sc.UserID, "feeds", res.Feeds, "terms", res.Terms,
			"entities", res.Entities,
			"domains", res.Domains, "topics", res.Topics, "ranked", res.Ranked,
			"cold_start", res.ColdStart, "smart_plus", res.SmartPlus)
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
	ItemID   string
	SourceID string
	Text     string
	// Title on its own, for entity extraction.
	//
	// Kept separately rather than recovered from Text because a headline is a
	// qualitatively different kind of evidence: a publisher writes it to name the thing
	// the article is about, so a capitalised phrase there is almost always a real name.
	// Body text is where a feed's own furniture lives. See deriveEntities.
	Title     string
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
			Title:     it.Title,
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
func (s *Service) deriveTopics(ctx context.Context, sc store.Scope, plus Enhancer, engaged []engagedItem,
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

	// Smart+ writes the labels a join of top terms cannot.
	//
	// §18.2 wants the reason line to read "matches your NPU inference reading" rather
	// than "Nbsp · Font · 6f6f6f" — which is a real label this application produced
	// before the vectoriser stopped eating HTML. The deterministic label is passed as the
	// fallback the model is improving on, and is kept whenever the model declines, errors,
	// or returns something too long to fit a chip.
	//
	// Before ReplaceTopics, so the label the reader's own rename is compared against is
	// the final one. Reversing the two would let a Smart+ label overwrite a rename on
	// every derivation, which is the one thing §18.2 is explicit about not doing.
	if plus != nil {
		s.labelTopics(ctx, plus, res.Topics)
	}

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

// MaxLabelledTopics bounds how many topics get a Smart+ label per derivation.
//
// Twelve. One call per topic, so this is the cost ceiling for the labelling pass — and
// past a dozen topics the tail is small clusters the reader will not recognise anyway.
// They keep their deterministic labels, which are adequate for a three-member cluster.
const MaxLabelledTopics = 12

// labelTopics replaces deterministic top-term labels with written ones, in place.
//
// Sequential rather than concurrent, deliberately. This runs in a background job nobody is
// waiting on, and twelve parallel requests to one provider is how an instance earns a rate
// limit that then breaks the re-rank — the call that actually changes what the reader sees.
//
// A failure on one topic leaves that topic's label alone and continues to the next. There
// is no reason a single unlucky cluster should cost the other eleven their labels.
func (s *Service) labelTopics(ctx context.Context, plus Enhancer, ts []topics.Topic) {
	n := 0
	for i := range ts {
		if n >= MaxLabelledTopics {
			return
		}
		if len(ts[i].TopTerms) == 0 {
			continue
		}
		n++
		label, err := plus.LabelTopic(ctx, ts[i].TopTerms, ts[i].Label)
		if err != nil {
			if s.log != nil {
				s.log.Info("smart+ topic label declined; keeping the term-based one",
					"fallback", ts[i].Label, "err", err)
			}
			continue
		}
		ts[i].Label = label
	}
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
func (s *Service) deriveHomeRanking(ctx context.Context, sc store.Scope, plus Enhancer,
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

	// The reader's own exclusions, which the ranking ignored entirely until now.
	//
	// `in_megafeed = 0` and an active `muted_until` are both the reader saying "not on
	// my front page", and a ranked homepage that overrules them is worse than one that
	// does not exist: the setting is visibly present in feed settings and visibly does
	// nothing. Items stay fully readable in the feed's own list, which is the whole
	// distinction the setting draws.
	eligible, err := s.repo.MegafeedSources(ctx, sc)
	if err != nil {
		return 0, err
	}

	// The named things the reader follows, for the entity term and the content-match gate.
	followed, err := s.repo.EntityAffinity(ctx, sc, MaxEntities)
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

	out := make([]scoredItem, 0, len(candidates))

	// One pass over the candidates buys both of the story-level signals: how many
	// OTHER sources carried this, and whether it is a near-duplicate of something
	// already on the page. See corroborate.
	stories := corroborate(candidates, corpus)

	for _, it := range candidates {
		if !eligible[it.SourceID] {
			continue
		}
		aff := byFeed[it.SourceID]
		host := strings.TrimPrefix(urlnorm.Host(it.URL), "www.")

		vec := corpus.TFIDF(itemText(it.Title, it.ContentHTML, it.Summary))
		topicIdx, topicScore := topics.Nearest(vec, topicSet)
		// Below the clustering cut, "nearest topic" is not a match.
		//
		// topics.Nearest returns the closest centroid and its cosine, and that cosine is
		// non-zero for almost any item sharing any vocabulary with anything the reader has
		// read. rank emits its topic reason on `TopicScore > 0`, so without this floor
		// nearly every candidate claimed a topic match — measured: 197 of 200 passed the
		// content gate, which made the gate decorative and My Feed the unread list again.
		//
		// The floor is topics.DefaultThreshold, the same 0.18 that decides whether two
		// items belong in one cluster. That equivalence is the argument: an item called
		// "close to a topic you read" should be at least as close to it as its own members
		// are to each other. Anything looser is a coincidence of shared words.
		if topicScore < topics.DefaultThreshold {
			topicIdx, topicScore = -1, 0
		}

		published, _ := time.Parse(time.RFC3339Nano, it.PublishedAt)
		st := stories[it.ID]
		rankItem := rank.Item{
			ID:              it.ID,
			SourceID:        it.SourceID,
			PublishedAt:     published,
			TopicScore:      topicScore,
			Entities:        namedIn(it.Title, followed),
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
		// A recommendation has to be ABOUT something. This is the gate that makes My Feed
		// a recommendation list rather than the unread list reordered — see
		// hasContentMatch.
		if !hasContentMatch(r) {
			continue
		}
		r = applyDeliberate(r, itemSignals[it.ID])
		out = append(out, scoredItem{item: it, res: r})
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

	// Smart+ re-orders the head of the list, and only the head.
	//
	// Free Smart still does all of recall and all of the scoring; the model is shown the
	// candidates that already won and asked which of those to lead with. That division
	// is deliberate and is the one §18.8 permits: the payload is titles, summaries and a
	// derived profile, so a model can judge "which of these is the better read" and
	// cannot be handed the reading history it would need to do candidate generation.
	//
	// It is also the division that keeps the free tier honest. If the model chose the
	// candidate set, an instance without a key would be running a different and worse
	// product rather than the same product without a garnish.
	plusTier := map[string]bool{}
	if plus != nil {
		plusTier = s.applySmartPlus(ctx, plus, out, topicSet, feeds)
	}

	rows := make([]store.RankedItem, 0, len(out))
	for i, sc := range out {
		// Both halves of each reason: the term is what the client renders a short,
		// translatable label from, and the prose is the hover and the fallback. Storing
		// only the prose made the explanations untranslatable and unreadable at list
		// width — see store.RankReason.
		reasons := make([]store.RankReason, 0, len(sc.res.Reasons))
		for _, r := range sc.res.Reasons {
			reasons = append(reasons, store.RankReason{Term: r.Term, Text: r.Text})
		}
		tier := store.TierSmart
		if plusTier[sc.item.ID] {
			tier = store.TierSmartPlus
		}
		rows = append(rows, store.RankedItem{
			ItemID:  sc.item.ID,
			Score:   sc.res.Score,
			Rank:    i + 1,
			Slot:    slotFor(i, len(out)),
			Reasons: reasons,
			Tier:    tier,
		})
	}
	if err := s.repo.ReplaceHomeRanking(ctx, sc, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// contentTerms are the scoring factors that say what an item is ABOUT.
//
// Everything else in the formula describes its CIRCUMSTANCES: how new it is, which feed
// carried it, how much that feed publishes. Those are real signals and they are not reasons
// to recommend something.
var contentTerms = map[string]bool{
	// The item matches a cluster of things the reader has read.
	"topic": true,
	// Its headline names a brand, product or organisation they follow.
	"entity": true,
	// Several of their feeds carried the same story, which is a fact about the story
	// rather than about any one feed.
	"corroboration": true,
	// The reader wrote a rule that weights this. Their own instruction outranks any
	// inference, so it counts.
	"manual": true,
	// `domain` is deliberately ABSENT, and it is the one that looks like it belongs.
	//
	// §18.6's argument for domain affinity is that an aggregator item almost never points
	// at the aggregator, so where an item POINTS is a different signal from which feed
	// mentioned it. True — for aggregators. For a publisher's own feed the target domain is
	// that publisher's domain, so "points at engadget.com, which you keep opening" is the
	// same fact as "from a feed you read closely", stated twice.
	//
	// That is most feeds, and it showed in the numbers: on a real database `domain`
	// appeared on 138 of 200 rows and `feed` on 149, largely the same rows. Admitting it
	// here would let the gate pass nearly everything on what is really feed affinity
	// wearing a second name — which is precisely the "defaults from all feeds" this gate
	// exists to stop. It still CONTRIBUTES to the score; it just cannot be the sole reason
	// an item is called a recommendation.
}

// hasContentMatch reports whether a scored item earned its place on something other than
// being new and arriving from a feed the reader opens.
//
// # Why My Feed refuses to fill itself
//
// Without this gate every unread item is ranked, which on a real database meant 200 rows
// whose reasons were `fresh` on all 200, `feed` on 149 and `domain` on 138. That is a
// correctly-ordered list and it is not a recommendation list: the reader sees their unread
// items in a different order and no statement about why any of them is there. The
// complaint it produced was exact — "filled with defaults from all feeds".
//
// So an item needs at least one CONTENT reason. If none of the candidates has one, the
// ranked page is empty and the UI says the interest layer is still learning, which is true
// and is better than a confident wrong answer (§18.4). Empty and honest beats full and
// meaningless — and the empty state names what fixes it, which the reader is the only one
// who can do.
//
// # Why the negative terms cannot qualify
//
// Only positive contributions count. `duplicate` and `skipped` are content-level
// observations, and an item held up as a recommendation BECAUSE it is a near-duplicate of
// something already shown would be the gate working backwards.
func hasContentMatch(r rank.Result) bool {
	for _, why := range r.Reasons {
		if why.Delta > 0 && contentTerms[why.Term] {
			return true
		}
	}
	return false
}

// namedIn returns the followed entities whose names appear in a headline.
//
// Title only, matching where the entities were found in the first place (see
// deriveEntities): bodies carry feed furniture and headlines carry names. Comparing against
// the body would also match an entity mentioned once in passing, which is not what the
// article is about.
//
// Labels are returned, not keys, because the caller puts them in a sentence a reader will
// read: "about Android Auto, which you follow" — not "android auto".
func namedIn(title string, followed []store.Entity) []string {
	if title == "" || len(followed) == 0 {
		return nil
	}
	lower := strings.ToLower(title)
	var out []string
	for _, e := range followed {
		if strings.Contains(lower, e.Name) {
			out = append(out, e.Label)
		}
	}
	return out
}

// SmartPlusHead is how many of the top candidates Smart+ is asked about.
//
// Forty. It is a cost and an attention bound at once. The model is being asked "which of
// these should lead", and a reader works through the first screen or two of a ranked page
// — re-ordering position 150 is tokens spent on a row nobody reaches. Forty titles and
// summaries is also a request that fits comfortably in one call, so a derivation makes at
// most one model call for ranking however much the reader has read.
const SmartPlusHead = 40

// SmartPlusWant is how many picks the model is asked to return.
//
// Twelve, out of forty. Asking it to order all forty would spend reasoning on the tail it
// was not chosen for, and a full ordering is also harder to sanity-check: a short list of
// leads can be verified by reading it, while a permutation of forty cannot.
const SmartPlusWant = 12

// applySmartPlus lets the model reorder the head of the ranked list.
//
// Returns the set of item ids the model actually promoted, which becomes the `smart_plus`
// tier marking. Items it left alone stay `smart` — see store.TierSmartPlus: the tier is a
// claim about what decided the placement, and an agreement is not an influence.
//
// # Failure is always "keep the free-tier answer"
//
// Every error path here returns without touching `out`. A missing key, a timeout, a
// rate limit, a malformed reply, an index the model invented: all of them mean the reader
// gets free Smart's ordering, which is a complete and useful product. Nothing about
// Smart+ is allowed to produce an empty or broken homepage, and the way to guarantee that
// is for the failure branch to do nothing rather than to do something careful.
//
// # Why it reorders in place rather than rescoring
//
// The model returns a preference order, not scores. Inventing numbers to slot its picks
// into the existing score space would make the stored score a mixture of two
// incomparable scales — and `score` is what the debug tools, the tuning panel and any
// future explanation read. So the scores stay exactly as free Smart computed them and only
// the ORDER changes; the reasons stay attached to their items, which is what keeps every
// row's explanation true after the move.
func (s *Service) applySmartPlus(ctx context.Context, plus Enhancer, out []scoredItem,
	topicSet []topics.Topic, feeds []store.FeedAffinity) map[string]bool {

	head := len(out)
	if head > SmartPlusHead {
		head = SmartPlusHead
	}
	if head < 2 {
		// Nothing to reorder. Worth returning early rather than making the call: a
		// one-item page has no ordering, and paying for an opinion about it is pure waste.
		return nil
	}

	cands := make([]Candidate, 0, head)
	for _, sc := range out[:head] {
		cands = append(cands, Candidate{
			Title: sc.item.Title,
			// Plain text, not markup. The summary is HTML in practice and sending tags
			// would spend tokens on furniture and hand a provider markup it has no use
			// for. sanitize.Text is the same call the vectoriser makes for the same
			// reason.
			Summary: firstRunes(sanitize.Text(sc.item.Summary), 300),
		})
	}

	prof := ProfileHint{}
	for _, t := range topicSet {
		prof.Topics = append(prof.Topics, TopicHint{Label: t.Label, Terms: t.TopTerms})
	}
	// Source TITLES by affinity, never their URLs — a feed URL is a stable identifier that
	// would let a provider recognise this instance across requests (§18.8).
	// internal/llm.RankPayload.Trim caps the count; this only has to order them.
	//
	// Titles come from the candidates rather than from a new query: the items already
	// carry the resolved title, and the alternative is another round trip to name feeds
	// that are by definition already represented on the page.
	titleOf := map[string]string{}
	for _, sc := range out {
		if t := sc.item.SourceTitle; t != "" {
			titleOf[sc.item.SourceID] = t
		}
	}
	sorted := make([]store.FeedAffinity, len(feeds))
	copy(sorted, feeds)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Score != sorted[j].Score {
			return sorted[i].Score > sorted[j].Score
		}
		// A tie-break, so the profile is byte-identical on a re-derivation over unchanged
		// input. Without it map order leaks into an outbound request body.
		return sorted[i].SourceID < sorted[j].SourceID
	})
	for _, f := range sorted {
		if t := titleOf[f.SourceID]; t != "" {
			prof.Sources = append(prof.Sources, t)
		}
	}

	picks, err := plus.RerankCandidates(ctx, cands, prof, SmartPlusWant)
	if err != nil {
		if s.log != nil {
			// Info, not Warn. A Smart+ call that did not happen is not a fault in this
			// instance — no key, no network and a provider outage are all ordinary — and
			// logging it as a warning trains the operator to ignore warnings.
			s.log.Info("smart+ rerank declined; keeping the free ranking", "err", err)
		}
		return nil
	}

	// Validate every index before moving anything. A model returning 47 for a list of 40,
	// or the same index twice, must not be able to panic a background job or duplicate a
	// row — and both are exactly what a plausible-looking reply does when it drifts.
	seen := make(map[int]bool, len(picks))
	order := make([]int, 0, len(picks))
	for _, i := range picks {
		if i < 0 || i >= head || seen[i] {
			continue
		}
		seen[i] = true
		order = append(order, i)
	}
	if len(order) == 0 {
		return nil
	}

	promoted := make([]scoredItem, 0, head)
	tier := make(map[string]bool, len(order))
	for _, i := range order {
		promoted = append(promoted, out[i])
		tier[out[i].item.ID] = true
	}
	// Everything the model did not pick keeps its free-tier order, behind the picks. Not
	// dropped: the reader subscribed to these feeds and an item the model had no opinion
	// about is still an item they may want.
	for i := range out[:head] {
		if !seen[i] {
			promoted = append(promoted, out[i])
		}
	}
	copy(out[:head], promoted)

	// A pick that was already going to be first was not influenced by the model, so it is
	// not a Smart+ pick. This is what stops a configured key from relabelling the whole
	// page as paid-for when it changed nothing.
	for i, sc := range out[:head] {
		if i < len(order) && order[i] == i {
			delete(tier, sc.item.ID)
		}
	}
	if s.log != nil {
		s.log.Info("smart+ reranked the head of the feed",
			"considered", head, "promoted", len(order), "changed", len(tier))
	}
	return tier
}

// firstRunes truncates on a rune boundary, so a multi-byte character is never cut in
// half — a broken rune in an outbound JSON body is a request the provider rejects whole.
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
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

// MaxEntities bounds the stored entity list.
//
// A hundred and fifty. The screen that shows these is a list a person reads, and past
// a hundred-odd rows it stops being a description of someone and becomes a dump. The
// tail is also where the heuristic's false positives live — a one-off "released pixel"
// never accumulates weight — so cutting it is the same cut that removes the noise.
const MaxEntities = 150

// EntityMinMentions is how many engaged items must mention a phrase before it counts as
// something the reader follows.
//
// Two. One mention is an article that happened to name a thing; two is the beginning of a
// pattern. It is what separates a brand from a sentence-initial accident: "Android Auto"
// recurs across everything a reader of phone news opens, while "released pixel" and "four
// nerds" appear once and never again. textvec.Phrases accepts a known false positive on
// the grounds that aggregation would filter it; this is the aggregation doing that.
const EntityMinMentions = 2

// Entities come from HEADLINES, not from article bodies.
//
// # The failure this fixes, and the fix that was worse
//
// Extracting from the full text produced, on a real database: `Android 16`, `Apple
// Watch`, `Hugging Face`, `Micro Four Thirds`, `Mercedes C126`, `Seattle Times` — and
// alongside them `Continue Reading`, `News Section` and `Reading Category`. Those three
// are a FEED TEMPLATE. One publisher puts "Continue Reading" in every item's body, so
// from the extractor's side it is a capitalised phrase recurring across many engaged
// articles, which is precisely the shape of a brand.
//
// The first attempt was a structural rule: require the phrase in items from two
// different SOURCES, since template chrome comes from one source by definition. It
// worked and it cost far too much — measured on the same data, entities went from
// fourteen to TWO. At any realistic reading volume most real brands are covered by one
// of a reader's feeds, so the rule removed nearly everything true along with everything
// false.
//
// Titles are the sharper cut, and for a reason rather than by luck: a headline is
// written to name the thing the article is about, so a capitalised phrase in one is
// almost always a real name. Feed furniture lives in bodies and footers — no publisher
// puts "Continue Reading" in a headline. Same three false positives removed, and the
// real entities kept.
//
// A blocklist was the third option and the worst. It would have handled these three and
// not the fourth nobody has seen yet, and after `source` and `image` nearly deleted
// "open source" and image models from the vocabulary, hand-written word lists have
// earned their suspicion here.

// deriveEntities turns recurring capitalised phrases into named things.
//
// # What this answers that nothing else did
//
// The interest layer could say what SUBJECTS a reader follows and could not name one
// THING. Term affinity is a bag of words and topics are clusters of them, so "cameras"
// was expressible and "the Lumix line" was not — on a real database `lumix`, `powershot`
// and `vivo` sat in the vocabulary as isolated unigrams with nothing to connect them to
// the products they name.
//
// # Weight is engagement, not frequency
//
// The obvious implementation counts mentions. It is wrong in the way that matters: a
// phrase named in forty articles the reader never opened is not an interest, and a count
// says it is. Each mention contributes the ENGAGED ITEM's own weight, which already
// carries the reader's dwell, completion and verdicts, and which is already
// recency-decayed. So a brand mentioned twice in pieces that were read to the end
// outranks one mentioned nine times in headlines that were scrolled past.
//
// # Label casing has to be stored
//
// The vectoriser lowercases, so the original casing is gone by the time a phrase exists.
// It is not recoverable by rule — a deterministic title-case turns "iphone" into "Iphone"
// and "tcl" into "Tcl" — so the label is recovered from the item text that produced the
// phrase, and only stored casing is trustworthy afterwards.
func (s *Service) deriveEntities(ctx context.Context, sc store.Scope, plus Enhancer, engaged []engagedItem) (int, error) {
	byName := map[string]*entityAcc{}

	for _, e := range engaged {
		// Deduplicated per item: an article that names a product in its headline and
		// four more times in the body is one item's worth of evidence, not five. Without
		// this a single verbose article would outweigh a genuine pattern across several.
		seen := map[string]bool{}
		// Title only. See the note above EntityMinMentions: bodies carry feed furniture
		// and headlines carry names.
		for _, phrase := range textvec.Phrases(e.Title) {
			if seen[phrase] {
				continue
			}
			seen[phrase] = true
			a := byName[phrase]
			if a == nil {
				a = &entityAcc{label: properCase(e.Title, phrase), sources: map[string]bool{}}
				byName[phrase] = a
			}
			a.weight += e.Weight
			a.mentions++
			a.sources[e.SourceID] = true
			if e.EngagedAt.After(a.last) {
				a.last = e.EngagedAt
			}
		}
	}

	// Smart+ names what the heuristic cannot see: lowercase names (npm, curl, ffmpeg)
	// produce no capitalised phrase at all, and a three-word name arrives as two
	// overlapping pairs. Its answers are merged rather than replacing the heuristic's, so
	// removing the API key narrows the list instead of emptying it.
	if plus != nil {
		s.mergeLLMEntities(ctx, plus, engaged, byName)
	}

	out := make([]store.Entity, 0, len(byName))
	for name, a := range byName {
		if a.mentions < EntityMinMentions {
			continue
		}
		kind := store.EntityPhrase
		if a.llm {
			kind = store.EntityLLM
		}
		out = append(out, store.Entity{
			Name: name, Label: a.label, Kind: kind,
			Weight: a.weight, Mentions: a.mentions,
			LastSeen: a.last.Format(time.RFC3339Nano),
		})
	}
	// Sorted by weight then name: the name breaks ties so a re-derivation over unchanged
	// input produces byte-identical rows, which the rebuild test depends on. Map iteration
	// order alone would make it fail intermittently, which is the worst way for it to fail.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Weight != out[j].Weight {
			return out[i].Weight > out[j].Weight
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > MaxEntities {
		out = out[:MaxEntities]
	}
	return len(out), s.repo.ReplaceEntityAffinity(ctx, sc, out)
}

// mergeLLMEntities folds Smart+'s extracted names into the heuristic's accumulator.
//
// Merged, not substituted. A key that is configured today may be removed tomorrow, and if
// the model's answers had REPLACED the free-tier ones the entity list would empty out on
// the next derivation — the reader would experience removing a key as the feature breaking
// rather than as it getting narrower.
//
// # The mention count still comes from the headlines, not from the model
//
// The model is asked WHICH names exist, never how much the reader cares. Weight and
// mentions are recounted here by scanning the titles for each name it returned, so the
// evidence for "you follow this" remains the reader's own engagement. A model that
// enthusiastically listed forty brands cannot promote any of them past
// EntityMinMentions on its own, which is what keeps the extraction from becoming an
// opinion about the reader.
//
// Failure is silent and total: on any error the heuristic's entities stand unchanged.
func (s *Service) mergeLLMEntities(ctx context.Context, plus Enhancer, engaged []engagedItem, byName map[string]*entityAcc) {
	titles := make([]string, 0, len(engaged))
	for _, e := range engaged {
		if t := strings.TrimSpace(e.Title); t != "" {
			titles = append(titles, t)
		}
	}
	if len(titles) == 0 {
		return
	}

	found, err := plus.ExtractEntities(ctx, titles)
	if err != nil {
		if s.log != nil {
			// Info, not Warn: no key, no network and a provider outage are all ordinary,
			// and warning about them trains an operator to ignore warnings.
			s.log.Info("smart+ entity extraction declined; keeping the heuristic's list", "err", err)
		}
		return
	}

	for _, ent := range found {
		if ent.Name == "" {
			continue
		}
		a := byName[ent.Name]
		if a == nil {
			a = &entityAcc{label: ent.Label, sources: map[string]bool{}}
			byName[ent.Name] = a
			// Counted from the reader's own engagement, not from the model's confidence.
			for _, e := range engaged {
				if !containsFold(e.Title, ent.Name) {
					continue
				}
				a.weight += e.Weight
				a.mentions++
				a.sources[e.SourceID] = true
				if e.EngagedAt.After(a.last) {
					a.last = e.EngagedAt
				}
			}
		} else if ent.Label != "" {
			// The heuristic already found it. Keep its evidence and take the model's
			// label, which is the half it is genuinely better at: "Micro Four Thirds"
			// rather than the two overlapping pairs a bigram pass produces.
			a.label = ent.Label
		}
		a.llm = true
	}
}

// containsFold reports whether a title mentions a name, ignoring case.
//
// Case-insensitive because the name is normalised to lowercase and the headline is not.
// Substring rather than token matching, deliberately: a model returns "Micro Four Thirds"
// and the headline may write "micro four-thirds", and a token match would miss it while a
// substring match on the normalised form still lands often enough to be worth having.
func containsFold(title, name string) bool {
	return strings.Contains(strings.ToLower(title), name)
}

// properCase recovers a phrase's original capitalisation from the text it came from.
//
// textvec lowercases, so by the time a phrase exists its casing is gone, and it cannot be
// reconstructed by rule: title-casing gives "Iphone" and "Tcl", which are wrong in a way
// a reader notices immediately on a screen that claims to list the things they follow.
//
// So the source text is searched for the phrase case-insensitively and the original span
// returned. Falls back to the lowercased form, which is honest — better a lowercase label
// than a confidently wrong capitalisation.
func properCase(text, phrase string) string {
	lower := strings.ToLower(text)
	if i := strings.Index(lower, phrase); i >= 0 {
		return text[i : i+len(phrase)]
	}
	// The phrase came from tokenised text, so the words are present but the separator
	// may not be a single space — "state-of-the-art" tokenises across punctuation. Not
	// worth reconstructing; the lowercase form is a fair label.
	return phrase
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
