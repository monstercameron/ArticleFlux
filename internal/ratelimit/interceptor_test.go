package ratelimit

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func okHandler(_ context.Context, _ any) (any, error) { return "served", nil }

func fixedKey(k string) KeyFunc { return func(context.Context) string { return k } }

// call runs the interceptor once and reports whether the handler ran.
func call(t *testing.T, in grpc.UnaryServerInterceptor) (served bool, err error) {
	t.Helper()
	ran := false
	_, err = in(context.Background(), nil, &grpc.UnaryServerInfo{},
		func(ctx context.Context, req any) (any, error) {
			ran = true
			return okHandler(ctx, req)
		})
	return ran, err
}

// A burst is allowed and the one after it is not — the whole point of the
// interceptor, asserted on the rule's own numbers rather than on a literal.
func TestTheBurstIsAllowedAndTheNextCallIsRefused(t *testing.T) {
	rule := Rule{Name: "test", Per: time.Minute, Limit: 60, Burst: 3}
	in := Unary(New(Options{}), rule, fixedKey("s:abc"), nil)

	for i := 0; i < rule.Burst; i++ {
		served, err := call(t, in)
		if !served || err != nil {
			t.Fatalf("call %d of the burst was refused: %v", i+1, err)
		}
	}
	served, err := call(t, in)
	if served {
		t.Fatal("the handler ran past the burst; nothing is being limited")
	}
	if got := status.Code(err); got != codes.ResourceExhausted {
		t.Errorf("refusal code is %v, want ResourceExhausted", got)
	}
}

// §20.7 maps exhaustion to a refusal carrying retry_after. A client left to
// guess backs off on a schedule unrelated to when it would be served.
func TestARefusalSaysHowLongToWait(t *testing.T) {
	rule := Rule{Name: "test", Per: time.Minute, Limit: 60, Burst: 1}
	in := Unary(New(Options{}), rule, fixedKey("s:abc"), nil)

	if _, err := call(t, in); err != nil {
		t.Fatalf("the first call was refused: %v", err)
	}
	_, err := call(t, in)
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("the refusal is not a gRPC status: %v", err)
	}
	if len(st.Details()) == 0 {
		t.Error("the refusal carries no details, so no retry_after reaches the client")
	}
}

// Two callers must not share a bucket. Keyed wrongly, one busy client would
// lock out everybody else on the instance.
func TestInterceptorKeysAreIndependent(t *testing.T) {
	rule := Rule{Name: "test", Per: time.Minute, Limit: 60, Burst: 1}
	l := New(Options{})
	first := Unary(l, rule, fixedKey("s:one"), nil)
	second := Unary(l, rule, fixedKey("s:two"), nil)

	if _, err := call(t, first); err != nil {
		t.Fatalf("first caller refused: %v", err)
	}
	if _, err := call(t, first); err == nil {
		t.Fatal("the first caller was not limited")
	}
	if served, err := call(t, second); !served || err != nil {
		t.Errorf("a second caller was refused because of the first: %v", err)
	}
}

// Failing open is deliberate in three places, and each one would otherwise be a
// way to take the whole surface down: a caller that cannot be identified, a
// limiter that was never built, and a key function that was not supplied.
func TestItFailsOpenRatherThanRefusingEveryone(t *testing.T) {
	rule := Rule{Name: "test", Per: time.Minute, Limit: 1, Burst: 1}
	for name, in := range map[string]grpc.UnaryServerInterceptor{
		"unidentifiable caller": Unary(New(Options{}), rule, fixedKey(""), nil),
		"no limiter":            Unary(nil, rule, fixedKey("s:abc"), nil),
		"no key function":       Unary(New(Options{}), rule, nil, nil),
	} {
		for i := 0; i < 5; i++ {
			served, err := call(t, in)
			if !served || err != nil {
				t.Errorf("%s: call %d was refused (%v); this path must let everything through",
					name, i+1, err)
				break
			}
		}
	}
}
