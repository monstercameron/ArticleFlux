# ArticleFlux — a multi-tenant feed reader, bookmark manager, and ranking engine

*GoWebComponents v5 + GoGRPCBridge. Working name. Started 2026-07-26.*

---

## Status — 2026-07-26

**The reader runs.** `./scripts/make.ps1 dev` serves it on **:9000**, against real feeds.

| | |
|---|---|
| Vertical slice | storage → ingestion → service → gRPC-over-tunnel → wasm client |
| Feeds proven against | HN, Lobsters, The Verge, Simon Willison, Dan Luu, Rust, Go, xkcd — **256 items** |
| Go tests | 14 packages |
| e2e | Playwright, desktop + phone, against a real server and a real database |
| Gates closed | **G1** (FTS5 — see D2), **G5** (23.8 MB / 5.2 MB gzipped — see R4) |
| Structural guards | all four green in CI |

**Build order changed.** The tiers were being built bottom-up and the first visible artefact was a
build-status page, which was correctly rejected. A vertical slice was cut instead and the breadth is
being backfilled behind it — see the note at the top of `TODO.md`. **No decision changed**; the slice
narrowed scope, not design.

**Three findings that only appeared by running things**, each now written into the section it affects:

1. **D2 / G1** — FTS5 is a per-connection loadable extension, not compiled in. The desk answer said
   otherwise. Binds §22.2.
2. **R4 / G5** — the bundle is 23.8 MB (5.2 MB gzipped), which makes the Service Worker load-bearing
   rather than optional and puts A5's budget under real pressure.
3. **§21** — `-allow-private`, added so a self-hosted instance could reach a LAN feed, originally
   disabled the SSRF guard outright. Since the dev server sets it automatically on a loopback bind,
   the most commonly-run configuration had no SSRF protection at all. It is now a *narrower deny
   list*: RFC1918 and loopback are reachable, link-local and the cloud metadata endpoint never are.

Two UI defects worth recording because both were invisible to every test that did not open a browser:
GWC's `Props.Style` cannot set CSS custom properties (the per-source hue silently rendered grey), and
a `func(string)` event handler receives `event.target.value` rather than the key (search-on-Enter
silently never fired). Both are noted in `design/README.md`.

### What landed after the first commit — 2026-07-26, night

Written down here because all of it was built from use rather than from the spec, and an undocumented
feature is one nobody can decide to keep. Each has its section; this is the index.

| Shipped | Where it is specified |
|---|---|
| **`AuthService`** — login / logout / whoami, hashed revocable sessions, attempt limiting | §7.1a, **A36** |
| **`-dev` is a flag, not a bind address** — the reverse-proxy hole, closed | §21, **A36** |
| **Operator CLI** — `init` · `adduser` · `passwd` · `migrate` · `backup` | §22.3, §22.5, §7.2 |
| **`Preflight`** — the server refuses to listen if it cannot work | §22.4 |
| **`/readyz`** — readiness, separate from liveness, touching the database | §22.4 |
| **`store.Backup`** — `VACUUM INTO` + integrity check + retention | §22.5 |
| **The login screen** — `Root` · `Login` · the token, the interceptor, `WhoAmI` at boot | §7.1b |
| **Categories** — folders in the rail, one per feed, filed at add-time | §6.10, **A37** |
| **Tag identity vs presentation** — `label` + `glyph` (`0008`), `internal/tagglyph` | §6.6, **A38** |
| **The settings surface** — seven tabs, including the log ring and per-RPC latency | §20.17 |
| **The add-a-feed dialog** — name and file it at the moment of adding | §20.18 |
| **Themes, accents and the motion system** — every paintable value is a token, and the Appearance surface writes them at runtime | §20.16, **A39** |
| **The splash** — real download progress, in the reader's own theme, before the module exists | §20.20 |
| **Focus mode** — the reading pane takes the window; the columns close rather than vanish | §20.21 |
| **The filmstrip** — below 1220px the panes slide instead of hiding each other | §20.22 |
| **Dialogs that leave** — all six overlays animate in both directions, and are untabbable closed | §20.23 |
| **`internal/sanitize`** — five named policies over GWC's engine | §21, TODO 2.9 |
| **The D7 extraction bake-off** — 12 committed pages, three libraries, one command | §25.1 D7 |

**Verified 2026-07-26, 21:35** — `go build ./...`, `GOOS=js GOARCH=wasm go build ./client/...` and
`go test ./...` are all green. **e2e is not**: the last Playwright run failed on a categories test, and
the suite has not been updated for the login screen it now has to get through. *(Earlier in this same
batch the wasm build was broken for a stretch —
the rail's category props were half-wired while `railProps` had already changed — and it went unnoticed
because **`go build ./...` does not compile the client**. Worth stating once: a green Go suite is not
evidence the app builds, let alone runs. Putting the wasm build on CI's default path is the fix, and it
is TODO 8b.32.)*
*Rev 8 — post-review. Two adversarial passes applied: correctness/consistency and operational
lifecycle. §22 (Operations) is new and was the largest hole in rev 7.*

---

## The document set

Five documents, one spec. Each owns something the others must not duplicate, because duplicated facts
drift and drifted facts get implemented.

| Doc | Owns | Does not contain |
|---|---|---|
| **`plan.md`** | **The spec of record.** Decisions (`A#`), open questions (`D#`), risks (`R#`), schema, services, milestones (`M#`), tests (`T#`) | Build order — that's `TODO.md` |
| **`TODO.md`** | **Build order.** Dependency-ordered tiers, the five gates, and the page / settings / component / flow inventories (Appendices A–D) | Decisions. It *cites* them by id |
| **`FLOWS.md`** | **Behaviour of the nine paths that are easy to get subtly wrong**, drawn so the wrong version is visibly wrong | Anything it doesn't draw |
| **`docs/FEATURES.md`** | **Behaviour.** Every feature and capability from the outside — what happens when a reader touches it — and whether it is shipped, partial, engine-only or planned | Decisions, build order, or schema. It *describes*; it never settles anything |
| **`design/`** | Visual spec — palette, type, layout, interaction. **Mockups, not source** (see `design/README.md`) | Implementation. It is hand-written CSS/JS on purpose; A26 governs the real thing |

**Precedence.** `plan.md` wins. If `TODO.md` or `FLOWS.md` contradicts it, they are wrong and get
fixed. **If the implementation contradicts `plan.md`, the plan is wrong — and it gets corrected in the
same change, not later.** A spec that has quietly drifted from the code is worse than no spec, because
it still gets trusted.

**Stable identifiers, all greppable** — use these rather than prose references, so an agent can resolve
them mechanically:

| Id | Meaning | Defined in |
|---|---|---|
| `A1`–`A41` | Settled decisions | §2, §18.1a for A34–A35, §27.14 for A41 |
| `D0`–`D23` | Open decisions | §25, and §27.14 for D23 |
| `R0`–`R23` | Risks | §25, and §27.14 for R23 |
| `M0`–`M29` | Milestones | §24, and §27.14 for M29 |
| `T1`–`T24` | Tests that must stay green | §23, and §27.11 for T24 |
| `§N` / `§N.M` | Plan sections | here |
| `1.1`–`9.x` | Build items | `TODO.md` |
| `G1`–`G5` | Gates — stop and write down a number | `TODO.md` |

**Every milestone has a complete brief** in `TODO.md` Tier 9: the plan sections that define it, the
pages, the components, the flow diagram, and the tests that must go green. Start there, not here.

### When the spec is silent

The plan is deliberately detailed, and it is still not complete — no plan is. This is the rule for
what happens at the edge, and it exists because **an agent that guesses produces work that looks
finished**. A wrong variable name is visible in review; a wrongly-invented table is not.

| Situation | What to do |
|---|---|
| An open **`D`** covers it | **Stop. Ask. Record the answer as a resolution.** Never pick one and proceed — nine of them block a milestone precisely because guessing wrong is expensive |
| No `D`, and the choice is **local** — a name, an error string, a fixture shape, a helper's signature | **Decide it and move on.** Note it in the commit message. Asking about these wastes more than it protects |
| No `D`, and the choice is **structural** — a table, a column, an RPC, a capability, a transport, an index, a dependency | **Stop and ask.** It becomes a new `D` (open question) or a new `A` (settled decision), written down before the code |
| A **new third-party dependency** | **Always ask**, without exception. Every dependency is a permanent decision made in thirty seconds |
| The implementation contradicts the plan | **The plan is wrong.** Fix it *in the same change*, not later |
| A **gate** (`G1`–`G5`) hasn't been run | Run it. Its whole purpose is that the answer cannot be reasoned to |

**Two things are never silent-decidable**, whatever the pressure: anything touching **tenant
isolation** (§6.1) and anything touching the **LLM egress boundary** (§18.8). Both fail silently, both
are security properties, and both have a test that must be extended alongside any change.

---

## 0. Read this first

### What rev 8 changed

Rev 7 was reviewed twice — once for internal contradictions and unverifiable technical claims, once
for operational gaps. Both found real problems. The seven that mattered most:

1. **`ON DELETE CASCADE` on global tables was a cross-tenant data bomb** (§6.2). Deleting one `sources`
   row would have wiped every *other* tenant's favorites, tags, and shares for that feed — directly
   contradicting §6.7's retention promise. **Sources are now soft-deactivated, never deleted.**
2. **Newsletter sources leaked private mail across tenants** (§14.1). Keying a mailbox-derived source
   by sender address would have merged two people's private email into one global `items` row.
   **Mailbox sources are now per-user-keyed, and A14's global rationale explicitly does not apply.**
3. **There was no schema migration story at all** (§22.1) across 25 milestones of schema changes on a
   live database.
4. **There was no way to create the first user** (§22.3). Invites require an admin; resets require a
   user. Day zero had no path in.
5. **SQLite concurrency was never specified** (§22.2) despite four concurrent writers in the
   architecture diagram. `SQLITE_BUSY` on day one, not at scale.
6. **"Backups are non-optional" had no mechanism** (§22.5) — a raw file copy of a live WAL database
   is a torn, possibly unrestorable snapshot.
7. **Tier-1 extraction was scheduled *after* the two milestones that depend on it** (§24).

Also corrected: an api_token could carry admin capabilities into a third-party mobile app; the
`shares` DDL had been dropped in rev 7's compression; the `uis_unread` index didn't serve the sort
shapes the plan called hottest; and **"crib CashFlux's AuthService" was overstated** — verified,
CashFlux uses bcrypt not Argon2id, a hand-rolled JSON codec not protobuf, and has no tenants or
capabilities at all. M2 is more new work than rev 7 implied.

### Honest read on scope

This started as "an RSS reader." It is now a multi-tenant, role-based, internet-hosted platform with a
ranking engine, an archival bookmark manager, a scraper, newsletter ingestion, two LLM integrations,
offline sync, a third-party client API, an automation engine, and an admin console. **26 milestones
across 5 phases.** M8 is the first daily driver; M13 is the ship line. Adding above a line means
moving something below it.

---

## 1. What this is

A self-hosted, internet-reachable, **multi-tenant** knowledge surface:

- **Homepage megafeed** — one ranked river. **Smart** (deterministic, free) / **Smart+** (LLM, opt-in).
- **Feed reader** — keyboard-driven, live-updating, reader-mode extraction, **rules and mute filters**,
  item tags, offline trip packs.
- **Bookmark manager** — any device, archived copies so links don't rot.
- **Any source, not just RSS** — feeds, scraped pages, **email newsletters**.
- **Works with the apps you already use** — a **Google Reader–compatible API** so Reeder, Unread, and
  NetNewsWire sync against it.
- **Trends** — what you actually read, and which feeds died without telling you.
- **Screensaver mode** · **admin console**.

One Go binary owns the data. A GoWebComponents wasm client speaks gRPC over an authenticated
WebSocket tunnel. Everything is exportable in formats other tools accept.

---

## 2. Decisions

| # | Decision | Choice |
|---|---|---|
| **A1** | Source of truth | **Server of record**, with a **read-only offline mirror** (§12) |
| **A2** | Scope | See §5 phases |
| **A3** | Framework | GWC **v5.0.0** — `ui.PostAsync` makes a stream-driven UI safe |
| **A4** | Client storage | **IndexedDB via `interop.PersistentStore` only — no client SQLite** (§12.2) |
| **A5** | Transport (own client) | **gRPC over GoGRPCBridge v1.1.1** |
| **A6** | Article rendering | A **four-tier ladder** (§10.1) |
| **A7** | Feed discovery | **Deterministic first, LLM last, parser always validates** (§11) |
| **A8** | Favorites = starred | One concept, keyboard `s`. Notes **private by default** (§7.7) |
| **A9** | Deployment | **Remote, TLS, authenticated — v1** (§4) |
| **A10** | Standards | Conformance is a feature (§15) |
| **A11** | Ranking tiers | **Smart** free/always-on · **Smart+** LLM/opt-in/**OpenAI** (§18) |
| **A12** | LLM providers | One `llm.Provider`: **OpenAI** for Smart+, **Claude** for discovery/drafting |
| **A13** | Tenancy | **Shared-schema, shared-database, `tenant_id` everywhere** (§6.1) |
| **A14** | Feed normalization | **`sources` (global, polled once) + `subscriptions` (per-user)** (§6.2). **Does not apply to `kind='mailbox'`** (§14.1). |
| **A15** | Authorization | **Capability-based, one interceptor + one repository scope** (§7.4–7.6) |
| **A16** | Configurability | Everything tunable is **three-layer: system → tenant → user** (§8) |
| **A17** | Authentication | **Username + password, no 2FA.** Three-rung recovery (§7.1–7.3) |
| **A18** | Third-party clients | **Google Reader–compatible REST API** (§15.1) — a second, deliberate transport |
| **A19** | Automation | **A per-user rules engine** with mute as a first-class action (§13) |
| **A20** | Email ingestion | **IMAP polling, not an SMTP listener** (§14.1) |
| **A21** | Item tags | **Per-user labels on items**, orthogonal to folders. Prerequisite for A18/A19 (§6.6) |
| **A22** | **Deletion** | **Global rows are never hard-deleted.** `sources` and `items` soft-deactivate; only per-user rows cascade (§6.3). |
| **A23** | **Migrations** | **Numbered, forward-only, applied at boot, checksum-guarded**, one per milestone that touches schema (§22.1) |
| **A24** | **SQLite concurrency** | **WAL + `busy_timeout` + a single serialized writer**, decided up front (§22.2) |
| **A25** | **Ordering authority** | **Server-assigned `rev`, never client wall-clock**, for every conflict resolution (§12.4) |
| **A26** | **Go all the way down** | **All UI logic and all CSS live in GWC.** No `.css` files, no application JavaScript. `syscall/js` is quarantined in one package. The only JS is `wasm_exec.js`, the boot shim, and the Service Worker — which cannot be Go. The shim and the inline `<style>` it comes with are the **one** exemption, and they are exempt for a reason that cannot be engineered away: they run *before the module that emits the stylesheet exists* (§20.6, §20.20). |
| **A27** | **Verdicts, not bookmarks** | **Like / dislike (`user_item_state.rating` ∈ {-1,0,+1}) replaces starring in the UI** (§18.4). Starring answers "keep this"; a verdict answers "was this worth my time", and the *negative* half is the signal ranking actually needs. |
| **A28** | **The reading pane is a stream** | Reaching the end of an article **appends** the next one and scrolling back **prepends** the previous, rather than replacing the pane's contents (§20.9). Scrolling through an article marks it read; scrolling is the whole reading loop. |
| **A29** | **The item list is as long as the scope, not as long as what is loaded** | `ListItemsResponse.total` sizes the virtual list to the true result set from the first paint; unloaded rows are placeholders that resolve (§20.10). |
| **A30** | **Where you were is account state** | Scope, article, and every filter are server-side prefs, restored on connect before the first list is fetched (§20.13). A reader who reloads lands back where they were, on any machine. |
| **A31** | **Listening is free by default and Smart+ by choice** | The browser's own `speechSynthesis` reads articles at no cost, offline, in the voice the reader already chose. OpenAI TTS is an **opt-in per user, on an instance that supplied a key**, behind a host allowlist (§10.7). |
| **A32** | **Keyboard-complete, and it says so** | Arrows move *within* a pane, Tab *between* them, Ctrl-K is the palette, `?` is the sheet that lists all of it (§20.14). |
| **A33** | **A setting is labelled by who it belongs to** | The per-feed panel separates `subscriptions` (yours) from `sources` (shared, polled once for the whole server) and says how many other people are on the other end before you change one (§20.15). |
| **A34** | **The client outbox** | Signals are batched, coalesced and order-preserving in `client/track`, with client-generated ids the server dedupes on (§18.1a). |
| **A35** | **Analytics may never degrade reading** | `RecordEngagements` is the one RPC whose failure the client swallows silently (§18.1a). |
| **A36** | **Authentication is its own thing, never inferred from topology** | A bind address describes the network, not the caller. `-dev` (no login) is an explicit flag, **default off, and refused on any bind but loopback**; `AuthService` is registered unconditionally so one binary serves a laptop and a domain. Sessions are server-side rows, stored as SHA-256, revocable, 30 days (§7.1a). |
| **A37** | **Where vs what: folders are exclusive, tags are not** | A subscription has **at most one folder** and **any number of tags**. A folder answers "where does this live", a tag "what is this about" — collapsing them gives either a secretly-exclusive tag or a rail that cannot say where a feed belongs (§6.10). Flat for now; `parent_id` and the depth `CHECK` stay in the schema. |
| **A38** | **A tag has an identity and a presentation, and only one of them is editable** | `tags.name` is the handle — what you type, what the chip says, what `SetFeedTag` takes — and nothing renames it. `label` and `glyph` are the rail row's, empty meaning "use the name", the same override idiom as `subscriptions.title` over `sources.title` (§6.6). |
| **A39** | **Every paintable value is a token, and motion is one of them** | No literal colour outside a `design.Theme`; a theme is a set of custom-property values, so a new theme costs one list and not a rule per sheet. Every duration is written `calc(var(--mo) * t)` — `--mo: 0` makes reduced motion *absent* rather than suppressed, and there is no way to author an animation that escapes the gate (§20.16). |
| **A40** | **The connection is a state machine, not a boolean** | Five states — `live` · `connecting` · `offline` · `down` · `blocked` — driven by four inputs: gRPC connectivity, a **client-side keepalive verdict**, browser lifecycle events, and the code of the last RPC. **Retry is not the answer to every failure.** Most errors say nothing about the connection at all, and a few — a version-skew refusal, a revoked session, a deleted tenant — are *terminal*: the loop must stop and name a remedy, because retrying a permanent refusal is a loop that never recovers and never says why. And **retry is not durability** — what survives an outage is the outbox (§12.4), not the reconnect (§20.19). |

---

## 3. Architecture

```
┌───────────────── any browser, anywhere (+ offline) ─────────┐   ┌──────────────────────┐
│  Service Worker: app shell + Web Push          (§12, §17)   │   │ Reeder · Unread ·    │
│  IndexedDB: trip packs + outbox + leader election    (§12)  │   │ NetNewsWire · FeedMe │
│  app.wasm (GWC v5) — version-stamped, skew-checked (§22.6)  │   └──────────┬───────────┘
│   Home | Reader | Bookmarks | Trends | Screensaver |         │              │ GReader REST
│   Settings | Admin                                          │              │   (§15.1)
└──────────────────────────┬──────────────────────────────────┘              │
                           │ authenticated wss:// (+ HTTPS for packs)        │
┌──────────────────────────▼─────────────────────────────────────────────────▼───────────┐
│  articleflux                                                                               │
│   http:   static · /grpc · /img · /websub · /pack/:id · /reader/api/0/* · /pub/:slug   │
│           /healthz · /readyz  (unauthenticated, §22.4)                                 │
│   authz:  interceptor → Scope{tenant, user, caps} on every call              (§7)      │
│   grpc:   Auth Profile Tenant Admin Home Feed Item Tag Note Bookmark Rule Render       │
│           Audio Offline Notify Stats Event Import View Settings                        │
│   jobs:   rule fan-out · ranking · extraction · pack build · audio  (queued, §22.7)    │
│   llm:    Provider iface + shared timeout & circuit breaker                 (§22.8)    │
│   poller: sources · scrape rules · IMAP · dead links — priority queue       (§22.7)    │
│   store:  SQLite (WAL, single writer) — repository layer is the ONLY SQL surface       │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 4. A9: remote deployment, and what it forces

All v1: **TLS mandatory** · **auth mandatory** (`WithAuthorize` at the upgrade *before* tunnel
resources are allocated, plus a per-RPC interceptor) · **startup refuses a non-loopback bind without
TLS and a credential** (code, not a README line) · **the SSRF guard is the perimeter** · rate limits
and connection caps are real · a public callback unlocks WebSub · **backups with a real mechanism**
(§22.5).

Rev 7 added three public surfaces, each with its own treatment: the **sync API** (§15.1,
token-authenticated, rate-limited), **public share feeds** (§7.8, unguessable and revocable), and
**outbound webhooks** (§17.2, SSRF-guarded like any other fetch). Rev 8 adds a fourth:
**`/healthz` and `/readyz`** (§22.4), unauthenticated by necessity and therefore deliberately
information-free.

> **Correction carried from review.** Earlier revisions said to "crib CashFlux's `AuthService` and
> admin-console shape." Verified against the source: CashFlux uses **bcrypt** (A17 requires Argon2id),
> a **hand-rolled JSON codec** over `grpc.ServiceDesc` with a comment marking real protobuf as a later
> step (M0 assumes `buf generate`), and has **no tenants table and no capability system**. It is a
> useful *stylistic* reference for device-family and rate-limiter patterns and for the hardened
> `grpctunnel.Wrap` call — **not** a source for tenant, capability, or protobuf plumbing. Budget M2
> and M18 as new work.

---

## 5. Phases

| Phase | Milestones | What you get |
|---|---|---|
| **1. Foundation** | M0–M3 | Transport spike · parser + pipeline + **tier-1 extraction** · **multi-tenant schema incl. item tags, rules, revisions** · **migrations, WAL, bootstrap, backup, health** · auth/users/roles · settings registry · TLS |
| **2. Reader** | M4–M8 | Read path · state + engagement logging · **rules + mute** · item tags · organize/subscribe · interchange · flexible UI · typography · command palette · **M8 = first daily driver** |
| **3. Personal & sync** | M9–M13 | Bookmarks · offline trip packs · **GReader sync API** · notifications · trends · polish **← ship line** |
| **4. Intelligence** | M14–M17 | Smart homepage · adaptive intervals · Smart+ · translation · audio/TTS |
| **5. Platform & extras** | M18–M28 | Admin console · sharing · public feeds · newsletters · webhooks · revisions UI · scraping · AI discovery · WebSub · screensaver · **page proxy · headless renderer · frame stream** |

---

## 6. Data model

### 6.1 Tenancy: shared schema, and the rule that keeps it safe

One database, one schema, `tenant_id` on every tenant-owned row — chosen over database-per-tenant
because **cross-tenant feed sharing is nearly impossible with separate databases**.

**One missing `WHERE tenant_id = ?` is a cross-tenant data leak.** Discipline doesn't prevent that;
structure does. **All SQL lives in `internal/store`** · **every repository method takes
`Scope{TenantID, UserID, Caps}` as its first argument** — it's in the signature, so you can't omit it
· **a test enumerates every exported method** against a two-tenant fixture · **a build-time check**
fails if `db.Query`/`db.Exec` appear outside `internal/store` · **service-layer ownership assertions on
top**.

**Every FK-shaped column declares `REFERENCES`.** Rev 7 was inconsistent — `tags`, `rules`,
`rule_hits`, `item_tags.user_id`, and `user_item_state.tenant_id` were bare integers, which is exactly
the "discipline instead of structure" failure this section warns against. Where a `tenant_id` is
denormalized for query speed, it carries a `CHECK`/trigger tying it to `users.tenant_id` rather than
being left silent.

### 6.2 The source/subscription split (A14)

```sql
CREATE TABLE sources (                     -- GLOBAL. One row per feed URL. Polled once.
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL DEFAULT 'syndicated',        -- syndicated | scraped | mailbox
  natural_key TEXT NOT NULL UNIQUE,               -- §6.4: URL, or user-scoped for mailbox
  url TEXT, site_url TEXT, title TEXT NOT NULL, description TEXT, icon_url TEXT,
  owner_user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,  -- NULL unless kind='mailbox'
  etag TEXT, last_modified TEXT, content_hash TEXT,
  redirected_from TEXT, redirect_count INTEGER NOT NULL DEFAULT 0,   -- §15.4 (301 handling)
  websub_hub TEXT, websub_topic TEXT, websub_expires_at INTEGER,
  effective_interval_s INTEGER NOT NULL, observed_cadence_s INTEGER,
  median_items_per_poll REAL,                     -- §15.5 flood guard baseline
  next_fetch_at INTEGER NOT NULL DEFAULT 0,
  last_fetched_at INTEGER, last_success_at INTEGER, last_item_at INTEGER,
  consecutive_failures INTEGER NOT NULL DEFAULT 0, last_error TEXT, retry_after_until INTEGER,
  lifecycle TEXT NOT NULL DEFAULT 'ok',           -- ok | failing | dormant | gone       §10.6
  archive_policy TEXT NOT NULL DEFAULT 'auto',    -- auto | always | never               §10.6
  last_body BLOB, last_body_at INTEGER,           -- gzipped raw feed, for re-parse       §10.6
  deactivated_at INTEGER, deactivated_reason TEXT,          -- A22: soft delete, never DELETE
  created_at INTEGER NOT NULL
);

CREATE TABLE subscriptions (               -- PER USER. Preferences, not content.
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id   INTEGER NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
  source_id INTEGER NOT NULL REFERENCES sources(id),        -- NO CASCADE (A22)
  folder_id INTEGER REFERENCES folders(id) ON DELETE SET NULL,
  custom_title TEXT,
  interval_pref_s INTEGER, interval_mode TEXT NOT NULL DEFAULT 'auto',
  render_mode TEXT NOT NULL DEFAULT 'auto',
  home_weight REAL NOT NULL DEFAULT 1.0,
  home_mode TEXT NOT NULL DEFAULT 'full',         -- full | highlights | muted        §18.5
  highlights_per_week REAL,                       -- target rate; cutoff is solved for §18.5
  highlights_cutoff REAL,                         -- re-fitted as volume drifts
  trial_until INTEGER, trial_verdict TEXT,        -- recommendation trials            §18.7
  hide_from_counts INTEGER NOT NULL DEFAULT 0,
  notify_mode TEXT NOT NULL DEFAULT 'none',
  offline_depth TEXT NOT NULL DEFAULT 'inherit', offline_count INTEGER, offline_days INTEGER,
  visibility TEXT NOT NULL DEFAULT 'private',
  created_at INTEGER NOT NULL,
  UNIQUE(user_id, source_id)
);

CREATE TABLE items (                       -- GLOBAL. One copy per article.
  id INTEGER PRIMARY KEY,
  source_id INTEGER NOT NULL REFERENCES sources(id),        -- NO CASCADE (A22)
  guid TEXT NOT NULL, url TEXT, title TEXT NOT NULL, author TEXT, summary TEXT,
  content_html TEXT, extracted_html TEXT, extracted_at INTEGER,
  archived_text TEXT,                                       -- §10.6 near-universal; feeds FTS
  archive_reason TEXT,                                      -- ingest | interaction | distress
  origin_dead INTEGER NOT NULL DEFAULT 0, origin_checked_at INTEGER,   -- §10.6 link rot
  enclosure_url TEXT, enclosure_type TEXT, enclosure_bytes INTEGER, enclosure_duration_s INTEGER,
  lang TEXT, dupe_key TEXT,                                 -- §15.3 cross-source dedup
  published_at INTEGER NOT NULL, updated_at INTEGER, first_seen_at INTEGER NOT NULL,
  content_hash TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1,
  word_count INTEGER, deactivated_at INTEGER,
  UNIQUE(source_id, guid)
);
CREATE INDEX items_source_pub ON items(source_id, published_at DESC, id DESC);   -- §6.5
CREATE INDEX items_dupe       ON items(dupe_key) WHERE dupe_key IS NOT NULL;

CREATE TABLE item_revisions (
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL, title TEXT, content_html TEXT, content_hash TEXT NOT NULL,
  seen_at INTEGER NOT NULL, PRIMARY KEY (item_id, revision)
);

CREATE TABLE user_item_state (             -- PER USER. The hottest join in the app.
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  source_id INTEGER NOT NULL REFERENCES sources(id),  -- denormalized for §6.5 index
  published_at INTEGER NOT NULL,                      -- denormalized for §6.5 index
  is_read INTEGER NOT NULL DEFAULT 0, read_at INTEGER,
  is_favorite INTEGER NOT NULL DEFAULT 0, favorited_at INTEGER,
  is_muted INTEGER NOT NULL DEFAULT 0, muted_by_rule_id INTEGER REFERENCES rules(id) ON DELETE SET NULL,
  updated_at INTEGER NOT NULL, rev INTEGER NOT NULL DEFAULT 0,        -- A25
  PRIMARY KEY (user_id, item_id)
);
```

### 6.3 A22: global rows are never hard-deleted

Rev 7 had `items.source_id REFERENCES sources(id) ON DELETE CASCADE`, with `item_tags`,
`item_revisions`, `shared_items`, and `user_item_state` cascading in turn. Because sources and items
are **global**, deleting one source row would have destroyed **every other tenant's** favorites, tags,
notes, and public shares for that feed — triggered by one tenant's cleanup, and in flat contradiction
of §6.7's retention promise.

**The rule:** `sources` and `items` are **soft-deactivated** (`deactivated_at`), never `DELETE`d. An
"unsubscribe" removes the caller's `subscriptions` row and nothing else. A source with zero remaining
subscribers stops being polled and is marked `deactivated_at`; its items and everyone's state survive.
Only **per-user** rows cascade, and only from that user's own deletion (§7.9).

### 6.4 Mailbox sources are per-user keyed

`sources.natural_key` is the URL for `kind IN ('syndicated','scraped')`. For `kind='mailbox'` it is
**`mailbox:<mailbox_id>:<sender>`**, and `owner_user_id` is set.

This is not a detail. A14's "one row per feed, polled once" rationale exists because public RSS URLs
return the same bytes to everyone. **That is false for email.** Keying a newsletter source by sender
address alone would merge two people's private mail into one global `items` row, exposing it through
shared items, tags, ranking, and potentially the public share feed (§7.8). Mailbox sources are
deduplicated per user and never shared, and the polling-efficiency argument simply does not apply.

### 6.5 Indexing the hottest path

The hottest query is *"this user's unread items, newest first, optionally scoped to a folder."*
Rev 7's `uis_unread(user_id, is_read, is_muted, item_id)` served unread **counts** but not sorted,
paginated lists — because `published_at` lived on `items`, so every listing fell through to a
join-then-sort.

Fix: **denormalize `source_id` and `published_at` onto `user_item_state`** (they're immutable for an
item, so there's no update anomaly), and index for the real shapes:

```sql
CREATE INDEX uis_unread_recent ON user_item_state(user_id, is_read, is_muted, published_at DESC, item_id DESC);
CREATE INDEX uis_source_recent ON user_item_state(user_id, source_id, is_read, published_at DESC, item_id DESC);
CREATE INDEX uis_favorite      ON user_item_state(user_id, is_favorite, favorited_at DESC) WHERE is_favorite = 1;
```

Folder scoping resolves `folder_id → source_id[]` from `subscriptions` first, then uses
`uis_source_recent`. **Benchmark all three shapes at M4** on 50k items × 3 users — flat unread count,
unread-by-newest, and unread-by-folder. Rev 7 only committed to the first, which was the one that
already worked.

---

**G3 · MEASURED 2026-07-26 at 50,000 items × 3 users × 150 feeds** (`internal/store/hotquery_test.go`,
`HOTQUERY=1`). The denormalisation above was **never actually built** — `user_item_state` has neither
`source_id` nor `published_at`, and TODO 5.3 recorded it as done. So the numbers below are for the
schema as it exists, and the surprise is that the denormalisation turned out **not to be needed**.

| Shape | Before | After | |
|---|---|---|---|
| unread by newest | **478 ms** | **0.5 ms** | ✅ |
| unread by folder | 178 ms | **0.5 ms** | ✅ |
| keyset page 40 | 408 ms | **0.3 ms** | ✅ |
| flat unread count | 447 ms | 556 ms | ❌ still over |
| sidebar with per-feed counts | 512 ms | 447 ms | ❌ still over |

**What was actually wrong: the query planner, not the schema.** `EXPLAIN QUERY PLAN` showed SQLite
driving from `subscriptions`, joining all ~16,700 unread rows to `items`, and then sorting every one
of them in a `USE TEMP B-TREE FOR ORDER BY` to take the first 50. Pinning `items_published` — an
index that has existed since `0001_init` — makes it walk items newest-first and stop after 51. **No
migration, no denormalisation, three orders of magnitude.** It is not an artefact of the benchmark's
`ANALYZE`: the planner picks the same plan with the statistics dropped.

**And a second bug the fix exposed.** With the index pinned, page 2 on the real 3,800-item
development database went from 13 ms to **1.3 seconds**. The keyset cursor was
`published_at < ? OR (published_at = ? AND id < ?)`, and SQLite cannot turn an `OR` into an index
range — so it scanned the whole index evaluating the predicate per row. Rewritten as the row-value
comparison `(published_at, id) < (?, ?)`, which is seekable, it became **0.5 ms**. Both pages on the
real database improved 20× over where they started. *Worth keeping: page 1 got faster while page 2
collapsed, which is exactly the regression a page-1 benchmark never sees.*

**Why the dev database said everything was fine.** It has 3,800 items, where materialise-and-sort
costs 13 ms. This is R2's curve, and it is why §6.5 asked for a number at 50k rather than a number at
whatever size the box happened to have.

**What remains, and what it costs.** Both failures are *counting* — they must visit every unread row
and cannot stop at 50, so the index hint does nothing for them. R2 names the fix, "a materialized
per-user unread index", and says to measure first. **This is the measurement, and the answer is
build it**: the sidebar renders per-feed unread counts on every screen, and half a second there is
not something the client can work around. Tracked as **TODO 5.4a**, with the current numbers recorded
in `knownSlow` as a ratchet — a regression past them fails, and the entry has to be deleted when the
counter lands.

---

**5.4a · MEASURED 2026-07-26, same fixture.** The counter landed and `knownSlow` is now **empty**.

| Shape | Before | After |
|---|---|---|
| flat unread count | 556 ms | **3.4 ms** |
| sidebar with per-feed counts | 447 ms | **3.8 ms** |

**§6.5's prescription was half right, and this is the half that was right.** No counter table and no
maintained integer were needed — the denormalisation this section asked for was enough, once it
actually existed (`0015_uis_source.sql`). `source_id` on `user_item_state` plus a **partial** index
`(user_id, source_id) WHERE read_at IS NULL` turns "unread in this source" into one index range that
never touches `items`. Partial matters twice over: it is only as large as the unread backlog, and it
*shrinks as someone catches up*, which is the opposite of how the old count behaved.

The badge is computed as the **sum of the sidebar's per-feed counts**, from the same expression, so
the total and the numbers beside each feed cannot disagree. Two numbers on one screen computed two
different ways is how they stop agreeing.

**The index had to be pinned, again.** With `ANALYZE`'s statistics the planner preferred the older
`uis_user_unread` via an `ANY(user_id)` skip-scan — 50,000 rows per feed, 150 feeds — and ran the
sidebar in **2.5 s, worse than before the migration**. `INDEXED BY uis_unread_by_source` fixed it.
That is now twice in this section that a correct index lost to the planner. The pattern worth
carrying: **an index added after `ANALYZE` has statistics for an older one is not chosen by being
better; it has to be named.**

**The real defect was upstream and the counter only made it visible.** Counting from state rows is
correct only if every visible item has one, and 80 of 3,806 items in the development database had
none. Fan-out was creating them — but fan-out is a queued job that applies *rules*: it runs on a
worker after ingest, can be delayed or retried, and does nothing for a reader with no rules.
Delivery is not a rule outcome, it is what ingest means, so `deliver()` now runs inside
`IngestItems`' transaction and `Subscribe` backfills the items a global source already holds. **A
denormalisation is a good way to discover that the thing you were denormalising was already wrong.**

**Guarding a count that fails silently.** Nothing throws when this drifts; the badge is just quietly
low, forever, and nobody reports it. So: a trigger fills both columns for any writer that forgets
(the hot paths set them explicitly and the `WHEN` clause skips them, so it is free); two more move a
deactivated item out of every count by nulling `source_id`, which keeps the reader's star and rating
where deleting the row would not; `ReconcileUnread` returns the drift it repaired rather than
swallowing it; and `TestUnreadCountNeverDrifts` runs 120 randomised reads, unreads, stars and
mark-all-reads, comparing against a recomputation after **every single one** — then asserts the
reconciler finds nothing left to fix, because a write path quietly leaning on its own safety net has
no safety net.

### 6.6 Item tags (A21) — the prerequisite

```sql
CREATE TABLE tags (
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL, color TEXT,
  UNIQUE(user_id, name COLLATE NOCASE)
);
CREATE TABLE item_tags (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  tag_id  INTEGER NOT NULL REFERENCES tags(id)  ON DELETE CASCADE,
  applied_by_rule_id TEXT,                 -- NULL = a person; a rule id = which rule
  at INTEGER NOT NULL, PRIMARY KEY (user_id, item_id, tag_id)
);
CREATE INDEX item_tags_tag ON item_tags(user_id, tag_id, item_id);
```

**Tags are orthogonal to folders**: a feed lives in one folder; an item carries any number of tags.
Google Reader worked this way, the GReader API models read/starred/broadcast as *system tags* over the
same mechanism, and the rules engine's `tag` action writes here. One table, three consumers.

**As built — both levels now exist.** `0004_tags.sql` carries `tags` + `subscription_tags`, per-user,
created on first use and removed with the last association; the feed-level one arrived first because
labelling a feed is what a reader wanted at 151 subscriptions. `item_tags` then shipped in
`0010_content.sql`, which is what A21 is actually for and what both rules (A19) and the sync API (A18)
need.

*Corrected 2026-07-27: this section previously sketched an `item_tags.source` enum
(`manual | rule | sync`) and said the table was still owed. Neither was true — the shipped column is
`applied_by_rule_id` (NULL means a person applied it, a rule id says which rule), with a partial index
over the non-NULL case. The plan was wrong, so the plan changed.*

**That column cannot answer "did a rule apply this?" after the fact, and a fan-out fix had to route
around it.** `applied_by_rule_id` lives on the `item_tags` row, and that row is exactly what
`UntagItem` deletes when a reader removes a tag — so at the precise moment you need to know whether a
rule or a person put it there, the answer has already been deleted with it. That is why the
at-least-once redelivery guard (§13.2) reads `rule_hits` instead: it is append-only, `UntagItem` never
touches it, and it therefore survives the event it is being asked about. Worth remembering before
anyone adds a column intending it to carry provenance — provenance stored *on* the thing dies *with*
the thing.

**Identity and presentation are two columns (A38).** `0008_tag_style.sql` adds:

```sql
ALTER TABLE tags ADD COLUMN label TEXT NOT NULL DEFAULT '';  -- the rail row's name; '' = use name
ALTER TABLE tags ADD COLUMN glyph TEXT NOT NULL DEFAULT '';  -- one char from internal/tagglyph; '' = section default
```

A tag is a *handle* — short, typeable, the string on a chip and the argument to `SetFeedTag` — and it
is a *destination*, a row in the rail that wants to read like the rows around it. One string cannot be
good at both, and whichever the reader optimises for, the tag is worse at the other. So `name` is never
edited (`UpdateTagRequest` has no `name` field, and its absence is the point of the message) and
`label` is the override, empty meaning "use `name`" so there is never a second copy to drift.

The **glyph is stored as the character**, not as an index into the catalogue. An index is a promise
never to reorder or remove an entry; break it once and every reader's tags silently become different
symbols with nothing in the data to show it happened. `internal/tagglyph` holds the fixed set — fifty
marks in seven groups, text-presentation only so they inherit the row's colour and weight, and
**server-validated**, because the sidebar is not a place to accept arbitrary client text. It lives in
`internal/` for the same reason `internal/signals` does: the picker and the validator must be reading
one list, or the panel can offer something the save will refuse.

Neither column is indexed. Both are read only as part of the tag row, there is no "find the tag whose
glyph is ⚑" query, and the per-user cap is 200.

### 6.7 Identity, sharing, rules, and the rest

```sql
CREATE TABLE tenants (id INTEGER PRIMARY KEY, slug TEXT NOT NULL UNIQUE, name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active', quota_json TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL, deleted_at INTEGER);
CREATE TABLE users (id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  username TEXT NOT NULL, display_name TEXT, recovery_email TEXT, recovery_email_verified_at INTEGER,
  password_hash BLOB, is_superadmin INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'active', timezone TEXT NOT NULL DEFAULT 'UTC',   -- §22.9
  created_at INTEGER NOT NULL, last_seen_at INTEGER, deleted_at INTEGER,
  UNIQUE(tenant_id, username));

CREATE TABLE shares (                      -- restored; rev 7 dropped this DDL entirely
  id INTEGER PRIMARY KEY,
  object_kind TEXT NOT NULL,               -- 'folder' | 'view' | 'bookmark_folder'
  object_id INTEGER NOT NULL,
  owner_tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  owner_user_id   INTEGER NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
  grantee_tenant_id INTEGER REFERENCES tenants(id) ON DELETE CASCADE,
  grantee_user_id   INTEGER REFERENCES users(id)   ON DELETE CASCADE,
  perm TEXT NOT NULL,                      -- 'read' | 'contribute'   (§7.8 enumerates contribute)
  created_by INTEGER NOT NULL REFERENCES users(id),
  created_at INTEGER NOT NULL, expires_at INTEGER, revoked_at INTEGER,
  CHECK (grantee_tenant_id IS NOT NULL OR grantee_user_id IS NOT NULL)
);
CREATE INDEX shares_grantee ON shares(grantee_tenant_id, grantee_user_id) WHERE revoked_at IS NULL;

CREATE TABLE api_tokens (
  id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash BLOB NOT NULL UNIQUE, label TEXT NOT NULL,
  scope TEXT NOT NULL,                     -- ENUM: 'reader_ro' | 'reader_rw'   (§15.2)
  created_at INTEGER NOT NULL, last_used_at INTEGER, expires_at INTEGER, revoked_at INTEGER);

CREATE TABLE rules (
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, position INTEGER NOT NULL DEFAULT 0,
  match_json TEXT NOT NULL, actions_json TEXT NOT NULL,
  stop_processing INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  last_matched_at INTEGER, match_count INTEGER NOT NULL DEFAULT 0);
CREATE TABLE rule_hits (id INTEGER PRIMARY KEY,
  rule_id INTEGER NOT NULL REFERENCES rules(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  actions_json TEXT NOT NULL, at INTEGER NOT NULL);
```

**Interest layer (§18)** — every table below is *derived* and safe to `DELETE` and rebuild from
`engagements`, which is why the raw log is the thing that must never be lost:

```sql
CREATE TABLE topics (                             -- §18.2 — clusters, not one vector
  id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  label TEXT NOT NULL, label_source TEXT NOT NULL DEFAULT 'terms',   -- terms | llm | user
  centroid BLOB, dims INTEGER, top_terms_json TEXT NOT NULL,
  member_count INTEGER NOT NULL DEFAULT 0,
  trend TEXT NOT NULL DEFAULT 'steady',           -- rising | steady | fading | dormant
  suppressed INTEGER NOT NULL DEFAULT 0,          -- "not an interest" = negative on the cluster
  last_engaged_at INTEGER, updated_at INTEGER NOT NULL);
CREATE TABLE item_topics (user_id INTEGER NOT NULL, item_id INTEGER NOT NULL,
  topic_id INTEGER NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
  score REAL NOT NULL, PRIMARY KEY (user_id, item_id, topic_id));

CREATE TABLE domain_affinity (                    -- §18.6 — target domains, not just sources
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, domain TEXT NOT NULL,
  impressions INTEGER NOT NULL DEFAULT 0, opens INTEGER NOT NULL DEFAULT 0,
  stars INTEGER NOT NULL DEFAULT 0, notes INTEGER NOT NULL DEFAULT 0,
  median_dwell_ms REAL, last_at INTEGER, PRIMARY KEY (user_id, domain));

CREATE TABLE outlinks (                           -- §18.7 rung 1 — links inside what you read
  id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  source_id INTEGER NOT NULL, target_domain TEXT NOT NULL, target_url TEXT NOT NULL,
  found_at INTEGER NOT NULL);
CREATE INDEX outlinks_domain ON outlinks(target_domain);

CREATE TABLE recommendations (                    -- §18.7 candidates
  id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  domain TEXT NOT NULL, feed_url TEXT, title TEXT,
  score REAL NOT NULL, rung INTEGER NOT NULL,     -- which signal surfaced it (1–5)
  evidence_json TEXT NOT NULL,                    -- the "why", shown verbatim in the UI
  cadence_per_week REAL, last_post_at INTEGER,    -- health gate: not dead, not a firehose
  status TEXT NOT NULL DEFAULT 'new',             -- new | dismissed | trialing | subscribed
  first_seen_at INTEGER NOT NULL, last_scored_at INTEGER, dismissed_at INTEGER,
  UNIQUE(user_id, domain));
```

### 6.8 The rest of the schema

Earlier revisions listed these as names in a paragraph while M1 said "the full §6 schema." That is
two thirds of the tables unspecified. Written out, grouped, with `REFERENCES` on every FK-shaped
column per §6.1's own rule.

```sql
-- ═══ organisation ═════════════════════════════════════════════════════════
CREATE TABLE folders (
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id   INTEGER NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
  kind TEXT NOT NULL,                               -- 'feed' | 'bookmark'
  parent_id INTEGER REFERENCES folders(id) ON DELETE CASCADE,
  depth INTEGER NOT NULL DEFAULT 0,                 -- maintained on write; CHECK depth < 8
  name TEXT NOT NULL, position INTEGER NOT NULL DEFAULT 0,
  visibility TEXT NOT NULL DEFAULT 'private',       -- private | tenant | shared
  created_at INTEGER NOT NULL,
  CHECK (depth < 8), CHECK (parent_id IS NULL OR parent_id <> id)
);
CREATE INDEX folders_user ON folders(user_id, kind, parent_id, position);
```

> **A folder cannot be its own ancestor.** The `CHECK`s stop the trivial cases; the **cycle check on
> reparent is application-side** — walk the ancestor chain before writing, reject if the new parent is
> a descendant. Netscape bookmark import is the path most likely to produce one, so it re-validates.

```sql
-- ═══ user content — the irreplaceable rows (R8) ═══════════════════════════
CREATE TABLE notes (
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  body_md TEXT NOT NULL,
  rev INTEGER NOT NULL DEFAULT 0,                   -- A25 ordering authority
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, item_id)
);

CREATE TABLE bookmarks (
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id   INTEGER NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
  url TEXT NOT NULL, url_norm TEXT NOT NULL,
  title TEXT NOT NULL, description TEXT, notes_md TEXT, favicon_url TEXT,
  folder_id INTEGER REFERENCES folders(id) ON DELETE SET NULL,
  archived_html BLOB, archived_text TEXT, archived_at INTEGER,   -- BLOB = zstd (R7)
  source_item_id INTEGER REFERENCES items(id) ON DELETE SET NULL,
  is_unread INTEGER NOT NULL DEFAULT 1, is_favorite INTEGER NOT NULL DEFAULT 0,
  last_checked_at INTEGER, http_status INTEGER, is_dead INTEGER NOT NULL DEFAULT 0,
  rev INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  UNIQUE(user_id, url_norm)                         -- per user, not global
);
CREATE INDEX bookmarks_created ON bookmarks(user_id, created_at DESC);
CREATE INDEX bookmarks_unread  ON bookmarks(user_id, is_unread, created_at DESC) WHERE is_unread = 1;
CREATE INDEX bookmarks_dead    ON bookmarks(user_id, is_dead) WHERE is_dead = 1;

CREATE TABLE bookmark_tags (
  bookmark_id INTEGER NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
  tag_id      INTEGER NOT NULL REFERENCES tags(id)      ON DELETE CASCADE,
  PRIMARY KEY (bookmark_id, tag_id)
);

CREATE TABLE views (                                -- saved ViewSpec (§20.2)
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id   INTEGER NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
  name TEXT NOT NULL, kind TEXT NOT NULL,           -- 'item' | 'bookmark'
  position INTEGER NOT NULL DEFAULT 0,
  spec_json TEXT NOT NULL, created_at INTEGER NOT NULL
);

-- ═══ alternate sources ════════════════════════════════════════════════════
CREATE TABLE scrape_rules (
  source_id INTEGER PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE,
  index_url TEXT NOT NULL, url_template TEXT,
  item_selector TEXT NOT NULL, title_selector TEXT NOT NULL, link_selector TEXT NOT NULL,
  date_selector TEXT, date_layout TEXT,
  summary_selector TEXT, image_selector TEXT, author_selector TEXT,
  respect_robots INTEGER NOT NULL DEFAULT 1,
  last_ok_at INTEGER, empty_polls INTEGER NOT NULL DEFAULT 0,   -- RULE_BROKEN trigger
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);

CREATE TABLE mailboxes (
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id   INTEGER NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
  host TEXT NOT NULL, port INTEGER NOT NULL, username TEXT NOT NULL,
  secret_enc BLOB NOT NULL,                         -- §6.9: encrypted, never logged
  folder TEXT NOT NULL DEFAULT 'INBOX',
  use_plus_addressing INTEGER NOT NULL DEFAULT 0,
  last_uid INTEGER, last_ok_at INTEGER, last_error TEXT,
  created_at INTEGER NOT NULL
);

-- ═══ signals and derived interest (§18) ═══════════════════════════════════
-- SHIPPED as migrations/0007_engagements.sql, 2026-07-26. TEXT ids and RFC3339
-- elsewhere; INTEGER ms here, and the differences below are all deliberate.
CREATE TABLE engagements (                          -- APPEND ONLY. Everything else rebuilds from this.
  id TEXT PRIMARY KEY,                              -- CLIENT-generated; this IS the dedupe (A33)
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id TEXT,                                     -- NULL for term/source signals (a search query)
  source_id TEXT,                                   -- denormalised; every rollup groups by it
  kind TEXT NOT NULL,                               -- the closed set lives in internal/signals
  value REAL,                                       -- per kind: ms · 0..1 ratio · count
  surface TEXT NOT NULL,                            -- home | list | reader | search | screensaver | sync_api
  context TEXT,                                     -- small JSON: {"pos":7,"of":12} {"q":"fts5"}; ≤1 KiB
  session_id TEXT,                                  -- client-assigned; the server sees one tunnel
  at INTEGER NOT NULL,                              -- client-observed unix MS, clamped (§18.1a)
  recorded_at INTEGER NOT NULL                      -- server MS, so skew stays measurable
);
CREATE INDEX engagements_user_at        ON engagements(user_id, at DESC);
CREATE INDEX engagements_user_item      ON engagements(user_id, item_id) WHERE item_id IS NOT NULL;
CREATE INDEX engagements_user_source_at ON engagements(user_id, source_id, at DESC) WHERE source_id IS NOT NULL;
-- Partial, listing only the kinds that are sparse: impressions outnumber
-- everything else by two orders of magnitude, so an index including them would
-- be the table again.
CREATE INDEX engagements_user_kind_at   ON engagements(user_id, kind, at DESC)
    WHERE kind NOT IN ('impression','dwell','scroll_depth');
```

> **`bulk_read` and `impression` are why this table exists in this shape.** Storing a derived
> "open rate" only would make R17 unfixable — you could never re-derive it once you learned that bulk
> reads must not count. **Log the event, derive the score.**

> **Four deviations from the DDL rev 8 specified, each load-bearing.** `id` is a client-generated TEXT
> so a retried batch collides on the primary key instead of double-counting — double-counted dwell is
> the difference between "read carefully" and "left the tab open". `item_id` has **no `REFERENCES`**:
> a signal stays true about an item that a restore or a pack no longer holds, and a foreign key would
> let a repair delete the evidence. `at` is INTEGER **milliseconds** because every derivation over
> this table is arithmetic on time, and parsing RFC3339 per row across millions of rows is a cost paid
> forever for a cosmetic consistency. `kind` has **no CHECK constraint**: a new signal must not require
> a migration on a live database, so the closed set is enforced in `internal/signals` at the service
> boundary, where an unknown kind can be reported and counted rather than aborting a write.

```sql
CREATE TABLE feed_affinity (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
  impressions INTEGER NOT NULL DEFAULT 0, opens INTEGER NOT NULL DEFAULT 0,
  favorites INTEGER NOT NULL DEFAULT 0, notes INTEGER NOT NULL DEFAULT 0,
  bookmarks INTEGER NOT NULL DEFAULT 0, bounces INTEGER NOT NULL DEFAULT 0,
  median_dwell_ms REAL, completion_rate REAL,
  volume_per_day REAL, half_life_hours REAL,        -- per-source decay (§18.4)
  score REAL NOT NULL DEFAULT 0, last_engaged_at INTEGER, updated_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, source_id)
);
CREATE TABLE term_affinity (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  term TEXT NOT NULL, weight REAL NOT NULL, doc_freq INTEGER,
  updated_at INTEGER NOT NULL, PRIMARY KEY (user_id, term)
);
CREATE TABLE item_embeddings (                      -- Smart+ only; rolling 30-day window
  item_id INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
  model TEXT NOT NULL, dims INTEGER NOT NULL, vec BLOB NOT NULL, created_at INTEGER NOT NULL
);
CREATE TABLE home_ranking (                         -- materialised; the homepage is an indexed read
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  score REAL NOT NULL, rank INTEGER NOT NULL,
  slot TEXT NOT NULL,                               -- top | explore | cluster_head
  cluster_id INTEGER, topic_id INTEGER REFERENCES topics(id) ON DELETE SET NULL,
  reasons_json TEXT NOT NULL,                       -- shown verbatim (§18.9)
  tier TEXT NOT NULL,                               -- smart | smart_plus
  computed_at INTEGER NOT NULL, PRIMARY KEY (user_id, item_id)
);
CREATE INDEX home_ranking_rank ON home_ranking(user_id, rank);

-- ═══ derived article artefacts ════════════════════════════════════════════
CREATE TABLE item_translations (
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  lang TEXT NOT NULL, title TEXT, body_html TEXT, model TEXT,
  created_at INTEGER NOT NULL, PRIMARY KEY (item_id, lang)
);
CREATE TABLE item_audio (
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  voice TEXT NOT NULL, backend TEXT NOT NULL,       -- browser | npu
  path TEXT NOT NULL, bytes INTEGER NOT NULL, duration_s INTEGER,
  created_at INTEGER NOT NULL, last_played_at INTEGER,
  PRIMARY KEY (item_id, voice)
);
```

> **`item_audio.bytes` exists so R7 is enforceable.** Generated audio is tens of MB per article and is
> the first thing shed under the §22.6 disk ladder — which needs a number to shed by.

```sql
-- ═══ notifications and outbound (§17) ═════════════════════════════════════
CREATE TABLE push_subscriptions (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id TEXT REFERENCES devices(id) ON DELETE CASCADE,
  endpoint TEXT NOT NULL UNIQUE, p256dh TEXT NOT NULL, auth TEXT NOT NULL,
  created_at INTEGER NOT NULL, last_ok_at INTEGER, failures INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE notification_log (                     -- dedup + "why did I get this?"
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id INTEGER REFERENCES items(id) ON DELETE CASCADE,
  reason TEXT NOT NULL,                             -- rule:<id> | source_new | digest
  sent_at INTEGER NOT NULL, window_key TEXT NOT NULL
);
CREATE INDEX notif_window ON notification_log(user_id, window_key);

CREATE TABLE webhooks (
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id   INTEGER NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
  name TEXT NOT NULL, url TEXT NOT NULL, secret_enc BLOB NOT NULL,
  events_json TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  last_ok_at INTEGER, last_error TEXT, created_at INTEGER NOT NULL
);

-- ═══ offline (§12) ════════════════════════════════════════════════════════
CREATE TABLE offline_packs (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  spec_json TEXT NOT NULL,                          -- the ViewSpec + depth it was built from
  depth TEXT NOT NULL, item_count INTEGER NOT NULL, bytes INTEGER NOT NULL,
  status TEXT NOT NULL,                             -- building | ready | expired | failed
  token_hash BLOB NOT NULL,                         -- /pack/:id is signed, short-TTL (§21)
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL
);
CREATE TABLE pack_items (
  pack_id INTEGER NOT NULL REFERENCES offline_packs(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  PRIMARY KEY (pack_id, item_id)
);
CREATE TABLE outbox_conflicts (                     -- notes only; never auto-resolved (§12.4)
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  local_body_md TEXT NOT NULL, server_body_md TEXT NOT NULL,
  local_at INTEGER NOT NULL, server_rev INTEGER NOT NULL,
  detected_at INTEGER NOT NULL, resolved_at INTEGER, resolution TEXT
);

-- ═══ identity, auth, audit ════════════════════════════════════════════════
CREATE TABLE roles (
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER REFERENCES tenants(id) ON DELETE CASCADE,   -- NULL = system-seeded
  name TEXT NOT NULL, caps_json TEXT NOT NULL,
  UNIQUE(tenant_id, name)
);
CREATE TABLE user_roles (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role_id INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  PRIMARY KEY (user_id, role_id)
);
CREATE TABLE invites (
  code_hash BLOB PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  role_id INTEGER REFERENCES roles(id) ON DELETE SET NULL,
  created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
  redeemed_by INTEGER REFERENCES users(id) ON DELETE SET NULL, redeemed_at INTEGER
);
CREATE TABLE devices (
  id TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  label TEXT, family_id TEXT NOT NULL,
  refresh_hash BLOB NOT NULL, client_version TEXT,  -- §22.10 skew check
  created_at INTEGER NOT NULL, last_seen_at INTEGER, expires_at INTEGER,
  revoked_at INTEGER, revoked_reason TEXT           -- 'reuse_detected' revokes the family
);
CREATE INDEX devices_family ON devices(family_id);
CREATE TABLE recovery_codes (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash BLOB NOT NULL, created_at INTEGER NOT NULL, used_at INTEGER
);
CREATE TABLE reset_tokens (
  token_hash BLOB PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  issued_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
  origin TEXT NOT NULL,                             -- admin | email | cli
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, used_at INTEGER
);
CREATE TABLE login_attempts (
  id INTEGER PRIMARY KEY, at INTEGER NOT NULL,
  username TEXT, tenant_id INTEGER, ip TEXT,
  outcome TEXT NOT NULL                             -- ok | bad_password | unknown_user | locked
);
CREATE INDEX login_user ON login_attempts(username, at DESC);
CREATE INDEX login_ip   ON login_attempts(ip, at DESC);

CREATE TABLE audit_log (                            -- actor may be tombstoned, never deleted (§7.9)
  id INTEGER PRIMARY KEY, at INTEGER NOT NULL,
  actor_user_id INTEGER, acting_as_user_id INTEGER, tenant_id INTEGER,
  action TEXT NOT NULL, object_kind TEXT, object_id INTEGER, detail_json TEXT
);
CREATE INDEX audit_at ON audit_log(tenant_id, at DESC);

-- ═══ platform ═════════════════════════════════════════════════════════════
CREATE TABLE settings (
  scope TEXT NOT NULL,                              -- system | tenant | user
  scope_id INTEGER, key TEXT NOT NULL, value_json TEXT NOT NULL,
  updated_at INTEGER NOT NULL, updated_by INTEGER,
  PRIMARY KEY (scope, scope_id, key)
);
CREATE TABLE jobs (                                 -- §22.7; durable, restart-survivable
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL,                               -- fanout | rank | extract | pack | audio
                                                    -- | preserve | recommend | linkcheck | embed
  tenant_id INTEGER, payload_json TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'queued',             -- queued | running | done | failed | dead
  priority INTEGER NOT NULL DEFAULT 0,
  attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT,
  run_after INTEGER NOT NULL, locked_by TEXT, locked_at INTEGER,
  created_at INTEGER NOT NULL, finished_at INTEGER
);
CREATE INDEX jobs_ready ON jobs(state, run_after, priority DESC) WHERE state = 'queued';

CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL
);
CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL);
```

> **`jobs` was never in the schema at all** despite §22.7 depending on it. `locked_by` + `locked_at`
> are what make a crashed worker's jobs reclaimable rather than stuck; without them the queue is
> durable but not restart-*survivable*, which is a different and weaker property.

### 6.9 Secrets at rest

Four kinds of secret, three different treatments — worth stating because "encrypted" is doing
different work in each case:

| Secret | Treatment | Why |
|---|---|---|
| Passwords, recovery codes, invite codes, reset tokens, API tokens, pack tokens | **Hashed, never recoverable** (Argon2id or SHA-256 for high-entropy tokens) | We only ever need to *verify* |
| Mailbox passwords, webhook secrets | **Symmetrically encrypted** with a key from config | We must *present* them to a third party |
| LLM API keys, VAPID keys, the DB encryption key | **Config or environment, never the database** | Bootstrapping — the DB can't hold the key that opens it |
| Feed URLs, item titles, note bodies | **Plaintext, but never logged** (§22.11) | Personal, not secret |

`credentials(k, v)` holds only the derived, non-bootstrapping material: signing keys and the VAPID
pair. **Key rotation** re-encrypts `secret_enc` columns in one transaction and is a CLI operation,
not an API.

### 6.10 Categories — folders as built (A37)

`folders` has been in `0001_init` since the slice; nothing used it until the rail needed somewhere to
put 151 subscriptions. `internal/store/folders.go` is that use, and it is deliberately narrower than
the DDL above:

| DDL affords | As built | Why |
|---|---|---|
| `parent_id` + `depth < 8` | **Flat.** Nothing writes `parent_id` | A nesting UI is a picker that needs a path, a drop target that needs a drop zone, and a rail that can indent itself off the right edge. The column stays so the day it is wanted is a migration nobody has to write |
| `kind ∈ {feed, bookmark}` | `feed` only | Bookmarks are M9 |
| `visibility ∈ {private, tenant, shared}` | `private` only | Sharing is M20 |
| — | `MaxFolderName = 48`, `MaxFoldersPerUser = 200` | The width the rail draws before it ellipsises, and the same cap `tags` carries. A control that silently truncates is worse than one that says no |

Five RPCs: `ListFolders` · `CreateFolder` · `RenameFolder` · `DeleteFolder` · `SetFeedFolder`, plus
`folder_id` on `Subscribe` so a feed can be filed at the moment it is added (§20.18). Four rules that
each close a way this goes wrong:

- **Creating a name that exists returns the existing row**, matched case-insensitively, rather than
  erroring. It is called from the add-a-feed form, where "Tech" and "tech" are one intent and an error
  is a dead end in the middle of a task the reader is halfway through.
- **Deleting a category unfiles its feeds and unsubscribes nothing.** Deleting a shelf is not deleting
  the books. `subscriptions.folder_id` goes null in the same transaction.
- **Renaming is allowed** — unlike a tag (A38) — because nothing refers to a folder by name. The name
  is not a key, so renaming it orphans nothing.
- **No feed counts on the wire.** `ListFeeds` already carries `folder_id` on every feed, so the rail
  groups them client-side. A second source of the same number is a second thing to keep in step, and
  the one that drifts is the one nobody is looking at.

Ordering is `position, name`: position is what the reader arranged, and the name breaks the tie for
every row that has never been arranged — which on a fresh account is all of them. Ordering on position
alone leaves same-position rows in whatever order SQLite returns, stable within a query and free to
change between them, which reads as a sidebar that shuffles itself.

**Retention:** keep 500 items per source; **never prune** items that are favorited, unread, noted,
tagged, shared, engaged, or in a live offline pack — **for any user**. Bookmarks are never pruned.
A22 is what makes this promise keepable.

---

## 7. Authentication, authorization, identity

### 7.1 Authentication (A17)

Username + password, tenant-scoped, no second factor. **Argon2id** (`x/crypto/argon2`) at 64 MB /
3 iterations / parallelism 2, **tuned to the box** via a startup benchmark. **Always run the hash even
for an unknown username**, or login timing is a free enumeration oracle; every failure returns the
identical *"invalid username or password."* **Minimum 12 characters plus a breached-password check** —
a bundled bloom filter of top-N Pwned Passwords, or the HIBP k-anonymity range API (free, no key,
sends only a 5-char SHA-1 prefix). Sessions are device-scoped refresh-token families with rotation.
**Changing a password requires the current one**; a reset doesn't, but kills every session everywhere.

### 7.1a What shipped — 2026-07-26 (A36)

`proto/articleflux/v1/auth.proto` · `internal/transport/grpcsrv/auth.go` · `internal/store/identity.go`
· `cmd/articleflux/admin.go`. **This is the floor for facing the public internet, not the ceiling**, and
the gap between it and §7.1 is listed at the end rather than left to be discovered.

**Why it was built now, ahead of its milestone.** `DevMode` — which serves the single local account
with no login at all — was derived from `isLoopback(bindAddr)`. Every reverse-proxy deployment in
existence, including the nginx one this ships with, terminates TLS on :443 and forwards to
`127.0.0.1:9000`. So **the canonical way to host this was also the way to publish one's entire reading
history, notes and all, to anyone who typed the domain** — and the more careful the operator was about
binding to loopback, the more exposed they were. A bind address is a fact about network topology and
says nothing about who is on the other end of a connection; nothing that cannot answer "who is calling"
may decide whether to ask for a password. Hence A36.

**Three RPCs, and `AuthService` is the only one reachable without a credential**, because it is where
credentials come from.

| | |
|---|---|
| `Login` | Username + password → a bearer token. **The hash always runs**, against a real Argon2id decoy computed at boot when the username does not exist — a uniform error message alone does not close the timing oracle, the work has to actually happen. One error for missing / wrong / deactivated |
| `Logout` | Revokes **the calling session**, read from metadata rather than from the body — accepting an arbitrary token would let anyone revoke anyone's. Idempotent, and deliberately not an oracle for "was that a real token" |
| `WhoAmI` | Who the caller is, and **`dev_mode`**, so a client can say out loud that it is unauthenticated. Separate from `GetVersion`, which stays information-free for the readiness probe |

**Sessions.** Server-side rows; only the token's **SHA-256** is stored, so a database dump does not
hand out live sessions. 30-day TTL — this is a reader opened on a phone at breakfast and a laptop at
night, and a weekly expiry is a recurring tax on the one interaction that has nothing to do with
reading; the compensating control is that revocation is real and a password change kills every session.
`device_id` groups a browser profile so a future account screen can revoke a *device*. Expired and
revoked rows are purged by the poller, revoked ones kept a week so "which device did I sign out, and
when" stays answerable. A successful login **re-hashes** if the stored parameters are stale — the only
moment the plaintext is ever in hand — and deliberately does *not* revoke, or the first login after a
parameter bump would log the reader straight back out.

**Attempt limiting.** A fixed-window counter, 10/minute, on two keys: the username and the client
address, cleared for the username on a correct password so someone who fumbles four times and then
succeeds is not locked out by their own history. **The honest caveat:** RPCs arrive multiplexed over
one WebSocket, so the peer address is the tunnel's — behind nginx that is `127.0.0.1` for everyone, and
the per-IP key collapses to a single bucket in exactly the deployment where it matters most. The
per-username limiter carries the weight; a real per-IP limit needs the forwarded address threaded
through the tunnel handshake (TODO 7.3d).

**Multi-tenancy is where this is knowingly incomplete.** Usernames are unique *per tenant*, so
`UserForLogin` is only unambiguous while there is one tenant. Two matches return `ErrAmbiguousUser` and
a `FailedPrecondition` that says so, rather than picking one: a silent wrong-tenant login is
unrecoverable, and the error makes **D12 arrive as an outage rather than as a data leak.** Login needs
a tenant hint — a subdomain or an explicit field — before there is a second tenant.

**The operator CLI**, because a login needs something to log in as (§22.3): `init` (first tenant +
superadmin, **refuses to run twice**), `adduser`, `passwd` (break-glass, §7.2 — revokes every session),
`migrate`, `backup`. Passwords are read from a flag, then `ARTICLEFLUX_PASSWORD`, then a terminal
prompt; minimum 12 characters, **no composition rules** — length is the only property that reliably
costs an attacker anything. Roles are validated against the four the column documents, since an account
created as `"admins"` would fail closed on every check with no clue why.

**Still owed from §7.1–7.3, and none of it is optional for a public instance:** lockout (only rate
limiting exists), refresh-token families with reuse detection, recovery codes, admin-minted reset
tokens, sudo mode, the breached-password check, per-box Argon2id tuning, and the capability map (§7.4 —
`role` is carried on the `Scope` but no static per-method map fails closed yet).

### 7.1c Boot order — what the splash is actually for

`Root` holds the screen for two round trips, not one: **`WhoAmI` and `GetPrefs`**. The second one is
there because of A30 (§20.13) — where you were is account state — and account state has to be
*fetched*, which means it cannot be a component's initial value unless something holds the screen
while it arrives. Something already does.

Before this, the saved view was the reader's own opening effect: it mounted with its defaults, painted
the All stream with an expanded rail and the house theme, and snapped into the saved feed a round trip
later. Reported as *"after the splash it is instantly the default view and then flashes to the past
state."*

The rule the boot sequence now follows, and the reason it is written here rather than left in the code:

| What | Where it must happen |
|---|---|
| Anything on `documentElement` — theme, accent, reading size, motion, pane widths | **Before the phase flips.** These are custom properties, so applying them costs no render and applying them late costs a repaint of the whole app |
| Anything that is a component's initial state — scope, filters, rail folds | **As a `UseState` initial value**, from the prop `Root` passes down. A hook's initial value is read once, on mount, which is the only moment restoring is free |
| The saved article, and the first list fetch | **After mount**, in the effect — they need the list, and the list needs the scope that was just restored |

The login path does the same, so signing in on a second machine lands in the view you left rather than
assembling it afterwards. The reader keeps its own fetch for when the prop is absent: that is the old
behaviour, flash included, and it is the correct fallback — a flash is a blemish, and a reader dropped
back to All every morning is the feature not working.

### 7.1b The client half — `Root`, `Login`, and the token

`client/data/auth.go` · `client/view/{root,login}.go`. Built immediately after §7.1a, because a server
that requires a login and a client that cannot perform one is a reader nobody can open.

**`Root` is the mount point and `Reader` is now its child**, which is a structural decision rather than
a tidiness one: `Reader` holds forty-odd hooks and mounts a virtualised list, so rendering it behind a
login overlay would fetch a feed list the caller is not entitled to, collect thirty `Unauthenticated`
errors, and paint the furniture of an account nobody has proven they own. Three phases, and the middle
one earns its place:

| Phase | When | Why it exists |
|---|---|---|
| `checking` | a token is in storage and `WhoAmI` has not answered | Without it, a page with a *good* token paints the login screen for a few hundred milliseconds and then replaces it. **That flash trains people to start typing a password they do not need**, which is worse than a moment of nothing |
| `login` | no token, or the server rejected it | |
| `reader` | the server confirmed the identity | |

**A wasm client cannot know at boot whether its stored credential is still good** — it may have
expired, been revoked from another device, or been minted against a database that has since been
restored from a backup. Only the server knows, so `Root` asks. The connection is dialled once and
reused across the login rather than redialled, since a second tunnel counts against the per-client
connection cap for nothing.

The token lives in local storage under a **namespaced, versioned key** (`articleflux.v1.token`), so a
change to what is stored is a change to the key rather than a migration of somebody's browser. It is
held in a package-level variable read by a **client interceptor** that attaches `authorization: Bearer`
to every call: the interceptor is installed when the connection is dialled, which is before anyone has
logged in, and a token that arrives later has to reach it. The same interceptor watches for
`Unauthenticated` and hands control back to `Root` — **except on `Login` itself**, which answers
`Unauthenticated` for a wrong password and must not be read as an expired session. `device_id` is
persisted alongside and **deliberately not cleared on sign-out**: it identifies the browser, not the
session, and a device that gets a new id on every logout defeats the account screen it exists for.

### 7.2 Recovery — three rungs, none of it billed

| Rung | Mechanism | Cost |
|---|---|---|
| **1** | **Recovery codes** — 10 single-use, shown once, Argon2id-hashed | $0 |
| **2** | **Admin-minted reset** — single-use, 15-min token from the console | $0 |
| **3** | **Emailed reset link** — *optional*, only if SMTP is configured | ~$0, external dep |

**Break-glass:** `articleflux admin reset-password --user X` from the host filesystem — filesystem access
*is* proof of ownership, it's what Gitea and Grafana do, and it's audited like any other reset.
*(Distinct from first-run bootstrap, which is §22.3 — that's the case where no user exists at all.)*

All rungs: hashed at rest, single-use, 15-minute TTL, invalidate every session on success, and the
initiate endpoint always answers *"if that account exists, a reset was started."*

### 7.3 No 2FA: the consequence, and what buys it back

**With one factor on a public service, a phished or stuffed password is total account compromise.**
Four free controls: **login rate limiting and lockout** per-username *and* per-IP (the highest-value
control here) · **refresh-token reuse detection** revoking the whole device family on replay ·
**sudo mode** re-authentication before role changes, suspension, impersonation, deletion, or full
export · **an identity gate in front of `/admin`** (Cloudflare Access free tier) — the closest thing
to free 2FA, since the check happens before a request reaches the app.

*On the cost premise:* SMS 2FA costs per message, a fine reason to skip it. **TOTP costs nothing** —
RFC 6238, a shared secret and an HMAC, ~a day of work. `user_mfa` would be a purely additive
migration, so this stays a one-day change whenever wanted.

### 7.4 Capabilities, not role checks

```
feed.read  feed.subscribe  feed.unsubscribe  feed.settings   item.state.write  item.tag
note.write  bookmark.read  bookmark.write  rule.read  rule.write  view.write  offline.pack
share.public  webhook.write  notify.manage  api.token.mint  llm.use  export.self  export.all
profile.write  tenant.settings.write  user.invite  user.role.set  user.suspend  user.delete
tenant.create  tenant.suspend  tenant.delete  system.health  system.audit  system.impersonate
```

Seeded roles: **viewer** · **member** · **admin** · **owner** · **superadmin**.

### 7.5 One enforcement point

A gRPC interceptor resolves the credential → `Scope{TenantID, UserID, Caps}` and **rejects before the
handler runs**, against a static per-method capability map declared beside service registration.
**A method with no entry fails closed.** The sync API (§15.1) resolves its token through the same path
and the same map — one authorization model, not two.

### 7.6 Two layers below it

The interceptor answers "may this user call this method." It cannot answer "does this user own row
4711." That's the repository `Scope` plus explicit ownership assertions in the service layer. Both,
always.

### 7.7 Visibility

**private** (owner only) · **tenant** · **shared** (grants in `shares`).

**Notes default to private and stay private even from tenant admins.** An admin can delete a user's
data; an admin should not read their reading notes. Say that in the UI.

### 7.8 Sharing — and what "contribute" actually means

A `shares` row grants `read` or `contribute` on a folder. Rev 7 never said what contribute *permits*,
which left two contributors on one folder with no defined model. Enumerated:

| Action | `read` | `contribute` | owner |
|---|:---:|:---:|:---:|
| See the folder and its sources | ✅ | ✅ | ✅ |
| Read items, keep own read/star/tag/note state | ✅ | ✅ | ✅ |
| Add a source to the folder | — | ✅ | ✅ |
| Remove a source **they added** | — | ✅ | ✅ |
| Remove a source **someone else added** | — | — | ✅ |
| Rename / move / delete the folder | — | — | ✅ |
| Re-share to a third party | — | — | ✅ |

Contributed sources record `added_by`. **The owner always wins** a conflicting edit, and folder
rename/delete are owner-only, so there is no concurrent-edit ambiguity to resolve. Every share is
audit-logged, expirable, and revocable from both sides.

**Public shares** (§7.8b): share an item with an optional comment into a collection that is **itself a
public Atom feed** at `/pub/:slug` — Google Reader's social layer, whose removal people still bring up.
Unguessable 128-bit slug, rotatable (rotation breaks existing subscribers — say so), revocable per item
and per collection. **Excerpt-only, never the full extracted article** — republishing someone else's
content under your name is a licensing decision, not a technical one. Sanitized Atom with ETag/
Last-Modified, its own rate limit, and `X-Robots-Tag: noindex` unless opted in.

### 7.9 Deleting a user (rev 7 had no path at all)

Rev 7's admin console offered invite, roles, suspend, force-logout, revoke, reset — **never delete** —
while the schema was full of `ON DELETE CASCADE` on `user_id`. Deletion would only ever have happened
via raw SQL, firing cascades nobody designed.

`user.delete` is a real, sudo-gated, audited action with an explicit policy:

| Data | On delete |
|---|---|
| Subscriptions, `user_item_state`, tags, item_tags, rules, notes, bookmarks, engagements, views, packs, devices, tokens | **Cascade-deleted.** Purely personal. |
| `items`, `sources` | **Untouched** (A22). Global. |
| Mailbox sources they owned (`kind='mailbox'`) | **Cascade-deleted** — private to them by §6.4. |
| Shares they **granted** | Revoked; grantees notified. |
| Shares they **received** | Revoked. |
| Sources they contributed to a shared folder | **Reassigned to the folder owner**, not removed — otherwise deleting a user silently guts someone else's folder. |
| `audit_log`, `rule_hits` | **Retained, actor anonymized** to a tombstone id. An audit trail you can erase by deleting the actor isn't one. |
| Public share collections | Disabled and slug retired. |

**Deletion is soft first** (`users.deleted_at`, immediate session revocation, invisible everywhere),
with a hard purge after a configurable grace period. Accidental deletion of the only person who knows
what those 400 rules did is not recoverable from a nightly backup without losing everyone else's day.

### 7.10 Profile

`ProfileService`: display name, timezone (§22.9), locale, avatar, and **recovery email with a verified
flag**. Rev 7 implied an emailed reset (§7.2 rung 3) without ever capturing or verifying an address.
Changing the recovery email requires the current password and notifies the old address if one was set.

---

## 8. Settings architecture (A16)

Everything tunable resolves **system → tenant → user**, most specific wins. One pattern for fetch
timing, offline depth, ranking weights, typography, notifications, quiet hours, and screensaver
behavior.

A **typed settings registry**: each setting declares key, type, default, allowed range, the scopes it
may be set at, and the capability required. **The settings UI renders itself from the registry** —
the only way this survives past ~80 settings. Export/import as JSON, and `GET` returns the resolved
value **plus which layer supplied it**, so "why is this 30 minutes?" is answerable in the UI.

---

## 9. Admin console

A route, not a separate binary — capability-gated, invisible without them.

**Tenants** (superadmin): create, rename, suspend/resume, **delete (§9.1)**, quotas, usage.
**Users**: invite via admin-minted code, roles, suspend, force-logout, device and API-token lists with
per-item revoke, password reset, **delete (§7.9)**. **Health**: poller status and **lag** (§22.7),
job-queue depth, **feed error leaderboard**, inactive sources, storage by table, LLM spend and
**circuit-breaker state** (§22.8), event ring depth per tenant, sync-API client activity. **Audit**
with filters. **Impersonation** — audited on entry *and* exit with a persistent banner; impersonation
without a loud, logged trail is a backdoor. **Shares**: who can see what, both directions, one-click
revoke.

### 9.1 Deleting a tenant with live inbound shares

Rev 7 allowed tenant deletion and cross-tenant sharing without saying what happens when both apply.
Deleting tenant A while tenant B holds a share into A's folder must not silently break B.

Deletion is a **three-step, non-instant** operation: (1) **enumerate** every inbound and outbound share
and show them in the confirmation — "3 other tenants lose access to 2 folders"; (2) **notify** affected
tenants and mark the shares `revoked_at` with a reason, so B sees "shared folder withdrawn" rather
than a vanished sidebar entry; (3) **soft-delete** with a grace period before purge. B's own tags,
notes, and state on those items **survive**, because items are global and A22 keeps them — B just
loses the access path.

---

## 10. The article

### 10.1 The rendering ladder

| Tier | What | Cost | When |
|---|---|---|---|
| **0. Feed content** | `content:encoded` is often the whole article | free | ✅ default |
| **1. Reader extraction** | Server fetches, readability, cached in `extracted_html` | one fetch | ✅ **M2 — see below** |
| **1a. Asset proxy** | Our origin re-serves the images the article points at | one fetch per asset, cached | **M9 — §10.1a** |
| **2. Page snapshot** | Fetch, rewrite every asset URL, sanitize, serve from our own origin | one fetch + rewriting | **M27 — §10.1b** |
| **2r. Rendered snapshot** | Tier 2, but a headless browser runs the page's JS before we take the DOM | one browser render | **M28 — §10.1c** |
| **3. Frame stream** | Screencast frames, diffed to tiles, over the bidi tunnel | a browser per session | **M28 — §10.1d** |
| **4. Interactive** | Tier 3 plus input events back up the same stream | a browser per session | **M28 — folded into tier 3** |

**Tier 1 moved to Phase 1 (M2).** Rev 7 scheduled it at M13 while M9 (bookmark archiving) and M10
(offline `text` packs) both required it — §10.1 itself listed them as consumers. It is a dependency of
five features (reader mode, archiving, offline text, ranking text, TTS) and belongs in the foundation.
M13 keeps only the render-mode *switcher* UI.

**Rev 9 folds tier 4 into tier 3 and un-scopes both.** Rev 8 called the interactive remote browser out
of scope, which was the right instinct about *cost* and the wrong conclusion about *shape*. A frame
stream nobody can click is strictly worse than a screenshot: it costs a live browser and delivers less
than a PNG. The input half is `Input.dispatchMouseEvent` over a stream that already exists, and it is
the half that makes the other half worth having. **Either both or neither** — and this plan now says
both, behind a flag, at M28.

**About the iframe:** `X-Frame-Options: DENY` and `frame-ancestors` CSP mean a large share of news
sites refuse to be framed, and the parent page can't reliably detect it — you get a blank box. Keep the
button, add a visible "didn't load — try Reader" fallback on timeout, don't build a layout that assumes
it works. **Note what tiers 2 and above do to this problem:** bytes we re-serve from our own origin
carry the headers *we* set, so the framing refusal disappears. That is a side effect, not the goal, but
it is the reason the snapshot tiers make the article view work on sites that otherwise cannot be
embedded at all.

### 10.1-R The runtime ladder — what the client actually picks, and in what order

The table above is a **capability** list: what the server can produce. It is not the order anything is
tried in, and reading it as one gets the product backwards.

**The runtime order, decided 2026-07-26 (Cam):**

| # | Rung | Chosen when | Fidelity | Cost |
|---|---|---|---|---|
| **1** | **Client loads the real page** | always, first | perfect — it *is* the page | zero |
| **2** | **Frame stream** (tiers 3–4) | the client's network cannot reach the origin | near-perfect: live, interactive, every script runs | a browser per session |
| **3** | **Compressed post-rendered HTML** (tier 2r) | the link cannot sustain the stream | visual, but frozen — no interaction | one render, then cached |
| **4** | **Reader text** (tier 1 + 1a) | the link cannot sustain a page either, or 3 fails | the words and the pictures | already built |

**The ordering principle is fidelity first, degrade only when a constraint forces it.** Earlier drafts
of this section escalated by *server cost* — cheap fetch, then render, with the stream as a flag-gated
last resort. That optimised the wrong variable. The reader is trying to see a page; the question at
each rung is "what stops me showing the real thing," and the answer is a constraint, not a preference.
So each step down is a named constraint being hit, and no step is taken speculatively.

**Three consequences, stated plainly because they are not free:**

1. **Rung 2 makes tier 3 load-bearing rather than optional.** Under the old ordering the blocked-network
   story worked at every stage: extraction gave you text, the asset proxy gave you pictures, snapshots
   gave you pages. Under this one the *primary* answer to "blocked" is the frame stream, so the
   headline feature does not exist until M28 lands. Rungs 3 and 4 are what make that survivable in the
   meantime, and they are why rung 4 is written into this table rather than left implicit.
2. **R22 becomes a default exposure rather than an opt-in one.** A flag-gated stream nobody turns on
   has no traffic signature. A stream that is the automatic answer to a blocked origin has one every
   time the case it exists for occurs. That does not change the design, but it does change the UI
   obligation: the first time rung 2 engages, the reader is told what is about to happen and can stop
   it. Consent moves from a settings toggle to the moment of use.
3. **A blocked network now implies a live browser per reading session, by default.** On the D8 home box
   that is the single largest resource commitment in this document, and it is why §22.7's queue and a
   hard session cap are prerequisites of M28 rather than polish on top of it.

**The hard part is not the rungs, it is knowing which one to be on.** Both transitions are detections,
and neither is reliable by default:

- **"The network blocked it"** is close to undetectable from the client by design. A cross-origin
  fetch that is blocked, a DNS failure, a captive portal, and plain offline all surface as the same
  opaque error, and an iframe that was refused looks exactly like one that is still loading — the
  paragraph above this section says so about `X-Frame-Options` and it applies with more force here.
  A **probe** is therefore required rather than optional, and it is the design work rung 2 depends on.
- **"The link cannot sustain the stream"** is the easier half and should be *measured, not predicted*.
  `navigator.connection` (`effectiveType`, `saveData`, `downlink`) is a hint and is absent on some
  browsers; the stream's own throughput is ground truth. Rung 3 is therefore entered by observing
  frames arriving too slowly to be worth their cost, and the tile diff (§10.1d) is what makes that
  measurable in the first place.

Both detections are **D21**. Until it is answered, the ladder is manual — a switcher the reader
operates — which is a perfectly good v1 and is what `RenderModeSwitcher` already exists to be.

### 10.1a The blocked-origin case, and why the asset proxy is not a Phase 5 feature

**The motivating case, stated plainly.** You are reading from a network that blocks the origin — a
corporate filter, a captive network, a country. The server is not on that network; it is at home behind
D8's tunnel, and it can reach what you cannot. Every tier above 0 is therefore also a *reachability*
feature and not only a *readability* one: the server fetches, and you receive bytes from a hostname the
filter already permits.

Three things follow, and the third is a bug that exists today.

1. **Tier 1 already solves the text.** Extraction fetches from the server and caches
   `extracted_html`; the article body arrives regardless of what your network thinks of the publisher.
   This is the cheapest 80% of the feature and it is already scheduled at M2.
2. **Tier 0 and tier 1 do not solve the images.** Feed HTML and extracted HTML both carry `<img src>`
   pointing at the origin, and the *browser* resolves those — from your network, not the server's. So
   the words arrive and the pictures hang. On a hardware review, that is most of the article.
3. **Therefore the asset proxy is a reading-path fix, not a snapshot sub-feature.** It is roughly a
   day of work and it repairs an article that renders wrong right now, on any blocked network, at
   tier 0. It has no dependency on tiers 2–4 and must not be scheduled behind them.

**`imgproxy` (§21's name for it) is one endpoint and one rewrite hook.** `internal/sanitize` already
walks the DOM with `x/net/html` for every policy; rewriting `img/@src`, `img/@srcset`, `source/@srcset`
and `poster` as it walks costs a function call. The endpoint validates a signed URL, fetches through the
§21 guard, caps size and content-type to images, and caches to disk keyed by URL hash.

**It also finishes the archive.** §10.6 preserves `archived_html` so an article survives its publisher;
an archive whose images still point at a dead origin is half an archive. That is why this lands **with
M9** rather than in polish — archiving is the consumer that makes it structural rather than cosmetic.

**One deliberate exception.** `internal/sanitize`'s `Newsletter` policy *drops* remote images rather
than proxying them, and the rewrite hook must not quietly re-enable them. Proxying a tracking pixel
still tells the sender you opened the mail — the fetch happens either way, only the source IP changes.
The reasoning is already written into the package; the proxy inherits it rather than overriding it.

### 10.1b Serving a whole page: the two rules that are not negotiable

Tier 2 is: fetch the page, resolve every asset URL against the final post-redirect URL, rewrite them
through the proxy, drop every script, sanitize, cache, serve. The rewrite surface is larger than
"images" — `srcset`, `<source>`, `<link rel=stylesheet>`, `@import` and `url()` inside both inline
`<style>` and fetched CSS (recursively, since CSS pulls fonts and images), `<video>`/`<audio>`,
`<a href>`, `<base>` — plus stripping `integrity` (the bytes change, so SRI would fail every asset) and
any `<meta http-equiv="Content-Security-Policy">`.

That part is tedious and testable. The two rules that decide whether this is safe are not:

**Rule 1 — proxied pages are never served from the app's origin.** Sanitized-but-hostile HTML sitting
on the origin that holds the session is one bypass away from reading it. The response gets
`default-src 'none'`-class CSP, `nosniff`, and a sandboxed iframe with neither `allow-scripts` nor
`allow-same-origin` — but the control that actually holds is a **separate hostname**
(`proxy.<instance>`), so the browser's own origin boundary is doing the work instead of a CSP string we
have to keep correct forever. Same argument as §21's layering everywhere else: the fast rejection is
the header, the guarantee is the boundary.

> **D20, decided 2026-07-27 — and now enforced.** Rule 1 was true and unenforced: with
> `ProxyOrigin` empty the page proxy served from the app's own origin, which is what the rule
> forbids, and the development server ran that way. The page proxy now REFUSES to start without a
> separate origin and says why. There is no same-origin fallback — a fallback is the thing the rule
> forbids — so an instance that cannot give the proxy a hostname simply does not get the page proxy.
> Images are exempt: the asset proxy serves non-executable bytes with `nosniff`, so the origin
> boundary buys little, and holding them to the same rule would cost every instance its image proxy
> for nothing.

**Rule 2 — the proxy never accepts a URL the server did not choose.** An authenticated endpoint that
fetches arbitrary URLs from the instance's IP is an open proxy wearing a login, and the §21 guard stops
SSRF without stopping *abuse of egress*. Every URL the proxy fetches must be one that already appeared
in an item the caller was allowed to read.

**Auth is a signed capability, not the session token.** An `<iframe>` and an `<img>` cannot set an
`Authorization` header, and §21 already refuses query-string session tokens for `/speech` — that URL
lands in history, referrers and access logs. So proxy URLs carry a short-TTL HMAC, minted by an
authenticated path and verified at the edge, exactly the mechanism §21 already specifies for pack URLs.

> **Implementation note, 2026-07-26 — the capability signs the URL, not an index.** This section first
> specified the parameter as an *item id plus an asset index*, resolved server-side on every request.
> Built, that turned out to be the wrong shape for two reasons, and the property it was protecting
> survives without it.
>
> The cost: re-deriving the URL means a database read and a full HTML parse **per image**, so a forty-
> image article parses itself forty times to serve one page. And the index is positional — a publisher
> edit (§10.3 keeps revisions precisely because they happen) shifts it, and the reader silently gets
> the wrong picture, which is worse than a missing one.
>
> The property was never actually enforced by the index. A logged-in caller who wants a URL of their
> choosing inside item HTML can subscribe to a feed they control and put it there; both designs reduce
> to netguard plus the mint gate. So the mint gate is the whole control, and it is unchanged: a
> capability exists only for a URL the server found in stored HTML that the caller could read. What is
> signed is `("asset", url, exp)`, and `exp` is inside the signature rather than beside it — a client
> that could edit the expiry without invalidating the signature would hold a permanent one.
>
> What is genuinely given up: a capability outlives the HTML it came from, for its TTL. It grants
> fetching one public image through this server for a few hours and carries no identity. That is the
> reason the TTL is hours rather than weeks.

### 10.1c The headless renderer — tier 2r

Tier 2 breaks on anything React-rendered: fetch the HTML and you get `<div id="root"></div>`. The fix
is a real browser, server-side, that runs the page and hands us the DOM afterwards.

**The renderer is a swap-in for the fetcher, and nothing downstream changes.** `Render(url)` returns
bytes where `Fetch(url)` returned bytes; rewriting, sanitizing, caching, the proxy origin and the signed
URLs are all identical. This is the single most important property of the design — tier 2r is a new
*source* of HTML, not a new pipeline.

**Escalate, don't default — within this rung.** The cheap fetch runs first; the renderer is used only
when the result is empty or implausibly short for its word count. Roughly 70% of blogs, docs and news
sites never need a browser, and the ones that do are exactly the ones worth spending three seconds on.
*This is a rule about how rung 3 produces its HTML, not about when rung 3 is chosen — §10.1-R owns
that, and under it rung 3 is entered because the link cannot sustain rung 2.*

**Compressed, because that is the entire reason this rung exists.** §10.1-R reaches tier 2r when
bandwidth cannot carry a frame stream, so a snapshot that ships a 4 MB hero image has answered the
wrong question. What "compressed" means here, concretely:

- **Brotli or gzip on the wire**, precompressed once at render and cached that way — the same trade
  §7.6 already makes for the wasm bundle, and for the same reason: the artifact changes rarely and is
  served often.
- **Images downscaled and re-encoded to WebP** at the reading column's width, through the §10.1a asset
  proxy that already fetches and caches them. A 2400px hero on a 700px column is bytes nobody sees.
- **Scripts already gone**, which is a size win before it is a security one, and usually the largest
  single one on a modern page.
- **A budget, enforced and reported.** If the compressed artifact still exceeds it, degrade to rung 4
  rather than sending it — the reader asked for a page they could load, and half of one delivered
  slowly is worse than the text delivered now.

**Mechanism.** Chrome DevTools Protocol, driven from Go — `chromedp` or `rod`, both pure Go, both able
to attach to an already-installed Chromium (Edge is present on the reference box). No Node, no
Playwright, no browser download, which keeps A26 intact. Load, wait for network-idle with a hard
timeout, scroll to the bottom once so lazy images resolve, then take `outerHTML`. A full-page
screenshot is the same session and one more call, and it is the fallback artifact when the DOM dump
comes out unusable.

**Ceilings, stated up front so nobody rediscovers them at M28.** Scripts are dropped after the render,
so the page is *frozen at the moment of capture*: accordions, tabs, carousels, menus and infinite
scroll are dead. Cookie banners freeze in place on top of the content unless dismissed or removed by an
injected rule. Anything requiring your login renders as the server's anonymous session, which is to say
logged out. A page that needs interaction is a tier-3 page, and that is the honest boundary between the
two halves of this feature.

**Cost.** 300–500 MB and 1–4 seconds per render, CPU-heavy while it lasts. Therefore: **one at a time,
queued through §22.7's job queue, on demand, cached forever after.** Rendering is never a background
sweep — a preservation pass that shells out to a browser for every item would cook the box and look
like an attack from the publisher's side.

### 10.1d The frame stream — tiers 3 and 4

For the pages tier 2r cannot freeze usefully, the answer is not a better snapshot; it is the live
browser itself.

**Mechanism.** `Page.startScreencast` emits `Page.screencastFrame` events carrying a JPEG plus scroll
and scale metadata, each acknowledged with `Page.screencastFrameAck` or the stream stops. It is the
same machinery behind DevTools' device mirroring, and it is **damage-driven rather than fixed-rate** —
a static article costs roughly one frame and then silence, which is precisely the shape of reading.

**The transport already exists.** GoGRPCBridge tunnels bidirectional streaming, so frames go down a
server-stream and clicks, scrolls and keystrokes come back up the same one:
`Input.dispatchMouseEvent`, `dispatchKeyEvent`, `Input.synthesizeScrollGesture`. No WebRTC stack, no
second port, no new origin — and the §21 caps already applied to the tunnel apply to this.

**Tiles, because whole frames are the wrong unit.** Screencast gives complete JPEGs with no inter-frame
compression: a 1280×800 frame is 150–250 KB, and continuous scrolling at 10fps is 1.5–2.5 MB/s, which
is a poor way to use a tunnel and a worse way to use a corporate network. So the server diffs
consecutive frames into 64×64 blocks and sends only the changed ones, with the client compositing into
a tile grid it already holds. On a mostly-static page this collapses to near nothing. It is real work —
frame differencing, a client-side compositor — and it is the difference between "works on a good
connection" and "works from where you actually are".

**The safety property is the best in the ladder.** Pixels cannot XSS anything. No rewriting, no
sanitizing, no proxy origin, no signed asset URLs — the hostile page never reaches our origin in any
form. Tier 3 is the *most* dangerous option for the server and the *least* dangerous one for the
client, and those are different threat models with different answers.

**What it will feel like, honestly.** A click travels client → tunnel → box → Chrome → repaint → JPEG →
back, which is 150–300 ms in a realistic deployment. Clicking a link is fine. Scrolling is laggy.
Typing is unpleasant. This is a tool for reaching a page, not for living in one, and the UI should not
pretend otherwise.

**Why not real video.** VP8/H.264 over WebRTC is dramatically better on bandwidth and smoothness, and
it assumes a Linux container with an X server and an ffmpeg capture pipeline — the neko/Kasm shape. The
reference box is Windows. Screencast is the option that is not swimming upstream, and the tile diff
recovers most of what the codec would have given us.

**One operational caveat that belongs in the spec rather than in a surprise.** A persistent WebSocket
carrying megabytes of JPEG to a personal domain has a completely different traffic signature from
reading text, and networks that block origins are usually networks that look at signatures. Tier 3 is
the loudest thing in this document. That is a fact about the deployment, not a reason not to build it.

**§10.1-R changes what follows from that, and it is worth being exact.** Tier 3 was going to be
flag-gated off and reached only by someone who went looking. It is now **rung 2** — the automatic
answer to a blocked origin — so "off by default" would mean the ladder never works. The gate does not
disappear, it moves:

- The **instance** switch stays. An operator can refuse to run browsers on their box, and rungs 3–4
  answer instead.
- The **per-reader** gate becomes consent at the moment of use rather than a setting: the first time
  the ladder wants rung 2, the reader is told what it does — a live browser on the server, a
  continuous stream to this machine — and chooses. Remembered per instance, revocable.
- **The default for a reader who has never been asked is rung 3**, not rung 2. Falling *down* the
  ladder without being asked is always allowed; falling to the rung with a traffic signature is not.

That is the one place where fidelity-first is deliberately overridden, and R22 is the reason.

### 10.2 Typography and reading comfort

Three-layer per §8: **font family** (a small curated set plus system), **size**, **line height**,
**measure** (max line width in ch), **paragraph spacing**, **alignment**, **theme**
(light/dark/sepia/high-contrast), **image display**, and **direction** (LTR/RTL/auto from
`items.lang`). Live preview while adjusting.

### 10.3 Revisions and diffs

Publishers silently edit. `content_hash` changes on re-fetch already detect it. Keep the last N
revisions in `item_revisions`, mark the item **"edited 2h ago"**, offer an inline diff.

**The write path ships at M1, not at M23 when the UI does.** Otherwise the feature launches with no
history to show after months of ingest. Same rule for embeddings (§18) — populate from day one, expose
when the feature lands. An edit **never** resets read state.

### 10.4 Audio: TTS and podcasts

An article read aloud and an `<enclosure>` are the same player. Two TTS backends: the browser's
`SpeechSynthesis` (free, instant, mediocre) and **server-side local NPU TTS** — the Supertonic pipeline
already running on the X2 at ~10× realtime, which no self-hosted reader has. `AudioService.Synthesize`
streams chunks, cached in `item_audio`, **counted against storage quota and first to be shed under
disk pressure** (§22.6). Player: queue, speed, resume position, background playback, "listen to my
unread" from any `ViewSpec`. Offline packs gain an `audio` depth.

### 10.5 Translation

Per-item, on demand, via `llm.Provider`. Detect `items.lang`, cache in `item_translations`, count
against the **same LLM budget and the same circuit breaker** as everything else (§22.8).

### 10.5a UI translation, and the one LLM client (shipped 2026-07-26)

Three decisions, made together because they only make sense together.

**Every Smart+ feature goes through `internal/llm`, and `internal/llm` uses OpenAI's Responses API
(`POST /v1/responses`) and nothing else.** Not chat completions, not assistants, and not a second SDK
bolted on for the next feature. One endpoint means one egress boundary to audit, one place the key is
read, one spend meter, one breaker; two would mean two of each, and the second of each is the one
nobody checks. Responses specifically, because structured output is a first-class request field
(`text.format`) — so a feature that needs JSON gets a schema-validated object rather than a model that
sometimes wraps it in a code fence — and because `max_output_tokens` plus `incomplete_details` make
truncation an explicit, detectable outcome. That last one is not theoretical: a translated catalog
silently missing its final forty keys is exactly the failure this app would otherwise ship, so
`llm.ErrTruncated` **refuses a partial answer** rather than returning the text it did get.

Two request-level defaults worth knowing about: `store:false`, because the Responses API defaults to
retaining input for thirty days and a self-hosted reader must not inherit that silently; and a host
allowlist checked against the request about to go out rather than against the endpoint constant, which
is the same rule `internal/tts` documents.

**The API key is a persisted, encrypted SETTING, not only an environment variable.** It lives at
`settings.scope='system'`, key `smart.openai_api_key`, sealed with AES-GCM under a 32-byte key at
`secrets.key` beside the database (`ARTICLEFLUX_SECRET_KEY` overrides). Three things follow:

- **The key is read through a function on every call, never captured at construction.** Changing it on
  the Settings screen takes effect without a restart, which is not a nicety for a box nobody wants to
  SSH into.
- **`internal/tts` reads the SAME function.** One credential drives every Smart+ feature. An instance
  where the voice works and translation does not, because one read the environment and the other read
  the setting, is a bug with no visible shape from the settings screen.
- **What 0600-beside-the-data actually buys, stated rather than implied:** it protects a leaked
  *database* — a backup, a `VACUUM INTO` copy, a `.db` someone emailed themselves — and not a
  compromised host. Same bet §7.2 already makes. If no key can be opened, `SetSystemSecret` **refuses**
  rather than writing a credential in the clear.

**UI translation is a Smart+ feature, and it is filed beside the key that pays for it.** §22.16
extracted the catalog; this translates it. `internal/smart` reads the English catalog *directly from
`client/i18n`* — that package has no build tag, so the server can import it — batches it 60 messages at
a time through a strict `json_schema`, and caches the result per locale keyed by **a hash of the
English**. A build that edits one string invalidates every translation of it; a build that does not is
free forever. Plural messages are flattened to `key#category` for the wire, because a strict schema
cannot express an object with arbitrary keys.

Consequences that are not obvious from the code:

- **The picker lives on the Smart+ tab, not under Appearance.** Choosing a language spends money.
  A picker that looked like a free preference, three tabs from the thing that pays for it, is a bill
  nobody expected.
- **Every Smart+ RPC is owner-only** (`superadmin`, `admin`), checked at the top of each method rather
  than in a wrapper — until §7's capability interceptor lands, a gate that lives elsewhere is a gate
  somebody adds an RPC without. A member may still turn Smart+ *voice* on for themselves; that is a
  per-user preference and costs the instance nothing it cannot already spend.
- **The chosen locale is remembered in `localStorage`, not in server prefs.** It has to be known before
  the reader mounts (§22.16a), and reading it from the server would put a round trip in front of the
  first paint for every reader — including the overwhelming majority who never leave English.
- **Translation quality has a failure mode the catalog cannot see.** The command palette ranks by
  prefix-of-label; a translation that reads well but starts every command with the same word makes the
  palette useless in that language. `Retranslate` (force) is the only lever a reader has, and it exists
  for exactly this.
- **A partial catalog is cached on purpose.** Nine tenths of the keys is a usable UI — the Bundle falls
  back to English for the rest — and paying again for the same nine tenths helps nobody.

### 10.6 Preservation — when the source goes away

Earlier revisions archived **bookmarks** eagerly at save but **items** lazily (`extracted_html` was
"NULL until requested"). That's backwards for the failure that actually happens: a feed dies, and every
item you never opened is now a title, a teaser, and a dead link. The feed's own `content_html` survives
— but for the many feeds that publish a truncated excerpt, that's a paragraph and a "Continue reading."

**Seven distinct failures hide behind "the feed went down", and they need different answers:**

| Failure | What's actually lost | Answer |
|---|---|---|
| Endpoint 5xx / timeout | Nothing yet | Backoff, `last_error`, **and trigger a preservation sweep** — the *articles* are probably still up even when the *feed* isn't |
| Feed 404 / domain gone | All future items | Mark `gone`, stop polling, keep everything (A22) |
| **Article link rot** — feed fine, article 404s | The content, silently | The common case. Archive coverage + a visible banner + Wayback fallback |
| Feed truncates to the last 20 | History you never fetched | **RFC 5005 backfill on first subscribe** (§15.6) — only possible while the feed is alive |
| Publisher edits | The earlier version | `item_revisions` already keeps it |
| Paywall appears | Future access | Whatever we archived pre-paywall stands |
| Site blocked from the server | Everything, until it isn't | Retries; archives keep working |

**Tiered archival — eager where it's likely to matter, lazy elsewhere.** Archiving everything is a
fetch storm and a storage problem (R7); archiving nothing is the current bug.

- **Eagerly at ingest** — items from sources with high open-rate affinity · items that land in the
  homepage **Top** slot · items whose feed content looks truncated *and* whose source you engage with ·
  any source set to `archive_policy = always`.
- **On interaction** — anything opened, starred, noted, tagged, or bookmarked archives immediately if
  it hasn't already. Unchanged from before, still correct.
- **On distress — the new one.** When a source crosses into `failing`, enqueue a **preservation sweep**
  over its recent un-archived items, rate-limited and polite. A feed erroring is the best early warning
  you get that a site is in trouble, and it usually arrives while the article URLs still resolve.
- **Never** for `archive_policy = never`.

**`archived_text` is stored for everything we fetch at all; `archived_html` follows the policy.** Text
is a fraction of the size, it's what makes search work, and it's what you actually need when the origin
is gone. This is the cheap half of the feature and it should be near-universal.

**Source lifecycle**, surfaced in feed health (§16.3) and as `FeedStateChanged`:

```
ok → failing (N consecutive errors) → gone (persistent 404/410/NXDOMAIN, or 30 days failing)
ok → dormant (fetches fine, nothing published in 90 days)
```

`gone` sets `deactivated_at`, stops polling, **keeps every item and every user's state** (A22), and
reports honestly: *"AnandTech is gone. You have 412 items, 89 with full saved text."* That sentence is
the feature.

**Link rot on items, not just bookmarks.** A slow background sweep checks liveness for **engaged**
items only — starred, noted, tagged, bookmarked — because checking 50k is rude and pointless. An open
that 404s sets `items.origin_dead` immediately.

**What the reader shows**, which is where this becomes real rather than a database property:

- Archived → serve it with a plain banner: *"The original is gone. This is the copy saved on 12 March."*
- Not archived → serve feed content plus *"the original is no longer available and no copy was saved"*
  — **and offer a Wayback link** (`web.archive.org/web/2/<url>`, no key, no cost). The honest answer
  to "we don't have it" is "someone else might."

**Governance**, because this is the biggest single driver of R7: `archive_policy` per source
(`auto | always | never`), a global archive cap with eviction that is **forbidden from evicting an
archive whose origin is dead** — that's the entire point — or one belonging to an engaged item, and
zstd compression on `archived_html`.

**One raw feed body per source is kept** (`sources.last_body`, gzipped, replaced each fetch). Cheap,
and it lets a parser fix be re-applied to the last known state without re-fetching a feed that may no
longer answer.

**The promise stays modest and true:** ArticleFlux keeps what it fetched. It is not archiving the web, and
the UI should never imply it is.

---

### 10.7 Listening (A31)

Two engines behind one control, and the difference between them is an egress
boundary rather than a quality setting.

**Free / always-on — `window.speechSynthesis`.** Installed, offline, no key, no
cost, and it uses the voice the reader already chose system-wide. The text comes
from the rendered `innerText` rather than from the stored HTML, so what is spoken
is exactly what is on screen — entities already resolved, hidden content already
dropped.

> Chunked into ~220-character sentences, and that is not an optimisation.
> A single long utterance hits a long-standing Chrome bug: it stops after roughly
> fifteen seconds with no error and no `end` event, so the article simply goes
> quiet in the middle. Many short utterances sidestep it, and they also make
> pause and stop responsive, because the engine can only act on a boundary it has
> reached.

**Smart+ — OpenAI `/v1/audio/speech`, `GET /speech?t=<sealed ticket>`.** Four
gates, all of which must hold, and each answers with a different status because
they are different problems:

| Gate | Failure | Why that code |
|---|---|---|
| Authenticated | `401`, or `403` for a ticket that will not open | Article text is private. `403` because the caller *did* present a credential — `401` invites a retry with the login it already has, which loops |
| Item visible to this scope | `404` — never `403` | §20.7: a permission error confirms the item exists |
| Instance has a key (`OPENAI_API_KEY`) | `501` | The server is fine and simply has no key |
| **This user opted in** (`tts.smartPlus`, default off) | `403` | The one the reader can actually fix |

A plain HTTPS endpoint rather than an RPC, because the client is an `<audio>`
element: a URL lets the browser stream, buffer and cache it, none of which comes
free through a WebSocket and a blob.

**That same fact is why the ticket exists.** An `<audio src>` cannot send an
`Authorization` header, so the header path can only identify a caller through the
DevMode fallback — which made this whole feature work on a laptop and answer
`401` on every deployed instance. The URL therefore *is* the credential, as it is
for `/asset` (§10.1a) and `/p` (§10.1b). The difference is that those two mint an
identity-free capability over a public target, and this one has to carry a scope,
because reading an item needs one. So the payload is **sealed** with AES-256-GCM
under `speech.key` rather than signed in the clear: authenticated like a
signature, and opaque as well — a tenant and user id in an `<audio src>` would
land in browser history, in the referrer and in every access log between here and
the listener. It is minted by `GetItem` onto `Item.speech_url`, expires in six
hours, and names its own item, so a valid ticket cannot be paired with someone
else's id.

**One article is paid for once**, and three things hold that up: the disk cache
by `(item, model, voice)` never expires; concurrent requests for the same
artifact collapse onto a single paid call, which is what stops a second press of
play during the tens of seconds a real article takes from starting a second
synthesis; and a synthesis already paid for finishes onto the cache even if the
reader who started it navigates away. No TTL anywhere in that, deliberately —
article text is immutable, so an expiry could only ever be a schedule for
re-buying identical audio.

The provider's error text is **capped, not suppressed** — trimmed to 300 bytes and
returned inside the Go error, in both `internal/tts` and `internal/llm`. *(Corrected
2026-07-27: this section said "logged and never returned", and the code has never
done that. Both packages cap and return, each with a comment saying so. The plan was
wrong, so the plan changed.)*

The reasoning behind capping rather than suppressing is that a provider failure a
reader cannot see anything about is a failure nobody can act on — "it didn't work" is
not a bug report. The reasoning behind the cap is the original concern, unchanged:
provider errors can echo request content, and request content here is the user's own
article (§22.11). A cap bounds how much of it can come back; it does not eliminate
the path. Two things follow that are worth being explicit about rather than assuming:
the text reaches the **reader's own** error surface, where it is their own article and
not a disclosure, and it must not be written to a shared log at a level that outlives
the request.

**Listening to a list.** Three preferences, each default off, each a separate
decision:

| Preference | What it changes | Why it is its own switch |
|---|---|---|
| `tts.smartPlus` | Who speaks — OpenAI instead of the browser | The egress boundary |
| `tts.digest` | What is spoken — ~1 minute of summary instead of the article | A *second* egress and a second bill. Consent to being read aloud is not consent to being read and rewritten |
| `tts.podcast` | What is spoken — the article as one slot of a continuous broadcast, opening by handing over from the story just played (§19) | A *third* egress. It **outranks** `tts.digest` rather than combining with it: both replace the article text, and there is no coherent "summary of a broadcast segment" |
| `tts.autoplay` | What happens when it ends — the next article, until the list runs out | Changes nothing about this article, so it is the one toggle that does not interrupt playback |

The digest is written to be HEARD, which is a different job from being
shortened: prose written for the eye leans on paragraph breaks, subheadings and
the ability to skim back a line, none of which survive a synthesiser. So the
prompt is mostly prohibitions, and `cleanForSpeech` strips the markdown it was
told not to emit anyway — a stray asterisk is not a cosmetic problem, it is the
word "asterisk" in the middle of a sentence, cached as audio.

**Broadcast segments (`internal/smart/podcast.go`)** solve the *other* half of
listening to a list, and it is the half the digest never touched. Six digests
played back to back are six essays with a hard cut between each: no handover, no
sense of an order, nothing at the seam to say one thing ended and another began.
Every bulletin ever made fixes that with a sentence or two of connective tissue,
and it is the whole difference between a playlist and a programme.

So a segment is **that article's slot in a running broadcast**: it is given the
source and headline of the story just played — not its text, which is what keeps
a two-hour session costing exactly what a two-hour queue of digests costs — and
opens by handing over from it, naming the real relation where there is one.

Two consequences worth stating because they are what a reader would otherwise
report as bugs:

- **The unit of caching is the ORDERED PAIR**, not the article, in both the text
  cache (`podcast-cache/`) and the audio cache (`item#podcast:prev`). The same
  story after a different story is a different recording. Sharing a key would
  have the narrator hand over from something the listener never heard — which
  does not sound like a bug, it sounds like the narrator misremembering.
- **The predecessor travels as `&p=<item id>` beside the sealed ticket**, not
  inside it. The ticket is minted by `GetItem`, long before anyone knows what
  this will be played after; the order is the client's, decided at play time, and
  it differs between two listens of the same feed. It is safe to be
  caller-supplied because the server resolves it through the *same scope* as the
  item being spoken, so the worst a forged value achieves is a handover from an
  article the reader could already read.

The prompt's prohibitions are the feature, and each names something a model asked
for "a podcast segment" produces unprompted and audibly: an invented show name, a
"welcome back" to a listener who has not been anywhere, a sign-off at the end of
every segment so a forty-minute session ends forty times, and — the damaging one
— "coming up next", followed by a story the model has never seen.

Audio is keyed by `(item, mode, model, voice)`. The mode is not optional: without
it, turning the digest on serves yesterday's full-article audio and turning it
off serves the digest, each looking exactly like the toggle not working.

Continuous play marks each article read as it finishes — hearing it out is the
same claim scrolling to the last line already makes — and prefetches the next
track during the current one, which is the difference between a session and
forty seconds of silence at every seam. Every segment opens `From {source}.
{title}.`: a queue with no announcement tells a listener what a piece is called
but not where it came from, and that is most of how anyone decides whether to
keep listening.

**The floating transport.** `listenBar` lives at the head of the article because
listening is a decision made before reading, and because a floating player covers
the text it is reading. That rule is not overturned by `nowPlaying` — it is
completed. The floating bar appears only once the in-article control has left the
viewport, so by construction it never covers the article it is reading; it covers
whatever you scrolled away to, at the moment you have no other way to stop it. It
carries the one thing the in-article control never needs — which article is
talking — and the title itself is the control that takes you back.

The Smart+ toggle sits **next to the play button**, not in settings. It is an
egress decision, and the reader should be able to see its state at the moment
they press play.

---

## 11. Finding the feed: deterministic first, model last

**The LLM proposes, the parser disposes** — every candidate is fetched and parsed before it's offered.

Rungs: (1) `<link rel="alternate">` ~60–70% · (2) path probes ~20% more · (3) platform rules (YouTube,
Reddit, GitHub, Substack, Mastodon) · (4) **LLM proposer** for the tail · (5) **no feed → offer a
scrape rule** (§14.2) · (6) **still nothing → offer newsletter subscription** (§14.1).

### 11.1 As built — 2026-07-26

**The provider is OpenAI, not Anthropic.** Rev 8 specified `claude-opus-5` via `anthropic-sdk-go`
here, and the code went the other way on purpose: `internal/llm` is **the one egress boundary** — one
endpoint to audit, one key in Settings, one budget meter, one breaker — and a second SDK would have
meant a second of each, plus a second key an operator has to know to configure before the feature
works. The plan is corrected rather than the code. If a second provider is ever wanted, it belongs
behind `internal/llm`, not beside it.

| Rung | Built? | Where |
|---|---|---|
| 1 `<link rel="alternate">` | ✅ | `internal/discover` — candidates are FETCHED AND PARSED; a declaration pointing at a 404 is never offered |
| 2 path probes | ✅ | six paths, the typed directory before the site root, and only when the page declared nothing |
| 3 platform rules | — | YouTube/Reddit/Substack shapes are still owed |
| 4 LLM feed proposer | — | the tail; rung 5 turned out to cover most of what it was for |
| 5 **scrape rule from the page** | ✅ | `internal/smart` proposes, `internal/scrapesel` disposes (§14.2, §11.2) |
| 6 newsletter | — | §14.1, M22 |

### 11.2 Rung 5: the model writes a scrape rule

`smart.SiteAnalyzer.Propose` sends a **distilled outline** of the page — tags, ids, classes, a few
attributes, repeated siblings collapsed to `… x N more` — and never the page. On a typical blog index
that is 6–12 KB against 300–800 KB of HTML, and the collapse is not only a size trick: the repeated
block IS the answer to "where is the list", written down.

**Nothing the model says is trusted.** The proposed selectors are compiled and run against the page
before the reader sees anything, and the answer is refused when: nothing matches, no container yields
a link, only one item comes out (the selector found the hero post, not the list), or every item shares
a title (the title selector reached outside the container). One retry, given the specific failure as
input. Two attempts, then `ErrNoRule` — a third is spending someone's money on the same guess.

The reader is shown the extracted items, the count, the model's one-line note, and the rule itself.
There is deliberately **no confidence score**: a number a model assigns to its own answer is not
evidence, and five real headlines pulled off the page are.

**Consent is two conditions, both checked at the RPC** (§18.8): `smart.subscribe` is on for this user
(default off, its own key rather than sharing `tts.smartPlus` — different content, different
decision), *and* the request carried the flag the button sets. Neither implies the other.

**robots.txt is checked before the model, not after** — asking permission after spending the request
is asking rhetorically. Fetch failures mean allowed; a missing robots.txt is the common case.

### 11.2a The selector language, and what a rule looks like

A rule is eight fields. `item_selector` finds the CONTAINER of one entry; everything else is
evaluated **inside** a container match, so a selector that reaches the page header is a bug the
extractor cannot see.

| Field | Required | What it holds |
|---|---|---|
| `item_selector` | ✅ | the repeated container — `article.post`, `ul#_listUl li` |
| `title_selector` | ✅ | the entry's own heading, inside the container |
| `link_selector` | ✅ | **must end in `@href`** |
| `date_selector` · `date_layout` | — | see below; getting this wrong is the silent failure |
| `summary_selector` · `image_selector` · `author_selector` | — | `""` when the page does not carry one |

**`@attr` is ours, not CSS.** `cascadia` matches selectors against an `x/net/html` tree and has no
concept of reading an attribute, so `internal/attrsel` splits on `@`: `h2 a@href` is "run `h2 a`,
then read `href`". Without the suffix you get the element's text.

**Dates are the field most often got wrong and the only one that fails silently.** An unparseable
date becomes "now" at first ingest, which is stable (published_at is never rewritten) but puts a
five-year-old entry at the top of the reader. Prefer an attribute — `time@datetime` — and leave
`date_layout` empty. For human text, set both, with the layout written against Go's reference date:

```
"Jun 1, 2026"       → Jan 2, 2006
"01/06/2026"        → 02/01/2006
"2026-06-01 14:03"  → 2006-01-02 15:04
```

A relative date ("3 days ago") has no layout. The prompt tells the model to leave both fields empty
rather than guess, because first-seen is a better wrong answer than a parsed-wrong one.

**Worked example — a chapter list** (verified 2026-07-27 with `pagescan`, 9 of 9 extracted):

```
url    https://www.webtoons.com/en/action/omniscient-reader/list?title_no=2154
item   ul#_listUl li._episodeItem
title  span.subj
link   a.detail_list_link@href
date   span.date          layout: Jan 2, 2006
```

The outline that produced it shows why: `ul#_listUl.detail_list` with `li#episode_309._episodeItem`
followed by `… x 8 more li._episodeItem`. Note the trap the prompt now names explicitly — the id on
the `li` encodes ONE episode, so a rule built on `li#episode_309` matches one row today and none
tomorrow. The class, or the list container plus a child, is what generalises.

### 11.2b The counterexample: pages with nothing in them

`https://hni-scantrad.net/comics/hajime-no-ippo` is 7.5 KB of HTML containing a navbar, a logo, the
word "Loading", and `<router-view>`. The chapters are fetched by the site's Vue app from its own JSON
API after the page loads. **No selector can find them, because they are not there.**

This is detected before the model is called (`smart.ClientRendered`) and reported as its own status,
`js_rendered`, rather than as a failure to retry. Two reasons, and the second is the important one:

1. It costs nothing to notice, and a model call to be told the same thing costs money.
2. **A model handed an app shell does not come back empty-handed.** It finds the most list-shaped
   thing in the markup — the navigation — and proposes selectors for it. Those compile, they match,
   and they produce a "feed" of menu items. The validation gate catches most of that, but "we
   refused its answer" and "there was never an answer" are different facts and only the second has a
   remedy a reader can act on.

The detector is deliberately conservative: an app-shell mount point (`#app`, `#root`, `#__next`,
`<router-view>`, `data-reactroot`) **and** a body carrying under 800 characters of text. A
server-rendered page inside `#__next` has its text and is not flagged — that case has a test, because
flagging it would refuse most of the modern web.

### 11.2b(i) …and what to do about them: follow the data, not the page

The entries are not missing. They are one request away, and that request is a plain GET returning
plain JSON to the same origin — so the answer for a client-rendered page is not "no", it is a second
dialect of rule. `internal/jsonsel` is `scrapesel`'s sibling: dotted paths instead of CSS selectors,
the same Item out the other end, so ingest, dedup, health, ranking and search cannot tell which
extractor produced a source.

**The rule that follows the site above** (verified end to end on 2026-07-27 — 170 chapters
subscribed, stored with real titles and dates, second poll added nothing):

```
page   https://hni-scantrad.net/comics/hajime-no-ippo
data   https://hni-scantrad.net/api/comics/hajime-no-ippo   ← found by probing /api + the page path
items  comic.chapters          (170 entries)
title  full_title              "Round1515: Loneliness at the Top"
link   url                     "/read/hajime-no-ippo/en/ch/1515" → resolved against the PAGE
date   published_on            ISO 8601
id     slug_lang_vol_ch_sub    "en-N-1515-N"
```

**Verified with the model in the loop, 2026-07-27.** Asked for nothing but the response's shape,
`gpt-5-mini` proposed exactly the rule above — `comic.chapters` / `full_title` / `url` /
`published_on` / `slug_lang_vol_ch_sub` — and its note said why: *"it lists individual chapter
releases"*. The chain end to end (`internal/reader`, `TestLiveEndToEndWithTheModel`): no feed found →
app shell recognised → data endpoint discovered → shape sent → paths returned → 170 chapters
extracted and stored → second poll adds nothing. Both live tests are skipped unless `AF_LIVE=1` and a
key is set; nothing in CI touches a stranger's server or spends money.

**Two things that cost a wasted call to learn**, written down so they are not learned twice:

- **`max_output_tokens` covers the REASONING on a reasoning model.** The first attempt bounded the
  answer at 1200 — fifty times what eight paths and a sentence need — and came back `ErrTruncated`
  having spent the budget thinking and emitted nothing. That failure is indistinguishable from a bad
  prompt unless you know to look. It is 6000 now.
- **Effort belongs low here.** The question is structural and the evidence is in front of the model;
  deliberation buys nothing and is charged by the token. `llm.Request.Effort` was added for it.

**Discovery is four GETs, not a crawl**: `/api` + the page's path, the path + `.json`, `/api/v1` +
the path, and the section index. Each candidate is fetched, parsed, and rejected unless it contains
an array of at least two objects — the same "the parser disposes" rule the feed rungs follow. The
longest array of objects is passed to the model as a *hint*, not as the answer: a response carrying a
list of entries also carries arrays of genres, teams and related titles, and the field names are what
settle it.

**Three things are enforced rather than assumed**, because this rule holds an address the server will
fetch hourly forever:

- **Same site.** `SubscribeJSON` refuses a data address on another host. Without that the RPC is an
  open proxy a client can point anywhere.
- **robots.txt**, checked against the data address, on subscribe and on every poll.
- **A stable id when the response has one** (`id_selector`). It is what "have I seen this?" reads, so
  an API that renumbers its URLs cannot re-deliver its whole archive as new.

**Why not render the page instead.** `chromedp` is already in the module graph, so it is a small step
technically and a large one otherwise: executing a stranger's JavaScript server-side bypasses every
SSRF rule this application has, because netguard lives in Go's transport and a browser's network
stack does not consult it. That reopens §21's hole for every scraped source, on a self-hosted box, to
obtain an array that one GET already returns — with the types (`chapter` as a number, `published_on`
as a timestamp) that rendering would have thrown away. Headless stays available as a later rung
behind an operator's explicit opt-in, for sites that have no API at all.

**Still owed**: §14.2's `url_template` forward-probing, GraphQL endpoints, and APIs behind
authentication.

### 11.2c pagescan: the bench

`go run ./internal/tools/pagescan <url>` prints what the model would receive — the distilled outline,
verbatim — and, with `-rule 'item|title|link|date|summary|image'`, runs a rule against the live page
and prints what it extracted. `-feeds` climbs rungs 1-3.

It exists because two failures look identical from the outside: a proposal that missed the list
because the outline never showed it, and a proposal that read fine and extracts nothing. Without a
way to see the outline the temptation is to reword the prompt until something works, which is how a
prompt turns into superstition. It fetches over the network, which is why it is a tool and not a
test — nothing in CI should depend on a stranger's homepage.

### 11.3 What a scraped source costs

Polled on a **one-hour floor** (`reader.ScrapeInterval`), and full text is fetched for **new items
only, at most five per poll** (`reader.MaxFullTextPerPoll`). §14.2 says "one request per poll, no
crawling"; this is the exception, with a number rather than a silent one — without it a scraped item
is a headline and a link, which is a bookmark list rather than a reader. `store.KnownGUIDs` exists for
exactly this: "new" has to be decided before ingest, because after it everything looks equally
present.

A rule that matches nothing increments `scrape_rules.empty_polls` and writes the reason onto the
source's health, which is what makes a redesign distinguishable from a site that went quiet.

---

## 12. Offline — trip packs

### 12.1 Depth and scope

| Depth | Contains | Rough size / 100 items |
|---|---|---|
| `meta` | title, summary, metadata | ~200 KB |
| `text` | + extracted article HTML (tier 1) | ~3 MB |
| `media` | + images, downscaled server-side | ~30 MB |
| `audio` | + generated TTS / enclosures | ~50 MB |
| `full` | + tier-2 page snapshot, rendered (2r) where the origin needs JS · assets already local via §10.1a | ~100 MB+ |

Per subscription: last N items and/or N days, `inherit` by default, plus a **global MB cap** with
eviction — read-and-old first, **never** an item with an unsynced outbox entry.

### 12.2 Why not client SQLite (preserving A4)

Offline reading needs blob-by-key retrieval and one small index, not SQL. **IndexedDB through
`interop.PersistentStore` is sufficient**, keeping wazero + SQLite (~2.5 MB gzip) out of the bundle.

### 12.3 The Service Worker problem, stated plainly

**A Service Worker intercepts `fetch`. It cannot see WebSocket frames.** Because A5 put every RPC on a
tunnel, the standard PWA recipe does not apply. So: **app shell over plain HTTPS, SW-cached** (without
it the app can't boot on a plane) · **packs over plain HTTPS, not gRPC** — `BuildPack` materializes
server-side and returns an ID; `GET /pack/:id` is one authenticated, resumable, compressed response ·
**client store** in IndexedDB · **read path falls back** to the mirror with an unmistakable badge.
`BuildPack` streams progress.

### 12.4 Writes offline — and A25

Every offline mutation appends to an **IndexedDB outbox** with a client-generated **idempotency key**.

**Ordering authority is the server's `rev`, never the client's wall clock (A25).** Rev 7 said
"last-writer-wins by timestamp" without saying whose clock — and a phone that has been offline for a
week on a plane, with drifted time, would silently overwrite genuinely newer server state. Each mutation
carries the `rev` it observed; the server applies it only if `rev` still matches, and otherwise returns
the current row for the client to re-resolve. Client timestamps are recorded for display, never for
ordering.

- `is_read` / `is_favorite` / tags → server-`rev` compare-and-set; a lost race is invisible and fine.
- **Notes are never overwritten.** Divergence keeps both, writes `outbox_conflicts`, and prompts.

### 12.5 Multiple tabs and devices

**Two tabs of the same app share one IndexedDB.** This exact pattern produced a whole-dataset
last-writer-wins clobber in CashFlux — same stack, same persistence layer — and ArticleFlux' outbox is
strictly more complex.

Mitigation, in the design rather than discovered later: **`BroadcastChannel` leader election** — one
tab owns the tunnel, the outbox drain, and mirror writes; others proxy through it and render from
shared state. Plus a **generation stamp** on the mirror so a non-leader write is rejected rather than
merged. Cross-*device* divergence is handled by A25's `rev` on the server.

### 12.6 Automatic vs manual

A **"prepare for trip"** button (pick views, see estimated size, watch progress) and a **background
policy** ("keep the last 50 unread at `text` depth"). Request `navigator.storage.persist()` and
**degrade honestly if denied** rather than pretending the data is safe.

---

## 13. Rules, filters, and mute (A19)

### 13.1 Shape

**Match** — `{all|any: [{field, op, value}]}` over `title`, `author`, `content`, `url`, `source`,
`folder`, `tag`, `word_count`, `age`, `lang`. Ops: `contains`, `not_contains`, `equals`,
`starts_with`, `regex`, `gt`, `lt`.

**Actions** — `mark_read` · `star` · `tag <name>` · **`mute`** · `set_home_weight` ·
`move_to_folder` · `stop_processing` — **all available at M6**. `notify` and `webhook` are
**schema-reserved but UI-gated** until `NotifyService` (M12) and `WebhookService` (M22) exist; rev 7
listed them as if the whole set shipped at M6, which it can't.

Rules are ordered, all-match by default, with an explicit `stop_processing`. Every fire writes a
`rule_hits` row — audit trail and the basis for undo.

### 13.2 Where they run — the consequence of A14

**Rules are per-user; `items` are global.** So rules evaluate **per subscriber**, not once at ingest.
Backwards, one user's mute filter hides an item from every other subscriber — a silent cross-user bug.

**But not inline with the poll.** Fan-out is `O(new_items × subscribers)`; a widely-shared source
returning 50 items to 200 subscribers is 10,000 evaluations blocking the fetch loop. **Fan-out is a
queued background job** (§22.7) with per-source batching and backpressure. The same job applies
`notify_mode` and feeds the ranking pipeline — one pass, not three — and its lag is visible in §9.

### 13.3 Mute is not delete

A muted item is **stored, flagged, and excluded from lists and counts** — not dropped. Unmuting has to
work, an over-broad rule must be recoverable, and a `MUTED` view shows what a rule is actually eating.
Rules report hit counts so you can tell a precise filter from a bulldozer.

### 13.4 Authoring

**Preview against the last N items before saving** — a streaming RPC showing exactly what would match.
A rule you can't dry-run is a rule you're afraid to write. Optional **retroactive apply** with a count,
a confirmation, and undo via `rule_hits`. Claude can draft a rule from plain English; the preview
proves it; you confirm.

### 13.5 Rules rot

`last_matched_at` and `match_count` let the UI say **"this rule hasn't matched in 6 months"** — the
rules equivalent of inactive-feed detection. Dead rules are how filter sets become untrustworthy.

---

## 14. Sources beyond RSS

### 14.1 Newsletters via IMAP (A20)

**IMAP polling, not an SMTP listener.** Running SMTP means MX records, port 25, reverse DNS,
SPF/DKIM, and a spam pipeline. Polling a mailbox you already own needs **no inbound port, no DNS, no
spam stack**, and a mailbox is just another `sources.kind`.

- `mailboxes(user_id, host, port, username, secret_enc, folder, use_plus_addressing, last_uid, …)`.
- **Each sender becomes a per-user source**, keyed `mailbox:<mailbox_id>:<sender>` (§6.4). **Never
  global** — that would merge two people's private mail.
- MIME → prefer `text/html`, fall back to `text/plain` → **sanitize hardest in the app**: newsletters
  are the most tracker-dense HTML on the internet. Strip pixels, proxy images, kill remote CSS.
- Credentials encrypted at rest; app-specific passwords strongly preferred.

### 14.2 Scraped pages

CSS-selector rules against an index page, landing in `items` under `sources.kind='scraped'` — so lists,
search, notes, favorites, rules, ranking, and events need no changes.

```
index_url: https://example.com/blog    item_selector: article.post
title_selector: h2 a                   link_selector: h2 a@href
date_selector: time@datetime           summary_selector: p.excerpt
```

**The `@attr` suffix is our own mini-DSL, not CSS.** `andybalholm/cascadia` matches selectors against
`x/net/html` trees and has no attribute-extraction syntax — so `internal/scrape` owns a small parser
that splits on `@`, runs cascadia on the selector half, and reads the named attribute off matched
nodes. Rev 7 implied the library provided this; it doesn't.

A **URL-template** variant (`/{yyyy}/{mm}/{dd}/`, `/issue/{n}`) probes forward from the last known
value and catches JavaScript-rendered index pages selectors can't touch.

**Guardrails** — an impolite scraper gets the server IP banned, taking down *every tenant*: honor
`robots.txt` · **1-hour minimum interval** · honest UA with a contact URL · absolute `Retry-After` ·
one request per poll, no crawling · `RULE_BROKEN` when a rule yields zero items for N consecutive
polls.

---

## 15. Interoperability

### 15.1 The sync API (A18)

FreshRSS, miniflux, and Tiny Tiny RSS all expose a **Google Reader–compatible** API, and that is how
they get first-class mobile apps without writing any: **Reeder, Unread, Fiery Feeds, NetNewsWire, and
FeedMe all speak it.**

- **Surface:** `/reader/api/0/*` — subscription list, stream contents, item contents, edit-tag,
  mark-all-as-read, tag list, continuation-token paging.
- **A second transport, deliberately** — plain REST, not the tunnel. Second such exception after
  offline packs, same reason: the client isn't ours.
- **It maps onto our schema almost exactly, which is not a coincidence** — GReader's model is
  streams + labels-on-items + per-user read state, which is `sources`/`subscriptions`, `item_tags`,
  and `user_item_state`. That's *why* A21 is a prerequisite.
- **Mutations go through the same service layer** as the UI, so rules, ranking signals, and events all
  fire identically. A read marked in Reeder must move the homepage and reach an open browser tab.

### 15.2 Token scopes — capped, not inherited

`api_tokens.scope` is a **fixed enum**, not free text: `reader_ro` (read + own state) or `reader_rw`
(+ subscribe/unsubscribe, tags). Each maps to a **hard-capped capability subset enforced at the
interceptor**, independent of the minting user's role.

This matters: rev 7's free-text `scopes TEXT DEFAULT 'reader'` meant an admin minting a token for a
phone app would hand that app `tenant.settings.write`, `user.role.set`, and `system.impersonate`. A
long-lived token pasted into a third-party client is the worst possible place for admin capabilities.
No token can ever carry an admin or system capability, whatever its owner's role.

Tokens are hashed at rest, per-client labelled ("Reeder on iPhone"), listed with last-used,
individually revocable, and rate-limited per token.

**Fever API** is simpler and older; client support has moved to GReader. Deferred, not rejected.

### 15.3 Feed formats, namespaces, and cross-source duplicates

**Formats:** RSS 0.90 / 0.91 / 0.92 / 0.93 / 0.94 / **1.0 (RDF)** / **2.0**; Atom **0.3** and **1.0**;
JSON Feed **1.0** / **1.1**. *(Whether `gofeed` distinguishes the two 0.91 dialects is unverified —
confirm at M1 rather than asserting it.)*

**Namespaces:** `content:encoded` (**beats `description`**) · `dc:creator`/`dc:date` ·
`sy:updatePeriod`/`updateFrequency` · `media:thumbnail`/`media:content` · `itunes:*` ·
`atom:link rel=self|hub|next` · `slash:comments` · `<enclosure>`.

**Cross-source duplicates.** `UNIQUE(source_id, guid)` dedups *within* a source only, so a syndicated
story in two subscribed feeds appears twice in the plain unread list. §18's clustering handles this for
*ranking* but not for reading. So: compute `items.dupe_key` at ingest — normalized URL after stripping
tracking params, falling back to a normalized-title + published-day hash — and **visually group
duplicates in list views** ("also in 2 other feeds"), collapsing to the first-seen copy. Grouping, not
merging: the rows stay distinct so per-source state and unsubscribing stay correct.

### 15.4 Redirects and moved feeds

Rev 7 covered conditional GET and `Retry-After` but not permanent redirects. **On a 301, update
`sources.url` to the target**, record `redirected_from`, and surface "source moved" in feed settings.
Following a redirect chain forever without updating is not just wasteful — if the old URL later expires
and is re-registered by someone else (a known RSS failure mode), an unchanged `sources.url` becomes a
path for a stranger to publish into a feed people trust. Cap chains at 5 hops, re-run the SSRF guard on
every hop, and stop updating after `redirect_count` exceeds a threshold.

### 15.5 The backlog-flood guard

If a publisher's CMS migration changes GUID format, every historical item looks new to
`UNIQUE(source_id, guid)`. On a global source with many subscribers that floods **every** subscriber's
unread count and — worse — fires `notify` rules and push digests for years-old content, for everyone,
at once.

**Guard:** compare a poll's new-item count against `median_items_per_poll`. Beyond a multiple (say 10×,
and more than N absolute), **ingest the items but suppress fan-out** — no notifications, no unread
badge spike — mark the source `FLOOD_SUSPECTED`, and surface it in the admin console and the owner's
feed settings for one-click "accept" or "discard." Distinct from RFC 5005 backfill, which is an
intentional, expected first-subscribe action.

### 15.6 Protocol standards

Conditional GET (RFC 7232) · **`Retry-After` honored absolutely** · **RFC 5005 Atom paging** — follow
`rel="prev-archive"` on first subscribe to **backfill history**, which also gives the ranker something
to learn from on day one · **WebSub** — subscribe, verify the challenge, **validate the HMAC**, renew
before `lease_seconds`, fall back to polling on failure.

### 15.7 Import / export

| Data | Import | Export |
|---|---|---|
| Feeds + folders | **OPML 2.0** (nested) | **OPML 2.0** (nested) |
| Bookmarks | **Netscape HTML**, Chrome JSON, Pinboard JSON | **Netscape HTML** + JSON |
| Items + tags + state | GReader/FreshRSS, Feedly/Inoreader JSON | Full JSON |
| Notes | — | Markdown bundle with source links |
| Rules, scrape rules | JSON | JSON |
| Everything | — | Full JSON (incl. engagements) |
| The database | — | **`VACUUM INTO` snapshot** (§22.5), not a file copy |

**Import must be idempotent** — re-importing updates, never duplicates, keyed on `url_norm` / feed URL.

---

## 16. Trends, stats, and feed health

**We already collect every signal this needs** in `engagements` — the page is close to free.

### 16.1 Reading trends

Items read over time · **time-of-day heatmap** · top sources by open rate, items
read, time spent · most-starred/noted/tagged · unread backlog growth · estimated reading time · streaks.

### 16.2 Per source

Items/week, open rate, median dwell, star and note rate, first and last engagement, and
**where it ranks against your other feeds** — the screen that tells you a feed you *feel* loyal to is
one you actually skip.

### 16.3 Feed health — the actionable half

**Inactive sources** ("no items in 90 days", one-click
unsubscribe) · **failing sources** (sorted worst-first, shared with §9's leaderboard) ·
**never-opened sources** (subscribed, delivering, never once opened — the least flattering list in the
app) · **noisy sources** (high volume, low engagement) · `hide_from_counts` for feeds you skim.

All of it one-click actionable — a stats page you can't act on is a poster. **It also drives feature
discovery** (§20.5): the noisy-sources list is where "mute this?" should be offered, since a command
palette only helps people who already know a feature exists.

---

## 17. Notifications and outbound

### 17.1 Web Push

**Web Push (VAPID) is free and needs no service**, and the Service Worker already exists for offline.

Per-subscription `notify_mode`: `none` | `new` | `digest`, plus rule-driven `notify`. **Digest by
default** — per-item push on a busy reader is unusable within a day. **Quiet hours** are stored as
local wall-clock + IANA timezone, not a baked UTC offset (§22.9). `push_subscriptions` per device;
`notification_log` for dedup and for "why did I get this?" Suppressed entirely for a source in
`FLOOD_SUSPECTED` (§15.5).

### 17.2 Outbound webhooks and send-to

One mechanism covers the whole integrations category: an **HMAC-signed outbound webhook**, fired
manually or by a rule. Presets for Slack, Discord, Obsidian Local REST, generic JSON. Send-to-Kindle
needs SMTP and is gated on §7.2 rung 3.

**An outbound webhook to a user-supplied URL is the same SSRF-class hole as feed fetching** — same
guard, optional per-tenant allowlist. A webhook that can reach `169.254.169.254` is a
credential-exfiltration primitive.

---

## 18. The interest layer: ranking, highlights, recommendation

One model, three jobs: rank your feeds (§18.4), pull the two good items out of a firehose you'd never
read in full (§18.5), and find sites you don't know about yet (§18.7).

**Smart** is deterministic, free, always on, zero egress. **Smart+** is opt-in and re-ranks Smart's
output — never replaces it.

### 18.1 Signals, and the negative ones that are easy to get wrong

Logged raw to `engagements`, derived on a schedule — *log raw events, derive scores*, or you can't
re-derive with a better formula later.

> **Implemented 2026-07-26.** The taxonomy below is no longer prose: `internal/signals` is the
> authority and this section describes it. A kind that is not in `signals.specs` is rejected at the
> service boundary, so the list here and the code cannot drift. **`internal/signals` wins** if they
> disagree — that is the one inversion of the usual precedence rule in this document, and it exists
> because a signal vocabulary split across a spec and a registry is a signal vocabulary that will be
> extended in one of them. See §18.1a for what shipped and what it corrected here.

**Positive, strongest first:** **liked** (A27 — an explicit verdict, and the only signal a reader
gives deliberately about quality) · **note written** (you stopped and typed — nothing else costs more
effort) · **bookmarked** · **tagged** · **completed** (scrolled to the end) · **long dwell** ·
**opened** · **opened soon after arrival**.

**Negative signals need more care than earlier revisions gave them**, because the obvious
implementation poisons the model:

| Event | Reading | Weight |
|---|---|---|
| **Opened, then left in under ~15s** | You looked and it wasn't for you | **Strong negative** — stronger than never opening, because it is an *informed* rejection |
| **Disliked** (A27) | A direct instruction, given after reading | **Strongest negative** |
| Explicit *less like this* | A direct instruction | Strongest negative |
| Muted by a rule | You legislated against it | Strong negative, attributed to **what the rule matched** (a source, a term), not to the item |
| Visible and passed over repeatedly | Soft rejection | Weak negative, and only when the row was genuinely on screen |
| **Marked read in bulk (`A`, mark-all-read)** | You gave up on a backlog | **Neutral. Never negative.** |
| Never delivered, never on screen | No information at all | Nothing |

**That fifth row is load-bearing.** The naive implementation reads one `mark all read` as 143
rejections and destroys weeks of signal in a keystroke. Bulk reads carry a distinct `engagements.kind`
and are excluded from affinity entirely — as are reads arriving from a sync client that auto-marks on
scroll, which is why the API records the *reason* for a state change and not only the state.

**A27 — the verdict replaces the star in the UI.** `user_item_state.rating ∈ {-1,0,+1}`, one signed
column rather than two booleans because like and dislike are mutually exclusive by definition and a
pair of flags makes "liked AND disliked" representable — a state every consumer then needs a policy
for. Setting the verdict an item already has clears it, because the only other way back from a
mis-click is to assert the opposite, which is a lie about what you think. The column is indexed
partially (`WHERE rating != 0`): verdicts are sparse by nature, so the index stays small enough to
live in cache while still serving the Liked and Disliked streams.

The *negative* half is the reason this exists. Starring answers "keep this" and a reader who never
stars still has opinions; knowing which sources reliably waste ten minutes is the signal §18.4 most
needs, and before A27 there was nowhere to record it.

**Impressions are recorded, not just actions.** Without knowing an item was on screen and passed over,
you cannot tell disinterest from never having seen it, and every rate you compute is against a
denominator you invented. The list emits a coalesced impression event for rows visible longer than
~1s. This is the cheapest thing in the whole layer and it makes every other number honest.

### 18.1a What shipped, and the three things rev 8 had wrong

Built 2026-07-26: `internal/signals` (vocabulary, pure) · `migrations/0007_engagements.sql` ·
`store.RecordEngagements` / `ItemSignals` / `FeedSignals` / `EngagementsSince` ·
`ReaderService.RecordEngagements` · `client/platform` observers · `client/track` (the outbox).

**Nine passive signals rev 8 did not have.** Each is either free or nearly so, and each observes
something none of the others can:

| Kind | What it sees that nothing else does |
|---|---|
| `searched` + `search_opened` | A term the reader **volunteered**. No inference at all, and the cheapest high-quality signal in the app — FTS5 search already shipped |
| `chose` | Opening row 7 of 12 rejects the other 11 **simultaneously**. Carries `{"pos","of"}` because without position the model learns "he likes whatever the ranker put first" |
| `reread` | Scrolled back **up** to re-read a paragraph. Almost nothing instruments it; it is one of the strongest positives a reader emits without meaning to |
| `clicked_out` | Followed through to the publisher: the excerpt was not enough |
| `selected` | Selected text — quoting, looking something up. **Length only, never content** |
| `listened` | Fraction of TTS heard. Immune to the backgrounded-tab problem that makes dwell fragile |
| `note_abandoned` | Started composing and left. Weaker than a note, stronger than nothing |
| `tagged` | Hand-applied labels are **supervised** data for §18.2's otherwise-unsupervised clustering |
| `sync_read` | A third-party client auto-marking on scroll. Neutral, for the same reason `bulk_read` is |

Time-of-day, day-of-week, session position and age-at-open needed **no new instrumentation** — they
are derivations over `at` and `session_id`, and age-at-open is where §18.4's per-source `half_life`
actually comes from, which rev 8 asked for without saying how to compute.

**Three corrections to this section, each found by trying to implement it:**

1. **"Opened, then left in under ~15s" is wrong as an absolute.** Fifteen seconds is most of a link
   post and a rounding error on a 4,000-word essay — the naive rule scores every longform piece as an
   informed rejection. Dwell is now normalised against **expected reading time** (`word_count`, which
   was already in the schema, at 238 wpm) and classified as a **ratio**: <0.25 bounce, <0.7 skim,
   else read. The spirit of the rule survives; the constant did not.
2. **`completed` is forgeable.** Flinging the scrollbar to the bottom reaches the end without reading
   a word, and rev 8 scored that as the second-strongest positive available. `signals.Pace` catches
   it: a completion at more than 3× a plausible reading speed is demoted to a skim.
3. **Dwell without an attention gate is not a measurement.** An article left open in a background tab
   overnight produces an eight-hour dwell, and one of those outweighs a month of honest reading in
   any averaging scheme. `platform.OnAttention` gates on visibility, window focus, and 60s of
   silence, and only attentive time is banked. The idle threshold is deliberately generous because
   the dangerous error is the opposite one: a reader absorbed in a long article emits no events for
   minutes, and an eager timeout would score deep reading as absence.

**A34 — the client outbox.** *(Numbered A33 when this section was written, which collided with §2's
A33 — the per-feed settings decision, cited in three places. The signals pair moved rather than the
one with more citations; a stable id that means two things is worse than a renumbering.)* Rev 8 said
signals are "logged raw to `engagements`" without saying how
they get there, and most of them are only observable in the client. There is now a batched,
coalescing, order-preserving outbox in `client/track`: it ships at 25 events or on a tick, keeps a
failed batch **in order** for the next attempt, bounds the backlog at 500 (dropping the *oldest*,
since recent signal is worth more), and flushes on `pagehide` **and** on a hidden `visibilitychange`,
because neither fires reliably alone. Event ids are client-generated and the server dedupes on the
primary key, so a retry after an unconfirmed batch cannot double-count. §6.5's `events` service is
server→client and is a different thing; this is the opposite direction.

**A35 — analytics may never degrade reading.** `RecordEngagements` is the one RPC whose failure the
client swallows: it does not touch the connection indicator, it has a short timeout, and every error
path drops data and continues. The worst acceptable outcome of a broken signals layer is a worse
ranking. It is never a page that will not load.

**Clock handling.** `engagements.at` is **client-observed** unix milliseconds, which looks like a
violation of A25 and is not: A25 forbids ordering *conflicting writes* on a client clock, and an
engagement is not a conflicting write — it is an observation only the client was present for, and
dwell and session structure are unreconstructible from the arrival time of a batch that came in four
minutes late over a reconnect. It is clamped server-side (`signals.Clamp`) against `recorded_at`:
more than 2 minutes fast collapses to now, more than 14 days old floors to the backlog limit. Both
columns are kept so skew stays measurable instead of being silently absorbed.

**Still not built:** the derivation job itself (`feed_affinity`, `term_affinity`, `domain_affinity`,
topics) — TODO 5.9 and 6.9. The log is being written now so that when they land there is something to
derive from; see R12, which this closes the front half of.

### 18.2 Topics, not one vector

**A single interest vector is the average of your interests, and matches none of them.** Someone
reading SQLite internals, NPU inference, and the Go runtime has a centroid sitting in empty space
between the three.

So cluster engaged items into **topics** — over embeddings when Smart+ is on, over TF-IDF term vectors
when it isn't. Each topic carries a centroid, top terms, a label (top terms deterministically; a
better one from an LLM if available), a member count, and a trend.

An item scores against its **nearest topic**, not the average. Three things follow that a single
vector cannot do:

- **Explainability gets specific** — "matches your *NPU inference* reading" instead of "matches your
  interests."
- **Diversity becomes measurable** — if 70% of your reading is one topic, the app can say so, and
  Explore can deliberately serve the starved ones instead of picking at random.
- **Recommendation gets a target** (§18.7) — you look for sites that match a *topic*, not a blur.

Topics are visible and editable in Trends: rename, merge, split, or mark one **not an interest**,
which applies a strong negative across its whole cluster. A model you can correct is a model you will
trust; one that just asserts things about you is one you will resent.

### 18.3 Drift — interests expire

Engagement weight decays exponentially with recency (half-life ~90 days, tunable), and each topic
carries a trend: **rising · steady · fading · dormant**.

This is what lets the app say the genuinely useful thing — *"you haven't read anything about
Kubernetes in five months, and three subscriptions only serve that topic"* — which is an unsubscribe
suggestion with a reason attached, and it feeds the dormant-source list in §16.3.

### 18.4 The scorer

```
score =  w_topic·TopicMatch(nearest)      + w_feed ·FeedAffinity
       + w_fresh·Decay(age, half_life(source)) + w_corr·Corroboration
       + w_manual·home_weight
       − w_vol  ·VolumePenalty            − w_dupe·SimilarToRecentlySeen
       − w_neg  ·NegativeAffinity
```

**`VolumePenalty` is the single most important term.** Without it the megafeed is "whoever posts
most," and a 50/day firehose drowns a weekly essayist. **Half-life is per-source**, derived from that
source's own engagement-vs-age curve: news decays in hours, an essay is fine three days later, and one
global half-life is wrong in both directions.

**Three slots:** **Top** (~70%) · **Explore** (~20% — pure affinity converges to a monoculture and
fails *invisibly*, because the page still looks full; Explore now targets **under-served topics**
rather than sampling at random) · **Clusters** (~10%, one card per corroborated story).

**Cold start:** recency + round-robin across sources, with an honest "learning your reading" state.
Topics need roughly 50–100 engaged items to mean anything; the app says so rather than presenting a
confident wrong answer.

### 18.5 Highlights mode — the good bits of a firehose

The problem: Hacker News posts 94 a day. You will never read it in full, but you don't want to miss
the two that matter. Muting loses them; subscribing normally drowns everything else.

So `subscriptions.home_mode` has three states rather than two:

| Mode | Behaviour | For |
|---|---|---|
| `full` | Every item is eligible for the homepage | Feeds you read most of |
| **`highlights`** | Only items above a per-feed score cutoff reach the homepage | Firehoses, aggregators, high-volume news |
| `muted` | Never on the homepage; still readable in its own view | Feeds you keep for reference |

**The threshold is expressed as a rate, not a score.** You set *"about 3 a week from Hacker News"* and
the system solves for the cutoff, re-fitting as the feed's volume changes. Nobody can reason about
"score > 0.62"; everybody can reason about three a week.

**Scoring differs in highlights mode, and this is the important part.** For a firehose, `FeedAffinity`
is meaningless — you don't like Hacker News, you like *specific things* on Hacker News. So in
`highlights` mode the feed term is dropped and the weight moves to:

- **Corroboration** — carried by other sources you follow
- **Domain affinity** — the item's *target domain* is one you engage with (§18.6), which is often a
  stronger signal than anything about the aggregator itself
- **Topic match** against your nearest topic
- **External signal** where the feed exposes it — `slash:comments`, points in the title, enclosure
  counts. Advisory only; popularity is not interest.

**The app proposes the mode.** A source at 94/day with a 3% open rate should be *offered* highlights
mode with a suggested rate, from the Trends noisy-sources list. That is the analytics layer paying for
itself in one interaction, and it's a far better answer than expecting someone to find a setting.

### 18.6 Domain affinity — a cheap signal nobody uses

Items have target domains, and aggregator items overwhelmingly point *elsewhere*. Track engagement per
**target domain** as well as per source: `domain_affinity(user_id, domain, opened, starred, noted,
dwell, last_at)`.

Two payoffs, both free:

1. **Better firehose filtering** (§18.5) — an HN item pointing at a domain you have starred four times
   is a strong candidate regardless of its title.
2. **Recommendation candidates** (§18.7) — a domain you keep engaging with *through* an aggregator,
   and don't subscribe to directly, is the single most obvious feed to suggest.

### 18.7 Recommending sites you don't follow yet

Same discipline as feed discovery (§11): **deterministic first, LLM last, always validated.** Every
candidate is fetched and parsed before it is ever shown — a recommendation that 404s teaches you not
to trust the feature.

| Rung | Signal | Cost | Quality |
|---|---|---|---|
| **1** | **Outbound link mining.** Extract links from the articles you actually read (`extracted_html` is already there), normalise to domains, weight by your engagement with the *linking* article. | a URL parse | **The best one.** "Simon Willison linked here 6 times in pieces you read." |
| **2** | **Aggregator pass-through** (§18.6). Domains you starred or noted that arrived via a firehose but that you don't subscribe to. | free | Excellent and immediately actionable |
| **3** | **Blogroll / OPML mining.** Fetch `/blogroll`, `/links`, `<link type="text/x-opml">` on sites you rate highly. | one fetch | Very good on the indie web, absent elsewhere |
| **4** | **Topic match.** Embed candidates from 1–3 and rank against your topic centroids (§18.2). | Smart+ | A *ranker* over harvested candidates, not a discoverer |
| **5** | **LLM + web search.** "Sites writing about SQLite internals and ARM inference, similar to X and Y." | one API call | The tail only, and every result validated |

**Rungs 1 and 2 do most of the work, cost nothing, and explain themselves** — which matters more here
than anywhere else in the app, because you are asking someone to add a subscription on your say-so.

**Candidate scoring** blends link frequency × engagement with the linking article, distinct referring
sources (three different writers linking somewhere beats one linking six times), topic match, and
**publishing health** — a candidate is rejected outright if it has no discoverable feed, hasn't posted
in 6 months, or posts more than ~20/day, because recommending a dead site or a firehose is worse than
recommending nothing.

**Every recommendation shows its evidence**, and the evidence is the whole product: *"3 writers you
read linked here 11 times · you starred 4 of its articles via Hacker News · matches your NPU
inference topic · posts ~2/week."* Dismissals are remembered per domain and never re-suggested.

**Trial subscriptions close the loop.** Accepting a recommendation subscribes it in a `trial` state
for two weeks, where items appear **only in Explore** so it can't flood anything. At the end the app
reports what actually happened — *"you opened 1 of 23 — drop it?"* — and defaults to dropping. This is
the analytics layer grading its own recommendation, which is the only honest way to run one.

**Guardrails.** Never recommend a domain already subscribed, dismissed, or muted. Skip pure
aggregators that would re-serve your existing feeds. **Deliberately include one or two adjacent-but-
different candidates** and label them as such, because a recommender optimised purely for similarity
is a filter-bubble engine with better manners.

### 18.8 Smart+ and the egress boundary

**(a) Embeddings** — term matching misses that "transformer inference latency" and "speeding up LLM
serving" are the same interest. Embed title + summary at ingest, cluster into topics (§18.2), rank by
cosine to the nearest. **256-dim vectors over a rolling 30-day window: 50k items × 256 × 4 bytes
≈ 51 MB**, and the live window is a fraction of that — brute-force cosine in Go scans it in
milliseconds, so **no vector database**. Clustering and duplicate detection come free from the same
vectors.

**(b) Top-N re-rank** — the top ~100 plus the derived interest profile, returning ~20 ordered with a
one-line reason each via structured output. One call per refresh: judgment, not similarity.

**The egress allowlist, unchanged and enforced.** Smart+ may send item **titles and summaries** for
candidates, plus a **derived profile**: topic labels, their top ~30 terms, and the top ~10 source
*titles* by affinity — bare strings, aggregated, never a log. It may **never** send notes, bookmarks,
full article text, raw engagement events, timestamps, per-item history, usernames, or anything
tenant-identifying. Site recommendation (§18.7 rung 5) sends **topic terms only** — never your
subscription list, never your reading history. This is a named exception in `internal/llm` with a test
asserting the outbound body matches an allowlist, not a comment expressing intent.

`openai-go` is already in GWC's `go.mod`. **Model IDs in config**, validated against the provider's
models endpoint on save. **Hard budget ceiling with a visible meter**, shared with translation.
**Fail soft always** — any error, timeout, missing key, exhausted budget, or open circuit falls back
to Smart. Smart+ must never produce an empty homepage.

### 18.9 Explainability is the product

Every ranked item shows why — *"you open 84% of this source"* · *"matches your NPU inference reading"*
· *"3 other feeds carried this"* · *"you starred 4 things from this domain."* An unexplained ranker
feels arbitrary and you stop trusting it, and **the reason is what makes feedback actionable**: *less
like this — because of the source* is a usable instruction where a bare thumbs-down is not.

Feedback surfaces: more/less like this · not interested in `<term>` · not interested in `<topic>` ·
mute this source on the homepage · switch this source to highlights. Plus a tuning panel showing every
weight with a live preview of the reordering before saving, and — the honest one — **a "what did you
hide from me?" view** listing what the ranker suppressed and why, because a filter you cannot audit is
one you cannot trust.

## 19. Screensaver / slideshow mode

Fullscreen headline slideshow from any `ViewSpec` (homepage ranking by default). One headline in large
type, source + favicon + relative time, optional `media:thumbnail` background with a scrim. Cross-fade;
Ken Burns drift **only when `prefers-reduced-motion` is unset**. Auto-advance (default 12s), `←`/`→`,
`space` pause, `Enter` opens, `Esc` exits. **Wake lock** via `navigator.wakeLock`, released on exit.
**Idle auto-start** after N minutes, off by default. **Works offline** from the trip pack — and it's
the most likely place you'd notice a broken offline path.

---

## 20. Client, contract, and UX

### 20.1 Services

> **As built, 2026-07-26.** Three services exist, not seventeen: **`AuthService`** (`Login` · `Logout`
> · `WhoAmI` — §7.1a), **`SystemService`** (version, stats, latency, log ring), and **`ReaderService`**,
> which currently carries what the eventual `FeedService` / `ItemService` / `TagService` /
> `NoteService` / `SettingsService` will own — feeds, items, state, search, prefs, tags, **folders**,
> notes, engagements and per-feed settings. The split below is the destination; collapsing it while the
> client is one screen keeps one interceptor and one error taxonomy instead of eight. Splitting is a
> `buf`-visible break, so it happens at a milestone boundary, deliberately, not by drift.

```protobuf
AuthService     Login · RefreshToken · Logout · ListDevices · RevokeDevice · ChangePassword
                SudoCheck · BeginRecovery · RedeemRecoveryCode · RedeemResetToken
                RegenerateRecoveryCodes · MintApiToken · ListApiTokens · RevokeApiToken
ProfileService  GetProfile · UpdateProfile · SetRecoveryEmail · VerifyRecoveryEmail      (§7.10)
TenantService   ListTenants · CreateTenant · UpdateTenant · SuspendTenant
                PreviewTenantDeletion · DeleteTenant · GetQuotaUsage                     (§9.1)
UserService     ListUsers · Invite · RedeemInvite · SetRoles · SuspendUser
                PreviewUserDeletion · DeleteUser · MintResetToken · ForceLogout          (§7.9)
AdminService    GetHealth · ListAudit · Impersonate · EndImpersonation · ListShares · RevokeShare
SettingsService GetResolved · Put · Reset · Describe(registry) · Export · Import
HomeService     GetHomeFeed · SubmitFeedback · GetTuning · PutTuning · RecomputeNow(stream)
                GetSuppressed                          -- "what did you hide from me?"  §18.9
TopicService    ListTopics · RenameTopic · MergeTopics · SplitTopic · SuppressTopic     §18.2
RecommendService ListRecommendations · DismissRecommendation · StartTrial · EndTrial
                RefreshCandidates(stream)                                               §18.7
FeedService     ListSubscriptions · Subscribe · Unsubscribe · UpdateSubscription · Refresh
                DiscoverFeeds(stream) · folders CRUD · ShareFolder · UnshareFolder
                AcceptFlood · DiscardFlood                                               (§15.5)
ItemService     ListItems(ViewSpec) · GetItem · MarkRead(scope, before_ts) · SetFavorite
                Search · RecordEngagement(batched) · GetRevisions · DiffRevisions
TagService      ListTags · CreateTag · RenameTag · DeleteTag · TagItems · UntagItems
RuleService     ListRules · PutRule · DeleteRule · ReorderRules · PreviewRule(stream)
                ApplyRetroactive(stream) · UndoRuleHits · ProposeRule(stream)
ScrapeService   GetRule · PutRule · PreviewRule(stream) · ProposeRule(stream)
MailboxService  ListMailboxes · PutMailbox · TestMailbox(stream) · DeleteMailbox
NoteService     GetNote · PutNote · DeleteNote · ListNotes · ExportNotes · ResolveConflict
BookmarkService list/get/save/update/delete · SetTags · CheckLinks · SaveFromItem
ShareService    CreatePublicShare · RotateSlug · ShareItem · UnshareItem · ListShared
RenderService   GetArticle · MintProxyURL · RenderPage · StreamPage(bidi)                §10.1
                -- MintProxyURL is the only way a proxy URL comes into existence: it takes an
                -- item id, never a URL, and returns a short-TTL signed capability (§10.1b).
                -- StreamPage is bidi because tiers 3 and 4 are one feature: frames down,
                -- input up, same stream, same lifetime.
AudioService    Synthesize(stream) · GetAudio · BuildQueue
TranslateService Translate · ListTranslations
StatsService    GetTrends · GetSourceStats · GetFeedHealth · SuggestUnsubscribes
NotifyService   Subscribe · Unsubscribe · ListSubscriptions · TestPush · GetLog
WebhookService  ListWebhooks · PutWebhook · DeleteWebhook · TestWebhook · SendTo
OfflineService  EstimatePack · BuildPack(stream) · ListPacks · DeletePack · SyncOutbox
ViewService     ListViews · SaveView · DeleteView · ReorderViews
EventService    WatchEvents(stream)
ImportService   ImportOPML/Bookmarks/Rules/Reader (stream) · Export* · BackupNow
```

Every destructive admin action pairs with a `Preview*` that enumerates blast radius **before**
confirmation (§7.9, §9.1). `RecordEngagement` is batched, fire-and-forget. `MarkRead` takes an optional
`before_ts` so the classic **"mark read: all / older than a day / older than a week"** is one parameter.

### 20.2 `ViewSpec`

```protobuf
message ViewSpec {
  Scope  scope  = 1;  // HOME|UNREAD|ALL|FAVORITES|NOTED|TAGGED|MUTED|SOURCE|FOLDER|SHARED|SEARCH
  Sort   sort   = 2;  // SMART|NEWEST|OLDEST|UNREAD_FIRST|BY_SOURCE|BY_TITLE|RECENTLY_SAVED
  Filter filter = 3;  // unread_only, has_note, tag_ids[], since, source_ids[], offline_only, lang
  Layout layout = 4;  // COMPACT|COMFORTABLE|CARD|GRID|SLIDESHOW (+ pane config)
}
```

`SMART` reads `home_ranking`; `MUTED` shows what rules are eating. Keyset pagination on
`(published_at, id)` served by §6.5's indexes, never `OFFSET`.

### 20.3 `Event` and the ring buffer

`seq` + oneof: `ItemsArrived` · `FeedStateChanged` (REFRESHING|OK|ERROR|RATE_LIMITED|RULE_BROKEN|
INACTIVE|FLOOD_SUSPECTED|MOVED) · `CountsChanged` · `ItemStateChanged` · `BookmarkChanged` ·
`HomeRankingChanged` · `ShareChanged` · `PackReady` · `RuleFired` · `AudioReady`.

**`since_seq` is the whole design.** A tunnel left open all day *will* drop. The server replays from
`since_seq`; a client further behind than the buffer gets `RESYNC_REQUIRED`.

**The ring buffer is per-tenant, not global.** Rev 7 said "~1000 events" without saying whose. A single
global buffer means one tenant's burst — a large import, or §13.2's fan-out — evicts events a quieter
tenant hasn't consumed, forcing a spurious full-cache drop on a tenant who did nothing. Per-tenant
buffers (~1000 each) with per-tenant depth visible in §9. Events are **filtered by scope in the
fan-out**, never on the client. **State changed via the sync API raises the same events.**

### 20.4 Client shape

Routes: `/` home · `/unread` `/all` `/favorites` `/notes` `/tag/:tag` `/muted` · `/source/:id`
`/folder/:id` `/view/:id` · `/search` · `/b` bookmarks · `/trends` · `/screensaver` · `/settings`
(+ `/smart`, `/offline`, `/appearance`, `/rules`, `/notifications`, `/apps`, `/profile`) · `/admin`.

Reads via one `query.New(WithStaleTime(30s))` + `ui.UseQuery` **with an offline-mirror fallback**;
writes via `ui.UseMutation` with optimistic values — mandatory, since a non-optimistic mark-read means
every `j` waits on a WAN round trip. The event pump owns `WatchEvents` on one goroutine and **never
touches state directly** — every event becomes a `ui.PostAsync` closure patching the cache, coalesced
on a ~100 ms tick.

**Expect to hand-roll reconnect.** `WithReconnectPolicy` exists, but CashFlux's `syncbridge/client.go`
bypasses it for a watch-loop, noting library reconnect can't fire once a blocking read is in flight.
Budget for the watch loop, not the advertised option.

**Density modes resolve the VirtualList fixed-height constraint** — each mode has its own fixed
`ItemHeight`, global not per-row: Compact 32px, Comfortable 88px, Card 140px, Grid, Slideshow. *(GWC's
own doc comment calls the fixed height "a real limitation rather than an oversight," so this is the
sanctioned response, not a workaround.)*

**Responsive is in scope — this supersedes the earlier "no mobile layout" line.** Three panes above
1220px, two below it, and a single column with a drawer below 900px; on a phone the list is the home
screen, sources are a bottom sheet, and the article pushes in over the top with actions in the thumb
arc. Mobile web and the GReader sync API (§15.1) are **complements, not substitutes** — the API serves
people who prefer Reeder or NetNewsWire, the responsive view serves everyone else and costs nothing at
the server. Consequences for the client build: `ItemHeight` becomes viewport-dependent (rows are
taller on a phone, so the density setting resolves per breakpoint), safe-area insets and 46px touch
targets are a baseline, and every hover affordance needs a tap equivalent. Designs:
`design/03-fanciful.html` (desktop) and `design/04-fanciful-mobile.html` (phone).

### 20.5 Industry-standard UX

**Keyboard — the Google Reader canon:** `j`/`n` next · `k`/`p` prev · `o`/`Enter` open · **`s` star**
· `m` toggle read · `b` bookmark · `e` note · **`t` tag** · `v` open original · `r` refresh ·
`A` mark all read (**all / >1d / >1w**) · `/` search · `g`+`h`/`u`/`a`/`s`/`b`/`t` · `1`–`4` render
tier · `d` density · `+`/`-` more/less like this · **`Ctrl-K` command palette** · `?` sheet · `Esc`.

**Feature discovery can't rely on the palette alone** — `Ctrl-K` only helps people who know what to
search for. **Contextual nudges driven by §16's data**: "you've skipped 12 of the last 15 from this
source — mute it?", "this feed hasn't updated in 90 days — unsubscribe?", "you star everything from
this author — make a rule?" That's the discovery surface, and the data already exists.

**Onboarding branches on tenant state.** A brand-new tenant gets import-OPML-or-pick-starters. **A new
member joining a populated tenant** sees tenant-visibility folders listed as *available to subscribe*,
not auto-subscribed — silently filling someone's reader with 200 feeds they didn't choose is a bad
first impression, and unsubscribing 200 things is worse.

**Behaviors people notice when missing:** unread counts on every row · bold = unread ·
mark-read-on-scroll and on-open (toggles) · keep-unread override · next-unread wraps · scroll position
restored per feed · confirmation on mark-all-read · relative timestamps with absolute on hover ·
**undo toasts instead of confirm dialogs** where reversible · real empty states.

**Accessibility:** focus management, `role="feed"`/`role="article"`, visible focus rings,
keyboard-only operation, `prefers-reduced-motion`, and **RTL** (handled in §10.2's direction setting).

### 20.6 A26 — Go all the way down

**Every line of application logic and every line of CSS is Go.** That is the point of the framework;
half-adopting it produces the worst of both worlds.

**CSS: zero stylesheets.** Everything goes through `css` / `css/u`, which fold into hashed classes the
wasm sink auto-injects. Verified against the package — the chosen design is fully expressible:

> **Executed 2026-07-26, not just read.** The Tier-1 boot page (`internal/httpx`) is authored entirely
> through this table and shipped with **`css.StyleBlock()`** — GWC's author-in-Go / ship-inline SSR
> path — and the emitted CSS was inspected: `Root`, `Custom`, `Global`, `Preflight`, `Media` and
> `Keyframes` all produce correct output on a **native** build, including `@media` wrapping,
> content-hashed `@keyframes` names, and a `prefers-reduced-motion` override. The page carries **zero
> `.css` files and zero `<script>` tags**, asserted by test. So A26's CSS half is proven rather than
> assumed, months before Tier 8 depends on it — which is the lesson D2 just charged us for.
>
> Two things worth knowing before Tier 8. `css.Keyframes` emits a **content-hashed** animation name
> and sets `animation-name` on the rule it returns, so it pairs with `animation-duration` /
> `-iteration-count` rather than with the `animation` shorthand. And the native sink is
> **process-global and append-only**, so a server must build its sheet **once** (`sync.Once`) rather
> than per request, or `Harvest()` grows without bound on every hit.

| The design needs | GWC provides |
|---|---|
| Per-source hue as a custom property (`--c`) | `css.Custom(name, value)` + `css.Var(...)` |
| Radial wash behind the article | `css.RadialGradient(shape, stops…)`, typed |
| Tinted highlight under a reason | `css.ColorMix(a, b, pct)` — **emits `in oklab`, not `in srgb`**, so the tint differs slightly from the mockup |
| Fraunces `SOFT`/`WONK` axes · `backdrop-filter` · `-webkit-line-clamp` · `env(safe-area-inset-*)` | `css.Raw(prop, value)` — the universal escape hatch |
| Breakpoints | `css.Media(query, rules…)` |
| `:root` tokens, resets, `@font-face` | `css.Root(...)` · `css.Global(sel, ...)` · `css.Preflight()` |
| Transitions and motion | `css.Keyframes` / `css.At` |

**Interaction is Go, not JS.** Pane resizing, the mobile drawer, swipe-back, the slideshow timer and
the keyboard map are all `ui.UseState` / `ui.UseEvent` / `ui.UseRef` with GWC's typed event handlers.
No inline `onclick`, no `<script>` block doing work.

**All `syscall/js` lives in one package: `client/platform`.** Wake lock, `storage.persist()`, Web Push
subscription, `BroadcastChannel` leader election (§12.5), IndexedDB via `interop.PersistentStore`, and
Service Worker registration each get a typed Go wrapper there, and **nothing else imports `js`**. Same
discipline as "all SQL in `internal/store`", for the same reason: one place to audit, one place to
test, one place to fix when a browser API moves.

**The three permitted pieces of JavaScript**, and why each is unavoidable:

1. **`wasm_exec.js`** — Go's own runtime shim. Not ours, not editable.
2. **The boot shim** — `web/index.html`. It was ~15 lines and is now closer to ninety, and the
   growth is worth being honest about rather than rounding down: it instantiates the module, counts
   the bytes on the way in, and paints the splash in the reader's own theme (§20.20). Every one of
   those jobs happens **before the wasm module exists**, which is the only test a line of JavaScript
   has to pass to live here. Anything that could wait for the module has not been added and will not
   be.

   The same exemption covers the **inline `<style>` block** in that file, which is the one place in
   this repository where CSS is not authored in Go. It has to be: it paints on the first frame, and
   the alternative is a white flash on a dark application on every load. The price is that the house
   palette is duplicated in it, and the price is **paid, not waived** — `client/design/bootpalette_test.go`
   fails if that copy and `client/design/tokens.go` disagree, if the progress rule stops being the
   source hues in order, if the reduced-motion query goes, or if either side of the `af.boot`
   handshake is renamed.
3. **`sw.js`** — the Service Worker. **This one genuinely cannot be Go.** A worker is registered by
   URL as a JS file and runs in its own global scope, so a Go service worker would need its own
   `wasm_exec.js` and a multi-megabyte instantiate on every cold start — for a script whose entire job
   is answering `fetch` from a cache in single-digit milliseconds. It stays JS, stays under ~60 lines,
   and does exactly two things: cache the app shell and receive push. Anything more belongs in the
   wasm app.

**`design/*.html` are specifications, not source.** They are hand-written CSS and vanilla JS because
that was the fastest way to decide how the thing should look. Nobody ports them; they get
reimplemented in Go against this section.

### 20.7 The API contract

Referenced everywhere, specified nowhere until now. All three transports (§8 diagram in `FLOWS.md`)
share it.

**Error taxonomy — and the one that leaks.** Codes are gRPC; the REST sync API maps them to HTTP.

| Condition | Code | HTTP |
|---|---|---|
| No credential, expired, or revoked | `Unauthenticated` | 401 |
| Authenticated, lacks the capability **for an object in your own tenant** | `PermissionDenied` | 403 |
| **Object belongs to another tenant, or does not exist** | **`NotFound`** | 404 |
| Validation — bad field, bad enum, malformed cursor | `InvalidArgument` | 400 |
| State is wrong — source deactivated, session not idle, trial already ended | `FailedPrecondition` | 409 |
| Quota, rate limit, or LLM budget exhausted | `ResourceExhausted` | 429 |
| `rev` mismatch on a compare-and-set (A25) | `Aborted` | 409 |
| Circuit open, degraded mode, storage read-only | `Unavailable` | 503 |

> **Cross-tenant access returns `NotFound`, never `PermissionDenied`.** `PermissionDenied` on
> item 4711 confirms item 4711 exists — which is a tenant-isolation leak dressed as good manners. The
> rule: **within your tenant, tell the truth about permissions; across tenants, the object does not
> exist.** T1 asserts this, not just that the row wasn't returned.

Every error carries a structured detail so the client can render something useful rather than a
toast saying "error": `{code, message, field?, quota?, retry_after_s?, doc_ref?}`. `message` is
**always safe to display** — internal detail goes to the log with the request id (§22.11), never to
the client.

**Pagination — opaque, keyset, spec-bound.**

```
cursor = base64url( sort_key | id | spec_hash )
```

- **Keyset, never `OFFSET`** — OFFSET degrades exactly when a list gets long enough to matter.
- **`spec_hash` binds the cursor to the `ViewSpec` that produced it.** Paging with a cursor from a
  different sort or filter is `InvalidArgument`, not silently-wrong results — the failure mode
  otherwise is a user changing sort mid-scroll and getting an interleaved list nobody can explain.
- **Cursors are opaque and unstable across releases.** A stale cursor is `InvalidArgument`; the client
  restarts from the top rather than guessing.
- Default page 50, max 200. `SMART` sort pages on `(rank, item_id)` from `home_ranking`; everything
  else on `(published_at DESC, id DESC)` per §6.5's indexes.

**Idempotency.** Every mutating RPC accepts an optional `idempotency_key` (client-generated, 128-bit).
It is **required** for anything replayed from the offline outbox (§12.4). The server stores
`(user_id, key) → response` for 24h and replays the stored response verbatim on a repeat. This is what
makes "drain the outbox after a week offline" safe rather than hopeful — a partial drain that
reconnects mid-flight must not double-apply.

**Rate limits**, per user unless noted, enforced at the interceptor:

| Surface | Limit | Why |
|---|---|---|
| Login | 5/min per user, 20/min per IP, then exponential lockout | §7.3 — the main compensation for no 2FA |
| Recovery initiate | 3/hour per user, 10/hour per IP | Cheap to abuse, expensive to ignore |
| Sync API (`api_tokens`) | 60/min per token | A polling client that misbehaves shouldn't affect a browser session |
| Public share feed (`/pub/:slug`) | 30/min per IP | The one endpoint strangers can reach |
| WebSub callback | 120/min per source | Hub bugs happen |
| Pack build | 3 concurrent, 10/hour | Extraction is expensive |
| LLM-backed RPCs | bounded by the §22.8 breaker + budget, not a count | Cost, not throughput, is the constraint |
| Everything else | 600/min per user | A backstop, not a policy |

**Versioning.** The proto is `articleflux.v1`. **Additive changes only** within v1 — new fields, new RPCs,
new enum values. A client must tolerate unknown enum values by falling back to a documented default,
because the sync API and the SW-cached wasm both guarantee old clients in the wild (§22.10). Removing
or renumbering a field means `v2`, and `buf breaking` in CI is what enforces that rather than
discipline.

### 20.8 Placeholders, not spinners

Every pane that fetches renders a **skeleton shaped like the thing it is waiting for**, never a
spinner and never an empty box. A skeleton row is the same `--row` height as a real one, a skeleton
image reserves its aspect ratio, and a skeleton paragraph is shaped like prose — a run of full-width
lines closing on a short one. The point is not decoration: a placeholder that resolves to a different
size is a layout shift with extra steps, and a spinner says "wait" where a skeleton says "here is
what is coming and where it will be".

Three in-flight flags, not one — feeds, items, article. They belong to three panes that finish at
different times, and a single `busy` makes the whole screen go pending whenever any part of it is.

### 20.9 The reading pane is a stream (A28)

The article pane holds a **sequence** of articles, not one:

- Approaching the bottom **appends** the next item from the list; approaching the top **prepends**
  the previous one, with the scroll position held across the insertion (`KeepScrollAnchored`).
  Replacing the pane's contents on advance — the first implementation — meant reaching the end of an
  article made it vanish mid-paragraph with nothing to scroll back to.
- **Which article is being read is a scroll position, not a click.** The topmost article in the
  viewport drives the document title, the verdict chips, the highlighted list row, and marking read.
- Opening from the list seeds the stream with the article **before** the clicked one, so scrolling up
  works from the first frame, and scrolls the clicked one to the top of the pane.
- Bodies are fetched **per article and one ahead in each direction**, so the skeleton is only ever
  seen on a cold open.
- Articles over `clampWords` (900, ≈4 minutes) are **clamped by default** with a "Read the rest"
  control, so time-per-item while scanning stays roughly constant. One 4,000-word essay between two
  headlines makes a feed unpredictable to scan, and scanning is what the stream is for.

**Movement the APP causes is not evidence of reading** *(fixed 2026-07-27)*. The two rules above —
seed the article before the clicked one, hold the scroll across a prepend — both put an article the
reader has never seen above the fold, and "scrolled completely past" is one of the two ways this app
marks something read. So opening row *n* and then row *n+2* marked **all three** read, and credited a
`Completed` engagement for the middle one: an article that was never on screen for a frame scored as
finished. Reported by Cam as *"isn't granular enough"*, which is the polite version.

The fix is a suppression set (`skipPast`), not a weaker rule about scrolling. Three sites put an id in
— the seeded predecessor on a fresh open, everything the travel passes over on a jump inside the
stream, and the article a prepend inserts — and **one site takes it out again: becoming the topmost
article.** Scrolling up into something *is* reading it, so the suppression has to end exactly there;
without that, a jumped-over article could never be marked read by scrolling again, which is the same
bug with the sign flipped.

> Rejected: a time window around the programmatic scroll. It makes correctness depend on how fast the
> browser settles a smooth scroll, which is not a property this can be built on. The ids are known at
> the moment the app moves them, so name them.

**Both scroll triggers re-arm on GROWTH, not only on position.** A purely positional edge fires once
and goes quiet, because appending keeps the reader inside the trigger zone — downward reading stopped
dead after exactly one article. If the container got taller since the last fire, the previous request
has been satisfied and the trigger re-arms.

### 20.10 Lazy loading respects the shape of the feed (A29)

`ListItemsResponse.total` (first page only) is a `COUNT` over the *same* filter set as the page query
— `listFilter` is shared by `ListItems` and `CountQuery` precisely so the two cannot drift. The client
sizes the virtual list to `max(total, len(loaded))`, renders unloaded indices as placeholders, and
fills toward wherever the viewport actually is:

- The trigger is the scroll **position**, not proximity to the end of the loaded rows. Those are the
  same thing only while the list is exactly as long as what has been fetched; once it is as long as
  the feed, dragging the thumb produces no "near end" event at all, just a new position.
- A page request is sized to the **gap**, capped at `MaxLimit`, so a long drag is a short chain of
  large pages rather than sixty small ones. Keyset cursors cannot seek to an arbitrary offset — that
  is the price of cursors that never skip or repeat rows while feeds are being polled.

**The hazard this exposed, and the rule that follows.** A GWC `ui.State` read from inside an
asynchronous callback returns the value **as of the render that created the callback**. So
`items.Set(append(items.Get(), page...))` in a response handler silently discards every page that
landed while the request was in flight: the client fetched all 3,621 items in sixty round trips and
kept 380 of them, while the filler — seeing a list that never grew — kept asking for more forever.

> **Rule.** Anything an async callback *accumulates into* (a slice it appends to, a map it merges
> into) must live in a `ui.UseRef`, and the callback body must be reached through the `actions` Ref so
> it is the newest render's closure. State is what the render reads; the Ref is what the callback
> writes. `client/view/reader.go` does this via `itemsRef` + `setItems`, and `act.pageLanded` /
> `act.bodyLanded` / `act.fill`.

### 20.13 Resuming, and what counts as state (A30)

Everything the reader set deliberately is a server-side preference, restored on
connect **before the first item list is fetched**:

| Key | What it restores |
|---|---|
| `read.kind` / `read.value` / `read.title` | Which stream, feed, tag or search |
| `read.item` | The article, reopened in place if it is still in that list |
| `list.unreadOnly` · `rail.unreadOnly` · `rail.filter` | The three filters |
| `pane.rail` · `pane.list` | Pane widths |
| `tts.smartPlus` | The egress opt-in |
| `tts.digest` | Speak a ~1 minute summary instead of the article — a second egress, so a second opt-in |
| `tts.autoplay` | Carry on to the next article when this one ends |

Four flat keys for the scope rather than one encoded string: flat keys cannot be
mis-parsed, and a key this app stops understanding is ignored on the next boot
instead of resolving to something wrong.

**The item list is deliberately not fetched at connect.** Loading the default
scope first and replacing it a round trip later is a visible flash of the wrong
feed plus a wasted page request on every boot. The prefs effect owns that fetch
on both paths — the restore and the nothing-saved default — and a *failed* prefs
call still loads the default, because losing your place is a small regression and
losing the feed is the app not working.

`list.unreadOnly` must be restored before the fetch, not after: `loadItems` takes
the flag as an argument, so setting it afterwards fetches the wrong list and then
quietly disagrees with the toggle the reader is looking at.

### 20.14 Keyboard-complete (A32)

**Arrows move within a pane; Tab moves between them.** That split is the whole
design: 151 tab stops between the rail and the article is not navigation, and
roving focus inside each pane is what every list-and-detail application has
converged on. Focus is read out of the DOM rather than tracked in state, so
clicking a row and then pressing an arrow continues from the row that was
clicked — an index in state would silently disagree with the pointer.

| Where | Keys |
|---|---|
| Anywhere | `Ctrl K` palette · `?` shortcut sheet · `1` `2` `3` panes · `/` search · `f` feed filter · `r` refresh · `u` unread-only · `Esc` close / stop / back |
| Feed list | `↑ ↓` move · `Enter` open |
| Article list | `↑ ↓` move **and open** · `j` `k` next/previous |
| An article | `o` original · `l` like · `d` dislike · `t` read later · `U` mark unread · `Ctrl Enter` save the note NOW (it autosaves on a pause and on blur regardless) |

Two rules that are easy to get wrong and were:

- **Escape must be handled before the is-typing guard.** Otherwise the guard
  swallows it and the only way out of the search box is the mouse — which in a
  keyboard-first app is the same as no way out.
- **The list's arrows OPEN as they move.** A reader moving through a list is
  reading it; arrows that only move focus while `j`/`k` opens would be two
  behaviours for one gesture.

**The palette (`Ctrl K`) is not a second search box.** Search queries article text
and costs a round trip; the palette matches what the client already holds — feeds,
tags, streams, commands — and answers on the keystroke. Ranking is three tiers
(whole-label prefix, word prefix, substring), shorter labels first. Deliberately
**not** fuzzy: subsequence scoring makes almost everything match almost everything
at 151 feeds, and a list that never narrows is worse than no palette. Its commands
call the same handlers the chips do, so it cannot drift into a second
implementation of the same verb.

### 20.15 Per-feed settings (A33)

Reached from a gear on the sidebar row — hidden until hover **or keyboard focus**, and always visible
below 900px where there is no hover to depend on. The gear is a *sibling* of the row, not a child: a
`<button>` inside a `<button>` is invalid and browsers resolve it by hoisting one out, after which the
click target is not the element that was drawn.

The panel is grouped by ownership, and that grouping is A14 made visible:

| Group | Table | Changing it affects |
|---|---|---|
| **Yours** — name override, in-the-ranked-feed, mute, offline depth, tags | `subscriptions` | you |
| **Shared** — feed URL, site URL, poll interval | `sources` | **every subscriber on this server** |
| **Health** — last fetch/success/next, item and unread counts, the publisher's error verbatim | `sources` | read-only |
| **Actions** — fetch now, mark all read, unsubscribe | — | — |

The shared group's warning is its *heading*, not a footnote beneath it: someone changing a poll
interval should read why before they change it. It names the number of other subscribers, because
that is what makes the warning land.

Both tables are written in **one transaction**. They are not independent — a panel that renamed the
subscription and then failed to set the interval would leave the reader looking at a form where half
of what they submitted took effect, with no indication which half.

`fetch_interval_s` is clamped at the write to **5 minutes – 1 week**, and `next_fetch_at` is
recomputed from the *last* fetch rather than from now, so lengthening an interval cannot postpone a
poll that is already overdue. The floor is politeness rather than performance: the column is global,
so one user setting ten seconds makes this server hammer a publisher on everyone's behalf — the same
reasoning as honouring conditional GET (§22).

Every mutable field is `optional` on the wire, unset meaning "leave it alone" — the same tri-state
rule `SetItemState` uses, so a client that knows about half these fields cannot blank the other half
by omitting them.

### 20.16 Themes and motion — every value is a token (A39)

`client/design/theme.go` and `client/design/motion.go`, both authored in the GWC `css` package like the
rest of the sheet (A26).

**Themes are variable sets, not stylesheets.** Every colour, face and metric the 1,700-line sheet
paints with is a custom property, and `theme.go` is the only place their values are decided. The
alternative — a class or a second sheet per theme — multiplies the sheet by the number of themes; a
variable costs one declaration however many there are, and a theme nobody anticipated (a reader's own)
costs exactly what a built-in one does. Five ship: **Fanciful** (the default, `design/03-fanciful.html`
transcribed), **Ink**, **Ledger**, **Daylight**, **Contrast**.

Two invariants hold it together, and breaking either is how a theming layer starts lying:

- **`Theme.Vars()` is the single ordered list.** The sheet's first paint and the runtime applier both
  iterate it. A token in one and not the other changes only on reload — invisible until someone
  switches themes twice.
- **No literal colour outside a `Theme`.** The hexes that were in the sheet — the verdict green and
  coral, the modal shadow — were precisely the ones that broke on a light ground.

**`Wash` is a token for a reason worth remembering.** The article's radial gradient carried a fixed
24% of the source hue, which is correct on plum — a ground with colour of its own to dilute the mix.
Over a *neutral* ground there is nothing to dilute it with, so the identical declaration reads as light
falling in on Fanciful and as a green panel on Ink, and worst of all on **Contrast**, whose whole claim
is maximum legibility and which was carrying the heaviest decoration of the five. It is per-theme now
(24 · 19 · 13 · 11 · 6%), which also collapses the earlier light-tone override into the same knob: the
problem was never light-versus-dark, it was ornament calibrated against one ground and used on five.
**Every theme passed §20.16.2's contrast floor throughout** — this is precisely the class of defect a
ratio cannot see, and it survived because nobody had opened the theme.

`Tone` (`dark` / `light`) is not decoration: a source hue at 78% lightness is a clear mark on plum and
an illegible smear on cream, so `--ink`, the hue where it carries *text* rather than fills a shape, is
darkened on a light ground. Tone is what selects that.

**Motion is gated by a token, not by an override.** Every duration is written
`calc(var(--mo) * <time>)`, where `--mo` is a plain multiplier: 1 is on, **0 is off**, and off means
the transition is *absent* — arriving at exactly the state it would have animated to — rather than
suppressed. What this replaces is a `* { transition: none }` rule under a reduced-motion query at the
bottom of the sheet, which is a broom: it works until someone writes a transition with `!important`, or
in a later layer, or on a pseudo-element the universal selector misses, and nothing fails loudly when
it stops working. Making the token the only way a duration can be *written* means a new animation is
gated because it was authored at all.

Three durations, and a fourth would be a sign the case is really one of these three: `--t1` 110ms for
colour and opacity on small controls, `--t2` 180ms for transforms and marks, `--t3` 300ms for
entrances. Four gestures, and the vocabulary is ink and light rather than the usual bouncing cards,
because this reader's world is print: **warm** (hover changes a colour and nothing moves — in a rail of
151 rows, geometry that shifts on hover is seasick), **mark** (a rule draws itself from its centre, the
one place with a hair of overshoot, because it is the most-repeated gesture in the app), **arrive**
(fade up seven pixels, decelerating, no overshoot), **breathe** (slow, low-amplitude, never blinking).

**The runtime half is the Appearance tab** (`client/view/theme.go`). Four preferences —
`ui.theme` · `ui.accent` · `ui.reading` · `ui.motion` — and one `applyAppearance` that runs on every
change and at boot. It writes token values onto `documentElement.style`; an inline declaration outranks
the `:root` block the sheet emitted; the browser repaints. **No component re-renders when the theme
changes**, which is why switching themes with 151 rail rows and a virtualised list of 3,600 items on
screen costs a paint rather than a reconciliation.

Every stored value keeps **"unset" distinguishable from "set to the default"**: an empty theme means
the house theme, an empty accent means whatever the theme chose, an empty motion means *ask the
operating system*. That is what lets the screen offer a way back to following the system, and what
stops a reader who never opened it from having a preference invented for them. Accents are the source
hues reused as interface accents, with a **separate light set** taken down to where they can carry
white — the same `Tone` problem as `--ink`. Reading size is **three choices, not a slider**, and the
prefs are server-side (A30), so the look of the reader travels with the account rather than with the
browser.

#### 20.16.1 What the four gestures are actually spent on

**Marks travel; they do not cross-fade.** The selected row in the item list paints nothing of its own.
The highlight is one cursor drawn on the *scroll container*, moved by `--cursor` — an index times the
row height — and the reason it is a pseudo-element of the scroller rather than a real element is that
an absolutely positioned child of a scroll container is laid out in that container's **content space**.
Its y needs no scroll arithmetic and no recompute as the list moves under it. Two rows lighting and
unlighting is two events where the reader made one gesture; a mark that slides is the gesture. This is
the most-repeated interaction in the application (`j j j k`), so it is the one worth the most care, and
`platform.ScrollIntoView` goes `behavior: smooth` on the same bit — the list keeps step with the
article rather than jumping to it.

**Spawning animates the arrival of *data*, not the arrival of an element on screen.** The list is
virtualised, so a plain mount animation fires on every row that scrolls past — a slot machine at any
real speed. So `setItems` diffs each incoming list against the one it replaces and marks only ids that
were not there before; only those rows carry `data-fresh`, and the mark is cleared after 900ms so a row
scrolled away from and back mounts quietly. The property falls out correctly with no special cases: a
scope change makes the whole first page fresh, *load more* makes only the appended page fresh, and
marking an article read rebuilds the list out of items that are all already present — so **the list
does not move under the reader's hand on `j`**, which is the case that matters most because it happens
a thousand times a day. The stagger counts from the first fresh row the reader can *see* rather than
from the first row of the page, or a *load more* forty rows deep would pause and then flash the whole
screen at once.

**"Loading…" is a claim; a moving rule is evidence.** The three waits that used to be bare text — more
items, the next article, a bulk operation — carry an indeterminate hairline in the accent. Not a
spinner: the note's save mark is already a spinner meaning something else, and reusing the shape would
make both mean less.

**Looping animations are gated on amplitude, not duration.** `animation-duration: 0s` with
`iteration-count: infinite` is a corner of the spec browsers have disagreed about, and "the skeleton
froze mid-shimmer at a visibly wrong offset" is a bug that would appear *only* for the readers who
asked for less motion. So the four loops keep a real duration and animate to the value they started
from. `--mo-off` (`calc(1 - var(--mo))`) is the inverse, for the few places where turning motion off
means changing a value rather than zeroing a time: the loading rule's travelling band widens to the
whole rule and holds still, because a short band frozen at one end reads as a determinate progress bar
stuck at 0%, which is a worse lie than a steady mark.

#### 20.16.2 The three guards, which is what makes A39 a decision rather than a convention

All native, all in `client/design`, all asserting against the **actually emitted** stylesheet
(`css.Reset()` → `Sheet()` → `css.Harvest()`) rather than against the source that produced it. Each
covers a failure that is invisible in a screenshot of either half alone:

- **No dangling token, and no token a theme cannot reach.** A `var(--typo)` computes to nothing and
  the declaration is silently dropped; a token the sheet reads that is absent from `Theme.Vars()` keeps
  the house value forever, which only shows up when someone switches themes twice.
- **No ungated duration**, with the four amplitude-gated loops named by their exact durations so a
  fifth fails the test rather than quietly joining the exemption. And **no literal colour**, with
  `:root` and the reader-mode iframe's deliberately-white base stripped by name — so a stray hex
  anywhere else still fails.
- **A readability floor for `--ink`, in the browser** (`e2e/appearance.spec.mjs`). The Go floor cannot
  reach it: `--ink` does not exist until an engine resolves a `color-mix()` against a hue the server
  assigned at runtime, so Go only ever sees the expression. On the light theme that mix put the amber
  source at **4.45:1** — below AA, on the source name of every row — and it was invisible to
  everything: the Go floor passed, the screenshots looked plausible, no ratio anywhere was wrong. The
  measurement now happens in the shipping engine, read back as real sRGB bytes off a canvas, because
  `getComputedStyle` returns `oklab()` for a color-mix and parsing those three numbers as RGB is how
  the first version of the check reported 18:1 for a colour that was failing. A second case asserts the
  seven hues are still *distinguishable* afterwards, since the floor alone is satisfiable by painting
  every source the same near-black.
- **A readability floor.** Every theme's text tokens are checked against all three grounds they can
  land on — the page, a hovered row, and the selected row a reader sits on for as long as they are
  reading it — at 4.5:1, the accent in the direction it is actually used (a *fill*, carrying `--bg`).
  Adding a theme is a five-line struct literal, which is exactly the property that makes it easy to add
  an unreadable one. It found three: Daylight's `--mute`, `--pos` and `--neg` were 3.9–4.2:1 against
  the selected row. It also found that **Fanciful's own `--mute` is 4.42:1 on a hovered row and 3.94:1
  on the selected one** — transcribed verbatim from `design/03-fanciful.html`, so that one is a
  decision about the mockup and not a value to nudge (**D22**, TODO 8b.44). It is recorded with its measured ratios and
  ratcheted: it may not get worse, and no new theme inherits the exception.

### 20.17 The settings surface

One page, not a modal — settings behind a dialog get skimmed, and several of these need a sentence to
explain themselves (what Smart+ sends where; what a poll interval does to a publisher). Seven tabs, in
the order someone needs them: **Reading** · **Listening** (including the egress switch, with its
warning attached) · **Feeds** · **Account** · **Server** · **Activity** · **Speed**.

The last three are what a self-hosted application uniquely owes its operator, and they are the reason
this is a surface rather than a preferences dialog: **nobody is tailing a log file behind this and
there is no dashboard.** The person running it is the person reading it. Server answers "is it
healthy" (version, storage, counts), Activity answers "what just happened" (the `SystemService` log
ring), Speed answers "why is it slow" (per-RPC latency). If those are not answerable here they are not
answerable at all.

### 20.18 Adding a feed, and categories in the rail (A37)

**The add-a-feed dialog.** It was a URL box pinned to the foot of the rail — the fastest possible path
for the one thing it could do, paste and Enter, with no room for the other two decisions a reader makes
when they add a feed: what to call it, and where it goes. Both were reachable only afterwards, from a
different panel, on a row they then had to find. It is a dialog now and the rail keeps a button where
the box was; the cost is one click on the fast path, and what it buys is naming and filing at the
moment the reader is already thinking about them rather than in a tidy-up pass that never happens.

Three fields and no more. Poll interval, cache depth and mute belong to a feed you already have and
have formed an opinion about — they live in the per-feed panel (§20.15), and putting them here would
make adding a feed a configuration exercise.

**The rail is a fixed head, a scrolling middle and a fixed foot.** It used to be one long scroller, so
the five streams — the destinations used most, and the only ones whose position a reader can learn —
slid away the moment they went looking for a subscription. They are six rows; paying for them once at
the top is cheaper than paying to scroll back to them. Adding a feed sits at the foot for the same
reason. Each of the three groups folds away, and the fold state is remembered: at 151 subscriptions the
rail cannot show everything, and which part earns the height changes by the hour.

**Categories are shown *as well as* the flat list, deliberately.** The flat list answers "where is that
feed" — alphabetical, filterable, the fastest route to a name you remember. The categories answer "what
have I got on this subject", which a flat list of 151 rows cannot answer at all. Showing a feed twice
costs a row in a section that folds away; not showing it costs the question. Each category is itself a
fold, because opening all of them at once is the flat list again with headings in it.

> **Implementation note that will apply again.** The open-category set travels as a comma-joined
> **string**, not a map. `railProps` is compared by value to keep 151 rows from re-rendering, and a map
> field compares by identity — it would be a different value on every render and defeat the bailout for
> the entire rail.

**A tag row is not a feed row**, and the tag panel (`UpdateTag`) is short because of it: no publisher,
no poll interval, no health, nothing shared with anyone else on the server. It reuses the feed panel's
vocabulary rather than inventing a second dialog language, and every string in it works to keep one
distinction straight — the **tag** ("rust", what you type, what the chip says) versus the **row**
("Systems programming" ⚛, what the rail draws). Getting that backwards is the one way the feature does
damage, because a reader who believes they renamed the tag will go looking for the new word in the tag
field and not find it. There is no `GetTagSettings`: `ListTags` already returns everything the panel
shows, so the dialog opens with its content instead of with a spinner.

### 20.11 Mobile: a persistent tab bar

Below 900px the shell becomes two grid rows — panes, then a **four-tab bar** (Read · Feeds · Notes ·
Settings). A grid row rather than a fixed overlay, so the panes are shorter by exactly the bar's
height; a fixed bar sits on top of the last list row, and the row underneath it is the one that can
never be tapped. `env(safe-area-inset-bottom)` clears the home indicator.

It renders on every viewport and CSS hides it above 900px, rather than being conditional in Go —
conditional rendering would need the viewport width in component state, which means a resize listener
re-rendering the tree. **Settings is phone-only** and holds the switches that live in the list header
on a wide screen, plus the item counts that explain the scrollbar's behaviour.

Before this, the rail — and therefore the whole subscription list — was reachable on a phone only by
backing out twice from wherever you happened to be.

### 20.12 Row states: age and verdict

A row says three things about itself, in words as well as colour (a colour alone does not survive
being colour-blind or being glanced at):

| `data-age` | Meaning | Treatment |
|---|---|---|
| `new` | Unread, published < 24h | Amber `new` pill — the app's own accent, so it reads as the app pointing |
| `unread` | Unread, 24h–30d | The default row |
| `stale` | **Unread and > 30 days old** | Outlined `stale` pill, title softened, hue bar to 50% |
| `read` | Read, at any age | Title loses weight, hue bar to 35% |

`read` wins over age: once you have read it, how old it is stops being the useful fact. `stale` is
the one a reader acts on — it is permission to skip, and in a firehose "unread" and "worth reading"
stop being the same thing.

A note, when there is one, **outranks** both the ranking reason and the publisher's summary in the
third line: in the Notes stream a row of headlines is nearly useless for finding the one you want,
because what you remember is what *you* wrote.

### 20.19 The connection is a state machine (A40)

A5 put every read and every write on **one WebSocket**, held open for days by a tab that gets
suspended, moved between networks, and backgrounded, talking to a server that is a box in someone's
house. That single socket is the app's only nervous system, and rev 8 specified exactly one sentence
about its failure behaviour — §20.4's *"expect to hand-roll reconnect"*. This section is the rest of
it.

The framing that produces every decision below: **the indicator's one job is that "silently
disconnected" must never look like "a quiet news day."** A reader who is shown stale content and told
it is live has been lied to in the one way this application cannot afford, because the content of a
reader is *absence* — nothing new — and absence is exactly what a broken connection also looks like.

#### 20.19.1 The failure taxonomy

Seven ways the tunnel dies. The middle column is what the code does **as built on 2026-07-26**; it is
recorded here because "we already retry" was the belief, and retrying is only the answer to three of
these.

| # | Failure | Socket behaviour | As built | Required |
|---|---|---|---|---|
| **F1** | Server restart / redeploy | Close frame (or RST) arrives | ✅ backoff redial, ~500 ms | Clean `1001` close and `GracefulStop` so the blip does not roll back an in-flight mutation |
| **F2** | Server down, DNS gone, cert expired | Dial fails repeatedly | ✅ backoff to the 20 s cap, forever | Distinguish "the server is unreachable" from "you are offline" — different remedies |
| **F3** | **Half-open socket** — NAT/CGNAT reclaim, VPN drop, Wi-Fi vanishing | **Nothing.** No FIN, no RST | ❌ **the browser's `WebSocket` stays open and gRPC stays `READY` — the indicator says LIVE forever** | Client keepalive; verdict within ~40 s |
| **F4** | Lid close / device sleep | Dead on wake, timers frozen through the sleep | ⚠ recovers only after the remaining backoff, up to 20 s | Kick on resume; ≤1 s to reconnect |
| **F5** | Network change — Wi-Fi→cellular, VPN up/down | Old socket dead, `online`/`offline` fires | ⚠ same as F4; **nothing in `client/` listens for `online`** | `ResetConnectBackoff()` on `online` |
| **F6** | Backgrounded tab | Alive, but JS timers throttled to ~1/min | ⚠ backoff and keepalive both ride JS timers, so both are throttled | Do not fight the throttle — **verify and refetch on `visible`** |
| **F7** | **Terminal refusal** — session expired/revoked, version skew (§22.10), tenant deleted | Socket connects **fine**; every call is refused | ⚠ **half.** §7.1b's interceptor clears the token and routes to `Login`, so the *auth* case is handled — but it also paints the indicator **`down`** on the way there, on a connection that is perfectly healthy, and **skew has no handling at all** | Classify. A refused *call* is not a broken *connection*; `blocked` names a remedy |

**F3 and F7 are the two that matter**, and neither is a tuning problem. F3 is a missing probe; F7 is a
missing distinction. Everything else is a schedule.

> **On F7's remaining half, precisely.** §7.1b landed the right *behaviour* for an expired session and
> got there through the auth interceptor rather than through the connection layer, which is why the
> connection layer still believes an `Unauthenticated` is a transport failure. The consequence today is
> cosmetic — a red dot on the way to a login screen. The consequence tomorrow is not: **version skew
> refuses at the handshake**, and a refusal the client reads as an outage is retried on the backoff
> schedule forever, which is exactly what §22.10 says must not happen.

#### 20.19.2 Five states, because there are five remedies

Three states cannot express this: `down` currently means "your Wi-Fi is off", "the box in the closet
is unplugged" and "your session expired" at once, and those need three different sentences from the
reader.

| State | Means | What the indicator says | Remedy offered |
|---|---|---|---|
| `live` | RPCs are flowing | Nothing loud | — |
| `connecting` | First connect, or a redial in flight | Quiet, **and only after ~1 s** (below) | — |
| `offline` | `navigator.onLine` is false | "You're offline" | Reading continues from the mirror (§12.3) |
| `down` | We have a network; the server does not answer | "Can't reach the server · retrying in `N`s" | **Retry now** |
| `blocked` | Connected, and refused — expired session, skew, deleted tenant | "Your session expired" / "A new version is available" | **Sign in** / **Reload** |

**The indicator lags the transport, deliberately.** A server restart reconnects in ~500 ms; painting
`connecting` for it converts an invisible event into a visible flicker on every deploy. So
`connecting` is suppressed for the first **1 s** and `down` is not shown until either the first redial
has failed *or* 3 s have passed. In the other direction there is no hysteresis at all: `blocked`
paints immediately, and `live` paints immediately, because delaying good news is only ever confusing.

The countdown is not decoration. It is what buys the 20 s backoff cap its headroom (§20.19.5): a
reader who can *see* the wait and click through it does not experience the cap as a hang, which is the
entire reason the cap does not have to be tuned lower and hammer a server that is still booting.

#### 20.19.3 Liveness: two keepalives, and the trap between them

There are two, at different layers, and they answer different questions.

**The WebSocket ping (server → client, 30 s / 90 s idle)** — shipped, `internal/app/app.go`. It
answers *"can the server reclaim this connection slot?"* and it keeps intermediaries from reaping an
idle tunnel: nginx `proxy_read_timeout` defaults to 60 s and Cloudflare idles at 100 s, so a reader
with nothing new for two minutes would otherwise be disconnected by the proxy on a schedule. The
browser answers pongs in its own stack — **no JavaScript, no wasm, and no application timer is
involved, so this one survives tab throttling.** It is why F3 is a client-side problem only: the
server always finds out.

**The gRPC keepalive (client → server, 30 s / 10 s timeout)** — **not shipped, and it is the fix for
F3.** `grpctunnel.WithTunnelKeepalive` exists and is unused. It sends HTTP/2 PING frames *through* the
tunnel; a probe that goes unanswered for the timeout tears the transport down and re-dials, which is
the only mechanism that can notice a socket the browser still believes is open.

> **The trap, and it is not hypothetical.** `ApplyTunnelKeepalivePolicy` sets
> `PermitWithoutStream: true` — the client will ping on an idle tunnel, which is the entire point. The
> server's `grpc.NewServer` currently takes **no** `KeepaliveEnforcementPolicy`, so it runs the
> defaults: `MinTime: 5m`, `PermitWithoutStream: false`. A client pinging every 30 s with no open
> stream collects two ping strikes and is sent **`GOAWAY ENHANCE_YOUR_CALM (too_many_pings)`** — it
> reconnects, pings, and is kicked again. **Enabling the client half alone converts a silent
> half-open bug into a visible flap every ~60 s.** The two options must land in the same commit:
>
> ```go
> // client — client/data/client.go
> grpctunnel.WithTunnelKeepalive(30*time.Second, 10*time.Second)
> // server — internal/app/app.go, on grpc.NewServer
> grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
>     MinTime: 20 * time.Second, PermitWithoutStream: true,
> })
> ```
>
> `MinTime` sits below the client interval with margin, because a throttled or descheduled client
> ping arrives *late*, never early — and a policy tuned to exactly 30 s would only ever be violated by
> jitter in our own favour.

The client interval also feeds the server's idle reaper: a probing client produces traffic every 30 s,
so the 90 s read deadline now only expires on a genuinely silent peer rather than on a reader who has
not clicked anything.

**Both numbers live in `internal/connpolicy`, imported by both ends.** The finding was that these two
must ship together, so the structural answer is one file that names both with the invariant between
them and a test that fails when they diverge — not two correct-looking numbers in two files neither of
which reads as wrong on its own. It holds no gRPC types on purpose: `client/data` imports it, and
every byte it pulls in is a byte in `app.wasm` (R4).

> **gRPC silently clamps the client interval to a 10 s minimum** — no error, nothing logged. So the
> obvious response to "detection feels slow" is to lower `ClientInterval`, and the obvious response
> produces a number that is not used, a detection budget that is wrong, and no evidence anywhere that
> either happened. 30 s is comfortably above it and `connpolicy.GRPCClientFloor` has a test, because
> the trap is not that 10 s is the floor — it is that going under it changes nothing and says nothing.
> (Found by a test that set 200 ms and then waited five seconds for a detection that could not arrive
> before ten.)

#### 20.19.4 The detection budget

How long the indicator may lie, per failure. These are the acceptance numbers for T21.

| Failure | Worst case to the truth | Mechanism |
|---|---|---|
| F1 restart | < 1 s | Close frame |
| F2 unreachable | < 1 s | Dial failure |
| **F3 half-open** | **≤ 40 s** (30 s probe + 10 s timeout) | Client keepalive — **today: never** |
| F4 wake | ≤ 1 s after resume | `visibilitychange` kick |
| F5 network change | ≤ 1 s after `online` | `ResetConnectBackoff` |
| F6 background tab | Truth **on becoming visible**, not before | Accepted — see below |
| F7 terminal | First refused RPC | Error classification |

**F6 is accepted rather than solved, and that is a decision.** Chrome throttles timers in a hidden tab
to roughly one per minute and applies an intensive budget after five minutes, so neither the backoff
timer nor the keepalive probe can be trusted there. Fighting it (Web Workers, `Worker`-hosted probes,
audio-context tricks) costs battery on a device whose owner is not looking at the app to buy accuracy
nobody can observe. The rule instead: **a hidden tab makes no promises; a tab becoming visible
verifies before it renders.** On `visible` the client kicks the connection and re-runs the recovery
refetch, so the first thing a returning reader sees is either fresh data or an honest `down`.

#### 20.19.5 The schedule, and the three events that jump it

The backoff itself is unchanged and stays where it is — 500 ms → ×1.6 → **20 s cap**, jitter 0.2,
`MinConnectTimeout` 5 s, **no attempt limit ever** (`client/data/client.go`). gRPC's own 120 s default
is right for a datacentre client with thousands of peers and wrong for one tab and one server.

What is missing is that nothing can *interrupt* it. `conn.Connect()` is a no-op while a subchannel
sits in `TRANSIENT_FAILURE` — the backoff timer owns the redial — so the client currently has no way
to act on knowing the network just came back. `ClientConn.ResetConnectBackoff()` is exactly that
primitive: it resets every subchannel's backoff and redials at once. Three events call it, plus one
button:

| Trigger | Why |
|---|---|
| `online` | The OS just told us the network exists again. Waiting out a 20 s timer at that point is a choice, not a constraint |
| `visibilitychange` → visible | Covers wake, bfcache restore, and every throttled timer that did not fire while hidden |
| `pageshow` with `persisted: true` | A bfcache restore closed the socket underneath us; gRPC may not have noticed yet |
| **Retry now** | The reader is looking at the countdown and is more certain than we are |

`offline` does the opposite: it paints the `offline` state and stops the countdown, because there is
nothing to count down to and a timer promising a reconnect that cannot happen is a worse lie than
silence.

#### 20.19.6 Classifying the error: transport, application, terminal

Every RPC failure currently marks the connection `down` (`Client.track`), because `err != nil` is the
whole test. §20.7 already defines a taxonomy in which most errors say nothing about the connection at
all; the connection layer has to read it. Two things are wrong today: a `NotFound` from a perfectly
healthy server paints the indicator red, and the one code that *is* handled — `Unauthenticated`, by
§7.1b's interceptor — is handled a layer above, so the connection layer still misreads it on the way
past.

| Codes | Class | Effect on the connection | Effect on the caller |
|---|---|---|---|
| `Unavailable`, `DeadlineExceeded` | **Transport** | → `down`, keep retrying | Retry or roll back |
| `Unauthenticated` | **Terminal** | → `blocked`, **stop retrying**, hold the outbox | Login screen (8b.33) |
| `FailedPrecondition` + skew detail | **Terminal** | → `blocked` | Purge the SW cache, hard reload (§22.10) |
| `PermissionDenied`, `NotFound`, `InvalidArgument`, `Aborted`, `ResourceExhausted` | **Application** | **None. The connection is fine** | Render the §20.7 detail |
| `Canceled` | Neither | None — this is our own teardown | Nothing |

Two consequences worth stating separately. **`ResourceExhausted` carries `retry_after_s`** (§20.7) and
it must be honoured *instead of* the schedule, not alongside it — a client that backs off 500 ms
against a server saying "wait 30 seconds" is the rate limiter's problem rather than its subject. And
**`blocked` must be reachable from a cold boot**, not only mid-session: the connection can come up
before the reader's session is checked, which is the sequence 8b.33 will introduce the moment `WhoAmI`
runs on connect.

#### 20.19.7 Recovery is debounced, generation-guarded, and sometimes skipped

Every transition into `READY` currently fires four RPCs — feeds, tags, folders, items. On a flapping
tunnel that is a refetch storm at the exact moment the server is least able to serve one, and it is
self-reinforcing: the storm is what keeps the newly recovered connection saturated. Three rules:

- **Coalesce** on a 2 s trailing window, and never more than one recovery refetch per 5 s.
- **Skip** when the outage was shorter than 2 s. Nothing published in two seconds is worth four round
  trips, and the poll interval is measured in minutes.
- **Generation-guard** every load. A recovery refetch can overtake, or be overtaken by, a load already
  in flight; without a generation stamp the older response wins by arriving last, and the reader gets
  a list that matches neither request. This is the same discipline as the note autosave's
  withhold-the-tick rule — *a response is only allowed to land if it is still the answer to the
  current question.*

What recovery must **not** refetch stays as built: the open article. A reader may have been mid-page
for the whole outage, and replacing the text under them is worse than a list that is thirty seconds
old.

#### 20.19.8 Retry is not durability

The reconnect loop protects *reads*. It does nothing for writes, and the gap is currently invisible
because everything is fast and local:

- **Mutations have no outbox.** `SetItemState` and friends are direct RPCs with `WaitForReady(true)`
  and a 20 s deadline. Marking an article read during an outage therefore hangs for twenty seconds,
  rolls the optimistic UI back, and shows "Couldn't save that." The idempotency keys are already being
  sent — the queue that would make them worth something is §12.4's IndexedDB outbox, **specified and
  unbuilt**. Until it exists, an outage silently discards reading state, which is the one thing a
  reader assumes is safe.
- **The signals buffer is RAM only.** `client/track` holds up to 500 events and drops the oldest;
  `pagehide` flushes, and that flush **cannot succeed while disconnected**. Closing a tab at the end of
  an offline session loses the whole session's signals. A34 calls this an outbox and §12.4 puts the
  outbox in IndexedDB; the signals half must land in the same store, with the same cap and the same
  oldest-drop.

Stated as a rule, because it generalises past this app: **a retry loop is a latency optimisation; an
outbox is a durability guarantee.** They are not substitutes, and shipping the first one makes the
absence of the second harder to notice, not easier.

#### 20.19.8a The read half: a cache, and what it is not

Fixing writes alone left an asymmetry that was worse for being half-solved: a reader could mark up an
article during an outage and keep the marks, then click the feed beside it and get a skeleton for
twenty seconds followed by an error — for content the browser had held in memory minutes earlier. Two
changes, and the second is the one that matters:

- **A read started on a known-dead connection is bounded at 4 s, not 20.** `WaitForReady(true)` stays,
  because a call made during a half-second reconnect should wait for it. But the same option applied
  to a real outage means every click hangs for the full call deadline and *then* fails, which is the
  worst possible ordering of those two events. Four seconds is chosen against the backoff schedule —
  retries land at ~0.5 s, 1.3 s, 2.6 s, 4.6 s — so the window contains three or four whole attempts.
  Anything that was coming back has come back.
- **The last answer to each read is kept and served when the transport fails** (`client/data/cache.go`).
  Bounded twice over, by entries and by bytes, because an entry count alone lets a handful of large
  answers fill a quota shared with the outbox and the signals buffer — and those matter more. LRU by
  *use*, not by age: the feed you keep returning to is the one that must survive. Persisted on
  `pagehide` only, never on the read path, for the same reason the outbox is synchronous there and the
  cache is not written on every navigation: a list response is tens of kilobytes and localStorage is
  synchronous.

Three rules make it safe rather than merely useful. **The server is tried first, always** — a cache
consulted first is a cache that serves stale data to a working connection. **The fallback is only
taken on a transport failure** — a `NotFound` is the server answering, and serving a cached copy of a
deleted feed would be the application arguing with itself. **Only what the server said is stored** —
never a cache hit, or an entry refreshes its own timestamp on every miss and the badge starts claiming
an outage-old list is minutes old.

> **This is not §12's trip packs and must not be described as them.** Packs are a deliberate, sized,
> server-built bundle of things you have *not* read; this is only the last answer to each question you
> have *already asked*. It makes "go back to what I was reading" work on a plane. It does not make
> "read today's news" work on a plane, and §12 is still owed.

**The badge is load-bearing** (§12.3's "unmistakable badge"). It carries the age as well as the fact,
because a list from four minutes ago and one from yesterday are the same word and very different
things to act on. Styled as a statement rather than an alarm: nothing is broken, and an alarm here
would train readers to dismiss the one row that must not be dismissed.

#### 20.19.8b Three operations that are refused rather than queued

Queueing is not always the kind answer. `Refresh`, `Subscribe` and `MarkAllRead` return `ErrOffline`
immediately, each for its own reason, and the reasons are worth keeping distinct:

| Operation | Why not queued |
|---|---|
| `Refresh` | The fetching happens on the **server**, over the public internet. A disconnected client cannot merely not-hear the answer — it cannot cause the work to happen |
| `Subscribe` | The server **validates** the feed before anything is stored. An optimistic subscribe would put a row in the rail that might turn out not to be a feed |
| `MarkAllRead` | Mints a **new undo batch per call**, so a replay leaves two batches and an undo offer that reverses half its own work — the one mutation here that is not idempotent |

Each says so in one plain line, in the same frame as the press, and none of them apologises or
suggests retrying: the remedy is "wait", and an instruction the reader cannot follow is worse than a
fact they can. This matters more than it looks, because by the time a reader meets it they have
already watched three articles stay marked and will reasonably assume *everything* is queued.

**Built 2026-07-26** (`client/outbox`, `client/data/outbox_wasm.go`), and three decisions in it are
worth reading before extending it:

- **At-least-once is safe here, and only here.** Everything queued is an *absolute value* — read is
  true, rating is −1, the note body is this text — so applying it twice is applying it once. That is a
  property of these specific mutations, not a licence: **`MarkAllRead` is deliberately not queued**,
  because it mints an undo batch per call and replaying it would leave the undo offering to reverse
  half the work. Anything added later has to answer the same question first. This is also why the
  outbox did not have to wait on server-side idempotency.
- **localStorage, not IndexedDB — a deliberate departure from §12.4.** §12.4 chose IndexedDB for
  *packs*: megabytes localStorage cannot hold. A mutation queue has two properties packs do not — it
  must be readable **synchronously at boot**, before the first render can honestly draw anything, and
  writable from **`pagehide`**, where an asynchronous transaction is not guaranteed to commit before
  the tab is gone. Both are what localStorage does and IndexedDB does not. When the pack outbox lands
  the two may share a store *only* if the pagehide path stays synchronous.
- **`ErrQueued` is not an error.** A caller that rolls its optimistic value back on it would be the
  application discarding a write it has in fact retained — worse than the bug it replaces, because it
  looks deliberate. The rule for callers is the opposite of an error's: keep what you drew, and say it
  is waiting.

**Still owed, and now load-bearing rather than theoretical:** §20.7's idempotency store has a table
(`idempotency_keys`) and nothing that reads or writes it. The client's keys were also *stable per
item* (`"unread-<id>"`), which is a replay hazard the day that changes — mark unread, read, unread, and
the third call is answered from the first one's cached response and silently applies nothing. Keys are
now unique per press; the server half is owed.

#### 20.19.9 What the server owes the connection

- **`KeepaliveEnforcementPolicy`** — §20.19.3. Non-optional the moment the client probes.
- **Readiness-gate the upgrade.** `/grpc` currently accepts a WebSocket while the instance is not
  ready (`/readyz` exists and nothing consults it), producing a client that connects successfully and
  then fails every call — which classifies as `down` and retries hard against a server that is
  already struggling. It should refuse the upgrade with **503 + `Retry-After`**, which is a
  reconnecting client's honest instruction to wait.
- **Close cleanly.** Shutdown is `srv.Shutdown` followed by `a.grpc.Stop()`, and `http.Server.Shutdown`
  **does not wait for hijacked connections** — a WebSocket upgrade is one. So a deploy severs live
  tunnels mid-call rather than draining them. `GracefulStop` under the same 5 s deadline, then `Stop`,
  and a `1001 going away` close frame so the client redials at once instead of inferring a failure.
- **Say `Retry-After` when refusing.** The tunnel's abuse control answers a breached cap with a bare
  `429` and no header, so a client that hits it backs off on a schedule unrelated to when it would be
  welcome back. This one lives in **GoGRPCBridge**, not here.

#### 20.19.10 Making flakiness falsifiable

"It feels flaky" is unfalsifiable without numbers, and this is a self-hosted app with no dashboard
behind it (§20.17). Both halves are cheap:

- **Server**, into the §22.15 metrics and the Settings → Server tab: upgrades accepted/refused, closes
  by reason, idle-timeout reaps, ping-write failures, and current tunnel count.
- **Client**, into the §20.3 ring and Settings → Activity: reconnect count, cumulative downtime, and
  time-since-last-successful-RPC for this session. *Sessions and reconnects, in that order* — one
  reconnect an hour is a network; forty is a bug.

#### 20.19.11 Deliberately not done

- **`MaxConnectionAge`.** Forcing periodic reconnects is a load-balancer rebalancing tool. With one
  server it buys nothing and spends a redial and a refetch per interval, per reader.
- **A polling or SSE fallback for blocked WebSockets.** A corporate proxy that eats `wss://` breaks
  this app completely, and a second transport to survive it is a second implementation of every
  mutation path — precisely what R3 says is the way this design fails. The honest answer is an error
  that names the cause.
- **A hand-rolled watch loop replacing gRPC's reconnect.** §20.4 budgeted for one on CashFlux's
  precedent. It has not been needed: `WithReconnectPolicy` + `Watch`'s `Idle` kick covers it, because
  nothing here holds a blocking server stream — which was the specific thing that defeated the library
  in CashFlux. **Revisit if and when `WatchEvents` (§20.3) lands**, since that is exactly a blocking
  read in flight.
- **Tuning the 20 s cap down.** The countdown plus **Retry now** (§20.19.2) makes the cap a visible,
  skippable wait rather than a hang. Lowering it instead would have every reader's tab hammering a
  server during the minute it takes to come back up.

### 20.20 The splash

`web/index.html`. It exists because the module is **six megabytes gzipped**, and a wordmark sitting on
a dark screen for eight seconds is indistinguishable from a hang.

**Real progress, not a spinner.** The shim streams the fetch through a byte counter and still hands a
`Response` to `instantiateStreaming`, so streaming compilation is preserved. `content-length` is
treated as a *hint* rather than a fact, because the server prefers a precompressed `.gz` while
`res.body` yields **decoded** bytes: the moment the count passes the header the bar drops the
percentage and roams. An honest "4.1 MB downloaded" beats a confident wrong number, and a bar that
sails past 100% is the confident wrong number this avoids. Any failure in the counting path falls
straight back to the plain fetch — progress is a nicety, booting is not.

**It wears the reader's theme.** Themes live on the server (A30), so the splash cannot know one — the
transport is the thing that is loading. Left alone that means a plum flash on every load for someone
running Daylight: a dark flash on a bright screen, forever, which is the one flash a splash exists to
prevent. So `applyAppearance` mirrors four colours into `localStorage` under `af.boot`, purely so this
one frame can be right. It is a rendering *hint* and not state — written from what the server already
said, read one frame before the real values arrive, and a browser refusing storage simply gets the
house theme.

**The design is the product's own.** The progress fill is a gradient through the seven hand-picked
source hues, in order, because the idea this whole reader rests on is that every source owns a hue —
the bar is made of the thing it is loading. Above it the amber marker draws itself beside the wordmark,
the same "you are here" gesture every row uses, making its first appearance at the door. The ground
carries the same fractal noise the application does, so the first frame is already the material.

Three details that are each a decision:

- **The plate is delayed 120ms.** A warm cache boots well under that, and a wordmark that appears and
  vanishes inside the window is a flash — worse than never having shown one. The ground is correct
  either way, so nothing is missing during the wait.
- **It hands over rather than cutting.** Fixed *above* `#app` and faded out over a reader that
  `go.run` has already painted underneath, because `go.run` returns only once the Go program blocks and
  `main` renders before it blocks.
- **Reduced motion is answered by the machine**, because the app's own switch is a server-side
  preference and the transport is what is loading. This is the one screen where the media query is not
  a fallback but the only signal there is.

### 20.21 Focus mode

The reading pane takes the window: `w`, or the control pinned to the top right of the article. One
attribute — `data-focus` on `.shell` — and the whole of it is CSS (`client/design/focus.go`).

**The columns close; they do not vanish.** `display: none` cannot be animated, and the difference
matters more here than usual: those two panes *are* the navigation, and something that disappears with
no transit leaves the reader unsure whether it was hidden or lost. They are grid **tracks**, so closing
them is animating four track widths to zero, which browsers interpolate as long as the track *count*
does not change. The direction of the motion is the explanation.

That constraint is why there are three rules and not one. The layout already redefines
`grid-template-columns` at 1220px (three tracks) and 900px (one), and a five-track value against a
three-track layout **snaps** — the exact thing being avoided. Each breakpoint gets a value with its own
count, in narrowing order so the last matching one wins. On a phone the tab bar goes too: "focus" that
leaves a navigation bar on screen is a setting that did not do what it said.

**Full width is the means, not the point.** A 66-character column pinned to the left of a 1900px window
is worse than the three-pane layout it replaced — a stripe of text and an acre of nothing. The
article's gutters grow to whatever centring takes, expressed as **padding** rather than a `max-width`
because padding interpolates from the 60px it already has and a max-width would have to animate from
`none`. The nav, the note and the seams between articles move with it, or the column arrives centred
with its own furniture still hugging the left. The source wash comes down to half: it is a gradient
sized to the article's box, so the same declaration is atmosphere at pane width and a tinted slab at
1600px — and it is dimmed by *opacity*, because a gradient's colour stops do not interpolate and
changing the mix would snap the wash at the moment everything around it is sliding.

**The control is the one piece of bespoke iconography in the application.** Everything else is a text
glyph, deliberately: a row of drawn icons is a toolbar and this reader is not one. This earns the
exception because its meaning *is* a geometry — four corner brackets moving apart, at 16px, which no
character says as plainly — and because the brackets carry the state without a label, which is what
lets it be a 34px square that never covers the prose it floats over. They rest pulled **in** when the
columns are open and spring out under the pointer, a preview of the gesture; in focus mode they rest at
the full box and pull in. It sits on a **zero-height sticky perch** so it pins to the top of the pane
without the design growing the top bar it deliberately does not have, and it is translucent with a blur
because a solid chip punches a hole in the paragraph behind it.

It is a **persisted preference** (`ui.focus`), which is only safe because the way out never leaves the
screen: the control stays pinned, and `Escape` peels focus mode first — "back to the list" is a dead
key while the list is closed.

### 20.22 The filmstrip (§20.11, ≤1220px)

Below 1220px the reading pane replaces the list, and below 900px one column holds
whichever pane the tab bar has chosen. Both were `display: none` — **a hard cut on
the most-used navigation in the application.** On a phone, opening an article,
going back to the list and reaching the rail are the entire interaction, and every
one of them was a frame-to-frame replacement with nothing to say which direction
the reader had gone. Two panes swapping instantly is indistinguishable from the
application having been replaced.

The panes are a strip now: rail, list, article, in the order they are already in,
so the direction of travel carries the meaning without anyone being taught it —
deeper moves left, back moves right, the same as a stack of paper. Each pane knows
its own position (`--pane`), the shell knows which position is showing
(`--strip`), and every offset is one expression: `(--pane - --strip) * 100%`.

Three things follow that are worth stating because each was a decision:

- **They are laid out at every width now**, because a transform cannot animate an
  element that is not being laid out. On a phone the rail lays out its 151 rows
  while the reader is in an article — the same work the desktop does at every
  width, once rather than per frame, after which the strip is a compositor
  transform. In exchange the panes **keep their scroll positions across a switch**,
  which `display: none` does not reliably do, and which is the difference between
  returning to the list where you left it and returning to the top of it.
- **`visibility`, with a delay equal to the slide.** A pane that is merely
  translated off-screen is still in the accessibility tree — a screen reader would
  read all three as one long page. Delaying the hide until the slide finishes is
  what lets the outgoing pane stay drawn for its own exit.
- **Three rules, not one.** The layout already redefines the columns at 1220px and
  900px, and a five-track value against a three-track layout does not interpolate.
  Each breakpoint gets a value with its own track count.

Two things the e2e suite had to be taught, both of which are the suite encoding a
superseded truth rather than a bug:

- **"No horizontal overflow" now means "nothing UNCLIPPED sticks out."** The sweep
  used to flag any box whose right edge passed the viewport, which was safe while
  nothing was ever off-screen. Two panes now sit a full viewport to the side on
  purpose. The rule became: an element is fine if some ancestor clips horizontally
  *and that ancestor stays inside the viewport* — the question the test always
  meant, which is whether the page can be scrolled sideways. It still catches the
  long-unbroken-URL case it was written for, because `.pane` sets only
  `overflow-y` and therefore computes `overflow-x: auto` — scrollable, not
  clipping.
- **The reading pane finally measures itself on a phone.** It was `display: none`
  below 900px until the view changed, and an element with no layout has no scroll
  metrics — so the continuous stream (A28) never appended there. It does now,
  which is a fix rather than a regression, and it is why `.article h1` became a
  strict-mode violation at phone widths as well as desktop ones.

### 20.23 Dialogs, which are the only gesture that has to run backwards

All six overlays — palette, shortcut sheet, per-feed panel, tag panel, add-a-feed,
category editor — answered `if !open { return nil }`. That is correct, and it is
also why they could only ever animate in one direction: an element unmounted the
instant it closes has nothing left to animate. Six dialogs, six hard cuts, on the
key a reader presses to *get out* of something.

The scrim is rendered at all times now and carries `data-open`
(`client/view/modal.go`). Because the transition a browser runs is always the one
belonging to the state being moved **to**, the entrance and the exit can differ
without a line of Go knowing about it — and they do: arriving takes its time
(0.18s/0.3s), leaving is brisk (0.11s/0.18s). A dialog that lingers on dismissal
feels like the application is reluctant to let go.

`visibility: hidden` rather than `opacity: 0` alone, so a closed dialog is out of
the accessibility tree and out of the tab order. Two consequences worth writing
down, because both bit on the way in:

- **`.pal-scrim` is no longer a unique selector.** Six of them are in the document
  at all times, so a bare `.pal-scrim` resolves to whichever comes first.
- **The status banner had the same shape and got the same treatment**, by a
  different technique: it is wrapped in a grid whose single row goes `0fr → 1fr`,
  the only way to animate a collapse to the content's *own* height without
  pinning it open. Affordable for one line of text; declined for the rail's 151
  feed rows, where keeping them laid out to animate their removal is the exact
  cost the fold exists to avoid.
- **Anything that focuses a field inside a dialog must retry until the focus
  LANDS, not until the element exists.** `.focus()` on anything inside
  `visibility: hidden` is a silent no-op, so `platform.FocusField` found the
  palette's input on the first frame, focused nothing, and stopped — and the
  palette opened without a cursor in it, which killed Escape and the arrow keys,
  because the palette owns those only while its own field has focus.

---

## 21. Security

- **Auth** — `WithAuthorize` at the upgrade *plus* a per-RPC interceptor. Argon2id (tuned),
  device-scoped refresh-token families with rotation and **reuse detection**.
  **Shipped 2026-07-26 (§7.1a, A36):** `AuthService`, hashed revocable sessions, attempt limiting, and
  the fix for the worst hole this project has had — `DevMode` was *derived from a loopback bind*, which
  meant the standard nginx-forwards-to-127.0.0.1 deployment served the first user's superadmin scope to
  the internet. It is now an explicit `-dev` flag, default off, **refused on any bind but loopback**.
  Still owed: `WithAuthorize`, the interceptor's capability map, refresh families, lockout.
- **Tunnel origin** — `WithAllowedOrigins` is applied when `-origin` is set, and only then: an empty
  allowlist would reject every browser rather than fall back to the library's same-origin default. That
  default compares `Origin` against `Host`, which is correct **as long as the proxy forwards `Host`
  faithfully** — so a production instance sets `-origin` explicitly rather than depending on someone
  else's nginx config.
- **HTML sanitising** — `internal/sanitize`, five named policies over GWC's engine (which owns the
  parse, the allowlist walk, the scheme check and the mutation-XSS hardening, and is not reimplemented).
  What GWC has no opinion about is *where the HTML came from*, and that is the whole question: `Feed`
  keeps images because a hardware review is mostly photographs; **`Newsletter` drops remote images
  outright** — a tracking pixel reports that you opened a message, when, how often and from which IP,
  and proxying it still tells the sender it was opened; `Archived` is our own extraction output, trusted
  more but still publisher bytes; `Public` is text and emphasis only, for someone who subscribed to
  nothing; `Note` is the reader's own text, still sanitised because people paste. A single policy tuned
  between these would be too strict for feeds and too loose for newsletters.
- **Single-factor compensations** (§7.3) — rate limiting and lockout, breached-password rejection,
  sudo mode, an identity gate in front of `/admin`. Without 2FA these *are* the control set.
- **Enumeration resistance** — uniform login errors, hash always run, reset-initiate answers
  identically for existing and missing accounts.
- **Authorization** — capability map **fails closed**; the sync API uses the same map, with
  **hard-capped token scopes that can never carry admin capabilities** (§15.2).
- **Tenant isolation** — §6.1. The two-tenant leak test is the highest-value test in the suite, and it
  must exercise `shares` specifically (§6.7).
- **A22 deletion safety** — global rows are never hard-deleted, so no single-tenant action can destroy
  another tenant's state.
- **Private-mail isolation** — mailbox sources are per-user keyed (§6.4) and can never be shared,
  ranked across users, or reach a public feed.
- **TLS** — startup refuses a non-loopback bind without TLS *and* a credential.
- **SSRF** — reject non-`http(s)`; resolve and reject loopback, RFC1918, link-local, ULA, metadata
  addresses; **re-check after every redirect**, including 301 chains (§15.4). One guard used by
  `fetch`, `assetproxy`, `pageproxy`, `render`, `extract`, `discover`, `scrape`, pack building, **and
  outbound webhooks**. *(Rev 9 renames: `imgproxy` → `assetproxy`, `shot` → `render`, and adds
  `pageproxy` — §10.1a–d.)*
- **Proxy egress (§10.1b)** — the asset proxy and the page proxy **only fetch URLs the server itself
  chose**: a capability is minted solely for a URL found in stored HTML the caller was allowed to
  read, and an unsigned request is refused before any work happens. The §21 guard stops SSRF; the mint
  gate is what stops the instance being used as a general egress proxy by someone who has a login.
- **Proxy origin isolation (§10.1b)** — proxied HTML is served from a **separate hostname**, never the
  app's, with a `default-src 'none'`-class CSP, `nosniff`, and a sandboxed iframe carrying neither
  `allow-scripts` nor `allow-same-origin`. The headers are the fast rejection; the origin boundary is
  the guarantee.
- **Proxy URLs** are short-TTL HMAC capabilities over `(scope, item, asset, exp)` — minted by an
  authenticated RPC, verified at the edge, never the session token. Same rule and same reason as pack
  URLs and `/speech`: an `<img src>` ends up in history, referrers and logs.
- **The renderer (§10.1c–d)** is a browser executing hostile pages on the instance. It runs with a
  disposable profile, no access to the data directory, one render at a time through the job queue, a
  hard timeout, and **never** with a logged-in session of anything.
- **XSS** — `sanitize.Sanitize` at ingest *and* render across feed, extracted, archived, scraped,
  packed, **newsletter**, and **public-feed** content. Newsletters get the strictest policy.
- **Public share feeds** — unguessable rotatable slug, excerpt-only, independently rate-limited,
  `noindex` by default.
- **Mailbox credentials** encrypted at rest; app-specific passwords preferred.
- **Pack URLs** authenticated, tenant-scoped, short-TTL signed.
- **`/healthz`, `/readyz`** are unauthenticated and therefore **deliberately information-free** — a
  status code and nothing else. No version, no counts, no tenant data (§22.4).
- **Tunnel hardening** — `WithAllowedOrigins`, `WithReadLimitBytes(4<<20)` (**a deliberate tightening;
  the library default is 16 MiB**), `WithKeepalive`, and the three connection/upgrade caps.
- **WebSub callback** — verify the challenge and **validate the HMAC on every push**.
- **LLM egress boundary** — §18.1's allowlist, enforced in `internal/llm` and tested as a security
  property.
- **Impersonation** — audited on entry and exit, persistent banner while active.

---

## 22. Operations and lifecycle

Rev 7 described a product, not a running system. This section is the correction.

### 22.1 Migrations (A23)

Twenty-five milestones add tables and columns to a database that is **live with tenant data from M2
onward**. There must be a mechanism, named now:

- **Numbered, forward-only** SQL files (`0001_init.sql`, `0002_item_tags.sql`, …), applied in order at
  boot inside a transaction, recorded in `schema_migrations(version, checksum, applied_at)`.
- **Checksum-guarded** — a changed already-applied migration aborts startup rather than diverging
  silently.
- **Every milestone that touches schema ships its migration with it.** "M11 adds `api_tokens`" is not
  a plan item unless it includes `00xx_api_tokens.sql`.
- **No down-migrations.** Rolling back a schema on a live single-file DB is how you lose data; the
  rollback path is *restore the pre-migration snapshot* (§22.5), which the migrator takes
  automatically before applying anything.
- Tool: `golang-migrate` or `goose`, or ~150 lines of our own — the mechanism matters more than the
  dependency.

### 22.2 SQLite concurrency (A24)

The architecture has **four concurrent writers**: HTTP handlers, gRPC handlers, the poller, and
background jobs. Rev 7 never said how they coexist, which means `SQLITE_BUSY` on day one.

- **`journal_mode=WAL`** — readers don't block the writer, which is most of the problem.
- **`busy_timeout=5000`**, `synchronous=NORMAL`, `foreign_keys=ON` (**required**, or every
  `REFERENCES` in §6 is decorative — SQLite defaults it off).
- **A single serialized writer**: one `*sql.DB` capped at `SetMaxOpenConns(1)` for writes, with a
  separate read pool. Fighting for the write lock is worse than queueing for it.
- **Long jobs never hold the write lock** — extraction, pack building, and audio synthesis do their
  work outside a transaction and take the lock only to commit.
- Checkpoint WAL on a schedule so it doesn't grow unbounded.

### 22.3 First run — the bootstrap gap

Rev 7 had **no way to create the first user.** Invites require an admin (§9); recovery rung 2 requires
an admin; break-glass reset requires an existing user. Day zero had no path in.

`articleflux init` — or first-boot detection of an empty `users` table — creates tenant 1 and the first
superadmin, either interactively or by printing a **one-time enrollment token** valid for 15 minutes,
logged loudly. The server **refuses to serve the app** while no superadmin exists rather than starting
in a state where anyone who finds it can claim it.

Also at boot: **validate config and fail loudly** — TLS files readable, bind address consistent with
credentials (§4), storage path writable, LLM keys well-formed if present, IMAP reachable if configured.
A config error should stop startup with one clear line, not surface as a mystery at 3am.

**Shipped 2026-07-26.** `articleflux init -user … -password …` creates tenant 1 and its superadmin and
**refuses to run on a populated instance** — `init` on a live box is nearly always someone re-running
the setup steps, and silently adding a second superadmin is worse than an error. It points at `adduser`
and `passwd` instead. There is no one-time enrolment token: it would be a second bootstrap path to
secure, and filesystem access is already the proof of ownership every rung here rests on (§7.2). The
password comes from the flag, then `ARTICLEFLUX_PASSWORD`, then a terminal prompt — in that order, so a
password never has to appear in a process listing or a shell history.

### 22.4 Health and readiness

`/healthz` (process alive) and `/readyz` (DB open, migrations applied, poller scheduled) —
**unauthenticated**, because a supervisor, a Docker healthcheck, and the Cloudflare Tunnel of D8 all
need them before any session exists, and **deliberately information-free**: a status code, nothing else.
§9's rich health screen stays authenticated.

**Shipped 2026-07-26.** Both endpoints exist and the difference between them is the entire point:
`/healthz` **does not touch the database**, because a liveness probe that fails on a slow query gets the
process killed and restarted into the same slow query; `/readyz` runs `SchemaVersion` under a 2-second
timeout and answers `503 unready` if it cannot. One word each.

**`app.Preflight` refuses to listen at all when the instance cannot work**, and returns a *joined*
error rather than the first one, because someone who has just set up a droplet usually has more than
one of these wrong at once and a one-at-a-time boot loop is a miserable way to find that out. Three
checks, each a way this has actually gone wrong:

| Check | The failure it prevents |
|---|---|
| **An account exists** (unless `-dev`) | A login screen nobody can get past — a bricked deploy that looks like a working one, with `/healthz` green. The fix is one command, but only if you are told what it is |
| **`webRoot/index.html` exists** | The server 404s the app while the health check passes, so an uptime monitor reports green |
| **The data directory is writable** | Surfaces otherwise as a SQLite error on the first *write*, minutes or hours after start. Probed by **writing and removing a file**, not by `stat` — a directory can be listable and not writable, and SQLite needs to create the `-wal` and `-shm` siblings, not just open the database |

Still owed from the list above: TLS files, LLM key shape, IMAP reachability.

### 22.5 Backups that actually restore

"Nightly, N retained" was a promise with no mechanism. **A raw file copy of a live WAL database is a
torn snapshot.**

**`VACUUM INTO '<path>'`** — SQLite's online backup, safe against concurrent writers, producing a
compact consistent file. Nightly plus **before every migration** (§22.1). Retain N, verify each by
opening it and running `PRAGMA integrity_check` — an unverified backup is a hope. **A documented,
actually-executed restore drill** before M13; the first restore attempt must not be during an incident.

**Shipped 2026-07-26** — `store.Backup` / `store.PruneBackups` / `articleflux backup -out <file|dir>
[-keep N]`. `VACUUM INTO` takes a read transaction for the whole operation, so what lands is one point
in time; `cp` of the three WAL files copies them at three different instants, and a writer in between
produces a backup that **opens cleanly, passes a smoke test, and is wrong** — which is the worst kind
of broken. The copy is then opened and integrity-checked before it counts.

Three details that each protect a real failure: the write goes to `<dst>.partial` **in the same
directory** and is renamed at the end (an interrupted backup otherwise leaves a truncated file under
the name of a good one, and the retention sweep would happily delete a real backup to make room for it;
same directory so the rename is atomic rather than a cross-device copy); **backups never overwrite**,
which is `VACUUM INTO`'s own rule and the right one; and a **failed prune does not fail the command**,
because the backup succeeded and a cron job should not report failure for a night that was actually
backed up.

Still owed: the automatic pre-migration snapshot (§22.1 promises one and `Migrate` does not take it),
any scheduling — `-keep` exists so a cron line is one command, but nothing schedules it — and the
restore drill itself.

### 22.6 Running out of disk

`content_html` + extractions + archives + embeddings + engagements + packs + revisions + **generated
audio** grow monotonically. A full disk on SQLite is `SQLITE_FULL` mid-write.

**A degrade ladder, by watermark**, so failure is graceful and legible rather than opaque:

| Free space | Behavior |
|---|---|
| < 20% | Warn in admin; stop new audio synthesis, tier-2 snapshots and **new renders (§10.1c)** |
| < 10% | Stop pack building and image caching **including the asset proxy cache**; aggressive retention sweep; notify admins |
| < 5% | **Read-only mode for content ingest** — polling pauses, but reading, marking read, notes, and the sync API keep working |
| < 2% | Refuse all writes but the outbox drain; loud banner |

Core state (read/star/tags/notes) is the **last** thing shed, because it's the irreplaceable part.

### 22.7 The job queue and poller backpressure

Rule fan-out (§13.2), ranking, extraction, pack building, and audio all belong on a **queue with
bounded concurrency**, not inline with whatever triggered them.

- Durable enough to survive restart (a `jobs` table is fine at this scale — no external broker).
- **Per-kind concurrency caps** so pack building can't starve rule fan-out.
- **The poller uses a priority queue by staleness ratio** (how late relative to its own interval), not
  FIFO — otherwise one slow batch permanently penalizes everything behind it.
- **When chronically behind**: widen intervals proportionally rather than falling further behind
  silently, surface **poller lag** in §9, and alert past a threshold. Rev 7 showed queue depth, which
  is observability; this is a policy.

### 22.8 LLM timeouts and circuit breaking

Smart+ and discovery fail soft (§18, §11), but rev 7 said nothing for translation (§10.5) or rule
drafting (§13.4/§14.2). On a shared multi-tenant server, a wedged provider ties up goroutines and
connections **for every tenant** — a noisy-neighbor failure distinct from per-feature degradation.

**One shared client in `internal/llm`**: hard per-call timeout (30s), bounded in-flight calls, and a
**circuit breaker** that opens after N consecutive failures and half-opens on a timer. Every
LLM-calling feature degrades to its non-LLM path when the circuit is open. Breaker state is visible in
§9 so "AI features are off" is answerable rather than mysterious.

### 22.9 Time, timezones, and DST

- **All timestamps stored as UTC unix integers.** No exceptions, no local-time columns.
- **Scheduling windows** — quiet hours, screensaver idle triggers, digest windows — are stored as
  **local wall-clock plus an IANA timezone** (`users.timezone`), resolved at evaluation time. A baked
  UTC offset shifts a "22:00–07:00" quiet window by an hour twice a year.
- **Feed dates are frequently wrong** — future timestamps, epoch zero, missing timezones. Clamp
  `published_at` to `[first_seen_at - 10 years, first_seen_at + 1 day]`; a feed claiming 2087 must not
  pin itself to the top of every list forever.
- **Client clocks are never authoritative** (A25) — display only.

### 22.10 Client/server version skew

§12.3 deliberately SW-caches `app.wasm` so the app boots offline. That is exactly the setup for a
**months-stale client with old proto stubs talking to an upgraded server**, from a device that hasn't
been online to pick up a new shell.

- The client sends its **build stamp in the tunnel handshake**; the server compares against a
  **minimum supported client version**.
- Below minimum → the connection is refused with a distinguishable status, and the client **purges the
  SW cache and hard-reloads** rather than retrying forever. *"Rather than retrying forever" is not
  self-enforcing:* skew is a **terminal** classification in §20.19.6, and without that entry the
  refusal is indistinguishable from an outage and the reconnect loop grinds against it.
- Within a compatible range → allowed, with a non-blocking "update available" toast.
- The stamp is baked at build time and surfaced in §9, so "what version is that device on?" is
  answerable.

### 22.11 Logging

Structured (`log/slog`), leveled, to stdout — a supervisor or container runtime handles rotation. One
request/RPC id threaded through handlers, jobs, and the poller so a failure is traceable across the
queue boundary. **Never log**: passwords, tokens, recovery codes, mailbox credentials, note or article
bodies, or LLM payloads. Feed URLs and item titles at debug only — they're personal.

### 22.12 Performance budgets

Targets, so "is this fast enough" has an answer other than an opinion. Each is a CI-checkable number
on the reference box (the D8 home server, not a workstation).

| Budget | Target | Fails when |
|---|---|---|
| **Wasm boot to first paint** | < 1.5s warm, < 4s cold | The bundle grew (G5) or the shell isn't SW-cached |
| Home render, 50 items | < 120ms after data | Row memoisation broke (R4) |
| List scroll, 5k items | 60fps sustained | `VirtualList` keying or a non-fixed row height |
| **Unread-by-folder query** | < 30ms at 50k × 3 users | The §6.5 indexes stopped being used (G3, R2) |
| Poll throughput | ≥ 200 sources/min, 8 workers | Per-host semaphore starvation or a slow guard |
| Fan-out | ≥ 2k subscriber-items/sec | Doing work inline that belongs in a job |
| Full ranking recompute, one user | < 5s at 50k items | Topic clustering not incremental |
| Extraction | < 2s p95 per article | The library choice (D7) |
| Pack build, 500 items at `text` | < 90s | Extraction not parallel |
| Server RSS at rest | < 400 MB | A cache without a bound |
| Event fan-out latency | < 250ms poll→browser | Coalescing window too wide or ring contention |

**Two of these are load-bearing rather than nice:** the unread-by-folder query, because it's the app's
hottest path and R2 says it changed shape twice; and wasm boot, because A5 spent the bundle budget on
gRPC and G5 is the gate that decides whether that was affordable.

### 22.13 Browser support

The design and the platform layer between them require: WebAssembly, IndexedDB, Service Worker,
`BroadcastChannel`, `color-mix()`, `backdrop-filter`, variable fonts, `env(safe-area-inset-*)`,
`100dvh`, and `navigator.wakeLock`.

**Baseline: the last two versions of Chrome, Edge, Firefox, and Safari.** No IE, no polyfills, no
transpilation — this is a self-hosted tool for one household, and pretending otherwise costs real
effort for nobody.

**Two platform facts that change features, not just styling:**

- **iOS Safari only delivers Web Push to a home-screen-installed PWA.** §17.1's notifications simply do
  not exist on iPhone until the user adds ArticleFlux to their home screen. The UI must say that rather
  than offering a toggle that silently does nothing — and it's an argument for the manifest and install
  prompt landing with M12, not later.
- **`navigator.wakeLock` is absent on some Safari versions.** §19's screensaver degrades to "the screen
  may sleep" rather than failing; it should not be gated on the API existing.

`prefers-reduced-motion`, `prefers-color-scheme`, and keyboard-only operation are baseline everywhere.

### 22.14 CI — the safety net for agentic implementation

Under waterfall-then-implement, CI is what stops a plausible-looking change from quietly violating a
decision. Every gate below maps to something this plan already promised:

| Check | Enforces |
|---|---|
| `go vet` + `staticcheck` | — |
| `go test ./...` | Everything, incl. **T1–T20** |
| **No `db.Query`/`db.Exec` outside `internal/store`** | §6.1 structural tenant safety |
| **No `syscall/js` outside `client/platform`** | A26 |
| **No `.css` file in the tree** | A26 |
| **Every repository method takes `Scope` first** (reflection test) | §6.1 |
| **Every RPC has a capability-map entry** | §7.5 fail-closed |
| `buf lint` + **`buf breaking` vs main** | §20.7 additive-only |
| Migrations apply to empty **and** to the previous release's schema | T7 |
| **`app.wasm` size ratchet** — fail if it grows >5% without an explicit baseline bump | G5, R4 |
| Playwright e2e, **Windows-native** (no Docker/WSL) | — |

The wasm ratchet matters more than it looks: bundle growth is invisible per-commit and fatal
cumulatively, and A5 is the decision it would silently invalidate.

### 22.15 Observability

OpenTelemetry is already in the dependency tree. **No external collector is required** — metrics are
exposed in-process and read by the §9 admin health screen; OTLP export is opt-in config for anyone who
wants it elsewhere.

**Metrics that answer a question someone will actually ask:** poll lag (p50/p95) · job queue depth
*by kind* · fan-out items/sec · event ring depth **per tenant** (§20.3's starvation risk) · LLM spend
and breaker state · SQLite write-lock wait time (§22.2's single writer, and the first thing to look at
when everything feels slow) · disk headroom against the §22.6 watermarks · sync-API requests per token.

**One request id threads handler → job → poller**, which is the only way a failure that crosses the
queue boundary is traceable at all.

### 22.16 Internationalisation

GWC ships an `i18n` package. **UI strings go through it from M4, even though only English ships.**
Retrofitting extraction across ~50 pages and ~90 settings later is miserable and always gets deferred
forever; doing it from the first component costs almost nothing.

Three things are separate and worth not conflating:

- **UI localisation** — the app's own strings. Extracted now, translated whenever.
- **Content translation** (§10.5) — translating *articles*. Unrelated machinery, LLM-backed, opt-in.
- **Locale formatting** — dates, numbers, relative times. **Applies immediately even in English**,
  because `users.timezone` and locale already exist and a reader is full of timestamps.

**RTL** is handled by §10.2's direction setting rather than a separate effort, and the design's
logical-property usage (`padding-inline`, not `padding-left`) is what makes that cheap.

#### 22.16a What shipping it actually taught (2026-07-26, TODO 8.4a)

The UI goes through GWC's `i18n` **using its API, not around it**: `i18n.Provider`
puts a `Runtime` in context, `UseI18n` reads it back, `Runtime.T(namespace, key)`
looks up and interpolates, `Runtime.NS` binds a namespace for a helper that reads
many keys from one surface, and `i18n.UseLocale` is the reactive, persisted locale
state. There is no hand-rolled accessor. Switching language re-renders; it does not
reload.

A first pass DID hand-roll a package-level `T`, justified as "translation happens
inside loops over 3,600 rows, and hooks are positional". **That reason was wrong** —
you call the hook once at the top and use the returned Runtime inside the loop — and
it is recorded here because the correct constraint is a different one that still
shapes the design:

**This view layer renders through plain functions, not nested components.** `grep -c
ui.CreateElement client/view` returns three. Four functions can legally hold a hook
(`Root`, `Login`, `Reader`, `railPane`); the other ~70, holding most of the 527 call
sites, are ordinary Go — `feedSettingsBody`, `helpSheet`, `connLabel`,
`humanDuration(int64)`, `thousands(int)`, `themeLabel(design.Theme)`. A further 47
call sites are inside `go func()` in Reader, running after render returns. So the
Runtime is **threaded as an explicit first parameter** through those helpers, and
converting them into components instead would add ~70 fibers per render to a tree
that virtualises 3,600 rows precisely to avoid that.

Four findings from doing it, each of which will bite whoever touches this next:

**The Provider must live in a component that re-renders rarely — it lives in `Root`.**
When a Provider fiber re-renders, the reconciler compares old and new context values
and calls `markSubtreeNeedsUpdate` on the whole subtree if they differ; that flag is
checked in `buildFiberNeedsWork` **before** any props comparison, so it defeats every
memo bailout below it. `i18n.Runtime` is a struct of func fields, so it is not
`Comparable` and `fastEqual` falls through to `reflect.DeepEqual`, where two non-nil
funcs are **never** equal. The context value therefore reads as "changed" on every
render of whatever component mounts the Provider. That is exactly right for a
language switch and a disaster anywhere else. `Root` has two pieces of state — the
auth phase and the locale — and both are events the whole screen should redraw for.
**Mounting the Provider inside `Reader` would mark the 151-row rail and the
virtualised list dirty on every keystroke.**

**For the same reason, a Runtime must never go on a props struct.** `railProps` is
compared by value to stop 151 rows re-rendering; a func field makes that comparison
either impossible or always-false. Hence the parameter.

**`UseLocale` clamps to its supported set at boot, before any translation is
imported** — so the language list lives in `client/i18n`, not beside the translator in
`internal/smart`. A supported set derived from "what is registered in the bundle"
would be `["en"]` on every cold start, and a reader who chose French would be reset to
English before the fetch that would have registered French even began.

**Anything rendered outside the Provider cannot see it.** `Root` builds its `child`
value before handing it to `Provider`, so `bootSplash` had to become a mounted
component rather than a function called inline — otherwise it reads an empty catalog
and renders raw keys.

Two costs, both accepted:

- **Importing GWC's `i18n` costs 221 KB gzipped** (5.96 → 6.18 MB against G5's
  ratchet, R4), entirely `x/text` via `language.Parse` in `NormalizeLocale` and
  `message.NewPrinter` in `FormatNumber`. GWC's plural rules are hand-rolled and
  free; `client/i18n.Number` already avoids `FormatNumber` with a separator table,
  but a package-level import links the tables regardless. **The fix belongs in GWC** —
  `NormalizeLocale` is BCP-47 canonicalisation of a tag we control and needs no CLDR —
  and would remove the cost for every GWC app.
- **A forced re-translation reloads the page.** It changes no state: same locale, same
  props, so the context value never moves and the memoised panes keep the strings they
  already rendered. Only the Provider can invalidate them, and it is in `Root`, which
  `Reader` cannot reach. It is a repair action taken almost never, so one reload beats
  plumbing a revision counter up through the component that exists to re-render rarely.

**The audit that followed "zero hardcoded copy" (2026-07-26).** The guard reported
zero and that was never the same as "everything": it only sees `client/view`. A sweep
of every surface a reader can read found four gaps, all now closed.

- **`relTime`** — the most-rendered string in the app, on every list row and every
  article eyebrow — was `t.Format("2 Jan")`. **Go's layouts are not locale-aware and
  never will be**, so month names were English in every language. Now `month.1`–`.12`
  plus a `time.dayMonth` pattern, so a locale that writes the month first can reorder
  it. The unit abbreviations reuse the `unit.*` keys the settings screen already uses,
  so "5m" cannot become two different translations of one idea.
- **`internal/tagglyph`'s fifty glyph names and seven group headings** — the
  `aria-label` and tooltip on every cell of a grid of *unlabelled symbols*, which makes
  them matter most to exactly the readers who cannot tell ◆ from ◈ at 13px. Keyed by
  the character, because the character is the stable identity; `tagglyph` keeps its Name
  field as the fallback.
- **`web/index.html`'s splash** — five strings shown *before the wasm module exists*, so
  they cannot read a Go catalog at the moment they are needed. Solved by the mechanism
  this file already established for the theme: the running app mirrors them into
  localStorage on every language change (`mirrorBootCopy`), and the shim reads them one
  frame before Go takes over. The English stays in the markup as the fallback for a
  first-ever load, a storage-refusing browser, and no-JS.
- **gRPC status messages** — every refusal the reader saw was prose composed on the
  server and rendered verbatim, and **the server cannot translate them**: the language
  is a per-device choice in localStorage that the server never sees. So the server now
  sends a KEY plus arguments in an `articleflux.v1.ErrorDetail` alongside the English,
  and `view.serverText` resolves it against the same catalog as everything else. The
  English message stays on the status on purpose — it is what the two consumers with no
  catalog get, the Google Reader sync API (§20.7) and curl.

Two contracts follow, and both are tested rather than trusted. `TestEveryServerErrorKeyExists`
walks `internal/transport/grpcsrv` for `errKey` calls and fails if a key is missing from
the catalog or outside the `srv` namespace — the key crosses the wire as a string, so
nothing in the type system connects the two halves. `TestServerErrorKeysMatchTheirEnglishFallback`
fails if the wire's English and the catalog's English drift, because each half is
correct on its own and the divergence is otherwise invisible.

What stays untranslated, and is not a gap: **feed content** — titles, summaries, authors,
bodies. That is §10.5's job and a different machine. And **`{err}` interpolations**: the
sentence around a transport failure is translated, gRPC's own socket text inside it is
not, because paraphrasing the actionable half helps nobody.

**`client/i18n` carries no build tag, deliberately.****`client/i18n` carries no build tag, deliberately.** `client/view` is `js && wasm` and
cannot be linked into a native test binary; the catalog can, which is what makes
`keycoverage_test.go` and `provider_test.go` possible — and, unplanned but decisive,
what lets the *server* read the English catalog in order to translate it (§10.5a).

**The ratchet is a guard plus tests, not a convention.** `internal/tools/guards` gained
a fifth check — no hardcoded user-facing copy in `client/view` — passing at zero across
all eleven files. `client/i18n` adds: a referenced key that does not exist, a registered
key nothing uses, malformed placeholders, plurals missing `other`, and six tests that
render the real Provider/`UseI18n` path through `ui.RenderToString` natively and assert
that English resolves, plurals select by locale, an imported catalog renders, missing
keys fall back to English rather than to raw identifiers, and `Import` refuses to
overwrite English. Keys built at runtime from a stable id (`tr.T("theme", t.Name)`) are
invisible to the static checks, which is why `themeLabel` and its siblings fall back to
the source package's own label rather than trusting the catalog.

#### 22.16b Rules for adding UI copy

The section above is why. This is what to do, and it is prescriptive: every rule below is
enforced by a guard or a test, and the enforcement is named so you can see what will fail
before it fails.

**Rule 0 — a call site and its catalog key land in the SAME commit.** This is the one that
gets broken, because adding copy is the interesting half and registering it is not. A key
referenced but not registered renders the identifier to a reader (`list.staleNote` on
screen, in a box, in production). `TestEveryReferencedKeyExists` fails on it;
`TestNoOrphanedKeys` fails on the reverse.

**Where a string comes from, by situation.** Four shapes, and there is always one that fits:

| you are in | do this |
|---|---|
| a component (`ui.CreateElement`-mounted) | `tr := i18n.UseI18n()` **once**, at the top with the other hooks |
| a plain helper that returns `ui.Node` or `string` | take `tr i18n.Runtime` as the **first** parameter |
| a helper reading many keys from one surface | `ns := tr.NS("feedSettings")`, then `ns.T("key")` |
| outside a render — a `UseEffect` body, a goroutine, a mirror to non-Go | `i18n.At(locale)` |

`UseI18n` is a **hook**. Once, unconditionally, at the top. GWC matches hooks positionally,
so one behind a branch or inside a loop binds to the wrong slot. Pass the Runtime down; do
not call the hook again.

**Never do these three.** Each has bitten once already:

- **Never put a `Runtime` on a props struct.** It carries func fields, so the struct is not
  comparable and GWC's memo bailout either breaks or never fires — which is the bailout
  keeping 151 rail rows and 3,600 virtualised items from re-rendering. Pass it as a
  parameter.
- **Never mount `i18n.Provider` anywhere but `Root`.** A Provider re-render marks its whole
  subtree dirty *before* props are compared, and the Runtime's func fields make the context
  value read as changed on every render of whoever mounts it. In `Root` that is correct —
  its only state is the auth phase and the locale, both redraw-everything events. In
  `Reader` it would mark the rail and the list dirty on every keystroke.
- **Never branch on translated text.** `strings.HasPrefix(label, "Unread")` is a bug in
  every language but one. Branch on the flag that produced the label.

**Plurals go through the catalog, never through `if n == 1`.** Register with `plural(...)`
and call `tr.T("list", "readingTime", i18n.Count(n))`, or `i18n.CountWith(n, args)` when the
message also interpolates. English distinguishes two forms; Polish and Arabic do not, and
the call site is the wrong place to learn that. `TestPluralsHaveOther` fails on a plural
missing its `other` form, which is the one the runtime falls back to.

**Server refusals carry a key, not just prose.** The server cannot translate itself — the
language is a per-device `localStorage` value it never sees. So:

```go
return errKey(codes.PermissionDenied, "srv.adminOnly",
    "only an administrator can change Smart+ settings", nil)
```

…and register the same English in `client/i18n/en_srv.go`. The message on the status is not
redundant: it is what the two consumers with no catalog get, the Google Reader sync API
(§20.7) and curl. `TestEveryServerErrorKeyExists` fails if the key is unregistered or outside
the `srv` namespace; `TestServerErrorKeysMatchTheirEnglishFallback` fails if the two Englishes
drift, because each is correct alone and the divergence is otherwise invisible.

**Keys built at runtime need a fallback.** `tr.T("theme", t.Name)` is invisible to every
static check, so the call site compares against the missing-key form and falls back to the
source package's own label — see `view.themeLabel`, `view.glyphName`. Add the prefix to
`dynamicPrefixes` in `keycoverage_test.go` with a note saying what supplies the suffix.

**Splash strings are special and there are five of them.** `web/index.html` runs before the
wasm module exists, so it cannot read the catalog. Add to `en_boot.go`, mirror through
`view.mirrorBootCopy`, AND update the English fallback baked into `index.html` — the two must
agree, because the fallback is what a first-ever load, a storage-refusing browser and a no-JS
reader see.

**What is deliberately NOT translated**, so nobody "fixes" it: feed content (titles, bodies,
authors — that is §10.5, a different machine); gRPC's own transport text inside an `{err}`
interpolation (paraphrasing the actionable half helps nobody); command-line strings, model
identifiers, keyboard key names, CSS class names, and `data-` attribute values.

---

## 23. Testing

Highest-value first:

**T1 · Tenant isolation** — two-tenant fixture; every exported repository method asserted unable to
   cross, **including via `shares`**. *The single most valuable test in the project.*
**T2 · A22 deletion safety** — deactivating a source, and deleting a user, leave every *other* user's
   favorites, tags, notes, and shares intact.
**T3 · Private-mail isolation** — two users receiving the same newsletter get **separate** sources and
   items; neither can reach the other's.
**T4 · Capability map completeness** — every RPC *and* sync-API route has an entry; unmapped fails
   closed; **no `api_token` scope can reach an admin capability**.
**T5 · Sync API conformance** — golden transcripts, plus **a real client smoke test**: point NetNewsWire
   or Reeder at a seeded instance and assert subscribe/read/star/tag/mark-all round-trip. Compatibility
   claimed without a real client is compatibility not yet had — and it runs every release, not once.
**T6 · Rules correctness** — **a mute by user A does not hide the item from user B**, ordering and
   `stop_processing` honored, retroactive apply undoable, preview matches actual.
**T7 · Migrations** — every migration applies to an empty DB *and* to the previous release's schema with
   representative data; a tampered checksum aborts startup.
**T8 · Backup/restore drill** — `VACUUM INTO` under concurrent writes, `integrity_check` passes, restore
   produces a working instance. Automated, not a runbook.
**T9 · LLM egress allowlist** — the outbound body contains only titles, summaries, and the §18.1 derived
   profile. No notes, article text, raw events, or identifiers. A security test.
**T10 · Offline round trip** — build a pack, go offline, read, tag, write a note, reconnect; the outbox
    drains, **`rev` conflicts resolve without clock reliance**, and a note conflict is surfaced rather
    than clobbered.
**T11 · Two-tab safety** — two tabs, leader election holds, no clobber (the CashFlux failure mode).
**T12 · Version skew** — a below-minimum client is refused and purges its SW cache.
**T13 · Flood guard** — a source whose GUIDs all change ingests without firing notifications for every
    subscriber.
**T14 · SQLite contention** — poller, handlers, and jobs writing concurrently produce no `SQLITE_BUSY`
    at expected load.
**T15 · Dedup preserves state** — same feed twice: read/favorite/note/tags survive for every subscriber,
    no duplicate rows, an edit doesn't reset read state.
**T16 · Reconnect loses nothing**, and **per-tenant ring buffers** — one tenant's burst doesn't force
    `RESYNC_REQUIRED` on another.
**T17 · Ranking golden fixture** — fixed engagement log → fixed ordering; volume normalization prevents
    firehose domination; cold start sensible; explore slot never empty; Smart+ failure falls back.
**T18 · Hot-query performance** — all three §6.5 shapes benchmarked at M4 on 50k items × 3 users.
**T19 · Auth and recovery** — lockout triggers and releases · replayed refresh token revokes the family ·
    login and reset-initiate timing indistinguishable · recovery code and reset token single-use ·
    reset kills every session · sudo mode gates role changes · CLI break-glass audited ·
    **`articleflux init` creates exactly one superadmin and can't be re-run**.
**T20 · Webhook SSRF**, **public feed safety** (excerpt-only, rotation invalidates), **newsletter
    sanitization** corpus, **301 handling** (URL updates, chain capped, guard re-run per hop).
**T21 · Connection state machine** (§20.19, A40) — **(a)–(d) built and green 2026-07-26; (e) owed.**
    Five parts, and the first two are the ones that would have caught what the audit found by reading:
    **(a) Classification, native** — a table test over every §20.7 code asserting which of transport /
    application / terminal it lands in. `NotFound` must not touch the indicator; `Unauthenticated`
    must stop the retry loop.
    **(b) Half-open detection, against a blackhole** — a TCP relay that accepts and then silently
    stops forwarding, which is the only honest way to reproduce F3. The relay must keep READING and
    discard, not stop reading: a relay that blocks fills the kernel buffer, the peer's writes fail,
    and the client learns something is wrong from backpressure rather than from a probe — passing for
    the wrong reason and proving nothing about the keepalive. A browser cannot be made to do this; the
    test is native, over the same tunnel client. **~12 s, and it cannot be made much faster**, because
    gRPC clamps the client interval to a 10 s floor (§20.19.3).
    **(c) The idle soak is two seconds, not thirty minutes.** Written as a half-hour test, which is not
    a test anyone runs. The same property is provable in under two seconds by lowering the SERVER's
    `MinTime` — the one knob that makes the shipping numbers observable at speed — and watching an
    idle, correctly-probing client stay `Ready`. That is the `too_many_pings` regression test, and it
    is what fails the day someone removes the enforcement policy as an unused option, because the gRPC
    defaults it falls back to kick that client immediately. The numbers themselves are pinned
    separately in `internal/connpolicy`.
    **(d) Recovery does not storm** — and the spec's own number was wrong. Ten *sub-second* blips
    produce **zero** refetches, not one: nothing publishes in two seconds, and the outage floor
    suppresses them entirely. Ten *real* outages spanning ~26 s produce **five** — one per spacing
    window, which is the honest answer between "one per flap" (the storm) and "one in total" (a stale
    screen). Also asserted: a response from a superseded load never lands.
    **(e) E2E, Windows-native** — kill the server mid-session and assert `down`; restart and assert
    `live` plus a refetched list; `context.setOffline(true)` and assert `offline`, not `down`.

**T22 · Nothing is lost across an outage** — the write half of T21. **Built 2026-07-26 at the unit
    level** (`client/outbox`, `client/track`): coalescing keeps the LAST intent and its key, ordering
    between items survives, the cap drops oldest and says how many, a superseded key acked mid-drain
    does not take the newer intent with it, and a buffer written on `pagehide` comes back in the next
    tab and ships. *Still owed: the end-to-end version* — mark five articles read with the server down,
    restart it, and assert all five survived a real drain. That one is Playwright and rides with
    T21(e) on 8b.34.

**T23 · The proxy tiers, and the one property that matters** (§10.1) — **(a) nothing leaks to the
    origin**: render a fixture page through tier 2 and assert against a request log that *zero*
    requests left our origin, which is the only way `url()` inside a background shorthand gets caught.
    **(b) The blocked-origin case, reproduced properly** — the origin reachable from the server and
    firewalled from the client, because a test where both halves run on one machine passes while the
    feature is broken. **(c) Capability scoping** — a signed URL for item A cannot fetch item B's
    assets; an expired one is refused; a proxy path on the app hostname is refused. **(d) No free-text
    URL** reaches any fetch, asserted at the handler. **(e) Escalation** — a JS-only fixture comes back
    empty from tier 2 and filled from tier 2r. **(f) No orphans** — a dropped tier-3 stream leaves no
    browser process behind, and the screencast is damage-driven rather than fixed-rate on a static
    page.

Plus the parser corpus over every §15.3 format including broken feeds saved verbatim, one fixture per
namespace, date/charset tables, extraction corpus, scraping redesign fixture, import/export round
trips, hallucinated-discovery rejection, client render/mapper units, and Playwright E2E.

---

## 24. Milestones

**Phase 1 — Foundation**
**M0** Transport spike: `go.mod` + replace, `buf generate`, `grpctunnel.Wrap`, one unary RPC + one
streamed event through `ui.PostAsync`. *Exit: **`app.wasm` size measured and written down here**.*
**M1** Pipeline + full schema + **operations floor**: parser across every §15.3 format/namespace;
fetcher (conditional GET, SSRF, `Retry-After`, **301 handling**); full schema incl. `item_tags`,
`rules`, `item_revisions`, `shares`; **migration runner (A23), WAL + single writer (A24), `VACUUM INTO`
backup, `/healthz`**; repository `Scope` layer + leak test; poller with backoff and priority queue;
**revision write path active from day one**. No UI.
**M2** Identity + **tier-1 extraction**: tenants, users, roles, capabilities, invites, devices;
password auth with breach-check, rate limiting, refresh rotation + reuse detection; recovery rungs 1–2
+ CLI break-glass; **`articleflux init` bootstrap**; sudo mode; fail-closed capability map; TLS; bind
check. **Plus `internal/extract`**, moved up from M13 because M9/M10 depend on it.
**M3** Settings registry + three-layer resolution + **job queue** — before the UI, so every later
feature has somewhere to put its knobs and its background work.

**Phase 2 — Reader**
**M4** Read path: `ListItems`/`GetItem`/counts, sidebar, virtualized list, tier-0 article, live
`ItemsArrived`, **all three hot-query benchmarks**.
**M5** State + **engagement logging from day one** + notes (private by default) + **item tags**.
**M6** **Rules engine**: matcher, the seven implementable actions, **queued per-subscriber fan-out**,
mute, streaming preview, retroactive apply + undo, rule-rot surfacing.
**M7** Organize + subscribe + **all interchange**, round-trip tested.
**M8** Flexible UI: `ViewSpec` end to end, sort/filter/density, saved views, **typography**,
time-scoped mark-read, keyboard map, **command palette**, branching onboarding, empty states.
**← first daily driver.**

**Phase 3 — Personal & sync**
**M9** Bookmarks + archiving (uses M2's extractor) + dead-link checks + bookmarklet + **the asset
proxy (§10.1a)** — an archive whose images still point at a dead origin is half an archive, and it is
the same day of work that repairs reading from a network which blocks the publisher. **Pullable
earlier**: it depends on nothing above M4.
**M10** **Offline trip packs**: SW app-shell caching, `BuildPack`, IndexedDB mirror, **leader
election**, outbox with `rev` resolution, **version-skew handshake**.
**M11** **GReader sync API** + capped token scopes + event parity + conformance suite **and a real
Reeder/NetNewsWire smoke test**.
**M12** **Notifications** (Web Push, digest, DST-correct quiet hours) + **Trends/stats/feed health** +
**contextual nudges**.
**M13** Polish: render-mode switcher, FTS across items/notes/bookmarks, a11y + RTL, dark mode,
connection badge, retention, **restore drill**, perf pass. **← ship line.**

**Phase 4 — Intelligence**
**M14** Smart homepage: affinity derivation, **impression + bulk-read signal taxonomy (§18.1)**,
term-vector topics, scorer with volume normalization, three slots with topic-aware Explore,
explainability, feedback, tuning panel, cold start, golden fixtures.
**M15** **Highlights mode (§18.5)** — `home_mode`, rate-to-cutoff solver, the alternate firehose
scoring, and the app *proposing* the mode from noisy-source data · **domain affinity (§18.6)** ·
adaptive intervals · cross-source dedup grouping.
**M16** **Recommendations (§18.7)** — outlink mining, aggregator pass-through, blogroll/OPML mining,
candidate health gating, evidence UI, trial subscriptions with a verdict. **Rungs 1–3 only; no LLM.**
**M17** **Smart+**: embeddings, embedding-based topics, re-rank, §18.8 egress allowlist + test,
shared budget meter, **circuit breaker**, fail-soft. Recommendation rungs 4–5 land here.
**M18** **Translation** + **audio/TTS + podcast player**.

**Phase 5 — Platform & extras**
**M19** Admin console incl. **user deletion (§7.9)** and **tenant deletion with share preview (§9.1)**.
**M20** Folder sharing with the §7.8 contribute matrix + scoped event fan-out.
**M21** Public shared-items feeds.
**M22** Newsletters via IMAP (per-user keyed).
**M23** Outbound webhooks + send-to.
**M24** Article revisions UI and diffs (data has been accumulating since M1).
**M25** Scraped feeds + AI rule drafting.
**M26** AI discovery rung 4 · WebSub · screensaver.
**M27** **Page proxy (§10.1b)** — tier 2: static fetch, full asset rewriting including CSS `url()`,
the `snapshot` sanitize policy, the separate proxy hostname, signed capability URLs, disk cache, and
the render-mode switcher gaining a *Page* option. **Ships the whole safety envelope** that M28 then
reuses unchanged.
**M28** **Headless renderer (§10.1c–d)** — the browser pool behind `render.Render`, tier 2r
(post-render DOM into M27's pipeline, with the escalate-on-empty rule), compression to a byte budget,
the full-page screenshot fallback, then tiers 3–4: `StreamPage` bidi RPC, screencast frames diffed to
64×64 tiles, input events back up the same stream, session caps, and the **ladder controller** of
§10.1-R. **This milestone is what makes the blocked-network story exist at all** — under the runtime
ordering, rung 2 is the primary answer and rungs 3–4 are its fallbacks, so nothing above reader text
works until this lands. Instance switch stays; the per-reader gate is consent at first use, and an
un-asked reader defaults to rung 3.
**M29** **Classification and the item pipeline (§27)** — `internal/classify` (the pure scorer, the
26-category lexicon, the corpus ratchet **T24**), `internal/pipeline` (`JobAnalyze`, `item_analysis`,
the `Analyzer` and `Contributor` registries), migration `0021`, fan-out reordered downstream of
analysis, Settings → Classification with its live preview, and the Unsorted view. **Depends on M17**
for the egress harness §27.4e amends and on nothing else; the free tier is complete without it.
**Answer D23 first** — the word "category" is currently the rail's word for a folder.

---

## 25. Open decisions and risks

### 25.0 Proposed resolutions — awaiting sign-off

Eighteen decisions are open. **Thirteen of them are choices, not discoveries** — they can be settled at a
desk without running anything, and each one left open is a place an implementing agent will either
stall or guess. Drafted below with the reasoning, so accepting is one word and overriding is one
sentence. **Until signed off these remain open**; nothing here is settled by having been written down.

| D | Proposed | Why | What it costs to accept |
|---|---|---|---|
| **D5** name | **Keep `ArticleFlux`** | It means *news brought from afar*, it's short, and the alternative is an afternoon of bikeshedding. It undersells the bookmark half — nobody will care | Module path locks to `github.com/monstercameron/ArticleFlux`. **Cheapest to change now, churn across every import later** |
| **D8** hosting | **Home box + Cloudflare Tunnel** | Real hostname, free TLS, **no inbound port** — which shrinks §21's threat surface more than any code | ⚠️ **Cloudflare terminates TLS, so it sees plaintext** — your notes and reading history. If that's unacceptable, a €5 VPS with Caddy + Let's Encrypt is the only option where nobody else holds the cert. Tailscale Funnel has the same property as CF |
| **D9** bookmarklet vs extension | **Bookmarklet only in v1** | Zero install, no store review, no per-browser build, and it works on a locked-down work machine — the case that motivated it | No "already saved" indicator, no tag autocomplete at save time |
| **D10** two LLM providers | **Keep the interface; ship OpenAI first** | `llm.Provider` lands at 6.11 regardless. Smart+ (OpenAI) is M17; Claude for rules and discovery is M25–26 — so it's sequencing, not duplication | Two keys, two dashboards eventually. Revisit consolidation once both are real, not before |
| **D11** Smart+ model ids | **Config keys with no default**, validated against the provider's `/models` at save | Any default written here is stale by implementation. Setup requires an explicit pick | One more required setup field |
| **D12** who are the tenants ⚠ | **Invite-only. Family and friends. No self-signup** | This is the one that *removes* work | **No registration flow, no CAPTCHA, no email verification, no abuse tooling.** Quotas become advisory rather than adversarial, deletion is a courtesy not a legal obligation, uptime is best-effort. **Self-signup stays additive** if that ever changes |
| **D14** email direction | **IMAP in (M22). No SMTP out, ever** | Rungs 1–2 cover recovery for an invite-only instance; Web Push covers notification | Recovery rung 3 never ships. No send-to-Kindle. No emailed alerts |
| **D15** GReader scope | **Target NetNewsWire and Reeder specifically** | The spec is de-facto; clients implement loose subsets. Two clients that work beat broad coverage nothing validates | Other clients are best-effort. Say so in `/settings/apps` rather than implying universal support |
| **D16** public feed republishing | **Excerpt-only, permanently** | Not a v1 limitation — a policy. Full-text republishing of someone else's writing under your name is a licensing question, not a feature | Public shares are a pointer plus your comment, which is what Google Reader's was |
| **D21** how the ladder detects its rungs (§10.1-R) | **Manual switcher in v1; automatic only once a probe exists** | "The network blocked it" is close to undetectable from the client — a blocked fetch, a DNS failure, a captive portal and plain offline are one opaque error, and a refused iframe looks like a loading one. Guessing wrong drops a reader onto a live browser they did not need, or strands them on a blank box. A probe (does the client reach a known-good origin? does the *server* reach one the client cannot?) is real design work and should not be faked | The ladder is a switcher the reader operates, which `RenderModeSwitcher` already exists to be. Automatic escalation waits. The bandwidth half is the easier one and lands first: measure the stream's own throughput rather than trusting `navigator.connection`, which is a hint and is missing on some browsers |
| **D19** does the renderer ship, and where | **Yes, on the reference box, one render at a time, flag-gated off by default** | Edge is already installed and `chromedp` attaches to an existing Chromium, so this adds a dependency on a browser that is *already there* rather than a new host, a container or a Node toolchain. A second box would be the clean answer and is a second box to own | ~1 GB of headroom and a CPU spike per render on the machine that also serves reading. The queue (§22.7) is what keeps that from being felt. **If the box is the fanless one, expect thermal throttling under repeated renders** and treat sustained rendering as out of budget |
| **D20** proxy origin | **A separate hostname (`proxy.<instance>`), from the first line of code** | Retrofitting an origin split *after* signed URLs are minted and cached is a migration of every stored artifact. The CSP-and-sandbox-only version is one mistake away from the session token, and it is the same amount of work today | One more DNS name and one more tunnel route — a single extra entry in the Cloudflare config. TLS is free on both under D8 |
| **D17** quota accounting | **Subscription count + tenant-exclusive bytes.** Shared source/item storage excluded entirely | The only definition that is both enforceable and fair under global dedup (A14). Tenant-exclusive = packs, archives, audio, embeddings, mailbox items, notes, bookmarks | A tenant subscribing to 500 popular feeds costs almost no quota. Correct, and occasionally surprising |
| **D22** Fanciful's `--mute` is below AA (§20.16.2) | **Take it to about `#A093AC`** — in the mockup first, then `tokens.go` | The house tertiary colour measures **4.90:1 on the page, 4.42:1 on a hovered row and 3.94:1 on the row a reader is sitting on**, at the 11.5px it is used at for datelines and counts. That is AA for large text only, and none of this text is large. It is transcribed verbatim from `design/03-fanciful.html` and **the mockup is the specification**, so the fix is a decision about the mockup rather than a value to nudge in Go — which is exactly why this is a D and not a bug | `#A093AC` clears 4.5:1 on all three grounds and is the smallest change that does; the four generated themes already pass everywhere. Until it is answered, `sheet_test.go` records the two failing pairs **with their measured ratios and ratchets them** — they may not get worse, and a new theme cannot inherit the exception |


**The five that genuinely cannot be settled at a desk** stay open by necessity, not neglect:
**D0** (tag and push v5.0.0 — an action), **D1** (gofeed coverage against real feeds), ~~**D2**~~
(**closed 2026-07-26 by G1** — and it came back *no*, against the desk read; see §25.1), **D7**
(extraction quality, judged by eyeball), **D13** (pack transport, needs a real 30 MB pack). Each
requires executing something. **That is why this plan is not, and cannot be, fully waterfall** — three
of its numbers are unknowable from a document. D2 is the proof: the desk answer said "largely
resolved," and running it found a per-connection registration requirement that changes `store.Open()`.

**Accepting all fourteen closes every desk-decidable question and leaves five spikes.** That is the most
plan-complete this design can honestly be before code exists.

---

### 25.1 The open list

**D0 — GWC dependency.** `go list -m -versions .../GoWebComponents/v5` **returns nothing.** The
CHANGELOG has a `v5.0.0 - 2026-07-25` entry but **no matching git tag** — verified. Needs
`replace ... => ../GoWebComponents`, and A9 sharpens it: a remote deployment means building on, or
copying source to, the target box. *Recommendation: tag and push v5.0.0.* *(GoGRPCBridge v1.1.1 is a
real tag matching HEAD.)*

**D1 — Parser. RESOLVED 2026-07-26.** `mmcdole/gofeed` underneath, our normalisation layer on top.
Confirmed against a **committed 27-fixture corpus** (`internal/feed/testdata/corpus`, TODO 4.2): 21 feeds
fetched verbatim from live publishers, 6 hand-written for the formats nobody serves any more. Coverage
verified end-to-end through the real `Fetch` path: RSS 0.91 · RSS 2.0 · RSS 1.0/RDF · Atom 0.3 ·
Atom 1.0 · JSON Feed 1.1 · `itunes:` · `content:encoded` · `dc:` · `media:` · a non-UTF-8 encoding ·
a comments feed · a rewriting proxy (FeedBurner) · a hosted generator (Blogger).

**gofeed's parsing was not the problem. Our layer around it was.** The corpus found three bugs on its
first run, all of which had shipped, and none of which any unit test in the tree could have caught
because each lived in the seam between two packages that were individually correct:

1. **Every non-UTF-8 feed was mojibake.** `charsetdec` decoded Windows-1252 → UTF-8 correctly, then
   handed the result to gofeed with the XML declaration still saying `encoding="windows-1252"` — so
   gofeed decoded it a *second* time. `Café` → `CafÃ©`. Invisible for ~97% of feeds because a second
   decode of UTF-8-declared UTF-8 is a no-op. Fixed by `charsetdec.retagDeclaration`, which rewrites
   the declaration to `utf-8` on the way out: a document whose declaration contradicts its own bytes
   is a lie that this package told, so this package repairs it.
2. **Fragment-only permalinks collapsed onto one item.** `stableGUID` fell back to `urlnorm.Norm`,
   which strips the fragment. A linkblog publishing a day's entries as `…/08.html#stockMarket`,
   `#nodesc`, `#ents` produced three items with one guid — two of them permanently unstorable. Fixed
   by a new `urlnorm.ItemKey` (Norm + fragment). `Norm` and `DupeKey` keep stripping it, which is
   right for bookmark identity and cross-source dedup; the three purposes genuinely disagree, and the
   answer is three functions rather than one compromise that is subtly wrong for all of them.
3. **Podcasts showed no author.** The Changelog fixture carries one `<itunes:author>` at the channel
   and none on any of its 1,012 episodes. Items now inherit the channel author.

**The 0.91 dialect question is closed too** — it parses, and it has no `<guid>` at all, which is what
made bug 2 visible. *The lesson worth keeping: the corpus was scheduled at 4.2, after 4.1 was marked
done. Everything it found had already shipped.*

**D2 — FTS5. RESOLVED 2026-07-26 by G1**, and the desk answer above was wrong in a way that matters.
FTS5 is **not compiled into** `ncruces/go-sqlite3` — `PRAGMA compile_options` has no `ENABLE_FTS5`, and
a plain `sql.Open("sqlite3", …)` fails `CREATE VIRTUAL TABLE … USING fts5` with **`no such module:
fts5`**. It ships as a **loadable wasm extension** (`ext/fts5.Register`, backed by
`go-sqlite3-wasm/v3/fts5`) that must be registered **per connection**:

```go
db, err := driver.Open(dsn, fts5.Register)   // NOT sql.Open — the init hook is the whole point
```

**The binding constraint on §22.2 / TODO 3.3:** `store.Open()` must go through `driver.Open` with the
hook, and it must do so for **both** A24 pools — the read pool and the single-writer pool. A pooled
connection that skips the hook works for every non-FTS query and then fails only on search, so the
failure would surface late and look like a search bug rather than a wiring bug. `_ ".../embed"` is
**not** to be imported alongside the driver; the driver already embeds the wasm and the package prints
a warning.

Verified working under the hook: external-content table over `items` with `content='items'`/
`content_rowid='id'`, AFTER INSERT/DELETE/UPDATE trigger sync (including staleness after both UPDATE
and DELETE), `tokenize='porter unicode61'` stemming, column filters (`title:decoding`), `snippet()`,
and `bm25()` ordering. §6's three FTS tables and §18.2's tokenizer-driven term affinity are cleared to
proceed. Pinned permanently by `internal/store/fts5_spike_test.go` so a dependency bump that drops FTS5
fails a test instead of silently removing search.

**D3 — Live updates.** Resolved: server-streaming over the tunnel (+ WebSub, M26).

**D4 — Auth.** Resolved by A9 — required in v1, landing in M2.

**D5 — The name.** **Still open**, and increasingly wrong: it is a reader, a bookmark manager, a
ranking engine, and a multi-tenant platform. Settle it before the module path sets.

**D6 — Where the server runs.** Resolved: remote (§4).

**D7 — Extraction library. RESOLVED 2026-07-26 — `go-shiori/go-readability`.** Evaluate a
Go readability port before writing one; quality is judged by eyeball across hundreds of sites. Serves
five consumers and lands at M2.

**Built 2026-07-26:** a **committed 12-page corpus** (`internal/extract/testdata/articles/*.html` +
`.meta`, one URL per file) spanning the shapes that break extractors differently — Ars Technica, The
Verge, Eurogamer, Substack, Wikipedia, MDN, a GitHub README, the Go blog, Paul Graham, Dan Luu, Simon
Willison, CNX Software — and a bake-off over **go-readability · trafilatura · domdistiller**, scored on
characters kept, whether a title was found, wall time, and **boilerplate hits**: a fixed list of
phrases ("Subscribe", "Cookie", "Related Stories", "Skip to content") none of which belongs in a
reading pane, which is the most useful automatic quality signal available without a human.

Two structural choices worth keeping. It is a **separate Go module** (`internal/extract/bakeoff/`)
because two of the three libraries lose, and keeping them in the main `go.mod` would drag wazero, a
WASM-compiled re2, zerolog and a date parser into the dependency graph of a server that uses none of
them — permanently, to preserve a comparison that runs about once a year. A nested module is invisible
to the parent's `go build ./...`, so the evidence stays runnable without the parent paying for it. And
it stays in the tree rather than being deleted, because **D7 is exactly the kind of decision that gets
re-litigated in six months** and the cheapest answer to "why aren't we using X?" is a command anyone
can re-run against the same twelve pages.

**RESOLVED 2026-07-26: `go-shiori/go-readability`.** The dump was read. Two properties decided it,
neither of which the scoreboard above shows — all three libraries extracted all twelve pages, found a
title on all twelve, and tripped the boilerplate list on only Wikipedia.

**Text quality.** Trafilatura's plain-text serialiser inserts spaces around inline elements — *"The
Room , Troll 2 , or Fateful Findings"*. Across the corpus that is **199 artefacts to readability's 5**
(domdistiller: 87). Three of the five consumers read the plain text and one of them reads it out loud,
so this is not cosmetic. On The Verge, trafilatura also duplicated three sentences and kept a
commission disclaimer — its *higher* character count on that page was chrome and repetition, which is
why "characters kept" cannot be scored as more-is-better.

**Content retention.** On the photo-heavy CNX Software review, readability kept **34 images**,
domdistiller 36, and trafilatura **8**, out of 117 in the source. Dropping three quarters of the
photographs from a review of a physical object is dropping the article.

**The measurement that nearly decided it the other way.** Readability looked bad on markup bulk —
5.8× text size on WordPress against trafilatura's 1.4×. That is 38 KB of `srcset` attributes:
responsive image candidates, which are *data*, and which 2.9's attribute allowlist removes before
anything reaches storage. A real measurement of something that does not survive to storage. Worth
remembering the next time a benchmark looks decisive.

**Recorded weakness:** readability is weakest on documentation-shaped pages — the MDN fixture retains
navigation scaffolding that trafilatura strips. Feeds rarely carry those and the cost is cosmetic.

**Now wired in.** `internal/extract` is the package: `Fetch` (SSRF-guarded, 8 MiB cap) and `FromBytes`,
returning sanitised HTML *and* plain text from one pass — deriving text at four call sites produces
four different word counts, and the one used for ranking is the one nobody checks. Output goes through
`sanitize.Archived`. Below `MinWords` (25) it returns `ErrNoContent` rather than an empty success,
because readability hands back the navigation on a section index and a reading pane containing "Home
About Contact" is worse than an honest fallback to the feed's own content.

**A fourth bug in the same family fell out of it, and it generalises.** `go-shiori/dom` runs
*statistical* charset detection (`gogs/chardet`) over whatever it is handed and re-decodes on its
guess, so correct UTF-8 that is mostly ASCII with sparse accented words is re-read as Latin-1 — the
same `café` → `cafÃ©` failure D1 found between charsetdec and gofeed, arriving through a third door.
`charsetdec` now retags HTML `<meta charset>` and `http-equiv` declarations as well as XML ones, but
the durable fix is architectural: `extract` parses the DOM itself and hands readability a *document*,
so nothing downstream is given a second chance to decode. **The rule, now three for three: a library
handed bytes will re-derive the encoding, and it is right to. Pass parsed structures across package
boundaries, not bytes.** It also hides well — a short page gives the detector too little signal and
comes through clean, so it only appears at realistic article length, which is why unit tests missed it
and a 12-page corpus did not.

**D8 — Hosting.** *Recommendation: home box + Cloudflare Tunnel* — a real hostname with TLS and no
inbound port. §22.4's `/readyz` is what the tunnel health-checks.

**D9 — Bookmarklet vs extension.** Bookmarklet in v1.

**D10 — Two LLM providers.** `llm.Provider` plus the shared breaker (§22.8) makes consolidation a
config change.

**D11 — Smart+ model IDs.** Config-driven, validated on save.

**D12 — Who are the other tenants? RESOLVED 2026-07-26: invite-only, family and friends, no
self-signup.** Taken as drafted in §25.0. *Recorded as an assumption made to unblock 5.1 and 6.1
rather than as a preference of mine — it is Cam's call, and it is one sentence to override.* The
reason it was safe to take: it is the only open decision that **removes** work, and reversing it is
purely additive.

What it deletes from the build: registration flow, CAPTCHA, email verification, abuse tooling,
adversarial quota enforcement, and any legal deletion obligation. Quotas become **advisory** —
`tenants.quota_subscriptions` already carries the comment saying so — and uptime is best-effort.

What it constrains: 6.1 builds invites (a superadmin mints a code) and not registration. 7.9's
`articleflux init` produces exactly one superadmin and the server refuses to serve without one, which
is coherent only because nobody else can create an account. Rate limiting and lockout stay, because
those defend against someone who has the login page, and an invite-only instance still has one.

**If this changes**, the additive path is a `tenants.self_signup` flag plus a registration surface;
nothing built under this decision has to be undone.

**D13 — Pack transport.** Confirm the plain-HTTPS split at M10 with a real 30 MB pack.

**D14 — Email direction.** §14.1 needs **IMAP in**; §7.2 rung 3 and send-to-Kindle need **SMTP out**.
Independent. *Recommendation: IMAP in at M21, skip SMTP unless send-to-Kindle becomes a must.*

**D15 — GReader API scope.** The spec is de-facto, not formal. *Target NetNewsWire and Reeder first,
test against real clients at M11, treat the rest as best-effort.*

**D16 — Public feeds and republishing.** Excerpt-only is the default for a reason.

**D17 — Quota accounting under global dedup. NEW.** §9 promises "max sources, storage MB" per tenant,
but sources and items are global and deduplicated (A14) — a feed five tenants share is stored once.
Charging every subscriber in full double-counts bytes that don't exist; charging only the first is
unenforceable free-riding. *Recommendation: quota on **subscription count** (unambiguous) plus
**tenant-exclusive storage** (packs, archives, audio, embeddings, mailbox items — the genuinely
per-tenant bytes), and exclude shared source/item storage from quota entirely.* Decide before M18
builds the usage display.

**RESOLVED 2026-07-26 as recommended.** Taken to unblock 5.2, and cheap to take because D12 made
quotas advisory rather than adversarial — the number's job is to warn a friend that they have
subscribed to nine hundred feeds, not to stop an attacker.

Consequences that are worth stating plainly because they look like bugs otherwise:

- **A tenant subscribing to 500 popular feeds costs almost no storage quota.** That is correct under
  A14 and it will still surprise whoever reads the usage display first, so §9's display names the two
  numbers separately rather than summing them into one misleading "MB used".
- **Deactivating a source frees nothing**, because the bytes were never charged to anyone.
- Quota is therefore two independent counters, not one: `subscriptions` rows (already trivially
  countable) and a `tenant_bytes` figure summed over the exclusive tables as they arrive. Nothing in
  `sources` or `items` participates, which is why `sources` needs no accounting columns — the open
  question TODO 5.2 flagged, now answered: **none**.

**D18 — Do passive signals and explicit verdicts belong in the same sum? NEW, 2026-07-26.** §18.4
blends every term into one linear score. The problem this raises, now that the signals themselves are
being collected: dwell, completion and click-through all correlate strongly with **ease of
consumption**, not with worth. Optimise a single linear score over them and the homepage converges on
the most trivially clickable thing published that day — and it fails **invisibly**, because the page
still looks full. Explore's 20% does not fix it; that addresses topic monoculture, not quality
collapse. The A27 verdict is the only signal in the system that measures whether something was worth
the time, and it is sparse *because* it is deliberate. *Recommendation: passive signals generate and
order the candidate set (recall); verdicts and the deliberate acts — notes, tags, click-throughs —
calibrate a re-rank over it (precision), rather than being one more weighted term in the sum.* This is
a change to §18.4 rather than to §18.1, so the log being written now is correct either way — which is
the argument for deciding it late rather than guessing now. **Blocks 6.9 and the M12 scorer.**

**RESOLVED 2026-07-26 as recommended: two stages, not one sum.** Taken to unblock 6.9's derivation
job. The shape 6.9 now builds:

1. **Recall.** Passive signals — dwell, completion, scroll-past, open rate, feed and term affinity —
   generate and order the candidate set. This is where volume lives, and it is allowed to be
   correlated with ease of consumption because its only job is to not miss things.
2. **Precision.** The deliberate acts — A27 verdicts, notes, tags, click-throughs to the source —
   calibrate a re-rank over that set. These are sparse *because* they cost the reader something, and
   that cost is exactly what makes them evidence of worth rather than of stickiness.

The failure this avoids is specific and invisible: a single linear score over passive terms converges
on the most trivially clickable thing published that day, and the page still looks full while it
happens. Nothing in the interface would show it. Explore's 20% does not help — that is a defence
against topic monoculture, not against quality collapse.

**Two consequences for 6.9.** `feed_affinity`, `term_affinity` and `domain_affinity` are recall-stage
tables and derive from passive signals as §18.1 already specifies. `home_ranking` is the re-ranked
output and must therefore be derived *after* them in the same job, reading verdicts separately —
which means it is not simply another weighted column alongside the affinities, and the table comment
says so. `bulk_read` remains neutral in both stages.

**R0 — One factor on a public service.** §7.3's controls are the mitigation, not optional polish. If
this ever holds other people's data, revisit TOTP (a day, $0) before any other feature.

**R1 — Cross-tenant leak.** Treat a repository method added without a `Scope` parameter as a build
break, not a review comment.

**R2 — `user_item_state` cost.** Denormalizing `source_id`/`published_at` (§6.5) trades write cost and
storage for the sort shapes that matter. Validate at M4; the fallback is a materialized per-user unread
index — *measure first*.

**R3 — Three transports.** Tunnel + HTTPS packs + REST sync. Every mutation path must raise the same
events and run the same rules, or "marked read in Reeder" silently diverges. One service layer, three
entry points — enforce it.

**R4 — Bundle size. MEASURED 2026-07-26 (G5): 23.8 MB raw, 5.2 MB gzipped.**

That is a large bundle by any standard, and it is dominated by three things that are not optional:
the Go runtime itself, `google.golang.org/grpc` + `protobuf`, and GWC. A `-trimpath -ldflags="-s -w"`
release build saves under 4% — the symbol table was never the problem.

**What it means, honestly:**

- **On a desktop over LAN this is a non-issue.** 5.2 MB over gzip is one load, cached thereafter.
- **On a phone over cellular it is a real cost** — several seconds on a first visit, and it must never
  be paid twice. That makes the Service Worker (§12) load-bearing rather than a nicety, and it is why
  the static handler now serves a **precompressed `.gz` sibling**: shipping 23.8 MB when 5.2 MB is
  available is the difference between usable and not.
- **A5 (offline packs) survives**, but with less headroom than assumed. The pack budget has to be
  planned against ~5 MB already spent, not against zero.

**RE-MEASURED 2026-07-27: 31.4 MB raw, 6.6 MB gzipped.** The number above is the original G5
measurement and is kept because the reasoning was written against it; this is where it has got to.

| | 2026-07-26 (G5) | 2026-07-27 | |
|---|---|---|---|
| raw | 23.8 MB | **31.4 MB** | +32% |
| gzipped | 5.2 MB | **6.6 MB** | +27% |

**The ratchet is at 96% of its ceiling.** `wasm-baseline.txt` is 30,175,802 and CI fails at +5%
(31,684,592); the build is 31,368,999. One more feature of any size trips it, and the next person to
see that failure will be somebody who added a button. That is the ratchet working — but it means the
next bump is a decision about the trend rather than about one change, and it should be taken as one.

**What it changes about A5.** The conclusion above stands and its margin does not: the pack budget is
now planned against **~6.6 MB already spent**, not ~5 MB. Offline packs are still affordable and the
headroom shrank by a quarter in one day of feature work, which is the rate that matters rather than
the number.

**What it does not change.** The composition: still the Go runtime, grpc + protobuf, and GWC. Nothing
added since G5 is individually large — the growth is a hundred small things, which is exactly the
shape that has no single fix and is why the ratchet exists.

**What was NOT done, deliberately:** TinyGo. It would cut this substantially and would also drop
`syscall/js` compatibility guarantees, reflection-heavy protobuf, and parts of the standard library
this code uses. That is a much larger decision than a build flag, and it should be made against a
measured need rather than a number that looked big.

**The ratchet is now live in CI** (§22.14): +5% over `wasm-baseline.txt` fails the build. The point is
not the current number — it is that the next 5 MB has to be argued for.

**R5 — Reconnect correctness. AUDITED AND CLOSED 2026-07-26 (§20.19, TODO 8c).** Both findings below
are fixed and tested; what remains is recorded at the end. The audit's own conclusion stands as the
reason it was worth doing: **the retry loop was the part that was already right.**

Backoff, jitter, no attempt limit, the `Idle` kick and the recovery refetch are all built and correct.
The audit found the risk is not in the schedule, it is in the two failures a schedule cannot see:

- **A half-open socket is invisible to the client (F3).** The server pings and reclaims the slot in
  90 s; the *client* has no probe at all, so the browser holds a dead `WebSocket` open, gRPC stays
  `READY`, and the indicator reports **live** indefinitely. This is the precise failure the indicator
  exists to prevent, and it is the one it cannot currently detect. `WithTunnelKeepalive` is the fix
  and **must ship with the server's `KeepaliveEnforcementPolicy`** or it flaps every 60 s on
  `too_many_pings` (§20.19.3).
- **A refused call is read as a broken connection (F7).** §7.1b's interceptor now handles an expired
  session correctly — clear the token, go to `Login` — but it does that a layer above the connection,
  which still marks `down` on the way past, and on every application error besides. The cosmetic half
  is a red dot on a healthy socket. The real half is **version skew, which refuses at the handshake**:
  a refusal the client cannot distinguish from an outage is retried on the backoff schedule forever,
  which is precisely what §22.10 says must not happen and has no mechanism preventing.

Downgraded in importance by the same audit: the hand-rolled watch loop §20.4 budgeted for on
CashFlux's precedent has not been needed, because nothing here holds a blocking server stream.
**That changes the day `WatchEvents` (§20.3) lands** — revisit then, not before.

**Closed.** Client keepalive ships with the server's enforcement policy (`internal/connpolicy` holds
both numbers and the invariant); classification is a table test; the mutation outbox and the signals
buffer both survive a closed tab. Half-open detection is proven against a blackhole relay that accepts
bytes and stops delivering them — the only honest reproduction, and one Playwright cannot perform.

**What is still owed, and it is no longer this risk:** server-side idempotency enforcement (§20.7) and
skew's server half (§22.10). Both are now *specified* rather than assumed — see §20.19.6 and §20.19.8.

**R6 — Offline write conflicts.** Notes are the sharp edge; keep-both-and-prompt is deliberate.

**R7 — Storage growth.** Audio is the new worst offender — a 30-minute article read aloud is tens of
MB, per item. §22.6's degrade ladder is the runtime answer; the retention policy decision is still owed
**before M9**.

**R8 — Rules as a footgun.** An over-broad mute looks like "the feed stopped updating." Preview before
save, the `MUTED` view, per-rule hit counts, undo via `rule_hits`, rule-rot surfacing.

**R9 — Sync API compatibility drift.** Clients implement the de-facto spec loosely. The real-client
smoke test is the only honest guard, and it runs every release.

**R10 — Filter bubble** fails invisibly — the page still looks full. Explore now targets *under-served
topics* (§18.4) rather than sampling at random, and recommendations deliberately include one or two
adjacent-but-different candidates (§18.7). Both need tests asserting they are never empty and never
purely similar. **R11 — Volume domination** is the likeliest way the homepage ships broken.
**R12 — Cold start**: engagement logging at M5, nine milestones before the ranker.

**R17 — Bulk-read poisoning. The sharpest risk in the interest layer.** One `mark all read` reads as
143 informed rejections if `engagements.kind` doesn't distinguish it, and weeks of signal die in a
keystroke — silently, with no error, detectable only as the homepage quietly getting worse. Same for
sync clients that auto-mark on scroll (§15.1). §18.1's taxonomy is the fix and it needs its own test.

**R18 — Impressions are a prerequisite, not a nicety.** Without recording what was *on screen and
passed over*, every rate in §18 is computed against an invented denominator, and "you skip this
source" is indistinguishable from "this source sits at the bottom of a list you never scroll." Cheap,
and it must land with M14 rather than after.

**R19 — A bad recommendation costs trust disproportionately.** Mis-ranking your own feeds is an
annoyance; telling someone to subscribe to a dead site, a firehose, or something they already
dismissed is the kind of wrong that retires the feature. Hence the health gate, the evidence line,
remembered dismissals, and the trial verdict — all of which exist to make being wrong cheap to undo.

**R20 — Outlink mining is an SSRF surface.** §18.7 rung 1 harvests arbitrary URLs out of article HTML
and then *fetches* them for discovery and health checks. Same guard as every other fetch (§21), plus a
per-run cap — otherwise a hostile article is a way to make your server probe an internal network.

**R21 — An authenticated proxy is still an egress proxy.** §10.1b's tiers let anyone with a login make
the instance fetch and re-serve the web from its own address. The §21 guard prevents SSRF; it does
nothing about volume, about what is fetched, or about who is really holding the account. Hence the
no-free-text-URL rule, per-user rate limits on proxy and render alike, and the fact that the parameter
is an item id — **the proxy can only reach pages you already subscribed to**, which is a much smaller
surface than "the internet" and costs nothing to enforce.

**R22 — Tier 3 is the loudest thing in this product, and the deployment is the risk.** A persistent
WebSocket carrying megabytes of JPEG from a workplace to a personal domain does not look like reading,
and the networks most likely to make tier 3 attractive are the networks most likely to be looking. This
is not a code risk and no code mitigates it. It is why tier 3 ships flag-gated and off, why the tile
diff matters beyond bandwidth, and why the UI should say plainly what the mode does before it is
switched on.

**R13 — Newsletter ingestion is a foothold for hostile HTML.** Strictest sanitization in the app, its
own corpus.

**R14 — Scraping** is fragile and impolite by default; a banned IP takes down *every tenant*.

**R15 — Client sprawl.** Seven shells. **Self-imposed** cap of ~800 lines/file, ~200 for
`client/main.go` — a house rule, not a GWC convention.

**R16 — Scope.** §0 and §5 exist because this grew from "an RSS reader" to a platform. The lines are
the deliverable, not decoration.

---

## 26. Immediate next step

M0: `go.mod` + replace + buf config; minimal `articleflux.proto` with one unary and one streaming RPC;
`buf generate`; `cmd/articleflux` serving `web/` and `/grpc`; build `gwc.exe` from the GoWebComponents
checkout; `client/main.go` dialing the tunnel, calling the unary RPC, rendering a streamed tick through
`ui.PostAsync`. Then weigh `app.wasm`.

Then **M1 → M2 → M3 before anything visual.** M1 now carries the operations floor (migrations, WAL,
backup, health) alongside the schema — all things that are miserable to retrofit and boring to build,
which is exactly why they go first. **Answer D12 before M2**, and **confirm FTS5 in M1 before writing
three FTS tables.**

*(§26 is the M0 brief and is kept as written; it has been true since the first commit and is now
history. New feature specs append below it rather than displacing it.)*

---

## 27. Classification, and the pipeline that pays for it

> **Specified 2026-07-27. Nothing here is built.** This section is the spec of record for two
> features — **auto-categories** and **auto-tags**, each with a free deterministic tier and an opt-in
> model tier — and for the thing they are the first two customers of: **one analysis pass over each
> new item, whose result every other feature reads instead of re-deriving.**
>
> Read §27.2 even if you only care about the classifier. The pipeline is the load-bearing half; the
> classifier is what proves it works.

### 27.0 The shape of it in one paragraph

A poll lands new items. Ingest writes them, delivers them, and enqueues **one analysis job per
batch** (§27.2). That job runs the deterministic analyzers once, globally, for all tenants at once —
tokenise, vector, language, genre, keyphrases, entities, and a **category score for every category in
the default taxonomy** — and writes a single `item_analysis` row per item. Everything downstream then
reads that row: fan-out labels the item for each subscriber against *their* taxonomy and *their* tag
vocabulary (§27.3), the interest layer stops recomputing TF-IDF from raw text on every derivation
(§27.8), and the trends screen gets a category histogram for free. **Smart+ is an escalation, not a
stage** (§27.4): the model is asked only about the items the deterministic pass could not confidently
place, and when it is asked, every feature that wants something from that article contributes to
**one** request and gets its slice of **one** answer back.

### 27.0a D23 — the word "category" is already taken, and that has to be settled first

`docs/FEATURES.md` §10 is titled **"Categories (folders)"**. The rail calls a `folders` row a
category. That word is now wanted for a different thing, and shipping both is how a settings screen
ends up with two controls named Categories that do unrelated jobs.

They *are* unrelated, and `internal/store/folders.go` already says why at length: a folder answers
**"where does this feed live"** and has exactly one answer; the new axis answers **"what is this
article about"** and is a property of the article, not of the subscription. Merging them — making a
folder able to hold articles as well as feeds — is the tempting move and it is the same mistake
folders.go rejected when tags already existed: you get either a folder that is secretly non-exclusive,
or a rail that cannot say where a feed belongs because a feed's articles landed in nine places.

**Recommendation: rename the rail's "Categories" back to "Folders"** — its schema name, and an
accurate description of what it does — **and give "Category" to the article axis**, where the word
does the most work and where every reader already expects to find it. That is one i18n string, one
`docs/FEATURES.md` heading, and no schema change. The alternative is naming the new axis **Sections**,
which is honest (it is a newspaper section) and which nobody will type into a search box.

**This is D23 and it is Cam's call.** Everything below is written with the recommendation taken; if
the answer is "Sections", it is a find-and-replace over one Go package and one proto message and
nothing else moves.

### 27.1 Three axes, and what each one is for

| Axis | Schema | Scope | Cardinality | Answers | Set by |
|---|---|---|---|---|---|
| **Folder** | `folders` + `subscriptions.folder_id` | per user | exactly one per **feed** | where does this feed live | the reader, by hand |
| **Category** | `categories` + `item_categories` (new) | per user, over a **global default set** | one primary + up to two secondary per **item** | what section of the paper is this | the classifier, correctable |
| **Tag** | `tags` + `item_tags` (exists) | per user | many per item, capped | what specifically is this about | the reader, rules, and now the classifier |

The distinction Cam drew is exactly right and it is worth writing down as a rule the taxonomy is
checked against rather than as an intention:

> **A category is a place. A tag is a claim.**
>
> A category is *generic, closed, and mutually comparable* — there are ~26 of them, every item belongs
> to one of them or to none, and "which is bigger, Software or Politics" is a meaningful question. A
> tag is *specific, open, and unbounded* — `sqlite`, `rp2350`, `eu-ai-act`, `layoffs` — and asking
> whether there are more `sqlite` items than `rust` items is a question about the reader's interests,
> not about the world.

Two consequences that fall straight out of it and that the implementation must obey:

1. **A category must never be created by the classifier.** The taxonomy is a fixed vocabulary the
   reader may extend deliberately. A classifier that invents categories produces 400 of them in a
   month and the axis stops being comparable, which was the only reason it exists.
2. **A tag may be created by the classifier, but only from a vocabulary the reader can see.**
   Free-form model-invented tags are the same failure one level down: `ai-safety`, `AI safety`,
   `AI-Safety` and `safety-in-ai` arrive as four tags in six weeks. §27.3e is how new tags actually
   enter — through a *suggestion* the reader accepts, never through a write nobody authorised.

### 27.1a Genre is a third thing, it is nearly free, and it does not ship a UI in v1

"Release notes for PostgreSQL 19" and "an essay about why PostgreSQL won" are the same category and
are not the same kind of article. Genre — `news · analysis · opinion · tutorial · release · review ·
interview · roundup · research · announcement · obituary · sponsored` — is orthogonal to subject and
is answered by the same read that answers everything else, so `item_analysis.genre` is **populated
from day one and surfaced by nothing**. It costs one enum column and zero extra requests, and the
first feature that wants it (a "skip the roundups" filter, a reader-mode hint, a digest that leads
with analysis) finds three months of history already there instead of starting a backfill.

This is the pipeline's argument in miniature and it is why it is stated here rather than deferred:
**the expensive part is reading the article, and the read has already happened.**

### 27.2 The pipeline

#### 27.2a Stages, and the global/per-user split that makes it affordable

Items are global (A14). A source with 200 subscribers is fetched once and stored once. Classification
must inherit that or it is 200× the cost of the thing it is classifying — and the first version of
this, written per-subscriber because fan-out is per-subscriber, would have made one model request per
subscriber per article. **That is the mistake this split exists to prevent**, and it is easy to make
because every other per-item feature in this codebase is correctly per-user.

```
poll → IngestItems ─┬─ writes items                 (global, A14)
                    ├─ deliver()  → user_item_state (per user, in the same tx)
                    └─ enqueue JobAnalyze{item_ids} (global)

JobAnalyze  (global, once per item, cap 2)
   stage 1  deterministic analyzers, always, no network
   stage 2  the shared model read — ONLY for items stage 1 could not place (§27.4a)
   writes   item_analysis (one row per item)
   then     enqueue JobFanout{source_id, item_ids}   ← unchanged payload, new ordering

JobFanout   (per source batch, loops subscribers, cap 4)
   per subscriber, in one transaction, in this order:
     a. label      — match the user's taxonomy + tag vocabulary against item_analysis
     b. rules      — rules.Evaluate, which can now see category and auto-tags
     c. state      — upsertState / applyTag / recordHit, exactly as today

JobLabelPlus (per user batch, cap 1, only for users who opted in AND have custom labels
              the deterministic matcher could not resolve — §27.4d)
```

**Fan-out is now enqueued by `JobAnalyze` rather than by ingest.** That is the one behavioural change
to an existing path and it is what makes the ordering a fact rather than a hope: a rule matching
`category = software` must not race the thing that decides the category. The cost is that delivery of
an item's *rules* now waits on analysis. Delivery of the **item** does not — `deliver()` stays inside
the ingest transaction where it already is, so an unread count is still correct the instant the poll
finishes, and a stalled analyzer delays labels, never articles. That distinction is the whole
justification for `deliver()` having been moved into ingest in the first place, and it must not be
undone here.

**Labeling lives inside fan-out rather than in its own job** because it is a token-set intersection
over a row that is already loaded — tens of microseconds, against the milliseconds fan-out already
spends on rules. A third job kind would double the queue rows and the transaction count to save
nothing, and it would reintroduce the ordering problem it was meant to solve.

#### 27.2b The contributor registry — how a feature injects into the shared read

The requirement, in Cam's words: *"so we avoid reading the feed items multiple times, other features
can inject into the prompting."* Concretely:

```go
// Analyzer is deterministic work over a batch. No network, no clock beyond
// what the batch carries, no per-user state.
type Analyzer interface {
    Name() string                       // stable; keys its slice of the analysis
    Version() int                       // bumped when its output would change → backfill
    Needs() Inputs                      // Title | Summary | Body | Vector | priors
    Analyze(ctx context.Context, b *Batch) error
}

// Contributor is a feature's claim on the SHARED model read. Implementing it is
// the only way to get anything out of the model per-item; there is no second
// path, for the same reason internal/llm is the only path to the provider.
type Contributor interface {
    Name() string
    Priority() int                      // drop order when over budget
    // Instructions returns this feature's fragment of the system prompt, and
    // Schema returns the JSON-schema for its ONE top-level property. The
    // property name is Name(); collisions panic at registration.
    Instructions() string
    Schema() map[string]any
    EstTokens() int                     // its share of the budget, declared
    // Consume receives only its own slice, already unmarshalled from the union.
    Consume(ctx context.Context, b *Batch, raw json.RawMessage) error
}
```

The pipeline composes one request whose `instructions` is the concatenation of the enabled
contributors' fragments and whose `text.format.schema` is an object with one property per
contributor, then splits the reply and hands each contributor its own slice. Five features, one
request, one article read once.

Six rules that keep that from becoming the thing that breaks every feature at once:

1. **Registration is compile-time-ish and fails loud.** Duplicate names, a schema fragment that is not
   a valid strict-mode object, or a fragment whose top-level key is not `Name()` panics at `init`. A
   registry that accepts a bad fragment at runtime turns one feature's typo into every feature's
   outage.
2. **Failure is per-slice.** A slice that fails to unmarshal, or that a `Consume` rejects, fails that
   contributor only. The others keep their answers. There is exactly one exception: a reply that is
   not JSON at all, or that `llm.ErrTruncated` fired on, is a failure of the whole read — and it is
   the reason `MaxOutputTokens` is computed as the **sum of declared `EstTokens` plus a reasoning
   headroom**, not guessed.
3. **The union has a hard width.** Above eight enabled contributors, or above the token ceiling, the
   pipeline drops the lowest-priority ones and **logs what it dropped by name**. A silently narrowed
   read is a feature that appears to work and quietly stopped.
4. **A contributor may not see another contributor's slice.** Chaining ("genre first, then let the
   summariser know it is a review") is a second request, and a second request is a second bill. If two
   features genuinely need to be sequential they are one contributor.
5. **Contributors declare their consent key.** A contributor whose feature is off for this instance is
   not in the union at all — not present-but-ignored. The union that goes out is the union that was
   consented to, and `AuditEgress` is run against the assembled body, not against the template.
6. **The deterministic analyzer is always the fallback.** Every contributor must have a free-tier
   answer for the case where the read did not happen, because the read frequently will not happen:
   no key, budget spent, breaker open, or — the common case by design — the item was not ambiguous
   enough to escalate.

#### 27.2c Everything the pipeline writes is derived

`internal/derive`'s one rule extends to cover this: **`ClearDerived` then a re-run must reproduce
`item_analysis` byte-for-byte**, and a test asserts it. `items` and `engagements` remain the only
irreplaceable tables. That is what makes a wrong lexicon a five-minute fix instead of a migration, and
it is the property that lets §27.9's reclassification exist at all.

Two things are therefore explicitly **not** derived and live outside `item_analysis`: a label the
reader applied by hand, and a label the reader **removed** (§27.5). Both are decisions, not
derivations, and a recompute that erased either of them would be the application arguing with its
user.

### 27.3 Smart — the deterministic tier

Free, always on, zero egress, and it is the product. Smart+ re-ranks and refines Smart's answer;
it never replaces it, and an instance with no API key gets a complete feature.

#### 27.3a A lexicon, not a regex per pattern

The obvious implementation is a `[]*regexp.Regexp` per category, matched against each item. At 26
categories × ~70 terms that is ~1,800 regex scans per article, and at 6,000 articles a day it is 11
million scans — for a job that is supposed to be the *cheap* tier.

**Tokenise once, then look things up.** `internal/textvec` already scans feed text correctly —
apostrophes, hyphens, unicode, and where a sentence ends — and that scan is shared rather than
rewritten, because two scanners diverge silently and the day one of them learns about a new unicode
dash a term stops matching in half the application with nothing failing.

> **Corrected 2026-07-27, during 10.1.** This first read *"reuse `textvec.Tokenize` and
> `textvec.Phrases`"*, and both are wrong for this caller. `Tokenize` is two decisions wearing one
> name — how text is SPLIT, and which words are worth KEEPING — and only the split is right for
> everybody:
>
>   - `MinTermLen` drops everything under three characters, which is `ai`, `ev`, `ui`, `ux`, `os`,
>     `vr`, `5g` and `f1`. A classifier that cannot see "AI" is not a classifier. They are noise in
>     a TF-IDF vocabulary and they are the whole point in a lexicon.
>   - Dropping stopwords breaks phrase adjacency: the term "war on drugs" becomes "war drugs", and
>     "state of the art" becomes the phrase "state art" — a term nobody wrote.
>   - `Phrases` only emits *capitalised* bigrams, by design (it is looking for product names). Every
>     lowercase multi-word term a lexicon is made of — `zero day`, `patch notes`, `clinical trial` —
>     is invisible to it.
>
> So `textvec.Scan` was added: the same scanner, exported, with **no filter**. `internal/classify`
> applies its own (none at all — it generates 1-, 2- and 3-grams over the raw stream and looks each
> one up), and the interest layer keeps `Tokenize` unchanged. One split, two filters, and
> `TestScanAgreesWithTokenizeOnTheSplit` asserts they can only ever disagree about the second.

- **Lexicon match** is a hash lookup of each token and each 2/3-gram against one combined
  `map[string][]weightedLabel`. One pass over the token stream, O(tokens), independent of how many
  categories exist. Adding the 27th category costs nothing per item.
- **Regex is the escape hatch**, for the small set of user-authored patterns that genuinely need one
  (`\bCVE-\d{4}-\d+\b`, `\bRFC ?\d{3,5}\b`). Capped per user (**32 patterns**, and each compiled once
  into the existing `rules` regex cache pattern). Go's RE2 has no catastrophic backtracking, so a
  hostile pattern costs linear time rather than the ReDoS this would otherwise be — worth stating
  because "we accept user regex in a per-item hot loop" is a sentence that should come with its
  reason.

#### 27.3b Scoring, fields, and the right to refuse

```
score(category) = Σ over matched terms:  weight(term) × fieldMultiplier(where it matched)
                  ─────────────────────────────────────────────────────────────────────
                                    saturate(len(matched), k)
```

| Field | Multiplier | Why |
|---|---|---|
| `title` | ×3.0 | A word in a headline was chosen; a word in the body may be an aside |
| `url` slug | ×2.0 | Publishers put their own section in the path — `/technology/`, `/markets/` — and it is the single highest-precision signal available for free |
| `summary` | ×2.0 | Written to describe the piece |
| `source.title` + folder | ×1.0, capped | A feed named "Ars Technica" biases every item toward hardware, which is *usually* right and is exactly how a politics piece on Ars gets misfiled. Capped at one contribution total |
| `body` | ×1.0, first ~2,000 words | Beyond that is comment furniture and related-links |

> **Corrected 2026-07-27, during 10.1.** This first specified a `saturate(n) = 1 + log(1+n)` divisor
> over distinct matched terms, and it was solving the right problem the wrong way round — dividing by
> a function that *grows* with the number of distinct terms penalises breadth, which is evidence,
> instead of penalising repetition, which is not. What shipped is simpler and needs no tuning
> constant: **a term contributes exactly once, at the highest field multiplier it was seen in.** A
> 4,000-word article saying "kubernetes" eleven times scores what a 300-word one saying it once
> scores, because repetition stops being counted at all rather than being attenuated.
>
> Length normalisation survives, with a narrower job. `score /= 1 + log(1 + bodyWords/400)` is the
> same divisor for every label, so **it cannot reorder them** — its only effect is to make `MinScore`
> mean the same thing on a link post and on a feature. Without it the long piece clears any fixed
> floor on breadth alone and the short one never clears it.

**Assignment:**

- **Primary** requires `score ≥ MinScore` (defaults `MinScore = 3.0`, still to be calibrated against
  the corpus in 10.3, which is why `DefaultStrategy` records them as one constant each rather than
  inline).
- **Ambiguous**, not refused, when the runner-up is within `Margin` (default `1.35`).

  > **Corrected 2026-07-27, during 10.1.** The margin was first specified as a second *requirement*
  > for the primary — `score ≥ runnerUp × Margin` — and that reads more cautious than it is. It meant
  > a CVE in a game engine, scoring 8.0 `security` against 7.0 `software`, produced **no category at
  > all**. Two strong signals disagreeing about which section is not the same as no signal, and
  > collapsing them to Unsorted throws away a confident read of what an article is about in order to
  > avoid choosing between two correct answers.
  >
  > So the margin sets `Result.Ambiguous` and nothing else. That is strictly better because it is
  > exactly the signal §27.4a already wanted: with escalation on, the model breaks the tie; with it
  > off, the top scorer wins and a debatable chip is not a wrong one. The margin is now a *routing*
  > decision, not an assignment one, which is the separation of concerns the first draft was missing.

- **Secondary**, up to two, requires `score ≥ MinScore` and no margin. Secondary categories are what
  make "Software" and "Security" both true of a CVE writeup without pretending one of them is the
  section it belongs in. **No primary means no secondary** — an item that cleared nothing is Unsorted
  entirely, rather than carrying two labels it was not confident enough to lead with.
- **Otherwise: nothing.** Not "Other", not "General" — **no row**. An item with no category is
  correct and common, and the list UI shows no chip rather than a wrong one. `topics.MinMembers`,
  `topics.ColdStart` and the discovery ladder's `ErrNoRule` all make the same choice: this codebase
  refuses rather than guesses, and the classifier is the feature where guessing is most visible and
  least forgivable. The Unsorted view (§27.6) is how the reader sees what it declined to place, which
  is also the best source of lexicon fixes anyone will ever get.

#### 27.3c Negative terms — the part that decides whether anyone trusts it

Every real corpus contains "Apple picking season", "the Amazon is burning", "a beach in Java", "Rust
Belt manufacturing", "Python at the zoo", "Mercury in retrograde", "Tesla's 1899 patents", "Meta
questions about the study". A lexicon without anti-signals files all of them under Software and the
reader stops reading the chips within a week.

Each label carries **`exclude` terms** that subtract, and **`require` guards** — a term that only
counts when a second term from a small set is also present in the item. `apple` scores for `hardware`
only alongside one of `iphone · ipad · mac · ios · macos · app store · cupertino · tim cook · vision
pro · airpods`; alone it scores nothing. This is not clever and it does not need to be: forty guarded
terms across the whole taxonomy remove the great majority of the embarrassing misfiles, and the ones
they miss are exactly the ambiguous items §27.4a escalates.

The guard list is **corpus-derived, not imagined** — §27.11's labeled corpus is where a new guard's
justification comes from, and a guard added without a corpus case that motivates it is a guess with
a comment on it.

#### 27.3d The default taxonomy

Twenty-six, flat, no hierarchy. Flat because a two-level taxonomy needs the reader to agree with the
parent split before any of it helps, and twenty-six is the size at which a chip row, a settings
screen and a trends histogram are all still legible. Sub-division is what tags are for.

| # | slug | Name | Anchor terms (sample of ~60–90 each) | Guards / excludes |
|---|---|---|---|---|
| 1 | `software` | Software & Development | compiler, runtime, api, framework, refactor, git, kubernetes, postgres, sqlite, typescript, rust, golang, deploy, latency, open source | `rust` −belt; `python` −snake, −zoo; `go` requires a second software term |
| 2 | `ai` | AI & Machine Learning | llm, transformer, inference, fine-tune, embedding, prompt, gpu training, diffusion, agent, benchmark, hallucination, rag | `agent` requires an AI term; −real estate agent |
| 3 | `hardware` | Hardware & Chips | soc, npu, arm64, fab, tsmc, ryzen, snapdragon, motherboard, thermal, teardown, nanometer, benchmark | `apple`/`amazon` guarded |
| 4 | `security` | Security & Privacy | cve, exploit, ransomware, zero-day, breach, phishing, tls, encryption, malware, patch tuesday, supply chain | `patch` requires security term |
| 5 | `science` | Science & Research | peer-reviewed, arxiv, study finds, physics, genome, quantum, particle, hypothesis, replication | −"study" alone |
| 6 | `health` | Health & Medicine | clinical trial, fda, diagnosis, vaccine, insulin, mental health, cdc, therapy, symptom | |
| 7 | `business` | Business & Companies | acquisition, layoffs, ipo, revenue, quarterly, ceo, startup, funding round, antitrust | |
| 8 | `finance` | Finance & Markets | interest rate, inflation, bond, equities, nasdaq, crypto, mortgage, fed, earnings | `crypto` also `software` secondary |
| 9 | `politics` | Politics & Policy | election, senate, parliament, legislation, referendum, coalition, ballot, campaign | |
| 10 | `world` | World News | ceasefire, border, refugee, summit, sanctions, earthquake, protest, humanitarian | |
| 11 | `law` | Law & Courts | lawsuit, supreme court, indictment, verdict, appeal, settlement, gdpr fine, injunction | |
| 12 | `climate` | Climate & Environment | emissions, wildfire, biodiversity, drought, cop30, carbon, glacier, conservation | `amazon` guarded → forest terms |
| 13 | `space` | Space | orbit, launch, nasa, esa, satellite, mars, telescope, spacex, payload, lunar | |
| 14 | `energy` | Energy & Industry | grid, solar, nuclear, lithium, refinery, turbine, pipeline, battery plant | |
| 15 | `transport` | Transport & Mobility | ev, rail, airline, autonomous, freight, cycling infrastructure, faa, transit | |
| 16 | `gaming` | Gaming | steam, playstation, nintendo, speedrun, patch notes, indie game, esports, mod | `patch notes` beats `security.patch` by weight |
| 17 | `filmtv` | Film & TV | box office, streaming, season finale, director, trailer, netflix, cast, a24 | |
| 18 | `music` | Music | album, tour dates, single, vinyl, label, festival, spotify, mixing | |
| 19 | `culture` | Art & Culture | museum, exhibition, gallery, sculpture, archive, restoration, curator | |
| 20 | `books` | Books & Writing | novel, memoir, publisher, translation, essay collection, prose, manuscript | |
| 21 | `sport` | Sport | fixture, transfer, playoff, championship, injury report, formula 1, olympics | |
| 22 | `food` | Food & Drink | recipe, restaurant, sourdough, espresso, fermentation, michelin, brewing | |
| 23 | `travel` | Travel & Outdoors | itinerary, visa, trail, national park, hostel, flight route, backpacking | |
| 24 | `design` | Design & Typography | typeface, kerning, layout, ux, wireframe, colour palette, accessibility, figma | |
| 25 | `work` | Work & Careers | hiring, remote work, burnout, promotion, union, résumé, interview loop | `interview` guarded (vs genre) |
| 26 | `education` | Education | curriculum, university, tuition, student, pedagogy, accreditation, mooc | |

The **full lexicon is Go, not SQL** — `internal/classify/lexicon/*.go`, one file per category, each
term with its weight and its guards. Three reasons, and the third is the real one:

1. It ships and versions with the build, so an instance cannot be running a lexicon nobody can
   reproduce.
2. It is testable against the corpus in a normal `go test` with no database.
3. **`git blame` on a term answers "why is this here".** A lexicon in a table is a lexicon whose 900
   rows nobody can account for a year from now, and the first thing anyone does with an unaccountable
   lexicon is stop editing it.

The reader's overrides are data (§27.3f). The default is code.

#### 27.3e Tags — a starter lexicon, and how a new tag is actually born

Same machinery, different vocabulary and a much sharper threshold. The starter set is ~250 focused
terms whose *names are also their match terms* — `sqlite`, `postgres`, `kubernetes`, `rust`, `ffmpeg`,
`raspberry-pi`, `llm`, `nvidia`, `openai`, `ransomware`, `gdpr`, `layoffs`, `ipo`, `spacex`,
`formula-1` — grouped so the settings screen can offer them as a dozen packs (*Systems · Web · AI ·
Security · Markets · Space · Motorsport …*) rather than as a wall of 250 checkboxes.

Three constraints, each of which exists because of a specific way auto-tagging goes wrong:

- **Auto-tags are capped at 5 per item** against `MaxItemTags`'s 20, and they need a *higher*
  confidence than categories do (a tag is a claim). An over-tagged row renders as a wall of chips and
  the reader's own tags become invisible inside it — which is the failure that makes people turn
  tagging off entirely rather than tune it.
- **Auto-tagging is off by default**, and the toggle opens a **dry run** first: what the classifier
  *would* have tagged across the last 200 items, before a single row is written. This is §13.4's rule
  — *a rule you cannot dry-run is a rule you are afraid to write* — applied to the one feature that
  writes into the reader's own vocabulary. Categories, by contrast, are **on by default**: they write
  to their own table, they are one click to clear, and an empty category axis makes the feature
  invisible rather than optional.
- **The classifier never invents a tag.** New vocabulary enters through **suggestion**: the entity
  extractor (which `internal/derive` already runs, and which moves into this pipeline — §27.8) counts
  names across the reader's own items, and a name that recurs enough surfaces as *"`Ollama` has
  appeared in 14 of your articles this month — make it a tag?"* One click creates the tag, seeds its
  match terms from the entity's surface forms, and backfills it over the retained window. That is a
  vocabulary that grows with the reader instead of at them, and it is a straight reuse of
  `entity_affinity` (0019) rather than a second entity mechanism.

#### 27.3f The builtin taxonomy is code; the reader's changes are a delta

`user_categories` and `user_tag_rules` store **only differences** from the shipped defaults: disabled,
renamed, re-coloured, extra include/exclude terms, extra regex, a per-label `MinScore` override, and
the Smart+ prompt (§27.4c). A user-defined category is the same row with `builtin_slug = NULL`.

Storing overrides rather than copies is what lets a lexicon improvement in v1.4 reach a reader who
renamed two categories in v1.2. Copy-on-first-edit — the obvious alternative — freezes that reader's
taxonomy at the version they first touched it, permanently and invisibly, and there is no way to tell
afterwards which of their 26 categories are frozen.

### 27.4 Smart+ — the model as the escalation path

#### 27.4a It runs on the ambiguous items, not on all of them

**This is the cost design and it is the most important decision in §27.**

The naive version sends every item to the model. At 150 feeds and ~40 items a day that is ~6,000
requests a day for a self-hosted reader, which is not a feature, it is a subscription. And it spends
the most on the items the free tier already gets right — a Hacker News post about SQLite does not need
a language model to be filed under Software.

So the deterministic pass runs first, always, and the model is asked only when the free tier **says it
is unsure**, which it can already do precisely because §27.3b made refusing an outcome:

| Free-tier outcome | Escalate? |
|---|---|
| Primary assigned, clear margin | **No.** Nothing to buy |
| `MinScore` met but margin thin (two categories within 1.35×) | **Yes** — this is the tie-break case, and it is the one the model is genuinely better at |
| Nothing met `MinScore` (the item is Unsorted) | **Yes**, subject to budget |
| The reader has custom categories or custom tags with prompts | **Yes** for those labels specifically (§27.4d) |
| Body text unavailable and title+summary under 25 words | **No.** There is nothing to read; escalating buys a coin flip at full price |

`escalate: never | ambiguous | always`, defaulting to **ambiguous**. The property that makes it worth
building this way: **the spend falls as the lexicon improves**. Every corpus-driven lexicon fix in
§27.11 permanently removes a class of items from the escalation set. A pipeline that always calls the
model has no such feedback loop and costs the same forever.

> **Measured 2026-07-27, and it corrects this section.** This first said "roughly a quarter to a
> third of items", which was a guess and was optimistic. `TestEscalationRate` over the 302-item
> corpus gives **0.470**, decomposed:
>
> | reason | share | |
> |---|---|---|
> | `confident` | 0.517 | not sent — the gate doing its job |
> | `unsorted` | 0.328 | **the lever** |
> | `not_english` | 0.099 | the free tier cannot read it and the model can |
> | `ambiguous` | 0.043 | the tie-break the margin was built for |
> | `no_text` | 0.013 | nothing worth sending |
>
> **That figure is an upper bound on production, not a forecast of it.** The corpus is deliberately
> not a representative sample: it carries **15% unsortable and 10% non-English** items, far above any
> real subscription list, because neither group can be measured from a naturally-collected sample —
> nobody's feed contains forty-six articles that are definitively about nothing. Over-representing
> them is what makes false assignment measurable at all, and it inflates this total as a side effect.
>
> **`unsorted` at 0.328 is the number to work on, and it is not mainly a gate problem.** At
> `MinScore` 3.0 the free tier declines 30% of the articles that *have* a correct answer (§27.3b),
> and every one of those escalates. Per-label floors on the diffuse categories — §27.11's
> `weakRecall` list — would cut the refusal rate and the escalation rate together. That is the
> feedback loop this section is built around, pointing at a specific piece of work rather than at a
> hope.

#### 27.4b The shared read

One request per **item** (not per batch of items — see below), carrying:

```json
{ "article": { "title": "…", "summary": "…", "body": "…", "source": "…" },
  "labels":  { "categories": [{"slug":"…","name":"…","prompt":"…"}, …],
               "tags":       [{"slug":"…","name":"…","prompt":"…"}, …] },
  "want":    { "category": 1, "secondary": 2, "tags": 5 } }
```

and returning an object with one property per enabled contributor:

```json
{ "classify": { "primary": "software", "secondary": ["security"], "confidence": 0.82,
                "tags": ["sqlite","wal"], "unsure": false },
  "genre":    { "kind": "analysis" },
  "keyphrases": { "phrases": ["write-ahead log", "checkpoint starvation"] },
  "abstract": { "text": "…" } }
```

**Per item, not per batch**, and this is deliberate against the obvious optimisation. Ten articles in
one request share a context, and a model given ten articles and asked to categorise each one
demonstrably drifts toward labelling them consistently *with each other* — a batch from one feed comes
back suspiciously uniform. It also makes one truncation lose ten answers, and it makes `Consume`'s
error handling ten times more consequential. Batching is reserved for the per-user label pass
(§27.4d), where the unit of work genuinely is "these items against this vocabulary".

`confidence` and `unsure` are **the model's own**, and they are used for exactly one thing: `unsure:
true` means fall back to the free-tier answer rather than overwrite it. §11.2's rule holds — a number
a model assigns to its own answer is not evidence — so confidence is stored, shown to nobody, and
used only to break a tie between two labels the model itself returned.

#### 27.4c Per-label prompts

Every category and every tag carries an optional one-or-two-line `prompt`, which is what Cam asked
for and which is the highest-leverage knob in the feature:

> **`security`** — "Assign this when the article is about the security *of* systems: vulnerabilities,
> breaches, defensive tooling, cryptography. Do **not** assign it for physical security, national
> security policy, or job security."

The prompts are assembled into the `labels` block of the payload — attached to the label they belong
to, not concatenated into the system prompt — so a reader who writes forty of them gets forty
instructions the model can attribute rather than a wall of prose. Each is capped (**240 characters**),
and the total labels block is capped (**4,000 characters**, lowest-priority labels dropped and the
drop logged), because the failure here is silent and expensive: a reader tunes ten prompts, the
eleventh pushes the request past the model's attention, and the earlier ones quietly stop working.

Defaults ship with a prompt for each of the 26 built-ins, written to the same standard as the example
above: they state what the label is **not** at least as clearly as what it is, because that is where
the misfiles come from and because `internal/smart`'s existing three prompts already demonstrate the
pattern works.

#### 27.4d The per-user pass, and why it cannot be folded into the shared read

The shared read is global — one article, all tenants. But a *user-defined* category ("Things To Bring
Up In Standup") and its prompt are that user's, and putting them in a global request would mean one
reader's vocabulary shaping every other reader's labels, and one reader's private taxonomy leaving the
machine on behalf of an instance-wide job. Neither is acceptable, and the second is a privacy defect,
not a quality one.

So: the shared read answers **the default taxonomy and the vocabulary-free facets** (genre,
keyphrases, entities, abstract). User-defined labels are resolved in two steps:

1. **Deterministically first, against `item_analysis` rather than against the raw text.** Matching a
   custom label's terms against the stored keyphrases, entities, category scores and vector is far
   better than matching them against the article, because the analysis has already done the hard
   part. Most custom labels resolve here and cost nothing.
2. **`JobLabelPlus`** for what is left: per user, **batched** (up to 20 items × that user's unresolved
   labels in one request), gated on the per-user consent key, rate-limited, and hard-capped by a
   per-user daily budget the settings screen shows. This is the only place a user's own vocabulary
   leaves the instance.

#### 27.4e Egress — §18.8 is amended here, deliberately and narrowly

§18.8's allowlist permits titles and summaries and forbids **full article text**. This feature needs
body text to do its job well, so this is a **named amendment to §18.8**, written down rather than
quietly worked around, with the argument stated so it can be disagreed with:

> **What changes.** `internal/llm` gains one new payload type, `ClassifyPayload`, permitted to carry
> an article's **title, summary, source title, and up to 2,000 words of body text**, together with a
> **label vocabulary** (slugs, names, prompts).
>
> **Why this is a different question from §18.8's.** §18.8 protects the **reader**. Its subject is the
> reading history: what you opened, what you dwelt on, what you starred — a per-item log that
> identifies a person. An article's body is **the publisher's text, fetched from a public URL**, and
> the fact disclosed by sending it is "some instance subscribes to this feed", which is aggregate and
> already implied by the fetch itself. The classifier is asking a question **about the article**;
> §18.8b's re-rank asks a question **about the reader**, and it keeps its allowlist unchanged.
>
> **What is still forbidden, and now explicitly.** No user id, tenant id, folder name, or feed URL. No
> read/starred/dwell state. No notes, ever. No engagement events, no timestamps, no per-item history.
> The **source title** is permitted (it is the byline a model needs to tell a press release from
> reporting) and the **feed URL is not** (it is a stable identifier that lets a provider recognise the
> same instance across requests — §18.8's own reasoning, unchanged).
>
> **The user's label vocabulary is reader data, not publisher data**, and it therefore keeps §18.8's
> treatment: it only leaves under the per-user consent key (§27.4d), never in the global read.
>
> **Enforcement is the same mechanism, not a promise.** `ClassifyPayload` has fields only for what may
> leave, and the audit runs against the **assembled** body in a test — the union read from §27.2b is
> audited as assembled rather than as templated, so a contributor cannot add a key the allowlist has
> not seen.
>
> **Corrected 2026-07-27, during 10.12 — a SECOND allowlist, not additions to `EgressKeys`.** This
> first said "`EgressKeys` gains `article`, `body`, `source`, …", and that would have quietly undone
> the thing §18.8 is built on. `AuditEgress` is one global check, so admitting `body` there makes a
> body legal in a **rank** payload too — and the entire argument for types-as-enforcement is that the
> boundary must be impossible to widen by accident from somewhere else. One shared list means every
> future exception loosens every existing caller, which is the failure-open mode §18.8 rejected a
> scrubbing function for.
>
> So the interest layer keeps `EgressKeys` unchanged, classification carries `ClassifyKeys`, and each
> has its own audit over one shared walk. Two further guards make the split real rather than a
> convention: `TestEgressKeysWereNotWidened` asserts `body` never appears in `EgressKeys` and that it
> still has exactly its original ten entries, and `ForbiddenKeys` enumerates what **no** list may ever
> admit — `url` · `feed_url` · `user_id` · `tenant_id` · `note` · `read_at` · `dwell` ·
> `published_at` · `author` · `folder` — checked against every allowlist in the package. An allowlist
> is a statement about today's payloads; that one is a statement about the boundary, and only the
> second survives the next amendment.

**Consent is two keys, and neither implies the other** — the precedent `smart.subscribe` set for
exactly this shape:

| Key | Layer | Default | Governs |
|---|---|---|---|
| `smart.classify` | **system / owner** | off | May this instance send fetched article text for classification at all |
| `feed.smartPlusLabels` | **per user** | off | May *my* category and tag vocabulary be sent, and may per-user labeling run for me |

`feed.smartPlus` (the §18.8b re-rank) is a third and unrelated key and stays that way. Three keys is
more than a settings screen wants and fewer than the number of distinct decisions a reader is being
asked to make, which is the ratio that matters.

#### 27.4f Budget, breaker, and failing soft

Every failure path ends in the same place: **the free-tier answer, already computed, already written.**
No key, key revoked, budget exhausted, breaker open (TODO 6.11), truncated reply, unparseable slice,
provider 500, context cancelled — all of them leave the item with its deterministic labels and a
`llm_at` that stays NULL, which is also the marker §27.9's backfill uses to retry later.

Two ceilings, because they fail differently: a **per-instance daily token budget** shared with
translation and TTS (one bill, one meter), and a **per-user daily request cap** on `JobLabelPlus`, so
one reader with 300 custom labels cannot spend the instance's budget before anyone else's poll runs.
Both are visible numbers on the settings screen, and hitting either is a **line in the UI**, not a
silent degradation — "Smart+ classification is paused until tomorrow; 1,340 items were classified by
the free tier" is a sentence a person can act on, and an unexplained drop in label quality is not.

### 27.5 Provenance, and never re-applying what the reader removed

This codebase has already paid for this lesson once, in `store/fanout.go`: `ON CONFLICT DO NOTHING`
cannot tell *"never tagged"* from *"tagged once, and the reader took it off"*, so an at-least-once
redelivery silently restores a tag someone deliberately removed. A classifier that re-runs on a
schedule would do this constantly and would be **the** reason people turn it off.

Every label row carries `source ∈ {user, rule, smart, smart_plus}` and `score`, and there is a
**removal ledger** — `label_removals(user_id, item_id, kind, label_id, at)` — written when a reader
removes an auto-applied label. The classifier consults it before every write, forever. Removing a
label is therefore a **standing instruction**, not a state that the next analysis run overwrites, and
it is the same shape as `rule_hits`'s `alreadyFired`: an audit row that survives the deletion of the
thing it describes, which is exactly why `item_tags.applied_by_rule_id` could not be the answer there
and cannot be the answer here.

Three further consequences worth writing down:

- **A hand-applied label is never touched by the classifier**, not even to change its score. `source =
  user` is terminal.
- **Removals are per label, not per item.** Removing `security` from a CVE post must not stop
  `software` from ever being applied to it.
- **A removal is per (user, item, label), and it is also evidence.** Five removals of the same label
  in a week is the strongest possible signal that a lexicon entry is wrong, and §27.6's settings
  screen surfaces exactly that: *"you have removed `gaming` from 7 items — 6 of them matched on `patch
  notes`. Exclude it?"* This is the feature that makes the taxonomy improve by being used, and it
  costs one query over a table that has to exist anyway.

### 27.6 The configuration surface

**Settings → Classification**, a new tab in the existing settings shell, four panels:

1. **Categories** — the 26 built-ins plus the reader's own, each row: enabled · name · glyph · colour ·
   assigned count (30d) · removed count (30d). Expanding one gives include terms, exclude terms,
   guards, regex, `MinScore` override, and the Smart+ prompt, with a **live match count against the
   last 200 items that updates as you type**. That last part is the panel — a term list without a
   live count is a text box you are guessing into.
2. **Tags** — the starter packs, the reader's own tags with their match terms, the auto-tag toggle
   behind its dry run, and the pending **entity suggestions** (§27.3e).
3. **Strategy** — field multipliers, `MinScore`, `Margin`, max secondary categories, max auto-tags,
   whether body text is used, and `escalate: never | ambiguous | always`. Every knob shows what
   changing it does to the last 200 items **before** it is saved.
4. **Smart+** — the two consent keys with plain sentences about what leaves the machine, the model,
   the budget meter, the per-user cap, and a list of the enabled **contributors** by name with what
   each one asks the model for. A reader should be able to read that list and know what their article
   text is being used for; a feature that egresses and cannot produce that list is a feature that has
   not finished.

Plus, outside settings: an **Unsorted** view in the rail (items the classifier declined to place),
which is where the reader corrects it, and where a maintainer gets the only honest sample of what the
lexicon misses.

### 27.7 Schema — migration `0021_classification.sql`

```sql
-- Global, one row per item. Derived: ClearDerived + re-run reproduces it.
CREATE TABLE item_analysis (
  item_id         TEXT PRIMARY KEY REFERENCES items(id),   -- no cascade; see below
  analyzer_version INTEGER NOT NULL,   -- code constant; bump forces re-analysis
  lexicon_hash    TEXT    NOT NULL,    -- hash of the shipped lexicon, same purpose
  lang            TEXT,
  genre           TEXT,                -- §27.1a; populated, surfaced by nothing in v1
  category_scores TEXT NOT NULL,       -- JSON {slug: score} over the DEFAULT taxonomy
  keyphrases      TEXT,                -- JSON []string
  entities        TEXT,                -- JSON [{name,label}] — feeds entity_affinity
  abstract        TEXT,                -- one-line, Smart+ only, NULL otherwise
  vector          BLOB,                -- the TERM-FREQUENCY vector — see the correction below
  analyzed_at     TEXT NOT NULL,
  llm_at          TEXT,                -- NULL = free tier answered; also the retry marker
  llm_model       TEXT
);
CREATE INDEX item_analysis_stale ON item_analysis(analyzer_version, lexicon_hash);

-- The taxonomy. Built-ins are CODE (§27.3f); this table holds the reader's delta.
CREATE TABLE categories (
  id           TEXT PRIMARY KEY,
  tenant_id    TEXT NOT NULL,
  user_id      TEXT NOT NULL,
  builtin_slug TEXT,                   -- NULL = user-defined
  name         TEXT,                   -- NULL = inherit the built-in's name
  glyph        TEXT, colour TEXT,
  enabled      INTEGER NOT NULL DEFAULT 1,
  position     INTEGER NOT NULL DEFAULT 0,
  min_score    REAL,                   -- NULL = inherit
  include_json TEXT, exclude_json TEXT, regex_json TEXT,
  prompt       TEXT,                   -- §27.4c, ≤240 chars
  created_at   TEXT NOT NULL,
  UNIQUE(user_id, builtin_slug),
  UNIQUE(user_id, name)
);

CREATE TABLE item_categories (
  tenant_id   TEXT NOT NULL, user_id TEXT NOT NULL,
  item_id     TEXT NOT NULL, category_id TEXT NOT NULL,
  kind        TEXT NOT NULL,           -- 'primary' | 'secondary'
  score       REAL NOT NULL,
  source      TEXT NOT NULL,           -- 'user' | 'rule' | 'smart' | 'smart_plus'
  assigned_at TEXT NOT NULL,
  PRIMARY KEY (user_id, item_id, category_id)
);
CREATE INDEX item_categories_browse ON item_categories(user_id, category_id, assigned_at DESC);
CREATE UNIQUE INDEX item_categories_one_primary
  ON item_categories(user_id, item_id) WHERE kind = 'primary';

-- §27.5. Survives the deletion of the row it describes. Never garbage-collected
-- while the item exists; it is an instruction, not a cache.
CREATE TABLE label_removals (
  tenant_id TEXT NOT NULL, user_id TEXT NOT NULL,
  item_id   TEXT NOT NULL,
  kind      TEXT NOT NULL,             -- 'category' | 'tag'
  label_id  TEXT NOT NULL,
  at        TEXT NOT NULL,
  PRIMARY KEY (user_id, item_id, kind, label_id)
);
CREATE INDEX label_removals_label ON label_removals(user_id, kind, label_id, at DESC);

-- Tag matching config. The tag itself stays in `tags` (0004) — one vocabulary.
CREATE TABLE tag_rules (
  tag_id       TEXT PRIMARY KEY REFERENCES tags(id) ON DELETE CASCADE,
  tenant_id    TEXT NOT NULL, user_id TEXT NOT NULL,
  auto         INTEGER NOT NULL DEFAULT 0,
  include_json TEXT, exclude_json TEXT, regex_json TEXT,
  min_score    REAL,
  prompt       TEXT
);

ALTER TABLE item_tags ADD COLUMN source TEXT NOT NULL DEFAULT 'user';
ALTER TABLE item_tags ADD COLUMN score  REAL;
```

> **Corrected 2026-07-27, during 10.5 — the vector column cannot hold TF-IDF.** The line above first
> read *"the TF-IDF vector derive stops recomputing"*, and that is not a thing this table can store,
> for a reason that is not an implementation detail:
>
> **TF is a property of the document. IDF is a property of the collection.**
>
> There is no collection here. This row is written once per item, globally, before anyone has read
> it — while `internal/derive` computes IDF over one reader's engaged items in a rolling ninety-day
> window, which is a different corpus per user and per day. Freezing an IDF at ingest would score
> every reader against document frequencies taken from whatever else happened to be in that poll's
> batch, and the numbers would then drift as batch composition changed rather than as anyone's
> interests did.
>
> So the column holds **TF**, and `derive` applies its own IDF at derivation time. That keeps
> derive's semantics exactly as they are today while still removing the expensive half from the hot
> path: tokenising and counting a four-thousand-word article is the cost; looking up a document
> frequency per term is not. The saving A41 was written for survives intact — a derivation fires
> after every poll and every engagement batch, and each one currently re-tokenises every engaged item
> from raw text.

`item_analysis` is the only new **global** table; everything else is per-user and carries `tenant_id`
+ `user_id` for the leak harness (T1). `SubscribersOf`/`ItemsByID`-style unscoped access to
`item_analysis` is by design and goes on `unscopedByDesign` with the same justification ingest already
carries: it holds nothing per-user, so there is nothing for a scope to protect.

### 27.8 What else the pipeline pays for

The classifier is the first customer. These are the ones already visible, and they are the reason the
pipeline is worth building as infrastructure instead of as one feature's internals:

| Consumer | Today | With the pipeline |
|---|---|---|
| `internal/derive` | Rebuilds a TF-IDF corpus from raw item text on **every** derivation, and a derivation fires after every poll and every engagement batch | Reads `item_analysis.vector`. The most expensive part of the interest layer stops being recomputed |
| Entity extraction | Smart+ only, over **engaged titles**, inside `derive` | Runs in the pipeline over **every** item, once, free tier included; `entity_affinity` (0019) gets a real corpus, and §27.3e's tag suggestions fall out of it |
| `internal/topics` | Clusters over vectors it derives | Same clustering, vectors read rather than built; and category share becomes a second, human-legible axis next to cluster share |
| Ranking (§18.4) | Feed, term, domain, topic affinity | **Category affinity** as a term, and §18.4's Explore slot can deliberately serve a starved *category* — legible in a way a starved cluster is not (*"you have read no Science in three weeks"*) |
| Trends (§16) | Topics and volume | A category histogram over time, at no additional cost, which is the single most requested chart in every reader ever shipped |
| Rules (§13) | `title · author · content · url · source · folder · tag · word_count · age · lang` | **`category`** and **`genre`** as new `rules.Field`s. `category = security AND genre = release → tag "patch"` is a rule people actually want, and it is nine lines in `rules.go` |
| Search (§15) | FTS over text | Facets: filter results by category, which is the cheapest large improvement search will ever get |
| Digest / highlights (§18.5) | Picks by score | Picks **per category**, so a daily digest covers the reader's spread instead of five items about one thing |
| Offline packs (§12) | Items by ranking | Can be built per category |

**None of these are in scope for the first landing** and every one of them is a reason the interfaces
in §27.2b are worth getting right on the first pass rather than the second.

### 27.9 Versioning, staleness, and reclassification

`analyzer_version` (a Go constant) and `lexicon_hash` (a hash of the shipped lexicon) are on every
row. Either changing makes existing rows **stale but valid** — the labels stand until they are
recomputed, because the alternative is a deploy that blanks every chip in the app until a backfill
finishes.

A **low-priority backfill job** re-analyses stale rows at a trickle (default 500/hour, one worker,
below every other kind in `DefaultCaps`), newest first, because the item someone might read this
morning matters more than the one from March. Progress is a line on the admin status screen (§9),
because a silent multi-day backfill is indistinguishable from a broken one.

Three explicit reclassification triggers, all of which reuse the same machinery:

1. **Lexicon change on deploy** — automatic, trickled, newest first.
2. **The reader edits a label** — that label only, over the retained window, immediately. Editing
   `security`'s terms and seeing the count change *now* is the entire feedback loop of §27.6.
3. **"Reclassify everything"** — a button, with a count and a confirmation, for after a big taxonomy
   edit. It respects `label_removals` (§27.5), which is precisely the case where a reader would
   otherwise get every label they have ever removed handed back to them at once.

### 27.10 Failure modes, in the order they will actually happen

1. **The lexicon is wrong and the chips are embarrassing.** The most likely outcome by far, and the
   only defence is the corpus ratchet (§27.11) plus the removals-as-evidence loop (§27.5). Ship with
   fewer confident labels rather than more shaky ones — an item with no chip costs nothing and a wrong
   chip costs trust.
2. **Auto-tags flood the vocabulary.** Mitigated by the 5-per-item cap, the higher threshold, the
   off-by-default dry run, and the rule that the classifier never creates a tag.
3. **Analysis falls behind the poller.** `JobAnalyze` is now upstream of fan-out, so a stalled analyzer
   delays *rules*, not delivery. Watch the queue depth per kind that §9 already shows; if analysis is
   chronically behind, the free tier alone still keeps up trivially and `escalate` is the knob.
4. **A model read costs more than expected.** The ambiguity gate is the structural answer; the budget
   ceiling is the hard stop; the meter is how anyone notices before the bill does.
5. **A contributor's schema fragment breaks the union.** Caught at registration for shape, isolated
   per-slice at runtime, and the whole read still has the free-tier fallback under it.
6. **`item_analysis` grows.** ~1–3 KB per item; at 50k items in the retained window that is 50–150 MB,
   the same order as `item_embeddings` (§18.8a's 51 MB) and on the same retention sweep. Vectors are
   pruned (`Vector.Prune`) before storage, as `textvec` already supports.
7. **Two writers race on one item's analysis.** `JobAnalyze` is capped at 2 and the write is an upsert
   keyed on `item_id`, so the loser overwrites with an identical derivation. Harmless by construction —
   which is only true because it is derived.

### 27.11 Tests, and the corpus ratchet

The one that matters: **`internal/classify/lexicon/testdata/corpus.jsonl`** — a few hundred real feed
items, hand-labeled, committed. `TestTaxonomyPrecision` asserts **per-category precision and recall
floors** and the floors **only go up**. This is the same ratchet discipline the motion spec and the
coverage manifest already use in this house, and it is the only thing that stops a lexicon from
decaying one well-meaning term at a time. A term added without a corpus case that motivates it is a
guess.

> **Built 2026-07-27 — 302 items**, 249 real (pulled read-only from the development database) and 53
> written for the categories a tech-only feed set has no examples of, of which **46 have no correct
> category at all**. That last group is the one most likely to be skimped on and the only way to
> measure false assignment; the corpus test asserts its size for that reason.
>
> It lives beside the lexicon rather than in `internal/classify`, because `go test` resolves
> `testdata/` per package and a test reaching up out of its own directory breaks the day somebody
> moves a package. The corpus grades the taxonomy, so it belongs to the taxonomy.
>
> **First measurement: precision 0.83–1.00 across almost every category, and recall as low as 0.00 —
> with every one of the twelve worst confusions being `X→(none)`.** That is a precise and reassuring
> diagnosis. The lexicon was not misfiling articles, it was *refusing* them, which means the term
> lists are right and only the bar was wrong. The opposite result — confident misfiles — would have
> meant rewriting every category.
>
> **The recall ratchet is a named list, not a lower floor.** Three categories (`politics` 0.125,
> `science` 0.273, `transport` 0.375) do not clear the 0.45 bar, and lowering the bar to accommodate
> them would assert nothing about the other twenty-three. They are enumerated with their measured
> values instead: each may not get worse, the list may only shrink, and **a category that starts
> clearing the floor fails the test until it is removed from the list** — because an exception that
> outlives its problem is a permanently lowered bar. What the three have in common is diagnostic:
> their vocabulary is diffuse, and the intended fix is a per-label `MinScore`, not a global one.

| Test | Asserts |
|---|---|
| `TestTaxonomyPrecision` | Per-category precision/recall floors over the corpus. Ratchets |
| `TestDeterministic` | Same item + same lexicon → identical scores, twice, in either order |
| `TestClearDerivedReproduces` | `ClearDerived` + re-run reproduces `item_analysis` exactly (extends the existing derive test) |
| `TestRemovalIsHonoured` | Remove an auto-label, re-run analysis three times, it stays gone |
| `TestUserLabelNeverOverwritten` | `source='user'` survives every recompute path |
| `TestNoCategoryIsAnAnswer` | An off-topic item gets no row, not a default one |
| `TestGuardTerms` | The Apple/Amazon/Java/Rust-Belt corpus cases, individually |
| `TestEgressAllowlist` | The **assembled** classify body and the **assembled union** pass `AuditEgress`; a contributor adding a key fails the test |
| `TestNoUserVocabularyInGlobalRead` | The global payload contains no user-defined label, for a user who has forty |
| `TestConsentGates` | With `smart.classify` off, zero outbound requests, whatever else is enabled |
| `TestUnionIsolation` | One contributor returning garbage does not cost the others their answers |
| `TestBudgetExhaustionFallsBack` | Budget spent → free-tier labels written, `llm_at` NULL, no error surfaced to the reader |
| `TestClassifyThroughput` | 10,000 items through the deterministic pass under a wall-clock floor, so the lexicon cannot quietly become O(categories × terms) |
| `TestUserRegexBounded` | 32-pattern cap enforced; a pathological pattern is linear (RE2) and does not stall a batch |
| **T24** | **The corpus ratchet is a named test in the plan's test register**, because a ratchet nobody named is a ratchet somebody will lower |

### 27.12 Cost, in tokens rather than currency

Prices move and a plan that quotes them is wrong within a quarter. Per **escalated** item, with the
default contributor set: ~1,400–2,200 input tokens (2,000 words of body dominates) and ~150–400
output.

Volume depends entirely on the escalation rate, and §27.4a now carries a measured one — **0.470 on
the corpus, which is an upper bound** because that corpus over-represents the two groups that always
escalate. Both ends are worth writing down rather than picking the flattering one:

| | escalation | reads/day | input | output |
|---|---|---|---|---|
| **corpus rate** (upper bound) | 47% | ~2,800 | ~5.0M | ~0.8M |
| **plausible production** | ~30% | ~1,800 | ~3.2M | ~0.5M |

at 150 feeds × ~40 items/day on the default small model. The gap between those rows is mostly
`unsorted`, which is lexicon work rather than a fixed cost — see §27.4a.

Three levers, in the order they should be reached for: **`escalate: never`** (free tier only, and the
product still works), **body off** (title + summary only — roughly a fifth of the tokens and
noticeably worse on long-form), and **fewer contributors**. The meter shares §18.8's, so one number
answers "what is Smart+ costing me" across translation, speech and classification.

### 27.13 Deliberately not in v1

Hierarchical categories · per-source category overrides ("everything from this feed is Gaming" — a
rule already does it) · cross-user taxonomy sharing · embedding-based category matching (§18.8a's
vectors will be better than the lexicon and are a second implementation, so they wait until the corpus
can prove it) · a genre UI (§27.1a) · multi-language lexicons beyond the English default (`lang` is
recorded, and a non-English item is simply left unsorted rather than mislabeled — refusing is the
correct behaviour and it is already the behaviour).

### 27.14 Register

| Id | Entry |
|---|---|
| **A41** | **One analysis pass per item, globally, and every per-item feature reads it.** No feature may re-derive from raw item text what `item_analysis` already holds, and no feature may make its own per-item model request — it registers a `Contributor` or it does without |
| **D23** | **The word "category"** (§27.0a). Recommendation: rail's "Categories" → "Folders"; "Category" becomes the article axis. **Open — Cam's call** |
| **R23** | **A wrong chip is worse than no chip.** Classification is the most visible surface in the app and the only one that is confidently wrong in public. Mitigated by refusing to classify (§27.3b), the corpus ratchet (§27.11), removals-as-evidence (§27.5), and shipping categories on / auto-tags off (§27.3e) |
| **T24** | **`TestTaxonomyPrecision`** — the corpus ratchet (§27.11) |
| **M29** | **Classification and the item pipeline.** §27. `internal/classify` · `internal/pipeline` · `0021` · `JobAnalyze` · fan-out reordering · Settings → Classification · the Unsorted view. Depends on M17 (Smart+ egress harness) for §27.4e and on nothing else |
