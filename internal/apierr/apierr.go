// Package apierr is §20.7's error taxonomy, in one place (TODO 7.3a).
//
// # Why one place
//
// The taxonomy was implemented in `grpcsrv`, which is one of THREE transports
// that share it — the tunnel, the Google Reader-compatible sync API (§15.1),
// and the proxy endpoints. A taxonomy that lives in one transport is a taxonomy
// the other two will re-derive, and the way you find out they derived it
// differently is a client that handles a 404 from one surface and a 403 from
// another for the same condition.
//
// So the mapping from a domain error to a code lives here, and every transport
// asks. gRPC codes are the canonical form and HTTP is derived, because the
// table in §20.7 is written that way.
//
// # The rule that leaks, and the one line of this package that matters
//
// **Cross-tenant access returns NotFound, never PermissionDenied.**
//
// `PermissionDenied` on item 4711 confirms item 4711 exists. That is a
// tenant-isolation leak with good manners: the attacker learns the id space is
// dense, learns which ids are real, and can enumerate. The rule is
//
//	within your tenant, tell the truth about permissions;
//	across tenants, the object does not exist.
//
// The repositories already implement half of this by returning ErrNotFound for
// a row in another tenant rather than a permission error — see the G2 leak
// harness. This package is the other half, and `CrossTenant` exists as a named
// constructor so a handler that knows it is refusing a cross-tenant access
// cannot express that as anything else.
//
// # Messages are always safe to display
//
// §20.7 requires it, and §22.11 says why: internal detail goes to the log with
// a request id, never to the client. An error whose message is a wrapped
// database error is an information leak that also fails to tell the user
// anything they can act on. Every Error here carries a catalog KEY, so the
// client renders a translated message and the English text is only a fallback
// for the two consumers with no catalog — the sync API and curl.
package apierr

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Kind is one row of §20.7's table.
//
// A kind rather than a bare code because the code is not enough to act on: two
// InvalidArguments, one naming a field and one naming a stale cursor, want
// different things from the client. The kind is what a handler chooses; the
// code is derived.
type Kind int

const (
	// KindInternal is the zero value ON PURPOSE. An error nobody classified is
	// an Internal error with a safe message, which is the only safe default —
	// the alternative is a zero value that means something specific and gets
	// applied to errors that are not that thing.
	KindInternal Kind = iota

	// KindUnauthenticated — no credential, expired, or revoked.
	KindUnauthenticated
	// KindPermissionDenied — authenticated, lacks the capability, FOR AN OBJECT
	// IN YOUR OWN TENANT. Never for one in another tenant; see KindNotFound.
	KindPermissionDenied
	// KindNotFound — the object does not exist, or belongs to another tenant.
	// Those two are deliberately the same answer.
	KindNotFound
	// KindInvalidArgument — bad field, bad enum, malformed cursor.
	KindInvalidArgument
	// KindFailedPrecondition — the state is wrong: source deactivated, trial
	// already ended, session not idle.
	KindFailedPrecondition
	// KindResourceExhausted — quota, rate limit, or LLM budget.
	KindResourceExhausted
	// KindAborted — a `rev` mismatch on a compare-and-set (A25).
	KindAborted
	// KindUnavailable — circuit open, degraded mode, storage read-only.
	KindUnavailable
	// KindUnimplemented — the server does not have this feature wired.
	//
	// Not in §20.7's table, and added rather than folded into Internal because
	// the two are different facts about the same request: Internal means the
	// server broke, Unimplemented means it is working correctly and this
	// deployment simply does not have the thing. A client can offer to hide a
	// control for one and must not for the other.
	KindUnimplemented
)

// grpcCode is §20.7's first column. Kept as a table rather than a switch so the
// mapping can be read as the specification it is.
var grpcCode = map[Kind]codes.Code{
	KindInternal:           codes.Internal,
	KindUnauthenticated:    codes.Unauthenticated,
	KindPermissionDenied:   codes.PermissionDenied,
	KindNotFound:           codes.NotFound,
	KindInvalidArgument:    codes.InvalidArgument,
	KindFailedPrecondition: codes.FailedPrecondition,
	KindResourceExhausted:  codes.ResourceExhausted,
	KindAborted:            codes.Aborted,
	KindUnavailable:        codes.Unavailable,
	KindUnimplemented:      codes.Unimplemented,
}

// httpStatus is §20.7's second column, for the sync API.
//
// Not derived from the gRPC code by a generic mapping: FailedPrecondition and
// Aborted are both 409 here, which no generic table produces, and the sync API's
// clients are third-party readers that will do the wrong thing with a surprise.
var httpStatus = map[Kind]int{
	KindInternal:           http.StatusInternalServerError,
	KindUnauthenticated:    http.StatusUnauthorized,
	KindPermissionDenied:   http.StatusForbidden,
	KindNotFound:           http.StatusNotFound,
	KindInvalidArgument:    http.StatusBadRequest,
	KindFailedPrecondition: http.StatusConflict,
	KindResourceExhausted:  http.StatusTooManyRequests,
	KindAborted:            http.StatusConflict,
	KindUnavailable:        http.StatusServiceUnavailable,
	KindUnimplemented:      http.StatusNotImplemented,
}

// Code returns the gRPC code for a kind.
func (k Kind) Code() codes.Code {
	if c, ok := grpcCode[k]; ok {
		return c
	}
	return codes.Internal
}

// HTTP returns the HTTP status for a kind, for the sync API.
func (k Kind) HTTP() int {
	if s, ok := httpStatus[k]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// Error is a classified failure with everything a client needs to render it.
type Error struct {
	Kind Kind
	// Key is the catalog key, "srv.something". What makes the message
	// translatable; see the package comment.
	Key string
	// Message is the English fallback. ALWAYS SAFE TO DISPLAY — never a wrapped
	// internal error.
	Message string
	// Args are placeholder values for the catalog message. Server-side
	// identifiers only, never the reader's own content.
	Args map[string]string

	// Field names the request field at fault, for a validation error.
	Field string
	// Quota names the limit that was hit.
	Quota string
	// RetryAfter is how long to wait. Zero means no useful estimate.
	RetryAfter time.Duration
	// DocRef points at documentation for a failure a person has to act on.
	DocRef string

	// cause is the underlying error. It goes to the LOG, never to the client —
	// unexported precisely so it cannot be reached by a handler assembling a
	// response.
	cause error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.cause)
	}
	return e.Message
}

// Unwrap exposes the cause to errors.Is/As, and therefore to the logger, while
// keeping it off the response.
func (e *Error) Unwrap() error { return e.cause }

// WithCause attaches the underlying error for logging.
func (e *Error) WithCause(err error) *Error { e.cause = err; return e }

// WithField names the offending request field.
func (e *Error) WithField(f string) *Error { e.Field = f; return e }

// WithArgs sets the catalog placeholders.
func (e *Error) WithArgs(a map[string]string) *Error { e.Args = a; return e }

// New builds a classified error.
func New(k Kind, key, msg string) *Error {
	return &Error{Kind: k, Key: key, Message: msg}
}

// ---------------------------------------------------------------------------
// The constructors that encode a decision
// ---------------------------------------------------------------------------

// CrossTenant is the one this package exists for.
//
// A handler that has established the caller is reaching for another tenant's
// object calls THIS, and gets a NotFound. Having it as a named constructor is
// the point: the alternative is a handler author deciding, at the moment they
// are thinking about permissions, that PermissionDenied is the honest answer —
// which it is, and which confirms the object exists.
//
// It deliberately takes no object id and produces the same message as a genuine
// miss, because a response that differs in any way is the leak arriving through
// a different door.
func CrossTenant() *Error {
	return New(KindNotFound, "srv.notFound", "not found")
}

// NotFound is a genuine miss. Identical to CrossTenant by construction — that
// identity is the property, and a test asserts it.
func NotFound() *Error {
	return New(KindNotFound, "srv.notFound", "not found")
}

// Unauthenticated covers missing, expired and revoked credentials alike.
func Unauthenticated() *Error {
	return New(KindUnauthenticated, "srv.notAuthenticated", "not authenticated")
}

// PermissionDenied is for an object IN THE CALLER'S OWN TENANT that they lack
// the capability for. Using it across tenants is the leak; use CrossTenant.
func PermissionDenied(capability string) *Error {
	return New(KindPermissionDenied, "srv.permissionDenied",
		"you do not have permission to do that").
		WithArgs(map[string]string{"capability": capability})
}

// Invalid is a validation failure naming the field at fault, so a form can put
// the message beside the input rather than at the top.
func Invalid(field, key, msg string) *Error {
	return New(KindInvalidArgument, key, msg).WithField(field)
}

// StaleCursor is InvalidArgument rather than an empty page.
//
// An empty page means "you have reached the end", and a client that reads a
// stale cursor as the end stops paging and shows a truncated list with no error
// anywhere — the worst kind of bug, because it looks like data.
func StaleCursor() *Error {
	return New(KindInvalidArgument, "srv.staleCursor",
		"this page cursor is out of date; reloading the list")
}

// RateLimited reports a limit with the wait attached.
//
// The retry hint is not politeness: without it every client invents its own
// backoff, and the ones that invent badly turn a rate limit into a hot loop
// against the thing that was already overloaded.
func RateLimited(quota string, retryAfter time.Duration) *Error {
	e := New(KindResourceExhausted, "srv.rateLimited",
		"too many requests; please slow down")
	e.Quota = quota
	e.RetryAfter = retryAfter
	return e
}

// QuotaExceeded is a limit that waiting will not fix — a subscription count, a
// storage allowance — as distinct from a rate limit, which is a limit that
// waiting DOES fix. Same code, and the client must tell them apart to know
// whether to offer "try again" or "upgrade".
func QuotaExceeded(quota string) *Error {
	e := New(KindResourceExhausted, "srv.quotaExceeded",
		"you have reached a limit on this account")
	e.Quota = quota
	return e
}

// Conflict is A25's compare-and-set failure: the caller's `rev` is stale.
//
// Aborted rather than FailedPrecondition, because Aborted is the code that
// means "retry the whole read-modify-write", which is exactly the remedy.
func Conflict() *Error {
	return New(KindAborted, "srv.revConflict",
		"this changed somewhere else; reloading")
}

// Precondition is a state error: the thing exists, you may touch it, and it is
// not in a state where this makes sense.
func Precondition(key, msg string) *Error {
	return New(KindFailedPrecondition, key, msg)
}

// Unavailable is a temporary refusal — a breaker open, degraded mode, or a
// read-only store.
func Unavailable(key, msg string, retryAfter time.Duration) *Error {
	e := New(KindUnavailable, key, msg)
	e.RetryAfter = retryAfter
	return e
}

// Unimplemented reports a feature this deployment does not have wired.
func Unimplemented(key, msg string) *Error {
	return New(KindUnimplemented, key, msg)
}

// Internal is the safe default for anything unclassified.
//
// The cause is attached for the log and never reaches the client.
func Internal(cause error) *Error {
	return New(KindInternal, "srv.internal", "internal error").WithCause(cause)
}

// ---------------------------------------------------------------------------
// gRPC
// ---------------------------------------------------------------------------

// Status converts any error into a gRPC status carrying §20.7's detail.
//
// An error that is not an *Error becomes Internal with a safe message. That is
// the fail-safe direction: an unclassified error reaching a client as its own
// text is how a database error ends up on somebody's screen.
func Status(err error) error {
	if err == nil {
		return nil
	}
	var e *Error
	if !errors.As(err, &e) {
		e = Internal(err)
	}

	st := status.New(e.Kind.Code(), e.Message)
	detail := &pb.ErrorDetail{
		Key: e.Key, Args: e.Args, Field: e.Field,
		Quota: e.Quota, DocRef: e.DocRef,
	}
	if e.RetryAfter > 0 {
		// Rounded UP, and to at least one second. Rounding down produces a hint
		// that expires before the limit does, so a client that obeys it exactly
		// retries into the same refusal and learns the hint is a lie.
		secs := int32((e.RetryAfter + time.Second - 1) / time.Second)
		if secs < 1 {
			secs = 1
		}
		detail.RetryAfterS = secs
	}
	withDetail, derr := st.WithDetails(detail)
	if derr != nil {
		// Attaching the detail can fail on a marshalling error. The status still
		// carries the code and a safe message, which is strictly better than
		// returning nothing.
		return &statusError{st: st, src: e}
	}
	return &statusError{st: withDetail, src: e}
}

// statusError is a gRPC status that still remembers WHY.
//
// # The bug this exists to close
//
// `st.Err()` produces an error carrying only what goes on the wire: a code, a
// safe message, and the detail. That is exactly right for the client and it
// silently threw away the cause — so an internal error took this path,
//
//	handler → toStatus(err) → apierr.Status → st.Err() → grpc-go → client
//
// and the wrapped database error attached by `Internal(cause)` was dropped at
// the third step. The client saw "internal error", and NOTHING ANYWHERE LOGGED
// WHAT HAPPENED. Three separate comments in this codebase — this package's, the
// one on reader.toStatus, and internal/reqid's — describe the original going
// "to the log with a request id"; no code did it, because by the time any
// interceptor saw the error there was nothing left to log.
//
// # How it stays safe
//
// grpc-go asks an error for its status through the GRPCStatus method, so the
// WIRE form is `st` and is unchanged: same code, same safe message, same
// detail. The classified error hangs off Unwrap, which reaches nothing but
// server-side code — errors.As in an interceptor, and the logger it hands the
// cause to.
//
// Error() deliberately returns the rich form, message and cause, because the
// only consumers of an error's own string here are logs. Anything rendering to
// a client goes through Status/HTTPStatus, which read `Message` and never this.
type statusError struct {
	st  *status.Status
	src *Error
}

func (e *statusError) Error() string { return e.src.Error() }

// GRPCStatus is what grpc-go calls to get the wire form. Its presence is the
// entire reason this type can carry a cause without leaking it.
func (e *statusError) GRPCStatus() *status.Status { return e.st }

// Unwrap exposes the classification, and through it the cause, to errors.As.
func (e *statusError) Unwrap() error { return e.src }

// HTTPStatus returns the HTTP code and a JSON-shaped body for the sync API.
func HTTPStatus(err error) (int, map[string]string) {
	if err == nil {
		return http.StatusOK, nil
	}
	var e *Error
	if !errors.As(err, &e) {
		e = Internal(err)
	}
	body := map[string]string{"code": e.Key, "message": e.Message}
	if e.Field != "" {
		body["field"] = e.Field
	}
	if e.Quota != "" {
		body["quota"] = e.Quota
	}
	if e.RetryAfter > 0 {
		secs := int((e.RetryAfter + time.Second - 1) / time.Second)
		if secs < 1 {
			secs = 1
		}
		body["retry_after_s"] = strconv.Itoa(secs)
	}
	if e.DocRef != "" {
		body["doc_ref"] = e.DocRef
	}
	return e.Kind.HTTP(), body
}

// LogDetail returns what a SERVER-SIDE log line should say about an error.
//
// The safe message is what the client got and is nearly useless in a log — "not
// found" tells whoever is reading nothing about what was not found or why. This
// returns the cause when there is one, so the log line beside the request id
// carries the wrapped database error, the parse failure, the timeout.
//
// Empty when the error carries no more than the client already knows, which is
// the honest answer for a validation failure: there is no hidden detail, the
// message IS the reason.
func LogDetail(err error) string {
	var e *Error
	if !errors.As(err, &e) {
		if err != nil {
			return err.Error()
		}
		return ""
	}
	if e.cause == nil {
		return ""
	}
	return e.cause.Error()
}

// KindOf reports the classification of an error, for tests and for logging.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}
