package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestUpgradeIsGatedOnReadiness — §20.19.9.
//
// `/readyz` existed from the start and nothing consulted it, so `/grpc` would
// accept a tunnel into an instance that could not answer a single call. The
// client that results is the worst kind: connected, failing every RPC, and
// retrying hard against a server that is already struggling. 503 with
// `Retry-After` instead, which is an instruction the backoff already knows how
// to obey.
func TestUpgradeIsGatedOnReadiness(t *testing.T) {
	a, err := Open(t.Context(), Config{DBPath: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)

	// Ready: the upgrade is attempted, and fails on the handshake rather than on
	// the gate. Anything but 503 proves the request reached the tunnel.
	res, err := http.Get(srv.URL + "/grpc")
	if err != nil {
		t.Fatalf("GET /grpc: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode == http.StatusServiceUnavailable {
		t.Fatalf("a healthy server refused the upgrade with 503")
	}

	// Now break it in the way that matters: the database is gone, so reads
	// cannot be served, while the HTTP surface is still perfectly happy to
	// accept a WebSocket.
	if err := a.Close(); err != nil {
		t.Logf("Close: %v", err) // expected on an already-draining server
	}

	res, err = http.Get(srv.URL + "/grpc")
	if err != nil {
		t.Fatalf("GET /grpc after close: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503: an unready server that accepts the tunnel "+
			"produces a client that connects and then fails every call, which "+
			"classifies as an outage and is retried hard", res.StatusCode)
	}
	// The header is the point, not decoration. Without it the client falls back
	// to its own schedule, which knows nothing about when this server expects to
	// be able to answer.
	if got := res.Header.Get("Retry-After"); got == "" {
		t.Error("no Retry-After on a 503: the refusal tells the client to go away " +
			"without telling it when to come back")
	}
}
