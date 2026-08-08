package app

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/apierr"
	"github.com/monstercameron/ArticleFlux/internal/reqid"
)

// The panic boundary for the RPC surface.
//
// # Why this is not optional
//
// grpc-go does NOT recover panics in handlers. A handler that dereferences a
// nil unwinds past the top of the goroutine grpc-go started for that call, and
// a panic that reaches the top of any goroutine takes the PROCESS down — not
// the call, not the connection, the whole server. Every other reader loses
// their session, the poller stops mid-cycle, and `internal/obs`'s log ring, the
// one place the evidence lived, dies with it.
//
// The rest of the program already knows this. `internal/jobs` recovers per job
// and `reader.pollOneRecovered` recovers per source, both with a comment saying
// why. Those cover work this server does on a timer. Nothing covered the work
// it does when a reader clicks something, which is the larger surface and the
// one that runs untested code paths first.
//
// # What a recovered panic is worth as a log line
//
// A stack, the method, and the request id. The stack because a panic's location
// is the entire diagnosis and nothing else in the system records it; the method
// because it is the one bounded label that says which handler; the id because
// the reader saw an "internal error" and the two have to be joinable — that is
// what §22.11 asks for and a panic is the case it was written for.
//
// # What the CALLER gets
//
// `internal error`, with a reference and nothing else. A panic value is a
// programming detail and frequently contains an address or a fragment of the
// data that caused it, so it is subject to the same §20.7 rule as any other
// cause: it goes to the log, never to the client.

// recoverUnary catches a panic from a handler or from any interceptor after it.
//
// Its position in the chain is the design: AFTER the request-id interceptor, so
// a recovered panic has an id to report, and BEFORE everything else, so a panic
// in the version check, the limiter, authorization or idempotency is caught too.
// Those run before any handler and are the code most likely to meet a malformed
// request first.
func (a *App) recoverUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (res any, err error) {
		defer func() {
			if v := recover(); v != nil {
				err = a.recordPanic(ctx, shortMethod(info.FullMethod), v, debug.Stack())
				res = nil
			}
		}()
		return handler(ctx, req)
	}
}

// recoverStream is the same boundary for streams, which need it MORE.
//
// A stream handler runs for as long as a tab is open and does its work in
// response to events rather than to one request, so it executes far more
// distinct code than a unary call does, over a much longer window, and a panic
// in it is correspondingly likelier. It was also entirely uncovered: the stream
// chain reached authorization and telemetry and stopped.
func (a *App) recoverStream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo,
		handler grpc.StreamHandler) (err error) {
		defer func() {
			if v := recover(); v != nil {
				err = a.recordPanic(ss.Context(), shortMethod(info.FullMethod), v, debug.Stack())
			}
		}()
		return handler(srv, ss)
	}
}

// logRPCError writes the line that makes a failed call explicable.
//
// # Why this did not exist, and what it cost
//
// Nothing logged a failed RPC. The interceptors counted one, timed it, and
// marked the span; the handler had already converted its error to a gRPC
// status, and `apierr.Status` dropped the cause on the way. So an internal
// error produced a client toast reading "internal error", a counter tick with
// no detail, and NO LOG LINE AT ALL — while three comments in the codebase
// described the original going to the log with a request id.
//
// Returning the request id to the reader (§22.11) made that worse rather than
// better: a reader who can now quote a reference is a reader whose reference
// leads to an empty log. The two halves only work together.
//
// # Level by who is at fault
//
// A 404 for a deleted item, a validation failure, a rate limit and a permission
// refusal are the server WORKING. Logging those at Error would fill the ring —
// five hundred records, shared with everything else — with normal behaviour,
// and the genuine faults would age out behind them. They are logged at Debug so
// they are there when somebody raises the level to chase a specific complaint.
//
// Internal, Unknown, Unavailable and DataLoss are the server failing. Those get
// Error, and they are what the settings screen shows by default.
func (a *App) logRPCError(ctx context.Context, method string, err error) {
	if err == nil {
		return
	}
	code := status.Code(err)

	level := slog.LevelDebug
	switch code {
	case codes.Internal, codes.Unknown, codes.Unavailable, codes.DataLoss:
		level = slog.LevelError
	}

	args := []any{"rpc.method", method, "rpc.code", code.String()}
	// The cause, which is the entire reason this line is worth writing. Absent
	// for a refusal whose message already IS the reason.
	if detail := apierr.LogDetail(err); detail != "" {
		args = append(args, "err", detail)
	}
	a.log.Log(ctx, level, "rpc failed", args...)

	// Counted as well as logged, but only for the faults: an error counter that
	// moves every time somebody opens a deleted article is one nobody can alert
	// on.
	if level == slog.LevelError {
		a.tel.Instruments.Errors.Add(ctx, 1, metric.WithAttributes(
			attribute.String("subsystem", "rpc"),
			attribute.String("class", code.String()),
		))
	}
}

// recordPanic logs, counts, marks the span, and returns the safe error.
//
// One function for both chains so the two cannot come to report a panic
// differently — which is exactly what happened to the RPC metrics they also
// both write.
func (a *App) recordPanic(ctx context.Context, method string, v any, stack []byte) error {
	cause := fmt.Errorf("panic: %v", v)

	// RecordError rather than a bare log call: it bumps the error counter, marks
	// the active span, and writes the line, and a panic is the one event that
	// must appear in all three. A dashboard that cannot see panics reports a
	// healthy server right up until somebody mentions the reader keeps logging
	// them out.
	a.tel.RecordError(ctx, a.log, "rpc", "panic", cause,
		"rpc.method", method,
		"stack", string(stack))

	return apierr.WithRequestID(apierr.Status(apierr.Internal(cause)), reqid.From(ctx))
}
