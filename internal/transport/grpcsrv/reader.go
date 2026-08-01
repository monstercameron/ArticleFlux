// Package grpcsrv adapts the service layer to gRPC.
//
// It is deliberately thin: translation between proto messages and domain types,
// plus the §20.7 error taxonomy. Any logic that appears here is logic the REST
// sync API and the offline-pack channel will not have, which is exactly the
// divergence the single service layer exists to prevent.
package grpcsrv

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/apierr"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/recommendjob"
	"github.com/monstercameron/ArticleFlux/internal/rewrite"
	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// ReaderServer implements pb.ReaderServiceServer.
type ReaderServer struct {
	pb.UnimplementedReaderServiceServer
	svc *reader.Service
	// scopeOf resolves the caller. Injected rather than hard-wired so the auth
	// interceptor owns identity and this file stays about translation.
	scopeOf func(context.Context) (store.Scope, error)
	// mintAsset turns an absolute subresource URL into a proxy URL, or "" to
	// leave it alone. Nil disables the §10.1a rewrite entirely.
	mintAsset func(absURL string) string
	// mintPage turns an article URL into a §10.1b page-proxy URL. Nil means the
	// instance does not proxy pages, and the client hides the control rather
	// than offering one that 501s.
	mintPage func(absURL string) string
	// mintStream turns an article URL into a §10.1d live-view URL. Nil means
	// the instance does not run a browser, and the client offers no Live toggle.
	mintStream func(absURL string) string
	// scrollLive delivers a wheel event to a running live view.
	scrollLive func(sessionID string, dx, dy float64) error
	// mintSpeech turns an item into a §10.7 Smart+ listening URL. Nil, or ""
	// back, means this instance has no OpenAI key and the client leaves the
	// listen control on the browser's own synthesiser.
	//
	// It takes a scope where the others take a URL, because what it authorises
	// is per-reader rather than per-target: the article is private, so the
	// ticket has to say who may hear it. See app.SpeechURL.
	mintSpeech func(ctx context.Context, sc store.Scope, itemID string) string
	// categorizer suggests a category for a feed Subscribe just added. Nil on
	// an instance that never wired one (every test that does not want a Smart+
	// dependency) — Subscribe treats that exactly like the pref being off: no
	// suggestion, no error.
	categorizer *smart.Categorizer
	// repo backs the /discover RPCs (§18.7, M16) directly rather than through
	// reader.Service, which has no recommendations methods of its own —
	// recommend storage is its own concern (internal/store/outlinks.go), not
	// part of the read/subscribe surface reader.Service wraps.
	repo *store.ReaderRepo
	// recommender enqueues a fresh scoring pass. Nil on an instance that never
	// wired one, exactly like categorizer — RefreshRecommendations then reports
	// Unavailable rather than panicking.
	recommender *recommendjob.Service
}

// NewReaderServer wires a service to gRPC.
func NewReaderServer(svc *reader.Service, scopeOf func(context.Context) (store.Scope, error)) *ReaderServer {
	return &ReaderServer{svc: svc, scopeOf: scopeOf}
}

// WithAssetProxy points article images at the local proxy (§10.1a).
//
// This lives at the transport edge on purpose, and it is the one place in this
// file that needed an argument for being here. The rewrite is not domain logic
// — the stored HTML is left exactly as the publisher wrote it, and nothing
// downstream of the database changes. What it produces is a URL for *this*
// client to fetch, minted with an expiry, valid for hours. Doing it in the
// service would mean caching or persisting URLs that expire, which is how you
// end up serving a stale capability from a pack built last week.
func (s *ReaderServer) WithAssetProxy(mint func(absURL string) string) *ReaderServer {
	s.mintAsset = mint
	return s
}

// WithPageProxy lets the client open the publisher's page through us (§10.1b).
func (s *ReaderServer) WithPageProxy(mint func(absURL string) string) *ReaderServer {
	s.mintPage = mint
	return s
}

// WithSpeech offers the §10.7 Smart+ voice on an opened article.
func (s *ReaderServer) WithSpeech(
	mint func(ctx context.Context, sc store.Scope, itemID string) string,
) *ReaderServer {
	s.mintSpeech = mint
	return s
}

// WithCategorizer offers the Smart+ category suggestion Subscribe attaches to
// a successful add (subscribe.go's smartCategorizePref gate).
func (s *ReaderServer) WithCategorizer(c *smart.Categorizer) *ReaderServer {
	s.categorizer = c
	return s
}

// WithRecommendations wires /discover's RPCs to storage and the job that
// scores it.
func (s *ReaderServer) WithRecommendations(repo *store.ReaderRepo, rec *recommendjob.Service) *ReaderServer {
	s.repo = repo
	s.recommender = rec
	return s
}

// WithLiveView offers the §10.1d browser stream alongside the page proxy, and
// the input path that makes it more than a poster.
func (s *ReaderServer) WithLiveView(
	mint func(absURL string) string,
	scroll func(sessionID string, dx, dy float64) error,
) *ReaderServer {
	s.mintStream = mint
	s.scrollLive = scroll
	return s
}

// ScrollLiveView moves a running live view (§10.1d).
//
// Authorisation is possession of the session id, which IS the stream URL's
// signature — the same proof that authorised opening the view. So there is no
// separate check here, and deliberately no lookup of who owns the session:
// there is nothing to look up, because the capability is the whole claim.
//
// A dead session answers `live: false` rather than an error. The client is
// sending these from a wheel handler at speed; turning an expired view into a
// stream of red errors would say "something is broken" about the ordinary,
// expected end of a session.
func (s *ReaderServer) ScrollLiveView(ctx context.Context, req *pb.ScrollLiveViewRequest) (*pb.ScrollLiveViewResponse, error) {
	if _, err := s.scopeOf(ctx); err != nil {
		return nil, toStatus(err)
	}
	if s.scrollLive == nil {
		return &pb.ScrollLiveViewResponse{Live: false}, nil
	}
	if err := s.scrollLive(req.GetSessionId(), req.GetDeltaX(), req.GetDeltaY()); err != nil {
		return &pb.ScrollLiveViewResponse{Live: false}, nil
	}
	return &pb.ScrollLiveViewResponse{Live: true}, nil
}

// toStatus maps a domain error to the §20.7 taxonomy.
//
// The rule that matters: a row belonging to another tenant returns NotFound, not
// PermissionDenied. PermissionDenied on item 4711 confirms item 4711 exists,
// which is a tenant-isolation leak dressed as good manners.
// toStatus turns a domain error into §20.7's wire form.
//
// The table it used to hold moved to `internal/apierr` (TODO 7.3a). It lived
// here, and here is one of the THREE transports that share the taxonomy — a
// mapping owned by one transport is one the other two re-derive, and that gets
// discovered when a client sees a 404 from the tunnel and a 403 from the sync
// API for the same condition.
//
// What has not changed: an unrecognised error becomes Internal with a fixed
// safe message, and the original goes to the log with a request id (§22.11).
// The client never sees a wrapped database error.
func toStatus(err error) error {
	return apierr.Status(apierr.FromDomain(err))
}

func (s *ReaderServer) ListFeeds(ctx context.Context, _ *pb.ListFeedsRequest) (*pb.ListFeedsResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	feeds, total, err := s.svc.ListFeeds(ctx, sc)
	if err != nil {
		return nil, toStatus(err)
	}
	// Sized to the sidebar it is about to fill; see ListItems for why.
	out := &pb.ListFeedsResponse{
		TotalUnread: int32(total),
		Feeds:       make([]*pb.Feed, 0, len(feeds)),
	}
	// My Feed's count, alongside the sidebar it is drawn in.
	//
	// A failed count is not a failed sidebar. Losing it costs one badge; refusing to return
	// the feed list because a COUNT went wrong would trade the whole rail for an ornament —
	// the same trade ListItems makes with its total, for the same reason.
	if n, cerr := s.svc.CountRanked(ctx, sc); cerr == nil {
		out.RankedCount = int32(n)
	}
	for _, f := range feeds {
		out.Feeds = append(out.Feeds, &pb.Feed{
			Id: f.ID, SourceId: f.SourceID, Title: f.Title,
			FeedUrl: f.FeedURL, SiteUrl: f.SiteURL, FolderId: f.FolderID,
			UnreadCount:         int32(f.UnreadCount),
			LastSuccessAt:       f.LastSuccess,
			ConsecutiveFailures: int32(f.Failures),
			LastError:           f.LastError,
		})
	}
	return out, nil
}

func (s *ReaderServer) ListItems(ctx context.Context, req *pb.ListItemsRequest) (*pb.ListItemsResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	// My Feed is not a filter over the item list, it is a different table, so it
	// returns before the query below is built.
	//
	// Until this existed the megafeed scope fell through the switch to "no filter" and
	// silently served the plain chronological list. That is the worst available
	// behaviour: the client asked for a ranking, got something that looks like one, and
	// nothing anywhere reports that the interest layer was not consulted.
	if req.GetScope() == pb.ListScope_LIST_SCOPE_MEGAFEED {
		return s.listRanked(ctx, sc, req)
	}

	q := store.ListQuery{
		Cursor:     req.GetCursor(),
		Limit:      int(req.GetLimit()),
		UnreadOnly: req.GetUnreadOnly(),
		SourceIDs:  req.GetSourceIds(),
	}
	switch req.GetScope() {
	case pb.ListScope_LIST_SCOPE_FEED:
		q.SourceID = req.GetSourceId()
	case pb.ListScope_LIST_SCOPE_STARRED:
		// Starred is never filtered by unread: a starred item is one you kept,
		// and hiding the ones you have read would empty the list you saved.
		q.StarredOnly = true
		q.UnreadOnly = false
	case pb.ListScope_LIST_SCOPE_LIKED:
		// Same reasoning: a verdict is something you gave AFTER reading, so
		// filtering these by unread would always return nothing.
		q.RatedOnly = 1
		q.UnreadOnly = false
	case pb.ListScope_LIST_SCOPE_DISLIKED:
		q.RatedOnly = -1
		q.UnreadOnly = false
	}

	items, next, err := s.svc.ListItems(ctx, sc, q)
	if err != nil {
		return nil, toStatus(err)
	}
	// One batched lookup for the whole page (§27.2), beside the list rather
	// than inside toPBItem: ListItems returns up to 200 rows, and a per-item
	// category query would be 200 round trips against the single-writer
	// pool's read side for a page a reader waits on.
	//
	// A failed lookup is not a failed list, the same trade CountItems makes
	// below — cats stays nil, every item's category comes back the zero
	// value, and the reader still gets their articles.
	cats, cerr := s.svc.CategoriesFor(ctx, sc, itemIDs(items))
	if cerr != nil {
		cats = nil
	}
	// Sized up front. The page length is already known — it is len(items) — and
	// growing into it costs six reallocations and six copies for a fifty-row
	// page, on the request a reader waits on more often than any other.
	out := &pb.ListItemsResponse{
		NextCursor: next,
		Items:      make([]*pb.Item, 0, len(items)),
	}
	for _, it := range items {
		out.Items = append(out.Items, withCategory(toPBItem(it, false), cats[it.ID]))
	}

	// Only on the first page. The total does not change while paging, and the
	// client needs it once — to size the scrollbar to the whole result set so
	// unloaded rows are places you can scroll to rather than places that do not
	// exist yet.
	//
	// A failed count is not a failed list. Losing the count costs the scrollbar
	// its final length, which the client already handles by falling back to the
	// loaded length; refusing to return the page the user asked for because a
	// COUNT went wrong would be trading a whole feature for a cosmetic one.
	if req.GetCursor() == "" {
		if n, cerr := s.svc.CountItems(ctx, sc, q); cerr == nil {
			out.Total = int32(n)
		}
	}
	return out, nil
}

// GetItemRevisions answers "what did this say before?" (TODO F34).
//
// Its own call rather than a field on GetItem: the history is several full
// article bodies, and almost nobody asks for it — it is a click on the "edited"
// badge, which most articles never show. Putting it on the article would make
// every open pay for a thing that is empty in the overwhelming majority of cases.
//
// An empty result is a normal answer, not a NotFound. It means "no edit seen",
// which covers both an article nobody changed and — importantly — every article
// stored before we started hashing bodies. Those are indistinguishable from
// here and it would be dishonest to report either as "never edited".
func (s *ReaderServer) GetItemRevisions(ctx context.Context, req *pb.GetItemRevisionsRequest) (*pb.GetItemRevisionsResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	revs, err := s.svc.ItemRevisions(ctx, sc, req.GetItemId(), int(req.GetLimit()))
	if err != nil {
		return nil, toStatus(err)
	}
	// Old bodies go through the same image proxy as the current one. Skipping it
	// would make the one screen where a reader compares two versions the one
	// screen that fetches pictures straight from the publisher — a privacy hole
	// opened by a feature about honesty. The lookup is for the article's URL,
	// which is what relative image paths resolve against; if it fails, the
	// revisions are still returned unproxied-but-sanitized rather than withheld.
	base, berr := s.svc.GetItem(ctx, sc, req.GetItemId())

	out := &pb.GetItemRevisionsResponse{Revisions: make([]*pb.ItemRevision, 0, len(revs))}
	for _, r := range revs {
		body := r.ContentHTML
		if berr == nil {
			body = s.proxyImages(ctx, sc, base, body)
		}
		out.Revisions = append(out.Revisions, &pb.ItemRevision{
			Title: r.Title, Summary: r.Summary, ContentHtml: body,
			SeenAt: r.SeenAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

func (s *ReaderServer) GetItem(ctx context.Context, req *pb.GetItemRequest) (*pb.GetItemResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	it, err := s.svc.GetItem(ctx, sc, req.GetId())
	if err != nil {
		return nil, toStatus(err)
	}
	out := toPBItem(it, true)
	// A single-item lookup, which is still the batched call — it just has a
	// batch of one. A failed lookup degrades the same way ListItems does: the
	// article is more important than its chip.
	if cats, cerr := s.svc.CategoriesFor(ctx, sc, []string{it.ID}); cerr == nil {
		withCategory(out, cats[it.ID])
	}
	// The note rides along with the article rather than needing its own round
	// trip: you open an item to read it, and your own note is part of that.
	if note, nerr := s.svc.GetNote(ctx, sc, req.GetId()); nerr == nil {
		out.Note = note
	}
	out.ContentHtml = s.proxyImages(ctx, sc, it, out.GetContentHtml())
	if it.URL != "" {
		if s.mintPage != nil {
			out.ProxyUrl = s.mintPage(it.URL)
		}
		if s.mintStream != nil {
			out.StreamUrl = s.mintStream(it.URL)
		}
	}
	// Outside the URL guard above: the other two proxy the publisher's page and
	// need somewhere to point, but reading an article aloud only needs the text
	// we already hold. A scraped item with no canonical URL is still listenable.
	if s.mintSpeech != nil {
		out.SpeechUrl = s.mintSpeech(ctx, sc, it.ID)
	}
	return &pb.GetItemResponse{Item: out}, nil
}

// proxyImages repoints an article's subresources at the local proxy (§10.1a).
//
// Returns the HTML unchanged whenever it cannot do better, which is most of the
// interesting cases: no proxy configured, no item URL to resolve relative
// references against, the user opted out, or the rewrite itself failed. A
// broken rewrite must degrade to the publisher's own URLs — those work on any
// network that is not blocking them, which is every network but the one this
// feature exists for.
func (s *ReaderServer) proxyImages(ctx context.Context, sc store.Scope, it store.Item, htm string) string {
	if s.mintAsset == nil || htm == "" {
		return htm
	}
	// Relative URLs resolve against the article, not the feed: a feed hosted on
	// one domain routinely carries items on another.
	base, err := url.Parse(it.URL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return htm
	}
	// Opt-out, not opt-in. The default is on because the failure it prevents —
	// an article whose pictures never load — looks like this application is
	// broken, and the reader has no way to know the difference.
	if prefs, perr := s.svc.GetPrefs(ctx, sc); perr == nil && prefs[proxyImagesPref] == "false" {
		return htm
	}
	out, err := rewrite.HTML(htm, rewrite.Options{
		Base:  base,
		Asset: s.mintAsset,
		// Links stay pointed at the publisher: the reader is reading our copy
		// of the article, and clicking a link is a deliberate act of leaving.
		// Tier 2 is where that changes.
		DropIntegrity: true,
		DropMetaCSP:   true,
	})
	if err != nil {
		return htm
	}
	return out
}

// proxyImagesPref is the per-user opt-out. Only the exact string "false" turns
// it off, so an unset preference — which is every user until one goes looking —
// means on.
const proxyImagesPref = "proxy.images"

func (s *ReaderServer) SetItemState(ctx context.Context, req *pb.SetItemStateRequest) (*pb.SetItemStateResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	// The optional fields are load-bearing: an unset flag must leave the stored
	// value alone, so marking read does not clobber a star set on another device.
	var c store.StateChange
	if req.Read != nil {
		v := req.GetRead()
		c.Read = &v
	}
	if req.Starred != nil {
		v := req.GetStarred()
		c.Starred = &v
	}
	if req.Rating != nil {
		v := int(req.GetRating())
		c.Rating = &v
	}
	it, rev, err := s.svc.SetItemState(ctx, sc, req.GetItemId(), c)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.SetItemStateResponse{Item: toPBItem(it, false), Rev: rev}, nil
}

func (s *ReaderServer) MarkAllRead(ctx context.Context, req *pb.MarkAllReadRequest) (*pb.MarkAllReadResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	before := req.GetBefore()
	if before == "" {
		before = time.Now().UTC().Format(time.RFC3339Nano)
	}
	sourceID := ""
	if req.GetScope() == pb.ListScope_LIST_SCOPE_FEED {
		sourceID = req.GetSourceId()
	}
	n, batch, err := s.svc.MarkAllRead(ctx, sc, sourceID, before)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.MarkAllReadResponse{Marked: int32(n), UndoToken: batch}, nil
}

func (s *ReaderServer) Subscribe(ctx context.Context, req *pb.SubscribeRequest) (*pb.SubscribeResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	f, existed, description, err := s.svc.Subscribe(ctx, sc, req.GetUrl(), req.GetTitle(), req.GetFolderId())
	if err != nil {
		// A category that is not this user's is NotFound like every other
		// unowned row, and must not be reported as a bad URL — the form would
		// blame the field the reader can see rather than the one that was wrong.
		if errors.Is(err, store.ErrNotFound) {
			return nil, toStatus(err)
		}
		// A bad URL is the user's mistake and they can fix it, so it is
		// InvalidArgument rather than a generic failure.
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	out := &pb.SubscribeResponse{
		Feed: &pb.Feed{
			Id: f.ID, SourceId: f.SourceID, Title: f.Title,
			FeedUrl: f.FeedURL, SiteUrl: f.SiteURL, FolderId: f.FolderID,
			UnreadCount: int32(f.UnreadCount),
		},
		SourceExisted: existed,
	}
	s.suggestCategory(ctx, sc, req.GetFolderId(), f.Title, description, out)
	return out, nil
}

// smartCategorizePref is the per-user opt-in for the model filing a feed into
// a category on add.
//
// The same idiom as smartFollowPref right above it: default off, checked HERE
// at the caller before any Smart+ request is spent, and every failure below —
// pref off, no categorizer wired, no key, a bad reply — resolves to simply
// leaving the response's suggestion fields unset, never an error surfaced to
// the reader. The category names themselves stay off the wire in the request
// unless this is on, which is the point of a per-user key rather than a
// global one: a reader who never turns this on has never had their taxonomy
// read by a model at all.
const smartCategorizePref = "smart.categorize"

// suggestCategory attaches a Smart+ category suggestion to a subscribe
// response, or attaches nothing.
//
// Only when the reader left folder_id empty — a reader who already chose a
// category has already answered the question this exists to ask, and
// suggesting a different one would be the app arguing with a choice just
// made, not helping with one that was skipped.
func (s *ReaderServer) suggestCategory(ctx context.Context, sc store.Scope,
	chosenFolderID, feedTitle, feedDescription string, out *pb.SubscribeResponse) {

	if chosenFolderID != "" || s.categorizer == nil {
		return
	}
	prefs, err := s.svc.GetPrefs(ctx, sc)
	if err != nil || prefs[smartCategorizePref] != "true" {
		return
	}
	folders, err := s.svc.ListFolders(ctx, sc)
	if err != nil {
		return
	}
	names := make([]string, 0, len(folders))
	for _, f := range folders {
		names = append(names, f.Name)
	}
	category, isNew, err := s.categorizer.Suggest(ctx, feedTitle, feedDescription, names)
	if err != nil {
		// Never fails the subscribe that already succeeded — see
		// smartCategorizePref's own comment.
		return
	}
	out.SuggestedCategory = category
	out.SuggestedCategoryIsNew = isNew
}

// ListFolders is the first of the category RPCs.
//
// Naming errors — empty, too long, a duplicate on rename — are InvalidArgument
// rather than Internal: they are the reader's to fix and the message says how,
// so swallowing them into "internal error" would leave a dialog that refuses to
// save and will not say why.
func (s *ReaderServer) ListFolders(ctx context.Context, _ *pb.ListFoldersRequest) (*pb.ListFoldersResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	folders, err := s.svc.ListFolders(ctx, sc)
	if err != nil {
		return nil, toStatus(err)
	}
	out := &pb.ListFoldersResponse{}
	for _, f := range folders {
		out.Folders = append(out.Folders, pbFolder(f))
	}
	return out, nil
}

func (s *ReaderServer) CreateFolder(ctx context.Context, req *pb.CreateFolderRequest) (*pb.CreateFolderResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	f, err := s.svc.CreateFolder(ctx, sc, req.GetName())
	if err != nil {
		return nil, folderStatus(err)
	}
	return &pb.CreateFolderResponse{Folder: pbFolder(f)}, nil
}

func (s *ReaderServer) RenameFolder(ctx context.Context, req *pb.RenameFolderRequest) (*pb.RenameFolderResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	f, err := s.svc.RenameFolder(ctx, sc, req.GetFolderId(), req.GetName())
	if err != nil {
		return nil, folderStatus(err)
	}
	return &pb.RenameFolderResponse{Folder: pbFolder(f)}, nil
}

func (s *ReaderServer) DeleteFolder(ctx context.Context, req *pb.DeleteFolderRequest) (*pb.DeleteFolderResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.svc.DeleteFolder(ctx, sc, req.GetFolderId()); err != nil {
		return nil, toStatus(err)
	}
	return &pb.DeleteFolderResponse{}, nil
}

func (s *ReaderServer) SetFeedFolder(ctx context.Context, req *pb.SetFeedFolderRequest) (*pb.SetFeedFolderResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.svc.SetFeedFolder(ctx, sc, req.GetSourceId(), req.GetFolderId()); err != nil {
		return nil, toStatus(err)
	}
	return &pb.SetFeedFolderResponse{}, nil
}

func pbFolder(f store.Folder) *pb.Folder {
	return &pb.Folder{Id: f.ID, Name: f.Name, Position: int32(f.Position)}
}

// folderStatus keeps the taxonomy's own errors intact and hands everything else
// to the standard mapping, so an unowned folder is still NotFound.
func folderStatus(err error) error {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrNoScope) {
		return toStatus(err)
	}
	return status.Error(codes.InvalidArgument, err.Error())
}

func (s *ReaderServer) Unsubscribe(ctx context.Context, req *pb.UnsubscribeRequest) (*pb.UnsubscribeResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.svc.Unsubscribe(ctx, sc, req.GetSourceId()); err != nil {
		return nil, toStatus(err)
	}
	return &pb.UnsubscribeResponse{}, nil
}

func (s *ReaderServer) Refresh(ctx context.Context, req *pb.RefreshRequest) (*pb.RefreshResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	res, err := s.svc.Refresh(ctx, sc, req.GetSourceIds())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.RefreshResponse{
		SourcesPolled: int32(res.Polled),
		NewItems:      int32(res.NewItems),
		Errors:        res.Errors,
	}, nil
}

func (s *ReaderServer) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	items, snippets, err := s.svc.Search(ctx, sc, req.GetQuery(), req.GetSourceId(), int(req.GetLimit()))
	if err != nil {
		return nil, toStatus(err)
	}
	out := &pb.SearchResponse{
		Snippets: snippets,
		Total:    int32(len(items)),
		Items:    make([]*pb.Item, 0, len(items)),
	}
	for _, it := range items {
		out.Items = append(out.Items, toPBItem(it, false))
	}
	return out, nil
}

// toPBItem converts a domain item. withContent is false for list responses: a
// 50-item page carrying full article bodies is megabytes over a tunnel for text
// nobody has scrolled to yet.
// listRanked serves LIST_SCOPE_MEGAFEED from the materialised homepage.
//
// # The empty page is a real state, not an error
//
// A reader whose interest layer has never run, or who has read everything on it, gets
// zero rows and a nil cursor. That is not a failure and must not be reported as one:
// the client distinguishes "nothing ranked yet" from "the call failed" by the absence
// of an error, and turning an honest empty page into a status code would make a new
// account look broken. §18.4's cold-start state is the client's job to render, and it
// can only do that if it is told the truth.
func (s *ReaderServer) listRanked(ctx context.Context, sc store.Scope,
	req *pb.ListItemsRequest) (*pb.ListItemsResponse, error) {

	// The cursor is the last rank served. Malformed input starts from the top rather
	// than erroring: a bad cursor costs one repeated page, and refusing to render the
	// homepage because a query string was wrong is the worse trade.
	after := 0
	if c := req.GetCursor(); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			after = n
		}
	}

	ranked, items, err := s.svc.ListRanked(ctx, sc, after, int(req.GetLimit()))
	if err != nil {
		return nil, toStatus(err)
	}

	out := &pb.ListItemsResponse{}
	for i, it := range items {
		m := toPBItem(it, false)
		r := ranked[i]
		m.RankSlot = r.Slot
		m.RankTier = r.Tier
		m.RankTopic = r.TopicID
		// Parallel arrays, filled in one pass so they cannot fall out of step. The
		// client indexes one by the other's position, so a term without its prose would
		// silently label the wrong factor.
		m.RankReasons = make([]string, 0, len(r.Reasons))
		m.RankReasonTerms = make([]string, 0, len(r.Reasons))
		for _, why := range r.Reasons {
			m.RankReasons = append(m.RankReasons, why.Text)
			m.RankReasonTerms = append(m.RankReasonTerms, why.Term)
		}
		// The single-line field predates the list and is what the ordinary item row
		// already renders (see client/view/panes.go). Populating it with the STRONGEST
		// reason rather than a join of all of them: the reasons are sorted by absolute
		// contribution, so the first is the one that actually decided the placement,
		// and a concatenation of four clauses is not a sentence anyone reads.
		if len(r.Reasons) > 0 {
			m.RankReason = r.Reasons[0].Text
		}
		out.Items = append(out.Items, m)
	}

	// A next cursor only when the page was filled. The ranking is at most MaxRanked
	// rows, so a short page is the end of it — and offering a cursor past the end would
	// have the client fetch one guaranteed-empty page on every visit.
	if len(ranked) > 0 && len(ranked) == pageLimit(int(req.GetLimit())) {
		out.NextCursor = strconv.Itoa(ranked[len(ranked)-1].Rank)
	}

	// Total is the whole ranked set, not this page, for the same reason ListItems sends
	// it: the scrollbar has to be the size of the result set before the result set has
	// been fetched. Only on the first page.
	if after == 0 {
		if all, _, cerr := s.svc.ListRanked(ctx, sc, 0, store.MaxRankedPage); cerr == nil {
			out.Total = int32(len(all))
		}
	}
	return out, nil
}

// pageLimit mirrors the store's clamp so "was this page full?" is decided by the same
// number that decided how many rows to read. Two copies of that limit would make the
// cursor stop one page early or loop forever.
func pageLimit(requested int) int {
	if requested <= 0 || requested > store.MaxRankedPage {
		return store.MaxRankedPage
	}
	return requested
}

func toPBItem(it store.Item, withContent bool) *pb.Item {
	out := &pb.Item{
		Id: it.ID, SourceId: it.SourceID, SourceTitle: it.SourceTitle,
		Title: it.Title, Author: it.Author, Summary: it.Summary,
		Url: it.URL, PublishedAt: it.PublishedAt,
		Read: it.Read, Starred: it.Starred, Rating: int32(it.Rating),
		WordCount: int32(it.WordCount), ImageUrl: it.ImageURL,
		// Free on every row: two scalars already selected with the article, so a
		// list can render the "edited" badge without a second query (TODO F34).
		Revision: int32(it.Revision), EditedAt: it.EditedAt,
	}
	if withContent {
		out.ContentHtml = it.ContentHTML
	}
	return out
}

// itemIDs collects a page's ids for the batched CategoriesFor call. A small
// helper rather than an inline loop at each of ListItems' two call sites
// (the plain list and would-be future ones), so the "one call per page" shape
// stays in one place.
func itemIDs(items []store.Item) []string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return ids
}

// withCategory fills a converted item's category fields from a resolved
// lookup and returns it, so a call site can wrap toPBItem in place.
//
// Kept separate from toPBItem itself: toPBItem takes one item and must not
// query anything (see its own comment), while the category lookup is scoped,
// batched, and belongs beside whatever list it is filling in.
func withCategory(out *pb.Item, cat store.ItemCategory) *pb.Item {
	out.Category = cat.Primary
	out.SecondaryCategories = cat.Secondary
	out.CategoryReason = cat.Reason
	out.Genre = cat.Genre
	out.CategoryByModel = cat.ByModel
	return out
}

func (s *ReaderServer) GetPrefs(ctx context.Context, _ *pb.GetPrefsRequest) (*pb.GetPrefsResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	p, err := s.svc.GetPrefs(ctx, sc)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetPrefsResponse{Prefs: p}, nil
}

func (s *ReaderServer) SetPrefs(ctx context.Context, req *pb.SetPrefsRequest) (*pb.SetPrefsResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.svc.SetPrefs(ctx, sc, req.GetPrefs()); err != nil {
		// A rejected preference is the client's mistake to fix (too many keys, a
		// value that is too long), so it is InvalidArgument rather than Internal.
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &pb.SetPrefsResponse{}, nil
}

func (s *ReaderServer) ListTags(ctx context.Context, _ *pb.ListTagsRequest) (*pb.ListTagsResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	tags, bySource, err := s.svc.ListTags(ctx, sc)
	if err != nil {
		return nil, toStatus(err)
	}
	out := &pb.ListTagsResponse{
		BySource: make(map[string]*pb.TagIDs, len(bySource)),
		Tags:     make([]*pb.Tag, 0, len(tags)),
	}
	for _, t := range tags {
		out.Tags = append(out.Tags, toPBTag(t))
	}
	for src, ids := range bySource {
		out.BySource[src] = &pb.TagIDs{Ids: ids}
	}
	return out, nil
}

func (s *ReaderServer) SetFeedTag(ctx context.Context, req *pb.SetFeedTagRequest) (*pb.SetFeedTagResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	t, err := s.svc.SetFeedTag(ctx, sc, req.GetSourceId(), req.GetName(), req.GetOn())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, toStatus(err)
		}
		// A too-long or empty name is the caller's to fix.
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &pb.SetFeedTagResponse{Tag: toPBTag(t)}, nil
}

// toPBTag is the one place a store.Tag becomes a wire Tag. It exists because
// three call sites were each spelling out the same four fields, and the fourth —
// UpdateTag — is the one that would have been written without the new ones.
func toPBTag(t store.Tag) *pb.Tag {
	return &pb.Tag{
		Id: t.ID, Name: t.Name, FeedCount: int32(t.Feeds),
		Label: t.Label, Glyph: t.Glyph,
	}
}

func (s *ReaderServer) UpdateTag(ctx context.Context, req *pb.UpdateTagRequest) (*pb.UpdateTagResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	// Unset means "leave it alone"; a present empty string means "clear it".
	// Copied into locals rather than aliasing req, so the patch outlives the
	// request without holding it.
	var p store.TagPatch
	if req.Label != nil {
		v := req.GetLabel()
		p.Label = &v
	}
	if req.Glyph != nil {
		v := req.GetGlyph()
		p.Glyph = &v
	}
	t, err := s.svc.UpdateTag(ctx, sc, req.GetTagId(), p)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, toStatus(err)
		}
		// A label over the limit or a glyph outside the catalogue is the
		// caller's to fix, not this server's to swallow.
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &pb.UpdateTagResponse{Tag: toPBTag(t)}, nil
}

func (s *ReaderServer) SetNote(ctx context.Context, req *pb.SetNoteRequest) (*pb.SetNoteResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.svc.SetNote(ctx, sc, req.GetItemId(), req.GetBody()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, toStatus(err)
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &pb.SetNoteResponse{}, nil
}

func (s *ReaderServer) ListNotes(ctx context.Context, req *pb.ListNotesRequest) (*pb.ListNotesResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	items, notes, err := s.svc.NotedItems(ctx, sc, int(req.GetLimit()))
	if err != nil {
		return nil, toStatus(err)
	}
	out := &pb.ListNotesResponse{}
	for i, it := range items {
		pi := toPBItem(it, false)
		pi.Note = notes[i]
		out.Items = append(out.Items, pi)
	}
	return out, nil
}

func (s *ReaderServer) GetFeedSettings(ctx context.Context, req *pb.GetFeedSettingsRequest) (*pb.GetFeedSettingsResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	f, err := s.svc.GetFeedSettings(ctx, sc, req.GetSourceId())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetFeedSettingsResponse{Settings: toPBFeedSettings(f)}, nil
}

func (s *ReaderServer) UpdateFeedSettings(ctx context.Context, req *pb.UpdateFeedSettingsRequest) (*pb.UpdateFeedSettingsResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	// Optional means unset means "leave it alone". Copying into locals rather
	// than taking the address of req.Field keeps the patch independent of the
	// request's lifetime.
	var p store.FeedSettingsPatch
	if req.Title != nil {
		v := req.GetTitle()
		p.Title = &v
	}
	if req.InMegafeed != nil {
		v := req.GetInMegafeed()
		p.InMegafeed = &v
	}
	if req.MutedUntil != nil {
		v := req.GetMutedUntil()
		p.MutedUntil = &v
	}
	if req.CacheDepth != nil {
		v := int(req.GetCacheDepth())
		p.CacheDepth = &v
	}
	if req.FetchIntervalS != nil {
		v := int(req.GetFetchIntervalS())
		p.FetchIntervalS = &v
	}
	f, err := s.svc.UpdateFeedSettings(ctx, sc, req.GetSourceId(), p)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpdateFeedSettingsResponse{Settings: toPBFeedSettings(f)}, nil
}

func toPBFeedSettings(f store.FeedSettings) *pb.FeedSettings {
	return &pb.FeedSettings{
		SourceId: f.SourceID,
		Title:    f.Title, ResolvedTitle: f.ResolvedTitle,
		InMegafeed: f.InMegafeed, MutedUntil: f.MutedUntil,
		CacheDepth:     int32(f.CacheDepth),
		FeedUrl:        f.FeedURL,
		SiteUrl:        f.SiteURL,
		Kind:           f.Kind,
		FetchIntervalS: int32(f.FetchIntervalS),
		NextFetchAt:    f.NextFetchAt,
		LastFetchAt:    f.LastFetchAt, LastSuccessAt: f.LastSuccessAt,
		LastError: f.LastError, ConsecutiveFailures: int32(f.Failures),
		ItemCount: int32(f.ItemCount), UnreadCount: int32(f.UnreadCount),
		SubscriberCount: int32(f.SubscriberCount),
	}
}

// RecordEngagements appends observations to the signals log (§18.1).
//
// Thin, like the rest of this file: strings cross the wire and internal/signals
// owns what they mean. The kind is NOT translated through a switch here — a
// transport that knew the taxonomy would be a second place that has to be
// updated for every new signal, and the one that got forgotten would silently
// drop events.
func (s *ReaderServer) RecordEngagements(ctx context.Context, req *pb.RecordEngagementsRequest) (*pb.RecordEngagementsResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	evs := make([]signals.Event, 0, len(req.GetEvents()))
	for _, e := range req.GetEvents() {
		evs = append(evs, signals.Event{
			ID:        e.GetId(),
			ItemID:    e.GetItemId(),
			SourceID:  e.GetSourceId(),
			Kind:      signals.Kind(e.GetKind()),
			Value:     e.GetValue(),
			Surface:   signals.Surface(e.GetSurface()),
			Context:   e.GetContext(),
			SessionID: e.GetSessionId(),
			At:        e.GetAt(),
		})
	}
	accepted, rejected, err := s.svc.RecordEngagements(ctx, sc, evs)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.RecordEngagementsResponse{
		Accepted: int32(accepted), Rejected: int32(rejected),
	}, nil
}

// UndoMarkAllRead reverses a bulk mark, using the token the mark returned.
func (s *ReaderServer) UndoMarkAllRead(ctx context.Context, req *pb.UndoMarkAllReadRequest) (*pb.UndoMarkAllReadResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	n, err := s.svc.UndoMarkAllRead(ctx, sc, req.GetUndoToken())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UndoMarkAllReadResponse{Restored: int32(n)}, nil
}

// evidenceText unwraps recommendjob's stored evidence — a JSON array holding
// the one rendered sentence recommend.describe built (see recommendjob.go's
// comment on why it is stored as JSON but is prose, not structure) — into
// what the client actually displays.
func evidenceText(raw string) string {
	var parts []string
	if err := json.Unmarshal([]byte(raw), &parts); err != nil || len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// ListRecommendations returns the reader's open suggestions (§18.7, M16).
func (s *ReaderServer) ListRecommendations(ctx context.Context, _ *pb.ListRecommendationsRequest) (*pb.ListRecommendationsResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	if s.repo == nil {
		return &pb.ListRecommendationsResponse{}, nil
	}
	recs, err := s.repo.Recommendations(ctx, sc, 0)
	if err != nil {
		return nil, toStatus(err)
	}
	out := &pb.ListRecommendationsResponse{
		Recommendations: make([]*pb.Recommendation, 0, len(recs)),
	}
	for _, r := range recs {
		out.Recommendations = append(out.Recommendations, &pb.Recommendation{
			Domain: r.Domain, FeedUrl: r.FeedURL, Title: r.Title,
			Score: r.Score, Rung: int32(r.Rung), Evidence: evidenceText(r.Evidence),
		})
	}
	return out, nil
}

// RefreshRecommendations enqueues a fresh scoring pass. Async, matching
// Refresh's own shape for a feed poll: the client re-lists rather than
// blocking one request on a batch of validating fetches to other people's
// servers.
func (s *ReaderServer) RefreshRecommendations(ctx context.Context, _ *pb.RefreshRecommendationsRequest) (*pb.RefreshRecommendationsResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	if s.recommender == nil {
		return nil, status.Error(codes.Unavailable, "recommendations are not enabled on this instance")
	}
	if err := s.recommender.Enqueue(ctx, sc); err != nil {
		return nil, toStatus(err)
	}
	return &pb.RefreshRecommendationsResponse{}, nil
}

// AcceptRecommendation subscribes to the suggestion's already-validated feed
// URL and marks it accepted so it leaves the open list without being read as
// a dismissal.
func (s *ReaderServer) AcceptRecommendation(ctx context.Context, req *pb.AcceptRecommendationRequest) (*pb.AcceptRecommendationResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	if s.repo == nil {
		return nil, status.Error(codes.Unavailable, "recommendations are not enabled on this instance")
	}
	domain := req.GetDomain()
	if domain == "" {
		return nil, status.Error(codes.InvalidArgument, "domain is required")
	}
	rec, ok, err := s.repo.RecommendationByDomain(ctx, sc, domain)
	if err != nil {
		return nil, toStatus(err)
	}
	if !ok || rec.FeedURL == "" {
		return nil, status.Error(codes.NotFound, "no open recommendation for that domain")
	}

	f, _, _, err := s.svc.Subscribe(ctx, sc, rec.FeedURL, rec.Title, "")
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.repo.AcceptRecommendation(ctx, sc, domain); err != nil {
		return nil, toStatus(err)
	}
	return &pb.AcceptRecommendationResponse{
		Feed: &pb.Feed{
			Id: f.ID, SourceId: f.SourceID, Title: f.Title,
			FeedUrl: f.FeedURL, SiteUrl: f.SiteURL, FolderId: f.FolderID,
			UnreadCount: int32(f.UnreadCount),
		},
	}, nil
}

// RejectRecommendation is permanent (§18.7): the domain is never suggested
// again for this reader.
func (s *ReaderServer) RejectRecommendation(ctx context.Context, req *pb.RejectRecommendationRequest) (*pb.RejectRecommendationResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	if s.repo == nil {
		return nil, status.Error(codes.Unavailable, "recommendations are not enabled on this instance")
	}
	domain := req.GetDomain()
	if domain == "" {
		return nil, status.Error(codes.InvalidArgument, "domain is required")
	}
	if err := s.repo.DismissRecommendation(ctx, sc, domain); err != nil {
		return nil, toStatus(err)
	}
	return &pb.RejectRecommendationResponse{}, nil
}
