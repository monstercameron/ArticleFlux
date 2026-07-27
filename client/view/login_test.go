//go:build js && wasm

package view

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// TestLoginMessageNil covers the one case with no gRPC status at all: no
// error to report.
func TestLoginMessageNil(t *testing.T) {
	tr := mustRuntime(t)
	if got := loginMessage(tr, nil); got != "" {
		t.Errorf("loginMessage(nil) = %q, want empty string", got)
	}
}

// TestLoginMessageNotAGRPCStatus covers an error that status.FromError
// cannot parse as a Status at all (ok == false) — the errGeneric fallback.
func TestLoginMessageNotAGRPCStatus(t *testing.T) {
	tr := mustRuntime(t)
	err := errors.New("some plain Go error, not a gRPC status")
	want := "Couldn't sign in. Check the server is running and try again."
	if got := loginMessage(tr, err); got != want {
		t.Errorf("loginMessage(plain error) = %q, want %q", got, want)
	}
}

// TestLoginMessageEveryGRPCCode is the exhaustive sweep the task calls for:
// every code.Code this build can receive from the transport, and the bucket
// loginMessage's switch puts it in.
func TestLoginMessageEveryGRPCCode(t *testing.T) {
	tr := mustRuntime(t)

	const (
		errUnreachable = "Can't reach the server. Check it's running, then try again."
		errGeneric     = "Couldn't sign in. Check the server is running and try again."
	)
	const rawMessage = "some transport-composed english"

	cases := []struct {
		code codes.Code
		want string
	}{
		// OK is not actually reachable through this path: status.Error(OK, …)
		// returns a nil error (grpc's own contract), which is the err==nil
		// case tested separately above. Included here so the sweep visibly
		// covers all seventeen codes rather than skipping one silently.
		{codes.OK, ""},

		{codes.Canceled, errGeneric},
		{codes.Unknown, errGeneric},
		{codes.InvalidArgument, errGeneric},
		{codes.DeadlineExceeded, errUnreachable},
		{codes.NotFound, errGeneric},
		{codes.AlreadyExists, errGeneric},
		{codes.PermissionDenied, errGeneric},
		{codes.ResourceExhausted, rawMessage}, // routed through serverText
		{codes.FailedPrecondition, rawMessage},
		{codes.Aborted, errGeneric},
		{codes.OutOfRange, errGeneric},
		{codes.Unimplemented, errGeneric},
		{codes.Internal, errGeneric},
		{codes.Unavailable, errUnreachable},
		{codes.DataLoss, errGeneric},
		{codes.Unauthenticated, rawMessage},
	}

	seen := map[codes.Code]bool{}
	for _, c := range cases {
		seen[c.code] = true
		t.Run(c.code.String(), func(t *testing.T) {
			err := status.Error(c.code, rawMessage)
			got := loginMessage(tr, err)
			if got != c.want {
				t.Errorf("loginMessage(code=%s, %q) = %q, want %q", c.code, rawMessage, got, c.want)
			}
		})
	}

	// codes.Code is a uint32 with a known, small, contiguous range in this
	// gRPC version (0..16). If a future bump adds one, this fails loudly
	// instead of silently leaving it untested.
	for i := codes.Code(0); i <= codes.Unauthenticated; i++ {
		if !seen[i] {
			t.Errorf("code %s (%d) is not covered by this sweep", i, i)
		}
	}
}

// TestLoginMessageResolvesAServerCatalogKey exercises the full real-world
// path: the server attaches an ErrorDetail naming a catalog key (see
// internal/apierr and client/view/srverr.go's serverText), and loginMessage
// must resolve it to the TRANSLATED sentence, not the raw transport message.
func TestLoginMessageResolvesAServerCatalogKey(t *testing.T) {
	tr := mustRuntime(t)

	st := status.New(codes.Unauthenticated, "raw transport text nobody should see")
	withDetail, err := st.WithDetails(&pb.ErrorDetail{Key: "srv.badCredentials"})
	if err != nil {
		t.Fatalf("WithDetails: %v", err)
	}

	want := "invalid username or password"
	if got := loginMessage(tr, withDetail.Err()); got != want {
		t.Errorf("loginMessage with ErrorDetail key srv.badCredentials = %q, want %q", got, want)
	}
}

// TestLoginMessageFallsBackWhenTheKeyIsUnknown covers serverText's middle
// fallback tier: a NEWER server sends a catalog key this build has never
// heard of. The English message the server also sent must win, not the
// bare identifier and not errGeneric.
func TestLoginMessageFallsBackWhenTheKeyIsUnknown(t *testing.T) {
	tr := mustRuntime(t)

	st := status.New(codes.FailedPrecondition, "a sentence from a future server version")
	withDetail, err := st.WithDetails(&pb.ErrorDetail{Key: "srv.aKeyThisBuildHasNeverHeardOf"})
	if err != nil {
		t.Fatalf("WithDetails: %v", err)
	}

	want := "a sentence from a future server version"
	got := loginMessage(tr, withDetail.Err())
	if got != want {
		t.Errorf("loginMessage with an unknown catalog key = %q, want the server's English fallback %q", got, want)
	}
	if got == "srv.aKeyThisBuildHasNeverHeardOf" {
		t.Errorf("loginMessage leaked the raw catalog key instead of falling back to English")
	}
}
