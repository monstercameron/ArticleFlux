package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// seedIdentity creates one tenant/user, for tests that only need a login target.
func seedIdentity(t *testing.T) (*ReaderRepo, context.Context) {
	t.Helper()
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "alice",
		Hash: "hash1", Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	return repo, ctx
}

func mkSession(t *testing.T, repo *ReaderRepo, ctx context.Context, id, userID, tenantID, tokenHash string, ttl time.Duration) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateSession(ctx, NewSession{
		SessionID: id, UserID: userID, TenantID: tenantID, TokenHash: tokenHash,
		DeviceID: "dev1", UserAgent: "test-agent", Now: now,
		ExpiresAt: time.Now().UTC().Add(ttl).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScopeForSessionResolvesALiveSession(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("token-a")
	mkSession(t, repo, ctx, "sess1", "u1", "t1", hash, time.Hour)

	sc, err := repo.ScopeForSession(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if sc.TenantID != "t1" || sc.UserID != "u1" || sc.Role != "member" {
		t.Errorf("scope = %+v, want t1/u1/member", sc)
	}
}

func TestScopeForSessionRejectsUnknownToken(t *testing.T) {
	repo, ctx := seedIdentity(t)
	if _, err := repo.ScopeForSession(ctx, secret.HashToken("never-issued")); !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

// The expiry and revocation checks live in SQL (see identity.go's comment on
// why); this is the behavioural proof they actually fire.
func TestScopeForSessionRejectsRevokedAndExpiredSessions(t *testing.T) {
	repo, ctx := seedIdentity(t)

	revokedHash := secret.HashToken("revoked-token")
	mkSession(t, repo, ctx, "sess-revoked", "u1", "t1", revokedHash, time.Hour)
	if err := repo.RevokeSession(ctx, revokedHash, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ScopeForSession(ctx, revokedHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoked session: = %v, want ErrNotFound", err)
	}

	expiredHash := secret.HashToken("expired-token")
	mkSession(t, repo, ctx, "sess-expired", "u1", "t1", expiredHash, -time.Hour)
	if _, err := repo.ScopeForSession(ctx, expiredHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired session: = %v, want ErrNotFound", err)
	}
}

// A deactivated account's sessions must stop resolving even if nobody got
// around to revoking them individually — deactivation is meant to be total.
func TestScopeForSessionRejectsDeactivatedUser(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("still-valid-token")
	mkSession(t, repo, ctx, "sess1", "u1", "t1", hash, time.Hour)

	if _, err := repo.db.Write.ExecContext(ctx,
		`UPDATE users SET deactivated_at = ? WHERE id = 'u1'`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ScopeForSession(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

func TestUserForLoginFindsByUsernameCaseInsensitively(t *testing.T) {
	repo, ctx := seedIdentity(t)
	u, err := repo.UserForLogin(ctx, "ALICE")
	if err != nil {
		t.Fatal(err)
	}
	if u.UserID != "u1" || u.TenantID != "t1" || u.Hash != "hash1" {
		t.Errorf("got %+v", u)
	}
}

func TestUserForLoginUnknownUsername(t *testing.T) {
	repo, ctx := seedIdentity(t)
	if _, err := repo.UserForLogin(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

func TestUserForLoginExcludesDeactivatedAccounts(t *testing.T) {
	repo, ctx := seedIdentity(t)
	if _, err := repo.db.Write.ExecContext(ctx,
		`UPDATE users SET deactivated_at = ? WHERE id = 'u1'`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UserForLogin(ctx, "alice"); !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

// D12's ambiguity: usernames are unique per tenant, not globally, so two
// tenants naming the same person must not silently pick one.
func TestUserForLoginIsAmbiguousAcrossTenants(t *testing.T) {
	repo, ctx := seedIdentity(t)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t2", Name: "Other", UserID: "u2", Username: "alice",
		Hash: "hash2", Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UserForLogin(ctx, "alice"); !errors.Is(err, ErrAmbiguousUser) {
		t.Errorf("= %v, want ErrAmbiguousUser", err)
	}
}

func TestCreateSessionStampsLastLogin(t *testing.T) {
	repo, ctx := seedIdentity(t)
	var before string
	if err := repo.db.Read.QueryRowContext(ctx,
		`SELECT ifnull(last_login_at,'') FROM users WHERE id='u1'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != "" {
		t.Fatal("fixture user already has a last_login_at")
	}

	mkSession(t, repo, ctx, "sess1", "u1", "t1", secret.HashToken("tok"), time.Hour)

	var after string
	if err := repo.db.Read.QueryRowContext(ctx,
		`SELECT ifnull(last_login_at,'') FROM users WHERE id='u1'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after == "" {
		t.Error("last_login_at was not stamped by CreateSession")
	}
}

func TestRevokeSessionIsIdempotentAndSilent(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("tok")
	mkSession(t, repo, ctx, "sess1", "u1", "t1", hash, time.Hour)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if err := repo.RevokeSession(ctx, hash, now); err != nil {
		t.Fatal(err)
	}
	// A second revoke of the same token, and a revoke of a token that never
	// existed, must both be quiet no-ops — logout is not an oracle.
	if err := repo.RevokeSession(ctx, hash, now); err != nil {
		t.Errorf("second revoke errored: %v", err)
	}
	if err := repo.RevokeSession(ctx, secret.HashToken("never-existed"), now); err != nil {
		t.Errorf("revoke of an unknown token errored: %v", err)
	}
}

func TestPurgeExpiredSessionsRemovesDeadRowsOnly(t *testing.T) {
	repo, ctx := seedIdentity(t)
	mkSession(t, repo, ctx, "sess-live", "u1", "t1", secret.HashToken("live"), time.Hour)
	mkSession(t, repo, ctx, "sess-expired", "u1", "t1", secret.HashToken("expired"), -time.Hour)
	revokedHash := secret.HashToken("revoked")
	mkSession(t, repo, ctx, "sess-revoked", "u1", "t1", revokedHash, time.Hour)
	// Revoked in the past, so it is eligible for the purge cut below.
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	if err := repo.RevokeSession(ctx, revokedHash, past); err != nil {
		t.Fatal(err)
	}

	n, err := repo.PurgeExpiredSessions(ctx, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("purged %d, want 2 (expired + revoked-before-cut)", n)
	}
	var remaining int
	if err := repo.db.Read.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Errorf("%d sessions remain, want 1 (the live one)", remaining)
	}
}

func TestCreateFirstUserOnlyEverSucceedsOnce(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	created, err := repo.CreateFirstUser(ctx, NewTenant{
		TenantID: "t1", Name: "First", UserID: "u1", Username: "owner",
		Hash: "h", Role: "superadmin", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first call reported created=false")
	}

	// A second attempt — the double-click race this exists to close — must
	// report false rather than creating a second tenant or erroring.
	created2, err := repo.CreateFirstUser(ctx, NewTenant{
		TenantID: "t2", Name: "Second", UserID: "u2", Username: "intruder",
		Hash: "h", Role: "superadmin", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Error("a second CreateFirstUser call reported created=true")
	}
	n, err := repo.CountUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d users exist after a rejected second bootstrap, want 1", n)
	}
}

func TestCountUsersExcludesDeactivated(t *testing.T) {
	repo, ctx := seedIdentity(t)
	if n, err := repo.CountUsers(ctx); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v, want 1", n, err)
	}
	if _, err := repo.db.Write.ExecContext(ctx,
		`UPDATE users SET deactivated_at = ? WHERE id = 'u1'`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.CountUsers(ctx); err != nil || n != 0 {
		t.Errorf("n=%d err=%v, want 0 after deactivation", n, err)
	}
}

func TestSetPasswordHashUpdatesAndReportsNotFound(t *testing.T) {
	repo, ctx := seedIdentity(t)
	if err := repo.SetPasswordHash(ctx, "u1", "new-hash"); err != nil {
		t.Fatal(err)
	}
	hash, err := repo.PasswordHashFor(ctx, Scope{TenantID: "t1", UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if hash != "new-hash" {
		t.Errorf("hash = %q, want new-hash", hash)
	}
	if err := repo.SetPasswordHash(ctx, "nonexistent", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

func TestSessionAuthenticatedAtIsZeroUntilStamped(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("tok")
	mkSession(t, repo, ctx, "sess1", "u1", "t1", hash, time.Hour)

	at, err := repo.SessionAuthenticatedAt(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !at.IsZero() {
		t.Errorf("freshly created session has authenticated_at = %v, want zero", at)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.StampAuthenticated(ctx, hash, now); err != nil {
		t.Fatal(err)
	}
	at2, err := repo.SessionAuthenticatedAt(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if at2.IsZero() {
		t.Error("authenticated_at is still zero after StampAuthenticated")
	}
	if time.Since(at2) > time.Minute {
		t.Errorf("authenticated_at = %v, too far from now", at2)
	}
}

func TestSessionAuthenticatedAtNotFoundForDeadSession(t *testing.T) {
	repo, ctx := seedIdentity(t)
	if _, err := repo.SessionAuthenticatedAt(ctx, secret.HashToken("nope")); !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

func TestStampAuthenticatedReportsNotFoundOnDeadSession(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("tok")
	mkSession(t, repo, ctx, "sess1", "u1", "t1", hash, time.Hour)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.RevokeSession(ctx, hash, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.StampAuthenticated(ctx, hash, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("stamping a revoked session = %v, want ErrNotFound", err)
	}
}

// The exception IS the point (see identity.go): the caller's own session must
// survive a "log out everywhere else" action.
func TestRevokeOtherSessionsKeepsTheCallersOwn(t *testing.T) {
	repo, ctx := seedIdentity(t)
	keepHash := secret.HashToken("keep-me")
	mkSession(t, repo, ctx, "sess-keep", "u1", "t1", keepHash, time.Hour)
	mkSession(t, repo, ctx, "sess-other-1", "u1", "t1", secret.HashToken("kill-1"), time.Hour)
	mkSession(t, repo, ctx, "sess-other-2", "u1", "t1", secret.HashToken("kill-2"), time.Hour)

	n, err := repo.RevokeOtherSessions(ctx, "u1", keepHash, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("revoked %d, want 2", n)
	}
	if _, err := repo.ScopeForSession(ctx, keepHash); err != nil {
		t.Errorf("the caller's own session was revoked: %v", err)
	}
}

func TestRevokeAllSessionsEndsEveryOne(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hashes := []string{secret.HashToken("a"), secret.HashToken("b")}
	mkSession(t, repo, ctx, "s1", "u1", "t1", hashes[0], time.Hour)
	mkSession(t, repo, ctx, "s2", "u1", "t1", hashes[1], time.Hour)

	if err := repo.RevokeAllSessions(ctx, "u1", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	for _, h := range hashes {
		if _, err := repo.ScopeForSession(ctx, h); !errors.Is(err, ErrNotFound) {
			t.Errorf("session with hash %s still resolves after RevokeAllSessions", h)
		}
	}
}

func TestFirstTenantIDReturnsTheEarliestCreated(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "old", Name: "Old", UserID: "u1", Username: "a", Hash: "h",
		Role: "member", Now: "2020-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "new", Name: "New", UserID: "u2", Username: "b", Hash: "h",
		Role: "member", Now: "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	id, err := repo.FirstTenantID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id != "old" {
		t.Errorf("FirstTenantID = %q, want old", id)
	}
}

func TestFirstTenantIDNotFoundOnEmptyInstance(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	if _, err := repo.FirstTenantID(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}
