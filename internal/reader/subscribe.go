package reader

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/discover"
	"github.com/monstercameron/ArticleFlux/internal/extract"
	"github.com/monstercameron/ArticleFlux/internal/jsonsel"
	"github.com/monstercameron/ArticleFlux/internal/scrapesel"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/timeutil"
	"github.com/monstercameron/ArticleFlux/internal/urlnorm"
)

// Following a page that has no feed — the service half of §11 and §14.2.
//
// Three things live here, in the order they happen: analysing an address,
// subscribing to what the analysis found, and polling the result forever after.
// The third is the one that decides whether this feature is real: a scrape rule
// nobody polls is a demo.

// ScrapeInterval is the floor for a scraped source's poll interval (§14.2).
//
// One hour, and it is a FLOOR rather than a setting, because the cost of getting
// this wrong is not paid by us. A feed is a document a publisher produces for
// machines; an index page is a page they produce for people, and fetching it
// every fifteen minutes from a server that also serves two hundred feeds is how
// an IP ends up blocked for every tenant on the instance.
const ScrapeInterval = 3600

// MaxFullTextPerPoll bounds the second-order fetches.
//
// §14.2 says "one request per poll, no crawling", and full text is a deliberate
// exception to it with a number attached rather than a silent one. The reasoning:
// an index page carries a headline and maybe an excerpt, so without this the
// reading pane has nothing to show and every scraped item is a link to somewhere
// else — which is not a reader, it is a bookmark list. Five is roughly a day of
// posting for the kind of site this feature is for, so a healthy source stays
// caught up while a site that dumps forty posts at once is fetched over several
// polls instead of in one burst.
const MaxFullTextPerPoll = 5

// Analysis is what AnalyzeSite found.
type Analysis struct {
	PageTitle string
	Feeds     []discover.Candidate
	Scrape    *smart.Proposal
	// JSON is the same answer for a page that renders itself: paths into the
	// response its own JavaScript fetches, rather than selectors against markup
	// that has nothing in it (§11.2b).
	JSON *smart.JSONProposal
	// Status explains an absent proposal — see the proto's smart_status.
	Status string
}

// Status values for Analysis.Status.
const (
	StatusNotAsked = "not_asked"
	StatusNoKey    = "no_key"
	StatusRefused  = "refused"
	StatusFailed   = "failed"
	// StatusClientRendered is a page whose entries are fetched by JavaScript
	// after it loads, so its HTML contains no list to write selectors against.
	//
	// Its own status rather than folding into "failed" because the remedy is
	// different and the reader can act on it: a feed address elsewhere on the
	// site will still work, and no amount of retrying this page will. It is also
	// detected BEFORE the model is called, so this one costs nothing.
	StatusClientRendered = "js_rendered"
)

// ErrNoAnalyzer means the instance was built without Smart+ wiring. Distinct
// from "no key": this one is a deployment fact rather than a setting.
var ErrNoAnalyzer = errors.New("reader: site analysis is not available")

// WithSiteAnalysis wires the optional half of subscribing: page discovery and
// the model that reads a page when discovery finds nothing.
//
// Optional on purpose. Every other path in this service works without any of it,
// and an instance with no OpenAI key — the default — still climbs rungs 1-3.
func (s *Service) WithSiteAnalysis(pages *discover.Fetcher, ext *extract.Extractor,
	an *smart.SiteAnalyzer) *Service {
	s.pages = pages
	s.extract = ext
	s.analyzer = an
	return s
}

// AnalyzeSite climbs the subscribe ladder for one address.
//
// The order is the point: everything deterministic runs first and the model is
// asked only when the free rungs came back empty AND the caller asked for it.
// Reversed — model first, "confirm" after — it would spend a request on the
// ~80% of sites that simply declare their feed.
func (s *Service) AnalyzeSite(ctx context.Context, sc store.Scope, rawURL string, useSmart bool) (
	*Analysis, error) {

	if s.pages == nil {
		return nil, ErrNoAnalyzer
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || urlnorm.Host(rawURL) == "" {
		return nil, fmt.Errorf("reader: %q is not a URL", rawURL)
	}

	page, feeds, err := s.pages.Feeds(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	out := &Analysis{Feeds: feeds}
	if page != nil {
		out.PageTitle = page.Title
	}
	// A feed was found: the model is not asked, whatever the caller sent. Paying
	// for a scrape rule for a site that publishes Atom is spending money to get
	// a worse answer.
	if len(feeds) > 0 {
		return out, nil
	}
	if !useSmart {
		out.Status = StatusNotAsked
		return out, nil
	}
	if s.analyzer == nil || !s.analyzer.Configured(ctx) {
		out.Status = StatusNoKey
		return out, nil
	}
	if page == nil {
		out.Status = StatusFailed
		return out, nil
	}
	// robots.txt before the model, not after: asking permission after spending
	// the request is asking rhetorically. This is also the cheap check, so a site
	// that says no costs one small fetch rather than a page of tokens.
	if !s.pages.Allowed(ctx, page.URL) {
		out.Status = StatusRefused
		return out, nil
	}
	// An application shell has nothing to SELECT — but the entries are one
	// request away, and that request is a plain GET returning plain JSON. So
	// the answer for these pages is not "no": it is to follow the data the page
	// follows (§11.2b).
	//
	// The model is never shown the shell. Handed one it does not come back
	// empty-handed — it comes back with the navigation, which repeats and
	// compiles and produces a feed of menu items.
	if smart.ClientRendered(page.HTML) {
		api := s.pages.JSONEndpoint(ctx, page.URL)
		if api == nil {
			out.Status = StatusClientRendered
			return out, nil
		}
		prop, err := s.analyzer.ProposeJSON(ctx, page.URL, api.URL, api.ItemsPath, api.Body)
		if err != nil {
			if errors.Is(err, smart.ErrNoRule) || errors.Is(err, smart.ErrNoList) {
				out.Status = StatusFailed
				return out, nil
			}
			return nil, err
		}
		out.JSON = prop
		return out, nil
	}

	prop, err := s.analyzer.Propose(ctx, page.URL, page.HTML)
	if err != nil {
		// A refusal to propose is a normal outcome — some pages genuinely have
		// no list on them — so it is reported as a status rather than as an
		// error. A transport failure is not, and is returned.
		if errors.Is(err, smart.ErrNoRule) || errors.Is(err, smart.ErrNoList) {
			out.Status = StatusFailed
			return out, nil
		}
		return nil, err
	}
	out.Scrape = prop
	return out, nil
}

// SubscribeScrape subscribes to a page using a rule the reader accepted.
//
// The rule is re-run against the live page before anything is written. That is
// not paranoia about the client: it is the same rule against a page that may
// have changed in the minutes since the preview, and a rule that no longer
// matches must not become a subscription that was born broken. It is also what
// makes the RPC safe to call with a hand-written rule — the client is not
// trusted, and this is where that is enforced rather than assumed.
func (s *Service) SubscribeScrape(ctx context.Context, sc store.Scope,
	indexURL, title, folderID string, rule scrapesel.Rule) (store.Feed, int, error) {

	if s.pages == nil {
		return store.Feed{}, 0, ErrNoAnalyzer
	}
	indexURL = strings.TrimSpace(indexURL)
	if indexURL == "" || urlnorm.Host(indexURL) == "" {
		return store.Feed{}, 0, fmt.Errorf("reader: %q is not a URL", indexURL)
	}
	rule.IndexURL = indexURL

	compiled, err := scrapesel.Compile(rule)
	if err != nil {
		return store.Feed{}, 0, err
	}
	if !s.pages.Allowed(ctx, indexURL) {
		return store.Feed{}, 0, fmt.Errorf(
			"reader: robots.txt asks us not to fetch %s", indexURL)
	}
	page, err := s.pages.Page(ctx, indexURL)
	if err != nil {
		return store.Feed{}, 0, err
	}
	res, err := scrapesel.Extract(compiled, []byte(page.HTML), timeutil.Now())
	if err != nil {
		return store.Feed{}, 0, err
	}
	if len(res.Items) == 0 {
		return store.Feed{}, 0, fmt.Errorf(
			"reader: that rule finds nothing on %s right now", indexURL)
	}

	if strings.TrimSpace(title) == "" {
		title = page.Title
	}
	// The natural key namespaces scraped sources away from feeds, so a site
	// whose feed is discovered later becomes a SECOND source rather than
	// silently inheriting a scraped one's items and history.
	f, _, err := s.repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey:     "scrape:" + urlnorm.DupeKey(indexURL),
		FeedURL:        indexURL,
		SiteURL:        indexURL,
		Title:          strings.TrimSpace(title),
		FolderID:       folderID,
		Kind:           "scrape",
		FetchIntervalS: ScrapeInterval,
	})
	if err != nil {
		return store.Feed{}, 0, err
	}

	if err := s.repo.PutScrapeRule(ctx, sc, store.ScrapeRule{
		SourceID:        f.SourceID,
		IndexURL:        indexURL,
		ItemSelector:    rule.ItemSelector,
		TitleSelector:   rule.TitleSelector,
		LinkSelector:    rule.LinkSelector,
		DateSelector:    rule.DateSelector,
		DateLayout:      rule.DateLayout,
		SummarySelector: rule.SummarySelector,
		ImageSelector:   rule.ImageSelector,
		AuthorSelector:  rule.AuthorSelector,
		RespectRobots:   true,
	}); err != nil {
		return store.Feed{}, 0, err
	}

	// Ingest what the preview already extracted rather than fetching the page a
	// third time. The reader watched these items appear in the dialog; making
	// them wait while the same page is fetched again to produce the same list
	// would be spending a request to look busy.
	n, err := s.ingestScraped(ctx, f.SourceID, res)
	if err != nil {
		return f, 0, err
	}
	refreshed, ferr := s.repo.ListFeeds(ctx, sc)
	if ferr == nil {
		for _, rf := range refreshed {
			if rf.SourceID == f.SourceID {
				return rf, n, nil
			}
		}
	}
	return f, n, nil
}

// SubscribeJSON subscribes to a page whose entries live in an API (§11.2b).
//
// The counterpart to SubscribeScrape and, like it, the rule is re-run against
// the live response before anything is written: the client is not trusted, the
// preview may be minutes old, and a rule that no longer works must not become a
// subscription that was born broken.
func (s *Service) SubscribeJSON(ctx context.Context, sc store.Scope,
	indexURL, title, folderID string, rule jsonsel.Rule) (store.Feed, int, error) {

	if s.pages == nil {
		return store.Feed{}, 0, ErrNoAnalyzer
	}
	indexURL = strings.TrimSpace(indexURL)
	dataURL := strings.TrimSpace(rule.DataURL)
	if indexURL == "" || urlnorm.Host(indexURL) == "" {
		return store.Feed{}, 0, fmt.Errorf("reader: %q is not a URL", indexURL)
	}
	if dataURL == "" || urlnorm.Host(dataURL) == "" {
		return store.Feed{}, 0, fmt.Errorf("reader: %q is not a URL", dataURL)
	}
	// Same origin, enforced rather than assumed. Without this the RPC is an
	// open proxy: a client could name any page and any unrelated data address,
	// and the server would fetch the second one hourly forever on its behalf.
	if !sameHost(indexURL, dataURL) {
		return store.Feed{}, 0, fmt.Errorf(
			"reader: the data address must be on the same site as the page")
	}
	rule.IndexURL = indexURL

	compiled, err := jsonsel.Compile(rule)
	if err != nil {
		return store.Feed{}, 0, err
	}
	if !s.pages.Allowed(ctx, dataURL) {
		return store.Feed{}, 0, fmt.Errorf("reader: robots.txt asks us not to fetch %s", dataURL)
	}
	body, err := s.pages.JSON(ctx, dataURL)
	if err != nil {
		return store.Feed{}, 0, err
	}
	res, err := jsonsel.Extract(compiled, body, timeutil.Now())
	if err != nil {
		return store.Feed{}, 0, err
	}
	if len(res.Items) == 0 {
		return store.Feed{}, 0, fmt.Errorf("reader: that rule finds nothing at %s right now", dataURL)
	}

	if strings.TrimSpace(title) == "" {
		if page, perr := s.pages.Page(ctx, indexURL); perr == nil {
			title = page.Title
		}
	}
	f, _, err := s.repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey:     "scrape:" + urlnorm.DupeKey(indexURL),
		FeedURL:        indexURL,
		SiteURL:        indexURL,
		Title:          strings.TrimSpace(title),
		FolderID:       folderID,
		Kind:           "scrape",
		FetchIntervalS: ScrapeInterval,
	})
	if err != nil {
		return store.Feed{}, 0, err
	}
	if err := s.repo.PutScrapeRule(ctx, sc, store.ScrapeRule{
		SourceID:        f.SourceID,
		IndexURL:        indexURL,
		Kind:            "json",
		DataURL:         dataURL,
		ItemSelector:    rule.ItemsPath,
		TitleSelector:   rule.TitlePath,
		LinkSelector:    rule.LinkPath,
		LinkTemplate:    rule.LinkTemplate,
		IDSelector:      rule.IDPath,
		DateSelector:    rule.DatePath,
		SummarySelector: rule.SummaryPath,
		ImageSelector:   rule.ImagePath,
		AuthorSelector:  rule.AuthorPath,
		RespectRobots:   true,
	}); err != nil {
		return store.Feed{}, 0, err
	}

	n, err := s.ingestScraped(ctx, f.SourceID, jsonItems(res.Items))
	if err != nil {
		return f, 0, err
	}
	refreshed, ferr := s.repo.ListFeeds(ctx, sc)
	if ferr == nil {
		for _, rf := range refreshed {
			if rf.SourceID == f.SourceID {
				return rf, n, nil
			}
		}
	}
	return f, n, nil
}

// sameHost keeps a JSON rule pointed at the site the reader subscribed to.
func sameHost(a, b string) bool {
	ha, hb := urlnorm.Host(a), urlnorm.Host(b)
	return ha != "" && strings.EqualFold(ha, hb)
}

// jsonItems converts to the shape ingestScraped takes, which is scrapesel's.
//
// The conversion exists so the two extractors stay independent of each other:
// neither imports the other, and the pipeline downstream cannot tell which one
// produced an item — which is the property that keeps ingest, dedup, health,
// ranking and search working unchanged for both.
func jsonItems(in []jsonsel.Item) scrapesel.Result {
	out := scrapesel.Result{Matched: len(in)}
	for _, it := range in {
		out.Items = append(out.Items, scrapesel.Item{
			GUID: it.GUID, URL: it.URL, DupeKey: it.DupeKey, Title: it.Title,
			Author: it.Author, Summary: it.Summary,
			PublishedAt: it.PublishedAt, ImageURL: it.ImageURL,
		})
	}
	return out
}

// pollJSON is the scraped source's poll when its rule is a json one.
func (s *Service) pollJSON(ctx context.Context, src store.SourceRow, rule store.ScrapeRule) (int, error) {
	if rule.RespectRobots && !s.pages.Allowed(ctx, rule.DataURL) {
		s.recordFetch(ctx, store.FetchOutcome{
			SourceID: src.ID, Err: "robots.txt now disallows this address"})
		return 0, fmt.Errorf("reader: robots.txt disallows %s", rule.DataURL)
	}
	body, err := s.pages.JSON(ctx, rule.DataURL)
	if err != nil {
		s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: err.Error()})
		return 0, err
	}
	compiled, err := jsonsel.Compile(jsonsel.Rule{
		IndexURL:     rule.IndexURL,
		DataURL:      rule.DataURL,
		ItemsPath:    rule.ItemSelector,
		TitlePath:    rule.TitleSelector,
		LinkPath:     rule.LinkSelector,
		LinkTemplate: rule.LinkTemplate,
		IDPath:       rule.IDSelector,
		DatePath:     rule.DateSelector,
		SummaryPath:  rule.SummarySelector,
		ImagePath:    rule.ImageSelector,
		AuthorPath:   rule.AuthorSelector,
	})
	if err != nil {
		s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: err.Error()})
		return 0, err
	}
	res, err := jsonsel.Extract(compiled, body, timeutil.Now())
	if err != nil {
		s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: err.Error()})
		return 0, err
	}

	_ = s.repo.RecordScrapeOutcome(ctx, src.ID, len(res.Items))
	if len(res.Items) == 0 {
		msg := "the rule matched nothing in this response"
		if res.Found > 0 {
			msg = fmt.Sprintf("%d entries found but none produced an item", res.Found)
		}
		s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: msg})
		return 0, errors.New("reader: " + msg)
	}

	n, err := s.ingestScraped(ctx, src.ID, jsonItems(res.Items))
	if err != nil {
		s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: err.Error()})
		return 0, err
	}
	s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, SiteURL: rule.IndexURL})
	return n, nil
}

// pollScrape is the scraped source's poll, in place of a feed fetch.
func (s *Service) pollScrape(ctx context.Context, src store.SourceRow) (int, error) {
	if s.pages == nil {
		return 0, ErrNoAnalyzer
	}
	rule, err := s.repo.ScrapeRuleFor(ctx, src.ID)
	if err != nil {
		// A scraped source with no rule cannot be polled and never will be
		// pollable, so it is recorded as a failure rather than retried into a
		// backoff nobody reads.
		s.recordFetch(ctx, store.FetchOutcome{
			SourceID: src.ID, Err: "no scrape rule for this source"})
		return 0, err
	}
	// Two dialects, one source kind. A json rule reads the address the page's
	// own JavaScript reads; an html rule reads the page.
	if rule.Kind == "json" {
		return s.pollJSON(ctx, src, rule)
	}
	if rule.RespectRobots && !s.pages.Allowed(ctx, rule.IndexURL) {
		// A site that changed its mind is obeyed, and the reason is recorded
		// where the feed's health is shown. Silently continuing would be the
		// version of this feature that gets an instance banned.
		s.recordFetch(ctx, store.FetchOutcome{
			SourceID: src.ID, Err: "robots.txt now disallows this page"})
		return 0, fmt.Errorf("reader: robots.txt disallows %s", rule.IndexURL)
	}

	page, err := s.pages.Page(ctx, rule.IndexURL)
	if err != nil {
		s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: err.Error()})
		return 0, err
	}
	compiled, err := scrapesel.Compile(scrapesel.Rule{
		IndexURL:        rule.IndexURL,
		ItemSelector:    rule.ItemSelector,
		TitleSelector:   rule.TitleSelector,
		LinkSelector:    rule.LinkSelector,
		DateSelector:    rule.DateSelector,
		DateLayout:      rule.DateLayout,
		SummarySelector: rule.SummarySelector,
		ImageSelector:   rule.ImageSelector,
		AuthorSelector:  rule.AuthorSelector,
	})
	if err != nil {
		s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: err.Error()})
		return 0, err
	}
	res, err := scrapesel.Extract(compiled, []byte(page.HTML), timeutil.Now())
	if err != nil {
		s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: err.Error()})
		return 0, err
	}

	// The health signal §14.2 asks for, and the one that makes a redesign
	// distinguishable from a quiet week: zero items from a rule that used to
	// work is a BROKEN RULE, and it is counted rather than swallowed.
	_ = s.repo.RecordScrapeOutcome(ctx, src.ID, len(res.Items))
	if len(res.Items) == 0 {
		msg := "the scrape rule matched nothing on this page"
		if res.Matched > 0 {
			msg = fmt.Sprintf("%d containers matched but none produced an item", res.Matched)
		}
		s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: msg})
		return 0, errors.New("reader: " + msg)
	}

	n, err := s.ingestScraped(ctx, src.ID, res)
	if err != nil {
		s.recordFetch(ctx, store.FetchOutcome{SourceID: src.ID, Err: err.Error()})
		return 0, err
	}
	s.recordFetch(ctx, store.FetchOutcome{
		SourceID: src.ID, Title: page.Title, SiteURL: rule.IndexURL,
	})
	return n, nil
}

// ingestScraped stores extracted items, fetching full text for the new ones.
//
// "New" is decided BEFORE ingest, against the guids already stored, because
// after ingest every item looks equally present and the choice would be between
// re-fetching the whole page's worth of articles every poll or never fetching
// any. This is the only reason store.KnownGUIDs exists.
func (s *Service) ingestScraped(ctx context.Context, sourceID string, res scrapesel.Result) (int, error) {
	guids := make([]string, 0, len(res.Items))
	for _, it := range res.Items {
		guids = append(guids, it.GUID)
	}
	known, err := s.repo.KnownGUIDs(ctx, sourceID, guids)
	if err != nil {
		// Not fatal: the worst case is that full text is fetched for items that
		// are already stored, which is wasteful and harmless. Failing the poll
		// over it would trade the whole article list for a cache miss.
		known = map[string]bool{}
	}

	items := make([]store.IngestItem, 0, len(res.Items))
	budget := MaxFullTextPerPoll
	for _, it := range res.Items {
		item := store.IngestItem{
			GUID: it.GUID, URL: it.URL, DupeKey: it.DupeKey, Title: it.Title,
			Author: it.Author, Summary: it.Summary, ContentHTML: it.ContentHTML,
			PublishedAt: it.PublishedAt, ImageURL: it.ImageURL,
		}
		if !known[it.GUID] && budget > 0 && s.extract != nil && it.URL != "" {
			budget--
			if art := s.fullText(ctx, it.URL); art != nil {
				// The page's own values win over the index page's guesses,
				// except where the index knew something the article did not: a
				// listing frequently carries a date the article page omits.
				item.ContentHTML = art.HTML
				item.WordCount = art.WordCount
				if art.Title != "" {
					item.Title = art.Title
				}
				if art.Excerpt != "" && item.Summary == "" {
					item.Summary = art.Excerpt
				}
				if art.ImageURL != "" && item.ImageURL == "" {
					item.ImageURL = art.ImageURL
				}
				if item.PublishedAt.IsZero() && !art.PublishedAt.IsZero() {
					item.PublishedAt = art.PublishedAt
				}
				if art.Byline != "" && item.Author == "" {
					item.Author = art.Byline
				}
			}
		}
		items = append(items, item)
	}

	ing, err := s.repo.IngestItems(ctx, sourceID, items)
	if err != nil {
		return 0, err
	}
	s.mineOutlinks(ctx, sourceID, ing.NewIDs)
	return ing.New, nil
}

// fullText fetches one article, or returns nil.
//
// Every failure is a nil: a paywall, a login wall, a page readability cannot
// find an article in, a timeout. The item still lands with its headline and its
// link, which is what the index page gave us and is strictly better than
// dropping it because its body could not be read.
func (s *Service) fullText(ctx context.Context, pageURL string) *extract.Article {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	art, err := s.extract.Fetch(ctx, pageURL)
	if err != nil || art == nil || art.WordCount < extract.MinWords {
		return nil
	}
	return art
}
