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
	"strings"
	"syscall"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/app"
	"github.com/monstercameron/ArticleFlux/internal/buildver"
	"github.com/monstercameron/ArticleFlux/internal/clientaddr"
	"github.com/monstercameron/ArticleFlux/internal/envfile"
	"github.com/monstercameron/ArticleFlux/internal/fluxcast"
	"github.com/monstercameron/ArticleFlux/internal/opml"
	"github.com/monstercameron/ArticleFlux/internal/seedread"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The single build constant, shared with the wasm client so the two cannot
// disagree about what version they are (§22.10). See internal/buildver.
const version = buildver.Version

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

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
	case "migrate":
		err = migrate(log, args)
	case "backup":
		err = backup(log, args)
	case "seed":
		err = seed(log, args)
	case "seed-reading":
		err = seedReading(log, args)
	case "poll":
		err = poll(log, args)
	case "fluxcast":
		err = fluxcastCmd(log, args)
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

func usage() {
	fmt.Fprint(os.Stderr, `articleflux — a self-hosted feed reader

  articleflux serve   [-addr host:port] [-db path] [-web dir] [-origin url] [-dev]
  articleflux init    -user name -password pass [-db path]
  articleflux adduser -user name -password pass [-role member] [-db path]
  articleflux passwd  -user name -password pass [-db path]
  articleflux migrate [-db path]
  articleflux backup  -out path [-db path] [-keep n]
  articleflux seed    [-db path] [-feeds url,url,...]
  articleflux seed-reading [-db path] [-focus word,word] [-read 0.6] [-seed 1]
  articleflux poll    [-db path]
  articleflux import  -file feeds.opml [-db path] [-fetch]
  articleflux export  [-file feeds.opml] [-db path]
  articleflux fluxcast [-db path] [-minutes 20] [-style balanced] [-rate 1.0] [-quickhits]
  articleflux version

serve defaults to 127.0.0.1:9000 and REQUIRES a login. Pass -dev to serve the
local account with no login; that is refused on any bind but loopback.

A fresh install needs "articleflux init" once — without an account there is
nothing to log in as, and serve says so at boot rather than at the login screen.
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
	// Metrics are always readable at /metrics with no configuration. This flag is
	// only about SENDING them somewhere, which is a network decision and so is
	// opt-in — the same position §18 takes about the model egress boundary.
	//
	// OTEL_EXPORTER_OTLP_ENDPOINT is the spelling every other OpenTelemetry
	// program uses; honouring it means a collector already configured for the
	// box needs nothing said twice.
	otlpEndpoint := fs.String("otlp-endpoint", envOr("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		"OpenTelemetry collector base URL, e.g. http://localhost:4318; empty keeps metrics local to /metrics")
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
	if err := fs.Parse(args); err != nil {
		return err
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
		OTLPEndpoint:      *otlpEndpoint,
		OTLPInsecure:      *otlpInsecure,
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
		Handler:           logging(log, *behindProxy, a.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: the gRPC tunnel is a long-lived WebSocket, and a write
		// deadline would sever it on a timer.
	}

	logPosture(log, dev, *addr, splitList(*origin), *behindProxy)

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
		f, existed, err := a.Service().Subscribe(ctx, sc, u, "", "")
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

	producer := fluxcast.NewRepo(a.Repo())
	produced, err := producer.Produce(ctx, sc, fluxcast.Options{
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

// printRundown is the deliverable Job 4 asks for: a running order a person
// can read and judge, not a struct dump. Title, then per segment its theme
// and stories (source, role, words, estimated minutes), then the total —
// exactly the shape the visual rundown (TODO 11.17) will eventually render,
// because internal/rundown is the one place both read from.
func printRundown(w io.Writer, p fluxcast.Produced, rate float64) {
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
			mins := float64(st.Words) / (150.0 * rateOrOne(rate)) // 11.3's own arithmetic
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

func rateOrOne(rate float64) float64 {
	if rate <= 0 {
		return 1
	}
	return rate
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
func logPosture(log *slog.Logger, dev bool, addr string, origins []string, behindProxy bool) {
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
		"trusts_forwarded_for", behindProxy)

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
// believed only behind `-behind-proxy` and why the leftmost entry is the one
// taken.
func clientAddr(r *http.Request, trustProxy bool) string {
	return clientaddr.Of(r, trustProxy)
}

func logging(log *slog.Logger, trustProxy bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The tunnel is one long-lived request; logging its duration would emit a
		// single line hours later and nothing in between.
		if r.URL.Path == "/grpc" {
			log.Info("tunnel open", "remote", clientAddr(r, trustProxy))
			next.ServeHTTP(w, r)
			log.Info("tunnel closed", "remote", clientAddr(r, trustProxy))
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Info("req", "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "ms", time.Since(start).Milliseconds(),
			"remote", clientAddr(r, trustProxy))
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

	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer f.Close()

	doc, err := opml.Parse(f)
	if err != nil {
		return fmt.Errorf("import %s: %w", *file, err)
	}
	log.Info("parsed", "title", doc.Title, "feeds", len(doc.Feeds), "folders", len(doc.Folders))

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

	// The OPML folders become categories, resolved once each rather than per
	// feed: a 144-feed export from FreshRSS has perhaps eight of them, and
	// resolving inside the loop would be 144 writes to create eight rows.
	//
	// This is the one place a category arrives as a NAME. Everywhere else the
	// client holds ids from ListFolders, which is why FolderByName lives on the
	// service rather than being how filing works generally.
	folderIDs := map[string]string{}
	for _, feed := range doc.Feeds {
		if feed.Folder == "" {
			continue
		}
		if _, ok := folderIDs[feed.Folder]; ok {
			continue
		}
		id, err := a.Service().FolderByName(ctx, sc, feed.Folder)
		if err != nil {
			// An unusable folder name must not cost the feeds inside it: they
			// import unfiled, which is recoverable, where skipping them is not.
			log.Warn("category skipped", "name", feed.Folder, "err", err)
			continue
		}
		folderIDs[feed.Folder] = id
	}

	var added, shared, failed int
	for _, feed := range doc.Feeds {
		_, existed, err := a.Service().SubscribeOnly(ctx, sc, feed.FeedURL, feed.Title, feed.SiteURL,
			folderIDs[feed.Folder])
		if err != nil {
			// One bad row must not abort a 144-feed migration.
			log.Warn("skipped", "title", feed.Title, "url", feed.FeedURL, "err", err)
			failed++
			continue
		}
		added++
		if existed {
			shared++
		}
	}
	log.Info("imported", "subscribed", added, "already_known", shared, "skipped", failed)

	if !*fetch {
		log.Info("feeds will fill in as the poller runs; pass -fetch to wait")
		return nil
	}
	res, err := a.Service().Refresh(ctx, sc, nil)
	if err != nil {
		return err
	}
	log.Info("fetched", "sources", res.Polled, "new_items", res.NewItems, "errors", len(res.Errors))
	for _, e := range res.Errors {
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
	feeds, _, err := a.Service().ListFeeds(ctx, sc)
	if err != nil {
		return err
	}

	doc := &opml.Document{Title: "ArticleFlux"}
	for _, f := range feeds {
		doc.Feeds = append(doc.Feeds, opml.Feed{
			Title: f.Title, FeedURL: f.FeedURL, SiteURL: f.SiteURL,
		})
	}

	out := io.Writer(os.Stdout)
	if *file != "" {
		fh, err := os.Create(*file)
		if err != nil {
			return err
		}
		defer fh.Close()
		out = fh
	}
	if err := opml.Write(out, doc); err != nil {
		return err
	}
	if *file != "" {
		log.Info("exported", "feeds", len(doc.Feeds), "file", *file)
	}
	return nil
}
