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
	"github.com/monstercameron/ArticleFlux/internal/idgen"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/pwpolicy"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Account recovery (§7.2), which had every piece except a way in.
//
// # What was actually missing
//
// The storage has been right since 6.1 and remains untouched here: codes are
// hashed, single-use is enforced inside the UPDATE's own WHERE so two concurrent
// redemptions cannot both win, regenerating replaces rather than appends, and
// reset tokens expire and invalidate their predecessors. What did not exist was
// a caller. `ConsumeRecoveryCode` and `ConsumeResetToken` had none — not in an
// RPC, not in the CLI — so Setup printed a sheet of ten codes, told the reader to
// keep it somewhere safe, and nothing in the application would ever look at one.
//
// That is worse than shipping no recovery at all. A reader who has been handed
// recovery does not arrange another way back in, and finds out which kind of
// promise it was on the day they need it — locked out of a self-hosted box with
// nobody to file a support request with.
//
// # Both paths converge, and that is the point
//
// A recovery code and an admin's reset token are different proofs of the same
// claim: this person is entitled to this account without presenting its
// password. Once either is verified, everything after it must be identical — the
// same policy on the new password, the same total revocation, the same session.
// Two copies of "and then reset the account" is how one of them ends up keeping
// the old sessions alive. So they share `completeRecovery`, and each handler's
// only job is to prove the claim.
//
// # The revocation is total, with no exception for the caller
//
// `ChangePassword` keeps the calling session and its refresh family, because the
// person who just re-proved their password should not be logged out for doing
// so. Recovery is the opposite case and takes the opposite decision: the account
// is being recovered FROM a lost or stolen credential, the caller had no session
// to begin with, and anything already holding one is exactly what this is meant
// to eject. `ChangePasswordAndRevoke` is passed empty keeps, which is the shape
// its own comment describes as belonging to a break-glass reset.

// recoveryCodeHash is how a recovery code is stored and how it is checked, and
// it must be the SAME function in both places.
//
// # The bug this exists to have already fixed
//
// `authn.NormalizeRecoveryCode` folds a typed code back to canonical form: upper
// case, dashes and spaces removed, and Crockford's O→0 / I→1 / L→1 mapping so a
// letter somebody wrote for a digit still works. That is the entire reason the
// codes are Crockford base32 — they get written on paper and typed back months
// later by somebody already locked out.
//
// Minting stored `HashToken(code)` over the PRESENTED form, dashes and all,
// while redemption computed `HashToken(Normalize(code))`. Those never agree, so
// every recovery code ever issued by this application was unredeemable — a
// correctly typed code and a garbage one produced exactly the same refusal.
//
// It survived because nothing redeemed a code: the mint side had tests, the
// normalise side had tests, and no test crossed the gap because there was no
// code path that crossed it either. The fix is not "call Normalize in the right
// places" — it is that there is one function, so the two sides cannot describe
// the credential differently.
func recoveryCodeHash(code string) string {
	return secret.HashToken(authn.NormalizeRecoveryCode(code))
}

// recoveryLedgerKey namespaces recovery attempts in `login_attempts`.
//
// Same reasoning as sudoLedgerKey: this is a second guessing surface on an
// account, and filing it under the plain username would let somebody hammering
// recovery codes lock the owner out of ordinary login — locking the door the
// owner would use to fix things. `recover:<lowercased username>` counts
// separately, earns the same curve, and cannot reach the login counter.
//
// The username rather than a user id, because this path runs BEFORE the account
// is resolved and has to be able to count attempts against names that do not
// exist. That is the same reason `login_attempts` has no foreign key on the
// column.
func recoveryLedgerKey(username string) string {
	return "recover:" + strings.ToLower(strings.TrimSpace(username))
}

// resetLedgerKey namespaces reset-token redemption by ADDRESS.
//
// There is no username in the request — the token names the account, and asking
// for one alongside would add a field an attacker can vary while proving
// nothing. So there is no per-account key available here before the token is
// verified, and the per-address counter is what remains. That is adequate for
// what this guards: a 256-bit token has no keyspace worth searching, and the
// counter exists to stop somebody grinding at the endpoint, not to protect a
// secret that guessing could reach.
func resetLedgerKey(addr string) string { return "reset:" + addr }

// RedeemRecoveryCode spends one entry from the printed sheet and resets the
// account (§7.2).
func (s *AuthServer) RedeemRecoveryCode(ctx context.Context, req *pb.RedeemRecoveryCodeRequest) (
	*pb.RedeemRecoveryCodeResponse, error) {

	username := strings.TrimSpace(req.GetUsername())
	code := strings.TrimSpace(req.GetCode())
	if username == "" || code == "" {
		return nil, errBadRecovery
	}

	ledgerKey, addr := recoveryLedgerKey(username), clientKey(ctx)
	uKey, cKey := "r:"+ledgerKey, "c:"+addr
	if !s.limiter.allow(uKey) || !s.limiter.allow(cKey) {
		s.log.Warn("recovery rate limited", "username", username, "client", addr)
		return nil, errKey(codes.ResourceExhausted, "srv.tooManyAttempts",
			"too many attempts; wait a minute and try again", nil)
	}
	if d, ok := s.lockout(ctx, ledgerKey, addr); !ok {
		s.log.Warn("recovery locked out", "username", username, "client", addr,
			"reason", d.Reason, "retry_after", d.RetryAfter)
		s.record(ctx, ledgerKey, addr, store.LoginLocked)
		s.trail.Record(ctx, audit.Event{
			Action: audit.ActionLockout,
			Detail: map[string]string{
				"surface": "recovery", "username": username, "client": addr,
				"retry_after": d.RetryAfter.String(),
			},
		})
		return nil, apierr.Status(apierr.RateLimited("recovery", d.RetryAfter))
	}
	fail := func() {
		s.limiter.fail(uKey)
		s.limiter.fail(cKey)
		s.record(ctx, ledgerKey, addr, store.LoginBadPassword)
	}

	// The password is checked BEFORE the code is spent. A code burned on a
	// rejected password would be a recovery attempt that cost the reader one of
	// ten chances and got them nothing — and they are already having a bad day.
	//
	// It leaks nothing: the policy is a client-side-knowable constant, and
	// anybody can learn it from the setup screen without a code at all.
	if err := pwpolicy.Check(req.GetNewPassword(), username); err != nil {
		return nil, errKey(codes.InvalidArgument, "srv.weakPassword", err.Error(), nil)
	}

	u, lookupErr := s.repo.UserForLogin(ctx, username)
	if lookupErr != nil {
		if errors.Is(lookupErr, store.ErrAmbiguousUser) {
			// D12 arriving as an outage, exactly as it does on Login: a username is
			// unique per tenant, so with a second tenant it stops identifying an
			// account. Not a credential problem and not one the person typing can
			// fix, so it does not get the uniform refusal.
			s.log.Error("recovery is ambiguous: username exists in more than one tenant",
				"username", username)
			return nil, errKey(codes.FailedPrecondition, "srv.ambiguousUser",
				"this username exists in more than one tenant; the server cannot tell them apart", nil)
		}
		// Same answer as a wrong code, for the same reason Login answers one thing
		// to every failure: distinguishing them is a free account-enumeration API,
		// and here it would also say which accounts have recovery configured.
		fail()
		return nil, errBadRecovery
	}

	ok, err := s.repo.ConsumeRecoveryCode(ctx, u.UserID, recoveryCodeHash(code))
	if err != nil {
		s.log.Error("consuming a recovery code", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	if !ok {
		fail()
		s.log.Warn("recovery code rejected", "username", username, "client", addr)
		return nil, errBadRecovery
	}

	out, err := s.completeRecovery(ctx, u, req.GetNewPassword())
	if err != nil {
		return nil, err
	}
	s.limiter.reset(uKey)
	s.limiter.reset(cKey)
	s.record(ctx, ledgerKey, addr, store.LoginOK)

	remaining, cErr := s.repo.RecoveryCodesRemaining(ctx,
		store.Scope{TenantID: u.TenantID, UserID: u.UserID, Role: u.Role})
	if cErr != nil {
		// Non-fatal. The recovery SUCCEEDED; failing it now over a count used to
		// draw a sentence would undo a completed password reset to report a
		// cosmetic number.
		s.log.Warn("counting the remaining recovery codes", "err", cErr)
	}

	s.log.InfoContext(ctx, "account recovered with a recovery code", "username", u.Username,
		"client", addr, "sessions_ended", out.sessionsEnded, "codes_remaining", remaining)
	// The highest-value line in the file: somebody just entered an account
	// without its password. Actor is the recovered user — they are who the new
	// session belongs to — and the detail says how, from where, and what it cost
	// the account, which is everything an owner needs to tell "that was me" from
	// "that was not".
	s.trail.Record(ctx, audit.Event{
		Action: audit.ActionRecoveryRedeemed, Actor: u.UserID, Tenant: u.TenantID,
		Detail: map[string]string{
			"username": u.Username, "client": addr,
			"sessions_ended":  strconv.FormatInt(out.sessionsEnded, 10),
			"codes_remaining": strconv.Itoa(remaining),
		},
	})
	return &pb.RedeemRecoveryCodeResponse{
		Token:           out.token,
		ExpiresAt:       out.expiresAt,
		Username:        u.Username,
		Role:            u.Role,
		CodesRemaining:  int32(remaining),
		SessionsEnded:   int32(out.sessionsEnded),
		RefreshRecordId: out.refreshRecord,
		RefreshToken:    out.refreshToken,
	}, nil
}

// RedeemResetToken completes an admin-minted reset (§7.2).
func (s *AuthServer) RedeemResetToken(ctx context.Context, req *pb.RedeemResetTokenRequest) (
	*pb.RedeemResetTokenResponse, error) {

	token := strings.TrimSpace(req.GetToken())
	if token == "" {
		return nil, errBadRecovery
	}

	addr := clientKey(ctx)
	ledgerKey := resetLedgerKey(addr)
	cKey := "c:" + addr
	if !s.limiter.allow(cKey) {
		return nil, errKey(codes.ResourceExhausted, "srv.tooManyAttempts",
			"too many attempts; wait a minute and try again", nil)
	}
	if d, ok := s.lockout(ctx, ledgerKey, addr); !ok {
		s.record(ctx, ledgerKey, addr, store.LoginLocked)
		return nil, apierr.Status(apierr.RateLimited("recovery", d.RetryAfter))
	}

	// The token is consumed before the password is validated, which is the
	// opposite order to the recovery-code path above, and deliberately.
	//
	// A reset token names an account and nothing else names it, so there is no
	// username to hand `pwpolicy.Check` until the token has been spent — and the
	// username check ("a password cannot be your username") is the part of the
	// policy that a generic list cannot replace. Consuming first costs a caller
	// who then fails the policy one token, which an admin can mint again in a
	// second; checking first would cost the check.
	userID, err := s.repo.ConsumeResetToken(ctx, secret.HashToken(token))
	if err != nil {
		if !errors.Is(err, store.ErrBadResetToken) {
			s.log.Error("consuming a reset token", "err", err)
			return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
		}
		s.limiter.fail(cKey)
		s.record(ctx, ledgerKey, addr, store.LoginBadPassword)
		s.log.WarnContext(ctx, "reset token rejected", "client", addr)
		return nil, errBadRecovery
	}

	u, err := s.repo.UserForRecovery(ctx, userID)
	if err != nil {
		// The token was live and its account is gone or deactivated. Not the
		// caller's fault and not something they can act on, but not a way in
		// either.
		s.log.Warn("a reset token named an account that cannot be recovered",
			"user", userID, "err", err)
		return nil, errBadRecovery
	}

	if err := pwpolicy.Check(req.GetNewPassword(), u.Username); err != nil {
		return nil, errKey(codes.InvalidArgument, "srv.weakPassword", err.Error(), nil)
	}

	out, err := s.completeRecovery(ctx, u, req.GetNewPassword())
	if err != nil {
		return nil, err
	}
	s.limiter.reset(cKey)
	s.record(ctx, ledgerKey, addr, store.LoginOK)

	s.log.InfoContext(ctx, "account recovered with a reset token", "username", u.Username,
		"client", addr, "sessions_ended", out.sessionsEnded)
	s.trail.Record(ctx, audit.Event{
		Action: audit.ActionResetRedeemed, Actor: u.UserID, Tenant: u.TenantID,
		Detail: map[string]string{
			"username": u.Username, "client": addr,
			"sessions_ended": strconv.FormatInt(out.sessionsEnded, 10),
		},
	})
	return &pb.RedeemResetTokenResponse{
		Token:           out.token,
		ExpiresAt:       out.expiresAt,
		Username:        u.Username,
		Role:            u.Role,
		SessionsEnded:   int32(out.sessionsEnded),
		RefreshRecordId: out.refreshRecord,
		RefreshToken:    out.refreshToken,
	}, nil
}

// errBadRecovery is the single answer to every failed redemption.
//
// One message for an unknown username, a wrong code, a spent code, an expired
// token and a deactivated account alike — the same discipline as
// errBadCredentials, and for a sharper reason: the distinctions here would say
// which accounts exist AND which of them have recovery set up, which is a map of
// where to spend effort.
var errBadRecovery = errKey(codes.Unauthenticated, "srv.badRecovery",
	"that recovery code or reset link is not valid", nil)

// recoveryResult is what both paths need back from the shared half.
type recoveryResult struct {
	token     string
	expiresAt string
	// The refresh half (§7.3a SEC4). Empty on an instance that does not issue
	// refresh tokens, which is a supported state and not a failure.
	//
	// Carried here rather than minted at the two call sites, because they are
	// the same act with two different proofs in front of it — and a refresh
	// family minted in one and forgotten in the other is a reader whose
	// recovered session dies in twelve hours for reasons nobody can see.
	refreshRecord string
	refreshToken  string
	sessionsEnded int64
}

// completeRecovery is everything after the proof: set the password, revoke
// everything, mint a session.
//
// The order is load-bearing. `ChangePasswordAndRevoke` is one transaction, so
// the new password and the death of every old session commit together or not at
// all — a reset that stored the password and then failed to revoke would tell
// somebody they had ejected an intruder when they had not. The new session is
// created AFTER it, so it cannot be caught by the revocation it follows.
func (s *AuthServer) completeRecovery(ctx context.Context, u store.LoginUser,
	newPassword string) (recoveryResult, error) {

	hash, err := secret.HashPassword(newPassword, secret.Active())
	if err != nil {
		s.log.Error("hashing a recovered password", "err", err)
		return recoveryResult{}, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}

	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)

	// Empty keeps: revoke everything. See the file comment — the caller had no
	// session, and what already holds one is what this is ejecting.
	ended, familiesEnded, err := s.repo.ChangePasswordAndRevoke(ctx, u.UserID, hash, "", "", stamp)
	if err != nil {
		s.log.Error("resetting the password during recovery", "err", err, "user", u.UserID)
		return recoveryResult{}, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}

	token := idgen.Token()
	// The same two-lifetimes rule as Login: twelve hours when the client can
	// renew, thirty days when it cannot. Without the family registered below
	// this would have been a session that ends before lunch on the one path
	// where being logged out again is cruellest.
	expires := now.Add(s.accessTTL())
	// One record id for both the session and the family, as at Login: the
	// session's DeviceID and the devices row have to name each other or the
	// refresh would rotate a family the session is not part of.
	record := idgen.DeviceID()
	if err := s.repo.CreateSession(ctx, store.NewSession{
		SessionID: idgen.New(),
		UserID:    u.UserID,
		TenantID:  u.TenantID,
		TokenHash: secret.HashToken(token),
		DeviceID:  record,
		UserAgent: userAgent(ctx),
		Now:       stamp,
		ExpiresAt: expires.Format(time.RFC3339Nano),
		// Setting a password IS an authentication, exactly as at Setup and Login,
		// so the sudo window opens here. Somebody who just proved possession of a
		// recovery code and chose a new password has done more than a login
		// requires; asking them to type it again to reach their account settings
		// would be theatre.
		AuthenticatedAt: stamp,
	}); err != nil {
		// The password IS already changed and every session IS already revoked.
		// Reporting failure is correct — the caller has no session — and the
		// account is in a good state: they can sign in with the password they just
		// set. Saying so beats a generic error on a screen where the reader cannot
		// tell whether anything happened.
		s.log.Error("creating the session after recovery", "err", err, "user", u.UserID)
		return recoveryResult{}, errKey(codes.Internal, "srv.recoveredNoSession",
			"your password was reset, but signing you in failed; sign in with the new password", nil)
	}

	// The device family, gated exactly as Login's is. Non-fatal for the same
	// reason: the session above is already valid, and refusing a completed
	// recovery over renewal bookkeeping would turn a renewal problem into "your
	// password was reset but you are not signed in".
	//
	// A fresh family, necessarily — `ChangePasswordAndRevoke` above just killed
	// every one this account had, which is the point of a recovery.
	var refresh, refreshRecord string
	if s.issueRefresh {
		refresh = idgen.Token()
		scope := store.Scope{TenantID: u.TenantID, UserID: u.UserID, Role: u.Role}
		if err := s.repo.RegisterDevice(ctx, scope, record, idgen.New(), refresh,
			"", clientStamp(ctx)); err != nil {
			s.log.WarnContext(ctx, "registering the device family after recovery",
				"err", err, "record", record)
			refresh = ""
		} else {
			refreshRecord = record
		}
	}

	_ = familiesEnded
	return recoveryResult{
		token:         token,
		expiresAt:     expires.Format(time.RFC3339),
		refreshRecord: refreshRecord,
		refreshToken:  refresh,
		sessionsEnded: ended,
	}, nil
}
