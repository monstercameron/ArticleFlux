package apierr

import (
	"errors"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reqid"
)

// WithRequestID stamps the request id onto an error on its way back to a client.
//
// # Why this is a rewrite rather than a constructor argument
//
// The id is minted by an interceptor and the error is built by a handler, and
// the handler has no reason to know about either. Threading the id through every
// constructor would put a logging concern into the signature of every failure
// path in the program, and the one call site somebody forgets is — reliably —
// on the error nobody could reproduce.
//
// So the error is built as it always was, and the id is attached at the single
// point where both are in scope: the interceptor, on the way out.
//
// # It goes in args, not in the message
//
// The message stays exactly what §20.7 requires — safe, translatable, and
// asserted verbatim by a number of tests. The id rides in the ErrorDetail's args
// map, which is documented as carrying server-side identifiers, so a client that
// knows the key renders "internal error (reference 7f3a9c)" and one that does
// not renders "internal error" and is no worse off than before.
//
// An error carrying no detail at all still gets one, because an Internal error
// is exactly the case where the reference matters most and exactly the case
// where a handler was least likely to have classified anything.
func WithRequestID(err error, id string) error {
	if err == nil || id == "" {
		return err
	}

	st, ok := status.FromError(err)
	if !ok {
		// Not a status yet — an unclassified error escaping a handler. Run it
		// through the normal taxonomy first so it is sanitized before anything
		// is attached to it, rather than stamping an id onto a raw database
		// error and sending both.
		st, _ = status.FromError(Status(err))
	}

	details := st.Details()
	rebuilt := make([]protoadapt.MessageV1, 0, len(details)+1)
	stamped := false
	for _, d := range details {
		if ed, isDetail := d.(*pb.ErrorDetail); isDetail {
			if ed.Args == nil {
				ed.Args = map[string]string{}
			}
			ed.Args[reqid.ArgKey] = id
			stamped = true
		}
		if m, isV1 := d.(protoadapt.MessageV1); isV1 {
			rebuilt = append(rebuilt, m)
		}
	}
	if !stamped {
		rebuilt = append(rebuilt, &pb.ErrorDetail{
			Key:  "srv.internal",
			Args: map[string]string{reqid.ArgKey: id},
		})
	}

	out, derr := status.New(st.Code(), st.Message()).WithDetails(rebuilt...)
	if derr != nil {
		// Re-attaching can fail on a marshalling error. The original error is
		// still correct and still safe; losing the reference is the right thing
		// to lose here.
		return err
	}
	// Rebuilding a status from its code and message drops everything that is
	// not on the wire, which includes the cause. Carrying it across is the
	// whole point of statusError: stamping the id must not be the step that
	// makes the failure unloggable.
	var classified *Error
	if errors.As(err, &classified) {
		return &statusError{st: out, src: classified}
	}
	return out.Err()
}

// RequestIDOf reads the request id back off an error, for tests and for the
// sync API's JSON body.
func RequestIDOf(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return ""
	}
	for _, d := range st.Details() {
		if ed, isDetail := d.(*pb.ErrorDetail); isDetail {
			return ed.GetArgs()[reqid.ArgKey]
		}
	}
	return ""
}
