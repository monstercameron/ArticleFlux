# ArticleFlux vs. the field — feature matrix and honest standing

*Compiled 2026-07-27. ArticleFlux capabilities are taken from `docs/FEATURES.md` (which is itself
compiled from `plan.md` rev 9, `TODO.md`, `FLOWS.md`, the proto contract and the client source).
Competitor capabilities are from vendor feature/pricing pages and 2026 comparison write-ups; sources
are listed at the end.*

---

## The rule this document follows

**ArticleFlux is scored on what a reader can use today, not on what is in the plan.** Anything
engine-only or designed-not-built is marked as such and is *not* counted as a win. A matrix that
credits unshipped work is a marketing document, and it will make the roadmap wrong — the whole point
of running this comparison is to find out where the gaps actually are.

### Legend

| | Meaning |
|---|---|
| ✅ | Shipped and usable |
| 💲 | Exists, but only on a paid tier |
| ◧ | Partial — real but incomplete; the gap is named in the notes |
| ⚙ | **ArticleFlux only:** the engine exists and is tested, with no UI or no RPC in front of it |
| ○ | **ArticleFlux only:** designed in the plan, no code |
| — | Not offered |
| ? | Could not verify; treat as unknown, not as absent |

---

## The field

Eleven products, chosen to cover every business model a reader can be built on: hosted freemium,
hosted flat-fee, self-hosted open source, and native client.

| Product | Model | Price (2026) | Limits | Shape |
|---|---|---|---|---|
| **ArticleFlux** | Self-hosted, free | $0 + your own OpenAI spend | none | Go server + SQLite + wasm client, multi-tenant |
| **Feedly** | Hosted freemium | Free · Pro $6/mo annual ($72/yr) · Pro+ $8.25/mo annual ($99/yr, $12.99 monthly) · Enterprise from $1,600/mo | 100 / 1,000 / 2,500 sources | Market-intelligence platform that also reads feeds |
| **Inoreader** | Hosted freemium | Free (ad-supported) · Pro $7.50/mo annual ($90/yr, $9.99 monthly) · Teams from $44.99/mo | 150 / 2,500 feeds | Power-user aggregator with automation |
| **NewsBlur** | Hosted freemium | Free · Premium $36/yr · Premium Archive $99/yr | 64 / 1,024 / 4,096 sites | Indie reader with a training model and a permanent archive |
| **Feedbin** | Hosted flat | $5/mo (30-day trial) | — | Minimalist hosted reader, newsletters-first |
| **Readwise Reader** | Hosted flat | Included in Readwise Full: $9.99/mo annual ($119.88/yr) or $12.99 monthly | — | Read-later + knowledge capture that ingests RSS |
| **Folo** | Open source, hosted+desktop | Free (AGPL-3) | — | AI-native reader with a community/curation layer |
| **FreshRSS** | Self-hosted OSS | $0 (AGPL-3) | none | PHP + SQLite/MySQL; the feature-rich self-host default |
| **Miniflux** | Self-hosted OSS | $0 (AGPL-3); $15/yr hosted | none | Single Go binary + PostgreSQL; minimalist |
| **Tiny Tiny RSS** | Self-hosted OSS | $0 (GPL) | none | PHP; the configurable/plugin one |
| **NetNewsWire** | Native client, OSS | $0 | none | Apple-only client; syncs against someone else's server |

Not tabled but worth knowing: **The Old Reader** (free/premium, Google-Reader nostalgia + social),
**Reeder** (~$10/yr, unified media timeline client), **Matter** (~$5/mo, audio-first read-later),
**CommaFeed** / **yarr** (tiny self-hosted), and the dead ones that keep coming up — **Omnivore**
(shut 2024) and **Pocket** (shut 2025). Their absence is not a judgement; they do not change any row
below.

---

## A. Ingest — getting things into the reader

| | AF | Feedly | Inoreader | NewsBlur | Feedbin | Readwise | Folo | FreshRSS | Miniflux | TT-RSS | NNW |
|---|---|---|---|---|---|---|---|---|---|---|---|
| RSS / Atom / JSON Feed | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Feed autodiscovery from a page URL | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Common-path probing when nothing is declared | ✅ | ? | ? | ? | ? | ? | ? | — | — | — | — |
| Platform rules (YouTube/Reddit/GitHub/Mastodon) | ○ | ✅ | ✅ | ◧ | ◧ | ◧ | ✅ | ◧ | ◧ | ◧ | ◧ |
| **Follow a site that has no feed at all** | ✅ | 💲 | 💲 | 💲 | — | — | ? | ✅ | ✅ | ✅ | — |
| …and the **rule is written by a model**, not by you | ✅ | — | — | — | — | — | — | — | — | — | — |
| …with the rule **validated against the page before it is offered** | ✅ | — | — | — | — | — | — | — | — | — | — |
| …falling back to the site's own **JSON endpoint** | ✅ | — | — | — | — | — | — | — | — | — | — |
| robots.txt checked before spending a model call | ✅ | ? | ? | ? | — | — | ? | — | — | — | — |
| Newsletters into the reader (email address) | ○ | 💲 | 💲 | 💲 | ✅ | ✅ | ✅ | ◧ | — | ◧ | — |
| PDFs / EPUB / YouTube transcripts | — | — | — | — | — | ✅ | ◧ | — | — | — | — |
| Save-from-web bookmarklet / share sheet | ○ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| OPML import / export | ◧ CLI | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Netscape bookmarks / Chrome JSON import | ⚙ | — | — | — | — | — | — | — | — | — | — |
| Full-text extraction of truncated feeds | ✅ | 💲 | 💲 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| WebSub / push refresh | — | ✅ | ✅ | ✅ | ✅ | ? | ? | ✅ | ✅ | ✅ | n/a |

**Read this row first: "the rule is written by a model."** Everyone else who lets you follow a
feedless site makes *you* write the selector (FreshRSS XPath, Miniflux scraper rules, TT-RSS
plugins) or gives you a point-and-click builder on a paid tier (Feedly RSS Builder on Pro+,
Inoreader web feeds). ArticleFlux is the only one where you paste a URL and a model produces the
rule — and the only one that *compiles and runs the proposed rule against the page*, refusing it when
it yields nothing, one item, or a set of identical titles, before the reader ever sees it. That
validation step is the actual differentiator; "an LLM writes a scraper" is a demo, "an LLM writes a
scraper we then refuse to trust" is a feature.

**Where it loses:** import and export exist **only as CLI subcommands** — `articleflux import -file
feeds.opml` works, and no screen calls it, so on a multi-tenant instance a reader who is not the
operator cannot bring their feeds in at all. No bookmarklet, no newsletters, no WebSub — polling
only.

---

## B. The reading surface

| | AF | Feedly | Inoreader | NewsBlur | Feedbin | Readwise | Folo | FreshRSS | Miniflux | TT-RSS | NNW |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Three-pane layout | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ◧ | ✅ | ✅ |
| Resizable panes | ✅ | ◧ | ✅ | ✅ | ◧ | ✅ | ✅ | ◧ | — | ✅ | ✅ |
| **Pane widths stored server-side (follow the account)** | ✅ | ? | ? | ? | ? | ? | ? | — | — | — | n/a |
| Continuous reading stream (no back-to-list) | ✅ | ◧ | ✅ | ✅ | ✅ | ◧ | ✅ | ✅ | — | ✅ | ◧ |
| Virtualised list sized to the **true** scope total | ✅ | ? | ? | ? | ? | ? | ? | — | — | — | ✅ |
| Scroll-anchored prepends (new items don't move you) | ✅ | ? | ? | ? | ? | ? | ? | ? | ? | ? | ✅ |
| Full keyboard map + shortcut sheet | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Command palette | ✅ | — | — | — | — | ✅ | ✅ | — | — | — | — |
| Focus / distraction-free mode | ✅ | ✅ | ✅ | ◧ | ✅ | ✅ | ✅ | ◧ | ✅ | ◧ | ✅ |
| Themes (multiple, not just dark/light) | ✅ 5 | ◧ | ✅ | ◧ | ◧ | ✅ | ✅ | ✅ | ◧ | ✅ | ◧ |
| Accent colours / reading sizes | ✅ 7/3 | ◧ | ✅ | ◧ | ◧ | ✅ | ✅ | ✅ | ◧ | ✅ | ◧ |
| **A motion system with a reduced-motion contract** | ✅ | ? | ? | ? | ? | ? | ? | — | — | — | ✅ |
| Skeletons rather than spinners | ✅ | ✅ | ✅ | ◧ | ✅ | ✅ | ✅ | — | — | — | n/a |
| In-page render of the original page (proxied) | ◧ | 💲 | 💲 | ✅ | ✅ | ✅ | ✅ | — | — | ◧ | ✅ |
| Live remote-browser view of a page | ◧ | — | — | — | — | — | — | — | — | — | — |
| Interface translated (i18n) | ✅ | ✅ | ✅ | ◧ | — | ◧ | ✅ | ✅ 15+ | ✅ | ✅ | ◧ |
| Article **content** translation | ○ | 💲 | 💲 | — | — | 💲 | ✅ | — | — | ◧ | — |

**Where it wins:** the list is virtualised *and* honest — the scrollbar reflects a `COUNT` over the
same filter set as the page query, so the thumb is truthful from first paint, and filling is driven
by scroll position rather than by proximity to the loaded end. Most hosted readers infinite-scroll
instead, which is easier and lies about how much is left. Layout as account state is a small thing
that no hosted competitor appears to do.

**Where it loses:** the render ladder has no controller (two buttons and a switch, no automatic
escalation, no per-feed default), live view is view-only with no input channel, and article
translation is planned only.

---

## C. Organising and triage

| | AF | Feedly | Inoreader | NewsBlur | Feedbin | Readwise | Folo | FreshRSS | Miniflux | TT-RSS | NNW |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Folders / categories | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Feed-level tags/labels (orthogonal to folders) | ✅ | ✅ | ✅ | ◧ | ✅ | ✅ | ✅ | ✅ | — | ✅ | — |
| Item-level tags | ⚙ | 💲 | ✅ | ◧ | ✅ | ✅ | ✅ | ✅ | — | ✅ | — |
| Saved / read-later | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Notes on an article | ✅ | 💲 | ✅ | ◧ | — | ✅ | ✅ | ◧ | — | ◧ | — |
| Highlights on selected text | ○ | 💲 | ✅ | — | — | ✅ | ✅ | — | — | — | — |
| Full-text search over items | ✅ | 💲 | 💲 | 💲 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ◧ |
| Search over notes / archives | ⚙ | 💲 | 💲 | 💲 | ✅ | ✅ | ? | ✅ | ✅ | ✅ | — |
| Saved searches / monitoring feeds | ○ | 💲 | 💲 | ◧ | ✅ | ✅ | ? | ✅ | — | ✅ | — |
| **Rules engine (if/then over incoming items)** | ⚙ | 💲 | 💲 | 💲 | ✅ | ◧ | ? | ✅ | ✅ | ✅ | — |
| Mute / suppress by rule | ⚙ | 💲 | 💲 | ✅ | ◧ | ◧ | ? | ✅ | ✅ | ✅ | — |
| Mark-all-read **with undo** | ✅ | ◧ | ◧ | ◧ | ◧ | ◧ | ? | ◧ | ◧ | ◧ | ◧ |
| Per-feed poll interval | ✅ | 💲 | 💲 | 💲 | — | — | ? | ✅ | ✅ | ✅ | n/a |
| Permanent archive (stories never expire) | ○ | 💲 | 💲 | 💲 | ✅ | ✅ | ? | ✅ | ✅ | ✅ | ◧ |

**The rules row is the biggest single hole.** The matcher exists, is tested, handles every operator
and ordering and stop-processing, fans out per subscriber as a queued job, and logs its hits — and
there is no list screen, no editor, no preview, no retroactive apply, no `/muted` view. Every
competitor at this tier ships rules; two of the free self-hosted ones ship them for nothing. This is
not a research problem, it is a UI that has not been built, which makes it the cheapest large win on
the board.

**Mark-all-read with undo** is genuinely rare — most readers offer a confirm dialog, which is a
worse trade (it taxes the 99 correct presses to protect the 1 wrong one).

---

## D. Signals, ranking and AI

| | AF | Feedly | Inoreader | NewsBlur | Feedbin | Readwise | Folo | FreshRSS | Miniflux | TT-RSS | NNW |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Explicit like / dislike signals | ✅ | ◧ | ◧ | ✅ | — | — | ✅ | — | — | ✅ | — |
| Implicit signal collection (dwell, scroll, opens) | ✅ | ? | ? | ◧ | — | ◧ | ? | — | — | — | — |
| Trained relevance model over your behaviour | ✅ | 💲 | 💲 | ✅/💲 | — | — | ✅ | ◧ | — | ✅ | — |
| **Ranked / prioritised home stream** | ✅ | 💲 | 💲 | ✅ | — | ◧ | ✅ | — | — | ◧ | — |
| Explanation of *why* an item ranked | ✅ | ◧ | ◧ | ✅ | — | — | ? | — | — | ◧ | — |
| …as separate clauses you could disagree with | ✅ | — | — | ◧ | — | — | — | — | — | — | — |
| A tuning panel over the weights | ○ | 💲 | 💲 | ✅ | — | — | ◧ | — | — | ◧ | — |
| A view of what ranking suppressed | ○ | 💲 | 💲 | ✅ | — | — | ? | — | — | ◧ | — |
| Topic clustering (TF-IDF or model) | ✅ | 💲 | 💲 | 💲 | — | — | ✅ | — | — | — | — |
| Deterministic classification before any model | ⚙ | — | — | ◧ | — | — | — | — | — | ◧ | — |
| Duplicate / same-story clustering | ○ | 💲 | 💲 | 💲 | — | — | ✅ | — | — | — | — |
| AI summary of an article | ✅ | 💲 | 💲 | 💲 | — | ✅ | ✅ | — | — | — | — |
| AI over the whole timeline (digest/briefing) | ○ | 💲 | 💲 | 💲 | — | ◧ | ✅ | — | — | — | — |
| Ask-a-question about an article | — | 💲 | 💲 | 💲 | — | ✅ | ✅ | — | — | — | — |
| **Bring your own model key** | ✅ | — | 💲 | 💲 | — | — | ✅ | — | — | — | — |
| Model spend visible to the person paying | ✅ | — | ◧ | ◧ | — | — | ? | — | — | — | — |
| AI is **opt-in per user with the egress named** | ✅ | ◧ | ◧ | ✅ | n/a | ◧ | ◧ | n/a | n/a | n/a | n/a |
| Recommendations for sites you don't follow | ⚙ | 💲 | 💲 | ◧ | — | — | ✅ | — | — | — | — |

> **Corrected 2026-07-27, after checking the code rather than the catalogue.** The first draft of this
> section scored ranking as engine-only, on `FEATURES.md`'s word that it had "no wire surface at all".
> That is false: `LIST_SCOPE_MEGAFEED` serves My Feed; `rank_slot` names which of the three slots a
> pick filled; `rank_tier` says whether free Smart or paid Smart+ produced it; `rank_reasons` carries
> ordered clauses and `rank_reason_terms` their machine keys; `rank_topic` is the cold-start signal;
> the chips render in `client/view/panes.go`. See §28.4 of the plan.

Two honest readings of this section:

1. **The AI plumbing is better than the AI product.** ArticleFlux is one of very few that runs on
   your own key with no markup, shows you tokens in/out/requests, gates every call behind a per-user
   switch, and states the exact egress in the copy before anything is sent ("its tags and classes,
   not its text"). Inoreader shipped BYOAI in April 2026 and NewsBlur lets you pick the provider on
   the $99 tier — but both are still a vendor's pipe. That is a real position.
2. **The ranking product exists and cannot be argued with.** Per-clause reasons are a stronger
   explainability story than anything else in the table — NewsBlur explains a score, Feedly says a
   topic matched, and neither breaks the judgement into parts. What is missing is the other half of
   §18.9: a reader can read *why* an item ranked and has no way to say *that reason is wrong*. There
   is no tuning panel and no view of what was suppressed, and both of those are where NewsBlur's
   trainer earns its $99/yr. We ship the harder half and not the one people touch.

---

## E. Listening

| | AF | Feedly | Inoreader | NewsBlur | Feedbin | Readwise | Folo | FreshRSS | Miniflux | TT-RSS | NNW |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Read an article aloud | ✅ | ◧ | ✅ | — | — | ✅ | ◧ | — | — | ◧ | — |
| Offline/browser voice with no key and no cost | ✅ | ? | ? | — | — | ◧ | ? | — | — | ? | — |
| Neural voice (paid model) | ✅ | — | 💲 | — | — | 💲 | ? | — | — | — | — |
| **Summarise-before-reading as spoken audio** | ✅ | — | — | — | — | ◧ | ◧ | — | — | — | — |
| **A queue rewritten as a broadcast, with handovers** | ✅ | — | — | — | — | — | — | — | — | — | — |
| Continuous queue ("keep playing") | ✅ | — | ✅ | — | — | ✅ | ? | — | — | — | — |
| Floating transport when the article scrolls away | ✅ | ? | ? | — | — | ✅ | ? | — | — | — | — |
| **Audio cached per item+model+voice, forever** | ✅ | n/a | ? | n/a | n/a | ? | ? | n/a | n/a | n/a | n/a |
| Podcast/enclosure playback | — | ✅ | ✅ | ◧ | ✅ | ✅ | ✅ | ✅ | ◧ | ✅ | ✅ |

This is one of ArticleFlux's two clearest wins. The pairing that nobody else has is **two engines
behind one control where the difference is an egress boundary rather than a quality setting** — the
free browser synthesiser is always there, and the paid neural voice is a toggle sitting *next to the
play button* rather than buried in settings, because it is a decision about what leaves the machine
and you should see its state at the moment you press play. The permanent per-item cache follows from
a correct observation others charge around: article text is immutable, so an audio expiry is just a
schedule for re-buying identical bytes.

The gap: no podcast/enclosure playback at all, which is table stakes in most of the field.

---

## F. Resilience, offline, and honesty about state

| | AF | Feedly | Inoreader | NewsBlur | Feedbin | Readwise | Folo | FreshRSS | Miniflux | TT-RSS | NNW |
|---|---|---|---|---|---|---|---|---|---|---|---|
| **Connection modelled as states with distinct remedies** | ✅ 5 | ◧ | ◧ | ◧ | ◧ | ◧ | ◧ | — | — | — | ◧ |
| **Durable mutation outbox that survives closing the tab** | ✅ | ? | ? | ? | ? | ◧ | ? | — | — | — | n/a |
| Offline reading of already-fetched articles | ◧ | 💲 | 💲 | ◧ | ◧ | ✅ | ✅ | ◧ | ◧ | ◧ | ✅ |
| Downloadable offline packs for a trip | ○ | 💲 | 💲 | — | — | ✅ | ✅ | — | — | — | ✅ |
| Staleness badge on cached content | ✅ | — | — | — | — | ? | ? | — | — | — | ◧ |
| Operations **refused** rather than silently queued | ✅ | — | — | — | — | — | — | — | — | — | — |
| Resume where you were, across machines | ✅ | ◧ | ✅ | ✅ | ◧ | ✅ | ✅ | ◧ | ◧ | ◧ | ◧ |
| Live push of new items into an open session | ◧ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ◧ | ◧ | ✅ | n/a |
| Article preservation when the source dies | ⚙ | 💲 | 💲 | 💲 | ✅ | ✅ | ? | ◧ | ◧ | ◧ | — |

**The strongest philosophical win in the product is here** and it is invisible in most comparisons:
five connection states because there are five remedies, and the explicit rule that *"silently
disconnected" must never look like "a quiet news day."* The content of a reader is absence, and
absence is also what a broken socket looks like — every other reader in this table ships a dot.
Likewise the three operations that are **refused** offline rather than queued, and the distinction
that a retry loop is a latency optimisation while an outbox is a durability guarantee.

**The counterweight, and it is heavy:** **live updates do not arrive today** — and the reason is
smaller than it sounds. `WatchEvents` is on the wire, rate-limited and concurrency-capped; the client
implements the whole pump with coalescing in `client/data/stream_wasm.go`; **nothing in `client/app`
calls it.** Both ends of a shipped feature, and no call site. Every hosted competitor pushes;
ArticleFlux polls, with a very good story about why its polling is honest.

---

## G. Platform reach and interoperability

| | AF | Feedly | Inoreader | NewsBlur | Feedbin | Readwise | Folo | FreshRSS | Miniflux | TT-RSS | NNW |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Web app | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| iOS / Android apps | — | ✅ | ✅ | ✅ | ◧ | ✅ | ✅ | ◧ | ◧ | ◧ | ◧ |
| Desktop apps | — | ◧ | ◧ | — | — | ✅ | ✅ | — | — | — | ✅ |
| Responsive/phone layout in the web app | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | n/a |
| **Google Reader / Fever API (third-party clients)** | ○ | ◧ | ✅ | ◧ | ✅ | — | ? | ✅ | ✅ | ◧ | n/a |
| Public API for automation | ○ | 💲 | 💲 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | n/a |
| Outbound webhooks / integrations | ○ | 💲 | 💲 | ◧ | ✅ | ✅ | ? | ◧ | ✅ 25+ | ✅ | — |
| Push notifications | ○ | 💲 | 💲 | ✅ | ◧ | ✅ | ✅ | ◧ | ◧ | ◧ | ✅ |
| Sharing / public collections | ○ | 💲 | 💲 | ✅ | ✅ | ◧ | ✅ | ✅ | — | ✅ | — |

**This is the section where ArticleFlux is simply behind, with no argument to make.** No mobile app,
no desktop app, no sync API, therefore no NetNewsWire/Reeder on the phone, no automation surface, no
notifications. Miniflux — a one-person minimalist project — ships Fever, Google Reader, a REST API
and 25+ integrations. For a self-hosted reader, **the Google Reader API is the single highest-leverage
missing feature**, because it converts "no mobile client" from a roadmap item into someone else's
solved problem overnight.

---

## H. Ownership, privacy, and running it

| | AF | Feedly | Inoreader | NewsBlur | Feedbin | Readwise | Folo | FreshRSS | Miniflux | TT-RSS | NNW |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Self-hostable | ✅ | — | — | ◧ | ◧ | — | ✅ | ✅ | ✅ | ✅ | n/a |
| Open source | ? | — | — | ✅ | ◧ | — | ✅ | ✅ | ✅ | ✅ | ✅ |
| No ads | ✅ | ◧ | ◧ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| No feed/source cap | ✅ | — | — | — | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Multi-tenant / multi-user | ✅ | n/a | n/a | n/a | n/a | n/a | ◧ | ✅ | ✅ | ✅ | — |
| Single binary + SQLite (no external DB) | ✅ | n/a | n/a | n/a | n/a | n/a | — | ✅ | — | — | n/a |
| **Server health surfaced inside the reader** | ✅ | n/a | n/a | n/a | n/a | n/a | — | ◧ | ◧ | ◧ | n/a |
| **Per-RPC latency (p50/p95/max) in the UI** | ✅ | n/a | n/a | n/a | n/a | n/a | — | — | — | — | n/a |
| Live activity log in the UI | ✅ | n/a | n/a | n/a | n/a | n/a | — | ◧ | — | ◧ | n/a |
| Operator CLI | ✅ | n/a | n/a | n/a | n/a | n/a | ◧ | ✅ | ✅ | ✅ | n/a |
| Verified nightly backups | ✅ | n/a | n/a | n/a | n/a | n/a | — | ◧ | ◧ | ◧ | n/a |
| Version-skew detection between client and server | ✅ | n/a | n/a | n/a | n/a | n/a | ? | — | — | — | n/a |
| Password policy + lockout | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ◧ | ✅ | ◧ | n/a |
| **Role enforcement** | ✅ | ✅ | ✅ | n/a | n/a | n/a | ◧ | ✅ | ✅ | ✅ | n/a |
| Admin console | ○ | n/a | n/a | n/a | n/a | n/a | ◧ | ✅ | ✅ | ✅ | n/a |
| Runs entirely in a browser tab as a demo | ✅ | — | — | — | — | — | — | — | — | — | — |

**The operator tabs are a real and unusual idea.** Settings → Server / Activity / Speed exist because
of a correct observation about self-hosting: *nobody is tailing a log file behind this, and the
person running it is the person reading it.* No self-hosted competitor puts p95 per-RPC latency and a
live log ring in the reader's own settings. Neither does any hosted one, because they don't have to.

**One thing must be said against this column.** The deployment story is written, reasoned and
**never run against a real droplet**, which means the twenty-minute runbook is a hypothesis.

> **Corrected 2026-07-27.** This paragraph also carried `FEATURES.md`'s claim that roles are "stored
> and not enforced". They are enforced — `AuthzUnary` is in the interceptor chain at
> `internal/app/app.go:906` with `AuthzStream` beside it. Two separate commits landed the map and its
> refusal logging. A doc that reports a shipped security control as absent is its own kind of hazard:
> it was about to be re-implemented.

---

## Where ArticleFlux actually wins

Ranked by how hard the win would be for a competitor to copy.

1. **Model-written, machine-validated follow rules for feedless sites.** Not "we call an LLM" — the
   discipline around it: robots.txt checked *before* the spend, selectors compiled and run against
   the real page, refusal on one-item / identical-title / no-container results, exactly one retry
   with the specific failure as input, JSON-endpoint fallback when the page builds itself in the
   browser, and no confidence score anywhere (five real headlines are evidence; a model's
   self-assigned number is not). Nobody else in this table attempts it.
2. **Listening as an egress decision.** Two engines, one control, the toggle next to play, per-item
   permanent cache, concurrent presses collapsing to one paid call, summarise-before-reading as a
   first-class option. Inoreader and Readwise have TTS; neither frames or gates it this way.
3. **Connection honesty.** Five states with five remedies, the anti-goal stated outright, a durable
   outbox that survives tab close, three operations refused rather than queued, a staleness badge.
   The whole field ships a dot.
4. **Ranking that explains itself in parts.** Not one score and not one reason: ordered clauses, the
   slot each pick filled, whether free Smart or paid Smart+ produced it, and machine keys underneath
   the prose. Every competitor that explains a ranking explains it as a single claim. Breaking it
   into clauses is what would make it *correctable* — which is the half we have not built (F4a), and
   is why this is fourth rather than first.
5. **BYO key with the meter visible.** Your OpenAI key, your spend, tokens in/out/requests on screen,
   labelled as a signal rather than a bill. Inoreader's BYOAI (April 2026) is the closest thing and
   still runs through their product.
6. **Operator surfaces inside the reader.** Server, Activity and Speed tabs; version-skew detection;
   verified backups; boot-time refusal on an unmapped RPC.
7. **No caps, no ads, no tier.** Feedly's free tier is 100 sources with no AI; Inoreader's free tier
   is ad-supported at 150 feeds; NewsBlur's is 64 sites. ArticleFlux's constraint is your disk.
8. **Craft-level list behaviour** — true-total scrollbars, position-driven filling, scroll-anchored
   prepends, skeletons, a motion system with a reduced-motion contract, i18n at zero hardcoded
   strings. Individually small; collectively the reason it feels unlike a self-hosted app.
9. **A full demo that runs in a browser tab** with the server compiled into the same module, where
   the three server-dependent features refuse through the same API the real server would. No
   competitor offers a no-signup full-fidelity trial of this kind.

## Where ArticleFlux loses

Ranked by how much it costs a real user.

1. **No mobile app and no sync API.** The single largest gap. It is a *web reader on a laptop*,
   competing against products people read on a phone in a queue. Shipping the Google Reader API is
   the cheap version of fixing this — it hands the phone problem to NetNewsWire and Reeder.
2. **No rules UI.** The engine is done. Every paid competitor and two free self-hosted ones ship
   this. Until the screens exist, "automation" is a column ArticleFlux forfeits entirely.
3. **Ranking you cannot argue with.** My Feed ships with per-clause reasons — better explainability
   than anything else in the table — and there is no tuning panel and no suppressed view, so a
   reader who disagrees with a judgement has nowhere to put that. NewsBlur's trainer is exactly this
   affordance, and it is half of what §18.9 promises.
4. **Live updates don't arrive** — for want of a call site. Server RPC and client pump both ship;
   nothing calls the pump. Everyone else pushes.
5. **No notifications, no webhooks, no integrations, no sharing.** Miniflux alone ships 25+
   integrations for free.
6. **No newsletters, no podcast playback, no PDF/EPUB.** Feedbin and Readwise both eat the "one inbox
   for everything I read" positioning that ArticleFlux does not contest.
7. **No import/export UI.** The engine round-trips OPML, Netscape bookmarks and Chrome JSON — and
   there is no data tab, which means **there is no supported way for a new user to bring 151 feeds
   in.** For a self-hosted reader whose users are all migrating from somewhere, this is closer to a
   blocker than to a gap.
8. **No bookmarks/archiving UI and no preservation banner.** The tiered archival, distress sweep and
   never-evict-a-dead-origin rules are built and invisible.
9. **Deployment never actually run.** Until one real droplet is stood up, "self-hostable" is a
   claim, not a feature — and it is the claim the entire positioning rests on.
10. **Search is items-only.** Notes and bookmark FTS indexes exist, unwired.
11. **No permanent archive guarantee**, no trends/feed-health view, no highlights.
12. **The documentation under-reports the product's two strongest server-side features** — the
    ranked home and role enforcement (§28.4). That is a competitive weakness, not just an internal
    one: the catalogue is what a comparison, a README or a landing page gets written from — and this
    document was, and it was wrong in the direction that costs a sale.

## The honest one-line summary

**ArticleFlux is, today, a very well-built single-machine reading surface with an unusually
principled approach to model spend, audio, ranking explainability and connection state — reachable
only from a desktop browser, on an instance you cannot yet import your feeds into.** Its shipped
feature set beats Miniflux and NetNewsWire on reading experience, matches FreshRSS on
breadth-of-ingest while beating it decisively on polish and losing to it on interop, and competes
with Feedly Pro+ and NewsBlur Archive on the ranking itself while losing to both on everything that
lets a reader *correct* the ranking.

The comparison makes the priority order obvious, and it is not the one a feature list would suggest:
**data import UI → rules UI → Google Reader API → the event pump's call site → the tuning panel.**
The first four are all wiring, not invention.

---

## Method and caveats

- ArticleFlux rows started from `docs/FEATURES.md` (2026-07-27) and were then **checked against the
  proto contract and the import graph**, which corrected two of them — ranking and role enforcement,
  both under-reported. Four further corrections went the other way and were errors in the checking
  rather than in the catalogue (plan §28.4); the rule that settles both directions is that a
  package's existence says nothing about its reachability, and its import edge says everything. Where this document and `FEATURES.md`
  disagree, this one was verified against the code and that one was not. The plan records the rule
  that follows in §28.4: shipped-state is read from the proto and the client, never from a document.
- `FEATURES.md` also records five behavioural defects and a non-green test tree as of the same date;
  those are not reflected as ✅ downgrades here, so the shipped column is *slightly* optimistic in
  the ways that file names.
- Competitor rows are from vendor pages and 2026 third-party comparisons. Third-party comparison
  sites are frequently affiliate-funded and their feature claims are not independently tested; where
  a claim was not confirmed on a vendor page it is marked `?` rather than guessed.
- Prices are list prices in USD as of July 2026 and move often. Annual and monthly rates are both
  given where they differ materially.
- `?` means unverified, not absent. There are more `?` marks in the ArticleFlux-favouring rows
  (server-side layout, true-total scrollbars, outbox durability) precisely because those are
  implementation details vendors do not document — a competitor may well do them.

## Sources

- [Inoreader Pricing 2026 — Readless](https://www.readless.app/blog/inoreader-pricing-2026)
- [Feedly vs Inoreader vs NewsBlur 2026 — Readless](https://www.readless.app/blog/feedly-vs-inoreader-vs-newsblur-2026)
- [Feedly Pricing 2026 — Readless](https://www.readless.app/blog/feedly-pro-pricing-vs-readless-2026)
- [Feedly Pro+ and Leo AI — Coywolf](https://coywolf.com/news/productivity/feedly-pro-plus-leo-ai/)
- [RSS Reader Pricing Comparison 2026 — Nutshell](https://www.nutshellnewsletter.com/tools/rss-reader-pricing)
- [Best RSS Feed Readers in 2026 — Nutshell](https://www.nutshellnewsletter.com/blog/best-rss-feed-readers-2026)
- [NewsBlur Features](https://www.newsblur.com/features) · [Ask AI & Daily Briefing](https://www.newsblur.com/features/ask-ai) · [Premium Archive](https://www.newsblur.com/pricing/archive) · [Intelligence Training](https://www.newsblur.com/features/intelligence-training)
- [NewsBlur vs Feedbin](https://www.newsblur.com/compare/feedbin) · [NewsBlur vs The Old Reader](https://www.newsblur.com/compare/the-old-reader)
- [Miniflux features](https://miniflux.app/features.html)
- [FreshRSS](https://freshrss.org/index.html)
- [FreshRSS vs Miniflux vs Tiny Tiny RSS — Pi Stack](https://www.pistack.xyz/posts/self-hosted-rss-readers/)
- [Self-hosting alternatives: RSS readers — selfh.st](https://selfh.st/alternatives/rss-readers/)
- [Folo — GitHub](https://github.com/RSSNext/Folo)
- [Readwise Reader Pricing 2026 — Readless](https://www.readless.app/blog/readwise-reader-pricing-2026)
- [The 3 best RSS reader apps in 2026 — Zapier](https://zapier.com/blog/best-rss-feed-reader-apps/)
