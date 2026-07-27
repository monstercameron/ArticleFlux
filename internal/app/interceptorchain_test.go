package app

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	"github.com/monstercameron/ArticleFlux/internal/buildver"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/ratelimit"
	"github.com/monstercameron/ArticleFlux/internal/skew"
)

// buildHandler documents three interceptors and installs five; skew and the
// rate limiter are wired between reqid and idem but are not named in the
// comment above grpc.ChainUnaryInterceptor in app.go. That comment is stale
// documentation of an otherwise-correct chain, not a functional bug — the
// relationships it does describe (reqid before idem, idem before the latency
// timer) still hold, and skew's own inline comment separately states it runs
// "ahead of everything that does work". The tests below pin the actual
// behaviour rather than the prose.

// dialFor opens a real client connection through app.go's real handler: a
// WebSocket upgrade, a hijacked socket, HTTP/2 over it, and the whole
// interceptor chain buildHandler assembles. Nothing here is faked.
func dialFor(t *testing.T, url string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpctunnel.DialContext(t.Context(), url,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// staleStampCtx tags a call as coming from a client below the server's
// configured minimum (buildver.MinClient = "0.1.0").
func staleStampCtx(t *testing.T) context.Context {
	t.Helper()
	return metadata.AppendToOutgoingContext(t.Context(), buildver.ClientStampHeader, "0.0.1")
}

// TestSkewIsInstalledOnTheRealServer is skew's half of what
// TestTheLimiterIsInstalledOnTheRealServer already does for the rate limiter:
// drive a real tunnel into buildHandler's real chain and delete the wiring
// line in app.go to see this fail. Nothing exercises this today — the only
// end-to-end interceptor-wiring test in this package is the rate limiter's.
func TestSkewIsInstalledOnTheRealServer(t *testing.T) {
	url, _ := tunnelTo(t, false)
	client := pb.NewReaderServiceClient(dialFor(t, url))

	_, err := client.ListFeeds(staleStampCtx(t), &pb.ListFeedsRequest{})
	if err == nil {
		t.Fatal("a client stamped below the server's minimum was served — skew is not " +
			"in the chain buildHandler assembles")
	}
	if code := status.Code(err); code != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", code)
	}
	if !strings.Contains(err.Error(), skew.Sentinel) {
		t.Errorf("error %q does not carry %q, which is the substring the client "+
			"recognises to offer Reload instead of retrying forever", err.Error(), skew.Sentinel)
	}
}

// A current stamp must pass, so the test above is about the version and not
// about the header merely being present.
func TestACurrentClientStampPassesOnTheRealServer(t *testing.T) {
	url, _ := tunnelTo(t, false)
	client := pb.NewReaderServiceClient(dialFor(t, url))

	ctx := metadata.AppendToOutgoingContext(t.Context(), buildver.ClientStampHeader, buildver.Version)
	_, err := client.ListFeeds(ctx, &pb.ListFeedsRequest{})
	// No session, so this must fail — but on authentication, never on skew.
	if status.Code(err) == codes.FailedPrecondition && strings.Contains(err.Error(), skew.Sentinel) {
		t.Errorf("a client at the server's own build version was refused for skew: %v", err)
	}
}

// GetVersion is skew's documented exemption: refusing the one call that
// explains a refusal is a closed loop for a client that cannot get past it.
func TestSkewExemptsGetVersionOnTheRealServer(t *testing.T) {
	url, _ := tunnelTo(t, false)
	client := pb.NewSystemServiceClient(dialFor(t, url))

	if _, err := client.GetVersion(staleStampCtx(t), &pb.GetVersionRequest{}); err != nil {
		t.Errorf("GetVersion refused a too-old client: %v — that client can now never "+
			"learn why it is being refused", err)
	}
}

// TestSkewRunsBeforeTheRateLimiterOnTheRealServer is the ordering property
// itself, proven by an observable side effect rather than by reading
// buildHandler: a caller whose bucket is already exhausted, and who is ALSO
// below the version minimum, must be told it is too old — not that it is
// rate limited. If the two interceptors were swapped, the exhausted bucket
// would answer first and this would see ResourceExhausted instead.
func TestSkewRunsBeforeTheRateLimiterOnTheRealServer(t *testing.T) {
	url, _ := tunnelTo(t, false)
	client := pb.NewReaderServiceClient(dialFor(t, url))

	authed := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer chain-order-probe")
	current := metadata.AppendToOutgoingContext(authed, buildver.ClientStampHeader, buildver.Version)

	exhausted := false
	for i := 0; i < ratelimit.DefaultPerUser.Burst+20; i++ {
		_, err := client.ListFeeds(current, &pb.ListFeedsRequest{})
		if status.Code(err) == codes.ResourceExhausted {
			exhausted = true
			break
		}
	}
	if !exhausted {
		t.Fatal("never exhausted this credential's bucket, so the next call proves nothing")
	}

	stale := metadata.AppendToOutgoingContext(authed, buildver.ClientStampHeader, "0.0.1")
	_, err := client.ListFeeds(stale, &pb.ListFeedsRequest{})
	code := status.Code(err)
	if code != codes.FailedPrecondition {
		t.Errorf("an exhausted AND too-old caller got %v, want FailedPrecondition. Skew "+
			"must run before the rate limiter so a below-minimum client is refused "+
			"before its request does any work; a swapped order would show "+
			"ResourceExhausted here instead, from the bucket exhausted above", code)
	}
	if !strings.Contains(err.Error(), skew.Sentinel) {
		t.Errorf("the refusal does not carry the skew sentinel: %v", err)
	}
}
