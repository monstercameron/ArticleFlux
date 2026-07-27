// Package data is the client's connection to the server.
//
// It owns exactly one thing: a gRPC connection over the GoGRPCBridge tunnel, and
// the connection state the UI shows. Everything above it works in domain terms
// and never touches a stub.
//
// # Why a tunnel rather than plain gRPC
//
// Browsers cannot open the HTTP/2 connection that gRPC-over-HTTP normally
// requires. The tunnel carries gRPC frames over one WebSocket — which is also
// why the standard Service Worker offline recipe does not apply here. A Service
// Worker cannot see WebSocket frames, so offline packs travel over plain HTTPS
// instead.
//
// # Reading order, for anyone new to this package
//
// The files divide by question rather than by layer, and this is the order they
// answer in:
//
//	client.go     dialling, the stub, and the connection the rest of it wraps
//	conn.go       what a connection STATE means, and what an error does and does
//	              not say about the transport — deliberately untagged, so it is
//	              testable natively
//	cache.go      the read-through cache that makes an outage degrade the app
//	              instead of emptying it
//	coalesce.go   collapsing a burst of server events into one repaint
//	drain.go      draining the offline outbox once the connection returns
//	keys.go       the cache key vocabulary, so two callers cannot disagree about
//	              what "this feed's first page" is called
//
// # Why this file exists and holds nothing
//
// The package doc used to live in client.go, which is behind `//go:build js &&
// wasm`. A native build excludes that file, so `go doc ./client/data` printed
// the full description under one build and nothing at all under the other — and
// the empty one is the build a person runs when they want to read the code
// without setting up a wasm toolchain first. Documentation invisible from the
// cheaper of two builds is documentation pointed away from the newcomer.
//
// It lives here instead, unconstrained, so both builds agree. Nothing else
// belongs in this file; a declaration here would have to hold for the native
// build too, which is not what this package is for.
package data
