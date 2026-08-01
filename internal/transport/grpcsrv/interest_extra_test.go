package grpcsrv

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The two error paths interest_test.go's own fixture never exercises: it
// always resolves a real, valid scope, so neither GetInterestProfile's nor
// SteerInterest's own error-propagation lines ever run.

func TestGetInterestProfilePropagatesAScopeError(t *testing.T) {
	_, repo, _, _ := newInterestServer(t)
	srv := NewReaderServer(reader.New(repo, nil),
		func(context.Context) (store.Scope, error) { return store.Scope{}, errors.New("session lookup failed") })

	if _, err := srv.GetInterestProfile(context.Background(), &pb.GetInterestProfileRequest{}); err == nil {
		t.Fatal("a failing scope resolver produced no error")
	}
}

// TestGetInterestProfilePropagatesARepositoryError. An unscoped Scope passes
// the transport's own err != nil check (scopeOf itself did not fail) and
// fails one layer down, at the first repository read.
func TestGetInterestProfilePropagatesARepositoryError(t *testing.T) {
	_, repo, _, _ := newInterestServer(t)
	srv := NewReaderServer(reader.New(repo, nil),
		func(context.Context) (store.Scope, error) { return store.Scope{}, nil })

	_, err := srv.GetInterestProfile(context.Background(), &pb.GetInterestProfileRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated — store.ErrNoScope maps to KindUnauthenticated", status.Code(err))
	}
}

func TestSteerInterestPropagatesAScopeError(t *testing.T) {
	_, repo, _, _ := newInterestServer(t)
	srv := NewReaderServer(reader.New(repo, nil),
		func(context.Context) (store.Scope, error) { return store.Scope{}, errors.New("session lookup failed") })

	_, err := srv.SteerInterest(context.Background(), &pb.SteerInterestRequest{
		TopicKey: "whatever", Level: pb.SteerLevel_STEER_LEVEL_LESS,
	})
	if err == nil {
		t.Fatal("a failing scope resolver produced no error")
	}
}

// TestSteerInterestPropagatesANonNotFoundError. store.ErrNoScope reaching
// SteerTopic must not be reported as NotFound — the ErrNotFound arm exists
// specifically for a cluster that has retired, not for a caller with no
// scope at all, and confusing the two would tell an unauthenticated caller
// "no such topic" instead of the truth.
func TestSteerInterestPropagatesANonNotFoundError(t *testing.T) {
	_, repo, _, _ := newInterestServer(t)
	srv := NewReaderServer(reader.New(repo, nil),
		func(context.Context) (store.Scope, error) { return store.Scope{}, nil })

	_, err := srv.SteerInterest(context.Background(), &pb.SteerInterestRequest{
		TopicKey: "whatever", Level: pb.SteerLevel_STEER_LEVEL_LESS,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
	if errors.Is(err, store.ErrNotFound) {
		t.Error("an unscoped caller was told the topic does not exist, which leaks nothing "+
			"but is still the wrong reason")
	}
}
