package host

import "errors"

// ErrLocked means another copy of the desktop app already owns this data
// directory.
var ErrLocked = errors.New("another ArticleFlux window is already using this data folder")

// Lock is an exclusive claim on a data directory, held for the life of the
// process.
//
// SQLite would happily let two processes open the same file — WAL mode is
// designed for exactly that — so this is not protecting the database's
// integrity. It protects the application's: two copies would run two pollers,
// two job pools and two derivation schedules against one reading history, and
// each would believe it was the only writer of state it caches in memory.
//
// An OS-level lock rather than a PID file, because the failure a PID file has
// is the one that matters here: a crash leaves it behind, and the next launch
// either refuses forever or has to guess whether the PID was reused. The
// kernel releases this one when the process dies, however it dies.
type Lock struct {
	path    string
	release func() error
}

// AcquireLock claims dir's lock file or returns ErrLocked.
func AcquireLock(path string) (*Lock, error) {
	rel, err := acquire(path)
	if err != nil {
		return nil, err
	}
	return &Lock{path: path, release: rel}, nil
}

// Release gives the claim up. Safe to call twice.
func (l *Lock) Release() error {
	if l == nil || l.release == nil {
		return nil
	}
	rel := l.release
	l.release = nil
	return rel()
}
