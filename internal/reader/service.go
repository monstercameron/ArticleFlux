// Package reader is the service layer: the one place that knows what a reading
// operation means.
//
// All three transports route through here — the gRPC tunnel the web client uses,
// the plain-HTTPS offline-pack channel, and the Google Reader-compatible REST API
// that Reeder and NetNewsWire speak (§20.7). If any of them reimplemented "mark
// read", "marked read in Reeder" would drift from the web UI, and that drift is
// invisible until a user notices their unread count is wrong on one device.
package reader

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/urlnorm"
)

// Service is the reading surface.
type Service struct {
	repo    *store.ReaderRepo
	fetcher *feed.Fetcher
}

// New returns a Service.
func New(repo *store.ReaderRepo, f *feed.Fetcher) *Service {
	return &Service{repo: repo, fetcher: f}
}

// ListFeeds returns the sidebar.
func (s *Service) ListFeeds(ctx context.Context, sc store.Scope) ([]store.Feed, int, error) {
	feeds, err := s.repo.ListFeeds(ctx, sc)
	if err != nil {
		return nil, 0, err
	}
	total := 0
	for _, f := range feeds {
		total += f.UnreadCount
	}
	return feeds, total, nil
}

// ListItems returns a page.
func (s *Service) ListItems(ctx context.Context, sc store.Scope, q store.ListQuery) ([]store.Item, string, error) {
	return s.repo.ListItems(ctx, sc, q)
}

// CountItems returns how many items a query matches in all, for the client's
// scrollbar. Same query object as ListItems, so the two cannot describe
// different result sets.
func (s *Service) CountItems(ctx context.Context, sc store.Scope, q store.ListQuery) (int, error) {
	return s.repo.CountQuery(ctx, sc, q)
}

// GetItem returns one item with content.
func (s *Service) GetItem(ctx context.Context, sc store.Scope, id string) (store.Item, error) {
	return s.repo.GetItem(ctx, sc, id)
}

// SetItemState applies a read/star change.
func (s *Service) SetItemState(ctx context.Context, sc store.Scope, itemID string, c store.StateChange) (store.Item, int64, error) {
	rev, err := s.repo.SetItemState(ctx, sc, itemID, c)
	if err != nil {
		return store.Item{}, 0, err
	}
	it, err := s.repo.GetItem(ctx, sc, itemID)
	return it, rev, err
}

// MarkAllRead marks a feed or everything read.
func (s *Service) MarkAllRead(ctx context.Context, sc store.Scope, sourceID, before string) (int, error) {
	return s.repo.MarkAllRead(ctx, sc, sourceID, before)
}

// Search runs a full-text query.
func (s *Service) Search(ctx context.Context, sc store.Scope, query, sourceID string, limit int) ([]store.Item, []string, error) {
	return s.repo.Search(ctx, sc, query, sourceID, limit)
}

// Subscribe adds a feed by URL and immediately polls it.
//
// Polling synchronously is a deliberate latency choice: a user who has just
// added a feed and sees an empty list assumes it did not work. One fetch is a
// second or two, and it is the difference between "added" and "added and here it
// is".
func (s *Service) Subscribe(ctx context.Context, sc store.Scope, rawURL, title string) (store.Feed, bool, error) {
	if rawURL == "" {
		return store.Feed{}, false, errors.New("reader: no url")
	}
	if urlnorm.Host(rawURL) == "" {
		return store.Feed{}, false, fmt.Errorf("reader: %q is not a URL", rawURL)
	}

	key := feed.NaturalKey(rawURL)
	f, existed, err := s.repo.Subscribe(ctx, sc, key, rawURL, "", title)
	if err != nil {
		return store.Feed{}, false, err
	}

	// A source another tenant already polls has items waiting, so only a genuinely
	// new source needs the synchronous fetch.
	if !existed {
		if _, err := s.pollOne(ctx, store.SourceRow{ID: f.SourceID, FeedURL: rawURL}); err != nil {
			// The subscription stands even if the first poll fails: the feed may
			// be briefly down, and unsubscribing the user for that would be
			// worse than an empty list plus a health warning.
			return f, existed, nil
		}
		refreshed, ferr := s.repo.ListFeeds(ctx, sc)
		if ferr == nil {
			for _, rf := range refreshed {
				if rf.SourceID == f.SourceID {
					return rf, existed, nil
				}
			}
		}
	}
	return f, existed, nil
}

// Unsubscribe drops the subscription, never the source (A22).
func (s *Service) Unsubscribe(ctx context.Context, sc store.Scope, sourceID string) error {
	return s.repo.Unsubscribe(ctx, sc, sourceID)
}

// RefreshResult reports a refresh.
type RefreshResult struct {
	Polled   int
	NewItems int
	Errors   []string
}

// MaxConcurrentPolls bounds a refresh.
//
// Six rather than "all of them": a user with 200 feeds would otherwise open 200
// simultaneous connections, which looks like an attack to a shared host and
// exhausts file descriptors on a small box. Six keeps a full refresh brisk while
// staying a well-behaved client.
const MaxConcurrentPolls = 6

// Refresh polls sources now.
func (s *Service) Refresh(ctx context.Context, sc store.Scope, sourceIDs []string) (RefreshResult, error) {
	all, err := s.repo.SubscribedSources(ctx, sc)
	if err != nil {
		return RefreshResult{}, err
	}
	if len(sourceIDs) > 0 {
		want := make(map[string]bool, len(sourceIDs))
		for _, id := range sourceIDs {
			want[id] = true
		}
		filtered := all[:0]
		for _, src := range all {
			if want[src.ID] {
				filtered = append(filtered, src)
			}
		}
		all = filtered
	}

	var (
		mu  sync.Mutex
		res RefreshResult
		wg  sync.WaitGroup
	)
	sem := make(chan struct{}, MaxConcurrentPolls)

	for _, src := range all {
		wg.Add(1)
		go func(src store.SourceRow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			n, err := s.pollOne(ctx, src)
			mu.Lock()
			defer mu.Unlock()
			res.Polled++
			res.NewItems += n
			if err != nil {
				// One dead feed must not fail the whole refresh. The error is
				// reported per source so the UI can flag that feed specifically.
				res.Errors = append(res.Errors, src.FeedURL+": "+err.Error())
			}
		}(src)
	}
	wg.Wait()
	sort.Strings(res.Errors)
	return res, nil
}

// pollOne fetches and ingests a single source, recording the outcome either way.
func (s *Service) pollOne(ctx context.Context, src store.SourceRow) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	parsed, err := s.fetcher.Fetch(ctx, src.FeedURL,
		feed.Conditional{ETag: src.ETag, LastModified: src.LastModified})
	if err != nil {
		// Recording the failure is what drives the backoff and the dormant-feed
		// nudge. A poll that fails silently makes a dead feed look like a quiet
		// one, and those need to look different.
		_ = s.repo.RecordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: err.Error()})
		return 0, err
	}
	if parsed.NotModified {
		_ = s.repo.RecordFetch(ctx, store.FetchOutcome{
			SourceID: src.ID, ETag: parsed.ETag, LastModified: parsed.LastModified,
		})
		return 0, nil
	}

	items := make([]store.IngestItem, 0, len(parsed.Items))
	for _, it := range parsed.Items {
		items = append(items, store.IngestItem{
			GUID: it.GUID, URL: it.URL, DupeKey: it.DupeKey, Title: it.Title,
			Author: it.Author, Summary: it.Summary, ContentHTML: it.ContentHTML,
			PublishedAt: it.PublishedAt, ImageURL: it.ImageURL, WordCount: it.WordCount,
		})
	}
	ing, err := s.repo.IngestItems(ctx, src.ID, items)
	if err != nil {
		_ = s.repo.RecordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: err.Error()})
		return 0, err
	}

	_ = s.repo.RecordFetch(ctx, store.FetchOutcome{
		SourceID: src.ID, ETag: parsed.ETag, LastModified: parsed.LastModified,
		Title: parsed.Title, SiteURL: parsed.SiteURL, IconURL: parsed.IconURL,
	})
	return ing.New, nil
}

// PollDue polls whatever the scheduler says is due. Runs unscoped because
// sources are global (A14) — the poller serves every tenant at once, which is
// the entire economic argument for a shared source table.
func (s *Service) PollDue(ctx context.Context, limit int) (RefreshResult, error) {
	due, err := s.repo.DueSources(ctx, limit)
	if err != nil {
		return RefreshResult{}, err
	}
	var (
		mu  sync.Mutex
		res RefreshResult
		wg  sync.WaitGroup
	)
	sem := make(chan struct{}, MaxConcurrentPolls)
	for _, src := range due {
		wg.Add(1)
		go func(src store.SourceRow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			n, err := s.pollOne(ctx, src)
			mu.Lock()
			defer mu.Unlock()
			res.Polled++
			res.NewItems += n
			if err != nil {
				res.Errors = append(res.Errors, src.FeedURL+": "+err.Error())
			}
		}(src)
	}
	wg.Wait()
	return res, nil
}

// SubscribeOnly attaches a feed without fetching it.
//
// The split from Subscribe exists because bulk import and single-feed add want
// opposite things. Adding one feed by hand should show its articles immediately,
// so Subscribe polls synchronously. Importing 144 feeds that way takes minutes
// and looks like a hang, so import subscribes and lets the poller catch up —
// giving a usable sidebar in under a second.
func (s *Service) SubscribeOnly(ctx context.Context, sc store.Scope, rawURL, title, siteURL string) (store.Feed, bool, error) {
	if rawURL == "" {
		return store.Feed{}, false, errors.New("reader: no url")
	}
	if urlnorm.Host(rawURL) == "" {
		return store.Feed{}, false, fmt.Errorf("reader: %q is not a URL", rawURL)
	}
	return s.repo.Subscribe(ctx, sc, feed.NaturalKey(rawURL), rawURL, siteURL, title)
}

// GetPrefs returns a user's UI preferences.
func (s *Service) GetPrefs(ctx context.Context, sc store.Scope) (map[string]string, error) {
	p, err := s.repo.GetPrefs(ctx, sc)
	return p, err
}

// SetPrefs merges preferences.
func (s *Service) SetPrefs(ctx context.Context, sc store.Scope, p map[string]string) error {
	return s.repo.SetPrefs(ctx, sc, p)
}

// ListTags returns a user's tags and the feed associations.
func (s *Service) ListTags(ctx context.Context, sc store.Scope) ([]store.Tag, map[string][]string, error) {
	tags, err := s.repo.ListTags(ctx, sc)
	if err != nil {
		return nil, nil, err
	}
	bySource, err := s.repo.TagsForFeeds(ctx, sc)
	return tags, bySource, err
}

// SetFeedTag adds or removes a tag on a feed.
func (s *Service) SetFeedTag(ctx context.Context, sc store.Scope, sourceID, name string, on bool) (store.Tag, error) {
	return s.repo.SetFeedTag(ctx, sc, sourceID, name, on)
}

// SourcesForTag returns the feeds carrying a tag.
func (s *Service) SourcesForTag(ctx context.Context, sc store.Scope, tagID string) ([]string, error) {
	return s.repo.SourcesForTag(ctx, sc, tagID)
}

// SetNote writes or clears an item's note.
func (s *Service) SetNote(ctx context.Context, sc store.Scope, itemID, body string) error {
	return s.repo.SetNote(ctx, sc, itemID, body)
}

// GetNote returns an item's note.
func (s *Service) GetNote(ctx context.Context, sc store.Scope, itemID string) (string, error) {
	return s.repo.GetNote(ctx, sc, itemID)
}

// NotedItems returns everything this user has annotated.
func (s *Service) NotedItems(ctx context.Context, sc store.Scope, limit int) ([]store.Item, []string, error) {
	return s.repo.NotedItems(ctx, sc, limit)
}

// GetFeedSettings and UpdateFeedSettings are the per-feed panel. Thin, like the
// rest of this layer: the clamping and the tenant check live in the repository,
// where they cannot be bypassed by a second caller.
func (s *Service) GetFeedSettings(ctx context.Context, sc store.Scope, sourceID string) (store.FeedSettings, error) {
	return s.repo.GetFeedSettings(ctx, sc, sourceID)
}

func (s *Service) UpdateFeedSettings(ctx context.Context, sc store.Scope, sourceID string,
	p store.FeedSettingsPatch) (store.FeedSettings, error) {
	return s.repo.UpdateFeedSettings(ctx, sc, sourceID, p)
}

// RecordEngagements validates and appends observations to the signals log.
//
// Partial success is deliberate. One malformed event must not discard the other
// 199 in a batch: the client cannot repair it, retrying will fail identically,
// and the information in the good events is gone forever if this returns an
// error. Invalid events are counted and dropped, and a non-zero count means the
// client and the server disagree about the taxonomy — a bug to notice in the
// logs, not a condition to retry.
//
// Validation runs on the server on every event because the client is not
// trusted to have got it right. A kind the ranker does not know is collected
// forever and read never, which is worse than being rejected loudly.
func (s *Service) RecordEngagements(ctx context.Context, sc store.Scope,
	evs []signals.Event) (accepted, rejected int, err error) {

	good := make([]signals.Event, 0, len(evs))
	for _, e := range evs {
		if verr := signals.Validate(e); verr != nil {
			rejected++
			continue
		}
		good = append(good, e)
	}
	if len(good) == 0 {
		return 0, rejected, nil
	}

	// The batch cap belongs to storage (one short transaction on the single
	// writer, A24), so oversized batches are split here rather than refused —
	// a phone that was offline for a fortnight has a legitimately large outbox
	// and its signal is exactly the signal worth keeping.
	for len(good) > 0 {
		n := len(good)
		if n > store.MaxEngagementBatch {
			n = store.MaxEngagementBatch
		}
		written, werr := s.repo.RecordEngagements(ctx, sc, good[:n])
		if werr != nil {
			return accepted, rejected, werr
		}
		accepted += written
		good = good[n:]
	}
	return accepted, rejected, nil
}

// ItemSignals is the per-item rollup the ranking and AI layers read.
func (s *Service) ItemSignals(ctx context.Context, sc store.Scope, itemIDs []string) (map[string]store.ItemSignal, error) {
	return s.repo.ItemSignals(ctx, sc, itemIDs)
}

// FeedSignals is the per-source rollup: open rates, dwell, and the deliberate
// acts that §18.4's FeedAffinity term is derived from.
func (s *Service) FeedSignals(ctx context.Context, sc store.Scope, sinceMS int64) ([]store.FeedSignal, error) {
	return s.repo.FeedSignals(ctx, sc, sinceMS)
}

// EngagementCount backs the §18.4 cold-start check. Topics need roughly 50–100
// engaged items to mean anything, and saying "learning your reading" is more
// honest than presenting a confident wrong answer.
func (s *Service) EngagementCount(ctx context.Context, sc store.Scope) (int, error) {
	return s.repo.CountEngagements(ctx, sc)
}
