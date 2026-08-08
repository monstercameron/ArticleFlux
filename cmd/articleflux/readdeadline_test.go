package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Which requests get a read deadline, and which must never.
//
// # Why this is worth a test rather than a careful comment
//
// A read deadline is not inert once the response is being written. net/http
// runs a background read on the connection to notice the client going away —
// that is what powers request-context cancellation — and an expired read
// deadline makes that read fail. net/http answers a failed background read by
// CANCELLING THE REQUEST CONTEXT, about eighty milliseconds later, in the middle
// of the response. The client receives a cleanly truncated body and no error.
//
// So arming one against a response that is supposed to last is not a slow
// timeout, it is a silent kill: no log line, no client error, a stream that
// stops. `/grpc` was exempt from the beginning. `/stream` — the MJPEG live view,
// which selects on r.Context().Done() frame by frame — was not, and was being
// cut off at requestReadTimeout while the renderer's own IdleTimeout said three
// minutes.
//
// The middleware is checked directly rather than through a real server so the
// test costs nothing and does not have to wait out a sixty-second constant.
// http.NewResponseController finds SetReadDeadline on the ResponseWriter, so a
// recorder that implements it observes exactly what the middleware did.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	armed bool
	at    time.Time
}

func (d *deadlineRecorder) SetReadDeadline(t time.Time) error {
	d.armed, d.at = true, t
	return nil
}

func TestOnlyLongLivedPathsEscapeTheReadDeadline(t *testing.T) {
	for _, c := range []struct {
		path      string
		wantArmed bool
		why       string
	}{
		{"/grpc", false, "the gRPC tunnel is hijacked and the deadline survives Hijack"},
		{"/stream", false, "the live view is an MJPEG response that ends only when the viewer leaves"},
		{"/", true, "an ordinary page must still be bounded"},
		{"/speech", true, "a bounded response, however slow, is still a request that has to arrive"},
		{"/pub/abc", true, "the public share is an ordinary request"},
		{"/asset", true, "the proxy is an ordinary request"},
	} {
		rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		var reached bool
		h := boundRequestReads(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			reached = true
		}))
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))

		if !reached {
			t.Errorf("%s: the handler was not reached at all", c.path)
		}
		if rec.armed != c.wantArmed {
			if c.wantArmed {
				t.Errorf("%s: no read deadline was armed, but one must be — %s",
					c.path, c.why)
			} else {
				t.Errorf("%s: a read deadline was armed, which cancels the request "+
					"context mid-response and ends the stream silently — %s",
					c.path, c.why)
			}
		}
	}
}

// The deadline that IS armed is the stated one, so lowering the constant is a
// deliberate act rather than something a refactor can do by accident.
func TestTheArmedDeadlineIsRequestReadTimeout(t *testing.T) {
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	before := time.Now()
	boundRequestReads(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !rec.armed {
		t.Fatal("no deadline was armed on an ordinary request")
	}
	got := rec.at.Sub(before)
	// A window rather than an equality: the clock moves between the two reads.
	if got < requestReadTimeout-time.Second || got > requestReadTimeout+time.Second {
		t.Errorf("deadline is %v from now, want about %v", got, requestReadTimeout)
	}
}
