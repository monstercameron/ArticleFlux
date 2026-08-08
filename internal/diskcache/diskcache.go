// Package diskcache bounds the total size of an on-disk cache directory.
//
// # The gap this closes
//
// Five caches live beside the database — `speech-cache`, `asset-cache`,
// `page-cache`, `digest-cache`, `podcast-cache` — and every one of them had a
// per-ITEM ceiling and no total. `assetproxy.DefaultMaxBytes` refuses an eight-
// megabyte image; nothing refuses the eight thousandth eight-megabyte image. On
// the development box those five had reached 318 MB unattended, 179 of it
// speech, and in production they share `/var/lib/articleflux` with the SQLite
// database.
//
// That last fact is the whole reason this is not a tidiness feature. A cache
// with no ceiling is a slow leak pointed at the volume the database writes to,
// and SQLite meeting a full disk is not a cache miss — it is `SQLITE_FULL` on
// every write while every read keeps working, which is the failure mode
// `App.diskReady` exists to make visible and this exists to prevent.
//
// # Why a sweeper over the directory rather than accounting inside each cache
//
// Each cache would have to grow a size ledger, keep it correct across a crash,
// and agree with the filesystem after somebody deletes a file by hand. Five
// implementations of that is five chances to be subtly wrong about a number
// nobody checks. Walking the directory is O(files) on a timer that already
// exists, needs no state, cannot disagree with reality, and works on a cache
// this package has never heard of — which is what makes it correct for the two
// caches `internal/smart` creates by name rather than through a constructor.
//
// # Eviction is by modification time, oldest first
//
// Not by size, and not by cost to recreate. Recency is the only signal a bare
// directory carries, and it is the right one here: these caches key on content
// that does not change, so the file nobody has touched in a month is the one
// nobody is going to ask for. Cost WOULD be the better signal — a synthesised
// audio file was paid for and a proxied image was not — and that is exactly why
// the budgets differ per cache rather than being traded off inside one sweep.
package diskcache

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Budget is one cache and the size it may reach.
type Budget struct {
	// Name is what the log calls it. The directory's base name, ordinarily.
	Name string
	// Dir is the cache root. A directory that does not exist is not an error —
	// a feature nobody has used has no cache — and is reported as empty.
	Dir string
	// MaxBytes is the ceiling. Zero or negative disables the sweep entirely,
	// which is how an operator says "I will manage this myself".
	MaxBytes int64
}

// Result reports one sweep.
type Result struct {
	Name string
	// Before and After are the directory's total size either side of the sweep.
	Before, After int64
	// Removed counts files deleted.
	Removed int
}

// lowWaterFraction is how far below the ceiling a sweep evicts to.
//
// Not to the ceiling exactly. A sweep that stops the instant it is under budget
// leaves the directory sitting on the line, so the next write puts it over and
// the next sweep runs again — a delete per file forever, and the oldest entries
// churning at whatever the poll interval is. Evicting to 85% buys a margin the
// cache can fill normally before anybody has to think about it again.
const lowWaterFraction = 0.85

// Sweep deletes the oldest files until the directory is under its budget.
//
// A budget of zero returns immediately having read nothing, which matters: the
// walk is the expensive part, and "unbounded" must not cost a directory scan
// every poll cycle to discover that it is unbounded.
//
// Errors deleting individual files are swallowed on purpose. On Windows a file
// another goroutine has open cannot be removed, and on any platform a cache
// entry that vanished between the walk and the delete is the ordinary race
// rather than a fault. Either way the next sweep tries again, and failing the
// whole housekeeping pass over one locked file would mean the directory grows
// forever because one reader was slow.
func Sweep(ctx context.Context, b Budget) (Result, error) {
	res := Result{Name: b.Name}
	if b.MaxBytes <= 0 || b.Dir == "" {
		return res, nil
	}

	type entry struct {
		path string
		size int64
		mod  int64
	}
	var entries []entry
	var total int64

	err := filepath.WalkDir(b.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subdirectory is skipped rather than fatal: the rest
			// of the cache is still worth bounding.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		total += info.Size()
		entries = append(entries, entry{path: path, size: info.Size(), mod: info.ModTime().UnixNano()})
		return nil
	})
	if os.IsNotExist(err) {
		// No directory means no cache, which is the correct state for a feature
		// nobody has used.
		return res, nil
	}
	if err != nil {
		return res, err
	}

	res.Before, res.After = total, total
	if total <= b.MaxBytes {
		return res, nil
	}

	target := int64(float64(b.MaxBytes) * lowWaterFraction)
	// Oldest first. A stable sort so two files written in the same nanosecond —
	// which happens, on a filesystem with coarse timestamps — evict in a
	// deterministic order rather than a different one each pass.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].mod < entries[j].mod })

	for _, e := range entries {
		if res.After <= target {
			break
		}
		if ctx.Err() != nil {
			break
		}
		if err := os.Remove(e.path); err != nil {
			continue
		}
		res.After -= e.size
		res.Removed++
	}
	return res, nil
}

// Usage reports a directory's total size in bytes.
//
// Separate from Sweep because the readiness probe and the operator's "what is
// on this disk" question both want the number without the eviction — and
// because a caller that only wants to REPORT must not be able to delete by
// calling the wrong function.
func Usage(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return total, err
}
