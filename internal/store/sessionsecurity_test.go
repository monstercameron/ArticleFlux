package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// §7.3a SEC3, tested at the store rather than only through the callers above it.
//
// These three functions carry the whole "a reader who thinks they have ended a
// thief's session actually has" property, and they had no test in this package.
// They were exercised indirectly — the CLI's passwd test and grpcsrv both reach
// them — but `go test ./...` credits a package's coverage only from its own test
// binary, so that exercise is invisible here, and more importantly an indirect
// test pins the CALLER's behaviour. What has to hold is a property of the SQL:
// either everything commits, or nothing does, and a revoked credential is
// revoked by every path that asks.

// seedDeviceSession creates a user, a device carrying a refresh family, and a
// live session bound to that device — the shape a real login leaves behind, and
// the only shape in which "revoke the family too" means anything.
func seedDeviceSession(t *testing.T, repo *ReaderRepo, ctx context.Context,
	sessionID, tokenHash, deviceID, familyID string) Scope {
	t.Helper()
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}
	if err := repo.RegisterDevice(ctx, sc, deviceID, familyID, "refresh-"+deviceID, "laptop", "v1"); err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateSession(ctx, NewSession{
		SessionID: sessionID, UserID: "u1", TenantID: "t1", TokenHash: tokenHash,
		DeviceID: deviceID, UserAgent: "test-agent", Now: now,
		ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sc
}

// --- RevokeSessionAndFamily ---------------------------------------------------

// Logging out has to end the refresh family too. Ending only the session leaves
// the device able to mint a new one, which is a logout that did not log
// anything out.
func TestRevokeSessionAndFamilyEndsBothHalves(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("token-a")
	seedDeviceSession(t, repo, ctx, "sess1", hash, "dev-a", "fam-a")

	if _, err := repo.FamilyForSession(ctx, hash); err != nil {
		t.Fatalf("setup: the session has no family: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.RevokeSessionAndFamily(ctx, hash, now); err != nil {
		t.Fatalf("RevokeSessionAndFamily: %v", err)
	}

	if _, err := repo.ScopeForSession(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("the session still resolves after revocation: %v", err)
	}
	if _, err := repo.FamilyForSession(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("the refresh family survived the logout: %v", err)
	}
}

// An unknown or already-revoked token is not an error. Logging out twice, or
// with a token from a previous deployment, must be a no-op rather than a 500 on
// the way out the door.
func TestRevokeSessionAndFamilyIsANoOpForAnUnknownToken(t *testing.T) {
	repo, ctx := seedIdentity(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if err := repo.RevokeSessionAndFamily(ctx, secret.HashToken("never-issued"), now); err != nil {
		t.Errorf("revoking an unknown token: %v", err)
	}

	hash := secret.HashToken("token-a")
	seedDeviceSession(t, repo, ctx, "sess1", hash, "dev-a", "fam-a")
	if err := repo.RevokeSessionAndFamily(ctx, hash, now); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if err := repo.RevokeSessionAndFamily(ctx, hash, now); err != nil {
		t.Errorf("revoking twice: %v", err)
	}
}

// RegisterDevice can fail non-fatally, leaving a session with no device row. The
// logout must still end the session rather than failing because there is no
// family to end.
func TestRevokeSessionAndFamilyHandlesASessionWithNoDevice(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("token-a")
	// mkSession names a device that was never registered.
	mkSession(t, repo, ctx, "sess1", "u1", "t1", hash, time.Hour)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.RevokeSessionAndFamily(ctx, hash, now); err != nil {
		t.Fatalf("RevokeSessionAndFamily with no device row: %v", err)
	}
	if _, err := repo.ScopeForSession(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("the session survived a logout that found no device: %v", err)
	}
}

// One person logging out must not log out the other. The revocation is keyed on
// the token, and a query that widened to the user would end every session on
// every device.
func TestRevokeSessionAndFamilyLeavesOtherSessionsAlone(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hashA := secret.HashToken("token-a")
	hashB := secret.HashToken("token-b")
	seedDeviceSession(t, repo, ctx, "sess1", hashA, "dev-a", "fam-a")
	seedDeviceSession(t, repo, ctx, "sess2", hashB, "dev-b", "fam-b")

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.RevokeSessionAndFamily(ctx, hashA, now); err != nil {
		t.Fatalf("RevokeSessionAndFamily: %v", err)
	}

	if _, err := repo.ScopeForSession(ctx, hashB); err != nil {
		t.Errorf("logging out one device ended another's session: %v", err)
	}
	if _, err := repo.FamilyForSession(ctx, hashB); err != nil {
		t.Errorf("logging out one device ended another's refresh family: %v", err)
	}
}

// --- FamilyForSession ---------------------------------------------------------

func TestFamilyForSessionReturnsTheDevicesFamily(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("token-a")
	seedDeviceSession(t, repo, ctx, "sess1", hash, "dev-a", "fam-a")

	got, err := repo.FamilyForSession(ctx, hash)
	if err != nil {
		t.Fatalf("FamilyForSession: %v", err)
	}
	if got != "fam-a" {
		t.Errorf("family = %q, want fam-a", got)
	}
}

func TestFamilyForSessionRefusesAnUnknownToken(t *testing.T) {
	repo, ctx := seedIdentity(t)
	if _, err := repo.FamilyForSession(ctx, secret.HashToken("never-issued")); !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

// A session with no device joins to nothing, and the join must produce
// ErrNotFound rather than an empty family id that a caller would then use as a
// real one.
func TestFamilyForSessionRefusesASessionWithNoDevice(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("token-a")
	mkSession(t, repo, ctx, "sess1", "u1", "t1", hash, time.Hour)

	got, err := repo.FamilyForSession(ctx, hash)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("= (%q, %v), want ErrNotFound", got, err)
	}
	if got != "" {
		t.Errorf("returned a family id (%q) alongside an error", got)
	}
}

// --- ChangePasswordAndRevoke --------------------------------------------------

// The break-glass reset: the account is being recovered FROM a lost credential,
// so nothing survives. Empty keeps mean keep nothing.
func TestChangePasswordAndRevokeEndsEverythingWhenNothingIsKept(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hashA := secret.HashToken("token-a")
	hashB := secret.HashToken("token-b")
	seedDeviceSession(t, repo, ctx, "sess1", hashA, "dev-a", "fam-a")
	seedDeviceSession(t, repo, ctx, "sess2", hashB, "dev-b", "fam-b")

	now := time.Now().UTC().Format(time.RFC3339Nano)
	sessions, families, err := repo.ChangePasswordAndRevoke(ctx, "u1", "new-hash", "", "", now)
	if err != nil {
		t.Fatalf("ChangePasswordAndRevoke: %v", err)
	}
	if sessions != 2 {
		t.Errorf("ended %d sessions, want 2", sessions)
	}
	if families != 2 {
		t.Errorf("ended %d refresh families, want 2", families)
	}

	for name, h := range map[string]string{"a": hashA, "b": hashB} {
		if _, err := repo.ScopeForSession(ctx, h); !errors.Is(err, ErrNotFound) {
			t.Errorf("session %s survived the reset: %v", name, err)
		}
		if _, err := repo.FamilyForSession(ctx, h); !errors.Is(err, ErrNotFound) {
			t.Errorf("refresh family %s survived the reset: %v", name, err)
		}
	}
}

// The self-service change: the person who just proved their password again is
// not logged out for doing so, and every OTHER credential dies.
func TestChangePasswordAndRevokeKeepsTheCallersOwnCredentials(t *testing.T) {
	repo, ctx := seedIdentity(t)
	mine := secret.HashToken("token-mine")
	theirs := secret.HashToken("token-theirs")
	seedDeviceSession(t, repo, ctx, "sess1", mine, "dev-mine", "fam-mine")
	seedDeviceSession(t, repo, ctx, "sess2", theirs, "dev-theirs", "fam-theirs")

	now := time.Now().UTC().Format(time.RFC3339Nano)
	sessions, families, err := repo.ChangePasswordAndRevoke(ctx, "u1", "new-hash", mine, "fam-mine", now)
	if err != nil {
		t.Fatalf("ChangePasswordAndRevoke: %v", err)
	}
	if sessions != 1 || families != 1 {
		t.Errorf("ended %d sessions and %d families, want 1 and 1", sessions, families)
	}

	if _, err := repo.ScopeForSession(ctx, mine); err != nil {
		t.Errorf("the caller was logged out by their own password change: %v", err)
	}
	if _, err := repo.FamilyForSession(ctx, mine); err != nil {
		t.Errorf("the caller's refresh family was ended by their own change: %v", err)
	}
	if _, err := repo.ScopeForSession(ctx, theirs); !errors.Is(err, ErrNotFound) {
		t.Errorf("the other session survived the password change: %v", err)
	}
	if _, err := repo.FamilyForSession(ctx, theirs); !errors.Is(err, ErrNotFound) {
		t.Errorf("the other refresh family survived the password change: %v", err)
	}
}

// The hash is replaced in the SAME transaction as the revocations. The previous
// shape wrote them separately, which let a revocation failure land after the RPC
// had already reported success with an invented zero count.
func TestChangePasswordAndRevokeReplacesTheHash(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("token-a")
	seedDeviceSession(t, repo, ctx, "sess1", hash, "dev-a", "fam-a")

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, _, err := repo.ChangePasswordAndRevoke(ctx, "u1", "the-new-hash", "", "", now); err != nil {
		t.Fatalf("ChangePasswordAndRevoke: %v", err)
	}

	var stored string
	if err := repo.db.Read.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE id = ?`, "u1").Scan(&stored); err != nil {
		t.Fatalf("read back the hash: %v", err)
	}
	if stored != "the-new-hash" {
		t.Errorf("password hash is %q; the old one is still live", stored)
	}
}

// A reset on an account with nothing live is not an error and must report
// honest zeroes rather than a count of something it did not do.
func TestChangePasswordAndRevokeOnAnAccountWithNoSessions(t *testing.T) {
	repo, ctx := seedIdentity(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	sessions, families, err := repo.ChangePasswordAndRevoke(ctx, "u1", "new-hash", "", "", now)
	if err != nil {
		t.Fatalf("ChangePasswordAndRevoke: %v", err)
	}
	if sessions != 0 || families != 0 {
		t.Errorf("reported %d sessions and %d families on an account with neither", sessions, families)
	}
}

// Already-revoked rows are not counted again. A count that grew every time
// somebody reset a password would be reported to the reader as sessions ended,
// and it would be a number about history rather than about anything live.
func TestChangePasswordAndRevokeDoesNotRecountDeadRows(t *testing.T) {
	repo, ctx := seedIdentity(t)
	hash := secret.HashToken("token-a")
	seedDeviceSession(t, repo, ctx, "sess1", hash, "dev-a", "fam-a")

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, _, err := repo.ChangePasswordAndRevoke(ctx, "u1", "hash-2", "", "", now); err != nil {
		t.Fatalf("first reset: %v", err)
	}
	sessions, families, err := repo.ChangePasswordAndRevoke(ctx, "u1", "hash-3", "", "", now)
	if err != nil {
		t.Fatalf("second reset: %v", err)
	}
	if sessions != 0 || families != 0 {
		t.Errorf("the second reset re-counted %d sessions and %d families that were already dead",
			sessions, families)
	}
}

// --- ReadOnly -------------------------------------------------------------------

// The regression for a total breakage: store.Open with ReadOnly set could never
// succeed.
//
// verify() proves FTS5 is registered on a connection drawn from each pool by
// creating a probe table in `temp.`. A read-only connection carries
// query_only(1), which refuses every write INCLUDING one into temp — so the
// probe failed, Open returned an error, and every caller of ReadOnly was
// cmd/classifyprobe, whose three entry points therefore could not start at all.
//
// The tell was that nothing anywhere exercised the option. A flag with no test
// and one caller is a flag nobody will notice has stopped working.
func TestOpenReadOnlySucceedsAndRefusesWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ro.db")

	// A real database first — ReadOnly cannot create one.
	rw, err := Open(Options{Path: path})
	if err != nil {
		t.Fatalf("open read-write: %v", err)
	}
	if _, err := rw.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ro, err := Open(Options{Path: path, ReadOnly: true})
	if err != nil {
		t.Fatalf("open read-only: %v", err)
	}
	defer ro.Close()

	// It reads.
	if _, err := ro.SchemaVersion(context.Background()); err != nil {
		t.Errorf("a read-only database could not answer a read: %v", err)
	}

	// And it refuses to write, which is the point of the option: a probe must
	// not alter what it is measuring.
	if _, err := ro.Write.Exec(`CREATE TABLE scribble (x TEXT)`); err == nil {
		t.Error("a read-only database accepted a write")
	}
}

// FTS5 still has to be PROVEN present on a read-only pool, not assumed. The
// whole reason verify exists is that a missing hook produces a database that
// works until somebody searches.
func TestReadOnlyStillProvesFTS5IsRegistered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ro.db")
	rw, err := Open(Options{Path: path})
	if err != nil {
		t.Fatalf("open read-write: %v", err)
	}
	if _, err := rw.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_ = rw.Close()

	ro, err := Open(Options{Path: path, ReadOnly: true})
	if err != nil {
		t.Fatalf("open read-only: %v", err)
	}
	defer ro.Close()

	for name, pool := range map[string]*sql.DB{"read": ro.Read, "write": ro.Write} {
		var n int
		if err := pool.QueryRow(
			`SELECT count(*) FROM pragma_module_list WHERE name = 'fts5'`).Scan(&n); err != nil {
			t.Errorf("%s pool: %v", name, err)
			continue
		}
		if n != 1 {
			t.Errorf("%s pool does not have fts5 registered; search would fail late", name)
		}
	}
}

func TestOpenReadOnlyRefusesADatabaseThatIsNotThere(t *testing.T) {
	// It must not CREATE one. A probe pointed at a typo should say so rather
	// than quietly measuring an empty database it made itself.
	path := filepath.Join(t.TempDir(), "absent.db")
	db, err := Open(Options{Path: path, ReadOnly: true})
	if err == nil {
		_ = db.Close()
		t.Fatal("opening a database that does not exist read-only reported success")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("a read-only open created the database file")
	}
}
