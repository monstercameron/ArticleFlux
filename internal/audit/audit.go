// Package audit is the wiring between a security-relevant action and the two
// places it has to show up: the durable trail, and an operator's attention.
//
// # Why this package exists at all
//
// `audit_log` was created in migration 0009, `store.Audit` writes it,
// `store.AuditTrail` reads it, and both have been correct since the day they
// were written. Until §7.3c NOTHING CALLED EITHER ONE. Roles could change,
// passwords could be reset, an account could be recovered by a stranger holding
// a code, and the instance recorded none of it. §7.9 described an audit log the
// application did not have.
//
// That is the third mechanism found in this state in one review — recovery
// codes and refresh families were the others — and the shape is always the
// same: a correct component, a correct test for that component, and no caller.
// So this package is deliberately not another component. It is the caller, and
// the only thing it adds is that using it is easier than not using it.
//
// # One call, two outputs, and why they are not separable
//
// A record in a table nobody reads is not a control. A log line with no durable
// record behind it is not evidence. Both are needed, and if they were two calls
// then one of them would eventually be forgotten at a new call site — which is
// the exact failure this package was written in response to.
//
// So `Record` does both. There is no way to write the row without emitting the
// line, and no way to emit the line without writing the row.
//
// # What must never appear in a Detail
//
// Passwords, session tokens, refresh secrets, recovery codes, reset tokens. The
// trail is read by whoever runs the box and, unlike the sessions table, it is
// meant to be long-lived and quotable in an incident report. A credential in it
// is a credential in every backup of it, forever. Usernames, addresses, counts
// and outcomes are the material here.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Action names what happened, as a closed vocabulary.
//
// Constants rather than strings at the call site, because the trail's whole
// value is being able to ask "show me every password reset" six months later —
// and a free-text action produces `password.reset`, `password_reset` and
// `passwordReset` in the same table. They are greppable, which is the point:
// `grep -r audit.Action` finds every kind of event this application can record.
type Action string

const (
	// --- credential lifecycle. The events an account takeover cannot avoid.
	ActionLogin           Action = "auth.login"
	ActionLogout          Action = "auth.logout"
	ActionPasswordChanged Action = "auth.password.changed"
	ActionReauthenticated Action = "auth.reauthenticated"

	// --- recovery (§7.2). Every one of these is somebody getting into an
	// account without its password, which is the highest-value line in the file.
	ActionRecoveryRedeemed    Action = "auth.recovery.redeemed"
	ActionResetRedeemed       Action = "auth.reset.redeemed"
	ActionRecoveryRegenerated Action = "auth.recovery.regenerated"
	ActionResetIssued         Action = "auth.reset.issued"

	// --- refusals worth keeping. Individual failed passwords are NOT here: they
	// live in `login_attempts`, which exists to count them and is purged on a
	// schedule — `App.sweepSecurity`, on the poll cycle, at ninety days by
	// default. That sentence was aspirational for a while and this note is here
	// so it does not become so again: `PurgeLoginAttempts` existed with no
	// production caller, so "purged on a schedule" described a schedule nobody
	// had written. What belongs in the durable trail is the SUMMARY event — the
	// moment a threshold was crossed — because that is the one an operator would
	// want to find later, and one row beats four hundred.
	//
	// This table has a window of its own now too (365 days), for the reason
	// `internal/retention/security.go` argues: a trail that only grows is one an
	// incident report eventually cannot read.
	ActionLockout      Action = "auth.lockout"
	ActionRefreshReuse Action = "auth.refresh.reuse"

	// --- administration, from the shell. An operator acting on somebody else's
	// account is the case §7.9 is most concerned with: it is legitimate, it is
	// invisible from inside the app, and it is indistinguishable from an intruder
	// with filesystem access unless it is written down.
	ActionAccountCreated Action = "account.created"
	ActionPasswordReset  Action = "account.password.reset"
	ActionInstanceClaim  Action = "instance.claimed"
	// ActionKeyRotated is `articleflux rotate-key`: every stored credential
	// re-encrypted and secrets.key replaced.
	//
	// The paragraph above is the whole argument for it, and this is the action it
	// fits best. A rotation is legitimate, it is invisible from inside the app —
	// nothing on any screen changes — and it is indistinguishable from an
	// intruder with filesystem access re-sealing the database under a key of
	// their own.
	//
	// It also has a shape none of the others do: the operation people run it for
	// is a SUSPECTED EXPOSURE. So the moment somebody reads `articleflux audit`
	// in earnest is the moment they most need to see whether the key was rotated,
	// by whom, and when — and until this existed the answer was a timestamped
	// `.old` file in a directory, or nothing.
	ActionKeyRotated Action = "instance.key.rotated"
)

// Severity splits the trail into what is recorded and what is worth waking up
// for.
//
// It does not change what is stored — every event lands in `audit_log`
// identically. It changes the LOG LEVEL, which is the only alerting channel a
// self-hosted box reliably has: journald, a log shipper, or somebody's grep. An
// operator who wants paging wires it to Warn on the `security_event` key and
// gets the events that matter without the ones that do not.
type Severity int

const (
	// Notice is the routine record. Somebody signed in; somebody signed out.
	Notice Severity = iota
	// Alert is an event that changes who can reach the account, or a refusal
	// that means somebody was trying. Logged at Warn: a signed-in reader
	// generates none of these in normal use, so the rate is the signal.
	Alert
)

// severity is the fixed classification, in one table rather than at each call
// site — so "is a password change an alert?" has one answer instead of one per
// caller.
//
// Default is Alert. An action nobody classified is more likely to be a new
// security event than a new routine one, and the cost of being wrong that way
// is a log line at Warn instead of Info. Same fail-loud default as authz's map
// and authn's sudo list.
var severity = map[Action]Severity{
	ActionLogin:  Notice,
	ActionLogout: Notice,
}

// Severity reports how loudly an action should be logged.
func (a Action) Severity() Severity {
	if s, ok := severity[a]; ok {
		return s
	}
	return Alert
}

// Event is one thing that happened.
type Event struct {
	Action Action
	// Actor is the user id responsible. Empty for an unauthenticated actor —
	// somebody redeeming a recovery code is not yet anybody — in which case
	// Subject carries who it was done TO.
	Actor string
	// Subject is the account acted upon, when that differs from the actor: an
	// admin resetting somebody's password, or a recovery that has no actor yet.
	Subject string
	Tenant  string
	// Detail is structured context, JSON-encoded into `detail_json`.
	//
	// NEVER a credential. See the package comment — this table outlives the
	// sessions it describes and is quoted in incident reports.
	Detail map[string]string
}

// Sink is the storage half. `*store.ReaderRepo` satisfies it.
//
// An interface rather than the concrete repo so a test can assert what would be
// written without a database, and so this package does not become a second
// place that knows the schema.
type Sink interface {
	Audit(ctx context.Context, e store.AuditEntry) error
}

// Recorder writes the trail and raises the alarm.
type Recorder struct {
	sink Sink
	log  *slog.Logger
}

// New returns a Recorder. A nil sink or logger is tolerated and turns the
// corresponding half into a no-op, so a caller assembled without one degrades
// rather than panicking on a path somebody reaches once a year.
func New(sink Sink, log *slog.Logger) *Recorder {
	return &Recorder{sink: sink, log: log}
}

// Record writes one event.
//
// # It never returns an error, and that is a decision rather than laziness
//
// `store.Audit`'s own comment sets the rule: an instance that refuses to delete
// a user because it could not write a log line is an instance that cannot be
// administered. The same holds for every caller here — a login must not fail
// because the trail is full, and a password change that already committed
// cannot be un-committed by a logging failure.
//
// So a write failure is logged at Error and swallowed. Returning an error would
// invite exactly one of two mistakes at each of a dozen call sites: failing the
// action, or writing `_ =` and dropping the news that the audit trail is broken.
func (r *Recorder) Record(ctx context.Context, e Event) {
	if r == nil || e.Action == "" {
		return
	}

	// The log line goes out FIRST, and unconditionally.
	//
	// It is the half that reaches a human, and the database is the half more
	// likely to be the thing that is broken — a disk that is full, a schema
	// mid-migration. Emitting the line first means the one event an operator
	// most needs to see survives the failure of the thing they would have read
	// it from.
	if r.log != nil {
		attrs := []any{
			// A stable key, deliberately. This is the string an operator greps or
			// points an alerting rule at, so it is part of the interface: renaming
			// it silently breaks somebody's paging.
			"security_event", string(e.Action),
		}
		if e.Actor != "" {
			attrs = append(attrs, "actor", e.Actor)
		}
		if e.Subject != "" && e.Subject != e.Actor {
			attrs = append(attrs, "subject", e.Subject)
		}
		for k, v := range e.Detail {
			attrs = append(attrs, k, v)
		}
		if e.Action.Severity() == Alert {
			r.log.WarnContext(ctx, "security event", attrs...)
		} else {
			r.log.InfoContext(ctx, "security event", attrs...)
		}
	}

	if r.sink == nil {
		return
	}

	var detail string
	if len(e.Detail) > 0 {
		// Marshalling a map[string]string cannot fail, but checking beats
		// discarding an error to save a line — and a silently empty detail column
		// is the kind of thing nobody notices until they need it.
		if b, err := json.Marshal(e.Detail); err == nil {
			detail = string(b)
		}
	}

	// Subject rides in `acting_as_user_id`, which migration 0009 added for
	// impersonation ("who was this done on behalf of"). An admin resetting a
	// password is the same relationship: one identity acting on another's
	// account. Reusing the column keeps "who was affected" answerable by one
	// query rather than by parsing JSON.
	if err := r.sink.Audit(ctx, store.AuditEntry{
		ActorUserID:  e.Actor,
		ActingAsUser: e.Subject,
		TenantID:     e.Tenant,
		Action:       string(e.Action),
		ObjectKind:   "account",
		ObjectID:     e.Subject,
		Detail:       detail,
	}); err != nil && r.log != nil {
		// Error, not Warn. A failing audit write means the instance is currently
		// unable to record what is happening to it, which is a bigger problem
		// than whatever it failed to record.
		r.log.ErrorContext(ctx, "the audit trail could not be written",
			"security_event", string(e.Action), "err", err)
	}
}
