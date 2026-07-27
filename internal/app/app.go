// Package app wires the server together: storage, service, transports, and the
// background poller.
//
// It exists so cmd/articleflux stays a flag parser. Everything here is reachable
// from a test without starting a process.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	"github.com/monstercameron/ArticleFlux/internal/favicon"
	"github.com/monstercameron/ArticleFlux/internal/feed"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/transport/grpcsrv"
	"github.com/monstercameron/ArticleFlux/internal/tts"
)

// Config configures the app.
type Config struct {
	DBPath string
	// WebRoot serves the wasm client. Empty disables static serving.
	WebRoot string
	Log     *slog.Logger
	Version string
	Commit  string
	// AllowPrivateFeeds relaxes the SSRF guard. Off in production; on for the
	// dev server so a locally-served fixture feed can be subscribed to.
	AllowPrivateFeeds bool
	// PollInterval is how often the background poller runs. Zero disables it.
	PollInterval time.Duration
	// DevMode serves the single local account without a login.
	//
	// cmd/articleflux only sets this for a loopback bind. An internet-facing
	// instance with DevMode on would be an open reader — anyone who can reach
	// the port is the superadmin.
	DevMode bool
}

// App is a wired server.
type App struct {
	cfg  Config
	db   *store.DB
	repo *store.ReaderRepo
	svc  *reader.Service
	// tts is the Smart+ voice. Nil-safe: absent means every request to /speech
	// answers 501, which is the correct answer for an instance that never
	// configured an API key.
	tts     *tts.Client
	log     *slog.Logger
	icons   *favicon.Fetcher
	grpc    *grpc.Server
	handler http.Handler
	stopPo  chan struct{}
}

// Open builds the app and applies migrations.
func Open(ctx context.Context, cfg Config) (*App, error) {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	db, err := store.Open(store.Options{Path: cfg.DBPath})
	if err != nil {
		return nil, err
	}
	n, err := db.Migrate(ctx)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if n > 0 {
		cfg.Log.Info("migrations applied", "count", n)
	}

	repo := store.NewReaderRepo(db)
	svc := reader.New(repo, feed.New(feed.Config{AllowPrivateAddresses: cfg.AllowPrivateFeeds}))

	a := &App{cfg: cfg, db: db, repo: repo, svc: svc, log: cfg.Log,
		icons: favicon.New(cfg.AllowPrivateFeeds),
		// Cached beside the database, so a backup that copies the data directory
		// carries the audio with it and a re-listen after a restore is still free.
		tts:    tts.New(filepath.Join(filepath.Dir(cfg.DBPath), "speech-cache")),
		stopPo: make(chan struct{})}
	a.buildHandler()
	return a, nil
}

// DB exposes the database, for tests and for the CLI.
func (a *App) DB() *store.DB { return a.db }

// Service exposes the service layer, for tests and seeding.
func (a *App) Service() *reader.Service { return a.svc }

// Repo exposes the repository, for seeding.
func (a *App) Repo() *store.ReaderRepo { return a.repo }

// Close shuts everything down.
func (a *App) Close() error {
	select {
	case <-a.stopPo:
	default:
		close(a.stopPo)
	}
	if a.grpc != nil {
		a.grpc.Stop()
	}
	return a.db.Close()
}

// Handler is the HTTP surface.
func (a *App) Handler() http.Handler { return a.handler }

func (a *App) buildHandler() {
	a.grpc = grpc.NewServer()
	pb.RegisterReaderServiceServer(a.grpc,
		grpcsrv.NewReaderServer(a.svc, a.scopeFromContext))
	pb.RegisterSystemServiceServer(a.grpc,
		grpcsrv.NewSystemServer(a.cfg.Version, a.cfg.Commit, a.db))

	// The tunnel carries gRPC over one WebSocket, which is what lets a wasm
	// client speak real gRPC — browsers cannot open the HTTP/2 connection gRPC
	// normally requires.
	//
	// The caps are not decoration. An unbounded read limit lets one client
	// allocate arbitrary server memory from a single frame, and no connection cap
	// means one misbehaving tab can exhaust the listener.
	tunnel := grpctunnel.Wrap(a.grpc,
		grpctunnel.WithReadLimitBytes(4<<20),
		grpctunnel.WithKeepalive(30*time.Second, 90*time.Second),
		grpctunnel.WithMaxConnectionsPerClient(8),
		grpctunnel.WithMaxUpgradesPerClientPerMinute(30),
	)

	mux := http.NewServeMux()
	mux.Handle("/grpc", tunnel)
	mux.HandleFunc("/favicon", a.serveFavicon)
	// Registered unconditionally, and gated inside: the handler itself reports
	// 501 with no key and 403 without the per-user opt-in. Registering it only
	// when configured would make "why is listening missing?" answerable only by
	// reading the server logs.
	mux.HandleFunc("/speech", a.serveSpeech)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	// Dev-only reset, gated on exactly the same flag as the no-login mode — which
	// cmd/articleflux restricts to a loopback bind. It exists for the e2e suite,
	// which shares one database across tests; without it a test that marks an
	// article read changes what every later test sees.
	//
	// It is registered ONLY in DevMode, so a production instance does not have
	// an unauthenticated "wipe my read state" endpoint sitting there.
	if a.cfg.DevMode {
		mux.HandleFunc("/debug/reset-state", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			sc, err := a.devScope(r.Context())
			if err != nil {
				http.Error(w, "no local account", http.StatusPreconditionFailed)
				return
			}
			if err := a.repo.ResetUserState(r.Context(), sc); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "reset")
		})
	}

	if a.cfg.WebRoot != "" {
		mux.Handle("/", a.static(a.cfg.WebRoot))
	}
	a.handler = mux
}

// static serves the client with the headers a wasm app needs.
func (a *App) static(root string) http.Handler {
	fs := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Clean(r.URL.Path)

		switch {
		case strings.HasSuffix(p, ".wasm"):
			// Without this exact type the browser refuses to stream-compile and
			// silently falls back to a slower path, or fails outright.
			w.Header().Set("Content-Type", "application/wasm")
		case strings.HasSuffix(p, ".js"):
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}

		// Serve a precompressed sibling when one exists.
		//
		// G5 measured the bundle at 23.8 MB raw and 5.2 MB gzipped — a 4.5x
		// difference that decides whether this is usable on a phone at all.
		// Precompressed rather than on-the-fly because gzipping 24 MB per request
		// would burn more CPU than the whole rest of the server combined, and the
		// file only changes when the client is rebuilt.
		if gz, ok := precompressed(root, p, r); ok {
			w.Header().Set("Content-Encoding", "gzip")
			// Vary is mandatory: without it a cache can hand the gzipped bytes to
			// a client that did not ask for them, which fails as a corrupt wasm
			// module rather than as anything legible.
			w.Header().Add("Vary", "Accept-Encoding")
			r = r.Clone(r.Context())
			r.URL.Path = gz
		}

		// The wasm binary is rebuilt constantly during development, and a cached
		// stale binary looks exactly like "my change did nothing".
		if strings.HasSuffix(p, ".wasm") || strings.HasSuffix(p, ".js") || p == "/" {
			w.Header().Set("Cache-Control", "no-store")
		}

		// Client-side routing: a deep link must serve the app shell rather than
		// 404, or refreshing on /feed/123 breaks.
		if r.URL.Path != "/" && !hasExt(p) {
			if _, err := os.Stat(filepath.Join(root, strings.TrimPrefix(p, "/"))); errors.Is(err, os.ErrNotExist) {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		fs.ServeHTTP(w, r)
	})
}

func hasExt(p string) bool { return filepath.Ext(p) != "" }

// StartPoller runs the background fetch loop until Close.
//
// Polling on a timer rather than on demand is what makes the reader feel like a
// reader: items are there when you open it, instead of arriving after a spinner.
func (a *App) StartPoller(ctx context.Context) {
	if a.cfg.PollInterval <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(a.cfg.PollInterval)
		defer t.Stop()
		for {
			select {
			case <-a.stopPo:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				// Icons ride along with the poll rather than on their own timer:
				// the same "we are doing background work now" moment, and it keeps
				// the two from overlapping their outbound connections.
				go a.WarmFavicons(ctx, 25)
				res, err := a.svc.PollDue(ctx, 25)
				if err != nil {
					a.log.Warn("poll", "err", err)
					continue
				}
				if res.Polled > 0 {
					a.log.Info("polled", "sources", res.Polled, "new", res.NewItems,
						"errors", len(res.Errors))
				}
			}
		}
	}()
}

// precompressed reports whether a `.gz` sibling exists for path and the client
// accepts gzip, returning the sibling's URL path.
//
// Only wasm and js are considered: those are the large, static, rebuilt-rarely
// assets where the 4.5x saving is worth a stat() per request. HTML is small
// enough that compressing it would cost more in complexity than it saves.
func precompressed(root, p string, r *http.Request) (string, bool) {
	if !strings.HasSuffix(p, ".wasm") && !strings.HasSuffix(p, ".js") {
		return "", false
	}
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		return "", false
	}
	rel := strings.TrimPrefix(filepath.ToSlash(p), "/")
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)+".gz")); err != nil {
		return "", false
	}
	return "/" + rel + ".gz", true
}
