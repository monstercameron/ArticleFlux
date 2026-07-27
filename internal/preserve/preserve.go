// Package preserve archives article content before the source goes away
// (TODO 6.12, plan.md §10.6).
//
// # The failure this exists for
//
// A feed dies, and every item the reader never opened is now a title, a teaser
// and a dead link. The feed's own `content_html` survives — but for the many
// feeds that publish a truncated excerpt, that is one paragraph and a "Continue
// reading" that goes nowhere.
//
// Earlier revisions of the plan archived bookmarks eagerly and items lazily,
// which is backwards for the failure that actually happens: nobody bookmarks the
// article they were going to read next week.
//
// # Tiered, because both extremes are wrong
//
// Archiving everything is a fetch storm and a storage problem (R7). Archiving
// nothing is the bug. So §10.6's four triggers:
//
//   - **Eagerly at ingest** for high-affinity sources, Top-slot items, and
//     anything whose feed content looks truncated.
//   - **On interaction** — opened, starred, noted, tagged. Cheap and obviously
//     right: the reader has told you it matters.
//   - **On distress**, which is the one worth having. A source erroring is the
//     best early warning that a site is in trouble, and it usually arrives while
//     the article URLs still resolve. That window is the whole opportunity.
//   - **Never** for `archive_policy = never`.
//
// # `archived_text` is near-universal; `archived_html` follows the policy
//
// Text is a fraction of the size, it is what makes search work, and it is what
// you actually need when the origin is gone. The expensive half is the markup.
package preserve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/monstercameron/ArticleFlux/internal/extract"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Reason records why an item was archived, for the §10.6 audit and for eviction
// policy — an archive taken during a distress sweep is more precious than one
// taken speculatively.
type Reason string

const (
	// ReasonIngest is the eager tier: high-affinity source or truncated content.
	ReasonIngest Reason = "ingest"
	// ReasonInteraction is opened, starred, noted or tagged.
	ReasonInteraction Reason = "interaction"
	// ReasonDistress is the sweep triggered when a source starts failing.
	ReasonDistress Reason = "distress"
	// ReasonManual is an explicit request.
	ReasonManual Reason = "manual"
)

// TruncationWords is the word count below which feed content is treated as an
// excerpt rather than the article.
//
// 120 words is about a paragraph and a half. Below that, a feed is almost
// certainly publishing a teaser — and a teaser is exactly the case where losing
// the origin loses the article, so it is the case worth spending a fetch on.
const TruncationWords = 120

// SweepLimit bounds one distress sweep.
//
// A source in distress may have thousands of un-archived items, and fetching all
// of them from a site that is already struggling is the opposite of polite. The
// most recent 50 are the ones the reader plausibly still cares about.
const SweepLimit = 50

// Payload is a preserve job.
type Payload struct {
	ItemIDs []string `json:"item_ids"`
	Reason  Reason   `json:"reason"`
	// SourceID is set for a distress sweep, where the items are chosen here
	// rather than by the caller.
	SourceID string `json:"source_id,omitempty"`
}

// Extractor is the subset of internal/extract this needs.
//
// An interface so the service is testable without a network. Not because a
// second implementation is expected — because the alternative is that every test
// of the archiving POLICY has to stand up an HTTP server, and policy is the part
// worth testing.
type Extractor interface {
	Fetch(ctx context.Context, url string) (*extract.Article, error)
}

// Service archives items.
type Service struct {
	repo *store.ReaderRepo
	ex   Extractor
	log  *slog.Logger
}

// New builds the service.
func New(repo *store.ReaderRepo, ex Extractor, log *slog.Logger) *Service {
	return &Service{repo: repo, ex: ex, log: log}
}

// Enqueue queues archival for specific items.
func (s *Service) Enqueue(ctx context.Context, itemIDs []string, reason Reason) error {
	if len(itemIDs) == 0 {
		return nil
	}
	payload, err := json.Marshal(Payload{ItemIDs: itemIDs, Reason: reason})
	if err != nil {
		return err
	}
	_, err = s.repo.Enqueue(ctx, store.NewJob{
		Kind:    store.JobPreserve,
		Payload: string(payload),
		// Interaction beats the speculative tiers: the reader is looking at it
		// now, and reader mode wants the extraction.
		Priority: priorityFor(reason),
	})
	return err
}

// EnqueueDistressSweep queues the §10.6 sweep for a failing source.
//
// Deduped per source, because a source that is failing will keep failing and
// every poll would otherwise queue another sweep — turning a struggling site
// into a site being swept every fifteen minutes by us.
func (s *Service) EnqueueDistressSweep(ctx context.Context, sourceID string) error {
	payload, err := json.Marshal(Payload{SourceID: sourceID, Reason: ReasonDistress})
	if err != nil {
		return err
	}
	_, err = s.repo.Enqueue(ctx, store.NewJob{
		Kind:      store.JobPreserve,
		Payload:   string(payload),
		Priority:  priorityFor(ReasonDistress),
		DedupeKey: "distress:" + sourceID,
	})
	return err
}

func priorityFor(r Reason) int {
	switch r {
	case ReasonInteraction:
		return 8
	case ReasonDistress:
		// Above the speculative tier and below interaction. The window is
		// closing but nobody is waiting on it.
		return 5
	default:
		return 2
	}
}

// Handle is the job handler.
func (s *Service) Handle(ctx context.Context, job store.Job) error {
	var p Payload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return fmt.Errorf("preserve: unreadable payload: %w", err)
	}

	ids := p.ItemIDs
	if p.SourceID != "" {
		found, err := s.repo.UnarchivedItems(ctx, p.SourceID, SweepLimit)
		if err != nil {
			return fmt.Errorf("preserve: sweep: %w", err)
		}
		ids = found
	}
	if len(ids) == 0 {
		return nil
	}

	items, err := s.repo.ItemsByID(ctx, ids)
	if err != nil {
		return err
	}

	var firstErr error
	for _, it := range items {
		if err := s.archiveOne(ctx, it, p.Reason); err != nil {
			// One article failing must not abandon the rest of a sweep. On a
			// source in distress most of them will fail, and the few that
			// succeed are the entire point of trying.
			if firstErr == nil && !errors.Is(err, errSkip) {
				firstErr = err
			}
			if s.log != nil {
				s.log.Debug("could not archive an item",
					"item", it.ID, "url", it.URL, "err", err)
			}
		}
	}
	return firstErr
}

var errSkip = errors.New("preserve: nothing to do")

func (s *Service) archiveOne(ctx context.Context, it store.Item, reason Reason) error {
	if it.URL == "" {
		return errSkip
	}
	already, err := s.repo.HasArchive(ctx, it.ID)
	if err != nil {
		return err
	}
	if already {
		return errSkip
	}

	art, err := s.ex.Fetch(ctx, it.URL)
	if err != nil {
		// The origin is already unreachable. Recording that is worth more than
		// the failure: it is what marks the archive irreplaceable if one is
		// taken later, and what tells the UI to show a Wayback link.
		if markErr := s.repo.MarkOriginDead(ctx, it.ID); markErr != nil {
			return markErr
		}
		return err
	}

	return s.repo.PutArchive(ctx, store.Archive{
		ItemID: it.ID,
		// HTML follows the policy; text is stored for everything fetched at all,
		// because it is a fraction of the size, it is what makes search work,
		// and it is what you need when the origin is gone.
		HTML:   art.HTML,
		Text:   art.Text,
		Bytes:  int64(len(art.HTML) + len(art.Text)),
		Reason: string(reason),
	})
}

// ShouldArchiveAtIngest decides the eager tier (§10.6).
//
// Pure, and separated from the fetching for the usual reason: this is the part
// with the judgement in it, and it should be testable without a network.
func ShouldArchiveAtIngest(feedAffinity float64, wordCount int, policy string) (bool, Reason) {
	switch policy {
	case "never":
		return false, ""
	case "always":
		return true, ReasonIngest
	}

	// Truncated content from a source the reader engages with. This is the
	// combination that matters: a teaser is where losing the origin loses the
	// article, and affinity is what keeps it from being every feed on the
	// instance.
	if wordCount > 0 && wordCount < TruncationWords && feedAffinity >= 0.3 {
		return true, ReasonIngest
	}
	// High affinity regardless of length: these are the ones the reader will
	// actually miss.
	if feedAffinity >= 0.6 {
		return true, ReasonIngest
	}
	return false, ""
}

// SweepOnDistress decides whether a source's health warrants a sweep.
//
// Three consecutive failures rather than one. A single timeout is noise — a
// phone tethering, a publisher restarting nginx — and sweeping on it would mean
// crawling a site every time its TLS terminator hiccups.
func SweepOnDistress(consecutiveFailures int) bool { return consecutiveFailures >= 3 }
