package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/apierr"
	"github.com/monstercameron/ArticleFlux/internal/reqid"
)

// openForPanicTest builds a real App, because the point of these tests is the
// wiring: a fake logger and a nil telemetry would prove the interceptor calls
// something, not that what it calls exists and records.
func openForPanicTest(t *testing.T) *App {
	t.Helper()
	a, err := Open(t.Context(), Config{
		DBPath:       filepath.Join(t.TempDir(), "test.db"),
		PollInterval: 0,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// TestAPanickingHandlerDoesNotTakeTheProcessDown is the property the boundary
// exists for.
//
// Without the interceptor this test does not fail — the test BINARY dies, which
// is the same thing the server does in production and is worth stating plainly:
// grpc-go does not recover handler panics, so the failure mode being prevented
// here is total, not partial.
func TestAPanickingHandlerDoesNotTakeTheProcessDown(t *testing.T) {
	a := openForPanicTest(t)
	ctx := reqid.With(context.Background(), "cafebabe")

	res, err := a.recoverUnary()(ctx, struct{}{},
		&grpc.UnaryServerInfo{FullMethod: "/articleflux.v1.ReaderService/ListItems"},
		func(context.Context, any) (any, error) {
			var boom *struct{ n int }
			return boom.n, nil // nil dereference, the realistic shape
		})

	if err == nil {
		t.Fatal("a panicking handler returned no error")
	}
	if res != nil {
		t.Errorf("res = %v, want nil — a panicked call has no result to return", res)
	}
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("code = %v, want Internal", got)
	}
}

// The caller gets §20.7's safe message and a reference, and nothing else. A
// panic value routinely contains an address or a fragment of the data that
// caused it, so it is subject to the same rule as any other cause.
func TestARecoveredPanicTellsTheClientNothingUnsafe(t *testing.T) {
	a := openForPanicTest(t)
	ctx := reqid.With(context.Background(), "cafebabe")

	_, err := a.recoverUnary()(ctx, struct{}{},
		&grpc.UnaryServerInfo{FullMethod: "/articleflux.v1.ReaderService/ListItems"},
		func(context.Context, any) (any, error) {
			panic("secret-token-in-the-panic-value")
		})

	st, _ := status.FromError(err)
	if st.Message() != "internal error" {
		t.Errorf("message = %q, want the safe fallback", st.Message())
	}
	if strings.Contains(st.Message(), "secret-token") {
		t.Error("the panic value reached the client")
	}
	if got := apierr.RequestIDOf(err); got != "cafebabe" {
		t.Errorf("request id on the error = %q, want %q — a reader shown "+
			"\"internal error\" has nothing to quote without it", got, "cafebabe")
	}
}

// The directive this file was written under: an error boundary that swallows a
// panic without logging it is worse than no boundary, because the server now
// stays up and says nothing. The stack is the whole diagnosis and nothing else
// in the system records it.
func TestARecoveredPanicIsLoggedWithItsStack(t *testing.T) {
	a := openForPanicTest(t)
	ctx := reqid.With(context.Background(), "cafebabe")

	_, _ = a.recoverUnary()(ctx, struct{}{},
		&grpc.UnaryServerInfo{FullMethod: "/articleflux.v1.ReaderService/ListItems"},
		func(context.Context, any) (any, error) {
			panic("boom")
		})

	// Read back through the ring, which is what the settings screen shows. A
	// line that only reached stderr is one nobody running this can retrieve.
	var found bool
	for _, rec := range a.ring.Recent(50, slog.LevelError) {
		if !strings.Contains(rec.Attrs, "boom") {
			continue
		}
		found = true
		for _, want := range []string{
			"cafebabe",   // the id, so the reader's report joins this line
			"ListItems",  // which handler
			"recover.go", // a frame from the stack
		} {
			if !strings.Contains(rec.Attrs, want) {
				t.Errorf("the panic log line is missing %q\ngot: %s", want, rec.Attrs)
			}
		}
	}
	if !found {
		t.Error("no log line recorded the panic — the boundary swallowed it silently")
	}
}

// Streams need the boundary more than unary calls do: they run for as long as a
// tab is open, so they execute more code over a longer window.
func TestAPanickingStreamHandlerIsRecoveredAndLogged(t *testing.T) {
	a := openForPanicTest(t)
	ctx := reqid.With(context.Background(), "feedface")

	err := a.recoverStream()(struct{}{}, &ctxStream{ctx: ctx},
		&grpc.StreamServerInfo{FullMethod: "/articleflux.v1.ReaderService/WatchEvents"},
		func(any, grpc.ServerStream) error {
			panic("stream boom")
		})

	if err == nil {
		t.Fatal("a panicking stream handler returned no error")
	}
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("code = %v, want Internal", got)
	}
	if got := apierr.RequestIDOf(err); got != "feedface" {
		t.Errorf("request id = %q, want %q", got, "feedface")
	}

	var found bool
	for _, rec := range a.ring.Recent(50, slog.LevelError) {
		if strings.Contains(rec.Attrs, "stream boom") && strings.Contains(rec.Attrs, "WatchEvents") {
			found = true
		}
	}
	if !found {
		t.Error("the stream panic was not logged")
	}
}

// A handler that returns normally must be left entirely alone — a boundary that
// alters the ordinary path is a boundary that has to be reasoned about on every
// call, rather than only when something has already gone wrong.
func TestTheBoundaryIsInvisibleWhenNothingPanics(t *testing.T) {
	a := openForPanicTest(t)

	want := "the result"
	res, err := a.recoverUnary()(context.Background(), struct{}{},
		&grpc.UnaryServerInfo{FullMethod: "/articleflux.v1.ReaderService/ListItems"},
		func(context.Context, any) (any, error) { return want, nil })

	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if res != want {
		t.Errorf("res = %v, want %q", res, want)
	}
}
