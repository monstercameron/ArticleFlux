package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// Idempotency storage (TODO 7.3c, plan.md §12.4, §20.7).
//
// # Why this has to exist BEFORE the offline outbox, not after
//
// A phone that has been offline queues mutations and drains them on reconnect.
// If the connection drops halfway through the drain, the client cannot know
// which of its writes landed — so it resends, because the alternative is losing
// them. Without a replay, the second attempt applies a second time:
//
//	a star toggles back off
//	a note is appended twice
//	an unread count moves by two
//
// All three are silent. None of them errors, and none of them is visible until
// the reader notices their own data is wrong. That is why §20.7 requires this
// for every outbox-replayed mutation rather than treating it as a refinement.
//
// # Verbatim, and why the response is stored rather than recomputed
//
// The replay returns the bytes that were sent the first time. Recomputing the
// answer would be subtly different — a `rev` has moved on, a timestamp is
// later — and a client that receives a *different* response to the same request
// has no way to tell a replay from a second effect.
//
// # The request hash is not paranoia
//
// A key is chosen by the client. Two different requests sent under one key —
// a bug, a reused constant, a truncated uuid — would otherwise return the first
// one's answer for the second one's write, which is worse than no idempotency at
// all: the write is silently dropped AND the caller is told it succeeded.

// ErrIdemConflict means the key was reused for a different request.
var ErrIdemConflict = errors.New("store: this idempotency key was used for a different request")

// IdemTTL is how long a response is replayable.
//
// 24 hours, from §20.7. Long enough that a phone left in a bag overnight still
// drains correctly, short enough that the table does not become a second copy of
// every mutation the instance has ever served.
const IdemTTL = 24 * time.Hour

// IdemRecord is a stored response.
type IdemRecord struct {
	Response []byte
	Status   int
}

// BeginIdempotent checks for a previous response under this key.
//
// Returns (record, true, nil) when this request has already been served — the
// caller returns the stored response and does nothing else. Returns
// (_, false, nil) when it is new and should proceed.
func (r *ReaderRepo) BeginIdempotent(ctx context.Context, s Scope, key, method string, request []byte) (IdemRecord, bool, error) {
	if !s.Valid() {
		return IdemRecord{}, false, ErrNoScope
	}
	if key == "" {
		// No key means the caller is not asking for idempotency. Treating an
		// empty key as a key would make every request without one collide with
		// every other.
		return IdemRecord{}, false, nil
	}

	hash := requestHash(method, request)

	var storedHash string
	var response []byte
	var status int
	var expires string
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT request_hash, response_blob, status_code, expires_at
		   FROM idempotency_keys WHERE user_id = ? AND key = ?`,
		s.UserID, key).Scan(&storedHash, &response, &status, &expires)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return IdemRecord{}, false, nil
	case err != nil:
		return IdemRecord{}, false, err
	}

	// Expired: treat as new. The row is left for the sweeper rather than deleted
	// here, because a read path that writes turns every GET into a lock wait on
	// the single writer.
	if expires < stamp(time.Now().UTC()) {
		return IdemRecord{}, false, nil
	}

	if storedHash != hash {
		return IdemRecord{}, false, ErrIdemConflict
	}
	if response == nil {
		// The first attempt is still in flight, or it crashed before recording a
		// result. Reporting a conflict is wrong (it is the same request) and
		// replaying nothing is wrong (there is no answer yet), so the caller is
		// told to proceed — the underlying mutation is expected to be safe to
		// repeat, which is what at-least-once delivery already requires.
		return IdemRecord{}, false, nil
	}
	return IdemRecord{Response: response, Status: status}, true, nil
}

// CompleteIdempotent records the response so a retry can replay it.
//
// Called after the mutation commits. Deliberately NOT in the same transaction:
// this is a cache of an answer, and a failure to record it must not roll back
// the write it describes. The cost of that choice is a crash-shaped window where
// the write happened and the response was not stored — in which case the retry
// re-runs a mutation that was already safe to repeat.
func (r *ReaderRepo) CompleteIdempotent(ctx context.Context, s Scope, key, method string, request, response []byte, status int) error {
	if !s.Valid() {
		return ErrNoScope
	}
	if key == "" {
		return nil
	}

	now := time.Now().UTC()
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO idempotency_keys
			    (user_id, key, method, request_hash, response_blob, status_code,
			     created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(user_id, key) DO UPDATE SET
			    response_blob = excluded.response_blob,
			    status_code   = excluded.status_code`,
			s.UserID, key, method, requestHash(method, request), response, status,
			stamp(now), stamp(now.Add(IdemTTL)))
		return err
	})
}

// ReserveIdempotent records that a request is in flight, before it runs.
//
// Optional, and worth doing for expensive mutations: without it, two concurrent
// retries of the same key both see "no row" and both execute. With it, the
// second sees the reservation, finds no response yet, and proceeds anyway —
// which sounds useless until you notice the row is what makes the CONFLICT check
// work for a client that reuses a key for a genuinely different request while
// the first is still running.
func (r *ReaderRepo) ReserveIdempotent(ctx context.Context, s Scope, key, method string, request []byte) error {
	if !s.Valid() {
		return ErrNoScope
	}
	if key == "" {
		return nil
	}
	now := time.Now().UTC()
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO idempotency_keys
			    (user_id, key, method, request_hash, response_blob, status_code,
			     created_at, expires_at)
			VALUES (?, ?, ?, ?, NULL, 0, ?, ?)
			ON CONFLICT(user_id, key) DO NOTHING`,
			s.UserID, key, method, requestHash(method, request),
			stamp(now), stamp(now.Add(IdemTTL)))
		return err
	})
}

// PurgeIdempotency removes expired keys.
//
// Unscoped: housekeeping over every tenant's dead rows, the same category as
// PurgeExpiredSessions.
func (r *ReaderRepo) PurgeIdempotency(ctx context.Context) (int, error) {
	var n int64
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM idempotency_keys WHERE expires_at < ?`, stamp(time.Now().UTC()))
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}

// requestHash fingerprints a request so a reused key with different content is
// detectable.
//
// Includes the method, because "SetItemState" and "SetNote" with identical
// bodies are different requests and a client that reused a key across them would
// otherwise get the wrong one's answer.
func requestHash(method string, request []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{0})
	h.Write(request)
	return hex.EncodeToString(h.Sum(nil)[:16])
}
