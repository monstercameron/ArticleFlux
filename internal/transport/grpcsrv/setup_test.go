package grpcsrv

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"github.com/monstercameron/ArticleFlux/internal/authn"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// newAuthEmpty builds a server over a FRESH, unclaimed database — no seeded
// user. newAuth already seeds "cam", which is wrong for Setup's happy path:
// Setup's entire job is claiming an instance that has nobody yet.
func newAuthEmpty(t *testing.T) (*AuthServer, *store.ReaderRepo) {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "setup.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := store.NewReaderRepo(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	scopeOf := func(ctx context.Context) (store.Scope, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return store.Scope{}, store.ErrNoScope
		}
		v := md.Get("authorization")
		if len(v) == 0 {
			return store.Scope{}, store.ErrNoScope
		}
		sc, err := repo.ScopeForSession(ctx, secret.HashToken(strings.TrimPrefix(v[0], "Bearer ")))
		if err != nil {
			return store.Scope{}, store.ErrNoScope
		}
		return sc, nil
	}
	// Refresh-token issuance is gated off by default (§7.3a SEC4); these tests
	// assert on Setup's refresh token and record id, so they opt in — see
	// WithRefreshTokens.
	return NewAuthServer(repo, scopeOf, log, false).WithRefreshTokens(true), repo
}

func TestSetupHappyPathClaimsAFreshInstance(t *testing.T) {
	s, repo := newAuthEmpty(t)

	res, err := s.Setup(context.Background(), &pb.SetupRequest{
		Username: "cam", Email: "cam@example.com", Password: testPassword,
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if res.GetToken() == "" {
		t.Error("setup returned no token")
	}
	if res.GetRole() != "superadmin" {
		t.Errorf("role = %q, want superadmin", res.GetRole())
	}
	if res.GetRefreshRecordId() == "" {
		t.Error("setup returned no refresh record id")
	}
	if res.GetRefreshToken() == "" {
		t.Error("setup returned no refresh token")
	}
	if len(res.GetRecoveryCodes()) != authn.RecoveryCodeCount {
		t.Errorf("got %d recovery codes, want %d", len(res.GetRecoveryCodes()), authn.RecoveryCodeCount)
	}

	// The minted token actually works, and it works for the sudo-gated calls
	// too — a login IS an authentication, and so is claiming the instance.
	who, err := s.WhoAmI(withToken(res.GetToken()), &pb.WhoAmIRequest{})
	if err != nil {
		t.Fatalf("whoami with the setup token: %v", err)
	}
	if who.GetUsername() != "cam" {
		t.Errorf("username = %q, want cam", who.GetUsername())
	}
	if _, err := s.RegenerateRecoveryCodes(withToken(res.GetToken()),
		&pb.RegenerateRecoveryCodesRequest{}); err != nil {
		t.Errorf("a sudo-gated call right after setup was refused: %v", err)
	}

	n, err := repo.CountUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("CountUsers = %d, want exactly 1", n)
	}
}

func TestSetupRejectsAUsernameTooShort(t *testing.T) {
	s, _ := newAuthEmpty(t)
	_, err := s.Setup(context.Background(), &pb.SetupRequest{
		Username: "a", Password: testPassword,
	})
	if got := codeOf(err); got != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

func TestSetupRejectsAWeakPassword(t *testing.T) {
	s, _ := newAuthEmpty(t)
	_, err := s.Setup(context.Background(), &pb.SetupRequest{
		Username: "cam", Password: "short",
	})
	if got := codeOf(err); got != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

func TestSetupRejectsAnEmailThatDoesNotLookLikeOne(t *testing.T) {
	s, _ := newAuthEmpty(t)
	_, err := s.Setup(context.Background(), &pb.SetupRequest{
		Username: "cam", Email: "not-an-email", Password: testPassword,
	})
	if got := codeOf(err); got != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

// This is, per the file's own doc comment, "the whole security story": Setup
// takes a password over an UNAUTHENTICATED call and mints a superadmin. If a
// second call can do it again, the endpoint is a permanent backdoor rather
// than a one-time claim.
func TestSetupRefusesASecondAccountAfterOneExists(t *testing.T) {
	s, repo := newAuthEmpty(t)

	first, err := s.Setup(context.Background(), &pb.SetupRequest{
		Username: "cam", Password: testPassword,
	})
	if err != nil {
		t.Fatalf("first setup: %v", err)
	}

	_, err = s.Setup(context.Background(), &pb.SetupRequest{
		Username: "mallory", Password: "a-completely-different-passphrase",
	})
	if err == nil {
		t.Fatal("SECURITY: a second Setup call created a second superadmin account " +
			"over an unauthenticated RPC")
	}
	if got := codeOf(err); got != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition (srv.alreadySetUp)", got)
	}

	n, err := repo.CountUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("SECURITY: CountUsers = %d after a refused second setup, want 1", n)
	}
	// And the first account's session is untouched by the refused attempt.
	if _, err := s.WhoAmI(withToken(first.GetToken()), &pb.WhoAmIRequest{}); err != nil {
		t.Errorf("the first account's session stopped working after a refused second setup: %v", err)
	}
	// The second username must not have been able to sign in either.
	if _, err := s.Login(context.Background(), &pb.LoginRequest{
		Username: "mallory", Password: "a-completely-different-passphrase",
	}); err == nil {
		t.Error("SECURITY: the rejected second account can log in anyway")
	}
}

func TestLooksLikeEmail(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"ordinary address", "cam@example.com", true},
		{"multi-label domain", "cam@example.co.uk", true},
		{"no at sign", "no-at-sign", false},
		{"leading at sign", "@example.com", false},
		{"trailing at sign, nothing after", "cam@", false},
		{"two at signs", "two@@example.com", false},
		{"three-part with two at signs", "a@b@c.com", false},
		{"no dot in domain", "nodotindomain@localhost", false},
		{"dot at the very start of the domain", "cam@.com", false},
		{"dot at the very end of the domain", "cam@example.", false},
		{"leading whitespace", " cam@example.com", false},
		{"trailing whitespace", "cam@example.com ", false},
		{"internal whitespace", "cam @example.com", false},
		{"empty string", "", false},
	}
	for _, c := range cases {
		if got := looksLikeEmail(c.in); got != c.want {
			t.Errorf("%s (%q): looksLikeEmail = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}
