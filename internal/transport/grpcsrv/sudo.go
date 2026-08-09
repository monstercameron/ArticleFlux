package grpcsrv

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/apierr"
	"github.com/monstercameron/ArticleFlux/internal/audit"
	"github.com/monstercameron/ArticleFlux/internal/authn"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/pwpolicy"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/username"
)

// Sudo mode, enforced (§7.3, TODO 6.1).
//
// `internal/authn` has known which operations need fresh authentication, how
// long fresh lasts, and what to do about an action nobody classified — since the
// policy was written. What it never had was a caller. A policy with no
// enforcement point is a document, and this file is the difference.
//
// # The three pieces, and why it takes three
//
// A session records WHEN its holder last proved who they are (`authenticated_at`,
// migration 0020). Ordinary traffic deliberately does not refresh that stamp: a
// control that demands a password must not be satisfiable by reading articles.
// So there has to be one call whose whole job is to ask again — `Reauthenticate`
// — and the gated operations check the stamp rather than asking for a password
// each. Without the first, sudo can only ever FAIL; without the second, every
// dangerous operation grows its own password field and its own way of getting it
// wrong.
//
// # What is gated today, and what is not yet built
//
// `authn.sudoRequired` lists eight actions. Three of them — changing a password,
// replacing the recovery sheet, and re-authentication itself — have surfaces
// here. The other five (role changes, suspension, impersonation, deletion, a
// full export) are operations this application does not have RPCs for yet. That
// is stated rather than quietly ignored: when those arrive they call
// `requireSudo` and nothing else about this file changes, which is the point of
// putting the check behind one function.

// errSudoRequired tells the client to ask for the password again.
//
// A distinct code from Unauthenticated, and that distinction is the entire
// usefulness of it: `Unauthenticated` means the session is no good and the right
// response is the login screen, while this means the session is fine and the
// right response is a password prompt over the top of what the reader was doing.
// A client that cannot tell them apart logs somebody out for trying to change
// their password.
func errSudoRequired(action authn.SudoAction) error {
	return errKey(codes.PermissionDenied, "srv.sudoRequired",
		"this needs your password again", map[string]string{"action": string(action)})
}

// requireSudo refuses unless the caller re-authenticated recently.
//
// Order matters here. The scope is resolved FIRST, so a caller with no session
// gets "sign in" rather than "confirm your password" — being told to re-enter a
// password you never entered is a dead end.
//
// In DevMode there is no session at all: the scope comes from
// `FirstUserScope`, so there is no token to carry a stamp and no password
// anybody typed. Sudo is skipped there, which is the same trade DevMode already
// makes everywhere else — it is loopback-only and refuses to start otherwise —
// and it is recorded here rather than discovered later by somebody wondering why
// the dev server never asks.
func (s *AuthServer) requireSudo(ctx context.Context, action authn.SudoAction) (store.Scope, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil || !sc.Valid() {
		return store.Scope{}, errKey(codes.Unauthenticated, "srv.noSession", "sign in first", nil)
	}
	if !authn.NeedsSudo(action) {
		return sc, nil
	}
	if s.devMode {
		return sc, nil
	}

	token := bearerToken(ctx)
	if token == "" {
		return store.Scope{}, errSudoRequired(action)
	}
	at, err := s.repo.SessionAuthenticatedAt(ctx, secret.HashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// The session resolved a moment ago and does not now: revoked
			// underneath us, or expired between the two queries. Either way the
			// answer is the login screen, not a password prompt.
			return store.Scope{}, errKey(codes.Unauthenticated, "srv.noSession", "sign in first", nil)
		}
		// A stamp that cannot be read FAILS CLOSED, which is the opposite of the
		// login ledger's choice a few files over — and deliberately. There, an
		// unreadable table would have locked every user out of the instance; here
		// it costs one password prompt on operations somebody performs a handful
		// of times a year.
		s.log.Error("reading the sudo stamp", "err", err)
		return store.Scope{}, errSudoRequired(action)
	}
	if !authn.SudoFresh(at, time.Now().UTC()) {
		return store.Scope{}, errSudoRequired(action)
	}
	return sc, nil
}

// sudoLedgerKey is how a re-authentication attempt is filed in `login_attempts`.
//
// # Why it is not the username
//
// The ledger's per-account count is what Login's lockout curve reads. Filing
// these under the plain username would mean somebody holding a STOLEN session
// could lock the real owner out of logging in, just by guessing wrong at the
// confirmation prompt — turning a control against the thief into a weapon
// against the victim. The in-memory limiter already refused to key on the
// username for that reason; the durable counter has to make the same choice or
// it reintroduces what the limiter avoided, permanently.
//
// So the key is a separate namespace over the same table: `sudo:<user id>`.
// It counts, it persists, it earns the same exponential curve — and it is
// disjoint from the login key, so the two lockouts cannot reach each other.
// A user id rather than a username because it does not change under them.
//
// # Why locking this path is not a way around it
//
// A locked sudo prompt is escaped by logging in again, which re-stamps
// `authenticated_at`. That is not a bypass — it is the intended recovery, and it
// costs the attacker the one thing they do not have. The owner types their
// password and is through; the thief is left with a session that can read
// articles, which is what a session with no fresh authentication is supposed to
// be worth.
func sudoLedgerKey(userID string) string { return "sudo:" + userID }

// Reauthenticate re-proves the password on an existing session.
//
// This is the SECOND password check in the application, and until §7.3b it was
// nothing like the first. Login has four controls in front of it — an in-memory
// limiter, a durable ledger, an exponential lockout and an outcome metric — and
// this had one: a fixed-window counter, in memory, ten a minute, cleared by a
// restart and by every deploy.
//
// That is the wrong end to leave open. The caller here ALREADY HOLDS A SESSION,
// which is the exact circumstance sudo mode exists for, and a hit buys a
// fifteen-minute window in which ChangePassword revokes every other session on
// the account. Ten guesses a minute, forever, with nothing written down, against
// the credential that owns the instance — and the owner's first notice would
// have been being logged out of their own reader.
//
// It now files through the same ledger and the same curve as Login, under a
// namespaced key. See sudoLedgerKey for why the key is not the username.
func (s *AuthServer) Reauthenticate(ctx context.Context, req *pb.ReauthenticateRequest) (
	*pb.ReauthenticateResponse, error) {

	sc, err := s.scopeOf(ctx)
	if err != nil || !sc.Valid() {
		return nil, errKey(codes.Unauthenticated, "srv.noSession", "sign in first", nil)
	}
	token := bearerToken(ctx)
	if token == "" {
		// DevMode has a scope and no session. Nothing to stamp, and nothing to
		// prove — see requireSudo.
		if s.devMode {
			return &pb.ReauthenticateResponse{
				SudoExpiresAt: time.Now().UTC().Add(authn.SudoWindow).Format(time.RFC3339),
			}, nil
		}
		return nil, errKey(codes.Unauthenticated, "srv.noSession", "sign in first", nil)
	}

	// Rate limited on the SESSION, not the username. An attacker guessing here
	// already holds a live session, so the thing worth slowing down is this
	// session's guessing — and keying on the username would let somebody with a
	// stolen session lock the real owner out of logging in, turning a
	// confirmation prompt into a denial of service against its own account.
	key := "s:" + secret.HashToken(token)
	if !s.limiter.allow(key) {
		return nil, errKey(codes.ResourceExhausted, "srv.tooManyAttempts",
			"too many attempts; wait a minute and try again", nil)
	}

	// The durable half, on top of the limiter above, for the same reason Login
	// has both: a limiter blunts a burst and then forgets, and it forgets
	// completely on restart. An attacker who waits for a deploy — or provokes one
	// — gets a fresh budget against a counter with amnesia, which against a
	// password is the whole game.
	ledgerKey, addr := sudoLedgerKey(sc.UserID), clientKey(ctx)
	if d, ok := s.lockout(ctx, ledgerKey, addr); !ok {
		s.log.Warn("re-authentication locked out", "user", sc.UserID, "client", addr,
			"reason", d.Reason, "retry_after", d.RetryAfter)
		s.record(ctx, ledgerKey, addr, store.LoginLocked)
		s.trail.Record(ctx, audit.Event{
			Action: audit.ActionLockout, Actor: sc.UserID, Tenant: sc.TenantID,
			Detail: map[string]string{
				"surface": "reauthenticate", "client": addr,
				"retry_after": d.RetryAfter.String(),
			},
		})
		return nil, apierr.Status(apierr.RateLimited("reauthenticate", d.RetryAfter))
	}

	hash, err := s.repo.PasswordHashFor(ctx, sc)
	if err != nil {
		s.log.Error("reading the password hash for re-authentication", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	ok, _, verr := secret.VerifyPassword(req.GetPassword(), hash, secret.Active())
	if verr != nil || !ok {
		s.limiter.fail(key)
		s.record(ctx, ledgerKey, addr, store.LoginBadPassword)
		// Warn, not Info. A wrong password at a confirmation prompt is somebody
		// holding a live session who cannot produce the password that opened it,
		// and that is worth seeing in a log even once.
		s.log.Warn("re-authentication failed", "user", sc.UserID, "client", addr)
		// Not errBadCredentials: that message names a username, and there is no
		// username in this exchange — the session already said who this is.
		return nil, errKey(codes.Unauthenticated, "srv.badPassword", "that password is not right", nil)
	}
	s.limiter.reset(key)
	// Clears the durable count too — `FailureCounts` reads "since the last ok",
	// so without this row a person who fumbles twice carries those two failures
	// into every future confirmation prompt they ever see.
	s.record(ctx, ledgerKey, addr, store.LoginOK)

	now := time.Now().UTC()
	if err := s.repo.StampAuthenticated(ctx, secret.HashToken(token),
		now.Format(time.RFC3339Nano)); err != nil {
		s.log.Error("stamping the re-authentication", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	// A sudo window opening is worth a row: for the next fifteen minutes this
	// session can change the password and replace the recovery sheet, and if it
	// was not the owner who opened it, this is the line that says when.
	s.trail.Record(ctx, audit.Event{
		Action: audit.ActionReauthenticated, Actor: sc.UserID, Tenant: sc.TenantID,
		Detail: map[string]string{"client": addr},
	})
	return &pb.ReauthenticateResponse{
		SudoExpiresAt: now.Add(authn.SudoWindow).Format(time.RFC3339),
	}, nil
}

// proveCurrentPassword refuses unless the caller can produce the password the
// account currently has.
//
// # Why this exists alongside requireSudo
//
// The sudo stamp is written at LOGIN, so for fifteen minutes after somebody
// signs in the window is open without anyone having typed a password since. For
// most gated operations that is the intended trade — the point of a window is
// not to ask four times. For the two operations that change the CREDENTIAL
// ITSELF it is a hole: a live but unattended session could rename the account or
// replace its password and, because the password change revokes every other
// session, lock the owner out using the owner's own credential.
//
// So those two ask, every time, on top of sudo. That is deliberately the
// opposite of what ChangePassword's proto comment argued before 2026-08-09, and
// the older reasoning is worth keeping in view rather than deleting: asking for
// a password twice in fifteen minutes really does train people to type it into
// whatever asks, and that argument holds for prompts people meet often. Changing
// your password is rare and deliberate, and the habit it builds — that changing
// a credential requires the old one — is the one worth having.
//
// Everything Reauthenticate learned about being guessed at applies here and is
// shared rather than re-derived: the session-keyed in-memory limiter, the
// durable ledger under `sudo:<user id>`, the exponential lockout, and a row in
// the audit trail when it locks. A caller guessing here already holds a session
// and is guessing at the credential that owns the instance, which is the worst
// place in the application to leave a counter with amnesia.
//
// DevMode skips it, for requireSudo's reason: there is no session, no stored
// token and no password anybody typed, so there is nothing to prove against.
func (s *AuthServer) proveCurrentPassword(ctx context.Context, sc store.Scope, password string) error {
	if s.devMode {
		return nil
	}
	token := bearerToken(ctx)
	if token == "" {
		return errKey(codes.Unauthenticated, "srv.noSession", "sign in first", nil)
	}
	if password == "" {
		return errKey(codes.Unauthenticated, "srv.badPassword", "that password is not right", nil)
	}

	key := "s:" + secret.HashToken(token)
	if !s.limiter.allow(key) {
		return errKey(codes.ResourceExhausted, "srv.tooManyAttempts",
			"too many attempts; wait a minute and try again", nil)
	}
	ledgerKey, addr := sudoLedgerKey(sc.UserID), clientKey(ctx)
	if d, ok := s.lockout(ctx, ledgerKey, addr); !ok {
		s.log.Warn("credential change locked out", "user", sc.UserID, "client", addr,
			"reason", d.Reason, "retry_after", d.RetryAfter)
		s.record(ctx, ledgerKey, addr, store.LoginLocked)
		s.trail.Record(ctx, audit.Event{
			Action: audit.ActionLockout, Actor: sc.UserID, Tenant: sc.TenantID,
			Detail: map[string]string{
				"surface": "credential-change", "client": addr,
				"retry_after": d.RetryAfter.String(),
			},
		})
		return apierr.Status(apierr.RateLimited("credential-change", d.RetryAfter))
	}

	hash, err := s.repo.PasswordHashFor(ctx, sc)
	if err != nil {
		s.log.Error("reading the password hash to confirm a credential change", "err", err)
		return errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	ok, _, verr := secret.VerifyPassword(password, hash, secret.Active())
	if verr != nil || !ok {
		s.limiter.fail(key)
		s.record(ctx, ledgerKey, addr, store.LoginBadPassword)
		s.log.Warn("credential change refused: wrong password",
			"user", sc.UserID, "client", addr)
		return errKey(codes.Unauthenticated, "srv.badPassword", "that password is not right", nil)
	}
	s.limiter.reset(key)
	// Clears the durable count for the same reason Reauthenticate clears it:
	// `FailureCounts` reads "since the last ok", so two fumbles would otherwise
	// follow this person into every prompt they ever see again.
	s.record(ctx, ledgerKey, addr, store.LoginOK)
	return nil
}

// ChangePassword replaces the caller's password and ends every other session.
func (s *AuthServer) ChangePassword(ctx context.Context, req *pb.ChangePasswordRequest) (
	*pb.ChangePasswordResponse, error) {

	sc, err := s.requireSudo(ctx, authn.SudoChangePasswd)
	if err != nil {
		return nil, err
	}

	// The old password, before anything is written. Ordered ahead of the policy
	// check on purpose: a caller who cannot prove who they are should learn
	// nothing about which passwords this instance would have accepted.
	if err := s.proveCurrentPassword(ctx, sc, req.GetCurrentPassword()); err != nil {
		return nil, err
	}

	username, _, err := s.repo.Identity(ctx, sc)
	if err != nil {
		s.log.Error("reading the identity for a password change", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	// The same policy the CLI applies, including the username check: a password
	// that contains the account name is the one an attacker guesses first, and
	// the check only works where the username is known — which is here.
	if err := pwpolicy.Check(req.GetNewPassword(), username); err != nil {
		return nil, errKey(codes.InvalidArgument, "srv.weakPassword", err.Error(), nil)
	}

	hash, err := secret.HashPassword(req.GetNewPassword(), secret.Active())
	if err != nil {
		s.log.Error("hashing a new password", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}

	// The family tied to THIS session, so it survives alongside the session
	// itself — the person who just proved their password again should not be
	// logged out of their own device for doing so. ErrNotFound (no family, or
	// none live) just means there is nothing to except: every other family
	// still goes.
	keepFamily, ferr := s.repo.FamilyForSession(ctx, secret.HashToken(bearerToken(ctx)))
	if ferr != nil {
		keepFamily = ""
	}

	// One transaction (§7.3a SEC3): the hash, every other session, and every
	// other refresh family commit together or not at all. The previous shape
	// stored the hash and revoked sessions as two writes, and reported success
	// with an invented zero count when the second failed — which told a reader
	// they had ended a thief's session when they had not. Here a failure at any
	// point leaves the OLD password and OLD credentials consistently live,
	// never half of each, and the RPC fails loudly instead of lying.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	ended, familiesEnded, err := s.repo.ChangePasswordAndRevoke(ctx, sc.UserID, hash,
		secret.HashToken(bearerToken(ctx)), keepFamily, now)
	if err != nil {
		s.log.Error("changing password and revoking sessions", "err", err, "user", sc.UserID)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	s.log.InfoContext(ctx, "password changed", "user", sc.UserID, "sessions_ended", ended,
		"families_ended", familiesEnded)
	s.trail.Record(ctx, audit.Event{
		Action: audit.ActionPasswordChanged, Actor: sc.UserID, Tenant: sc.TenantID,
		Detail: map[string]string{
			"client":         clientKey(ctx),
			"sessions_ended": strconv.FormatInt(ended, 10),
			"families_ended": strconv.FormatInt(familiesEnded, 10),
		},
	})
	return &pb.ChangePasswordResponse{SessionsEnded: int32(ended)}, nil
}

// RegenerateRecoveryCodes issues a fresh sheet and discards the old one.
func (s *AuthServer) RegenerateRecoveryCodes(ctx context.Context, _ *pb.RegenerateRecoveryCodesRequest) (
	*pb.RegenerateRecoveryCodesResponse, error) {

	sc, err := s.requireSudo(ctx, authn.SudoRecoveryCodes)
	if err != nil {
		return nil, err
	}

	// Named `sheet` rather than `codes`, which is the grpc status package here.
	sheet, err := authn.GenerateRecoveryCodes(authn.RecoveryCodeCount)
	if err != nil {
		s.log.Error("generating recovery codes", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	// recoveryCodeHash, NOT secret.HashToken: the stored hash has to be over the
	// same normalised form redemption computes, or no code ever matches. See its
	// comment — that mismatch is why every sheet issued before §7.3b was
	// unredeemable.
	hashes := make([]string, 0, len(sheet))
	for _, c := range sheet {
		hashes = append(hashes, recoveryCodeHash(c))
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, sc, hashes); err != nil {
		s.log.Error("storing recovery codes", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	s.log.InfoContext(ctx, "recovery codes regenerated", "user", sc.UserID, "count", len(sheet))
	// This decides who can get back in WITHOUT a password. Somebody on a borrowed
	// session minting themselves a permanent way back is precisely the scenario
	// sudo mode guards, and this is the row that shows it happened.
	s.trail.Record(ctx, audit.Event{
		Action: audit.ActionRecoveryRegenerated, Actor: sc.UserID, Tenant: sc.TenantID,
		Detail: map[string]string{
			"client": clientKey(ctx), "count": strconv.Itoa(len(sheet)),
		},
	})
	return &pb.RegenerateRecoveryCodesResponse{Codes: sheet}, nil
}

// ChangeUsername renames the caller's account.
//
// # Why it is in this file
//
// A username is half a credential. An attacker who can change it silently has
// changed what the owner must type to get in, and on an instance whose recovery
// address IS the username (see internal/username) they have also changed where
// "reset my password" arrives. So it takes the same two proofs the password
// change takes — a sudo window and the current password — and lands in the same
// audit trail.
//
// # What it does NOT do
//
// It does not revoke sessions. The password is unchanged and the sessions were
// issued to an account rather than to a string, so ending them would sign
// somebody out of their phone for correcting a typo. ChangePassword revokes
// because the old credential may be in someone else's hands; this one has no
// such implication and should not borrow its consequences.
func (s *AuthServer) ChangeUsername(ctx context.Context, req *pb.ChangeUsernameRequest) (
	*pb.ChangeUsernameResponse, error) {

	sc, err := s.requireSudo(ctx, authn.SudoChangePasswd)
	if err != nil {
		return nil, err
	}
	if err := s.proveCurrentPassword(ctx, sc, req.GetCurrentPassword()); err != nil {
		return nil, err
	}

	// Normalised BEFORE it is checked, because the normalised form is what gets
	// stored — validating one string and writing another is how a rule ends up
	// guarding nothing.
	name := username.Normalise(req.GetNewUsername())
	if err := username.Check(name); err != nil {
		return nil, errKey(codes.InvalidArgument, "srv.badUsername", err.Error(), nil)
	}

	current, _, ierr := s.repo.Identity(ctx, sc)
	if ierr != nil {
		s.log.Error("reading the identity for a rename", "err", ierr)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	// A rename to the name it already has is a no-op rather than an error. The
	// reader asked for a state, and the state is already true; refusing would be
	// the interface arguing about how they got there.
	if strings.EqualFold(current, name) {
		return &pb.ChangeUsernameResponse{Username: current}, nil
	}

	if err := s.repo.UpdateUsername(ctx, sc, name); err != nil {
		if errors.Is(err, store.ErrUsernameTaken) {
			return nil, errKey(codes.AlreadyExists, "srv.usernameTaken",
				"that username is already in use", nil)
		}
		s.log.Error("renaming an account", "err", err, "user", sc.UserID)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	s.log.InfoContext(ctx, "username changed", "user", sc.UserID)
	// The OLD name is recorded and the new one is not, which is the way round
	// that helps: the row is filed under the account id, so what it is called now
	// is a lookup away, and what it USED to be called is the thing that would
	// otherwise be gone. An operator reading this file after a takeover is trying
	// to work out what the account was when they last recognised it.
	s.trail.Record(ctx, audit.Event{
		Action: audit.ActionUsernameChanged, Actor: sc.UserID, Tenant: sc.TenantID,
		Detail: map[string]string{"client": clientKey(ctx), "previous": current},
	})
	return &pb.ChangeUsernameResponse{Username: name}, nil
}

// passphraseMatches verifies a recovery passphrase against the stored hash.
//
// Argon2id, deliberately — see the migration and pwpolicy.CheckPassphrase. The
// caller is responsible for the rate limiting, and RedeemRecoveryCode already
// has it: the same ledger, the same curve and the same uniform refusal a wrong
// code gets, so a passphrase attempt is indistinguishable from a code attempt
// from outside.
//
// An account with no passphrase answers false rather than an error. "No
// passphrase configured" and "wrong passphrase" must look identical from the
// wire, or the refusal becomes a probe for which accounts have one.
func (s *AuthServer) passphraseMatches(ctx context.Context, userID, candidate string) (bool, error) {
	if candidate == "" {
		return false, nil
	}
	hash, err := s.repo.RecoveryPassphraseHash(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	ok, _, verr := secret.VerifyPassword(candidate, hash, secret.Active())
	if verr != nil {
		return false, verr
	}
	return ok, nil
}

// SetRecoveryPassphrase stores, replaces or clears the caller's passphrase.
//
// Sudo AND the current password, like the other two credential changes: setting
// one is cutting a new key to the account, and the post-login window would
// otherwise let an unattended session cut itself one that outlives the session.
//
// An empty passphrase clears it, which is how the reader turns the feature off.
// The alternative — a second RPC with the same guards and the same shape — is
// two ways to be in the wrong state.
func (s *AuthServer) SetRecoveryPassphrase(ctx context.Context, req *pb.SetRecoveryPassphraseRequest) (
	*pb.SetRecoveryPassphraseResponse, error) {

	sc, err := s.requireSudo(ctx, authn.SudoChangePasswd)
	if err != nil {
		return nil, err
	}
	if err := s.proveCurrentPassword(ctx, sc, req.GetCurrentPassword()); err != nil {
		return nil, err
	}

	phrase := req.GetPassphrase()
	if strings.TrimSpace(phrase) == "" {
		if cerr := s.repo.ClearRecoveryPassphrase(ctx, sc); cerr != nil {
			s.log.Error("clearing a recovery passphrase", "err", cerr)
			return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
		}
		s.trail.Record(ctx, audit.Event{
			Action: audit.ActionRecoveryPassphrase, Actor: sc.UserID, Tenant: sc.TenantID,
			Detail: map[string]string{"client": clientKey(ctx), "state": "cleared"},
		})
		return &pb.SetRecoveryPassphraseResponse{Configured: false}, nil
	}

	username, _, ierr := s.repo.Identity(ctx, sc)
	if ierr != nil {
		s.log.Error("reading the identity for a passphrase change", "err", ierr)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	// The current password goes in, so "the same as your password" can be
	// refused — the two credentials exist so that losing one leaves the other,
	// and a copy of the password survives nothing the password does not.
	if err := pwpolicy.CheckPassphrase(phrase, username, req.GetCurrentPassword()); err != nil {
		return nil, errKey(codes.InvalidArgument, "srv.weakPassphrase", err.Error(), nil)
	}

	hash, herr := secret.HashPassword(phrase, secret.Active())
	if herr != nil {
		s.log.Error("hashing a recovery passphrase", "err", herr)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	if serr := s.repo.SetRecoveryPassphrase(ctx, sc, hash); serr != nil {
		s.log.Error("storing a recovery passphrase", "err", serr)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	s.log.InfoContext(ctx, "recovery passphrase set", "user", sc.UserID)
	// The same weight as regenerating the sheet: this decides who can get back
	// into the account WITHOUT a password, which is the highest-value line in
	// the file.
	s.trail.Record(ctx, audit.Event{
		Action: audit.ActionRecoveryPassphrase, Actor: sc.UserID, Tenant: sc.TenantID,
		Detail: map[string]string{"client": clientKey(ctx), "state": "set"},
	})
	return &pb.SetRecoveryPassphraseResponse{Configured: true}, nil
}
