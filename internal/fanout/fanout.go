// Package fanout applies each subscriber's rules to newly ingested items
// (TODO 6.7, plan.md §13.2).
//
// # Two decisions, both of which are easy to get backwards
//
// **Per subscriber, never once at ingest.** Rules are per-user and items are
// global (A14). Evaluating once at ingest and storing the outcome on the item
// would let one user's mute filter hide an article from every other subscriber —
// a silent cross-user bug whose symptom is "an article I know exists is missing
// from my reader" and whose cause is in a stranger's rule list.
//
// **Never inline with the poll.** Fan-out is O(items × subscribers). A popular
// source returning 50 items to 200 subscribers is 10,000 rule evaluations
// standing between the fetcher and the next feed, and the queue exists so the
// poll can finish and the fan-out can be paced.
//
// # One job per (source, batch), not one per subscriber
//
// The alternative — enqueue one job per subscriber — sounds more parallel and is
// worse: it multiplies queue rows by the subscriber count, and the per-job
// overhead (claim, transaction, complete) starts to dominate the few
// milliseconds of actual matching. The job loops subscribers internally and
// takes one transaction each, which keeps the write lock held briefly while
// still being restartable at subscriber granularity.
package fanout

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/rules"
	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Payload is what a fanout job carries.
//
// Item IDs rather than the items themselves. A payload holding fifty articles'
// bodies would be megabytes of duplicated content in the jobs table, and the
// items are already in the database by the time this runs — the job is a pointer
// to work, not a copy of it.
type Payload struct {
	SourceID string   `json:"source_id"`
	ItemIDs  []string `json:"item_ids"`
}

// Service runs fan-out.
type Service struct {
	repo *store.ReaderRepo
	log  *slog.Logger
}

// New builds the service.
func New(repo *store.ReaderRepo, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// Enqueue queues fan-out for a batch of newly ingested items.
//
// Called by ingest, in the same transaction that stored the items where
// possible: the queue being in the same database is what makes "these items
// exist and will be fanned out" a single atomic fact rather than two writes with
// a crash window between them.
func (s *Service) Enqueue(ctx context.Context, sourceID string, itemIDs []string) error {
	if len(itemIDs) == 0 {
		return nil
	}
	payload, err := json.Marshal(Payload{SourceID: sourceID, ItemIDs: itemIDs})
	if err != nil {
		return err
	}
	_, err = s.repo.Enqueue(ctx, store.NewJob{
		Kind: store.JobFanout,
		// Above extraction and packs. Fan-out is what makes an item visible at
		// all, and everything else is an enrichment of something already there.
		Priority: 10,
		Payload:  string(payload),
	})
	return err
}

// Handle is the job handler, registered with the pool.
func (s *Service) Handle(ctx context.Context, job store.Job) error {
	var p Payload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return fmt.Errorf("fanout: unreadable payload: %w", err)
	}
	if p.SourceID == "" || len(p.ItemIDs) == 0 {
		return nil
	}

	subs, err := s.repo.SubscribersOf(ctx, p.SourceID)
	if err != nil {
		return fmt.Errorf("fanout: subscribers: %w", err)
	}
	if len(subs) == 0 {
		// A source with no subscribers is normal: unsubscribing never
		// deactivates the source (A22), so a global row can outlive its last
		// reader and still be polled until the poller retires it.
		return nil
	}

	items, err := s.repo.ItemsByID(ctx, p.ItemIDs)
	if err != nil {
		return fmt.Errorf("fanout: items: %w", err)
	}
	if len(items) == 0 {
		return nil
	}

	now := time.Now().UTC()
	var firstErr error
	for _, sub := range subs {
		if err := s.forSubscriber(ctx, sub, items, now); err != nil {
			// One subscriber's failure must not abandon the rest. A corrupt rule
			// set or a missing folder belongs to one account, and returning here
			// would mean every other subscriber's items stay invisible until
			// that account is fixed.
			if firstErr == nil {
				firstErr = err
			}
			if s.log != nil {
				s.log.Warn("fan-out failed for one subscriber",
					"user", sub.UserID, "source", p.SourceID, "err", err)
			}
		}
	}
	return firstErr
}

func (s *Service) forSubscriber(ctx context.Context, sub store.Subscriber,
	items []store.Item, now time.Time) error {

	scope := store.Scope{TenantID: sub.TenantID, UserID: sub.UserID}
	ruleSet, err := s.repo.RulesFor(ctx, scope)
	if err != nil {
		return err
	}

	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}

	// CategoriesFor (§27.8, TODO 10.29): resolved once per subscriber, matching
	// RulesFor above, rather than once per batch — v1 has no per-reader
	// taxonomy override yet (§27.3f, TODO 10.9) so today it returns the same
	// answer for everyone, but the call already takes the Scope a per-reader
	// override will need and there is nowhere else in fan-out that has it.
	cats, err := s.repo.CategoriesFor(ctx, scope, ids)
	if err != nil {
		return err
	}

	ruleItems := make([]rules.Item, len(items))
	for i, it := range items {
		ruleItems[i] = toRuleItem(it, sub, cats[it.ID])
	}

	_, err = s.repo.FanoutItems(ctx, sub, ruleSet, ruleItems, ids, now)
	return err
}

// toRuleItem projects a stored item into the closed field set rules can see.
//
// The content field is plain TEXT, not HTML. A rule matching "contains rust"
// against raw markup would match a link to rust-lang.org in a footer, an image
// filename, or a CSS class — none of which is what the person writing the rule
// meant, and all of which are invisible when they check the article.
func toRuleItem(it store.Item, sub store.Subscriber, cat store.ItemCategory) rules.Item {
	body := it.ContentHTML
	if body == "" {
		body = it.Summary
	}
	published, _ := time.Parse(time.RFC3339Nano, it.PublishedAt)

	return rules.Item{
		Title:       it.Title,
		Author:      it.Author,
		Content:     sanitize.Text(body),
		URL:         it.URL,
		SourceTitle: sub.SourceName,
		FolderName:  sub.FolderName,
		Tags:        sub.Tags,
		WordCount:   it.WordCount,
		PublishedAt: published,
		Lang:        sub.Lang,
		// zero-valued (Unsorted, or never analysed) is indistinguishable from
		// "no rule should match" — the same contract Lang already has above.
		Category: cat.Primary,
		Genre:    cat.Genre,
	}
}
