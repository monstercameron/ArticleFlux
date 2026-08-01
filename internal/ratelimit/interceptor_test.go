package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func okHandler(_ context.Context, _ any) (any, error) { return "served", nil }

func fixedKey(k string) KeyFunc { return func(context.Context) string { return k } }

// fakeStream is Context()-only, matching the pattern grpcsrv's own stream
// interceptor tests use: embed the interface for the unused methods, override
// the one the interceptors actually call.
type fakeStream struct{ grpc.ServerStream }

func (fakeStream) Context() context.Context { return context.Background() }

// callStream runs a stream interceptor once and reports whether the handler ran.
func callStream(t *testing.T, in grpc.StreamServerInterceptor) (served bool, err error) {
	t.Helper()
	ran := false
	err = in(nil, fakeStream{}, &grpc.StreamServerInfo{},
		func(any, grpc.ServerStream) error {
			ran = true
			return nil
		})
	return ran, err
}

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

// Stream shares Unary's logic almost exactly; the interesting case is that
// the same burst/refusal/fail-open behavior holds when the interceptor reads
// its key from ss.Context() rather than the ctx argument.
func TestStreamBurstAllowedThenRefused(t *testing.T) {
	rule := Rule{Name: "test", Per: time.Minute, Limit: 60, Burst: 2}
	in := Stream(New(Options{}), rule, fixedKey("s:abc"), nil)

	for i := 0; i < rule.Burst; i++ {
		served, err := callStream(t, in)
		if !served || err != nil {
			t.Fatalf("call %d of the burst was refused: %v", i+1, err)
		}
	}
	served, err := callStream(t, in)
	if served {
		t.Fatal("the handler ran past the burst; nothing is being limited")
	}
	if got := status.Code(err); got != codes.ResourceExhausted {
		t.Errorf("refusal code is %v, want ResourceExhausted", got)
	}
}

func TestStreamFailsOpenRatherThanRefusingEveryone(t *testing.T) {
	rule := Rule{Name: "test", Per: time.Minute, Limit: 1, Burst: 1}
	for name, in := range map[string]grpc.StreamServerInterceptor{
		"unidentifiable caller": Stream(New(Options{}), rule, fixedKey(""), nil),
		"no limiter":            Stream(nil, rule, fixedKey("s:abc"), nil),
		"no key function":       Stream(New(Options{}), rule, nil, nil),
	} {
		for i := 0; i < 5; i++ {
			served, err := callStream(t, in)
			if !served || err != nil {
				t.Errorf("%s: call %d was refused (%v); this path must let everything through",
					name, i+1, err)
				break
			}
		}
	}
}

// A nil *Concurrent must behave like an absent cap, the same fail-open
// contract Unary/Stream give a nil *Limiter — a status page calling Refused
// or Held before the cap is configured must not panic.
func TestConcurrentNilIsInert(t *testing.T) {
	var c *Concurrent
	if got := c.Refused(); got != 0 {
		t.Errorf("nil Concurrent.Refused() = %d, want 0", got)
	}
	if got := c.Held("k"); got != 0 {
		t.Errorf("nil Concurrent.Held() = %d, want 0", got)
	}
	in := c.Interceptor("test", nil, fixedKey("s:abc"))
	served, err := callStream(t, in)
	if !served || err != nil {
		t.Errorf("nil Concurrent refused a stream: %v", err)
	}
}

// A limit of zero or less disables the cap, per NewConcurrent's doc comment.
func TestConcurrentZeroLimitDisablesTheCap(t *testing.T) {
	c := NewConcurrent(0)
	in := c.Interceptor("test", nil, fixedKey("s:abc"))
	for i := 0; i < 10; i++ {
		served, err := callStream(t, in)
		if !served || err != nil {
			t.Fatalf("call %d refused with limit 0: %v", i, err)
		}
	}
}

// The cap admits up to the limit concurrently, refuses the one past it, and
// releases on the handler returning so the next stream can be admitted.
func TestConcurrentCapsAndReleases(t *testing.T) {
	c := NewConcurrent(2)
	release := make(chan struct{})
	blocking := c.Interceptor("test", nil, fixedKey("s:abc"))

	var wg sync.WaitGroup
	admitted := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = blocking(nil, fakeStream{}, &grpc.StreamServerInfo{},
				func(any, grpc.ServerStream) error {
					admitted <- struct{}{}
					<-release
					return nil
				})
		}()
	}
	<-admitted
	<-admitted

	if got := c.Held("s:abc"); got != 2 {
		t.Errorf("Held() = %d while two streams are open, want 2", got)
	}

	served, err := callStream(t, blocking)
	if served {
		t.Fatal("a third stream was admitted past a cap of 2")
	}
	if got := status.Code(err); got != codes.ResourceExhausted {
		t.Errorf("refusal code is %v, want ResourceExhausted", got)
	}
	if got := c.Refused(); got != 1 {
		t.Errorf("Refused() = %d, want 1", got)
	}

	close(release)
	wg.Wait()

	if got := c.Held("s:abc"); got != 0 {
		t.Errorf("Held() = %d after both streams closed, want 0 (evicted, not left at zero)", got)
	}
	// The slot is free again.
	served, err = callStream(t, blocking)
	if !served || err != nil {
		t.Errorf("a stream was refused after the cap freed up: %v", err)
	}
}

// An unidentifiable caller or a missing key function must fail open, the same
// as Unary and Stream.
func TestConcurrentFailsOpenWithoutAKey(t *testing.T) {
	c := NewConcurrent(1)
	for name, in := range map[string]grpc.StreamServerInterceptor{
		"unidentifiable caller": c.Interceptor("test", nil, fixedKey("")),
		"no key function":       c.Interceptor("test", nil, nil),
	} {
		for i := 0; i < 3; i++ {
			served, err := callStream(t, in)
			if !served || err != nil {
				t.Errorf("%s: call %d was refused (%v); this path must let everything through",
					name, i+1, err)
				break
			}
		}
	}
}
