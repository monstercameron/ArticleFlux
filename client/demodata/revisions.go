package demodata

import (
	"context"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// GetItemRevisions answers §10.1's "this changed since you read it" disclosure.
//
// Served rather than refused because the demo can hold the whole of it: an edit
// history is text this instance already has, and the feature it demonstrates —
// that a wire story quietly lost a number from its headline between two polls —
// is one nobody can see on a screenshot and few readers have ever been shown at
// all.
//
// The fixture is exactly one article with exactly one superseded version, which
// is roughly the proportion on a real instance. An empty history is the normal
// answer here and it means "no edit seen", never "no edit happened" — so the
// empty response below is a real answer rather than a stub.
func (s *readerService) GetItemRevisions(_ context.Context, req *pb.GetItemRevisionsRequest) (
	*pb.GetItemRevisionsResponse, error) {

	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	// An unknown id gets an empty history rather than NotFound, which is what
	// the server does: a disclosure that 404s on an article the list can still
	// show would turn a missing history into an error banner.
	revs := in.revisions[req.GetItemId()]

	// Newest first, and the fixture is written that way — so the limit takes
	// from the front. Zero means the server's default; a handful is the useful
	// answer, because nobody reads the twentieth version of a wire story.
	limit := int(req.GetLimit())
	if limit <= 0 || limit > len(revs) {
		limit = len(revs)
	}
	return &pb.GetItemRevisionsResponse{Revisions: revs[:limit]}, nil
}
