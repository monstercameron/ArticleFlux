package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/audit"
	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// `articleflux reset` mints the credential that lets somebody who has lost both
// their password and their recovery sheet back into their account. It had no
// test, which is a strange place for this codebase to have a hole: it is the
// one command whose output IS a credential, and the two things that make it
// safe — the token really works, and it is nowhere except the operator's
// terminal — are both properties nothing was checking.

// resetFixture builds an instance with one account and returns its path and the
// account's id.
func resetFixture(t *testing.T) (dbPath, userID string) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "reset.db")
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hash, err := secret.HashPassword(testPasswordForCLI, secret.DefaultParams)
	if err != nil {
		t.Fatal(err)
	}
	userID = idgen.New()
	if err := store.NewReaderRepo(db).CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: idgen.New(), Name: "Test", UserID: userID, Username: "cam",
		Hash: hash, Role: "superadmin", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	return dbPath, userID
}

// tokenPattern picks the token out of the printed block. The command prints it
// on its own indented line, which is the shape an operator copies from.
var tokenPattern = regexp.MustCompile(`(?m)^\s{2}([A-Za-z0-9_\-]{16,})$`)

func runReset(t *testing.T, dbPath string, args ...string) (out string, logged string, err error) {
	t.Helper()
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	out = captureStdout(t, func() {
		err = reset(log, append([]string{"-db", dbPath}, args...))
	})
	return out, logBuf.String(), err
}

// The property that matters: what got printed is a token the application will
// actually accept. A command that prints a plausible-looking string and stores
// a different hash would look correct in every other test.
func TestResetPrintsATokenThatReallyRedeems(t *testing.T) {
	dbPath, userID := resetFixture(t)
	out, _, err := runReset(t, dbPath, "-user", "cam")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}

	m := tokenPattern.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no token on its own line in:\n%s", out)
	}
	token := m[1]

	db, err := store.Open(store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewReaderRepo(db)

	got, err := repo.ConsumeResetToken(context.Background(), secret.HashToken(token))
	if err != nil {
		t.Fatalf("the printed token was refused: %v", err)
	}
	if got != userID {
		t.Errorf("the token resets %s, want the account it was issued for (%s)", got, userID)
	}
}

// Minting a second token kills the first. An admin who issues another has
// decided the first should not work — usually because they are not sure it
// reached the right person — and leaving both live would make "issue a new one"
// double the attack surface instead of replacing it.
func TestResetInvalidatesThePreviousToken(t *testing.T) {
	dbPath, _ := resetFixture(t)

	first, _, err := runReset(t, dbPath, "-user", "cam")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := runReset(t, dbPath, "-user", "cam")
	if err != nil {
		t.Fatal(err)
	}
	t1 := tokenPattern.FindStringSubmatch(first)
	t2 := tokenPattern.FindStringSubmatch(second)
	if t1 == nil || t2 == nil {
		t.Fatalf("could not read both tokens:\n%s\n---\n%s", first, second)
	}
	if t1[1] == t2[1] {
		t.Fatal("two runs printed the same token")
	}

	db, err := store.Open(store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewReaderRepo(db)
	ctx := context.Background()

	if _, err := repo.ConsumeResetToken(ctx, secret.HashToken(t1[1])); !errors.Is(err, store.ErrBadResetToken) {
		t.Errorf("the superseded token is still live (err=%v)", err)
	}
	if _, err := repo.ConsumeResetToken(ctx, secret.HashToken(t2[1])); err != nil {
		t.Errorf("the newest token was refused: %v", err)
	}
}

// The token is a credential, and the whole reason it is printed rather than
// logged is that a log line goes to the journal, gets shipped, and outlives the
// token by however long the retention is.
func TestResetNeverPutsTheTokenAnywhereButStdout(t *testing.T) {
	dbPath, _ := resetFixture(t)
	out, logged, err := runReset(t, dbPath, "-user", "cam")
	if err != nil {
		t.Fatal(err)
	}
	m := tokenPattern.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no token in:\n%s", out)
	}
	if strings.Contains(logged, m[1]) {
		t.Errorf("the reset token was written to the log:\n%s", logged)
	}
	if !strings.Contains(logged, "reset token issued") {
		t.Errorf("the issue itself was not logged; that half IS audit evidence:\n%s", logged)
	}

	// The same rule in the durable trail. §7.9's audit log outlives the token
	// and is quoted in incident reports; a credential in it is a credential in
	// every backup of it.
	db, err := store.Open(store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entries, err := store.NewReaderRepo(db).AuditTrailInstance(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	var issued *store.AuditEntry
	for i, e := range entries {
		if e.Action == string(audit.ActionResetIssued) {
			issued = &entries[i]
		}
		if strings.Contains(e.Detail, m[1]) {
			t.Fatalf("the reset token is in the audit trail: %+v", e)
		}
	}
	if issued == nil {
		t.Fatal("no auth.reset.issued row; the trail is meant to show both halves — " +
			"a token minted and never redeemed is worth seeing")
	}
	if !strings.Contains(issued.Detail, "cam") || !strings.Contains(issued.Detail, "cli") {
		t.Errorf("the audit row does not say who or how: %q", issued.Detail)
	}
}

// -origin prints the link the client route actually answers. This printed a URL
// to a route that did not exist for a while, which left the only way through as
// pasting the token by hand out of the address bar — the exact step the link
// exists to remove, asked of somebody already locked out.
func TestResetLinkUsesTheRouteTheClientRecognises(t *testing.T) {
	dbPath, _ := resetFixture(t)

	// The trailing slash is what somebody pastes from a browser, and it must not
	// produce a doubled one.
	out, _, err := runReset(t, dbPath, "-user", "cam", "-origin", "https://feed.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	m := tokenPattern.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no token in:\n%s", out)
	}
	want := "https://feed.example.com/reset?token=" + m[1]
	if !strings.Contains(out, want) {
		t.Errorf("the printed link is not %q:\n%s", want, out)
	}

	// Without an origin there is no link to print, and inventing a host would be
	// a guess that lands the reader somewhere that is not their instance.
	bare, _, err := runReset(t, dbPath, "-user", "cam")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bare, "/reset?token=") {
		t.Errorf("a link was printed with no -origin to build it from:\n%s", bare)
	}
}

func TestResetRefusesWithoutAUserAndForAnUnknownOne(t *testing.T) {
	dbPath, _ := resetFixture(t)

	if _, _, err := runReset(t, dbPath); err == nil {
		t.Error("reset ran with no -user")
	}
	if _, _, err := runReset(t, dbPath, "-user", "   "); err == nil {
		t.Error("reset accepted a whitespace-only -user")
	}

	_, _, err := runReset(t, dbPath, "-user", "nobody")
	if err == nil {
		t.Fatal("reset minted a token for an account that does not exist")
	}
	if !strings.Contains(err.Error(), "nobody") {
		t.Errorf("the error does not name the account asked for: %v", err)
	}
}

// --- promptPassword -------------------------------------------------------------

// withStdin replaces os.Stdin with a pipe carrying s.
//
// Under `go test` stdin is never a terminal, so this exercises the FALLBACK
// half of promptPassword — the one that makes
// `echo hunter2xxxxx | articleflux init -user cam` work. The terminal half
// cannot be reached without a pty and is not simulated here; what is asserted
// is that the non-terminal path never waits for an echo it will not get.
func withStdin(t *testing.T, s string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = prev
		_ = r.Close()
	})
	go func() {
		_, _ = w.WriteString(s)
		_ = w.Close()
	}()
}

func TestPromptPasswordReadsAPipedLineAndStripsTheNewline(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"hunter2xxxxxxxx\n", "hunter2xxxxxxxx"},
		{"hunter2xxxxxxxx\r\n", "hunter2xxxxxxxx"}, // a file written on Windows
		{"hunter2xxxxxxxx", "hunter2xxxxxxxx"},     // no trailing newline at all: EOF with content
		// Spaces are kept. A password is bytes, and trimming them here would
		// mean a password that works at the prompt and not at the login.
		{"  spaced out  \n", "  spaced out  "},
	} {
		withStdin(t, tc.in)
		got, err := promptPassword("Password: ")
		if err != nil {
			t.Fatalf("promptPassword(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("promptPassword(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Closed stdin with nothing on it is an error rather than an empty password.
// Returning "" would hand pwpolicy an empty string and produce a confusing
// rejection instead of "there was no input".
func TestPromptPasswordFailsOnEmptyStdinRatherThanReturningNothing(t *testing.T) {
	withStdin(t, "")
	if got, err := promptPassword("Password: "); err == nil {
		t.Errorf("promptPassword returned %q for closed stdin, want an error", got)
	}
}

// The prompt itself goes to stderr, so stdout stays clean for anything being
// piped — `articleflux reset` prints a credential to stdout and a prompt mixed
// into it would end up in whatever the operator pasted it into.
func TestPromptPasswordKeepsStdoutClean(t *testing.T) {
	withStdin(t, "hunter2xxxxxxxx\n")
	out := captureStdout(t, func() {
		if _, err := promptPassword("Password: "); err != nil {
			t.Error(err)
		}
	})
	if out != "" {
		t.Errorf("the prompt was written to stdout: %q", out)
	}
}
