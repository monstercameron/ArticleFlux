// Package recommendjob turns outlinks and aggregator pass-throughs into scored
// recommendations (TODO 6.10, plan.md §18.7).
//
// Rungs 1–3, no LLM. §18.7's own note is that rungs 1 and 2 "do most of the
// work, cost nothing, and explain themselves" — and the explaining matters more
// here than anywhere else in the application, because the output asks someone to
// add a subscription on the system's say-so.
//
// # The shape
//
//	harvest   outlinks from articles the reader engaged with (rung 1)
//	          plus domains reached through an aggregator and saved (rung 2)
//	validate  fetch each candidate once and find out whether it has a feed
//	score     internal/recommend applies the health gate and the evidence
//	store     replacing the open set, never the dismissals
//
// # Validation happens before scoring, not after
//
// §18.7: "every candidate is fetched and parsed before it is ever shown — a
// recommendation that 404s teaches you not to trust the feature." Validating
// after scoring would be cheaper (only the winners get fetched) and wrong: the
// health gate's inputs ARE the validation, so a candidate scored without it is
// scored on evidence nobody checked.
package recommendjob

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/recommend"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Window is how far back engagement is considered.
const Window = 90 * 24 * time.Hour

// MaxCandidates bounds one run.
//
// Forty. Each surviving candidate costs a validating fetch of somebody else's
// site, and a job that crawls two hundred domains because a reader had a busy
// week is impolite in a way that gets an instance blocked.
const MaxCandidates = 40

// Payload is a recommend job.
type Payload struct {
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
}

// Validator discovers whether a domain publishes a feed, and how often.
//
// An interface so the scoring and gating are testable without crawling. The real
// implementation is a feed-discovery fetch; the point of the seam is that the
// POLICY — what counts as healthy, what evidence is persuasive — is the part
// worth testing, and it should not need a network to exercise.
//
// The returned Health carries up to two sample posts from the same fetch
// (internal/discover.MaxSamples) — see RelevanceChecker for what they are for.
type Validator interface {
	Validate(ctx context.Context, domain string) (recommend.Health, string, string)
}

// RelevanceChecker is the "2 posts reviewed" gate (Cam, 2026-07-31).
//
// Smart+ only, and optional: a Service built without one skips the check
// entirely and scores candidates on rungs 1-3's deterministic evidence alone,
// exactly as before. When configured, every candidate that reaches scoring
// with two sample posts is reviewed first, and a candidate that fails review
// is rejected regardless of how strong its evidence otherwise is — see
// recommend.gate, which checks this before the health rules.
//
// topic is terms only, never the reader's subscription list or reading
// history (§18.8) — the caller is responsible for deriving it that way; this
// interface only forwards what it is given. positive/negative are the same
// taste-calibration titles WebSearchFinder.Find takes (Cam, 2026-08-01) —
// either may be nil.
type RelevanceChecker interface {
	Check(ctx context.Context, topic string, samples []recommend.Sample, positive, negative []string) (relevant bool, reason string, err error)
}

// WebSearchFinder is rung 5's discovery half (Cam, 2026-08-01; plan.md §18.7):
// given the reader's topic terms, find candidate domains from the open web —
// the source rungs 1-2 lack, and the reason Discover has nothing to show a
// sparse or fresh account. Smart+ only, and optional, exactly like
// RelevanceChecker: a Service built without one simply supplies no rung-5
// candidates, and rungs 1-3 are entirely unaffected.
//
// Every domain Find returns is UNTRUSTED model output, never shown or scored
// on its own say-so — Run validates each one through the same Validator every
// other rung uses, and it clears the same health gate and (if configured)
// the same relevance gate before it can ever be recommended.
//
// Find returns domain -> reason, not a []string plus a side-channel: the
// reason is what becomes recommend.Evidence.WebSearchReason, and keeping it
// paired with the domain that earned it is what stops the two from drifting
// apart the way §18.7's evidence sentences never do. A map of primitives
// rather than a shared struct type, same decoupling RelevanceChecker uses
// (plain bool/string/error) — internal/smart's implementation satisfies this
// structurally without importing this package.
//
// positive/negative are the same taste-calibration titles RelevanceChecker
// takes — either may be nil.
type WebSearchFinder interface {
	Find(ctx context.Context, topic string, positive, negative []string) (map[string]string, error)
}

// Service runs the job.
type Service struct {
	repo     *store.ReaderRepo
	val      Validator
	rel      RelevanceChecker
	ws       WebSearchFinder
	topic    func(ctx context.Context, sc store.Scope) (string, error)
	examples func(ctx context.Context, sc store.Scope) (positive, negative []string, err error)
	log      *slog.Logger
}

// New builds the service. rel, ws, topicOf and examplesOf may each be nil —
// the relevance gate and rung-5 web search are both skipped entirely when
// their half is absent, which keeps rungs 1-3 working on an instance with
// Smart+ off. examplesOf may be nil even when rel/ws are configured: both
// Check and Find accept nil examples and simply run on topic/samples alone
// (Cam, 2026-08-01's taste-calibration addition, not a hard requirement of
// either call).
func New(
	repo *store.ReaderRepo, val Validator, rel RelevanceChecker, ws WebSearchFinder,
	topicOf func(ctx context.Context, sc store.Scope) (string, error),
	examplesOf func(ctx context.Context, sc store.Scope) (positive, negative []string, err error),
	log *slog.Logger,
) *Service {
	return &Service{repo: repo, val: val, rel: rel, ws: ws, topic: topicOf, examples: examplesOf, log: log}
}

// SmartPlusPrefKey is the per-user opt-in for the relevance gate — its own
// key, not derive.SmartPlusPrefKey, for the reason derive.go documents at
// length for its own third key: this egresses a DIFFERENT thing (a
// candidate's own posts and the reader's topic terms) and bills the reader
// independently of feed ranking, so consent to one must not be read as
// consent to the other.
const SmartPlusPrefKey = "discover.smartPlus"

// relFor returns the checker this run may use: the wired one when the reader
// has opted in, nil otherwise. Mirrors derive.Service.plusFor exactly,
// including why it is resolved once per run rather than cached on Service
// (shared across every user) or checked per candidate (which could let the
// reader's toggle disagree with itself mid-run).
func (s *Service) relFor(ctx context.Context, sc store.Scope) RelevanceChecker {
	if s.rel == nil {
		return nil
	}
	prefs, err := s.repo.GetPrefs(ctx, sc)
	if err != nil || prefs[SmartPlusPrefKey] != "true" {
		return nil
	}
	return s.rel
}

// wsFor returns the finder this run may use, gated by the SAME
// SmartPlusPrefKey as relFor — not a separate rung-5-specific key. Web
// search is unambiguously a Smart+ feature (it costs real API spend beyond
// the relevance check), but it is still one opt-in decision from the
// reader's point of view — "let Smart+ touch Discover" — not two switches
// they have to find and understand separately.
func (s *Service) wsFor(ctx context.Context, sc store.Scope) WebSearchFinder {
	if s.ws == nil {
		return nil
	}
	prefs, err := s.repo.GetPrefs(ctx, sc)
	if err != nil || prefs[SmartPlusPrefKey] != "true" {
		return nil
	}
	return s.ws
}

// Enqueue schedules a run for one user.
//
// Deduped per user and low priority: recommendations are the least urgent thing
// the instance does, and a reader who never opens /discover should not have
// their poll cycle competing with it.
func (s *Service) Enqueue(ctx context.Context, sc store.Scope) error {
	payload, err := json.Marshal(Payload{TenantID: sc.TenantID, UserID: sc.UserID})
	if err != nil {
		return err
	}
	_, err = s.repo.Enqueue(ctx, store.NewJob{
		Kind:      store.JobRecommend,
		TenantID:  sc.TenantID,
		Payload:   string(payload),
		Priority:  0,
		DedupeKey: sc.UserID,
	})
	return err
}

// Handle is the job handler.
func (s *Service) Handle(ctx context.Context, job store.Job) error {
	var p Payload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return fmt.Errorf("recommendjob: unreadable payload: %w", err)
	}
	sc := store.Scope{TenantID: p.TenantID, UserID: p.UserID}
	if !sc.Valid() {
		return fmt.Errorf("recommendjob: payload names no user")
	}
	_, err := s.Run(ctx, sc, time.Now().UTC())
	return err
}

// Result reports what a run produced.
type Result struct {
	Candidates  int
	Validated   int
	Recommended int
	Rejected    int
	// Reviewed counts candidates that went through the "2 posts reviewed"
	// gate — zero on an instance with Smart+ off, or when a candidate simply
	// did not have two sample posts to review.
	Reviewed     int
	FailedReview int
	// WebSearched counts candidate domains rung 5 (web search) found and
	// validated this run — zero on an instance with Smart+ off, when the
	// reader hasn't opted in, or when rungs 1-2 already found enough that
	// WebSearchMinCandidates never triggered the search at all.
	WebSearched int
}

// WebSearchMinCandidates is the trigger for rung 5. Web search runs only
// when rungs 1-2 harvested fewer candidates than this, not on every run —
// two reasons: it is a real, billable API call (a search-and-synthesize
// task, pricier than the two-post relevance read), and it exists as a
// fallback SOURCE for a sparse or fresh account, not a replacement for the
// reader's own reading signal once that signal exists. Five is deliberately
// generous — Discover showing three thin outlink candidates plus a couple of
// web-search ones reads better than either extreme (an empty panel, or a
// panel where deterministic and searched candidates compete 1:1 every time).
const WebSearchMinCandidates = 5

// applyRelevanceGate runs the "2 posts reviewed" check against c and records
// the outcome on both c and res. Shared between the rung-1/2 harvest loop and
// the rung-5 web-search loop in Run — one place decides what "reviewed" means
// so the two sources cannot silently drift apart.
func (s *Service) applyRelevanceGate(
	ctx context.Context, rel RelevanceChecker, topic, domain string,
	positive, negative []string,
	health recommend.Health, c *recommend.Candidate, res *Result,
) {
	if rel == nil || !health.HasFeed || len(health.Samples) < 2 {
		return
	}
	ok, reason, err := rel.Check(ctx, topic, health.Samples, positive, negative)
	if err != nil {
		// A review that could not run is not evidence either way. Scoring
		// without Checked=true falls back to whatever deterministic evidence
		// the candidate already has, which is the existing, already-safe
		// behaviour — it does not fail the whole run over one candidate's
		// LLM call.
		if s.log != nil {
			s.log.WarnContext(ctx, "recommendjob: relevance check failed", "domain", domain, "error", err)
		}
		return
	}
	res.Reviewed++
	c.Relevance = recommend.Relevance{Checked: true, OK: ok, Reason: reason}
	if !ok {
		res.FailedReview++
		// The reason a candidate was turned down is logged, because without it
		// a run that reviews nineteen sites and fails all nineteen is
		// indistinguishable from a run that found nothing — and the two have
		// nothing in common. The reader sees the same empty page either way,
		// and the operator (who is the same person here) had no way to tell
		// whether the gate was working or misfiring.
		if s.log != nil {
			s.log.Info("recommendjob: candidate failed review",
				"domain", domain, "topic", topic, "reason", reason)
		}
	}
}

// Run harvests, validates, scores and stores.
func (s *Service) Run(ctx context.Context, sc store.Scope, now time.Time) (Result, error) {
	var res Result

	evidence, err := s.repo.OutlinkCandidates(ctx, sc, now.Add(-Window).UnixMilli(), MaxCandidates*3)
	if err != nil {
		return res, fmt.Errorf("recommendjob: harvest: %w", err)
	}

	dismissed, err := s.repo.DismissedDomains(ctx, sc)
	if err != nil {
		return res, err
	}
	domains, err := s.repo.DomainAffinities(ctx, sc)
	if err != nil {
		return res, err
	}
	// "Steer by rejection" (Cam, 2026-08-01) — see recommend.Thresholds.TopicDismissals
	// for the curve. Fetched once per run for the same reason dismissed/domains are.
	topicDismissals, err := s.repo.DismissedTopics(ctx, sc)
	if err != nil {
		return res, err
	}

	// Resolved once per run: being wired is not consent (see relFor), and the
	// topic describes the reader, not any one site, so it is resolved once
	// too, not re-derived per candidate. Needed by EITHER Smart+ half now,
	// not just the relevance checker.
	rel := s.relFor(ctx, sc)
	ws := s.wsFor(ctx, sc)
	var topic string
	var positiveExamples, negativeExamples []string
	if (rel != nil || ws != nil) && s.topic != nil {
		var terr error
		topic, terr = s.topic(ctx, sc)
		if terr != nil {
			return res, fmt.Errorf("recommendjob: topic terms: %w", terr)
		}
		if s.examples != nil {
			var eerr error
			positiveExamples, negativeExamples, eerr = s.examples(ctx, sc)
			if eerr != nil {
				// Taste examples are calibration, not a requirement — unlike
				// a failed topic resolution (which leaves Check/Find nothing
				// to work from at all), a failed example lookup still leaves
				// a perfectly usable topic-only request. Logged, not fatal.
				if s.log != nil {
					s.log.WarnContext(ctx, "recommendjob: taste examples failed", "error", eerr)
				}
			}
		}
	}

	var candidates []recommend.Candidate
	state := map[string]recommend.State{}
	// knownDomains is every domain the harvest loop below has already seen,
	// dismissed/subscribed/appended alike — rung 5 checks this before
	// spending a validating fetch on a domain rungs 1-2 already settled.
	knownDomains := map[string]bool{}

	for _, e := range evidence {
		knownDomains[e.Domain] = true
		if len(candidates) >= MaxCandidates {
			break
		}
		// Filtered BEFORE the validating fetch, not after. §18.7's guardrails are
		// also a politeness budget: fetching a site to score a recommendation the
		// reader already refused is a request nobody wanted made.
		if dismissed[e.Domain] {
			state[e.Domain] = recommend.State{Dismissed: true}
			continue
		}
		if e.Subscribed {
			state[e.Domain] = recommend.State{Subscribed: true}
			continue
		}

		health, feedURL, title := s.val.Validate(ctx, e.Domain)
		res.Validated++

		c := recommend.Candidate{
			Domain:  e.Domain,
			FeedURL: feedURL,
			Title:   title,
			Rung:    recommend.RungOutlinks,
			Health:  health,
			Evidence: recommend.Evidence{
				LinkCount:        e.LinkCount,
				DistinctSources:  e.DistinctSources,
				SourceTitles:     e.SourceTitles,
				EngagementWeight: e.EngagementWeight,
			},
		}

		// Rung 2: a domain the reader reached through an aggregator and saved.
		// §18.7 calls this "excellent and immediately actionable", and it is —
		// they have already engaged with the site repeatedly, just not directly.
		if aff, ok := domains[e.Domain]; ok && aff.Stars > 0 {
			c.Rung = recommend.RungAggregator
			c.Evidence.StarredViaAggregator = aff.Stars
		}

		// The "2 posts reviewed" gate. Applied here, before the candidate is
		// even handed to Score, so a candidate that fails review is rejected
		// by the ordinary gate() path — one place decides what gets shown,
		// not two.
		s.applyRelevanceGate(ctx, rel, topic, e.Domain, positiveExamples, negativeExamples, health, &c, &res)

		candidates = append(candidates, c)
	}

	// Rung 5 (Cam, 2026-08-01): a fallback SOURCE, not a replacement — only
	// runs when rungs 1-2 came up short. See WebSearchMinCandidates for why.
	//
	// "Came up short" means SURVIVORS, not finds, and that distinction is the
	// whole bug this measured on the development instance:
	//
	//	candidates=40 validated=40 recommended=0 rejected=40
	//	reviewed=15 failed_review=15 web_searched=0
	//
	// Forty candidates found, forty rejected, nothing to show — and the web
	// search skipped, because forty is comfortably more than five. The gate
	// was reading the size of the pile before anything had been thrown out of
	// it, so the account most in need of a new source was the one guaranteed
	// never to get one. A reader in that state sees "No suggestions yet" run
	// after run and concludes the feature does not work.
	//
	// Scoring rungs 1-2 first costs nothing — Score is pure, and the candidates
	// are already validated and gated by this point — and it answers the
	// question the gate was always asking: is there anything HERE to show?
	survivors := recommend.Score(candidates, state,
		recommend.Thresholds{TopicDismissals: topicDismissals}, now)
	if ws != nil && topic != "" && len(survivors.Recommendations) < WebSearchMinCandidates {
		found, err := ws.Find(ctx, topic, positiveExamples, negativeExamples)
		if err != nil {
			// Same discipline as a failed relevance check: a search that could
			// not run is not evidence the reader has nothing to see, it is an
			// absent source — the run still completes on whatever rungs 1-2
			// found, empty or not.
			if s.log != nil {
				s.log.WarnContext(ctx, "recommendjob: web search failed", "error", err)
			}
		}
		// Bounded by how many are NEEDED, not by how full the slice already is.
		// `len(candidates) >= MaxCandidates` was the same mistake as the trigger
		// above one level down: with forty about-to-be-rejected candidates in
		// hand, the cap was already met and rung 5 would have added nothing even
		// once it was allowed to run. At most five new domains get fetched here,
		// which is a politeness cost worth paying for a reader who would
		// otherwise see nothing at all.
		want := WebSearchMinCandidates - len(survivors.Recommendations)
		added := 0
		for domain, reason := range found {
			if added >= want {
				break
			}
			// Every domain here is UNTRUSTED model output (llm.WebSearchPayload's
			// own doc) — dismissed/subscribed/already-harvested filtering
			// applies exactly as it does to a rung-1/2 candidate, and BEFORE
			// the validating fetch for the same politeness-budget reason.
			if dismissed[domain] || knownDomains[domain] {
				continue
			}
			knownDomains[domain] = true

			health, feedURL, title := s.val.Validate(ctx, domain)
			res.Validated++
			res.WebSearched++

			c := recommend.Candidate{
				Domain: domain, FeedURL: feedURL, Title: title,
				Rung:   recommend.RungLLM,
				Health: health,
				// The search's own reason for this domain IS the evidence —
				// threaded straight from the reply, not re-derived, for the
				// same reason §18.7's evidence sentences never are: the claim
				// and its justification must not be able to drift apart.
				Evidence: recommend.Evidence{WebSearchReason: reason},
			}
			s.applyRelevanceGate(ctx, rel, topic, domain, positiveExamples, negativeExamples, health, &c, &res)
			candidates = append(candidates, c)
			added++
		}
	}
	res.Candidates = len(candidates)

	scored := recommend.Score(candidates, state,
		recommend.Thresholds{TopicDismissals: topicDismissals}, now)
	res.Recommended = len(scored.Recommendations)
	res.Rejected = len(scored.Rejected)

	rows := make([]store.StoredRecommendation, 0, len(scored.Recommendations))
	for _, rec := range scored.Recommendations {
		// The evidence is stored as JSON but is a rendered SENTENCE rather than
		// a structure. §18.7 shows it verbatim, and keeping the prose next to the
		// score means the reason cannot drift from the number that produced it —
		// which is what happens when a UI re-derives the wording from fields.
		ev, err := json.Marshal([]string{rec.Evidence})
		if err != nil {
			return res, err
		}
		rows = append(rows, store.StoredRecommendation{
			Domain:     rec.Domain,
			FeedURL:    rec.FeedURL,
			Title:      rec.Title,
			Score:      rec.Score,
			Rung:       int(rec.Rung),
			Evidence:   string(ev),
			TopicLabel: rec.TopicLabel,
		})
	}
	if err := s.repo.ReplaceRecommendations(ctx, sc, rows); err != nil {
		return res, err
	}

	if s.log != nil {
		// web_search says which of three things happened, because "candidates=0"
		// on its own cannot distinguish them and the difference is the whole
		// question a reader asks when Discover is empty: was the web searched at
		// all, was it skipped because the opt-in is off or the taste profile is
		// empty, or did it run and find nothing? Without this the only honest
		// answer available from the log was "I don't know".
		s.log.Info("scored recommendations",
			"user", sc.UserID, "candidates", res.Candidates,
			"validated", res.Validated, "recommended", res.Recommended,
			"rejected", res.Rejected, "reviewed", res.Reviewed,
			"failed_review", res.FailedReview,
			"web_search", webSearchStatus(ws, topic, res),
			"web_searched", res.WebSearched,
			"dismissed_domains", len(dismissed))
	}
	return res, nil
}

// webSearchStatus names why rung 5 did or did not run, for the summary log.
//
// The three "did not" cases have completely different remedies — turn Smart+
// review on, read more so there is a topic to search for, or nothing at all
// because rungs 1-2 already had enough — and an empty Discover looks identical
// in all of them.
func webSearchStatus(ws WebSearchFinder, topic string, res Result) string {
	switch {
	case ws == nil:
		return "skipped: smart+ review is off"
	case topic == "":
		return "skipped: no topic terms yet"
	case res.WebSearched > 0:
		return "ran"
	default:
		return "ran or unneeded: no new domains"
	}
}
