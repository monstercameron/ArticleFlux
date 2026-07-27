package store

import (
	"context"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/reqid"
)

// §22.11 asks for the request id in handlers AND jobs, and the queue is where
// it usually stops. Most of what this application does for a reader happens
// later, on a worker — fan-out, extraction, archival — so an id that ends at
// the RPC boundary explains the enqueue and nothing about the work.
func TestAJobRemembersTheRequestThatQueuedIt(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := reqid.With(context.Background(), "rpc-abc123")

	if _, err := repo.Enqueue(ctx, NewJob{Kind: JobFanout, Payload: `{"x":1}`}); err != nil {
		t.Fatal(err)
	}

	job, err := repo.Claim(context.Background(), ClaimOptions{Worker: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	if job.OriginRequestID != "rpc-abc123" {
		t.Errorf("origin = %q, want the id of the request that queued it", job.OriginRequestID)
	}
}

// Scheduler work no request asked for must not claim an origin, or the log
// says a user asked for something nobody asked for.
func TestSchedulerWorkHasNoOrigin(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()

	if _, err := repo.Enqueue(ctx, NewJob{Kind: JobFanout, Payload: `{}`}); err != nil {
		t.Fatal(err)
	}
	job, err := repo.Claim(ctx, ClaimOptions{Worker: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	if job.OriginRequestID != "" {
		t.Errorf("origin = %q on work no request queued", job.OriginRequestID)
	}
}
