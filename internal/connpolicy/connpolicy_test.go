package connpolicy

import "testing"

// TestPairing is the whole reason this package exists.
//
// It is a four-line test standing in for a failure that presents as "the
// connection keeps dropping about once a minute" and is invisible in both files
// that cause it: the client probes at one number, the server tolerates at
// another, and neither reads as wrong on its own. If someone tunes either value
// without the other, this fails here rather than in production.
func TestPairing(t *testing.T) {
	if ServerMinTime >= ClientInterval {
		t.Fatalf("ServerMinTime (%v) must be below ClientInterval (%v): a client "+
			"probing faster than the server tolerates is answered with GOAWAY "+
			"too_many_pings, which is a reconnect loop, not a keepalive",
			ServerMinTime, ClientInterval)
	}
	// Pings arrive late, never early — a throttled tab, a descheduled goroutine,
	// a GC pause all delay them. A floor with no slack is one that can only be
	// violated by jitter in our own favour.
	if Margin < ClientInterval/4 {
		t.Errorf("margin %v is under a quarter of the %v interval: too tight to "+
			"survive a throttled or descheduled ping", Margin, ClientInterval)
	}
}

// TestDetectionBudget pins the number §20.19.4 promises: the longest the
// indicator may report a live connection over a dead socket.
//
// Before the probe existed this was unbounded — the honest entry in the table
// was "never" — so the value of pinning it is less the 40s than the fact that
// it is now finite at all.
func TestDetectionBudget(t *testing.T) {
	const want = 40
	if got := (ClientInterval + ClientTimeout).Seconds(); got != want {
		t.Errorf("worst-case time-to-truth is %vs, §20.19.4 promises %vs", got, want)
	}
}

// TestAboveGRPCFloor guards a trap that costs nothing to fall into and says
// nothing when you do.
//
// gRPC replaces a client keepalive interval below 10s with 10s — silently, at
// transport construction, with no error returned and nothing logged. So the
// obvious response to "detection is too slow" is to lower ClientInterval, and
// the obvious response produces a number that is not used, a documented budget
// that is wrong, and no evidence anywhere that either happened.
func TestAboveGRPCFloor(t *testing.T) {
	if ClientInterval < GRPCClientFloor {
		t.Fatalf("ClientInterval %v is below gRPC's %v floor: gRPC will silently "+
			"use %v instead, so the real detection budget is %v — not the %v this "+
			"package claims",
			ClientInterval, GRPCClientFloor, GRPCClientFloor,
			GRPCClientFloor+ClientTimeout, ClientInterval+ClientTimeout)
	}
}
