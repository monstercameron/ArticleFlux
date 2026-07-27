package app

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/ratelimit"
	"github.com/monstercameron/ArticleFlux/internal/reqid"
	"github.com/monstercameron/ArticleFlux/internal/skew"
	"github.com/monstercameron/ArticleFlux/internal/transport/grpcsrv"
)

// The streaming chain (TODO P1, §20.7, §22.10, §22.11).
//
// # What was wrong
//
// The unary chain mints a request id, refuses a stale client, sheds a flood and
// opens a span. The stream chain carried authorization and nothing else — so a
// streaming call was unmetered, untimed, unversioned and unlimited. That was
// theoretical while nothing streamed; `WatchEvents` made it live, and by its own
// description it "holds a goroutine and a subscription for as long as a tab is
// open".
//
// # Why the tunnel's cap is not this cap
//
// GoGRPCBridge bounds WebSockets per client (8). Every stream multiplexes over
// one WebSocket, so that bound permits eight tunnels times however many streams
// a client cares to open. The thing worth limiting is the stream.
//
// # Two limits, because a stream fails in two ways
//
// The ordinary per-caller rule admits the OPENING, which stops open-close churn.
// A concurrency cap bounds what is HELD, which stops slow accumulation. Either
// alone leaves the other door open.

// maxStreamsPerCaller is how many streams one credential may hold at once.
//
// Four. The application opens exactly one — the event pump — and the number is
// four rather than one so that a reload racing its predecessor's teardown, a
// second tab, and a future second stream all fit without anybody meeting a limit
// during normal use. A credential holding five is not a reader.
const maxStreamsPerCaller = 4

// streamChain builds the interceptor chain in the order the unary chain
// establishes, for the same reasons recorded there.
func (a *App) streamChain() grpc.ServerOption {
	return grpc.ChainStreamInterceptor(
		// One id per stream, minted here so a stream's log lines group like every
		// other call's. Without it a stream is the one thing in the system whose
		// errors cannot be traced back to the request that started it.
		func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo,
			handler grpc.StreamHandler) error {
			return handler(srv, &ctxStream{ServerStream: ss, ctx: reqid.With(ss.Context(), "")})
		},
		// Version skew before anything does work, exactly as in the unary chain.
		skew.Stream(a.skewPolicy()),
		// Then the limits, BEFORE authorization — a flood from an unauthorized
		// caller is still a flood, and a limiter that only limits callers who got
		// past authorization protects the wrong side of the door.
		a.rateLimitStream(),
		a.streamCap.Interceptor("stream.concurrent", a.log, a.rateKey),
		grpcsrv.AuthzStream(a.policy, a.scopeFromContext, a.log, a.recordDenial),
		a.streamTelemetry(),
	)
}

// ctxStream carries a replacement context down a stream.
//
// grpc's ServerStream hands its context out and takes no way to change it, so an
// interceptor that wants to add anything — a request id, a deadline, a value —
// has to wrap. This is the whole of that wrapper: everything else is delegated.
type ctxStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *ctxStream) Context() context.Context { return s.ctx }

// rateLimitStream admits stream OPENINGS against the ordinary per-caller rule.
func (a *App) rateLimitStream() grpc.StreamServerInterceptor {
	if a.cfg.DevMode {
		return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo,
			handler grpc.StreamHandler) error {
			return handler(srv, ss)
		}
	}
	return ratelimit.Stream(
		ratelimit.New(ratelimit.Options{}),
		ratelimit.DefaultPerUser,
		a.rateKey,
		a.cfg.Log,
	)
}

// streamTelemetry spans and counts a stream the way the unary chain does its
// calls — with one deliberate difference in what the duration MEANS.
//
// For a unary call the duration is how long the work took. For a stream it is
// how long the stream was HELD, which is a different quantity that happens to
// share a unit: an hour-long event stream is not a slow call. It is recorded
// under its own instrument name so nothing averages the two together and
// concludes the server got slower the day live updates shipped.
func (a *App) streamTelemetry() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo,
		handler grpc.StreamHandler) error {

		name := info.FullMethod
		ctx, span := a.tel.Tracer.Start(ss.Context(), "stream."+name,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("rpc.method", name)))
		defer span.End()

		started := time.Now()
		err := handler(srv, &ctxStream{ServerStream: ss, ctx: ctx})
		elapsed := time.Since(started)

		code := status.Code(err)
		attrs := metric.WithAttributes(
			attribute.String("rpc.method", name),
			attribute.String("rpc.code", code.String()),
		)
		a.tel.Instruments.RPCRequests.Add(ctx, 1, attrs)
		a.tel.Instruments.StreamDuration.Record(ctx, elapsed.Seconds(), attrs)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(otelcodes.Error, code.String())
		}
		return err
	}
}
