package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// withPassword sets ARTICLEFLUX_PASSWORD for the duration of the test and
// restores whatever was there before — the only non-interactive path left
// after §7.3a SEC5 removed the `-password` flag.
func withPassword(t *testing.T, pw string) {
	t.Helper()
	prev, had := os.LookupEnv("ARTICLEFLUX_PASSWORD")
	if err := os.Setenv("ARTICLEFLUX_PASSWORD", pw); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("ARTICLEFLUX_PASSWORD", prev)
		} else {
			_ = os.Unsetenv("ARTICLEFLUX_PASSWORD")
		}
	})
}

// §7.3a SEC5: the only non-argv automation input is ARTICLEFLUX_PASSWORD, and
// this is its precedence test — there is no flag left to race it against.
func TestResolvePasswordReadsTheEnvironmentAndEnforcesPolicy(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	pw, err := resolvePassword("cam")
	if err != nil {
		t.Fatalf("resolvePassword: %v", err)
	}
	if pw != testPasswordForCLI {
		t.Errorf("resolvePassword = %q, want the environment value", pw)
	}

	// The policy still applies to the environment path — this is not a bypass
	// for automation, it is the SAME check a terminal entry gets.
	withPassword(t, "short")
	if _, err := resolvePassword("cam"); err == nil {
		t.Error("a policy-violating ARTICLEFLUX_PASSWORD was accepted")
	}
}

// testPasswordForCLI is a name distinct from grpcsrv's testPassword (this is a
// different package) but built to the same shape: long enough, and not built
// from "cam" or "articleflux".
const testPasswordForCLI = "a-rather-long-passphrase-42"

// §7.3a SEC3, exercised through the break-glass CLI rather than the RPC: a
// reset must end the account's sessions AND refresh families in the same
// transaction as the hash change, or a device someone lost control of stays
// able to renew itself past the reset that was supposed to end that.
func TestPasswdRevokesSessionsAndFamiliesTransactionally(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "passwd.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)

	oldHash, err := secret.HashPassword("the-password-being-replaced-9", secret.DefaultParams)
	if err != nil {
		t.Fatal(err)
	}
	tenantID, userID := idgen.New(), idgen.New()
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: tenantID, Name: "Test", UserID: userID, Username: "cam",
		Hash: oldHash, Role: "superadmin", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}

	// A live session AND a live refresh family, exactly what a device someone
	// is resetting because of would be holding.
	sc := store.Scope{TenantID: tenantID, UserID: userID, Role: "superadmin"}
	sessionToken := idgen.Token()
	now := time.Now().UTC()
	if err := repo.CreateSession(ctx, store.NewSession{
		SessionID: idgen.New(), UserID: userID, TenantID: tenantID,
		TokenHash: secret.HashToken(sessionToken), DeviceID: "rec1",
		Now:       now.Format(time.RFC3339Nano),
		ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RegisterDevice(ctx, sc, "rec1", "fam1", "refresh-secret", "laptop", "1.0"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	withPassword(t, "a-brand-new-passphrase-77")
	if err := passwd(log, []string{"-user", "cam", "-db", dbPath}); err != nil {
		t.Fatalf("passwd: %v", err)
	}

	db2, err := store.Open(store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	repo2 := store.NewReaderRepo(db2)

	if _, err := repo2.ScopeForSession(ctx, secret.HashToken(sessionToken)); err == nil {
		t.Error("the session survived a break-glass password reset")
	}
	if err := repo2.RotateRefresh(ctx, "rec1", "refresh-secret", idgen.Token()); err == nil {
		t.Error("the refresh family survived a break-glass password reset — a device that " +
			"still holds it could renew itself past the reset")
	}
	u, err := repo2.UserForLogin(ctx, "cam")
	if err != nil {
		t.Fatal(err)
	}
	if u.Hash == oldHash {
		t.Error("passwd did not change the stored hash")
	}
}
