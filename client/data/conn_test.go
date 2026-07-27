// Tests for the connection's decisions, all of which run NATIVELY — no browser,
// no server, no waiting. That is the point of conn.go carrying no build tag:
// the two failures the §20.19 audit turned up were both provable without a
// browser, and neither had a test that could have caught them.
package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestClassify walks §20.7's error taxonomy from the client's end.
//
// The bug this pins: `if err != nil { down }`. Every row where the want is
// ClassApplication is a case that used to paint the indicator red over a
// perfectly working connection, and the two Terminal rows are cases that used to
// be retried on the backoff schedule forever.
func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   Class
		reason Reason
	}{
		{"nil is the only proof of life", nil, ClassOK, ReasonNone},
		{"explicit OK", status.Error(codes.OK, ""), ClassOK, ReasonNone},

		// The only two codes that are about the transport at all.
		{"unavailable", status.Error(codes.Unavailable, "connection refused"), ClassTransport, ReasonNone},
		{"deadline exceeded", status.Error(codes.DeadlineExceeded, "ctx"), ClassTransport, ReasonNone},
		{"raw non-status error is a dead transport",
			errors.New("websocket: bad handshake"), ClassTransport, ReasonNone},

		// Refusals that retrying cannot lift.
		{"unauthenticated", status.Error(codes.Unauthenticated, "session expired"),
			ClassTerminal, ReasonAuth},
		{"version skew", status.Error(codes.FailedPrecondition,
			"refusing: "+SkewSentinel+" (min 1.4.0)"), ClassTerminal, ReasonSkew},

		// The server ANSWERED. Every one of these is evidence the connection
		// works, and none of them may touch the indicator.
		{"not found", status.Error(codes.NotFound, "no such item"), ClassApplication, ReasonNone},
		{"permission denied", status.Error(codes.PermissionDenied, "nope"), ClassApplication, ReasonNone},
		{"invalid argument", status.Error(codes.InvalidArgument, "bad cursor"), ClassApplication, ReasonNone},
		{"aborted", status.Error(codes.Aborted, "rev mismatch"), ClassApplication, ReasonNone},
		{"resource exhausted", status.Error(codes.ResourceExhausted, "slow down"), ClassApplication, ReasonNone},
		{"internal", status.Error(codes.Internal, "boom"), ClassApplication, ReasonNone},
		{"plain failed precondition is not skew",
			status.Error(codes.FailedPrecondition, "source deactivated"), ClassApplication, ReasonNone},

		// Our own teardown.
		{"canceled", status.Error(codes.Canceled, "ctx"), ClassNone, ReasonNone},
		{"context.Canceled", context.Canceled, ClassNone, ReasonNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.err)
			if got.Class != tc.want {
				t.Errorf("Classify(%v).Class = %v, want %v", tc.err, got.Class, tc.want)
			}
			if got.Reason != tc.reason {
				t.Errorf("Classify(%v).Reason = %q, want %q", tc.err, got.Reason, tc.reason)
			}
		})
	}
}

// TestClassifyRetryAfter — a server that says how long to wait is obeyed
// instead of the schedule. Backing off 500ms against "wait thirty seconds"
// makes this client the rate limiter's problem rather than its subject.
func TestClassifyRetryAfter(t *testing.T) {
	tests := []struct {
		msg  string
		want time.Duration
	}{
		{"quota exceeded, retry_after_s=30", 30 * time.Second},
		{"retry_after_s=1 please", time.Second},
		{"retry_after_s=0", 0},
		{"quota exceeded", 0},
		{"retry_after_s=", 0},
		{"retry_after_s=abc", 0},
		// A server with a bug must not park the app until someone reloads.
		{"retry_after_s=999999", time.Hour},
	}
	for _, tc := range tests {
		got := Classify(status.Error(codes.ResourceExhausted, tc.msg)).RetryAfter
		if got != tc.want {
			t.Errorf("RetryAfter(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// TestBackoffAt pins the schedule the countdown estimates from, and — more to
// the point — pins the CAP. Twenty seconds is a decision (§20.19.5), not a
// default: gRPC's own is 120s, which is right for a datacentre client with
// thousands of peers and wrong for one tab and one server.
func TestBackoffAt(t *testing.T) {
	if got := backoffAt(1); got != 500*time.Millisecond {
		t.Errorf("first retry = %v, want 500ms — a server restart must be invisible", got)
	}
	if got := backoffAt(2); got != 800*time.Millisecond {
		t.Errorf("second retry = %v, want 800ms", got)
	}
	// It must actually reach the cap, and never exceed it.
	for n := 1; n < 100; n++ {
		if d := backoffAt(n); d > reconnectMax {
			t.Fatalf("backoffAt(%d) = %v, over the %v cap", n, d, reconnectMax)
		}
	}
	if got := backoffAt(30); got != reconnectMax {
		t.Errorf("backoffAt(30) = %v, want the %v cap", got, reconnectMax)
	}
	// Monotonic: a countdown that goes backwards is worse than none.
	for n := 1; n < 30; n++ {
		if backoffAt(n+1) < backoffAt(n) {
			t.Fatalf("backoff went backwards between %d and %d", n, n+1)
		}
	}
}

// TestRecoveryGate is the anti-storm rule (§20.19.7). Every transition into
// READY refetches the rail, the tags, the folders and the list; on a flapping
// tunnel that is four round trips per flap, at the exact moment the server is
// least able to serve them.
func TestRecoveryGate(t *testing.T) {
	t0 := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return t0.Add(d) }

	t.Run("a blip refetches nothing", func(t *testing.T) {
		var g RecoveryGate
		g.Lost(at(0))
		g.Recovered(at(1500 * time.Millisecond))
		if fetch, after := g.Poll(at(1500 * time.Millisecond)); fetch || after != 0 {
			t.Errorf("1.5s outage asked for a refetch (%v, %v) — nothing publishes in 1.5s",
				fetch, after)
		}
	})

	t.Run("a real outage refetches once", func(t *testing.T) {
		var g RecoveryGate
		g.Lost(at(0))
		g.Recovered(at(10 * time.Second))
		fetch, _ := g.Poll(at(10 * time.Second))
		if !fetch {
			t.Fatal("a ten-second outage must refetch")
		}
		// And exactly once: polling again is not a second recovery.
		if fetch, _ := g.Poll(at(10 * time.Second)); fetch {
			t.Error("polled twice, fetched twice")
		}
	})

	t.Run("ten rapid blips refetch nothing at all", func(t *testing.T) {
		// The flap that actually happens: a connection bouncing every half
		// second. Under the old code this was forty round trips.
		var g RecoveryGate
		now := time.Duration(0)
		fetches := 0
		for range 10 {
			g.Lost(at(now))
			now += 500 * time.Millisecond
			g.Recovered(at(now))
			if fetch, _ := g.Poll(at(now)); fetch {
				fetches++
			}
			now += 100 * time.Millisecond
		}
		if fetches != 0 {
			t.Errorf("got %d refetches from ten sub-second blips, want 0", fetches)
		}
	})

	t.Run("sustained flapping is paced, not suppressed", func(t *testing.T) {
		// Outages long enough to matter, one after another for ~26s. Every one
		// of them deserves a refetch and the reader only needs a few: the
		// answer is one per spacing window, not one per flap and not one in
		// total.
		var g RecoveryGate
		now := time.Duration(0)
		fetches := 0
		for range 10 {
			g.Lost(at(now))
			now += 2500 * time.Millisecond
			g.Recovered(at(now))
			if fetch, _ := g.Poll(at(now)); fetch {
				fetches++
			}
			now += 100 * time.Millisecond
		}
		if fetches != 5 {
			t.Errorf("got %d refetches across ten real outages spanning %v, want 5 (one per 5s)",
				fetches, now)
		}
	})

	t.Run("a suppressed refetch is deferred, not dropped", func(t *testing.T) {
		var g RecoveryGate
		g.Lost(at(0))
		g.Recovered(at(5 * time.Second))
		if fetch, _ := g.Poll(at(5 * time.Second)); !fetch {
			t.Fatal("first refetch must run")
		}
		g.Lost(at(6 * time.Second))
		g.Recovered(at(9 * time.Second))
		fetch, after := g.Poll(at(9 * time.Second))
		if fetch {
			t.Fatal("second refetch ran inside the spacing floor")
		}
		if after != time.Second {
			t.Fatalf("asked to wait %v, want 1s (5s spacing from the 5s fetch)", after)
		}
		// The caller comes back when told to, and the refetch is still owed.
		if fetch, _ := g.Poll(at(10 * time.Second)); !fetch {
			t.Error("the deferred refetch was dropped rather than delayed")
		}
	})

	t.Run("flapping mid-outage keeps the first moment", func(t *testing.T) {
		// A drop that bounces through Connecting on its way nowhere is ONE
		// outage. Re-stamping it would make every outage look 100ms long and
		// suppress every refetch there is.
		var g RecoveryGate
		g.Lost(at(0))
		g.Lost(at(4 * time.Second))
		g.Recovered(at(5 * time.Second))
		if fetch, _ := g.Poll(at(5 * time.Second)); !fetch {
			t.Error("a five-second outage was mistaken for a one-second blip")
		}
	})

	t.Run("recovery without a loss is the first connection", func(t *testing.T) {
		// The initial load owns the first connection; refetching here would
		// fetch page one twice on every boot.
		var g RecoveryGate
		g.Recovered(at(0))
		if fetch, _ := g.Poll(at(0)); fetch {
			t.Error("the first connection asked for a recovery refetch")
		}
	})
}
