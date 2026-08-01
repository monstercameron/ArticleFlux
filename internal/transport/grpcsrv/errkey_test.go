package grpcsrv

import (
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/apierr"
)

// kindOf is the reverse of the direction apierr owns, and it exists only for
// these legacy call sites — so every case it actually maps deserves its own
// assertion rather than a spot check that happens to pass.
func TestKindOfCoversEveryMappedCodeAndFallsBackToInternal(t *testing.T) {
	cases := []struct {
		code codes.Code
		want apierr.Kind
	}{
		{codes.Unauthenticated, apierr.KindUnauthenticated},
		{codes.PermissionDenied, apierr.KindPermissionDenied},
		{codes.NotFound, apierr.KindNotFound},
		{codes.InvalidArgument, apierr.KindInvalidArgument},
		{codes.FailedPrecondition, apierr.KindFailedPrecondition},
		{codes.ResourceExhausted, apierr.KindResourceExhausted},
		{codes.Aborted, apierr.KindAborted},
		{codes.Unavailable, apierr.KindUnavailable},
		{codes.Unimplemented, apierr.KindUnimplemented},
		// Unrecognised becomes Internal, the same fail-safe apierr applies to an
		// unrecognised error — codes.DataLoss is picked because nothing in this
		// package ever emits it, so it can only reach the default arm.
		{codes.DataLoss, apierr.KindInternal},
	}
	for _, c := range cases {
		if got := kindOf(c.code); got != c.want {
			t.Errorf("kindOf(%v) = %v, want %v", c.code, got, c.want)
		}
	}
}
