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
	"github.com/monstercameron/ArticleFlux/internal/opml"
)

const version = "0.1.0-dev"

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
	case "seed":
		err = seed(log, args)
	case "poll":
		err = poll(log, args)
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

  articleflux serve [-addr host:port] [-db path] [-web dir]
  articleflux seed  [-db path] [-feeds url,url,...]
  articleflux poll   [-db path]
  articleflux import -file feeds.opml [-db path] [-fetch]
  articleflux export [-file feeds.opml] [-db path]
  articleflux version

serve defaults to 127.0.0.1:9000.
`)
}

// commonFlags are shared by every subcommand that touches storage.
func commonFlags(fs *flag.FlagSet) *string {
	return fs.String("db", "articleflux.db", "path to the SQLite database")
}

func serve(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := commonFlags(fs)
	addr := fs.String("addr", "127.0.0.1:9000", "address to listen on")
	// bin/web, not web/: web/ holds the source index.html, and bin/web is the
	// assembled root the build produces. Serving the source directory would work
	// right up until it needed a file the build generates.
	webRoot := fs.String("web", filepath.Join("bin", "web"), "assembled web root (see ./scripts/make.ps1 wasm)")
	poll := fs.Duration("poll", 15*time.Minute, "how often to poll feeds in the background; 0 disables")
	user := fs.String("user", "cam", "username for the local account")
	pass := fs.String("password", "articleflux", "password for the local account, on first run only")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// DevMode serves the single local account with no login. It is tied to a
	// loopback bind rather than to a flag anyone can pass: an internet-facing
	// instance with DevMode on is an open reader, where whoever reaches the port
	// is the superadmin.
	dev := isLoopback(*addr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.Open(ctx, app.Config{
		DBPath:  *dbPath,
		WebRoot: *webRoot,
		Log:     log,
		Version: version,
		Commit:  commit(),
		DevMode: dev,
		// Loopback development wants to subscribe to a fixture feed served from
		// the same machine, which the SSRF guard blocks by design.
		AllowPrivateFeeds: dev,
		PollInterval:      *poll,
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

	if _, err := os.Stat(filepath.Join(*webRoot, "app.wasm")); err != nil {
		log.Warn("client not built — run ./scripts/make.ps1 wasm",
			"expected", filepath.Join(*webRoot, "app.wasm"))
	}

	a.StartPoller(ctx)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           logging(log, a.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: the gRPC tunnel is a long-lived WebSocket, and a write
		// deadline would sever it on a timer.
	}

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

func seed(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	dbPath := commonFlags(fs)
	list := fs.String("feeds", "", "comma-separated feed URLs; empty uses the starter set")
	user := fs.String("user", "cam", "username for the local account")
	pass := fs.String("password", "articleflux", "password, on first run only")
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

func logging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The tunnel is one long-lived request; logging its duration would emit a
		// single line hours later and nothing in between.
		if r.URL.Path == "/grpc" {
			log.Info("tunnel open", "remote", r.RemoteAddr)
			next.ServeHTTP(w, r)
			log.Info("tunnel closed", "remote", r.RemoteAddr)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Info("req", "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "ms", time.Since(start).Milliseconds())
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
	pass := fs.String("password", "articleflux", "password, on first run only")
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

	sc, err := a.EnsureDevUser(ctx, "cam", "articleflux")
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
