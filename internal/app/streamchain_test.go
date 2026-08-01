package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/buildver"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// P1: a streaming call must be subject to the limits every other call is.
//
// The chain used to carry authorization and nothing else, which was harmless
// while nothing streamed. `WatchEvents` holds a goroutine and a bus subscription
// for as long as a tab is open, so "how many may one caller hold" stopped being
// theoretical the day it shipped — and the tunnel's per-client WebSocket cap is
// not that limit, because every stream multiplexes over one WebSocket.

// openWatch starts one event stream and returns it with its cancel.
//
// It waits for the stream to be ESTABLISHED rather than merely requested:
// `NewStream` returns before the server has run its interceptors, so a test that
// counted requests would count streams the server has not yet accepted — and
// then race its own limit.
// streamHarness is a real server with a real session: every stream below has to
// get past authorization before it can be refused by anything more interesting.
func streamHarness(t *testing.T) (*App, pb.EventServiceClient, context.Context) {
	t.Helper()
	const username, password = "cam", "correct-horse-battery"

	a, err := Open(t.Context(), Config{DBPath: t.TempDir() + "/streams.db", PollInterval: 0})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	hash, err := secret.HashPassword(password, secret.Active())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Repo().CreateTenantAndUser(t.Context(), store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: username,
		Hash: hash, Role: "superadmin", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("CreateTenantAndUser: %v", err)
	}

	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	conn := dialFor(t, "ws://"+strings.TrimPrefix(srv.URL, "http://")+"/grpc")

	login, err := pb.NewAuthServiceClient(conn).Login(t.Context(),
		&pb.LoginRequest{Username: username, Password: password})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	authed := metadata.AppendToOutgoingContext(context.Background(),
		"authorization", "Bearer "+login.GetToken())
	return a, pb.NewEventServiceClient(conn), authed
}

func openWatch(t *testing.T, client pb.EventServiceClient, parent context.Context) (pb.EventService_WatchEventsClient, context.CancelFunc, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	stream, err := client.WatchEvents(ctx, &pb.WatchEventsRequest{})
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	// A refusal arrives on the first Recv, not from NewStream and not reliably
	// from Header: gRPC creates the client stream locally and the server's
	// interceptor error is delivered as the stream's first event. An accepted
	// WatchEvents has nothing to send yet, so it simply does not answer — which
	// is what "established" looks like from here.
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		_, err := stream.Recv()
		done <- result{err}
	}()
	select {
	case r := <-done:
		cancel()
		if r.err == nil {
			// An accepted stream that sent something. Also established.
			return stream, func() {}, nil
		}
		return nil, func() {}, r.err
	case <-time.After(2 * time.Second):
		// Silence means the server took it.
		return stream, cancel, nil
	}
}

func TestTheNPlusOnethStreamFromOneCallerIsRefused(t *testing.T) {
	a, client, authed := streamHarness(t)

	var cancels []context.CancelFunc
	t.Cleanup(func() {
		for _, c := range cancels {
			c()
		}
	})

	for i := 0; i < maxStreamsPerCaller; i++ {
		_, cancel, err := openWatch(t, client, authed)
		if err != nil {
			t.Fatalf("stream %d of %d was refused inside the cap: %v", i+1, maxStreamsPerCaller, err)
		}
		cancels = append(cancels, cancel)
	}

	_, cancel, err := openWatch(t, client, authed)
	cancels = append(cancels, cancel)
	if err == nil {
		t.Fatalf("a caller holding %d streams opened another; nothing bounds how many "+
			"goroutines and subscriptions one credential can pin", maxStreamsPerCaller)
	}
	// The same answer a refused unary call gets, so a client needs one branch
	// rather than two.
	if got := status.Code(err); got != codes.ResourceExhausted {
		t.Errorf("code = %v, want ResourceExhausted", got)
	}
	if a.streamCap.Refused() == 0 {
		t.Error("the refusal was not counted, so a cap that starts biting is invisible " +
			"until somebody reports that their second tab stopped updating")
	}
}

// Closing one gives the slot back. A cap that only counts up is a cap that ends
// the session.
func TestClosingAStreamFreesItsSlot(t *testing.T) {
	a, client, authed := streamHarness(t)

	var cancels []context.CancelFunc
	for i := 0; i < maxStreamsPerCaller; i++ {
		_, cancel, err := openWatch(t, client, authed)
		if err != nil {
			t.Fatalf("stream %d refused inside the cap: %v", i+1, err)
		}
		cancels = append(cancels, cancel)
	}
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()

	// Give one back and wait for the server to notice: the release happens when
	// the handler returns, which is one cancellation round trip away.
	cancels[0]()
	deadline := time.Now().Add(10 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		var cancel context.CancelFunc
		_, cancel, err = openWatch(t, client, authed)
		if err == nil {
			cancels = append(cancels, cancel)
			break
		}
		cancel()
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Errorf("a slot was never released after a stream closed: %v", err)
	}
	_ = a
}

// Version skew applies to streams too. A client too old to understand the
// answer should not be handed a stream — it costs more than a refused call,
// because it holds a subscription for as long as the tab stays open.
// DevMode has no way to tell one caller from another, so the stream limiter is
// bypassed exactly as the unary one is (see proxylimit_test.go) — otherwise
// every tab and every parallel test worker collapses into one bucket keyed on
// 127.0.0.1.
func TestRateLimitStreamIsBypassedInDevMode(t *testing.T) {
	a := &App{cfg: Config{DevMode: true}}
	mw := a.rateLimitStream()

	called := false
	err := mw(nil, fakeServerStream{ctx: context.Background()}, &grpc.StreamServerInfo{},
		func(srv any, ss grpc.ServerStream) error { called = true; return nil })
	if err != nil {
		t.Fatalf("DevMode stream limiter refused: %v", err)
	}
	if !called {
		t.Fatal("the handler never ran; DevMode must pass every stream through")
	}
}

// fakeServerStream is the minimal grpc.ServerStream a bare interceptor needs.
type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s fakeServerStream) Context() context.Context { return s.ctx }

// streamTelemetry records a failed stream's status on the span and still
// returns the handler's own error unchanged — the wrapper must not swallow or
// replace it.
func TestStreamTelemetryRecordsAFailedStream(t *testing.T) {
	a, _, _ := streamHarness(t)
	mw := a.streamTelemetry()

	want := status.Error(codes.Internal, "boom")
	err := mw(nil, fakeServerStream{ctx: context.Background()},
		&grpc.StreamServerInfo{FullMethod: "/articleflux.v1.EventService/WatchEvents"},
		func(srv any, ss grpc.ServerStream) error { return want })
	if err != want {
		t.Fatalf("err = %v, want the handler's own error unchanged", err)
	}
}

func TestAStaleClientCannotOpenAStream(t *testing.T) {
	_, client, authed := streamHarness(t)

	stale := metadata.AppendToOutgoingContext(authed, buildver.ClientStampHeader, "0.0.1")
	_, cancel, err := openWatch(t, client, stale)
	defer cancel()
	if err == nil {
		t.Fatal("a client below the minimum opened a stream")
	}
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition — the same refusal a stale unary call gets", got)
	}
}
