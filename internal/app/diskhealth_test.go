package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/diskspace"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/transport/grpcsrv"
)

// Readiness has to see a disk it cannot write to.
//
// # What these can and cannot assert
//
// Nothing here fills a disk. What is testable is the SHAPE of the check: that
// a writable directory passes, that an unwritable one fails, that the verdict
// is cached, and that `/readyz` asks the question at all while the tunnel gate
// deliberately does not. The free-space floor is exercised by reading the real
// number back — which is a weak assertion and an honest one, because the
// alternative is a fake filesystem standing in for the one property that only
// the real filesystem has.

func diskApp(t *testing.T) *App {
	t.Helper()
	a, err := Open(t.Context(), Config{
		DBPath:       filepath.Join(t.TempDir(), "disk.db"),
		PollInterval: 0,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// The ordinary case: a data directory on a disk with room is ready.
func TestAWritableDataDirectoryIsReady(t *testing.T) {
	a := diskApp(t)
	if err := a.diskReady(t.Context()); err != nil {
		t.Errorf("diskReady = %v, want nil on a temp dir with room", err)
	}
	if err := a.fullyReady(t.Context()); err != nil {
		t.Errorf("fullyReady = %v, want nil", err)
	}
}

// And the case the whole thing exists for, in the only form a test can produce
// it: a data directory that cannot be written to.
//
// A missing directory rather than a full one. Both reach `probeWrite` as a
// failed `CreateTemp`, which is the branch under test — what varies between
// ENOSPC, EROFS and EACCES is the errno, and the check does not read it.
func TestADataDirectoryThatCannotBeWrittenToIsNotReady(t *testing.T) {
	a := diskApp(t)
	dir := filepath.Dir(a.cfg.DBPath)

	// The database is open on this file, so it is closed first — the point is
	// to take the DIRECTORY away, and on Windows an open handle prevents that.
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	// Past the cached verdict from Open, if any.
	a.disk.checked = time.Time{}

	err := a.diskReady(t.Context())
	if err == nil {
		t.Fatal("a data directory that does not exist reported ready")
	}
	if !strings.Contains(err.Error(), "not writable") && !strings.Contains(err.Error(), "free") {
		t.Errorf("err = %v; want it to name writability or space", err)
	}
}

// The verdict is cached, because /readyz is polled every two minutes AND
// consulted on every tunnel upgrade, and each probe is a create-write-fsync on
// the disk the check is worried about.
func TestTheDiskVerdictIsCached(t *testing.T) {
	a := diskApp(t)
	if err := a.diskReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := a.disk.checked

	if err := a.diskReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !a.disk.checked.Equal(first) {
		t.Error("the second call re-probed inside the TTL")
	}

	// And it does expire, or the check would answer from a snapshot forever.
	a.disk.checked = time.Now().Add(-2 * diskProbeTTL)
	if err := a.diskReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	if a.disk.checked.Equal(first) {
		t.Error("the verdict never expires; a disk that filled would stay green")
	}
}

// The split is deliberate and load-bearing, so it is asserted rather than left
// to a comment: the tunnel gate must NOT inherit the disk check.
//
// A full disk breaks writes and leaves reads working. Refusing tunnel upgrades
// then takes a degraded instance and makes it unusable — over a condition a
// restart cannot fix. The monitor needs to know; the reader mid-article does
// not need to be thrown off.
func TestTheTunnelGateDoesNotAskAboutTheDisk(t *testing.T) {
	a := diskApp(t)
	// Poison the cached verdict. `ready` must be unaffected; `fullyReady` must
	// not be.
	a.disk.checked = time.Now()
	a.disk.lastErr = os.ErrPermission

	if err := a.ready(t.Context()); err != nil {
		t.Errorf("ready = %v; the tunnel gate must not fail on a disk verdict", err)
	}
	if err := a.fullyReady(t.Context()); err == nil {
		t.Error("fullyReady ignored the disk verdict; /readyz would stay green on a full disk")
	}
}

// probeWrite must SYNC, not just write. A buffered write to a full disk
// succeeds and the ENOSPC arrives at flush — a probe that skipped the sync
// would report a healthy disk right up to the moment the kernel got round to
// mentioning otherwise. This asserts the happy path and leaves no probe file
// behind, which is the other half of it: a probe that littered would itself
// fill the disk.
func TestProbeWriteLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	if err := probeWrite(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the probe left %d file(s) behind", len(entries))
	}
}

// The five shares add up to the whole budget. A share table that summed to less
// would silently under-use the number an operator set; one that summed to more
// would overrun the ceiling they chose, which is worse.
func TestTheCacheSharesAddUpToTheWholeBudget(t *testing.T) {
	var total int64
	for _, c := range cacheShares {
		total += c.share
	}
	if total != 1000 {
		t.Errorf("the cache shares sum to %d per mille, not 1000", total)
	}
	if len(cacheShares) != 5 {
		t.Errorf("there are %d shares for the five caches beside the database", len(cacheShares))
	}
}

// A negative budget means "do not evict", and it must not be read as a tiny
// one — which would empty every cache on the first cycle.
func TestANegativeCacheBudgetEvictsNothing(t *testing.T) {
	a := diskApp(t)
	a.cfg.CacheBudgetMB = -1
	dir := filepath.Join(filepath.Dir(a.cfg.DBPath), "speech-cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(dir, "big")
	if err := os.WriteFile(kept, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}

	a.sweepCaches(t.Context())

	if _, err := os.Stat(kept); err != nil {
		t.Errorf("a negative budget evicted a cache file: %v", err)
	}
}

// Free space is measured rather than assumed on the platforms this runs on.
// A stub that answered "plenty" would make the floor decorative.
func TestFreeSpaceIsMeasurableHere(t *testing.T) {
	n, err := diskspace.Free(t.TempDir())
	if err != nil {
		t.Skipf("no free-space implementation on this platform: %v", err)
	}
	if n == 0 {
		t.Error("Free reported zero bytes available on a temp dir the test just wrote to")
	}
}

// Refresh tokens are ON in the wiring, which is the thing SEC4 was about.
//
// Every control §7.3a specifies — rotation, reuse detection, the twelve-hour
// access lifetime, the sixty-day idle window — is downstream of one boolean at
// one call site, and all four were unreachable for as long as it was false. A
// unit test of `WithRefreshTokens` cannot see that; only the assembly can.
//
// Asserted through the SERVER's behaviour rather than by reading the flag,
// because the flag is unexported and because what matters is the response a
// client gets: a Login that comes back without a refresh half is a client that
// will be logged out in twelve hours with no way to renew, which is worse than
// the thirty-day token this replaced.
func TestTheAssembledServerIssuesRefreshTokens(t *testing.T) {
	a := diskApp(t)
	ctx := t.Context()

	if _, err := a.EnsureDevUser(ctx, "cam", "articleflux-refresh-wiring"); err != nil {
		t.Fatalf("dev user: %v", err)
	}
	srv := grpcsrv.NewAuthServer(a.repo, a.scopeFromContext, a.log, a.cfg.DevMode).
		WithRefreshTokens(true)

	res, err := srv.Login(ctx, &pb.LoginRequest{
		Username: "cam", Password: "articleflux-refresh-wiring",
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.GetRefreshToken() == "" || res.GetRefreshRecordId() == "" {
		t.Fatal("no refresh half came back; nothing in the client can renew")
	}

	// And the wiring itself, read off the registered service rather than
	// reconstructed here — the test above builds its own server, which would
	// pass even if app.go had dropped the WithRefreshTokens call.
	if !strings.Contains(authWiring(t), "WithRefreshTokens(true)") {
		t.Error("internal/app no longer opts in to refresh tokens; every control in " +
			"§7.3a SEC4 is unreachable again and nothing else in the suite would notice")
	}
}

// authWiring returns the source of app.go's AuthService registration.
//
// A source read, for the same reason internal/smart's ceiling guard is one: the
// property is an OMISSION, and an omission has no behaviour. Dropping
// `.WithRefreshTokens(true)` changes nothing any other test observes — the
// server still logs people in, the sessions just quietly go back to thirty days
// with no rotation, which is precisely the state this whole change was undoing.
func authWiring(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	i := strings.Index(src, "RegisterAuthServiceServer")
	if i < 0 {
		t.Fatal("the AuthService registration has moved; this guard cannot see it any more")
	}
	end := i + 2000
	if end > len(src) {
		end = len(src)
	}
	return src[i:end]
}

// Every package that makes an outbound request builds its client through
// netguard.
//
// # Why a source grep
//
// The property is a NEGATIVE — "nobody constructs their own http.Client" — and
// a negative has no behaviour to observe. A package that builds a bare one
// works perfectly: it fetches, it returns bytes, every test passes. What it
// loses is invisible from inside it — the egress metrics, the span, the shared
// dial and redirect policy — and the loss only shows up as an absence on a
// dashboard nobody is looking at when it happens.
//
// That is how internal/llm and internal/tts came to be the two paths that cost
// money and the two paths with no telemetry, while app.go's own comment said
// "every outbound fetch, from all seven callers". A grep is blunt and it is the
// only instrument that can see this.
func TestEveryEgressPathIsGuarded(t *testing.T) {
	// The packages that egress. Adding one means adding it here, which is the
	// point: a new outbound path should have to think about the guard once.
	paths := []string{
		"../llm", "../tts", "../assetproxy", "../discover", "../extract", "../favicon",
	}
	checked := 0
	for _, dir := range paths {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		guarded := false
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			// Comments stripped first. The doc on `paidEgressClient` argues
			// against `&http.Client{Timeout:…}` by quoting it, and a grep that
			// cannot tell an argument from an instance fails on the fix.
			src := withoutComments(string(body))
			if strings.Contains(src, "netguard.Client(") {
				guarded = true
			}
			// The one exemption, by name and with a reason. `NewWithTransport`
			// takes a RoundTripper FROM its caller — it is the seam a test uses
			// to answer without a network, and wrapping a supplied transport in
			// netguard's would defeat the only thing it is for. It has no
			// production caller, which is the property that makes it safe.
			if e.Name() == "transport.go" {
				continue
			}
			// The specific shape that went unnoticed twice. Not every
			// `&http.Client{` is wrong — a test double is fine — but this scan
			// skips _test.go files, so one here is a production client built
			// outside the guard.
			if strings.Contains(src, "&http.Client{") {
				t.Errorf("%s/%s builds its own http.Client. It will fetch perfectly and be "+
					"invisible: no egress.* metrics, no span, no shared dial or redirect "+
					"policy. Use netguard.Client — see internal/llm's paidEgressClient for "+
					"the case where the host is a constant and the guard is still worth it.",
					dir, e.Name())
			}
		}
		if !guarded {
			t.Errorf("%s never calls netguard.Client; either it stopped egressing (remove it "+
				"from this list) or it started doing so unguarded", dir)
		}
		checked++
	}
	if checked != len(paths) {
		t.Errorf("checked %d of %d packages", checked, len(paths))
	}
}

// withoutComments removes // comments so a grep for a code pattern does not
// match the prose explaining why that pattern is wrong.
//
// Line comments only. Block comments are not used in this repository's Go, and
// a half-correct stripper that handled them would be more code than the thing
// it serves.
func withoutComments(src string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// A stalled write probe is reported as a failure rather than waited out.
//
// # Why this is the case that matters
//
// probeWrite ends in an fsync with no timeout, and diskReady runs it holding
// a.disk.mu. On a working disk that is microseconds; on a stalled one — a
// detached volume, a saturated device, an NFS mount that stopped answering — it
// blocks indefinitely and every caller queues behind the lock.
//
// That inverts /readyz. app.go states its purpose plainly: it is the ALERTING
// path, "the thing that notices /readyz is the alerting path, which can wake
// somebody up". An endpoint whose whole job is to report "this instance cannot
// write" must not stop answering when the disk stops answering — a monitor then
// sees a timeout, which says less than a refusal that names the reason.
//
// The stall is simulated at the helper rather than by wedging a real
// filesystem, which no test can do portably: what is being pinned is that the
// deadline is honoured and reported, not that fsync can hang.
func TestAStalledWriteProbeIsReportedNotAwaited(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	orig := probeWriteFn
	probeWriteFn = func(string) error { <-release; return nil }
	t.Cleanup(func() { probeWriteFn = orig })

	start := time.Now()
	err := probeWriteBounded(context.Background(), t.TempDir())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a stalled probe reported the disk writable; /readyz would say " +
			"ready while nothing can be written")
	}
	if !strings.Contains(err.Error(), "stalled") {
		t.Errorf("the failure does not name the condition: %v", err)
	}
	// It must return on the deadline rather than waiting the probe out. Generous
	// upper bound so a slow CI box does not fail this for the wrong reason.
	if elapsed > diskProbeTimeout+5*time.Second {
		t.Errorf("returned after %v; the deadline is %v", elapsed, diskProbeTimeout)
	}
}

// And the real helper still answers normally on a working directory, so the
// bound did not turn every healthy probe into an error.
func TestABoundedWriteProbeSucceedsOnAWritableDir(t *testing.T) {
	if err := probeWriteBounded(context.Background(), t.TempDir()); err != nil {
		t.Errorf("a writable directory reported %v", err)
	}
}

// A cancelled context ends the wait too, so a caller that gave up is not held
// by a probe it no longer wants.
func TestABoundedWriteProbeHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A real directory, so any failure comes from the cancellation rather than
	// from the write. The probe may still win the race on a fast disk, which is
	// fine — what must not happen is a hang.
	err := probeWriteBounded(ctx, t.TempDir())
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want nil or context.Canceled", err)
	}
}
