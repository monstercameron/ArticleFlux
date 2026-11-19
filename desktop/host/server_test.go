package host

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeWebRoot is the smallest web root internal/app's Preflight accepts: an
// index.html and an app.wasm. The smoke test is about the shell — data folder,
// migrations, port, gate, shutdown — not the client, so it does not need the
// 24 MB bundle; ARTICLEFLUX_DESKTOP_WEB points it at a real one when wanted.
func fakeWebRoot(t *testing.T) string {
	t.Helper()
	if real := os.Getenv("ARTICLEFLUX_DESKTOP_WEB"); real != "" {
		return real
	}
	dir := t.TempDir()
	files := map[string]string{
		"index.html":   "<!doctype html><title>ArticleFlux</title><p>desktop smoke</p>",
		"app.wasm":     "\x00asm\x01\x00\x00\x00",
		"wasm_exec.js": "// stand-in",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// scratchProfile is a LOCALAPPDATA for one test, removed afterwards on a best
// effort.
//
// Not t.TempDir, for one reason: t.TempDir FAILS the test when it cannot empty
// the directory, and on Windows that happens for a cause that is not this
// code's. A file that has been closed — the database, its WAL — stays "delete
// pending" for as long as anything else still has it open, and the indexer and
// the virus scanner open every new file they see. While it is pending the
// directory reports itself not empty, and a test that booted, served and shut
// down correctly is marked failed by its own tidying up. Seen once in eight
// runs on a loaded machine, which on a hosted runner is a gate that goes red
// for no reason a few times a month.
//
// So: retry for a few seconds, and if the directory is still there, say so and
// leave it. A leftover temp folder is the operating system's to collect; a
// false failure is somebody's afternoon.
func scratchProfile(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "articleflux-desktop-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var err error
		for i := 0; i < 50; i++ {
			if err = os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Logf("left %s behind: %v", dir, err)
	})
	return dir
}

// TestDesktopSmoke is the headless version of launching the app: everything
// the window would do except draw. It runs against a temporary LOCALAPPDATA, so
// it can never touch the real profile.
func TestDesktopSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("opens and migrates a real database")
	}
	t.Setenv("LOCALAPPDATA", scratchProfile(t))
	dir, err := ResolveDataDir("")
	if err != nil {
		t.Fatal(err)
	}
	paths := PathsIn(dir)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireLock(paths.Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	web := fakeWebRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	srv, err := Start(ctx, Options{Paths: paths, WebRoot: web, Log: quietLog(), PollInterval: -1})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = srv.Close()
		}
	}()

	// First run created the database in the profile, migrated.
	if _, err := os.Stat(paths.DB); err != nil {
		t.Fatalf("no database at %s: %v", paths.DB, err)
	}
	// And the sidecars internal/app writes beside it, in the same folder.
	if _, err := os.Stat(filepath.Join(dir, "secrets.key")); err != nil {
		t.Errorf("secrets.key was not written beside the database: %v", err)
	}

	if !strings.HasPrefix(srv.Origin(), "http://127.0.0.1:") || strings.HasSuffix(srv.Origin(), ":9000") {
		t.Fatalf("origin = %q", srv.Origin())
	}

	// A second copy is refused while this one runs.
	if _, err := AcquireLock(paths.Lock); !errors.Is(err, ErrLocked) {
		t.Errorf("second lock: %v, want ErrLocked", err)
	}

	// The window's path: launch link → cookie → the reader.
	jar, _ := cookiejar.New(nil)
	window := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	body := get(t, window, srv.LaunchURL(), 200)
	if !strings.Contains(body, "<title>") {
		t.Errorf("/ after launch did not serve the client shell: %.120q", body)
	}
	get(t, window, srv.Origin()+"/healthz", 200)
	get(t, window, srv.Origin()+"/readyz", 200)
	get(t, window, srv.Origin()+"/app.wasm", 200)

	// Anyone else — a browser tab that guessed the port — gets nothing.
	stranger := &http.Client{Timeout: 10 * time.Second}
	get(t, stranger, srv.Origin()+"/", 403)
	get(t, stranger, srv.Origin()+"/healthz", 403)
	get(t, window, srv.Origin()+"/debug/reset-state", 404)

	// Desktop preferences live in the database, not beside it.
	want := Bounds{X: 40, Y: 50, Width: 1300, Height: 820}
	if err := srv.SaveWindow(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, ok := srv.Window(ctx); !ok || got != want {
		t.Errorf("window = %+v, %v; want %+v", got, ok, want)
	}

	// The OPML menu items' half that is not a dialog.
	out := filepath.Join(t.TempDir(), "subs.opml")
	if _, err := srv.ExportOPML(ctx, out); err != nil {
		t.Fatalf("export: %v", err)
	}
	if b, _ := os.ReadFile(out); !strings.Contains(string(b), "<opml") {
		t.Errorf("export wrote %.80q", b)
	}
	// Import is only exercised on files it must refuse: a real subscription
	// would make the import fetch the feed, and this test does not reach the
	// internet. The parser and the subscribe ladder have their own tests.
	notOPML := filepath.Join(t.TempDir(), "notes.txt")
	_ = os.WriteFile(notOPML, []byte("not a subscription list"), 0o644)
	if _, err := srv.ImportOPML(ctx, notOPML); err == nil || !strings.Contains(err.Error(), "OPML") {
		t.Errorf("importing a text file: err = %v, want an OPML refusal", err)
	}
	huge := filepath.Join(t.TempDir(), "huge.opml")
	_ = os.WriteFile(huge, make([]byte, 3<<20), 0o644)
	if _, err := srv.ImportOPML(ctx, huge); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("importing 3 MB: err = %v, want the RPC's size refusal", err)
	}

	origin := srv.Origin()
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed = true

	// A clean quit checkpoints the WAL into the database and removes it.
	if _, err := os.Stat(paths.DB + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the WAL is still there after a clean shutdown (stat err = %v)", err)
	}
	if _, err := stranger.Get(origin + "/healthz"); err == nil {
		t.Error("still answering after Close")
	}

	// The second launch reopens the same database on the same origin, so the
	// client's per-device storage survives a restart.
	srv2, err := Start(ctx, Options{Paths: paths, WebRoot: web, Log: quietLog(), PollInterval: -1})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	defer srv2.Close()
	if srv2.Origin() != origin {
		t.Errorf("second launch origin = %q, want the remembered %q", srv2.Origin(), origin)
	}
	if got, ok := srv2.Window(ctx); !ok || got != want {
		t.Errorf("window after restart = %+v, %v", got, ok)
	}
}

func TestAMissingClientIsAStartupErrorNotACrash(t *testing.T) {
	if testing.Short() {
		t.Skip("opens and migrates a real database")
	}
	paths := PathsIn(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := Start(ctx, Options{Paths: paths, WebRoot: t.TempDir(), Log: quietLog(), PollInterval: -1})
	var se *StartError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *StartError the shell can show in a dialog", err)
	}
	if !strings.Contains(se.Err.Error(), "index.html") {
		t.Errorf("message does not say what is missing: %v", se.Err)
	}
	// And it let go of the database on the way out: the WAL is not left open.
	if _, err := os.Stat(paths.DB + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("WAL left behind by a failed start (stat err = %v)", err)
	}
}

func get(t *testing.T, c *http.Client, url string, want int) string {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Errorf("GET %s = %d, want %d: %.200s", url, resp.StatusCode, want, b)
	}
	return string(b)
}
