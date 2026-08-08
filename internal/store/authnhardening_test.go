package store

import (
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// The two store-level halves of the authn review: what the address bucket is
// allowed to count, and how an expiry stamp is compared.
//
// Both were found by reading rather than by failing, and both are the kind of
// thing that reverts silently — one is a single word in an IN list, the other is
// a comparison that looks obviously correct and is not. A test is what makes
// putting them back a red build instead of a regression nobody notices for a
// year.

// --- FailureCounts: the address bucket counts guesses, not their consequences -

// A locked-out account's own retries must not drive the shared address counter.
//
// This is the DoS the per-account curve is capped to avoid, arriving through the
// other door: `locked` rows are written BY the lockout path, so counting them
// meant a refused attempt fed the counter that refused it. Behind a proxy or
// carrier NAT the address is a household, so an owner retrying their own locked
// account took their neighbours off the service with them.
func TestFailureCountsIgnoresLockedRowsForTheAddress(t *testing.T) {
	repo, ctx := seedIdentity(t)

	// Well past AddressLimit (20), all of them the lockout path's own bookkeeping.
	for range 25 {
		if err := repo.RecordLoginAttempt(ctx, LoginAttempt{
			Username: "alice", IP: "203.0.113.7", Outcome: LoginLocked,
		}); err != nil {
			t.Fatalf("RecordLoginAttempt: %v", err)
		}
	}

	_, ipFails, err := repo.FailureCounts(ctx, "alice", "203.0.113.7", time.Minute)
	if err != nil {
		t.Fatalf("FailureCounts: %v", err)
	}
	if ipFails != 0 {
		t.Errorf("the address bucket counted %d locked rows; a refusal is not a guess "+
			"and counting it lets an owner lock out their own household", ipFails)
	}
}

// The guesses themselves still count, or removing `locked` would have disarmed
// the control rather than corrected it.
func TestFailureCountsStillCountsGuessesForTheAddress(t *testing.T) {
	repo, ctx := seedIdentity(t)

	for _, outcome := range []LoginOutcome{
		LoginBadPassword, LoginBadPassword, LoginUnknownUser,
	} {
		if err := repo.RecordLoginAttempt(ctx, LoginAttempt{
			Username: "alice", IP: "203.0.113.8", Outcome: outcome,
		}); err != nil {
			t.Fatalf("RecordLoginAttempt: %v", err)
		}
	}

	_, ipFails, err := repo.FailureCounts(ctx, "alice", "203.0.113.8", time.Minute)
	if err != nil {
		t.Fatalf("FailureCounts: %v", err)
	}
	if ipFails != 3 {
		t.Errorf("the address bucket counted %d guesses, want 3", ipFails)
	}
}

// --- sessionUnexpired: precision must not decide validity ---------------------

// A session whose expiry carries a fractional second is live until that second.
//
// `time.RFC3339Nano` trims trailing zeros, so the column holds a mix of widths
// and no fixed-precision "now" orders correctly against all of them. The old
// comparison put ".5Z" below "Z" — '.' sorts under 'Z' — so any session that
// happened to expire mid-second was already dead when asked about at the top of
// it. Sub-second on a thirty-day TTL is not a user-visible bug; the reason it is
// pinned is that the same comparison on a lower bound would have failed OPEN.
func TestScopeForSessionHonoursAFractionalExpiry(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("fractional")

	// An expiry an hour out, deliberately carrying a fraction that trims to one
	// digit — the exact shape that mis-sorted.
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second).Add(500 * time.Millisecond)
	if err := repo.CreateSession(ctx, NewSession{
		SessionID: "sess-frac", UserID: "u1", TenantID: "t1", TokenHash: hash,
		DeviceID: "dev1", UserAgent: "test-agent",
		Now:       time.Now().UTC().Format(time.RFC3339Nano),
		ExpiresAt: expires.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := repo.ScopeForSession(ctx, hash); err != nil {
		t.Errorf("a session expiring in an hour did not resolve: %v", err)
	}
}

// And the boundary still closes: an expiry in the past is not a session,
// fraction or no fraction.
func TestScopeForSessionRefusesAnExpiredFractionalStamp(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("expired-fractional")

	expires := time.Now().UTC().Add(-time.Hour).Truncate(time.Second).Add(500 * time.Millisecond)
	if err := repo.CreateSession(ctx, NewSession{
		SessionID: "sess-old", UserID: "u1", TenantID: "t1", TokenHash: hash,
		DeviceID: "dev1", UserAgent: "test-agent",
		Now:       time.Now().UTC().Format(time.RFC3339Nano),
		ExpiresAt: expires.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := repo.ScopeForSession(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("an expired session resolved: %v", err)
	}
}

// The sudo stamp reads through the same predicate, so it has to agree. A sudo
// window that outlived its own session would be the one place the freshness
// check could be satisfied by a credential nothing else accepts.
func TestSessionAuthenticatedAtAgreesWithScopeForSession(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("sudo-frac")

	now := time.Now().UTC()
	expires := now.Add(time.Hour).Truncate(time.Second).Add(500 * time.Millisecond)
	if err := repo.CreateSession(ctx, NewSession{
		SessionID: "sess-sudo", UserID: "u1", TenantID: "t1", TokenHash: hash,
		DeviceID: "dev1", UserAgent: "test-agent",
		Now:             now.Format(time.RFC3339Nano),
		ExpiresAt:       expires.Format(time.RFC3339Nano),
		AuthenticatedAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	at, err := repo.SessionAuthenticatedAt(ctx, hash)
	if err != nil {
		t.Fatalf("SessionAuthenticatedAt on a live session: %v", err)
	}
	if at.IsZero() {
		t.Error("the sudo stamp came back zero for a session that carries one")
	}
}
