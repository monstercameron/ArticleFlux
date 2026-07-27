# ArticleFlux — build order

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
| **G4** | 7.9 | Does `articleflux init` produce exactly one superadmin, once? | §22.3 — ✅ **passed 2026-07-26**: yes, and it refuses to run on a populated instance |
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
- [x] **D7 · Pick an extraction library.** ✅ **RESOLVED 2026-07-26 — `go-shiori/go-readability`.**
      Twelve pages, three libraries, and the scoreboard did not decide it: all three extracted all
      twelve and found a title on all twelve. Two things it does not show did. **Text quality** —
      trafilatura's serialiser inserts spaces around inline elements ("The Room , Troll 2 ,"), 199
      artefacts to readability's 5, and three of the five consumers read the plain text while one
      reads it aloud. **Content retention** — on a photo-heavy review readability kept 34 images to
      trafilatura's 8, out of 117. *The metric that nearly decided it wrongly:* readability's 5.8×
      markup bulk turned out to be 38 KB of `srcset`, which is data and which 2.9 strips before
      storage — a real measurement of something that does not survive to storage.
      ~~Original ticket:~~ Evaluate 2–3 Go readability ports against 10 saved articles;
      judge by eyeball, the only metric that counts. **Five consumers depend on it** — reader mode,
      bookmark archiving, offline text, ranking text, TTS. *Blocks 4.4, which is now Phase 1.*
      ◧ 2026-07-26 (night) — **the harness is built, the eyeball pass is not.** `internal/extract/testdata/articles/` holds **12 committed pages** with their source URLs (Ars Technica, The Verge, Eurogamer, Substack, Wikipedia, MDN, a GitHub README, the Go blog, Paul Graham, Dan Luu, Simon Willison, CNX Software), and `internal/extract/bakeoff/` scores **go-readability · trafilatura · domdistiller** on characters kept, title found, wall time and **boilerplate hits** — a fixed list of phrases ("Subscribe", "Cookie", "Related Stories", "Skip to content") none of which belongs in a reading pane, which is the best automatic proxy available for the judgement a person has to make.
      > It is a **separate Go module** on purpose: two of the three lose, and keeping them in the root `go.mod` would drag wazero, a WASM-compiled re2, zerolog and a date parser into the dependency graph of a server that uses none of them, permanently, to preserve a comparison that runs about once a year. It stays in the tree rather than being deleted because **D7 gets re-litigated**, and the cheapest answer to "why aren't we using X?" is a command anyone can re-run: `cd internal/extract/bakeoff && go test -run TestBakeoff -v`, or `BAKEOFF_DUMP=<dir> … -run TestBakeoffDump` to read the actual output.
      > **Nothing is wired into the app** — `internal/extract` has no non-test Go file — so the §10.1 ladder's tier 2 is still unbuilt and all five consumers are still waiting. *(`go-readability` shows up as `// indirect` in the root `go.mod`; that is bake-off fallout, not an adoption.)*
- [x] **D1 · Confirm `gofeed` coverage.** ✅ **RESOLVED 2026-07-26 by the 4.2 corpus.** gofeed's
      parsing was never the problem — our layer around it was, and the corpus found three shipped
      bugs on its first run, each in a seam between two packages that were individually correct and
      individually tested. See plan.md §25.1 D1.
      ~~Original ticket:~~ Decision is made (gofeed + our own normalisation layer on
      top). Verify against 20 real feeds incl. one RSS 1.0/RDF and one Atom 0.3, and confirm
      `content:encoded`, `dc:`, `media:`, `itunes:`. *Blocks 4.1.*
- [x] **D17 · Quota accounting shape.** ✅ **RESOLVED 2026-07-26 as recommended** — subscription
      count + tenant-exclusive bytes; shared source/item storage excluded entirely. **Answers 5.2's
      open question directly: `sources` carries no accounting columns at all.** Cheap to take because
      D12 made quotas advisory rather than adversarial.
      ~~Original ticket:~~ Sources and items are global and deduplicated, so "storage MB
      per tenant" is undefined. Recommendation in the plan: subscription count + tenant-exclusive
      bytes only. *Blocks 5.2 — `sources` should carry whatever accounting needs.*
- [x] **D12 · Who are the other tenants?** ✅ **RESOLVED 2026-07-26: invite-only, family and friends,
      no self-signup.** Taken as an assumption to unblock 5.1/6.1 — Cam's call, one sentence to
      override, and safe to take because it is the only decision that *removes* work and reversing it
      is purely additive. Deletes registration, CAPTCHA, email verification and abuse tooling from the
      build; keeps rate limiting and lockout, which defend the login page an invite-only instance
      still has.
      ~~Original ticket:~~ Family, friends, or public signup. Decides self-signup,
      abuse handling, quota enforcement, deletion obligations, uptime promises. *Blocks 6.1.*

---

## Tier 1 — Repo skeleton

- [x] **1.1** Directory skeleton per plan §5: `cmd/ internal/ client/ proto/ web/ migrations/ e2e/`
- [x] **1.2** `go.mod` (`github.com/monstercameron/ArticleFlux`), Go 1.26, `replace` for GWC ← D0
- [x] **1.3** `buf.yaml` + `buf.gen.yaml` — CashFlux's shape (remote plugins, `paths=source_relative`,
      out `internal/pb`)
- [x] **1.4** **`make.ps1`**, not a `Makefile`: `gen build test wasm run lint migrate` (+ `tools`,
      `dev`, `clean`). **`make` is not installed on this box** and the Windows-native testing rule
      means there is no WSL or Docker to borrow one from — a Makefile nobody here can run, next to
      the script everyone actually runs, is two build systems one of which is a lie. Verb names are
      kept identical so a Makefile stays cheap to add if it ever earns its place.
- [x] **1.5** Build `gwc.exe` from the GWC checkout; pin the command in the Makefile
- [x] **1.6** `.gitignore` — `bin/ web/bin/ *.db *.db-wal *.db-shm backups/`
- [x] **1.7** `cmd/articleflux/main.go` — `version` prints and exits; **`serve` is the default and runs
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
rarely and read deliberately, and a note must survive anything that resets read state. It **saves
itself**: 800ms after the typing stops, immediately on leaving the field, and immediately on
Ctrl+Enter for anyone who wants it now. Plain Enter stays a newline, because a note that submits on
Enter cannot hold two sentences. A sync glyph beside the field reports pending → saving → saved, and
says so out loud only when a save FAILS — the reader is not asked to remember a keystroke, so the
glyph is the entire feedback loop and the one thing it must never do is claim a save that has not
landed (it withholds the tick if typing continued while the write was in flight). The Notes stream
orders by when the *note* changed, not when the article was published — it is a list of your own
writing, and you look for it by when you wrote it. It reloads on a note appearing or disappearing,
never on every autosave, or the list would reshuffle under someone still writing in it.

**Tags on the article.** The feed's tags render on the note panel with an × on each, so a tag can be
seen and removed where it was added. They are the FEED's tags — removing one from an article removes
it from the feed — which the heading says in full.

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

## Per-feed settings, and the first commit (2026-07-26, end of day)

**The gear** — a per-feed settings panel behind a gear that appears on hover. Cam: *"only show the
glyphs on hover so it doesnt over power the design"* — 151 gears down a sidebar is a column of
hardware competing with the one thing the rail is for.

> Hidden by **opacity, not `display:none`**, and also shown on `:focus-visible`. A control that only
> exists on hover cannot be reached from a keyboard, and this rail is meant to be fully navigable
> without a pointer. Below 900px it is always visible: touch has no hover, and hiding a control
> behind a gesture the device cannot make is the same as not shipping it.

> The gear is a **sibling** of the row, not a child. A `<button>` inside a `<button>` is invalid, and
> browsers resolve it by hoisting one out — after which the click target is whichever survived, not
> the one that was drawn.

**The panel is organised by who a setting belongs to**, which is A14 made visible rather than a
layout preference:

- **Yours** (`subscriptions`) — name override, in-the-ranked-feed, mute, offline depth, tags.
- **Shared** (`sources`) — feed URL, site URL, poll interval, and *"N other people on this server read
  this feed. Changing these changes them for everyone."* as the group heading, not a footnote under
  it. Someone changing a poll interval should read why before they change it.
- **Health** — last fetch, last success, next fetch, item and unread counts, and the publisher's own
  error string verbatim. That string is the single most useful thing on the panel when a feed dies,
  and paraphrasing it helps nobody.
- **Actions** — fetch now, mark all read, and unsubscribe styled destructive but only on hover.

No migration was needed: `subscriptions.title / in_megafeed / muted_until / cache_depth` and
`sources.fetch_interval_s` were all in `0001_init.sql` already. Both tables are written in **one
transaction** — a panel that renamed the subscription and then failed to set the interval would leave
the reader looking at a form where half of what they submitted took effect, with no way to tell which
half.

`fetch_interval_s` is clamped **at the write** to 5 minutes–1 week, and `next_fetch_at` is recomputed
from the *last* fetch rather than from now: lengthening the interval must not postpone a poll that is
already overdue. The floor is politeness, not performance — the column is global, so one user setting
ten seconds would have this server hammering a publisher on everyone's behalf.

**First commit** — `70dd580`, 100 files, authored as Cam rather than as the agent (the machine-wide
git identity was `Claude <claude@anthropic.com>`; set locally, not globally). `.gitignore` hardened
before anything was staged: `speech-cache/` and `*.mp3` (synthesised audio is the same privacy class
as the database, in a directory nobody thinks of as data), `*.opml` (a complete list of what someone
reads), keys and certificates **by extension** rather than by name — nobody commits a file called
"secret", they commit `server.key` at half past midnight — and `/e2e/_*.mjs` for the one-off probes.
Every staged file was scanned for key-shaped strings first. `.env.example` documents `OPENAI_API_KEY`
without holding one. **No remote, and none is to be added.**

---

## A login, categories, and the things a token can't be inferred from (2026-07-26, night)

Spec entries: **A36–A39**, §6.10, §7.1a, §20.16–20.18, §22.3–22.5. This batch is mostly *deployability*
— the reader was usable and not hostable, and the gap between those two was one flag.

**1. The `-dev` hole, and what it says about inferring security from topology.** `DevMode` — which
serves the single local account with **no login at all** — was `isLoopback(bindAddr)`. Every
reverse-proxy deployment terminates TLS on :443 and forwards to `127.0.0.1:9000`, so the standard way
to host this was also the way to publish someone's entire reading history to whoever typed the domain,
and the more carefully the operator bound to loopback the more exposed they were. It is now an explicit
`-dev`, default off, **refused on any bind but loopback** — belt and braces, because the flag alone
would eventually be pasted into a systemd unit by someone who wanted to skip a login screen once.

> **The transferable rule:** a bind address is a fact about network topology and cannot tell you who is
> on the other end of a connection. Nothing that cannot answer "who is calling" may decide whether to
> ask for a password.

**2. `AuthService`** — `Login` / `Logout` / `WhoAmI`, hashed revocable sessions, per-username and
per-client attempt limiting, an Argon2id **decoy** hash so a missing username costs the same as a real
one (a uniform error message does not close a timing oracle; the work has to actually happen), and a
loud `FailedPrecondition` when a username exists in two tenants rather than a silent wrong-tenant
login. Full detail in plan §7.1a, including the six things §7.1 still owes.

**3. The operator CLI** (`cmd/articleflux/admin.go`) — a login needs something to log in as, so the
replacement for `EnsureDevUser` had to land in the same change: `init` (refuses to run twice),
`adduser`, `passwd`, `migrate`, `backup`. Password from flag → `ARTICLEFLUX_PASSWORD` → terminal
prompt, so it never has to appear in a process listing.

**4. `Preflight` + `/readyz`.** The server now refuses to listen when it cannot work — no account, no
built client, unwritable data directory — and reports **all** the failures at once, because someone
setting up a droplet usually has more than one wrong and a one-at-a-time boot loop is a miserable way
to find out. The writability check *writes and removes a file* rather than stat-ing: a directory can be
listable and not writable, and SQLite needs to create the `-wal` and `-shm` siblings.

**5. Backups** (`store.Backup`, TODO 3.4) — `VACUUM INTO` + `PRAGMA integrity_check` + `.partial` +
rename + retention. `cp` of a live WAL database produces a file that opens cleanly, passes a smoke
test, and is missing a transaction.

**6. Categories** (A37) — folders were in `0001_init` from the start and nothing used them. Six RPCs,
flat, per-user; deleting one **unfiles its feeds and unsubscribes nothing**; creating one that exists
returns it rather than erroring, because that call comes from the add-a-feed form where "Tech" and
"tech" are one intent. The rail shows them *as well as* the flat list — the flat list answers "where is
that feed", the categories answer "what have I got on this subject", and a 151-row flat list cannot
answer the second at all.

**7. A tag's name and a tag's row are different things** (A38, `0008_tag_style.sql`). `name` is the
handle — what you type, what the chip says, what `SetFeedTag` takes — and it is now never edited;
`label` and `glyph` are the rail row's, empty meaning "use the name", the same override idiom as
`subscriptions.title` over `sources.title`. `internal/tagglyph` is the fifty-mark catalogue, in
`internal/` so the picker and the validator read one list, and the **character is stored, not an
index** — an index is a promise never to reorder the list, and breaking it silently changes every
reader's tags with nothing in the data to show it happened.

**8. The settings surface** — seven tabs, and the last three (Server / Activity / Speed) are what a
self-hosted app owes its operator: nobody is tailing a log file behind this and there is no dashboard,
so "is it healthy", "what just happened" and "why is it slow" are answerable there or nowhere.

**9. Themes and motion** (A39) — five themes as sets of custom-property values, and every duration
written `calc(var(--mo) * t)` so reduced motion makes an animation *absent* rather than suppressed. The
previous mechanism was a `* { transition: none }` rule at the bottom of the sheet, which is a broom: it
works until someone writes a transition with `!important`, in a later layer, or on a pseudo-element,
and nothing fails loudly when it stops working. **Built but not reachable** — see 8b.31.

**10. `internal/sanitize`** (TODO 2.9) — five named policies over GWC's engine, because GWC has no
opinion about *where the HTML came from* and that is the whole question. The `Newsletter` policy drops
remote images outright rather than proxying them: proxying a tracking pixel still tells the sender the
message was opened.

**11. The D7 corpus** — twelve committed article pages and a three-library bake-off, in a **separate Go
module** so the two libraries that lose do not enter the server's dependency graph forever. The
decision is still open; what exists is the evidence and a command to re-run it.

> ### Two things this batch left broken, and they are the top of the list
>
> - **The wasm client does not compile.** `GOOS=js GOARCH=wasm go build ./client/...` fails in
>   `client/view/reader.go` — the rail's category section and the add-feed dialog are written and their
>   props are half-wired. Native build and `go test ./...` are both green, which is the point worth
>   remembering: **`go build ./...` does not compile the client**, so a green suite is not evidence the
>   app runs. Restoring the wasm build to CI's *default* path is 8b.32.
> - **Auth has no client half.** Nothing in `client/` calls `Login`, stores a token, or sends
>   `authorization` metadata, so every RPC falls through to `devScope` and a server started without
>   `-dev` serves a reader that cannot get a session. The server is deployable; the client is not.

---

## Tier 2 — Pure atoms

Leaf packages. No ArticleFlux imports, no DB, no network, table-driven tests. Everything above is made of
these, and each one is genuinely small.

- [x] **2.1 `timeutil`** — UTC helpers · **wall-clock + IANA window resolution** (quiet hours, digests,
      idle triggers) · `ClampPublished(published, firstSeen)` for feeds claiming 2087 or epoch zero.
      *Done when: a 22:00–07:00 window is correct across a spring-forward and a fall-back date.* §22.9
      ✅ 2026-07-26 — `internal/timeutil` — `UTC`, `ClampPublished` (a feed claiming tomorrow gets clamped to first-seen), `ParseWindow`, `Window.Contains`, `NextOccurrence`. Table-driven test.

- [x] **2.2 `idgen`** — sortable ids · **128-bit unguessable slugs** (public shares) · idempotency keys
      · device and token-family ids
      ✅ 2026-07-26 — `internal/idgen` — sortable `New()`, plus `Slug()` 128-bit, `Token()` 256-bit, `IdempotencyKey()`, `DeviceID()`, and `TimeOf()` to read the timestamp back out. Test asserts monotonicity within a millisecond.

- [x] **2.3 `secret`** — Argon2id hash/verify **plus a startup tuning benchmark** returning params for
      the box · HMAC · token hashing · symmetric encrypt for mailbox credentials. *Done when: the
      benchmark picks params hitting a target ms budget.* §7.1
      ✅ 2026-07-26 — `internal/secret` — Argon2id hash/verify with `Tune(target, ceiling)` benchmarking the box at startup, SHA-256 for tokens (fast on purpose — 256 bits of CSPRNG has nothing to brute-force), HMAC sign/verify, and AES-GCM for mailbox credentials.

- [x] **2.4 `urlnorm`** — two outputs from one canonicaliser: `Norm` (bookmark identity, unique per
      user) and `DupeKey` (cross-source duplicates). Strip `utm_*`/`fbclid`/`gclid`/`ref`, sort query,
      drop trailing slash and fragment. *Done when: 30 real-world pairs collapse and two genuinely
      different articles don't.* §6.2, §15.3
      ✅ 2026-07-26 — `internal/urlnorm` — both outputs from one canonicaliser: `Norm` for identity and `DupeKey` for the aggressive dedup key, plus `Host`. Tracking-parameter stripping covered by test.

- [x] **2.5 `feeddate`** — ~15 layouts incl. the malformed ones (no tz, `GMT+0000`, single-digit days,
      `-0000`). *Done when: a corpus of real broken dates parses.*
      ✅ 2026-07-26 — `internal/feeddate` — the malformed layouts included. Table-driven.

- [x] **2.6 `charsetdec`** — `x/net/html/charset`, reconciling the XML declaration against the HTTP
      `Content-Type` when they disagree. *Done when: Windows-1252 and Shift-JIS fixtures decode.*
      ✅ 2026-07-26 — `internal/charsetdec` — `x/net/html/charset`, reconciling the XML declaration against the HTTP header.

- [x] **2.7 `netguard`** — **the SSRF guard.** Scheme allowlist; reject loopback, RFC1918, link-local,
      ULA, `169.254.169.254`; `CheckRedirect` re-runs **per hop**. *Done when: every reject case has a
      test, including redirect-to-localhost.* **Seven callers depend on this.** §21
      ✅ 2026-07-26 — `internal/netguard` — the SSRF guard, two-layer (pre-resolve and post-resolve). Found a real bug in review: `::ffff:0:0/96` in the block list matched the ENTIRE IPv4 internet, because `IPNet.Contains` calls `To4()` on the network address and collapses it to `0.0.0.0/0`.

- [x] **2.8 `attrsel`** — parse `h2 a@href` into (selector, attr). **We own this; cascadia has no
      attribute syntax.** §14.2
      ✅ 2026-07-26 — `internal/attrsel` — `h2 a@href` → (selector, attr). Ours, because cascadia has no concept of an attribute target.

- [x] **2.9 `sanitize`** — wrapper over GWC `sanitize` with **named policies**: `feed`, `newsletter`
      (strictest — pixels, remote CSS), `archived`, `public` (excerpt). *Done when: an XSS corpus is
      neutralised under every policy.*
      ✅ 2026-07-26 — `internal/sanitize` — **five** policies, not four: `Feed` · `Newsletter` · `Archived` · `Public` · `Note` (the reader's own text, still sanitised because people paste). GWC's engine is not reimplemented — it owns the parse, the allowlist walk, the scheme check and the mutation-XSS hardening. What it has no opinion about is *where the HTML came from*, which is the whole question: `Feed` keeps images because a hardware review is mostly photographs, and `Newsletter` **drops remote images outright** rather than proxying them, since proxying a tracking pixel still tells the sender the message was opened. The XSS corpus is built: **48 real vectors × 5 policies**, checked by substring rather than by parsing, because a parser in the test would share assumptions with the parser in the sanitizer and a shared wrong assumption is the bug being hunted. A policy added later inherits the whole corpus; a policy loosened later has to survive it; an unmapped policy value fails closed to `Public`. Two things the allowlist cannot express live in a pre-pass over the parse tree: every link gains `rel="noopener noreferrer"` (without it `target=_blank` hands the opened page a handle to navigate ours — one attribute in a publisher's own feed buys them a phishing primitive), and tracking pixels are dropped on a deliberately narrow heuristic. *Owed: the `client/view` call sites still invoke GWC directly rather than going through a policy.*

- [x] **2.10 `outlinks`** — extract and normalise links out of article HTML → domains, skipping
      self-links and nav chrome. Pure. **This is §18.7 rung 1, the best recommendation signal, and it
      costs a URL parse.** ← 2.4
      ✅ 2026-07-26 — `internal/outlinks` — extract and normalise links out of article HTML, skipping self-links and the usual syndication noise.

- [x] **2.11 `textvec`** — TF-IDF term vectors, cosine, and simple agglomerative clustering. Pure.
      **Powers term affinity and topics with no LLM at all**, which is what keeps Smart free. §18.2
      ✅ 2026-07-26 — `internal/textvec` — TF-IDF vectors, cosine, agglomerative clustering. Pure, and the input for 4.10 when the interest layer lands.

> *Done when:* `go test ./internal/...` green and **none of these import each other** (2.10 → 2.4 is
> the only permitted edge).

---

## Tier 3 — Storage foundation

Build the safety rails **before** the first repository, so every repo is born correct.

- [ ] **3.1** `migrations/0001_init.sql` — **all ~49 tables**: §6.2 core, §6.3 A22 rules, §6.6 tags,
      §6.7 identity + interest, **§6.8 the rest** (folders, notes, bookmarks, engagements, jobs,
      settings, auth, offline, notifications). `REFERENCES` on every FK-shaped column, the three §6.5
      indexes, and the folder depth `CHECK`s ← G1
      ◧ 2026-07-26 — **9 tables, not ~49.** `0001_init` covers the reading core (tenants, users, sessions, sources, subscriptions, folders, items, user_item_state, items_fts + triggers); 0002–0008 add prefs, favicons, tags, notes, ratings, engagements and tag style (`label` + `glyph`, A38). The rules, bookmarks, mailbox and interest-layer tables are not written yet — they arrive with the tiers that use them, which is cheaper than a 49-table migration nothing reads. **`folders` was in 0001 and unused until the rail needed it** (§6.10) — the one case where writing the column early paid off, since categories shipped with no migration at all.

- [x] **3.2 `store/migrate`** — numbered, forward-only, in a transaction, `schema_migrations` with a
      **checksum guard that aborts startup on drift**, and an **automatic snapshot before applying**.
      No down-migrations; the rollback path is restore. (A23) §22.1
      ✅ 2026-07-26 — `(*DB).Migrate` — numbered, forward-only, each in its own transaction, with a `schema_migrations` checksum. `TestMigrateIsIdempotent` and `TestChecksumDriftAbortsStartup` hold it: an edited migration aborts the boot rather than silently diverging.

- [x] **3.3 `store/sqlitex`** — `Open()` via **`driver.Open(dsn, fts5.Register)`, never `sql.Open`**
      (G1: FTS5 is a per-connection loadable extension — a pooled connection that misses the hook
      serves every query fine and fails only on search) with **WAL · `busy_timeout=5000` ·
      `synchronous=NORMAL` · `foreign_keys=ON`** (SQLite defaults it **off**, which would make every
      `REFERENCES` above decorative) · **separate read pool + a single-connection write pool, both
      hooked** · scheduled WAL checkpoint. *Done when: four concurrent writers produce zero
      `SQLITE_BUSY`, **and a `MATCH` succeeds on a connection drawn from each pool**.* (A24) §22.2
      ✅ 2026-07-26 — `internal/store.Open` — `driver.Open(dsn, fts5.Register)` on BOTH pools, never `sql.Open` (G1). WAL, `busy_timeout`, `foreign_keys=1`, and `_txlock=immediate` on the single writer (A24). `verify()` probes each pool at boot; `TestFTS5OnEveryPooledConnection` and `TestPragmasAreActuallySet` keep it honest.

- [x] **3.4 `store/backup`** — `VACUUM INTO` + `PRAGMA integrity_check` + retention. *Done when: a
      backup taken under concurrent writes restores and opens.* §22.5
      ✅ 2026-07-26 — `store.Backup` / `store.BackupName` / `store.PruneBackups`, driven by `articleflux backup -out <file|dir> [-keep N]`. `VACUUM INTO` holds one read transaction, so the copy is a single point in time; the result is then **opened and integrity-checked**, because an unverified backup is a belief about a file and the moment you need it is the worst moment to test the belief. Written to `<dst>.partial` in the same directory and renamed, so an interrupted run cannot leave a truncated file under the name of a good one for the retention sweep to preserve. *Owed: the automatic pre-migration snapshot §22.1 promises, anything that schedules this, and the restore drill (T8).*

- [x] **3.5 `store.Scope`** — `{TenantID, UserID, Caps}`, the **first parameter of every repository
      method**. In the signature, so it cannot be forgotten.
      ✅ 2026-07-26 — `store.Scope{TenantID, UserID, Role}` is the first parameter of every repository method, and **guard 4 fails the build** if one is added without it (23 methods checked).

- [x] **3.6 Two-tenant fixture** — tenants A and B, overlapping sources, disjoint user state. Every
      repo test built after this uses it.
      ✅ 2026-07-26 — `TestTenantIsolation` — two tenants over overlapping sources with disjoint user state; the second tenant's reads return nothing of the first's.

- [ ] **3.7 G2 · The leak-test harness** — reflect over exported repository methods; assert none
      returns a B-owned row under an A scope. **Fails on any method added without coverage.**
      *Done when: it passes with zero repositories and would fail the moment a bad one is added.*
      ◧ 2026-07-26 — Not started. Guard 4 enforces that a Scope is *taken*; nothing yet asserts it is *used* in the WHERE clause. `TestTenantIsolation` covers the current methods by hand, which does not scale to the next twenty.

- [x] **3.8 Build-time check** — fails if `db.Query`/`db.Exec` appear outside `internal/store` §6.1
      ✅ 2026-07-26 — `internal/tools/guards` — `no SQL outside internal/store`, 45 files checked, run in CI and locally. Three sibling guards ride with it: no `syscall/js` outside `client/platform`, no `.css` files (A26), and 3.5's Scope check.

- [x] **3.9 A22 deletion-safety test** — deactivating a source, and deleting a user, leave every
      *other* user's favourites, tags, notes and shares intact. **Write it now**, while the schema is
      the only thing that can break it. §6.3
      ✅ 2026-07-26 — `TestGlobalRowsDoNotCascade` and `TestUserRowsDoCascade` — deactivating a source leaves its items; deleting a user takes their state and nothing global (A22).

---

## Tier 4 — Domain packages

Composites of Tier 2. Bytes in, structs out. Still no database.

- [x] **4.1 `feed`** — parse → `ParsedFeed`/`ParsedItem`. gofeed underneath, **our normalisation layer
      on top** so swapping it is one file ← 2.5, 2.6, D1
      ✅ 2026-07-26 — `internal/feed` — gofeed underneath with our own normalisation on top: identity, `DupeKey`, published-date clamping, word counts, image extraction.

- [x] **4.2 `feed/testdata/corpus`** ✅ 2026-07-26 — **27 fixtures**: 21 fetched verbatim from live
      publishers, 6 hand-written for the formats nobody serves any more (RSS 0.91, Atom 0.3, JSON
      Feed, a genuinely Windows-1252 document, the guid ladder, seventeen date layouts). Marked
      `binary` in `.gitattributes`, because `text=auto` would rewrite line endings inside files whose
      only job is byte-identity and one of them is not valid UTF-8 at all. Grown by
      `go run ./internal/tools/corpus -add <url> -slug <name>`. **Found three shipped bugs on its
      first run** (see D1). Includes a feed that parses cleanly and contains zero items — dead since
      2021, still served — because "this site went quiet" and "we are broken" must not look alike.
      ~~Original ticket:~~ — 25+ real feeds covering every §15.3 format, **saved verbatim
      including the broken ones**. Grows forever: every future bug adds a fixture before it gets a fix.
      ◧ 2026-07-26 — `internal/feed/testdata` is empty. The parser has been exercised against 151 real feeds in the dev database, which is better than nothing and worse than a committed corpus: nothing in CI would catch a regression on a format nobody is subscribed to today.

- [x] **4.3 `fetch`** — conditional GET (ETag/Last-Modified) · gzip · caps (15s, 5 MB, 5 redirects) ·
      honest UA · **`Retry-After`** · **301 reports the new canonical URL** ← 2.7 §15.4
      ✅ 2026-07-26 — Conditional GET (`If-None-Match` / `If-Modified-Since`, 304 handled), gzip, a body cap with `LimitReader`, timeouts, and the 2.7 SSRF guard on every request. *Deviation: the caps are 32 MB / 30s rather than the 5 MB / 15s the plan names — real feeds exceeded 5 MB. Lives inside `internal/feed` rather than its own package.*

- [x] **4.4 `extract`** ✅ 2026-07-26 — `internal/extract`: `Fetch` (SSRF-guarded, 8 MiB cap, resolves
      relative links against the post-redirect URL) and `FromBytes`, returning sanitised HTML **and**
      plain text from one pass, because deriving text at four call sites produces four word counts and
      the one used for ranking is the one nobody checks. Output goes through `sanitize.Archived`.
      Below `MinWords` (25) it returns `ErrNoContent` rather than an empty success — readability hands
      back the navigation on a section index, and a reading pane reading "Home About Contact" is worse
      than an honest fallback to the feed's own content. **It parses the DOM itself and hands
      readability a document**, because `go-shiori/dom` re-guesses the charset from bytes and
      double-decodes; see D7. ~~Original:~~ readability → clean HTML + plain text ← 4.3, 2.9, D7.
      **Phase 1, not Phase 3** — five features depend on it and rev 7 had it scheduled after two of
      them.
- [x] **4.5 `opml`** — nested OPML 2.0 both ways. *Done when: out → in → identical.*
      ✅ 2026-07-26 — `internal/opml` — nested OPML 2.0 both directions, round-trip asserted. Cam's live FreshRSS export (151 feeds) imported through it.

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
      ◧ 2026-07-26 — `tenants`, `users` and `sessions` exist and `ScopeForSession` resolves a token hash into a Scope. `roles`, `user_roles`, `invites`, `devices` and `api_tokens` are not — they belong with 6.1/6.2, and there is one local account until then.
      ◧ 2026-07-26 (night) — the **session lifecycle** landed with 6.1's first half: `UserForLogin` (loud on a username that exists in two tenants, rather than a silent wrong-tenant login), `CreateSession` (session row + `last_login_at` in one transaction, so the account screen cannot lie about "was that me?"), `RevokeSession`, `RevokeAllSessions`, `PurgeExpiredSessions` (run by the poller, revoked rows kept a week), `Identity`, `CountUsers`, `SetPasswordHash`, `AddUser`, `FirstTenantID`. All ten are **unscoped by design** and registered as such in `internal/tools/guards` with a reason each — the guard's exemption list is now where "why does this method not take a Scope" is answered. Note what is *not* exempt: `Identity` takes a Scope, because "who am I" is a question about an authenticated caller.
      > `SetPasswordHash` deliberately does **not** revoke sessions. Two callers want opposite things: the break-glass reset is a password *change* and must revoke everything, while a login that re-hashes under stronger Argon2id parameters changed no password and must revoke nothing. Bundling the revoke made the first login after a parameter bump log the reader straight back out.

- [x] **5.2** `sources` · `subscriptions` — **soft-deactivate, never delete** (A22) · `natural_key`
      **per-user for `kind='mailbox'`** (§6.4) · `home_mode` and the highlights fields ← D17
      ✅ 2026-07-26 — `Subscribe` / `Unsubscribe` / `SubscribedSources` — `natural_key` deduplicates two tenants onto one polled row (A14), and unsubscribing never touches the source (A22).

- [x] **5.3** `items` · `item_revisions` · `user_item_state` — the denormalised `source_id`/
      `published_at` and **all three §6.5 indexes**
      ✅ 2026-07-26 — `items` + `user_item_state` with the denormalised `source_id`, keyset paging that never skips or repeats (`TestKeysetPaginationCoversEveryRowOnce`), and `CountQuery` sharing its filter builder with `ListItems` so the two cannot describe different result sets. *`item_revisions` is not written yet.*

- [ ] **5.4 G3 · Hot-query benchmark** — 50k items × 3 users, **all three shapes**: flat unread count,
      unread-by-newest, **unread-by-folder**. *Do not proceed without numbers.* R2
      ◧ 2026-07-26 — `internal/store/bigdb_test.go` runs the three shapes against the REAL dev database (3,621 items): paging 9ms, mark-all-read 16ms, search prompt. It is not the 50k × 3-user synthetic fixture the plan asks for, and it skips when no dev database is present — so it proves the queries are fine today and guards nothing in CI.

- [ ] **5.5** `tags` · `item_tags` — A21, prerequisite for both rules and the sync API
      ◧ 2026-07-26 — Tags exist and are per-user, but they attach to a **subscription**, not an item. `item_tags` — which is what A21 actually specifies and what the sync API needs — is not built.

- [x] **5.6** `notes` (private by default) · `bookmarks` · `bookmark_tags`
      ✅ 2026-07-26 — `item_notes` (0005) — private, separate from `user_item_state` so a note is not coupled to read state, with `NotedItems` for the Notes stream. *Bookmarks are not built; read-later reuses `starred_at` instead.*

- [ ] **5.7** `rules` · `rule_hits` · `scrape_rules` · `mailboxes`
- [ ] **5.8** `settings` · `views` · `engagements` (append-only, **with the §18.1 kind taxonomy
      including `impression` and `bulk_read`**) · `audit_log`
      ◧ 2026-07-26 — **`engagements` is DONE**, ahead of its tier because every day of reading
      without it is signal that cannot be reconstructed (R12). `migrations/0007_engagements.sql` +
      `store.RecordEngagements` (batched, one tx, `INSERT OR IGNORE` on a client-generated id so a
      retried batch cannot double-count) + the three reads the interest layer needs: `ItemSignals`,
      `FeedSignals`, `EngagementsSince`. `settings`, `views` and `audit_log` are untouched.
- [ ] **5.9** Interest layer: `topics` · `item_topics` · `domain_affinity` · `outlinks` ·
      `recommendations` · `feed_affinity` · `term_affinity` · `home_ranking`. **All derived — a
      `DELETE` and rebuild from `engagements` must produce the same result**, which is the test.
- [ ] **5.10** FTS5 triggers for `items_fts`, `notes_fts`, `bookmarks_fts`, and a search repo over them
      ◧ 2026-07-26 — `items_fts` with its three triggers and a `Search` repo over it, verified by `TestSearchFindsASeededItem` and `TestSearchIndexTracksUpdates`. `notes_fts` and `bookmarks_fts` are not built.

- [x] **5.11 `folders`** — categories: list · create · rename · delete · file a feed. **Per-user, flat,
      one folder per subscription** (A37, §6.10). *Added to the tier after the fact: the table shipped
      in `0001_init` and the repository is what was missing.*
      ✅ 2026-07-26 — `internal/store/folders.go` + `folders_test.go`. Flat by choice — nothing writes `parent_id`, and the `depth < 8` CHECK stays so nesting is a migration nobody has to write. `MaxFolderName = 48` (the width the rail draws before it ellipsises) and `MaxFoldersPerUser = 200` (the cap `tags` already carries). Delete **unfiles and never unsubscribes**; create is idempotent on a case-insensitive name, because the caller is the add-a-feed form and an error there is a dead end mid-task. Ordered `position, name` — position alone leaves unarranged rows in whatever order SQLite returns, which reads as a sidebar that shuffles itself.

- [x] **5.12 tag style** — `tags.label` + `tags.glyph` (`0008_tag_style.sql`, A38) and `UpdateTag`.
      ✅ 2026-07-26 — the identity/presentation split: `name` is never edited, `label` and `glyph` are the rail row's, empty meaning "use the underlying name" — the same override idiom as `subscriptions.title` over `sources.title`, so there is one rename idiom in the schema and not two. `store.TagPatch` is tri-state per field. Covered by `internal/store/tags_test.go`. `internal/tagglyph` holds the fifty-mark catalogue (server-validated, count asserted by a test rather than trusted to a comment) — see 8b.28.

> *Done when:* every repo has a leak test, 5.9's rebuild test passes, and **5.4's numbers are written
> into `plan.md` §6.5**.

---

## Tier 6 — Services

Business logic over repositories. Still headless.

- [ ] **6.1 `authn`** — login (hash always run, uniform errors) · **rate limiting + lockout** per-user
      *and* per-IP · refresh families with **reuse detection → revoke the family** · recovery codes ·
      reset tokens · sudo mode ← 2.3, 5.1, D12
      ◧ 2026-07-26 (night) — **about a third of it, and it is the floor for the public internet rather than the ceiling** (plan §7.1a, A36). Built *ahead of its milestone* because `DevMode` was derived from a loopback bind, which made the standard reverse-proxy deployment an open reader. What exists: `AuthService.Login/Logout/WhoAmI`, the hash **always** running against a boot-computed Argon2id decoy on an unknown username, one uniform error for missing/wrong/deactivated, a fixed-window limiter at 10/minute on **both** the username and the client address, SHA-256-stored 30-day sessions with real revocation, opportunistic re-hash on login, and `device_id` grouping. What is owed, all of it still: **lockout** (the limiter is not one), refresh families and reuse detection, recovery codes, reset tokens, sudo mode, the breached-password check, and per-box Argon2id tuning.
      > **Two honest caveats, both recorded in the code.** RPCs arrive multiplexed over one WebSocket, so the peer address is the tunnel's — behind nginx that is `127.0.0.1` for everyone, and the per-IP key **collapses to one bucket in exactly the deployment where it matters most**. The per-username limiter carries the weight; a real per-IP limit needs the forwarded address threaded through the tunnel handshake (7.3d). And the limiter is in memory, so a restart clears it — persistent lockout state needs a table, which is this item's job and not the transport's.
      > **D12 now arrives as an outage rather than as a leak.** Usernames are unique *per tenant*, so login is unambiguous only while there is one tenant; two matches return `FailedPrecondition` and say so. A second tenant needs a tenant hint (subdomain or explicit field) before it can exist.
- [ ] **6.2 `authz`** — capability set, **static per-method map, fails closed on unmapped**. Serves
      both the tunnel and the REST sync API — one model, not two. §7.5
- [ ] **6.3 `settingsreg`** — typed registry + **system → tenant → user** resolution, returning the
      value *and which layer supplied it* ← 5.8
- [ ] **6.4 `jobs`** — durable queue (`jobs` table), **per-kind concurrency caps** so pack building
      can't starve rule fan-out, retry, restart-survivable §22.7
- [ ] **6.5 `events`** — **per-tenant** ring buffers (~1000 each), `since_seq` replay,
      `RESYNC_REQUIRED`, scope-filtered fan-out. *Done when: tenant A's burst cannot evict tenant B.*
- [x] **6.6 `ingest`** — fetch → parse → identity/dedup → `dupe_key` → revision detect → store →
      **queue** fan-out. Includes the **flood guard** (§15.5) and **outlink harvesting** (2.10).
      ← 4.1, 4.3, 5.3
      ✅ 2026-07-26 — `internal/store/ingest.go` — identity, `dupe_key` dedup, and `RecordFetch` storing the outcome either way, because a poll that fails silently makes a dead feed look like a quiet one and those must look different.

- [ ] **6.7 `fanout`** — the per-subscriber job: evaluate rules, write `user_item_state` + `item_tags`,
      emit events, feed ranking signals. **Per subscriber, never once at ingest** — the §13.2 bug —
      and **never inline with the poll**, since it's `O(items × subscribers)`.
- [ ] **6.8 `poll`** — **priority queue by staleness ratio** (not FIFO), backoff, `Retry-After`,
      per-host semaphore, adaptive intervals, **lag metric**, widen-when-behind policy ← 6.4
      ◧ 2026-07-26 — A background poller runs on a fixed interval (`-poll`, default 15m) and per-feed refresh is wired to the UI. The **priority queue by staleness ratio** is not — it is still FIFO over everything due, so a feed that posts weekly is polled as often as one that posts hourly. `sources.fetch_interval_s` is now per-feed adjustable (A33), which is the manual version of the same idea.

- [ ] **6.9 `signals`** — impression coalescing · the §18.1 kind taxonomy (**`bulk_read` is neutral,
      never negative**) · scheduled derivation of `feed_affinity`, `term_affinity`, `domain_affinity`,
      topics, and `home_ranking`. *Done when: a simulated `mark all read` over 143 items changes no
      affinity score.* R17
      ◧ 2026-07-26 — **Collection is done; derivation is not.** `internal/signals` owns the taxonomy
      (25 kinds, priors, validation, dwell normalisation, clock clamping) and is the authority §18.1
      now defers to. `client/track` does the impression coalescing — two consecutive visibility polls,
      one impression per item per session — behind a `Sender` interface, so all of it is tested
      natively off the browser. `bulk_read` and `sync_read` are excluded from affinity in
      `signals.Spec.Affinity` **and** in `FeedSignals`' WHERE clause, and both are covered by tests.
      What remains is the scheduled job: nothing yet writes `feed_affinity`, `term_affinity`,
      `domain_affinity`, topics or `home_ranking`. See **D18**, which should be settled before the
      scorer is written — it decides whether this is one linear sum or a two-stage recall/precision
      split.
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

- [x] **7.1** `proto/articleflux/v1/` — start with `Auth`, `Feed`, `Item`, `Event`. Grow per milestone.
      ✅ 2026-07-26 — `proto/articleflux/v1/{reader,system}.proto` — ReaderService now carries feeds, items, state, search, prefs, tags, notes, and per-feed settings. Additive-only within v1, which `buf breaking` enforces in CI.
      ✅ 2026-07-26 (night) — **`auth.proto`** (`Login` · `Logout` · `WhoAmI`) is the third service, registered **unconditionally including in DevMode**: an instance that starts on a laptop and later gets a domain must not need a different binary, and a client that can always call `Login` has one code path instead of two. ReaderService gained folders (`ListFolders` · `CreateFolder` · `RenameFolder` · `DeleteFolder` · `SetFeedFolder`), `folder_id` on `Subscribe`, and `UpdateTag` + `Tag.label` / `Tag.glyph`. All additive. *There is deliberately no `GetTagSettings` — `ListTags` already returns everything that panel shows, so fetching it again would be a round trip for data the client is holding, and the dialog opens with its content instead of a spinner.*

- [x] **7.2** `buf generate` wired into `make gen`; commit `internal/pb`
      ✅ 2026-07-26 — `./scripts/make.ps1 gen` runs `buf generate`; `internal/pb` is committed.

- [x] **7.3** gRPC service impls — **thin**: authz → validate → call Tier 6 → map to pb. No logic here.
      ✅ 2026-07-26 — `internal/transport/grpcsrv` — translation and the §20.7 taxonomy only. Every clamp and every tenant check lives in the repository, where a second caller cannot bypass it.

- [ ] **7.3a `internal/apierr`** — the §20.7 taxonomy in one place. **Cross-tenant returns `NotFound`,
      never `PermissionDenied`** — the latter confirms the object exists, which is a tenant leak with
      good manners. Structured detail `{code,message,field,quota,retry_after_s}`; `message` is always
      safe to display. *Done when: T1 asserts the code, not just the empty result.*
      ◧ 2026-07-26 — The taxonomy is implemented — `grpcsrv.toStatus` maps cross-tenant to `NotFound`, never `PermissionDenied` — but it lives in the transport package rather than in `internal/apierr`, and there is no structured detail payload yet.

- [ ] **7.3b `internal/page`** — opaque keyset cursors, `spec_hash`-bound so a cursor from a different
      `ViewSpec` is `InvalidArgument` rather than silently-wrong results §20.7
      ◧ 2026-07-26 — Keyset cursors are base64url and exact (published, id) tuples, in `internal/store`. They are **not** `spec_hash`-bound, so a cursor from one scope replayed against another returns plausible wrong rows rather than `InvalidArgument`.

- [ ] **7.3c `internal/idem`** — `(user_id, key) → response`, 24h TTL, verbatim replay. **Required for
      every outbox-replayed mutation** — a partial drain that reconnects mid-flight must not
      double-apply §12.4, §20.7
      ◧ 2026-07-26 — `SetItemState` accepts an `idempotency_key` and every caller sends a deterministic one, but nothing stores or replays it. Harmless today because there is no offline outbox to drain; required before there is (§12.4).

- [ ] **7.3d Rate limiters** — the §20.7 table, at the interceptor, per-user and per-IP
      ◧ 2026-07-26 (night) — **login only**, in `grpcsrv/auth.go`: 10/minute per username and per client address, in memory. Nothing else is limited, and the per-IP half is largely fictional — every RPC arrives over one WebSocket, so behind a proxy the peer address is `127.0.0.1` for every user on the instance. **A real per-IP limit requires the forwarded address to be threaded through the tunnel handshake**, which is this item's first piece of work.
- [x] **7.4** `grpctunnel.Wrap` hardened: `WithAllowedOrigins` (exact) · `WithReadLimitBytes(4<<20)`
      (a deliberate tightening; the library default is 16 MiB) · `WithKeepalive` · the three
      connection/upgrade caps · `WithAuthorize` ← 6.2
      ✅ 2026-07-26 — `grpctunnel.Wrap` with `WithReadLimitBytes(4<<20)` (tightened from the library's 16 MiB default), keepalive, and the two connection caps. *Not yet: `WithAllowedOrigins`, which needs the real deployment origin, and `WithAuthorize`, which needs 6.2.*
      ✅ 2026-07-26 (night) — `WithAllowedOrigins` is wired to `serve -origin a,b,c` and **applied only when set**, because an empty allowlist would reject every browser rather than fall back to the library's same-origin default. That default compares `Origin` against `Host`, which holds *as long as the proxy forwards `Host` faithfully* — so production sets `-origin` explicitly rather than depending on someone else's nginx. `WithAuthorize` still needs 6.2.

- [x] **7.5** `/healthz` + `/readyz` — **unauthenticated, status code only, deliberately
      information-free** §22.4
      ✅ 2026-07-26 — `/healthz` — unauthenticated, status code and one word. *`/readyz` still to come.*
      ✅ 2026-07-26 (night) — `/readyz` shipped, and the difference between the two is the whole reason they are separate endpoints rather than one convenient probe: **`/healthz` never touches the database** (a liveness probe that fails on a slow query gets the process killed and restarted into the same slow query), while `/readyz` runs `SchemaVersion` under a 2-second timeout and answers `503 unready`. One word each — a readiness probe is unauthenticated by definition, so anything it says is said to everyone.

- [x] **7.6** Static serving + `web/` layout
      ✅ 2026-07-26 — Static serving from the assembled `bin/web`, with precompressed `.gz` siblings preferred and the `application/wasm` content type `instantiateStreaming` requires.

- [ ] **7.7** `cmd/articleflux` — config load, **validate-and-fail-loudly at boot** (TLS readable, bind vs
      credentials, storage writable, LLM keys well-formed, IMAP reachable), graceful shutdown
      ◧ 2026-07-26 (night) — **`app.Preflight`** covers three of the five and the server refuses to listen without them: an account exists (unless `-dev`), `webRoot/index.html` exists, and the data directory is writable. It returns a **joined** error rather than the first, because someone setting up a droplet usually has several wrong at once and a one-at-a-time boot loop is a miserable way to find that out. The writability check **writes and removes a probe file** rather than stat-ing the directory — a directory can be listable and not writable, and SQLite must create the `-wal` and `-shm` siblings, not just open the database. `-dev` off a loopback bind is refused here too, which is the bind-vs-credentials check in its only form that currently applies. Graceful shutdown is wired (`signal.NotifyContext`). *Owed: TLS files, LLM key shape, IMAP reachability — none of which have a configuration to validate yet.*

- [ ] **7.8** **Version-skew handshake** — client build stamp in the tunnel handshake, server minimum
      version, refusal below it. The SW-cached wasm makes this inevitable, not hypothetical. §22.10
- [x] **7.9 G4 · `articleflux init`** — create tenant 1 + the first superadmin, or print a one-time
      15-minute enrolment token. **The server refuses to serve while no superadmin exists.**
      *Done when: it runs once, is audited, and cannot be re-run.* §22.3
      ✅ **G4 PASSED 2026-07-26 (night)** — `articleflux init -user … -password …` creates exactly one tenant and one superadmin, and **refuses on a populated instance** (`CountUsers > 0`), pointing at `adduser` / `passwd` instead: `init` on a live box is nearly always someone re-running the setup steps, and silently adding a second superadmin is worse than an error. `app.Preflight` is the other half — the server will not listen with zero accounts unless `-dev`. **No enrolment token**, deliberately: it is a second bootstrap path to secure, and filesystem access is already the proof of ownership every rung of §7.2 rests on. Password resolves flag → `ARTICLEFLUX_PASSWORD` → terminal prompt, so it need never appear in a process listing. *Owed: the audit row — there is no `audit` table yet, so "is audited" is not met.*

- [x] **7.10** `articleflux admin reset-password` break-glass §7.2
      ✅ 2026-07-26 (night) — spelled **`articleflux passwd -user … -password …`**, plus `adduser` for a second account. Both validate the role against the four the column documents (an account created as `"admins"` fails closed on every check with no clue why) and enforce a **12-character minimum with no composition rules** — length is the only property that reliably costs an attacker anything, and "must contain a symbol" reliably costs the user a password they write down somewhere worse. `passwd` revokes **every** session for that user, which is the point of a break-glass reset.
- [ ] **7.11** `internal/log` — `slog`, leveled, request-id threaded through handlers **and jobs**.
      **Never log** secrets, note bodies, article bodies, or LLM payloads. §22.11
      ◧ 2026-07-26 — `slog` is wired through the app and leveled, and §22.11's never-log rule is observed — the OpenAI TTS error path logs the provider message and returns a safe string, because provider errors can echo the user's article. Request-id threading is not done.

> *Done when:* `articleflux init` → login over the tunnel → one unary RPC → one streamed event, driven from
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
      ✅ 2026-07-26 — `web/index.html` + the bootstrap; assembled into `bin/web` by `./scripts/make.ps1 wasm`, with precompressed `.gz` siblings written temp-then-move.

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
      ⏸ 2026-07-26 — **Deferred, deliberately — still open.** Every string is still inline English. Doing this properly means routing ~200 strings through GWC `i18n`, and the UI is still changing shape weekly — retrofitting once it settles is cheaper than re-extracting after every redesign. The debt is real and §22.16 already names it.

- [x] **8.5 `client/design/tokens.go`** — the fanciful palette via `css.Root` + `css.Custom`, the type
      scale, spacing, `css.Preflight()`, and **`HueFor(sourceID)`** — a deterministic per-source hue.
      That hue is the design's one real idea, and it must be a pure function so the sidebar dot, the
      list edge, the highlight tint and the article wash always agree.
      ✅ 2026-07-26 — `client/design/tokens.go` — the palette **transcribed** from `design/03-fanciful.html`, plus `HueFor` (7 named hues, then OKLCH at matched L/C).

- [x] **8.6 `client/data/conn.go`** — dial, auth, **build stamp** (7.8), and a **hand-rolled reconnect
      watch loop** — *not* `WithReconnectPolicy`; CashFlux found it can't fire once a blocking read is
      in flight
      ✅ 2026-07-26 — `client/data` dials the tunnel and reports `ConnState`; the badge reads it.
      ✅ 2026-07-26 — **retry policy, both halves.** `WithReconnectPolicy` sets the backoff
      (500ms → ×1.6 → 20s cap, jitter 0.2) because gRPC's own 120s cap is a datacentre number and
      this is one tab talking to one box that gets restarted and lid-closed; there is **no attempt
      limit** — it retries for as long as the page is open. The hand-rolled half is
      `Client.Watch`: `GetState`/`WaitForStateChange` drives the indicator without polling, calls
      `Connect()` on Idle (nothing re-dials an idle conn until an RPC asks, so an untouched tab
      would sit disconnected), and fires `onRecover` on each return to Ready — which is where the
      reader refetches, since a reconnect is exactly the moment the screen went stale. Calls
      default to `WaitForReady(true)` so a click during a blip waits out the reconnect instead of
      erroring, bounded by `callTimeout`; Refresh and Subscribe opt back out (a 2-minute silent
      wait is worse than an honest "disconnected"). **Still owed at 8.7:** a blocking `WatchEvents`
      read can wedge without the conn leaving Ready — that is the CashFlux failure this note
      warned about, and `Watch` as written would not see it.

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

## Tier 8b — shipped, but never planned

Everything below was built in response to using the thing, not from the spec. It is listed here so
the plan and the product describe the same application: an untracked feature is one nobody can decide
to remove. Each carries the decision it became, so the reasoning is findable from either document.

**Reading**

- [x] **8b.1 The reading stream** — A28, §20.9. Scrolling appends the next article and prepends the
      previous one; nothing is taken away. Marks read on scroll-past and on click.
- [x] **8b.2 Neighbour prefetch** — bodies either side of the current article are fetched on arrival,
      so the skeleton is only ever seen on a cold open.
- [x] **8b.3 Long-article clamp** — over 900 words collapses with "Read the rest", so scan time per
      item stays roughly constant. One 4,000-word essay between two headlines makes a feed
      unpredictable to scan, and scanning is what the stream is for.
- [x] **8b.4 Continuous-read plumbing** — `OnScrollNearEnd` / `OnScrollNearTop` re-arm on **growth**,
      not only on position; `KeepScrollAnchored` holds the reader's place across a prepend;
      `OnTopmostChild` decides which article is being read from scroll position rather than from what
      was clicked.

**The list**

- [x] **8b.5 True-length virtual list** — A29, §20.10. `ListItemsResponse.total` from a `COUNT` that
      shares its filter builder with the page query, placeholder rows for unloaded indices, and a
      filler driven by scroll POSITION rather than proximity to the loaded end.
- [x] **8b.6 Loading placeholders** — §20.8. Skeletons shaped like the thing they stand in for, in the
      rail, the list and the article. Three in-flight flags, not one.
- [x] **8b.7 Row states** — §20.12. `new` / `unread` / `stale` (30d) / `read`, as words as well as
      colour. Notes preview their first words in the row, because in the Notes stream what you
      remember is what *you* wrote.
- [x] **8b.8 Favicons** — 30-day server-side cache keyed by host, served with a transparent pixel for
      hosts that have none so a miss costs one request a month. Shown over the source hue in the rail,
      the item rows and the article eyebrow; the hue stays underneath so a site with no icon is still
      identifiable.

**Signals**

- [x] **8b.9 Verdicts** — A27, migration `0006_rating`. Like / dislike, `rating ∈ {-1,0,+1}`, one
      signed column because the two are mutually exclusive by definition. Re-pressing clears.
- [x] **8b.10 Read later** — reuses `starred_at` and `LIST_SCOPE_STARRED`, which already existed and
      already synced. Plus **mark unread**, without which a stream that marks things read as you
      scroll past them has no way back.
- [x] **8b.11 Quick notes** — `item_notes` (0005), per article, with a Notes stream. **8b.12 Feed
      tags** — per-user labels on a subscription, created on first use.

**Navigation and input**

- [x] **8b.13 Command palette** — Ctrl-K. Matches what the client already holds, three-tier ranking,
      deliberately not fuzzy: subsequence scoring makes everything match everything at 151 feeds.
- [x] **8b.14 Keyboard-complete** — A32, §20.14. Arrows within a pane, Tab between, `1`/`2`/`3` pane
      jumps, `?` sheet. Roving focus read from the DOM so the pointer and the keyboard cannot disagree.
- [x] **8b.15 Feed name filter** — appears past 8 feeds. **8b.16 Unread-only feed toggle** — at 151
      subscriptions the rail is mostly feeds that did not publish today.
- [x] **8b.17 Search** — FTS5 over `items_fts`, quoted terms, wired to `/`.

**Listening**

- [x] **8b.18 Browser speech** — A31, §10.7. Free, offline, the reader's own system voice, chunked to
      sentences to dodge Chrome's fifteen-second cutoff.
- [x] **8b.19 Smart+ voice** — OpenAI TTS behind `GET /speech`, four gates, disk cache keyed by
      (item, model, voice), host allowlist checked against the URL being requested rather than against
      the constant. Off per user by default; a server with no key cannot egress at all.

**Continuity and configuration**

- [x] **8b.20 Resume** — A30, §20.13. Scope, article and all three filters restored before the first
      list is fetched, so there is no flash of the wrong feed.
- [x] **8b.21 Per-feed settings** — A33, §20.15. Gear on hover, panel grouped by who a setting belongs
      to, one transaction across both tables, poll interval clamped at the write.
- [x] **8b.22 Server-side pane widths** — layout belongs to an account, not a browser.

**Housekeeping**

- [x] **8b.23 Repo hygiene** — everything generated under `bin/`, MIT LICENSE, README, `make.ps1`,
      hardened `.gitignore` (secrets by extension, `speech-cache/`, `*.opml`), `.gitattributes`, and
      the first commit.
- [x] **8b.24 e2e harness** — Playwright against a real server, real database and a fixture feed
      server, with `killListener`-by-PID and a per-run database. *Its assertions are stale: they still
      reference starring, the pre-transcription breakpoints and the 104px row. See below.*

> **Known-stale, and the next thing to fix:** `e2e/*.spec.mjs` asserts against the app as it was
> before the design transcription, the verdict change and the 96px row. Until they are updated their
> output means nothing, so the suite is currently **not** a gate — which is exactly the state a test
> suite must not be left in quietly.

**Organisation** *(2026-07-26, night)*

- [x] **8b.25 Categories in the rail** — A37, §6.10. Folders as a repository, six RPCs and a rail
      section, all per-user and flat. Shown **as well as** the flat feed list: the flat list answers
      "where is that feed", the categories answer "what have I got on this subject", and 151 rows
      cannot answer the second. Each category folds; the open set travels as a **comma-joined string**,
      not a map, because `railProps` is compared by value and a map field compares by identity — it
      would defeat the re-render bailout for the whole rail.
- [x] **8b.26 The rail is head / scroll / foot** — the five streams and the masthead are pinned, the
      scroll starts at the Feeds band, add-a-feed sits at the foot. Six rows paid for once beats
      scrolling back to them. All three groups fold, and the state is remembered.
- [x] **8b.27 The add-a-feed dialog** — §20.18. URL, name, category, and nothing else. Poll interval,
      cache depth and mute belong to a feed you already have an opinion about, and putting them here
      would make adding a feed a configuration exercise.
- [x] **8b.28 Tag identity vs presentation** — A38, `0008_tag_style.sql`, `internal/tagglyph`,
      `UpdateTag`, and a per-tag panel behind the same gear the feed rows use. Fifty marks in seven
      groups, **text presentation only** so they inherit the row's colour and weight — a rail of emoji
      reads as stickers stuck onto the design, and the one tag wearing one outshouts the 150 feeds
      above it. The **character is stored, not an index into the list**.

**Configuration and the operator**

- [x] **8b.29 The settings surface** — §20.17. Seven tabs, and the last three exist because a
      self-hosted app has no dashboard behind it: **Server** ("is it healthy"), **Activity** (the log
      ring — "what just happened"), **Speed** (per-RPC latency — "why is it slow").
- [x] **8b.30 `OnDelegatedBlur`** — `focusout`, not `blur`, because blur does not bubble and a
      delegated listener on a stable container never sees it. This is the debounced note's safety net:
      a reader who types and immediately clicks away has finished with that field, and waiting out the
      rest of the debounce is how autosave loses writing. `platform.Listener` grew an `extra` closure
      so one Listener can release a second event, a second target, or an interval.

**Built but not reachable — and that is the interesting part of the list**

- [ ] **8b.31 The theming engine and the motion system** — A39, §20.16. Five themes and three durations
      exist in `client/design/{theme,motion}.go`, and the sheet emits `Fanciful.Vars()` +
      `MotionVars()` at first paint, so the tokens are live. **Nothing writes them at runtime**: there
      is no applier on `documentElement.style`, no picker in Settings, and no `theme` / `motion` pref.
      Four of the five themes are therefore unreachable and `--mo` is only ever set by the
      `prefers-reduced-motion` block. *Done when: switching themes twice repaints correctly and the
      choice survives a reload on another machine (A30 applies — it is account state, not browser
      state).*
- [ ] **8b.32 Restore the wasm build.** `GOOS=js GOARCH=wasm go build ./client/...` **fails** —
      `client/view/reader.go` has seven declared-and-unused state hooks from the category and add-feed
      work, and still passes `addValue` / `onAddInput` / `onAddKey` to a `railProps` that no longer has
      them. **`go build ./...` does not compile the client**, so the native build and all Go tests are
      green while the app does not build at all. *Done when: the wasm build is in CI's default path,
      which is the only thing that stops this recurring.*
- [ ] **8b.33 The client half of authentication.** The server mints sessions (§7.1a) and nothing in
      `client/` calls `Login`, stores a token, or sends `authorization` metadata — so every RPC falls
      through to `devScope`, and an instance started **without `-dev` serves a reader that cannot get a
      session**. Needs: a login screen, token storage that survives a reload, the metadata on every
      call, `WhoAmI` on connect to choose between the reader and the login screen, a visible banner
      when `dev_mode` is true, and a path back to the login screen on `Unauthenticated`. *This is what
      makes A9 (remote deployment) reachable, and it is the top of the list.*

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
`/setup` (first-run wizard after `articleflux init`) · `/404` · **the version-skew screen** (client below
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
| **First run** | `articleflux init` → one-time token printed → `/enroll/:token` → tenant 1 + superadmin → `/setup` wizard → OPML import or starter feeds |
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
