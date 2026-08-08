// Package diskspace answers how much room is left where the database lives.
//
// # Why this exists at all
//
// `/healthz` says the process is up and `/readyz` said the schema version was
// readable. Neither of those changes when the volume fills, and a full volume
// is the one failure this application is actually engineered towards: five
// unbounded caches used to point at the same directory as the SQLite file (see
// `internal/diskcache`). What a full disk produces is `SQLITE_FULL` on every
// write while every read keeps working — so the reader loads, articles appear,
// nothing can be marked read, both probes are green and the watchdog is silent.
// A restart does not help and the watchdog will keep trying anyway.
//
// Free space is therefore a readiness input, not a metric. It has to be a
// number the process can compare against a floor BEFORE the writes start
// failing, because after they start failing the only remaining signal is a
// reader complaining.
//
// # Why the syscall and not just a write probe
//
// `App.diskReady` does both, and they answer different questions. A write probe
// is the ground truth — it catches a read-only remount and a wrong owner as
// well as a full disk — but it only fires once there is no room at all, which
// is too late to be a warning. This gives the headroom, so the instance can go
// unready with a gigabyte left rather than with none.
//
// # Why stdlib syscalls rather than golang.org/x/sys
//
// x/sys is in the module graph as an indirect dependency and promoting it to a
// direct one is a decision `CONTRIBUTING.md` says gets asked about. Both
// platforms this ships on expose what is needed from `syscall` alone, in about
// fifteen lines each, so the question does not have to be asked.
package diskspace

import "errors"

// ErrUnsupported means this platform has no implementation here.
//
// Callers treat it as "no information" rather than as a failure: a readiness
// check that refused to be ready on an operating system nobody deploys to
// would be a worse bug than the one being prevented.
var ErrUnsupported = errors.New("diskspace: not supported on this platform")

// Free reports the bytes available to this process on the filesystem holding
// path.
//
// Available to THIS process, not free on the device. On Unix those differ by
// the reserved-blocks percentage — five percent by default on ext4, which on a
// small droplet volume is gigabytes — and the number that matters is the one an
// unprivileged writer can actually use.
func Free(path string) (uint64, error) { return free(path) }
