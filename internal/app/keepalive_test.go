package app

import (
	"context"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	"github.com/monstercameron/ArticleFlux/internal/connpolicy"
)

// The two tests here are the ones that would have caught the §20.19 audit's
// findings by running rather than by reading. Both are native, both take about
// a second, and neither needs a browser: the client half of the tunnel has a
// native build, so everything except the DOM is reachable from `go test`.

// serveTunnel starts a real app on a real listener and returns its ws:// URL.
func serveTunnel(t *testing.T) string {
	t.Helper()
	a, err := Open(t.Context(), Config{
		DBPath: filepath.Join(t.TempDir(), "test.db"),
		// No poller: this is a transport test, and a background fetcher would
		// add network flakiness to a question about pings.
		PollInterval: 0,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/grpc"
}

// dialTunnel opens a connection with the keepalive parameters under test.
func dialTunnel(t *testing.T, url string, interval, timeout time.Duration) *grpc.ClientConn {
	t.Helper()
	conn, err := grpctunnel.DialContext(t.Context(), url,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpctunnel.WithTunnelKeepalive(interval, timeout),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	conn.Connect()
	return conn
}

// waitFor blocks until the connection reaches state, or the test fails.
func waitFor(t *testing.T, conn *grpc.ClientConn, want connectivity.State, within time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), within)
	defer cancel()
	for {
		s := conn.GetState()
		if s == want {
			return
		}
		if !conn.WaitForStateChange(ctx, s) {
			t.Fatalf("connection sat in %v for %v, waiting for %v", s, within, want)
		}
	}
}

// TestIdleTunnelIsNotKicked is the regression test for the trap that makes
// client keepalive dangerous to ship on its own (§20.19.3).
//
// The client probes an IDLE tunnel, which is the entire point of the feature —
// an idle connection is exactly when a dead one goes unnoticed. gRPC's DEFAULT
// enforcement (MinTime 5m, PermitWithoutStream false) answers that with
// `GOAWAY ENHANCE_YOUR_CALM (too_many_pings)` on the second ping, so a server
// that merely forgets `KeepaliveEnforcementPolicy` does not lose keepalive: it
// gains a reconnect loop, roughly once a minute, that reads like a flaky
// network.
//
// **This is what fails if someone deletes that option as unused.** At the
// shipping numbers (30s probe, 20s floor) observing it takes over a minute of
// real time, so the floor is lowered here and the same behaviour is watched in
// under two seconds. The numbers themselves are checked in
// internal/connpolicy.
func TestIdleTunnelIsNotKicked(t *testing.T) {
	restore := keepaliveMinTime
	keepaliveMinTime = 50 * time.Millisecond
	t.Cleanup(func() { keepaliveMinTime = restore })

	url := serveTunnel(t)
	// Legal against the floor above, and ~20 pings inside the window below. Under
	// the gRPC defaults every one of them is a strike.
	conn := dialTunnel(t, url, 100*time.Millisecond, 500*time.Millisecond)
	waitFor(t, conn, connectivity.Ready, 10*time.Second)

	// Nothing is sent for two seconds: no RPCs, no streams. Just the probe.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s := conn.GetState(); s != connectivity.Ready {
			t.Fatalf("an idle, correctly-probing client left Ready for %v — the server "+
				"is enforcing gRPC's defaults, which means KeepaliveEnforcementPolicy "+
				"is missing and every real client will flap about once a minute", s)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestHalfOpenSocketIsDetected is F3, the failure that had no detection at all.
//
// A blackhole — a path that accepts bytes and silently stops delivering them,
// with no FIN and no RST — is the only honest way to reproduce a CGNAT reclaim
// or a VPN drop. Nothing in the connection is closed, so without a probe the
// client sits in Ready forever and the indicator reports a live connection over
// a socket that has been dead for an hour.
//
// Playwright cannot make a browser do this, which is why the test lives here.
//
// It takes about twelve seconds and cannot be made much faster: gRPC clamps a
// client keepalive interval to a 10s minimum, SILENTLY, so a test that asks for
// 200ms is really asking for ten seconds and a test that then waits five for a
// detection is asserting something impossible. That clamp is the reason this is
// slow, and it is pinned as an invariant in internal/connpolicy — the trap is
// not that 10s is the floor, it is that going under it changes nothing and says
// nothing.
func TestHalfOpenSocketIsDetected(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out a real keepalive interval; gRPC's 10s floor is the reason")
	}
	restore := keepaliveMinTime
	keepaliveMinTime = time.Second
	t.Cleanup(func() { keepaliveMinTime = restore })

	upstream := serveTunnel(t)
	bh := newBlackhole(t, strings.TrimSuffix(strings.TrimPrefix(upstream, "ws://"), "/grpc"))

	// The floor, and a short timeout: it is the interval that cannot be lowered.
	conn := dialTunnel(t, "ws://"+bh.addr()+"/grpc", connpolicy.GRPCClientFloor, time.Second)
	waitFor(t, conn, connectivity.Ready, 10*time.Second)

	// The network vanishes without saying so.
	bh.cut()

	// One probe interval plus one timeout, with room for scheduling. In
	// production that budget is 30s + 10s (§20.19.4); before the probe existed
	// the honest entry in that table was "never".
	ctx, cancel := context.WithTimeout(t.Context(),
		connpolicy.GRPCClientFloor+5*time.Second)
	defer cancel()
	for conn.GetState() == connectivity.Ready {
		if !conn.WaitForStateChange(ctx, connectivity.Ready) {
			t.Fatal("the connection stayed Ready over a blackholed socket — nothing " +
				"is probing, so a half-open connection is invisible and the " +
				"indicator will report a live tunnel indefinitely")
		}
	}
}

// blackhole is a TCP relay that can stop delivering without closing anything.
//
// The distinction is the whole point. Closing a socket is a failure gRPC has
// always handled — a close event arrives and the redial starts within
// milliseconds. A path that simply stops carrying bytes produces no event at
// all, which is why it needs a probe to detect and a fake like this one to test.
type blackhole struct {
	ln     net.Listener
	target string

	mu      sync.Mutex
	stopped bool
}

func newBlackhole(t *testing.T, target string) *blackhole {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	b := &blackhole{ln: ln, target: target}
	t.Cleanup(func() { _ = ln.Close() })
	go b.serve()
	return b
}

func (b *blackhole) addr() string { return b.ln.Addr().String() }

// cut makes every byte in flight, in both directions, disappear.
func (b *blackhole) cut() {
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()
}

func (b *blackhole) isCut() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stopped
}

func (b *blackhole) serve() {
	for {
		down, err := b.ln.Accept()
		if err != nil {
			return
		}
		up, err := net.Dial("tcp", b.target)
		if err != nil {
			_ = down.Close()
			return
		}
		go b.pipe(down, up)
		go b.pipe(up, down)
	}
}

// pipe copies until cut, and then keeps READING and throws the bytes away.
//
// Draining rather than blocking is deliberate: a relay that stopped reading
// would fill the kernel buffer, the peer's writes would block and eventually
// error, and the client would learn something was wrong — from backpressure
// rather than from a probe. That would make the test pass for the wrong reason
// and prove nothing about the keepalive.
func (b *blackhole) pipe(dst, src net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 && !b.isCut() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				return
			}
			return
		}
	}
}
