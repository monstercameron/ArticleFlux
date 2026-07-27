// The connection's own vocabulary: the states the indicator can show, what an
// error says (and does not say) about the transport, and the two small decisions
// — when to give up on retrying, and when a reconnect is worth a refetch.
//
// This file carries NO build tag on purpose, while the rest of the package is
// `js && wasm`. Everything here is arithmetic over a gRPC status code and a
// clock, it is where the connection's correctness actually lives, and pinning it
// to the browser would mean the only way to test any of it is to open one. The
// rule that follows: **anything in this package that can be decided without the
// DOM belongs here**, so it can be decided in a test instead.
//
// Spec: plan.md §20.19, decision A40.
package data

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ConnState is what the always-visible indicator shows.
//
// It exists because a reader that has silently stopped receiving looks exactly
// like a quiet news day, and those two must never be confusable.
//
// Five rather than three, because there are five remedies (§20.19.2). One red
// dot previously meant "your Wi-Fi is off", "the server is unplugged" and "your
// session expired" at once, and those need three different sentences from the
// person reading.
type ConnState string

const (
	// Connecting — a first connect or a redial is in flight. Suppressed for the
	// first second of a redial: a server restart reconnects in about half of
	// one, and painting this for it turns an invisible event into a flicker on
	// every deploy.
	Connecting ConnState = "connecting"
	// Live — calls are flowing.
	Live ConnState = "live"
	// Offline — the browser says there is no network. Distinct from Down
	// because the remedy is the reader's, not the server's, and because a
	// countdown promising a reconnect that cannot happen is worse than silence.
	Offline ConnState = "offline"
	// Down — there is a network and the server is not answering. The one state
	// that offers a countdown and a way to skip it.
	Down ConnState = "down"
	// Blocked — connected, and refused. An expired session or a client too old
	// for the server. **Retrying cannot fix any of these**, so the loop stops
	// and the reader is given the action instead.
	Blocked ConnState = "blocked"
)

// Class is what a failed RPC says about the CONNECTION, which is usually
// nothing at all.
//
// The bug this replaces was `if err != nil { down }` — one test standing in for
// §20.7's whole taxonomy, so a `NotFound` from a perfectly healthy server
// painted the indicator red, and a refusal that would never succeed was retried
// on the backoff schedule forever.
type Class int

const (
	// ClassOK — the call succeeded, which is the only positive proof of a
	// working transport there is.
	ClassOK Class = iota
	// ClassTransport — the connection is genuinely not working.
	ClassTransport
	// ClassApplication — the server answered, and the answer was no. The
	// connection is fine and must not be repainted.
	ClassApplication
	// ClassTerminal — the server answered, the answer was no, and it will keep
	// being no until a human does something.
	ClassTerminal
	// ClassNone — our own teardown. Says nothing about anything.
	ClassNone
)

// Reason names what a ClassTerminal verdict needs from the reader, because
// "blocked" without a remedy is just a redder version of "down".
type Reason string

const (
	ReasonNone Reason = ""
	// ReasonAuth — the credential is gone, expired or revoked. Remedy: sign in.
	// §7.1b's interceptor owns the navigation; this owns the description.
	ReasonAuth Reason = "auth"
	// ReasonSkew — this client is older than the server supports (§22.10).
	// Remedy: purge the Service Worker cache and hard-reload.
	ReasonSkew Reason = "skew"
)

// SkewSentinel is how a server refusal announces itself as version skew.
//
// §22.10 requires that a below-minimum client "purges the SW cache and
// hard-reloads rather than retrying forever", and a refusal the client cannot
// distinguish from an outage is retried forever by construction. A sentinel in
// the message is the cheapest thing that survives the tunnel: gRPC status
// details would need a proto message and a version of the proto old clients do
// not have — which is a poor way to tell an old client it is old.
//
// **The server half is owed** — nothing sends this yet. The client recognising
// it first is deliberate: the client that has to act on it is by definition the
// old one, so the recognition has to ship before the refusal does or the first
// skew refusal ever sent lands on clients that cannot understand it.
const SkewSentinel = "articleflux:client-too-old"

// Verdict is Classify's whole answer.
type Verdict struct {
	Class  Class
	Reason Reason
	// RetryAfter is the server's own instruction, honoured INSTEAD of the
	// backoff schedule. Backing off 500ms against a server that said "wait
	// thirty seconds" makes this client the rate limiter's problem rather than
	// its subject.
	RetryAfter time.Duration
}

// Classify decides what an RPC error means for the connection (§20.19.6).
//
// The mapping is §20.7's error taxonomy read from the other end. Note how few
// codes are about the transport at all: of the eight the server can return,
// exactly one is.
func Classify(err error) Verdict {
	if err == nil {
		return Verdict{Class: ClassOK}
	}
	// Context errors before status codes, because `status.FromError` does not
	// recognise them: a bare context.Canceled maps to Unknown, which falls
	// through to "the server answered" — and the server did not answer, we
	// hung up. Cancellation is the single most common non-nil error in this
	// client (a scope changing under an in-flight load, a component
	// unmounting, the page going away), so getting it wrong would repaint the
	// indicator on ordinary navigation.
	switch {
	case errors.Is(err, context.Canceled):
		return Verdict{Class: ClassNone}
	case errors.Is(err, context.DeadlineExceeded):
		return Verdict{Class: ClassTransport}
	}
	st, ok := status.FromError(err)
	if !ok {
		// No gRPC status at all: this never reached a server, so it is the
		// plumbing — a failed handshake, a dead socket, a dial that could not
		// resolve. Treating an unrecognised error as an application error
		// would be the old bug inverted: silence about a connection that is
		// genuinely gone.
		return Verdict{Class: ClassTransport}
	}
	switch st.Code() {
	case codes.OK:
		return Verdict{Class: ClassOK}

	case codes.Unavailable, codes.DeadlineExceeded:
		// The only two that mean what the old code assumed every error meant.
		// Unavailable is §20.7's "circuit open, degraded mode, storage
		// read-only" as well as a dead socket — both are "try again later",
		// which is what the indicator says.
		return Verdict{Class: ClassTransport}

	case codes.Unauthenticated:
		return Verdict{Class: ClassTerminal, Reason: ReasonAuth}

	case codes.FailedPrecondition:
		// Ordinarily application state — "source deactivated, session not idle"
		// — and only terminal when it is the one precondition a reader cannot
		// satisfy by doing something else: being on the wrong build.
		if strings.Contains(st.Message(), SkewSentinel) {
			return Verdict{Class: ClassTerminal, Reason: ReasonSkew}
		}
		return Verdict{Class: ClassApplication}

	case codes.ResourceExhausted:
		return Verdict{Class: ClassApplication, RetryAfter: retryAfterOf(st.Message())}

	case codes.Canceled:
		// Our own teardown: a component unmounting, a scope changing under an
		// in-flight load, the page going away. Reporting a connection failure
		// for a call WE stopped is how a tab close paints the indicator red on
		// its way out.
		return Verdict{Class: ClassNone}

	default:
		// PermissionDenied, NotFound, InvalidArgument, Aborted, Internal,
		// Unimplemented. The server answered — which is proof the connection
		// works, not evidence that it does not.
		return Verdict{Class: ClassApplication}
	}
}

// retryAfterOf pulls §20.7's `retry_after_s` out of a status message.
//
// Read from the message rather than from status details for the same reason as
// SkewSentinel: it survives a client whose generated proto predates the detail
// message. Absent or unparseable means "use the schedule", never zero-wait.
func retryAfterOf(msg string) time.Duration {
	const key = "retry_after_s="
	i := strings.Index(msg, key)
	if i < 0 {
		return 0
	}
	rest := msg[i+len(key):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	secs := 0
	for _, ch := range rest[:end] {
		secs = secs*10 + int(ch-'0')
		if secs > 3600 {
			// A server asking for more than an hour is a server with a bug, and
			// honouring it would park the app until the tab is reloaded.
			return time.Hour
		}
	}
	return time.Duration(secs) * time.Second
}

// How the tunnel comes back.
//
// This is a self-hosted reader: the server is a box in someone's house that gets
// restarted, updated, suspended with the laptop lid, and moved between networks,
// while the tab stays open for days. Giving up is never the right answer, so
// there is no attempt limit anywhere here — the connection retries for as long
// as the page is open, and the only thing that changes is how often.
//
// The shape: fast enough that a server restart is invisible (half a second, then
// a second, then two), capped low enough that coming back from a lid-close or a
// changed network is a few seconds' wait rather than a couple of minutes. gRPC's
// own default caps at 120s, which is correct for a datacentre client with
// thousands of peers and wrong for one browser tab and one server — a reader who
// opens the laptop should not have to reload the page because the backoff
// happens to be two minutes into a wait.
//
// Jitter stays on. There is usually one client, so a thundering herd is not the
// risk; a phone and a laptop both waking to the same Wi-Fi and hammering the
// same lock-step schedule is.
//
// **The cap is 20s and stays 20s.** The pressure to lower it comes from the
// worst case feeling like a hang — which the countdown and the Retry-now
// affordance (§20.19.2) remove, at no cost to a server that is still booting.
const (
	reconnectInitial  = 500 * time.Millisecond
	reconnectMax      = 20 * time.Second
	reconnectFactor   = 1.6
	reconnectJitter   = 0.2
	reconnectMinTries = 5 * time.Second
)

// backoffAt estimates the delay gRPC waits before its nth consecutive retry,
// n counting from 1.
//
// An ESTIMATE, and labelled one everywhere it surfaces: gRPC applies ±20%
// jitter internally and does not expose the number it picked. It is honest
// enough for a countdown that exists to tell someone whether to wait or to
// press the button, and the button is why being a second out costs nothing.
func backoffAt(n int) time.Duration {
	if n <= 1 {
		return reconnectInitial
	}
	d := float64(reconnectInitial)
	for i := 1; i < n; i++ {
		d *= reconnectFactor
		if d >= float64(reconnectMax) {
			return reconnectMax
		}
	}
	return time.Duration(d)
}

// Recovery pacing (§20.19.7).
//
// Every transition into a working connection refetches the rail, the tags, the
// folders and the list — four round trips. On a flapping tunnel that is a
// refetch storm at the exact moment the server is least able to serve one, and
// it is self-reinforcing: the storm is what keeps the newly recovered
// connection saturated.
const (
	// recoveryMinOutage — a blip shorter than this changed nothing worth four
	// round trips. Feeds are polled in minutes; two seconds of downtime cannot
	// have produced an article.
	recoveryMinOutage = 2 * time.Second
	// recoveryMinSpacing — the floor between refetches however often the
	// connection flaps.
	recoveryMinSpacing = 5 * time.Second
)

// RecoveryGate decides whether a reconnect has earned a refetch.
//
// Deliberately a value type with an injected clock rather than a goroutine with
// a ticker: this is the logic that is wrong in the interesting cases (a flap, a
// blip, a reconnect during a reconnect), and logic that owns its own time can
// only be tested by waiting.
type RecoveryGate struct {
	// MinOutage and MinSpacing default to the constants above when zero, so the
	// zero value is the shipping policy rather than "no policy".
	MinOutage  time.Duration
	MinSpacing time.Duration

	down      bool
	downAt    time.Time
	pending   bool
	lastFetch time.Time
}

func (g *RecoveryGate) minOutage() time.Duration {
	if g.MinOutage > 0 {
		return g.MinOutage
	}
	return recoveryMinOutage
}

func (g *RecoveryGate) minSpacing() time.Duration {
	if g.MinSpacing > 0 {
		return g.MinSpacing
	}
	return recoveryMinSpacing
}

// Lost records the connection failing. Repeated calls keep the FIRST moment:
// an outage that flaps through Connecting on its way nowhere is one outage, and
// re-stamping it on every transition would make every outage look two seconds
// long and suppress every refetch.
func (g *RecoveryGate) Lost(now time.Time) {
	if g.down {
		return
	}
	g.down = true
	g.downAt = now
}

// Recovered records the connection working again, and arms a refetch if the
// outage was long enough to have changed anything.
func (g *RecoveryGate) Recovered(now time.Time) {
	if !g.down {
		return
	}
	g.down = false
	if now.Sub(g.downAt) >= g.minOutage() {
		g.pending = true
	}
}

// Poll asks whether a refetch should run now.
//
// It returns (false, d>0) to mean "not yet, ask again in d" — the caller owns
// the timer, because the caller is the only one that can cancel it when the
// component unmounts. Call it after Recovered, and again after the delay it
// asks for; a caller that never comes back simply does not refetch, which is
// the safe direction to fail in.
func (g *RecoveryGate) Poll(now time.Time) (fetch bool, after time.Duration) {
	if !g.pending {
		return false, 0
	}
	if !g.lastFetch.IsZero() {
		if since := now.Sub(g.lastFetch); since < g.minSpacing() {
			return false, g.minSpacing() - since
		}
	}
	g.pending = false
	g.lastFetch = now
	return true, 0
}
