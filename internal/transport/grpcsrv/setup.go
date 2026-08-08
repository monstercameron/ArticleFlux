package grpcsrv

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/audit"
	"github.com/monstercameron/ArticleFlux/internal/authn"
	"github.com/monstercameron/ArticleFlux/internal/idgen"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/pwpolicy"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// First-run setup (§7.11).
//
// # Why this is an RPC and not just the CLI
//
// `articleflux init` already creates the first account, and it is the right
// tool for somebody who is already on the box. It is the wrong one for the
// person this feature is for: a reader who has just been handed a URL by
// whoever ran the deploy, or who clicked a one-click image and has never seen a
// shell. Making a terminal the only way to claim an instance means the terminal
// is part of the product.
//
// # The refusal is the whole security story
//
// This endpoint takes a password over an unauthenticated call and creates a
// superadmin, which is exactly the shape of a backdoor if it outlives its
// purpose. So it exists only while the instance has no accounts, and it says so
// with FailedPrecondition afterwards — not NotFound, not Unimplemented, because
// somebody re-running a bookmarked setup URL deserves to be told the instance is
// already claimed rather than that the server is broken.
//
// The count is checked inside CreateFirstUser's transaction rather than here.
// Checking first and creating second is a race with a window measured in
// milliseconds and consequences measured in "two people own this server": on a
// fresh public droplet, two browsers hitting setup at once is not hypothetical,
// it is what happens when somebody double-clicks.
const setupMinUsername = 2

// Setup creates the first account and signs it in.
func (s *AuthServer) Setup(ctx context.Context, req *pb.SetupRequest) (*pb.SetupResponse, error) {
	username := strings.TrimSpace(req.GetUsername())
	email := strings.TrimSpace(req.GetEmail())
	password := req.GetPassword()

	if len([]rune(username)) < setupMinUsername {
		return nil, errKey(codes.InvalidArgument, "srv.setupUsername",
			"choose a username of at least two characters", nil)
	}
	// Shape only, and deliberately shallow: there is no mailer here, so an
	// address this server cannot verify is an address it must not pretend to
	// have verified. The check catches a typo like a missing @, and stops
	// there rather than performing rigour it cannot back up.
	if email != "" && !looksLikeEmail(email) {
		return nil, errKey(codes.InvalidArgument, "srv.setupEmail",
			"that does not look like an email address", nil)
	}
	// The same policy every other password on this instance meets. Applying it
	// here matters more than anywhere else: this is the account that can do
	// everything, chosen in the first thirty seconds of using the app, which is
	// exactly when somebody types the password they use everywhere.
	if err := pwpolicy.Check(password, username); err != nil {
		return nil, errKey(codes.InvalidArgument, "srv.weakPassword", err.Error(), nil)
	}

	hash, err := secret.HashPassword(password, secret.Active())
	if err != nil {
		s.log.Error("hashing the setup password", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}

	now := time.Now().UTC()
	tenantID, userID := idgen.New(), idgen.New()
	created, err := s.repo.CreateFirstUser(ctx, store.NewTenant{
		TenantID: tenantID,
		Name:     "Local",
		UserID:   userID,
		Username: username,
		Email:    email,
		Hash:     hash,
		Role:     "superadmin",
		Now:      now.Format(time.RFC3339Nano),
	})
	if err != nil {
		s.log.Error("creating the first account", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	if !created {
		return nil, errKey(codes.FailedPrecondition, "srv.alreadySetUp",
			"this instance already has an account; sign in instead", nil)
	}

	scope := store.Scope{TenantID: tenantID, UserID: userID, Role: "superadmin"}

	// The codes are generated and stored before the session, so a failure here
	// fails setup outright rather than leaving somebody signed in believing they
	// have recovery they never received.
	sheet, err := authn.GenerateRecoveryCodes(authn.RecoveryCodeCount)
	if err != nil {
		s.log.Error("generating recovery codes", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	// recoveryCodeHash, not secret.HashToken — see its comment. The two have to
	// agree with what redemption computes, and for the whole of 6.1 they did not.
	hashes := make([]string, 0, len(sheet))
	for _, c := range sheet {
		hashes = append(hashes, recoveryCodeHash(c))
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, scope, hashes); err != nil {
		s.log.Error("storing recovery codes", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}

	token := idgen.Token()
	// A fresh server-side ID, not anything derived from what the caller sent. An
	// earlier draft seeded this from the username, which would have made the
	// device identifier guessable from a name the reader chose in public.
	record := idgen.DeviceID()
	// The same two-lifetimes rule Login follows: short when the client can
	// renew, long when it cannot. Setup registers a device family below on
	// exactly the same gate, so the two decisions have to read the same flag —
	// a setup session that expired in twelve hours on an instance that issued
	// no refresh token would lock the person who just claimed the box out of it
	// by lunchtime.
	expires := now.Add(s.accessTTL())
	if err := s.repo.CreateSession(ctx, store.NewSession{
		SessionID: idgen.New(),
		UserID:    userID,
		TenantID:  tenantID,
		TokenHash: secret.HashToken(token),
		DeviceID:  record,
		UserAgent: userAgent(ctx),
		Now:       now.Format(time.RFC3339Nano),
		ExpiresAt: expires.Format(time.RFC3339Nano),
		// Setting a password IS an authentication, so the sudo window opens
		// here exactly as it does on login.
		AuthenticatedAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		s.log.Error("creating the setup session", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}

	// A device family, so the new session can refresh like any other — gated
	// by WithRefreshTokens exactly as Login is (§7.3a SEC4). A failure costs
	// the refresh token and nothing else — the account exists and the session
	// works, and refusing setup over bookkeeping would be worse.
	var refresh, refreshRecord string
	if s.issueRefresh {
		refresh = idgen.Token()
		if err := s.repo.RegisterDevice(ctx, scope, record, idgen.New(), refresh,
			"", clientStamp(ctx)); err != nil {
			s.log.Warn("registering the setup device family", "err", err)
			refresh = ""
		} else {
			refreshRecord = record
		}
	}

	s.log.InfoContext(ctx, "instance claimed", "username", username, "role", "superadmin",
		"email_given", email != "")
	// The first row in the trail, and the one that establishes everything after
	// it: this is an unauthenticated call that created a superadmin. If the
	// instance was claimed by somebody other than its owner, this is where it
	// shows — and it can only ever happen once.
	s.trail.Record(ctx, audit.Event{
		Action: audit.ActionInstanceClaim, Actor: userID, Tenant: tenantID,
		Detail: map[string]string{
			"username": username, "role": "superadmin", "client": clientKey(ctx),
		},
	})
	return &pb.SetupResponse{
		Token:           token,
		ExpiresAt:       expires.Format(time.RFC3339),
		Username:        username,
		Role:            "superadmin",
		RefreshRecordId: refreshRecord,
		RefreshToken:    refresh,
		RecoveryCodes:   sheet,
	}, nil
}

// looksLikeEmail is a shape check, not a validation.
//
// One @, something either side, and a dot in the domain. Everything stricter is
// a lie on a server with no mailer: the only honest test of an address is
// sending to it, and nothing here can. RFC 5322 in a regex would reject
// addresses that work and accept addresses that do not, at greater length.
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 || strings.Count(s, "@") != 1 {
		return false
	}
	domain := s[at+1:]
	dot := strings.IndexByte(domain, '.')
	return dot > 0 && dot < len(domain)-1 && !strings.ContainsAny(s, " \t\r\n")
}
