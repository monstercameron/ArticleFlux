# ArticleFlux — every feature and capability, and how each behaves

*A UX-level catalogue of the whole application: what exists, what it does when a person touches it,
and what is designed but not yet built. Compiled 2026-07-27 from `plan.md` (rev 9), `TODO.md`,
`FLOWS.md`, the proto contract, the i18n catalogs, and the client source.*

---

## How to read this

This document answers one question per entry: **if a reader does this, what happens?** It is the
behavioural companion to the three documents that already exist and deliberately does not duplicate
them:

| Doc | Owns |
|---|---|
| `plan.md` | The spec of record — decisions (`A#`), open questions (`D#`), risks (`R#`), schema, milestones |
| `TODO.md` | Build order, gates, and the page/settings/component/flow inventories |
| `FLOWS.md` | The nine paths that are easy to get subtly wrong, drawn |
| **`docs/FEATURES.md`** | **What each capability does from the outside, and whether it does it today** |

**Precedence is unchanged: `plan.md` wins.** If this file and the plan disagree, this file is wrong.
If this file and the *running application* disagree, this file is wrong and should be corrected in
the same change.

### Status legend

| | Meaning |
|---|---|
| ✅ | **Shipped.** A reader can use it today in the running app. |
| ◧ | **Partial.** Some of it works; the gap is named in the entry. |
| ⚙ | **Engine only.** The logic exists and is tested, with no RPC or no UI in front of it. |
| ○ | **Planned.** Designed in `plan.md`, not built. The entry describes the intended behaviour. |

The appendix at the end indexes every entry by state, so "what is actually usable today" is one
lookup rather than a read-through.

---

# Part I — The reading surface

The whole application is one mounted tree. There is no router yet (M8 brings `ViewSpec` and saved
views); every surface below is a pane, a dialog, or a scope inside the reader.

## 1. The shell — three panes

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.4, §20.11, §20.22 |

Three columns: **rail** (subscriptions), **list** (articles in the current scope), **article**
(the reading stream). Above 1220px all three are visible. Between 900 and 1220px the article pane
replaces the list. Below 900px one column shows at a time, chosen by a four-tab bar.

**The panes are resizable.** A 6px grip between each pair; drag with the pointer (pointer capture, so
the drag survives leaving the handle), clamped so no pane can be dragged to nothing. The width is
written to a CSS variable live during the drag and saved **once** on release.

**Widths are account state, not browser state.** They live in server prefs (`pane.rail`,
`pane.list`) and come back on a different machine. A reader that forgets your layout when you open it
on the laptop has not remembered anything.

## 2. The rail

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.18, §6.10, A37 |

A **fixed head, a scrolling middle, and a fixed foot**. The head holds the masthead and the streams;
the middle scrolls through feeds, tags and categories; the foot holds **Add a feed**. The five
streams are the destinations used most and the only ones whose position a reader can learn — paying
for six rows once at the top beats scrolling back to them.

**Four bands, each folds, and the fold state is remembered:**

- **Streams** — All feeds · Unread · Read later · Liked · Notes
- **Feeds** — every subscription, alphabetical
- **Tags** — your per-feed labels
- **Categories** — folders, each itself a fold

**Categories are shown *as well as* the flat feed list, deliberately.** The flat list answers "where
is that feed"; the categories answer "what have I got on this subject", which 151 flat rows cannot
answer at all. A feed appearing twice costs a row in a section that folds away.

**Every row carries its source hue** as a dot, with the favicon layered over it when there is one.
The hue stays underneath: a site with no icon must still be identifiable, and the icon is
faster-recognition for sites you already know rather than a replacement for the system.

**A feed that is not responding says so** in its accessible name (`… · not responding`) and carries
the publisher's last error in its tooltip — a dead feed and a quiet feed are otherwise identical.

**Two filters**, both persisted server-side:

- **Unread-only** (`All` / `Unread` chips) — at 151 subscriptions the rail is mostly feeds that did
  not publish today, and scrolling past them is the actual daily cost.
- **A name filter**, which appears once you have more than 8 feeds.

The selected feed is always shown even when it has nothing unread — hiding what you are reading is
disorienting.

## 3. The item list

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.10, §20.12, A29 |

A **virtualised** list: ~14 DOM rows whether 60 or 3,600 items are loaded. Rows are a fixed 96px, and
the height is a structural constant rather than a style — variable rows need a measurement pass each,
which is exactly the cost virtualisation removes.

**The scrollbar is honest from the first paint.** The list is sized to the scope's *true* total
(`ListItemsResponse.total`, a `COUNT` over the same filter set as the page query), not to what has
been fetched. Unloaded indices render as placeholders and resolve as they arrive.

**Filling is driven by scroll position, not by proximity to the loaded end** — those are the same
thing only while the list is as long as what has been fetched. A page request is sized to the *gap*
and capped, so dragging the thumb a long way is a short chain of large pages rather than sixty small
ones. There is also a keyboard-reachable **Load more**, so the feature is not pointer-only.

### 3a. Row states

A row says three things about itself, **in words as well as colour** — a colour alone does not
survive being colour-blind or being glanced at.

| State | Meaning | Treatment |
|---|---|---|
| `new` | Unread, published under 24h ago | Amber **new** pill |
| `unread` | Unread, 24h–30d | The default row |
| `stale` | **Unread and over 30 days old** | Outlined **stale** pill, title softened, hue bar to 50% |
| `read` | Read, at any age | Title loses weight, hue bar to 35% |

`read` wins over age: once you have read it, how old it is stops being the useful fact. `stale` is
the one a reader acts on — it is permission to skip.

Rows also carry **saved for later**, **liked** / **disliked**, and a **Note** flag. A note, when
there is one, **outranks** the ranking reason and the publisher's summary in the third line: in the
Notes stream a row of headlines is nearly useless for finding the one you want, because what you
remember is what *you* wrote.

### 3b. The list header

Scope title, a subtitle that names the ordering ("Newest first", "N unread, newest first",
"Matching your search, most relevant first"), and the controls: **search**, **connection state**,
**Refresh**, **Unread only**, **Mark all read**, **Settings**, **Keyboard shortcuts**.

There is deliberately **no top bar** across the app. It was removed in the design-parity pass; these
controls live in the list header and add-a-feed lives at the foot of the rail.

### 3c. Empty states

Every empty list names the situation and then says what to do about it. The second line is the point;
dropping it turns direction back into a shrug.

| Scope | Empty says |
|---|---|
| Unread | "All caught up" · *press `u` to show everything again* |
| Search | "Nothing matched" · *try fewer words, or clear the search box* |
| Read later | "Nothing saved for later" · *press `t` on an article* |
| Liked | "Nothing liked yet" · *press `l`, or use ▲ Like* |
| No articles at all | "No articles yet" · *add a feed URL, then hit Refresh* |

## 4. The article pane is a stream

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.9, A28, `FLOWS.md` §5a |

**The reading pane holds a sequence of articles, not one.** This is the single most distinctive
interaction in the application.

- Approaching the bottom **appends** the next item from the list.
- Approaching the top **prepends** the previous one, with the scroll position held across the
  insertion — inserting content above a scroll position otherwise shoots the paragraph being read off
  the bottom of the screen.
- **Nothing is ever taken away from under the reader.** Replacing the pane's contents on advance —
  the first implementation — made reaching the end of an article vanish it mid-paragraph with nothing
  to scroll back to.

**Which article is being read is a scroll position, not a click.** The topmost article in the
viewport drives the document title, the verdict chips, the highlighted list row (the list scrolls to
match), and marking read.

**Opening from the list seeds the stream with the article *before* the clicked one**, so scrolling up
works from the first frame, and scrolls the clicked one to the top of the pane.

**Bodies are prefetched one ahead in each direction**, so the skeleton is only ever seen on a cold
open.

**Articles over 900 words are clamped by default** with a *Read the rest · N min* control, so
time-per-item while scanning stays roughly constant. One 4,000-word essay between two headlines makes
a feed unpredictable to scan, and scanning is what the stream is for.

**Movement the app causes is not evidence of reading.** The seeded predecessor, the article a prepend
inserts, and everything a jump passes over are all suppressed from marking. Becoming topmost takes an
id back out of that set — scrolling *up* into something is reading it.

### 4a. The article's chip row

Reading time · word count · **Like** ▲ · **Dislike** ▼ · **Read later** · **Mark unread** ·
**Open original** · **View page** / **Full width** (when the page proxy is on) · the listen bar.

### 4b. The note panel

| | |
|---|---|
| **Status** | ✅ |

Under every article: your note, and the feed's tags.

**The note saves itself** — 800ms after typing stops, immediately on leaving the field, and
immediately on `Ctrl+Enter` for anyone who wants it now. Plain `Enter` stays a newline, because a
note that submits on Enter cannot hold two sentences.

A **sync glyph** reports pending → saving → saved, and speaks in words only when a save *fails*
("Not saved — still only on this device"). It withholds the tick if typing continued while the write
was in flight: the one thing it must never do is claim a save that has not landed.

**Tags on the article are the *feed's* tags**, and the heading says so in full — removing one from an
article removes it from the feed. Removal is instant (optimistic, with rollback); **adding shows the
wait honestly**, because the tag's id is the server's to assign: the chip appears immediately in a
dashed pending state with the × withheld until it exists.

## 5. The keyboard

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.14, A32 |

**Arrows move *within* a pane; Tab moves *between* them.** That split is the whole design — 151 tab
stops between the rail and the article is not navigation. Focus is read out of the DOM rather than
tracked in state, so clicking a row and then pressing an arrow continues from the row that was
clicked.

| Where | Keys |
|---|---|
| **Anywhere** | `Ctrl-K` palette · `?` shortcut sheet · `,` settings · `1` `2` `3` jump to rail/list/article · `/` search · `f` filter feeds · `r` refresh · `u` unread-only · `Esc` close / stop reading aloud / back |
| **Feed list** | `↑ ↓` move · `Enter` open |
| **Article list** | `↑ ↓` move **and open** · `j` `k` next / previous |
| **An article** | `o` or `Enter` open original · `l` like · `d` dislike · `t` read later · `U` mark unread · `w` focus mode · `Ctrl-Enter` save the note now |

Two rules that are easy to get wrong and were:

- **Escape is handled before the is-typing guard.** Otherwise the guard swallows it and the only way
  out of the search box is the mouse, which in a keyboard-first app is the same as no way out.
- **The list's arrows open as they move.** A reader moving through a list is reading it; arrows that
  only moved focus while `j`/`k` opened would be two behaviours for one gesture.

**The shortcut sheet (`?`) is grouped by *where* a key works**, not alphabetically, because that is
the question a reader actually has.

## 6. The command palette

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.14 |

`Ctrl-K`. **It is not a second search box.** Search queries article text and costs a round trip; the
palette matches what the client already holds — feeds, tags, streams, commands — and answers on the
keystroke.

Ranking is three tiers: whole-label prefix, then word prefix, then substring, shorter labels first.
**Deliberately not fuzzy** — subsequence scoring makes almost everything match almost everything at
151 feeds, and a list that never narrows is worse than no palette.

Results are labelled by kind (Feed · Tag · Stream · Command) and carry a hint (unread count, feed
count). Commands available: refresh · mark all read · toggle unread-only · toggle feeds-with-unread ·
listen to this article · save for later · mark unread · like · dislike · open the original · reduce
motion · change the theme · switch directly to any named theme.

Its commands call the same handlers the chips do, so it cannot drift into a second implementation of
the same verb.

## 7. Dialogs

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.23 |

Six overlays — palette, shortcut sheet, per-feed panel, tag panel, add-a-feed, category editor.

**All six animate in both directions.** They used to return nothing when closed, which is why they
could only ever animate one way: an element unmounted the instant it closes has nothing left to
animate. The scrim now renders at all times and carries an open flag; **arriving takes its time
(0.18s/0.3s) and leaving is brisk (0.11s/0.18s)**, because a dialog that lingers on dismissal feels
like the application is reluctant to let go.

A closed dialog is `visibility: hidden`, so it is out of the accessibility tree and out of the tab
order.

**The status banner leaves too** — it collapses to its own content height rather than cutting. That
matters because it carries the **Undo** after a bulk mark: it is on screen at the exact moment a
reader is deciding whether they meant it.

---

# Part II — Organising what you read

## 8. Adding a feed

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.18, §11 |

A dialog, opened from the foot of the rail. **Three fields and no more:**

- **Feed address** — the address of the feed itself
- **Category** — where it sits in the sidebar, with a **＋ New** inline to create one without
  leaving
- **Name** — your own name for it; blank uses the publisher's

Poll interval, cache depth and mute belong to a feed you already *have* and have formed an opinion
about — they live in the per-feed panel. Putting them here would make adding a feed a configuration
exercise.

**The dialog replaced a URL box pinned to the rail's foot.** The box was the fastest possible path
for the one thing it could do — paste and Enter — with no room for the other two decisions a reader
makes when they add a feed. Both were reachable only afterwards, from a different panel, on a row
they then had to find. The cost is one click on the fast path.

## 9. The discovery ladder — when the address is not a feed

| | |
|---|---|
| **Status** | ◧ — rungs 1, 2 and 5 shipped; rungs 3, 4 and 6 owed |
| **Spec** | §11, §11.1–11.2c, `FLOWS.md` §6 |

Paste any page address. The dialog says *"Looking for a feed…"* and climbs:

| Rung | What it does | State |
|---|---|---|
| **1** | Reads `<link rel="alternate">` out of the page head | ✅ |
| **2** | Probes six common paths (`/feed`, `/rss.xml`, `/atom.xml`, …), and only when the page declared nothing | ✅ |
| **3** | Platform rules — YouTube, Reddit, GitHub, Substack, Mastodon | ○ |
| **4** | An LLM proposes feed candidates for the tail | ○ |
| **5** | **No feed exists → Smart+ follow reads the page and writes a rule** | ✅ |
| **6** | Still nothing → offer newsletter subscription | ○ (M22) |

**Every candidate is fetched and parsed before it is offered.** A declaration pointing at a 404 is
never shown. Each is labelled with how it was found — *"the page links to it"* versus *"found by
trying a common address"* — because those are different claims and the reader is choosing between
them. Each shows its title and item count.

### 9a. Smart+ follow — the model writes a scrape rule

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §11.2, §11.2a, §11.2b(i) |

When no feed exists, a lamp in the dialog offers **Smart+ follow**. The copy states the egress
before anything is sent: *"it sends the page's structure — its tags and classes, not its text — to
OpenAI, which works out the rule for following it."*

What the reader sees: *"Reading the page…"*, then **the extracted articles themselves**, the count,
the model's one-line note, and the rule. There is deliberately **no confidence score** — a number a
model assigns to its own answer is not evidence, and five real headlines pulled off the page are.
Confirming reports a receipt: *"Now following Example · 11 articles."*

**Nothing the model says is trusted.** Proposed selectors are compiled and run against the page
first, and refused when nothing matches, when no container yields a link, when only one item comes
out (it found the hero post, not the list), or when every item shares a title (the title selector
reached outside the container). One retry with the specific failure as input, then it gives up —
a third is spending money on the same guess.

**robots.txt is checked before the model, not after.** Asking permission after spending the request
is asking rhetorically.

**Client-rendered pages are recognised and named, not retried.** A page that is an app shell with
"Loading" in it reports *"This page builds itself in your browser and does not publish its entries
anywhere we can reach."* A model handed an app shell does not come back empty-handed — it finds the
most list-shaped thing in the markup, which is the navigation, and proposes selectors for it.

**And then it follows the data instead.** If the site fetches its entries from its own JSON address,
that address is discovered (four GETs, not a crawl), its shape is sent, and a second dialect of rule
reads named fields out of it. The reader is told plainly: *"This page loads its entries from its own
address, and that is what Smart+ follow found."*

Two consent conditions are checked at the RPC and neither implies the other: the per-user
`smart.subscribe` switch is on, **and** the request carried the flag the button sets.

### 9b. Two refusals with their own copy

- **No key**: *"This server has no OpenAI key. Whoever runs it adds one in Settings → Smart+."*
- **robots.txt**: *"This site's robots.txt asks us not to read this page, so we won't."*

## 10. Categories (folders)

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §6.10, A37 |

Per-user, **flat**, one category per subscription. Create · rename · delete · file a feed. Reached
from the rail's Categories band or from the add-a-feed dialog.

Four behaviours that each close a way this goes wrong:

- **Creating a name that already exists returns the existing one**, matched case-insensitively,
  rather than erroring. The call comes from the add-a-feed form, where "Tech" and "tech" are one
  intent and an error is a dead end mid-task.
- **Deleting a category unfiles its feeds and unsubscribes nothing.** The confirmation says the
  number: *"Deleting it moves 4 feeds to Unfiled. Nothing is unsubscribed."* Deleting a shelf is not
  deleting the books.
- **Renaming is allowed** — unlike a tag — because nothing refers to a folder by name.
- **Deletion is press-again-to-confirm**, in place, rather than a modal.

Names cap at 48 characters (the width the rail draws before ellipsising) and 200 per user. A control
that silently truncates is worse than one that says no.

## 11. Tags

| | |
|---|---|
| **Status** | ◧ — feed-level shipped; item-level is engine-only |
| **Spec** | §6.6, A21, A38 |

**Per-user labels, created on first use.** Nobody wants to manage a taxonomy; they want to label the
thing in front of them. The last association removes the tag, so the list stays a list of things
actually in use.

**Today they attach to a *subscription*.** Item-level tags (`item_tags`) — which is what A21 is
actually for, and what the rules engine and the sync API need — exist in the store and have no UI.

### 11a. A tag's name and a tag's row are two different things

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | A38 |

- **The tag** — `rust`. What you type, what `SetFeedTag` takes, what the chips under an article say.
  **Nothing renames it.**
- **The row** — "Systems programming ⚛". What the rail draws. Renameable, and a **mark** chosen from
  a fifty-glyph catalogue in seven groups.

The panel's copy works entirely to keep those straight, because getting it backwards is the one way
this feature does damage: a reader who believes they renamed the tag will go looking for the new word
in the tag field and not find it.

The glyphs are **text presentation only**, so they inherit the row's colour and weight — a rail of
emoji reads as stickers stuck onto the design. The **character is stored, not an index** into the
list: an index is a promise never to reorder, and breaking it silently changes every reader's tags
with nothing in the data to show it happened. Each glyph carries a translated name as its accessible
label, which matters most to exactly the readers who cannot tell ◆ from ◈ at 13px.

There is no `GetTagSettings` — the panel opens with its content rather than a spinner.

## 12. Per-feed settings

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.15, A33 |

A gear on the rail row — **hidden until hover or keyboard focus**, and always visible below 900px
where there is no hover to depend on. 151 gears down a sidebar is a column of hardware competing with
the one thing the rail is for.

**The panel is grouped by who a setting belongs to**, which is the global-source design made visible:

| Group | Changing it affects |
|---|---|
| **Yours** — name override, in-the-ranked-feed, mute, keep-offline depth, tags | you |
| **Shared** — feed URL, website, fetch interval | **every subscriber on this server** |
| **Health** — last fetch, last success, next fetch, items held, consecutive failures, the publisher's error verbatim | read-only |
| **Actions** — fetch now, mark all read, unsubscribe | — |

**The shared group's warning is its heading, not a footnote beneath it**, and it names the number:
*"3 other people on this server read this feed. Changing these changes them for everyone."* Someone
changing a poll interval should read why before they change it.

Both tables are written in **one transaction**. A panel that renamed the subscription and then failed
to set the interval would leave the reader looking at a form where half of what they submitted took
effect, with no indication which half.

**Fetch interval is clamped at the write** to 5 minutes – 1 week, and the next fetch is recomputed
from the *last* one rather than from now — lengthening an interval must not postpone a poll that is
already overdue. The floor is politeness rather than performance: the column is global, so one user
setting ten seconds makes this server hammer a publisher on everyone's behalf.

**Unsubscribing says what survives**: *"Unsubscribing removes it from your sidebar. The articles stay
on the server."*

> **Known gap:** *Keep offline* (0 / 25 / 100 / 500) is stored and honoured by nothing — offline trip
> packs are §31 below, and unbuilt. The control is currently a stated preference with no consumer.

## 13. Refresh

| | |
|---|---|
| **Status** | ✅ |

The Refresh chip refreshes **the selected feed**, not all 151. Refreshing 150 sources because someone
wanted one checked is rude to 149 publishers and slow for the person waiting. *Fetch every feed now*
exists on the Settings → Feeds tab for when that is genuinely what you want.

The result is a receipt, not a spinner disappearing: *"Checked 12 feeds · 3 new · 1 failed."* Each
clause is its own plural message.

## 14. Mark all read, and undo

| | |
|---|---|
| **Status** | ✅ |

Marks the current scope and reports the count: *"Marked 143 read"*, with **Undo** in the banner. The
undo is a real server-side batch, not a client replay — *"Put 143 back as unread."*

**A bulk mark never counts against a feed's ranking.** The Reading tab says so out loud: giving up on
a backlog is not a verdict on the publisher. This is the sharpest risk in the whole interest layer —
one careless `A` reads as 143 informed rejections and destroys weeks of signal silently, detectable
only as the homepage slowly getting worse.

## 15. Search

| | |
|---|---|
| **Status** | ◧ — items only |
| **Spec** | §5.10 |

`/` or the search field. Full-text over SQLite FTS5 with porter stemming, not a `LIKE` query
pretending. Results are ordered by relevance and the subtitle says so.

Notes and bookmark archives have their own FTS indexes and repository methods (`SearchNotes`,
`SearchBookmarks`) built and **not wired to any UI**. They are deliberately separate searches rather
than one federated list: the corpora answer different questions, and a merged result would have to
invent a ranking between them, which buries the notes — the rarer and more valuable hit.

---

# Part III — Signals: what you tell the reader about what you read

## 16. Verdicts — like and dislike

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | A27, §18.1 |

`l` / `d`, or the ▲ / ▼ chips. One signed value, because like and dislike are mutually exclusive by
definition and a pair of flags makes "liked AND disliked" representable — a state every consumer then
needs a policy for.

**Pressing the verdict an item already has clears it**, because the only other way back from a
mis-click is to assert the opposite, which is a lie about what you think.

**The negative half is the reason this exists.** Starring answers "keep this", and a reader who never
stars still has opinions. Knowing which sources reliably waste ten minutes is the signal the ranking
layer most needs, and before verdicts there was nowhere to record it.

There is a **Liked** stream. There is deliberately **no Disliked stream** — a list of things you
decided were not worth your time is not somewhere anyone goes — though the palette can still reach
the scope, and the list header names it if you get there.

## 17. Read later, and mark unread

| | |
|---|---|
| **Status** | ✅ |

`t` saves an article for later; there is a **Read later** stream. `U` marks unread — without which a
stream that marks things read as you scroll past them has no way back.

## 18. Notes

| | |
|---|---|
| **Status** | ✅ |

Covered in §4b. They live in their own table rather than in the read-state row: read/star is a flag
the reading loop writes constantly, a note is prose written rarely and read deliberately, and **a
note must survive anything that resets read state**.

**The Notes stream orders by when the *note* changed**, not when the article was published — it is a
list of your own writing, and you look for it by when you wrote it. It reloads when a note appears or
disappears, never on every autosave, or the list would reshuffle under someone still writing in it.

## 19. The signals layer

| | |
|---|---|
| **Status** | ⚙ — collection shipped, derivation shipped, nothing consumes it in the UI |
| **Spec** | §18.1, §18.1a, A34, A35 |

Twenty-five signal kinds are collected as the reader reads, invisibly. The vocabulary lives in
`internal/signals` and a kind not in it is rejected at the service boundary.

Nine of these observe something nothing else can:

| Kind | What only it sees |
|---|---|
| `searched` / `search_opened` | A term the reader **volunteered** — no inference at all |
| `chose` | Opening row 7 of 12 rejects the other 11 **simultaneously**; carries position |
| `reread` | Scrolled back **up** to re-read a paragraph |
| `clicked_out` | Followed through to the publisher: the excerpt was not enough |
| `selected` | Selected text — **length only, never content** |
| `listened` | Fraction of speech heard; immune to the backgrounded-tab problem |
| `note_abandoned` | Started composing and left |
| `tagged` | Hand-applied labels are supervised data for otherwise-unsupervised clustering |
| `sync_read` | A third-party client auto-marking; neutral, like a bulk read |

**Three things the naive implementation gets wrong, and this one does not:**

1. **"Left in under 15 seconds" is wrong as an absolute.** Fifteen seconds is most of a link post and
   a rounding error on a 4,000-word essay. Dwell is normalised against expected reading time and
   classified as a *ratio*.
2. **Completion is forgeable.** Flinging the scrollbar to the bottom reaches the end without reading
   a word; a completion at over 3× a plausible reading speed is demoted to a skim.
3. **Dwell without an attention gate is not a measurement.** An article left open in a background tab
   overnight produces an eight-hour dwell. Only attentive time is banked — gated on visibility, focus
   and 60s of silence, generously, because the dangerous error is the opposite one.

**Analytics may never degrade reading.** This is the one RPC whose failure the client swallows
entirely: short timeout, no effect on the connection indicator, every error path drops data and
continues. The worst acceptable outcome of a broken signals layer is a worse ranking; it is never a
page that will not load.

**The outbox** batches, coalesces and preserves order, ships at 25 events or on a tick, keeps a
failed batch in order for the next attempt, bounds the backlog at 500 (dropping the *oldest* — recent
signal is worth more), and flushes on both `pagehide` and a hidden `visibilitychange`, because
neither fires reliably alone.

---

# Part IV — Listening

## 20. Read any article aloud

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §10.7, A31 |

Two engines behind one control, and the difference between them is an **egress boundary** rather than
a quality setting.

**Free and always on — the browser's own synthesiser.** Installed, offline, no key, no cost, and it
uses the voice the reader already chose system-wide. The text comes from the rendered article rather
than from stored HTML, so what is spoken is exactly what is on screen.

> Chunked into ~220-character sentences, and that is not an optimisation: a single long utterance
> hits a long-standing Chrome bug where it stops after roughly fifteen seconds with no error and no
> event, so the article simply goes quiet mid-way. Many short utterances sidestep it, and they also
> make pause and stop responsive.

**Smart+ voice — OpenAI, opt-in, per reader.** The toggle sits **next to the play button**, not in
settings: it is an egress decision, and the reader should be able to see its state at the moment they
press play. Four gates, each answering with a different status because they are different problems —
not signed in, item not visible to you, the server has no key, or you have not opted in. Only the
last is one the reader can fix, and its copy says so.

**One article is paid for once.** Cached on disk by item + model + voice, forever — article text is
immutable, so an expiry could only ever be a schedule for re-buying identical audio. Concurrent
presses of play collapse onto a single paid call, and a synthesis already paid for finishes onto the
cache even if the reader navigates away.

**A floating transport appears when the article being played scrolls out of sight** — *"Back to
what's playing"* — so you can keep reading elsewhere without losing the control.

## 21. Summarise before reading

| | |
|---|---|
| **Status** | ✅ |

A Smart+ option beside the voice: turns a long article into about a minute of spoken summary instead
of reading the whole thing. A second request to OpenAI, charged once per article and then cached
forever. Named for what it produces rather than for the machinery — *"Summarise before reading"* says
what you get; "LLM summarisation" says what we built.

## 22. Keep playing

| | |
|---|---|
| **Status** | ✅ |

When an article finishes, mark it read and start the next one down the list. Stops at the end of what
is loaded, and says so — *"That's the end of the list"* is the queue working, not an error. Works
with either voice. Called "Keep playing" rather than autoplay, which is a word people associate with
something being done to them.

---

# Part V — How it looks, and how it moves

## 23. Themes

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.16, A39 |

**Five themes, and a theme is a set of variable values rather than a stylesheet.** Every colour, face
and metric the sheet paints with is a custom property, decided in one place. A class-per-theme
approach multiplies the sheet by the number of themes; a variable costs one declaration however many
there are.

| Theme | What it is for |
|---|---|
| **Fanciful** | The house plum. Warm, low-contrast, made for evenings |
| **Ink** | Near-black and cold. The one that holds up in a bright room |
| **Ledger** | Sepia and lamplight. Long sessions, no blue |
| **Daylight** | Paper. For reading at a desk with the blinds open |
| **Contrast** | Maximum legibility. Black ground, white type, hard edges |

Each theme card on the Appearance tab **is drawn in its own colours** — what you see is what the
reader becomes.

**Seven accents**, which are the source hues reused as interface colour, with a **separate light set**
taken down to where they can carry white. Plus *"The theme's own"*, which is not the same as any of
the seven: every stored value keeps **"unset" distinguishable from "set to the default"**, which is
what lets the screen offer a way back to following the theme or the system, and what stops a reader
who never opened it from having a preference invented for them.

**Three reading sizes, not a slider.** Only the article column moves, and the line stays 66
characters — larger type gives a *narrower column* rather than a longer line. The control shows its
own effect: the specimen under it is set in the reading face at the chosen size.

**Switching themes costs a paint, not a re-render.** Token values are written onto the root element's
inline style, which outranks the sheet's `:root` block, so no component re-renders — which is what
makes it instant with 151 rail rows and 3,600 virtualised items on screen.

**Every paintable value is a token, enforced.** The stylesheet is asserted against *as emitted*: no
dangling variable, no token a theme cannot reach, no literal colour outside a theme, and a
readability floor at 4.5:1 against all three grounds a row can land on — the page, a hovered row, and
the selected row a reader sits on for as long as they are reading.

## 24. Motion

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.16, §20.16.1 |

**Every duration is gated by a multiplier token.** 1 is on, **0 is off**, and off means the
transition is *absent* — arriving at exactly the state it would have animated to — rather than
suppressed. What that replaces is a blanket "no transitions" rule at the bottom of the sheet, which
is a broom: it works until someone writes a transition with `!important`, in a later layer, or on a
pseudo-element, and nothing fails loudly when it stops working.

Three settings: **Full** · **Reduced** · **Follow my system setting**, and the screen tells you what
your machine is currently asking for.

**Three durations and four gestures**, and the vocabulary is ink and light rather than bouncing
cards, because this reader's world is print:

- **warm** — hover changes a colour and nothing moves. In a rail of 151 rows, geometry that shifts on
  hover is seasick.
- **mark** — a rule draws itself from its centre, the one place with a hair of overshoot, because it
  is the most-repeated gesture in the app.
- **arrive** — fade up seven pixels, decelerating, no overshoot.
- **breathe** — slow, low-amplitude, never blinking.

**Three things the motion is actually spent on:**

1. **The selection travels.** The item list's highlight is one cursor drawn on the scroll container
   and moved, not a background lighting on one row and going out on another — that is two events
   where the reader made one gesture. This is the most-repeated interaction in the application
   (`j j j k`), so it is the one worth the most care.
2. **Spawning animates the arrival of *data*, not of an element on screen.** The list is virtualised,
   so a plain mount animation would fire on every row that scrolls past — a slot machine at any real
   speed. Only genuinely new ids animate, which is what keeps **the list still under the reader's
   hand on `j`**.
3. **The waits say they are still working.** The three moments that used to be bare text — more
   items, the next article, a bulk operation — carry an indeterminate hairline in the accent. Not a
   spinner: the note's save mark is already a spinner meaning something else.

**Looping animations are gated on amplitude, not duration**, because a zero-duration infinite
animation is a spec corner browsers disagree about, and "the skeleton froze mid-shimmer at a visibly
wrong offset" is a bug that would appear *only* for the readers who asked for less motion.

## 25. Focus mode

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.21 |

`w`, or the control pinned to the top right of the article. The reading pane takes the window.

**The columns close; they do not vanish.** Those two panes *are* the navigation, and something that
disappears with no transit leaves the reader unsure whether it was hidden or lost. The direction of
the motion is the explanation.

**Full width is the means, not the point.** A 66-character column pinned to the left of a 1900px
window is worse than the three-pane layout it replaced — a stripe of text and an acre of nothing. The
article recentres, and the nav, the note and the seams move with it.

The control is **the one piece of bespoke iconography in the application** — four corner brackets
that pull in and spring out, at 16px. Everything else is a text glyph, deliberately: a row of drawn
icons is a toolbar and this reader is not one. This earns the exception because its meaning *is* a
geometry that no character says as plainly.

It is a **persisted preference**, which is only safe because the way out never leaves the screen: the
control stays pinned, and `Escape` peels focus mode first.

## 26. The filmstrip, and the phone

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.11, §20.22 |

Below 1220px the panes become a **strip** — rail, list, article, in the order they are already in —
so the direction of travel carries the meaning without anyone being taught it: **deeper moves left,
back moves right**, the same as a stack of paper. Before this they were hard cuts, and two panes
swapping instantly is indistinguishable from the application having been replaced.

Because they slide rather than disappear, the panes **keep their scroll positions across a switch**,
which is the difference between returning to the list where you left it and returning to the top of
it.

**Below 900px there is a four-tab bar**: Read · Feeds · Notes · Settings. It is a grid row rather
than a fixed overlay, so the panes are shorter by exactly the bar's height — a fixed bar sits on top
of the last list row, and the row underneath it is the one that can never be tapped. The home
indicator is cleared with a safe-area inset, and targets are 48px.

**Settings is phone-only in that bar** and holds the switches that live in the list header on a wide
screen, plus the item counts that explain the scrollbar's behaviour.

## 27. The splash

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.20 |

The module is six megabytes gzipped, and a wordmark sitting on a dark screen for eight seconds is
indistinguishable from a hang.

**Real byte progress, not a spinner.** The bytes are counted on the way in while streaming
compilation is preserved. The declared length is treated as a *hint*: the moment the count passes it,
the bar drops the percentage and roams, because *"4.1 MB downloaded"* beats a confident wrong number,
and a bar that sails past 100% is the confident wrong number this avoids.

**It wears the reader's theme.** Themes live on the server, so the splash cannot know one — the
transport is the thing that is loading. So four colours are mirrored into local storage purely so
this one frame can be right. Otherwise a Daylight reader gets a dark flash on a bright screen on
every load, which is the one flash a splash exists to prevent.

**The design is the product's own.** The progress fill is a gradient through the seven source hues,
in order, because the idea this whole reader rests on is that every source owns a hue — the bar is
made of the thing it is loading. The plate is delayed 120ms, because a warm cache boots well under
that and a wordmark that appears and vanishes inside the window is worse than never showing one.

## 28. Skeletons, not spinners

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.8 |

Every pane that fetches renders a **skeleton shaped like the thing it is waiting for** — a skeleton
row is the same height as a real one, a skeleton image reserves its aspect ratio, a skeleton
paragraph is shaped like prose. A placeholder that resolves to a different size is a layout shift
with extra steps, and a spinner says "wait" where a skeleton says "here is what is coming and where
it will be".

**Three in-flight flags, not one** — feeds, items, article. They belong to three panes that finish at
different times, and a single busy flag makes the whole screen go pending whenever any part of it is.

## 29. Interface language

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §22.16, §22.16a, §10.5a |

Every string in the UI goes through a catalog — **zero hardcoded copy in the view layer, enforced by
a structural guard.**

**The language picker lives on the Smart+ tab, not under Appearance**, and that is deliberate:
choosing a language *spends the key*. A picker that looked like a free preference, three tabs from
the thing that pays for it, is a bill nobody expected. The screen labels each language **ready**
(cached, free) or **will translate** (a call).

A language you have used before is free — the translation is cached on the server, keyed by a hash of
the English, so a build that edits one string re-translates it and a build that does not is free
forever. **Retranslate** exists as a repair action for when something reads wrong.

Switching language re-renders; it does not reload — except a *forced* retranslation, which does, and
says so.

Four surfaces that a naive extraction misses and this one covers: relative timestamps (Go's date
layouts are not locale-aware and never will be), the fifty glyph names, the five splash strings shown
*before* the module exists, and **gRPC status messages** — the server sends a key plus arguments
alongside the English, because the language is a per-device choice the server never sees.

Deliberately **not** translated: feed content (that is a different machine), and gRPC's own transport
text inside an error interpolation — paraphrasing the actionable half helps nobody.

---

# Part VI — Seeing the page itself

## 30. The render ladder

| | |
|---|---|
| **Status** | ◧ — three rungs of four exist; the controller does not |
| **Spec** | §10.1, §10.1-R, §10.1a–d |

The motivating case, stated plainly: **you are reading from a network that blocks the origin.** The
server is not on that network and can reach what you cannot. So every tier above the feed's own
content is a *reachability* feature and not only a *readability* one.

| Rung | What you get | State |
|---|---|---|
| **The real page** | Perfect — it *is* the page | ✅ (Open original) |
| **Live view** — a browser on the server, streamed as frames | Near-perfect: every script runs. A picture, so you cannot select or search it | ◧ view-only |
| **Page** — fetched, rewritten, sanitised, served from your server | Visual but frozen: no accordions, no tabs, no carousels | ✅ |
| **Reader text** — extraction, plus proxied images | The words and the pictures | ✅ |

**The ordering principle is fidelity first, degrade only when a constraint forces it.** Each step
down is a named constraint being hit, not a preference.

### 30a. Images through the server

| | |
|---|---|
| **Status** | ✅ |

Article images are re-served from your own server. Invisible when it works, and it repairs the case
where the words arrive and the pictures hang — which on a hardware review is most of the article.

Default on, per-reader opt-out. **Newsletters are exempt and must stay exempt**: the newsletter
sanitiser drops remote images outright, and proxying a tracking pixel still tells the sender you
opened the mail — only the source IP changes.

### 30b. View page

| | |
|---|---|
| **Status** | ✅ |

Two controls on the article's chip row:

- **View page** — the publisher's page in a sandboxed frame in the reading column, fetched by your
  server, scripts removed, assets rewritten to come through your server too.
- **Full width** — the same page in a new tab, where the browser gives it the whole window.
  Implemented as a real link, not a click handler, so middle-click, ctrl-click and "open in new
  window" all work.

**Both are absent rather than disabled when the server has the page proxy off** — a disabled control
would advertise a feature the server refuses.

A side effect worth knowing: bytes re-served from your own origin carry the headers *you* set, so
**sites that refuse to be embedded stop refusing**. That is the blank-box problem this area is prone
to, solved as a consequence rather than as a goal.

> **Known gap:** external stylesheets do not survive the round trip yet — the asset endpoint
> allowlists images, so a page comes back with its inline styles only. Most sites keep their CSS in
> external files, so **most sites currently come back legible rather than faithful.**

### 30c. Live view

| | |
|---|---|
| **Status** | ◧ — view-only |

A real browser on your server, painting the page, streamed to you frame by frame. Chosen from a
two-position control beside **View page**.

The copy says what it is, in the mode it applies to: *"A picture of the page — you can't select or
search it."* A reader who picks Live and then cannot select a quote will conclude the feature is
broken rather than that it is a picture.

**What it will feel like, honestly:** a click travels client → server → browser → repaint → image →
back, which is 150–300ms in a realistic deployment. Clicking a link is fine. Scrolling is laggy.
Typing is unpleasant. This is a tool for *reaching* a page, not for living in one, and the UI does
not pretend otherwise.

**The connection is the session.** The browser tab on the server lives exactly as long as the
response: switching away or closing the tab kills it. No session table, no ids, no reaper.

> **Owed:** the input channel (so today it is genuinely view-only), the tile diff (every frame is
> currently a whole image), and the one-time explainer before first use — this mode has a very
> different traffic signature from reading text, and switching it on is a different act from picking a
> font.

> **Owed:** the ladder controller itself. Today this is two buttons and a two-position switch, not a
> ladder: there is no automatic escalation, no per-feed or global default, and no keyboard binding.
> Automatic escalation is blocked on a real probe — "the network blocked it", "DNS failed", "captive
> portal" and "plain offline" are one opaque error from the client, and a refused frame looks exactly
> like a loading one.

---

# Part VII — When the connection is not there

## 31. The connection is a state machine, not a dot

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.19, A40 |

**The indicator's one job: "silently disconnected" must never look like "a quiet news day."** The
content of a reader is *absence* — nothing new — and absence is exactly what a broken connection also
looks like.

Five states, because there are five remedies. Each says a word as well as showing a colour.

| State | Says | Offers |
|---|---|---|
| `live` | connected | — |
| `connecting` | connecting | — |
| `offline` | no network | — (the browser has already told us a retry would fail) |
| `down` | no server | **Retry · Ns** with a live countdown |
| `blocked` | action needed | **Sign in** or **Reload** |

**The indicator lags the transport, deliberately.** A server restart reconnects in about half a
second; painting "connecting" for it turns an invisible event into a visible flicker on every deploy.
So `connecting` is suppressed for the first second and `down` waits for either a failed redial or
three seconds. **In the other direction there is no hysteresis at all** — `blocked` and `live` paint
immediately, because delaying good news only confuses.

**The countdown is not decoration.** It is what lets the retry schedule cap at 20 seconds without
feeling like a hang: a reader who can *see* the wait and click through it does not experience the cap
as a freeze.

**A refused call is not a broken connection.** A "not found" from a perfectly healthy server does not
turn the indicator red. Only transport failures do; an expired session or a version mismatch is
*terminal* and stops the retry loop rather than grinding against a permanent refusal forever.

**Three events jump the queue**: the OS saying the network is back, the tab becoming visible, and a
back-forward-cache restore. Plus the **Retry now** button — the reader is looking at the countdown
and is more certain than we are.

**A hidden tab makes no promises, and a tab becoming visible verifies before it renders.** Browsers
throttle timers in a background tab to about one a minute; fighting that costs battery on a device
whose owner is not looking at the app, to buy accuracy nobody can observe.

## 32. Nothing is lost across an outage

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.19.8, §12.4 |

**Marks made while disconnected are kept.** Marking read, rating, saving for later, mark unread and
notes all queue and drain on reconnect, in order, with a receipt when enough is replayed to be worth
noticing: *"Saved 7 changes made while you were offline."*

The queue survives closing the tab.

> The rule this rests on: **a retry loop is a latency optimisation; an outbox is a durability
> guarantee.** They are not substitutes, and shipping a good retry loop makes the missing outbox
> *harder* to notice, not easier.

## 33. The last thing you saw is kept

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.19.8a |

When the transport fails, the last answer to each read is served from a bounded local cache, with a
badge: *"Showing what you last saw, 4 minutes ago — you're offline."*

**The age is in the line on purpose.** Four minutes and yesterday are the same word and very
different things to act on. It is styled as a statement rather than an alarm: nothing is broken, and
an alarm here would train readers to dismiss the one row that must not be dismissed.

Because the neighbouring articles are already prefetched, losing the connection mid-stream leaves you
able to keep going in **both** directions rather than hitting a wall on the next press of `j`.

**A read on a known-dead connection gives up after 4 seconds, not 20.** Waiting the full call
deadline and *then* failing is the worst possible ordering of those two events; four seconds contains
three or four whole reconnect attempts, so anything that was coming back has come back.

> **This is not trip packs and must not be described as them.** It is the last answer to each
> question you have *already asked*. It makes "go back to what I was reading" work on a plane. It does
> not make "read today's news" work on a plane.

## 34. Three operations refused rather than queued

| | |
|---|---|
| **Status** | ✅ |

Queueing is not always the kind answer. Each of these says why, in the same frame as the press:

| Operation | The message |
|---|---|
| **Refresh** | *"Can't fetch new articles while you're offline — the server does the fetching."* |
| **Add a feed** | *"Adding a feed needs the server to check it first."* |
| **Mark all read** | *"Marking everything read needs the server, so it can be undone."* |

None apologises or suggests retrying: the remedy is "wait", and an instruction the reader cannot
follow is worse than a fact they can. **This matters more than it looks**, because by the time a
reader meets one of these they have already watched three articles stay marked and will reasonably
assume everything is queued.

## 35. Where you were is account state

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.13, A30, §7.1c |

Scope, the open article, all three filters, both pane widths, theme, accent, reading size and motion
are **server-side preferences**. A reader who reloads lands back where they were, on any machine.

**They are fetched behind the splash, not after it.** A preference that decides the first frame
cannot be fetched after the first frame — otherwise the reader paints its defaults and snaps into the
saved state a round trip later, which reads as a flash to the past. The splash is already up and
already holding the screen, so both round trips happen there.

If the preference call fails, the default view still loads: losing your place is a small regression,
losing the feed is the app not working.

---

# Part VIII — The settings surface

## 36. One page, nine tabs

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §20.17 |

A page, not a modal — settings behind a dialog get skimmed, and several of these need a sentence to
explain themselves (what Smart+ sends where; what a poll interval does to a publisher).

**The last three are what a self-hosted application uniquely owes its operator**, and they are the
reason this is a surface rather than a preferences dialog: **nobody is tailing a log file behind this
and there is no dashboard.** The person running it is the person reading it.

| Tab | Answers | Contents |
|---|---|---|
| **Reading** | what the list shows me next | Articles: everything / unread only · Feeds in the sidebar: all / with unread · Mark read: when you scroll past / only when you open · the bulk-read-is-neutral disclaimer |
| **Appearance** | what it looks like | Five theme cards drawn in their own colours · seven accents plus "the theme's own" · three reading sizes with a live specimen · motion: full / reduced / follow the system |
| **Listening** | what it sounds like, and what leaves the machine | Browser voice (always available) · **Smart+ voice**, with its egress warning attached · **Summarise before reading** · **Keep playing** · the caching note |
| **Smart+** | what it costs | The OpenAI key (stored encrypted, never returned, last four shown) · the model · **tokens in / out / requests since the process started** · the interface-language picker |
| **Feeds** | my subscriptions as a whole | Feed count, unread, "N of M loaded" · **Fetch every feed now** · **Mark this list read** · a pointer to the per-feed gear |
| **Account** | who I am on this server | Signed in as · server · connection · **reconnects**, shown only once one has happened |
| **Server** | is it healthy | Version · commit · schema migration · uptime · database and WAL size · path · article/note/tag/rated/saved counts · poll interval · last successful fetch · heap, goroutines, GC |
| **Activity** | what just happened | The in-memory log ring, newest first, with a level filter |
| **Speed** | why is it slow | Per-RPC count, p50, p95, max, and a failing-calls section |

**Reconnects appear only after one has happened**, so their presence is itself information rather
than a "0" nobody reads. The count comes first: *one an hour is a network, forty is a bug*, and the
reader can only tell those apart by the number.

**The Smart+ spend meter is labelled as a signal, not a bill** — it resets when the process restarts,
and the screen says so rather than letting anyone mistake it for accounting.

> **Structural gap:** there is no settings *registry* yet. Preferences are a flat per-user key/value
> table and every control above is hand-built. That is affordable at twelve keys and is exactly what
> stops being affordable at ninety — the plan's registry (typed, with system → tenant → user
> resolution and *which layer supplied this value*) is unbuilt.

---

# Part IX — Accounts, security, and running the thing

## 37. Signing in

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §7.1a, §7.1b, A36 |

A username and a password. Sessions last 30 days — this is a reader opened on a phone at breakfast
and a laptop at night, and a weekly expiry is a recurring tax on the one interaction that has nothing
to do with reading.

**Three boot phases, and the middle one earns its place:**

| Phase | When | Why |
|---|---|---|
| `checking` | a token is stored and the server has not answered | Without it, a page with a *good* token paints the login screen for a few hundred milliseconds and then replaces it — **and that flash trains people to start typing a password they do not need** |
| `login` | no token, or the server rejected it | |
| `reader` | the server confirmed the identity | |

**An unauthenticated page never constructs the reader.** Rendering it behind a login overlay would
fetch a feed list the caller is not entitled to, collect thirty rejections, and paint the furniture of
an account nobody has proven they own.

**On a loopback origin the login screen prefills both fields** with the documented development
credentials, so it is one click. A deployed instance never prefills — and the check parses the origin
as a *host*, because a substring test for "localhost" would prefill on a domain somebody else can
own.

**Every login failure says the same thing, and takes the same time.** An unknown username is hashed
against a real decoy, because a uniform error message alone does not close a timing oracle — the work
has to actually happen.

## 38. Failed logins, lockout, and recovery

| | |
|---|---|
| **Status** | ◧ — enforced at the server; no recovery UI |
| **Spec** | §7.1–7.3, §7.1a |

**The lockout is a table, not a bigger rate limiter.** Failures are counted in the database since the
account's last *successful* login, so a restart no longer hands an attacker a fresh budget — and the
correct password is refused *during* a lockout, because one that lets it through is one an attacker
walks past on the guess that happens to be right. A username that does not exist locks out on
identical terms, so the lockout is not an account-existence oracle.

**The curve doubles and then stops**, and the cap is the security decision rather than the doubling:
an uncapped lockout is a denial of service against the account *owner* that any stranger can trigger.
Three free, then doubling to a fifteen-minute ceiling — **14 guesses in the first hour and 4 an hour
after**.

**Passwords are 12 characters minimum with no composition rules.** Length is the only property that
reliably costs an attacker anything; "must contain a symbol" reliably costs the user a password they
write down somewhere worse. They are checked against a bundled known-password list that **folds**
candidates — case, leet substitutions, trailing digits and punctuation — so a decorated variant is
refused by the same entry as the plain word.

**Recovery codes** are Crockford base32 — no I, L, O or U — because these get written on paper and
typed back months later by somebody already locked out and already annoyed, with nobody to file a
support request with. Any case, any grouping, and the commonly-confused letters map back.

**Refresh tokens are single-use and bound to a device family.** Presenting a spent one means a replay
or a theft, and since the server cannot tell those apart it revokes the **whole family**.

> **Owed:** the recovery *screens*. Codes, admin-minted reset tokens and sudo-mode all exist at the
> server and repository layer with no UI in front of them, and there is **no sign-out button** —
> clearing local storage is the current logout.

## 39. What the server refuses to do

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §21, §22.3, §22.4 |

- **It refuses to start** without an account, without a built client, or with an unwritable data
  directory — and reports **all** the failures at once, because someone setting up a droplet usually
  has more than one wrong and a one-at-a-time boot loop is a miserable way to find that out.
- **It refuses to run the no-login development mode on any bind but loopback**, and refuses it
  alongside a reverse-proxy flag. A bind address describes network topology and cannot tell you who is
  on the other end of a connection.
- **It says which posture it is in at boot** — whether a password is required, and what stands
  between the socket and the internet. Development mode says it at warning level, because that line
  describes a server owned by anyone who can reach the port.
- **Every outbound fetch goes through an SSRF guard.** Link-local and the cloud metadata endpoint are
  never reachable, on any configuration.
- **A capability a client asks for that has no entry fails closed.** A new RPC is unreachable until
  deliberately granted.

## 40. The operator command line

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | §22.3, §22.5, §7.2 |

`init` (creates the first tenant and superadmin; **refuses to run twice** — on a live box that is
nearly always somebody re-running the setup steps) · `adduser` · `passwd` (break-glass; revokes every
session) · `migrate` · `backup` · `seed` · `poll` · `version`.

Passwords resolve flag → environment → terminal prompt, so one never has to appear in a process
listing or a shell history.

**Backups are `VACUUM INTO` plus an integrity check**, written to a partial file and renamed. A `cp`
of a live write-ahead-log database produces a file that opens cleanly, passes a smoke test, and is
missing a transaction — which is the worst kind of broken.

## 41. Health, readiness, and version skew

| | |
|---|---|
| **Status** | ✅ |

Two endpoints and the difference is the entire point: **liveness never touches the database** (a
probe that fails on a slow query gets the process killed and restarted into the same slow query),
while **readiness** does, under a timeout. Both are unauthenticated and therefore deliberately
information-free — a status code and one word.

**The gRPC upgrade is readiness-gated**, so a client cannot connect successfully into an instance
that cannot serve reads and then fail every call.

**Shutdown closes cleanly** with a going-away frame, so a deploy does not sever a live tunnel
mid-call and roll back somebody's mark-read.

**A client too old for the server is refused with a distinguishable status**, which the client
classifies as terminal — it stops retrying and offers **Reload** rather than grinding against a
permanent refusal forever. The cached service worker makes this inevitable rather than hypothetical.

## 42. Deployment

| | |
|---|---|
| **Status** | ◧ — written and reasoned; never run against a real droplet |
| **Spec** | `deploy/README.md` |

A hardened systemd unit (loopback bind, unprivileged user, strict filesystem protection), an nginx
site, a nightly verified-backup timer, and a runbook that takes a bare Ubuntu droplet to TLS in about
twenty minutes.

**The nginx `/grpc` block is the load-bearing part**: the default read timeout is 60 seconds and the
tunnel is idle whenever nobody is clicking, so the default severs it on a timer and the client
reconnects — which presents as *"the reader refreshes randomly"* rather than as a proxy
misconfiguration.

## 43. The demo

| | |
|---|---|
| **Status** | ✅ |
| **Spec** | `docs/DEMO.md` |

The real reader with an invented instance compiled into the same module, published as a static page.
No account, no server, nothing stored: the "server" is the browser tab, and closing it is the undo
button.

Everything a reader does works — marking, starring, rating, notes, tags, categories, feed settings,
search, subscribe, unsubscribe, mark-all-read and its undo. Three things need a server and each
**refuses through the same API the real server would**, so the UI explains it with copy it already
has: Smart+, translation, and the page proxy / speech.

---

# Part X — Built, with nothing in front of it

Each of these is tested Go with no RPC, no UI, or both. They are listed because **an untracked
capability is one nobody can decide to keep**, and because each is closer to shipping than its
milestone suggests.

| # | Capability | What exists | What is missing |
|---|---|---|---|
| 44 | **Rules engine** ⚙ | The pure matcher (every operator, ordering, stop-processing), per-subscriber fan-out as a queued job, mute as a reversible flag, rule-hit logging | Every screen: the list, the editor, the live preview, retroactive apply, undo, the `/muted` view |
| 45 | **Ranking and topics** ⚙ | The scorer with its reason list, volume penalty, per-source half-life, highlights-mode scoring, TF-IDF topic clustering, the derivation job | Any wire surface at all — there is no home service, no ranked stream, no tuning panel, no explanation line |
| 46 | **Recommendations** ⚙ | Outlink harvesting, aggregator pass-through, the health gate, evidence strings, scoring | The `/discover` surface, dismissal, trial subscriptions and their verdicts |
| 47 | **Preservation** ⚙ | Tiered archival, the distress sweep when a source starts failing, eviction that can never drop an archive whose origin is dead | The reader-facing banner (*"the original is gone; this is the copy saved on 12 March"*), the Wayback fallback, source lifecycle transitions |
| 48 | **Item-level tags** ⚙ | Store and repository | Every UI |
| 49 | **Mailboxes** ⚙ | Encrypted credential storage, scheduling columns, poller exclusion | The IMAP client itself, and any screen |
| 50 | **Settings registry** ⚙ | Typed registry with three-layer resolution that reports which layer supplied a value | The RPC and the self-rendering UI |
| 51 | **Capability authorization** ⚙ | A static per-method map that fails closed, plus a boot check that refuses to start on an unmapped RPC | Wiring into the interceptor. **Roles are stored and not enforced today.** |
| 52 | **Job queue** ⚙ | Durable, restart-survivable, per-kind concurrency caps | Any visibility |
| 53 | **Event ring buffers** ⚙ | Per-tenant, replay from a sequence, resync signalling | The streaming RPC and the client pump — live updates do not arrive today |
| 54 | **Disk degrade ladder** ⚙ | Four watermarks, shedding audio and packs first and keeping read state alive longest | A banner, and wiring the asset cache into it |
| 55 | **Import / export** ◧ | OPML both directions (a live 151-feed export imported through it), Netscape bookmarks both directions, Chrome JSON in | Any UI — there is no data tab |
| 56 | **Interchange formats** ✅ | RSS 0.91 / 1.0 / 2.0, Atom 0.3 / 1.0, JSON Feed, `content:encoded`, Dublin Core, media, iTunes namespaces, charset reconciliation, ~15 date layouts, a 27-fixture corpus | — |
| 57 | **Extraction** ✅ | Readability with sanitised HTML and plain text from one pass, refusing an implausibly short result rather than returning navigation as an article | The render-mode switcher that would let a reader choose it per feed |

---

# Part XI — Designed, not built

These have no code. Each entry is **the intended behaviour**, so the UX decision does not have to be
made again when the milestone arrives.

## 58. Bookmarks and archiving ○

Any URL, from any device, with an archived copy so links do not rot. `/b` all · `/b/unread` the
read-it-later queue · `/b/dead` **broken links** — which is the archive's moment: *"the site is gone,
the copy isn't."* A bookmarklet rather than an extension: zero install, no store review, no
per-browser build, and it works on a locked-down work machine.

The empty state on `/b` **is** the onboarding — it installs the bookmarklet.

## 59. Offline trip packs ○

*"Prepare for a trip"*: pick views, see an **estimated size** before committing, watch progress. Five
depths from metadata to full page snapshots. Read, star, tag and write notes with no connection; on
landing the queue drains.

**Notes are the one thing never overwritten.** A read flag losing a race is invisible and fine; a note
edited in two places keeps **both** and prompts, because notes cannot be regenerated.

Packs download over plain HTTPS rather than the tunnel, because a service worker cannot see WebSocket
frames.

## 60. Third-party app sync ○

A Google Reader–compatible API so Reeder, Unread, Fiery Feeds, NetNewsWire and FeedMe sync against
your instance. Mint a token in Settings → Apps, labelled per device, listed with last-used,
individually revocable.

**A token's scope is capped and never inherited.** A long-lived token pasted into a phone app is the
worst possible place for administrative capability to leak — no token can carry one, whatever its
owner's role.

**The invariant that makes it worth having: a read marked in Reeder moves the homepage in an open
browser tab.** Mutations go through the same service layer as the UI, so rules, signals and events all
fire identically.

## 61. Notifications ○

Web Push, **digest by default** — per-item push on a busy reader is unusable within a day. Quiet
hours stored as local wall-clock plus a timezone, so a 22:00–07:00 window does not shift by an hour
twice a year. A log so *"why did I get this?"* is answerable.

Suppressed entirely for a source suspected of flooding.

**On iPhone this does not exist until you add ArticleFlux to the home screen**, and the UI must say
that rather than offering a toggle that silently does nothing.

## 62. Trends and feed health ○

Items read over time · a time-of-day heatmap · top sources by open rate and time spent · unread
backlog growth · streaks. Per source: items per week, open rate, median dwell, and **where it ranks
against your other feeds** — the screen that tells you a feed you *feel* loyal to is one you actually
skip.

The actionable half is **feed health**: inactive · failing · **never-opened** (the least flattering
list in the app) · noisy. Every row one-click actionable, because a stats page you cannot act on is a
poster.

**It is also the feature-discovery surface.** A command palette only helps people who already know a
feature exists; the noisy-sources list is where *"mute this?"* should be offered.

## 63. The ranked homepage ○

One river, three slots: **Top** (~70%) · **Explore** (~20%) · **Clusters** (~10%, one card per
corroborated story).

**Explore targets under-served topics rather than sampling at random**, because pure affinity
converges to a monoculture and fails *invisibly* — the page still looks full.

**Cold start is honest**: recency plus round-robin, with a stated *"learning your reading"* state
rather than a confident wrong answer. Topics need roughly 50–100 engaged items to mean anything.

**Explainability is the product.** Every ranked item shows why — *"you open 84% of this source"*,
*"matches your NPU inference reading"*, *"3 other feeds carried this"* — because an unexplained ranker
feels arbitrary and **the reason is what makes feedback actionable**: *less like this — because of the
source* is a usable instruction where a bare thumbs-down is not.

Plus a tuning panel with a live preview of the reordering before saving, and — the honest one —
**a "what did you hide from me?" view**, because a filter you cannot audit is one you cannot trust.

## 64. Highlights mode ○

The problem: an aggregator posts 94 a day. You will never read it in full and you do not want to miss
the two that matter.

**You set the threshold as a rate, not a score.** *"About 3 a week from Hacker News"*, and the system
solves for the cutoff and re-fits as the feed's volume drifts. Nobody can reason about "score > 0.62";
everybody can reason about three a week.

**The app proposes the mode** from the noisy-sources list rather than expecting someone to find a
setting.

## 65. Topics you can correct ○

Rename · merge · split · **mark one "not an interest"**, which applies a strong negative across its
whole cluster. A model you can correct is a model you will trust; one that just asserts things about
you is one you will resent.

## 66. Recommending sites you do not follow ○

**Every recommendation shows its evidence, and the evidence is the whole product**: *"3 writers you
read linked here 11 times · you starred 4 of its articles via Hacker News · matches your NPU inference
topic · posts ~2/week."*

Candidates are rejected outright if they have no discoverable feed, have not posted in six months, or
post more than ~20 a day — recommending a dead site or a firehose is worse than recommending nothing.

**Trials close the loop.** Accepting subscribes for two weeks, items appear **only in Explore** so
nothing floods, and at the end the app grades its own recommendation: *"you opened 1 of 23 — drop
it?"*, defaulting to dropping.

Dismissals are remembered per domain and never re-suggested. One or two candidates are deliberately
**adjacent-but-different** and labelled as such, because a recommender optimised purely for similarity
is a filter-bubble engine with better manners.

## 67. Article translation ○

Per item, on demand, with the language detected from the item. Cached, and counted against the same
budget and the same circuit breaker as everything else Smart+.

## 68. Rules, from the reader's side ○

Match on title, author, content, URL, source, folder, tag, word count, age or language. Act:
mark read · star · tag · **mute** · set home weight · move to folder · stop processing.

**Preview against the last N items before saving** — a rule you cannot dry-run is a rule you are
afraid to write. Optional retroactive apply with a count, a confirmation, and undo.

**Mute is not delete.** A muted item is stored, flagged and excluded from lists and counts, and
`/muted` shows what a rule is actually eating. Rules report hit counts so you can tell a precise
filter from a bulldozer, and `"this rule hasn't matched in 6 months"` is how a filter set stays
trustworthy.

## 69. Newsletters ○

IMAP polling, not an inbound mail server — polling a mailbox you already own needs no port, no DNS
and no spam stack. Each sender becomes a source. **Newsletter HTML gets the strictest sanitisation in
the application**: pixels stripped, remote CSS killed, remote images dropped rather than proxied.

**A mailbox source is never global.** Keying one by sender address alone would merge two people's
private mail into one shared row.

## 70. Outbound webhooks ○

One HMAC-signed mechanism covers the whole integrations category, fired manually or by a rule, with
presets for the usual targets. Guarded exactly like a feed fetch — a webhook that can reach the cloud
metadata endpoint is a credential-exfiltration primitive.

## 71. Article revisions ○

Publishers silently edit, and the detection already runs — the revision write path has been active
since the first migration precisely so the feature does not launch with no history to show. The UI:
mark the item **"edited 2h ago"** and offer an inline diff. **An edit never resets read state.**

## 72. Screensaver ○

Fullscreen headline slideshow from any view. One headline in large type, source, favicon, relative
time, an optional image background with a scrim. Auto-advance at 12 seconds, arrows to move, space to
pause, Enter to open, Escape out. Ken Burns drift **only** when reduced motion is unset. A wake lock
where the browser has one, degrading to *"the screen may sleep"* rather than failing.

**It works offline from the trip pack** — and it is the most likely place you would notice a broken
offline path.

## 73. Sharing ○

**Folder sharing** with an enumerated contribute matrix — a contributor can add a source and remove
one *they* added; only the owner can remove someone else's, rename, delete, or re-share. Every share
is audit-logged, expirable and revocable **from both sides**, and there is a `/shared` surface so a
grantee can find what they were given.

**Public collections** publish as an Atom feed at an unguessable, rotatable address — Google Reader's
social layer, whose removal people still bring up. **Excerpt-only, permanently**: republishing someone
else's writing under your name is a licensing decision, not a feature.

## 74. The admin console ○

A capability-gated route, invisible without them. Tenants · users · health (poller lag, error
leaderboard, queue depth, storage by table, LLM spend and breaker state) · audit · shares.

**Every destructive action pairs with a preview that enumerates the blast radius before
confirmation** — *"3 other tenants lose access to 2 folders"*. **Impersonation is audited on entry
*and* exit with a persistent banner**; impersonation without a loud, logged trail is a backdoor.

## 75. Deleting a user, and deleting a tenant ○

Personal rows cascade. **Global rows never do** — sources and items survive, because they are shared
and one tenant's cleanup must not destroy another's history. Sources a leaver contributed to a shared
folder **reassign to the folder owner** rather than being removed. The audit trail is retained with
the actor anonymised: an audit trail you can erase by deleting the actor is not one.

Deletion is soft first, with a grace period. Accidental deletion of the only person who knows what
those 400 rules did is not recoverable from a nightly backup without losing everyone else's day.

---

# Part XII — The rules every screen obeys

Not features, but the reason the features feel consistent. Each is enforced by a test or a build
guard rather than by review.

| | Rule |
|---|---|
| **1** | **Every page owes four states** — loading (a skeleton, never a spinner on a list), empty (an invitation to act, never a shrug), error (what happened and what to do), offline (what is available). An app feels unfinished exactly where these were skipped. |
| **2** | **Undo toasts replace confirm dialogs** wherever the action is reversible. |
| **3** | **Colour is never the only carrier.** Every state says a word as well. |
| **4** | **A control the caller cannot use is absent, not present-then-erroring.** The "you can't do that" dialog never needs to exist. |
| **5** | **Optimistic where the id is ours, honest where it is the server's.** Marking read, rating and removing a tag apply instantly and roll back on failure; adding a tag shows a pending state, because the id is the server's to assign. |
| **6** | **Nothing that touches the network runs on the browser's event loop.** A blocking call there holds the loop the reply arrives on, so it cannot succeed — only time out, with the tab frozen for the full deadline. |
| **7** | **A click's payload is read on the click's own stack**, never from a shared reference a frame later. Otherwise two clicks in one frame make the first act on the second's target and the second act on nothing — silently. |
| **8** | **All CSS is Go, all logic is Go.** No stylesheet file exists and CI fails if one appears. The only JavaScript is the Go runtime shim, the boot shim, and the service worker — the first two because they run *before the module that emits the stylesheet exists*, the third because a worker is registered by URL as a JavaScript file. |
| **9** | **Browser APIs live in exactly one package**, so every other client package compiles and is testable natively. |
| **10** | **Cross-tenant access returns "not found", never "forbidden."** "Forbidden" on item 4711 confirms item 4711 exists, which is an isolation leak dressed as good manners. |
| **11** | **Every error carries a structured detail** so the client renders something useful rather than a toast saying "error", and the message is always safe to display — internal detail goes to the log with a request id. |
| **12** | **Pagination is keyset and bound to the query that produced it.** Paging with a cursor from a different sort or filter is an error, not silently-wrong results. |
| **13** | **The wire contract is additive-only**, enforced by a breaking-change check, because old clients exist in the wild. |
| **14** | **Nothing is settable that a registry does not declare** *(aspirational — see §36's gap)*. |
| **15** | **Responsive breakpoints ship in the same commit as the component.** A screen correct only at desktop width is not complete. |

---

# Part XIII — Known gaps and defects

Recorded because a gap that is only in someone's head is a gap nobody is assigned. Each is tracked in
`TODO.md` at the reference given.

## Measured 2026-07-27 — the tree is not green, and the docs said it was

Found by running the suite rather than by reading about it. `TODO.md` and this document both recorded
"38 packages passing"; that claim came from a run whose exit code had been swallowed by a pipe to
`tail`. The real state:

| | Finding |
|---|---|
| **A real defect, found and fixed inside one hour** | `TestIdleTunnelIsNotKicked` and `TestHalfOpenSocketIsDetected` (T21 b/c) failed in every run where the package built, each burning its full ~11.5s detection budget. The cause was visible in the log: `ws_upgrade_failed … error="websocket: response does not implement http.Hijacker"`. **A metrics middleware wrapped `ResponseWriter` without forwarding `Hijack()`, so every WebSocket upgrade on `/grpc` returned 500 and no tunnel could be established** — the pings had nothing to traverse, so both tests sat until they timed out. Fixed in `c0501ed`; both now pass (`ok internal/app 18.284s`). |
| **What that defect cost, and the test that was missing** | The failure surfaced as two tunnel tests timing out eleven seconds later, rather than as "the middleware broke the socket". A direct assertion — that the `ResponseWriter` reaching a handler through the full middleware chain still implements `http.Hijacker` and `http.Flusher` — fails instantly and names the cause. That test did not exist and is now being written. `Flusher` matters independently: `/stream` serves live-view frames as `multipart/x-mixed-replace` and cannot deliver them without it. |
| **The guards went red and green again inside two minutes** | A run at 03:09 showed `repository methods take Scope` failing on **19** methods with no `unscopedByDesign` entry, all in the newest code. All 19 were then verified individually as legitimately global — the login ledger and reset tokens precede identity, `ScopeForAPIToken`/`ScopeForDevice` *return* a Scope, `ScopesToDerive` produces the set of them, `RecordOutlinks` keys on globally-shared items, `ReconcileUnread` is a maintenance sweep. **No tenant-isolation hole.** Commit `749bd81` at 03:10:34 registered all 19 with written reasons, and a fresh `go run ./internal/tools/guards .` now exits 0 on all five. |
| **The `no SQL` guard hit was never real** | It came from `tq3/main.go`, an untracked scratch program at the repo root. The guard deliberately scans only git-tracked files, so a real run never saw it. Running a **stale `guards.exe`** from the repo root did. CI is unaffected — it runs `go run ./internal/tools/guards .` fresh. |
| **The full suite has a build cascade** | One run in three failed to build **17 packages** at once — every one of them downstream of `internal/store`. The shape of a single shared dependency failing, not seventeen problems. Intermittent; under investigation. |
| **CI would have caught all of it** | The workflow gates on `go build`, the wasm client build, native *and* wasm `go vet`, `-race` on `internal/jobs` and `internal/store`, staticcheck, govulncheck, the guards, `buf breaking`, and the size ratchet. It is well built. The failures above are therefore not a CI design gap. |

**The transferable lesson, and it caught me twice in one session:** `cmd | tail` reports the exit code of
`tail`. Any script or agent judging a suite through a pipe will report green over red. Capture the exit
code directly.

## What a testing campaign found, 2026-07-27

Sixteen real defects, thirteen fixed. Listed because the *distribution* is the useful part.

| Severity | Defect |
|---|---|
| **Unauthenticated DoS** | A crafted `Content-Type` on any subscribed feed crashed the whole server on the next poll. `strings.ToLower` is not length-preserving — a lone `0xEF` becomes a 3-byte U+FFFD — so an index found in the lowercased copy ran off the end of the original. Compounding it: **the poll goroutines had no `recover()` at all**, so any panic below them took the process down. Both fixed. |
| **Security** | `<base href="weird:">` was adopted as a resolution base without a scheme check, silently disabling *all* asset rewriting — sending the reader's browser and IP straight to the publisher and defeating the blocked-origin tier. Reachable by any hostile page. |
| **Two features dead** | A metrics middleware embedded `http.ResponseWriter` by value, which promotes only that interface's methods — so `Hijacker` and `Flusher` were lost. No `/grpc` WebSocket upgrade could complete and `/stream` could not flush frames. |
| **Thirteen stale surfaces** | A `ui.PostAsync` issued from a goroutine spawned *inside* an executing `PostAsync` callback never schedules a render. State lands; the DOM freezes. Hit `markAllRead`, `subscribeURL`, `refresh`, `followPage`, four category operations, `patchFeed`, `patchTag`, `unsubscribe`, `undoMarkAll`, and — worst — `addTag`, where a **server-confirmed tag vanished** from the chip. |
| **Correctness** | Stale `ui.State` read in a mount-only keyboard closure broke `j`/`k` navigation · `firstWords` emitted invalid UTF-8 into every Notes-row preview · `hostOf` mangled bracketed IPv6 · a non-idempotent CSS scanner invented a closing paren · three missing empty states · a hint pointing at a removed control. |

**Where the bugs were.** Ten packages assumed to be the riskiest — the security and auth layer — were already thoroughly tested and yielded **zero** defects. `client/view`, 12,660 lines with no tests at all, yielded two on first contact. Fuzzing packages that had none yielded four more. The correlation was not with how dangerous the code sounded; it was with whether anything had ever executed it.

**A framework question left open.** The nested-`PostAsync` symptom matches **H11** above. Reading the framework found `ui.PostAsync` itself probably sound — the inbox re-arms correctly — but two plausible mechanisms one layer down: `GoUseState`'s setter short-circuits on `fastEqual`, so a write that changes no value schedules nothing; and `resolveOwnedFiberTarget` can fail to remap a captured fiber and schedule nothing while the value has already been written. `reader.go`'s own `bumpList`/`revRef` workaround independently documents this symptom, which is evidence the team hit it before without naming it.

## Behavioural defects

| | Symptom | Ref |
|---|---|---|
| **The topmost-article handler may not be running.** | Scrolling through three articles does not change the document title or the highlighted row — the central rule "which article is being read is a scroll position" may not be in effect. Marking read still works, which is exactly why nobody noticed: that comes from the *other* scroll handler. | 8b.52 |
| **An async state write does not repaint when nothing else changed.** | An empty feed shows a loading skeleton that never resolves, for a request that succeeded — with zero items the only genuine change is one boolean, and that alone does not repaint. The app has been masking this by repainting on incidental scroll events. Suspected in the framework rather than in this app; if confirmed there, **any async update whose only change is a flag is invisible**, which is most "finished loading", "saved" and "failed" states. | H11 |
| **The house theme's tertiary text is below AA** — 4.42:1 on a hovered row and 3.94:1 on the selected one, at the size used for datelines and counts. Transcribed verbatim from the mockup, so this is a decision about the mockup rather than a value to nudge. Ratcheted so it cannot get worse and no new theme can inherit the exception. | 8b.44 / D22 |
| **External stylesheets do not survive the page proxy**, so proxied pages come back legible rather than faithful. | 6.15a |

## Missing surfaces for things that work

- **No sign-out button.** The sign-out call exists and works; clearing local storage is the current logout.
- **No settings registry**, so every preference control is hand-built.
- **Roles are stored and not enforced.** Handing someone a viewer role while believing it restricts anything is the mistake the deployment runbook explicitly warns about.
- **No live updates.** The event ring buffers exist; nothing streams them to a browser, so a second tab does not learn about the first tab's marks until it refetches.
- **Notes and bookmark-archive search** are indexed and unreachable.
- **Recovery screens** — codes, admin-minted resets, sudo mode — exist at the server and have no UI.

## Test and process debt

- **The end-to-end suite is not currently a gate.** The desktop run stands at 39 passed / 14 failed, most of them stale assertions rather than regressions; until it is green its output means nothing, which is the state a suite must not quietly be left in.
- **The phone build has never been compared against its own mockup.** Arithmetic passed every theme five times; *looking* is what found the article wash reading as a green panel on two themes nobody had opened.
- **The deployment has never met a real droplet**, and the Linux build recipes have never been executed on a machine that has `make`.
- **No restore drill has been run.** The first restore attempt must not be during an incident.

---

# Appendix — Index by state

**Shipped.** Three-pane shell · resizable panes · server-side layout · the rail · four folding
bands · hue dots · favicons · dormant-feed marking · two rail filters · virtualised list · true-length
sizing · position-driven filling · four row states · row flags · list header · empty states · the
reading stream · append/prepend · scroll-anchored prepends · topmost-drives-everything · seeded
predecessor · neighbour prefetch · long-article clamp · jump suppression · chip row · note panel ·
autosave with an honest tick · article tags · the keyboard map · the shortcut sheet · the command
palette · six two-way dialogs · the status banner · add-a-feed · discovery rungs 1–2 · Smart+ follow ·
JSON-rule following · client-rendered detection · categories · feed tags · tag identity vs
presentation · fifty glyphs · per-feed settings · shared-group warning · interval clamping ·
per-feed refresh · refresh receipt · mark-all-read with undo · search · verdicts · read later · mark
unread · notes stream · signal collection · signal derivation · the client outbox · browser speech ·
Smart+ voice · summarise-before-reading · keep-playing · the floating transport · five themes · seven
accents · three reading sizes · the motion system · the travelling cursor · data-arrival animation ·
indeterminate waits · focus mode · the filmstrip · the phone tab bar · the splash · skeletons ·
interface translation · image proxying · view page · five connection states · the mutation outbox ·
the read cache · the staleness badge · three honest refusals · resume · nine settings tabs · login ·
lockout · password policy · refresh families · boot refusals · the operator CLI · backups · health and
readiness · version skew · the demo.

**Partial.** Tags (feed-level only) · search (items only) · discovery (rungs 3/4/6 owed) · the
render ladder (no controller) · live view (view-only) · page proxy (no external CSS) · recovery (no
UI) · deployment (unproven) · import/export (no UI) · extraction (no switcher) · keep-offline (no
consumer) · plus the five defects above.

**Engine only.** Rules · ranking · topics · recommendations · preservation · item tags ·
mailboxes · settings registry · capability enforcement · the job queue · event rings · the degrade
ladder · notes/bookmark search · quota accounting.

**Planned.** Bookmarks · archiving UI · dead-link view · the bookmarklet · trip packs ·
leader election · conflict resolution · the sync API · token management · notifications · quiet
hours · trends · the heatmap · feed health · contextual nudges · the ranked homepage · three slots ·
explainability · the tuning panel · the suppressed view · highlights mode · topic editing · domain
affinity · recommendations UI · trials · translation · rules UI · the muted view · newsletters ·
webhooks · revisions UI · the screensaver · folder sharing · public feeds · the admin console ·
deletion previews.

---

*Compiled from the repository at 2026-07-27. When a feature moves state, correct it here in the same
change — a catalogue that has quietly drifted is worse than no catalogue, because it still gets
trusted.*
