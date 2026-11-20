package apierr

import (
	"errors"
	"strconv"
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

	var text *store.TextError
	switch {
	// What a reader typed was refused, and the refusal knows which limit.
	case errors.As(err, &text):
		return fromTextError(text).WithCause(err)

	// A row in another tenant returns ErrNotFound from the repositories, which
	// is the half of the cross-tenant rule the store implements. This is the
	// other half: it stays NotFound all the way to the client.
	case errors.Is(err, store.ErrNotFound):
		return NotFound().WithCause(err)

	case errors.Is(err, store.ErrNoScope):
		return Unauthenticated().WithCause(err)

	case errors.Is(err, store.ErrBadCursor), errors.Is(err, store.ErrCursorSpecMismatch):
		return StaleCursor().WithCause(err)

	case errors.Is(err, store.ErrBadTimestamp):
		// A caller's field, not a server fault: Internal here would tell the
		// client to retry something that will fail identically forever.
		//
		// No field is attached. More than one field reaches this — a mute
		// expiry, a mark-all-read cutoff — and naming one of them would have a
		// form highlight the wrong input on the other. The wrapped error names
		// the field it was, which is what an operator reading the log needs.
		return New(KindInvalidArgument, "srv.badTimestamp",
			"That is not a time this server can store. Send an RFC3339 timestamp.").
			WithCause(err)

	case errors.Is(err, store.ErrIdemConflict):
		// The same idempotency key with a DIFFERENT request body. Not a replay
		// to be answered from the cache — a client bug or a key collision, and
		// answering it with the stored response would apply neither operation
		// the caller asked for.
		return Precondition("srv.idempotencyConflict",
			"This request was already used for something else.").WithCause(err)

	case errors.Is(err, store.ErrInviteInvalid):
		// Deliberately not distinguishing expired, spent and never-existed: the
		// difference tells someone holding a guessed code whether it was real.
		return Invalid("code", "srv.inviteInvalid",
			"That invitation is not valid.").WithCause(err)

	case errors.Is(err, store.ErrBadResetToken):
		return Invalid("token", "srv.resetTokenInvalid",
			"That reset link is not valid.").WithCause(err)

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
			"This account name exists in more than one workspace.").WithCause(err)

	case errors.Is(err, llm.ErrCircuitOpen):
		// §22.8. The retry hint is the breaker's own cooling period rather than
		// a guess: a client that retries sooner is refused again, and one that
		// waits longer than it needs to makes the feature look broken after the
		// provider recovered.
		return Unavailable("srv.smartUnavailable",
			"Smart+ features are paused while the provider recovers.",
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
			"This server cannot store credentials until an encryption key is configured.")
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

// fromTextError turns a refused name or note into a keyed InvalidArgument that
// names its limit.
//
// One key per (subject, problem) rather than one generic "too long" with the
// subject as an argument, because the sentence differs in more than a noun in
// the languages the catalog is translated into — and "A tag label can be up to
// 48 characters" is the message, where "That is too long" is the complaint the
// audit (G19) made. The limit is the only argument: it is a server constant, and
// the reader's own text is not sent back to them in a status.
func fromTextError(e *store.TextError) *Error {
	max := strconv.Itoa(e.Max)
	args := map[string]string{"max": max}
	switch {
	case e.Subject == store.SubjectFolder && e.Problem == store.ProblemEmpty:
		return Invalid("name", "srv.folderNameEmpty", "A folder needs a name.")
	case e.Subject == store.SubjectFolder && e.Problem == store.ProblemTooLong:
		return Invalid("name", "srv.folderNameTooLong",
			"A folder name can be up to "+max+" characters.").WithArgs(args)
	case e.Subject == store.SubjectFolder && e.Problem == store.ProblemTaken:
		return Invalid("name", "srv.folderNameTaken", "You already have a folder with that name.")
	case e.Subject == store.SubjectTag && e.Problem == store.ProblemEmpty:
		return Invalid("name", "srv.tagEmpty", "A tag needs a name.")
	case e.Subject == store.SubjectTag && e.Problem == store.ProblemTooLong:
		return Invalid("name", "srv.tagTooLong",
			"A tag can be up to "+max+" characters.").WithArgs(args)
	case e.Subject == store.SubjectTagLabel && e.Problem == store.ProblemTooLong:
		return Invalid("label", "srv.tagLabelTooLong",
			"A tag label can be up to "+max+" characters.").WithArgs(args)
	case e.Subject == store.SubjectNote && e.Problem == store.ProblemTooLong:
		return Invalid("body", "srv.noteTooLong",
			"A note can be up to "+max+" characters.").WithArgs(args)
	}
	// A combination nobody has named yet is a bug in the caller of this function,
	// and has no catalog key: the safe Internal default beats a key the
	// client would render as its own identifier.
	return Internal(e)
}
