# Changelog

All notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and once there is a release this project
will follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Pre-1.0: `main` is the only supported version and the wire contract is additive-only within `v1`.
The full reasoning behind any entry lives in the commit message; this file is the index.

## [Unreleased]

### Security

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

### Added

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
