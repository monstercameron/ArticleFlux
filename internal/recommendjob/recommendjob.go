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
// interface only forwards what it is given.
type RelevanceChecker interface {
	Check(ctx context.Context, topic string, samples []recommend.Sample) (relevant bool, reason string, err error)
}

// Service runs the job.
type Service struct {
	repo  *store.ReaderRepo
	val   Validator
	rel   RelevanceChecker
	topic func(ctx context.Context, sc store.Scope) (string, error)
	log   *slog.Logger
}

// New builds the service. rel and topicOf may both be nil — the relevance
// gate is skipped entirely when either is absent, which keeps rungs 1-3
// working on an instance with Smart+ off.
func New(repo *store.ReaderRepo, val Validator, rel RelevanceChecker, topicOf func(ctx context.Context, sc store.Scope) (string, error), log *slog.Logger) *Service {
	return &Service{repo: repo, val: val, rel: rel, topic: topicOf, log: log}
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
	Candidates   int
	Validated    int
	Recommended  int
	Rejected     int
	// Reviewed counts candidates that went through the "2 posts reviewed"
	// gate — zero on an instance with Smart+ off, or when a candidate simply
	// did not have two sample posts to review.
	Reviewed     int
	FailedReview int
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

	// Resolved once per run: being wired is not consent (see relFor), and the
	// topic describes the reader, not any one site, so it is resolved once
	// too, not re-derived per candidate.
	rel := s.relFor(ctx, sc)
	var topic string
	if rel != nil && s.topic != nil {
		var terr error
		topic, terr = s.topic(ctx, sc)
		if terr != nil {
			return res, fmt.Errorf("recommendjob: topic terms: %w", terr)
		}
	}

	var candidates []recommend.Candidate
	state := map[string]recommend.State{}

	for _, e := range evidence {
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
		if rel != nil && health.HasFeed && len(health.Samples) >= 2 {
			ok, reason, err := rel.Check(ctx, topic, health.Samples)
			if err != nil {
				// A review that could not run is not evidence either way.
				// Scoring without Checked=true falls back to rungs 1-3's
				// deterministic evidence, which is the existing, already-safe
				// behaviour — it does not fail the whole run over one
				// candidate's LLM call.
				if s.log != nil {
					s.log.Warn("recommendjob: relevance check failed",
						"domain", e.Domain, "error", err)
				}
			} else {
				res.Reviewed++
				c.Relevance = recommend.Relevance{Checked: true, OK: ok, Reason: reason}
				if !ok {
					res.FailedReview++
				}
			}
		}

		candidates = append(candidates, c)
	}
	res.Candidates = len(candidates)

	scored := recommend.Score(candidates, state, recommend.Thresholds{}, now)
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
			Domain:   rec.Domain,
			FeedURL:  rec.FeedURL,
			Title:    rec.Title,
			Score:    rec.Score,
			Rung:     int(rec.Rung),
			Evidence: string(ev),
		})
	}
	if err := s.repo.ReplaceRecommendations(ctx, sc, rows); err != nil {
		return res, err
	}

	if s.log != nil {
		s.log.Info("scored recommendations",
			"user", sc.UserID, "candidates", res.Candidates,
			"validated", res.Validated, "recommended", res.Recommended,
			"rejected", res.Rejected, "reviewed", res.Reviewed,
			"failed_review", res.FailedReview)
	}
	return res, nil
}
