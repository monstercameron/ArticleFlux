// Package demodata is the server the demo build talks to, running inside the
// same wasm module as the client.
//
// The demo exists because ArticleFlux is a self-hosted reader: seeing it means
// installing Go, cloning two repositories, building a wasm bundle and running a
// server. That is a fair amount to ask of somebody deciding whether they want to
// look at it at all. A GitHub Pages build removes every step — it is a static
// page, and the "server" is this package.
//
// # Why it dispatches gRPC rather than faking the client
//
// The obvious shortcut is a fake data.Client with the same thirty methods. It is
// the wrong seam, twice over: the fake would diverge from the real client the
// first time a method changed, and — worse — the demo would no longer exercise
// the client. Everything between the view and the wire (the auth interceptor,
// the error taxonomy in Classify, the read cache, the outbox) would be skipped,
// so a demo that worked would be evidence about a code path nobody ships.
//
// So the seam is exactly one layer lower: grpc.ClientConnInterface, which is all
// the generated stubs need. This package answers Invoke by routing to a normal
// gRPC *server* implementation — the same generated ReaderServiceServer
// interface internal/transport/grpcsrv implements — through the same generated
// ServiceDesc handlers. The consequences are worth stating:
//
//   - The compiler enforces the API. A new RPC in the proto is a missing method
//     here, not a demo that silently returns nothing.
//   - Requests and responses are real protobuf messages, marshalled through the
//     same code paths, so the read cache's proto.Marshal round trip is real.
//   - Errors are real gRPC status codes, so Classify sees what it would see
//     from a server.
//
// # No build tag
//
// Nothing here touches the DOM, so nothing here needs a browser to be checked.
// That is the same rule client/data/conn.go states for itself, and it buys the
// same thing: the demo's behaviour is assertable in an ordinary `go test`.
package demodata

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Conn is a grpc.ClientConnInterface that answers out of memory.
//
// It is deliberately the narrow interface rather than something shaped like
// *grpc.ClientConn: the generated stubs take exactly this, and anything wider
// would be inventing a connection lifecycle the demo does not have.
type Conn struct {
	// The instance every service reads and writes. Held so callers can reach it
	// for the few things the RPC surface does not express — see Instance.
	inst *Instance

	routes map[string]route
}

// route is one method's handler and the service implementation it belongs to.
//
// grpc.MethodDesc.Handler has an unexported function type, so the descriptor is
// stored whole rather than the func pulled out of it: the field can be CALLED
// from here, but a value of that type cannot be assigned to a type declared
// here.
type route struct {
	impl any
	desc grpc.MethodDesc
}

// New builds a demo backend with the sample instance already loaded.
//
// now is the clock. It is a parameter rather than time.Now because every
// timestamp in the fixtures is relative — "forty minutes ago", not a date in
// 2026 — and a demo whose newest article is eight months old reads as abandoned
// rather than as sample data. Tests pass a fixed clock; the browser passes
// time.Now.
func New(now func() time.Time) *Conn {
	inst := seed(now)
	c := &Conn{inst: inst, routes: map[string]route{}}
	c.register(&pb.ReaderService_ServiceDesc, &readerService{inst: inst})
	c.register(&pb.AuthService_ServiceDesc, &authService{inst: inst})
	c.register(&pb.SystemService_ServiceDesc, &systemService{inst: inst})
	c.register(&pb.SmartService_ServiceDesc, &smartService{inst: inst})
	return c
}

// Instance is the data behind the connection, for the few callers that need it
// directly rather than over an RPC.
func (c *Conn) Instance() *Instance { return c.inst }

func (c *Conn) register(sd *grpc.ServiceDesc, impl any) {
	for _, m := range sd.Methods {
		c.routes["/"+sd.ServiceName+"/"+m.MethodName] = route{impl: impl, desc: m}
	}
}

// Invoke answers one unary call.
//
// The decode step copies the caller's request rather than handing the same
// pointer through, because a real transport does: a handler that retained the
// request message would, over a socket, be retaining a message nobody else can
// see, and here it would be retaining the caller's. Bugs that only appear over
// the wire are the ones a demo is least able to find, so the copy stays.
func (c *Conn) Invoke(ctx context.Context, method string, args, reply any, _ ...grpc.CallOption) error {
	r, ok := c.routes[method]
	if !ok {
		// The same code a server with an older proto would answer with, so the
		// client's own taxonomy (Classify → ClassApplication) treats it the way
		// it would treat the real thing.
		return status.Errorf(codes.Unimplemented, "%s is not served by the demo", method)
	}
	in, ok := args.(proto.Message)
	if !ok {
		return status.Errorf(codes.Internal, "%s: request is not a proto message", method)
	}
	// Counted here rather than in each handler, for the same reason the real
	// server counts in an interceptor: a method that forgets to count itself is
	// invisible on the screen that exists to show what the server is doing.
	c.inst.count(method)
	out, err := r.desc.Handler(r.impl, ctx, func(dst any) error {
		msg, ok := dst.(proto.Message)
		if !ok {
			return status.Errorf(codes.Internal, "%s: request is not a proto message", method)
		}
		proto.Reset(msg)
		proto.Merge(msg, in)
		return nil
	}, nil)
	if err != nil {
		return err
	}
	res, ok := reply.(proto.Message)
	if !ok {
		return status.Errorf(codes.Internal, "%s: reply is not a proto message", method)
	}
	src, ok := out.(proto.Message)
	if !ok {
		return status.Errorf(codes.Internal, "%s: handler returned no message", method)
	}
	proto.Reset(res)
	proto.Merge(res, src)
	return nil
}

// NewStream is refused rather than implemented.
//
// Every RPC in this API is unary (§20.7), so a streaming call here is not a
// demo limitation to apologise for — it is a call that does not exist, and
// saying so plainly is better than a stream that never yields.
func (c *Conn) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, status.Error(codes.Unimplemented, "the demo serves unary calls only")
}
