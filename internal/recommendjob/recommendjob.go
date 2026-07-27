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
type Validator interface {
	Validate(ctx context.Context, domain string) (recommend.Health, string, string)
}

// Service runs the job.
type Service struct {
	repo *store.ReaderRepo
	val  Validator
	log  *slog.Logger
}

// New builds the service.
func New(repo *store.ReaderRepo, val Validator, log *slog.Logger) *Service {
	return &Service{repo: repo, val: val, log: log}
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
			"rejected", res.Rejected)
	}
	return res, nil
}
