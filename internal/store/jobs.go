package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
)

// The durable job queue's storage half (TODO 6.4, plan.md §22.7).
//
// The queue lives in SQLite because at this scale an external broker is a second
// thing to run, back up and restore for no property the database does not
// already have. A `jobs` table in the same file as the data means enqueueing and
// the write that caused it are ONE transaction — which is the property that
// actually matters, and the one a broker cannot give you at any price.
//
// # Durable is not the same as restart-survivable
//
// Durable means the row outlives a crash. Survivable means a job that was
// *running* during the crash can be noticed and reclaimed. Without `locked_by`
// and `locked_at` a crashed worker's jobs sit in 'running' forever: the queue
// loses one slot per crash, silently, and the symptom weeks later is "polling
// got slow" rather than anything pointing at a crash.
//
// Claiming is `UPDATE ... WHERE state='queued'` inside the single writer (A24),
// so two workers cannot claim the same row — the write pool has exactly one
// connection and the update is atomic within it. That is why there is no
// SELECT-then-UPDATE race to reason about here, and why this would need real
// care if the writer pool ever grew.

// stamp formats a timestamp the way every other table in this database does.
// RFC3339Nano sorts lexically in the same order it sorts chronologically, which
// is what lets `run_after <= ?` be a string comparison against an index.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// JobKind names a queue. Per-kind concurrency caps are enforced against these.
type JobKind string

const (
	JobFanout    JobKind = "fanout"
	JobRank      JobKind = "rank"
	JobExtract   JobKind = "extract"
	JobPack      JobKind = "pack"
	JobAudio     JobKind = "audio"
	JobPreserve  JobKind = "preserve"
	JobRecommend JobKind = "recommend"
	JobLinkCheck JobKind = "linkcheck"
	JobEmbed     JobKind = "embed"
	JobDerive    JobKind = "derive"
)

// JobState is where a job is in its life.
type JobState string

const (
	StateQueued  JobState = "queued"
	StateRunning JobState = "running"
	StateDone    JobState = "done"
	StateFailed  JobState = "failed"
	// StateDead is a job that exhausted its attempts. Kept rather than deleted:
	// "why did this never happen" is answerable only if the corpse is still
	// there, and a dead-letter queue nobody can read is a deleted job with extra
	// steps.
	StateDead JobState = "dead"
)

// Job is one unit of queued work.
type Job struct {
	ID       string
	Kind     JobKind
	TenantID string
	Payload  string
	State    JobState
	Priority int
	Attempts int
	LastErr  string
	RunAfter time.Time
	LockedBy string
	LockedAt time.Time
}

// NewJob describes work to enqueue.
type NewJob struct {
	Kind     JobKind
	TenantID string
	Payload  string
	// Priority orders the ready set; higher runs first. Same priority falls back
	// to run_after, so a burst of equal-priority work is FIFO within itself.
	Priority int
	// RunAfter delays the job. Zero means now.
	RunAfter time.Time
	// DedupeKey makes enqueueing idempotent: a queued job with the same kind and
	// key is left alone rather than duplicated. Empty disables it.
	//
	// This is what stops a poller that runs every fifteen minutes from stacking
	// up ninety-six identical "derive affinities for this user" jobs overnight
	// while the first one is still waiting.
	DedupeKey string
}

// MaxAttempts is how many times a job is retried before it is declared dead.
const MaxAttempts = 5

// ErrNoJob is returned by Claim when nothing is ready.
var ErrNoJob = errors.New("store: no job ready")

// Enqueue adds a job.
//
// Returns the id, or the id of the existing job when DedupeKey matched — the
// caller cannot tell whether it created the work or joined it, which is correct:
// either way the work will happen exactly once.
func (r *ReaderRepo) Enqueue(ctx context.Context, n NewJob) (string, error) {
	if n.Kind == "" {
		return "", fmt.Errorf("store: a job needs a kind")
	}
	if n.RunAfter.IsZero() {
		n.RunAfter = time.Now().UTC()
	}

	id := idgen.New()
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		if n.DedupeKey != "" {
			var existing string
			err := tx.QueryRowContext(ctx,
				`SELECT id FROM jobs
				  WHERE kind = ? AND state = 'queued' AND json_extract(payload_json,'$._dedupe') = ?
				  LIMIT 1`,
				string(n.Kind), n.DedupeKey).Scan(&existing)
			switch {
			case err == nil:
				id = existing
				return nil
			case !errors.Is(err, sql.ErrNoRows):
				return err
			}
		}

		payload := n.Payload
		if payload == "" {
			payload = "{}"
		}
		if n.DedupeKey != "" {
			// The key is stored inside the payload rather than in a column so
			// the dedupe is queryable without a migration per new job kind.
			// json_set keeps whatever the caller put there.
			var err error
			payload, err = jsonSet(ctx, tx, payload, "_dedupe", n.DedupeKey)
			if err != nil {
				return err
			}
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO jobs (id, kind, tenant_id, payload_json, state, priority,
			                   attempts, run_after, created_at)
			 VALUES (?, ?, ?, ?, 'queued', ?, 0, ?, ?)`,
			id, string(n.Kind), nullify(n.TenantID), payload, n.Priority,
			stamp(n.RunAfter), stamp(time.Now().UTC()))
		return err
	})
	if err != nil {
		return "", fmt.Errorf("store: enqueue: %w", err)
	}
	return id, nil
}

// jsonSet writes one key into a JSON payload using SQLite's own json1, so the
// payload never has to be parsed in Go and a malformed one fails here rather
// than at claim time.
func jsonSet(ctx context.Context, tx *sql.Tx, payload, key, value string) (string, error) {
	var out string
	err := tx.QueryRowContext(ctx, `SELECT json_set(?, '$.'||?, ?)`, payload, key, value).Scan(&out)
	if err != nil {
		return "", fmt.Errorf("payload is not valid JSON: %w", err)
	}
	return out, nil
}

// ClaimOptions bound one claim.
type ClaimOptions struct {
	// Worker identifies the claimer, and is what makes a crashed worker's jobs
	// identifiable rather than merely stuck.
	Worker string
	// Kinds restricts the claim. Empty means any kind.
	Kinds []JobKind
	// Exclude names kinds that are already at their concurrency cap. §22.7's
	// per-kind caps are enforced by the caller passing this — the queue does not
	// know how many workers exist, and a cap it enforced alone would be wrong
	// the moment a second process started.
	Exclude []JobKind
}

// Claim atomically takes the next ready job.
//
// Ordered by priority then run_after: §22.7's "one slow batch must not
// permanently penalise everything behind it" is why the ready index is on
// (run_after, priority DESC) rather than on insertion order.
func (r *ReaderRepo) Claim(ctx context.Context, opt ClaimOptions) (Job, error) {
	if opt.Worker == "" {
		return Job{}, fmt.Errorf("store: a claim needs a worker id")
	}
	now := time.Now().UTC()

	var job Job
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		where := []string{"state = 'queued'", "run_after <= ?"}
		args := []any{stamp(now)}

		if len(opt.Kinds) > 0 {
			where = append(where, "kind IN ("+placeholders(len(opt.Kinds))+")")
			for _, k := range opt.Kinds {
				args = append(args, string(k))
			}
		}
		if len(opt.Exclude) > 0 {
			where = append(where, "kind NOT IN ("+placeholders(len(opt.Exclude))+")")
			for _, k := range opt.Exclude {
				args = append(args, string(k))
			}
		}

		q := `SELECT id, kind, ifnull(tenant_id,''), payload_json, priority, attempts
		        FROM jobs WHERE ` + strings.Join(where, " AND ") + `
		       ORDER BY priority DESC, run_after ASC, id ASC LIMIT 1`

		err := tx.QueryRowContext(ctx, q, args...).Scan(
			&job.ID, &job.Kind, &job.TenantID, &job.Payload, &job.Priority, &job.Attempts)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoJob
		}
		if err != nil {
			return err
		}

		// The claim itself. Re-checking state='queued' costs nothing and is what
		// would make this safe if the writer pool ever grew past one connection —
		// today A24 guarantees it, but the guarantee lives in a different file.
		res, err := tx.ExecContext(ctx,
			`UPDATE jobs SET state='running', locked_by=?, locked_at=?, attempts=attempts+1
			  WHERE id=? AND state='queued'`,
			opt.Worker, stamp(now), job.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNoJob
		}

		job.State = StateRunning
		job.Attempts++
		job.LockedBy = opt.Worker
		job.LockedAt = now
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return job, nil
}

// Complete marks a job done.
func (r *ReaderRepo) Complete(ctx context.Context, jobID string) error {
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE jobs SET state='done', finished_at=?, locked_by=NULL, locked_at=NULL
			  WHERE id=?`,
			stamp(time.Now().UTC()), jobID)
		return err
	})
}

// Fail records a failure and reschedules with backoff, or declares the job dead.
//
// Backoff is exponential from one minute. The first retry of a transient network
// failure should be soon; the fifth should not be, because by then the thing is
// probably not transient and retrying hard turns one broken feed into a
// self-inflicted load problem.
func (r *ReaderRepo) Fail(ctx context.Context, jobID string, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	// Bounded: a provider that echoes the request back in its error message
	// should not be able to write megabytes into this table one retry at a time.
	if len(msg) > 1000 {
		msg = msg[:1000] + "…"
	}

	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		var attempts int
		if err := tx.QueryRowContext(ctx, `SELECT attempts FROM jobs WHERE id=?`, jobID).
			Scan(&attempts); err != nil {
			return err
		}

		if attempts >= MaxAttempts {
			_, err := tx.ExecContext(ctx,
				`UPDATE jobs SET state='dead', last_error=?, finished_at=?,
				                 locked_by=NULL, locked_at=NULL
				  WHERE id=?`,
				msg, stamp(time.Now().UTC()), jobID)
			return err
		}

		delay := time.Duration(1<<uint(attempts-1)) * time.Minute
		if delay > time.Hour {
			delay = time.Hour
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE jobs SET state='queued', last_error=?, run_after=?,
			                 locked_by=NULL, locked_at=NULL
			  WHERE id=?`,
			msg, stamp(time.Now().UTC().Add(delay)), jobID)
		return err
	})
}

// ReclaimStale returns jobs whose worker died back to the queue.
//
// This is the half that makes the queue restart-survivable rather than merely
// durable. `olderThan` must exceed the longest a job legitimately runs, or this
// reclaims work that is still in progress and it runs twice — which is why the
// caller passes it rather than it being a constant here: pack building and rule
// fan-out differ by three orders of magnitude.
//
// Attempts are NOT incremented. A worker that was killed is not evidence that
// the job is bad, and counting it would push a job toward dead for a reason that
// has nothing to do with the job.
func (r *ReaderRepo) ReclaimStale(ctx context.Context, olderThan time.Duration) (int, error) {
	cut := stamp(time.Now().UTC().Add(-olderThan))
	var n int64
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE jobs SET state='queued', locked_by=NULL, locked_at=NULL,
			                 last_error='reclaimed after the worker went away'
			  WHERE state='running' AND locked_at < ?`, cut)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}

// GetJob reads one job.
//
// Exists so callers can inspect state without reaching for the database
// directly — guard 1 keeps SQL inside this package, and an exported *DB would
// quietly undo that for everyone.
func (r *ReaderRepo) GetJob(ctx context.Context, id string) (Job, error) {
	var j Job
	var runAfter, lockedBy, lockedAt sql.NullString
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT id, kind, ifnull(tenant_id,''), payload_json, state, priority,
		        attempts, ifnull(last_error,''), run_after, locked_by, locked_at
		   FROM jobs WHERE id = ?`, id).
		Scan(&j.ID, &j.Kind, &j.TenantID, &j.Payload, &j.State, &j.Priority,
			&j.Attempts, &j.LastErr, &runAfter, &lockedBy, &lockedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	j.LockedBy = lockedBy.String
	j.RunAfter, _ = time.Parse(time.RFC3339Nano, runAfter.String)
	if lockedAt.Valid {
		j.LockedAt, _ = time.Parse(time.RFC3339Nano, lockedAt.String)
	}
	return j, nil
}

// QueueDepth reports queued and running counts per kind.
//
// Feeds §9's status screen and, more importantly, the caller's concurrency caps:
// a worker pool asks this to decide which kinds to exclude from its next claim.
func (r *ReaderRepo) QueueDepth(ctx context.Context) (map[JobKind]QueueStat, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT kind,
		        sum(state='queued'),
		        sum(state='running'),
		        sum(state='dead')
		   FROM jobs
		  WHERE state IN ('queued','running','dead')
		  GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[JobKind]QueueStat{}
	for rows.Next() {
		var kind string
		var st QueueStat
		if err := rows.Scan(&kind, &st.Queued, &st.Running, &st.Dead); err != nil {
			return nil, err
		}
		out[JobKind(kind)] = st
	}
	return out, rows.Err()
}

// QueueStat is one kind's depth.
type QueueStat struct {
	Queued  int
	Running int
	Dead    int
}

// PurgeFinished deletes completed jobs older than a cutoff.
//
// Done jobs only. Dead ones are kept regardless of age, because they are the
// record of work that never happened and deleting them on a schedule means the
// only evidence of a persistent failure expires quietly.
func (r *ReaderRepo) PurgeFinished(ctx context.Context, olderThan time.Duration) (int, error) {
	cut := stamp(time.Now().UTC().Add(-olderThan))
	var n int64
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM jobs WHERE state='done' AND finished_at < ?`, cut)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}
