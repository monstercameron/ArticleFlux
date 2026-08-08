package app

import (
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
