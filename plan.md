# ArticleFlux — a multi-tenant feed reader, bookmark manager, and ranking engine

*GoWebComponents v5 + GoGRPCBridge. Working name. Started 2026-07-26.*

---

## Status — 2026-07-26

**The reader runs.** `./make.ps1 dev` serves it on **:9000**, against real feeds.

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
*Rev 8 — post-review. Two adversarial passes applied: correctness/consistency and operational
lifecycle. §22 (Operations) is new and was the largest hole in rev 7.*

---

## The document set

Four documents, one spec. Each owns something the others must not duplicate, because duplicated facts
drift and drifted facts get implemented.

| Doc | Owns | Does not contain |
|---|---|---|
| **`plan.md`** | **The spec of record.** Decisions (`A#`), open questions (`D#`), risks (`R#`), schema, services, milestones (`M#`), tests (`T#`) | Build order — that's `TODO.md` |
| **`TODO.md`** | **Build order.** Dependency-ordered tiers, the five gates, and the page / settings / component / flow inventories (Appendices A–D) | Decisions. It *cites* them by id |
| **`FLOWS.md`** | **Behaviour of the nine paths that are easy to get subtly wrong**, drawn so the wrong version is visibly wrong | Anything it doesn't draw |
| **`design/`** | Visual spec — palette, type, layout, interaction. **Mockups, not source** (see `design/README.md`) | Implementation. It is hand-written CSS/JS on purpose; A26 governs the real thing |

**Precedence.** `plan.md` wins. If `TODO.md` or `FLOWS.md` contradicts it, they are wrong and get
fixed. **If the implementation contradicts `plan.md`, the plan is wrong — and it gets corrected in the
same change, not later.** A spec that has quietly drifted from the code is worse than no spec, because
it still gets trusted.

**Stable identifiers, all greppable** — use these rather than prose references, so an agent can resolve
them mechanically:

| Id | Meaning | Defined in |
|---|---|---|
| `A1`–`A26` | Settled decisions | §2 |
| `D0`–`D17` | Open decisions | §25 |
| `R0`–`R20` | Risks | §25 |
| `M0`–`M26` | Milestones | §24 |
| `T1`–`T20` | Tests that must stay green | §23 |
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
| **A26** | **Go all the way down** | **All UI logic and all CSS live in GWC.** No `.css` files, no application JavaScript. `syscall/js` is quarantined in one package. The only JS is `wasm_exec.js`, a ~15-line bootstrap, and the Service Worker — which cannot be Go (§20.6). |
| **A27** | **Verdicts, not bookmarks** | **Like / dislike (`user_item_state.rating` ∈ {-1,0,+1}) replaces starring in the UI** (§18.4). Starring answers "keep this"; a verdict answers "was this worth my time", and the *negative* half is the signal ranking actually needs. |
| **A28** | **The reading pane is a stream** | Reaching the end of an article **appends** the next one and scrolling back **prepends** the previous, rather than replacing the pane's contents (§20.9). Scrolling through an article marks it read; scrolling is the whole reading loop. |
| **A29** | **The item list is as long as the scope, not as long as what is loaded** | `ListItemsResponse.total` sizes the virtual list to the true result set from the first paint; unloaded rows are placeholders that resolve (§20.10). |
| **A30** | **Where you were is account state** | Scope, article, and every filter are server-side prefs, restored on connect before the first list is fetched (§20.13). A reader who reloads lands back where they were, on any machine. |
| **A31** | **Listening is free by default and Smart+ by choice** | The browser's own `speechSynthesis` reads articles at no cost, offline, in the voice the reader already chose. OpenAI TTS is an **opt-in per user, on an instance that supplied a key**, behind a host allowlist (§10.7). |
| **A32** | **Keyboard-complete, and it says so** | Arrows move *within* a pane, Tab *between* them, Ctrl-K is the palette, `?` is the sheet that lists all of it (§20.14). |
| **A33** | **A setting is labelled by who it belongs to** | The per-feed panel separates `subscriptions` (yours) from `sources` (shared, polled once for the whole server) and says how many other people are on the other end before you change one (§20.15). |

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
│  ArticleFlux                                                                               │
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
| **5. Platform & extras** | M18–M25 | Admin console · sharing · public feeds · newsletters · webhooks · revisions UI · scraping · AI discovery · WebSub · screensaver |

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
  source TEXT NOT NULL DEFAULT 'manual',   -- manual | rule | sync
  at INTEGER NOT NULL, PRIMARY KEY (user_id, item_id, tag_id)
);
CREATE INDEX item_tags_tag ON item_tags(user_id, tag_id, item_id);
```

**Tags are orthogonal to folders**: a feed lives in one folder; an item carries any number of tags.
Google Reader worked this way, the GReader API models read/starred/broadcast as *system tags* over the
same mechanism, and the rules engine's `tag` action writes here. One table, three consumers.

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
CREATE TABLE engagements (                          -- APPEND ONLY. Everything else rebuilds from this.
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  source_id INTEGER NOT NULL,
  kind TEXT NOT NULL,     -- impression | opened | dwell | scrolled | completed | favorited
                          -- | tagged | noted | bookmarked | skipped | bounced
                          -- | bulk_read | rule_muted | more_like | less_like | not_interested
  value REAL,             -- dwell ms · scroll % · feedback magnitude
  surface TEXT NOT NULL,  -- home | reader | search | sync_api | screensaver
  at INTEGER NOT NULL
);
CREATE INDEX engagements_user_at ON engagements(user_id, at DESC);
CREATE INDEX engagements_item    ON engagements(user_id, item_id);
```

> **`bulk_read` and `impression` are why this table exists in this shape.** Storing a derived
> "open rate" only would make R17 unfixable — you could never re-derive it once you learned that bulk
> reads must not count. **Log the event, derive the score.**

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

### 7.2 Recovery — three rungs, none of it billed

| Rung | Mechanism | Cost |
|---|---|---|
| **1** | **Recovery codes** — 10 single-use, shown once, Argon2id-hashed | $0 |
| **2** | **Admin-minted reset** — single-use, 15-min token from the console | $0 |
| **3** | **Emailed reset link** — *optional*, only if SMTP is configured | ~$0, external dep |

**Break-glass:** `ArticleFlux admin reset-password --user X` from the host filesystem — filesystem access
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
| **2. Page snapshot** | Fetch, inline/proxy assets, sanitize, serve ourselves | one fetch + rewriting | Phase 5 |
| **3. Screenshot stream** | Headless browser → tiles over a server-stream | a browser per request | flag, later |
| **4. Interactive remote browser** | tier 3 + input over bidi | a browser per session | **out of scope** |

**Tier 1 moved to Phase 1 (M2).** Rev 7 scheduled it at M13 while M9 (bookmark archiving) and M10
(offline `text` packs) both required it — §10.1 itself listed them as consumers. It is a dependency of
five features (reader mode, archiving, offline text, ranking text, TTS) and belongs in the foundation.
M13 keeps only the render-mode *switcher* UI.

**About the iframe:** `X-Frame-Options: DENY` and `frame-ancestors` CSP mean a large share of news
sites refuse to be framed, and the parent page can't reliably detect it — you get a blank box. Keep the
button, add a visible "didn't load — try Reader" fallback on timeout, don't build a layout that assumes
it works.

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

**Smart+ — OpenAI `/v1/audio/speech`, `GET /speech?item=<id>`.** Three gates, all
of which must hold, and each answers with a different status because they are
different problems:

| Gate | Failure | Why that code |
|---|---|---|
| Authenticated | `401` | Article text is private |
| Item visible to this scope | `404` — never `403` | §20.7: a permission error confirms the item exists |
| Instance has a key (`OPENAI_API_KEY`) | `501` | The server is fine and simply has no key |
| **This user opted in** (`tts.smartPlus`, default off) | `403` | The one the reader can actually fix |

A plain HTTPS endpoint rather than an RPC, because the client is an `<audio>`
element: a URL lets the browser stream, buffer and cache it, none of which comes
free through a WebSocket and a blob. Audio is cached on disk by
`(item, model, voice)`, so a re-listen costs nothing and a scrub back does not
re-pay for the article. The provider's error text is logged and **never**
returned — provider errors can echo request content, and request content here is
the user's article (§22.11).

The Smart+ toggle sits **next to the play button**, not in settings. It is an
egress decision, and the reader should be able to see its state at the moment
they press play.

---

## 11. Finding the feed: deterministic first, model last

**The LLM proposes, the parser disposes** — every candidate is fetched and parsed before it's offered.

Rungs: (1) `<link rel="alternate">` ~60–70% · (2) path probes ~20% more · (3) platform rules (YouTube,
Reddit, GitHub, Substack, Mastodon) · (4) **LLM proposer** for the tail · (5) **no feed → offer a
scrape rule** (§14.2) · (6) **still nothing → offer newsletter subscription** (§14.1).

**Implementation** (`internal/discover`): **`claude-opus-5`** via `anthropic-sdk-go` ·
**`effort: "low"`** · **leave adaptive thinking on** (the default on Opus 5); do *not* set
`thinking: disabled`, which on this model can emit tool calls as plain text and leak `<thinking>` tags
· **structured output** (`json_schema` → `{candidates:[{url, title, kind, confidence}]}`) ·
**prompt caching** — fixed system prompt, and Opus 5's minimum cacheable prefix is **512 tokens** (down
from 1024 on 4.8), so it caches from the first repeat; page HTML goes after the cached prefix ·
**`web_search_20260209`** for the typed-a-name case · keys server-side only · **off by default.**

---

## 12. Offline — trip packs

### 12.1 Depth and scope

| Depth | Contains | Rough size / 100 items |
|---|---|---|
| `meta` | title, summary, metadata | ~200 KB |
| `text` | + extracted article HTML (tier 1) | ~3 MB |
| `media` | + images, downscaled server-side | ~30 MB |
| `audio` | + generated TTS / enclosures | ~50 MB |
| `full` | + tier-2 page snapshot | ~100 MB+ |

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
RenderService   GetArticle · StreamShot(stream)
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
2. **The bootstrap** — ~15 lines in `index.html` that instantiate the wasm module. Nothing else.
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

**Versioning.** The proto is `ArticleFlux.v1`. **Additive changes only** within v1 — new fields, new RPCs,
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
| An article | `o` original · `l` like · `d` dislike · `t` read later · `U` mark unread · `Ctrl Enter` save note |

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

---

## 21. Security

- **Auth** — `WithAuthorize` at the upgrade *plus* a per-RPC interceptor. Argon2id (tuned),
  device-scoped refresh-token families with rotation and **reuse detection**.
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
  `fetch`, `imgproxy`, `extract`, `discover`, `scrape`, `shot`, pack building, **and outbound
  webhooks**.
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

`ArticleFlux init` — or first-boot detection of an empty `users` table — creates tenant 1 and the first
superadmin, either interactively or by printing a **one-time enrollment token** valid for 15 minutes,
logged loudly. The server **refuses to serve the app** while no superadmin exists rather than starting
in a state where anyone who finds it can claim it.

Also at boot: **validate config and fail loudly** — TLS files readable, bind address consistent with
credentials (§4), storage path writable, LLM keys well-formed if present, IMAP reachable if configured.
A config error should stop startup with one clear line, not surface as a mystery at 3am.

### 22.4 Health and readiness

`/healthz` (process alive) and `/readyz` (DB open, migrations applied, poller scheduled) —
**unauthenticated**, because a supervisor, a Docker healthcheck, and the Cloudflare Tunnel of D8 all
need them before any session exists, and **deliberately information-free**: a status code, nothing else.
§9's rich health screen stays authenticated.

### 22.5 Backups that actually restore

"Nightly, N retained" was a promise with no mechanism. **A raw file copy of a live WAL database is a
torn snapshot.**

**`VACUUM INTO '<path>'`** — SQLite's online backup, safe against concurrent writers, producing a
compact consistent file. Nightly plus **before every migration** (§22.1). Retain N, verify each by
opening it and running `PRAGMA integrity_check` — an unverified backup is a hope. **A documented,
actually-executed restore drill** before M13; the first restore attempt must not be during an incident.

### 22.6 Running out of disk

`content_html` + extractions + archives + embeddings + engagements + packs + revisions + **generated
audio** grow monotonically. A full disk on SQLite is `SQLITE_FULL` mid-write.

**A degrade ladder, by watermark**, so failure is graceful and legible rather than opaque:

| Free space | Behavior |
|---|---|
| < 20% | Warn in admin; stop new audio synthesis and tier-2 snapshots |
| < 10% | Stop pack building and image caching; aggressive retention sweep; notify admins |
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
  SW cache and hard-reloads** rather than retrying forever.
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
    **`ArticleFlux init` creates exactly one superadmin and can't be re-run**.
**T20 · Webhook SSRF**, **public feed safety** (excerpt-only, rotation invalidates), **newsletter
    sanitization** corpus, **301 handling** (URL updates, chain capped, guard re-run per hop).

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
+ CLI break-glass; **`ArticleFlux init` bootstrap**; sudo mode; fail-closed capability map; TLS; bind
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
**M9** Bookmarks + archiving (uses M2's extractor) + dead-link checks + bookmarklet.
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

---

## 25. Open decisions and risks

### 25.0 Proposed resolutions — awaiting sign-off

Fifteen decisions are open. **Ten of them are choices, not discoveries** — they can be settled at a
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
| **D17** quota accounting | **Subscription count + tenant-exclusive bytes.** Shared source/item storage excluded entirely | The only definition that is both enforceable and fair under global dedup (A14). Tenant-exclusive = packs, archives, audio, embeddings, mailbox items, notes, bookmarks | A tenant subscribing to 500 popular feeds costs almost no quota. Correct, and occasionally surprising |

**The five that genuinely cannot be settled at a desk** stay open by necessity, not neglect:
**D0** (tag and push v5.0.0 — an action), **D1** (gofeed coverage against real feeds), ~~**D2**~~
(**closed 2026-07-26 by G1** — and it came back *no*, against the desk read; see §25.1), **D7**
(extraction quality, judged by eyeball), **D13** (pack transport, needs a real 30 MB pack). Each
requires executing something. **That is why this plan is not, and cannot be, fully waterfall** — three
of its numbers are unknowable from a document. D2 is the proof: the desk answer said "largely
resolved," and running it found a per-connection registration requirement that changes `store.Open()`.

**Accepting all ten closes every desk-decidable question and leaves five spikes.** That is the most
plan-complete this design can honestly be before code exists.

---

### 25.1 The open list

**D0 — GWC dependency.** `go list -m -versions .../GoWebComponents/v5` **returns nothing.** The
CHANGELOG has a `v5.0.0 - 2026-07-25` entry but **no matching git tag** — verified. Needs
`replace ... => ../GoWebComponents`, and A9 sharpens it: a remote deployment means building on, or
copying source to, the target box. *Recommendation: tag and push v5.0.0.* *(GoGRPCBridge v1.1.1 is a
real tag matching HEAD.)*

**D1 — Parser.** §15.3 is eleven formats plus seven namespaces. *Recommendation: `mmcdole/gofeed` for
breadth with our own normalization layer on top.* Verified: gofeed does parse the claimed lineage; only
the 0.91-dialect distinction is unconfirmed.

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

**D7 — Extraction library.** Evaluate a Go readability port before writing one — quality is judged by
eyeball across hundreds of sites. Now serves five consumers and lands at M2.

**D8 — Hosting.** *Recommendation: home box + Cloudflare Tunnel* — a real hostname with TLS and no
inbound port. §22.4's `/readyz` is what the tunnel health-checks.

**D9 — Bookmarklet vs extension.** Bookmarklet in v1.

**D10 — Two LLM providers.** `llm.Provider` plus the shared breaker (§22.8) makes consolidation a
config change.

**D11 — Smart+ model IDs.** Config-driven, validated on save.

**D12 — Who are the other tenants? ⚠ Still the biggest open question.** Family, friends, or strangers
with signups? It decides self-signup, abuse handling, quota enforcement, deletion obligations, and
uptime promises. **Answer before M2.**

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

**What was NOT done, deliberately:** TinyGo. It would cut this substantially and would also drop
`syscall/js` compatibility guarantees, reflection-heavy protobuf, and parts of the standard library
this code uses. That is a much larger decision than a build flag, and it should be made against a
measured need rather than a number that looked big.

**The ratchet is now live in CI** (§22.14): +5% over `wasm-baseline.txt` fails the build. The point is
not the current number — it is that the next 5 MB has to be argued for.

**R5 — Reconnect correctness**, now including version skew (§22.10) and hand-rolled reconnect (§20.4).

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

**R13 — Newsletter ingestion is a foothold for hostile HTML.** Strictest sanitization in the app, its
own corpus.

**R14 — Scraping** is fragile and impolite by default; a banned IP takes down *every tenant*.

**R15 — Client sprawl.** Seven shells. **Self-imposed** cap of ~800 lines/file, ~200 for
`client/main.go` — a house rule, not a GWC convention.

**R16 — Scope.** §0 and §5 exist because this grew from "an RSS reader" to a platform. The lines are
the deliverable, not decoration.

---

## 26. Immediate next step

M0: `go.mod` + replace + buf config; minimal `ArticleFlux.proto` with one unary and one streaming RPC;
`buf generate`; `cmd/ArticleFlux` serving `web/` and `/grpc`; build `gwc.exe` from the GoWebComponents
checkout; `client/main.go` dialing the tunnel, calling the unary RPC, rendering a streamed tick through
`ui.PostAsync`. Then weigh `app.wasm`.

Then **M1 → M2 → M3 before anything visual.** M1 now carries the operations floor (migrations, WAL,
backup, health) alongside the schema — all things that are miserable to retrofit and boring to build,
which is exactly why they go first. **Answer D12 before M2**, and **confirm FTS5 in M1 before writing
three FTS tables.**
