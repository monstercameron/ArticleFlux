package grpcsrv

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
	"github.com/monstercameron/ArticleFlux/internal/reader"
)

// feedAddError says WHAT the server saw when an address could not be added, as a
// keyed refusal the client can translate (client/i18n/en_srv.go), or nil when the
// failure is not one of those — a database error, a category that is not the
// caller's — and the caller's own handling should stand.
//
// Every one of these used to reach the reader as the same unkeyed
// InvalidArgument, which the client rendered as "Something went wrong on the
// server" — or, worse, was not an error at all: a 404 was subscribed and
// reported "Added" (H5). The order matters where errors wrap each other: a
// broken feed also satisfies ErrNotAFeed, and a blocked address can arrive
// wrapped in a url.Error.
func feedAddError(err error) error {
	var he *feed.HTTPError
	var ue *url.Error
	var ne net.Error
	switch {
	case errors.Is(err, reader.ErrBadURL):
		return errKey(codes.InvalidArgument, "srv.feedBadURL",
			"That is not a web address. Start it with http:// or https://.", nil)
	case errors.Is(err, netguard.ErrBlockedIP):
		return errKey(codes.InvalidArgument, "srv.feedPrivate",
			"That address is private or local, and this server does not fetch those.", nil)
	case errors.Is(err, netguard.ErrScheme):
		return errKey(codes.InvalidArgument, "srv.feedScheme",
			"Only http:// and https:// addresses can be followed.", nil)
	case errors.As(err, &he):
		return errKey(codes.InvalidArgument, "srv.feedHTTP",
			"That address answered HTTP "+strconv.Itoa(he.Status)+", so there is no feed there.",
			map[string]string{"status": strconv.Itoa(he.Status)})
	case errors.Is(err, feed.ErrBrokenFeed):
		return errKey(codes.InvalidArgument, "srv.feedBroken",
			"There is a feed at that address, but it is malformed and could not be read.", nil)
	case errors.Is(err, feed.ErrNotAFeed):
		return errKey(codes.InvalidArgument, "srv.feedNotAFeed",
			"That address is not a feed.", nil)
	case errors.Is(err, feed.ErrTooLarge):
		return errKey(codes.InvalidArgument, "srv.feedTooLarge",
			"That feed is larger than this server will read.", nil)
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return errKey(codes.InvalidArgument, "srv.feedTimeout",
			"That address took too long to answer.", nil)
	case errors.As(err, &ue) || errors.As(err, &ne):
		return errKey(codes.InvalidArgument, "srv.feedUnreachable",
			"Could not connect to that address.", nil)
	}
	return nil
}
