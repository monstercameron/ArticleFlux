package grpcsrv

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/scrapesel"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The subscribe ladder's transport half (§11, §14.2).

// smartFollowPref is the per-user opt-in for the model reading a page.
//
// The key names the SETTING — "Smart+ follow" in the interface — rather than the
// tier it bills to. Smart+ is the brand every model-backed feature wears (Smart+
// voice is the other), so a key called `smart.on` would be a key for all of
// them.
//
// Its own key rather than sharing tts.smartPlus, because they are different
// decisions about different content: one sends the article you chose to read to
// a speech provider, the other sends the STRUCTURE of a page you are thinking
// about subscribing to. A reader who wants one may well not want the other, and
// a single switch would make consent to the second implicit in the first.
//
// Default off, checked here, at the caller — which is rule 1 of the three
// internal/llm documents for every egress path.
const smartFollowPref = "smart.follow"

func (s *ReaderServer) AnalyzeSite(ctx context.Context, req *pb.AnalyzeSiteRequest) (
	*pb.AnalyzeSiteResponse, error) {

	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}

	// Two conditions have to hold before a page's markup leaves the machine, and
	// both are checked HERE rather than inside the service: the reader asked on
	// this request, and this reader has the preference on. Neither implies the
	// other — the request flag alone would let a stale client egress for someone
	// who turned the feature off, and the preference alone would egress on every
	// address anyone types.
	useSmart := req.GetSmart()
	if useSmart {
		prefs, perr := s.svc.GetPrefs(ctx, sc)
		if perr != nil || prefs[smartFollowPref] != "true" {
			// Not an error: the dialog turns this into the sentence that
			// explains what turning it on would do. An error here would be a red
			// message for something the reader has not done wrong.
			return &pb.AnalyzeSiteResponse{SmartStatus: "off"}, nil
		}
	}

	res, err := s.svc.AnalyzeSite(ctx, sc, req.GetUrl(), useSmart)
	if err != nil {
		switch {
		case errors.Is(err, reader.ErrNoAnalyzer):
			return nil, status.Error(codes.Unimplemented,
				"this server was built without site analysis")
		case errors.Is(err, llm.ErrNotConfigured):
			return &pb.AnalyzeSiteResponse{SmartStatus: reader.StatusNoKey}, nil
		default:
			// A page that will not load is the reader's problem to fix — a typo,
			// a site that is down, an address behind a login — so it is
			// InvalidArgument with the reason rather than a swallowed Internal.
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}

	out := &pb.AnalyzeSiteResponse{PageTitle: res.PageTitle, SmartStatus: res.Status}
	for _, c := range res.Feeds {
		out.Feeds = append(out.Feeds, &pb.FeedCandidate{
			Url: c.URL, Title: c.Title, ItemCount: int32(c.Items), How: c.How,
		})
	}
	if res.Scrape != nil {
		prop := &pb.ScrapeProposal{
			IndexUrl: req.GetUrl(),
			Rule:     pbRule(res.Scrape.Rule),
			Found:    int32(res.Scrape.Matched),
			Notes:    res.Scrape.Notes,
		}
		for _, it := range res.Scrape.Samples() {
			var published string
			if !it.PublishedAt.IsZero() {
				published = it.PublishedAt.UTC().Format(time.RFC3339)
			}
			prop.Samples = append(prop.Samples, &pb.ScrapeSample{
				Title: it.Title, Url: it.URL, PublishedAt: published, Summary: it.Summary,
			})
		}
		out.Scrape = prop
	}
	return out, nil
}

func (s *ReaderServer) SubscribeScrape(ctx context.Context, req *pb.SubscribeScrapeRequest) (
	*pb.SubscribeScrapeResponse, error) {

	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	f, n, err := s.svc.SubscribeScrape(ctx, sc, req.GetIndexUrl(), req.GetTitle(),
		req.GetFolderId(), domainRule(req.GetRule()))
	if err != nil {
		switch {
		case errors.Is(err, reader.ErrNoAnalyzer):
			return nil, status.Error(codes.Unimplemented,
				"this server was built without site analysis")
		case errors.Is(err, store.ErrNotFound):
			return nil, toStatus(err)
		default:
			// Everything else here is a rule that does not work or an address
			// that will not load, both of which the reader can act on.
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	return &pb.SubscribeScrapeResponse{
		Feed: &pb.Feed{
			Id: f.ID, SourceId: f.SourceID, Title: f.Title,
			FeedUrl: f.FeedURL, SiteUrl: f.SiteURL, FolderId: f.FolderID,
			UnreadCount: int32(f.UnreadCount),
		},
		Items: int32(n),
	}, nil
}

func pbRule(r scrapesel.Rule) *pb.ScrapeRule {
	return &pb.ScrapeRule{
		ItemSelector:    r.ItemSelector,
		TitleSelector:   r.TitleSelector,
		LinkSelector:    r.LinkSelector,
		DateSelector:    r.DateSelector,
		DateLayout:      r.DateLayout,
		SummarySelector: r.SummarySelector,
		ImageSelector:   r.ImageSelector,
		AuthorSelector:  r.AuthorSelector,
	}
}

func domainRule(r *pb.ScrapeRule) scrapesel.Rule {
	return scrapesel.Rule{
		ItemSelector:    r.GetItemSelector(),
		TitleSelector:   r.GetTitleSelector(),
		LinkSelector:    r.GetLinkSelector(),
		DateSelector:    r.GetDateSelector(),
		DateLayout:      r.GetDateLayout(),
		SummarySelector: r.GetSummarySelector(),
		ImageSelector:   r.GetImageSelector(),
		AuthorSelector:  r.GetAuthorSelector(),
	}
}
