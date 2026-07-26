# Tidings — build order

*Companion to `plan.md` (rev 8). The plan is organised by **feature**; this is organised by
**dependency** — atoms first, then composites, then systems. Nothing here should be startable before
everything above it is done.*

**How to use it:** top to bottom. Each item is roughly one sitting. `←` marks a non-obvious
dependency. "Done when" is the acceptance bar, and it is usually a passing test rather than a working
screen. **You will not see a UI until Tier 8. That is correct and it will feel wrong.**

> **Document set:** `plan.md` is the spec of record — decisions (`A#`), open questions (`D#`), risks
> (`R#`), schema, milestones (`M#`), tests (`T#`). **This file owns build order only** and cites the
> rest by id. `FLOWS.md` draws the nine paths that are easy to get subtly wrong. `design/` is the
> visual spec, and is mockups rather than source. **If this file and `plan.md` disagree, `plan.md`
> wins.** See `plan.md` → *The document set*.
>
> **Start at Tier 9** if you are picking up a milestone — each row there is the complete brief:
> the plan sections that define it, the pages, the components, the flow diagram, and the tests.

---

## The five gates

Places where you stop, get a number or an answer, and **write it into `plan.md`** before continuing.
Passing a gate on vibes is how a plan quietly becomes fiction.

| Gate | At | Question | Written into |
|---|---|---|---|
| **G1** | Tier 0 | Does `ncruces/go-sqlite3` FTS5 work in *our* build? | D2 |
| **G2** | 3.7 | Can a repository method leak across tenants? | T1 |
| **G3** | 5.4 | What do the three hot-query shapes actually cost at 50k × 3? | §6.5, R2 |
| **G4** | 7.9 | Does `tidings init` produce exactly one superadmin, once? | §22.3 |
| **G5** | 8.2 | How big is `app.wasm`? | R4 — this decides whether A5 stays affordable |

---

## Tier 0 — Unblock (no code)

- [x] **G1 · D2 · FTS5 spike.** ✅ **PASSED 2026-07-26** — but the desk answer was wrong. FTS5 is **not
      compiled in** (`no such module: fts5` on a plain `sql.Open`); it is a **loadable wasm extension
      registered per connection** via `driver.Open(dsn, fts5.Register)`. External content, trigger
      sync, porter stemming, column filters, `snippet()`, `bm25()` all verified. **Binds 3.3** — both
      A24 pools need the hook, and do **not** import `_ ".../embed"`. Pinned by
      `internal/store/fts5_spike_test.go`. See plan.md §25.1 D2.
- [ ] **D0 · Tag GWC v5.0.0.** CHANGELOG says `v5.0.0 - 2026-07-25`; there is no git tag and the proxy
      returns nothing. Tag and push, or accept `replace => ../GoWebComponents` and that the project
      can't build off this machine — which A9 (remote deploy) makes an actual problem. *Blocks 1.2.*
- [ ] **D7 · Pick an extraction library.** Evaluate 2–3 Go readability ports against 10 saved articles;
      judge by eyeball, the only metric that counts. **Five consumers depend on it** — reader mode,
      bookmark archiving, offline text, ranking text, TTS. *Blocks 4.4, which is now Phase 1.*
- [ ] **D1 · Confirm `gofeed` coverage.** Decision is made (gofeed + our own normalisation layer on
      top). Verify against 20 real feeds incl. one RSS 1.0/RDF and one Atom 0.3, and confirm
      `content:encoded`, `dc:`, `media:`, `itunes:`. *Blocks 4.1.*
- [ ] **D17 · Quota accounting shape.** Sources and items are global and deduplicated, so "storage MB
      per tenant" is undefined. Recommendation in the plan: subscription count + tenant-exclusive
      bytes only. *Blocks 5.2 — `sources` should carry whatever accounting needs.*
- [ ] **D12 · Who are the other tenants?** Family, friends, or public signup. Decides self-signup,
      abuse handling, quota enforcement, deletion obligations, uptime promises. *Blocks 6.1.*

---

## Tier 1 — Repo skeleton

- [x] **1.1** Directory skeleton per plan §5: `cmd/ internal/ client/ proto/ web/ migrations/ e2e/`
- [x] **1.2** `go.mod` (`github.com/monstercameron/Tidings`), Go 1.26, `replace` for GWC ← D0
- [x] **1.3** `buf.yaml` + `buf.gen.yaml` — CashFlux's shape (remote plugins, `paths=source_relative`,
      out `internal/pb`)
- [x] **1.4** **`make.ps1`**, not a `Makefile`: `gen build test wasm run lint migrate` (+ `tools`,
      `dev`, `clean`). **`make` is not installed on this box** and the Windows-native testing rule
      means there is no WSL or Docker to borrow one from — a Makefile nobody here can run, next to
      the script everyone actually runs, is two build systems one of which is a lie. Verb names are
      kept identical so a Makefile stays cheap to add if it ever earns its place.
- [x] **1.5** Build `gwc.exe` from the GWC checkout; pin the command in the Makefile
- [x] **1.6** `.gitignore` — `bin/ web/bin/ *.db *.db-wal *.db-shm backups/`
- [x] **1.7** `cmd/tidings/main.go` — `version` prints and exits; **`serve` is the default and runs
      the boot page on :9000** so there is something to watch while the rest gets built.
      **`go build ./...` green.** Grew past the ticket on purpose: a binary that only prints a
      version gives no feedback loop, and the loop is the point of Tier 1.
- [x] **1.7a Boot page** (`internal/httpx`, `internal/buildstatus`, `client/design`) — **the build
      rendered as a feed**, since a hue-edged row is what this application is made of: each tier is
      a row, finished tiers render *read* and unfinished ones *unread*, so progress needs no
      progress bar. Rows are parsed from **TODO.md on every request**, so the page cannot rot into
      a lie. **This is the A26 proof**: authored entirely with the GWC `css` package and shipped via
      `css.StyleBlock()` — `Global`/`Root`/`Custom`/`Media`/`Keyframes` all verified emitting
      natively, zero `.css` files, zero application JS (the 10s poll is a `<meta refresh>`).
      Responsive: mobile-first base, single-line rows from 640px, `prefers-reduced-motion` honoured.
      `design.HueFor` (TODO 8.5) landed here as the pure function both server and client will share.
- [x] **1.8 CI, before any real code** (§22.14) — `go vet` · `staticcheck` · `go test ./...` ·
      `buf lint` + `buf breaking` · **the four structural guards** (no SQL outside `internal/store`,
      no `syscall/js` outside `client/platform`, no `.css` in the tree, every repo method takes
      `Scope`) · migration-apply · **the `app.wasm` size ratchet**. *Under waterfall-then-implement
      CI is what stops a plausible change from silently violating a decision — it is worth more here
      than in a hand-written codebase, and it is nearly free to add now and painful later.*
      **Done:** `.github/workflows/ci.yml` (3 jobs) + `internal/tools/guards` — guards 1–3 pass, guard 4
      correctly reports **n/a** until `internal/repo` exists, because a guard that passes vacuously has
      quietly stopped being a guard. The size job is a **+5%-over-baseline ratchet per §22.14**, not an
      absolute budget, and no-ops until `client/app` and `wasm-baseline.txt` exist. Both CI jobs check
      out GWC as a sibling — D0's `replace` is otherwise a build that works on exactly one laptop.
      *Inert until this is a git repo with a remote; setting that up is not mine to do.*

---

## ⚠ Build order changed 2026-07-26 — vertical slice first

**What happened:** the tiers were being built bottom-up, and after Tier 1 the only thing running on
:9000 was a build-status page. Cam's response — *"that is a project status page ffs"* — is the
correct one. Under strict atoms→systems ordering the first screen that resembles a reader arrives
after roughly a hundred leaf tasks, which is a long time to look at scaffolding.

**What changed:** a **vertical slice** was cut through every tier to a working reader, and the
remaining breadth is being backfilled behind it. The slice took the narrowest path that is still
*real* — no mocks, no stubbed transport:

| Tier | Taken for the slice | Deferred |
|---|---|---|
| 2 | timeutil · idgen · secret · urlnorm · feeddate · charsetdec · netguard · attrsel · outlinks · textvec | sanitize wrapper (GWC's is used directly) |
| 3 | store.Open (A24 pools + G1 hook) · migrations · 0001_init (13 tables) | backup · the other ~36 tables |
| 4 | feed fetch/parse/normalise (gofeed) | extraction · discovery · IMAP · rules |
| 5 | ReaderRepo · identity | folders · tags · bookmarks · interest |
| 6 | reader.Service · poller | ranking · Smart/Smart+ · notifications |
| 7 | gRPC over the tunnel · SystemService · ReaderService | REST sync API · pack channel |
| 8 | the three-pane reader, keyboard map, responsive | virtual list · resizable panes · settings · admin |

**What did NOT change:** the decisions. A14/A22 global sources, A24's two pools, A25's server `rev`,
A26 Go-all-the-way-down, the §20.7 error taxonomy and keyset cursors are all implemented as written.
The slice narrowed *scope*, not *design* — which is the distinction that keeps this a waterfall
project rather than a rewrite.

**The ordering lesson, recorded because it will apply again:** atoms→systems is right for
*correctness* and wrong for *feedback*. A dependency-ordered plan should still surface a thin
end-to-end path early, because a plan nobody can see running is a plan nobody can correct.

---

## Reading at firehose scale — virtualisation, tags, notes, continuous scroll (2026-07-26)

Cam's 144-feed import made the difference between "works" and "usable" concrete. Six features, each
answering something that only shows up at 3,500 items:

**Virtualised list.** `html.VirtualList` over fixed-height rows. **14 DOM rows** whether 60 or 300+
items are loaded; scrolling moves a window rather than growing the document. The fixed height is not
a shortcut — variable rows need a measurement pass each, which is exactly the O(dataset) cost
virtualisation removes — so the row was designed to a height (104px) rather than the height being
discovered from the row. `ItemRowHeight` and the CSS must agree exactly: a row that renders taller
overlaps its neighbour and the scrollbar starts lying.

**Paged loading**, 60 at a time, edge-triggered on approaching the bottom, with a keyboard-reachable
"Load more" so the feature is not pointer-only.

**Per-feed refresh.** The Refresh chip refreshes the selected feed, not all 151. Refreshing 150
sources because someone wanted one checked is rude to 149 publishers and slow for the person waiting.

**Feed filter.** A toggle to show only feeds with something unread — at 150 subscriptions the sidebar
is mostly feeds that have not published today, and scrolling past them is the actual daily cost.
Persisted server-side. The selected feed is always shown even when empty; hiding what you are reading
is disorienting.

**Tags** (`0004_tags.sql`). Per-user, attached to the subscription rather than the source: the source
is global (A14) but the labelling is personal, and a shared taxonomy would leak one tenant's
organisation into another's sidebar. Created on first use — nobody wants to manage a taxonomy, they
want to label the thing in front of them — and the last association removes the tag, so the list stays
a list of things actually in use.

**Notes** (`0005_notes.sql`), plus a Notes stream. Kept in their own table rather than in
`user_item_state`: read/star is a flag the reading loop writes constantly, a note is prose written
rarely and read deliberately, and a note must survive anything that resets read state. Ctrl+Enter
saves; plain Enter stays a newline, because a note that submits on Enter cannot hold two sentences.
The Notes stream orders by when the *note* changed, not when the article was published — it is a list
of your own writing, and you look for it by when you wrote it.

**Continuous reading.** Scrolling an article to its end opens the next one, the list follows, and
everything scrolled past is read. Verified: ten articles in sequence, sixteen rows marked read, the
current row still visible in the list.

Two implementation notes worth keeping. Setting a scroll container's `scrollTop` **did not survive**
the re-render that opening an article causes; asking the target row to `scrollIntoView` on the next
frame does, because it re-queries the DOM after the window has been rebuilt. And
`dataset.removeProperty` does not exist — it is a `CSSStyleDeclaration` method — and calling it
**panics the entire wasm module**, taking the whole app down mid-drag.

---

## Design parity pass + resizable panes + favicons (2026-07-26)

Cam: *"did you match the html design?????"* — **partially, and less than I had implied.** Screenshotting
the mockup and the build side by side made the gaps concrete:

| `design/03-fanciful.html` | What was built | Now |
|---|---|---|
| **No top bar** | a utilitarian strip across the top | **removed**; search/refresh/connection live in the list header, add-feed at the foot of the rail |
| List pane opens with a **header** — title, subtitle, chips | nothing | built |
| Row order: **title → dateline → reason** | dateline above title, no reason | built (the reason slot falls back to the summary until ranking exists) |
| Article **chip row** (read time, words, actions) | plain buttons | built |
| Streams: Home / Unread / Starred | All / Starred only | Unread added |
| Ranking modes (Top / Explore / Clusters) and the "you open 84%" line | — | **still missing** — needs the ranking engine, not a layout change |

**Resizable panes.** The `.grip` CSS existed and nothing implemented it — a decoration pretending to be
a feature. Now a real pointer drag with `setPointerCapture` (without it the drag dies the moment the
pointer leaves the 10px handle), clamped so a pane can never be dragged to nothing, writing a CSS
variable during the drag and saving **once** at the end.

**Widths persist server-side** (`user_prefs`, new `GetPrefs`/`SetPrefs` RPCs), not in localStorage.
Layout is part of an account, not of a browser: a reader that forgets your panes when you open it on a
different machine has not remembered anything. Verified: drag → reload → the width comes back.

**Favicons, cached 30 days.** `internal/favicon` discovers `<link rel="icon">` and falls back to
`/favicon.ico`, all through netguard. Cached **by host**, not by source, because several feeds share a
site. Served from `/favicon?host=` with `max-age=2592000`. Three details that matter:

- **SVG is refused.** It is a document format that can carry script and this is served from our own
  origin — an SVG favicon is stored XSS wearing an icon's clothes.
- **Absence is recorded.** A host with no icon gets a row and a transparent pixel, so it costs one
  request a month instead of one per render.
- **The hue dot stays underneath the icon.** A site with no icon must still be identifiable, and the
  icon is recognition for sites you already know rather than a replacement for the system.

Two crashes fixed along the way: `dataset.removeProperty` does not exist (that is a
CSSStyleDeclaration method) and calling it **panics the whole wasm module**, taking the app down
mid-drag; and the keyset cursor's raw `0x1F` separator became base64url as §20.7 always specified.

---

## Client bugs found by running it at real scale (2026-07-26)

Cam imported his live FreshRSS export — **144 feeds, 3,562 items** — and the reader became unusable:
500–1000ms lag, the list flickering on every click, rows greying out a beat after being clicked, and
paging that silently did nothing. Five distinct causes, all in the client, all invisible to every
test that did not open a browser:

1. **`ui.UseEffect` with a computed dep list re-ran on every render**, refetching page one constantly.
   This alone produced four of the visible symptoms. Fixed with a `ui.UseRef` one-shot guard. *Treat
   GWC dependency lists as an optimisation hint, not a correctness guarantee.*
2. **Hooks called inside loops.** `itemRow`/`feedRow` were plain functions calling `ui.UseEvent` per
   row, so the hook count changed with the row count — a positional-hook violation, ~200 allocations
   per render, and the flicker on feed change.
3. **Hooks called conditionally.** `listPane` called `ui.UseEvent` after early returns and inside a
   `switch`, which is why "Load more" rendered but did nothing.
4. **The keyset cursor used a raw `0x1F` separator** instead of the base64url §20.7 specifies. A
   deviation from the spec that cost an afternoon.
5. **Scroll paging fired on every scroll event**, scheduling a render per frame — the "jitters up and
   down".

Fixes 2 and 3 were done together by making the row and pane helpers **hook-free** and routing every
click through **`data-*` delegation**: one listener on `#app` (which GWC renders *into* and therefore
never replaces), dispatching by attribute, with current callbacks held in a Ref and every state change
wrapped in `ui.PostAsync`. The sidebar also became a real component so 150 rows stop re-rendering when
an item is marked read.

**Result:** feed switching 500–1000ms → **21–43ms**, optimistic read visible **within 60ms** of the
click, infinite scroll working (60 → 120 → 180), and the list stable while idle.

**The lesson worth keeping:** every one of these passed `go test`, `go vet` and all four structural
guards. They were only findable by driving a browser against a realistic amount of data. A 5-item
fixture would never have surfaced any of them.

---

## Design transcription, the reading stream, verdicts, mobile (2026-07-26, later)

Cam: *"the designs and the implementation are still too far apart"* — then a run of behaviour
requests as he used it. All of it is done; the spec entries are A27–A29 and §20.8–20.12.

**1. The sheet was approximated, not transcribed.** `design/03-fanciful.html` was re-read line by line
and `client/design/{tokens,sheet}.go` rewritten against it. Every one of these was individually
invisible and collectively why the two read as different products: surfaces `#2C2138`→`#2B2239`,
`--line` `#3A2E48`→`#3D3150`, missing `--hair` and `--soft` entirely, rows 104px→**96px**, panes
272/368→**258/412px**, body 15→**14.5px**, item titles in the *reading* face instead of the **display**
face, the hue bar a `border-left` instead of an **inset rounded `::before`** (14px clear of each edge,
3px, rounded outer cap), and entirely absent: the **fractal-noise ground** at 3.5%, the grip hover
affordance, the `.why` highlighter, and the real breakpoints (**1220/900**, not 720/1080).

> `view.ItemRowHeight` and the sheet's `--row` are **one number in two places** and must agree
> exactly. A few pixels of disagreement accumulates down a virtualised list until rows overlap and
> the scrollbar starts lying about where you are.

**2. Loading placeholders everywhere** (§20.8) — skeleton rows in the rail and the list, a
paragraph-shaped skeleton body, a 16:9 image box. Same geometry as the real thing, so nothing moves
when the data lands.

**3. Lazy loading now respects the shape of the feed** (A29, §20.10). `ListItemsResponse.total` +
`store.CountQuery` sharing `listFilter` with `ListItems`. **This is where the session's worst bug
was:** a `ui.State` read inside an async callback returns the value *as of the render that made the
callback*, so `append(items.Get(), page...)` threw away every page that landed in between — sixty
round trips, 380 items kept, and the filler asking forever. The rule (accumulate into a `ui.UseRef`,
reach the callback body through the `actions` Ref) is written up in §20.10 and is the single most
transferable thing learned here.

**4. The reading pane became a stream** (A28, §20.9) — append/prepend, scroll-anchored prepends,
topmost-article tracking, per-article notes and tags through a delegated `input` listener, neighbour
prefetch, and the 900-word clamp. Cam: *"the scroll shouldn't be zapping away the items in the feed
item content window, it should append or prepend"*.

> **Edge-triggered scroll handlers must re-arm on GROWTH, not only on position.** Appending keeps the
> reader inside the trigger zone, so a positional edge fires once and dies — *"you broke immediate
> downward scroll, now I need to scroll up before I scroll back down"*. Both `OnScrollNearEnd` and
> `OnScrollNearTop` now re-arm when `scrollHeight` changes.

**5. Verdicts replace stars** (A27) — migration `0006_rating.sql`, `rating` through repo → service →
proto → client, ▲/▼ chips, Liked and Disliked streams, `l`/`d` keys.

**6. Row states** (§20.12) — `new` / `unread` / `stale` (30d) / `read`, as words and not only colour.

**7. Mobile** (§20.11) — a four-tab sticky bottom bar and a phone-only Settings pane. Before this the
subscription list was unreachable on a phone except by backing out twice.

**8. Smaller ones, all shipped:** a feed-name filter in the rail (appears past 8 feeds); the first few
words of a note in its list row; auto-open of the first unread item when a feed is picked (without
navigating, so a phone is not teleported into an article); the article headline is now the link.

**Also fixed here:** `make.ps1` used `CompressionLevel::SmallestSize`, which is .NET 5+ and resolves
to `$null` on Windows PowerShell 5.1 — it surfaced as an unrelated-looking GZipStream overload error
*and left a truncated `app.wasm.gz`*. Since the server prefers the `.gz`, that silently serves the
previous build. It now compresses to a temp file and moves it into place.

---

## Listening, the palette, keyboard-complete, and the rail (2026-07-26, later still)

Spec entries: A30–A32, §10.7, §20.13–20.14.

**1. Listening** (A31, §10.7) — `speechSynthesis` free and always on, chunked to
sentences to dodge Chrome's fifteen-second cutoff; OpenAI TTS behind `GET /speech`,
three gates, disk cache, `OPENAI_API_KEY` in the environment rather than a flag so it stays out of a
process listing. `internal/tts` holds the allowlist check against the URL *being requested*, not
against the constant, so an edit to the endpoint still fails closed.

**2. Resume** (A30, §20.13) — scope, article and all three filters survive a refresh. The initial
item fetch moved into the prefs effect so there is no flash of the wrong feed.

**3. Keyboard-complete** (A32, §20.14) — roving focus per pane, `1`/`2`/`3` pane jumps, `Ctrl K`
palette, `?` sheet. The sheet is grouped by WHERE a key works, not alphabetically, because that is
the question a reader actually has.

**4. The rail, redesigned** — Cam: *"its spread out a little too much"*. Measured first: **14 feeds
visible in 1000px**, with 80px of masthead, ~200px of stream rows and 36px per section heading — the
column's structure cost more than its contents. Three densities instead of one, the masthead on one
line, and the section heading/divider/control collapsed into a single 22px band where the label rides
the rule. **25 feeds visible** at the same width, and the five identical grey stream dots are gone —
they encoded nothing, and the amber selected-row bar the item list already uses does their job.

> **`display:inline-block` on `.feed-dot` was load-bearing.** It is an `<i>`, and width/height do
> not apply to an inline box. As a direct flex child it was blockified and looked right; inside
> `.feed-icon-wrap` — not a flex container — it collapsed to **0×0**. So every feed WITH a favicon
> lost its hue dot, and every dormant feed or failed icon rendered no marker at all. The symptom was
> a ragged left edge down 151 rows, which reads as a layout bug rather than as missing data. Found by
> screenshotting the rail and measuring `getBoundingClientRect()`, not by reading the CSS.

**5. Smaller ones:** favicons in the item rows and the article eyebrow (source id → host, derived
from the sidebar's own feed list rather than sent on every row); read-later (= `starred`, reusing the
column and scope that already existed) and mark-unread, because a stream that marks things read as
you scroll past them needs a way back; reaching the bottom of an article marks it read; a click
inside one does too.

> **Data note, 2026-07-26.** A bulk *Mark all read* at 14:53 local had flipped **3,549 items** in one
> minute, which is why every feed showed zero unread. Reverted by clearing `read_at` for exactly that
> one-minute bucket — genuinely-read items land in small buckets minutes apart and were left alone.
> 3,554 unread restored across 119 feeds. §18.1 already says bulk reads are neutral for ranking; this
> is the argument for also making them **undoable**, which they currently are not.

---

## Tier 2 — Pure atoms

Leaf packages. No Tidings imports, no DB, no network, table-driven tests. Everything above is made of
these, and each one is genuinely small.

- [ ] **2.1 `timeutil`** — UTC helpers · **wall-clock + IANA window resolution** (quiet hours, digests,
      idle triggers) · `ClampPublished(published, firstSeen)` for feeds claiming 2087 or epoch zero.
      *Done when: a 22:00–07:00 window is correct across a spring-forward and a fall-back date.* §22.9
- [ ] **2.2 `idgen`** — sortable ids · **128-bit unguessable slugs** (public shares) · idempotency keys
      · device and token-family ids
- [ ] **2.3 `secret`** — Argon2id hash/verify **plus a startup tuning benchmark** returning params for
      the box · HMAC · token hashing · symmetric encrypt for mailbox credentials. *Done when: the
      benchmark picks params hitting a target ms budget.* §7.1
- [ ] **2.4 `urlnorm`** — two outputs from one canonicaliser: `Norm` (bookmark identity, unique per
      user) and `DupeKey` (cross-source duplicates). Strip `utm_*`/`fbclid`/`gclid`/`ref`, sort query,
      drop trailing slash and fragment. *Done when: 30 real-world pairs collapse and two genuinely
      different articles don't.* §6.2, §15.3
- [ ] **2.5 `feeddate`** — ~15 layouts incl. the malformed ones (no tz, `GMT+0000`, single-digit days,
      `-0000`). *Done when: a corpus of real broken dates parses.*
- [ ] **2.6 `charsetdec`** — `x/net/html/charset`, reconciling the XML declaration against the HTTP
      `Content-Type` when they disagree. *Done when: Windows-1252 and Shift-JIS fixtures decode.*
- [ ] **2.7 `netguard`** — **the SSRF guard.** Scheme allowlist; reject loopback, RFC1918, link-local,
      ULA, `169.254.169.254`; `CheckRedirect` re-runs **per hop**. *Done when: every reject case has a
      test, including redirect-to-localhost.* **Seven callers depend on this.** §21
- [ ] **2.8 `attrsel`** — parse `h2 a@href` into (selector, attr). **We own this; cascadia has no
      attribute syntax.** §14.2
- [ ] **2.9 `sanitize`** — wrapper over GWC `sanitize` with **named policies**: `feed`, `newsletter`
      (strictest — pixels, remote CSS), `archived`, `public` (excerpt). *Done when: an XSS corpus is
      neutralised under every policy.*
- [ ] **2.10 `outlinks`** — extract and normalise links out of article HTML → domains, skipping
      self-links and nav chrome. Pure. **This is §18.7 rung 1, the best recommendation signal, and it
      costs a URL parse.** ← 2.4
- [ ] **2.11 `textvec`** — TF-IDF term vectors, cosine, and simple agglomerative clustering. Pure.
      **Powers term affinity and topics with no LLM at all**, which is what keeps Smart free. §18.2

> *Done when:* `go test ./internal/...` green and **none of these import each other** (2.10 → 2.4 is
> the only permitted edge).

---

## Tier 3 — Storage foundation

Build the safety rails **before** the first repository, so every repo is born correct.

- [ ] **3.1** `migrations/0001_init.sql` — **all ~49 tables**: §6.2 core, §6.3 A22 rules, §6.6 tags,
      §6.7 identity + interest, **§6.8 the rest** (folders, notes, bookmarks, engagements, jobs,
      settings, auth, offline, notifications). `REFERENCES` on every FK-shaped column, the three §6.5
      indexes, and the folder depth `CHECK`s ← G1
- [ ] **3.2 `store/migrate`** — numbered, forward-only, in a transaction, `schema_migrations` with a
      **checksum guard that aborts startup on drift**, and an **automatic snapshot before applying**.
      No down-migrations; the rollback path is restore. (A23) §22.1
- [ ] **3.3 `store/sqlitex`** — `Open()` via **`driver.Open(dsn, fts5.Register)`, never `sql.Open`**
      (G1: FTS5 is a per-connection loadable extension — a pooled connection that misses the hook
      serves every query fine and fails only on search) with **WAL · `busy_timeout=5000` ·
      `synchronous=NORMAL` · `foreign_keys=ON`** (SQLite defaults it **off**, which would make every
      `REFERENCES` above decorative) · **separate read pool + a single-connection write pool, both
      hooked** · scheduled WAL checkpoint. *Done when: four concurrent writers produce zero
      `SQLITE_BUSY`, **and a `MATCH` succeeds on a connection drawn from each pool**.* (A24) §22.2
- [ ] **3.4 `store/backup`** — `VACUUM INTO` + `PRAGMA integrity_check` + retention. *Done when: a
      backup taken under concurrent writes restores and opens.* §22.5
- [ ] **3.5 `store.Scope`** — `{TenantID, UserID, Caps}`, the **first parameter of every repository
      method**. In the signature, so it cannot be forgotten.
- [ ] **3.6 Two-tenant fixture** — tenants A and B, overlapping sources, disjoint user state. Every
      repo test built after this uses it.
- [ ] **3.7 G2 · The leak-test harness** — reflect over exported repository methods; assert none
      returns a B-owned row under an A scope. **Fails on any method added without coverage.**
      *Done when: it passes with zero repositories and would fail the moment a bad one is added.*
- [ ] **3.8 Build-time check** — fails if `db.Query`/`db.Exec` appear outside `internal/store` §6.1
- [ ] **3.9 A22 deletion-safety test** — deactivating a source, and deleting a user, leave every
      *other* user's favourites, tags, notes and shares intact. **Write it now**, while the schema is
      the only thing that can break it. §6.3

---

## Tier 4 — Domain packages

Composites of Tier 2. Bytes in, structs out. Still no database.

- [ ] **4.1 `feed`** — parse → `ParsedFeed`/`ParsedItem`. gofeed underneath, **our normalisation layer
      on top** so swapping it is one file ← 2.5, 2.6, D1
- [ ] **4.2 `feed/testdata/corpus`** — 25+ real feeds covering every §15.3 format, **saved verbatim
      including the broken ones**. Grows forever: every future bug adds a fixture before it gets a fix.
- [ ] **4.3 `fetch`** — conditional GET (ETag/Last-Modified) · gzip · caps (15s, 5 MB, 5 redirects) ·
      honest UA · **`Retry-After`** · **301 reports the new canonical URL** ← 2.7 §15.4
- [ ] **4.4 `extract`** — readability → clean HTML + plain text ← 4.3, 2.9, D7.
      **Phase 1, not Phase 3** — five features depend on it and rev 7 had it scheduled after two of
      them.
- [ ] **4.5 `opml`** — nested OPML 2.0 both ways. *Done when: out → in → identical.*
- [ ] **4.6 `netscape`** — Netscape bookmark HTML both ways + Chrome JSON in. *Done when: our export
      imports cleanly into a real Chrome.*
- [ ] **4.7 `scrapesel`** — rule + HTML → items ← 2.8, cascadia
- [ ] **4.8 `mailparse`** — MIME → normalised item; prefer `text/html`, fall back to `text/plain`,
      hand off to the `newsletter` sanitiser. ← 2.9 §14.1
- [ ] **4.9 `rules`** — **pure**: `(item, []Rule) → []Action`. Ops, ordering, `stop_processing`. No DB,
      no side effects. *Done when: a table test covers every op and precedence case.* §13.1
- [ ] **4.10 `topics`** — **pure**: vectors → clusters with centroids, top terms, deterministic labels.
      Works on TF-IDF vectors alone; embeddings just make it better. ← 2.11 §18.2
- [ ] **4.11 `rank`** — **pure**: `(signals, item) → (score, []Reason)`. Includes `TopicMatch`,
      `VolumePenalty`, per-source half-life, and **the alternate firehose scoring** that drops
      `FeedAffinity` in highlights mode. *Done when: the golden fixture passes and a deliberately
      lopsided-volume fixture proves a firehose can't dominate.* §18.4–18.5
- [ ] **4.12 `recommend`** — **pure**: candidates + evidence → scored list, with the **health gate**
      (no feed / silent 6 months / >20 per day → rejected). *Done when: a dead site and a firehose are
      both refused, and every survivor carries a human-readable evidence string.* §18.7

> *Done when:* the whole tier is unit-tested with **zero database and zero network**.

---

## Tier 5 — Repositories

One package each, `Scope` first, leak test per repo (3.7 enforces it).

- [ ] **5.1** `tenants` · `users` · `roles` · `user_roles` · `invites` · `devices` · **`api_tokens`
      (scope is a fixed enum, never inherited from the owner's role)** · `shares` · `public_shares`
- [ ] **5.2** `sources` · `subscriptions` — **soft-deactivate, never delete** (A22) · `natural_key`
      **per-user for `kind='mailbox'`** (§6.4) · `home_mode` and the highlights fields ← D17
- [ ] **5.3** `items` · `item_revisions` · `user_item_state` — the denormalised `source_id`/
      `published_at` and **all three §6.5 indexes**
- [ ] **5.4 G3 · Hot-query benchmark** — 50k items × 3 users, **all three shapes**: flat unread count,
      unread-by-newest, **unread-by-folder**. *Do not proceed without numbers.* R2
- [ ] **5.5** `tags` · `item_tags` — A21, prerequisite for both rules and the sync API
- [ ] **5.6** `notes` (private by default) · `bookmarks` · `bookmark_tags`
- [ ] **5.7** `rules` · `rule_hits` · `scrape_rules` · `mailboxes`
- [ ] **5.8** `settings` · `views` · `engagements` (append-only, **with the §18.1 kind taxonomy
      including `impression` and `bulk_read`**) · `audit_log`
- [ ] **5.9** Interest layer: `topics` · `item_topics` · `domain_affinity` · `outlinks` ·
      `recommendations` · `feed_affinity` · `term_affinity` · `home_ranking`. **All derived — a
      `DELETE` and rebuild from `engagements` must produce the same result**, which is the test.
- [ ] **5.10** FTS5 triggers for `items_fts`, `notes_fts`, `bookmarks_fts`, and a search repo over them

> *Done when:* every repo has a leak test, 5.9's rebuild test passes, and **5.4's numbers are written
> into `plan.md` §6.5**.

---

## Tier 6 — Services

Business logic over repositories. Still headless.

- [ ] **6.1 `authn`** — login (hash always run, uniform errors) · **rate limiting + lockout** per-user
      *and* per-IP · refresh families with **reuse detection → revoke the family** · recovery codes ·
      reset tokens · sudo mode ← 2.3, 5.1, D12
- [ ] **6.2 `authz`** — capability set, **static per-method map, fails closed on unmapped**. Serves
      both the tunnel and the REST sync API — one model, not two. §7.5
- [ ] **6.3 `settingsreg`** — typed registry + **system → tenant → user** resolution, returning the
      value *and which layer supplied it* ← 5.8
- [ ] **6.4 `jobs`** — durable queue (`jobs` table), **per-kind concurrency caps** so pack building
      can't starve rule fan-out, retry, restart-survivable §22.7
- [ ] **6.5 `events`** — **per-tenant** ring buffers (~1000 each), `since_seq` replay,
      `RESYNC_REQUIRED`, scope-filtered fan-out. *Done when: tenant A's burst cannot evict tenant B.*
- [ ] **6.6 `ingest`** — fetch → parse → identity/dedup → `dupe_key` → revision detect → store →
      **queue** fan-out. Includes the **flood guard** (§15.5) and **outlink harvesting** (2.10).
      ← 4.1, 4.3, 5.3
- [ ] **6.7 `fanout`** — the per-subscriber job: evaluate rules, write `user_item_state` + `item_tags`,
      emit events, feed ranking signals. **Per subscriber, never once at ingest** — the §13.2 bug —
      and **never inline with the poll**, since it's `O(items × subscribers)`.
- [ ] **6.8 `poll`** — **priority queue by staleness ratio** (not FIFO), backoff, `Retry-After`,
      per-host semaphore, adaptive intervals, **lag metric**, widen-when-behind policy ← 6.4
- [ ] **6.9 `signals`** — impression coalescing · the §18.1 kind taxonomy (**`bulk_read` is neutral,
      never negative**) · scheduled derivation of `feed_affinity`, `term_affinity`, `domain_affinity`,
      topics, and `home_ranking`. *Done when: a simulated `mark all read` over 143 items changes no
      affinity score.* R17
- [ ] **6.10 `recommendjob`** — harvest outlinks and aggregator pass-throughs → candidates → health
      gate → score → `recommendations`. **Rungs 1–3, no LLM.** ← 4.12, 2.10 §18.7
- [ ] **6.11 `llm`** — `Provider` iface; Claude + OpenAI impls; **shared timeout, bounded in-flight,
      circuit breaker**; **egress allowlist enforced and tested** §18.8, §22.8
- [ ] **6.12 `preserve`** — tiered archival (§10.6): eager at ingest for high-affinity and Top-slot
      items · on interaction · **the distress sweep when a source crosses into `failing`** · lifecycle
      transitions `ok → failing → gone` · link-rot checks for engaged items only · eviction that
      **cannot** drop an archive whose origin is dead. *Done when: killing a fixture feed mid-test
      leaves its items still readable.*
- [ ] **6.13 `degrade`** — disk watermark ladder (20/10/5/2%), shedding audio and packs first and
      keeping read state alive longest §22.6

> *Done when:* an integration test polls a fixture feed end-to-end and **two users get correct,
> independent state** — with no server and no UI in the picture.

---

## Tier 7 — Transport and the binary

- [ ] **7.1** `proto/tidings/v1/` — start with `Auth`, `Feed`, `Item`, `Event`. Grow per milestone.
- [ ] **7.2** `buf generate` wired into `make gen`; commit `internal/pb`
- [ ] **7.3** gRPC service impls — **thin**: authz → validate → call Tier 6 → map to pb. No logic here.
- [ ] **7.3a `internal/apierr`** — the §20.7 taxonomy in one place. **Cross-tenant returns `NotFound`,
      never `PermissionDenied`** — the latter confirms the object exists, which is a tenant leak with
      good manners. Structured detail `{code,message,field,quota,retry_after_s}`; `message` is always
      safe to display. *Done when: T1 asserts the code, not just the empty result.*
- [ ] **7.3b `internal/page`** — opaque keyset cursors, `spec_hash`-bound so a cursor from a different
      `ViewSpec` is `InvalidArgument` rather than silently-wrong results §20.7
- [ ] **7.3c `internal/idem`** — `(user_id, key) → response`, 24h TTL, verbatim replay. **Required for
      every outbox-replayed mutation** — a partial drain that reconnects mid-flight must not
      double-apply §12.4, §20.7
- [ ] **7.3d Rate limiters** — the §20.7 table, at the interceptor, per-user and per-IP
- [ ] **7.4** `grpctunnel.Wrap` hardened: `WithAllowedOrigins` (exact) · `WithReadLimitBytes(4<<20)`
      (a deliberate tightening; the library default is 16 MiB) · `WithKeepalive` · the three
      connection/upgrade caps · `WithAuthorize` ← 6.2
- [ ] **7.5** `/healthz` + `/readyz` — **unauthenticated, status code only, deliberately
      information-free** §22.4
- [ ] **7.6** Static serving + `web/` layout
- [ ] **7.7** `cmd/tidings` — config load, **validate-and-fail-loudly at boot** (TLS readable, bind vs
      credentials, storage writable, LLM keys well-formed, IMAP reachable), graceful shutdown
- [ ] **7.8** **Version-skew handshake** — client build stamp in the tunnel handshake, server minimum
      version, refusal below it. The SW-cached wasm makes this inevitable, not hypothetical. §22.10
- [ ] **7.9 G4 · `tidings init`** — create tenant 1 + the first superadmin, or print a one-time
      15-minute enrolment token. **The server refuses to serve while no superadmin exists.**
      *Done when: it runs once, is audited, and cannot be re-run.* §22.3
- [ ] **7.10** `tidings admin reset-password` break-glass §7.2
- [ ] **7.11** `internal/log` — `slog`, leveled, request-id threaded through handlers **and jobs**.
      **Never log** secrets, note bodies, article bodies, or LLM payloads. §22.11

> *Done when:* `tidings init` → login over the tunnel → one unary RPC → one streamed event, driven from
> a Go test client. **That is plan M0's exit criteria, reached properly.**

---

## Tier 8 — Client

First visible pixels. **Design is decided** — `design/03-fanciful.html` (desktop) and
`design/04-fanciful-mobile.html` (phone). Those files are **specifications, not source**: they are
hand-written CSS and vanilla JS, and nobody ports them.

**A26 governs this whole tier. Everything below is Go.** No `.css` files. No application JavaScript.
`syscall/js` only inside `client/platform`. *(Verified against the `css` package: `Raw`, `ColorMix`,
`RadialGradient`, `Media`, `Keyframes`, `Root`, `Global`, `Custom` and `Var` cover the entire design —
`css.Raw` handles the Fraunces variable axes, `backdrop-filter`, `-webkit-line-clamp` and
`env(safe-area-inset-*)`. Note `ColorMix` emits `in oklab`, so tints differ slightly from the mockup.)*

**Two standing requirements on every UI task in this tier** *(directive 2026-07-26)*:

1. **Run the `frontend-design` skill before writing any UI Go.** Not after, and not "the design is
   already decided so it doesn't apply" — the mockups fix the *direction*; each screen still needs its
   own type scale, hierarchy and signature decision, and the skill is what stops a screen from drifting
   into generic-AI-default territory. The two rejected directions are the evidence that skipping this
   step costs a rebuild.
2. **Responsive CSS is part of "done", not a follow-up ticket.** Every component ships its
   `css.Media` breakpoints in the same commit that ships the component. A screen that is only correct
   at desktop width is **not** complete, and 8.x is not checkable until it holds from the phone
   breakpoint up. `design/04-fanciful-mobile.html` is the phone reference.

- [x] **8.1** `web/index.html` — the host page and **the ~15-line bootstrap, which is the entire
      permitted application JS** · `wasm_exec.js` · wasm built via `gwc.exe` into `web/bin/`
      ✅ 2026-07-26 — `web/index.html` + the bootstrap; assembled into `bin/web` by `./make.ps1 wasm`, with precompressed `.gz` siblings written temp-then-move.

- [x] **8.2 G5 · Measure `app.wasm`** ✅ **23.8 MB raw / 5.2 MB gzipped**, 2026-07-26. Recorded in
      `wasm-baseline.txt` and written into plan.md R4. The release build saves <4% over the debug
      build — the symbol table was never the cost; grpc + protobuf + the Go runtime are.
      Consequence: the static handler serves a precompressed `.gz` sibling, and the Service Worker
      stops being a nicety.
- [ ] ~~8.2 original~~ and write the number into `plan.md` R4. **This decides whether A5
      stays affordable.** Fallback order if alarming: imports to plain HTTP, then reconsider gRPC for
      unary while keeping one streaming endpoint.
- [x] **8.3 `client/platform`** — **the only package that imports `syscall/js`.** Typed Go wrappers
      for: SW registration · `interop.PersistentStore` (IndexedDB) · `BroadcastChannel` (leader
      election, §12.5) · `navigator.wakeLock` · `storage.persist()` · Web Push subscribe · pointer and
      touch primitives the resize and swipe work needs. **Build this before any component**, or `js`
      calls leak into the view layer and never come back out.
      ✅ 2026-07-26 — `client/platform` — delegated click/input, scroll metrics, near-end/near-top (re-arming on growth), topmost-child tracking, `KeepScrollAnchored`, `ScrollChildToTop`, pane-resize pointer capture, keydown. Native stub keeps every other client package compilable off-browser. *Not yet: SW registration, IndexedDB, BroadcastChannel, wakeLock, Web Push — those arrive with §12.*

- [ ] **8.4 `web/sw.js`** — the one unavoidable JS file. **App-shell caching only** (packs at M10),
      under ~60 lines, registered from 8.3. Anything cleverer belongs in the wasm app. §12.3
- [ ] **8.4a `client/i18n`** — every UI string through GWC's `i18n` from the **first** component, even
      though only English ships. Retrofitting extraction across ~50 pages and ~90 settings is
      miserable and always gets deferred forever. Locale date/number formatting applies immediately.
      §22.16
- [x] **8.5 `client/design/tokens.go`** — the fanciful palette via `css.Root` + `css.Custom`, the type
      scale, spacing, `css.Preflight()`, and **`HueFor(sourceID)`** — a deterministic per-source hue.
      That hue is the design's one real idea, and it must be a pure function so the sidebar dot, the
      list edge, the highlight tint and the article wash always agree.
      ✅ 2026-07-26 — `client/design/tokens.go` — the palette **transcribed** from `design/03-fanciful.html`, plus `HueFor` (7 named hues, then OKLCH at matched L/C).

- [x] **8.6 `client/data/conn.go`** — dial, auth, **build stamp** (7.8), and a **hand-rolled reconnect
      watch loop** — *not* `WithReconnectPolicy`; CashFlux found it can't fire once a blocking read is
      in flight
      ✅ 2026-07-26 — `client/data` dials the tunnel and reports `ConnState`; the badge reads it.

- [ ] **8.7 `client/data/stream.go`** — event pump on one goroutine → **`ui.PostAsync`**, coalesced on
      a ~100 ms tick. **Never touch state directly.**
- [ ] **8.8 `client/view/model.go`** + `client/data/mappers.go` — **pb → plain view structs.** Nothing
      generated crosses this line. R3
- [ ] **8.9** `client/data/keys.go` + one package-level `query.New(WithStaleTime(30s))`
- [x] **8.10** Connection badge — early, because everything after it lies without it
      ✅ 2026-07-26 — Connection badge in the list header and in mobile Settings — dot plus the word.

- [x] **8.11 `components/itemrow.go`** — **fixed 96px** per the design, hue from 8.5. Settle heights
      before building rows.
      ✅ 2026-07-26 — `view.ItemRowHeight = 96` matching the sheet's `--row`. The two are one number in two places and must agree exactly.

- [x] **8.12 `components/itemlist.go`** — `html.VirtualList`, keyed, per-row memoised R4
      ✅ 2026-07-26 — `html.VirtualList`, keyed by item id, sized to `max(total, loaded)` (A29) with placeholder rows for unloaded indices.

- [x] **8.13 `components/sidebar.go`** — sources with hue dots and unread counts
      ✅ 2026-07-26 — Rail with hue dots, favicons over the dot, unread counts, dormant-feed state, tags, unread-only toggle and a name filter.

- [x] **8.14 `components/article.go`** — tier-0 render, the source-hue radial wash
      (`css.RadialGradient` + `css.Var`), sanitised
      ✅ 2026-07-26 — Article stream: source-hue radial wash, `html.RawHTML` through GWC `sanitize`, the 900-word clamp, notes and tags per article.

- [x] **8.15** `client/app/shell.go` + router; **`client/main.go` under 200 lines**
      ✅ 2026-07-26 — `client/app` + `client/view.Reader`.

- [x] **8.16** Views: unread → all → source → folder
      ✅ 2026-07-26 — All feeds · Unread · Liked · Notes · per-feed · per-tag · search.

- [x] **8.17 Pane resizing in Go** — `ui.UseState` for widths, pointer handlers from 8.3, persistence
      through `interop.PersistentStore`. Keyboard nudge, double-click reset, and the clamp that keeps
      the reading pane from being squeezed out. *The mockup's JS is the spec for the behaviour, not the
      implementation.*
      ✅ 2026-07-26 — Grips as 6px grid tracks, `setPointerCapture`, clamped widths written to CSS custom properties during the drag and persisted to the server on release (`pane.rail` / `pane.list` prefs), restored on connect. **Server-side, not `PersistentStore`** — layout belongs to an account, not a browser.

- [x] **8.18 Responsive** — `css.Media` breakpoints: three panes >1220px, two below, single column +
      drawer <900px. **`ItemHeight` resolves per breakpoint**, so density is a function of viewport
      rather than one global number. §20.4
      ✅ 2026-07-26 — 1220px drops the reading pane, 900px collapses to one column, per the mockup. *`ItemHeight` is still one global number; per-breakpoint density is deferred.*

- [x] **8.19 Mobile shell** — list-as-home, sources in a bottom sheet, article pushes over, actions in
      the thumb arc, safe-area insets via `css.Raw("padding-bottom", "env(safe-area-inset-bottom)")`,
      46px targets, swipe-back through 8.3's touch primitives
      ✅ 2026-07-26 — Sticky four-tab bottom bar (Read · Feeds · Notes · Settings) as a grid row, `env(safe-area-inset-bottom)`, 48px targets, phone-only Settings pane. *Not yet: swipe-back.*

> *Done when:* you can read a feed in a browser **and on a phone**, from another machine, over TLS —
> and `grep -rn "syscall/js" client/ | grep -v platform/` returns nothing. **Plan M4.**

---

## Tier 9 — Systems: the milestone → spec map

Everything above composes. From here **the plan's milestone order governs**. Each row is the complete
brief for that milestone: which plan sections define it, which pages (Appendix A), which components
(Appendix C), which flow diagram, and which test must go green.

| M | Ships | Spec | Pages | Components | Flow | Tests |
|---|---|---|---|---|---|---|
| **M5** | State · **engagement logging incl. impressions** · notes · item tags | §18.1, §6.6 | `/starred` `/notes` `/tag/:tag` | C3 `NoteEditor` `TagPicker` | 5 | T15 |
| **M6** | Rules engine UI (matcher already pure at 4.9) | §13 | `/settings/rules` `/muted` | C6 all | 2 | T6 |
| **M7** | Organise · subscribe · discovery rungs 1–3 · **all interchange** | §11, §15.7 | `/settings/sources` `/settings/data` | C3 `FolderNode` | 6 | T5 |
| **M8** | `ViewSpec` · density · saved views · typography · palette · onboarding **← first daily driver** | §10.2, §20.2, §20.5 | `/settings` shell, `/settings/appearance` `/reading` | C1, C2, `CommandPalette` | — | — |
| **M9** | Bookmarks + archiving + dead links + bookmarklet | §12 | `/b` `/b/unread` `/b/dead` `/b/:id` | C4 all | 9 | — |
| **M10** | **Offline trip packs** · leader election · outbox | §12 (offline), §22.10 | `/settings/offline`, version-skew screen | C8 all | 7 | T10, T11, T12 |
| **M11** | **GReader sync API** + capped tokens + event parity | §15.1, §15.2 | `/settings/apps` | C7 `TokenRow` `CopyField` | 8 | T5, T4 |
| **M12** | **Notifications** + **Trends** + contextual nudges | **§16**, **§17** | `/trends/*` `/settings/notifications` | C5 `TrendChart` `Heatmap` | — | — |
| **M13** | Polish · FTS · a11y + RTL · **restore drill** **← ship line** | §10.1, §22.5 | all empty/error states | C1 `EmptyState` `ErrorState` | — | T8 |
| **M14** | Smart homepage · topics · scorer · tuning panel | §18.1–18.4 | `/` `/trends/topics` | C5 `TuningPanel` `TopicEditor` `SuppressedList` | 5 | T17 |
| **M15** | **Highlights mode** · domain affinity · adaptive intervals | §18.5, §18.6 | `/trends/health` | C3 `ReasonLine` | 2 | T17 |
| **M16** | **Recommendations** rungs 1–3, no LLM | §18.7 | **`/discover`** | C5 `RecommendationCard` `TrialVerdictBanner` | — | — |
| **M17** | **Smart+** · embeddings · re-rank · egress allowlist | §18.8 | `/settings/smart` | C5 `WeightSlider` | — | T9 |
| **M18** | Translation · audio/TTS · podcast player | §10.4, §10.5 | — | C3 player | — | — |
| **M19** | **Admin console** incl. user + tenant deletion | **§9**, §7.9 | `/admin/*` | C7 all, `DeletionPreview` | 3 | T19 |
| **M20** | Folder sharing + contribute matrix + scoped fan-out | §7.8 | **`/shared`** ⚠ | C7 | — | T1 |
| **M21** | Public shared-item feeds | §7.8b | `/settings/sharing` | — | — | T20 |
| **M22** | Newsletters via IMAP, per-user keyed | §14.1, §6.4 | `/settings/newsletters` | — | 1 | T3, T20 |
| **M23** | Outbound webhooks + send-to | §17.2 | `/settings/webhooks` | — | — | T20 |
| **M24** | Article revisions UI + diffs (data since M1) | §10.3 | article overlay | C3 | 1 | — |
| **M25** | Scraped feeds + AI rule drafting | §14.2 | `/settings/sources` | C6 `RulePreview` | 6 | — |
| **M26** | Discovery rung 4 · WebSub · **screensaver** | §11, §15.6, **§19** | `/screensaver` | — | 6 | T20 |

**⚠ `/shared` and `/discover` are new routes** surfaced by Appendix D that predate no plan section —
`/shared` is specified in Appendix D5, `/discover` in §18.7.

---

## Standing rules

- **A repository method without a `Scope` first parameter is a build break, not a review comment.**
- **Every milestone that touches schema ships its migration with it.** No manual `ALTER`s, ever.
- **`foreign_keys=ON` or the whole schema is decorative.** SQLite defaults it off.
- **New parser bug → new corpus fixture, before the fix.**
- **`pb` types never cross into `client/view` or components.**
- **Long jobs never hold the SQLite write lock** — work outside the transaction, take the lock to
  commit.
- **Derived tables must rebuild from `engagements`** and produce the same answer.
- **When the spec is silent, follow plan.md → *When the spec is silent*.** Local choices: decide and
  note. Structural choices — a table, an RPC, a capability, a dependency: **stop and ask.** An open
  `D`: **stop and ask.** An agent that guesses produces work that looks finished.
- **Never hard-delete a `sources` or `items` row.** (A22)
- **No `.css` file exists.** All styling is `css` / `css/u`; `css.Raw` is the escape hatch for
  anything untyped. (A26)
- **`syscall/js` appears in exactly one package**, `client/platform`. A `js` import anywhere else in
  `client/` is a build break.
- **The only JavaScript is `wasm_exec.js`, the ~15-line bootstrap, and `sw.js`.** A fourth `.js` file
  needs a written justification, not a commit message.
- **No file over ~800 lines; `client/main.go` under 200.** Self-imposed, not a GWC rule.

---

## Appendix A — Page inventory

Every surface in the app. **Build order is the milestone column**, not this list. Three columns
matter more than they look: an app feels unfinished exactly where empty, loading, and error states
were skipped, and there are 50 places to skip them.

**Every page owes four states** — `L` loading (skeleton, never a spinner on a list) · `E` empty (an
invitation to act, never a shrug) · `X` error (what happened and what to do) · `O` offline (what's
available from the mirror). Marked below only where the state is *non-obvious*.

### Reader — M4–M8

| Route | Page | Notes |
|---|---|---|
| `/` | **Home** — ranked megafeed, three slots | E: "learning your reading — showing newest for now" |
| `/unread` | Unread | E is the *good* state: "nothing left." Say so warmly. |
| `/all` | All items | |
| `/starred` | Starred | *(plan §20.4 says `/favorites`; A8 and the keymap say starred — **pick `/starred`** and fix the plan)* |
| `/notes` | Noted items | E: "notes you write show up here" |
| `/tag/:tag` | Items by tag | |
| `/muted` | **What rules ate** | Exists so a filter is auditable. E: "no rules are hiding anything" |
| `/source/:id` | One source | X: last fetch error verbatim + retry |
| `/folder/:id` | Folder | |
| `/view/:id` | Saved view | X: the underlying source/tag was deleted |
| `/search` | Search — items, notes, archived bookmark text | E: no results ≠ nothing indexed; say which |
| `?item=:id` | Article overlay on any route | Deep-linkable. O: served from the pack if present |

### Bookmarks — M9

| Route | Page | Notes |
|---|---|---|
| `/b` | All bookmarks | E: install the bookmarklet — the empty state *is* the onboarding |
| `/b/unread` | Read-it-later queue | |
| `/b/tag/:tag` · `/b/folder/:id` | Filtered | |
| `/b/dead` | **Broken links** | The archive's moment: "the site is gone, the copy isn't" |
| `/b/:id` | Bookmark detail + archived copy | X: no archive was captured |

### Interest and analytics — M12, M14–M16

| Route | Page | Notes |
|---|---|---|
| `/trends` | Reading over time, heatmap, streaks | E: needs ~2 weeks of data — say so, don't show zeros |
| `/trends/sources` | Per-source open rate, dwell, rank | |
| `/trends/topics` | **Topics** — rename, merge, split, suppress | The model you can correct |
| `/trends/health` | Inactive · failing · never-opened · noisy | Every row one-click actionable |
| `/discover` | **Recommendations** — evidence, dismiss, trial | E: "not enough reading yet to suggest anything" |

### Modes

| Route | Page | Notes |
|---|---|---|
| `/screensaver` | Fullscreen slideshow — M11 | O: must work from the pack |

### Settings — M8 shell, pages land with their features

| Route | Page | Milestone |
|---|---|---|
| `/settings` | Index — search across all settings | M8 |
| `/settings/profile` | Name, timezone, locale, recovery email | M2/M8 |
| `/settings/account` | Password, recovery codes, sessions, devices | M2 |
| `/settings/appearance` | Theme, typography, density, motion | M8 |
| `/settings/reading` | Render default, mark-read behaviour, keymap | M8 |
| `/settings/sources` | Subscriptions, per-source overrides, fetch timing | M7 |
| `/settings/rules` | Rules list + editor + preview + retroactive | M6 |
| `/settings/smart` | **Weights, slots, serendipity, Smart+, budget** | M14 |
| `/settings/notifications` | Push, digest, quiet hours | M12 |
| `/settings/offline` | Depth, caps, current packs, storage used | M10 |
| `/settings/apps` | **API tokens for Reeder / NetNewsWire** | M11 |
| `/settings/sharing` | Public collections, folder shares | M20–21 |
| `/settings/data` | Import, export, backup, full dump | M7 |
| `/settings/newsletters` | IMAP mailboxes, test connection | M22 |
| `/settings/webhooks` | Outbound targets, test, HMAC secret | M23 |

### Admin — M19

`/admin` overview · `/admin/tenants` · `/admin/users` · `/admin/health` (poller lag, error
leaderboard, queue depth, storage by table, LLM spend + circuit state, ring depth per tenant) ·
`/admin/audit` · `/admin/shares`. **All capability-gated and invisible without them.**

### Unauthenticated and system — M2, M10

`/login` · `/recover` (begin · redeem code · redeem token) · `/enroll/:token` (invite or first-run) ·
`/setup` (first-run wizard after `tidings init`) · `/404` · **the version-skew screen** (client below
minimum: purge SW cache and hard reload, §22.10) · **the offline screen** (tunnel down, mirror empty).

---

## Appendix B — Settings registry

**The registry is data, and the UI renders itself from it** (§8) — the only way this survives ~90
settings. Each entry declares key, type, default, range, the scopes it may be set at, and the
capability required. `GET` returns the resolved value **and which layer supplied it**, so "why is this
30 minutes?" is answerable in the UI.

Scope key: **S** system · **T** tenant · **U** user · **F** per-source (on `subscriptions`).

| Group | Settings | Scope |
|---|---|---|
| **Appearance** | theme (light/dark/sepia/high-contrast/auto) · density (compact/comfortable/card) · reading font · size · line height · measure (ch) · paragraph spacing · alignment · direction (auto/ltr/rtl) · image display (full/constrained/hidden) · per-source hue on/off · reduced motion (follow/force) | T,U |
| **Reading** | default render mode (auto/feed/reader/snapshot) · mark read on open · mark read on scroll · keep-unread override · next-unread wraps feeds · restore scroll per feed · confirm mark-all-read · open original in new tab · timestamps (relative/absolute) | T,U |
| **List** | default sort · launch scope · show muted · hide read · page size | U |
| **Fetch timing** | global default interval · interval mode (auto/fixed/manual) · adaptive floor · adaptive ceiling · quiet hours start/end · pause all polling | S,T,U,F |
| **Fetch limits** | scraped-feed minimum interval · per-host concurrency · max redirects · body cap · request timeout · honour robots.txt | S |
| **Smart** | enabled · slot ratios (top/explore/clusters) · serendipity · weights (`w_topic` `w_feed` `w_fresh` `w_corr` `w_manual` `w_vol` `w_dupe` `w_neg`) · engagement half-life · target topic count · cold-start threshold | U |
| **Smart+** | enabled · embedding model · chat model · re-rank candidate count · **monthly budget (cents)** · egress consent acknowledged | T,U |
| **Recommendations** | enabled · rungs enabled (1–5) · trial length (days) · min cadence · max cadence · include adjacent-but-different | U |
| **Per-source** | custom title · folder · interval override · render mode · **home_mode (full/highlights/muted)** · highlights per week · home weight · hide from counts · notify mode · offline depth/count/days | F |
| **Notifications** | push enabled · digest interval · max per window · quiet hours · default notify mode · notify on rule match | T,U |
| **Offline** | default depth (meta/text/media/audio/full) · items per feed · days per feed · **global cap MB** · auto-pack policy · include audio · eviction policy | T,U |
| **Audio / translation** | TTS backend (browser/server) · voice · speed · audio retention days · translation enabled · target languages | T,U |
| **Account** | display name · timezone · locale · recovery email · password · recovery codes · sessions · API tokens | U |
| **Sharing** | public collections enabled · default visibility · noindex · share expiry default | T,U |
| **Data** | retention items per source · **archive policy (text-always / html-on-request / compressed)** · export formats · backup schedule · backups retained | S,T |
| **System** | bind address · TLS cert/key paths · storage path · **disk watermarks (20/10/5/2%)** · max request bytes · rate limits · log level · **minimum client version** · SMTP (optional) · IMAP poll interval | S |

**Three rules for the registry itself:**

1. **Nothing is settable that isn't in the registry.** A hardcoded constant someone later wants to
   change becomes a migration; a registry entry is a config write.
2. **Every setting declares its capability.** `system.*` requires `tenant.settings.write` or higher,
   and the UI hides what the caller can't set rather than erroring after the fact.
3. **Defaults must be good enough to never open this screen.** A registry of 90 settings is a failure
   if the first-run experience requires touching any of them.

---

## Appendix C — Component inventory

Atoms first, same as the tiers. **All Go, all `css`/`css/u`** (A26). A component that needs a browser
API calls `client/platform`, never `js` directly.

### C1 · Primitives — no app knowledge, built once, used everywhere

`Button` (primary · ghost · danger) · `IconButton` · `Chip` · `Tag` · `Input` · `Textarea` ·
`Select` · `Toggle` · `Slider` · `Checkbox` · `Radio` · `Menu` · `Dialog` · `ConfirmDialog` ·
`Sheet` (mobile bottom sheet) · `Toast` + `ToastHost` **with undo** · `Tooltip` · `Skeleton` ·
`Badge` · `ProgressBar` · `Meter` · `Divider` · `Tabs` · `SegmentedControl` · `Disclosure` ·
`KeyHint` · `CopyField` (API tokens, invite codes) · `EmptyState` · `ErrorState`.

> **Undo toasts replace confirm dialogs** wherever the action is reversible. Build `ToastHost` early —
> retrofitting undo means revisiting every destructive call site.

### C2 · Layout

`AppShell` (the 3-pane grid) · `ResizablePane` + `Grip` (TODO 8.17, Go not JS) · `Drawer` (mobile
sidebar) · `BottomTabs` · `ActionBar` (mobile article) · `ScrollArea` · `StickyHeader` ·
**`Page`** — the wrapper that bakes in loading / empty / error / offline so no route can forget them.

### C3 · Reader

**`SourceDot`** (hue from `HueFor(sourceID)` — the design's one idea) · `SourceRow` · `FolderNode`
(nested, disclosure) · `NavItem` · **`ItemRow`** — *three fixed-height variants, 32 / 96 / 140px; the
height is a structural constant, not a style* · **`ItemList`** (`html.VirtualList` wrapper, keyed,
memoised) · `SlotHeader` (Top / Explore / Clusters) · **`ReasonLine`** (explainability + meter) ·
`ClusterCard` · `ArticleHeader` (hue wash, eyebrow, facts) · `ArticleBody` (sanitised) ·
`RenderModeSwitcher` · `ReadingProgress` · **`NoteEditor`** (markdown, debounced autosave, explicit
saved tick) · `TagPicker` · `FeedHealthNudge` · **`ConnectionBadge`** · **`CommandPalette`** ·
`ShortcutSheet` · `SearchBar` · `SearchResult` (with snippet).

### C4 · Bookmarks

`BookmarkCard` · `BookmarkRow` · `ArchiveViewer` · **`DeadLinkBanner`** (the archive's moment: "the
site is gone, the copy isn't") · `BookmarkletInstaller`.

### C5 · Interest and analytics

`TrendChart` · `Heatmap` (time-of-day) · **`TopicChip`** + **`TopicEditor`** (rename · merge · split ·
suppress) · `SourceStatRow` · **`RecommendationCard`** (evidence line, dismiss, start trial) ·
`TrialVerdictBanner` · **`WeightSlider`** + **`TuningPanel`** with live reorder preview ·
**`SuppressedList`** — the "what did you hide from me?" view.

### C6 · Rules

`RuleRow` · `RuleEditor` · `MatchConditionRow` · `ActionRow` · **`RulePreview`** (streaming, per
keystroke) · `RuleHitList` · `UndoBanner`.

### C7 · Settings and admin

**`SettingsField`** — *one component, rendered from the registry (Appendix B), not ninety hand-built
forms. If you find yourself writing a second settings form, the registry is wrong.* ·
`SettingsGroup` · **`ResolvedLayerBadge`** ("set at tenant level") · `DeviceRow` · `TokenRow` ·
`QuotaMeter` · `AuditRow` · **`ImpersonationBanner`** · `HealthTile` · `ErrorLeaderboardRow` ·
`UserRow` · `TenantRow` · `InviteCodeCard` · **`DeletionPreview`** (blast radius before confirm).

### C8 · Offline and platform

`PackBuilder` (estimate → progress) · `OfflineBadge` · `OutboxIndicator` · **`ConflictResolver`**
(notes — keep both, never clobber) · `StorageMeter` · `UpdateAvailableToast` · `VersionSkewScreen`.

---

## Appendix D — User flows by role

Roles from §7.4: **viewer · member · admin · owner · superadmin**. Flows are where the plan gets
tested — modelling these surfaced four screens nobody had planned, marked **⚠ NEW** below.

### D1 · Superadmin

| Flow | Path |
|---|---|
| **First run** | `tidings init` → one-time token printed → `/enroll/:token` → tenant 1 + superadmin → `/setup` wizard → OPML import or starter feeds |
| **Onboard a tenant** | `/admin/tenants` → create → set quotas → mint invite → hand the code over **out of band** |
| **"It feels broken"** | `/admin/health` → error leaderboard → failing source → open it → last error verbatim |
| **Investigate a user's bug** | `/admin/users` → impersonate → **banner stays up** → exit → audit records both ends |
| **Suspend a tenant** | `/admin/tenants` → suspend → sessions revoked → their clients get a distinguishable status |
| **Delete a tenant** | → **`DeletionPreview` enumerates inbound shares** → notify affected tenants → soft-delete → purge after grace |
| **Restore drill** | stop → restore a `VACUUM INTO` snapshot → `integrity_check` → boot → verify. **Before M13, not during an incident.** |

### D2 · Owner / admin (tenant)

| Flow | Path |
|---|---|
| **Invite a member** | `/admin/users` → mint code → hand over → they redeem at `/enroll/:token` → role applied |
| **Reset a password** | mint a 15-min reset token (rung 2) → hand over → their sessions all die on success |
| **A member leaves** | → `DeletionPreview` → **their contributions to shared folders reassign to the folder owner**, not deleted → audit actor anonymised, never erased |
| **Tenant defaults** | `/settings/*` at tenant scope → members inherit → `ResolvedLayerBadge` shows them why |
| **Share a folder out** | folder → share → pick tenant/user → `read` or `contribute` → expiry → later revoke from either side |
| **Watch the AI bill** | `/settings/smart` → budget meter → cap hit → **Smart+ silently degrades to Smart**, page never empties |

### D3 · Member — the flows that matter

| Flow | Path and the part that's easy to get wrong |
|---|---|
| **First login, empty tenant** | onboarding → import OPML **or** pick starters → first poll → home says *"learning your reading"* rather than showing a confidently wrong ranking |
| **First login, populated tenant** | tenant-visible folders appear as **available to subscribe, not auto-subscribed** — silently filling someone's reader with 200 feeds is a bad first impression and unsubscribing 200 is worse |
| **Daily read** | home → `j`/`k` → `o` → read → `s` star · `b` bookmark · `e` note → next unread wraps to the next feed → `A` mark all read (**neutral to the model**, R17) |
| **Subscribe** | paste URL → `DiscoverFeeds` **streams rung by rung** → candidates validated before shown → pick → folder → done |
| **Subscribe, no feed exists** | rungs exhaust → **offer a scrape rule** → live preview per keystroke → save |
| **Tame a firehose** | `/trends/health` noisy list → **app proposes highlights mode** → "about 3 a week" → system solves the cutoff |
| **Write a rule** | `/settings/rules` → build match → **preview against the last N items** → save → optional retroactive apply → undo from `rule_hits` |
| **Check what a rule ate** | `/muted` → too aggressive → loosen → hit counts show precision |
| **Prepare for a plane** | `/settings/offline` → pick views → **size estimate** → build (streamed) → fly → read, star, write a note → land → outbox drains → **note conflict prompts, never clobbers** |
| **Connect a phone app** | `/settings/apps` → mint token (**capped scope, never the owner's role**) → paste into Reeder → read there → **the browser tab updates**, because the sync API raises the same events |
| **Find something new** | `/discover` → evidence line → start trial → 2 weeks in Explore only → **verdict: "you opened 1 of 23 — drop it?"**, defaulting to drop |
| **Correct the model** | `/trends/topics` → rename · merge · suppress · *not an interest* → homepage reorders |
| **Lose the password** | `/recover` → recovery code, **or** ask an admin for a token → all sessions die on success |
| **Leave with the data** | `/settings/data` → OPML + Netscape HTML + notes markdown + full JSON |

### D4 · Viewer (read-only)

Reads, marks their own read state, stars, writes notes. **Cannot** subscribe, unsubscribe, write
rules, mint tokens, or share. The rule: **the action is absent, never present-then-erroring.** The
capability map drives visibility, so an ungranted RPC has no button, and the "you can't do that"
dialog never needs to exist.

### D5 · ⚠ Screens the flows surfaced that weren't in the plan

- **`/shared` — "shared with you."** Cross-tenant folder grants had no landing surface. `ViewSpec` has
  a `SHARED` scope but no route, so a grantee had no way to *find* what they'd been given.
- **`ConflictResolver`.** §12.4 says a note conflict is "surfaced" — nothing said where. It needs a
  real screen, reachable from the outbox indicator.
- **Quota-exceeded state.** A member subscribing past the tenant's source cap has no defined
  experience. Needs an `ErrorState` naming the limit and who can raise it.
- **Trial verdict delivery.** §18.7 ends a trial with a verdict — that has to arrive somewhere (a
  notification plus a `/discover` banner), not sit in a table waiting to be found.

---

## Appendix E — Traceability

Everything in `plan.md` that can be implemented, implemented wrongly, or forgotten, mapped to where it
lands. **If a row here is blank, that thing has no owner.**

### E1 · Services → milestone

All 28 from §20.1. A service split across milestones ships its RPCs incrementally, not the whole
surface at once.

| Milestone | Services |
|---|---|
| **M2** | `AuthService` · `ProfileService` (RPCs) · `UserService` (invite + reset RPCs; UI at M19) |
| **M3** | `SettingsService` |
| **M4** | `ItemService` (list/get/state) · `EventService` · `FeedService` (list subscriptions) |
| **M5** | `NoteService` · `TagService` · `ItemService.RecordEngagement` |
| **M6** | `RuleService` |
| **M7** | `FeedService` (subscribe, folders, `DiscoverFeeds`) · `ImportService` |
| **M8** | `ViewService` · `ProfileService` (UI) |
| **M9** | `BookmarkService` |
| **M10** | `OfflineService` |
| **M11** | `AuthService.MintApiToken` / `ListApiTokens` / `RevokeApiToken` + the REST surface |
| **M12** | `NotifyService` · `StatsService` |
| **M13** | `RenderService` |
| **M14** | `HomeService` · `TopicService` |
| **M16** | `RecommendService` |
| **M18** | `AudioService` · `TranslateService` |
| **M19** | `TenantService` · `AdminService` · `UserService` (UI) |
| **M20–21** | `ShareService` |
| **M22** | `MailboxService` |
| **M23** | `WebhookService` |
| **M25** | `ScrapeService` · `FeedService.AcceptFlood` / `DiscardFlood` |

### E2 · Tests → the milestone that must make them green

| Test | Lands | Test | Lands |
|---|---|---|---|
| **T1** tenant isolation | M1 (3.7, G2) | **T11** two-tab safety | M10 |
| **T2** A22 deletion safety | M1 (3.9) | **T12** version skew | M10 |
| **T3** private-mail isolation | M22 | **T13** flood guard | M1 (6.6) |
| **T4** capability map complete | M2 | **T14** SQLite contention | M1 (3.3) |
| **T5** sync API conformance | M11 | **T15** dedup preserves state | M5 |
| **T6** rules correctness | M6 | **T16** reconnect + per-tenant rings | M4 |
| **T7** migrations | M1 (3.2) | **T17** ranking golden fixture | M14 |
| **T8** backup/restore drill | M13 | **T18** hot-query performance | M4 (5.4, G3) |
| **T9** LLM egress allowlist | M17 | **T19** auth and recovery | M2 |
| **T10** offline round trip | M10 | **T20** webhook SSRF · public feed · newsletter · 301 | M23 · M21 · M22 · M1 |

> **T20 is four tests wearing one number.** Split it when the first of the four lands rather than
> carrying a checkbox that is only ever partly true.

### E3 · Decisions → where each is enforced

A decision nobody enforces is a preference. Structural enforcement beats review comments.

| Decision | Enforced by |
|---|---|
| **A1** server of record + offline mirror | 8.6 read fallback · T10 |
| **A3** GWC v5 | 1.2 · D0 |
| **A4** no client SQLite | **CI: no `db/sqlite` import in `client/`** (1.8) |
| **A5** gRPC transport | 7.4 · G5 size ratchet |
| **A6** render ladder | 4.4 · M13 switcher |
| **A7** discovery deterministic-first | 6.10 · flow 6 · T-none — *add a "no unvalidated candidate" assertion to M7* |
| **A9** remote deployment | 7.7 bind check |
| **A10** standards conformance | 4.2 corpus · T5 |
| **A11/A12** Smart tiers, providers | 6.11 `llm.Provider` · T9 |
| **A13** shared-schema tenancy | 3.5 `Scope` · 3.7 · **CI reflection test** · T1 |
| **A14** source/subscription split | 5.2 · 6.7 fan-out · flow 2 |
| **A15** capability authz | 6.2 fail-closed map · **CI: every RPC mapped** · T4 |
| **A16** three-layer settings | 6.3 · Appendix B |
| **A17** auth, no 2FA | 6.1 · T19 · flows 3–4 |
| **A18** GReader sync API | 7.x REST · T5 |
| **A19** rules engine | 4.9 pure · 6.7 per-subscriber · T6 |
| **A20** IMAP not SMTP | 4.8 · 5.7 · T3 |
| **A21** item tags | 3.1 schema · 5.5 |
| **A22** never hard-delete globals | 5.2 · **3.9 / T2** |
| **A23** migrations | 3.2 · T7 |
| **A24** WAL + single writer | 3.3 · T14 |
| **A25** server `rev` ordering | 8.17 outbox · T10 |
| **A26** Go all the way down | **CI: no `.css`, no `syscall/js` outside `platform`** (1.8) |

**A2 and A8 are framing, not enforceable** — scope and vocabulary. Listed so the absence is deliberate.

### E4 · Risks → where each is mitigated

| Risk | Mitigated at | Risk | Mitigated at |
|---|---|---|---|
| **R0** single factor | 6.1 · T19 | **R11** volume domination | 4.11 lopsided fixture · T17 |
| **R1** cross-tenant leak | 3.7 · CI · T1 | **R12** cold start | 6.9 from M5 · T17 |
| **R2** join cost | 5.4 (G3) · T18 | **R13** newsletter hostile HTML | 2.9 `newsletter` policy · T20 |
| **R3** three transports | one service layer · flow 8 | **R14** scraping bans the IP | 4.7 guardrails · M25 |
| **R4** bundle size | 8.2 (G5) · CI ratchet | **R15** client sprawl | CI line-count check (1.8) |
| **R5** reconnect | 8.5 watch loop · T16 | **R16** scope | §5 phases — *process, not code* |
| **R6** offline conflicts | 8.17 · T10 | **R17** bulk-read poisoning | 6.9 · **TODO test 4** |
| **R7** storage growth | 6.12 · 6.13 ladder · §6.9 | **R18** impressions prerequisite | 6.9 with M14 |
| **R8** irreplaceable data | §6.8 `rev` cols · M7 export · T8 | **R19** bad recommendations | 4.12 health gate · trial verdict |
| **R9** sync drift | T5 real-client smoke, **every release** | **R20** outlink SSRF | 2.7 guard · 6.10 per-run cap |
| **R10** filter bubble | 4.11 explore slot · T17 | | |

### E5 · Open decisions → what they block

| D | Question | Blocks | By when |
|---|---|---|---|
| **D0** tag GWC v5.0.0 | 1.2 | before Tier 1 |
| **D1** gofeed coverage | 4.1 | before Tier 4 |
| **D2** FTS5 (G1) | 3.1 | **before the schema is written** |
| **D5** the name | 1.2 module path | before Tier 1 — *a rename after the module path sets is churn across every import* |
| **D7** extraction library | 4.4 | before Tier 4 |
| **D8** hosting (VPS vs tunnel) | 7.7 bind + TLS config | before M2 |
| **D9** bookmarklet vs extension | M9 | before M9 |
| **D10** two LLM providers | 6.11 interface shape | before M17 |
| **D11** Smart+ model ids | config schema | before M17 |
| **D12** who are the tenants | 6.1 identity model | **before M2** |
| **D13** pack transport | 8.x / M10 | before M10 |
| **D14** email direction | 4.8 / M22 | before M22 |
| **D15** GReader scope | M11 | before M11 |
| **D16** public feed republishing | M21 | before M21 |
| **D17** quota accounting | 5.2 | before Tier 5 |

**D3, D4, D6 are resolved** and carried only for the record.

> **Three block Tier 1 or earlier: D0, D2, D5.** D5 is the one most likely to be waved through and
> most annoying to fix later — the module path bakes the name into every import in the repo.

**Ten of these are choices, not discoveries**, and §25.0 drafts all ten with reasoning so they close in
one sign-off pass: D5, D8, D9, D10, D11, D12, D14, D15, D16, D17. **D12 is the one that removes work
rather than adding it** — invite-only deletes the entire registration, CAPTCHA, email-verification and
abuse-tooling surface.

The remaining five — **D0, D1, D2, D7, D13** — require executing something and stay open by necessity.
They are the reason this plan is not fully waterfall and cannot be: you cannot know a wasm bundle's
size, or whether FTS5 is compiled in, from a document.

---

## The four tests to write early and never let go red

1. **Two-tenant leak** (3.7) — the highest-value test in the project.
2. **A22 deletion safety** (3.9) — one tenant's cleanup cannot touch another's data.
3. **Dedup preserves state** — fetch the same feed twice; read/favourite/note/tags survive for *every*
   subscriber, no duplicate rows, and an edit doesn't reset read state.
4. **Bulk-read is neutral** (6.9) — `mark all read` over 143 items changes no affinity score. The
   failure is silent, and it eats weeks of signal.
