package grpcsrv

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"github.com/monstercameron/ArticleFlux/internal/authn"
	"github.com/monstercameron/ArticleFlux/internal/buildver"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// newAuthDevMode builds a second AuthServer over newAuth's database, wired the
// way internal/app actually wires DevMode: scopeOf ignores the credential
// entirely and always resolves the single local account (see
// app.devScope/FirstUserScope). Modelled here rather than imported, since this
// package should not depend on internal/app for a test fixture.
func newAuthDevMode(t *testing.T) (*AuthServer, *store.ReaderRepo) {
	t.Helper()
	_, repo := newAuth(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	scopeOf := func(ctx context.Context) (store.Scope, error) {
		return repo.FirstUserScope(ctx)
	}
	return NewAuthServer(repo, scopeOf, log, true), repo
}

func TestNewAuthServerDevModeReportsDevModeAndNeedsNoToken(t *testing.T) {
	s, _ := newAuthDevMode(t)
	// No metadata at all — DevMode's whole point is a credential-free box.
	who, err := s.WhoAmI(context.Background(), &pb.WhoAmIRequest{})
	if err != nil {
		t.Fatalf("whoami in dev mode: %v", err)
	}
	if !who.GetDevMode() {
		t.Error("dev_mode is false on a server constructed with devMode=true")
	}
	if who.GetUsername() != "cam" {
		t.Errorf("username = %q, want cam", who.GetUsername())
	}
}

func TestUserAgentTruncatesLongValuesAndDoesNotPanic(t *testing.T) {
	if got := userAgent(context.Background()); got != "" {
		t.Errorf("no metadata: got %q, want empty", got)
	}
	empty := metadata.NewIncomingContext(context.Background(), metadata.Pairs("user-agent", ""))
	if got := userAgent(empty); got != "" {
		t.Errorf("empty header: got %q, want empty", got)
	}
	short := metadata.NewIncomingContext(context.Background(), metadata.Pairs("user-agent", "curl/8.0"))
	if got := userAgent(short); got != "curl/8.0" {
		t.Errorf("short header: got %q, want curl/8.0", got)
	}
	otherKey := metadata.NewIncomingContext(context.Background(), metadata.Pairs("some-other-header", "x"))
	if got := userAgent(otherKey); got != "" {
		t.Errorf("metadata present without a user-agent key: got %q, want empty", got)
	}

	long := strings.Repeat("a", 500)
	longCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("user-agent", long))
	got := userAgent(longCtx)
	if len(got) != 256 {
		t.Fatalf("long header: len = %d, want 256 (bounded, not stored whole)", len(got))
	}
	if got != long[:256] {
		t.Error("truncation did not keep the prefix")
	}
}

func TestClientStampReadsTheHeaderAndDefaultsToEmpty(t *testing.T) {
	if got := clientStamp(context.Background()); got != "" {
		t.Errorf("no metadata: got %q, want empty", got)
	}
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(buildver.ClientStampHeader, "1.2.3+abcdef"))
	if got := clientStamp(ctx); got != "1.2.3+abcdef" {
		t.Errorf("got %q, want 1.2.3+abcdef", got)
	}
	emptyVal := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(buildver.ClientStampHeader, ""))
	if got := clientStamp(emptyVal); got != "" {
		t.Errorf("header present but empty: got %q, want empty", got)
	}
	// Metadata exists on the call, but not this key — distinct from no
	// metadata at all, and the branch that return "" at the end of the
	// function rather than the early !ok return.
	otherKey := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("some-other-header", "x"))
	if got := clientStamp(otherKey); got != "" {
		t.Errorf("metadata present without the client-stamp key: got %q, want empty", got)
	}
}

// record's own write-and-notify logic is exercised transitively by every
// Login call in auth_test.go; the gap is specifically the onOutcome branch,
// so it is driven directly rather than through more login churn.
func TestRecordWritesTheLedgerWithNoCallbackInstalled(t *testing.T) {
	s, repo := newAuth(t)
	ctx := context.Background()

	s.record(ctx, "record-direct-test", "9.9.9.9", store.LoginBadPassword)

	uf, _, err := repo.FailureCounts(ctx, "record-direct-test", "9.9.9.9", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if uf != 1 {
		t.Errorf("ledger fail count = %d, want 1 — record did not write the row", uf)
	}
}

// WithLoginMetrics is the counter's only installation point, and this drives
// Login through all four outcomes so the callback's wiring — not just
// record's SQL write — is what is under test.
func TestWithLoginMetricsSeesEveryLoginOutcome(t *testing.T) {
	s, _ := newAuth(t)
	ctx := context.Background()

	var got []store.LoginOutcome
	s.WithLoginMetrics(func(_ context.Context, o store.LoginOutcome) {
		got = append(got, o)
	})

	if _, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: testPassword}); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := s.Login(ctx, &pb.LoginRequest{Username: "ghost", Password: "wrong-password-here"}); err == nil {
		t.Fatal("unknown user succeeded")
	}
	// Free+1 wrong passwords, exactly the shape TestTheLockoutIsEnforced… in
	// auth_test.go uses to trip the durable lockout on the NEXT attempt.
	for i := 0; i <= authn.DefaultLockout.Free; i++ {
		_, _ = s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: "wrong-password-here"})
	}
	_, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: "wrong-password-here"})
	if codeOf(err) != codes.ResourceExhausted {
		t.Fatalf("expected the durable lockout to trip, got %v", err)
	}

	seen := map[store.LoginOutcome]bool{}
	for _, o := range got {
		seen[o] = true
	}
	for _, want := range []store.LoginOutcome{
		store.LoginOK, store.LoginBadPassword, store.LoginUnknownUser, store.LoginLocked,
	} {
		if !seen[want] {
			t.Errorf("WithLoginMetrics never saw outcome %q; saw %v", want, got)
		}
	}
}
