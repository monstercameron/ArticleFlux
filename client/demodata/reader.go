package demodata

import (
	"context"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// readerService is the demo's ReaderService.
//
// It implements the generated server interface rather than something shaped
// like it, so the day a method is added to the proto this file stops compiling.
// That is the whole reason the demo routes through gRPC: a fake that drifts is
// worse than no fake, because it demonstrates an application that no longer
// exists.
type readerService struct {
	pb.UnimplementedReaderServiceServer
	inst *Instance
}

// defaultLimit and maxLimit mirror the server's own (§20.7, ListItemsRequest).
// A demo that paged differently would make the virtualised list behave
// differently, and the list is the part most worth looking at.
const (
	defaultLimit = 50
	maxLimit     = 200
)

func (s *readerService) ListFeeds(_ context.Context, _ *pb.ListFeedsRequest) (*pb.ListFeedsResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	res := &pb.ListFeedsResponse{Feeds: make([]*pb.Feed, 0, len(in.feeds))}
	for _, f := range in.feeds {
		n := in.unreadFor(f.sourceID)
		res.TotalUnread += n
		res.Feeds = append(res.Feeds, f.message(n))
	}
	return res, nil
}

func (s *readerService) ListItems(_ context.Context, req *pb.ListItemsRequest) (*pb.ListItemsResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	matched := in.selectItems(req)

	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	// The cursor is an offset, and it is an offset for a reason that does not
	// generalise: the server's is a keyset because OFFSET degrades exactly when
	// a list gets long enough for paging to matter, and this list is forty
	// items held in a slice. What matters for the demo is that the cursor is
	// OPAQUE — the client must not be able to tell the difference — and it is.
	start := 0
	if cur := req.GetCursor(); cur != "" {
		n, err := strconv.Atoi(cur)
		if err != nil || n < 0 {
			return nil, status.Error(codes.InvalidArgument, "that cursor did not come from here")
		}
		start = n
	}
	start = min(start, len(matched))
	end := min(start+limit, len(matched))

	res := &pb.ListItemsResponse{Items: make([]*pb.Item, 0, end-start)}
	ranked := req.GetScope() == pb.ListScope_LIST_SCOPE_MEGAFEED
	for _, it := range matched[start:end] {
		m := it.message(false)
		if ranked {
			m.RankReason = it.rankReason(in.clock())
		}
		res.Items = append(res.Items, m)
	}
	if end < len(matched) {
		res.NextCursor = strconv.Itoa(end)
	}
	// First page only. It does not change while paging, and a count on every
	// page is work nobody reads.
	if req.GetCursor() == "" {
		res.Total = int32(len(matched))
	}
	return res, nil
}

// selectItems applies a list request's scope and filters. Caller holds the lock.
func (in *Instance) selectItems(req *pb.ListItemsRequest) []*item {
	sources := map[string]bool{}
	for _, id := range req.GetSourceIds() {
		sources[id] = true
	}
	if id := req.GetSourceId(); id != "" {
		sources[id] = true
	}

	out := make([]*item, 0, len(in.items))
	for _, it := range in.items {
		if req.GetUnreadOnly() && it.read {
			continue
		}
		switch req.GetScope() {
		case pb.ListScope_LIST_SCOPE_FEED:
			if !sources[it.feed.sourceID] {
				continue
			}
		case pb.ListScope_LIST_SCOPE_STARRED:
			if !it.starred {
				continue
			}
		case pb.ListScope_LIST_SCOPE_LIKED:
			if it.rating <= 0 {
				continue
			}
		case pb.ListScope_LIST_SCOPE_DISLIKED:
			if it.rating >= 0 {
				continue
			}
		case pb.ListScope_LIST_SCOPE_MEGAFEED:
			if !it.feed.inMegafeed {
				continue
			}
		default:
			// ALL and UNSPECIFIED. A tag row sends ALL with source_ids set, so
			// the filter applies here too rather than only under FEED.
			if len(sources) > 0 && !sources[it.feed.sourceID] {
				continue
			}
		}
		out = append(out, it)
	}

	if req.GetScope() == pb.ListScope_LIST_SCOPE_MEGAFEED {
		in.rankMegafeed(out)
	}
	return out
}

// rankMegafeed orders the ranked homepage in place.
//
// The score is the item's own interest weight decayed by age, which is the
// shape of the real thing (§18) without any of its machinery. What matters for
// a demo is that the ranked list is visibly NOT the chronological one and that
// every row can say why it is where it is.
func (in *Instance) rankMegafeed(items []*item) {
	now := in.clock()
	score := func(it *item) float64 {
		hours := now.Sub(it.published).Hours()
		if hours < 0 {
			hours = 0
		}
		s := it.weight - hours/24
		if it.starred {
			s += 2
		}
		s += float64(it.rating)
		return s
	}
	// A stable sort, so two items with the same score keep their chronological
	// order rather than shuffling between requests — a list that reorders under
	// a reader who changed nothing looks broken.
	sortStable(items, func(a, b *item) bool { return score(a) > score(b) })
}

func (s *readerService) GetItem(_ context.Context, req *pb.GetItemRequest) (*pb.GetItemResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	it := in.itemByID(req.GetId())
	if it == nil {
		return nil, status.Error(codes.NotFound, "no such article")
	}
	// proxy_url is deliberately empty. The page proxy fetches the publisher's
	// own page through the server, and the demo has no server and no publisher
	// — an address that 404s would be worse than the reading view the article
	// already has.
	return &pb.GetItemResponse{Item: it.message(true)}, nil
}

func (s *readerService) SetItemState(_ context.Context, req *pb.SetItemStateRequest) (*pb.SetItemStateResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	it := in.itemByID(req.GetItemId())
	if it == nil {
		return nil, status.Error(codes.NotFound, "no such article")
	}
	// Replays change nothing and are not errors. The outbox retries writes it
	// could not confirm, so this path is reached in ordinary use every time a
	// connection drops mid-write.
	if key := req.GetIdempotencyKey(); key != "" && in.applied[key] {
		return &pb.SetItemStateResponse{Item: it.message(false), Rev: in.rev}, nil
	}
	if req.Read != nil {
		it.read = req.GetRead()
		if it.read {
			it.readAt = stamp(in.clock())
		} else {
			it.readAt = ""
		}
	}
	if req.Starred != nil {
		it.starred = req.GetStarred()
	}
	if req.Rating != nil {
		r := req.GetRating()
		// Clamped rather than rejected, exactly as the proto says the server
		// does: a client sending 7 has a bug, and refusing the write would turn
		// it into a stuck row.
		switch {
		case r > 1:
			r = 1
		case r < -1:
			r = -1
		}
		it.rating = r
	}
	if key := req.GetIdempotencyKey(); key != "" {
		in.applied[key] = true
	}
	in.rev++
	return &pb.SetItemStateResponse{Item: it.message(false), Rev: in.rev}, nil
}

func (s *readerService) MarkAllRead(_ context.Context, req *pb.MarkAllReadRequest) (*pb.MarkAllReadResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	before := in.clock()
	if v := req.GetBefore(); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			before = t
		}
	}
	at := stamp(in.clock())
	token := in.nextID("undo")
	var marked []string
	for _, it := range in.items {
		if it.read || it.published.After(before) {
			continue
		}
		if req.GetScope() == pb.ListScope_LIST_SCOPE_FEED && it.feed.sourceID != req.GetSourceId() {
			continue
		}
		it.read = true
		it.readAt = at
		marked = append(marked, it.id)
	}
	// The token identifies exactly the rows THIS call marked. Rows that were
	// already read keep their earlier stamp and are not in the batch, so an undo
	// cannot resurrect something read last week.
	in.undo[token] = marked
	in.rev++
	in.logf("INFO", "marked read", "count", strconv.Itoa(len(marked)), "undo", token)
	return &pb.MarkAllReadResponse{Marked: int32(len(marked)), UndoToken: token}, nil
}

func (s *readerService) UndoMarkAllRead(_ context.Context, req *pb.UndoMarkAllReadRequest) (*pb.UndoMarkAllReadResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	ids, ok := in.undo[req.GetUndoToken()]
	if !ok {
		return nil, status.Error(codes.NotFound, "that undo has already been used")
	}
	delete(in.undo, req.GetUndoToken())
	var n int32
	for _, id := range ids {
		if it := in.itemByID(id); it != nil {
			it.read = false
			it.readAt = ""
			n++
		}
	}
	in.rev++
	return &pb.UndoMarkAllReadResponse{Restored: n}, nil
}

func (s *readerService) Subscribe(_ context.Context, req *pb.SubscribeRequest) (*pb.SubscribeResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	url := strings.TrimSpace(req.GetUrl())
	if url == "" {
		return nil, status.Error(codes.InvalidArgument, "a feed needs an address")
	}
	if !strings.Contains(url, ".") {
		// The server validates by FETCHING, which is the one thing the demo
		// cannot do. Refusing something that is not an address at all is the
		// honest half of that check rather than a pretence at the whole of it.
		return nil, status.Error(codes.InvalidArgument, "that does not look like an address")
	}
	if f := in.feedByURL(url); f != nil {
		return &pb.SubscribeResponse{
			Feed: f.message(in.unreadFor(f.sourceID)), SourceExisted: true,
		}, nil
	}
	f := in.addFeed(url, req.GetTitle(), req.GetFolderId(), "feed")
	in.stockItems(f, 3)
	in.resort()
	return &pb.SubscribeResponse{Feed: f.message(in.unreadFor(f.sourceID))}, nil
}

func (s *readerService) Unsubscribe(_ context.Context, req *pb.UnsubscribeRequest) (*pb.UnsubscribeResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	f := in.feedByID(req.GetSourceId())
	if f == nil {
		return nil, status.Error(codes.NotFound, "no such subscription")
	}
	feeds := in.feeds[:0]
	for _, x := range in.feeds {
		if x != f {
			feeds = append(feeds, x)
		}
	}
	in.feeds = feeds
	items := in.items[:0]
	for _, it := range in.items {
		if it.feed != f {
			items = append(items, it)
		}
	}
	in.items = items
	// The tags lose the feed too, which is what makes a tag disappear when its
	// last feed does — tags are created by use and retired the same way.
	for _, t := range in.tags {
		delete(t.sources, f.sourceID)
	}
	in.dropEmptyTags()
	return &pb.UnsubscribeResponse{}, nil
}

func (s *readerService) Refresh(_ context.Context, req *pb.RefreshRequest) (*pb.RefreshResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	now := in.clock()
	polled := int32(len(in.feeds))
	if ids := req.GetSourceIds(); len(ids) > 0 {
		polled = int32(len(ids))
	}
	for _, f := range in.feeds {
		f.lastFetch = now
		if f.failures == 0 {
			f.lastSuccess = now
		}
	}
	in.polls++

	// Up to three held-back articles per press, stamped with the moment they
	// arrived. When the reserve runs out the refresh still succeeds and reports
	// nothing new, which is what most refreshes of a real reader do.
	var fresh int32
	for fresh < 3 && len(in.incoming) > 0 {
		it := in.incoming[0]
		in.incoming = in.incoming[1:]
		it.published = now.Add(-time.Duration(fresh) * time.Minute)
		in.items = append(in.items, it)
		fresh++
	}
	in.resort()
	in.logf("INFO", "poll finished",
		"sources", strconv.Itoa(int(polled)), "new", strconv.Itoa(int(fresh)))
	return &pb.RefreshResponse{SourcesPolled: polled, NewItems: fresh}, nil
}

func (s *readerService) Search(_ context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	q := strings.TrimSpace(strings.ToLower(req.GetQuery()))
	if q == "" {
		return &pb.SearchResponse{}, nil
	}
	limit := int(req.GetLimit())
	if limit <= 0 || limit > maxLimit {
		limit = defaultLimit
	}
	res := &pb.SearchResponse{}
	for _, it := range in.items {
		if req.GetSourceId() != "" && it.feed.sourceID != req.GetSourceId() {
			continue
		}
		hay := strings.ToLower(it.title + " " + it.summary + " " + it.author + " " + it.body)
		if !strings.Contains(hay, q) {
			continue
		}
		res.Total++
		if len(res.Items) >= limit {
			continue
		}
		res.Items = append(res.Items, it.message(false))
		res.Snippets = append(res.Snippets, snippet(it.summary, q))
	}
	return res, nil
}

// snippet is the bit of text around a match.
//
// Plain text, no markup: the client does not render these yet (§20.7 keeps them
// index-aligned for when it does), and emitting HTML into a field nobody
// sanitises would be a bad habit to demonstrate.
func snippet(body, q string) string {
	i := strings.Index(strings.ToLower(body), q)
	if i < 0 {
		if len(body) > 160 {
			return body[:157] + "…"
		}
		return body
	}
	start := max(i-60, 0)
	end := min(i+len(q)+100, len(body))
	out := body[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(body) {
		out += "…"
	}
	return out
}

func (s *readerService) GetPrefs(_ context.Context, _ *pb.GetPrefsRequest) (*pb.GetPrefsResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	out := make(map[string]string, len(in.prefs))
	maps.Copy(out, in.prefs)
	return &pb.GetPrefsResponse{Prefs: out}, nil
}

func (s *readerService) SetPrefs(_ context.Context, req *pb.SetPrefsRequest) (*pb.SetPrefsResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	// A merge, not a replace: a client that only knows about half these keys
	// must not erase the other half by omitting them.
	maps.Copy(in.prefs, req.GetPrefs())
	return &pb.SetPrefsResponse{}, nil
}

func (s *readerService) ListTags(_ context.Context, _ *pb.ListTagsRequest) (*pb.ListTagsResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	res := &pb.ListTagsResponse{
		Tags:     make([]*pb.Tag, 0, len(in.tags)),
		BySource: map[string]*pb.TagIDs{},
	}
	for _, t := range in.tags {
		res.Tags = append(res.Tags, t.message())
		for src := range t.sources {
			ids := res.BySource[src]
			if ids == nil {
				ids = &pb.TagIDs{}
				res.BySource[src] = ids
			}
			ids.Ids = append(ids.Ids, t.id)
		}
	}
	return res, nil
}

func (s *readerService) SetFeedTag(_ context.Context, req *pb.SetFeedTagRequest) (*pb.SetFeedTagResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "a tag needs a name")
	}
	if in.feedByID(req.GetSourceId()) == nil {
		return nil, status.Error(codes.NotFound, "no such subscription")
	}
	t := in.tagByName(name)
	if t == nil {
		if !req.GetOn() {
			return nil, status.Error(codes.NotFound, "no such tag")
		}
		// Created by use. Nobody wants to manage a taxonomy; they want to label
		// the thing in front of them, and the tag list is the consequence.
		t = &tag{id: in.nextID("tag"), name: name, sources: map[string]bool{}}
		in.tags = append(in.tags, t)
	}
	if req.GetOn() {
		t.sources[req.GetSourceId()] = true
	} else {
		delete(t.sources, req.GetSourceId())
	}
	m := t.message()
	in.dropEmptyTags()
	return &pb.SetFeedTagResponse{Tag: m}, nil
}

func (s *readerService) UpdateTag(_ context.Context, req *pb.UpdateTagRequest) (*pb.UpdateTagResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	for _, t := range in.tags {
		if t.id != req.GetTagId() {
			continue
		}
		// Unset means "leave it alone"; an empty string means "clear it". That
		// distinction is the whole reason these fields are optional, and a demo
		// that collapsed the two would make the panel's clear button silent.
		if req.Label != nil {
			t.label = req.GetLabel()
		}
		if req.Glyph != nil {
			t.glyph = req.GetGlyph()
		}
		return &pb.UpdateTagResponse{Tag: t.message()}, nil
	}
	return nil, status.Error(codes.NotFound, "no such tag")
}

// dropEmptyTags retires a tag whose last feed went away. Caller holds the lock.
func (in *Instance) dropEmptyTags() {
	kept := in.tags[:0]
	for _, t := range in.tags {
		if len(t.sources) > 0 {
			kept = append(kept, t)
		}
	}
	in.tags = kept
}

func (s *readerService) ListFolders(_ context.Context, _ *pb.ListFoldersRequest) (*pb.ListFoldersResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	res := &pb.ListFoldersResponse{Folders: make([]*pb.Folder, 0, len(in.folders))}
	for _, f := range in.folders {
		res.Folders = append(res.Folders, f.message())
	}
	return res, nil
}

func (s *readerService) CreateFolder(_ context.Context, req *pb.CreateFolderRequest) (*pb.CreateFolderResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "a category needs a name")
	}
	// Matched case-insensitively, and an existing one is RETURNED rather than
	// refused: this is called from the add-a-feed form, where "Tech" and "tech"
	// are one intent and an error is a dead end halfway through a task.
	for _, f := range in.folders {
		if strings.EqualFold(f.name, name) {
			return &pb.CreateFolderResponse{Folder: f.message()}, nil
		}
	}
	f := &folder{id: in.nextID("fold"), name: name, position: int32(len(in.folders))}
	in.folders = append(in.folders, f)
	return &pb.CreateFolderResponse{Folder: f.message()}, nil
}

func (s *readerService) RenameFolder(_ context.Context, req *pb.RenameFolderRequest) (*pb.RenameFolderResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	f := in.folderByID(req.GetFolderId())
	if f == nil {
		return nil, status.Error(codes.NotFound, "no such category")
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "a category needs a name")
	}
	f.name = name
	return &pb.RenameFolderResponse{Folder: f.message()}, nil
}

func (s *readerService) DeleteFolder(_ context.Context, req *pb.DeleteFolderRequest) (*pb.DeleteFolderResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	f := in.folderByID(req.GetFolderId())
	if f == nil {
		return nil, status.Error(codes.NotFound, "no such category")
	}
	kept := in.folders[:0]
	for _, x := range in.folders {
		if x != f {
			kept = append(kept, x)
		}
	}
	in.folders = kept
	// The feeds in it are unfiled, never unsubscribed. Deleting a shelf is not
	// deleting the books.
	for _, x := range in.feeds {
		if x.folderID == f.id {
			x.folderID = ""
		}
	}
	return &pb.DeleteFolderResponse{}, nil
}

func (s *readerService) SetFeedFolder(_ context.Context, req *pb.SetFeedFolderRequest) (*pb.SetFeedFolderResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	f := in.feedByID(req.GetSourceId())
	if f == nil {
		return nil, status.Error(codes.NotFound, "no such subscription")
	}
	if id := req.GetFolderId(); id != "" && in.folderByID(id) == nil {
		return nil, status.Error(codes.NotFound, "no such category")
	}
	f.folderID = req.GetFolderId()
	return &pb.SetFeedFolderResponse{}, nil
}

func (s *readerService) SetNote(_ context.Context, req *pb.SetNoteRequest) (*pb.SetNoteResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	it := in.itemByID(req.GetItemId())
	if it == nil {
		return nil, status.Error(codes.NotFound, "no such article")
	}
	// An empty body deletes it, so "has a note" stays an existence check.
	it.note = req.GetBody()
	it.noteAt = in.clock()
	return &pb.SetNoteResponse{}, nil
}

func (s *readerService) ListNotes(_ context.Context, req *pb.ListNotesRequest) (*pb.ListNotesResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	limit := int(req.GetLimit())
	if limit <= 0 || limit > maxLimit {
		limit = 100
	}
	var noted []*item
	for _, it := range in.items {
		if it.note != "" {
			noted = append(noted, it)
		}
	}
	// Most recently WRITTEN first, not most recently published: this is a list
	// of your own writing, found by when you wrote it.
	sortStable(noted, func(a, b *item) bool { return a.noteAt.After(b.noteAt) })
	res := &pb.ListNotesResponse{}
	for _, it := range noted {
		if len(res.Items) >= limit {
			break
		}
		res.Items = append(res.Items, it.message(true))
	}
	return res, nil
}

func (s *readerService) GetFeedSettings(_ context.Context, req *pb.GetFeedSettingsRequest) (*pb.GetFeedSettingsResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	f := in.feedByID(req.GetSourceId())
	if f == nil {
		return nil, status.Error(codes.NotFound, "no such subscription")
	}
	return &pb.GetFeedSettingsResponse{Settings: in.settings(f)}, nil
}

func (s *readerService) UpdateFeedSettings(_ context.Context, req *pb.UpdateFeedSettingsRequest) (*pb.UpdateFeedSettingsResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	f := in.feedByID(req.GetSourceId())
	if f == nil {
		return nil, status.Error(codes.NotFound, "no such subscription")
	}
	// Every field is optional and unset means "leave it alone" — the same
	// tri-state rule SetItemState uses, and for the same reason: a client that
	// knows about half these fields must not blank the other half by omitting
	// them.
	if req.Title != nil {
		f.override = strings.TrimSpace(req.GetTitle())
	}
	if req.InMegafeed != nil {
		f.inMegafeed = req.GetInMegafeed()
	}
	if req.MutedUntil != nil {
		f.mutedUntil = req.GetMutedUntil()
	}
	if req.CacheDepth != nil {
		f.cacheDepth = req.GetCacheDepth()
	}
	if req.FetchIntervalS != nil {
		// The shared half of the panel. On a real instance this changes how
		// often a source is polled for EVERY subscriber, which is why the panel
		// says so; here there is one.
		f.fetchInterval = req.GetFetchIntervalS()
	}
	return &pb.UpdateFeedSettingsResponse{Settings: in.settings(f)}, nil
}

// AnalyzeSite answers "can I follow this page?" (§11).
//
// The real ladder fetches the page, reads its <link rel="alternate">, probes a
// handful of conventional paths, and parses every candidate before offering it.
// None of that is possible from a browser tab with no server behind it, so this
// returns the shape of an answer rather than a real one — and it says which
// rung it is pretending to be, because the dialog's whole argument is that a
// candidate was found somewhere specific rather than guessed.
func (s *readerService) AnalyzeSite(_ context.Context, req *pb.AnalyzeSiteRequest) (*pb.AnalyzeSiteResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	url := strings.TrimSpace(req.GetUrl())
	if url == "" || !strings.Contains(url, ".") {
		return nil, status.Error(codes.InvalidArgument, "that does not look like an address")
	}
	host := hostOf(url)
	res := &pb.AnalyzeSiteResponse{
		PageTitle: titleFromHost(host),
		Feeds: []*pb.FeedCandidate{{
			Url:       "https://" + host + "/feed.xml",
			Title:     titleFromHost(host),
			ItemCount: 14,
			How:       "declared",
		}, {
			Url:       "https://" + host + "/rss",
			Title:     titleFromHost(host) + " — all posts",
			ItemCount: 30,
			How:       "probed",
		}},
	}
	if !req.GetSmart() {
		// Rungs 1-3 found something, so there is nothing for the model to do and
		// nothing to charge for. smart_status stays empty, which is what the
		// dialog reads as "no proposal was asked for".
		return res, nil
	}
	res.Scrape = &pb.ScrapeProposal{
		IndexUrl: url,
		Rule: &pb.ScrapeRule{
			ItemSelector:    "article.post",
			TitleSelector:   "h2 a",
			LinkSelector:    "h2 a@href",
			DateSelector:    "time@datetime",
			SummarySelector: "p.standfirst",
			AuthorSelector:  ".byline a",
		},
		Samples: []*pb.ScrapeSample{
			{
				Title:       "The index page has no feed, and that is a choice",
				Url:         "https://" + host + "/posts/no-feed",
				PublishedAt: stamp(in.clock().Add(-30 * time.Hour)),
				Summary:     "A rule, and the three articles it pulled out of the page as it stands today.",
			},
			{
				Title:       "Selectors are evidence, not a promise",
				Url:         "https://" + host + "/posts/selectors",
				PublishedAt: stamp(in.clock().Add(-4 * 24 * time.Hour)),
				Summary:     "What a scrape rule extracted is the thing worth reading before accepting it.",
			},
		},
		Found: 12,
		Notes: "Keyed on the repeated <article class=\"post\"> block; dates are machine-readable.",
	}
	return res, nil
}

func (s *readerService) SubscribeScrape(_ context.Context, req *pb.SubscribeScrapeRequest) (*pb.SubscribeScrapeResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	url := strings.TrimSpace(req.GetIndexUrl())
	if url == "" {
		return nil, status.Error(codes.InvalidArgument, "a page needs an address")
	}
	if req.GetRule() == nil || req.GetRule().GetItemSelector() == "" {
		// The rule is what makes this different from Subscribe. An empty one
		// would be a subscription to a page nothing can be extracted from.
		return nil, status.Error(codes.InvalidArgument, "that rule has no item selector")
	}
	f := in.addFeed(url, req.GetTitle(), req.GetFolderId(), "scrape")
	n := in.stockItems(f, 3)
	in.resort()
	return &pb.SubscribeScrapeResponse{
		Feed: f.message(in.unreadFor(f.sourceID)), Items: n,
	}, nil
}

// settings renders one feed's panel. Caller holds the lock.
func (in *Instance) settings(f *feed) *pb.FeedSettings {
	s := &pb.FeedSettings{
		SourceId:      f.sourceID,
		Title:         f.override,
		ResolvedTitle: f.title(),
		InMegafeed:    f.inMegafeed,
		MutedUntil:    f.mutedUntil,
		CacheDepth:    f.cacheDepth,

		FeedUrl:        f.feedURL,
		SiteUrl:        f.siteURL,
		Kind:           f.kind,
		FetchIntervalS: f.fetchInterval,

		LastError:           f.lastError,
		ConsecutiveFailures: f.failures,
		ItemCount:           in.itemsFor(f.sourceID),
		UnreadCount:         in.unreadFor(f.sourceID),
		// One, and it is the reader looking at it. The field exists to make the
		// "this is shared with other subscribers" warning land, and inflating it
		// in a single-user demo would be inventing people.
		SubscriberCount: 1,
	}
	if !f.lastFetch.IsZero() {
		s.LastFetchAt = stamp(f.lastFetch)
		s.NextFetchAt = stamp(f.lastFetch.Add(time.Duration(f.fetchInterval) * time.Second))
	}
	if !f.lastSuccess.IsZero() {
		s.LastSuccessAt = stamp(f.lastSuccess)
	}
	return s
}

func (s *readerService) RecordEngagements(_ context.Context, req *pb.RecordEngagementsRequest) (*pb.RecordEngagementsResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	// Counted and discarded. The signals log feeds a ranking model that does not
	// exist here, and storing observations about somebody looking at a demo
	// would be collecting data for no purpose — which is exactly the thing a
	// self-hosted reader exists to avoid.
	var accepted, rejected int32
	for _, e := range req.GetEvents() {
		if e.GetKind() == "" {
			rejected++
			continue
		}
		accepted++
	}
	return &pb.RecordEngagementsResponse{Accepted: accepted, Rejected: rejected}, nil
}

// sortStable is sort.SliceStable with a typed comparator, so the call sites read
// as a comparison rather than as two index lookups.
//
// Stable, always. Two items with the same score keeping their previous order is
// the difference between a ranked list and a list that reshuffles itself under a
// reader who changed nothing.
func sortStable[T any](s []T, less func(a, b T) bool) {
	sort.SliceStable(s, func(i, j int) bool { return less(s[i], s[j]) })
}

// hostOf is the host part of an address, forgiving about what it is handed.
//
// The add-a-feed field takes whatever someone pasted, so "example.com/blog",
// "https://example.com" and "www.example.com/" all arrive here and all mean the
// same site.
func hostOf(url string) string {
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "www.")
	if s == "" {
		return "example.com"
	}
	return s
}

// titleFromHost is the name a feed gets when nobody supplied one.
//
// The publisher's own title is what the real thing uses, and it comes out of the
// document the demo cannot fetch. A capitalised domain is the honest stand-in:
// it is visibly derived from what was typed rather than invented.
func titleFromHost(host string) string {
	name := host
	if i := strings.LastIndex(name, "."); i > 0 {
		name = name[:i]
	}
	name = strings.NewReplacer("-", " ", ".", " ").Replace(name)
	parts := strings.Fields(name)
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	if len(parts) == 0 {
		return host
	}
	return strings.Join(parts, " ")
}
