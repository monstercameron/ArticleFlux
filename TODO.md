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
- [ ] **D20 · The proxy origin.** Separate hostname (`proxy.<instance>`) or a sandboxed iframe on the
      app's own origin? Plan §10.1b argues the hostname, and §25.0 proposes it. **This is the one
      choice here that is expensive to defer**: signed proxy URLs get minted, cached and stored, so
      splitting the origin afterwards is a migration of every artifact rather than a config change.
      *Blocks 7.12.*
- [ ] **D21 · How does the ladder know which rung it is on?** §10.1-R orders the runtime path
      **real page → (blocked) frame stream → (bandwidth) compressed rendered HTML → reader text**, and
      both arrows are detections. "Blocked" is close to undetectable from the client: a blocked fetch,
      a DNS failure, a captive portal and plain offline are one opaque error, and a refused iframe is
      indistinguishable from a loading one. §25.0 proposes **manual in v1** — the switcher operates
      the ladder — with automatic escalation waiting on a real probe. The bandwidth arrow is the
      easier half and should be *measured* from the stream's own throughput, not predicted from
      `navigator.connection`. *Blocks the automatic half of 8.22; blocks nothing else.*
- [ ] **D19 · Does the renderer ship, and where does the browser run?** §25.0 proposes yes, on the
      reference box, one render at a time, flag-gated off. Edge is already installed and `chromedp`
      attaches to an existing Chromium, so this is not a new host — but it is a browser process on the
      box that also serves reading, and on the fanless machine repeated renders throttle. *Blocks 6.14;
      does **not** block 4.13, 6.15 or 7.12, which are the static half and stand alone.*

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
      ✅ 2026-07-26 (night) — **it earned its place, and the original reasoning is untouched.** That
      reasoning was about the DEVELOPMENT box; it does not extend to the deployment target, which has
      `make`, has no PowerShell, and has to build and run this (A9). So there is now a `Makefile`
      alongside `make.ps1` with the **same verbs, one for one** — the "two build systems, one of which
      is a lie" hazard is only a hazard if they disagree, so anything added to one goes in the other.
      It adds three deployment verbs the PowerShell script has no use for: `linux`, `install-service`,
      `backup`. `Makefile text eol=lf` is pinned in `.gitattributes`: a recipe line arriving with CRLF
      makes GNU make hand a trailing `\r` to the shell, which fails as `command not found: go\r` and
      sends people hunting a broken toolchain.
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

**9. Themes and motion** (A39) — five themes as sets of custom-property values, seven accents (with a
separate light set), three reading sizes, and every duration written `calc(var(--mo) * t)` so reduced
motion makes an animation *absent* rather than suppressed. The previous mechanism was a
`* { transition: none }` rule at the bottom of the sheet, which is a broom: it works until someone
writes a transition with `!important`, in a later layer, or on a pseudo-element, and nothing fails
loudly when it stops working. The Appearance tab applies all four prefs by writing tokens onto
`documentElement.style`, so **no component re-renders when the theme changes** — switching themes with
151 rail rows and 3,600 virtualised items on screen costs a paint, not a reconciliation.

**9a. What the motion is spent on, and the guards that keep it honest** (§20.16.1–2) — the selection
in the item list is one cursor that **travels** rather than a background lighting on one row and going
out on another, drawn as a pseudo-element of the scroll container so it lives in content space and
needs no scroll arithmetic. Spawning animates the arrival of *data*, not of an element on screen: the
list is virtualised, so `setItems` diffs each incoming list against the one it replaces and only
genuinely-new ids animate — which is what keeps the list still under the reader's hand on `j`. The
three waits that were bare text carry an indeterminate rule, and the four looping animations are gated
on **amplitude** rather than duration, because `animation-duration: 0s` with `iteration-count:
infinite` is a spec corner and "the skeleton froze at a visibly wrong offset" would appear only for the
readers who asked for less motion. A39 stopped being a convention when `sheet_test.go` landed: no
dangling token, no token a theme cannot reach, no ungated duration, no literal colour, and a
readability floor that found four AA failures on the way in — three fixed, one (**D22**) escalated to
the mockup.

**9b. The splash** (§20.20) — the module is six megabytes gzipped, and a wordmark on a dark screen for
eight seconds is indistinguishable from a hang. Real byte progress, streamed through a counter that
still preserves streaming compilation, with `content-length` treated as a hint because the server
prefers a precompressed `.gz` while `res.body` yields decoded bytes. It wears the reader's own theme,
mirrored to `localStorage` by `applyAppearance` purely so this one frame can be right — the alternative
is a dark flash on a bright screen on every load for anyone running the light theme, which is the one
flash a splash exists to prevent.

**9c. Focus mode** (§20.21) — the reading pane takes the window, on `w` or the control pinned top
right. The columns **close** rather than vanish, because they are grid tracks and `display: none`
cannot be animated: those two panes are the navigation, and something that disappears with no transit
leaves the reader unsure whether it was hidden or lost. Full width is the means and not the point, so
the article recentres — a 66-character column pinned to the left of a 1900px window is worse than the
layout it replaced.

**12. The login screen** — `client/view/{root,login}.go` + `client/data/auth.go`. `Root` is now the
mount point and `Reader` is its child, so an unauthenticated page never *constructs* the reader: doing
otherwise would fetch a feed list the caller is not entitled to and paint the furniture of an account
nobody has proven they own. Three phases, and `checking` is the one that earns its place — without it a
page with a good token flashes the login screen for a few hundred milliseconds, which trains people to
start typing a password they do not need.

**10. `internal/sanitize`** (TODO 2.9) — five named policies over GWC's engine, because GWC has no
opinion about *where the HTML came from* and that is the whole question. The `Newsletter` policy drops
remote images outright rather than proxying them: proxying a tracking pixel still tells the sender the
message was opened.

**11. The D7 corpus** — twelve committed article pages and a three-library bake-off, in a **separate Go
module** so the two libraries that lose do not enter the server's dependency graph forever. The
decision is still open; what exists is the evidence and a command to re-run it.

> **Verified 2026-07-26, 21:35:** `go build ./...`, `GOOS=js GOARCH=wasm go build ./client/...` and
> `go test ./...` all green.
>
> **One lesson from the middle of this batch, worth more than the batch.** The wasm build was broken
> for a stretch — the rail's category props were half-wired while `railProps` had already changed — and
> nothing said so, because **`go build ./...` does not compile the client**. The native build stayed
> green, every Go test stayed green, and the app did not build at all. A green Go suite is not evidence
> that the client compiles, let alone runs. Putting the wasm build on CI's default path is **8b.32**,
> and until it is there this will happen again.

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
      ⊕ **Owed at M27: a sixth policy, `Snapshot`** (§10.1b) — a whole fetched page rather than an
      article fragment, so it keeps layout CSS and drops script, iframe, form and every event
      attribute. It inherits the 48-vector corpus by construction, which is the argument for the
      named-policy shape paying for itself the first time it is extended. **`Newsletter` is explicitly
      not affected**: 4.13's rewrite hook is per-policy, so the asset proxy cannot quietly turn
      dropped tracking pixels back into proxied ones.

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

- [x] **3.1** `migrations/0001_init.sql` — **all ~49 tables**: §6.2 core, §6.3 A22 rules, §6.6 tags,
      §6.7 identity + interest, **§6.8 the rest** (folders, notes, bookmarks, engagements, jobs,
      settings, auth, offline, notifications). `REFERENCES` on every FK-shaped column, the three §6.5
      indexes, and the folder depth `CHECK`s ← G1
      ◧ 2026-07-26 — **9 tables, not ~49.** `0001_init` covers the reading core (tenants, users, sessions, sources, subscriptions, folders, items, user_item_state, items_fts + triggers); 0002–0008 add prefs, favicons, tags, notes, ratings, engagements and tag style (`label` + `glyph`, A38). The rules, bookmarks, mailbox and interest-layer tables are not written yet — they arrive with the tiers that use them, which is cheaper than a 49-table migration nothing reads. **`folders` was in 0001 and unused until the rail needed it** (§6.10) — the one case where writing the column early paid off, since categories shipped with no migration at all.

      ✅ 2026-07-26 — **55 tables**, in migrations 0009–0013 grouped by concern rather than by
      tier. Two conventions differ from §6.8's DDL sketch and both are forced: ids are TEXT because
      every shipped table uses `idgen`'s sortable ids, and **a foreign key whose type does not match
      its referent is not a foreign key** — SQLite accepts the DDL and then never matches a row.
      Cascades are the part to review: `user_id` cascades everywhere, `item_id` almost never does
      (items are global, A22, so a source cleanup must not delete a note, a tag or a bookmark), and
      `rule_hits` is the deliberate exception because a hit is a statement *about* an item rather
      than user work. Three tests: every specified table exists **by name** (a count would pass a
      rename plus an addition), every FK-shaped column has a `REFERENCES` or a stated exemption, and
      both new FTS indexes track inserts, updates *and* deletes.
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

- [x] **3.7 G2 · The leak-test harness** — reflect over exported repository methods; assert none
      returns a B-owned row under an A scope. **Fails on any method added without coverage.**
      *Done when: it passes with zero repositories and would fail the moment a bad one is added.*
      ◧ 2026-07-26 — Not started. Guard 4 enforces that a Scope is *taken*; nothing yet asserts it is *used* in the WHERE clause. `TestTenantIsolation` covers the current methods by hand, which does not scale to the next twenty.

      ✅ 2026-07-26 — `internal/store/leak_test.go`. It does not test methods, it **enumerates**
      them: reflection walks every exported repository method, and each one taking a `Scope` is
      called under tenant A's scope while being handed tenant B's identifiers — the shape of the
      real attack, a valid session plus someone else's id. **38 scoped methods swept**, every row
      tenant B owns carries a canary, and any appearance of it at any depth in any return value is a
      leak. The other half is what keeps it valuable: a method taking no `Scope` must appear in
      `unscopedByDesign` **with a stated reason** or the test fails, so a new method cannot arrive
      uncovered (18 listed — bootstrap, authentication, and the global A14 rows). Guard 4 proved the
      Scope was *taken*; this proves it reaches the `WHERE`. **Verified by deliberately breaking
      `ListFeeds`' tenant filter**: the harness failed, named the method and printed the leaked feed
      title.
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

- [x] **4.6 `netscape`** — Netscape bookmark HTML both ways + Chrome JSON in. *Done when: our export
      imports cleanly into a real Chrome.*
      ✅ 2026-07-26 — `internal/netscape`. Read with an HTML5 parser (the format's unclosed `<DT>`
      and implied nesting is exactly the soup tree construction exists for) and **written in the same
      malformed shape on purpose**, because browsers parse it by convention and a tidy file is one
      some importers get wrong. Two bugs the tests found: sibling folders sharing a path slice, where
      the second overwrites the first's name in every bookmark already collected; and `ADD_DATE`
      units, where a 16-digit value fell into the milliseconds branch and produced the year 425014.
      *Owed: the ticket's actual bar — importing our export into a real Chrome — is not automated;
      there is no Chrome profile on this box.*
- [x] **4.7 `scrapesel`** — rule + HTML → items ← 2.8, cascadia
      ✅ 2026-07-26 — `internal/scrapesel`. `Compile` holds every refusable error (including a link
      selector reading text instead of `@href`, which yields a feed of unopenable items); `Extract`
      never fails on content, because a site can serve anything and an empty poll is a health signal.
      It reports `Matched` and `Skipped`, which is what separates **a redesign that broke the
      selector from a site that stopped publishing** — identical from the item count alone, opposite
      responses. Summary reads inner HTML, identity keeps the URL fragment, content goes through
      `sanitize.Feed`, and extraction is capped at 200 while still reporting the true match count.
- [x] **4.8 `mailparse`** — MIME → normalised item; prefer `text/html`, fall back to `text/plain`,
      hand off to the `newsletter` sanitiser. ← 2.9 §14.1
      ✅ 2026-07-26 — `internal/mailparse`. Adds the three things `net/mail` does not do and every
      real newsletter needs: multipart walking, quoted-printable/base64 transfer decoding, and RFC
      2047 encoded-words. **The last `multipart/alternative` part wins**, because RFC 2046 orders
      them worst-to-best and first-wins takes the "view this in your browser" line on every message
      that has one. `NaturalKey` is the single correct way to build §6.4's per-user key — global
      keying would merge two people's private mail, a privacy failure rather than a bug.
- [x] **4.9 `rules`** — **pure**: `(item, []Rule) → []Action`. Ops, ordering, `stop_processing`. No DB,
      no side effects. *Done when: a table test covers every op and precedence case.* §13.1
      ✅ 2026-07-26 — `internal/rules`. Pure, and `now` is a parameter, so §13.4's dry-run preview is
      *the same code* as the apply — a separately-implemented preview eventually lies about what the
      rule will do. Three decisions worth arguing with: an empty condition set matches **nothing**
      (empty-AND is conventionally true, and true here means a half-finished rule mutes the feed);
      the **earlier** rule wins on conflict, or moving a rule up the list stops changing its
      precedence; and `not_contains` over tags means "no tag matches". ~2µs per item per subscriber.
- [x] **4.10 `topics`** — **pure**: vectors → clusters with centroids, top terms, deterministic labels.
      Works on TF-IDF vectors alone; embeddings just make it better. ← 2.11 §18.2
      ✅ 2026-07-26 — `internal/topics`. Clusters TF-IDF vectors, so Smart needs no model and no
      network. Labels are deterministic and dull on purpose: one that changes on recomputation
      renames the reader's interests nightly. Input is sorted before clustering so tie-breaks cannot
      depend on row order. Clusters under three members are reported **unclustered** rather than
      promoted — a model that forces every article into a topic invents interests. Plus `Nearest`,
      `Starved` (Explore's under-served slot) and `Concentration`; under 50 items it reports cold
      start.
- [x] **4.11 `rank`** — **pure**: `(signals, item) → (score, []Reason)`. Includes `TopicMatch`,
      `VolumePenalty`, per-source half-life, and **the alternate firehose scoring** that drops
      `FeedAffinity` in highlights mode. *Done when: the golden fixture passes and a deliberately
      lopsided-volume fixture proves a firehose can't dominate.* §18.4–18.5
      ✅ 2026-07-26 — `internal/rank`. **The score is literally the sum of its reasons**, asserted,
      because §18.9 shows them verbatim and a drifted reason list is a lie that looks like
      transparency. `VolumePenalty` is logarithmic and clamped to 1 (linear effectively mutes feeds
      the reader chose; unbounded means the weights stop describing relative influence). Highlights
      mode **drops the feed term entirely** and redistributes it, and the threshold is set as a rate
      — "about 3 a week" — because nobody can reason about "score > 0.62". Golden fixture with each
      score's term breakdown recorded, plus the ticket's test: a weekly essayist still reaches the
      top ten against 94 items a day, and the firehose is not muted out of it either.
- [x] **4.12 `recommend`** — **pure**: candidates + evidence → scored list, with the **health gate**
      (no feed / silent 6 months / >20 per day → rejected). *Done when: a dead site and a firehose are
      both refused, and every survivor carries a human-readable evidence string.* §18.7

      ✅ 2026-07-26 — `internal/recommend`. The health gate refuses a dead site and a firehose for
      opposite reasons, plus no-feed, unreachable, aggregator, undated, subscribed, muted and
      dismissed. Every survivor carries the §18.7 evidence sentence assembled from the same fields
      that produced the score. Distinct referring writers outweigh raw link count, and links are
      weighted by engagement with the *linking* article. Adjacent candidates get reserved slots —
      they score lower by construction, so a plain top-N drops them and the anti-filter-bubble
      guardrail becomes a comment. Rejections are returned with reasons: a gate nobody can inspect is
      a gate nobody can fix.
- [x] **4.13 `rewrite`** — **pure**: HTML in, HTML out, with every subresource URL rewritten through a
      caller-supplied `func(absURL string) string`. Resolves against a base URL, and covers the whole
      surface rather than the obvious quarter: `img/@src` · `@srcset` (both `img` and `source`, with
      the descriptor syntax preserved) · `video/@poster` · `link[rel=stylesheet]` · `@import` and
      `url()` **inside inline `<style>` and inside fetched CSS**, recursively · `a/@href` · `<base>`.
      Strips `integrity` (the bytes change, so SRI would fail every asset it protects) and any
      `<meta http-equiv="Content-Security-Policy">`. ← 2.4 §10.1b
      *Done when: a fixture page with a relative `srcset`, a nested `@import` two levels deep, and a
      protocol-relative `//cdn` URL all come out pointing at the proxy — and a second pass over the
      output is a no-op.*
      **Cheap and self-contained. This is the piece 1a, 2 and 2r all share, and it is the one that
      quietly grows a long tail of formats, so it gets a table test from the first line.**
      ✅ 2026-07-26 — `internal/rewrite`. Fragments and documents are told apart rather than
      configured, because feed content is a run of `<p>` and parsing that as a document silently welds
      an `<html><body>` wrapper onto every article. `<base href>` is honoured **and then removed** —
      honouring it is correctness, removing it is the security half, since a surviving `<base>` points
      anything we missed straight back at the origin; inside a *fragment* it is dropped without being
      honoured at all, or one feed item could retarget every relative URL in the reading pane.
      CSS is scanned, not parsed: the question is only where the URLs are, and a scanner cannot be
      defeated by a shorthand property nobody thought to enumerate. Idempotence is a test, not a
      hope. **Two bugs the table found before any of it shipped:** `ParseFragment` rejects a context
      node whose `Data` and `DataAtom` disagree, and a node dropped at fragment top level has no
      parent to be removed from — the meta-CSP strip silently did nothing in exactly the case that
      matters. *Deviation: `srcset="a.png,b.png"` is one candidate, not two. That is what the WHATWG
      algorithm says and what browsers do; splitting it would rewrite two URLs the browser never
      requests and miss the one it does.*

> *Done when:* the whole tier is unit-tested with **zero database and zero network**.

---

## Tier 5 — Repositories

One package each, `Scope` first, leak test per repo (3.7 enforces it).

- [x] **5.1** `tenants` · `users` · `roles` · `user_roles` · `invites` · `devices` · **`api_tokens`
      (scope is a fixed enum, never inherited from the owner's role)** · `shares` · `public_shares`
      ◧ 2026-07-26 — `tenants`, `users` and `sessions` exist and `ScopeForSession` resolves a token hash into a Scope. `roles`, `user_roles`, `invites`, `devices` and `api_tokens` are not — they belong with 6.1/6.2, and there is one local account until then.
      ◧ 2026-07-26 (night) — the **session lifecycle** landed with 6.1's first half: `UserForLogin` (loud on a username that exists in two tenants, rather than a silent wrong-tenant login), `CreateSession` (session row + `last_login_at` in one transaction, so the account screen cannot lie about "was that me?"), `RevokeSession`, `RevokeAllSessions`, `PurgeExpiredSessions` (run by the poller, revoked rows kept a week), `Identity`, `CountUsers`, `SetPasswordHash`, `AddUser`, `FirstTenantID`. All ten are **unscoped by design** and registered as such in `internal/tools/guards` with a reason each — the guard's exemption list is now where "why does this method not take a Scope" is answered. Note what is *not* exempt: `Identity` takes a Scope, because "who am I" is a question about an authenticated caller.
      > `SetPasswordHash` deliberately does **not** revoke sessions. Two callers want opposite things: the break-glass reset is a password *change* and must revoke everything, while a login that re-hashes under stronger Argon2id parameters changed no password and must revoke nothing. Bundling the revoke made the first login after a parameter bump log the reader straight back out.

      ✅ 2026-07-26 — `internal/store/identityrepo.go` completes it: `roles`, `user_roles`,
      `invites`, `devices` and `api_tokens`. D12's shape, so there is an invites table and no
      registration one. A code is returned **once** and only its hash stored — an invite is a bearer
      credential for its whole life. Every failure reason returns **one** error, because
      distinguishing expired from used from never-existed tells someone probing which codes once
      existed. A token's scope is a **fixed enum, never inherited**: the effective set is the
      intersection with the role, so minting one can never be an escalation. Refresh reuse revokes
      the whole **family** — which holder is the thief is unknowable, so both are logged out.
- [x] **5.2** `sources` · `subscriptions` — **soft-deactivate, never delete** (A22) · `natural_key`
      **per-user for `kind='mailbox'`** (§6.4) · `home_mode` and the highlights fields ← D17
      ✅ 2026-07-26 — `Subscribe` / `Unsubscribe` / `SubscribedSources` — `natural_key` deduplicates two tenants onto one polled row (A14), and unsubscribing never touches the source (A22).

- [x] **5.3** `items` · `item_revisions` · `user_item_state` — the denormalised `source_id`/
      `published_at` and **all three §6.5 indexes**
      ✅ 2026-07-26 — `items` + `user_item_state` with the denormalised `source_id`, keyset paging that never skips or repeats (`TestKeysetPaginationCoversEveryRowOnce`), and `CountQuery` sharing its filter builder with `ListItems` so the two cannot describe different result sets. *`item_revisions` is not written yet.*

- [x] **5.4 G3 · Hot-query benchmark** — 50k items × 3 users, **all three shapes**: flat unread count,
      unread-by-newest, **unread-by-folder**. *Do not proceed without numbers.* R2
      ◧ 2026-07-26 — `internal/store/bigdb_test.go` runs the three shapes against the REAL dev database (3,621 items): paging 9ms, mark-all-read 16ms, search prompt. It is not the 50k × 3-user synthetic fixture the plan asks for, and it skips when no dev database is present — so it proves the queries are fine today and guards nothing in CI.

      ✅ **PASSED 2026-07-26 — and the answer was not the one the plan expected.**
      `internal/store/hotquery_test.go`, `HOTQUERY=1`: 50,000 items × 3 users × 150 feeds, all four
      shapes, median of seven runs after a warm-up.

      **unread by newest 478ms → 0.5ms. unread by folder 178ms → 0.5ms. Keyset page 40 408ms →
      0.3ms.** §6.5's prescribed fix — denormalise `source_id`/`published_at` onto
      `user_item_state` — was **never built** (5.3 recorded it as done; the columns do not exist),
      and it turned out **not to be needed**. `EXPLAIN QUERY PLAN` showed the planner driving from
      `subscriptions`, joining all ~16,700 unread rows and sorting every one in a `TEMP B-TREE` to
      take 50. Pinning `items_published` — an index present since `0001_init` — fixed it with no
      migration.

      **A second bug fell out of the fix**: with the index pinned, page 2 on the real database went
      13ms → **1.3 seconds**, because the cursor's `a < ? OR (a = ? AND b < ?)` is not seekable.
      Rewritten as the row-value `(a, b) < (?, ?)` it became 0.5ms. Page 1 had got *faster* while
      page 2 collapsed — the regression shape a page-1 benchmark never sees.

      **Still over budget: the two counting shapes** — flat unread count 556ms, sidebar with
      per-feed counts 447ms. They cannot stop at 50. R2's materialised counter is now justified by
      measurement rather than assumed; see 5.4a. Recorded in `knownSlow` as a ratchet, so a
      regression past them fails and the entry must be deleted when the counter lands.

- [x] **5.4a · The materialised per-user unread counter** — R2's fallback, now measured and
      required. The sidebar renders per-feed unread counts on **every screen** and takes 447ms at
      50k items; the flat total takes 556ms. Both must visit every unread row, so no index helps.
      Maintain a count per `(user_id, source_id)`, written by ingest/fan-out, `SetItemState`,
      `MarkAllRead` and `UndoMarkAllRead`. *Done when: both shapes are inside the 150ms budget, their
      `knownSlow` entries are deleted, and a reconcile function proves the maintained counter equals
      a recomputed one after a randomised sequence of reads, unreads and mark-all-reads — drift is
      the whole risk of a denormalised counter and it is silent.*

      ✅ 2026-07-26 — `migrations/0015_uis_source.sql` + `countUnreadFast`/`ReconcileUnread`.
      **556ms → 3.4ms and 447ms → 3.8ms**; both `knownSlow` entries deleted, so `knownSlow` is now
      empty. No counter table was needed: §6.5's `source_id`/`published_at` denormalisation on
      `user_item_state` plus a partial index `WHERE read_at IS NULL` turns "unread in this source"
      into one index range. Both numbers are computed by the **same expression** — the badge is the
      sum of the sidebar's per-feed counts — because two numbers on one screen computed two ways is
      how they stop agreeing.

      **The index had to be pinned.** With `ANALYZE`'s statistics SQLite preferred the pre-existing
      `uis_user_unread` via an `ANY(user_id)` skip-scan — 50,000 rows per feed, 150 times, for
      **2.5s, worse than before the migration**. `INDEXED BY uis_unread_by_source` fixed it. That is
      now twice on this table that the planner has chosen a scan over the right index; the pattern
      is that a partial or composite index added late loses to whatever `ANALYZE` already has
      statistics for.

      **The real bug was upstream, and the counter only exposed it.** Counting from state rows is
      only correct if every visible item has one, and 80 of 3,806 items in the development database
      had none. Fan-out was creating them — but fan-out is a *queued job that applies rules*, so
      delivery depended on a worker running, not being retried, and the user having rules at all.
      Delivery is not a rule outcome; it is what ingest means. `deliver()` now writes the rows
      inside `IngestItems`' transaction, and `Subscribe` writes them for the items a **global**
      source (A14) already holds — without which a new subscriber to a popular feed starts at zero
      unread and only counts what arrives afterwards.

      Three guards, because the failure of a denormalised count is that nothing throws — the badge
      is just quietly low forever. A trigger fills both columns for any writer that forgets (the hot
      paths set them explicitly and the `WHEN` clause skips them, so it costs nothing); two more
      move a deactivated item out of the count by nulling `source_id`, which keeps the reader's star
      and rating intact where deleting the row would not; and `TestUnreadCountNeverDrifts` runs 120
      randomised reads, unreads, stars and mark-all-reads, comparing against a recomputation **after
      every single one**. `ReconcileUnread` returns the drift it repaired rather than swallowing it,
      and the suite asserts that number is 0 after the sequence — a reconciler the write path quietly
      relies on is not a safety net.
- [x] **5.5** `tags` · `item_tags` — A21, prerequisite for both rules and the sync API
      ◧ 2026-07-26 — Tags exist and are per-user, but they attach to a **subscription**, not an item. `item_tags` — which is what A21 actually specifies and what the sync API needs — is not built.

      ✅ 2026-07-26 — `internal/store/itemtags.go`. `item_tags` is A21's, and it is **not**
      `feed_tags`: "everything from this feed is rust" and "this article is about rust" are different
      statements. Both join one `tags` table, since a reader who labels a feed and an article "rust"
      means the same tag. Tagging an invisible item returns `ErrNotFound` — the write succeeding
      would itself answer "does this id exist in another tenant". Untagging leaves the tag behind.
      `UntagItemsByRule` is the counterpart to `UnmuteByRule`.
- [x] **5.6** `notes` (private by default) · `bookmarks` · `bookmark_tags`
      ✅ 2026-07-26 — `item_notes` (0005) — private, separate from `user_item_state` so a note is not coupled to read state, with `NotedItems` for the Notes stream. *Bookmarks are not built; read-later reuses `starred_at` instead.*

- [x] **5.7** `rules` · `rule_hits` · `scrape_rules` · `mailboxes`
      ✅ 2026-07-27 — `rules`/`rule_hits` (`rulescodec.go`, `fanout.go`) and `scrape_rules`
      (`scrape.go`) were already built. This is **`mailboxes`**: `migrations/0016_mailbox_sources.sql`
      + `internal/store/mailboxes.go`, 14 tests.

      **Its own repo type, because it holds a decryption key.** `MailboxRepo` mirrors `SettingsRepo`
      rather than hanging off `ReaderRepo`: a repository that can decrypt credentials should be
      something you ask for, not something every `NewReaderRepo` caller receives. A mailbox password
      is the one credential in this database belonging to a **third party** — it cannot be hashed,
      because the poller has to use it, and a leak is somebody's email account rather than their
      reading history. `Mailbox` has **no password field at all**, so listing mailboxes cannot
      serialise one into an RPC response and cannot start doing so when a field is added later;
      reading it is a separate scoped call. With no encryption key configured the repo **refuses**
      rather than storing plaintext — an operator told this failed will fix it, one whose password
      was written in the clear never finds out.

      **Three things §6.3/§6.5 specified and nobody had built.** `sources.owner_user_id` ("NULL
      unless kind='mailbox'") did not exist, so §17's account deletion had no way to find the mailbox
      sources a user owned except by `LIKE` over `natural_key` — derivable-by-string-parsing is not
      the same as a column. `mailboxes` had no scheduling columns, so nothing could say when to poll
      one. And **the feed poller would have tried to HTTP-fetch mailbox sources**: their `feed_url`
      is `mailbox:<id>:<sender>`, which is not a URL, so every poll forever would have been "not a
      recognisable feed" — the exact failure `pollOne` already guards against for `kind='scrape'`,
      with a comment saying it is how a feature like this silently never works. `DueSources` and
      `PollerLag` now exclude them (the latter too, or one mailbox reports a permanent backlog).

      **The leak harness now sweeps every repository type, not just `ReaderRepo`.** It swept one type
      for as long as there was one, and would have gone on reporting a clean sweep of that one while
      a whole new repo went unexamined — which is the decay this harness exists to prevent, one level
      up. `TestEveryRepoTypeIsSwept` scans the package source for `*Repo` types and fails on any the
      sweep does not cover or explicitly excuse. Mutation-tested: dropping `ListMailboxes`' tenant
      filter fails with `LEAK: ListMailboxes returned tenant B data`.

      *Out of scope, deliberately:* the IMAP client itself. There is no IMAP dependency in `go.mod`
      and connecting is Tier 6 / **M22**; `mailparse` (4.8) already turns a message into an item, and
      this ticket is the storage between them.
- [x] **5.8** `settings` · `views` · `engagements` (append-only, **with the §18.1 kind taxonomy
      including `impression` and `bulk_read`**) · `audit_log`
      ◧ 2026-07-26 — **`engagements` is DONE**, ahead of its tier because every day of reading
      without it is signal that cannot be reconstructed (R12). `migrations/0007_engagements.sql` +
      `store.RecordEngagements` (batched, one tx, `INSERT OR IGNORE` on a client-generated id so a
      retried batch cannot double-count) + the three reads the interest layer needs: `ItemSignals`,
      `FeedSignals`, `EngagementsSince`. `settings`, `views` and `audit_log` are untouched.
      ✅ 2026-07-26 — `views` and `audit_log` land in `internal/store/views.go`; `engagements` was
      already done. A view's spec is opaque JSON (§20.2's vocabulary grows with the UI) but
      **validated as JSON on the way in**, because a malformed one otherwise fails at render time,
      on a screen, long after the save reported success. `Audit` takes no `Scope` — §7.9 tombstones
      actors and the log must survive what it describes — while `AuditTrail`, which reads it, does.
      *Owed: `settings` is 6.3's, done separately.*
- [x] **5.9** Interest layer: `topics` · `item_topics` · `domain_affinity` · `outlinks` ·
      `recommendations` · `feed_affinity` · `term_affinity` · `home_ranking`. **All derived — a
      `DELETE` and rebuild from `engagements` must produce the same result**, which is the test.
      ✅ 2026-07-26 — `internal/store/interest.go`. Every table is **replaced**, not upserted: a
      derivation is a whole-picture statement, and an upsert-only pass leaves a source the reader
      abandoned competing forever at whatever score it last had. One transaction each, so a partial
      derivation is never visible. **The rebuild test passes** — `ClearDerived` then re-run produces
      identical snapshots of all four tables, which is what makes `engagements` the only
      irreplaceable table here. A user's topic rename and suppression survive rederivation, matched
      by the cluster's top three terms rather than by an id that is regenerated each pass.
- [x] **5.10** FTS5 triggers for `items_fts`, `notes_fts`, `bookmarks_fts`, and a search repo over them
      ◧ 2026-07-26 — `items_fts` with its three triggers and a `Search` repo over it, verified by `TestSearchFindsASeededItem` and `TestSearchIndexTracksUpdates`. `notes_fts` and `bookmarks_fts` are not built.

      ✅ 2026-07-26 — `notes_fts` and `bookmarks_fts` land in 0010 with their triggers, and
      `internal/store/searchmore.go` adds `SearchNotes` and `SearchBookmarks`. **Separate searches
      rather than one federated list**: the corpora answer different questions and a merged result
      would have to invent a ranking between them, which buries the notes — the rarer and more
      valuable hit. Bookmark search covers the **archived text**, which is most of the value and the
      only search still answering after the origin dies.
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

      ◧ 2026-07-27 — **the lockout, recovery codes, reset tokens and sudo policy**, as
      `internal/store/authnrepo.go` + `internal/authn`, 27 tests. *Still owed: wiring these into
      `AuthService`, refresh-family wiring (the repo methods exist since 5.1), the breached-password
      check, and per-box Argon2id tuning.*

      **The lockout is a table, not a bigger limiter.** §7.1a shipped a fixed-window limiter in
      memory and recorded honestly that a restart clears it. That is fine for a limiter, whose job is
      to blunt a burst, and it is exactly the hole for a lockout, whose job is to survive one: an
      attacker who can provoke a restart — or who simply waits for a deploy — gets an unlimited
      budget against a counter that forgets. The count now derives from `login_attempts`, which 0009
      wrote for this, and a test closes and reopens the database to prove it.

      **Failures are counted since the last SUCCESS, not in a window.** A window means a correct
      password does not clear the count, so someone who mistypes twice and then logs in is still
      most of the way to a lockout for the rest of the window — and an account under slow attack
      never leaves the elevated state even while its owner is using it normally. The *address* count
      keeps its window, because an attacker who guesses one password correctly has not earned a
      clean slate for the others, and the address check runs first because it is the only control
      that sees an attacker rotating usernames.

      **The curve doubles and then stops**, and the cap is the security decision rather than the
      doubling: an uncapped lockout is a denial of service against the account OWNER that any
      stranger can trigger by typing a wrong password, and unlike the attacker the owner cannot move
      to another address. Three free, then 5s doubling to a 15-minute ceiling — **14 guesses in the
      first hour and 4 an hour after**, asserted as numbers so a change to the curve cannot be silent.

      **Single-use is enforced in the UPDATE's own WHERE**, for recovery codes and reset tokens
      alike, not by a read followed by a write. Two requests presenting the same code concurrently is
      precisely the attack on a check-then-act, and it is the one operation here where winning that
      race is a full account takeover — so there is a test that fires eight goroutines at one code
      and asserts exactly one wins. Regenerating codes *replaces* the set, because that action is
      what someone takes when they think the old sheet is compromised and survivors would make it a
      no-op that looked like it worked. Issuing a second reset token invalidates the first, for the
      same reason. Unknown, spent and expired tokens return **one** error, because telling them apart
      tells an attacker holding a guessed token whether it ever existed.

      **Recovery codes use Crockford base32** — no I, L, O or U — because these get written on paper
      and typed back months later by somebody already locked out and already annoyed, and there is
      nobody to file a support request with on a self-hosted box. Normalisation accepts any case,
      any grouping, and maps O/I/L back to the digits they were mistaken for.

      **Sudo mode fails closed on an unknown action**, the same reasoning as authz's map. `SudoFresh`
      refuses a zero timestamp *and* a future one: "no re-authentication recorded" must behave like
      "too long ago" rather than depending on which way a subtraction reads, and a clock that went
      backwards must not mint a permanent sudo session.
- [x] **6.2 `authz`** — capability set, **static per-method map, fails closed on unmapped**. Serves
      both the tunnel and the REST sync API — one model, not two. §7.5
      ✅ 2026-07-26 — `internal/authz`. Static per-method map, **fails closed on unmapped**, which
      is the property the package exists for: unmapped-means-allowed turns forgetting an entry into a
      silent grant of a new RPC to everyone. `Unmapped()` compares the map against the service's real
      method list so a **boot check** can refuse to start, rather than the first notice being a user
      getting a 403 on a shipped feature. The map names **capabilities**, not roles — a handler
      asking "is this an admin" hard-codes policy at every call site. An unknown role grants nothing;
      matching is case-insensitive. *Owed: wiring it into the interceptor, which is where the sync
      API joins the same model.*
- [x] **6.3 `settingsreg`** — typed registry + **system → tenant → user** resolution, returning the
      value *and which layer supplied it* ← 5.8
      ✅ 2026-07-26 — `internal/settingsreg` + `internal/store/settingslayers.go`. **system → tenant
      → user**, first hit wins, returning the value **and which layer supplied it** — "why is this
      off for me" has two very different answers and a screen that cannot tell them apart shows a
      control that silently does nothing. A misspelt key is refused rather than silently reading as
      the default, and a default that violates its own bounds is caught at registration. A setting's
      `Scope` is the lowest layer it may be written at, so "the admin decides" is structural. A
      corrupt stored value is **skipped and reported** and resolution continues to the next layer.
- [x] **6.4 `jobs`** — durable queue (`jobs` table), **per-kind concurrency caps** so pack building
      can't starve rule fan-out, retry, restart-survivable §22.7
      ✅ 2026-07-26 — `internal/store/jobs.go` + `internal/jobs`. In SQLite, so enqueueing and the
      write that caused it are **one transaction** — the property no external broker can give at any
      price. `locked_by`/`locked_at` make it restart-*survivable* rather than merely durable.
      **Writing the per-kind cap test found a real race**: six workers computed saturation at the
      same moment, all saw pack at zero, and all claimed a pack job before any registered — the exact
      failure the cap exists to prevent, produced by the cap's own implementation. A panicking
      handler fails its job, not the process. Dead jobs are kept with their cause. CI gained `-race`
      on the Linux job, because this box is arm64 and cannot run the detector at all.
- [x] **6.5 `events`** — **per-tenant** ring buffers (~1000 each), `since_seq` replay,
      `RESYNC_REQUIRED`, scope-filtered fan-out. *Done when: tenant A's burst cannot evict tenant B.*
      ✅ 2026-07-26 — `internal/events`. Per-tenant rings, `since_seq` replay, `RESYNC_REQUIRED`
      when a sequence has aged out (a partial replay leaves a client believing it is up to date while
      having missed something). Scope-filtered at subscription rather than at read. **The
      concurrency test found a process-level crash**: sending outside the tenant lock races `Close`
      and lands on a closed channel. Sends are non-blocking, so holding the lock costs nothing — the
      comment arguing otherwise was wrong on its own terms. *Done when: tenant A's burst cannot evict
      tenant B* — asserted.
- [x] **6.6 `ingest`** — fetch → parse → identity/dedup → `dupe_key` → revision detect → store →
      **queue** fan-out. Includes the **flood guard** (§15.5) and **outlink harvesting** (2.10).
      ← 4.1, 4.3, 5.3
      ✅ 2026-07-26 — `internal/store/ingest.go` — identity, `dupe_key` dedup, and `RecordFetch` storing the outcome either way, because a poll that fails silently makes a dead feed look like a quiet one and those must look different.

- [x] **6.7 `fanout`** — the per-subscriber job: evaluate rules, write `user_item_state` + `item_tags`,
      emit events, feed ranking signals. **Per subscriber, never once at ingest** — the §13.2 bug —
      and **never inline with the poll**, since it's `O(items × subscribers)`.
      ✅ 2026-07-26 — `internal/fanout` + `internal/store/fanout.go` + migration 0014. Per
      subscriber, never once at ingest, and there is a test for the §13.2 bug: alice mutes, bob still
      sees. One job per (source, batch) rather than per subscriber — the per-subscriber version
      multiplies queue rows by the subscriber count until claim/commit overhead dominates the
      matching. The state upsert **never overwrites what the reader set** (`coalesce`), because the
      queue is at-least-once and a re-run must not un-star something a person starred. Mute is a flag
      with `UnmuteByRule` to reverse it in one statement.
- [x] **6.8 `poll`** — **priority queue by staleness ratio** (not FIFO), backoff, `Retry-After`,
      per-host semaphore, adaptive intervals, **lag metric**, widen-when-behind policy ← 6.4
      ◧ 2026-07-26 — A background poller runs on a fixed interval (`-poll`, default 15m) and per-feed refresh is wired to the UI. The **priority queue by staleness ratio** is not — it is still FIFO over everything due, so a feed that posts weekly is polled as often as one that posts hourly. `sources.fetch_interval_s` is now per-feed adjustable (A33), which is the manual version of the same idea.

      ✅ 2026-07-26 — **the priority queue by staleness ratio is built.** `ORDER BY next_fetch_at`
      is oldest-due-first, which is wrong under load and compounds: at 10:30 a 15-minute feed due at
      10:00 has missed two whole cycles (ratio 2.0) while a 24-hour feed due at 09:00 is barely late
      by its own standards (0.06) — and oldest-due-first polls the second one, forever. A
      never-fetched source sorts first regardless. `PollerLag` adds §22.7's **policy** half: chronic
      lateness widens intervals proportionally, capped at 4×. *The ordering test was verified by
      reverting to the old `ORDER BY` and confirming it fails.*
- [x] **6.9 `signals`** — impression coalescing · the §18.1 kind taxonomy (**`bulk_read` is neutral,
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
      ✅ 2026-07-26 — `internal/derive`. **D18's two stages run in order and the control flow says
      so**: recall (passive signals → affinities, topics) then precision (`home_ranking`, where the
      deliberate acts *scale* what recall thought rather than being more weighted terms). **R17 is
      asserted end to end**: a mark-all-read over twelve items moves not one affinity score.
      `affinityWeight` returns zero for any unrecognised kind, so a signal added later without a
      considered weight contributes nothing rather than something accidental. Half-life is fitted per
      source and returns zero rather than guessing under ten samples.
- [x] **6.10 `recommendjob`** — harvest outlinks and aggregator pass-throughs → candidates → health
      gate → score → `recommendations`. **Rungs 1–3, no LLM.** ← 4.12, 2.10 §18.7
      ✅ 2026-07-26 — `internal/recommendjob` + `internal/store/outlinks.go`. Harvest → validate →
      gate → score → store, rungs 1–3, no LLM. The harvest join is the load-bearing part: outlinks
      are global and engagements are per user, so the intersection is "sites linked from things YOU
      read" — the other way round would recommend one tenant's reading to another. **Validation
      happens before scoring**, because the health gate's inputs *are* the validation and a candidate
      scored without it is scored on evidence nobody checked. Dismissed and subscribed domains are
      filtered **before** the fetch, since §18.7's guardrails are also a politeness budget.
      `RecordOutlinks` replaces rather than appends, or the "linked here 11 times" evidence inflates
      itself.
- [x] **6.11 `llm`** — `Provider` iface; Claude + OpenAI impls; **shared timeout, bounded in-flight,
      circuit breaker**; **egress allowlist enforced and tested** §18.8, §22.8
      ◐ 2026-07-26 — **Partially delivered by 8.4b, and deliberately left open.** `internal/llm`
      exists and is the single path every Smart+ feature takes, on the **OpenAI Responses API**.
      Done: the OpenAI impl, a shared request timeout, the egress host allowlist checked against
      the outgoing request (not the endpoint constant), `store:false`, structured output, and
      truncation surfaced as `ErrTruncated` rather than returned as a short answer.
      **NOT done, and this ticket stays open for them:** no `Provider` interface (there is one
      concrete client), **no circuit breaker**, **no bound on in-flight requests**, no Claude impl
      (§18 wants Claude for feed discovery), and the allowlist is enforced but has no test.
      A breaker matters more than it looks here: a translation is ~10 batched calls, so a
      provider outage during one costs ten failures and ten timeouts rather than one.
      ◧ 2026-07-26 — **transport built by the other lane, protections and egress boundary by this
      one.** `internal/llm` has the Responses client, host allowlist and budget meter;
      `breaker.go` adds §22.8's circuit breaker (opens at 5 consecutive failures, **half-opens to
      exactly one probe** — letting them all through hands a still-broken provider the full load
      every two minutes) and the concurrency bound, and `egress.go` adds §18.8's allowlist **as
      types rather than a filter**, because a filter fails open. `AuditEgress` is the test §18.8 asks
      for by name. *Owed: the `Provider` interface with a second implementation — D10 sequences
      Claude to M25–26, so one provider is correct for now.*
- [x] **6.12 `preserve`** — tiered archival (§10.6): eager at ingest for high-affinity and Top-slot
      items · on interaction · **the distress sweep when a source crosses into `failing`** · lifecycle
      transitions `ok → failing → gone` · link-rot checks for engaged items only · eviction that
      **cannot** drop an archive whose origin is dead. *Done when: killing a fixture feed mid-test
      leaves its items still readable.*
      ✅ 2026-07-26 — `internal/preserve` + `internal/store/archive.go`. Tiered archival with the
      §10.6 distress sweep, lifecycle marking, and eviction. **The ticket's bar is met as a test**:
      the fixture site is killed mid-test and every item is still readable in full. **Eviction can
      never drop an archive whose origin is dead** — in the `WHERE` clause, and re-checked inside the
      `DELETE`, because a sweep may discover the origin is gone in between and that archive is then
      the only copy in existence. A sweep of a dead site tries every item rather than stopping at the
      first: most will fail and the few that succeed are the point. *Owed: link-rot checks for
      engaged items, and the `ok → failing → gone` source lifecycle transitions.*
- [x] **6.13 `degrade`** — disk watermark ladder (20/10/5/2%), shedding audio and packs first and
      keeping read state alive longest §22.6

      ✅ 2026-07-26 — `internal/degrade`. A **pure function of free space**, so every rung is
      testable in a millisecond rather than by filling a disk — a ladder that can only be exercised
      for real is one nobody has seen run. The order is the design: at 5% free polling stops while
      reading, marking read and notes keep working, because new articles are re-fetchable from the
      publisher and a note is not. The outbox drain is never refused at any rung: those writes
      already happened on a device. A test asserts the ladder is **monotonic** — nothing becomes
      permitted again as space runs out — which is the typo no boundary test would catch.
- [x] **6.14 `assetproxy`** — the tier-1a service (§10.1a). `(signed url) → bytes`, where the
      signature is minted **only for a URL found in stored HTML the caller could read**, never taken
      from the caller · fetch through the
      2.7 guard · cap to image content-types and a few MB · cache to disk keyed by URL hash, beside the
      database like the speech cache so a data-directory backup carries it · negative-cache failures so
      a missing image costs one request a month, not one per render. ← 2.7, 2.9, 4.13, 5.3 §10.1a
      *Done when: an article renders its images with the **origin blocked at the client** and reachable
      only from the server — which is the whole feature, and is untestable if you only ever run both
      halves on one machine.*
      **Ships with M9 and is pullable to now.** It repairs an article that renders wrong today, and it
      is what makes an archive survive its publisher instead of keeping a manifest of dead image URLs.
      ⚠ **Do not let this re-enable newsletter images.** `sanitize.Newsletter` drops them deliberately
      — proxying a tracking pixel still confirms the open, only from a different IP. The rewrite hook
      is per-policy, not global.
      ✅ 2026-07-26 — `internal/assetproxy` + `internal/app/asset.go` + the `GetItem` seam.
      **The endpoint is unauthenticated and that is the same decision as `/speech`'s, not its
      opposite:** an `<img src>` cannot send an `Authorization` header, so the choice was never
      header-vs-query, it was capability-or-nothing. The URL *is* the capability — HMAC over
      `("asset", url, exp)` with the expiry **inside** the signature, 12h TTL, `secret.Sign`, key
      persisted at `<datadir>/proxy.key` 0600 so URLs on an open page survive a restart.
      *Deviation from §10.1b as written: the capability signs the URL rather than an item id + asset
      index.* The index cost a DB read and a full HTML parse **per image** — forty per article — and
      is positional, so a publisher edit shifts it and serves the wrong picture. It never enforced
      anything the mint gate does not: a caller who wants an arbitrary URL in item HTML can subscribe
      to a feed they control. Reasoning recorded in plan.md §10.1b.
      Content-type is allowlisted to images, and a generic `application/octet-stream` is **sniffed
      rather than passed through** — we send `nosniff`, so forwarding the origin's vagueness would
      turn a working JPEG into a blank box. Size cap enforced while reading, never from
      `Content-Length`, which is a claim by the server we are defending against. Failures are
      negative-cached for 6h: a dead image is re-read constantly, and without it every read is another
      request to a publisher who may already be rate-limiting us. Cache is one file per asset (header
      line + bytes, one atomic rename — two files would have a window where metadata and body
      disagree), fanned out by the first two hex characters.
      Default **on**, per-user opt-out via the `proxy.images` pref, instance switch `-proxy-images` /
      `ARTICLEFLUX_PROXY_IMAGES`. Rewriting happens at serve time in `GetItem` only: list responses
      carry no article body, and minting forty capabilities for HTML nobody has scrolled to would
      expire before use.
      *Owed:* the §22.6 watermark ladder does not yet shed this cache (6.13's job), and there is no
      per-user rate limit on the endpoint (7.3d's).
      ⚠ **Noticed while wiring:** `netguard.Get` always applies the **strict** `CheckURL`, so a client
      built with `AllowPrivate: true` is still refused a LAN address through that helper. `feed` and
      `extract` each work around it with the same four-line branch; this is the third copy, and the
      point at which the branch should move into `netguard` rather than being written a fourth time.

- [x] **6.15 `pageproxy`** — the tier-2 service (§10.1b). item → guarded fetch (4.3's client) →
      `charsetdec` → 4.13 rewrite pointing at 6.14 → a new **`sanitize.Snapshot` policy** (2.9: no
      script, no iframe, no form, no event attributes; keeps layout CSS) → disk cache → serve. Carries
      the **escalation hook** 6.16 plugs into: if the fetched body is empty or implausibly short for
      the item's word count, the page needs a browser.
      *Done when: a saved fixture page renders with **zero requests leaving our origin** — asserted
      against a request log, not by eye, because "it looked right" is how a `url()` in a background
      shorthand survives for a year.*
      ✅ 2026-07-26 — `internal/pageproxy` + `/p` + the client's two entry points.
      **Rewrite runs BEFORE sanitize, and that ordering is the whole correctness of the tier.**
      `<base href>` is not on any sanitize allowlist, so sanitizing first deletes it before anything
      can honour it, and every relative URL on the page silently resolves against the wrong host.
      Pinned by `TestBaseHrefIsHonouredDespiteSanitizeDroppingIt`.
      **The cache holds the RAW page, not the finished one** (30-minute TTL). The finished HTML
      carries capabilities with expiries in them; caching that would serve a page whose every image
      is dead. So the network is cached and the two parses are redone per view with fresh mints — a
      page view is a deliberate act, not a hot path.
      *Deviation: `sanitize.Snapshot` does NOT use the GWC engine.* GWC drops `<style>`, `<link>` and
      every `style` attribute **above** the policy layer — right for prose, fatal here, and no policy
      table can override it. Snapshot therefore walks the tree itself in
      `internal/sanitize/snapshot.go`, allowlisting what a *document* needs and dropping what can act.
      It carries the same 48-vector corpus as every other policy, which is what keeps the exception
      honest, plus its own tests for the useful half — a snapshot sanitizer that passes every security
      test and strips the stylesheet has failed at its job.

- [ ] **6.15a Stylesheets do not survive the round trip yet.** `<link rel=stylesheet>` is rewritten to
      `/asset?u=…`, and `/asset` allowlists **images**, so it answers 415 and the page renders with
      inline `<style>` only. Most sites keep their CSS in external files, so most sites currently come
      back mostly unstyled. Fixing it means teaching the asset endpoint a second content kind: fetch
      `text/css`, run `rewrite.CSS` over it so the images and fonts *it* references are proxied too
      (recursively — one level is not enough), and serve it as `text/css`. `rewrite.CSS` already exists
      and is tested; this is the endpoint half. **Until then tier 2 is legible rather than faithful.**

- [ ] **6.16 `render`** — the headless browser pool (§10.1c). `chromedp` attached to the installed
      Chromium · a **disposable profile with no access to the data directory** · exactly one render at
      a time through 6.4's queue · hard timeout · wait for network-idle, then one scroll-to-bottom pass
      so lazy images resolve · returns `outerHTML` **and** a full-page screenshot from the same
      session, because the screenshot is the fallback artifact when the DOM comes out unusable.
      ← D19, 6.4, 6.15 §10.1c
      *Done when: a JS-only fixture (an empty `#root` plus a script that fills it) comes back filled,
      and killing the browser mid-render **fails the job rather than hanging it** — a renderer that
      wedges is worse than one that refuses.*
      **Never a background sweep.** On demand, cached forever after. A preservation pass that shells
      out to a browser per item would cook the box and read as an attack from the publisher's side.
      ◧ 2026-07-27 — **The STREAMING half shipped; the snapshot half did not.** `internal/render`
      drives Chromium over CDP (`chromedp`), one session at a time, disposable incognito profile with
      no access to the data directory, browser started lazily on first use so an instance nobody
      streams from never pays 300 MB for the option. `FindBrowser` auto-detects Edge→Chrome→Chromium
      on Windows and google-chrome→chromium→edge on Linux, so the same config works on the laptop and
      the Ubuntu box; `-browser-path` overrides and does **not** fall back if the override is wrong.
      *Owed for tier 2r:* `outerHTML` after network-idle, the scroll-to-bottom pass for lazy images,
      the escalate-on-empty rule, and compression to a byte budget. The pool that all of that needs
      now exists.
      ⚠ **The one place this is weaker than everything else in the codebase**, recorded in
      `render.Stream`: `CheckURL` runs before navigation and stops the obvious attempt, but the
      browser dials for itself, so netguard's socket-level `Control` never sees it. A page that
      redirects to a private address after we hand it over is reachable **by the browser**. The
      mitigations are that nothing it fetches comes back as data (only as pixels) and that it holds
      no credentials — and that is why this rung is opt-in at the instance level while 1a and 2 are
      not.

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

- [x] **7.3b `internal/page`** — opaque keyset cursors, `spec_hash`-bound so a cursor from a different
      `ViewSpec` is `InvalidArgument` rather than silently-wrong results §20.7
      ◧ 2026-07-26 — Keyset cursors are base64url and exact (published, id) tuples, in `internal/store`. They are **not** `spec_hash`-bound, so a cursor from one scope replayed against another returns plausible wrong rows rather than `InvalidArgument`.

      ✅ 2026-07-26 — cursors now carry a 12-character hash of the query's filters, and a mismatch
      is **`InvalidArgument`, never an empty page**: an empty page means "you have reached the end",
      and a client reading a stale cursor that way stops paging and shows a truncated list with no
      error anywhere. The `Scope` is deliberately **not** in the hash — cross-tenant protection is
      the `WHERE` clause's job, and a cursor is not a capability.
- [x] **7.3c `internal/idem`** — `(user_id, key) → response`, 24h TTL, verbatim replay. **Required for
      every outbox-replayed mutation** — a partial drain that reconnects mid-flight must not
      double-apply §12.4, §20.7
      ◧ 2026-07-26 — `SetItemState` accepts an `idempotency_key` and every caller sends a deterministic one, but nothing stores or replays it. Harmless today because there is no offline outbox to drain; required before there is (§12.4).

      ✅ 2026-07-26 — `internal/store/idem.go`. `(user, key)` → the response, replayed **verbatim**
      for 24h. The stored bytes rather than a recomputed answer, because a client receiving a
      *different* response to the same request cannot tell a replay from a second effect. A key
      reused for a genuinely different request (method or body) is a **conflict** — returning the
      first one's answer for the second one's write drops the write *and* reports success. *Owed:
      the interceptor that calls it; the storage and the semantics are here.*
- [x] **7.3d Rate limiters** — the §20.7 table, at the interceptor, per-user and per-IP
      ◧ 2026-07-26 (night) — **login only**, in `grpcsrv/auth.go`: 10/minute per username and per client address, in memory. Nothing else is limited, and the per-IP half is largely fictional — every RPC arrives over one WebSocket, so behind a proxy the peer address is `127.0.0.1` for every user on the instance. **A real per-IP limit requires the forwarded address to be threaded through the tunnel handshake**, which is this item's first piece of work.
      ✅ 2026-07-26 — `internal/ratelimit`. §20.7's table as named rules **with a test asserting the
      numbers against the document**, because a limit that has drifted from the spec is invisible in
      a diff of one integer. Token buckets, not fixed windows: a fixed window lets a client send 60
      at 11:59:59 and 60 more at 12:00:00, and a polling client on a round-minute schedule finds that
      on the first day. `retry_after` is **rounded up** — rounding down guarantees one wasted request
      per refusal at the worst moment. Two deliberate fail-open choices: a misconfigured rule permits
      everything, and a full key table evicts rather than denying. *Owed: wiring it into the
      interceptor.*
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
- [ ] **7.12 Proxy endpoints** — `GET /asset` (6.14) and `GET /p/…` (6.15), both on the **separate
      proxy hostname** of D20, never the app's. Each takes a **signed short-TTL capability** over
      `(scope, item, asset, exp)` — `secret.Sign` already exists — minted by an authenticated RPC and
      verified here. **No free-text URL parameter anywhere.** Responses carry a
      `default-src 'none'`-class CSP, `nosniff`, `Cache-Control: private`, and no `Authorization` is
      ever read from the query string (§10.1b, the `/speech` rule). ← D20, 6.14, 6.15 §21
      *Done when: a capability for item A cannot fetch item B's assets, an expired one is refused, and
      a request for the proxy path arriving on the **app** hostname is refused outright rather than
      served — the origin split is only a control if it is enforced on both sides.*
      ◧ 2026-07-26 — **`/asset` shipped; `/p/…` has not, and D20 is why.** The image half needs no
      origin split: an image served with `nosniff` and `default-src 'none'; sandbox` is not a document
      and cannot reach the session. HTML is, and can. So `Config.ProxyOrigin` /
      `ARTICLEFLUX_PROXY_ORIGIN` exists **now**, unused by the asset path beyond moving its URLs, so
      that the page proxy inherits a configured split rather than needing one retrofitted after
      capabilities are minted and cached. Covered: signature required, tampered target refused,
      extended expiry refused (the expiry is inside the signature), expired → 410, blocked address
      refused even with a valid signature, 501 when unconfigured, conditional GET → 304, HEAD carries
      no body, key survives a restart.
      *Owed:* the `/p/…` half, per-user rate limits (7.3d), and the both-sides hostname enforcement,
      which cannot be written until D20 is answered.
      ✅ 2026-07-26 — **`/p` shipped too, and D20 turned out not to block it.**
      `Content-Security-Policy: sandbox` puts the response in an **opaque origin**: the document
      cannot read this application's localStorage or cookies even while served from the same host.
      That is a browser-enforced boundary rather than a weaker stand-in for one, and it is paired with
      `script-src 'none'`, `form-action 'none'`, `frame-ancestors 'self'` and `allow-popups` (so a
      link that opens in a new tab still works, sandboxed). The separate hostname is still worth
      having — it moves the guarantee from a header we must keep right forever to a boundary nobody
      can forget — so `ProxyOrigin` remains wired and D20 remains open, now as hardening rather than
      as a blocker.
      Capabilities are prefixed (`"asset\n…"` vs `"page\n…"`), so an image URL cannot be replayed to
      fetch a document; `TestAssetCapabilityCannotOpenAPage` pins it. The origin's own
      `X-Frame-Options` is **not** forwarded, which is what lets the reading pane embed a site that
      refuses to be embedded (§10.1's blank-box problem).

- [ ] **7.13 `StreamPage`** — tiers 3–4 (§10.1d). A **bidi** RPC, which the tunnel already carries:
      screencast frames down, input events up. Server side: `Page.startScreencast` → ack every frame →
      diff consecutive frames into **64×64 tiles** and send only what changed → `Input.dispatchMouseEvent`
      / `dispatchKeyEvent` / `Input.synthesizeScrollGesture` on the way back. **One session per user,
      hard idle timeout, and the browser dies with the stream.** ← 6.16, 7.1 §10.1d
      *Done when: a static page costs ~one frame and then silence (the damage-driven property is the
      entire performance argument — if it is emitting at a fixed rate, it is misconfigured), and a
      dropped connection leaves no orphaned browser behind.*
      **Flag-gated, off by default, per R22.**
      ◧ 2026-07-27 — **Shipped as MJPEG over plain HTTP, not as a bidi RPC, and the swap was the
      right call.** `multipart/x-mixed-replace` in an `<img>` is decoded by the browser natively,
      frame by frame — no proto change, no wasm streaming code, no canvas compositor, no tile format
      to design. `/stream` mints a third capability (prefix `"stream\n"`, so it cannot be spent on
      `/asset` or `/p`, pinned by `TestCapabilitiesDoNotCrossRungs`).
      **The connection IS the session.** No session table, no ids, no reaper: the browser tab lives
      exactly as long as the HTTP response, so switching away or closing the tab cancels the request
      context and the tab dies with it. Frames are dropped rather than queued when the reader's link
      cannot keep up — these are whole images, so the next one supersedes the last and queueing would
      only add latency to a stream already behind.
      *Owed, and both are additive:* the input channel (this is view-only, so tier 4 is still not
      built) and the 64×64 tile diff — every frame is currently a complete JPEG.
      Proven end to end by `TestStreamServesMultipartFrames`: signed URL → handler → real browser →
      parsed multipart frames with JPEG headers on the wire.

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
- [x] **8.4a `client/i18n`** — every UI string through GWC's `i18n` from the **first** component, even
      though only English ships. Retrofitting extraction across ~50 pages and ~90 settings is
      miserable and always gets deferred forever. Locale date/number formatting applies immediately.
      §22.16
      ⏸ 2026-07-26 — **Deferred, deliberately.** Every string was still inline English; the UI was
      changing shape weekly and retrofitting once it settled looked cheaper than re-extracting after
      every redesign.
      ✅ 2026-07-26 — **Done.** `client/i18n` + fifteen `en_*.go` catalogs, ~584 call sites,
      **all 12 files in `client/view` at zero hardcoded copy**, enforced by a fifth structural
      guard (`internal/tools/guards/i18n.go`). It uses the framework API — `i18n.Provider`,
      `UseI18n`, `Runtime.T(ns, key)`, `Runtime.NS`, and `i18n.UseLocale` for the reactive,
      persisted locale. **Switching language re-renders; it does not reload.**
      A first pass hand-rolled a package-level `T` and justified it as "hooks are positional and
      we translate inside loops". That reason was WRONG — you call the hook once at the top and
      use the Runtime in the loop. Corrected, and §22.16a now records the real constraint and the
      four findings that came out of doing it properly:
      - **The Provider must live in a rarely-rendering component, and lives in `Root`.**
        `Runtime` is a struct of func fields → not `Comparable` → `fastEqual` falls to
        `reflect.DeepEqual` → two non-nil funcs are never equal → the context value reads as
        "changed" on every render of whoever mounts it, and `markSubtreeNeedsUpdate` is checked
        BEFORE props comparison. Mounting it in `Reader` would mark the 151-row rail and the
        virtualised list dirty on every keystroke.
      - **A Runtime must never go on a props struct** — same reason; `railProps` is compared by
        value to stop 151 rows re-rendering. It is threaded as an explicit first parameter
        through the ~70 plain helpers instead (only 4 functions in the package can hold a hook).
      - **`UseLocale` clamps to its supported set at boot, before any Import**, so the language
        list lives in `client/i18n` — a set derived from the bundle would be `["en"]` on every
        cold start and would reset a French reader to English before the fetch began.
      - **Anything built before the Provider wraps it cannot see it** — `bootSplash` had to
        become a mounted component rather than an inline call.
      Verified rather than asserted: `provider_test.go` renders the real Provider/`UseI18n` path
      through `ui.RenderToString` natively and asserts English resolves, plurals select by locale,
      an imported catalog renders, missing keys fall back to English rather than raw identifiers,
      and `Import` refuses to overwrite English.
      Cost: **+221 KB gzipped**, entirely `x/text` via `NormalizeLocale`/`FormatNumber`. The fix
      belongs in GWC and would benefit every GWC app.

      ✅ 2026-07-26 — **Full-surface audit, after the guard reported zero.** The guard only sees
      `client/view`, so "zero" was never "everything". Four gaps found across every surface a
      reader can read; all four closed:
      - **`relTime`** — the most-rendered string in the app, on every list row and article
        eyebrow — was `t.Format("2 Jan")`. **Go's layouts are not locale-aware and never will
        be**, so month names were English in every language. Now `month.1`–`.12` plus a
        `time.dayMonth` pattern so a locale that writes the month first can reorder it; the unit
        abbreviations reuse the `unit.*` keys the settings screen already uses.
      - **`internal/tagglyph`'s 50 glyph names + 7 group headings** — the `aria-label` and tooltip
        on every cell of a grid of *unlabelled symbols*, so they matter most to exactly the
        readers who cannot tell ◆ from ◈ at 13px. Keyed by the character (the stable identity),
        with fallback to `tagglyph`'s own Name.
      - **`web/index.html`'s splash** — five strings shown BEFORE the wasm module exists, so they
        cannot read a Go catalog when needed. Mirrored to localStorage on every language change
        (`mirrorBootCopy`), exactly as this file already mirrors theme colours to `af.boot`. The
        English stays in the markup as the fallback for a first-ever load, blocked storage, and
        no-JS.
      - **gRPC status messages** — every refusal the reader saw was English regardless of locale,
        and the server **cannot** fix that itself: the language is a per-device localStorage value
        it never sees. It now sends a key + args in an `articleflux.v1.ErrorDetail` alongside the
        English, and `view.serverText` resolves it. 18 messages converted. The English stays on
        the status on purpose — it is what the two consumers with no catalog get, the GReader sync
        API (§20.7) and curl.
      Two new ratchets for the wire contract, because the key crosses as a string and nothing in
      the type system connects the halves: `TestEveryServerErrorKeyExists` (a server key that is
      unregistered or outside the `srv` namespace fails) and
      `TestServerErrorKeysMatchTheirEnglishFallback` (the wire's English and the catalog's must
      not drift — each half is correct alone, so the divergence is otherwise invisible).
      Untranslated by design, and stated in §22.16a: **feed content** (that is §10.5's job) and
      gRPC's own socket text inside an `{err}` interpolation.

      **Adding copy from here on: plan.md §22.16b is the rule set** — the four call shapes, the
      three things that will bite (Runtime on a props struct, Provider outside Root, branching on
      translated text), and what each test fails on. CONTRIBUTING.md carries the short version.

- [x] **8.4b `internal/llm` + `internal/smart` + the Smart+ settings surface** — one OpenAI client for
      every Smart+ feature, a persisted encrypted API key, and realtime UI translation. §10.5a, §22.16a
      ✅ 2026-07-26 — Not in the original tier list; added because §22.16's catalog made it a day of work
      rather than a milestone. What shipped:
      - **`internal/llm`** — the ONLY way this app talks to a model, and it uses the **Responses API**
        (`/v1/responses`) exclusively. Strict `json_schema` structured output; `store:false` so the
        reader's text is not retained for thirty days by default; host allowlist checked against the
        outgoing request; `ErrTruncated` **refuses partial answers** rather than returning a catalog
        missing its tail.
      - **`internal/store/settings.go`** — the `scope='system'` layer of §6.3's registry. AES-GCM at
        rest under `secrets.key` beside the database (`ARTICLEFLUX_SECRET_KEY` overrides). Methods are
        named `SystemValue`/`SetSystemSecret`/… rather than `Get`/`Set` **on purpose**: the guard's
        `unscopedByDesign` list is keyed by bare method name, so exempting a `Get` here would exempt
        `Get` on every future tenant-scoped repository.
      - **`internal/smart`** — reads the English catalog straight out of `client/i18n` (no build tag,
        so the server can import it), translates in batches of 60, caches per locale **keyed by a hash
        of the English** so a build that edits a string re-translates and one that does not is free.
      - **`SmartService`** (`proto/articleflux/v1/smart.proto`) — owner-only, checked per method. Never
        returns the key; `key_hint` is the last four characters.
      - **`client/view/smartsettings.go`** — key, model, spend, and the language picker on one tab,
        because the picker spends the key.
      - **`internal/tts` now reads the same key function**, so one credential drives every Smart+
        feature instead of the voice reading the environment and translation reading the setting.

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

- [ ] **8.20 `PageView` + the render-mode switcher** — the `RenderModeSwitcher` (C3) stops being a
      placeholder and gains its two new positions: **Page** (tier 2 / 2r) and **Live** (tier 3). Page
      renders 7.12's URL in an iframe with `sandbox` carrying neither `allow-scripts` nor
      `allow-same-origin`, sized to the reading column, with the §10.1 fallback chain made visible —
      *didn't load → try Reader* — because a blank box is the failure mode this whole area is prone to
      and the user must always have somewhere to go. ← 7.12, M13's switcher §10.1b
      *Done when: every mode degrades to the one below it on failure, and no mode can leave the pane
      empty with no explanation.*
      ◧ 2026-07-26 — **The two proxy entry points shipped; the switcher did not.** The article's chip
      row gains **View page** (a sandboxed iframe in the reading column) and **Full width** (the same
      URL in a new tab, where the browser gives it the whole window — "fullscreen" without building a
      fullscreen mode). Both are absent rather than disabled when the instance has the page proxy off:
      `Item.proxy_url` is empty and there is nothing to offer, and a disabled control would advertise
      a feature the server refuses.
      **Full width is an `<a target="_blank">`, not a click handler** — middle-click, ctrl-click and
      "open in new window" are three gestures the reader already knows, and routing a new tab through
      JavaScript breaks all three.
      The frame is `sandbox="allow-popups"` with no `allow-scripts` and no `allow-same-origin`,
      matching the response header exactly — belt at the embedding site, braces in the CSP.
      *Owed:* the mode switcher proper (auto/feed/reader/page/live), per-feed and global defaults, and
      the keyboard binding. Right now this is two buttons, not a ladder.
      ⚠ **Correction, same day: `-proxy-pages` now defaults ON, and the first default was a mistake.**
      The argument for off was "proxying a page fetches whole documents from arbitrary hosts". It does
      not — a page capability is only ever minted for an item's OWN url, the same URL the *Open
      original* button beside it already sends the reader's browser to. The marginal exposure is which
      machine makes the request, not what gets requested, which is a far smaller step than the comment
      claimed.
      What defaulting it off actually bought was an invisible feature: the control is **absent** rather
      than disabled when the proxy is off, so a missing flag and a missing feature look identical from
      the reading pane. `app.Open` now logs which of the two you are on at boot — `proxy enabled
      images=true pages=false note="the article's View page control will not appear"` — because a
      feature whose absence has no explanation is one that gets reported as broken. The inconsistent
      pair (`-proxy-pages` without `-proxy-images`) warns instead of failing in silence.

- [ ] **8.21 `RemotePage`** — the tier-3 client (§10.1d). Holds the tile grid, composites 7.13's
      changed 64×64 blocks onto a canvas, forwards pointer/key/scroll events back up the stream, and
      shows connection and cost state honestly: this mode holds a live browser open on the server.
      **A one-time explainer before first use** covering what it does and R22's traffic signature —
      not a legal notice, a sentence — because switching this on is a different act from picking a
      font. ← 7.13, 8.20
      *Done when: input round-trips, a lost connection tears the session down visibly rather than
      freezing on the last frame, and leaving the view stops the stream.*
      **Instance-gated, and consent-gated at first use — not off by default.** §10.1-R makes this
      **rung 2**, the automatic answer to a blocked origin, so "off unless someone goes looking"
      would mean the ladder never engages. The operator switch stays; the per-reader gate moves to the
      moment of use (R22 is a traffic signature, and a settings toggle nobody read is not consent).
      A reader who has never been asked falls to rung 3 instead.

- [ ] **8.22 The ladder controller** — §10.1-R. One place that decides which rung the article pane is
      on and why, in this order: **real page → (blocked) frame stream → (bandwidth) compressed
      rendered HTML → reader text**. Every step down is a *named constraint being hit*, never a
      preference, and the reason is displayed — a reader who has silently been dropped two rungs
      thinks the site is broken.
      **Manual in v1 (D21).** The switcher is the controller; automatic escalation waits on a probe
      that can actually tell "blocked" from "offline" from "still loading". Falling *down* the ladder
      never needs permission; reaching rung 2 does.
      ← 8.20, 8.21, D21 §10.1-R
      *Done when: each rung can be entered and left without a reload, every automatic transition names
      its constraint on screen, and the pane can never end up empty with no explanation.*

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
      ↻ 2026-07-26 — **Key source changed by 8.4b.** `internal/tts` no longer reads `OPENAI_API_KEY`
      at construction; it takes a `KeyFunc` and reads through it on every call, and `internal/app`
      hands it the SAME function `internal/llm` gets. One credential now drives every Smart+
      feature, changeable from Settings without a restart. The alternative was an instance where
      the voice worked and translation did not because one read the environment and the other read
      the setting — a difference with no visible shape from the settings screen.

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

**Appearance and identity, client-side**

- [x] **8b.31 The theming engine, and the Appearance tab that drives it** — A39, §20.16. Five themes,
      seven accents (a **separate light set**, taken down to where a hue can carry white — the same
      tone problem as `--ink`), three reading sizes, three durations. `applyAppearance` writes token
      values onto `documentElement.style`, which outranks the `:root` block the sheet emitted, so
      **nothing re-renders when the theme changes**. The four prefs are server-side (A30): the look of
      the reader travels with the account, not the browser. Every stored value keeps *unset*
      distinguishable from *set to the default*, which is what lets the screen offer a way back to
      following the system.
- [x] **8b.33 The client half of authentication** — §7.1b. `Root` mounts either `Login` or `Reader`,
      never both, so an unauthenticated page never constructs the reader. Token in namespaced,
      versioned local storage; a **client interceptor** attaches `authorization: Bearer` to every call
      and routes `Unauthenticated` back to `Root` — **except on `Login` itself**, which answers
      `Unauthenticated` for a wrong password and must not read as an expired session. `device_id`
      survives sign-out on purpose: it identifies the browser, not the session.

**The event loop, and the cost of an interaction**

- [x] **8b.35 No blocking call on the JS stack.** A `js.FuncOf` callback that blocks holds the
      JavaScript event loop, and the tunnel's WebSocket delivers its reply *on that loop* — so an RPC
      awaited from a handler cannot succeed, only time out, with the tab frozen for the whole
      deadline. The signals outbox was flushing inline from `OnPageHide` and from `Stop` (an effect
      cleanup, and effects run on the frame loop). Both now hand the ship to a goroutine; `Flush`
      grew an in-flight guard so a wedged tunnel cannot stack blocked batches. **The tell is a Chrome
      `[Violation] '<event>' handler took N000ms` where N000 equals an RPC timeout constant** — here
      `engagementTimeout`, to the millisecond.
- [x] **8b.36 The delegated dispatcher owns its click's payload.** `forItem` / `forValue` are refs
      every click shares, written synchronously by two sibling listeners and — until now — read a
      frame later from inside `ui.PostAsync`. Two clicks in one frame made the first body read the
      second's id and clear the ref, after which the second body read `""` and its action silently did
      nothing. Captured on the click's own stack now. *A dropped action with no error is the worst
      failure shape there is: it is indistinguishable from a control that was never wired up.*
- [x] **8b.37 Nothing derived rebuilt per frame.** The article chips' `source → tags` map was walked,
      allocated and sorted inside the render; `internal/tagglyph` rescanned its fifty-entry catalogue
      per group per repaint. Both are now built where their inputs change — the chips behind
      `setTagData`, the single write path for tag state, so the cache cannot go stale by omission.
      Same rule `hostsRef` already followed, and the same trap it already documented.
- [x] **8b.38 Taking a tag off is instant; putting one on says it is not.** Removal applies the
      server's own rules locally and rolls back on failure. Adding cannot — the id is the server's to
      assign — so the chip appears immediately in a pending state with the × withheld, rather than
      pretending or making the reader wait on two round trips with no feedback.
- [x] **8b.39 The three guards that make A39 a decision** — §20.16.2. Native, in `client/design`, and
      asserting against the **emitted** sheet (`css.Reset()` → `Sheet()` → `css.Harvest()`) rather than
      the source that produced it. No dangling `var()`; no token the sheet reads that `Theme.Vars()`
      cannot reach; no literal colour outside a `Theme` (`:root` and the reader-mode iframe's
      deliberately-white base stripped **by name**, so a stray hex elsewhere still fails); no duration
      that is not `calc(var(--mo) * …)`, with the four amplitude-gated loops named by exact duration so
      a fifth fails rather than quietly joining the exemption. Plus a **readability floor**: every
      theme's text tokens against all three grounds they land on — the page, a hovered row, and the
      selected row a reader sits on for as long as they are reading it — at 4.5:1. It found four
      failures on the way in. Three were mine (Daylight's `--mute`, `--pos` and `--neg` at 3.9–4.2:1
      against the selected row) and are fixed; the fourth is **Fanciful's own `--mute`, 4.42:1 hovered
      and 3.94:1 selected**, transcribed verbatim from the mockup — recorded with its measured ratios
      and ratcheted rather than nudged, because that one is a decision about the mockup. See 8b.44.
- [x] **8b.40 The selection travels.** The item list's highlight is one cursor on the scroll container,
      not a background that lights on one row and goes out on another — two events where the reader
      made one gesture. It is a pseudo-element of the scroller, so it is laid out in the container's
      **content space** and its y is an index times the row height: no scroll arithmetic, and no
      recompute as the list moves under it. `ScrollIntoView` goes `behavior: smooth` on the same bit,
      so the list keeps step with the article rather than jumping to it. Two bugs caught before they
      shipped: the state attribute was briefly written with `onOff()`, which returns **localised**
      words and would have silently stopped matching `[data-cursor='false']` for any non-English
      reader; and the cursor was opt-*out*, which put a highlight on the first skeleton row of every
      load.
- [x] **8b.41 Spawning animates the arrival of data, not of an element on screen.** The list is
      virtualised, so a mount animation fires on every row that scrolls past — a slot machine at any
      real speed. `setItems` diffs each incoming list against the one it replaces and only ids that
      were absent carry `data-fresh`, cleared after 900ms. The property falls out with no special
      cases: a scope change makes the first page fresh, *load more* makes only the appended page
      fresh, and marking read rebuilds from items that are all already present — so **the list does
      not move under the reader's hand on `j`**, which is the case that matters most because it
      happens a thousand times a day. The stagger counts from the first fresh row the reader can
      *see*: counting from the top of the page put every visible row past the cap on a *load more*,
      which is a quarter-second of nothing and then the whole screen at once. Measured before
      `[9,9,9,…]`, after `[0,0,…,1,2,3]`.
- [x] **8b.42 The waits say they are still working.** An indeterminate hairline in the accent on the
      three moments that were bare text — more items, the next article, a bulk operation. Not a
      spinner: the note's save mark is already one and means something else, and reusing the shape
      would make both mean less. `--mo-off` (`calc(1 - var(--mo))`) widens the travelling band to the
      whole rule when motion is off, because a short band frozen at one end reads as a determinate
      progress bar stuck at 0% — a worse lie than a steady mark.
- [x] **8b.43 The splash** — §20.20. Real byte progress on a six-megabyte module, streamed through a
      counter that still preserves streaming compilation; `content-length` treated as a hint, because
      the server prefers a precompressed `.gz` while `res.body` yields decoded bytes, so the bar drops
      the percentage and roams the moment the count passes it. The theme is mirrored to `localStorage`
      by `applyAppearance` purely so this one frame can be right — otherwise a Daylight reader gets a
      dark flash on a bright screen on every load, which is the one flash a splash exists to prevent.
      The progress fill is the seven source hues, in order, because the bar should be made of the thing
      it is loading. `client/design/bootpalette_test.go` pins the duplicated palette, the hue order,
      the reduced-motion query and the `af.boot` handshake.
- [x] **8b.48 The wash is a theme token, because looking at all five found what arithmetic could not.**
      The article's radial gradient carried a fixed 24% of the source hue. That is right on plum, where
      the ground has colour of its own to dilute the mix — and over a NEUTRAL ground there is nothing
      to dilute it with, so the same declaration reads as light falling in on Fanciful and as a green
      panel on Ink. Worst on **Contrast**, whose entire claim is *maximum legibility, black ground,
      white type, hard edges*, and which was carrying the heaviest decoration of the five. `--wash` is
      now per-theme (24 · 19 · 13 · 11 · 6%), which also replaces the light-tone override with one
      mechanism instead of two — the problem was never light-versus-dark. **Every theme's contrast
      passed the whole time**: this is the class of defect a ratio cannot see, and it survived four
      rounds of measurement because nobody had opened the theme.
- [x] **8b.45 Focus mode** — §20.21. `w`, or the control pinned top-right of the article. The columns
      **close** rather than vanish: they are grid tracks, so it is four widths animating to zero, which
      interpolates as long as the track count holds — hence three rules, one per breakpoint, because
      the layout already redefines the columns at 1220px and 900px and a five-track value against a
      three-track layout snaps. The article recentres via **padding** (it interpolates from the 60px it
      already has; a `max-width` would have to animate from `none`), and the source wash halves by
      *opacity*, because gradient stops do not interpolate and changing the mix would snap it while
      everything around it slid. The control is the one piece of bespoke iconography in the
      application, and it earns that because its meaning is a geometry no character states as plainly.

**Owed**

- [ ] **8b.44 Decide Fanciful's `--mute`** — **D22**. 8b.39 measured the house theme's tertiary text at
      **4.42:1 on a hovered row and 3.94:1 on the selected one** — below AA at the 11.5px it is used
      at, for datelines and counts. The value is transcribed verbatim from `design/03-fanciful.html`,
      and the mockup is the specification, so this is not a value to nudge in `theme.go`. About
      `#A093AC` clears 4.5:1 on all three grounds and is the smallest change that does. *Done when:
      the mockup and `tokens.go` agree on a value that passes, and the exception is deleted from
      `sheet_test.go` rather than re-ratcheted.*
- [x] **8b.46 Focus mode and the list cursor, driven in the running app.** Both had only ever been
      verified against the emitted stylesheet in a harness. Measured for real: the cursor steps
      `0px → 384px` over four presses of `j` — exactly four rows of 96 — with the 0.18s/0.11s
      transition live; `w` collapses the rail `258 → 96 at 120ms → 0` and sets `data-focus`; `Escape`
      brings it back; the button toggles and reports `aria-pressed` correctly. No page errors.
- [ ] **8b.47 Look at the other two themes on a phone, and against `design/04-fanciful-mobile.html`.**
      8b.39's readability floor is arithmetic and it passed every theme; 8b.48 is what *looking* found,
      and it found it in the two themes nobody had opened. The mobile mockup has never been compared
      against the built app at all. *Done when: all five themes have been seen at 390px beside the
      mockup, and whatever that turns up is either fixed or written down.*
- [ ] **8b.32 Put the wasm build on CI's default path.** `go build ./...` does not compile the client,
      and during this batch the wasm build was broken for a stretch while the native build and every Go
      test stayed green. *Done when: a broken `GOOS=js GOARCH=wasm go build ./client/...` fails CI on
      the same run that a broken `go test` would.*
- [ ] **8b.34 Refresh the e2e suite for the login, and get it green.** Four specs
      (`reader` · `design-parity` · `responsive` · `tagsettings`). Every spec drives a server that used
      to need no credential and now goes through `Root`, and several still assert pre-transcription
      behaviour (see 8b.24). *Until it is green the suite is not a gate, which is the state a suite
      must not quietly be left in.*

      ◧ 2026-07-26 (late) — **measured: 21 passed, 20 failed** on `--project=desktop`. All four
      `tagsettings` cases pass. The twenty are **stale assertions, not regressions**, and they cluster
      into three shapes worth fixing as three edits rather than twenty:
      **(a) strict-mode violations** where the app grew a second element matching an old selector —
      `.article h1` now resolves to three because the reading pane is a *stream*, and
      `{name: /Alpha Journal/}` matches the row *and* its gear;
      **(b) counts that grew** — `mark all read` asserts `.feed-count` reaches zero, but categories now
      carry counts too (its `Marked N read` assertion passes first, so **the click works**);
      **(c) design-parity colour** — the palette assertion reads a background the theming engine
      (8b.31) no longer sets on that element.
      *Attribution is by reading each failure, not by bisect:* several lanes were mid-refactor in this
      tree and there was no safe way to take a clean baseline, which is itself an argument for 8b.32.

---

## The connection, audited — what a retry loop cannot fix (2026-07-26, night)

Spec: **A40**, **§20.19**, **R5** (rewritten), **T21–T22**. Prompted by a plain question — *"how are we
handling idling the WebSocket, unstable connections, retry and backoff?"* — which turned out to have a
good answer for the third part and no answer at all for the first two.

**What was already right, and should not be touched:** the backoff shape (500 ms → ×1.6 → 20 s cap,
jitter 0.2, no attempt limit ever), the server's 30 s WS ping / 90 s idle reaper, `Watch`'s `Idle`
kick, `WaitForReady(true)` connection-wide with `Subscribe`/`Refresh` opting out, and the recovery
refetch that reloads the rail and the list but never the open article. That is a better starting point
than most projects have, and it is why the two real findings were invisible: **everything that fails
loudly was handled, so what remained was everything that fails silently.**

**Finding 1 — the client has no liveness probe (F3).** A half-open socket — CGNAT reclaim, VPN drop,
Wi-Fi vanishing without an RST — leaves the browser's `WebSocket` open and gRPC in `READY`, so the
indicator says **live** forever. The server notices in 90 s because it pings; the client never does,
because nothing pings from its side. The one job of the indicator is that "silently disconnected" must
never look like "a quiet news day", and this is the case where it cannot tell.

**Finding 2 — a refused call is read as a broken connection (F7).** `Client.track` marks the connection
`down` on *any* non-nil error, so an application `NotFound` from a perfectly healthy server paints the
indicator red. **8b.33 landed mid-audit and fixed the case that mattered most** — the auth interceptor
clears the token on `Unauthenticated` and routes to `Login` — but it did so a layer above the
connection, which still misreads it on the way past. That half is now cosmetic. The half that is not:
**version skew refuses at the handshake** (§22.10), and a refusal the client cannot tell from an outage
is retried on the backoff schedule forever. §22.10 has promised "rather than retrying forever" since
rev 8 with nothing implementing it.

**The ordering rule for this batch:** 8c.1 and 8c.2 are bug fixes and go first; 8c.1's two halves must
be **one commit**.

### The two that are bugs

- [x] **8c.1 Client keepalive — and the server option that must ship with it.** §20.19.3.
      `grpctunnel.WithTunnelKeepalive(30s, 10s)` on the dial, **and**
      `grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 20s, PermitWithoutStream:
      true})` on `grpc.NewServer` in `internal/app/app.go`. **Shipping the client half alone makes
      things worse, not better:** `ApplyTunnelKeepalivePolicy` sets `PermitWithoutStream: true`, the
      server currently runs the gRPC defaults (`MinTime: 5m`, `PermitWithoutStream: false`), and an
      idle client pinging every 30 s earns two ping strikes and a **`GOAWAY ENHANCE_YOUR_CALM
      (too_many_pings)`** — converting a silent half-open bug into a visible flap every ~60 s. `MinTime`
      sits below the client interval on purpose: a throttled ping arrives late, never early.
      *Done when: a blackholed connection (accept, then stop forwarding) is declared `down` within 40 s,
      and a 30-minute idle connection reconnects **zero** times — T21(b) and T21(c).*
- [x] **8c.2 Classify the error before believing it.** §20.19.6. A `classify(err)` in `client/data`
      returning transport / application / terminal, and `track` acting on the class instead of on
      `err != nil`. `Unavailable`/`DeadlineExceeded` → `down` and keep retrying · `Unauthenticated` and
      version skew → **`blocked`, stop retrying** · `PermissionDenied`/`NotFound`/`InvalidArgument`/
      `Aborted`/`ResourceExhausted` → **the connection is fine, do not touch the indicator** ·
      `Canceled` → our own teardown, nothing. Plus: honour `retry_after_s` from a `ResourceExhausted`
      **instead of** the schedule. ← *8b.33's interceptor already owns the `Unauthenticated`
      **remedy**; this owns the **classification**, and the two must not be merged — routing the
      screen and describing the connection are different jobs, and the interceptor cannot see the
      transport.* **The case with no owner at all is version skew.**
      *Done when: the §20.7 code table is a passing table test, and a server returning `NotFound` never
      turns the indicator red — T21(a).*

### The three that are missing reflexes

- [x] **8c.3 Lifecycle kicks.** §20.19.5. `platform.OnNetworkChange` / `OnResume` in the `syscall/js`
      quarantine, and `Client.Kick()` calling **`conn.ResetConnectBackoff()`** — `conn.Connect()` is a
      no-op in `TRANSIENT_FAILURE`, so today nothing can shorten a wait even when the OS has just said
      the network is back. Wired to `online`, `visibilitychange`→visible, and `pageshow` with
      `persisted: true` (a bfcache restore closes the socket underneath us). `offline` does the reverse:
      paint the offline state and stop the countdown. **Background-tab throttling is accepted, not
      fought** — a hidden tab makes no promises and a tab becoming visible verifies before it renders.
      *Done when: closing and reopening the lid reconnects in about a second rather than up to twenty.*
- [x] **8c.4 Five states, a countdown, and a Retry now.** §20.19.2. `offline` and `blocked` join
      `live`/`connecting`/`down`, because one red dot currently means "your Wi-Fi is off", "the box is
      unplugged" and "your session expired" — three different sentences the reader needs. Plus
      hysteresis in one direction only: `connecting` suppressed for the first second and `down` held
      until the first redial fails or 3 s pass, so a 500 ms redeploy stays invisible; `blocked` and
      `live` paint instantly, because delaying good news only confuses. **The countdown plus Retry now
      is what earns the 20 s cap its headroom** — a visible, skippable wait is not a hang, which is why
      the cap does not get tuned down into hammering a server that is still booting.
      *Done when: pulling the network shows "You're offline" and killing the server shows "Can't reach
      the server · retrying in Ns", and they are not the same screen.*
- [x] **8c.5 Recovery must not storm.** §20.19.7. Every `READY` transition currently fires four RPCs;
      on a flapping tunnel that is a refetch storm at the moment the server can least serve one, and the
      storm is what keeps the recovered connection saturated. Coalesce on a 2 s trailing window, at most
      one refetch per 5 s, **skip entirely when the outage was under 2 s**, and **generation-guard every
      load** so a superseded response cannot win by arriving last. Same discipline as the note
      autosave's withheld tick: *a response may only land if it is still the answer to the current
      question.*
      *Done when: ten recoveries in five seconds produce one refetch — T21(d).*

### What the server owes the connection

- [x] **8c.6 Readiness-gate the upgrade.** `/readyz` exists and **nothing consults it**, so `/grpc`
      accepts a WebSocket into an instance that cannot serve reads — producing a client that connects
      successfully, fails every call, classifies as `down`, and retries hard against a server that is
      already struggling. Refuse the upgrade with **503 + `Retry-After`**, which is a reconnecting
      client's honest instruction to wait. Small, local, and it makes 8c.2's classification correct
      during a boot rather than accidentally right.
- [x] **8c.7 Close cleanly on shutdown.** `srv.Shutdown` **does not wait for hijacked connections** and
      a WebSocket upgrade is one, so a deploy currently severs live tunnels mid-call via
      `a.grpc.Stop()`. `GracefulStop` under the existing 5 s deadline, then `Stop`, and a `1001 going
      away` close frame. The payoff is specific: an in-flight `SetItemState` at redeploy time presently
      rolls back the optimistic UI and shows "Couldn't save that" for a server that is coming straight
      back.
- [ ] **8c.8 ⬆ Upstream: `Retry-After` on a refused upgrade.** GoGRPCBridge answers a breached
      `WithMaxUpgradesPerClientPerMinute` / `WithMaxConnectionsPerClient` cap with a bare `429` and no
      header (`server.go`), so a client that hits it backs off on a schedule unrelated to when it would
      be welcome back. **Not fixable in this repo.** Low priority — ArticleFlux's caps (8 conns, 30
      upgrades/min) are generous enough that hitting them means a bug on our side — but file it while
      the context is fresh.

### Durability — the half a retry loop does not cover

- [x] **8c.9 The mutation outbox (§12.4, A25).** Specified in rev 8, unbuilt, and now the sharpest gap
      in this area. `SetItemState` and friends are direct RPCs with `WaitForReady(true)` and a 20 s
      deadline, so marking an article read during an outage **hangs for twenty seconds, rolls the
      optimistic UI back, and discards the write** — the one thing a reader assumes is safe. The
      idempotency keys are already being sent; what is missing is the IndexedDB queue that makes them
      worth something, plus `rev` compare-and-set on drain. **Bigger than everything above it and it
      should be scheduled as its own batch, not smuggled into this one.**
      *Done when: five articles marked read while disconnected are all read after a reconnect — T22.*
- [x] **8c.10 Persist the signals buffer.** `client/track` holds up to 500 events **in RAM**, and its
      `pagehide` flush cannot succeed while disconnected — so closing the tab at the end of an offline
      session loses the entire session. A34 calls it an outbox and §12.4 puts outboxes in IndexedDB;
      the signals half belongs in the same store with the same cap and the same oldest-drop. Rides
      along with 8c.9's store rather than building a second one.

### Proof, and making flakiness falsifiable

- [x] **8c.11 T21(a)–(d) · the connection suite, native half.** Five parts, and (b) is the one that needs building rather
      than writing: a **blackhole TCP relay** in `internal/testnet` that accepts and then silently stops
      forwarding, because that is the only honest way to reproduce a half-open socket — Playwright
      cannot make a browser do it. (c) the 30-minute idle soak is the `too_many_pings` regression test,
      and it is what fails the day someone deletes the server's enforcement policy as an unused option.
      (e) is Playwright, **Windows-native per the standing rule**, using `context.setOffline(true)` for
      the offline path — ← **8b.34**, since adding specs to a suite that is not currently a gate buys
      nothing. (a)–(d) are native and do not wait on it, which is the argument for that split: **the
      two findings this audit turned up are both provable without a browser.**
- [x] **8c.12 Count the reconnects.** §20.19.10. Server → §22.15 and Settings → Server: upgrades
      accepted/refused, closes by reason, idle reaps, ping-write failures, live tunnel count. Client →
      the §20.3 ring and Settings → Activity: reconnect count, cumulative downtime, time since the last
      successful RPC. **"It feels flaky" is unfalsifiable without these**, and this app has no dashboard
      behind it. One reconnect an hour is a network; forty is a bug, and today neither is visible.
      ✅ `internal/obs.Tunnels` (live · total · peak · since · since-last-drop) behind the tunnel's
      connect/disconnect hooks, and `Client.Health()` → a Settings → Account row that appears **only
      once a reconnect has happened**, so its presence is itself information rather than a "0" nobody
      reads. *Not done: the server counters are collected and not yet on a screen — that needs a proto
      field on `GetServerStats`, which was being regenerated by another lane all evening. Client half
      is visible now.*

### Found while wiring it, and fixed — no ticket had these

- [x] **8c.13 Idempotency keys are unique per PRESS, not per item.** The four call sites sent stable
      keys — `"unread-<id>"`, `"later-<id>-true"` — which reads as tidy and is a replay hazard the day
      §20.7's idempotency store is honoured: mark unread → read → unread, and the third call is
      answered from the FIRST one's cached response and silently applies nothing. Harmless while
      `idempotency_keys` is an unused table; reachable the moment it is not, and **an outbox is what
      makes it reachable, because an outbox is what replays.** Now `intentKey(prefix, id)` with a
      millisecond suffix. *The server half is 8c.15 — this ticket only stopped the client from being
      wrong when that lands.*
- [x] **8c.14 `loadMore` had the same race as `loadItems`.** 8c.5 said "generation-guard every load"
      and named one; there were two. A page in flight when the scope changes appended the OLD feed's
      items to the new list, which reads as two feeds interleaving. Latent on a LAN and reachable the
      moment a recovery refetch can replace the list under an in-flight page — so the reconnect work
      is what turned it from theoretical into ordinary. Both guarded now, and the in-flight flag is
      cleared on the discarded path or the filler wedges permanently.

### The read half — because fixing writes alone made the asymmetry worse

- [x] **8c.19 A read on a dead connection waits 4 seconds, not 20.** `WaitForReady(true)` is right for
      a half-second reconnect and wrong for an outage: every click hung for the full call deadline and
      *then* failed, which is the worst possible ordering of those two events. The new bound is chosen
      against the backoff schedule (retries at ~0.5s, 1.3s, 2.6s, 4.6s), so it contains three or four
      whole attempts — anything that was coming back has come back.
- [x] **8c.20 A read cache, so an outage degrades the app instead of emptying it.** §20.19.8a,
      `client/data/cache.go`. The last answer to each read, served when the transport fails, bounded by
      entries **and** bytes, LRU by *use* rather than by age, persisted on `pagehide` only. Wired into
      the rail, the list and article bodies — the last of those matters most, because neighbour
      prefetch (8b.2) has usually already pulled the articles either side of the current one, so
      losing the connection mid-stream leaves a reader able to keep going in both directions instead
      of hitting a wall on the next press of `j`. **Explicitly not trip packs:** this is the last
      answer to a question you already asked, so it makes "go back to what I was reading" work on a
      plane and does nothing for "read today's news". §12 is still owed and this does not reduce it.
      *Done when: killing the server and clicking a feed you have already opened shows the list with a
      badge, not a skeleton and an error.*
- [x] **8c.21 The staleness badge, and it carries the age.** §12.3 asks for an "unmistakable" badge and
      is right to: a list that silently shows yesterday's articles during an outage is the
      "silently disconnected looks like a quiet news day" failure wearing a different hat. "Cached"
      alone is not actionable — four minutes and yesterday are the same word — so the age is in the
      line. Styled as a statement, not an alarm: nothing is broken, and an alarm would train readers
      to dismiss the one row that must not be dismissed.
- [x] **8c.22 Three operations refused rather than queued, each saying why.** `ErrOffline` from
      `Refresh` (the fetching happens on the server), `Subscribe` (the server validates the feed before
      anything is stored) and `MarkAllRead` (mints a new undo batch per call, so a replay leaves two).
      Returned in the same frame as the press. **The reason this needs saying at all:** by the time a
      reader meets one of these they have already watched three articles stay marked, and will
      reasonably assume everything is queued — silence would read as a bug.
- [x] **8c.23 The drain's rules are testable without a browser.** `Drain` was wasm-only, so its three
      decisions — stop at a transport failure, hold everything on a terminal refusal, retire an op the
      server permanently refused — were unassertable. Extracted to an untagged `drain.go` with the
      sender injected, the same shape as `track.Store` and `conn.go`. Six tests, and the interesting
      one is the third: without it, one article deleted on another device turns the whole queue into a
      wall.

### Owed, and now specified rather than implied

- [ ] **8c.15 Server-side idempotency enforcement (§20.7).** `idempotency_keys` is in the schema,
      `idgen.IdempotencyKey()` exists, and **nothing reads or writes either** — the client has been
      stamping keys onto every mutating RPC into a void. Store `(user_id, key) → response` for 24h and
      replay it verbatim. *Was theoretical; the outbox made it load-bearing, because at-least-once
      delivery is only safe today by the accident that every queued mutation sets an absolute value
      (see §20.19.8). The first queued operation that is not idempotent-by-value needs this first.*
      ← **8c.13**, which is what makes the keys safe to honour.
- [ ] **8c.16 Version skew: the server half (§22.10).** The client recognises `data.SkewSentinel`,
      classifies it terminal, stops retrying and offers Reload. **Nothing sends it.** The ordering was
      deliberate — the client that must act on a skew refusal is by definition the OLD one, so
      recognition has to ship before the refusal does or the first refusal ever sent lands on clients
      that cannot understand it. Needs: a build stamp in the tunnel handshake, a minimum-supported
      version on the server, and the refusal carrying the sentinel.
- [ ] **8c.17 T21(e) · the Playwright half.** Kill the server mid-session and assert `down`; restart
      and assert `live` plus a refetched list; `context.setOffline(true)` and assert `offline`, not
      `down`. ← **8b.34**: adding specs to a suite that is not currently a gate buys nothing.

> **The transferable rule from this audit, and it is the reason it is written down rather than just
> fixed:** *a retry loop is a latency optimisation; an outbox is a durability guarantee.* They are not
> substitutes, and shipping a good retry loop makes the missing outbox **harder** to notice, not
> easier — the system now recovers so smoothly from the failures it can see that nobody goes looking
> for the ones it cannot.

### ✅ Built 2026-07-26, night — and the three things the build changed

**8c.1–8c.7, 8c.9–8c.14 and 8c.19–8c.23 done; 8c.8 and 8c.15–8c.17 open and specified above.** `go build ./...`,
`GOOS=js GOARCH=wasm go build ./client/...` and `go vet ./...` green; **`go test ./...` is 38 packages
passing, zero failures.**

**New packages, and why each is a package rather than a few lines somewhere.**

- **`internal/connpolicy`** — the four keepalive numbers and the invariant between them, imported by
  BOTH ends. The finding was "these two must ship together", so the structural answer is one file that
  names both and a test that fails when they diverge. Deliberately holds no gRPC types: `client/data`
  imports it, and every byte it pulls in is a byte in `app.wasm` (R4).
- **`client/data/conn.go`** — **untagged, while the rest of the package is `js && wasm`**. Classification,
  the backoff estimate and the recovery gate are arithmetic over a status code and a clock, which is
  where the interesting failures are. The rule this establishes: *anything in a wasm package that can
  be decided without the DOM belongs in an untagged file, so it can be decided in a test instead.*
  Both of the audit's findings turned out to be provable without a browser.
- **`client/outbox`** — the queue, pure and native-tested: coalescing, ordering, the cap, the round trip.
- **`internal/obs/tunnels.go`** — WebSocket lifetimes as counters rather than as log lines. Every
  connect already logged; what nobody could do was read a thousand of those and answer "is one an hour
  normal on this box?"

**Three things the build changed about the design.**

1. **gRPC silently clamps client keepalive to a 10s floor.** Found by a test that set 200ms, waited
   five seconds, and asserted a detection that could not arrive before ten. Nothing errors and nothing
   logs — so the obvious response to "detection feels slow" (lower the interval) produces a number that
   is not used and a documented budget that is wrong. Our 30s is comfortably above it; the floor is now
   `connpolicy.GRPCClientFloor` with a test, because the trap is not the limit, it is that going under
   it changes nothing and says nothing.
2. **The outbox is localStorage, not IndexedDB — a deliberate departure from §12.4.** §12.4 chose
   IndexedDB for *packs*: megabytes localStorage cannot hold. A mutation queue has two properties packs
   do not: it must be readable **synchronously at boot**, before the first render can honestly draw
   anything, and writable from **`pagehide`**, where an async IndexedDB transaction is not guaranteed
   to commit before the tab is gone. Both are what localStorage does and IndexedDB does not.
3. **`whenReady` and `Close` grew past their tickets, correctly.** One `a.ready()` behind both `/readyz`
   and the tunnel gate, because two readiness checks drift and the drift is silent — the probe says
   ready, the upgrade says no, and the operator is looking at a green dashboard.

**Two findings the work turned up that were not in the audit** — both now ticketed above as **8c.13**
and **8c.14**, because a finding recorded only in prose is a finding nobody is assigned.

**Not done, and each for a stated reason rather than for lack of time:**

- **8c.8** — `Retry-After` on a refused upgrade lives in **GoGRPCBridge**, which this repo consumes at a
  published `v1.1.1` with no `replace`. Editing the local checkout changes nothing here until that
  project tags a release, so it is a two-line change inside someone else's release cycle. ArticleFlux's
  own caps (8 connections, 30 upgrades/min) are generous enough that reaching them means a bug on this
  side. Filed rather than half-done.
- **8c.15 / 8c.16 / 8c.17** — see above. All three were implicit before this batch and are now written
  down with what they depend on.
- **8c.12's server-side display.** The counters exist and are collected; putting them on the Settings →
  Server tab needs a field on `GetServerStats`, and the proto was being regenerated by another lane
  through the evening. The client-side half is live.

**One edit outside this work, flagged rather than buried:** the wasm build was broken by an in-flight
typo in another lane's change (`id, value := forItem.Get(), value`). Completed to `forValue.Get()` —
unambiguous from the surrounding comment, and leaving `GOOS=js GOARCH=wasm go build` red while
reporting this batch green would have been the worse call. **Worth a glance from whoever owns that
change.** I hit the wasm build broken while the native build stayed green **twice in one evening** —
this typo, and separately a missing `strings` import in `theme.go` that its author fixed within a
minute. Neither would have reached a commit with **8b.32** done, which is the whole argument for it.

---

## Every interaction point, audited — the eight-second freeze and what else was on the JS stack (2026-07-26, late)

Prompted by three plain reports, in this order: *"the left side collapsable menu items take very long
to collapse"*, *"removing a tag is really slow and the ui seems to seize up"*, and then *"check all
interaction points and make sure they arent doing bad things and arent latency prone"*. The first two
turned out to be **the same bug wearing different clothes**, and the third found four more.

**The freeze.** Both reports carried the same console line — `[Violation] 'visibilitychange' handler
took 8000ms` — and 8000 is not a round number that appears by accident. It is `engagementTimeout` in
`client/data/client.go`, exactly.

> In Go/wasm a `js.FuncOf` callback that blocks **holds the JavaScript event loop**, and the tunnel
> is a WebSocket — so the reply the blocked call is waiting for arrives *on the loop it is holding*.
> The call cannot succeed by construction. It can only end at its own deadline, and the tab is frozen
> for the whole of it. `track.Collector.Start`'s page-hide handler was calling `Flush()` inline; the
> comment directly above `platform.OnPageHide` already said the flush "cannot await anything", which
> is how a rule that is written down still gets broken — it said *what* and not *why*.

Fixed by handing the ship to a goroutine on both the page-hide path and `Stop` (reached from an
effect cleanup, and effects run on the frame loop, which is also a JS callback). `Flush` grew an
**in-flight guard**: a batch takes up to eight seconds to give up, the periodic ship fires every ten,
and `Emit` starts another every time the buffer crosses `BatchSize` — so a wedged tunnel accumulated
blocked goroutines each holding a slice of events. Batching harder under pressure is the right
response to a slow connection; opening more calls to it is not. The rule is now in the doc comment on
`OnPageHide` **with the mechanism**, not just the prohibition.

*The rail collapse was never slow.* The click was queued behind a page that had deadlocked against
itself, and folding a section was simply the next thing the reader tried.

**Removing a tag waited on two round trips before anything moved** — the `SetFeedTag` call, and then
the `ListTags` refetch it queued — with no feedback in between. Removal is now optimistic, applying
the server's own two rules locally (drop the association; drop the tag if that was its last feed) with
a full rollback and a notice on failure. The refetch became a background reconcile rather than the
thing the reader waits for.

> **Adding cannot be optimistic and is not pretended to be.** The tag's id is the server's to assign,
> and the rail, the scope and the association map are all keyed by it — inventing one would mean a
> chip that is briefly clickable into a tag that does not exist. So the wait is shown rather than
> hidden: the chip appears at once, dashed and dimmed, with the × withheld because there is nothing on
> the server to remove yet. The honest version of instant — the input is acknowledged immediately, and
> the one capability that genuinely is not ready is the one that is withheld.

**Then the audit itself.** Thirty-three `js.FuncOf` entry points, every delegated handler, every RPC
call site and the render path. Four more findings, all fixed:

- **The delegated dispatcher dropped clicks.** Three sibling listeners: two write the click's payload
  into shared refs (`forItem`, `forValue`), the third reads them — but it read them *inside* its
  `ui.PostAsync` body, a frame later. Two clicks in one frame both queue a body; the first reads the
  **second** click's id and then clears the ref, so the second body reads `""` and its action silently
  does nothing. No error, no save, nothing to see. The payload is now captured on the click's own
  stack and passed into the closure. This was **not theoretical** — it is what made the tag panel's
  "clear the rename" e2e case fail, since that is a second click on the same button.
- **The chip derivation ran on every frame** — walking every feed's tags, allocating per feed and
  *sorting* each, from inside the render, to rebuild a value identical to the one discarded the frame
  before. Moved behind `setTagData`, the single write path for the three pieces of tag state, so the
  cache cannot be written any other way and there is nothing to forget. It also made the map identity
  stable, which is what lets `articlePane`'s props bailout work at all.
- **`tagglyph` rescanned its own catalogue per render** — `Groups()` and `In()` walked all fifty
  entries per group, ~350 comparisons and eight allocations every repaint, for a value that is a
  compile-time constant in all but name. Indexed once at init, beside the validation map that already
  was.
- **`tagByID` walked the tag list to discover the panel was shut.** The empty case is the common one
  and it is on the render path.

**What the audit found clean**, recorded because "we checked" is worth as much as "we fixed":
every RPC call site in `client/view` is inside a `go func()`; every `platform.On*` callback either
posts through `ui.PostAsync` or does local-only work; every listener is released and every ticker
stopped; scroll handlers are rAF-coalesced or edge-triggered and `passive`; the 500ms impression poll
reads a **virtualised** window rather than 151 rows; item actions (read · star · rate · unread ·
folder) were already optimistic with rollback; every `js.Value` index is bounds-guarded; and the
signals buffer persists on flush and page-hide rather than per event — a `localStorage` write per
`pointermove` would be a real cost for the one layer that may never cost the reader anything.

**My typo, flagged by another lane and fixed correctly.** A line-ranged `perl` rewrite of
`forValue.Get()` → `value` inside the dispatcher clipped the capture line itself into
`id, value := forItem.Get(), value`. Completing it to `forValue.Get()` was right, and the lane that
hit it was right not to leave the wasm build red while reporting green. A range-scoped text
substitution over a file another lane is editing is the wrong tool; the capture line was the one line
in the range that had to keep the old spelling.

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
| **M9** | Bookmarks + archiving + dead links + bookmarklet + **the asset proxy (6.14)** | §12, §10.1a | `/b` `/b/unread` `/b/dead` `/b/:id` | C4 all | 9 | T23 |
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
| **M27** | **Page proxy** — asset rewriting · `snapshot` policy · the proxy origin · signed URLs | §10.1b | article pane, `Page` mode | C3 `RenderModeSwitcher` **`PageView`** | — | T23 |
| **M28** | **Headless renderer** (2r, compressed) + **frame stream** (tiers 3–4) + **the ladder** | §10.1c–d, **§10.1-R** | article pane, `Live` mode | C3 **`RemotePage`**, ladder controller | — | T23 |

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
- **Nothing that touches the network runs on the JS stack.** A `js.FuncOf` callback, a frame-loop
  callback, or an effect cleanup that blocks holds the JavaScript event loop — and the tunnel's
  WebSocket delivers its reply *on that loop*, so the call cannot succeed, only time out, with the tab
  frozen for the full deadline. Hand it to a `go func()` and return. Cheap local work (arithmetic
  under a mutex, banking a dwell timer, a `localStorage` write that must commit before the tab is
  gone) may stay inline. **The tell in the wild is a Chrome `[Violation] '<event>' handler took
  N000ms` where N000 is one of our own timeout constants.** (8b.35)
- **A click's payload is read on the click's own stack, never from a shared ref one frame later.**
  Two clicks inside one frame otherwise make the first handler act on the second's target and the
  second act on nothing — silently, with no error to notice. (8b.36)
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

> **As built, 2026-07-26.** There is **no router** — the client is one mounted tree, and `Root` chooses
> between `Login` and `Reader` (§7.1b). So "the login page" exists as a *component*, not a route, and
> the rest of this appendix's routes are surfaces reached inside the reader: Settings is a pane with
> seven tabs (§20.17), per-feed and per-tag settings are dialogs, add-a-feed is a dialog. Routing
> arrives with M8's `ViewSpec` and saved views; until then a route in this table is a page that will
> exist, not a URL that does. `/recover`, `/enroll/:token` and `/setup` have no counterpart at all —
> `articleflux init` is a shell command, deliberately (§22.3).

---

## Appendix B — Settings registry

**The registry is data, and the UI renders itself from it** (§8) — the only way this survives ~90
settings. Each entry declares key, type, default, range, the scopes it may be set at, and the
capability required. `GET` returns the resolved value **and which layer supplied it**, so "why is this
30 minutes?" is answerable in the UI.

Scope key: **S** system · **T** tenant · **U** user · **F** per-source (on `subscriptions`).

> **As built, 2026-07-26 — there is no registry yet.** `user_prefs` is a flat per-user key/value table
> behind `GetPrefs` / `SetPrefs`, and the client owns the key names. What exists:
>
> | Key | What it holds |
> |---|---|
> | `pane.rail` · `pane.list` | Pane widths (§20 — layout is account state, not browser state) |
> | `rail.filter` · `rail.closed.*` | Unread-only toggle; which rail sections and categories are folded |
> | `read.kind` · `read.value` · `read.item` · `read.title` | A30 resume: scope, its argument, the open article and its title |
> | `ui.theme` · `ui.accent` · `ui.reading` · `ui.motion` | A39 appearance (§20.16) |
>
> Missing versus §8: types, defaults, ranges, the **system → tenant → user** resolution, the capability
> per setting, and `GetResolved` returning *which layer supplied the value*. That last one is what makes
> "why is this 30 minutes?" answerable, and nothing can answer it today. **The UI does not render itself
> from data** — each of the settings above is a hand-written control, which is affordable at twelve keys
> and is exactly what stops being affordable at ninety (6.3, M8).

| Group | Settings | Scope |
|---|---|---|
| **Appearance** | theme (light/dark/sepia/high-contrast/auto) · density (compact/comfortable/card) · reading font · size · line height · measure (ch) · paragraph spacing · alignment · direction (auto/ltr/rtl) · image display (full/constrained/hidden) · per-source hue on/off · reduced motion (follow/force) | T,U |
| **Reading** | default render mode (auto/feed/reader/**page**/**live**) · mark read on open · mark read on scroll · keep-unread override · next-unread wraps feeds · restore scroll per feed · confirm mark-all-read · open original in new tab · timestamps (relative/absolute) | T,U |
| **Proxy & rendering** | proxy images through the server (§10.1a) · page proxy enabled · **renderer enabled** (D19) · **live view enabled** (flag, off) · render escalation (auto/never) · snapshot retention days · max renders per day · proxy hostname (S, read-only — set at deploy) | S,T,U |
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
`RenderModeSwitcher` (five positions from M27: auto · feed · reader · **page** · **live**) ·
**`PageView`** (sandboxed iframe over the proxy origin, with the visible fallback chain) ·
**`RemotePage`** (tile compositor + input forwarding, flag-gated) ·
`ReadingProgress` · **`NoteEditor`** (markdown, debounced autosave, explicit
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

> **As built, 2026-07-26.** The components that exist do **not** come from this inventory, because the
> reader was built from use rather than from the list. What is in the tree: `Root` · `Login` ·
> `settingsPane` (seven tabs) · `settingsAppearance` (theme / accent / reading size / motion) ·
> `feedSettings` · `tagSettings` · `addFeed` · `railCategories` + `categoryRows` · the palette · the
> shortcut sheet. Two gaps this makes concrete — **`SettingsField` does not exist**, every control
> above is hand-built (affordable at twelve prefs, not at ninety — see Appendix B), and **`Page` does
> not exist**, so the four states are re-implemented per surface rather than baked in.

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
| **M13** | `RenderService.GetArticle` (the switcher only) |
| **M27** | `RenderService.MintProxyURL` |
| **M28** | `RenderService.RenderPage` · `StreamPage` (bidi) |
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
| **A27**–**A32** verdicts, stream, true-length list, resume, listening, keyboard | 8b.1–8b.20 · e2e |
| **A33** settings labelled by owner | 8b.21 · one transaction across both tables |
| **A34/A35** client outbox, analytics never degrades reading | `client/track` · 6.9 |
| **A36** auth is not inferred from topology | **`-dev` refused off loopback** (7.7) · `Preflight` · guards' unscoped-by-design list · *owed: a test that a non-loopback bind with `-dev` exits non-zero* |
| **A37** folders exclusive, tags not | 5.11 · `subscriptions.folder_id` is a single column, which is the enforcement |
| **A38** tag identity vs presentation | 5.12 · **`UpdateTagRequest` has no `name` field** — the wire shape is the enforcement |
| **A39** every value is a token | 8b.31 · **`client/design/sheet_test.go`** (8b.39) — fails on a hex outside a `Theme`, on a duration not written `calc(var(--mo) * …)`, on a token no theme can reach, and on a theme below AA. Asserted against the *emitted* sheet, so it is a decision now rather than a convention |

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
| **R10** filter bubble | 4.11 explore slot · T17 | **R21** proxy is an egress proxy | 6.14/6.15 item-id-only · 7.12 caps · T23 |
| | | **R22** tier 3 traffic signature | 7.13 flag-off · tile diff · 8.21 explainer |

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
| **D19** renderer: ships? where? | 6.14 | before M28 — *not before 4.13 / 6.15 / 7.12, which stand alone* |
| **D21** how the ladder detects its rungs | the automatic half of 8.22 | before the ladder is automatic — the manual switcher works without it |
| **D20** proxy origin | 7.12 | **before the first signed URL is minted** — splitting the origin later is a migration of every cached artifact |

**D3, D4, D6 are resolved** and carried only for the record.

> **Three block Tier 1 or earlier: D0, D2, D5.** D5 is the one most likely to be waved through and
> most annoying to fix later — the module path bakes the name into every import in the repo.

**Twelve of these are choices, not discoveries**, and §25.0 drafts all twelve with reasoning so they
close in one sign-off pass: D5, D8, D9, D10, D11, D12, D14, D15, D16, D17, **D19, D20**. **D12 is the
one that removes work rather than adding it** — invite-only deletes the entire registration, CAPTCHA,
email-verification and abuse-tooling surface. **D20 is the one with a deadline rather than a
dependency**: it is free today and a migration of every cached artifact once signed URLs exist.

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

---

## Hosting it, the login it forced, and an empty feed that lied (2026-07-26, night)

Prompted by *"for articleflux make sure this is ready to be hosted on linux and preferably via ubuntu
vps on digital ocean"*, and then by four follow-ups from actually running it: *"add the dev mode
credentials in the env.example file for quick reference and add a flag that disables this login for
dev mode"*, *"the dev server is down"*, and *"when a rss feed has not feed items, it shows the feed
items from the last selected feed instead of also saying no items"*.

The answer to the first was **no, and for one reason that mattered more than the rest of the list.**

### The finding that reordered the work

`DevMode` — no login at all, plus an unauthenticated `POST /debug/reset-state` — was **derived from a
loopback bind**. The reasoning was that loopback cannot be reached from outside the machine, which is
true of the *socket* and false of the *deployment*: every reverse-proxy setup terminates TLS on `:443`
and forwards to `127.0.0.1:9000`, including the nginx site now in `deploy/`. So the canonical way to
host this was also the way to publish an entire reading history to anyone who typed the domain. And
the other bind — `0.0.0.0` — had no login to offer, so **no bind address was both usable and safe.**

A bind address is a fact about network topology. It cannot tell you who is on the other end of a
connection, and nothing that cannot tell you that may decide whether to ask for a password. That is
why 6.1 was built ahead of its milestone rather than after it.

### Shipped

- [x] **H1 · `-dev` is a flag, not an inference.** Default off, refused on any non-loopback bind, and
      refused alongside `-behind-proxy` — a proxy in front of a loopback bind is a published
      instance, which is exactly the case it must never apply to. That second refusal exists because
      `.env` can set it (H3): without it the original hole walks back in through a stale development
      file copied to a server.
- [x] **H2 · The Linux half of the build.** A `Makefile` mirroring `scripts/make.ps1` **verb for
      verb**, plus `linux`, `install-service`, `backup`. 1.4's "no `make` on this box" reasoning was
      about the DEVELOPMENT box and does not extend to the droplet, which has make and no PowerShell.
      `Makefile text eol=lf` is pinned in `.gitattributes`: a recipe line with CRLF makes GNU make
      hand a trailing carriage return to the shell, and `command not found: go` with an invisible
      `\r` sends people hunting a broken toolchain.
- [x] **H3 · `internal/envfile` + a documented `.env.example`.** `.env.example` had said "copy to
      .env and fill in" since Tier 1 and **nothing ever read the file** — the only consumer read
      `OPENAI_API_KEY` from the process environment, so the instruction was true only for someone who
      already knew to export it by hand. Eighty lines, no dependency. The load order is the design:
      flag beats environment beats file, and **the real environment always wins**, so an
      `EnvironmentFile=` in a systemd unit cannot be overridden by a stray `.env` in the working
      directory.
- [x] **H4 · The deployment itself** — `deploy/` carries a hardened systemd unit, an nginx site, a
      nightly verified-backup timer, and a runbook that takes a bare Ubuntu droplet to TLS in about
      twenty minutes. The nginx `/grpc` block is the load-bearing part: `proxy_read_timeout` defaults
      to 60s and the tunnel is idle whenever nobody is clicking, so the default severs it on a timer
      and the client reconnects — which presents as "the reader refreshes randomly" rather than as a
      proxy misconfiguration. `MemoryDenyWriteExecute=no` is deliberate and commented: the SQLite
      driver is a wasm module JIT-compiled by wazero, and denying W^X kills the database on the first
      query.
- [x] **H5 · The dev account is one value.** It was five separate `"articleflux"` literals across
      `main.go` and the client, at **eleven characters** — one short of the twelve `init` enforces. So
      `.env.example` documented a password `init` refused, and following the documentation produced an
      error and then a server that would not start because no account had been created. Now
      `devUsername` / `devPassword` in `cmd/articleflux/admin.go`, `articleflux-dev`, referenced
      everywhere including the client prefill.
- [x] **H6 · Dev mode does not ask for a password.** `Root` used to short-circuit to the login screen
      whenever local storage held no token, which is right for a deployed instance and wrong for
      `serve -dev`: it presented a password prompt for a server that did not want one. Whether a
      credential is required is a fact about the SERVER, so the server is asked — `WhoAmI` runs even
      with no stored token. Cost is one fast RPC before the login screen on a genuinely
      unauthenticated boot.
- [x] **H7 · The login screen prefills on a loopback origin.** `cam` / `articleflux-dev`, both fields,
      one click. Gated on `platform.Origin()` parsed as a HOST — a substring test for "localhost"
      would prefill on `https://localhost.attacker.example`, which is a domain someone can own. A
      deployed instance never prefills.

### Three bugs found by running it rather than by reading it

- [x] **H8 · The infinite reload loop.** Making `Root` always ask `WhoAmI` (H6) met an interceptor
      that treated **any** `Unauthenticated` as "your session died" and reloaded the page. With no
      token the server correctly answers `Unauthenticated`, so: dial, ask, reload, forever. The login
      screen never survived long enough to submit, and the visible symptom was *"Couldn't sign in.
      Check the server is running"* — a message about the transport, from a server answering
      perfectly. Fixed by excluding the whole `AuthService`, matched on the **service prefix** rather
      than method by method: every method on it is a question ABOUT authentication rather than a call
      requiring it, and the failure mode of forgetting to add a new one is a reload loop.
- [x] **H9 · A password manager could kill the wasm module.** Reported from a real console trace:
      `panic: syscall/js: call of Value.Bool on undefined` in `OnKeyDown`. A manager filling the login
      form dispatches a synthetic `new Event('keydown')` to make frameworks notice the value it wrote,
      and a plain `Event` has no `key`, no `altKey`, no `ctrlKey`. Reading one returns `undefined`,
      and `Value.Bool()` on undefined does not return false — it **panics**, which in wasm tears down
      the module, every listener with it, and leaves the page a dead screenshot of itself. Now
      discriminated on `key` being a string, with every boolean read going through a guarded helper.
      The same unguarded pattern in `PrefersReducedMotion` went with it. **This is the class of bug
      guard 2 exists to contain and does not catch**: quarantining `syscall/js` in one package is what
      made it a five-line fix in one file, but nothing enforces that the package treats the runtime as
      untyped. Chrome's *"Password field is not contained in a form"* warning arrived in the same
      trace and was the clue; the fields are now in a `<form>`, which is also what lets a manager pair
      and save the credential at all.
- [x] **H10 · The empty feed showed the previous feed's articles.** Reproduced against the real
      database — 151 feeds, of which a large number hold zero items. `loadItems` set
      `itemsLoading = true` but never cleared `items`, and `listPane` only draws its skeleton when
      **both** loading and empty — a condition a scope change never reached. So it fell through and
      painted the old rows under the new title. On a feed with items this self-corrects when the
      response replaces them; on a feed with **zero** items it never corrects, because the response
      replaces them with nothing. The reader is then looking at one feed's articles filed under
      another feed's name, which is worse than a slow list: it is a list that lies. Fixed by
      `clearList()` on scope change in `selectScope` and `runSearch` — **not** inside `loadItems`,
      which is also how the list refreshes in place after "mark all read" and after a reconnect, where
      blanking the screen would turn a silent update into a flash. *Note the comment that was already
      there claiming the loading flag prevented "one frame showing the previous feed's rows" — it
      never could, because the branch it guarded was unreachable. A comment asserting a guarantee the
      code does not provide is worse than no comment.*

### Open, and it is not a UI bug

- [ ] **H11 · An async state write does not repaint when nothing else changed.** After H10, an empty
      feed no longer shows the wrong articles — it shows a **loading skeleton that never resolves**,
      for a request that succeeded. Traced end to end: the RPC is sent with the correct scope, returns
      without error, the response lands not-stale with `n=0`, and `itemsLoading` is set false and
      **reads back false**. No render follows. A revision counter threaded into `listProps` confirmed
      it — the state read back as `3` while the last render was `rev=2`.
      > Why only empty feeds: on a populated feed the new rows are self-evidently a change, and
      > `ScrollPaneToTop` fires a scroll event that repaints. With zero items every write in the
      > handler is already a no-op — the rows were cleared on the scope change, the total is already
      > 0, the cursor is already empty — so the only genuine change is one boolean, and that alone
      > does not repaint. **The app has been masking this by repainting on incidental scroll events.**
      >
      > Three fixes tried and rejected as insufficient, all kept where they are independently
      > correct: an in-flight counter so `itemsLoading` cannot stick when a superseded response
      > returns last (kept — real hardening for that case); a revision counter in `listProps` (kept,
      > and `rev` is marked "nothing reads it, do not remove"); and normalising an empty result to a
      > non-nil slice, because `nil` meaning *never loaded* and empty meaning *loaded, nothing there*
      > are different facts that must not share a representation (kept).
      >
      > **Next place to dig is `ui.PostAsync` and `runtime.PostAsyncGlobal` in the GWC checkout, not
      > `reader.go`.** If confirmed there it is not one screen's problem: any async update whose only
      > change is a flag is invisible, which is most "finished loading", "saved", and "failed" states
      > in the application.

### Owed from this batch

- **H2's Makefile has never been executed.** There is no `make` on this box (1.4), so every recipe was
  verified by running its shell body directly in bash — the `wasm_exec.js` discovery, the
  compress-to-temp-then-move, the size formatter. That is not the same as `make wasm` passing, and the
  first droplet build is where it gets proven.
- **`deploy/` has never been run against a real droplet.** Every file in it is reasoned from the
  failure it prevents, and none of it has met DigitalOcean.
- **A sign-out button.** `data.SignOut` exists, works, and has no affordance — wiring it into the
  settings screen means editing `reader.go` while another lane is rewriting it. Clearing local storage
  is the current logout.
- **D0 is now an operational footgun, not just a build inconvenience.** The droplet needs both
  checkouts side by side and both kept in step on every update. `make deps` says so loudly instead of
  failing with a path error, but tagging GWC v5.0.0 removes the whole class.
- **Roles are stored and not enforced** (6.2). `deploy/README.md` says so in its own "what is not here
  yet" section, because handing someone `-role viewer` while believing it restricts anything is the
  kind of mistake a runbook should prevent rather than enable.
