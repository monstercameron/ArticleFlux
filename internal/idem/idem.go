// Package idem enforces §20.7's idempotency contract at the interceptor
// (TODO 8c.15).
//
// # What was missing
//
// The `idempotency_keys` table, the repository methods and `idgen.Idempotency
// Key()` all existed, the client stamped a key onto every mutating RPC — and
// nothing on the server read one. The keys went into a void.
//
// That was survivable only by accident. Every mutation the outbox queues today
// sets an ABSOLUTE value ("read = true"), so applying it twice lands on the same
// state; §20.19.8 records that. The accident ends at the first queued operation
// that is relative — an append, a counter, a toggle — and the failure is silent
// when it comes:
//
//	a star toggles back off
//	a note is appended twice
//	an unread count moves by two
//
// Nothing errors. Nobody finds out until a reader notices their own data is
// wrong, which is much later and by then unattributable.
//
// # Why an interceptor rather than per-handler
//
// The same reason the capability map is one map: there are thirty-odd mutating
// RPCs and the thirty-first will be added by someone who does not know this
// exists. An interceptor covers a method the moment it has an
// `idempotency_key` field, which is a property of the proto rather than of
// somebody's memory.
package idem

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/monstercameron/ArticleFlux/internal/apierr"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// keyed is implemented by every request message carrying §20.7's key. The
// generated getter is the interface, so a new RPC opts in by declaring the
// field and nothing else.
type keyed interface {
	GetIdempotencyKey() string
}

// Store is what the interceptor needs from the repository.
type Store interface {
	BeginIdempotent(ctx context.Context, s store.Scope, key, method string, request []byte) (store.IdemRecord, bool, error)
	ReserveIdempotent(ctx context.Context, s store.Scope, key, method string, request []byte) error
	CompleteIdempotent(ctx context.Context, s store.Scope, key, method string, request, response []byte, status int) error
}

// ScopeFunc resolves the caller. The same one the servers take, so the
// interceptor and the handlers cannot disagree about who is calling.
type ScopeFunc func(context.Context) (store.Scope, error)

// marshal is DETERMINISTIC, and that is not a detail.
//
// The stored request hash is what catches a key reused for a different request.
// Protobuf marshalling is not canonical by default — map entries can be emitted
// in any order — so the ordinary options would hash the SAME request
// differently on a retry, and every replay would come back as a spurious
// conflict. Which is worse than no idempotency: the write is refused and the
// caller is told they made a mistake.
var marshal = proto.MarshalOptions{Deterministic: true}

// Unary returns the interceptor.
//
// A method with no `idempotency_key` field, or an empty key, passes straight
// through. That is deliberate: a read has nothing to replay, and forcing a key
// onto every call would make the table a log of every request the instance ever
// served — which is a browsing history under another name.
func Unary(st Store, scopeOf ScopeFunc) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {

		k, ok := req.(keyed)
		if !ok || k.GetIdempotencyKey() == "" {
			return handler(ctx, req)
		}
		sc, err := scopeOf(ctx)
		if err != nil {
			// Unauthenticated, and the handler will say so. Not answered here,
			// so there is one place that decides what an unauthenticated call
			// gets told.
			return handler(ctx, req)
		}

		msg, ok := req.(proto.Message)
		if !ok {
			return handler(ctx, req)
		}
		reqBytes, err := marshal.Marshal(msg)
		if err != nil {
			return nil, apierr.Status(apierr.Internal(err))
		}

		key, method := k.GetIdempotencyKey(), info.FullMethod

		rec, replay, err := st.BeginIdempotent(ctx, sc, key, method, reqBytes)
		if err != nil {
			// ErrIdemConflict lands here: the same key with a different request.
			// FromDomain turns it into a FailedPrecondition rather than replaying
			// the first answer for the second write, which would drop the write
			// AND report success.
			return nil, apierr.Status(apierr.FromDomain(err))
		}
		if replay {
			out, uerr := decode(method, rec.Response)
			if uerr != nil {
				// The stored bytes no longer parse — a response message whose
				// shape changed across a deploy. Re-running rather than erroring,
				// because an error strands a client that cannot stop retrying the
				// key.
				//
				// # Re-running is NOT free, and the old note here said it was
				//
				// It claimed "the mutations that exist set absolute values, so a
				// second application is a no-op". That is true of SetItemState —
				// read, starred and rating are absolute tri-states — and it is
				// NOT true of MarkAllRead, the only other keyed request:
				//
				//   - It mints an UNDO BATCH per call. client/data/conn.go says so
				//     in as many words, and refuses to queue it offline for
				//     exactly this reason: replaying it "would create two batches
				//     and leave the undo offering to reverse half its own work"
				//     (§20.19.8). Two files held opposite beliefs about the same
				//     RPC and this was the one that acted on its own.
				//   - Its `before` bound is what makes the marked SET stable, and
				//     the client deliberately leaves it empty so the server's clock
				//     decides. So a re-run marks everything up to the NEW now,
				//     silently including articles that arrived in between — which
				//     is the precise thing the field exists to prevent.
				//
				// It is not reachable today: this client sends no idempotency key
				// with MarkAllRead and never queues it, so the interceptor passes
				// it straight through. What makes it worth naming is that idem is
				// OPT-IN BY DECLARING A PROTO FIELD, so the assumption above is
				// inherited by whatever declares one next, silently. See
				// TestOnlyKnownRequestsCarryAnIdempotencyKey, which is what turns
				// "somebody should check" into a failing build.
				return runAndStore(ctx, st, sc, key, method, reqBytes, req, handler)
			}
			return out, nil
		}

		return runAndStore(ctx, st, sc, key, method, reqBytes, req, handler)
	}
}

func runAndStore(ctx context.Context, st Store, sc store.Scope, key, method string,
	reqBytes []byte, req any, handler grpc.UnaryHandler) (any, error) {

	// Claim the key BEFORE running, so the attempt leaves a trace.
	//
	// Without this the row appears only when the handler finishes, and a request
	// that crashes halfway leaves nothing at all — the retry is indistinguishable
	// from a first attempt, and a second key reused for a different body has
	// nothing to conflict against until the first one returns.
	//
	// It does NOT make concurrent duplicates run once, and cannot: two callers
	// presenting the same key at the same instant are the SAME request, and the
	// second one has no answer to be given yet. `BeginIdempotent` says so
	// explicitly — reporting a conflict would be wrong and replaying nothing
	// would be wrong, so it proceeds. What makes that safe is the property the
	// whole outbox already relies on: every queued mutation writes an absolute
	// value, so applying it twice lands on the same state (§20.19.8).
	//
	// A failure to reserve is not fatal. The reservation is bookkeeping that
	// narrows a window; refusing the request would turn a narrowed window into a
	// failed write.
	if err := st.ReserveIdempotent(ctx, sc, key, method, reqBytes); err != nil {
		_ = err // best-effort; see above
	}

	res, err := handler(ctx, req)
	if err != nil {
		// FAILURES ARE NOT STORED, and this is the decision worth arguing with.
		//
		// Storing them would make a transient failure permanent for 24 hours:
		// the client retries with the same key — which is exactly what a client
		// with a queued mutation does — and gets the stored failure back
		// forever, with no way to ask again. A retried key that succeeds the
		// second time is the behaviour people expect from a retry.
		//
		// The cost is that a genuinely non-idempotent operation which fails
		// halfway can be attempted twice. Nothing here is in that position:
		// every mutation writes absolute values inside one transaction.
		return nil, err
	}

	if out, ok := res.(proto.Message); ok {
		if resBytes, merr := marshal.Marshal(out); merr == nil {
			// A storage failure is not returned. The work SUCCEEDED, and telling
			// the caller it failed would make them retry a mutation that
			// already applied — turning a bookkeeping problem into the exact
			// double-apply this package exists to prevent.
			_ = st.CompleteIdempotent(ctx, sc, key, method, reqBytes, resBytes, 0)
		}
	}
	return res, nil
}

// decode rebuilds a stored response from the method's declared output type.
//
// The type is looked up in the protobuf registry rather than remembered,
// because the interceptor never sees the response type on a replay — it does
// not call the handler. `/articleflux.v1.ReaderService/SetItemState` gives the
// service and the method, and the descriptor gives the message.
func decode(fullMethod string, stored []byte) (proto.Message, error) {
	// "/pkg.Service/Method"
	trimmed := strings.TrimPrefix(fullMethod, "/")
	slash := strings.LastIndexByte(trimmed, '/')
	if slash < 0 {
		return nil, errBadMethod{fullMethod}
	}
	service, method := trimmed[:slash], trimmed[slash+1:]

	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, err
	}
	sd, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, errBadMethod{fullMethod}
	}
	md := sd.Methods().ByName(protoreflect.Name(method))
	if md == nil {
		return nil, errBadMethod{fullMethod}
	}
	mt, err := protoregistry.GlobalTypes.FindMessageByName(md.Output().FullName())
	if err != nil {
		return nil, err
	}
	out := mt.New().Interface()
	if err := proto.Unmarshal(stored, out); err != nil {
		return nil, err
	}
	return out, nil
}

type errBadMethod struct{ m string }

func (e errBadMethod) Error() string { return "idem: cannot resolve method " + e.m }
