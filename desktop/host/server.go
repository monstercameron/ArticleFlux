package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/app"
	"github.com/monstercameron/ArticleFlux/internal/buildver"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/transport/grpcsrv"
)

// Options configure Start.
type Options struct {
	// Paths is the data directory, already resolved (ResolveDataDir, PathsIn).
	Paths Paths
	// WebRoot is the assembled client: index.html, app.wasm(.gz/.br),
	// wasm_exec.js, fonts, icons. See docs/DESKTOP.md for why it ships beside
	// the executable rather than inside it.
	WebRoot string
	Log     *slog.Logger
	// PollInterval is how often feeds are polled while the window is open.
	// Zero means the server's own default of fifteen minutes; negative turns
	// the poller off (the smoke test, which must not reach the internet).
	PollInterval time.Duration
}

// Server is the reader, running in this process, behind the Gate.
type Server struct {
	app    *app.App
	scope  store.Scope
	http   *http.Server
	ln     net.Listener
	origin string
	token  string
	log    *slog.Logger
	stop   context.CancelFunc
	served chan error

	closeOnce sync.Once
	closeErr  error
}

// StartError is a failure the person can do something about, worded for a
// dialog rather than a log.
type StartError struct {
	Title string
	Err   error
}

func (e *StartError) Error() string { return e.Title + ": " + e.Err.Error() }
func (e *StartError) Unwrap() error { return e.Err }

// Start opens the database, migrates it, and starts serving.
//
// The order is serve's (cmd/articleflux), minus the parts that belong to a
// network server — flags, .env, the proxy and profiling gates — and plus the
// two the desktop needs: the remembered port, and the Gate.
//
// # Why the tunnel's origin allowlist is not set
//
// app.Config.AllowedOrigins is read once, inside app.Open, and the port — so
// the origin — is not known until after it: the remembered port is a row in
// the database app.Open is what opens. Left empty, the tunnel falls back to the
// WebSocket library's same-origin rule (Origin's host must equal Host), and the
// Gate pins Host to 127.0.0.1:<port> and refuses any other Origin on an
// upgrade, so the two together accept exactly the one origin AllowedOrigins
// would have named. No check is turned off to get there.
func Start(ctx context.Context, opt Options) (*Server, error) {
	if opt.Log == nil {
		opt.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	poll := opt.PollInterval
	switch {
	case poll == 0:
		poll = 15 * time.Minute
	case poll < 0:
		poll = 0
	}

	a, err := app.Open(ctx, app.Config{
		DBPath:  opt.Paths.DB,
		WebRoot: opt.WebRoot,
		Log:     opt.Log,
		Version: buildver.Version,
		Commit:  buildver.Commit,
		// The one-user mode. A person's own reader on their own laptop has no
		// login screen to get past; Gate is what makes that safe here.
		DevMode: true,
		// NOT the dev server's AllowPrivateFeeds. That relaxes the SSRF guard so a
		// developer can subscribe to a fixture on localhost; on a desktop it would
		// let a feed URL reach the person's router admin page.
		AllowPrivateFeeds: false,
		PollInterval:      poll,
		// serve's defaults (§10.1a, §10.1b): both on, the live view off.
		ProxyImages: true,
		ProxyPages:  true,
	})
	if err != nil {
		return nil, &StartError{Title: "ArticleFlux could not open its database", Err: err}
	}
	s := &Server{app: a, log: a.Log(), served: make(chan error, 1)}
	fail := func(title string, err error) (*Server, error) {
		_ = a.Close()
		return nil, &StartError{Title: title, Err: err}
	}

	sc, err := a.EnsureDevUser(ctx, localUsername(), randomPassword())
	if err != nil {
		return fail("ArticleFlux could not create the local account", err)
	}
	s.scope = sc
	if err := a.Preflight(ctx); err != nil {
		return fail("ArticleFlux is not installed correctly", err)
	}

	prefs, err := a.Repo().GetPrefs(ctx, sc)
	if err != nil {
		return fail("ArticleFlux could not read its settings", err)
	}
	ln, err := Listen(ParsePort(prefs[PrefPort]))
	if err != nil {
		return fail("ArticleFlux could not open a local port", err)
	}
	s.ln = ln
	port := Port(ln)
	if strconv.Itoa(port) != prefs[PrefPort] {
		// Remembered so the next launch has the same origin; see Listen.
		if err := a.Repo().SetPrefs(ctx, sc, store.Prefs{PrefPort: strconv.Itoa(port)}); err != nil {
			s.log.Warn("could not remember the port; the next launch will use a new one", "err", err)
		}
	}
	s.origin = OriginFor(port)
	if s.token, err = NewToken(); err != nil {
		_ = ln.Close()
		return fail("ArticleFlux could not start", err)
	}

	// The workers' lifetime is the server's, not the caller's: Start returns
	// long before the window closes.
	wctx, stop := context.WithCancel(context.Background())
	s.stop = stop
	a.StartWorkers(wctx)
	a.DeriveDue(wctx)
	a.StartPoller(wctx)

	s.http = &http.Server{
		Handler:           &Gate{Origin: s.origin, Host: HostFor(port), Token: s.token, Next: a.Handler()},
		ReadHeaderTimeout: 10 * time.Second,
		// No Read/WriteTimeout, for serve's reasons: the gRPC tunnel is a
		// hijacked, long-lived WebSocket and a socket deadline would sever it.
		IdleTimeout: 2 * time.Minute,
		ErrorLog:    slog.NewLogLogger(s.log.Handler(), slog.LevelError),
	}
	go func() { s.served <- s.http.Serve(ln) }()
	s.log.Info("desktop: serving", "origin", s.origin, "db", opt.Paths.DB)
	return s, nil
}

// Origin is the page origin, e.g. http://127.0.0.1:51234.
func (s *Server) Origin() string { return s.origin }

// LaunchURL is the address the window opens first. It carries the token, so it
// is handed to the window and to nothing else.
func (s *Server) LaunchURL() string { return s.origin + LaunchPath + "?t=" + s.token }

// Log is the app's logger, which also feeds the settings screen's log view.
func (s *Server) Log() *slog.Logger { return s.log }

// Window reads the remembered window position.
func (s *Server) Window(ctx context.Context) (Bounds, bool) {
	p, err := s.app.Repo().GetPrefs(ctx, s.scope)
	if err != nil {
		return Bounds{}, false
	}
	return ParseBounds(p[PrefWindow])
}

// SaveWindow remembers the window position.
func (s *Server) SaveWindow(ctx context.Context, b Bounds) error {
	return s.app.Repo().SetPrefs(ctx, s.scope, store.Prefs{PrefWindow: b.String()})
}

// ImportOPML subscribes the local account to an OPML file on disk.
//
// The same call the ImportOpml RPC makes (internal/transport/grpcsrv/opml.go,
// which is only the size check and the error mapping around it), with the same
// size limit — so a file the Settings screen would refuse is refused here too.
func (s *Server) ImportOPML(ctx context.Context, path string) (reader.ImportResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return reader.ImportResult{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, grpcsrv.MaxImportBytes+1))
	if err != nil {
		return reader.ImportResult{}, err
	}
	if len(data) > grpcsrv.MaxImportBytes {
		return reader.ImportResult{}, errors.New("that file is too large to import — a subscription list is normally a few dozen kilobytes")
	}
	return s.app.Service().ImportOPML(ctx, s.scope, data)
}

// ExportOPML writes the local account's subscriptions to path, returning the
// number of feeds. Written to a temporary sibling and renamed, so a failure
// part-way leaves the previous export intact rather than half of a new one.
func (s *Server) ExportOPML(ctx context.Context, path string) (int, error) {
	data, n, err := s.app.Service().ExportOPML(ctx, s.scope)
	if err != nil {
		return 0, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return n, nil
}

// Close stops serving and closes the database.
//
// The order is serve's on SIGTERM: stop accepting, drain, then app.Close —
// which stops the poller and the job pool, drains gRPC (the tunnel is a
// hijacked connection that http.Server.Shutdown does not wait for), and closes
// both SQLite pools. Closing the last connection is what checkpoints the WAL
// into the database and deletes the -wal file, so a clean quit leaves one file
// a person can copy.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var errs []error
		if err := s.http.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("http shutdown: %w", err))
		}
		if err := <-s.served; err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
		if err := s.app.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing the database: %w", err))
		}
		s.closeErr = errors.Join(errs...)
	})
	return s.closeErr
}

// localUsername names the one account. The Windows sign-in name, because it is
// what the person calls themselves on this machine; "reader" when it cannot be
// read or would not survive as a username.
func localUsername() string {
	u, err := user.Current()
	if err != nil {
		return "reader"
	}
	name := u.Username
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return -1
	}, name)
	if name == "" || len(name) > 64 {
		return "reader"
	}
	return name
}

// randomPassword is the local account's password, which nobody is ever asked
// for: DevMode serves the account without a login. It exists because the
// account row requires a hash, and a random one means the database copied to a
// server and started WITHOUT -dev is not open to a guessable default — the
// owner sets a real one there with `articleflux passwd`.
func randomPassword() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WebRootBeside is the default web root: a `web` folder next to the
// executable, which is how ./scripts/make.ps1 desktop lays the output out.
func WebRootBeside(exe string) string {
	return filepath.Join(filepath.Dir(exe), "web")
}
