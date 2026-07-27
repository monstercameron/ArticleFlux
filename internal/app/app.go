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
	"google.golang.org/grpc/keepalive"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	"github.com/monstercameron/ArticleFlux/internal/assetproxy"
	"github.com/monstercameron/ArticleFlux/internal/buildver"
	"github.com/monstercameron/ArticleFlux/internal/connpolicy"
	"github.com/monstercameron/ArticleFlux/internal/discover"
	"github.com/monstercameron/ArticleFlux/internal/extract"
	"github.com/monstercameron/ArticleFlux/internal/favicon"
	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/idem"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/obs"
	"github.com/monstercameron/ArticleFlux/internal/pageproxy"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/render"
	"github.com/monstercameron/ArticleFlux/internal/reqid"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/skew"
	"github.com/monstercameron/ArticleFlux/internal/smart"
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
	// It must be asked for explicitly (`-dev`), and cmd/articleflux refuses it on
	// any bind that is not loopback. It used to be *derived* from a loopback
	// bind, which was wrong in the one way that matters: nginx forwarding to
	// 127.0.0.1:9000 is a loopback bind, so the standard reverse-proxy
	// deployment silently served the first user's superadmin scope to the whole
	// internet. A bind address describes network topology, not who is calling.
	DevMode bool

	// ProxyImages turns the §10.1a asset proxy on for the instance.
	//
	// It is a server switch rather than only a preference because it decides
	// whether this box makes outbound requests on a reader's behalf at all, and
	// that is an operator's question. Per-user opt-out rides on top of it.
	//
	// Default off in the zero Config; cmd/articleflux turns it on, because the
	// case it fixes — images that never load because the reader's network
	// blocks the publisher the server can reach — looks like a bug in this
	// application rather than like a missing feature.
	ProxyImages bool

	// ProxyPages turns the §10.1b page proxy on — the tier-2 "show me the
	// actual site" view. Requires ProxyImages, which is what its subresources
	// are served through.
	//
	// A separate switch from ProxyImages because it is a materially different
	// commitment: an image proxy fetches files a page already referenced, while
	// this fetches whole documents from arbitrary hosts and re-serves them under
	// our own name. An operator may reasonably want the first and not the second.
	ProxyPages bool

	// ProxyStream turns on the §10.1d live view: a headless browser renders the
	// page and its frames are streamed to the reader as MJPEG.
	//
	// Off by default, and unlike ProxyPages that default is not going to move.
	// This is the one rung that runs a browser on the instance, and it is also
	// the one whose SSRF story is weaker than everything else here — the browser
	// dials for itself, so netguard's socket-level guard never sees it (see
	// render.Stream). An operator opts into that; it is not inherited from
	// "I wanted images to load".
	ProxyStream bool

	// BrowserPath overrides the browser binary for ProxyStream. Empty means the
	// usual locations for the platform — Edge or Chrome on Windows, Chrome or
	// Chromium on Linux — which is what lets the same config work on the
	// development laptop and the Ubuntu box.
	BrowserPath string

	// ProxyOrigin is the absolute origin minted proxy URLs point at, e.g.
	// "https://proxy.articleflux.example.com" (D20).
	//
	// Empty means same-origin relative URLs, which is correct for images and
	// NOT correct for the tier-2 page proxy: proxied HTML must never share an
	// origin with the app that holds the session (§10.1b). The field exists now
	// so that the split is a deploy-time setting rather than a migration of
	// every URL already minted and cached.
	ProxyOrigin string

	// AllowedOrigins is the exact set of page origins permitted to open the
	// tunnel, e.g. "https://articleflux.example.com" (TODO 7.4).
	//
	// Empty falls back to the WebSocket library's default same-origin policy,
	// which compares Origin against the request's Host. That is correct as long
	// as the proxy forwards Host faithfully — set this explicitly in production
	// so the guarantee does not depend on someone else's nginx.
	AllowedOrigins []string
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
	tts *tts.Client
	// The observability surface (§22.11): a ring of recent log records and
	// per-RPC latency, both in memory and both bounded. A self-hosted reader has
	// no operator, so these are how the person running it answers "why did that
	// feed stop working" without a terminal.
	ring *obs.Ring
	lat  *obs.Latency
	// tunnels counts WebSocket lifetimes, which is the only way anyone can tell
	// a reader on bad Wi-Fi from a keepalive tuned wrong (§20.19.10).
	tunnels *obs.Tunnels
	log     *slog.Logger
	// assets is the §10.1a image proxy. Nil means the instance never turned it
	// on, and every /asset request answers 501 — which is the honest answer for
	// a server that is working correctly and simply does not do this.
	assets *assetproxy.Fetcher
	// renderer is the §10.1d live view. Nil unless ProxyStream is on AND a
	// browser was actually found — "configured but no browser installed" and
	// "not configured" are the same answer to the client and a different line
	// in the log.
	renderer *render.Renderer
	// pages is the §10.1b page proxy — tier 2. Nil for the same reason and with
	// the same 501; it shares assetKey, since both capabilities are signed by
	// the instance and the message prefix keeps them from being interchangeable.
	pages *pageproxy.Fetcher
	// assetKey signs proxy capabilities. Persisted beside the database so URLs
	// on an open page survive a restart.
	assetKey []byte
	icons    *favicon.Fetcher
	// The Smart+ tier (§10.5, §18). All three are always non-nil: an instance
	// with no API key still serves the settings screen that says so, and a nil
	// service would make "not configured" indistinguishable from "your client
	// is newer than this server".
	//
	// settings also holds the encryption key for secrets at rest; secretKey is
	// nil when the server could not open one, in which case SetSecret refuses
	// rather than writing a credential in the clear.
	settings   *store.SettingsRepo
	secretKey  []byte
	llm        *llm.Client
	translator *smart.Translator
	grpc       *grpc.Server
	handler    http.Handler
	stopPo     chan struct{}
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

	// Argon2id, tuned to this box (§7.1, TODO 6.1).
	//
	// One constant is wrong twice: the OWASP baseline takes ~40ms on a modern
	// server and ~400ms on the small machine this is likely to be self-hosted
	// on, so shipping it means either a weak hash on fast hardware or a login
	// that feels broken on slow hardware.
	//
	// It only ever RAISES the cost — see TuneToBox. The benchmark measures the
	// box as it is at this instant, and a restart during a poll or on a noisy
	// VPS neighbour measures slow; a boot-time benchmark allowed to lower the
	// cost would be a downgrade attack anybody could trigger with load.
	//
	// Logged either way, because the hashing cost is a security property and a
	// silent change to one is not something an operator should have to discover
	// by benchmarking.
	if p, raised := secret.TuneToBox(secret.TuneTarget); raised {
		cfg.Log.Info("argon2id tuned to this machine",
			"memory_kib", p.Memory, "iterations", p.Time, "target", secret.TuneTarget)
	} else {
		cfg.Log.Info("argon2id left at the baseline; this machine is not fast "+
			"enough to raise it without exceeding the login budget",
			"memory_kib", p.Memory, "iterations", p.Time)
	}

	// The ring wraps whatever handler the caller configured rather than replacing
	// it: terminal output is what someone watching the process sees, and losing
	// it to gain a settings screen is a bad trade.
	ring := obs.NewRing(cfg.Log.Handler(), obs.DefaultSize)
	// And the request-id handler outside the ring, so an id reaches BOTH the
	// terminal and the settings screen's log view. Inside the ring it would
	// stamp only what the ring re-emitted, and the two views of the same event
	// would carry different fields — which is worse than neither having it,
	// because it makes the terminal look authoritative when it is not.
	cfg.Log = slog.New(reqid.NewHandler(ring))

	a := &App{cfg: cfg, db: db, repo: repo, svc: svc, log: cfg.Log,
		ring: ring, lat: obs.NewLatency(), tunnels: &obs.Tunnels{},
		icons:  favicon.New(cfg.AllowPrivateFeeds),
		stopPo: make(chan struct{})}

	// Smart+ (§10.5, §18).
	//
	// The encryption key lives beside the database for the reason the asset key
	// does, and with the same honest limitation: 0600 on a file next to the
	// data IS the access control, so this protects a leaked *database* — a
	// backup, a `VACUUM INTO` copy, a .db someone emailed themselves — and not
	// a compromised host. That is the realistic threat for a self-hosted box,
	// and it is worth saying out loud rather than implying more.
	//
	// A failure here is not fatal. An instance that cannot store secrets can
	// still take its key from OPENAI_API_KEY, and refusing to start over a
	// feature nobody may have configured would be the wrong trade.
	secretKey, kerr := loadOrCreateSecretKey(filepath.Dir(cfg.DBPath))
	if kerr != nil {
		cfg.Log.Warn("cannot store secrets: no encryption key", "err", kerr)
	}
	a.secretKey = secretKey
	a.settings = store.NewSettingsRepo(db, secretKey)
	// The key is read through a function on every call, not captured here, so
	// changing it on the Settings screen takes effect without a restart. The
	// stored key wins over the environment: someone who pastes a new key into
	// the running server means it, and an env var they cannot see would
	// silently override them.
	smartKey := func(ctx context.Context) string {
		if k, err := a.settings.SystemSecret(ctx, store.KeyOpenAIAPIKey); err == nil {
			if k = strings.TrimSpace(k); k != "" {
				return k
			}
		}
		return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	a.llm = llm.New(smartKey)
	a.translator = smart.NewTranslator(a.llm, a.settings)
	// Following a page that has no feed (§11, §14.2).
	//
	// Three pieces, wired together because they are one ladder: `discover`
	// climbs the free rungs and owns the raw page fetch and robots.txt,
	// `extract` reads the full text of a scraped item, and the analyser is the
	// last rung. The analyser is harmless without a key — it reports "no key"
	// and nothing egresses — so all three are wired unconditionally and the
	// gating lives at the RPC, where the per-user preference is.
	svc.WithSiteAnalysis(
		discover.New(discover.Config{AllowPrivateAddresses: cfg.AllowPrivateFeeds}),
		extract.New(extract.Config{AllowPrivateAddresses: cfg.AllowPrivateFeeds}),
		smart.NewSiteAnalyzer(a.llm, a.settings),
	)
	// The voice reads the SAME key function, which is the point: one credential
	// drives every Smart+ feature. An instance where the voice works and
	// translation does not — because one read the environment and the other read
	// the setting — is a bug nobody can see the shape of from the settings screen.
	//
	// Cached beside the database, so a backup that copies the data directory
	// carries the audio with it and a re-listen after a restore is still free.
	a.tts = tts.New(filepath.Join(filepath.Dir(cfg.DBPath), "speech-cache"),
		tts.KeyFunc(smartKey))

	if cfg.ProxyImages {
		dataDir := filepath.Dir(cfg.DBPath)
		key, kerr := loadOrCreateAssetKey(dataDir)
		if kerr != nil {
			// Not fatal. A reader that cannot proxy images is a reader with
			// some broken pictures; a reader that refuses to start is one
			// nobody can use at all, and this is a nicety on the article path.
			cfg.Log.Warn("asset proxy disabled: cannot open its signing key", "err", kerr)
		} else {
			a.assetKey = key
			a.assets = assetproxy.New(assetproxy.Options{
				// Beside the database, like the speech cache, so a backup of
				// the data directory carries it and a restore is still warm.
				Dir:          filepath.Join(dataDir, "asset-cache"),
				AllowPrivate: cfg.AllowPrivateFeeds,
				UserAgent:    "ArticleFlux/" + cfg.Version + " (+asset proxy)",
			})
			if cfg.ProxyPages {
				a.pages = pageproxy.New(pageproxy.Options{
					Dir:          filepath.Join(dataDir, "page-cache"),
					AllowPrivate: cfg.AllowPrivateFeeds,
					UserAgent:    "ArticleFlux/" + cfg.Version + " (+page proxy)",
				})
			}
			if cfg.ProxyStream {
				rr := render.New(render.Options{
					ExecPath:     cfg.BrowserPath,
					AllowPrivate: cfg.AllowPrivateFeeds,
				})
				// Checked now rather than on the first request. "No browser
				// installed" is a deploy-time fact, and discovering it when a
				// reader presses Live is discovering it in the worst place.
				if rr.Available() {
					a.renderer = rr
				} else {
					cfg.Log.Warn("live view disabled: no chromium-family browser found",
						"hint", "install Chrome or Chromium, or set -browser-path")
				}
			}
		}
	}

	// Say what the reader will see, at the level that decides it.
	//
	// The failure this replaces was a real one and it cost an evening: with the
	// page proxy off, the article's "View page" control is ABSENT rather than
	// disabled — deliberately, because a disabled button advertises a feature
	// the server refuses. The consequence is that a missing flag and a missing
	// feature look identical from the reading pane, and the only clue lives in
	// a flag you would have to already know about. One line at boot closes
	// that gap for good.
	switch {
	case a.pages != nil:
		cfg.Log.Info("proxy enabled", "images", true, "pages", true,
			"liveView", a.renderer != nil)
	case a.assets != nil:
		cfg.Log.Info("proxy enabled", "images", true, "pages", false,
			"note", "the article's View page control will not appear; pass -proxy-pages")
	case cfg.ProxyPages:
		// The one genuinely inconsistent pair, and it used to fail in silence:
		// pages are served through the asset proxy, so asking for one without
		// the other is asking for a page with no pictures and no stylesheet.
		cfg.Log.Warn("-proxy-pages needs -proxy-images and was ignored",
			"note", "pages are rewritten to fetch their subresources through the asset proxy")
	default:
		cfg.Log.Info("proxy disabled", "note", "articles will load images directly from publishers")
	}

	a.buildHandler()
	return a, nil
}

// Log returns the logger the app actually uses.
//
// Open WRAPS the logger it was given so every record also lands in the in-memory
// ring the settings screen reads. A caller that keeps logging through its
// original logger — cmd/articleflux and its HTTP middleware did exactly this —
// bypasses the ring, and the Activity screen shows a handful of poller lines
// while claiming to be the server's log.
func (a *App) Log() *slog.Logger { return a.cfg.Log }

// DB exposes the database, for tests and for the CLI.
func (a *App) DB() *store.DB { return a.db }

// Service exposes the service layer, for tests and seeding.
func (a *App) Service() *reader.Service { return a.svc }

// Repo exposes the repository, for seeding.
func (a *App) Repo() *store.ReaderRepo { return a.repo }

// keepaliveMinTime is the fastest client ping this server will tolerate on an
// idle tunnel (§20.19.3).
//
// It sits BELOW the client's 30s interval with margin, because a throttled or
// descheduled ping arrives late, never early — a policy tuned to exactly 30s
// would only ever be violated by jitter in our own favour.
//
// A var rather than a const for exactly one reason: the regression test that
// proves this policy is installed has to observe a client NOT being kicked, and
// at the shipping numbers that takes over a minute of real time. The test lowers
// this to milliseconds and watches the same behaviour in under a second. Nothing
// else may write it.
//
// The value is connpolicy's, so the client's interval and this floor are
// declared in one place with the invariant between them tested.
var keepaliveMinTime = connpolicy.ServerMinTime

// gracePeriod is how long a shutdown waits for calls in flight.
//
// Matched to the HTTP server's own shutdown deadline in cmd/articleflux, so the
// two do not disagree about how long a deploy is allowed to take.
const gracePeriod = 5 * time.Second

// Close shuts everything down, draining first (§20.19.9).
//
// The hard `Stop()` this replaces severed live tunnels mid-call, and the reason
// that was invisible is worth recording: `http.Server.Shutdown` **does not wait
// for hijacked connections**, and a WebSocket upgrade is one. So the HTTP half
// of the shutdown always returned promptly with every tunnel still open, and
// this function is where they actually died. The cost was specific — a
// `SetItemState` in flight during a redeploy rolled the optimistic UI back and
// told the reader "Couldn't save that" for a server that was coming straight
// back, which is the one lie the write path can tell.
//
// GracefulStop refuses new calls, lets the ones in flight finish, and closes
// each transport properly so the client re-dials at once instead of inferring a
// failure from silence. Bounded, because a stuck handler must not turn a deploy
// into a hang: past the deadline it is a hard Stop, which is exactly what used
// to happen immediately.
func (a *App) Close() error {
	select {
	case <-a.stopPo:
	default:
		close(a.stopPo)
	}
	// The browser is a child process, and one that outlives its parent is a
	// headless Chrome nobody knows about holding 300 MB until the box reboots.
	if a.renderer != nil {
		a.renderer.Close()
	}
	if a.grpc != nil {
		done := make(chan struct{})
		go func() {
			a.grpc.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(gracePeriod):
			a.log.Warn("gRPC did not drain in time; stopping hard",
				"after", gracePeriod)
			a.grpc.Stop()
			<-done
		}
	}
	return a.db.Close()
}

// Handler is the HTTP surface.
func (a *App) Handler() http.Handler { return a.handler }

// ready reports whether this instance can actually serve reads.
//
// One implementation behind both `/readyz` and the tunnel gate below, because
// two readiness checks drift and the drift is silent: the probe says ready, the
// upgrade says no, and the operator is looking at a green dashboard.
func (a *App) ready(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	_, err := a.db.SchemaVersion(ctx)
	return err
}

// whenReady refuses a WebSocket upgrade the server cannot honour (§20.19.9).
//
// `/readyz` existed and nothing consulted it, so `/grpc` would happily accept a
// tunnel into an instance that could not answer a single call. The client that
// results is the worst kind: connected, failing every RPC, and — because a
// failed RPC used to mean "the connection is down" — retrying hard against a
// server that is already struggling to come up. Every part of that is
// self-reinforcing.
//
// 503 plus `Retry-After` instead, which is a reconnecting client's honest
// instruction to wait, and which the backoff already knows how to obey. Five
// seconds because the thing being waited for is a database opening, not a
// deploy.
//
// Only the upgrade is gated. An established tunnel is left alone: a reader
// mid-article when the disk hiccups should see calls fail and recover, not have
// the connection pulled out from under them.
func (a *App) whenReady(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := a.ready(r.Context()); err != nil {
			a.log.Warn("refusing a tunnel upgrade: not ready", "err", err)
			w.Header().Set("Retry-After", "5")
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// skewPolicy reads the minimum client version this build serves.
//
// `RefuseUnstamped` stays FALSE. A request without the header is either a
// client older than the header — genuinely too old — or something that is not
// the wasm client at all: a curl, a test, the sync API. Nothing in the request
// tells those apart, and the cost of guessing wrong in the strict direction is
// locking a non-browser client out of the instance with no obvious cause.
func (a *App) skewPolicy() skew.Policy {
	min, ok := skew.Parse(buildver.MinClient)
	if !ok {
		// An unparseable minimum disables the check rather than refusing
		// everybody. A typo in a version constant must not be an outage.
		a.log.Warn("the minimum client version does not parse; skew checking is off",
			"min", buildver.MinClient)
		return skew.Policy{}
	}
	return skew.Policy{Min: min}
}

func (a *App) buildHandler() {
	// Three interceptors, and the ORDER is the design.
	//
	//   1. reqid   — mints the id, so everything after it can be correlated,
	//                including the idempotency replay path and its failures.
	//   2. idem    — §20.7's replay. Ahead of the timer, so a replayed call is
	//                not recorded as a fast one: a drained outbox would show up
	//                as a latency improvement on a method that did no work.
	//   3. latency — records every call. One place, because per-handler timing
	//                drifts the first time somebody adds a handler and forgets.
	//
	// Getting 1 and 2 the other way round is the tempting mistake: the replay is
	// exactly the path you want traceable, since it is the one nobody can
	// reproduce.
	a.grpc = grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			func(ctx context.Context, req any, _ *grpc.UnaryServerInfo,
				handler grpc.UnaryHandler) (any, error) {
				// One id per call, minted here because this is the only place
				// that sees all of them (§22.11). It is what connects the safe
				// "internal error" a reader is shown to the actual cause in the
				// log — a bug report saying "it said internal error" is
				// useless, and one saying "reference 7f3a9c" is a single grep.
				//
				// Minted rather than taken from the client: an id a caller
				// chooses is one they can collide, reuse across users, or use
				// to ask the log about somebody else.
				return handler(reqid.With(ctx, ""), req)
			},
			// Version skew (§22.10, TODO 7.8/8c.16), ahead of everything that
			// does work. A client below the minimum is refused before its
			// request touches the database, which is the point: the reason to
			// refuse it is that it may not understand what it is asking for.
			//
			// The client half — recognising the sentinel and offering Reload
			// instead of retrying — shipped first, deliberately. The client that
			// has to act on a skew refusal is by definition the OLD one, so
			// recognition had to be in the field before the first refusal was
			// ever sent.
			skew.Unary(a.skewPolicy()),
			// The missing half of a contract already in use: the client stamps a
			// key onto every mutating RPC and nothing on the server read one
			// (TODO 8c.15). Survivable only because every queued mutation sets
			// an absolute value, so applying it twice lands on the same state —
			// the first relative operation would have double-applied silently.
			idem.Unary(a.repo, a.scopeFromContext),
			func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
				handler grpc.UnaryHandler) (any, error) {
				start := time.Now()
				res, err := handler(ctx, req)
				// The full method is `/articleflux.v1.ReaderService/ListItems`; the last
				// segment is what a person reads.
				name := info.FullMethod
				if i := strings.LastIndexByte(name, '/'); i >= 0 {
					name = name[i+1:]
				}
				a.lat.Observe(name, time.Since(start), err != nil)
				return res, err
			}),
		// **This is the other half of the client's keepalive, and neither works
		// without it** (§20.19.3, client/data/client.go).
		//
		// The client probes an IDLE tunnel every 30s, which is the entire point:
		// an idle connection is exactly when a dead one goes unnoticed, because
		// nothing else is going to notice for the reader. gRPC's default
		// enforcement is MinTime 5m with PermitWithoutStream false, under which
		// that client collects two ping strikes and is sent
		// `GOAWAY ENHANCE_YOUR_CALM (too_many_pings)`. It reconnects, pings, and
		// is kicked again — so shipping the client half against these defaults
		// converts a silent half-open socket into a visible flap every sixty
		// seconds. The failure is loud, immediate, and reads like a network
		// problem rather than a configuration one.
		//
		// MinTime sits BELOW the client's interval with margin because a
		// throttled or descheduled ping arrives late, never early. A policy
		// tuned to exactly 30s would only ever be violated by jitter in our own
		// favour. If this option is ever removed as unused, the idle-soak test
		// (T21c) is what fails.
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             keepaliveMinTime,
			PermitWithoutStream: true,
		}),
	)
	pb.RegisterReaderServiceServer(a.grpc,
		grpcsrv.NewReaderServer(a.svc, a.scopeFromContext).
			WithAssetProxy(a.AssetURL).
			WithPageProxy(a.PageURL).
			WithLiveView(a.StreamURL, a.ScrollLive))
	pb.RegisterSystemServiceServer(a.grpc,
		grpcsrv.NewSystemServer(a.cfg.Version, a.cfg.Commit, a.db).
			WithObservability(a.repo, a.ring, a.lat, a.cfg.PollInterval, a.scopeFromContext))
	// The login surface. Registered unconditionally, including in DevMode: an
	// instance that starts on a laptop and later gets a domain must not need a
	// different binary, and a client that can always call Login is a client with
	// one code path instead of two.
	pb.RegisterAuthServiceServer(a.grpc,
		grpcsrv.NewAuthServer(a.repo, a.scopeFromContext, a.log, a.cfg.DevMode))
	// Smart+ (§10.5, §18). Registered on every instance, including one with no
	// API key: the screen that says "Smart+ is not configured" is served by
	// this, and an unconfigured instance that answered Unimplemented would look
	// like a version mismatch instead of a setting nobody has filled in.
	pb.RegisterSmartServiceServer(a.grpc,
		grpcsrv.NewSmartServer(a.settings, a.llm, a.translator, a.scopeFromContext, a.log))

	// The tunnel carries gRPC over one WebSocket, which is what lets a wasm
	// client speak real gRPC — browsers cannot open the HTTP/2 connection gRPC
	// normally requires.
	//
	// The caps are not decoration. An unbounded read limit lets one client
	// allocate arbitrary server memory from a single frame, and no connection cap
	// means one misbehaving tab can exhaust the listener.
	tunnelOpts := []grpctunnel.ServerOption{
		grpctunnel.WithReadLimitBytes(4 << 20),
		grpctunnel.WithKeepalive(30*time.Second, 90*time.Second),
		grpctunnel.WithMaxConnectionsPerClient(8),
		grpctunnel.WithMaxUpgradesPerClientPerMinute(30),
		// Counting tunnels (§20.19.10).
		//
		// "It feels flaky" is unfalsifiable without these, and this application
		// has no dashboard behind it — the Settings screen is where the person
		// running it finds out anything at all. One reconnect an hour is a
		// network; forty is a bug, and neither was visible.
		grpctunnel.WithConnectHook(func(*http.Request) { a.tunnels.Connected() }),
		grpctunnel.WithDisconnectHook(func(*http.Request) { a.tunnels.Disconnected() }),
	}
	// TODO 7.4's remaining half. Only applied when configured, because an empty
	// allowlist would reject every browser rather than fall back to same-origin.
	if len(a.cfg.AllowedOrigins) > 0 {
		tunnelOpts = append(tunnelOpts, grpctunnel.WithAllowedOrigins(a.cfg.AllowedOrigins...))
	}
	tunnel := grpctunnel.Wrap(a.grpc, tunnelOpts...)

	mux := http.NewServeMux()
	mux.Handle("/grpc", a.whenReady(tunnel))
	mux.HandleFunc("/favicon", a.serveFavicon)
	// Registered unconditionally, and gated inside: the handler itself reports
	// 501 with no key and 403 without the per-user opt-in. Registering it only
	// when configured would make "why is listening missing?" answerable only by
	// reading the server logs.
	mux.HandleFunc("/speech", a.serveSpeech)
	// Registered unconditionally and gated inside, for the same reason /speech
	// is: "why are the images missing?" should be answerable from the response
	// rather than only from the server log.
	mux.HandleFunc("/asset", a.serveAsset)
	mux.HandleFunc("/p", a.servePage)
	mux.HandleFunc("/stream", a.serveStream)
	// Liveness: the process is up and answering. Deliberately does not touch the
	// database — a liveness probe that fails on a slow query gets the process
	// killed and restarted into the same slow query.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	// Readiness: the process can actually serve reads. This one does touch the
	// database, which is the entire difference between the two — and it is why
	// they must be separate endpoints rather than one convenient probe.
	//
	// Status code and one word only (§22.4): a readiness probe is reachable
	// unauthenticated by definition, so anything it says is said to everyone.
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err := a.ready(r.Context()); err != nil {
			a.log.Warn("readiness check failed", "err", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("unready\n"))
			return
		}
		_, _ = w.Write([]byte("ready\n"))
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

// Preflight refuses to serve an instance that cannot work, before it starts
// listening (TODO 7.7).
//
// Every check here corresponds to a way this has actually gone wrong, and each
// one is cheap to verify at boot and expensive to discover later:
//
//   - No account on an authenticating bind means a login screen nobody can get
//     past. That is a bricked deploy that looks like a working one, and the fix
//     (`articleflux init`) is one command — but only if you are told.
//   - A missing web root means the server answers 404 for the app while the
//     health check passes, so an uptime monitor reports green.
//   - An unwritable data directory surfaces as a SQLite error on the first
//     write, which is minutes or hours after start.
//
// It returns a joined error rather than the first, because someone who has just
// set up a droplet usually has more than one of these wrong at once and a
// one-at-a-time boot loop is a miserable way to find that out.
func (a *App) Preflight(ctx context.Context) error {
	var problems []error

	if !a.cfg.DevMode {
		n, err := a.repo.CountUsers(ctx)
		switch {
		case err != nil:
			problems = append(problems, fmt.Errorf("counting accounts: %w", err))
		case n == 0:
			problems = append(problems, errors.New(
				"no accounts exist, so nobody can log in — run: articleflux init -user <name> -password <pass>"))
		}
	}

	if a.cfg.WebRoot != "" {
		index := filepath.Join(a.cfg.WebRoot, "index.html")
		if _, err := os.Stat(index); err != nil {
			problems = append(problems, fmt.Errorf(
				"web root %q has no index.html — the client is not built (run: make wasm): %w",
				a.cfg.WebRoot, err))
		}
	}

	// Written and removed rather than stat'd. A directory can be listable and not
	// writable, and SQLite needs to create the -wal and -shm siblings next to the
	// database, not just open the database itself.
	dir := filepath.Dir(a.cfg.DBPath)
	probe := filepath.Join(dir, ".articleflux-write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		problems = append(problems, fmt.Errorf(
			"data directory %q is not writable, so the WAL cannot be created: %w", dir, err))
	} else {
		_ = os.Remove(probe)
	}

	return errors.Join(problems...)
}

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
				// Dead sessions go with them. A row that can never authenticate
				// again is not history, and on a box nobody administers an
				// unbounded table is how a self-hosted instance grows a junk
				// drawer. Revoked rows are kept for a week first, so "which
				// device did I sign out, and when" is still answerable.
				if n, err := a.repo.PurgeExpiredSessions(ctx,
					time.Now().UTC().Add(-7*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
					a.log.Warn("purging sessions", "err", err)
				} else if n > 0 {
					a.log.Info("purged sessions", "count", n)
				}
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
