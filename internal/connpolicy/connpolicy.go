// Package connpolicy holds the four numbers that decide how long the tunnel can
// lie about being alive, and the one relationship between them that must hold.
//
// It exists because those numbers live at opposite ends of the system — the
// client's probe interval in `client/data`, the server's enforcement floor in
// `internal/app` — and **the failure mode when they disagree is not a missing
// feature, it is a working feature that flaps**. A client probing an idle tunnel
// faster than the server's `MinTime` is answered with
// `GOAWAY ENHANCE_YOUR_CALM (too_many_pings)`: it reconnects, probes, and is
// kicked again, roughly once a minute, forever. That reads like a network
// problem and is a configuration one, and nothing in either file would look
// wrong while reading it.
//
// So the two numbers are declared next to each other, with the invariant
// written down and a test that fails if anyone breaks it.
//
// Deliberately contains NO gRPC types. `client/data` imports this and every byte
// it pulls in is a byte in `app.wasm` (R4); a package of four durations costs
// nothing, and a package of ServerOption constructors would drag the gRPC server
// into a browser.
//
// Spec: plan.md §20.19.3, decision A40.
package connpolicy

import "time"

const (
	// ClientInterval is how often the client probes an idle tunnel.
	//
	// The probe is the ONLY thing that can notice a half-open socket — a CGNAT
	// reclaim, a VPN drop, a laptop's Wi-Fi vanishing without an RST. There is
	// no close event for those, so the browser's WebSocket stays open and gRPC
	// stays READY, and without this the indicator reports a live connection
	// over a socket that has been dead for an hour.
	ClientInterval = 30 * time.Second

	// ClientTimeout is how long an unanswered probe may go unanswered before
	// the transport is torn down and re-dialled.
	//
	// ClientInterval + ClientTimeout is the worst-case time-to-truth: 40s.
	// Shorter would be sharper and would also start declaring outages during
	// ordinary scheduling hiccups on a phone.
	ClientTimeout = 10 * time.Second

	// ServerMinTime is the fastest client ping the server tolerates on an idle
	// connection, paired with `PermitWithoutStream: true`.
	//
	// Both halves are load-bearing. gRPC's defaults are 5m and false, under
	// which the probing client above is kicked on its second ping — so a server
	// that simply forgets this option does not lose keepalive, it gains a flap.
	ServerMinTime = 20 * time.Second

	// GRPCClientFloor is gRPC's own minimum for a client keepalive interval.
	//
	// Below it, gRPC SILENTLY substitutes 10s — no error, no warning, and a
	// `ClientInterval` of 2s would simply not be the thing that happens. That
	// makes it a trap rather than a limit: the documented detection budget
	// would quietly become the real one plus eight seconds, and nothing at
	// runtime would say so. Discovered by a test that set 200ms and then waited
	// five seconds for a detection that could not arrive before ten.
	GRPCClientFloor = 10 * time.Second

	// Margin is how much slack the server leaves the client's schedule.
	//
	// It exists because ping arrival is asymmetric: a throttled, descheduled or
	// GC-delayed ping arrives LATE, never early. A floor tuned to exactly
	// ClientInterval could therefore only ever be violated by jitter in our own
	// favour — which is a policy that is correct on paper and fails in a
	// background tab.
	Margin = ClientInterval - ServerMinTime
)
