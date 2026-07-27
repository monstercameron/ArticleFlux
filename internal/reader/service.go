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

	"github.com/monstercameron/ArticleFlux/internal/discover"
	"github.com/monstercameron/ArticleFlux/internal/extract"
	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/urlnorm"
)

// Service is the reading surface.
type Service struct {
	repo    *store.ReaderRepo
	fetcher *feed.Fetcher
	// The optional half, wired by WithSiteAnalysis: page discovery, full-text
	// extraction and the Smart+ analyser. Every one of them is nil on an
	// instance that has not opted in, and every path that uses them says so.
	pages    *discover.Fetcher
	extract  *extract.Extractor
	analyzer *smart.SiteAnalyzer
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

// ListRanked returns a page of the materialised homepage — My Feed.
//
// `after` is the rank to resume from; zero starts at the top. Not a keyset cursor over
// (published_at, id) like ListItems, because the ordering here is a small dense
// integer the deriver assigned, and an opaque cursor over it would be ceremony around
// "the number after this one".
//
// Returns the ranking rows alongside the items rather than merging them into Item,
// because the two have different lifetimes: the item is a fact about the world and the
// ranking is this reader's current opinion of it. The transport joins them for the
// wire; the service keeps them distinguishable so a caller that only wants the items
// is not obliged to care.
func (s *Service) ListRanked(ctx context.Context, sc store.Scope, after, limit int) (
	[]store.RankedItem, []store.Item, error) {
	return s.repo.RankedItems(ctx, sc, after, limit)
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

// MarkAllRead marks a feed or everything read, and returns the batch stamp that
// undoes it. The stamp travels to the client so the undo needs no server-side
// session state — a reader who reloads before pressing it simply loses the offer,
// which is the right trade for not keeping per-user scratch state on disk.
func (s *Service) MarkAllRead(ctx context.Context, sc store.Scope, sourceID, before string) (int, string, error) {
	return s.repo.MarkAllRead(ctx, sc, sourceID, before)
}

// UndoMarkAllRead reverses one bulk mark.
func (s *Service) UndoMarkAllRead(ctx context.Context, sc store.Scope, batch string) (int, error) {
	return s.repo.UndoMarkAllRead(ctx, sc, batch)
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
func (s *Service) Subscribe(ctx context.Context, sc store.Scope, rawURL, title, folderID string) (store.Feed, bool, error) {
	if rawURL == "" {
		return store.Feed{}, false, errors.New("reader: no url")
	}
	if urlnorm.Host(rawURL) == "" {
		return store.Feed{}, false, fmt.Errorf("reader: %q is not a URL", rawURL)
	}

	f, existed, err := s.repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: feed.NaturalKey(rawURL), FeedURL: rawURL, Title: title, FolderID: folderID,
	})
	if err != nil {
		return store.Feed{}, false, err
	}

	// A source another tenant already polls has items waiting, so only a genuinely
	// new source needs the synchronous fetch.
	//
	// "Already polls" means SUCCESSFULLY. A source with no last_success_at is one
	// nobody has ever got anything out of — including one this very check
	// rejected a minute ago, because A22 keeps the row after the subscription is
	// rolled back. Skipping the poll for those would mean the second attempt at
	// an HTML page quietly succeeded where the first was refused, which is
	// exactly the loophole the refusal exists to close.
	if !existed || f.LastSuccess == "" {
		if _, err := s.pollOne(ctx, store.SourceRow{ID: f.SourceID, FeedURL: rawURL}); err != nil {
			// A refused ADDRESS is not a failed fetch, and the two cannot be
			// treated the same. A feed that is briefly down deserves the
			// subscription it just got — it will work tomorrow, and unsubscribing
			// someone over one timeout is worse than an empty list plus a health
			// warning. An address the guard will never dial (§21: link-local, the
			// cloud metadata endpoint) has no tomorrow: keeping it would leave a
			// permanent row in the sidebar, named after a URL, that can never
			// hold an article — and the dialog would have reported success.
			//
			// So this one is rolled back and reported. The reader sees why in the
			// field they typed it into, which is the only place the mistake can
			// be fixed.
			if errors.Is(err, netguard.ErrBlockedIP) || errors.Is(err, netguard.ErrScheme) {
				s.rollback(ctx, sc, f.SourceID)
				return store.Feed{}, false, err
			}
			// Not a feed at all — an HTML page, usually, which is what most
			// people paste. Same reasoning as a refused address and a different
			// remedy: the subscription is rolled back and the error is returned,
			// because the caller's next move is the subscribe ladder (§11), and
			// a ladder that runs while a junk source sits in the sidebar has
			// already lost. A page that becomes a feed tomorrow is not a thing
			// that happens; a page that HAS a feed is, and finding it is the
			// point.
			if errors.Is(err, feed.ErrNotAFeed) {
				s.rollback(ctx, sc, f.SourceID)
				return store.Feed{}, false, err
			}
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

// rollback undoes a subscription whose very first poll proved the address
// unusable, and retires the source it created.
//
// Both halves matter. Dropping only the subscription leaves a source nobody is
// subscribed to and the scheduler still polls — DueSources works over `sources`,
// not over subscriptions — so a mistyped address would be fetched, and fail,
// forever. Retiring only the source would leave the reader looking at a sidebar
// row for something that will never load.
//
// Errors are swallowed deliberately: this is the cleanup after a failure that
// has already been decided, and a failure to clean up must not replace the
// message that says what actually went wrong.
func (s *Service) rollback(ctx context.Context, sc store.Scope, sourceID string) {
	_ = s.repo.Unsubscribe(ctx, sc, sourceID)
	_ = s.repo.RetireUnusableSource(ctx, sourceID)
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

	// A scraped source's feed_url is an index PAGE. Handing it to the feed
	// parser produces "not a recognisable feed" on every poll forever, which is
	// how a feature like this silently never works.
	if src.Kind == "scrape" {
		return s.pollScrape(ctx, src)
	}

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
func (s *Service) SubscribeOnly(ctx context.Context, sc store.Scope, rawURL, title, siteURL, folderID string) (store.Feed, bool, error) {
	if rawURL == "" {
		return store.Feed{}, false, errors.New("reader: no url")
	}
	if urlnorm.Host(rawURL) == "" {
		return store.Feed{}, false, fmt.Errorf("reader: %q is not a URL", rawURL)
	}
	return s.repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: feed.NaturalKey(rawURL), FeedURL: rawURL, SiteURL: siteURL,
		Title: title, FolderID: folderID,
	})
}

// The category surface. Thin, like the rest of this layer: the naming rules, the
// cap and the ownership checks live in the repository, where a second caller
// cannot skip them.
func (s *Service) ListFolders(ctx context.Context, sc store.Scope) ([]store.Folder, error) {
	return s.repo.ListFolders(ctx, sc)
}

func (s *Service) CreateFolder(ctx context.Context, sc store.Scope, name string) (store.Folder, error) {
	return s.repo.CreateFolder(ctx, sc, name)
}

func (s *Service) RenameFolder(ctx context.Context, sc store.Scope, folderID, name string) (store.Folder, error) {
	return s.repo.RenameFolder(ctx, sc, folderID, name)
}

func (s *Service) DeleteFolder(ctx context.Context, sc store.Scope, folderID string) error {
	return s.repo.DeleteFolder(ctx, sc, folderID)
}

func (s *Service) SetFeedFolder(ctx context.Context, sc store.Scope, sourceID, folderID string) error {
	return s.repo.SetFeedFolder(ctx, sc, sourceID, folderID)
}

// FolderByName resolves a category name to its id, creating it if this user has
// no such category yet.
//
// It exists for OPML import, which carries folder NAMES rather than ids: an
// export from another reader is the one place a category arrives as text. Every
// other caller has an id, because every other caller got it from ListFolders.
func (s *Service) FolderByName(ctx context.Context, sc store.Scope, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	f, err := s.repo.CreateFolder(ctx, sc, name)
	if err != nil {
		return "", err
	}
	return f.ID, nil
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

// UpdateTag changes a tag's rail label and glyph. It cannot change the tag's
// name — see store.UpdateTag.
func (s *Service) UpdateTag(ctx context.Context, sc store.Scope, tagID string, p store.TagPatch) (store.Tag, error) {
	return s.repo.UpdateTag(ctx, sc, tagID, p)
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
