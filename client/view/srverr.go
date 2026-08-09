//go:build js && wasm

package view

import (
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reqid"
)

// serverKey returns the catalog key the server classified a refusal as, or "".
//
// serverText answers "what do I show the reader"; this answers "which refusal
// is this", which is a different question and the one a caller asks when a
// particular outcome is part of the FLOW rather than the end of it.
// `srv.sudoRequired` is the case that motivated it: the password change asks
// for it, expects to be refused the first time, and turns that refusal into a
// prompt — so it has to recognise the refusal without reading prose that
// changes with the reader's language.
//
// Deliberately the raw key, namespace included, matched against a constant at
// the call site. Splitting it here would invite comparing bare "sudoRequired"
// against a key some other namespace might one day also use.
// keySudoRequired is grpcsrv's `srv.sudoRequired`, the refusal that means "the
// session is fine, ask for the password again".
//
// Named here, once, because the alternative is the same string literal in every
// caller that has to tell it apart from a real failure — and getting it wrong
// is silent: the flow simply reports "this needs your password again" as an
// error and offers no way to give one.
//
// A literal rather than an import from the server package: the client cannot
// take a dependency on grpcsrv, and the string is the wire contract either way.
// A test in this package pins it against the server's constant.
const keySudoRequired = "srv.sudoRequired"

func serverKey(err error) string {
	if err == nil {
		return ""
	}
	st, ok := status.FromError(err)
	if !ok {
		return ""
	}
	for _, d := range st.Details() {
		if detail, isDetail := d.(*pb.ErrorDetail); isDetail && detail.GetKey() != "" {
			return detail.GetKey()
		}
	}
	return ""
}

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
		return withWait(tr, withReference(tr, out, st, detail), detail)
	}
	if m := st.Message(); m != "" {
		return withWait(tr, withReference(tr, m, st, seen), seen)
	}
	return err.Error()
}

// withWait appends how long to wait, when the server said.
//
// Every rate limit and every lockout in this application computes a precise
// retry_after and puts it on the ErrorDetail, and until now nothing read it —
// so the sentence a locked-out reader saw was "too many requests; please slow
// down" with no way to tell fifteen seconds from fifteen minutes. That is what
// makes a COOLDOWN feel like a ban: not the wait, but not knowing its length,
// which leaves retrying-immediately as the only way to find out and is exactly
// the behaviour the limiter is trying to stop.
//
// Rounded UP to the next minute above ninety seconds, and to the second below
// it. "Try again in 14 minutes" is what somebody acts on; "in 847 seconds" is
// arithmetic homework, and rounding DOWN would send them back a moment early to
// be refused again.
func withWait(tr i18n.Runtime, msg string, detail *pb.ErrorDetail) string {
	if detail == nil {
		return msg
	}
	secs := int(detail.GetRetryAfterS())
	if secs <= 0 {
		return msg
	}
	if secs <= 90 {
		return tr.T("srv", "waitSeconds", i18n.Args{"message": msg, "n": strconv.Itoa(secs)})
	}
	mins := (secs + 59) / 60
	return tr.T("srv", "waitMinutes", i18n.Args{"message": msg, "n": strconv.Itoa(mins)})
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
