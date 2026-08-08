// Command articleflux is the server.
//
//	articleflux serve      run the reader (default)
//	articleflux seed       subscribe the local account to a starter set of feeds
//	articleflux poll       fetch every due source once and exit
//	articleflux version    print the build and exit
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/app"
	"github.com/monstercameron/ArticleFlux/internal/buildver"
	"github.com/monstercameron/ArticleFlux/internal/clientaddr"
	"github.com/monstercameron/ArticleFlux/internal/envfile"
	"github.com/monstercameron/ArticleFlux/internal/fluxcast/produce"
	"github.com/monstercameron/ArticleFlux/internal/rundown"
	"github.com/monstercameron/ArticleFlux/internal/seedread"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The single build constant, shared with the wasm client so the two cannot
// disagree about what version they are (§22.10). See internal/buildver.
const version = buildver.Version

// logLevel is the process's verbosity, held in a LevelVar rather than baked
// into the handler at construction.
//
// A slog.HandlerOptions.Level that is a plain Level is read once and fixed for
// the life of the process, so raising verbosity meant editing this file and
// restarting — and a restart discards `internal/obs`'s log ring, which is the
// thing you were trying to read. The one moment you want debug output is the
// one moment the old arrangement made you destroy the evidence to get it.
//
// A LevelVar is read on every record and is safe to set concurrently, so the
// level can move while the process runs.
var logLevel = new(slog.LevelVar)

// setLogLevel parses a level name and applies it.
//
// The four names slog itself uses, matched case-insensitively because
// ARTICLEFLUX_LOG_LEVEL=DEBUG in a systemd unit is the same request as -log-level
// debug on a command line, and refusing one of them teaches nothing.
func setLogLevel(name string) error {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		logLevel.Set(slog.LevelDebug)
	case "", "info":
		logLevel.Set(slog.LevelInfo)
	case "warn", "warning":
		logLevel.Set(slog.LevelWarn)
	case "error":
		logLevel.Set(slog.LevelError)
	default:
		return fmt.Errorf("log level %q is not one of debug, info, warn, error", name)
	}
	return nil
}

// newLogHandler builds the stderr handler in the requested format.
//
// Text is the default because the person most likely to be reading this output
// is the one who just started the process, and `key=value` is what a human
// reads. JSON exists for the other case: once these lines are going into Loki,
// journald's structured fields, or anything that indexes rather than displays,
// a text handler means every consumer re-parses `key=value` — badly, because
// the values are quoted only when they need to be.
//
// The choice is at the OUTER handler, so the ring, the request-id stamp and the
// trace stamp are unaffected: format is a rendering decision and those three
// are about content.
func newLogHandler(format string) (slog.Handler, error) {
	opts := &slog.HandlerOptions{Level: logLevel}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "text":
		return slog.NewTextHandler(os.Stderr, opts), nil
	case "json":
		return slog.NewJSONHandler(os.Stderr, opts), nil
	default:
		return nil, fmt.Errorf("log format %q is not one of text, json", format)
	}
}

func main() {
	// The format is read from the environment here rather than from a flag,
	// because this logger has to exist before any subcommand parses anything —
	// and the errors it reports before that point are exactly the ones somebody
	// running under a log collector wants structured. `serve` re-reads it as a
	// flag below, which is what makes -log-format work on the command line.
	handler, herr := newLogHandler(os.Getenv("ARTICLEFLUX_LOG_FORMAT"))
	if herr != nil {
		// Reported through the format we could not build, which is the one
		// thing certain to work.
		fmt.Fprintf(os.Stderr, "articleflux: %v\n", herr)
		os.Exit(2)
	}
	log := slog.New(handler)

	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}

	var err error
	switch cmd {
	case "version":
		fmt.Printf("articleflux %s (%s)\n", version, commit())
		return
	case "serve":
		err = serve(log, args)
	case "init":
		err = initInstance(log, args)
	case "adduser":
		err = addUser(log, args)
	case "passwd":
		err = passwd(log, args)
	case "reset":
		err = reset(log, args)
	case "audit":
		err = auditCmd(log, args)
	case "migrate":
		err = migrate(log, args)
	case "backup":
		err = backup(log, args)
	case "vacuum":
		err = vacuumCmd(log, args)
	case "rotate-key":
		err = rotateKeyCmd(log, args)
	case "seed":
		err = seed(log, args)
	case "seed-reading":
		err = seedReading(log, args)
	case "poll":
		err = poll(log, args)
	case "fluxcast":
		err = fluxcastCmd(log, args)
	case "speech":
		err = speechCmd(log, args)
	case "import":
		err = importOPML(log, args)
	case "export":
		err = exportOPML(log, args)
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "articleflux: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Error(cmd, "err", err)
		os.Exit(1)
	}
}

// usageOut is where usage() writes. os.Stderr everywhere but the test that
// checks every subcommand is actually listed — a subcommand that exists and is
// not named here is one nobody finds.
var usageOut io.Writer = os.Stderr

func usage() {
	fmt.Fprint(usageOut, `articleflux — a self-hosted feed reader

  articleflux serve   [-addr host:port] [-db path] [-web dir] [-origin url] [-dev]
  articleflux init    -user name [-db path]
  articleflux adduser -user name [-role member] [-db path]
  articleflux passwd  -user name [-db path]
  articleflux reset   -user name [-origin url] [-db path]
  articleflux audit   [-n 50] [-since 24h] [-action a,b] [-alerts] [-json] [-db path]
  articleflux migrate [-db path]
  articleflux backup  -out path [-db path] [-keep n]
  articleflux vacuum  [-db path] [-incremental] [-n]
  articleflux rotate-key [-db path] [-yes] [-n]
  articleflux seed    [-db path] [-feeds url,url,...]
  articleflux seed-reading [-db path] [-focus word,word] [-read 0.6] [-seed 1]
  articleflux poll    [-db path]
  articleflux import  -file feeds.opml [-db path] [-fetch]
  articleflux export  [-file feeds.opml] [-db path]
  articleflux fluxcast [-db path] [-minutes 20] [-style balanced] [-rate 1.0] [-quickhits]
  articleflux speech   [-db path] [-full]
  articleflux version

serve defaults to 127.0.0.1:9000 and REQUIRES a login. Pass -dev to serve the
local account with no login; that is refused on any bind but loopback.

A fresh install needs "articleflux init" once — without an account there is
nothing to log in as, and serve says so at boot rather than at the login screen.

init/adduser/passwd ask for a password on a hidden terminal prompt, or read
ARTICLEFLUX_PASSWORD for scripted use — never as a command-line flag, which
would sit in shell history and process listings.

"audit" is the security trail (§7.9): sign-ins, password changes, recoveries,
lockouts and admin actions. "-alerts" drops routine sign-in/sign-out and leaves
the events worth looking at. Every entry is also logged live under the
"security_event" key, which is what to point an alerting rule at.

"passwd" sets a password here, on the box. "reset" instead prints a single-use
link the reader opens to choose their own, which is the one to use when the
person locked out is not the person with the shell — it avoids an operator
reading a password down a phone line. It lasts an hour and minting another
invalidates the first.
`)
}

// commonFlags are shared by every subcommand that touches storage.
func commonFlags(fs *flag.FlagSet) *string {
	return fs.String("db", "articleflux.db", "path to the SQLite database")
}

func serve(log *slog.Logger, args []string) error {
	// .env before the flags, so it can supply their defaults. See loadDotenv:
	// anything already in the real environment wins, and every one of these is
	// still overridable by an explicit flag.
	loadDotenv(log)

	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := commonFlags(fs)
	addr := fs.String("addr", envOr("ARTICLEFLUX_ADDR", "127.0.0.1:9000"), "address to listen on")
	// bin/web, not web/: web/ holds the source index.html, and bin/web is the
	// assembled root the build produces. Serving the source directory would work
	// right up until it needed a file the build generates.
	webRoot := fs.String("web", envOr("ARTICLEFLUX_WEB", filepath.Join("bin", "web")),
		"assembled web root (see ./scripts/make.ps1 wasm, or `make wasm`)")
	poll := fs.Duration("poll", 15*time.Minute, "how often to poll feeds in the background; 0 disables")
	user := fs.String("user", envOr("ARTICLEFLUX_DEV_USER", "cam"),
		"username for the -dev local account")
	pass := fs.String("password", envOr("ARTICLEFLUX_DEV_PASSWORD", devPassword),
		"password for the -dev local account, on first run only")
	// -dev is opt-in and refused off loopback. See below for why it is not
	// derived from the bind address any more.
	devFlag := fs.Bool("dev", envBool("ARTICLEFLUX_DEV"),
		"serve the local account with NO login; loopback binds only (env: ARTICLEFLUX_DEV)")
	// Profiling rides on exactly the same two-part gate as -dev, below, and for
	// a related reason: both publish something that assumes the only caller is
	// the person running the process. See internal/app/pprof.go.
	pprofFlag := fs.Bool("pprof", envBool("ARTICLEFLUX_PPROF"),
		"mount /debug/pprof and turn on the block and mutex profilers; loopback binds only (env: ARTICLEFLUX_PPROF)")
	origin := fs.String("origin", envOr("ARTICLEFLUX_ORIGIN", ""),
		"comma-separated page origins allowed to open the tunnel, e.g. https://reader.example.com")
	// Off by default, because X-Forwarded-For is a header any client can send.
	// See clientAddr for why trusting it unconditionally is worse than useless.
	behindProxy := fs.Bool("behind-proxy", envBool("ARTICLEFLUX_BEHIND_PROXY"),
		"trust X-Forwarded-For / X-Real-IP for client addresses; ONLY behind a proxy you control")
	// How MANY proxies, which is a separate question from whether there are any.
	//
	// X-Forwarded-For is a list the client writes the left-hand end of, and
	// nginx appends rather than replaces (`$proxy_add_x_forwarded_for`). So the
	// believable entries are the last N, and N is this. One is what
	// deploy/nginx.conf describes; a box behind a CDN as well has two.
	//
	// Too HIGH is the dangerous direction and it is silent: each extra hop moves
	// the trusted position one place left, and one place past the real edge is
	// whatever the caller typed into the header.
	proxyHops := fs.Int("proxy-hops", envIntDefault("ARTICLEFLUX_PROXY_HOPS", clientaddr.DefaultHops),
		"how many trusted proxies are in front, for reading X-Forwarded-For; only meaningful with -behind-proxy")
	// The five on-disk caches, as one number.
	//
	// They share the volume the database is on, and until this existed nothing
	// bounded their total — per-item ceilings only. A cache with no ceiling
	// beside a SQLite file is a slow leak pointed at every write the reader
	// makes; see internal/app/diskhealth.go for the whole path and for how the
	// budget is divided between them.
	cacheBudget := fs.Int("cache-budget-mb", envIntSigned("ARTICLEFLUX_CACHE_BUDGET_MB", app.DefaultCacheBudgetMB),
		"total megabytes the on-disk caches may occupy; negative disables eviction")
	// Metrics are always readable at /metrics with no configuration. This flag is
	// only about SENDING them somewhere, which is a network decision and so is
	// opt-in — the same position §18 takes about the model egress boundary.
	//
	// OTEL_EXPORTER_OTLP_ENDPOINT is the spelling every other OpenTelemetry
	// program uses; honouring it means a collector already configured for the
	// box needs nothing said twice.
	otlpEndpoint := fs.String("otlp-endpoint", envOr("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		"OpenTelemetry collector base URL, e.g. http://localhost:4318; empty keeps metrics local to /metrics")
	// The fraction of traces started here that are exported. 1.0 keeps the
	// behaviour an operator already has; lowering it is the only way to turn
	// down trace volume short of turning tracing off.
	otlpRatio := fs.Float64("otlp-sample-ratio", envFloatDefault("ARTICLEFLUX_OTLP_SAMPLE_RATIO", 1.0),
		"fraction of traces to export, 0-1; ignored without -otlp-endpoint")
	otlpInsecure := fs.Bool("otlp-insecure", envBool("ARTICLEFLUX_OTLP_INSECURE"),
		"send OTLP over plain HTTP; only sane for a collector on this host")
	// On by default, which is the odd one out among these flags and is
	// deliberate (§10.1a). The failure it prevents is an article whose images
	// silently never load because the *reader's* network blocks a publisher the
	// server can reach — and from the reading pane that is indistinguishable
	// from this application being broken. Turning it off is a real choice with
	// a real reason (every image becomes an outbound request from this box),
	// so it gets a flag rather than being buried.
	proxyImages := fs.Bool("proxy-images", envBoolDefault("ARTICLEFLUX_PROXY_IMAGES", true),
		"re-serve article images through this server (see plan.md §10.1a)")
	// On by default, matching -proxy-images. This started off and the argument
	// for that was wrong, so it is worth writing down rather than quietly
	// flipping: the claim was "proxying a page fetches whole documents from
	// arbitrary hosts".
	//
	// It does not. A page capability is only ever minted for an item's OWN url
	// (§10.1b's mint gate) — the exact URL "Open original" already sends the
	// reader's browser to, one click away, on the same article. The marginal
	// exposure over the button next to it is *who makes the request*, not what
	// gets requested. That is a much smaller step than the comment claimed.
	//
	// What defaulting it off actually cost was discoverability: the control is
	// absent rather than disabled when the proxy is off, so a reader who never
	// passed the flag sees a feature that appears not to exist, with the only
	// clue in a server flag they would have to already know about.
	proxyPages := fs.Bool("proxy-pages", envBoolDefault("ARTICLEFLUX_PROXY_PAGES", true),
		"serve publisher pages through this server (see plan.md §10.1b); requires -proxy-images")
	// Off by default and staying that way. This is the only rung that runs a
	// browser on the box, and the only one whose SSRF story is weaker than the
	// rest of this codebase — the browser dials for itself, so the socket-level
	// guard never sees it. That is an operator's decision, not something
	// inherited from wanting images to load.
	proxyStream := fs.Bool("proxy-stream", envBool("ARTICLEFLUX_PROXY_STREAM"),
		"run a headless browser and stream live page views (see plan.md §10.1d); requires -proxy-pages")
	browserPath := fs.String("browser-path", envOr("ARTICLEFLUX_BROWSER_PATH", ""),
		"browser binary for -proxy-stream; empty auto-detects Edge/Chrome/Chromium")
	proxyOrigin := fs.String("proxy-origin", envOr("ARTICLEFLUX_PROXY_ORIGIN", ""),
		"absolute origin for proxied content, e.g. https://proxy.example.com; empty means same-origin")
	logLevelFlag := fs.String("log-level", envOr("ARTICLEFLUX_LOG_LEVEL", "info"),
		"debug, info, warn or error")
	logFormatFlag := fs.String("log-format", envOr("ARTICLEFLUX_LOG_FORMAT", "text"),
		"text for a person reading the terminal, json for a log collector")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Applied before anything else logs, so a debug run captures the boot.
	//
	// Rejected rather than defaulted on a bad value: an operator who has just
	// asked for debug output and quietly received info would conclude the
	// problem they are chasing does not log, which is a worse place to be than
	// a startup error naming the four words that work.
	if err := setLogLevel(*logLevelFlag); err != nil {
		return err
	}
	// Rebuilt rather than mutated, because a handler's format is fixed at
	// construction. The caller's logger is replaced for the rest of serve, and
	// everything downstream — the ring, the request-id stamp, the trace stamp —
	// is layered onto this one inside app.Open.
	if h, err := newLogHandler(*logFormatFlag); err != nil {
		return err
	} else if *logFormatFlag != "text" {
		log = slog.New(h)
	}

	// DevMode used to be `isLoopback(*addr)`, and that was the single most
	// dangerous line in the program.
	//
	// The reasoning behind it was that a loopback bind cannot be reached from
	// outside the machine — which is true of the *socket* and false of the
	// *deployment*. Every reverse-proxy setup in existence, including the nginx
	// one this ships with, terminates TLS on :443 and forwards to
	// 127.0.0.1:9000. Under the old rule that bind turned authentication off, so
	// the canonical way to host this was also the way to publish one's entire
	// reading history, notes and all, to anyone who typed the domain.
	//
	// A bind address is a fact about network topology. It cannot tell you who is
	// on the other end of a connection, and nothing that cannot tell you that may
	// be allowed to decide whether to ask for a password. So: explicit flag,
	// default off, and still refused off loopback — belt and braces, since the
	// flag alone would eventually be pasted into a systemd unit by someone who
	// wanted to skip a login screen once.
	dev := *devFlag
	if dev && !isLoopback(*addr) {
		return fmt.Errorf(
			"-dev serves the local account with no login and %s is not a loopback address; "+
				"run `articleflux init` and log in instead", *addr)
	}
	// The loopback rule alone is NOT sufficient once -dev can come from a file.
	//
	// A deployed instance binds 127.0.0.1 and puts nginx in front — that is the
	// shipped systemd unit — so the loopback check passes there by design. It is
	// the whole reason DevMode stopped being derived from the bind address. Add a
	// `.env` that can set ARTICLEFLUX_DEV and the original vulnerability walks
	// straight back in through a new door: a stale development `.env` copied to a
	// server, or committed by someone who did not know it was read, and the
	// reader is open to the internet again.
	//
	// -behind-proxy is the operator stating that something is forwarding to this
	// process, which is exactly the fact the bind address cannot tell us. The two
	// together are never a development machine, so they are refused.
	if dev && *behindProxy {
		return errors.New(
			"-dev (no login) and -behind-proxy are mutually exclusive: a proxy in front of a " +
				"loopback bind is a published instance, which is precisely the case -dev must " +
				"never apply to. Unset ARTICLEFLUX_DEV, or drop -behind-proxy if nothing is in front")
	}

	// The same two clauses -dev gets, for the same two reasons, and it is worth
	// saying why rather than only mirroring the shape.
	//
	// A profiling endpoint is not authentication-shaped, so the instinct is that
	// it needs a weaker rule. It does not. /debug/pprof/profile parks a CPU
	// sampler on this process for thirty seconds per request and ?gc=1 forces a
	// collection, both from an unauthenticated GET — so anyone who can reach it
	// can hold the reader down. And the loopback clause alone is no more
	// sufficient here than it is there: the shipped systemd unit binds
	// 127.0.0.1 and puts nginx in front, so a stale `.env` carrying
	// ARTICLEFLUX_PPROF onto a server would pass a loopback check and publish it.
	prof := *pprofFlag
	if prof && !isLoopback(*addr) {
		return fmt.Errorf(
			"-pprof publishes an unauthenticated profiling surface and %s is not a loopback address; "+
				"profile over an SSH tunnel to a loopback bind instead", *addr)
	}
	if prof && *behindProxy {
		return errors.New(
			"-pprof and -behind-proxy are mutually exclusive: a proxy in front of a loopback bind " +
				"is a published instance, and /debug/pprof is not something to publish. " +
				"Unset ARTICLEFLUX_PPROF, or drop -behind-proxy if nothing is in front")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.Open(ctx, app.Config{
		DBPath:    *dbPath,
		WebRoot:   *webRoot,
		Log:       log,
		Version:   version,
		Commit:    commit(),
		DevMode:   dev,
		Profiling: prof,
		// Loopback development wants to subscribe to a fixture feed served from
		// the same machine, which the SSRF guard blocks by design.
		AllowPrivateFeeds: dev,
		PollInterval:      *poll,
		AllowedOrigins:    splitList(*origin),
		BehindProxy:       *behindProxy,
		TrustedProxyHops:  *proxyHops,
		CacheBudgetMB:     *cacheBudget,
		OTLPEndpoint:      *otlpEndpoint,
		OTLPInsecure:      *otlpInsecure,
		OTLPSampleRatio:   *otlpRatio,
		ProxyImages:       *proxyImages,
		ProxyPages:        *proxyPages,
		ProxyStream:       *proxyStream,
		BrowserPath:       *browserPath,
		ProxyOrigin:       *proxyOrigin,
	})
	if err != nil {
		return err
	}

	// From here on, log through the app's logger rather than the one we made:
	// Open wraps it so every record also lands in the ring the settings screen
	// reads, and the HTTP middleware below is the noisiest source there is.
	log = a.Log()
	defer a.Close()

	if dev {
		if _, err := a.EnsureDevUser(ctx, *user, *pass); err != nil {
			return fmt.Errorf("creating the local account: %w", err)
		}
	}

	// Refuse to listen if this instance cannot work. See app.Preflight for why
	// each check is there; the short version is that all three failures otherwise
	// surface long after boot, and one of them (no account) produces a login
	// screen nobody can get past while /healthz reports green.
	if err := a.Preflight(ctx); err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(*webRoot, "app.wasm")); err != nil {
		log.Warn("client not built — run `make wasm` (or ./scripts/make.ps1 wasm on Windows)",
			"expected", filepath.Join(*webRoot, "app.wasm"))
	}

	// Workers before the poller, and one derivation at boot.
	//
	// The boot pass is not cosmetic: without it a restart leaves the ranked
	// homepage showing whatever the last run produced until a full poll interval
	// has elapsed, which on the default interval is fifteen minutes of a page that
	// looks broken rather than stale.
	a.StartWorkers(ctx)
	a.DeriveDue(ctx)
	a.StartPoller(ctx)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           boundRequestReads(logging(log, *behindProxy, *proxyHops, a.Handler())),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: the gRPC tunnel is a long-lived WebSocket, and a write
		// deadline would sever it on a timer.
		//
		// No ReadTimeout either, for a less obvious version of the same reason.
		// A ReadTimeout is applied as a read deadline on the socket before the
		// handler runs, and `Hijack` does not clear it — so the deadline the
		// server set for reading a request would still be armed on the tunnel's
		// connection and would kill it at exactly that interval. The per-request
		// deadline in boundRequestReads is the ReadTimeout this cannot have: it
		// bounds the same thing and knows which requests not to bound.
		//
		// PLURAL, and that was the bug: the tunnel is not the only response here
		// that outlives a read deadline. `/stream` is an MJPEG live view, and a
		// read deadline expiring mid-response cancels the request context —
		// so it was being cut off at sixty seconds, silently. See
		// longLivedPaths.
		//
		// IdleTimeout, though, is safe and was missing. Without it a keep-alive
		// connection that finishes a request and says nothing more is held until
		// the client goes away, which on the standard topology nginx bounds for
		// us — and on the pre-DNS `-addr 0.0.0.0:9000` deployment
		// articleflux.service documents, nothing does. Two minutes is longer
		// than any browser's keep-alive and shorter than an afternoon.
		//
		// It does not touch the tunnel: a hijacked connection has left the
		// server's idle tracking entirely.
		IdleTimeout: 2 * time.Minute,

		// net/http's own errors, routed into this program's logger.
		//
		// A nil ErrorLog does not mean net/http is silent — it means net/http
		// writes to the standard `log` package, which goes to stderr as
		// unstructured text. So the one class of failure the application cannot
		// see for itself, RECOVERED HANDLER PANICS, was landing outside the
		// only log the settings screen can read: net/http recovers a panic in a
		// handler, logs it with its stack HERE, and closes the connection. From
		// inside the app that is indistinguishable from the reader's network
		// dropping.
		//
		// TLS handshake failures and malformed requests come through the same
		// channel and are worth the same treatment.
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelError),
	}

	logPosture(log, dev, *addr, splitList(*origin), *behindProxy, *proxyHops)

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "url", "http://"+*addr, "db", *dbPath, "dev", dev)

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// starterFeeds are subscribed by `articleflux seed`.
//
// Chosen to exercise the parser rather than to recommend reading: an Atom feed,
// two RSS 2.0 feeds with content:encoded, one with no per-item guid, and one
// high-volume firehose. If normalisation is wrong, it shows up here.
var starterFeeds = []string{
	"https://news.ycombinator.com/rss",
	"https://lobste.rs/rss",
	"https://www.theverge.com/rss/index.xml",
	"https://simonwillison.net/atom/everything/",
	"https://danluu.com/atom.xml",
	"https://blog.rust-lang.org/feed.xml",
	"https://go.dev/blog/feed.atom",
	"https://xkcd.com/rss.xml",
}

// seedReading writes a simulated reading history over the items already in the database.
//
// # Why this is a command and not a flag on `seed`
//
// `seed` subscribes to feeds — it makes the instance have CONTENT. This makes it have a
// READER. They are separate acts with separate risks: subscribing fetches from other
// people's servers, and this only writes rows locally, so mixing them would mean nobody
// could do the harmless one without doing the impolite one.
//
// It exists because My Feed is now honestly empty on a fresh database: a ranked page only
// shows items with a content-level reason (derive.hasContentMatch), and a reader who has
// read nothing has no interests to match. That is the correct behaviour and it makes the
// feature unobservable in development — a blank page cannot tell you whether the code
// works. This produces the weeks of reading that would otherwise be required.
//
// Deliberately NOT wired into `serve` or run automatically anywhere. Fabricated engagement
// in a real reader's database would corrupt the one table in the interest layer that cannot
// be recomputed, and it must take a person typing the command to do that.
func seedReading(log *slog.Logger, args []string) error {
	// .env, for the same reason serve reads it: this command ends by running a real
	// derivation, and if Smart+ is switched on for the account that derivation makes
	// LLM calls. Only `serve` loaded it, so the key was absent here and every paid
	// stage declined with "no API key" — a WARN line among several dozen, on a command
	// whose entire purpose is to show what the interest layer produces. The degradation
	// is deliberate and correct (§18: free Smart is the product); what was wrong was
	// showing a developer the free-tier result when they had configured the paid one.
	loadDotenv(log)

	fs := flag.NewFlagSet("seed-reading", flag.ExitOnError)
	dbPath := commonFlags(fs)
	user := fs.String("user", "cam", "username for the local account")
	pass := fs.String("password", devPassword, "password, on first run only")
	focus := fs.String("focus", "",
		"comma-separated title words this reader cares about; empty derives them from the corpus")
	read := fs.Float64("read", seedread.DefaultRead,
		"share of interesting items the reader opens, 0..1")
	seedVal := fs.Uint64("seed", 1,
		"makes the history reproducible; the same value gives the same reader")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	a, err := app.Open(ctx, app.Config{
		DBPath: *dbPath, Log: log, Version: version, DevMode: true,
	})
	if err != nil {
		return err
	}
	defer a.Close()

	sc, err := a.EnsureDevUser(ctx, *user, *pass)
	if err != nil {
		return err
	}

	var terms []string
	for _, t := range strings.Split(*focus, ",") {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			terms = append(terms, t)
		}
	}

	res, err := seedread.Run(ctx, a.Repo(), sc, seedread.Options{
		Focus: terms, Read: *read, Seed: *seedVal,
	})
	if err != nil {
		return err
	}
	log.Info("seeded a reading history",
		"items", res.Items, "focus", res.Focus,
		"impressions", res.Impressions, "opened", res.Opened,
		"completed", res.Completed, "reread", res.Reread, "liked", res.Liked,
		"bounced", res.Bounced, "skipped", res.Skipped)

	// Derive immediately, so the command's output is the answer rather than a promise. A
	// seeder that requires the operator to wait for a poll to find out whether it worked is
	// one they will run twice.
	a.DeriveNow(ctx, sc)
	return nil
}

func seed(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	dbPath := commonFlags(fs)
	list := fs.String("feeds", "", "comma-separated feed URLs; empty uses the starter set")
	user := fs.String("user", "cam", "username for the local account")
	pass := fs.String("password", devPassword, "password, on first run only")
	// Seeding is an operator action with explicit URLs, not user-supplied input
	// from a tenant — but it still defaults to off. Allowing loopback and RFC1918
	// by default would quietly weaken the SSRF guard for every future caller of
	// this command, which is exactly how such guards erode.
	allowPrivate := fs.Bool("allow-private", false,
		"permit feeds on loopback/LAN addresses (needed for local fixtures and self-hosted LAN feeds)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	a, err := app.Open(ctx, app.Config{
		DBPath: *dbPath, Log: log, Version: version, DevMode: true,
		AllowPrivateFeeds: *allowPrivate,
	})
	if err != nil {
		return err
	}
	defer a.Close()

	sc, err := a.EnsureDevUser(ctx, *user, *pass)
	if err != nil {
		return err
	}

	urls := starterFeeds
	if strings.TrimSpace(*list) != "" {
		urls = strings.Split(*list, ",")
	}

	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		// No category: the seed is a starter set, and inventing a taxonomy for
		// someone before they have read anything is filing their post for them.
		f, existed, _, err := a.Service().Subscribe(ctx, sc, u, "", "")
		if err != nil {
			// One unreachable feed must not abort the seed: the rest are still
			// worth having, and the failure is visible in the log.
			log.Warn("subscribe failed", "url", u, "err", err)
			continue
		}
		log.Info("subscribed", "title", f.Title, "url", u, "shared", existed)
	}

	res, err := a.Service().Refresh(ctx, sc, nil)
	if err != nil {
		return err
	}
	// The count that matters is what the user can now see, not what this last
	// Refresh added. Subscribe already polls each new source synchronously, so
	// NewItems here is legitimately 0 on a fresh seed — reporting that as "items"
	// made a working seed look like a broken one.
	total, cerr := a.Repo().CountItems(ctx, sc)
	if cerr != nil {
		return cerr
	}
	log.Info("seeded", "sources", res.Polled, "items", total,
		"new_this_pass", res.NewItems, "errors", len(res.Errors))
	for _, e := range res.Errors {
		log.Warn("feed error", "detail", e)
	}
	// A seed where every feed failed exits non-zero. It previously exited 0 with
	// warnings, which meant an e2e run against an unreachable fixture server
	// produced thirty confusing UI failures instead of one clear message about
	// the seed.
	if res.Polled > 0 && len(res.Errors) == res.Polled {
		return fmt.Errorf("every feed failed to fetch (%d/%d); nothing was seeded",
			len(res.Errors), res.Polled)
	}
	return nil
}

func poll(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("poll", flag.ExitOnError)
	dbPath := commonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	a, err := app.Open(ctx, app.Config{DBPath: *dbPath, Log: log, Version: version})
	if err != nil {
		return err
	}
	defer a.Close()

	res, err := a.Service().PollDue(ctx, 100)
	if err != nil {
		return err
	}
	log.Info("polled", "sources", res.Polled, "new", res.NewItems, "errors", len(res.Errors))
	return nil
}

// fluxcastCmd produces one rundown for a user and prints it — the terminal
// proof that TODO 11.1-11.5's four lanes now actually connect (plan.md §19 +
// §29). It reads whatever home_ranking, item_clusters and item_analysis this
// database already holds; it does not force a fresh derivation, matching
// `poll`'s own choice not to force one either. On a database whose
// background poller and boot derivation have run — which is every deployed
// instance and the dev server after a restart — that is already current.
//
// No model call reaches this command, by construction: internal/fluxcast's
// Produce never imports internal/llm or internal/smart, so this prints a
// complete rundown on an instance with no API key, exactly as TODO 11's rule
// 2 requires.
func fluxcastCmd(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("fluxcast", flag.ExitOnError)
	dbPath := commonFlags(fs)
	user := fs.String("user", devUsername, "username for the local account")
	pass := fs.String("password", devPassword, "password, on first run only")
	minutes := fs.Int("minutes", 20, "target rundown length in minutes (flux.length)")
	style := fs.String("style", store.StyleBalanced, "focused | balanced | explore (flux.style)")
	rate := fs.Float64("rate", 1.0, "playback rate multiplier (tts.rate); a higher rate fits more stories in the same minutes")
	quickHits := fs.Bool("quickhits", true, "include QUICK_HIT/MENTION roles (flux.quickHits)")
	title := fs.String("title", "", "rundown title; empty leaves it blank rather than inventing one (see rundown.Rundown.Title)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *minutes <= 0 {
		return fmt.Errorf("fluxcast: -minutes must be positive")
	}

	ctx := context.Background()
	a, err := app.Open(ctx, app.Config{DBPath: *dbPath, Log: log, Version: version, DevMode: true})
	if err != nil {
		return err
	}
	defer a.Close()

	sc, err := a.EnsureDevUser(ctx, *user, *pass)
	if err != nil {
		return err
	}

	producer := produce.NewRepo(a.Repo())
	produced, err := producer.Produce(ctx, sc, produce.Options{
		Title:          *title,
		Target:         time.Duration(*minutes) * time.Minute,
		Rate:           *rate,
		Style:          *style,
		AllowQuickHits: *quickHits,
	})
	if err != nil {
		return fmt.Errorf("fluxcast: %w", err)
	}

	printRundown(os.Stdout, produced, *rate)
	return nil
}

// speechCmd answers "why is read to me silent" from the one place that can.
//
// The browser cannot: an <audio> element reports a decode code and no HTTP
// status, so it genuinely cannot tell a refused key from a request that never
// arrived. The server can, and is forbidden from saying — the provider's
// message can quote the article being read aloud. So the answer lives here, in
// a terminal belonging to whoever owns the account.
//
// Free by default. -full writes one real segment and synthesises one short
// sentence, which is the only way to prove the whole path, and which spends.
func speechCmd(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("speech", flag.ExitOnError)
	dbPath := commonFlags(fs)
	user := fs.String("user", devUsername, "username for the local account")
	pass := fs.String("password", devPassword, "password, on first run only")
	full := fs.Bool("full", false, "also write and synthesise one real segment (this spends money)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	a, err := app.Open(ctx, app.Config{DBPath: *dbPath, Log: log, Version: version, DevMode: true})
	if err != nil {
		return err
	}
	defer a.Close()

	sc, err := a.EnsureDevUser(ctx, *user, *pass)
	if err != nil {
		return err
	}

	steps := a.DoctorSpeech(ctx, sc, *full)
	failed := false
	for _, s := range steps {
		fmt.Println(s)
		if !s.OK {
			failed = true
		}
	}
	if failed {
		// A non-zero exit so this is usable from a script and from CI, and so
		// that "it printed something" is not mistaken for "it passed".
		return fmt.Errorf("speech: at least one check did not pass")
	}
	return nil
}

// printRundown is the deliverable Job 4 asks for: a running order a person
// can read and judge, not a struct dump. Title, then per segment its theme
// and stories (source, role, words, estimated minutes), then the total —
// exactly the shape the visual rundown (TODO 11.17) will eventually render,
// because internal/rundown is the one place both read from.
func printRundown(w io.Writer, p produce.Produced, rate float64) {
	title := p.Rundown.Title
	if title == "" {
		title = "(untitled rundown)"
	}
	fmt.Fprintf(w, "%s\n", title)
	fmt.Fprintf(w, "target %s\n", p.Rundown.Target)
	fmt.Fprintln(w, strings.Repeat("=", 72))

	totalStories := 0
	for _, seg := range p.Rundown.Segments {
		theme := seg.Theme
		if theme == "" {
			theme = "(unsorted)"
		}
		fmt.Fprintf(w, "\n-- %s --\n", theme)
		for _, st := range seg.Stories {
			totalStories++
			headline := p.Titles[st.ItemID]
			if headline == "" {
				headline = st.ItemID
			}
			// 11.3's own arithmetic, taken from the package that owns it. The
			// literal 150.0 that stood here was a second copy of
			// rundown.WordsPerMinute, which made this table print its
			// per-story minutes by one implementation and its total, two
			// lines below, by another. They agreed only because both said
			// 150 — and WordsPerMinute's own comment describes it as a figure
			// calibrated against 140–160 broadcast norms, which is an
			// invitation to tune it.
			mins := float64(st.Words) / (rundown.WordsPerMinute * rundown.SafeRate(rate))
			source := "(no source on record)"
			if len(st.Sources) > 0 {
				source = strings.Join(st.Sources, ", ")
			}
			fmt.Fprintf(w, "  [%-10s] %-70s\n", st.Role, truncateFor(headline, 70))
			fmt.Fprintf(w, "               %-58s %4d words  ~%.1f min\n", source, st.Words, mins)
		}
	}

	fmt.Fprintln(w, strings.Repeat("=", 72))
	fmt.Fprintf(w, "%d stories, %d words, ~%s of a %s target\n",
		totalStories, p.Rundown.Words(), p.Rundown.Duration(rate).Round(time.Second), p.Rundown.Target)
}

// truncateFor keeps a headline inside a fixed-width column without cutting a
// rune in half — the same shape RankReason's own comment describes for a
// truncated hover string, applied here to a terminal column instead of a
// list row.
func truncateFor(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// isLoopback reports whether an address binds only the local machine.
//
// An empty or wildcard host is NOT loopback: ":9000" and "0.0.0.0:9000" listen
// on every interface, which is exactly the case where serving an
// unauthenticated superadmin session would be a hole rather than a convenience.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	switch host {
	case "localhost":
		return true
	case "", "0.0.0.0", "::":
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// dotenvPath is the file loaded before flags are parsed.
//
// Relative, so it is the `.env` of whatever directory the server was started
// from — the repository root during development. Deliberately not searched for
// up the tree: a loader that walks parent directories can pick up a `.env`
// belonging to something else entirely, and "which file configured this?" stops
// having a short answer.
const dotenvPath = ".env"

// loadDotenv applies `.env`, and says what it did.
//
// Logged rather than silent, and by KEY only. A configuration that arrives from
// a file nobody mentioned is the kind of thing people spend an afternoon on —
// especially the dev credentials below, where the symptom of a forgotten `.env`
// is "the password I am sure of does not work". The values never appear: this
// file holds an API key that bills and a password, and a log line that helpfully
// echoed either is how a secret ends up in a journal.
//
// A parse error is a warning rather than a fatal: `.env` is a development
// convenience, and refusing to start because a comment was malformed would be a
// worse failure than ignoring the file. The flags still have their defaults, and
// the warning names the problem.
func loadDotenv(log *slog.Logger) {
	applied, err := envfile.Load(dotenvPath)
	if err != nil {
		log.Warn("ignoring "+dotenvPath, "err", err)
		return
	}
	if len(applied) > 0 {
		log.Info("loaded "+dotenvPath, "keys", strings.Join(applied, ","))
	}
}

// envOr returns the environment value for key, or def when it is unset or empty.
//
// Empty counts as unset on purpose. `.env.example` ships keys with nothing after
// the `=` so they are visible and documented, and a copied-but-unfilled line
// must mean "I did not set this" rather than "set this to the empty string" —
// otherwise copying the example file silently blanks the bind address.
func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// envBool reads a boolean environment variable, defaulting to false.
//
// Only the affirmative spellings people actually type are true, and everything
// else — including a misspelling — is false. That asymmetry is deliberate for
// ARTICLEFLUX_DEV in particular: the failure mode of "meant to turn it off and
// it stayed on" is an unauthenticated reader, and the failure mode of "meant to
// turn it on and it stayed off" is a login prompt. Only one of those is a
// security incident, so ambiguity resolves towards the safe one.
func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// envBoolDefault is envBool for a setting whose default is on.
//
// It cannot share envBool's implementation, and the difference is the point:
// envBool resolves ambiguity towards *off* because the settings it reads are
// ones where "accidentally on" is the security incident. Here the accident runs
// the other way — a value nobody set must keep the default, and only an
// explicit, recognisable "off" turns the feature off. An unparseable value is
// therefore left as the default rather than read as false.
func envBoolDefault(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// envIntDefault reads a positive integer environment variable.
//
// Unset, unparseable, or non-positive all fall back to def, and that is the
// safe direction for its one caller: ARTICLEFLUX_PROXY_HOPS decides how far
// left in X-Forwarded-For the trusted entry is, and reading a typo as zero
// would move it. The default is the topology this repository ships, so a
// mistyped value behaves like a value nobody set.
func envIntDefault(key string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || n < 1 {
		return def
	}
	return n
}

// envIntSigned is envIntDefault for a setting where a negative value MEANS
// something.
//
// ARTICLEFLUX_CACHE_BUDGET_MB reads negative as "do not evict", which is a
// legitimate choice on a large volume with its own housekeeping — so unlike the
// hop count, this one cannot clamp at 1. Only an unparseable value falls back.
func envIntSigned(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

// logPosture states, in one line at boot, which of the two ways this instance
// can run it is actually running in.
//
// There is deliberately no `-prod` flag. Production is the DEFAULT and `-dev` is
// the opt-out, because the alternative polarity — a mode you must remember to
// turn on to be safe — is one that eventually does not get turned on, and the
// failure is silent and total. That is the same reasoning that removed DevMode's
// dependence on the bind address in the first place.
//
// But a default that is never stated is a default nobody checks. `dev=false` in
// the listening line is technically the answer and is easy to read past, so the
// posture is said out loud in the terms that matter: whether a password is
// required, and what is standing between this socket and the internet.
func logPosture(log *slog.Logger, dev bool, addr string, origins []string, behindProxy bool, hops int) {
	if dev {
		// Warn, not Info. This line describes a server anyone who can reach the
		// port owns, and it should not read like routine startup chatter.
		log.Warn("MODE=development — NO LOGIN; every request is the local superadmin",
			"addr", addr,
			"debug_endpoints", "/debug/reset-state",
			"ssrf_guard", "relaxed (private addresses reachable)")
		return
	}

	log.Info("MODE=production — login required",
		"addr", addr,
		"origin_allowlist", originSummary(origins),
		"trusts_forwarded_for", behindProxy,
		// The hop count is on the line because it is the one setting here whose
		// wrong value is silent AND exploitable: too high, and the trusted
		// position lands on an entry the caller wrote. See clientaddr.
		"trusted_proxy_hops", hops)

	// Production-only checks. These are warnings rather than refusals: each one
	// describes an instance that works and is weaker than it looks, and refusing
	// to start would be a worse trade than saying so — especially for someone
	// mid-deploy at midnight.
	if !isLoopback(addr) && len(origins) == 0 {
		log.Warn("no -origin set on a public bind: the tunnel falls back to the "+
			"WebSocket library's same-origin policy, which compares Origin against Host and "+
			"therefore holds only as long as whatever is in front forwards Host faithfully",
			"fix", "-origin https://your.domain (no trailing slash)")
	}
	// A loopback bind with no proxy declared is the shape of a local server; a
	// loopback bind WITH a proxy declared is the shipped deployment. The gap
	// between them — proxied but not declared — means client addresses in the log
	// are all 127.0.0.1, which is the difference between "who is hammering the
	// login" being answerable and not.
	if isLoopback(addr) && !behindProxy && len(origins) > 0 {
		log.Warn("an origin allowlist is set on a loopback bind but -behind-proxy is not: " +
			"if a reverse proxy is in front, every client address in the log will be the proxy's")
	}
}

// originSummary renders the allowlist for the boot line without pretending an
// empty list is a safe default.
func originSummary(origins []string) string {
	if len(origins) == 0 {
		return "(unset — same-origin fallback)"
	}
	return strings.Join(origins, ",")
}

// splitList parses a comma-separated flag into a trimmed, non-empty slice.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// clientAddr reports who made a request, for the log.
//
// It is a one-line call now, and that is the point of the change rather than a
// side effect of it. There used to be a copy of this rule here and a different
// copy behind the login limiter, and they disagreed in the deployment that
// matters: the log named the client while the limiter counted the proxy. A log
// that cannot be compared against the control it is meant to explain is worse
// than no log, because it is read as corroboration.
//
// internal/clientaddr holds the single rule, including why the header is
// believed only behind `-behind-proxy` and why the entry is counted from the
// RIGHT — the leftmost one is written by the caller, not by a proxy.
func clientAddr(r *http.Request, trustProxy bool, hops int) string {
	return clientaddr.Of(r, trustProxy, hops)
}

// requestReadTimeout is how long a request has to finish arriving.
//
// Sixty seconds, which is enormous for anything this server accepts — every
// HTTP body here is a form-sized POST or nothing at all; the large transfers
// are RPCs inside the tunnel, which this deliberately does not touch. It is set
// where it is because the number that matters is "not indefinite", and a
// generous bound nobody legitimate can hit is easier to leave alone than a
// tight one somebody has to keep tuning.
const requestReadTimeout = 60 * time.Second

// boundRequestReads gives every request but the tunnel a deadline for arriving.
//
// # Why this is not http.Server.ReadTimeout
//
// It would be, if the tunnel did not exist. `ReadTimeout` arms a read deadline
// on the socket before the handler runs, and `Hijack` hands that socket over
// with the deadline still armed — so a ReadTimeout of any value is also a
// hard limit on how long the WebSocket carrying every RPC in the application
// may live. That is the same trap `WriteTimeout` sets, one field over, and it
// is why the server has neither.
//
// The deadline therefore has to be applied per request, by something that knows
// which request is the tunnel. `/grpc` is skipped entirely; everything else gets
// bounded, which closes the slow-body half of slowloris that
// `ReadHeaderTimeout` leaves open — it bounds the headers and then stops
// caring.
//
// # Why the failure is soft
//
// `SetReadDeadline` is unavailable on a ResponseWriter that does not support it
// (an httptest recorder, most obviously). A middleware that refused the request
// in that case would break every test that exercises a handler directly, to
// protect against a condition that cannot arise on a real socket. So a
// ResponseController that cannot set the deadline is ignored and the request
// proceeds — under a real server it always can.
// longLivedPaths are the responses a read deadline must not be armed against.
//
// `/grpc` was here from the start and the reasoning above explains it. `/stream`
// belongs for the same reason and was missed, which is worth writing down
// because the two do not look alike: one is a hijacked WebSocket and the other
// is an ordinary HTTP response that simply never ends.
//
// # What the deadline does to a response that is still being written
//
// It is not inert. Once the handler starts writing, net/http runs a background
// read on the connection to notice the client going away — that is what powers
// request-context cancellation. An expired read deadline makes that read fail,
// and net/http answers a failed background read by CANCELLING THE REQUEST
// CONTEXT. Measured against a streaming handler: the context is cancelled about
// eighty milliseconds after the deadline, mid-response, and the client receives
// a cleanly truncated body with NO error.
//
// So `/stream` — the live view, `multipart/x-mixed-replace`, which selects on
// `r.Context().Done()` frame by frame — stopped dead at sixty seconds. Silently:
// no log line, no client error, an <img> that simply stops updating. The
// renderer's own IdleTimeout is three minutes, so a session the renderer
// intended to keep alive was being ended at a third of that by a middleware
// that never mentions it.
//
// A named set rather than a longer boolean, so the next endpoint that streams
// is added by someone who reads this comment.
var longLivedPaths = map[string]bool{
	"/grpc":   true, // the gRPC tunnel: hijacked, and the deadline survives Hijack
	"/stream": true, // the live view: MJPEG, ends only when the viewer leaves
}

func boundRequestReads(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !longLivedPaths[r.URL.Path] {
			_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(requestReadTimeout))
		}
		next.ServeHTTP(w, r)
	})
}

func logging(log *slog.Logger, trustProxy bool, hops int, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The tunnel is one long-lived request; logging its duration would emit a
		// single line hours later and nothing in between.
		if r.URL.Path == "/grpc" {
			log.Info("tunnel open", "remote", clientAddr(r, trustProxy, hops))
			next.ServeHTTP(w, r)
			log.Info("tunnel closed", "remote", clientAddr(r, trustProxy, hops))
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Info("req", "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "ms", time.Since(start).Milliseconds(),
			"remote", clientAddr(r, trustProxy, hops))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush and Hijack pass through so the WebSocket upgrade still works. Wrapping a
// ResponseWriter without them silently breaks streaming and upgrades — the
// tunnel would fail to establish with no obvious cause.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

func commit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return "unknown"
}

// importOPML subscribes the local account to every feed in an OPML file.
//
// Subscribing is separated from fetching on purpose. A 144-feed export takes
// minutes to fetch in full, and an import that appears to hang is one people
// interrupt halfway. Subscribe first — which is fast and gives an immediately
// usable sidebar — and let the background poller fill it in, or pass -fetch to
// wait.
func importOPML(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	dbPath := commonFlags(fs)
	file := fs.String("file", "", "path to an OPML file (required)")
	fetch := fs.Bool("fetch", false, "fetch every feed before returning, instead of leaving it to the poller")
	user := fs.String("user", "cam", "username for the local account")
	pass := fs.String("password", devPassword, "password, on first run only")
	allowPrivate := fs.Bool("allow-private", false, "permit feeds on loopback/LAN addresses")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("import: -file is required")
	}

	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}

	ctx := context.Background()
	a, err := app.Open(ctx, app.Config{
		DBPath: *dbPath, Log: log, Version: version, DevMode: true,
		AllowPrivateFeeds: *allowPrivate,
	})
	if err != nil {
		return err
	}
	defer a.Close()

	sc, err := a.EnsureDevUser(ctx, *user, *pass)
	if err != nil {
		return err
	}

	// The migration itself lives on the service (F1), not here. It used to live
	// in this function, which is why the only importer for the first year was
	// one that needed a shell on the server — the RPC behind Settings › Data
	// runs this same call, so the two cannot drift into disagreeing about what
	// an OPML file means.
	res, err := a.Service().ImportOPML(ctx, sc, data)
	if err != nil {
		return fmt.Errorf("import %s: %w", *file, err)
	}
	log.Info("imported", "title", res.Title, "subscribed", res.Subscribed,
		"already_subscribed", res.AlreadySubscribed, "already_on_server", res.Shared,
		"categories", res.Folders, "skipped", res.SkipCount())
	for _, s := range res.Skips {
		log.Warn("skipped", "title", s.Title, "url", s.URL, "err", s.Reason)
	}

	if !*fetch {
		log.Info("feeds will fill in as the poller runs; pass -fetch to wait")
		return nil
	}
	fetched, err := a.Service().Refresh(ctx, sc, nil)
	if err != nil {
		return err
	}
	log.Info("fetched", "sources", fetched.Polled, "new_items", fetched.NewItems,
		"errors", len(fetched.Errors))
	for _, e := range fetched.Errors {
		log.Warn("feed error", "detail", e)
	}
	return nil
}

// exportOPML writes the local account's subscriptions back out.
//
// An importer without an exporter is a roach motel; this is the other half.
func exportOPML(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	dbPath := commonFlags(fs)
	file := fs.String("file", "", "path to write; empty writes to stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	a, err := app.Open(ctx, app.Config{DBPath: *dbPath, Log: log, Version: version, DevMode: true})
	if err != nil {
		return err
	}
	defer a.Close()

	sc, err := a.EnsureDevUser(ctx, devUsername, devPassword)
	if err != nil {
		return err
	}
	// The service's exporter, for importOPML's reason — and because it is the
	// one that carries categories. This function used to write feeds flat,
	// which made the round trip lossy in exactly the way that matters to
	// somebody who spent an evening filing 151 feeds.
	data, n, err := a.Service().ExportOPML(ctx, sc)
	if err != nil {
		return err
	}

	if *file == "" {
		_, err := os.Stdout.Write(data)
		return err
	}

	// The close is CHECKED, and its error is the export's error.
	//
	// `defer fh.Close()` is what this was, and for a file being WRITTEN that
	// discards the one error that reports the write did not land. A successful
	// Write is not a successful save: on a filesystem with delayed allocation,
	// on a network mount, and on Windows, the failure that matters — out of
	// space, over quota, I/O error on writeback — is delivered at close. The
	// old shape logged "exported" and exited zero over a file that had been
	// truncated to nothing.
	//
	// That is the wrong way round for THIS command in particular. `export` is
	// how somebody keeps a copy of their subscriptions; a silent truncation is
	// discovered the day they try to restore it. The sibling write paths in this
	// repository already do it properly — internal/obs/spill.go checks Flush and
	// Close before renaming, cmd/precompress checks both of its closers — and
	// this was the one that did not.
	fh, err := os.Create(*file)
	if err != nil {
		return err
	}
	if _, werr := fh.Write(data); werr != nil {
		// Closed but not reported: the write error is the more specific one, and
		// returning the close error instead would name the symptom over the
		// cause.
		_ = fh.Close()
		return werr
	}
	if cerr := fh.Close(); cerr != nil {
		return fmt.Errorf("export: %s was written but did not close cleanly, so it "+
			"may be incomplete — do not rely on it as a backup: %w", *file, cerr)
	}

	// Only now. Anything above this line means the file on disk is not what was
	// asked for.
	log.Info("exported", "feeds", n, "file", *file)
	return nil
}

// envFloatDefault reads a fractional setting, keeping the default for anything
// it cannot parse.
//
// Same polarity as envBoolDefault and for the same reason: the one setting this
// reads is a trace sample ratio whose default is "export everything", and a
// typo that silently resolved to 0 would turn tracing off for somebody who had
// just gone to the trouble of configuring a collector — a failure they would
// diagnose as a broken exporter.
// The range is written as an ACCEPT rather than a reject, and that is what
// keeps NaN out.
//
// `ParseFloat` returns NaN with a nil error for the literal "NaN", and NaN
// compares false against everything — `NaN < 0` is false and `NaN > 1` is false
// — so the reject-form condition this used to have admitted the one value that
// is not a ratio. It then went to the trace sampler as a sampling probability.
//
// `f >= 0 && f <= 1` rejects it without naming it, and keeps doing so if
// somebody adds another bound later.
func envFloatDefault(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(key)), 64); err == nil &&
		v >= 0 && v <= 1 {
		return v
	}
	return def
}
