package demodata

import (
	"context"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// CountUnreadByCategory is the rail's per-label unread counts.
//
// # Why the labelled counts are all zero, and why that is the true answer
//
// The classification taxonomy — the labels behind "File under ?" — is not in
// this instance's fixtures. Nothing here is labelled, so every slug the rail
// asks about really does have nothing in it, and every unread article really is
// uncategorised. That is not a stub standing in for an answer: it is exactly
// what a real instance reports before its classifier has run over the backlog,
// which is the state every instance is in on its first day.
//
// So the handler counts, rather than refusing. It is the same arithmetic it
// would do over a labelled fixture, and the day the demo seeds labels the
// numbers stop being zero without this file changing.
//
// The slugs come from the CALLER (§20.7): a label with nothing in it must come
// back as 0 rather than be absent, because the rail renders all of them and a
// missing key and a zero are the same thing by the time they reach a map.
func (s *readerService) CountUnreadByCategory(_ context.Context, req *pb.CountUnreadByCategoryRequest) (
	*pb.CountUnreadByCategoryResponse, error) {

	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	// An empty map rather than a nil one, so a response with no slugs requested
	// still reads as a map on the other side.
	res := &pb.CountUnreadByCategoryResponse{Counts: map[string]int32{}}
	for _, slug := range req.GetSlugs() {
		res.Counts[slug] = 0
	}
	for _, it := range in.items {
		if !it.read {
			res.Uncategorised++
		}
	}
	return res, nil
}
