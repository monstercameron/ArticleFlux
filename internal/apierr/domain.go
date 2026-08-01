package apierr

import (
	"errors"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// FromDomain classifies a domain error into §20.7's taxonomy.
//
// This is the function every transport calls, and the reason it lives here
// rather than in a handler: the mapping from "the store said ErrNotFound" to
// "the client sees 404" is a decision made once, and three transports asking
// the same function is what keeps them from making it three different ways.
//
// # What it does NOT do
//
// It does not wrap an unrecognised error's text into the response. An error
// this function has never heard of becomes Internal with a safe message and the
// original attached for the log. That is the whole point of a taxonomy with a
// default: the failure mode of the alternative is a `sqlite3: no such column`
// on somebody's screen, and the failure mode of this is a log line.
func FromDomain(err error) error {
	if err == nil {
		return nil
	}
	// Already classified — a handler that made a deliberate choice keeps it.
	var already *Error
	if errors.As(err, &already) {
		return already
	}

	switch {
	// A row in another tenant returns ErrNotFound from the repositories, which
	// is the half of the cross-tenant rule the store implements. This is the
	// other half: it stays NotFound all the way to the client.
	case errors.Is(err, store.ErrNotFound):
		return NotFound().WithCause(err)

	case errors.Is(err, store.ErrNoScope):
		return Unauthenticated().WithCause(err)

	case errors.Is(err, store.ErrBadCursor), errors.Is(err, store.ErrCursorSpecMismatch):
		return StaleCursor().WithCause(err)

	case errors.Is(err, store.ErrIdemConflict):
		// The same idempotency key with a DIFFERENT request body. Not a replay
		// to be answered from the cache — a client bug or a key collision, and
		// answering it with the stored response would apply neither operation
		// the caller asked for.
		return Precondition("srv.idempotencyConflict",
			"this request was already used for something else").WithCause(err)

	case errors.Is(err, store.ErrInviteInvalid):
		// Deliberately not distinguishing expired, spent and never-existed: the
		// difference tells someone holding a guessed code whether it was real.
		return Invalid("code", "srv.inviteInvalid",
			"that invitation is not valid").WithCause(err)

	case errors.Is(err, store.ErrBadResetToken):
		return Invalid("token", "srv.resetTokenInvalid",
			"that reset link is not valid").WithCause(err)

	case errors.Is(err, store.ErrRefreshReuse):
		// A replayed refresh token means the family is already revoked — every
		// device in it. Unauthenticated rather than a state error, because the
		// only correct client response is to log in again.
		return Unauthenticated().WithCause(err)

	case errors.Is(err, store.ErrAmbiguousUser):
		// D12: usernames are unique per tenant, so a login with two matches is
		// unanswerable until there is a tenant hint. An outage, not a leak —
		// and FailedPrecondition says "the state is wrong", which it is.
		return Precondition("srv.ambiguousUser",
			"this account name exists in more than one workspace").WithCause(err)

	case errors.Is(err, llm.ErrCircuitOpen):
		// §22.8. The retry hint is the breaker's own cooling period rather than
		// a guess: a client that retries sooner is refused again, and one that
		// waits longer than it needs to makes the feature look broken after the
		// provider recovered.
		return Unavailable("srv.smartUnavailable",
			"Smart+ features are paused while the provider recovers",
			llm.OpenFor).WithCause(err)

	case errors.Is(err, llm.ErrBusy):
		// Not the circuit — the in-flight cap. ResourceExhausted rather than
		// Unavailable, because the service is working and this caller is simply
		// over its share, which is a different thing for a client to say.
		return RateLimited("llm_in_flight", 5*time.Second).WithCause(err)

	case errors.Is(err, store.ErrNoKey):
		// No encryption key configured, so a credential cannot be stored. An
		// operator has to fix this and the UI cannot, which is what DocRef is
		// for.
		e := Precondition("srv.noEncryptionKey",
			"this server cannot store credentials until an encryption key is configured")
		e.DocRef = "deploy/RUNBOOK.md#encryption-key"
		return e.WithCause(err)

	default:
		return Internal(err)
	}
}

// FromAuthz classifies an authorization failure.
//
// Split from FromDomain because it needs the caller's tenant to make the one
// decision this package cares about: PermissionDenied is honest for an object
// the caller can see, and a leak for one they cannot. `sameTenant` is that
// question, answered by the handler, which is the only layer that knows.
func FromAuthz(err error, capability string, sameTenant bool) error {
	if err == nil {
		return nil
	}
	if !sameTenant {
		return CrossTenant().WithCause(err)
	}
	return PermissionDenied(capability).WithCause(err)
}
