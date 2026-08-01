package grpcsrv

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/authn"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/pwpolicy"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
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

// Reauthenticate re-proves the password on an existing session.
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

	hash, err := s.repo.PasswordHashFor(ctx, sc)
	if err != nil {
		s.log.Error("reading the password hash for re-authentication", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	ok, _, verr := secret.VerifyPassword(req.GetPassword(), hash, secret.Active())
	if verr != nil || !ok {
		s.limiter.fail(key)
		// Not errBadCredentials: that message names a username, and there is no
		// username in this exchange — the session already said who this is.
		return nil, errKey(codes.Unauthenticated, "srv.badPassword", "that password is not right", nil)
	}
	s.limiter.reset(key)

	now := time.Now().UTC()
	if err := s.repo.StampAuthenticated(ctx, secret.HashToken(token),
		now.Format(time.RFC3339Nano)); err != nil {
		s.log.Error("stamping the re-authentication", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	return &pb.ReauthenticateResponse{
		SudoExpiresAt: now.Add(authn.SudoWindow).Format(time.RFC3339),
	}, nil
}

// ChangePassword replaces the caller's password and ends every other session.
func (s *AuthServer) ChangePassword(ctx context.Context, req *pb.ChangePasswordRequest) (
	*pb.ChangePasswordResponse, error) {

	sc, err := s.requireSudo(ctx, authn.SudoChangePasswd)
	if err != nil {
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
	s.log.Info("password changed", "user", sc.UserID, "sessions_ended", ended,
		"families_ended", familiesEnded)
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
	hashes := make([]string, 0, len(sheet))
	for _, c := range sheet {
		hashes = append(hashes, secret.HashToken(c))
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, sc, hashes); err != nil {
		s.log.Error("storing recovery codes", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	s.log.Info("recovery codes regenerated", "user", sc.UserID, "count", len(sheet))
	return &pb.RegenerateRecoveryCodesResponse{Codes: sheet}, nil
}
