package obs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Spill is the ring's copy on disk, so a restart does not erase what happened
// before it.
//
// # The failure this exists for
//
// The ring is bounded by count and lives in memory, which is the right design
// for "what just happened" and has one consequence that gets worse the more it
// matters: a process that CRASHES takes the evidence of its crash with it. A
// crash loop is the worst version — every restart destroys the record of the
// previous failure, so the one question an operator has ("why does it keep
// dying?") is the one question the log can never answer. Raising the log level
// does not help, because doing so requires a restart.
//
// # Why a flat file rather than the database
//
// The thing being diagnosed is frequently the database: locked, corrupt, out of
// disk, mid-migration. A log that needs a working database to record "the
// database is not working" is not a log. This needs nothing but a file
// descriptor.
//
// # Bounded the same way the ring is
//
// Two files, rotated by size. When the live file passes maxBytes it becomes
// `.1` and a fresh one starts, so the footprint is at most twice maxBytes and a
// rotation never leaves zero history — which a single truncating file would, at
// exactly the wrong moment.
//
// # Written through, not buffered
//
// Each record is written and flushed as it arrives. A buffer would be faster
// and would lose the last few lines in a crash, which are the only lines that
// matter here. The volume this justifies is small: the ring's own sizing note
// puts five hundred records at roughly an hour of a busy poller.
type Spill struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	f       *os.File
	size    int64
	// broken records that writing has failed, so a disk that has gone away
	// costs one log line rather than one per record forever after.
	broken bool
	// closed records that shutdown has happened, and unlike broken it is
	// permanent.
	//
	// Prune reopens the file, deliberately — it rewrites the live generation and
	// has to hand back a working handle. Shutdown is concurrent with the poll
	// cycle that calls it, so a sweep already in flight when Close runs would
	// reopen the file AFTER the process had finished with it and leave the
	// descriptor open for the life of the program. On Windows that is a lock
	// nobody can remove the file past, which is how this was found: a test's
	// TempDir cleanup failing on `poller.db.log.jsonl`.
	closed bool
	// onError reports the first failure. Held rather than logged directly
	// because this type is written to FROM the log handler, and logging from
	// inside a log handler is how a disk error becomes a stack overflow.
	onError func(error)
}

// spillRecord is the on-disk form.
//
// JSON with short, stable keys rather than the text handler's format: this is
// read back by a program, and `Attrs` is already a rendered string by the time
// the ring holds it, so nothing is gained by re-deriving structure here.
type spillRecord struct {
	T time.Time  `json:"t"`
	L slog.Level `json:"l"`
	M string     `json:"m"`
	A string     `json:"a,omitempty"`
}

// DefaultSpillBytes is the size at which the live file rotates.
//
// One megabyte holds several thousand records — far more than the ring's five
// hundred — so the constraint that actually binds is the ring's count, and this
// only bounds the disk. Two megabytes total beside a database that holds years
// of articles is not a number anybody needs to tune.
const DefaultSpillBytes = 1 << 20

// OpenSpill opens or creates the spill file.
//
// 0600 for the same reason the encryption key next to the database is: these
// lines carry feed URLs, usernames and error text, and on a self-hosted box the
// file mode IS the access control.
func OpenSpill(path string, maxSize int64, onError func(error)) (*Spill, error) {
	if maxSize <= 0 {
		maxSize = DefaultSpillBytes
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("obs: open spill: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("obs: stat spill: %w", err)
	}
	if onError == nil {
		onError = func(error) {}
	}
	return &Spill{path: path, maxSize: maxSize, f: f, size: info.Size(), onError: onError}, nil
}

// Write appends one record, rotating first if the file is full.
//
// Errors are reported once and then swallowed. A log sink that can fail the
// program is worse than one that stops working: the process is still serving
// readers, and taking it down because a disk filled would turn a diagnostic
// feature into an outage.
func (s *Spill) Write(rec Record) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken || s.closed || s.f == nil {
		return
	}

	line, err := json.Marshal(spillRecord{T: rec.Time, L: rec.Level, M: rec.Message, A: rec.Attrs})
	if err != nil {
		return // a record that will not marshal is not worth breaking the sink for
	}
	line = append(line, '\n')

	if s.size+int64(len(line)) > s.maxSize {
		if err := s.rotate(); err != nil {
			s.fail(err)
			return
		}
	}
	n, err := s.f.Write(line)
	s.size += int64(n)
	if err != nil {
		s.fail(err)
	}
}

func (s *Spill) fail(err error) {
	s.broken = true
	s.onError(err)
}

// rotate moves the live file aside and starts a new one. Caller holds the lock.
func (s *Spill) rotate() error {
	if err := s.f.Close(); err != nil {
		return err
	}
	// Rename over any existing previous file. Two generations, so a rotation
	// never leaves the process with no history at all.
	if err := os.Rename(s.path, s.path+".1"); err != nil {
		// Reopen so a failed rotation does not also lose the live file.
		f, oerr := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if oerr == nil {
			s.f = f
		}
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	s.f, s.size = f, 0
	return nil
}

// Prune drops every record older than cutoff, from both generations.
//
// # Why a size bound was not enough
//
// The rotation above bounds this file to two megabytes and says nothing about
// AGE, which is the wrong axis for what is actually in it. These lines carry
// usernames, client addresses, feed URLs and the text of authentication
// failures — the same material `login_attempts` and `audit_log` hold, and those
// have windows (internal/retention/security.go, §7.9). A quiet instance writes
// perhaps a few hundred records a week, so "two megabytes" is a retention
// policy of somewhere between one year and never, arrived at by accident and
// varying with how busy the server is. That is not a policy anybody chose.
//
// So the same window governs both, applied here on the same housekeeping
// timer. A rewrite rather than a truncation, because the file is chronological
// and the expired records are the ones at the FRONT.
//
// # Why rewriting is affordable
//
// Two files of at most a megabyte each, on a sweep that runs every few minutes
// and only writes when something actually expired. The alternative — dropping a
// whole generation once its newest record aged out — never expires anything on
// a live server, because the live file always has a fresh record at the end.
//
// Returns how many records were dropped.
func (s *Spill) Prune(cutoff time.Time) (int, error) {
	if s == nil {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Nothing after shutdown. The reopen below would otherwise resurrect a
	// descriptor the process has already finished with — see `closed`.
	if s.closed {
		return 0, nil
	}

	dropped, err := pruneSpillFile(s.path+".1", cutoff)
	if err != nil {
		return dropped, err
	}

	// The live file is rewritten with the handle closed, so the append offset
	// cannot be left pointing past the end of a file that just got shorter —
	// which would pad it with zero bytes and cost the very history this is
	// bounding.
	if s.f != nil {
		if err := s.f.Close(); err != nil {
			s.f = nil
			s.fail(err)
			return dropped, err
		}
		s.f = nil
	}
	n, perr := pruneSpillFile(s.path, cutoff)
	dropped += n

	f, oerr := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if oerr != nil {
		// Reported once and then silent, exactly as a failed write is: a log
		// sink that cannot reopen must not take the server down with it.
		s.fail(oerr)
		return dropped, oerr
	}
	s.f = f
	s.size = 0
	if info, serr := f.Stat(); serr == nil {
		s.size = info.Size()
	}
	// A successful reopen clears the broken flag. The disk answered.
	s.broken = false
	return dropped, perr
}

// pruneSpillFile rewrites one generation without its expired records.
//
// A missing file is not an error: the previous generation does not exist until
// the first rotation, and a file that pruned to nothing was removed by an
// earlier call.
func pruneSpillFile(path string, cutoff time.Time) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil
	}

	var kept [][]byte
	var dropped int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var r spillRecord
		if err := json.Unmarshal(line, &r); err != nil {
			// A line that will not parse is dropped rather than kept. Load
			// already skips it, so keeping it preserves nothing readable — and a
			// record whose timestamp cannot be read is a record this function
			// cannot promise anything about.
			dropped++
			continue
		}
		if r.T.Before(cutoff) {
			dropped++
			continue
		}
		kept = append(kept, append(append([]byte(nil), line...), '\n'))
	}
	scanErr := sc.Err()
	if cerr := f.Close(); cerr != nil && scanErr == nil {
		scanErr = cerr
	}
	if scanErr != nil {
		// Nothing is rewritten from a partial read. Rewriting what was scanned
		// would delete the tail of the file because it could not be READ, which
		// is the one outcome worse than keeping expired lines for another cycle.
		return 0, scanErr
	}
	if dropped == 0 {
		return 0, nil
	}
	if len(kept) == 0 {
		return dropped, os.Remove(path)
	}

	// Temp-and-rename rather than truncate-and-write: a crash midway through the
	// second leaves a file that is neither the old history nor the new one, and
	// this runs on a server whose crashes are exactly what the file is for.
	tmp := path + ".prune"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	w := bufio.NewWriter(out)
	for _, line := range kept {
		if _, err := w.Write(line); err != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
			return 0, err
		}
	}
	if err := w.Flush(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return dropped, nil
}

// Load reads back the most recent records, oldest first, at most limit.
//
// Oldest first because the caller replays them into the ring in arrival order;
// Recent then reverses on the way out, exactly as it does for live records.
//
// A malformed line is skipped rather than failing the load. The file is
// append-only and written a whole line at a time, but a crash mid-write can
// still leave a partial final line — and refusing to restore anything because
// the last line is short would discard the history for precisely the crash this
// was written to survive.
func (s *Spill) Load(limit int) []Record {
	if s == nil || limit <= 0 {
		return nil
	}
	s.mu.Lock()
	path := s.path
	s.mu.Unlock()

	// Previous generation first, then the live one: together they are in
	// arrival order.
	var all []Record
	for _, p := range []string{path + ".1", path} {
		all = append(all, readSpillFile(p, limit)...)
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all
}

func readSpillFile(path string, limit int) []Record {
	f, err := os.Open(path)
	if err != nil {
		return nil // a missing previous generation is the normal first-run case
	}
	defer func() { _ = f.Close() }()

	// A ring while scanning, so a file far larger than the limit costs the
	// limit in memory rather than the file.
	buf := make([]Record, 0, limit)
	sc := bufio.NewScanner(f)
	// Lines are short, but an attribute string can be long; the default 64KiB
	// token limit would silently stop the scan at the first one that exceeded
	// it, which reads as "the log ends here".
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var r spillRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		if len(buf) == limit {
			copy(buf, buf[1:])
			buf = buf[:limit-1]
		}
		buf = append(buf, Record{Time: r.T, Level: r.L, Message: r.M, Attrs: r.A})
	}
	// A scan error means the file was truncated or unreadable partway. What was
	// read is still valid and is returned — this runs at boot to recover the
	// history of a process that may have died mid-write, so "some of it" is the
	// expected good outcome and discarding it would defeat the feature.
	//
	// Checked rather than ignored so the reason is on the record: the failure
	// mode of not checking is a scan that stops at a long line and reports a
	// complete-looking history that simply ends.
	if err := sc.Err(); err != nil && len(buf) > 0 {
		buf = append(buf, Record{
			Time:    buf[len(buf)-1].Time,
			Level:   slog.LevelWarn,
			Message: "the restored log ends here: the spill file could not be read to the end",
			Attrs:   "err=" + err.Error(),
		})
	}
	return buf
}

// Close flushes and closes the file.
func (s *Spill) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}
