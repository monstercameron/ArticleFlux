package skew

import (
	"context"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/buildver"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Version
		ok   bool
	}{
		{"1.4.0", Version{1, 4, 0}, true},
		{"v1.4.0", Version{1, 4, 0}, true},
		{"1.4.0-rc2", Version{1, 4, 0}, true},
		{"0.1.0-dev", Version{0, 1, 0}, true},
		{"1.4.0+abc123", Version{1, 4, 0}, true},
		{"1.4", Version{1, 4, 0}, true},
		{"2", Version{2, 0, 0}, true},
		{" 1.2.3 ", Version{1, 2, 3}, true},
		{"10.20.30", Version{10, 20, 30}, true},
		// A fourth component is silently dropped, not rejected — a stamp with an
		// extra segment is still comparable on the three that matter.
		{"1.2.3.4", Version{1, 2, 3}, true},
		{"", Version{}, false},
		{"nightly", Version{}, false},
		{"1.x.0", Version{}, false},
		{"-1.0.0", Version{}, false},
	}
	for _, c := range cases {
		got, ok := Parse(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("Parse(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
	// The version this build actually ships must parse, or the check silently
	// disables itself on every deploy.
	if _, ok := Parse(buildver.Version); !ok {
		t.Errorf("the build's own version %q does not parse", buildver.Version)
	}
}

func TestOrdering(t *testing.T) {
	less := [][2]string{
		{"1.0.0", "2.0.0"},
		{"1.2.0", "1.3.0"},
		{"1.2.3", "1.2.4"},
		{"0.9.9", "1.0.0"},
		{"1.9.0", "1.10.0"}, // not string order
	}
	for _, c := range less {
		a, _ := Parse(c[0])
		b, _ := Parse(c[1])
		if !a.Less(b) {
			t.Errorf("%s should order before %s", c[0], c[1])
		}
		if b.Less(a) {
			t.Errorf("%s should not order before %s", c[1], c[0])
		}
	}
	v, _ := Parse("1.2.3")
	if v.Less(v) {
		t.Error("a version orders before itself")
	}
}

func policy(min string) Policy {
	v, _ := Parse(min)
	return Policy{Min: v}
}

// The refusal has to be the exact shape the client already classifies, because
// the client half shipped first and cannot be changed retroactively.
func TestARefusalIsWhatTheClientAlreadyRecognises(t *testing.T) {
	err := policy("1.4.0").Check("1.3.0")
	if err == nil {
		t.Fatal("an old client was not refused")
	}
	st := status.Convert(err)
	// FailedPrecondition specifically: the client's classifier only looks for
	// the sentinel under that code.
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("= %v, want FailedPrecondition — the client only inspects the "+
			"message for a sentinel under that code", st.Code())
	}
	if !strings.Contains(st.Message(), Sentinel) {
		t.Errorf("the refusal does not carry the sentinel: %q", st.Message())
	}
	// And the numbers, for a person reading a log or a curl.
	if !strings.Contains(st.Message(), "1.3.0") || !strings.Contains(st.Message(), "1.4.0") {
		t.Errorf("the refusal does not say which versions are involved: %q", st.Message())
	}
}

// The sentinel is duplicated across the client/server boundary because the wasm
// client cannot be imported here. Duplication that nothing checks is
// duplication that drifts, and the drift would be silent: the server refuses,
// the client fails to classify, and retries forever.
func TestTheSentinelMatchesTheClientsCopy(t *testing.T) {
	src, err := os.ReadFile("../../client/data/conn.go")
	if err != nil {
		t.Fatalf("cannot read the client's copy: %v", err)
	}
	want := `const SkewSentinel = "` + Sentinel + `"`
	if !strings.Contains(string(src), want) {
		t.Errorf("client/data does not declare %s\n"+
			"The two constants have drifted, so the server refuses and the client "+
			"cannot tell it apart from an outage — which it then retries forever.", want)
	}
}

func TestCurrentAndNewerClientsPass(t *testing.T) {
	p := policy("1.4.0")
	for _, v := range []string{"1.4.0", "1.4.1", "1.5.0", "2.0.0", "v1.4.0-rc1"} {
		if err := p.Check(v); err != nil {
			t.Errorf("client %s was refused: %v", v, err)
		}
	}
}

// The build this ships must serve the client it ships. A minimum above the
// current version refuses every client including the one in the same binary.
func TestThisBuildServesItsOwnClient(t *testing.T) {
	min, ok := Parse(buildver.MinClient)
	if !ok {
		t.Fatalf("MinClient %q does not parse", buildver.MinClient)
	}
	cur, ok := Parse(buildver.Version)
	if !ok {
		t.Fatalf("Version %q does not parse", buildver.Version)
	}
	if cur.Less(min) {
		t.Errorf("this build is %s and refuses anything below %s — it refuses "+
			"the wasm bundle shipped in the same binary", cur, min)
	}
	if err := (Policy{Min: min}).Check(buildver.Version); err != nil {
		t.Errorf("this build's own client is refused: %v", err)
	}
}

// A zero minimum is off, which is what a development build wants.
func TestAZeroMinimumDisablesTheCheck(t *testing.T) {
	var p Policy
	for _, v := range []string{"", "0.0.1", "nonsense"} {
		if err := p.Check(v); err != nil {
			t.Errorf("a disabled policy refused %q: %v", v, err)
		}
	}
}

// The judgement call, asserted in both directions so changing it is deliberate.
func TestUnstampedClientsArePassedUnlessTheOperatorSaysOtherwise(t *testing.T) {
	lenient := policy("1.4.0")
	if err := lenient.Check(""); err != nil {
		t.Errorf("an unstamped caller was refused by default: %v — that locks "+
			"out curl, tests and the sync API, none of which send the header", err)
	}

	strict := lenient
	strict.RefuseUnstamped = true
	err := strict.Check("")
	if err == nil {
		t.Fatal("RefuseUnstamped had no effect")
	}
	if !strings.Contains(status.Convert(err).Message(), "unknown") {
		t.Errorf("the refusal does not say the version was unknown: %q",
			status.Convert(err).Message())
	}
}

// Refusing on a stamp we cannot read turns a formatting change into an outage
// for everybody at once.
func TestAnUnreadableStampIsNotTreatedAsOld(t *testing.T) {
	p := policy("1.4.0")
	for _, v := range []string{"nightly", "main@abc123", "1.x.0"} {
		if err := p.Check(v); err != nil {
			t.Errorf("the unparseable stamp %q was refused: %v", v, err)
		}
	}
}

// Refusing the call that explains the refusal is a closed loop.
func TestGetVersionIsNeverRefused(t *testing.T) {
	inter := Unary(policy("9.9.9"))
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(MetadataKey, "0.0.1"))

	ran := false
	_, err := inter(ctx, nil,
		&grpc.UnaryServerInfo{FullMethod: "/articleflux.v1.SystemService/GetVersion"},
		func(context.Context, any) (any, error) { ran = true; return "ok", nil })
	if err != nil || !ran {
		t.Errorf("GetVersion was refused for skew (%v); a stale client can no "+
			"longer find out what the server is", err)
	}

	// But everything else is.
	_, err = inter(ctx, nil,
		&grpc.UnaryServerInfo{FullMethod: "/articleflux.v1.ReaderService/ListItems"},
		func(context.Context, any) (any, error) {
			t.Error("the handler ran for a client below the minimum")
			return nil, nil
		})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("= %v, want FailedPrecondition", status.Code(err))
	}
}

// The header the client attaches has to be the one the server reads.
func TestTheStampIsReadFromTheHeaderTheClientSends(t *testing.T) {
	if MetadataKey != buildver.ClientStampHeader {
		t.Fatalf("the server reads %q and the client sends %q", MetadataKey, buildver.ClientStampHeader)
	}
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(buildver.ClientStampHeader, "3.2.1"))
	if got := StampFrom(ctx); got != "3.2.1" {
		t.Errorf("StampFrom = %q, want 3.2.1", got)
	}
	if got := StampFrom(context.Background()); got != "" {
		t.Errorf("a context with no metadata yielded %q", got)
	}
}

// Metadata can be present without the stamp key ever having been set — a call
// from a peer that attaches its own unrelated metadata. That must read as
// unstamped, not panic on an empty Get result.
func TestStampFromWithMetadataButNoStampKey(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("other-key", "v"))
	if got := StampFrom(ctx); got != "" {
		t.Errorf("StampFrom = %q, want empty when the stamp key is absent", got)
	}
}

// The refusal path is exercised above by GetVersion's exemption; this is the
// ordinary allow path — a current client on a non-exempt method must reach
// the handler, or every call in the system would need the exemption.
func TestUnaryAllowsAnUnrefusedNonExemptCall(t *testing.T) {
	inter := Unary(policy("1.4.0"))
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(MetadataKey, "1.4.0"))

	ran := false
	_, err := inter(ctx, nil,
		&grpc.UnaryServerInfo{FullMethod: "/articleflux.v1.ReaderService/ListItems"},
		func(context.Context, any) (any, error) { ran = true; return "ok", nil })
	if err != nil || !ran {
		t.Errorf("a current client on a non-exempt method was refused: %v", err)
	}
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f fakeStream) Context() context.Context { return f.ctx }

// Stream carries the same policy as Unary, but through ServerStream.Context
// instead of the RPC context directly — it needs its own coverage because a
// stale client left holding an open stream costs a goroutine, not one round trip.
func TestStreamAppliesTheSamePolicyAsUnary(t *testing.T) {
	inter := Stream(policy("9.9.9"))
	stale := fakeStream{ctx: metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(MetadataKey, "0.0.1"))}

	ran := false
	err := inter(nil, stale, &grpc.StreamServerInfo{FullMethod: "/articleflux.v1.SystemService/GetVersion"},
		func(any, grpc.ServerStream) error { ran = true; return nil })
	if err != nil || !ran {
		t.Errorf("GetVersion stream was refused for skew (%v)", err)
	}

	err = inter(nil, stale, &grpc.StreamServerInfo{FullMethod: "/articleflux.v1.ReaderService/WatchItems"},
		func(any, grpc.ServerStream) error {
			t.Error("the stream handler ran for a client below the minimum")
			return nil
		})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("= %v, want FailedPrecondition", status.Code(err))
	}

	current := fakeStream{ctx: metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(MetadataKey, "9.9.9"))}
	ran = false
	err = inter(nil, current, &grpc.StreamServerInfo{FullMethod: "/articleflux.v1.ReaderService/WatchItems"},
		func(any, grpc.ServerStream) error { ran = true; return nil })
	if err != nil || !ran {
		t.Errorf("a current client's stream was refused: %v", err)
	}
}
