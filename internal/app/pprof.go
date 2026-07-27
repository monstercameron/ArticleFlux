package app

import (
	"net/http"
	"net/http/pprof"
	"runtime"
)

// The runtime profiling surface.
//
// # Why this is not always on
//
// /metrics next door is unauthenticated on purpose, and the thing that makes
// that safe is written down there: every attribute is a bounded label, so there
// is no feed URL, article title or username to read. It is counts and durations.
//
// None of that is true here. A heap profile is a sample of what this process is
// holding, and what this process is holding is article text, session tokens,
// mailbox credentials and the decrypted model key. A heap profile prints
// allocation SITES rather than contents, so it is not a direct read of any of
// them — but `?gc=1` forces a collection on a running server, /debug/pprof/profile
// parks a CPU sampler on it for thirty seconds by default, and goroutine?debug=2
// prints every stack in the process. The first two are a denial of service
// anyone can trigger; the last is a map of the program.
//
// So it is off unless asked for, and cmd/articleflux refuses to turn it on
// anywhere it could be reached from outside — the same two-part rule -dev gets,
// for the same reason. A loopback bind is a fact about a socket and cannot tell
// you who is on the other end of a connection, so loopback ALONE is not the
// gate: an instance behind nginx binds loopback by design.
//
// # Why net/http/pprof rather than a hand-rolled endpoint
//
// Because `go tool pprof http://host/debug/pprof/profile` is the interface every
// Go programmer already has, and a bespoke dump format is a second thing to
// learn that answers fewer questions. The paths below are exactly the ones that
// tool expects.
//
// # Why the handlers are registered by hand
//
// net/http/pprof registers itself on http.DefaultServeMux in an init function.
// This server does not serve DefaultServeMux, so importing the package for its
// side effect would publish nothing — but it would also mean the surface is
// controlled by an import rather than by a flag, and an import is not something
// a reader of app.go can see. These are the same handlers, mounted explicitly,
// on a mux this function was handed, in a block that is only reached when
// cfg.Profiling is true.
func registerPprof(mux *http.ServeMux) {
	// The index links to every named runtime profile — heap, goroutine, block,
	// mutex, allocs, threadcreate — and serves them itself, so the prefix needs
	// registering as well as the four with dedicated handlers.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}

// Sampling rates for the two profilers the runtime keeps off by default.
//
// These are the conventional starting points rather than anything measured: one
// blocking event recorded per microsecond of delay, one contended mutex unlock
// sampled in a hundred. If a profile comes back empty at these, turn them up —
// the numbers are not load-bearing, and nothing reads them but the line below.
const (
	blockProfileRate     = 1_000_000 // nanoseconds of blocking per sample
	mutexProfileFraction = 100       // one sampled contention event in this many
)

// enableProfiling turns those two on.
//
// Both are free at zero and cost real time when on: the block profiler
// timestamps blocking events, the mutex profiler walks a stack on contended
// unlocks. That is exactly the tax worth paying while hunting a stall, and
// exactly the one a reader should not pay forever — which is the other half of
// why this whole surface is opt-in rather than merely unlinked.
func enableProfiling() {
	runtime.SetBlockProfileRate(blockProfileRate)
	runtime.SetMutexProfileFraction(mutexProfileFraction)
}
