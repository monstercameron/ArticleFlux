// Package buildver is the one place that says what build this is.
//
// # A leaf package on purpose
//
// It imports NOTHING. That is the whole requirement: the wasm client has to
// state its own version on every call (§22.10, TODO 7.8), and the version had
// been a constant inside `cmd/articleflux` where the client cannot reach it.
// Reaching for `internal/skew` instead would pull `apierr` → `store` → the
// SQLite driver into the browser bundle, so the shared vocabulary lives here
// and `skew` refers to it rather than the other way round.
//
// # One constant, both halves
//
// The server and the wasm bundle are built from the same tree and shipped in
// the same binary, so in a matched deployment the client's stamp IS the
// server's version and the skew check can never fire. That is correct and it is
// the point: what fires the check is a Service Worker still serving a bundle
// from an older deploy, which has an older constant compiled into it. The check
// is a comparison between two builds, not between a build and a wish.
package buildver

// Version is this build.
//
// A constant rather than an `-ldflags -X` variable, because it has to be
// identical in the server binary and in the wasm bundle, and two build
// invocations that must agree on a flag are two build invocations that will
// eventually disagree. Bumping it is an edit to one line in one file.
const Version = "1.2.0"

// Commit is the git revision this SERVER binary was built from, injected with
// `-ldflags -X` by deploy/update.sh. Empty in every other build, which is the
// honest answer for one that was not deployed.
//
// # Why this is an -X variable when Version deliberately is not
//
// They answer different questions. Version is a contract between two builds —
// the server and the bundle a browser has cached — so it must be identical in
// both, which is exactly why it is a constant that nobody can set differently
// in two build invocations. Commit identifies ONE artefact: which tree the
// process now running was built from. Nothing compares it against the client,
// and a wasm bundle stamped with it would be a second place to get it wrong.
//
// # Why it exists at all
//
// Because "did the deploy actually land?" had no answer. On 2026-08-01 a
// promotion completed green, the webhook accepted the job, update.sh reported
// success in one second, and the box went on serving the previous build — and
// the only way anybody established that was to download the wasm module and
// grep the decompressed bytes for a CSS class that only exists in the new one.
// A deployment that cannot say what it is running is one where every check is a
// proxy for the thing you actually want to know.
//
// It is served as a header on /readyz, so the promotion that caused a deploy
// can watch for it and refuse to call itself finished until the box agrees.
var Commit string

// MinClient is the oldest client this server will serve.
//
// Raised only when an incompatibility is real, because raising it logs
// everybody on a cached bundle out until they hard-reload. It sits here beside
// Version so the two are read together: a minimum above the current version
// would refuse every client including the one this build ships, and having them
// in one file makes that impossible to miss.
const MinClient = "0.1.0"

// ClientStampHeader carries a client's build stamp on every RPC.
//
// Metadata rather than a field on a handshake message, because there is no
// handshake message — the tunnel multiplexes ordinary RPCs and which one a
// client makes first varies. A header is attached by one client interceptor and
// read by one server interceptor, so no RPC can forget it.
const ClientStampHeader = "x-articleflux-client"
