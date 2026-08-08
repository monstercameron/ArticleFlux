//go:build js && wasm

package view

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reqid"
)

// serverText turns a gRPC error into a sentence in the reader's language.
//
// The server attaches an `ErrorDetail` carrying a catalog key and arguments
// (see internal/transport/grpcsrv/errkey.go), because it cannot translate its
// own refusals — the language is a per-device choice it never sees. This
// resolves that key against the same catalog every other string comes from.
//
// Three fallbacks, in order, and each one is a real case rather than defensive
// padding:
//
//  1. No detail — an older server, or a status gRPC itself produced (a
//     transport failure has no ErrorDetail and never will). Use the message.
//  2. A detail whose key is not in the catalog — a NEWER server than this
//     client, sending a refusal this build has never heard of. The English
//     fallback the server also sent is exactly right for that, and it is why
//     the message is still populated server-side.
//  3. Not a gRPC status at all. Use err.Error().
//
// Never returns err.String(): gRPC wraps its own text as
// `rpc error: code = PermissionDenied desc = …`, which turns a clear
// instruction into something that reads like a crash.
func serverText(tr i18n.Runtime, err error) string {
	if err == nil {
		return ""
	}
	st, ok := status.FromError(err)
	if !ok {
		return err.Error()
	}
	// Held across the loop so the fallbacks below can still reach the request
	// id. A detail whose key this build does not carry falls through to the
	// server's English — and that is exactly the case where the reference is
	// most worth having, because a client and server that disagree about the
	// vocabulary is itself the bug being reported.
	var seen *pb.ErrorDetail
	for _, d := range st.Details() {
		detail, isDetail := d.(*pb.ErrorDetail)
		if !isDetail || detail.GetKey() == "" {
			continue
		}
		if seen == nil {
			seen = detail
		}
		ns, key, found := cutKey(detail.GetKey())
		if !found {
			continue
		}
		args := i18n.Args{}
		for k, v := range detail.GetArgs() {
			args[k] = v
		}
		out := tr.T(ns, key, args)
		// The missing-key form. A newer server naming a refusal this build does
		// not carry — its English is better than its identifier.
		if out == detail.GetKey() {
			break
		}
		return withReference(tr, out, st, detail)
	}
	if m := st.Message(); m != "" {
		return withReference(tr, m, st, seen)
	}
	return err.Error()
}

// withReference appends the server's request id to a message the reader is
// about to see, when there is one and when it is worth showing.
//
// # Why only for server-side failures
//
// The id exists so a bug report can be traced (§22.11), and a bug report is
// something a reader files about a failure THEY cannot fix. A permission
// refusal, a stale cursor, a rate limit and a validation error are all things
// the reader acts on themselves; a hex string after those is noise that makes a
// clear instruction look like a crash. Internal, Unknown, Unavailable and
// DataLoss are the ones where the honest answer is "tell whoever runs this".
//
// The gate is on the CODE rather than on whether an id was sent, because the
// interceptor stamps every error it sees — including the ones above.
func withReference(tr i18n.Runtime, msg string, st *status.Status, detail *pb.ErrorDetail) string {
	switch st.Code() {
	case codes.Internal, codes.Unknown, codes.Unavailable, codes.DataLoss:
	default:
		return msg
	}
	if detail == nil {
		return msg
	}
	id := detail.GetArgs()[reqid.ArgKey]
	if id == "" {
		return msg
	}
	return tr.T("srv", "reference", i18n.Args{"message": msg, "id": id})
}

// cutKey splits "namespace.key" at the FIRST dot, matching the catalog's own
// convention.
func cutKey(k string) (string, string, bool) {
	for i := 0; i < len(k); i++ {
		if k[i] == '.' {
			return k[:i], k[i+1:], i > 0 && i+1 < len(k)
		}
	}
	return "", "", false
}
