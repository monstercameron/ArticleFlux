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
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/discover"
	"github.com/monstercameron/ArticleFlux/internal/extract"
	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
	"github.com/monstercameron/ArticleFlux/internal/outlinks"
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
	// onSignals is called after engagements land, so the interest layer can be
	// rebuilt while the reader is still in the session that produced the signal.
	// Nil on an instance with no job pool, which is every test that does not want
	// one.
	onSignals func(store.Scope)
	// onRankPrefs is called when a preference that changes HOW the ranking is built
	// is written — today only the Smart+ opt-in. Separate from onSignals because the
	// two want opposite scheduling: signals arrive on their own and want rate
	// limiting, a toggle is deliberate and wants the work to start now (App.ForceDerive
	// versus App.NudgeDerive). Nil wherever onSignals is nil, and for the same reason.
	onRankPrefs func(store.Scope)
	// onIngest is called after a poll writes new items, so §20.3's live update
	// can announce them. Nil on an instance with no event bus — which is every
	// test, and every deployment before this was wired.
	//
	// A func rather than the bus itself, for the same reason onSignals is one:
	// this package is the service layer the sync surface and the offline-pack
	// channel also go through, and a dependency on a delivery mechanism here is
	// how one entry point starts behaving differently from another.
	onIngest func(sourceID string, itemIDs []string)
	// log is where PollDue and Refresh report a panic recovered from a
	// per-source poll (see pollOneRecovered and WithLogger). Nil on an instance
	// nobody has wired one into; logger() falls back to slog.Default() rather
	// than dropping the line, because a recover with nowhere to report is a
	// crash that becomes a feed that silently never updates.
	log *slog.Logger
}

// New returns a Service.
func New(repo *store.ReaderRepo, f *feed.Fetcher) *Service {
	return &Service{repo: repo, fetcher: f}
}

// WithSignalHook installs the callback that closes the interest loop.
//
// # Why the loop has to close faster than the poller
//
// The deriver was scheduled only from the background poll, at fifteen-minute
// intervals. Everything worked and the feature was still wrong from the reader's
// side: like three articles, go to My Feed, and it looks exactly as it did before —
// for up to a quarter of an hour, with nothing on screen to say why. A ranking that
// cannot be seen responding is one nobody believes is responding, and the natural
// next move is to stop giving it signals.
//
// The hook is a func rather than a *derive.Service because this package must not
// import the deriver: `reader` is the service layer that the REST sync surface and
// the offline-pack channel also go through, and a dependency on a background job
// here is how "marked read in Reeder" starts behaving differently from the web UI.
// The app wires the two together; neither knows the other's type.
//
// The callback is expected to be cheap and non-blocking — it enqueues, it does not
// derive. See App.NudgeDerive for the rate limit that makes that safe.
func (s *Service) WithSignalHook(f func(store.Scope)) *Service {
	s.onSignals = f
	return s
}

// WithRankPrefHook installs the callback fired when a ranking preference is written.
//
// Separate from WithSignalHook, and wired to a different method on the App, because the
// two arrive differently: signals come in a stream nobody asked for and want throttling,
// while a preference change is one deliberate act with somebody watching for the result.
// One hook serving both would have to choose, and either choice is wrong for one caller.
func (s *Service) WithRankPrefHook(f func(store.Scope)) *Service {
	s.onRankPrefs = f
	return s
}

// WithIngestHook installs the callback fired when a poll produces new items.
//
// Optional in the same way, and for the same reason: an instance with nobody
// watching for live updates should not pay for them, and every test that does
// not wire one gets a working service.
//
// Called with the ids of the items ingest CREATED, never the ones it updated. A
// publisher fixing a typo is not news, and treating it as news is how a live
// list starts reshuffling under somebody who is reading it.
func (s *Service) WithIngestHook(f func(sourceID string, itemIDs []string)) *Service {
	s.onIngest = f
	return s
}

// WithLogger installs the logger a panic recovered from a per-source poll is
// reported through (see pollOneRecovered). Optional, the same way
// WithSignalHook is: every caller that does not wire one gets a working
// default rather than a nil logger every call site has to guard against.
//
// The wiring is the same *slog.Logger the app hands internal/jobs.Options.Log
// — reqid-wrapped and ring-backed, so a recovered panic here lands in the same
// request-id-tagged log as everything else, and is readable from the settings
// screen, instead of narrating on its own. App.Open does this at the point it
// builds the service.
func (s *Service) WithLogger(l *slog.Logger) *Service {
	s.log = l
	return s
}

// logger returns the installed logger, or slog.Default() so a recovered panic
// is never reported into silence just because nobody called WithLogger.
func (s *Service) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}

// recordFetch writes a fetch outcome and logs if that write fails.
//
// # Why a helper for one line
//
// There are about twenty of these, and every one discarded the error into `_`.
// Discarding it is right — a poll that fetched an article successfully
// must not be reported as a failure because the bookkeeping row would not
// write, and there is nothing useful to do about it in the middle of a poll.
//
// Discarding it SILENTLY is not. RecordFetch is what puts "last checked" and
// "last error" in front of the reader, so if it starts failing — a locked
// database, a full disk — every feed in the UI freezes at its old timestamp
// while polling carries on working perfectly. The reader sees a reader that
// stopped updating; the log says nothing at all; and the one place that would
// explain it is the write that is failing.
//
// Warn rather than Error: the fetch itself succeeded and the reader's articles
// are stored. This is the instrument breaking, not the machine.
//
// The panic path in pollOneRecovered already logged exactly this, which is how
// the gap was found — one path treated the failure as worth reporting and the
// other nineteen dropped it.
func (s *Service) recordFetch(ctx context.Context, out store.FetchOutcome) {
	if err := s.repo.RecordFetch(ctx, out); err != nil {
		s.logger().WarnContext(ctx, "could not record a fetch outcome; the feed's "+
			"last-checked time and error will be stale in the UI",
			"source_id", out.SourceID, "err", err)
	}
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

// CountRanked returns how many items are on My Feed, for the rail's badge.
func (s *Service) CountRanked(ctx context.Context, sc store.Scope) (int, error) {
	return s.repo.CountRanked(ctx, sc)
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

// CategoriesFor resolves each item's category from its stored analysis
// scores. Thin, like the rest of this layer: the floor, the model-wins rule
// and the derive-on-read reasoning all live in store.CategoriesFor, where a
// second caller cannot bypass them by querying item_analysis directly.
func (s *Service) CategoriesFor(ctx context.Context, sc store.Scope, itemIDs []string) (map[string]store.ItemCategory, error) {
	return s.repo.CategoriesFor(ctx, sc, itemIDs)
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

// MarkAllRead marks everything the query selects read, and returns the batch
// stamp that undoes it. The stamp travels to the client so the undo needs no
// server-side session state — a reader who reloads before pressing it simply
// loses the offer, which is the right trade for not keeping per-user scratch
// state on disk.
//
// The query is the LIST the reader was looking at, not just a feed — see
// store.MarkQuery for what taking only a source id used to cost.
func (s *Service) MarkAllRead(ctx context.Context, sc store.Scope, q store.MarkQuery, before string) (int, string, error) {
	return s.repo.MarkAllRead(ctx, sc, q, before)
}

// UnreadByCategory counts unread articles per classification label, for the
// rail's counts.
func (s *Service) UnreadByCategory(ctx context.Context, sc store.Scope, slugs []string) (map[string]int, error) {
	return s.repo.UnreadByCategory(ctx, sc, slugs)
}

// UnreadUncategorised counts unread articles carrying no classification label.
func (s *Service) UnreadUncategorised(ctx context.Context, sc store.Scope) (int, error) {
	return s.repo.UnreadUncategorised(ctx, sc)
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
// Description is the feed's own channel description, as of the poll this call
// just ran — "" when the poll was skipped (an already-polling source) or when
// the feed simply declares none. It exists on this return only for the
// Smart+ categorizer (internal/smart/categorize.go), which wants the same
// words a reader would judge the feed by; nothing else in this package reads
// it, and it is not persisted (store.Feed carries no description column).
func (s *Service) Subscribe(ctx context.Context, sc store.Scope, rawURL, title, folderID string) (
	feedOut store.Feed, existed bool, description string, err error) {

	if rawURL == "" {
		return store.Feed{}, false, "", errors.New("reader: no url")
	}
	if urlnorm.Host(rawURL) == "" {
		return store.Feed{}, false, "", fmt.Errorf("reader: %q is not a URL", rawURL)
	}

	f, existed, err := s.repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: feed.NaturalKey(rawURL), FeedURL: rawURL, Title: title, FolderID: folderID,
	})
	if err != nil {
		return store.Feed{}, false, "", err
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
		_, desc, err := s.pollOneWithParsed(ctx, store.SourceRow{ID: f.SourceID, FeedURL: rawURL})
		if err != nil {
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
				return store.Feed{}, false, "", err
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
				return store.Feed{}, false, "", err
			}
			return f, existed, "", nil
		}
		refreshed, ferr := s.repo.ListFeeds(ctx, sc)
		if ferr == nil {
			for _, rf := range refreshed {
				if rf.SourceID == f.SourceID {
					return rf, existed, desc, nil
				}
			}
		}
		return f, existed, desc, nil
	}
	return f, existed, "", nil
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

			n, err := s.pollOneRecovered(ctx, src, s.pollOne)
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
	n, _, err := s.pollOneWithParsed(ctx, src)
	return n, err
}

// pollOneWithParsed is pollOne's body, plus the feed's own description on a
// successful poll. Split out rather than widening pollOne itself, because
// pollOne's (int, error) shape is also the `work func` signature
// pollOneRecovered, PollDue and Refresh share (see pollOneRecovered's own
// comment on why that seam exists) — every one of those is background work
// nothing reads a description from, and widening it there would mean three
// call sites and their tests carrying a value only Subscribe wants.
func (s *Service) pollOneWithParsed(ctx context.Context, src store.SourceRow) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	// A scraped source's feed_url is an index PAGE. Handing it to the feed
	// parser produces "not a recognisable feed" on every poll forever, which is
	// how a feature like this silently never works.
	if src.Kind == "scrape" {
		n, err := s.pollScrape(ctx, src)
		return n, "", err
	}

	parsed, err := s.fetcher.Fetch(ctx, src.FeedURL,
		feed.Conditional{ETag: src.ETag, LastModified: src.LastModified})
	if err != nil {
		// Recording the failure is what drives the backoff and the dormant-feed
		// nudge. A poll that fails silently makes a dead feed look like a quiet
		// one, and those need to look different.
		s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: err.Error()})
		return 0, "", err
	}
	if parsed.NotModified {
		s.recordFetch(ctx, store.FetchOutcome{
			SourceID: src.ID, ETag: parsed.ETag, LastModified: parsed.LastModified,
		})
		return 0, "", nil
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
		s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: err.Error()})
		return 0, "", err
	}

	// Outlink mining (§18.7 rung 1) runs on every freshly-ingested item, off the
	// feed's OWN content — not a second fetch of the article page. §18.7's rung
	// 1 needs SOMETHING to mine from the moment an article exists, and the
	// opportunistic archive tier (internal/preserve) never covers every item —
	// it is policy-gated and asynchronous. The feed's content_html is what
	// every item already has, right now, for free.
	//
	// Best-effort and non-fatal: one poll's worth of outlink mining failing
	// must not turn into a feed that stops updating, the same discipline
	// RecordFetch's error handling around it already follows.
	s.mineOutlinks(ctx, src.ID, ing.NewIDs)

	s.recordFetch(ctx, store.FetchOutcome{
		SourceID: src.ID, ETag: parsed.ETag, LastModified: parsed.LastModified,
		Title: parsed.Title, SiteURL: parsed.SiteURL, IconURL: parsed.IconURL,
	})

	// Announced AFTER the fetch outcome is recorded, so the poll is fully
	// consistent before anybody is told to come and look at it. A client that
	// reacted to the event and read a source row still mid-update would see a
	// feed with new items and a stale last-polled time.
	if len(ing.NewIDs) > 0 && s.onIngest != nil {
		s.onIngest(src.ID, ing.NewIDs)
	}
	return ing.New, parsed.Description, nil
}

// mineOutlinks records the editorial outbound links of newly-ingested items,
// which is what gives internal/recommend's rung 1 (outlink mining, §18.7)
// anything to work with. Called once per poll rather than per item mid-loop,
// because it needs each item's stored URL and content_html, which
// IngestItems does not hand back — ItemsByID is the same lookup
// internal/preserve's Handle already does for the same reason.
func (s *Service) mineOutlinks(ctx context.Context, sourceID string, newIDs []string) {
	if len(newIDs) == 0 {
		return
	}
	fresh, err := s.repo.ItemsByID(ctx, newIDs)
	if err != nil {
		s.logger().Debug("outlink mining: could not load fresh items", "source", sourceID, "err", err)
		return
	}
	for _, it := range fresh {
		if it.URL == "" || it.ContentHTML == "" {
			// A feed that ships no content (title/summary only) has nothing to
			// mine from — not an error, just nothing this item can contribute.
			continue
		}
		links := outlinks.Extract(it.ContentHTML, it.URL, outlinks.Options{})
		if len(links) == 0 {
			continue
		}
		if err := s.repo.RecordOutlinks(ctx, it.ID, sourceID, links); err != nil {
			// One item's links failing to record must not lose the rest of the
			// poll's — the same "one dead thing does not fail the batch"
			// discipline pollOneRecovered's caller applies across sources.
			s.logger().Debug("outlink mining: could not record links",
				"item", it.ID, "url", it.URL, "err", err)
		}
	}
}

// pollOneRecovered runs work (in production, always pollOne — see the
// parameter note below) and converts a panic into the same failed-fetch
// outcome pollOne already produces for an ordinary error, instead of letting
// it unwind past the top of the goroutine PollDue or Refresh started it on.
//
// # Why this has to live here, per source
//
// A panic that unwinds past the top of ITS OWN goroutine's stack kills the
// whole process, full stop — recover() anywhere else, including the
// queued-job path in internal/jobs/jobs.go, is on a different goroutine and
// cannot see it. PollDue and Refresh already run one goroutine per source
// (MaxConcurrentPolls bounds how many run AT ONCE, not how many exist), so
// recovering inside the same goroutine that calls this means a poisoned feed
// can only ever cost its own goroutine — the other sources in the batch were
// never on its stack and are unaffected.
//
// Everything downstream of work — fetcher.Fetch, charsetdec.Decode,
// feed.ParseBytes, the extractor, the sanitizer — parses bytes and headers
// from whatever a publisher's server sent back, with no authentication and no
// user interaction. One hostile or merely broken feed must not be able to take
// the reader down for every tenant on the next scheduled poll.
//
// # Never silent
//
// The recovered value is logged at ERROR with the source id, the feed url and
// the full stack, through ctx so a caller's request id (internal/reqid)
// survives onto the line — matching internal/jobs/jobs.go's "a job panicked"
// treatment, because a log that reads differently between the two recovery
// sites is one more thing to remember when grepping for either. It is then
// routed through RecordFetch exactly like an ordinary fetch error, so the
// source's consecutive_failures and last_error — what the Health panel and the
// failing-sources list both read (store.Feed, store.Stats.Dormant) — pick it
// up like any other broken feed. A recover that stops short of that leaves no
// trace in the data: the feed looks healthy and silently stops, which is worse
// than the crash it replaces, because nobody investigates a feed that merely
// looks a little stale.
//
// The error is prefixed "panic: ", identical to jobs.go's own recover, so a
// crash reads as visibly distinct from an ordinary transient fetch error in
// the failing-sources list — a publisher timing out looks nothing like a
// defect in this codebase, and the two should not be indistinguishable in the
// one place an operator would notice either.
//
// # Why work is a parameter instead of a hard-coded call to pollOne
//
// Service.fetcher is a concrete *feed.Fetcher, not an interface, so the
// network call inside pollOne cannot be swapped for a fake without changing
// that field (or New's signature) — a bigger change than this ticket, and one
// this fix deliberately does not make (see the ticket report). Taking the
// "poll one source" step as a parameter here, rather than hard-coding a call
// to pollOne, at least lets this exact function — the one PollDue and Refresh
// both call in production — be driven directly by a test double for the
// recover-and-record behaviour, without requiring the fetcher itself to be
// fakeable too.
func (s *Service) pollOneRecovered(ctx context.Context, src store.SourceRow,
	work func(context.Context, store.SourceRow) (int, error)) (n int, err error) {
	defer func() {
		if v := recover(); v != nil {
			stack := debug.Stack()
			s.logger().Log(ctx, slog.LevelError, "a feed poll panicked",
				"source_id", src.ID, "feed_url", src.FeedURL,
				"panic", v, "stack", string(stack))
			err = fmt.Errorf("panic: %v", v)
			n = 0
			if rerr := s.repo.RecordFetch(ctx, store.FetchOutcome{
				SourceID: src.ID, Err: err.Error(),
			}); rerr != nil {
				s.logger().Log(ctx, slog.LevelError,
					"recording a panicked poll's failure also failed",
					"source_id", src.ID, "err", rerr)
			}
		}
	}()
	return work(ctx, src)
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
			n, err := s.pollOneRecovered(ctx, src, s.pollOne)
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

// ListFolders opens the category surface.
//
// Thin, like the rest of this layer: the naming rules, the cap and the ownership
// checks live in the repository, where a second caller cannot skip them.
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

// SmartPlusPrefKey is the Smart+ opt-in for My Feed's ranking.
//
// The same string as derive.SmartPlusPrefKey, written out rather than imported: this
// package deliberately does not depend on the deriver (see WithSignalHook), and one
// constant is a smaller price than that dependency. TestRankPrefKeyMatchesTheDeriver in
// internal/app holds the two together — that package already imports both, so the
// duplication is checked by a compiler somewhere rather than by memory.
const SmartPlusPrefKey = "feed.smartPlus"

// SetPrefs merges preferences.
//
// Most preferences are the client's own business and this is a straight write. The Smart+
// opt-in is not: the SERVER reads it, on the next derivation, so saving it changes nothing
// a reader can see until something else happens to schedule one. The hook closes that gap,
// for the same reason onSignals exists — a control that produces no visible result is one
// people conclude is broken, and this one they have just chosen to pay for.
func (s *Service) SetPrefs(ctx context.Context, sc store.Scope, p map[string]string) error {
	if err := s.repo.SetPrefs(ctx, sc, p); err != nil {
		return err
	}
	// After the write, and only if it stuck. Announcing a preference that failed to
	// persist would rebuild the ranking from the OLD value and look like the toggle
	// silently reverted itself.
	//
	// Fired on the way off as well as on. Turning Smart+ back off has to drop the paid
	// ordering, and leaving it on screen after the switch says off is the worse
	// direction of the same bug: it looks like the feature is still being billed.
	if _, ok := p[SmartPlusPrefKey]; ok && s.onRankPrefs != nil {
		s.onRankPrefs(sc)
	}
	return nil
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

// ItemRevisions returns what an article used to say, newest first (TODO F34).
//
// The limit is clamped here rather than at the transport, because a revision is
// several full article bodies and an unbounded request is a memory question
// before it is a UI one.
func (s *Service) ItemRevisions(ctx context.Context, sc store.Scope, itemID string, limit int) ([]store.Revision, error) {
	if limit <= 0 || limit > maxRevisions {
		limit = maxRevisions
	}
	return s.repo.ItemRevisions(ctx, sc, itemID, limit)
}

// maxRevisions bounds a history request. Ten is far past useful — a wire story
// gets corrected two or three times — and the point of the ceiling is the
// response size, not the reader's patience.
const maxRevisions = 10

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

	// Only when something was actually stored. A batch that was entirely duplicates or
	// entirely invalid has changed nothing, and waking the deriver to recompute the
	// same answer is the sort of cost that only shows up as a warm laptop.
	if accepted > 0 && s.onSignals != nil {
		s.onSignals(sc)
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
