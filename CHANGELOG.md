# Changelog

All notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and once there is a release this project
will follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Pre-1.0: `main` is the only supported version and the wire contract is additive-only within `v1`.
The full reasoning behind any entry lives in the commit message; this file is the index.

## [Unreleased]

### Security

- **Sudo mode is enforced** (`AuthService.Reauthenticate` · `ChangePassword` ·
  `RegenerateRecoveryCodes`, TODO 6.1, §7.3). The policy existed — which operations need fresh
  authentication, how long fresh lasts, that an unclassified action fails closed — and nothing
  consulted it. A session now records **when its holder last proved who they are**, ordinary traffic
  deliberately does not refresh that stamp (reusing `last_seen_at` would make a control that demands
  a password satisfiable by reading articles), and a session minted by a **refresh** does not inherit
  it — otherwise a stolen refresh token could open the window the control exists to keep shut, and
  change the password with it. Refusal is `PermissionDenied`, never `Unauthenticated`: one means
  "show the login screen" and the other means "ask for the password over the top of what they were
  doing", and a client that conflates them signs somebody out for trying to protect their account.
  Changing a password ends every **other** session and keeps the caller's own. Regenerating recovery
  codes joined the protected list explicitly — it decides who can get back in without a password,
  and relying on the fail-closed default would have left the list that documents the control quietly
  incomplete.

- **Refresh-token families with reuse detection** (`AuthService.RefreshSession`, TODO 6.1, §7.3).
  A refresh token is single-use; presenting a spent one means either a replay or a stolen token
  being used alongside the real client, and since the server cannot tell those apart it revokes the
  whole device family. **The revocation was being rolled back by the error that reported it** —
  `Tx` rolls back on any error, so reuse detection detected the replay and then undid its own
  response, leaving the family live and a stolen token working silently. Found by the test written
  for it, fixed at the store layer, and now asserted from both the repository and the RPC.

- **The login lockout is enforced, not just designed** (TODO 6.1, §7.3). Failures are counted in the
  database since the account's last successful login, so a restart no longer hands an attacker a
  fresh budget, and the correct password is refused *during* a lockout — one that lets it through is
  one an attacker walks past on the guess that happens to be right. A username that does not exist
  locks out on identical terms, so the lockout is not an account-existence oracle.
- **Passwords are checked against a bundled known-password list** (`internal/pwpolicy`). Bundled
  rather than the HIBP range API: the k-anonymity prefix really does disclose nothing, but a request
  to a third party at the moment somebody types a password is the wrong default for a reader whose
  premise is that your reading does not leave the box. Candidates are folded — case, leet
  substitutions, trailing digits and punctuation — so "P@ssw0rd1!" and "Password123" are refused by
  the same entry as "password".
- **Argon2id tunes itself to the machine, and can only raise the cost.** One constant is ~40ms on a
  server and ~400ms on a small self-hosted box. The benchmark can never lower the parameters below
  the OWASP baseline: it measures the box as it is, so a restart under load would otherwise settle on
  weaker settings — a downgrade anyone could trigger with traffic.

- **A reverse-proxied instance no longer served an unauthenticated superadmin session.** `DevMode`
  — which serves the first account with no login and registers an unauthenticated
  `POST /debug/reset-state` — was *derived* from a loopback bind. The reasoning was that loopback
  cannot be reached from outside the machine, which is true of the socket and false of the
  deployment: every reverse-proxy setup, including the nginx site now in `deploy/`, terminates TLS
  on `:443` and forwards to `127.0.0.1:9000`. So the canonical way to host this was also the way to
  publish an entire reading history to anyone who typed the domain. A bind address describes network
  topology and cannot tell you who is on the other end of a connection; nothing that cannot tell you
  that may decide whether to ask for a password. It is now an explicit `-dev` flag, defaulting off
  and **refused on any non-loopback bind**.

- **The fonts came from Google, on every load, for everyone.** A reader whose stated boundary is
  that nothing they read leaves the machine was telling `fonts.googleapis.com` their IP address,
  their User-Agent and the fact that they had opened the app — before they had read anything. No
  setting turned it on and no policy mentioned it, because it arrived as a font choice rather than
  as a network decision. The faces are checked in as woff2 subsets and served from the same origin,
  which also means the typography survives being offline, on a LAN, or behind a firewall that does
  not resolve Google.
- **The app document had no security policy at all** (`internal/app/headers.go`). Every proxied
  sub-resource already shipped a tight one — sandboxed images, an opaque origin for proxied HTML, a
  locked-down live view — while the document those hang off set `Content-Type`, `Cache-Control` and
  nothing else. That is backwards: the app document is the origin holding the session and the
  reading history.
- **An IPv4 address wearing an IPv6 costume walked past the SSRF guard.** v4-mapped,
  v4-compatible, 6to4 and NAT64 (RFC 6052) each carry an IPv4 address inside an IPv6 one, so
  `::ffff:127.0.0.1` was not `127.0.0.1` to a deny list that only knew the plain form. `unwrapV4`
  reduces every such form to the address it carries, so one list covers all of them and the embedded
  address is judged on its own merits under both policies.
- **Telemetry is inert unless an operator points it somewhere.** OTLP export exists
  (`internal/telemetry`) and does nothing without `-otlp-endpoint` — the same egress boundary as the
  fonts above: an instance shipping spans to an endpoint nobody configured has had a network
  decision made on the reader's behalf.

### Fixed

- **The connection badge could say "down" over a working connection, forever** (`client/data`,
  §20.19, A40). An RPC that fails during an outage marks the transport failed. gRPC itself never
  notices — a socket that stops carrying traffic is indistinguishable from one nobody is using until
  the keepalive probes it forty seconds later — so `Watch` sits in `WaitForStateChange(READY)` and
  never wakes, the connection recovers silently, and the only thing that could correct the badge is a
  successful call that nobody is making. On a reading app, "nobody is clicking" is the normal state
  and precisely what the indicator exists for. `Kick` now **verifies** rather than assuming: it makes
  one cheap `Version` call and lets the ordinary error path judge the outcome. The transport's own
  `READY` is deliberately not used as the evidence — it is the state that lies about a socket the
  browser has abandoned.
- **`offline` and `down` were decided by an event that is not always delivered.** The browser's
  `online`/`offline` events were the only source of the flag that separates "your wifi is off" from
  "the server is not answering". Headless Chrome delivers them about half the time under network
  emulation — measured over a dozen runs, which is what turned a suspected flake into a fix — and
  real browsers are worse in the cases that matter most: waking from sleep, a VPN dropping, a phone
  switching to a network it cannot actually reach. A missed event never corrects itself, so a reader
  with no network got a countdown toward a reconnect that could not happen. The client now also
  **polls** `navigator.onLine` every two seconds (a synchronous property read) and treats a
  transition exactly as the event handler does, including the kick when the network returns. A failed
  call also asks the browser before reporting, because that is the one moment the answer matters.

- **The renderer could wedge holding the only render slot** (`internal/render`, TODO 6.16). Two
  places, the same mistake: the context argument was accepted and then not used for the work.
  `Snapshot` built its run context from the browser tab rather than from the caller's, so a reader
  who navigated away waited out the *renderer's* timeout — minutes — while every other request got
  `ErrBusy` for a render nobody wanted. `Stream` guarded its frame loop with the caller's context but
  ran the navigation on the tab's, so a server that accepts a connection and never answers blocked
  upstream of the code that would have noticed. The tab cannot simply *be* a child of the caller —
  chromedp carries the browser allocator in the context — so both now cancel through
  `context.AfterFunc`, and both report the **caller's** deadline rather than a bare
  `context.Canceled`: one of those is worth retrying and the other is not.

- **CI could not build the project at all, publicly.** `go.mod` replaces `GoWebComponents/v5` with a
  sibling checkout, and the branch it names did not exist on the remote — so every job died at its
  second step, before building or testing anything. Alongside it: `actions/checkout` refuses a
  `path:` outside the workspace, which is what three jobs still asked for; the composite action
  written to work around that was itself untracked; the demo's artifact check killed itself with
  `set -o pipefail` when `head -c 4` closed a pipe on `gzip`; the Service Worker's version stamping
  existed only in a working tree, so the workflow verified something no committed build did; and
  GitHub Pages was never switched on. The demo publishes now.
- **The Service Worker cached nothing on the demo.** Its shell listed `app.wasm`, and the static
  demo publishes only `app.wasm.gz` — so the install fetched a file that does not exist and left the
  module uncached: a worker whose entire job is booting offline, unable to boot the one file that
  matters. It lists both, and pays a 404 on whichever host lacks one.
- **A browser-dependent test hung the Windows CI job for sixty seconds.** `windows-latest` has Edge,
  launches it, and paints nothing — no GPU, no display, a cold profile. Skipped on CI behind
  `ARTICLEFLUX_BROWSER_TESTS=1`, keyed on CI rather than on Windows, because it passes on a real
  Windows desktop — which is where the MJPEG framing it covers was broken and found.
- **`TestJobsRunAndComplete` was racing its own assertion.** It counted handler invocations and
  asserted the queue was drained in the same breath, while the pool marks a job complete *after* the
  handler returns. It failed a few percent of the time and read as a queue bug, which is worse than
  no assertion: it teaches whoever sees it that the test is flaky rather than that the queue is
  broken.
- **The client had been stamping idempotency keys into a void** (TODO 8c.15, §20.7). The
  `idempotency_keys` table, the repository methods and the key generator all existed, and nothing on
  the server read one. That was survivable only by accident: every mutation the outbox queues sets an
  absolute value, so applying it twice lands on the same state. The accident ends at the first
  relative operation — an append, a counter, a toggle — and the failure is silent when it comes: a
  star toggles back off, a note is appended twice, an unread count moves by two. A gRPC interceptor
  now stores `(user, key) → response` for 24h and replays it verbatim; a method opts in by declaring
  the field, so the thirty-first mutating RPC is covered without anyone remembering.

- **Items ingested outside fan-out were invisible to every unread count.** 80 of 3,806 items in the
  development database had no `user_item_state` row. That row's existence is what makes an item
  known to a user, and it was being created by fan-out — a *queued job that applies rules*, which
  runs on a worker after ingest, can be delayed or retried, and does nothing useful for a reader
  with no rules. Delivery is not a rule outcome; it is what ingest means, so it now happens in
  `IngestItems`' own transaction. `Subscribe` does the same for items a source already holds:
  sources are global (A14), so subscribing to a feed someone else reads means subscribing to one
  that is already full, and without this a new subscriber's unread count started at zero and only
  counted what arrived afterwards.
- **An empty feed showed the previous feed's articles** (TODO H10). `loadItems` set the loading flag
  but never cleared the list, and the list pane only draws its skeleton when it is *both* loading and
  empty — which a scope change never was. A feed with items hid this, because the response replaced
  the rows; a feed with **zero** items never could, so the reader was looking at one feed's articles
  filed under another feed's name. The list is now cleared on a scope change, in `selectScope` and
  `runSearch` rather than in `loadItems`, because `loadItems` is also how the list refreshes in place
  after "mark all read" and after a reconnect, where blanking the screen would turn a silent update
  into a flash.
- **A password manager could kill the wasm module** (TODO H9). Filling the login form dispatches a
  synthetic `new Event('keydown')`, which has no `key`, no `altKey`, no `ctrlKey`. `Value.Bool()` on
  the resulting `undefined` does not return false — it panics, and a panic in wasm tears down the
  whole module and every listener with it, leaving the page a dead screenshot of itself. `OnKeyDown`
  now discriminates on the event actually carrying a key, and boolean reads go through a guarded
  helper; the same unguarded pattern in `PrefersReducedMotion` went with it. The login fields are now
  inside a `<form>`, which Chrome had been asking for and which is what lets a manager pair and save
  a credential at all.
- **An infinite reload loop on the login screen** (TODO H8). The client interceptor treated any
  `Unauthenticated` as a session ending and reloaded; asking `WhoAmI` at boot with no token gets
  exactly that answer, correctly, so the page reloaded forever and the login screen never survived
  long enough to submit. It presented as "Couldn't sign in. Check the server is running" — a message
  about the transport, from a server answering perfectly. The whole `AuthService` is now excluded,
  matched on the service prefix so a future method cannot reintroduce it by omission.

- **The feed poller would have tried to HTTP-fetch newsletter sources.** A mailbox source's
  `feed_url` is `mailbox:<id>:<sender>`, which is not a URL — so every poll forever would have been
  "not a recognisable feed", the exact failure already guarded against for scraped sources. Mailbox
  sources are filled by polling the *mailbox* (one IMAP connection yields many senders at once), so
  they are excluded from the poller's queue and from the lag metric, which would otherwise report a
  permanent backlog on any instance with one configured.

### Performance

- **The unread counts went from ~500ms to ~3.5ms at 50,000 items** (TODO 5.4a, plan §6.5). The
  sidebar's per-feed counts (447ms) and the flat badge (556ms) were the last two shapes over G3's
  150ms budget: a count must visit every unread row and cannot stop at 50, so the index hint that
  fixed the paged lists did nothing for them. Denormalising `source_id` onto `user_item_state` with
  a partial index on unread rows makes "unread in this source" one index range that never touches
  `items`. `knownSlow` is now empty. Both numbers are computed by the same expression — the badge is
  the sum of the per-feed counts — since two numbers on one screen computed two ways is how they
  stop agreeing. The index has to be named explicitly: with `ANALYZE`'s statistics SQLite preferred
  an `ANY(user_id)` skip-scan of an older index and ran the query in 2.5s, *worse than before the
  denormalisation existed*.

### Added

- **Rendered snapshots — a real browser runs the page and we keep what it made** (`render.Snapshot`,
  TODO 6.16, §10.1c, tier 2r). Tier 2 fetches HTML and gets `<div id="root"></div>` on anything
  built in the browser; this rung runs the scripts first. It returns `outerHTML`, a full-page
  screenshot from the *same* session, the title and the final URL — the screenshot because the HTML
  is what fails: a framework that rendered into a shadow root, a page that detected automation, an
  article inside a canvas all return well-formed markup with nothing in it. It waits for Chrome's own
  network-idle signal — matched to the navigation's loader id, so an earlier blank page's idle report
  is not mistaken for this one — then scrolls to the bottom and back so lazy images resolve. Capped
  at 8 seconds, and **reaching the cap is not an error**: analytics heartbeats and open websockets
  mean a large share of real pages never go quiet, disproportionately the heavy ones this rung exists
  for. One render at a time, sharing the slot with the live view. On demand, never a background
  sweep.
- **Three outcomes, because the ladder does three different things with them.** A snapshot can
  succeed, come back *empty* (`ErrEmptyRender` — escalate), or come back *too large*
  (`ErrOverBudget` — degrade to text). Emptiness is measured in **text**, after `<script>`, `<style>`,
  `<noscript>`, `<template>` and `<svg>` bodies are dropped: their contents are text *between* tags,
  so counting them makes every unmounted shell look full — the framework bundle and the JSON-LD block
  alone clear any threshold. The budget is measured on the **compressed** artifact, gzipped once at
  render and kept (§10.1c, the same trade §7.6 makes for the wasm bundle); judging by raw size would
  degrade pages that compress to a tenth of it and arrive comfortably. Both errors return the
  artifacts alongside, because a screenshot is exactly what the reader gets when the DOM is unusable,
  and because a caller with its own budget should not pay for the render twice.

- **An interest layer that can be thrown away and rebuilt** (`internal/derive`, TODO 6.9, §18,
  D18). Feed, term and domain affinity plus topics, derived from the engagement log in two stages
  and in this order: *recall* from passive signals (impressions, opens, dwell, completion), then
  *precision* from the explicit verdicts. Collapsing them into one sum is the mistake D18 names —
  passive signals are cheap and plentiful and would drown the handful of statements a reader
  actually made. Everything the package writes is derived, and a test asserts that `ClearDerived`
  plus a re-run reproduces it, which is what keeps `engagements` the only irreplaceable table.
- **Listening to a long article** (§10.7): `GetItem` returns a short-TTL sealed speech URL, and
  `internal/smart.Digest` rewrites the piece *for the ear* before the voice reads it — no bullets,
  no headings, no sentence whose structure only parses on a second look. The sealed URL exists
  because an `<audio src>` cannot send an `Authorization` header; without a capability in the URL,
  `/speech` could only identify a caller through the DevMode fallback, which is to say it worked on
  a laptop and not on a server. Empty whenever the instance has no key, which is the signal the
  client uses to stay on the browser's own synthesiser rather than offer an upgrade that cannot
  work.
- **Traces and metrics** (`internal/telemetry`, §22.11) — spans around every unary handler, so a
  slow call can be opened up rather than merely counted. Span names and method attributes come from
  the generated service descriptor, which is what makes them safe to record; the status code goes on
  the span and the error text never does, because `codes.NotFound` is six possible values and an
  error string is unbounded and quotes article titles back at whoever holds the traces.
- **The Service Worker** (`web/sw.js`, TODO 8.4, §12.3) — app-shell caching so the reader boots on a
  plane, and nothing else. `index.html` is **network-first**: cache-first on the shell is how a
  browser ends up running last month's app against this month's server forever, which is the failure
  the version-skew refusal exists to compensate for. Its cache version is checked against the build
  constant by a test, because forgetting to bump it serves old code indefinitely and nothing looks
  wrong. It is registered *after* the wasm module starts, so a bad worker can never prevent the app
  from booting — and the build now actually ships the file, which it did not.

- **Three more boot-time refusals** (`app.Preflight`, TODO 7.7): a web root with an `index.html` but
  no `app.wasm` (the page loads and shows nothing), a stored Smart+ key that is not shaped like one
  or that no longer decrypts, and newsletter mailboxes on an instance with no encryption key — whose
  passwords can never be read, which otherwise surfaces only as newsletters quietly stopping. IMAP
  *reachability* is deliberately not checked: a provider having a bad five minutes must not stop the
  reader from starting, and the poller already records and backs off per mailbox.

- **Version-skew refusal** (`internal/skew`, TODO 7.8 / 8c.16, §22.10). The wasm bundle is cached by
  a Service Worker, so "old client, new server" is not an edge case — it is the default state of
  every tab left open across a deploy. The client has recognised the skew sentinel since before
  anything sent it, deliberately: the client that must act on a skew refusal is by definition the
  old one, so recognition had to be in the field first. The server now sends it. A client with no
  stamp, or one this build cannot parse, is **allowed through** — refusing on unknown would lock out
  curl, tests and the sync API, and turn a formatting change into an instance-wide outage.
  `GetVersion` is exempt, because refusing the call that explains the refusal is a closed loop.

- **Request ids threaded through handlers *and* jobs** (`internal/reqid`, TODO 7.11, §22.11). §20.7's
  bargain is that the message a reader sees is always safe and the useful detail goes to the log —
  which only pays if the id reaches both ends. The queue is where this normally stops, and the queue
  is most of the work: fan-out, extraction and archival all happen later on a worker, so an id
  ending at the RPC boundary explains the enqueue and nothing about the work. A job now records the
  request that queued it and gets a fresh id of its own, because "what did this job do" and "what
  was the user doing when this got queued" are different questions. Ids are random and meaningless
  by design: one you can correlate across users is a tracking identifier, and one you can guess is a
  way to ask the log about somebody else.

- **`internal/apierr` — §20.7's error taxonomy in one place** (TODO 7.3a). It lived in `grpcsrv`,
  which is one of the three transports that share it; a taxonomy owned by one transport is one the
  other two re-derive, and you find out they derived it differently when a client sees a 404 from
  one surface and a 403 from another for the same condition. **Cross-tenant access returns
  `NotFound`, never `PermissionDenied`** — the latter confirms the object exists — and that is now a
  named constructor, with a test asserting a cross-tenant refusal is byte-identical to a genuine
  miss in code, message and detail payload. `ErrorDetail` gains §20.7's structured fields
  (`field`, `quota`, `retry_after_s`, `doc_ref`), additively, so a client that ignores all four still
  renders correctly; retry hints round **up**, because a hint that expires before the limit does
  teaches clients to ignore hints.

- **Lockout, recovery codes, reset tokens and sudo policy** (`internal/authn` + `store` ledger, TODO
  6.1 in part). The previous limiter lived in memory, so a restart cleared it — fine for a limiter,
  whose job is to blunt a burst, and exactly wrong for a lockout, whose job is to survive one. The
  count now derives from `login_attempts` and is measured **since the account's last successful
  login** rather than over a window, so typing your password right actually clears it. The curve
  doubles from 5s to a 15-minute ceiling: 14 guesses in the first hour, 4 an hour thereafter. The
  cap is deliberate — an uncapped lockout is a denial of service against the account owner that any
  stranger can trigger. Recovery codes and reset tokens are single-use, enforced by the UPDATE's own
  WHERE clause rather than a read-then-write, because that race is a full account takeover; unknown,
  spent and expired tokens all return one error, so a guess cannot be confirmed. Codes are Crockford
  base32 with no I, L, O or U, since they get written on paper and typed back by someone already
  locked out.

- **Newsletter mailbox storage** (`store.MailboxRepo`, TODO 5.7, A20): IMAP accounts with their
  passwords encrypted at rest, per-sender sources keyed **per user** so two people subscribed to the
  same newsletter never share a row, UID and backoff bookkeeping, and a deletion that withdraws the
  credential while leaving the mail already delivered. It is a separate repository type because it
  holds a decryption key, and `Mailbox` has no password field at all — reading a credential is a
  distinct scoped call, so listing mailboxes cannot serialise one by accident today or after
  somebody adds a field. Without a configured encryption key it refuses to store rather than
  falling back to plaintext. The IMAP client itself is still to come (M22).
- **The cross-tenant leak harness now sweeps every repository type.** It swept `ReaderRepo` for as
  long as that was the only one, and would have kept reporting a clean sweep of it while a second
  repository went entirely unexamined — the same silent coverage decay the harness exists to
  prevent. A new guard scans the package for `*Repo` types and fails on any the sweep does not cover
  or explicitly excuse.

- **A login** (`AuthService`, TODO 6.1 in part): `Login`/`Logout`/`WhoAmI` over the tunnel, a
  `/login` screen in the app's own vocabulary, and a bearer token attached by **one client
  interceptor** rather than by each of the thirty-odd RPC methods — the per-method version's failure
  mode is that someone adds the thirty-first and it silently runs unauthenticated. The password hash
  **always runs**, against a boot-computed Argon2id decoy when the username does not exist, so a
  missing account cannot be told from a wrong password with a stopwatch; there is one uniform error
  for missing, wrong, and deactivated. Sessions are stored as SHA-256 and are revocable, so a
  database dump is not a set of live sessions. Counting **failures only** in the rate limiter was the
  second attempt: the first cleared the counter on success, and because the client key is shared by
  everyone behind a proxy, one household's typos locked out the instance.
- **`init`, `adduser`, `passwd`, `migrate`, and `backup` subcommands.** `init` (TODO 7.9) refuses to
  run on a populated instance; `passwd` (7.10) revokes every session for the account, which is the
  point of a break-glass reset. `make.ps1` had been calling a `migrate` verb that did not exist.
- **Verified backups** (`store.Backup`, TODO 3.4): `VACUUM INTO` plus `PRAGMA integrity_check` on the
  copy. Not `cp` — in WAL mode an unknown amount of committed data lives in the `-wal` file, so
  copying the three files copies them at three different instants and a concurrent writer produces a
  backup that opens cleanly and is missing a transaction. It restores, it passes a smoke test, and it
  is wrong. Pinned by a test that backs up *under concurrent writes*.
- **Boot-time refusals** (`app.Preflight`, TODO 7.7 in part): no account, missing web root, or an
  unwritable data directory stops the server starting, with all the failures reported at once rather
  than one boot-loop at a time. Each otherwise surfaces hours later while `/healthz` reports green.
- **`/readyz`** (TODO 7.5), which touches the database — unlike `/healthz`, which deliberately does
  not, because a liveness probe that fails on a slow query gets the process killed and restarted into
  the same slow query.
- **A deployment**: `deploy/` ships a hardened systemd unit, an nginx site, a nightly backup timer,
  and a runbook that takes a bare Ubuntu droplet to a reader on TLS. Plus a `Makefile` mirroring
  `scripts/make.ps1` verb for verb — 1.4's reasoning ("no `make` on this box") was about the
  development box and does not extend to the deployment target, which has make and no PowerShell.
- **`serve -origin`** finishes TODO 7.4's `WithAllowedOrigins`, and **`serve -behind-proxy`** makes
  `X-Forwarded-For` trusted only where an operator has said a proxy is in front — it is a request
  header, so trusting it unconditionally lets any client write whatever address it likes into the log.
- **`.env` is actually read** (`internal/envfile`, TODO H3). `.env.example` had said "copy to .env and
  fill in" since Tier 1 while nothing read the file, so the instruction was true only for someone who
  already knew to export the variable by hand. Eighty lines, no dependency. A flag beats the
  environment beats the file, and the real environment always wins — so a systemd `EnvironmentFile=`
  cannot be overridden by a stray `.env` in the working directory. `ARTICLEFLUX_DEV=1` is the same
  switch as `-dev`, which is why `-dev` is now refused alongside `-behind-proxy`: a proxy in front of
  a loopback bind is a published instance, and without that refusal the original vulnerability walks
  back in through a development `.env` copied to a server.
- **The server states its posture at boot** (TODO H12). There is no `-prod` flag and there will not
  be one: production is the default and `-dev` is the opt-out, because a mode you must remember to
  turn on to be safe is one that eventually does not get turned on. But a default that is never
  stated is a default nobody checks, so `serve` now logs `MODE=production — login required` with the
  origin allowlist and whether forwarded addresses are trusted, or `MODE=development — NO LOGIN` at
  WARN along with the debug endpoints and relaxed SSRF guard that come with it. Two production-only
  warnings ride along: a public bind with no `-origin`, and an origin allowlist on a loopback bind
  without `-behind-proxy`. Warnings rather than refusals — each describes an instance that works and
  is weaker than it looks.
- **Dev mode no longer asks for a password**, and the login screen prefills `cam` /
  `articleflux-dev` on a loopback origin (TODO H6, H7). Whether a credential is required is a fact
  about the server, so the client asks it rather than assuming from local storage. The prefill is
  gated on the page origin parsed as a *host* — a substring test for "localhost" would fire on
  `https://localhost.attacker.example`.

### Known

- **An async state write does not repaint when nothing else changed** (TODO H11). With the empty-feed
  fix above in place, a feed with no items no longer shows the wrong articles — instead it shows a
  loading skeleton that never resolves, for a request that succeeded —
  the response lands, the flag is cleared, and no render follows. On a populated feed the new rows are
  self-evidently a change and the scroll-to-top fires a repaint; with an empty result the only change
  is one boolean. Isolated to GWC's async scheduling rather than to the reader, and worth treating as
  general: any update whose only change is a flag is currently invisible.

- **Identity repositories and the capability model** (`internal/store/identityrepo.go`,
  `internal/authz`, TODO 5.1 & 6.2). D12's shape, so there is an invites table and no registration
  one: a code is returned **once** and only its hash is stored, because an invite is a bearer
  credential for its whole life and a database dump should not be a pile of usable ones. Redemption
  is one transaction, so two people racing a code cannot both get an account. §7.5's map **fails
  closed on an unmapped method** — the alternative makes forgetting an entry a silent grant of a new
  RPC to everyone — and `Unmapped()` turns that from a runtime 403 on a shipped feature into a boot
  check. An API token's scope **intersects** the owner's role rather than uniting with it, so a
  viewer's read-write token gains nothing and minting one can never be an escalation. Refresh reuse
  revokes the whole **family**: which of the two holders is the thief is unknowable, so both are
  logged out, which is an inconvenience for the owner and the end of the session for the other one.

- **Recommendations actually run** (`internal/recommendjob` + `internal/store/outlinks.go`, TODO
  6.10) — harvest → validate → gate → score → store, rungs 1–3, no LLM. Outlinks are §18.7's rung 1
  and the best signal in the system: the links inside articles the reader engaged with, costing a URL
  parse and explaining themselves. **Validation happens before scoring, not after** — cheaper the
  other way round, and wrong, because the health gate's inputs *are* the validation, so a candidate
  scored without it is scored on evidence nobody checked. Dismissed and already-subscribed domains
  are filtered **before** the validating fetch: §18.7's guardrails are also a politeness budget, and
  fetching a site to score a recommendation the reader already refused is a request nobody wanted
  made. Re-extracting an item replaces its outlinks rather than appending, or the "linked here 11
  times" evidence climbs on its own.

- **Rate limits** (`internal/ratelimit`, TODO 7.3d) — §20.7's table as code, with a test asserting the
  numbers still match the document. Token buckets rather than fixed windows: a fixed window lets a
  client send 60 at 11:59:59 and 60 more at 12:00:00, twice the limit in one second, and a polling
  client with a round-minute schedule finds that immediately. Refusals carry `retry_after`, **rounded
  up** — rounding down tells the client to retry slightly too early, which is one guaranteed wasted
  request per refusal at exactly the moment the surface is under pressure. Keys are bounded and
  eviction fails **open**, because a limiter that started denying everyone when it ran out of map
  space would turn a traffic spike into an outage. A misconfigured rule (zero limit, zero window)
  permits everything for the same reason: a typo in a constant should not take a surface down.

- **The LLM circuit breaker and the §18.8 egress boundary** (`internal/llm/breaker.go`,
  `egress.go`, TODO 6.11). §22.8's breaker opens after five consecutive failures and **half-opens to
  exactly one probe** — letting them all through would hand a still-broken provider the full load
  every two minutes, which is a retry storm on a timer. The in-flight bound is on *concurrency*, not
  rate, because the problem is not how many calls are made but how many are stuck; being busy is
  counted separately from failing, or a traffic spike looks like an outage. §18.8's allowlist is
  **types, not a filter**: a filter fails open, since it can only remove what it was told about, so
  adding a field upstream sends it and the request still succeeds. The outbound shapes have fields
  only for what is permitted, candidate ids are per-request *ordinals* rather than database ids (an
  opaque id would let a provider correlate across requests and rebuild the history the allowlist
  exists to prevent), and `AuditEgress` walks the marshalled JSON reporting anything off the list —
  which is the test §18.8 asks for by name, "not a comment expressing intent".

- **Item tags, note and bookmark search, saved views, and the audit log** (TODO 5.5, 5.8, 5.10).
  `item_tags` is A21's, and it is not `feed_tags`: "this article is about rust" and "everything from
  this feed is rust" are different statements, and a feed about systems programming carries the
  occasional piece about hiring. Both share one `tags` table, because a reader who labels a feed and
  an article "rust" means the same tag. Notes and bookmarks get their own searches rather than a
  federated one — "is this article about X" and "did I write anything about X" are not comparable
  relevance-wise, and a blended list quietly buries the notes, which are the rarer and more valuable
  hit. Bookmark search covers the **archived text**, which is most of the value and the only search
  that keeps working after the origin dies. Saved-view specs are validated on the way in, since a
  malformed one otherwise fails at render time, on a screen, long after the save reported success.
  `Audit` takes no `Scope` on purpose — the actor may be tombstoned and the tenant may be one being
  deleted — while `AuditTrail`, which reads it, does.

- **Idempotent mutations** (`internal/store/idem.go`, TODO 7.3c, §12.4) — `(user, key)` → the response
  that was sent, replayed **verbatim** for 24 hours. This has to exist *before* the offline outbox,
  not after: a phone draining its queue and losing the connection halfway cannot know which writes
  landed, so it resends — and without a replay the second attempt applies again. A star toggles back
  off, a note is appended twice, an unread count moves by two, and every one of those is silent. The
  stored bytes are replayed rather than recomputed, because a client receiving a *different* answer
  to the same request cannot tell a replay from a second effect. A key reused for a genuinely
  different request (method or body) is a **conflict**, since returning the first request's answer
  for the second one's write drops the write *and* reports success — worse than no idempotency at all.

- **Page cursors are bound to their query** (TODO 7.3b, §20.7). A keyset cursor is a *position* —
  "resume after (published, id)" — and carries no record of what it was a position *in*. Replay one
  from the unread list against the starred list and every row is a plausible article at a plausible
  date: nothing errors, nothing looks wrong, and the reader silently gets a page that skips whatever
  the two lists disagree about. Cursors now carry a 12-character hash of the filters and a mismatch
  is **`InvalidArgument`, never an empty page** — an empty page means "you have reached the end", and
  a client that reads a stale cursor as the end stops paging and shows a truncated list with no error
  anywhere. The scope is deliberately *not* in the hash: cross-tenant protection is the `WHERE`
  clause's job, and a cursor is not a capability.

- **The typed settings registry** (`internal/settingsreg` + `internal/store/settingslayers.go`,
  TODO 6.3) — **system → tenant → user** resolution that returns the value *and which layer supplied
  it*. That second half is not a nicety: "why is this off for me?" has two very different answers —
  you turned it off, or your admin did — and a settings screen that cannot tell them apart shows a
  control that silently does nothing. Registered keys make a typo an error at the boundary instead of
  a silent fallback to the default that looks like a broken setting, and a default that violates its
  own bounds is caught at registration rather than by the one user who never overrode it. A setting's
  `Scope` is the lowest layer it may be written at, so "the admin decides retention" is structural
  rather than a control the UI hopes to hide. A corrupt stored value is **skipped and reported**, and
  resolution continues to the next layer — using it would let a bad tenant value mask a good system
  one, and erroring would blank a screen whose other eighty-nine settings are fine.

- **G3 passed, and the answer was not the one the plan expected** (`internal/store/hotquery_test.go`,
  TODO 5.4, §6.5, R2). At 50,000 items × 3 users: **unread by newest 478ms → 0.5ms, unread by folder
  178ms → 0.5ms, keyset page 40 408ms → 0.3ms.** §6.5 prescribed denormalising `source_id` and
  `published_at` onto `user_item_state` — that was **never built** (5.3 recorded it as done and the
  columns do not exist) and turned out **not to be needed**. `EXPLAIN QUERY PLAN` showed SQLite
  driving from `subscriptions`, joining all ~16,700 unread rows, and sorting every one in a `TEMP
  B-TREE` to take 50; pinning `items_published`, an index present since `0001_init`, fixed it with no
  migration.
- **A 1.3-second paging regression, found by the fix and then fixed.** With the index pinned, page 2
  on the real database went 13ms → 1.3s: the keyset cursor's `a < ? OR (a = ? AND b < ?)` is not
  seekable, so SQLite scanned the index evaluating it per row. As the row-value `(a, b) < (?, ?)` it
  seeks — 0.5ms, and both pages ended up 20× faster than before any of this. Page 1 had got *faster*
  while page 2 collapsed, which is the regression shape a page-1 benchmark never sees.
- **Still over budget and now specified**: the two *counting* shapes — flat unread count 556ms,
  sidebar with per-feed counts 447ms — must visit every unread row, so no index helps. R2's
  materialised counter is justified by measurement rather than assumed (TODO 5.4a), and the current
  numbers are recorded as a ratchet so a regression fails and the entry must be deleted when it lands.

- **Preservation and the degrade ladder** (`internal/preserve`, `internal/store/archive.go`,
  `internal/degrade`, TODO 6.12 & 6.13). §10.6's tiered archival, including the trigger worth having:
  **a distress sweep when a source starts failing**, because a feed erroring is the best early
  warning that a site is in trouble and it usually arrives while the article URLs still resolve —
  that window is the whole opportunity. The ticket's bar is met: killing the fixture site mid-test
  leaves every item still readable in full. **Eviction can never drop an archive whose origin is
  dead** — at that point it is the only copy that exists anywhere — enforced in the `WHERE` clause
  and re-checked inside the delete, since a sweep may discover the origin is gone in between. §22.6's
  ladder is a pure function of free space, so every rung is testable without filling a disk. Its
  order is the design: at 5% free, polling stops while reading, marking read and notes keep working
  — new articles are re-fetchable from the publisher and a note is not, so that is the correct
  priority rather than a compromise. The outbox drain is never refused at any rung, because those
  writes already happened on a device and refusing them destroys work the reader believes is saved.

- **The poller is a priority queue by staleness ratio** (TODO 6.8, §22.7), not oldest-due-first. The
  distinction only shows under load and then compounds: at 10:30, a 15-minute feed due at 10:00 has
  missed two whole cycles (ratio 2.0) while a 24-hour feed due at 09:00 is barely late by its own
  standards (0.06) — and oldest-due-first polls the second one. Under a backlog the slow feeds keep
  winning on absolute lateness and the fast ones fall further behind forever, which is §22.7's "one
  slow batch permanently penalising everything behind it". A never-fetched source sorts first
  regardless, because a feed someone just subscribed to showing nothing for fifteen minutes is the
  worst first impression available. Plus `PollerLag`, which is a **policy** rather than a metric:
  chronic lateness widens intervals proportionally (capped at 4×) instead of falling further behind
  silently. The ordering test was verified by reverting to the old `ORDER BY` and confirming it fails.

- **The live-update bus** (`internal/events`, TODO 6.5) — **per-tenant** ring buffers, which is the
  entire design: one shared buffer means a tenant importing 400 feeds evicts everyone else's events
  and their clients are told to resynchronise because of an import in an account they have nothing to
  do with. The test is exactly that, and it passes. A replay whose sequence has aged out returns
  `RESYNC_REQUIRED` rather than "what's left" — a client sent a partial replay believes it is up to
  date while having missed something, and nothing afterwards corrects it. Delivery is best-effort:
  events are a latency optimisation over polling and every screen can rebuild from a query, so a slow
  client is dropped rather than allowed to stall a write. **A concurrency test found a process-level
  crash**: sending outside the tenant lock races `Close`, and the send lands on a closed channel. The
  sends are non-blocking, so holding the lock across them costs nothing — the reasoning that argued
  for releasing it first was simply wrong.

- **The interest layer derives** (`internal/store/interest.go` + `internal/derive`, TODO 5.9 & 6.9) —
  the job that turns the raw engagement log into feed, term and domain affinity, topics, and the
  materialised homepage. **D18's two stages run in order and the code says so**: recall first (passive
  signals produce and order a candidate set), then precision (the deliberate acts — verdicts, notes,
  tags, click-throughs — *scale* what recall thought rather than being more weighted terms). Folding
  them into one sum is the failure D18 rejected, and it fails invisibly because the page still looks
  full. Everything written is a cache: `ClearDerived` then re-run produces byte-identical output, and
  a test asserts it — which is what makes `engagements` the only irreplaceable table here and "throw
  it away" a safe repair. **R17 is asserted end to end**: a mark-all-read over twelve items moves not
  one affinity score. A user's topic rename and suppression survive rederivation, matched by the
  cluster's top three terms rather than by an id that is regenerated each pass — §18.2 promises
  topics are editable, and a nightly job that renamed someone's interests would be untrustworthy in a
  way accuracy cannot compensate for. Half-life is fitted per source from the engagement-vs-age
  curve, and returns zero rather than guessing under ten samples.

- **Rule fan-out** (`internal/fanout`, `internal/store/fanout.go`, migration 0014, TODO 6.7) — the
  per-subscriber job that puts `internal/rules` to work. §13.2's decision is the whole design and is
  easy to get backwards: rules are per-user and items are global (A14), so evaluation happens **once
  per subscriber, never once at ingest** — otherwise one person's mute filter hides an article from
  every other subscriber, and the symptom lands in an account with no rule at all. A test asserts
  exactly that. Mute is a flag, not a deletion (§13.3): the item stays, `UnmuteByRule` reverses an
  over-broad rule in one statement, and a MUTED view can show what a rule is eating. The state
  upsert **never overwrites what the reader set** — `coalesce(existing, new)` — because the queue is
  at-least-once and a re-run must not un-star something a person starred. Rules match plain **text**,
  not markup, or "contains rust" matches a footer link to rust-lang.org that is invisible when they
  check the article. Both structural guards caught all eleven new unscoped repository methods and
  each now carries a written reason; `FanoutItems` is scoped by the `Subscriber` it takes rather than
  by a `Scope`, deliberately — two structs that must agree about who the user is can disagree, and
  one cannot.

- **The durable job queue** (`internal/store/jobs.go` + `internal/jobs`, TODO 6.4) — SQLite-backed,
  so enqueueing and the write that caused it are **one transaction**, which is the property no
  external broker can give you at any price. `locked_by` + `locked_at` are what make it
  restart-*survivable* rather than merely durable: without them a crashed worker's jobs sit in
  `running` forever and the queue loses a slot per crash, silently. Per-kind concurrency caps
  (§22.7) so pack building cannot starve rule fan-out — **the test for that found a real race in the
  first cut**, where six workers computed saturation simultaneously, all saw pack at zero and all
  claimed a pack job before any registered; claiming and booking the slot now share one critical
  section. A panicking handler fails its job rather than the process, since one malformed feed must
  not stop every other subscriber's work. Dead jobs are kept with their cause: a dead-letter queue
  nobody can read is a deleted job with extra steps. CI now runs `-race` on the Linux job, because
  the Windows box this is developed on is arm64 and **cannot** run the race detector at all.

- **Newsletters parse** (`internal/mailparse`, TODO 4.8) — MIME in, a normalised item out, no IMAP
  and no network. `net/mail` parses headers and stops there, so this adds the three things every real
  newsletter needs: multipart tree walking, quoted-printable and base64 transfer decoding, and RFC
  2047 encoded-word headers (undecoded, `=?UTF-8?Q?...?=` becomes the item's title verbatim). Within
  a `multipart/alternative` the **last** part wins, because RFC 2046 orders alternatives
  worst-to-best and a first-wins reader takes the "view this in your browser" fallback on every
  message that has one. The body leaves under `sanitize.Newsletter`, so **every remote image is
  dropped — pixel and photograph alike** — and the item records that it happened, since a newsletter
  rendering without its illustrations looks broken unless the reader is told it was deliberate.
  `NaturalKey` is the one correct way to build the per-user source key (§6.4): global keying would
  merge two people's private mail into one row, which is a privacy failure rather than a bug.
  Attachments never reach the body, nesting is depth-bounded, and a missing Message-ID falls back to
  a stable content hash so re-polling stays idempotent.

- **Scraped sources** (`internal/scrapesel`, TODO 4.7) — a scrape rule plus a page of HTML becomes
  items in the same shape a feed produces, so ingest, dedup, health, rules, ranking and search all
  work on a site with no feed without knowing it was scraped. Pure, so the rule editor can preview
  against a saved page rather than making the author edit selectors against a live site and guess.
  `Compile` holds every refusable error — including the most useful one, a link selector that reads
  text instead of `@href`, which otherwise yields a feed of items whose URL is the anchor's label.
  `Extract` never fails on content: a site can serve anything, and the caller needs `Matched` and
  `Skipped` to tell a redesign that broke the selector apart from a site that stopped publishing,
  which look identical from the item count alone. Selectors matching the container itself are handled
  (cascadia only searches descendants), the summary reads inner HTML while everything else reads text
  or an attribute, scraped content goes through `sanitize.Feed`, `javascript:` links never become
  items, and identity keeps the URL fragment — a one-page site distinguishes its entries by anchor
  and nothing else, the same trap the feed corpus found.

- **Bookmark interchange** (`internal/netscape`, TODO 4.6) — the Netscape bookmark format both ways
  plus Chrome's JSON in. The format is thirty years old, has no specification, and is what every
  browser imports; its defining property is that `<DT>` and `<p>` are never closed and nesting comes
  from document order. Read with an HTML5 parser, which handles exactly that soup — and **written in
  the same malformed shape on purpose**, because browsers parse it by convention and a tidy
  well-formed file is one some importers get wrong. `javascript:` entries are refused on both paths:
  a bookmarklet is code, and importing one as a link produces an entry that executes when clicked.
  Two bugs the tests caught: sibling folders sharing a path slice, where the second overwrites the
  first's name in every bookmark already collected (invisible until a file has two folders at one
  depth, which is every real file); and `ADD_DATE` units, where a 16-digit value fell into the
  milliseconds branch and produced the year 425014. Four epoch units are now discriminated by
  magnitude, with the boundary between unix-µs and WebKit-µs — the only pair within one order of
  magnitude — placed deliberately rather than at a round number. Chrome's localised root names are
  keyed off the stable map key, so a German profile imports the same as an English one.

- **Recommendations** (`internal/recommend`, TODO 4.12) — pure scoring over harvested candidates,
  with the §18.7 health gate that **refuses a dead site and a firehose for opposite reasons**, plus
  no-feed, unreachable, aggregator, undated, already-subscribed, muted, and dismissed. The asymmetry
  is deliberate: a missed recommendation costs one unshown card, a bad one costs trust in the whole
  feature, after which the good ones go unshown too. Every survivor carries the evidence sentence
  §18.7 shows verbatim — *"3 writers you read linked here 11 times · you saved 4 of its articles via
  Hacker News · matches your Npu Inference reading · posts ~7 a week"* — assembled from the same
  fields that produced the score, because this is the one place the reader is asked to act on the
  system's say-so and an unexplained recommendation is indistinguishable from an advert. Distinct
  referring writers outweigh raw link count (three people linking once each beats one linking six
  times, or the scorer just finds whoever links most), and links are weighted by engagement with the
  *linking* article. Adjacent candidates get reserved slots, because they score lower by construction
  and a plain top-N drops them every time — which would make §18.7's anti-filter-bubble guardrail a
  comment rather than a behaviour. Rejections are returned with reasons rather than discarded: a
  health gate nobody can inspect is one nobody can fix.

- **Interest topics** (`internal/topics`, TODO 4.10) — pure clustering over TF-IDF vectors, so Smart
  needs no model and no network. §18.2's argument holds: a single interest vector is the average of
  your interests and matches none of them, and someone reading SQLite internals, NPU inference and
  the Go runtime has a centroid sitting in empty space between the three. Labels are deterministic
  from the heaviest terms — dull on purpose, because a label that changes on recomputation renames
  the reader's interests behind their back every night. Input is sorted before clustering so tie
  breaks cannot depend on row order. Clusters below three members are reported as *unclustered*
  rather than promoted: two items sharing a rare word are a coincidence, and a model that forces
  every article into a topic invents interests. Also ships `Nearest` (an item scores against its
  nearest topic, never the average — which is what lets explainability say "matches your NPU
  inference reading"), `Starved` for Explore's under-served slot, and `Concentration` so the app can
  say "70% of your reading is one topic". Under 50 engaged items it reports cold start instead of
  presenting a confident wrong answer.

- **The ranking scorer** (`internal/rank`, TODO 4.11) — pure `(Signals, Item, Weights, now) ->
  (score, []Reason)`. **The score is literally the sum of its reasons**, asserted by a test, because
  §18.9 shows them verbatim and a reason list that has drifted from the arithmetic is a lie that
  looks like transparency. `VolumePenalty` is logarithmic and clamped to 1: linear would make a
  100-a-day feed ten times worse than a 10-a-day one and effectively mute feeds the reader chose,
  while unbounded would mean the weights stop describing relative influence. Half-life is per source,
  so a day-old item scores differently as news than as an essay. In **highlights mode** the feed term
  is dropped entirely and its weight redistributes to topic, domain and corroboration — nobody likes
  Hacker News, they like specific things on Hacker News — and the threshold is set as a **rate**
  ("about 3 a week"), solved into a cutoff, because nobody can reason about "score > 0.62". Too small
  a sample refuses to fit and shows everything rather than inventing a threshold, since a newly
  subscribed firehose showing nothing reads as broken. Pinned by a golden fixture with its term
  breakdown recorded, and by the test the ticket asks for: a weekly essayist still reaches the top
  ten against 94 items a day, and the firehose is not muted out of it either.

- **The rules engine matcher** (`internal/rules`, TODO 4.9) — pure: `(item, []Rule, now) -> []Action`,
  no database, no clock, no side effects. That is what lets §13.4's dry-run preview be *the same
  code* as the apply; a preview implemented separately eventually lies about what the rule will do,
  which is worse than having none. All ten fields, all seven operators, ordering, and
  `stop_processing` in both its rule-flag and action forms. Three decisions worth arguing with: an
  empty condition set matches **nothing** (empty-AND is conventionally true, and true here means a
  half-finished rule silently mutes the entire feed); the **earlier** rule wins on conflict, because
  otherwise moving a rule up the list would stop changing its precedence; and `not_contains` over the
  tag list means "no tag matches" rather than "some tag doesn't", which would be true of nearly every
  tagged item. An uncompilable regex is refused at authoring time and matches nothing at evaluation
  time — both halves matter, since a pattern that somehow got saved must not take the feed down.
  ~2µs per item per subscriber, so a 50-item fan-out to 200 subscribers is ~20ms.

- **The cross-tenant leak harness** (`internal/store/leak_test.go`, TODO 3.7 / G2 / T1) — the test
  the plan calls the highest-value one in the project. It does not test methods, it **enumerates**
  them: 38 scoped repository methods swept automatically, each called under tenant A's `Scope` while
  being handed tenant B's identifiers, which is the shape of the real attack — a valid session plus
  someone else's id. Every row tenant B owns carries a canary string, and any appearance of it at any
  depth in any return value is a leak. A method that takes no `Scope` must be listed in
  `unscopedByDesign` with a stated reason or the test fails, so a new method cannot quietly arrive
  uncovered. Guard 4 already proved a `Scope` was *taken*; this is what proves it reaches the `WHERE`
  clause, and a method that accepts a Scope and ignores it is exactly as leaky as one that never
  asked while looking correct in review. Verified by deliberately breaking `ListFeeds`' tenant filter
  and confirming the harness names the method and prints the leaked value.

- **The rest of the §6 schema** (migrations 0009–0013, TODO 3.1): 40 tables taking the database from
  the reading core to the whole specification — identity and invites, item tags (A21), item
  revisions, bookmarks and their archives, saved views, the rules engine, scraped sources, mailboxes,
  the derived interest layer, the job queue, settings, idempotency, offline packs, notifications and
  webhooks. Two conventions differ from the DDL sketched in §6.8 and both are forced: ids are TEXT
  because every shipped table uses `idgen`'s sortable ids, and **a foreign key whose type does not
  match its referent is not a foreign key** — SQLite accepts the DDL and then never matches a row.
  Three new tests: every specified table exists by name (a count would pass a rename), every
  FK-shaped column carries a `REFERENCES` or an explicit exemption with a reason, and `notes_fts` /
  `bookmarks_fts` track inserts, updates *and* deletes — FTS5 external content does not observe base
  table writes on its own, so without triggers the index is correct once and then drifts silently.
- **Reader mode has an extractor** (`internal/extract`, TODO 4.4). D7 is **resolved: go-shiori/
  go-readability**, decided by the bake-off rather than by reputation. All three candidates worked on
  all twelve pages; the split was on two things a character count hides. Trafilatura's text
  serialiser inserts spaces around inline elements — "The Room , Troll 2 ," — 199 artefacts against
  readability's 5, and three of the five consumers read the text while one reads it aloud. And on a
  photo-heavy hardware review readability kept 34 images to trafilatura's 8, which is losing the
  article. Readability's one apparent weakness, 5.8× markup bulk on WordPress, turned out to be 38 KB
  of `srcset` — data, not cruft, and removed by the sanitizer's allowlist regardless. The measurement
  that looked decisive was measuring something that does not survive to storage. Returns HTML and
  plain text from one pass, because deriving text at four call sites yields four different word
  counts and the one used for ranking is the one nobody checked.
- **A third double-decode, and a structural fix for the class.** `go-shiori/dom` runs *statistical*
  charset detection over whatever it is handed, so correct UTF-8 that is mostly ASCII with a few
  accented words gets re-read as Latin-1: "café" → "cafÃ©". `charsetdec` now also retags HTML `<meta
  charset>` and `http-equiv` declarations, but the real fix is that `extract` parses the DOM itself
  and hands readability a document — nobody downstream gets a second chance to decode. It hid well:
  a short page gives the detector too little signal and comes through clean, so it only appears at
  realistic article length.
- **Sanitisation has named policies** (`internal/sanitize`, TODO 2.9). GWC's sanitizer stays the
  engine; what it lacks is an opinion about where the HTML came from, and that turns out to be the
  whole question. Four sources, four threat models: a feed keeps its photographs because a hardware
  review is mostly photographs; a newsletter drops remote images outright, because in email a remote
  image is a read receipt and proxying it still tells the sender you opened the message; an archived
  page is our own extraction output; a public excerpt gets text and emphasis and nothing else. Plus
  two things an allowlist cannot express, because they are decisions about values rather than names:
  every link gains `rel="noopener noreferrer"` (without it, `target=_blank` hands the opened page a
  handle to navigate ours — a phishing primitive that costs an attacker one attribute in their own
  feed), and tracking pixels are removed. A 48-vector XSS corpus runs against *every* policy, so a
  policy added later inherits the whole corpus and a policy loosened later has to survive it. An
  unmapped policy value fails closed to the strictest one.

- **Feeds can be filed: folders, end to end** — store, service, five RPCs, and an OPML importer that
  finally keeps the categories it had been parsing and discarding. A subscription has one folder and
  any number of tags, which is not an arbitrary asymmetry: a folder answers *where does this live*
  (one answer, or the reader has not decided), a tag answers *what is this about* (as many as they
  like). Per-user, capped at 200, names capped at the width the rail can draw. `repo.Subscribe` now
  takes a `NewSubscription` struct rather than four positional strings.
- **A tag has a rail label and a glyph** (migration 0008). The identity (`rust`) and the destination
  (`Systems programming`) want different names, and one string made the reader choose which job to
  do badly. Empty means "use the name", the same override idiom as `subscriptions.title`. The glyph
  is stored as the character, not an index into the catalogue, so retiring an entry can never
  silently rewrite someone's tags; the catalogue itself lives in `internal/tagglyph` because the
  browser offers the choice and the server enforces it, and two lists would let a picker offer what
  a save refuses.
- **The D7 extraction bake-off** (`internal/extract/bakeoff`, its own module): twelve real pages —
  including a README and an MDN page, which are not articles and which a library should decline
  rather than hallucinate a body out of — compared across trafilatura, dom-distiller and
  readability. It stays in the tree because "why aren't we using X?" deserves a command anyone can
  re-run, not a paragraph nobody can check.
- **Undo for mark-all-read.** `MarkAllRead` returns a token identifying exactly the rows it flipped,
  and `UndoMarkAllRead` puts them back. The token is the batch's `rev` — server-assigned and
  monotonic per user — so there is no journal table and no server-side session state to expire; a
  wall-clock stamp collides inside one ~15ms Windows timer tick and would resurrect items read
  moments before the batch. The mark's upsert now skips rows that were already read rather than
  keeping their timestamp, which is what makes the batch identifiable at all.
- **The server describes itself** (`internal/obs`, `GetServerStats`, `ListLogs`): uptime, storage,
  scoped row counts, heap and goroutines, polling state, per-RPC latency, and the last N log records
  at or above a level. In memory, bounded by count rather than age, sampled percentiles from a fixed
  reservoir — a ring buffer, not a table. Both RPCs are authenticated and every count is the
  caller's, because a status screen reporting global counts on a multi-tenant instance discloses
  other tenants' activity. Timing is one unary interceptor, not per-handler instrumentation.
- **The client half of the signals layer** (`client/track`, `client/platform/signals_*.go`):
  attentive-time accumulation, impression coalescing (~1s on screen before a row counts), batching
  and shipping. Arithmetic behind a `Sender` interface so it is tested natively, off the browser;
  the DOM-shaped half stays in the quarantined `client/platform`. Every failure path drops data and
  continues — analytics may never make reading look broken (A34).
- **The connection indicator tells the truth while idle.** `Client.Watch` subscribes to the
  channel's own state rather than updating only when a call happens, so a tab that lost the tunnel
  an hour ago stops showing a healthy dot, and refetches when it comes back.
- **Repository documentation set** — README with screenshots, `CONTRIBUTING.md`, `SECURITY.md`,
  `CODE_OF_CONDUCT.md`, issue and pull-request templates, and this changelog. No code changed.
- **Per-feed settings**, behind a gear on each sidebar row — hidden until hover or keyboard focus,
  always visible below 900px. Grouped by *who a setting belongs to*: yours (name override, ranked
  feed, mute, offline depth, tags), shared (feed URL, site URL, poll interval — polled once for the
  whole server, so the heading says how many other people are affected), health (last fetch, last
  success, next fetch, items held, and the publisher's own error string verbatim), and actions.
  `fetch_interval_s` is clamped at the write to 5 minutes–1 week, and `next_fetch_at` recomputes from
  the *last* fetch, so lengthening an interval cannot postpone an overdue poll.
- **A glyph vocabulary.** Left of a label means what a thing *is* — the same glyph for a destination
  everywhere it appears, so the sidebar, the palette and the tab bar teach one vocabulary rather than
  three. Right is reserved for what will *happen*, and currently holds exactly one mark: the arrow on
  a link that leaves for another site.
- **Tier 8b in `TODO.md`** — backfills the twenty-four features that were built from using the
  reader rather than from the spec. An untracked feature is one nobody can decide to remove.

### Changed

- **Full motion is the default, on every machine.** The sheet carried a
  `(prefers-reduced-motion: reduce)` rule that zeroed `--mo` for anyone who had never opened the
  Appearance screen, so on those machines the default experience was an application whose every
  transition took zero time — and nothing said so, because the Appearance screen went on reporting
  full motion while the sheet quietly overruled it. The movement here is not decoration: it says
  which pane you came from and what changed. The OS preference is now a *choice* — "follow my system
  setting" — resolved in Go and written as an ordinary `data-motion` value, so `<html>` always states
  which mode is in force. A test fails if the media query returns.
- **Tidings is now ArticleFlux** — module path, proto package (`tidings.v1` → `articleflux.v1`),
  command, and the default database file. Renaming the wire package is a break, done now because
  pre-1.0 there is no old client to break and after the sync API ships there will be, permanently.
  The command is spelled `articleflux` everywhere a shell sees it; the product keeps its capitals in
  prose.
- **The big-database tests run against a copy.** They mutate — one of them marks every item read —
  and they did it to the development database, which is how this repository destroyed its own
  reading state twice on 2026-07-26 inside a `go test ./...` that reported PASS both times. `openDev`
  now copies the database and its WAL into `t.TempDir()`.
- **`ResetUserState` clears notes and feed tags too.** The e2e suite shares one database and resets
  between tests; a note or tag left behind was visible to every test after it.
- **`bulk_read` records one row carrying a count**, not one row per item. It scores 0.0 by design, so
  143 rows describing a single act were 143 rows of noise in the busiest table in the layer.
- **The task runner moved to `scripts/make.ps1`.** Not a rename: the script anchored all nine of its
  paths on `$PSScriptRoot`, which was the repository root only because the file happened to sit in
  it — from `scripts/` those same expressions would have built into a directory nobody serves from,
  silently and successfully.
- **Scrolling the item list no longer janks.** Flicking with a reading stream open measured p95
  49.9ms, fourteen dropped frames and two 66ms long tasks: `html.RawHTML` sanitises *and* parses
  markup into nodes, and scrolling the list re-rendered the component owning the stream, so a single
  scroll frame did thirty-nine full HTML parses. Bodies are now cached by item id, the palette is
  assembled only while open, the favicon-host map is built when feeds change rather than per frame,
  and the two stream-edge checks are id comparisons instead of linear scans of 3,621 items.
  **p95 49.9ms → 16.7ms, drops 14/152 → 2/154, long tasks 2 → 0.**
- **`-allow-private` is a narrower deny list, not an off switch.** It previously disabled the SSRF
  guard outright, and since the dev server sets it automatically on a loopback bind, the most
  commonly-run configuration had no SSRF protection at all. RFC1918 and loopback are now reachable;
  link-local and the cloud metadata endpoint never are.
- **`TODO.md` audited against the code.** Thirty-seven items ticked with evidence, fourteen left open
  but annotated with precisely what part exists. A checklist that says nothing about its half-built
  items is worse than one that admits them.

### Fixed

- **The tab froze for exactly eight seconds, repeatedly.** In Go/wasm a `js.FuncOf` callback that
  blocks holds the JavaScript event loop — and the tunnel is a WebSocket, so the reply the blocked
  call waits for arrives *on the loop it is holding*. The call could not succeed; it could only run to
  its deadline. The signals outbox was flushing inline from the page-hide handler and from `Stop`
  (reached from an effect cleanup, and effects run on the frame loop, which is also a JS callback), so
  every backgrounding froze the app for the full `engagementTimeout`. Reported as *"the collapsable
  menu items take very long to collapse"*: the rail was never slow, the click was queued behind a page
  deadlocked against itself. Both paths now hand the ship to a goroutine, and `Flush` will not start a
  second batch while one is in the air — a wedged tunnel was otherwise accumulating blocked goroutines
  each holding a slice of events. **The signature to recognise: a Chrome `[Violation]` whose duration
  equals one of our own timeout constants to the millisecond.**
- **A click could be silently dropped.** The delegated dispatcher read the click's target out of refs
  shared by every click, one frame after the click, from inside `ui.PostAsync`. Two clicks in a single
  frame made the first handler act on the second's target and then clear the ref, after which the
  second handler acted on nothing at all — no error, no save, nothing to notice. The payload is now
  captured on the click's own stack. Frames are not always 16ms: a busy or throttled tab stretches the
  window this raced in to seconds.
- **Removing a tag is immediate.** It waited on two sequential round trips — the write, then the tag
  list refetch it queued — with no feedback in between, so the chip sat under the pointer looking
  broken and the reader's next move was to click it again. Removal now applies the server's own rules
  locally with a rollback on failure. Adding cannot be optimistic (the id is the server's to assign),
  so it shows the wait instead of hiding it: the chip appears at once, pending, with its remove
  control withheld because there is nothing on the server to remove yet.
- **Two derived values were being rebuilt on every frame** — the article chips' source-to-tags map
  (walked, allocated per feed and sorted, from inside the render) and the tag glyph catalogue's
  grouping (a full rescan per group per repaint, for a value that is constant). Both now rebuild only
  where their inputs change. The chip map's identity is stable as a result, which is what lets the
  article pane's props comparison skip work at all.
- **The OPML fixture was being ignored, so two tests never ran.** `*.opml` correctly excludes real
  exports — an export is a complete list of what one person reads — but it also swallowed the
  package's only full-size parse fixture, and both tests that opened it called `t.Skip` when it was
  missing. A fresh clone ran neither and the package reported ok. The fixture is now synthesised to
  carry every hazard the real export had (144 entries, scrapers among the RSS rows, escaped
  ampersands, entities inside attribute values), checked in, and the skips are `t.Fatal`.
- **Clicking inside a dialog no longer closes it.** The delegated listener resolves a click to the
  nearest ancestor carrying `data-action`; with none on the dialog, every click inside walked up to
  the backdrop and hit its close action, so touching a text field shut the panel. Affected the feed
  panel, the command palette and the help sheet.
- **Feeds can be renamed from a button**, not only by pressing Enter. A text field whose sole commit
  is an unadvertised keystroke looks broken.
- **Per-source hues render.** `html.Props{Style: …}` cannot set CSS custom properties — GWC's adapter
  assigns JS properties, and that path does not reach `--vars`, so everything rendered grey. Fixed by
  passing a style *string* through `Raw`, which takes the `setAttribute` path.
- **Search-on-Enter fires.** A `func(string)` event handler receives `event.target.value` rather than
  the key, so the handler never matched.

## 2026-07-26 — the vertical slice (untagged)

The vertical slice: storage → ingestion → service → gRPC-over-tunnel → wasm client, running against
151 subscriptions and 3,621 real items.

### Added

- **Reading.** Three-pane desktop collapsing at 1220px and 900px to a single column with a
  persistent tab bar. The article pane is a continuous stream — reaching the bottom appends the next
  piece, scrolling up prepends the previous one with the scroll position held, and nothing is ever
  taken away from under the reader. Scrolling through an article marks it read.
- **The list**, virtualised at a fixed 96px row and sized to the scope's true total rather than to
  what has been fetched, so the scrollbar is honest from the first paint and dragging into unloaded
  territory fills toward you.
- **Signals** — like/dislike verdicts, read-later, notes, per-feed tags, mark-unread. The negative
  half is the point: it is the signal ranking needs and there was nowhere to put it.
- **Listening** — the browser's own synthesiser, free and offline, plus an opt-in Smart+ voice behind
  a host allowlist and a per-user switch that defaults to off.
- **Keyboard** — arrows within a pane, Tab between panes, `Ctrl-K` for the command palette, `?` for
  the sheet that documents all of it. Everything is reachable without a pointer.
- **Continuity** — scope, article and every filter are account state, restored before the first list
  is fetched.
- **Full-text search** over SQLite FTS5, with a permanent test gating it (the extension is loadable,
  not compiled in, so a dependency bump that dropped it would otherwise silently remove search).
- **Four structural guards in CI** — no `.css` files, no application JavaScript, no SQL outside
  `internal/store`, no `syscall/js` outside `client/platform`.
- **The document set** — `plan.md` as the spec of record, `TODO.md` the dependency-ordered build,
  `FLOWS.md` the nine paths that are easy to get subtly wrong, `design/` the visual spec.
