package apierr

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The contract statusError has to satisfy, stated as two opposite properties:
// the cause must be reachable from the SERVER side and unreachable from the
// wire. Either one alone is easy and useless.

func TestTheCauseSurvivesForTheLog(t *testing.T) {
	cause := errors.New("sql: database is locked")
	err := Status(Internal(cause))

	if got := LogDetail(err); got != cause.Error() {
		t.Errorf("LogDetail() = %q, want %q — an internal error nothing can "+
			"explain is the exact failure this type exists to prevent", got, cause.Error())
	}
	// errors.Is/As must reach through, so a caller can test for a sentinel.
	if !errors.Is(err, cause) {
		t.Error("errors.Is cannot reach the cause")
	}
}

func TestTheCauseNeverReachesTheWire(t *testing.T) {
	err := Status(Internal(errors.New("sql: database is locked")))

	st, ok := status.FromError(err)
	if !ok {
		t.Fatal("status.FromError did not recognise the error — grpc-go would " +
			"send Unknown for every classified failure")
	}
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal", st.Code())
	}
	if st.Message() != "internal error" {
		t.Errorf("message = %q, want the safe fallback", st.Message())
	}
	if strings.Contains(st.Message(), "database is locked") {
		t.Error("the cause is on the wire")
	}
	// The proto is what actually gets serialised, so check that too rather
	// than trusting the accessors.
	if p := st.Proto(); strings.Contains(p.String(), "database is locked") {
		t.Errorf("the cause is in the serialised status: %s", p.String())
	}
}

// grpc-go asks for a status through this method. If the assertion it uses ever
// stops matching, every error in the program silently becomes Unknown — a
// failure that would show up as "the client stopped handling 404s" long before
// anybody suspected the error type.
func TestTheErrorIsRecognisedThroughTheGRPCStatusMethod(t *testing.T) {
	err := Status(NotFound())

	var target interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &target) {
		t.Fatal("the error does not satisfy the interface grpc-go looks for")
	}
	if got := target.GRPCStatus().Code(); got != codes.NotFound {
		t.Errorf("code = %v, want NotFound", got)
	}
	if got := status.Code(err); got != codes.NotFound {
		t.Errorf("status.Code() = %v, want NotFound", got)
	}
}

// A refusal with no hidden detail says so, rather than repeating the message
// the client already has. A log line that only restates the response is noise.
func TestLogDetailIsEmptyWhenThereIsNothingHidden(t *testing.T) {
	if got := LogDetail(Status(Invalid("title", "srv.invalidField", "too long"))); got != "" {
		t.Errorf("LogDetail() = %q, want empty", got)
	}
	if got := LogDetail(nil); got != "" {
		t.Errorf("LogDetail(nil) = %q, want empty", got)
	}
}

// Stamping the request id rebuilds the status from its code and message, which
// is exactly the step that would drop the cause again.
func TestTheCauseSurvivesTheRequestIDStamp(t *testing.T) {
	cause := errors.New("sql: database is locked")
	err := WithRequestID(Status(Internal(cause)), "7f3a9c")

	if got := LogDetail(err); got != cause.Error() {
		t.Errorf("LogDetail() after stamping = %q, want %q", got, cause.Error())
	}
	if got := RequestIDOf(err); got != "7f3a9c" {
		t.Errorf("RequestIDOf() = %q, want the id", got)
	}
	if st, _ := status.FromError(err); st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal", st.Code())
	}
}
