package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Every check Preflight makes corresponds to a way this has gone wrong, and the
// property they share is that the failure is otherwise SILENT — the process
// starts, the health check is green, and something a person needs does not work.

func openFor(t *testing.T, cfg Config) *App {
	t.Helper()
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
	}
	a, err := Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// index.html present and app.wasm absent is the same failure the web-root check
// already names, one step further in: the page loads and then shows nothing.
func TestPreflightCatchesAWebRootWithNoModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := openFor(t, Config{WebRoot: dir, DevMode: true})

	err := a.Preflight(t.Context())
	if err == nil {
		t.Fatal("a web root with no app.wasm passed preflight")
	}
	if !strings.Contains(err.Error(), "app.wasm") {
		t.Errorf("the error does not name the missing module: %v", err)
	}

	// And with the module present it passes, so the check is about the module
	// rather than about the directory.
	if err := os.WriteFile(filepath.Join(dir, "app.wasm"), []byte("\x00asm"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.Preflight(t.Context()); err != nil {
		t.Errorf("a complete web root failed preflight: %v", err)
	}
}

// The compressed bundle counts: index.html loads whichever exists, and a deploy
// that ships only the .gz is a normal one.
func TestPreflightAcceptsAGzippedModule(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"index.html", "app.wasm.gz"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := openFor(t, Config{WebRoot: dir, DevMode: true})
	if err := a.Preflight(t.Context()); err != nil {
		t.Errorf("a gzip-only web root failed preflight: %v", err)
	}
}

// A key that arrived some other way than the RPC — a restored database, an
// environment variable, a hand-edited row — is caught at boot rather than at
// the first Smart+ click, which may be weeks later and looks like a broken
// feature.
func TestPreflightCatchesAMalformedSmartKey(t *testing.T) {
	a := openFor(t, Config{DevMode: true})
	if !a.settings.CanStoreSecrets() {
		t.Skip("no encryption key in this fixture")
	}
	if err := a.settings.SetSystemSecret(t.Context(), store.KeyOpenAIAPIKey,
		"https://platform.openai.com/api-keys", ""); err != nil {
		t.Fatal(err)
	}

	err := a.Preflight(t.Context())
	if err == nil || !strings.Contains(err.Error(), "sk-") {
		t.Errorf("a pasted URL in the API key field passed preflight: %v", err)
	}

	// A well-shaped key passes. Shape, not validity: the server cannot tell a
	// live key from a revoked one without spending a request.
	if err := a.settings.SetSystemSecret(t.Context(), store.KeyOpenAIAPIKey,
		"sk-not-a-real-key-but-the-right-shape", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Preflight(t.Context()); err != nil {
		t.Errorf("a well-shaped key failed preflight: %v", err)
	}
}

// A mailbox without a readable credential is completely silent otherwise: the
// poller runs, fails to decrypt, writes an error on a row nobody is looking at,
// and newsletters simply stop.
func TestPreflightCatchesMailboxesWithNoEncryptionKey(t *testing.T) {
	a := openFor(t, Config{DevMode: true})

	// A real tenant and user, because `mailboxes` has real foreign keys and a
	// test that skips itself on a constraint is not a test.
	if err := a.repo.CreateTenantAndUser(t.Context(), store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "u", Hash: "x",
		Role: "member", Now: "2026-07-27T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	// Written through the repository with a key, then read back without one.
	// That is exactly the situation being reproduced: a restored database whose
	// secrets.key did not come with it, so the ciphertext is there and nothing
	// can read it.
	keyed := store.NewMailboxRepo(a.db, []byte("0123456789abcdef0123456789abcdef"))
	if _, err := keyed.PutMailbox(t.Context(),
		store.Scope{TenantID: "t1", UserID: "u1", Role: "member"},
		store.Mailbox{Host: "imap.example.com", Username: "me@example.com"},
		"a-password"); err != nil {
		t.Fatal(err)
	}

	// With a key present nothing is wrong.
	if err := a.Preflight(t.Context()); err != nil &&
		strings.Contains(err.Error(), "encryption key") {
		t.Errorf("a mailbox with a key present was reported: %v", err)
	}

	// Without one, it must be reported — and must name how many, because "some
	// mailboxes" is not something an operator can act on.
	a.settings = store.NewSettingsRepo(a.db, nil)
	err := a.Preflight(t.Context())
	if err == nil || !strings.Contains(err.Error(), "encryption key") {
		t.Fatalf("mailboxes with no encryption key passed preflight: %v", err)
	}
	if !strings.Contains(err.Error(), "1 newsletter mailbox") {
		t.Errorf("the error does not say how many are affected: %v", err)
	}
}

// Someone setting up a droplet usually has more than one thing wrong at once,
// and a one-at-a-time boot loop is a miserable way to find that out.
func TestPreflightReportsEveryProblemAtOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No account (not DevMode) AND no wasm module.
	a := openFor(t, Config{WebRoot: dir})

	err := a.Preflight(t.Context())
	if err == nil {
		t.Fatal("an instance with no account and no module passed preflight")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no accounts exist") {
		t.Errorf("the missing account is not reported: %v", err)
	}
	if !strings.Contains(msg, "app.wasm") {
		t.Errorf("the missing module is not reported: %v", err)
	}
}
