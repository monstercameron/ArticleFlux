# ArticleFlux — flows

*Companion to `plan.md` (rev 8) and `TODO.md`. Mermaid — renders in VS Code preview, GitHub, and most
markdown viewers.*

These exist because three things in this design are easy to get subtly wrong in ways that don't throw
errors: **fan-out happens per subscriber, not per item** (§2); **refresh-token reuse must revoke a
family, not a token** (§3); and **bulk reads must not reach the interest model** (§5). Each is drawn
so the wrong version is visibly wrong.

> **Document set:** `plan.md` is the spec of record; `TODO.md` owns build order; **this file owns the
> behaviour of the paths below and nothing else.** If a diagram and `plan.md` disagree, `plan.md`
> wins. Every diagram cites the section it draws and the `T#` test that pins it — see the table at
> the end.

---

## 1. Feed ingestion — three entry points, one pipeline

```mermaid
flowchart TD
    subgraph ENTRY["Entry points"]
        POLL["Poller<br/>priority queue by<br/>staleness ratio"]
        HUB["WebSub push<br/>from a hub"]
        IMAP["IMAP poll<br/>per-user mailbox"]
    end

    POLL --> GUARD
    HUB --> HMAC{"HMAC<br/>valid?"}
    HMAC -- no --> DROP1["Drop + log.<br/>An unauthenticated<br/>callback is a content<br/>injection hole"]
    HMAC -- yes --> GUARD
    IMAP --> MIME["MIME parse<br/>prefer text/html"]

    GUARD["SSRF guard<br/>scheme + resolved IP"] --> FETCH["Conditional GET<br/>ETag / If-Modified-Since"]
    FETCH --> CODE{"Status"}
    CODE -- "304" --> DONE304["Stop. Most polls<br/>should end here."]
    CODE -- "429 / 503" --> RETRY["Honour Retry-After<br/>absolutely"]
    CODE -- "301" --> MOVED["Update sources.url<br/>re-run guard per hop<br/>cap chain at 5"]
    MOVED --> FETCH
    CODE -- "200" --> SNIFF["Sniff root element<br/>rss / feed / RDF / JSON"]

    SNIFF --> PARSE["gofeed + our<br/>normalisation layer"]
    MIME --> SANI2["sanitize: newsletter policy<br/>strictest in the app"]
    SANI2 --> NORM
    PARSE --> CHARSET["charset reconcile<br/>XML decl vs Content-Type"]
    CHARSET --> DATES["parse dates<br/>~15 layouts<br/>clamp absurd values"]
    DATES --> NORM["Normalised items"]

    NORM --> IDENT["Identity:<br/>guid / atom id / link /<br/>sha256(title+published)"]
    IDENT --> SEEN{"Seen this<br/>guid before?"}

    SEEN -- no --> FLOOD{"New count vs<br/>median per poll"}
    FLOOD -- "greater than 10x" --> SUSPECT["FLOOD_SUSPECTED<br/>ingest but SUPPRESS FAN-OUT.<br/>A GUID scheme change must not<br/>notify every subscriber about<br/>three years of backlog"]
    FLOOD -- normal --> INSERT["INSERT item<br/>compute dupe_key<br/>harvest outlinks"]

    SEEN -- yes --> HASH{"content_hash<br/>changed?"}
    HASH -- no --> NOOP["Touch nothing.<br/>Never reset read state —<br/>this is how bad readers<br/>resurrect a backlog nightly"]
    HASH -- yes --> REV["Write item_revisions,<br/>bump revision.<br/>Read state SURVIVES"]

    INSERT --> QUEUE["Enqueue fan-out job"]
    REV --> QUEUE
    SUSPECT --> ADMIN["Surface in admin +<br/>owner's feed settings<br/>accept / discard"]

    QUEUE --> FANOUT["See diagram 2"]

    style DONE304 fill:#1d5a38,color:#fff
    style NOOP fill:#1d5a38,color:#fff
    style SUSPECT fill:#8a5300,color:#fff
    style DROP1 fill:#a4262c,color:#fff
```

**The two green boxes are the common case.** A healthy poll ends at `304`, and a re-serve of unchanged
content ends at "touch nothing." If either is doing work, something is wrong.

---

## 2. Fan-out — per subscriber, off the poll thread

The single subtlest thing in the design. `items` are **global** (A14), rules are **per user**, so
evaluating once at ingest means one person's mute filter hides an item from everyone.

```mermaid
flowchart TD
    JOB["Fan-out job<br/>(queued, never inline<br/>with the poll)"] --> SUBS["For each subscriber<br/>of this source"]

    SUBS --> MODE{"subscription<br/>home_mode"}
    MODE -- muted --> STATE
    MODE -- full --> RULES
    MODE -- highlights --> SCORE["Score with the<br/>ALTERNATE weighting:<br/>drop FeedAffinity,<br/>lean on corroboration +<br/>domain affinity + topic"]
    SCORE --> CUT{"above the<br/>solved cutoff?"}
    CUT -- no --> STATE
    CUT -- yes --> RULES

    RULES["Evaluate that user's rules<br/>ordered, all-match,<br/>stop_processing honoured"] --> ACT{"actions"}
    ACT --> MR["mark_read"]
    ACT --> ST["star"]
    ACT --> TG["tag"]
    ACT --> MU["mute<br/>(stored + flagged,<br/>NOT deleted)"]
    ACT --> NT["notify<br/>(suppressed if<br/>FLOOD_SUSPECTED)"]

    MR --> STATE
    ST --> STATE
    TG --> STATE
    MU --> STATE
    NT --> PUSH["Web Push<br/>digest window"]

    STATE["Write user_item_state<br/>+ item_tags<br/>+ rule_hits"] --> EV["Emit scoped events<br/>ItemsArrived / CountsChanged"]
    EV --> RANK["Feed ranking signals"]

    style JOB fill:#342a44,color:#fff
    style RULES fill:#1d5a38,color:#fff
```

> **Cost:** `O(new_items x subscribers)`. A source with 200 subscribers returning 50 items is 10,000
> evaluations — which is exactly why this is a queued job with per-kind concurrency caps and not
> something the fetch loop waits on.

---

## 3. Authentication — bootstrap, login, session lifetime

```mermaid
flowchart TD
    BOOT["Server starts"] --> ANY{"any superadmin<br/>exists?"}
    ANY -- no --> INIT["REFUSE to serve the app.<br/>Print a one-time<br/>15-minute enrolment token"]
    INIT --> ENROLL["/enroll/:token<br/>create tenant 1<br/>+ first superadmin"]
    ENROLL --> READY
    ANY -- yes --> READY["Serve"]

    READY --> LOGIN["POST login<br/>username + password"]
    LOGIN --> LOCK{"locked out?<br/>per-user AND per-IP"}
    LOCK -- yes --> DENY1["Generic failure.<br/>Same message, same timing"]
    LOCK -- no --> HASH["ALWAYS run Argon2id,<br/>even for an unknown user —<br/>otherwise timing is a free<br/>user-enumeration oracle"]

    HASH --> OK{"match?"}
    OK -- no --> LOG1["Record attempt<br/>back off"] --> DENY1
    OK -- yes --> ISSUE["Issue access (15m)<br/>+ refresh, bound to a<br/>DEVICE FAMILY"]

    ISSUE --> USE["Client calls RPCs"]
    USE --> INT{"capability<br/>interceptor"}
    INT -- "method unmapped" --> FAILCLOSED["REJECT.<br/>A new RPC is unreachable<br/>until deliberately granted"]
    INT -- "lacks capability" --> DENY2["Reject before<br/>the handler runs"]
    INT -- ok --> SCOPE["Scope{tenant,user,caps}<br/>into context"] --> REPO["Repository enforces<br/>tenant isolation again"]

    USE --> EXP{"access<br/>expired?"}
    EXP -- yes --> REFRESH["Present refresh token"]
    REFRESH --> REUSE{"already<br/>used?"}
    REUSE -- yes --> NUKE["REVOKE THE WHOLE<br/>DEVICE FAMILY.<br/>A replayed token means<br/>it was stolen"]
    REUSE -- no --> ROTATE["Rotate: new pair,<br/>old one burned"] --> USE

    style FAILCLOSED fill:#a4262c,color:#fff
    style NUKE fill:#a4262c,color:#fff
    style HASH fill:#8a5300,color:#fff
```

**Third-party sync clients** take the same path with one difference: an `api_tokens` bearer resolves to
the same `Scope`, but its capability set is a **fixed enum** (`reader_ro` / `reader_rw`) that can never
inherit the owner's admin capabilities. A token pasted into a phone app is the worst possible place for
`system.impersonate` to leak.

---

## 4. Recovery — three rungs, none of them billed

```mermaid
flowchart TD
    LOST["Can't sign in"] --> WHO{"who are you?"}

    WHO -- "a user with codes" --> R1["Rung 1: recovery code<br/>10 issued once at signup<br/>Argon2id hashed at rest"]
    WHO -- "a user, no codes" --> R2["Rung 2: ask an admin<br/>admin mints a single-use<br/>15-minute token"]
    WHO -- "SMTP configured" --> R3["Rung 3: emailed link<br/>optional, off by default"]
    WHO -- "the superadmin,<br/>nobody above them" --> CLI["Break-glass:<br/>ArticleFlux admin reset-password<br/>from the host filesystem.<br/>Filesystem access IS the proof"]

    R1 --> SET
    R2 --> SET
    R3 --> SET
    CLI --> SET

    SET["Set a new password"] --> KILL["Invalidate EVERY session<br/>on EVERY device"]
    KILL --> AUDIT["Write audit entry"]

    INIT2["Anyone hits<br/>'begin recovery'"] --> SAME["Always answer<br/>'if that account exists,<br/>a reset was started'<br/>— or reset becomes the<br/>enumeration oracle login<br/>just closed"]

    style CLI fill:#8a5300,color:#fff
    style SAME fill:#1d5a38,color:#fff
    style KILL fill:#a4262c,color:#fff
```

---

## 5. The reading loop — and how it feeds the ranker

The loop that has to be right, because it runs hundreds of times a day and quietly trains everything.

```mermaid
flowchart LR
    OPEN["Open home"] --> LIST["Ranked list"]
    LIST --> IMP["Row visible >1s<br/>= IMPRESSION"]
    IMP --> NAV{"what happens"}

    NAV -- "j / k past it" --> SOFT["weak negative<br/>only because we know<br/>it was on screen"]
    NAV -- "o open" --> READ["Article"]
    NAV -- "A mark all read" --> BULK["kind = bulk_read<br/>NEUTRAL — excluded<br/>from affinity entirely"]

    READ --> DWELL{"time on page"}
    DWELL -- "under 15s" --> BOUNCE["STRONG negative.<br/>An informed rejection<br/>beats never opening"]
    DWELL -- "read through" --> POS["positive"]
    POS --> DEEP{"did more?"}
    DEEP -- "l like (A27)" --> S1["stronger — an explicit<br/>verdict, given after reading"]
    DEEP -- "b bookmark" --> S2["stronger still"]
    DEEP -- "e note" --> S3["STRONGEST —<br/>you stopped and typed"]
    NAV -- "d dislike (A27)" --> DIS["STRONGEST negative.<br/>The half nothing else<br/>could record"]

    SOFT --> LOG
    BOUNCE --> LOG
    DIS --> LOG
    S1 --> LOG
    S2 --> LOG
    S3 --> LOG
    BULK -.->|"excluded"| LOG

    LOG["engagements<br/>append-only, raw"] --> DERIVE["Scheduled derivation"]
    DERIVE --> FA["feed_affinity"]
    DERIVE --> DA["domain_affinity"]
    DERIVE --> TA["term_affinity"]
    DERIVE --> TOP["topics<br/>clustered, labelled,<br/>trended"]
    FA --> SCORE["Scorer"]
    DA --> SCORE
    TA --> SCORE
    TOP --> SCORE
    SCORE --> HR["home_ranking<br/>materialised"] --> LIST

    style BULK fill:#8a5300,color:#fff
    style S3 fill:#1d5a38,color:#fff
    style BOUNCE fill:#a4262c,color:#fff
    style DIS fill:#a4262c,color:#fff
    style LOG fill:#342a44,color:#fff
```

> **The dotted line is the whole point.** One `mark all read` is 143 items; treated as rejections it
> destroys weeks of signal in a keystroke, silently, detectable only as the homepage slowly getting
> worse. `engagements.kind` is what keeps it out.

---

### 5a. The reading stream (A28) — what "read it" actually means now

The article pane is a sequence, and scrolling is the whole interaction. Nothing is ever taken away
from under the reader.

```mermaid
flowchart TD
    CLICK["Click a row"] --> SEED["Seed the stream with<br/>[prev, clicked]<br/>and scroll the CLICKED one<br/>to the top of the pane"]
    SEED --> PF["Prefetch bodies:<br/>prev · current · next"]
    PF --> SCROLL{"reader scrolls"}

    SCROLL -- "down, near the bottom" --> APP["APPEND the next item<br/>+ prefetch one beyond"]
    SCROLL -- "up, near the top" --> PRE["PREPEND the previous item<br/>inside KeepScrollAnchored<br/>so their place is held"]
    SCROLL -- "past a boundary" --> TOP["A different article is now<br/>topmost in the viewport"]

    TOP --> FOCUS["current = that article"]
    FOCUS --> T1["document title"]
    FOCUS --> T2["verdict chips"]
    FOCUS --> T3["highlighted list row<br/>(list scrolls to match)"]
    FOCUS --> T4["MARK READ —<br/>scrolling into it<br/>is reading it"]

    APP --> GROW["scrollHeight changed"]
    PRE --> GROW
    GROW --> REARM["Trigger RE-ARMS.<br/>Position alone is not an edge:<br/>appending keeps you inside<br/>the zone forever"]
    REARM --> SCROLL

    APP -- "out of loaded items" --> PAGE["loadMore(len+1)<br/>then resume the extension<br/>the reader already scrolled for"]
    PAGE --> APP

    style T4 fill:#1d5a38,color:#fff
    style REARM fill:#8a5300,color:#fff
    style PRE fill:#342a44,color:#fff
```

> **`REARM` is the box that was missing.** Without it the stream advanced exactly once per visit to
> the bottom, and the reader had to scroll *up* and back down to get the next article — which is the
> opposite of continuous.

> **`PRE` is the other one.** Inserting content above a scroll position moves everything below it
> down by that height, so the paragraph being read shoots off the bottom of the screen.
> `KeepScrollAnchored` measures before, corrects after, across two frames — one frame still measures
> the old layout, which yields a delta of zero and looks exactly like not having called it.

---

## 6. Subscribing — deterministic first, model last, always validated

```mermaid
flowchart TD
    IN["User pastes a URL<br/>or types a name"] --> R1["Rung 1<br/>link rel=alternate<br/>in the page head"]
    R1 -- "found" --> VAL
    R1 -- "nothing" --> R2["Rung 2<br/>probe /feed /rss.xml<br/>/atom.xml /index.xml"]
    R2 -- "found" --> VAL
    R2 -- "nothing" --> R3["Rung 3<br/>platform rules:<br/>YouTube, Reddit, GitHub,<br/>Substack, Mastodon"]
    R3 -- "found" --> VAL
    R3 -- "nothing" --> R4["Rung 4<br/>LLM proposes candidates<br/>(+ web search if the user<br/>typed a name)"]
    R4 --> VAL
    R4 -- "nothing" --> R5["Rung 5<br/>no feed exists —<br/>offer a scrape rule"]

    VAL{"FETCH AND PARSE<br/>every candidate<br/>before showing it"}
    VAL -- "parses" --> SHOW["Show with title,<br/>cadence, item count"]
    VAL -- "404 / not a feed" --> DISCARD["Discard silently.<br/>A hallucinated URL that<br/>404s teaches the user<br/>not to trust the feature"]

    SHOW --> PICK["User picks"] --> SUB["Subscribe<br/>folder + interval"]
    R5 --> BUILD["Rule builder<br/>live preview per keystroke<br/>Claude drafts the selectors,<br/>the preview proves them"]
    BUILD --> SUB

    style VAL fill:#8a5300,color:#fff
    style DISCARD fill:#a4262c,color:#fff
    style SHOW fill:#1d5a38,color:#fff
```

---

## 7. Offline round trip — where offline apps break

```mermaid
flowchart TD
    PREP["Prepare for a trip"] --> EST["Estimate size<br/>by depth and scope"]
    EST --> BUILD["BuildPack (streamed)<br/>extract + images + audio"]
    BUILD --> DL["GET /pack/:id over<br/>PLAIN HTTPS — not the tunnel.<br/>A Service Worker cannot see<br/>WebSocket frames"]
    DL --> IDB["Unpack into IndexedDB"]

    IDB --> OFFLINE["Tunnel drops"]
    OFFLINE --> SHELL["App boots from the<br/>SW-cached shell"]
    SHELL --> READ2["Read from the mirror<br/>with an unmistakable badge"]
    READ2 --> MUT["Mark read, star, tag,<br/>write a note"]
    MUT --> OUT["Outbox: idempotency key<br/>+ the rev observed"]

    OUT --> BACK["Reconnect"]
    BACK --> RESUB["Re-subscribe with<br/>since_seq — a stream that<br/>just starts listening<br/>silently loses the gap"]
    BACK --> DRAIN["Drain the outbox in order"]
    DRAIN --> CAS{"rev still<br/>matches?"}
    CAS -- yes --> APPLY["Apply"]
    CAS -- "no, read/star/tag" --> LWW["Server rev wins.<br/>Losing a read-flag race<br/>is invisible and fine"]
    CAS -- "no, A NOTE" --> CONFLICT["KEEP BOTH.<br/>Write outbox_conflicts,<br/>prompt to resolve.<br/>Notes are the one thing<br/>that cannot be regenerated"]

    RESUB --> BEHIND{"further behind than<br/>the ring buffer?"}
    BEHIND -- yes --> RESYNC["RESYNC_REQUIRED<br/>drop caches, refetch"]
    BEHIND -- no --> REPLAY["Replay from since_seq"]

    style CONFLICT fill:#a4262c,color:#fff
    style DL fill:#8a5300,color:#fff
    style RESUB fill:#1d5a38,color:#fff
```

---

## 8. Three transports, one service layer

The invariant behind R3: a read marked in Reeder must move the homepage in an open browser tab.

```mermaid
flowchart TD
    A["Browser wasm client<br/>gRPC over wss tunnel"] --> SVC
    B["Reeder / NetNewsWire<br/>REST /reader/api/0/*"] --> AUTH2["api_token -> Scope<br/>capped scope enum"] --> SVC
    C["Offline pack download<br/>plain HTTPS"] --> SVC

    SVC["ONE service layer<br/>internal/*"] --> RULES2["rules run"]
    SVC --> SIG["signals recorded"]
    SVC --> EVT["events emitted"]
    SVC --> STORE["repository + Scope"]

    EVT --> BACKA["-> browser tab updates"]
    EVT --> BACKB["-> next sync poll sees it"]

    style SVC fill:#1d5a38,color:#fff
```

> **If a mutation path skips the service layer**, "marked read in Reeder" silently diverges from the
> web UI and nobody notices for weeks. Three entry points, one implementation — enforce it in review.

---

## 9. Preservation — the source goes away

Bookmarks archived eagerly, items lazily. That's backwards for the failure that actually happens.

```mermaid
flowchart TD
    ING["Item ingested"] --> TIER{"archive tier"}
    TIER -- "high-affinity source<br/>OR ranks into Top<br/>OR truncated + engaged" --> EAGER["Archive NOW"]
    TIER -- "policy = always" --> EAGER
    TIER -- "policy = never" --> SKIP["Feed content only"]
    TIER -- otherwise --> LAZY["Wait"]

    LAZY --> ACT{"user acts?"}
    ACT -- "open / star / note / tag" --> EAGER
    ACT -- "never" --> WATCH["archived_text only"]

    POLL2["Poll fails"] --> N{"consecutive<br/>failures"}
    N -- "under threshold" --> BACK["Back off, retry"]
    N -- "crosses threshold" --> FAILING["lifecycle = failing"]
    FAILING --> SWEEP["PRESERVATION SWEEP<br/>archive this source's<br/>un-archived items, rate-limited.<br/>The feed being down does NOT<br/>mean the articles are"]
    SWEEP --> EAGER

    FAILING --> STILL{"still failing<br/>after 30 days,<br/>or 404 / 410 / NXDOMAIN"}
    STILL -- yes --> GONE["lifecycle = gone<br/>deactivated_at set<br/>polling stops<br/>NOTHING is deleted (A22)"]
    GONE --> TELL["Tell the user what survived:<br/>'AnandTech is gone. You have<br/>412 items, 89 with full text.'"]

    OPEN2["User opens an item"] --> LIVE{"origin<br/>resolves?"}
    LIVE -- yes --> NORMAL["Render normally"]
    LIVE -- no --> MARK["origin_dead = 1"] --> HAVE{"archived?"}
    HAVE -- yes --> SERVE["Serve the copy + banner:<br/>'The original is gone.<br/>This is the copy saved 12 Mar.'"]
    HAVE -- no --> HONEST["Feed content + 'no copy was saved'<br/>+ Wayback link.<br/>The honest answer to<br/>'we don't have it' is<br/>'someone else might'"]

    EAGER --> CAP{"archive cap<br/>reached?"}
    CAP -- yes --> EVICT["Evict LRU —<br/>but NEVER an archive whose<br/>origin is dead, and never<br/>one belonging to an<br/>engaged item"]

    style SWEEP fill:#8a5300,color:#fff
    style GONE fill:#a4262c,color:#fff
    style SERVE fill:#1d5a38,color:#fff
    style EVICT fill:#8a5300,color:#fff
```

**The amber box is the insight.** A feed erroring is the best early warning you get that a site is in
trouble, and it almost always arrives while the article URLs still resolve. Sweeping *then* is the
difference between having the content and having a title.

---

## What these diagrams are load-bearing for

| Diagram | The bug it prevents | Test |
|---|---|---|
| 2 · Fan-out | One user's mute hides an item from every subscriber | TODO §4 |
| 3 · Auth | A stolen refresh token stays usable; an unmapped RPC is reachable | T10, T4 |
| 5 · Reading loop | `mark all read` destroys the interest model | R17, TODO test 4 |
| 6 · Subscribe | An unvalidated LLM guess reaches the UI | T9 |
| 7 · Offline | A note edited in two places is silently clobbered | T7 |
| 8 · Transports | Sync-API writes diverge from the web UI | R3 |
| 9 · Preservation | A feed dies and everything you never opened becomes a dead link | §10.6, R7 |
