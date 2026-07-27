package grpcsrv

import (
	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/apierr"
)

// errKey builds a status that a client can TRANSLATE.
//
// Every refusal a reader sees used to be prose composed on the server and
// rendered verbatim, which meant an interface running in French said "only an
// administrator can change Smart+ settings" in English. The server cannot
// translate it itself: the language is a per-device choice living in the
// browser's localStorage, and the server never sees it.
//
// So it sends both. `msg` stays the English fallback on the status — which is
// what the Google Reader sync API (§20.7) and anyone reading the error off a
// curl will get, neither of which has a catalog — and an ErrorDetail carries
// the key the wasm client looks up in the same catalog every other string comes
// from.
//
// # Why this now goes through internal/apierr
//
// §20.7's taxonomy is shared by three transports, and it used to be implemented
// in this package — which is one of them. A taxonomy living inside one
// transport is one the other two re-derive, and the way that gets discovered is
// a client handling a 404 from the tunnel and a 403 from the sync API for the
// same condition. `apierr` owns the table and the detail payload; this stays as
// the call-site-friendly shape the twenty-odd handlers already use.
//
// New classifications belong in `apierr` as named constructors, not here: a
// code passed inline is a decision nobody can find later.
func errKey(c codes.Code, key, msg string, args map[string]string) error {
	e := apierr.New(kindOf(c), key, msg)
	if len(args) > 0 {
		e = e.WithArgs(args)
	}
	return apierr.Status(e)
}

// kindOf maps a gRPC code back to §20.7's kind.
//
// The reverse direction exists only for these legacy call sites. It is not
// exported and should not grow users: a Kind carries what a handler MEANT and a
// code carries only what the wire says, so going kind→code loses nothing while
// code→kind is a guess that happens to be unambiguous today.
//
// An unrecognised code becomes Internal, which is the same fail-safe default
// apierr applies to an unrecognised error.
func kindOf(c codes.Code) apierr.Kind {
	switch c {
	case codes.Unauthenticated:
		return apierr.KindUnauthenticated
	case codes.PermissionDenied:
		return apierr.KindPermissionDenied
	case codes.NotFound:
		return apierr.KindNotFound
	case codes.InvalidArgument:
		return apierr.KindInvalidArgument
	case codes.FailedPrecondition:
		return apierr.KindFailedPrecondition
	case codes.ResourceExhausted:
		return apierr.KindResourceExhausted
	case codes.Aborted:
		return apierr.KindAborted
	case codes.Unavailable:
		return apierr.KindUnavailable
	case codes.Unimplemented:
		return apierr.KindUnimplemented
	default:
		return apierr.KindInternal
	}
}
