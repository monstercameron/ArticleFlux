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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/Tidings/internal/pb/tidings/v1"
	"github.com/monstercameron/Tidings/internal/reader"
	"github.com/monstercameron/Tidings/internal/store"
)

// ReaderServer implements pb.ReaderServiceServer.
type ReaderServer struct {
	pb.UnimplementedReaderServiceServer
	svc *reader.Service
	// scopeOf resolves the caller. Injected rather than hard-wired so the auth
	// interceptor owns identity and this file stays about translation.
	scopeOf func(context.Context) (store.Scope, error)
}

// NewReaderServer wires a service to gRPC.
func NewReaderServer(svc *reader.Service, scopeOf func(context.Context) (store.Scope, error)) *ReaderServer {
	return &ReaderServer{svc: svc, scopeOf: scopeOf}
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
		return status.Error(codes.NotFound, "not found")
	case errors.Is(err, store.ErrNoScope):
		return status.Error(codes.Unauthenticated, "not authenticated")
	default:
		// The message is internal and stays internal (§22.11): the client gets a
		// safe string, the log gets the detail with a request id.
		return status.Error(codes.Internal, "internal error")
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
	return &pb.GetItemResponse{Item: out}, nil
}

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
	n, err := s.svc.MarkAllRead(ctx, sc, sourceID, before)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.MarkAllReadResponse{Marked: int32(n)}, nil
}

func (s *ReaderServer) Subscribe(ctx context.Context, req *pb.SubscribeRequest) (*pb.SubscribeResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	f, existed, err := s.svc.Subscribe(ctx, sc, req.GetUrl(), req.GetTitle())
	if err != nil {
		// A bad URL is the user's mistake and they can fix it, so it is
		// InvalidArgument rather than a generic failure.
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &pb.SubscribeResponse{
		Feed: &pb.Feed{
			Id: f.ID, SourceId: f.SourceID, Title: f.Title,
			FeedUrl: f.FeedURL, SiteUrl: f.SiteURL,
			UnreadCount: int32(f.UnreadCount),
		},
		SourceExisted: existed,
	}, nil
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
		out.Tags = append(out.Tags, &pb.Tag{Id: t.ID, Name: t.Name, FeedCount: int32(t.Feeds)})
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
	return &pb.SetFeedTagResponse{
		Tag: &pb.Tag{Id: t.ID, Name: t.Name, FeedCount: int32(t.Feeds)},
	}, nil
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
