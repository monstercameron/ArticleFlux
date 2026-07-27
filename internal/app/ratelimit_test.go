package app

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"

	"github.com/monstercameron/ArticleFlux/internal/ratelimit"
)

func ctxWithAuth(token string) context.Context {
	md := metadata.Pairs("authorization", token)
	return metadata.NewIncomingContext(context.Background(), md)
}

func ctxFromAddr(ip string, port int) context.Context {
	return peer.NewContext(context.Background(),
		&peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: port}})
}

// The same session is one bucket however it spells its scheme. A client
// sending "bearer" must not be handed a second budget by capitalisation.
func TestTheBearerSchemeIsNotPartOfTheKey(t *testing.T) {
	a := &App{}
	upper := a.rateKey(ctxWithAuth("Bearer tok-abc"))
	lower := a.rateKey(ctxWithAuth("bearer tok-abc"))
	if upper == "" {
		t.Fatal("no key was derived from a bearer token")
	}
	if upper != lower {
		t.Errorf("Bearer gave %q and bearer gave %q; one session, two budgets", upper, lower)
	}
}

func TestDifferentSessionsGetDifferentKeys(t *testing.T) {
	a := &App{}
	if one, two := a.rateKey(ctxWithAuth("Bearer tok-one")),
		a.rateKey(ctxWithAuth("Bearer tok-two")); one == two {
		t.Errorf("two sessions share the key %q; one busy client would limit the other", one)
	}
}

// The key ends up in a log line, and the session token is a bearer credential.
// Writing it out hands live sessions to anyone who can read the log.
func TestTheRawTokenNeverAppearsInTheKey(t *testing.T) {
	const token = "a-secret-session-token"
	a := &App{}
	if key := a.rateKey(ctxWithAuth("Bearer " + token)); strings.Contains(key, token) {
		t.Errorf("the key %q contains the raw token", key)
	}
}

// An unauthenticated call — Login, before there is a session — keys on the
// address. Behind a proxy that is now the caller's own rather than nginx's,
// which is the other half of this ticket.
func TestAnUnauthenticatedCallKeysOnTheAddress(t *testing.T) {
	a := &App{}
	key := a.rateKey(ctxFromAddr("203.0.113.7", 51234))
	if key != "c:203.0.113.7" {
		t.Errorf("key = %q, want c:203.0.113.7", key)
	}
}

// The port must not reach the key, or every new socket is a fresh budget.
func TestTheAddressKeyIgnoresThePort(t *testing.T) {
	a := &App{}
	if one, two := a.rateKey(ctxFromAddr("203.0.113.7", 1111)),
		a.rateKey(ctxFromAddr("203.0.113.7", 2222)); one != two {
		t.Errorf("two ports gave %q and %q; each reconnect would reset the limit", one, two)
	}
}

// Neither a credential nor an address. Empty means "cannot tell", and the
// interceptor lets those through rather than refusing everyone.
func TestAnUnidentifiableCallerYieldsNoKey(t *testing.T) {
	a := &App{}
	if key := a.rateKey(context.Background()); key != "" {
		t.Errorf("key = %q, want empty so the call fails open", key)
	}
}

// A session and an address must not collide: the prefixes are what keep a
// token that happens to hash to an address's spelling out of its bucket.
func TestSessionAndAddressKeysAreInDifferentNamespaces(t *testing.T) {
	a := &App{}
	session := a.rateKey(ctxWithAuth("Bearer tok"))
	address := a.rateKey(ctxFromAddr("203.0.113.7", 1))
	if !strings.HasPrefix(session, "s:") || !strings.HasPrefix(address, "c:") {
		t.Errorf("keys are not namespaced: session=%q address=%q", session, address)
	}
}

// DevMode has no credential to key on, so the limiter would collapse every tab
// and every parallel e2e worker into the 127.0.0.1 bucket and start refusing
// the developer's own second window. It is off there, deliberately.
func TestTheLimiterIsOffInDevModeAndOnOtherwise(t *testing.T) {
	ran := 0
	handler := func(ctx context.Context, req any) (any, error) {
		ran++
		return nil, nil
	}
	dev := (&App{cfg: Config{DevMode: true}}).rateLimitUnary()
	// Far past DefaultPerUser's burst, from one indistinguishable caller.
	ctx := ctxFromAddr("127.0.0.1", 1)
	for i := 0; i < ratelimit.DefaultPerUser.Burst+50; i++ {
		if _, err := dev(ctx, nil, &grpc.UnaryServerInfo{}, handler); err != nil {
			t.Fatalf("dev call %d was rate limited: %v", i+1, err)
		}
	}
	if ran != ratelimit.DefaultPerUser.Burst+50 {
		t.Errorf("the dev handler ran %d times, want every call", ran)
	}

	// And the same traffic against a non-dev instance IS refused, so the
	// exemption above is the only thing turning it off.
	prod := (&App{}).rateLimitUnary()
	refused := false
	for i := 0; i < ratelimit.DefaultPerUser.Burst+50; i++ {
		if _, err := prod(ctx, nil, &grpc.UnaryServerInfo{}, handler); err != nil {
			refused = true
			break
		}
	}
	if !refused {
		t.Error("a non-dev instance never refused; the limiter is not wired at all")
	}
}

// TestTheLimiterIsInstalledOnTheRealServer drives a real tunnel into a real
// gRPC server and pushes one caller past the burst.
//
// The other tests here build interceptors by hand, which proves the logic and
// says nothing about whether it is WIRED. This one goes through the WebSocket
// upgrade, the HTTP/2 session and the interceptor chain as assembled by
// buildHandler, so deleting the line in app.go fails it.
//
// The credential is nonsense on purpose. rateKey reads the metadata and does
// not validate it — the point is that the limiter runs BEFORE the handler, so
// a caller who cannot authenticate is still counted rather than being handed a
// free channel to hammer. The assertion is the code CHANGING from
// Unauthenticated to ResourceExhausted, which is only possible if the
// interceptor is in the chain.
func TestTheLimiterIsInstalledOnTheRealServer(t *testing.T) {
	url, _ := tunnelTo(t, false)
	conn, err := grpctunnel.DialContext(t.Context(), url,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := pb.NewReaderServiceClient(conn)
	ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer not-a-real-session")

	sawAuthFailure := false
	for i := 0; i < ratelimit.DefaultPerUser.Burst+20; i++ {
		_, err := client.ListFeeds(ctx, &pb.ListFeedsRequest{})
		code := status.Code(err)
		if code == codes.ResourceExhausted {
			if !sawAuthFailure {
				t.Fatal("the first call was already rate limited, so this proves " +
					"nothing about where the limit is")
			}
			return // limited, after being let through the burst
		}
		if code == codes.Unauthenticated || code == codes.PermissionDenied ||
			code == codes.NotFound || code == codes.Internal {
			sawAuthFailure = true
			continue
		}
		if err != nil {
			t.Fatalf("call %d failed unexpectedly with %v: %v", i+1, code, err)
		}
		sawAuthFailure = true
	}
	t.Errorf("%d calls from one caller were never limited — the interceptor is not "+
		"in the chain that buildHandler assembles", ratelimit.DefaultPerUser.Burst+20)
}
