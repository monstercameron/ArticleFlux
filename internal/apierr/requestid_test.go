package apierr

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/reqid"
)

// The property this file exists for: an id the server minted has to survive the
// trip to a client, or §22.11's whole arrangement is a log-grouping feature
// wearing a bug-report feature's description.

func TestRequestIDSurvivesOnAClassifiedError(t *testing.T) {
	err := WithRequestID(Status(Internal(errors.New("the database is on fire"))), "7f3a9c")

	if got := RequestIDOf(err); got != "7f3a9c" {
		t.Fatalf("RequestIDOf() = %q, want %q", got, "7f3a9c")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal — stamping the id must not reclassify the error", st.Code())
	}
	// The safe message is the contract §20.7 states, and the cause is what it
	// exists to keep off the wire. Stamping must not smuggle either.
	if st.Message() != "internal error" {
		t.Errorf("message = %q, want the safe fallback %q", st.Message(), "internal error")
	}
	if got := st.Message() + RequestIDOf(err); strings.Contains(got, "on fire") {
		t.Error("the underlying cause reached the client")
	}
}

// An unclassified error must be sanitized on the way through, not stamped and
// forwarded. This is the path a handler takes when it returns a raw error.
func TestRequestIDSanitizesAnUnclassifiedError(t *testing.T) {
	err := WithRequestID(errors.New("pq: relation \"users\" does not exist"), "abc123")

	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal", st.Code())
	}
	if strings.Contains(st.Message(), "relation") {
		t.Errorf("message = %q — a raw error reached the client verbatim", st.Message())
	}
	if got := RequestIDOf(err); got != "abc123" {
		t.Errorf("RequestIDOf() = %q, want %q", got, "abc123")
	}
}

// A detail the handler already populated keeps everything it had. The id is an
// addition to the error, never a replacement for it.
func TestRequestIDPreservesAnExistingDetail(t *testing.T) {
	original := Invalid("title", "srv.invalidField", "that title is too long")
	err := WithRequestID(Status(original), "deadbeef")

	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", st.Code())
	}
	d := detailOf(t, err)
	if d.GetKey() != "srv.invalidField" {
		t.Errorf("key = %q, want it unchanged", d.GetKey())
	}
	if d.GetField() != "title" {
		t.Errorf("field = %q, want %q — the field a form puts the message beside", d.GetField(), "title")
	}
	if d.GetArgs()[reqid.ArgKey] != "deadbeef" {
		t.Errorf("args[%s] = %q, want the id", reqid.ArgKey, d.GetArgs()[reqid.ArgKey])
	}
}

// Nothing to stamp is not an error condition, on either argument.
func TestRequestIDNoOps(t *testing.T) {
	if got := WithRequestID(nil, "abc"); got != nil {
		t.Errorf("WithRequestID(nil, …) = %v, want nil", got)
	}
	original := Status(NotFound())
	if got := WithRequestID(original, ""); got != original {
		t.Error("an empty id must return the error untouched, not rebuild it")
	}
	if got := RequestIDOf(errors.New("not a status")); got != "" {
		t.Errorf("RequestIDOf(plain error) = %q, want empty", got)
	}
}

// CrossTenant's identity with NotFound is the one property this package exists
// for, and a request id is the obvious way to break it: two responses that
// differ in any way re-open the leak. They share a code, a message and now a
// detail shape.
func TestRequestIDDoesNotDistinguishCrossTenantFromAGenuineMiss(t *testing.T) {
	miss := WithRequestID(Status(NotFound()), "1111")
	cross := WithRequestID(Status(CrossTenant()), "2222")

	ms, _ := status.FromError(miss)
	cs, _ := status.FromError(cross)
	if ms.Code() != cs.Code() {
		t.Errorf("codes differ: %v vs %v", ms.Code(), cs.Code())
	}
	if ms.Message() != cs.Message() {
		t.Errorf("messages differ: %q vs %q", ms.Message(), cs.Message())
	}
	if detailOf(t, miss).GetKey() != detailOf(t, cross).GetKey() {
		t.Error("detail keys differ — a cross-tenant access is distinguishable from a miss")
	}
}

