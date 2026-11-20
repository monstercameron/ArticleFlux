package grpcsrv

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/opml"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Migration's transport half (F1): the two RPCs behind Settings → Data.
//
// Kept out of subscribe.go because these are not part of the subscribe ladder —
// they are how a whole reading life arrives and leaves, and the ladder is about
// one address at a time.

// MaxImportBytes bounds an upload well under gRPC's 4 MiB default receive limit.
//
// internal/opml.MaxSize is 8 MiB, which is the right bound for a file read off
// local disk by the CLI and the wrong one for a message on this tunnel: a file
// between the two would be refused by the transport with a size error rather
// than by us with a sentence. A 151-feed export is ~32 KB, so this is two orders
// of magnitude of headroom.
const MaxImportBytes = 2 << 20

// MaxImportRows caps how many rows one ImportOpml call takes however many the
// caller asks for. A chunk is meant to finish well inside a client deadline, and
// a caller that asks for ten thousand has re-invented the single call.
const MaxImportRows = 500

// importCeiling bounds an unchunked import that no longer depends on its
// caller's deadline.
const importCeiling = 10 * time.Minute

func (s *ReaderServer) ImportOpml(ctx context.Context, req *pb.ImportOpmlRequest) (
	*pb.ImportOpmlResponse, error) {

	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}

	data := req.GetOpml()
	if len(data) == 0 {
		return nil, errKey(codes.InvalidArgument, "srv.opmlEmpty", "That file was empty.", nil)
	}
	if len(data) > MaxImportBytes {
		return nil, errKey(codes.InvalidArgument, "srv.opmlTooBig",
			"That file is too large to import. A subscription list is normally a few dozen kilobytes.", nil)
	}

	// A chunk is bounded by its own size and runs under the caller's deadline,
	// which is the point of chunking. The unchunked form — a client that predates
	// start/max_rows, or a script — gets the whole file, and for that one the
	// caller's patience is NOT the server's to inherit: the client's deadline
	// used to cancel the loop half way, leaving some rows imported and no
	// report, and a half-finished import nobody was told about is the worst of
	// the outcomes (G1). It finishes whether or not anyone is still listening,
	// bounded by a ceiling of its own so a wedged write cannot hold the handler
	// forever.
	maxRows := int(req.GetMaxRows())
	if maxRows > MaxImportRows {
		maxRows = MaxImportRows
	}
	ictx := ctx
	if maxRows == 0 {
		var cancel context.CancelFunc
		ictx, cancel = context.WithTimeout(context.WithoutCancel(ctx), importCeiling)
		defer cancel()
	}
	res, err := s.svc.ImportOPMLRange(ictx, sc, data, int(req.GetStart()), maxRows)
	if err != nil {
		if errors.Is(err, opml.ErrNotOPML) {
			// The reader picked the wrong file, which is a thing they can fix —
			// so it is a sentence about the file rather than a swallowed Internal.
			return nil, errKey(codes.InvalidArgument, "srv.opmlNotOPML",
				"That file is not an OPML subscription list. Export one from your old reader and pick that.", nil)
		}
		return nil, toStatus(err)
	}

	out := &pb.ImportOpmlResponse{
		Title:             res.Title,
		Subscribed:        int32(res.Subscribed),
		AlreadySubscribed: int32(res.AlreadySubscribed),
		Shared:            int32(res.Shared),
		Folders:           int32(res.Folders),
		Total:             int32(res.Total),
		Next:              int32(res.Next),
		Skipped:           int32(res.SkipCount()),
	}
	for _, sk := range res.Skips {
		out.Skips = append(out.Skips, &pb.ImportSkip{
			Title: sk.Title, Url: sk.URL, Reason: sk.Reason,
		})
	}
	return out, nil
}

func (s *ReaderServer) ExportOpml(ctx context.Context, _ *pb.ExportOpmlRequest) (
	*pb.ExportOpmlResponse, error) {

	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	data, n, err := s.svc.ExportOPML(ctx, sc)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ExportOpmlResponse{Opml: data, Feeds: int32(n)}, nil
}
