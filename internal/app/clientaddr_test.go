package app

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The proxied client's address, and the one nginx would otherwise supply for
// everybody on the instance.
const (
	realClient   = "203.0.113.7"
	proxyPretend = "127.0.0.1"
)

// tunnelTo starts a real app and returns its ws:// URL and repo.
//
// It is the keepalive tests' serveTunnel with the one knob these tests are
// about. Kept separate rather than parameterised because those tests are about
// pings and these are about identity, and a shared helper with a boolean is how
// two unrelated tests start failing together.
func tunnelTo(t *testing.T, behindProxy bool) (string, *App) {
	t.Helper()
	a, err := Open(t.Context(), Config{
		DBPath:       filepath.Join(t.TempDir(), "test.db"),
		PollInterval: 0, // a transport test; a fetcher would only add flakiness
		BehindProxy:  behindProxy,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/grpc", a
}

// failOneLogin drives one doomed login through the tunnel, announcing itself as
// realClient in the handshake exactly as nginx would.
//
// The login has to FAIL: a failure is what the ledger records an address for,
// and the address in that row is the whole assertion.
func failOneLogin(t *testing.T, url, username string) {
	t.Helper()
	conn, err := grpctunnel.DialContext(t.Context(), url,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpctunnel.WithHeader("X-Forwarded-For", realClient),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer func() { _ = conn.Close() }()

	_, err = pb.NewAuthServiceClient(conn).Login(t.Context(), &pb.LoginRequest{
		Username: username,
		Password: "not the password",
	})
	if err == nil {
		t.Fatal("a login with no account behind it succeeded")
	}
}

// TestTheForwardedAddressReachesTheLoginLedger is TODO 7.3d, asserted end to
// end rather than by reading the plumbing.
//
// Everything between the handshake header and the row in `login_attempts` is
// real: a WebSocket upgrade, a hijacked socket, an HTTP/2 server synthesising
// requests over it, a gRPC peer derived from the connection, and the limiter
// keying on that peer. There is no seam in the middle where the address could
// be asserted more cheaply and still mean anything — the failure this catches
// is precisely that one of those layers drops it.
func TestTheForwardedAddressReachesTheLoginLedger(t *testing.T) {
	url, a := tunnelTo(t, true)
	failOneLogin(t, url, "nobody")

	_, ipFails, err := a.Repo().FailureCounts(t.Context(), "", realClient, time.Minute)
	if err != nil {
		t.Fatalf("FailureCounts: %v", err)
	}
	if ipFails != 1 {
		t.Errorf("the ledger counted %d failures against %s, want 1 — the forwarded "+
			"address is not reaching the limiter, so every user on the instance "+
			"still shares one bucket", ipFails, realClient)
	}

	// And it is not ALSO recorded against the proxy, which would mean the
	// address was added beside the old one rather than replacing it — the
	// per-instance bucket would survive under a different name.
	_, proxyFails, err := a.Repo().FailureCounts(t.Context(), "", proxyPretend, time.Minute)
	if err != nil {
		t.Fatalf("FailureCounts: %v", err)
	}
	if proxyFails != 0 {
		t.Errorf("the ledger counted %d failures against the proxy %s, want 0",
			proxyFails, proxyPretend)
	}
}

// The header is only as trustworthy as the operator's assertion about it.
//
// Without -behind-proxy anyone can send X-Forwarded-For, so believing it would
// hand every attacker a fresh limiter bucket per attempt — strictly worse than
// the shared bucket this ticket set out to fix. This is the test that makes the
// dangerous configuration the one that has to be asked for.
func TestTheForwardedAddressIsIgnoredWithoutBehindProxy(t *testing.T) {
	url, a := tunnelTo(t, false)
	failOneLogin(t, url, "nobody")

	_, ipFails, err := a.Repo().FailureCounts(t.Context(), "", realClient, time.Minute)
	if err != nil {
		t.Fatalf("FailureCounts: %v", err)
	}
	if ipFails != 0 {
		t.Errorf("an untrusted X-Forwarded-For was believed: %d failures landed on %s. "+
			"Any client can send that header, so this lets an attacker rotate "+
			"buckets at will", ipFails, realClient)
	}

	// The transport address is still recorded, so the limiter has not simply
	// stopped having an opinion.
	_, loopbackFails, err := a.Repo().FailureCounts(t.Context(), "", proxyPretend, time.Minute)
	if err != nil {
		t.Fatalf("FailureCounts: %v", err)
	}
	if loopbackFails != 1 {
		t.Errorf("the ledger counted %d failures against the transport address %s, want 1",
			loopbackFails, proxyPretend)
	}
}
