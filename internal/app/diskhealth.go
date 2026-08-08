package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/diskcache"
	"github.com/monstercameron/ArticleFlux/internal/diskspace"
)

// Whether this instance can still WRITE, which is the half readiness never
// asked about.
//
// # One failure, three gaps
//
// Nothing bounded the caches, nothing measured the disk, and the probes could
// not see a full one. That is a single path, not three problems: the caches
// grow into the volume the database is on, the volume fills, SQLite starts
// returning SQLITE_FULL on writes while every read keeps working, and both
// `/healthz` and `/readyz` stay green because the only thing readiness checked
// was that a schema version could be READ.
//
// What that looks like from outside is a reader that loads, shows articles,
// and silently fails to remember anything you did with them. Nobody files that
// as an outage for hours. `internal/diskcache` stops the growth; this stops the
// silence.
//
// # Why a write probe and not only the free-space number
//
// Free space answers "will this fill soon". It does not catch a volume
// remounted read-only after a filesystem error, a data directory whose owner
// changed under a redeploy, or a quota. Those produce exactly the same symptom
// — reads fine, writes fail — and only actually writing finds them. So the
// check does both, and the free-space floor exists so the instance can say it
// is unready with room to spare rather than at the moment it has none.
//
// # Why the result is cached
//
// `/readyz` is polled every two minutes by the watchdog, and readiness is also
// consulted on every tunnel upgrade. A stat plus a create-write-fsync-unlink on
// each of those is real IO on the same disk the check is worried about. Fifteen
// seconds is far shorter than any monitor's interval and long enough that a
// burst of upgrades costs one probe.

// diskFloor is how much room the data directory must have to count as ready.
//
// 256 MB. Not a guess at what SQLite needs for one transaction — that is
// kilobytes — but at what it needs to keep working through a checkpoint of a
// WAL that has grown while the disk was filling, plus the room a backup's
// `VACUUM INTO` wants. Below this the instance is not broken yet, and saying
// so early is the entire point: a probe that only fails once writes fail has
// told the operator nothing they had not already heard from a user.
const diskFloor = 256 << 20

// diskProbeTTL is how long a disk verdict is reused. See the note above.
const diskProbeTTL = 15 * time.Second

// incrementalVacuumPages bounds how much the poll cycle hands back per pass.
//
// A thousand pages is about four megabytes at SQLite's 4 KiB default, and a few
// milliseconds — because `PRAGMA incremental_vacuum(n)` moves pages and
// truncates rather than rewriting the file. The bound exists because this runs
// while somebody is reading: unbounded, the first pass after a large retention
// sweep would return hundreds of megabytes in one write-locked burst, which is
// a pause a reader notices for the sake of disk space nobody was waiting on.
//
// Four megabytes a cycle against a fifteen-minute poll is a gigabyte a week,
// which comfortably outruns anything retention can free.
const incrementalVacuumPages = 1000

// diskHealth caches the last verdict.
type diskHealth struct {
	mu       sync.Mutex
	checked  time.Time
	lastErr  error
	lastFree uint64
}

// diskReady reports whether the data directory can still be written to.
//
// The error is returned to the caller and NOT to the client: `/readyz` answers
// one word by §22.4, because an unauthenticated endpoint that names a path and
// a byte count is describing the instance to everybody. The detail goes to the
// log, where the operator is.
func (a *App) diskReady(ctx context.Context) error {
	dir := filepath.Dir(a.cfg.DBPath)

	a.disk.mu.Lock()
	defer a.disk.mu.Unlock()
	if time.Since(a.disk.checked) < diskProbeTTL {
		return a.disk.lastErr
	}
	a.disk.checked = time.Now()
	a.disk.lastErr = nil

	// The headroom, first, because it is the cheap one and because it is the
	// answer that arrives BEFORE the failure.
	free, err := diskspace.Free(dir)
	switch {
	case errors.Is(err, diskspace.ErrUnsupported):
		// No measurement on this platform. Not a failure — the write probe
		// below needs no platform support and is the ground truth anyway.
	case err != nil:
		// A stat that fails on the directory holding the database is itself
		// news, but it is not proof the disk is full, so it is logged and the
		// probe below decides.
		a.log.WarnContext(ctx, "cannot measure free space on the data directory", "dir", dir, "err", err)
	default:
		a.disk.lastFree = free
		if free < diskFloor {
			a.disk.lastErr = fmt.Errorf("the data directory has %d MB free, below the %d MB floor",
				free>>20, uint64(diskFloor)>>20)
			return a.disk.lastErr
		}
	}

	// And the ground truth. Written into the data directory itself rather than
	// into the system temp dir, because those are frequently different
	// filesystems and the one that matters is the one the database is on —
	// `PrivateTmp=yes` in the unit makes that especially true.
	if err := probeWriteBounded(ctx, dir); err != nil {
		a.disk.lastErr = fmt.Errorf("the data directory is not writable: %w", err)
		return a.disk.lastErr
	}
	return nil
}

// diskProbeTimeout bounds the write probe.
//
// Comfortably longer than an fsync on a working volume and comfortably shorter
// than any monitor's patience, which is the window this has to sit in.
const diskProbeTimeout = 5 * time.Second

// probeWriteBounded runs the probe with a deadline, and reports a stall AS a
// failure.
//
// # Why the unbounded version was wrong in the one case that matters
//
// probeWrite ends in f.Sync(), an fsync with no timeout, and diskReady runs it
// while holding a.disk.mu. On a working disk that is microseconds. On a stalled
// one — a detached block volume, a saturated device, an NFS mount that stopped
// answering — it blocks indefinitely, and so does every caller queued behind the
// mutex.
//
// That inverts the endpoint. `/readyz` exists to say "this instance cannot
// write", and app.go is explicit that it is the ALERTING path: "the thing that
// notices /readyz is the alerting path, which can wake somebody up." A probe
// that hangs when the disk hangs makes the endpoint stop answering at precisely
// the moment its answer is the most informative one. A monitor then sees a
// timeout, which is a weaker signal than a 503 that names the reason, and every
// concurrent prober piles up behind the lock.
//
// # The goroutine is deliberately allowed to outlive the call
//
// An fsync in flight cannot be cancelled — there is no syscall for it. So the
// helper returns on the deadline and leaves the write to finish or not. That
// leaks one goroutine and one file descriptor per stalled probe, bounded by
// diskProbeTTL to roughly one per fifteen seconds, and the temp file is still
// removed by probeWrite's own defer whenever the filesystem comes back.
//
// Answering is worth that. The alternative is answering nothing.
// probeWriteFn is the probe itself, indirected so a test can stall it.
//
// A hung filesystem is not something a test can produce portably, and the
// behaviour worth pinning is not "fsync can block" — it is that this function
// answers when the probe does not. Without the seam the only honest test would
// be a copy of the select below, which pins nothing about the code that ships.
var probeWriteFn = probeWrite

func probeWriteBounded(ctx context.Context, dir string) error {
	done := make(chan error, 1) // buffered, so the goroutine never blocks on send
	go func() { done <- probeWriteFn(dir) }()

	timer := time.NewTimer(diskProbeTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return fmt.Errorf("the write probe did not finish within %s, which is a "+
			"stalled filesystem rather than a full one", diskProbeTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// probeWrite creates, writes, syncs and removes a small file.
//
// The Sync is not ceremony. A buffered write to a full disk succeeds and the
// ENOSPC arrives at flush time, so a probe that skipped it would report a
// healthy disk right up to the moment the operating system got around to
// telling somebody.
func probeWrite(dir string) error {
	f, err := os.CreateTemp(dir, ".writeprobe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()

	if _, err := f.Write([]byte("articleflux write probe\n")); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// cacheBudgets is how the instance's total cache allowance is divided.
//
// # Why shares of one number rather than five settings
//
// An operator has one question — how much of this volume may caches have — and
// five knobs is four more chances to set a number nobody can add up. The shares
// are fixed here because they encode something an operator has no way to know:
// what each cache costs to refill.
//
// Speech gets half of it because it is the only one that was PAID for. An
// evicted synthesised file is a second OpenAI bill; an evicted proxied image is
// one more HTTP request to a server that was going to serve it anyway. Podcast
// segments are paid too, and smaller. Pages, assets and digests are cheap to
// recreate in that order, so they get what is left.
//
// The numbers are fractions of a thousand so integer arithmetic gets them
// exactly right and the total is visibly 1000.
var cacheShares = []struct {
	name  string
	share int64
}{
	{"speech-cache", 500},
	{"asset-cache", 200},
	{"podcast-cache", 150},
	{"page-cache", 100},
	{"digest-cache", 50},
}

// DefaultCacheBudgetMB is the total the five caches share.
//
// One gigabyte. Chosen against the smallest droplet this is expected to run on
// rather than against what the caches would like: the development box had
// reached 318 MB with nobody watching, and a ceiling three times that is
// generous while still being a number that fits beside a database and its
// backups on a 25 GB volume.
const DefaultCacheBudgetMB = 1024

// sweepCaches holds the five on-disk caches to their share of the budget.
//
// Runs on the poll cycle beside the retention sweeps, for the reason they do: a
// timer of its own is a second thing that can stop without anybody noticing.
//
// Every failure is logged and swallowed. A poll cycle that aborted because a
// cache file could not be deleted would stop the reader from getting articles
// over housekeeping, which is the wrong trade in both directions.
func (a *App) sweepCaches(ctx context.Context) {
	budgetMB := int64(a.cfg.CacheBudgetMB)
	if budgetMB == 0 {
		budgetMB = DefaultCacheBudgetMB
	}
	if budgetMB < 0 {
		// The operator's way of saying "I will manage this myself". Said out
		// loud once per cycle would be noise; it is in the boot posture instead.
		return
	}
	dataDir := filepath.Dir(a.cfg.DBPath)
	total := budgetMB << 20

	for _, c := range cacheShares {
		res, err := diskcache.Sweep(ctx, diskcache.Budget{
			Name:     c.name,
			Dir:      filepath.Join(dataDir, c.name),
			MaxBytes: total * c.share / 1000,
		})
		if err != nil {
			a.log.WarnContext(ctx, "sweeping a disk cache", "cache", c.name, "err", err)
			continue
		}
		if res.Removed > 0 {
			a.log.Info("evicted from a disk cache", "cache", c.name,
				"removed", res.Removed,
				"before_mb", res.Before>>20, "after_mb", res.After>>20)
		}
	}
}
