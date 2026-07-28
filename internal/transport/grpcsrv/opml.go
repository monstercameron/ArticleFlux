package grpcsrv

import (
	"context"
	"errors"

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

func (s *ReaderServer) ImportOpml(ctx context.Context, req *pb.ImportOpmlRequest) (
	*pb.ImportOpmlResponse, error) {

	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}

	data := req.GetOpml()
	if len(data) == 0 {
		return nil, errKey(codes.InvalidArgument, "srv.opmlEmpty", "that file was empty", nil)
	}
	if len(data) > MaxImportBytes {
		return nil, errKey(codes.InvalidArgument, "srv.opmlTooBig",
			"that file is too large to import — a subscription list is normally a few dozen kilobytes", nil)
	}

	res, err := s.svc.ImportOPML(ctx, sc, data)
	if err != nil {
		if errors.Is(err, opml.ErrNotOPML) {
			// The reader picked the wrong file, which is a thing they can fix —
			// so it is a sentence about the file rather than a swallowed Internal.
			return nil, errKey(codes.InvalidArgument, "srv.opmlNotOPML",
				"that file is not an OPML subscription list — export one from your old reader and pick that", nil)
		}
		return nil, toStatus(err)
	}

	out := &pb.ImportOpmlResponse{
		Title:             res.Title,
		Subscribed:        int32(res.Subscribed),
		AlreadySubscribed: int32(res.AlreadySubscribed),
		Shared:            int32(res.Shared),
		Folders:           int32(res.Folders),
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
