package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func idemFixture(t *testing.T) (*ReaderRepo, context.Context, Scope) {
	t.Helper()
	db := openTest(t)
	repo := NewReaderRepo(db)
	seedTenant(t, repo)
	return repo, context.Background(), Scope{TenantID: "t1", UserID: "u1", Role: "member"}
}

// §12.4's scenario: a phone drains its outbox, the connection drops mid-flight,
// and it resends. Without a replay the second attempt applies a second time —
// a star toggles back off, a note is appended twice — and all of it is silent.
func TestARetriedMutationReplaysVerbatim(t *testing.T) {
	repo, ctx, sc := idemFixture(t)

	const key, method = "outbox-42", "SetItemState"
	req := []byte(`{"item":"i1","read":true}`)
	resp := []byte(`{"rev":17}`)

	// First attempt: nothing stored, so it proceeds.
	if _, replay, err := repo.BeginIdempotent(ctx, sc, key, method, req); err != nil || replay {
		t.Fatalf("a first attempt was treated as a replay (replay=%v, err=%v)", replay, err)
	}
	if err := repo.CompleteIdempotent(ctx, sc, key, method, req, resp, 200); err != nil {
		t.Fatal(err)
	}

	// The retry gets the SAME bytes back, and must not run again.
	rec, replay, err := repo.BeginIdempotent(ctx, sc, key, method, req)
	if err != nil {
		t.Fatal(err)
	}
	if !replay {
		t.Fatal("the retry was not recognised; the mutation would apply twice")
	}
	if string(rec.Response) != string(resp) {
		t.Errorf("replayed %q, want the original %q", rec.Response, resp)
	}
	if rec.Status != 200 {
		t.Errorf("status = %d", rec.Status)
	}
}

// A key reused for a genuinely different request must be refused. Returning the
// first request's answer for the second one's write drops the write AND reports
// success, which is worse than having no idempotency at all.
func TestAReusedKeyWithDifferentContentIsRefused(t *testing.T) {
	repo, ctx, sc := idemFixture(t)
	const key = "reused"

	if err := repo.CompleteIdempotent(ctx, sc, key, "SetItemState",
		[]byte(`{"item":"i1"}`), []byte(`{"ok":true}`), 200); err != nil {
		t.Fatal(err)
	}

	// Same method, different body.
	if _, _, err := repo.BeginIdempotent(ctx, sc, key, "SetItemState", []byte(`{"item":"i2"}`)); !errors.Is(err, ErrIdemConflict) {
		t.Errorf("a different body under the same key = %v, want ErrIdemConflict", err)
	}
	// Same body, different method — also a different request.
	if _, _, err := repo.BeginIdempotent(ctx, sc, key, "SetNote", []byte(`{"item":"i1"}`)); !errors.Is(err, ErrIdemConflict) {
		t.Errorf("a different method under the same key = %v, want ErrIdemConflict", err)
	}
}

// Keys are per user, or one reader's outbox could replay another's response.
func TestKeysAreScopedToTheUser(t *testing.T) {
	repo, ctx, a := idemFixture(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t2", Name: "B", UserID: "u2", Username: "b",
		Hash: "x", Role: "member", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	b := Scope{TenantID: "t2", UserID: "u2", Role: "member"}

	const key = "shared-key"
	req := []byte(`{"x":1}`)
	if err := repo.CompleteIdempotent(ctx, a, key, "M", req, []byte(`"alice"`), 200); err != nil {
		t.Fatal(err)
	}

	rec, replay, err := repo.BeginIdempotent(ctx, b, key, "M", req)
	if err != nil {
		t.Fatal(err)
	}
	if replay {
		t.Errorf("user B replayed user A's response: %q", rec.Response)
	}
}

// An empty key means the caller is not asking for idempotency. Treating it as a
// key would make every request without one collide with every other.
func TestAnEmptyKeyIsNotAKey(t *testing.T) {
	repo, ctx, sc := idemFixture(t)

	if err := repo.CompleteIdempotent(ctx, sc, "", "M", []byte(`{}`), []byte(`{}`), 200); err != nil {
		t.Fatal(err)
	}
	_, replay, err := repo.BeginIdempotent(ctx, sc, "", "M", []byte(`{"different":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if replay {
		t.Error("an unkeyed request replayed another unkeyed request's response")
	}
}

func TestExpiredKeysAreNotReplayedAndAreSwept(t *testing.T) {
	repo, ctx, sc := idemFixture(t)
	const key = "old"
	req := []byte(`{"x":1}`)

	if err := repo.CompleteIdempotent(ctx, sc, key, "M", req, []byte(`"old"`), 200); err != nil {
		t.Fatal(err)
	}
	mustExec(t, repo, `UPDATE idempotency_keys SET expires_at = '2000-01-01T00:00:00Z' WHERE key = ?`, key)

	// Expired: the request proceeds rather than replaying a day-old answer.
	if _, replay, err := repo.BeginIdempotent(ctx, sc, key, "M", req); err != nil || replay {
		t.Errorf("an expired key replayed (replay=%v, err=%v)", replay, err)
	}

	n, err := repo.PurgeIdempotency(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("purged %d expired keys, want 1", n)
	}
}

// A reservation records the request before it runs, so a concurrent retry with
// DIFFERENT content is caught rather than executing alongside it.
func TestAReservationCatchesAConflictBeforeTheFirstFinishes(t *testing.T) {
	repo, ctx, sc := idemFixture(t)
	const key = "inflight"

	if err := repo.ReserveIdempotent(ctx, sc, key, "M", []byte(`{"x":1}`)); err != nil {
		t.Fatal(err)
	}

	// The same request proceeds: there is no answer yet, and the mutation is
	// safe to repeat under at-least-once delivery.
	if _, replay, err := repo.BeginIdempotent(ctx, sc, key, "M", []byte(`{"x":1}`)); err != nil || replay {
		t.Errorf("an in-flight request was blocked (replay=%v, err=%v)", replay, err)
	}
	// A different request under the same key is a conflict, even mid-flight.
	if _, _, err := repo.BeginIdempotent(ctx, sc, key, "M", []byte(`{"x":2}`)); !errors.Is(err, ErrIdemConflict) {
		t.Errorf("a conflicting in-flight key = %v, want ErrIdemConflict", err)
	}

	// And completing overwrites the reservation rather than duplicating it.
	if err := repo.CompleteIdempotent(ctx, sc, key, "M", []byte(`{"x":1}`), []byte(`"done"`), 200); err != nil {
		t.Fatal(err)
	}
	rec, replay, err := repo.BeginIdempotent(ctx, sc, key, "M", []byte(`{"x":1}`))
	if err != nil || !replay || string(rec.Response) != `"done"` {
		t.Errorf("after completing: replay=%v resp=%q err=%v", replay, rec.Response, err)
	}
}

func TestIdempotencyRequiresAScope(t *testing.T) {
	repo, ctx, _ := idemFixture(t)
	if _, _, err := repo.BeginIdempotent(ctx, Scope{}, "k", "M", nil); err != ErrNoScope {
		t.Errorf("= %v, want ErrNoScope", err)
	}
	if err := repo.CompleteIdempotent(ctx, Scope{}, "k", "M", nil, nil, 200); err != ErrNoScope {
		t.Errorf("= %v, want ErrNoScope", err)
	}
}
