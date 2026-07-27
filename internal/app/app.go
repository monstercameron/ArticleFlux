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
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	"github.com/monstercameron/ArticleFlux/internal/analyze"
	"github.com/monstercameron/ArticleFlux/internal/assetproxy"
	"github.com/monstercameron/ArticleFlux/internal/authz"
	"github.com/monstercameron/ArticleFlux/internal/buildver"
	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
	"github.com/monstercameron/ArticleFlux/internal/connpolicy"
	"github.com/monstercameron/ArticleFlux/internal/derive"
	"github.com/monstercameron/ArticleFlux/internal/discover"
	"github.com/monstercameron/ArticleFlux/internal/events"
	"github.com/monstercameron/ArticleFlux/internal/extract"
	"github.com/monstercameron/ArticleFlux/internal/favicon"
	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/idem"
	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/jobs"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
	"github.com/monstercameron/ArticleFlux/internal/obs"
	"github.com/monstercameron/ArticleFlux/internal/pageproxy"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/pipeline"
	"github.com/monstercameron/ArticleFlux/internal/ratelimit"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/render"
	"github.com/monstercameron/ArticleFlux/internal/reqid"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/skew"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/telemetry"
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

	// Profiling mounts /debug/pprof and turns the block and mutex profilers on.
	//
	// Off by default and gated in cmd/articleflux the same way -dev is —
	// loopback bind AND not behind a proxy — because the reasons are the same.
	// See internal/app/pprof.go for what the surface costs and discloses.
	Profiling bool

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

	// OTLPEndpoint enables OpenTelemetry export to a collector, e.g.
	// "http://localhost:4318". Empty means metrics stay local and are readable at
	// /metrics; nothing is sent anywhere.
	//
	// Default-off is the same position SECURITY.md takes about the model egress
	// boundary: a reader that ships telemetry to an endpoint nobody configured is
	// making a network decision on the operator's behalf.
	OTLPEndpoint string
	// OTLPInsecure sends to that endpoint over plain HTTP, for a collector on the
	// same host.
	OTLPInsecure bool

	// BehindProxy states that something terminates TLS and forwards to this
	// process.
	//
	// It is the operator asserting a fact the server cannot observe, and it gates
	// which forwarded headers are believed. Both of them are dangerous to trust
	// unconditionally, in different ways:
	//
	//   X-Forwarded-Proto  decides whether HSTS is emitted (see overTLS).
	//                      Believed from anyone, it lets a client pin
	//                      Strict-Transport-Security for a year against a
	//                      hostname that was never served over TLS.
	//   X-Forwarded-For    decides who every per-client control counts (see
	//                      trueClientAddr, TODO 7.3d). Believed from anyone, it
	//                      lets an attacker mint a fresh limiter bucket per
	//                      attempt — which is worse than the shared bucket it
	//                      replaces.
	//
	// What makes the assertion safe is the systemd unit's loopback bind: nothing
	// but the proxy can reach the socket, so nothing but the proxy can set the
	// headers.
	BehindProxy bool

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
	//
	tts *tts.Client
	// speak is what the listening path calls, and is normally the same object as
	// tts above. It exists as a separate, narrower field so a test can reach the
	// half of /speech that lies PAST the gates — everything from the script
	// choice to the cache key to the bytes — which was otherwise reachable only
	// by a request that ends in a paid call to OpenAI, and was therefore covered
	// by nothing. See speech.go's `speaker`.
	speak speaker
	// digest condenses an article into about a minute of spoken summary before
	// the voice reads it. Nil-safe: absent means the digest preference has
	// nothing to do and /speech reads the whole article, which is the correct
	// degradation — longer than asked for, and not silence.
	digest *smart.Digest
	// podcast rewrites an article as one slot of a continuous broadcast, handing
	// over from whatever was played before it (§19). Nil-safe on the same terms
	// as digest: absent means the preference has nothing to do and the reader
	// hears the article or its digest, which is the right degradation — a queue
	// instead of a programme, not silence.
	podcast *smart.Podcast
	// speechKey seals the listening tickets in an <audio src> (see SpeechURL).
	//
	// Its own key rather than assetKey, which is the one an obvious edit would
	// reach for: assetKey only exists when the image proxy is on, and the voice
	// has nothing to do with the image proxy. Sharing it would make turning
	// pictures off silently turn listening off too.
	speechKey []byte
	// The observability surface (§22.11): a ring of recent log records and
	// per-RPC latency, both in memory and both bounded. A self-hosted reader has
	// no operator, so these are how the person running it answers "why did that
	// feed stop working" without a terminal.
	// bus carries §20.3's live updates: the poll publishes, connected clients
	// subscribe over EventService.WatchEvents. Always present — it costs a map
	// until somebody connects, and making it optional would mean every publisher
	// carrying a nil check for a saving nobody can measure.
	bus *events.Bus
	// streamCap bounds how many streams one credential holds at once (TODO P1).
	// On the App rather than inside the chain so §9's status screen can report
	// refusals — a cap nobody can see is one that gets discovered by a reader
	// whose second tab stopped updating.
	streamCap *ratelimit.Concurrent
	ring      *obs.Ring
	lat       *obs.Latency
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
	// palettes writes themes: from a phrase the reader typed, and from what they
	// actually read (§20.16.3). Non-nil on every instance, like the translator —
	// the drift has a deterministic answer that needs no key, so an instance with
	// no credential still serves the whole surface.
	palettes *smart.Palettes
	// pool drains the durable job queue (§22.7). Always non-nil; Start is what
	// decides whether it actually runs, so a test can enqueue and drain by hand.
	pool *jobs.Pool
	// deriver rebuilds the interest layer from the engagement log (§18, D18).
	//
	// It is the reason the pool exists at all on a single-user instance: every
	// affinity number, topic and ranked homepage row is produced here and nowhere
	// else, so an unwired pool means `engagements` accumulates forever and
	// nothing ever reads it. That was the state before this field existed.
	deriver *derive.Service
	// analyzer runs the shared per-item pass (§27.2a, M29): one analysis per
	// ITEM for the whole instance, upstream of anything per-user.
	//
	// It is what makes a category exist at all. Before this field the pipeline,
	// the lexicon and the Smart+ classifier were all built and tested and nothing
	// called them — `internal/pipeline` had no importer outside its own tests, so
	// every article in the database was unclassified no matter how good the
	// classifier was.
	analyzer *analyze.Service
	// tel is the metric and trace surface (§22.11). Always non-nil — every call
	// site records unconditionally, so a nil here would be a panic on the first
	// request rather than a quiet absence of data.
	tel *telemetry.Telemetry
	// policy is the per-method capability map the authz interceptors enforce
	// (§7.5). Always non-nil: a nil policy would make every Check unreachable and
	// every RPC allowed, which is precisely the state this field exists to end.
	policy  *authz.Map
	grpc    *grpc.Server
	handler http.Handler
	stopPo  chan struct{}
	// lastDerive is the low-water mark for ScopesToDerive, under deriveMu.
	//
	// Held in memory rather than persisted: on restart it starts one window back,
	// which re-enqueues a derivation that the dedupe key collapses anyway. A
	// persisted cursor would buy nothing and could skip work if it were ever
	// written before the job succeeded.
	//
	// The mutex is not defending against the current call pattern, where serve
	// runs one DeriveDue at boot and the poller goroutine owns it afterwards. It
	// is there because DeriveDue is EXPORTED: the cursor is the one piece of
	// mutable state on App that a second caller could reasonably touch, and a torn
	// read of it skips a window of engagements silently.
	deriveMu   sync.Mutex
	lastDerive time.Time
	// lastNudge rate-limits engagement-triggered derivations (see NudgeDerive). Under
	// the same mutex as lastDerive because both are the deriver's schedule; two locks
	// for one concern is how they end up being taken in different orders.
	lastNudge time.Time
	// closing stops NudgeDerive from starting work during shutdown, and nudges tracks
	// the goroutines already started so Close can wait for them.
	//
	// Both exist because the first version logged "database is closed" on the way down:
	// a nudge fired, its goroutine was still reaching for the writer, and Close got to
	// db.Close() first. Harmless — the work was a recomputable ranking — but a shutdown
	// that reports a database error is one that gets investigated, and a real fault at
	// shutdown would then be indistinguishable from this noise.
	closing bool
	nudges  sync.WaitGroup
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
	bus := events.New()

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

	// Telemetry before anything that might want to record. New never fails for a
	// telemetry-only reason — a bad collector address costs traces, not the
	// reader — so an error here means the instruments themselves could not be
	// created, which is a programming error rather than a runtime condition.
	tel, err := telemetry.New(ctx, telemetry.Config{
		ServiceName:  "articleflux",
		Version:      cfg.Version,
		OTLPEndpoint: cfg.OTLPEndpoint,
		Insecure:     cfg.OTLPInsecure,
		Log:          cfg.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("app: telemetry: %w", err)
	}

	// Every outbound fetch, from all seven callers, through the one guard they
	// already share. Installed before any client is constructed.
	//
	// This is the metric that answers "is the internet broken, or is it us?" —
	// a reader whose feeds stop updating looks identical whether the publisher is
	// down, the DNS is wrong, or the SSRF guard is refusing an address, and
	// before this there was no way to tell from outside.
	netguard.Observer = func(purpose string, d time.Duration, status int, err error) {
		ctx := context.Background()
		attrs := metric.WithAttributes(
			attribute.String("purpose", purpose),
			telemetry.StatusClass(status),
			telemetry.Outcome(err),
		)
		tel.Instruments.EgressRequests.Add(ctx, 1, attrs)
		tel.Instruments.EgressDuration.Record(ctx, d.Seconds(), attrs)
	}

	a := &App{cfg: cfg, db: db, repo: repo, svc: svc, log: cfg.Log, bus: bus,
		streamCap: ratelimit.NewConcurrent(maxStreamsPerCaller),
		ring:      ring, lat: obs.NewLatency(), tunnels: &obs.Tunnels{}, tel: tel,
		policy: grpcsrv.DefaultPolicy(),
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
	a.palettes = smart.NewPalettes(a.llm, a.settings)

	// The interest layer (§18) and the pool that runs it (§22.7).
	//
	// Registered here rather than in StartWorkers because handlers must be in
	// place before any worker starts — jobs.Pool documents its handler map as
	// write-before-Start precisely so it needs no lock, and building the pool in
	// one place and populating it in another is how that invariant gets broken by
	// a later edit.
	//
	// Only `derive` is registered, and the other kinds in DefaultCaps are caps for
	// work that has no handler yet. That is safe only because nothing enqueues
	// them: an unregistered kind is FAILED and retried five times before being
	// declared dead (jobs.Pool.run), not skipped. So anything that starts
	// enqueueing a kind must register it in the same change, or the queue grows a
	// column of dead rows that look like a broken feature.
	//
	// Recommend is the near-term one, and it needs a discovery Validator — that is
	// /discover's build, not this one.
	// The deriver, with the Smart+ tier attached.
	//
	// Attached unconditionally, and gated INSIDE: smart.Interest checks llm.Configured on
	// every call and returns an error when there is no key, which derive treats as "no
	// Smart+ this time". Wiring it only when a key exists at boot would mean a key pasted
	// into the Settings screen did nothing until the next restart — the same mistake the
	// smartKey closure above exists to avoid.
	a.deriver = derive.New(repo, cfg.Log).
		WithSmartPlus(smart.NewInterest(a.llm, a.settings))
	a.pool = jobs.New(repo, jobs.Options{
		Log: cfg.Log,
		// The job queue is where a self-hosted instance actually breaks: a
		// derivation that started failing is invisible until somebody notices the
		// homepage stopped re-ranking, which is weeks. `job.Kind` is a closed set
		// from the schema, so it is safe as a label.
		OnResult: func(ctx context.Context, kind store.JobKind, d time.Duration, err error) {
			attrs := metric.WithAttributes(
				attribute.String("kind", string(kind)),
				telemetry.Outcome(err),
			)
			a.tel.Instruments.JobRuns.Add(ctx, 1, attrs)
			a.tel.Instruments.JobDuration.Record(ctx, d.Seconds(), attrs)
			if err != nil {
				a.tel.Instruments.Errors.Add(ctx, 1, metric.WithAttributes(
					attribute.String("subsystem", "jobs"),
					attribute.String("class", string(kind)),
				))
			}
		},
	})
	a.pool.Handle(store.JobDerive, a.deriver.Handle)

	// The classification pipeline (§27, M29).
	//
	// The lexicon is compiled once at boot and shared by every job: it is
	// immutable after Compile and safe for concurrent use, and compiling it per
	// batch would repeat ~1,700 term insertions on every poll for no reason.
	//
	// MustCompile rather than a handled error, matching `classify.MustCompile`'s
	// argument: the shipped taxonomy is a compile-time artefact, so an instance
	// that cannot build it is misbuilt rather than misconfigured, and failing at
	// boot puts that in front of whoever broke it instead of in front of a reader
	// whose chips stopped appearing.
	lexiconSet := classify.MustCompile(lexicon.Categories())
	a.analyzer = analyze.New(repo, pipeline.New(lexiconSet, classify.DefaultStrategy(), cfg.Log), cfg.Log).
		// Wired unconditionally, exactly as the deriver's Smart+ half is, and for
		// the same reason: the key is a SETTING that can be pasted into the
		// Settings screen while the process runs. Attaching this only when a key
		// exists at boot would mean a key added later did nothing until restart.
		// **Being wired is not consent** — smart.Classifier.Available checks the
		// owner's `smart.classify` setting on every batch and defaults to off.
		WithSmartPlus(
			smart.NewClassifier(a.llm, a.settings, lexiconSet),
			pipeline.DefaultPolicy,
		)
	a.pool.Handle(store.JobAnalyze, a.analyzer.Handle)
	// Close the interest loop inside the session that produced the signal, rather than
	// on the next poll. See reader.WithSignalHook for why the callback is a func, and
	// NudgeDerive for the rate limit that makes it safe to call on every batch.
	svc.WithSignalHook(a.NudgeDerive)
	// The Smart+ opt-in goes to ForceDerive rather than NudgeDerive: the reader is
	// waiting on this one, so it must not be dropped by the rate limit that exists to
	// tame the signal path above.
	svc.WithRankPrefHook(a.ForceDerive)
	// And the live-update announcement (§20.3). Wired here rather than inside
	// reader.New for the reason the signal hook is: the service layer must not
	// know how anybody is told, or the sync surface and the offline-pack channel
	// start behaving differently from the web UI.
	svc.WithIngestHook(a.onIngested)
	// The poller's own logger, and it has to be THIS one rather than the
	// default.
	//
	// `reader.pollOneRecovered` catches a panic from a source's poll — the path
	// that parses bytes and headers from an arbitrary publisher — and reports it.
	// Left unwired it falls back to `slog.Default()`, which on this application
	// means plain stderr: the line would never reach the ring buffer behind
	// Settings → Activity, which on a self-hosted box is the only place an
	// operator ever looks. A crash report nobody can see is a crash report that
	// did not happen.
	//
	// Wired here rather than at `reader.New` above because `cfg.Log` is replaced
	// by the reqid-aware handler further up, after the service was constructed.
	svc.WithLogger(cfg.Log)
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
	// The same object, seen through the narrow seam the listening path uses.
	// Assigned here rather than derived at the call site so there is exactly one
	// line to look at when asking "can these two ever disagree".
	a.speak = a.tts
	// Beside the audio it feeds, and cached for the same reason: the digest is
	// the expensive half of listening to a long article, and re-summarising a
	// paragraph that cannot have changed is money spent on an identical answer.
	a.digest = smart.NewDigest(a.llm, a.settings,
		filepath.Join(filepath.Dir(cfg.DBPath), "digest-cache"))
	// Its own directory rather than the digest's, because the two are keyed
	// differently — a digest is per article, a broadcast segment is per ORDERED
	// PAIR of them — and one cache holding two key shapes is one nobody can
	// reason about the size of.
	a.podcast = smart.NewPodcast(a.llm, a.settings,
		filepath.Join(filepath.Dir(cfg.DBPath), "podcast-cache"))
	// Unconditionally, unlike the asset key below: listening does not depend on
	// the image proxy being on, and an instance that turned pictures off must
	// not lose its voice as a side effect. A failure here is a warning rather
	// than fatal for the same reason the asset key's is — the reader still has
	// the browser's own synthesiser, which is the default anyway.
	if k, kerr := loadOrCreateKeyFile(filepath.Dir(cfg.DBPath), "speech.key"); kerr != nil {
		cfg.Log.Warn("Smart+ voice disabled: cannot open its ticket key", "err", kerr)
	} else {
		a.speechKey = k
	}

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
			// The page proxy REFUSES to run on the app's own origin (D20).
			//
			// §10.1b's Rule 1 is not negotiable and it was not enforced: with
			// `ProxyOrigin` empty this served sanitized-but-hostile third-party
			// HTML from the origin that holds the session, one sanitizer bypass
			// away from reading it. The field's own comment said that was "NOT
			// correct" and nothing stopped it, which is a hazard documented into
			// existence rather than out of it.
			//
			// The separate hostname is the control that actually holds — the
			// browser's origin boundary doing the work instead of a CSP string
			// somebody has to keep correct forever — so its absence disables the
			// feature rather than downgrading it. There is no same-origin
			// fallback, because a fallback is the thing the rule forbids.
			//
			// IMAGES are deliberately not held to this. The asset proxy serves
			// bytes with `nosniff` and an image content type; it cannot execute,
			// and the origin boundary buys correspondingly little. Pages are
			// HTML, which is the whole difference.
			switch {
			case !cfg.ProxyPages:
				// off; nothing to say
			case strings.TrimSpace(cfg.ProxyOrigin) == "":
				cfg.Log.Warn("the page proxy is OFF: it needs its own origin",
					"why", "proxied HTML must not share an origin with the session (§10.1b)",
					"fix", "set -proxy-origin https://proxy.<your-host> and point it at this server")
			default:
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
	case cfg.ProxyPages && strings.TrimSpace(cfg.ProxyOrigin) == "":
		// Already warned about above, where the decision is made. Nothing here.
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
	// Refuse new nudges, then wait for the ones already in flight.
	//
	// Order matters and is the whole point: setting the flag first means no goroutine
	// can be registered after Wait has been entered, so Wait cannot miss one and cannot
	// block on an arrival that is still coming.
	a.deriveMu.Lock()
	a.closing = true
	a.deriveMu.Unlock()
	a.nudges.Wait()

	// Before the database closes, and it WAITS — jobs.Pool.Stop blocks until every
	// worker has finished its current job. Closing the db underneath a running
	// derivation would surface as "database is closed" errors on a job that then
	// retries against a process that is going away.
	//
	// Unconditional: Stop on a pool that was never started returns immediately,
	// since there are no workers to wait for.
	if a.pool != nil {
		a.pool.Stop()
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
	// Flush telemetry AFTER the server has drained, so the spans and metrics
	// describing the last requests are included, and on a bounded context of its
	// own — an unreachable collector must not hold the shutdown open until
	// systemd loses patience and SIGKILLs a process mid-write to SQLite.
	if a.tel != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := a.tel.Shutdown(ctx); err != nil {
			a.log.Warn("telemetry did not flush cleanly", "err", err)
		}
		cancel()
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
			// The §20.7 limits (TODO 7.3d), and this position is the whole point
			// of having them.
			//
			// BEFORE authorization, which is the ordering a test caught us
			// getting backwards. Refusing an unauthorised caller first sounds
			// tidier — nothing they cannot do should cost them anything — but it
			// hands the one caller who cannot authenticate an UNLIMITED channel:
			// every call is rejected before it reaches the limiter, so the flood
			// is never counted and never shed. A limiter that only limits
			// callers who got past authorization is a limiter that protects the
			// server from its own users and from nobody else.
			//
			// The cost of this order is that a denied caller does consume their
			// own bucket. That is per-caller and it is the point.
			//
			// Before idem for a separate reason: reserving an idempotency key for
			// a call that is then refused burns it, and the client retries with
			// the same key and meets its own reservation.
			a.rateLimitUnary(),
			// Authorization (§7.5), and its position is the design.
			//
			// AFTER skew, because a client too old to understand the answer should
			// be told to reload rather than told it lacks a permission. AFTER the
			// limiter, per above. BEFORE idem, because a denied request that
			// reserved an idempotency key burns one the legitimate client is
			// about to retry with.
			//
			// It is one enforcement point for the whole surface. Before this, the
			// model was "each handler remembers", and `ListLogs` did not.
			grpcsrv.AuthzUnary(a.policy, a.scopeFromContext, a.log, a.recordDenial),
			// The missing half of a contract already in use: the client stamps a
			// key onto every mutating RPC and nothing on the server read one
			// (TODO 8c.15). Survivable only because every queued mutation sets
			// an absolute value, so applying it twice lands on the same state —
			// the first relative operation would have double-applied silently.
			idem.Unary(a.repo, a.scopeFromContext),
			func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
				handler grpc.UnaryHandler) (any, error) {
				// The full method is `/articleflux.v1.ReaderService/ListItems`; the last
				// segment is what a person reads.
				name := info.FullMethod
				if i := strings.LastIndexByte(name, '/'); i >= 0 {
					name = name[i+1:]
				}

				// A span around the handler, so a slow call can be opened up
				// rather than merely counted. The method name is bounded — it
				// comes from the generated service descriptor, not from the
				// caller — so it is safe as both a span name and an attribute.
				ctx, span := a.tel.Tracer.Start(ctx, "rpc."+name,
					trace.WithSpanKind(trace.SpanKindServer),
					trace.WithAttributes(attribute.String("rpc.method", name)))
				defer span.End()

				start := time.Now()
				res, err := handler(ctx, req)
				elapsed := time.Since(start)

				a.lat.Observe(name, elapsed, err != nil)

				// The gRPC status code, not the error text: `codes.NotFound` is
				// six possible values, an error string is unbounded and quotes
				// article titles back at you.
				attrs := metric.WithAttributes(
					attribute.String("rpc.method", name),
					attribute.String("rpc.code", status.Code(err).String()),
					telemetry.Outcome(err),
				)
				a.tel.Instruments.RPCRequests.Add(ctx, 1, attrs)
				a.tel.Instruments.RPCDuration.Record(ctx, elapsed.Seconds(), attrs)
				if err != nil {
					span.RecordError(err)
					span.SetStatus(otelcodes.Error, status.Code(err).String())
				}
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
		// Authorization for STREAMS, which the unary chain above does not see.
		//
		// This is not belt-and-braces. A stream RPC bypasses UnaryServerInterceptor
		// entirely, so with only the unary chain wired, WatchEvents — the one
		// long-lived per-scope subscription in the API — would be the single
		// endpoint on the server with no authorization at all, on a build whose
		// tests and metrics all reported authorization as enforced.
		// The streaming chain, which used to be authorization alone — see
		// streamchain.go for what was missing and why the tunnel's connection cap
		// was not standing in for it.
		a.streamChain(),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             keepaliveMinTime,
			PermitWithoutStream: true,
		}),
	)
	pb.RegisterReaderServiceServer(a.grpc,
		grpcsrv.NewReaderServer(a.svc, a.scopeFromContext).
			WithAssetProxy(a.AssetURL).
			WithPageProxy(a.PageURL).
			WithLiveView(a.StreamURL, a.ScrollLive).
			WithSpeech(a.SpeechURL))
	pb.RegisterSystemServiceServer(a.grpc,
		grpcsrv.NewSystemServer(a.cfg.Version, a.cfg.Commit, a.db).
			WithObservability(a.repo, a.ring, a.lat, a.cfg.PollInterval, a.scopeFromContext).
			// The broadcast's music beds (§19), beside the web root they ship in.
			// Empty when this instance serves no static files, which answers the
			// picker with silence rather than four tracks it cannot send.
			WithAudio(audioDirOf(a.cfg.WebRoot)))
	// The login surface. Registered unconditionally, including in DevMode: an
	// instance that starts on a laptop and later gets a domain must not need a
	// different binary, and a client that can always call Login is a client with
	// one code path instead of two.
	pb.RegisterAuthServiceServer(a.grpc,
		grpcsrv.NewAuthServer(a.repo, a.scopeFromContext, a.log, a.cfg.DevMode).
			WithLoginMetrics(func(ctx context.Context, outcome store.LoginOutcome) {
				a.tel.Instruments.LoginAttempts.Add(ctx, 1,
					metric.WithAttributes(attribute.String("outcome", string(outcome))))
			}))
	// Smart+ (§10.5, §18). Registered on every instance, including one with no
	// API key: the screen that says "Smart+ is not configured" is served by
	// this, and an unconfigured instance that answered Unimplemented would look
	// like a version mismatch instead of a setting nobody has filled in.
	pb.RegisterSmartServiceServer(a.grpc,
		grpcsrv.NewSmartServer(a.settings, a.llm, a.translator, a.scopeFromContext, a.log).
			// The voice's spend, beside the model's (TODO P3). Nil on an instance
			// with no key, which reports zeroes rather than nothing.
			WithSpeechMeter(a.tts).
			// Theming (§20.16.3). The repo comes with it because the drift is
			// derived from this reader's topics, and the consent preference for a
			// model-written palette is read from the same place.
			WithTheming(a.palettes, a.repo))
	// Live updates (§20.3). The only streaming surface in the API: it holds a
	// goroutine and a subscription for as long as a tab is open, which is why it
	// is its own service rather than another method on the reader.
	pb.RegisterEventServiceServer(a.grpc,
		grpcsrv.NewEventServer(a.bus, a.scopeFromContext, a.log))

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
	// The scrape endpoint (§22.11).
	//
	// UNAUTHENTICATED, like /healthz and /readyz beside it, and that is a
	// decision rather than an omission: a scraper is a machine with no session,
	// and a metrics endpoint behind a login is a metrics endpoint nobody
	// collects. What makes it safe is the discipline in internal/telemetry —
	// every attribute is a bounded label, so there is no feed URL, article title
	// or username to read here. It is counts and durations.
	//
	// It is still an information disclosure in the ordinary sense: request rates
	// say when somebody reads. Bind to loopback and let the reverse proxy decide
	// who reaches it, exactly as with the rest of the surface.
	mux.Handle("/metrics", a.tel.Handler)
	// The profiling surface (internal/app/pprof.go). Registered ONLY when asked
	// for, unlike /speech and /asset beside it which register unconditionally
	// and refuse inside.
	//
	// That difference is deliberate. Those two gate on a per-user preference and
	// answer 501 or 403 so that "why is listening missing?" is answerable from
	// the response. There is no reader-facing question this endpoint answers, so
	// there is nothing to gain from its 404 being informative — and an operator
	// who did not ask for it is better served by the path simply not existing.
	if a.cfg.Profiling {
		registerPprof(mux)
		enableProfiling()
		a.log.Warn("pprof is enabled at /debug/pprof — block and mutex profiling are on, " +
			"which costs measurable time; do not leave this on")
	}
	mux.HandleFunc("/favicon", a.serveFavicon)
	// Registered unconditionally, and gated inside: the handler itself reports
	// 501 with no key and 403 without the per-user opt-in. Registering it only
	// when configured would make "why is listening missing?" answerable only by
	// reading the server logs.
	mux.HandleFunc("/speech", a.serveSpeech)
	// Registered unconditionally and gated inside, for the same reason /speech
	// is: "why are the images missing?" should be answerable from the response
	// rather than only from the server log.
	// Both behind one per-client limiter (TODO 6.14 and 6.15, owed to 7.3d).
	// These are the two endpoints that make this server fetch on a reader's
	// behalf, and neither carries a session — see limitProxy for what that
	// leaves to key on and why it is only worth keying on now.
	proxyLimit := a.limitProxy()
	// And behind the ORIGIN gate, which is the other side of D20.
	//
	// "Proxied pages are never served from the app's origin" is only a control
	// if it is enforced in both directions: minting URLs that point at
	// `proxy.<host>` does nothing if the same paths still answer on the app's
	// hostname, because an attacker hands the reader the app-host version and
	// the browser gives it the app's origin — which is the exact thing the
	// split exists to prevent, reached by editing a URL.
	onProxyHost := a.limitToProxyHost()
	mux.Handle("/asset", onProxyHost(proxyLimit(http.HandlerFunc(a.serveAsset))))
	mux.Handle("/p", onProxyHost(proxyLimit(http.HandlerFunc(a.servePage))))
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

		// One new item, on demand, so a test can assert that something arriving
		// on the SERVER reaches an open screen (TODO F2).
		//
		// Ingest rather than a fabricated event: the point is the whole path —
		// the item lands, delivery gives every subscriber a state row, the poll
		// path publishes, the stream carries it, the pump invalidates and the
		// list refetches. Publishing a bare event would test the transport and
		// skip the reason it exists.
		//
		// DevMode only, beside reset-state and for the same reason: an
		// unauthenticated "write me an article" endpoint is not something a
		// production instance should own.
		mux.HandleFunc("/debug/ingest-one", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			sc, err := a.devScope(r.Context())
			if err != nil {
				http.Error(w, "no local account", http.StatusPreconditionFailed)
				return
			}
			feeds, err := a.repo.ListFeeds(r.Context(), sc)
			if err != nil || len(feeds) == 0 {
				http.Error(w, "no subscriptions to ingest into", http.StatusPreconditionFailed)
				return
			}
			source := feeds[0].SourceID
			guid := "debug-" + idgen.New()
			ing, err := a.repo.IngestItems(r.Context(), source, []store.IngestItem{{
				GUID: guid, URL: "https://example.invalid/" + guid,
				Title:       "A new article arrived while you were reading",
				ContentHTML: "<p>Written by /debug/ingest-one.</p>",
				PublishedAt: time.Now().UTC(),
				WordCount:   6,
			}})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// The same announcement a poll makes, through the same hook — so
			// what this exercises is the production path rather than a private
			// one that happens to look like it.
			a.onIngested(source, ing.NewIDs)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "ingested", ing.New)
		})
	}

	// Published scopes (§7.8b, TODO F29). Public by design and rate-limited on
	// its own budget: the address is the only credential, so an endpoint anybody
	// can poll needs a limit that does not come out of a reader's.
	mux.Handle("/pub/", a.shareLimit(http.HandlerFunc(a.servePublicShare)))

	if a.cfg.WebRoot != "" {
		mux.Handle("/", a.securityHeaders(a.static(a.cfg.WebRoot)))
	}
	// Metrics for every HTTP response, outermost so it sees the status the client
	// actually got — including the ones written by middleware that never reaches
	// a route.
	//
	// The client address is resolved OUTSIDE even that (TODO 7.3d), because
	// everything else in the chain — the tunnel's abuse caps, the login limiter,
	// the lockout ledger — asks who the client is, and behind a reverse proxy
	// the transport address answers "nginx" for all of them. Inert unless
	// -behind-proxy says a proxy is there to be believed.
	a.handler = a.trueClientAddr(a.httpMetrics(mux))
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
		case strings.HasSuffix(p, ".webmanifest"):
			// Not in Go's builtin extension table, so FileServer would sniff it as
			// text/plain. Browsers are lenient about this and Lighthouse is not,
			// which makes it exactly the kind of defect that is invisible until
			// somebody runs an audit and is told the app is not installable
			// (§20.24).
			w.Header().Set("Content-Type", "application/manifest+json")
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
		// The manifest is here for a different reason from the two above it: it is
		// tiny and it changes rarely, but a browser re-reads it to decide whether an
		// installed app is still the app it installed — so a cached one is an
		// install that keeps whatever name, colour or icon it was first shown, and
		// no amount of rebuilding changes it.
		if strings.HasSuffix(p, ".wasm") || strings.HasSuffix(p, ".js") ||
			strings.HasSuffix(p, ".webmanifest") || p == "/" {
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

	// Authorization coverage, first: an instance whose policy does not cover its
	// own API is one where some feature is silently 403 for everybody, and that
	// is worth refusing to start over rather than discovering from a bug report.
	if err := a.checkPolicyCoverage(); err != nil {
		problems = append(problems, err)
	}

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

	// The wasm bundle, not just index.html.
	//
	// index.html alone passing is the same failure the check above names, one
	// step further in: the server answers 200 for the page, the health check is
	// green, and the reader gets a blank screen because the module the page
	// loads is not there. `make wasm` producing nothing is a normal state of a
	// tree mid-build.
	if a.cfg.WebRoot != "" {
		var found bool
		for _, name := range []string{"app.wasm", "app.wasm.gz"} {
			if _, err := os.Stat(filepath.Join(a.cfg.WebRoot, name)); err == nil {
				found = true
				break
			}
		}
		if !found {
			problems = append(problems, fmt.Errorf(
				"web root %q has an index.html but no app.wasm — the page will load "+
					"and then show nothing (run: make wasm)", a.cfg.WebRoot))
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

	problems = append(problems, a.preflightCredentials(ctx)...)

	return errors.Join(problems...)
}

// preflightCredentials checks the stored secrets an operator configured.
//
// # No network, on purpose
//
// §7.7 asks for "LLM keys well-formed, IMAP reachable". The first is a shape
// check and costs nothing. The second is a TCP connection and a login to
// somebody else's mail server, and it is deliberately NOT done here: a provider
// having a bad five minutes would stop the whole reader from starting, which
// is a far worse outcome than newsletters being late. Reachability belongs on
// the poller, which already records `last_error` per mailbox and backs off.
//
// What IS checked is the thing an operator can only discover by waiting: a
// mailbox whose credential can never be decrypted.
func (a *App) preflightCredentials(ctx context.Context) []error {
	var problems []error

	// A pasted Smart+ key that is not a key. The shape check exists at the RPC
	// that sets it, so this catches a key that arrived some other way — a
	// restored database, an environment variable, a hand-edited row — and it
	// catches it at boot rather than at the first Smart+ click, which may be
	// weeks later and will look like the feature is broken.
	if a.settings != nil && a.settings.CanStoreSecrets() {
		if key, err := a.settings.SystemSecret(ctx, store.KeyOpenAIAPIKey); err == nil {
			if key != "" && !strings.HasPrefix(key, "sk-") {
				problems = append(problems, errors.New(
					"the stored Smart+ API key does not look like one (they begin with sk-); "+
						"clear it in Settings › Smart+ or set OPENAI_API_KEY"))
			}
		} else if !errors.Is(err, store.ErrNoSetting) {
			// It is STORED and will not decrypt, which means the encryption key
			// changed or was regenerated. Reported rather than ignored: treating
			// it as "not configured" invites someone to paste the key again,
			// which works, and quietly destroys whatever else the old key
			// protected.
			problems = append(problems, fmt.Errorf(
				"the stored Smart+ API key cannot be decrypted, so the encryption key "+
					"has changed — restore secrets.key or clear the setting: %w", err))
		}
	}

	// Mailboxes whose passwords are unreadable.
	//
	// A mailbox is useless without its credential, and this failure is
	// completely silent otherwise: the poller runs, fails to decrypt, records an
	// error on a row nobody is looking at, and newsletters simply stop.
	// Counted through the repository rather than with a query here: guard 1
	// exists so one place understands the schema, and a boot check is exactly
	// the sort of code that quietly acquires its own SQL.
	if mailboxes, err := store.NewMailboxRepo(a.db, nil).CountMailboxes(ctx); err == nil {
		if mailboxes > 0 && !a.settings.CanStoreSecrets() {
			problems = append(problems, fmt.Errorf(
				"%d newsletter mailbox(es) are configured but this instance has no "+
					"encryption key, so their passwords can never be read — restore "+
					"secrets.key or set ARTICLEFLUX_SECRET_KEY", mailboxes))
		}
	}

	return problems
}

// StartWorkers launches the job pool.
//
// Separate from StartPoller because the two are independent: a `serve` with
// -poll=0 still has an interest layer to rebuild when the reader marks something
// read, and a test wants the pool without a fetch loop. Calling it twice would
// start two sets of workers, so it is called once, from serve.
func (a *App) StartWorkers(ctx context.Context) {
	a.deriveMu.Lock()
	a.lastDerive = time.Now().UTC().Add(-derive.Window)
	a.deriveMu.Unlock()
	a.pool.Start(ctx)

	// The analysis backfill (§27.9).
	//
	// Without it the classifier only ever sees articles that arrive after the
	// upgrade, and every item already in the database stays unlabelled forever —
	// so the feature ships looking broken to precisely the reader who has the most
	// data. It is rate-limited and self-limiting (see analyze.Backfill), sweeps
	// newest-first, and stops on its own once everything is current.
	//
	// Its own goroutine because it is a ticker that outlives this call, and it
	// ends with the context the pool ends with.
	if a.analyzer != nil {
		go a.analyzer.RunBackfill(ctx, analyze.BackfillInterval)
	}
}

// DeriveDue enqueues an interest-layer rebuild for everyone who has read
// something since the last call.
//
// Exported so `serve` can run it once at boot rather than making the first
// homepage wait a whole poll interval, and so a test can drive it without a
// ticker.
//
// The low-water mark advances only on success. Advancing it first and then
// failing would silently skip a window of engagements, and the symptom — a
// homepage that is stale for exactly one interval, occasionally — is close to
// unfindable.
//
// # The window boundary is INCLUSIVE, and that is the deliberate direction
//
// ScopesToDerive matches `at >= since`, so an engagement landing in the same
// millisecond the cursor advances to is seen twice and schedules one redundant
// derivation. The alternative — an exclusive `>` — would SKIP that engagement
// instead, and the two failure modes are not comparable: redundant work is
// absorbed by the per-user dedupe key and costs one TF-IDF pass, while a skipped
// engagement is signal that is gone for good. This whole loop runs in well under
// a millisecond, so the collision is routine rather than theoretical.
func (a *App) DeriveDue(ctx context.Context) {
	a.deriveMu.Lock()
	defer a.deriveMu.Unlock()

	scopes, err := a.repo.ScopesToDerive(ctx, a.lastDerive)
	if err != nil {
		a.log.Warn("finding users to derive", "err", err)
		return
	}
	for _, sc := range scopes {
		if err := a.deriver.Enqueue(ctx, sc); err != nil {
			a.log.Warn("enqueueing derive", "user", sc.UserID, "err", err)
			return
		}
	}
	a.lastDerive = time.Now().UTC()
	if len(scopes) > 0 {
		a.log.Info("derive enqueued", "users", len(scopes))
	}
}

// NudgeInterval is the shortest gap between engagement-triggered derivations.
//
// Ninety seconds. The number is a trade between two visible failures rather than a
// tuning result: much longer and the ranking appears not to react to what you just
// read, which is the whole reason the hook exists; much shorter and a reading session
// becomes a continuous TF-IDF pass over ninety days of articles, on a box that is
// probably also serving the page.
//
// It bounds work per user, not per instance, which is the right axis: two readers each
// deserve a responsive ranking, and they do not contend for the same one.
const NudgeInterval = 90 * time.Second

// NudgeDerive asks for a derivation because the reader just did something.
//
// Rate-limited per user, and the limit is what makes it safe to call from the
// engagement path — which fires on every batch of twenty-five observations, so
// several times a minute while someone is reading.
//
// # Why the dedupe key is not enough on its own
//
// derive.Enqueue already dedupes per user, so a second job cannot QUEUE while the
// first is waiting. It says nothing about a job that has already started: enqueue
// during a run and a new row is created, so a busy session would chain derivations
// back to back with no gap. The dedupe key bounds the queue; this bounds the rate.
//
// Never blocks and never reports failure to the caller. This is a background
// improvement to an ordering, and the caller is in the middle of storing signals that
// have already been accepted — failing that write because a ranking could not be
// scheduled would trade the irreplaceable thing for the recomputable one.
func (a *App) NudgeDerive(sc store.Scope) {
	a.enqueueDerive(sc, false)
}

// ForceDerive asks for a derivation the reader is WAITING for, ignoring the rate limit.
//
// The one caller is the Smart+ opt-in. Flipping that switch used to save the preference
// and nothing else: the deriver reads it on its next run, so the ranking stayed exactly as
// it was — for up to NudgeInterval, or until the next poll — and My Feed looked identical
// after the reader had just turned on a paid feature. The complaint that followed was "I
// don't see the Smart+ branding", which was true, and the cause was not the badge.
//
// Exempt from the limit because the limit is aimed at something else. NudgeInterval bounds
// a signal path that fires several times a minute on its own; a toggle is a deliberate act
// a person performs once and then watches for a result. Throttling the deliberate act to
// protect against the automatic one drops precisely the request that someone is waiting on.
//
// It is still bounded: derive.Enqueue dedupes per user, so holding the switch down cannot
// queue a second job while the first waits, and a toggle is not a thing anyone can perform
// several times a second.
func (a *App) ForceDerive(sc store.Scope) {
	a.enqueueDerive(sc, true)
}

// enqueueDerive is the shared body: schedule a derivation for one user, off the caller's
// goroutine, honouring the shutdown handshake — and, unless forced, the rate limit.
func (a *App) enqueueDerive(sc store.Scope, force bool) {
	if a.pool == nil || !sc.Valid() {
		return
	}
	a.deriveMu.Lock()
	if a.closing || (!force && time.Since(a.lastNudge) < NudgeInterval) {
		a.deriveMu.Unlock()
		return
	}
	a.lastNudge = time.Now().UTC()
	// Registered under the same lock that checks `closing`, which is what makes the
	// handshake with Close airtight: either this Add happens before Close sets the flag
	// and Close waits for it, or the flag is already set and there is nothing to wait
	// for. Adding outside the lock reintroduces the window it exists to remove.
	a.nudges.Add(1)
	a.deriveMu.Unlock()

	// Its own goroutine with its own context: the caller's is an RPC context that is
	// cancelled the moment the response is written, and inheriting it would cancel the
	// enqueue roughly whenever it mattered.
	go func() {
		defer a.nudges.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.deriver.Enqueue(ctx, sc); err != nil {
			a.log.Warn("nudging derive", "user", sc.UserID, "err", err)
		}
	}()
}

// DeriveNow runs a derivation for one user synchronously, in the calling goroutine.
//
// For commands, not for the server. Everything else enqueues and lets the pool drain, which
// is right for a running instance and wrong for a CLI: a command that returns before the
// work happens forces the operator to guess whether it did, and `seed-reading` exists
// precisely so they can SEE the interest layer that their fabricated history produced.
//
// It does not need the pool started, which is the other half of why it is separate — a
// command should not have to spin up workers to do one job.
func (a *App) DeriveNow(ctx context.Context, sc store.Scope) {
	res, err := a.deriver.RunReporting(ctx, sc, time.Now().UTC())
	if err != nil {
		a.log.Error("deriving", "err", err)
		return
	}
	a.log.Info("derived the interest layer",
		"feeds", res.Feeds, "terms", res.Terms, "entities", res.Entities,
		"topics", res.Topics, "ranked", res.Ranked,
		"cold_start", res.ColdStart, "smart_plus", res.SmartPlus)
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
				// Retention (TODO F36). Runs on the same heartbeat and does
				// NOTHING unless somebody set a window: the stated default is
				// keep-forever, and `retention.Sweep` returns before touching the
				// database when the policy is zero.
				a.sweepRetention(ctx)
				// A span per cycle. The poll is the app's heartbeat — if it
				// stops, the reader goes quiet and looks like a slow news day,
				// which is the failure mode §22.11 exists to make visible.
				pollCtx, span := a.tel.Tracer.Start(ctx, "poll.cycle")
				// A request id for the cycle, the same way the job pool stamps
				// one before running a job (jobs.go) and the interceptor stamps
				// one per RPC.
				//
				// Without it a poll is the one path in the application whose
				// failures cannot be grepped back to the work that caused them —
				// and it is the path most likely to fail, because it is the one
				// that talks to a hundred and fifty strangers on a timer. §22.11
				// asks for one id threaded handler → job → poller; this is the
				// third of those three.
				pollCtx = reqid.With(pollCtx, "")
				start := time.Now()
				res, err := a.svc.PollDue(pollCtx, 25)
				elapsed := time.Since(start)

				a.tel.Instruments.PollDuration.Record(pollCtx, elapsed.Seconds(),
					metric.WithAttributes(telemetry.Outcome(err)))
				span.End()

				if err != nil {
					// The whole cycle failed, which is different from some feeds
					// failing and is worth its own counter: one means the
					// database or the scheduler, the other means a publisher.
					a.tel.Instruments.PollRuns.Add(ctx, 1,
						metric.WithAttributes(attribute.String("outcome", "cycle_error")))
					a.tel.RecordError(ctx, a.log, "poll", "cycle_failed", err)
					continue
				}

				failed := len(res.Errors)
				if ok := res.Polled - failed; ok > 0 {
					a.tel.Instruments.PollRuns.Add(ctx, int64(ok),
						metric.WithAttributes(attribute.String("outcome", "ok")))
				}
				if failed > 0 {
					a.tel.Instruments.PollRuns.Add(ctx, int64(failed),
						metric.WithAttributes(attribute.String("outcome", "source_error")))
				}
				if res.NewItems > 0 {
					a.tel.Instruments.ItemsIngested.Add(ctx, int64(res.NewItems))
				}

				if res.Polled > 0 {
					// Always logged when work happened, at Info: this line is how
					// somebody tailing the log knows the instance is alive. The
					// duration is here because "the poll is getting slower" is
					// the earliest warning of a database that needs a vacuum.
					a.log.Info("polled", "sources", res.Polled, "new", res.NewItems,
						"errors", failed, "duration_ms", elapsed.Milliseconds())
				} else {
					// Nothing was due. At Debug so it does not drown the log, but
					// present — "the poller ran and had nothing to do" and "the
					// poller is not running" are otherwise identical from outside.
					a.log.Debug("poll cycle: nothing due", "duration_ms", elapsed.Milliseconds())
				}
				// After the fetch, not before: new items are what the ranking is
				// over, and deriving first would rank the homepage against a
				// world one interval out of date.
				a.DeriveDue(ctx)
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

// audioDirOf is where the broadcast's music beds sit relative to the web root.
//
// Derived rather than configured, because they are shipped together: make.ps1
// copies web/audio into the same directory it copies index.html into, so a
// second flag would be a second thing to get wrong for no choice anybody wants
// to make. An instance serving no static files has no beds, which is the honest
// answer rather than a path that does not exist.
func audioDirOf(webRoot string) string {
	if webRoot == "" {
		return ""
	}
	return filepath.Join(webRoot, "audio")
}
