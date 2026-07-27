// Package grpcsrv adapts the service layer to gRPC.
//
// It is deliberately thin: translation between proto messages and domain types,
// plus the §20.7 error taxonomy. Any logic that appears here is logic the REST
// sync API and the offline-pack channel will not have, which is exactly the
// divergence the single service layer exists to prevent.
package grpcsrv

import (
	"context"
	"errors"
	"net/url"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/rewrite"
	"github.com/monstercameron/ArticleFlux/internal/signals"
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

// toStatus maps a domain error to the §20.7 taxonomy.
//
// The rule that matters: a row belonging to another tenant returns NotFound, not
// PermissionDenied. PermissionDenied on item 4711 confirms item 4711 exists,
// which is a tenant-isolation leak dressed as good manners.
func toStatus(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return errKey(codes.NotFound, "srv.notFound", "not found", nil)
	case errors.Is(err, store.ErrNoScope):
		return errKey(codes.Unauthenticated, "srv.notAuthenticated", "not authenticated", nil)
	default:
		// The message is internal and stays internal (§22.11): the client gets a
		// safe string, the log gets the detail with a request id.
		return errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
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
	out := &pb.ListFeedsResponse{TotalUnread: int32(total)}
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
	out := &pb.ListItemsResponse{NextCursor: next}
	for _, it := range items {
		out.Items = append(out.Items, toPBItem(it, false))
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
	// The note rides along with the article rather than needing its own round
	// trip: you open an item to read it, and your own note is part of that.
	if note, nerr := s.svc.GetNote(ctx, sc, req.GetId()); nerr == nil {
		out.Note = note
	}
	out.ContentHtml = s.proxyImages(ctx, sc, it, out.GetContentHtml())
	if s.mintPage != nil && it.URL != "" {
		out.ProxyUrl = s.mintPage(it.URL)
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
	f, existed, err := s.svc.Subscribe(ctx, sc, req.GetUrl(), req.GetTitle(), req.GetFolderId())
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
	return &pb.SubscribeResponse{
		Feed: &pb.Feed{
			Id: f.ID, SourceId: f.SourceID, Title: f.Title,
			FeedUrl: f.FeedURL, SiteUrl: f.SiteURL, FolderId: f.FolderID,
			UnreadCount: int32(f.UnreadCount),
		},
		SourceExisted: existed,
	}, nil
}

// The category RPCs.
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
	out := &pb.SearchResponse{Snippets: snippets, Total: int32(len(items))}
	for _, it := range items {
		out.Items = append(out.Items, toPBItem(it, false))
	}
	return out, nil
}

// toPBItem converts a domain item. withContent is false for list responses: a
// 50-item page carrying full article bodies is megabytes over a tunnel for text
// nobody has scrolled to yet.
func toPBItem(it store.Item, withContent bool) *pb.Item {
	out := &pb.Item{
		Id: it.ID, SourceId: it.SourceID, SourceTitle: it.SourceTitle,
		Title: it.Title, Author: it.Author, Summary: it.Summary,
		Url: it.URL, PublishedAt: it.PublishedAt,
		Read: it.Read, Starred: it.Starred, Rating: int32(it.Rating),
		WordCount: int32(it.WordCount), ImageUrl: it.ImageURL,
	}
	if withContent {
		out.ContentHtml = it.ContentHTML
	}
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
	out := &pb.ListTagsResponse{BySource: map[string]*pb.TagIDs{}}
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
