# AI features, their toggles, and the path to SchemaFlux

Written 2026-08-04. Scope: every AI-backed capability in ArticleFlux, the switch that governs each
one, and a phased plan for moving the LLM path onto `github.com/monstercameron/schemaflux`.

This is a working document, not a spec. Behaviour is described in `docs/FEATURES.md`; the intent
behind it is in `plan.md`. Neither is restated here — entries link out instead, per the
no-duplicated-facts rule in `CONTRIBUTING.md`.

> **Revised 2026-08-07, against the local checkout rather than from memory.**
>
> **It is called SchemaFlux now.** The module is `github.com/monstercameron/schemaflux`; the
> directory on this machine is still `Desktop/SchemaFlow`, and the internal environment variables
> read `SCHEMAFLUX_*`. Everything below has been renamed. This document keeps its filename for the
> moment so the links in `TODO.md` do not break — worth fixing in the same pass as the OTEL entries
> that point at it.
>
> **And it is being actively developed**, on `main`, on this machine — the checkout read for this
> revision was committed the same day. That is the important thing about every judgement here: the
> gap list below was measured against a moving target three days earlier, and **three of the ten
> gaps have already closed**, one of them exactly as Phase 2 asked for. A gap table against a repo
> somebody is working on daily is a photograph, not a fact, and the ✅/⚠ markers now record the date
> each one was last actually looked at.
>
> The rule that follows from that: **re-read the source before acting on any row of §6.** Two of the
> blockers were closed by work that had nothing to do with this document.

> ## Where the migration actually stands — 2026-08-07
>
> | Phase | State |
> |---|---|
> | **0 — Decide** | ✅ all three, by Cam. SchemaFlux is confined to `internal/llm`; a `replace` now, a `go get` when tagged |
> | **1 — Foundation** | ✅ **done.** SchemaFlux carries every Smart+ request and no feature behaves differently — the existing `internal/llm` suite passed unedited, which is what that claim means |
> | **2 — Upstream** | ◧ P2.1 and P2.2 landed upstream on their own. P2.3–P2.5 are **no longer blockers**: ArticleFlux keeps its own request body, so the missing `tools` field and per-request reasoning effort cost it nothing. They remain worth having for the library |
> | **3 — Migrate features** | ✅ **done, 2026-08-08.** Ten of the thirteen structured paths run on typed operations; A10 (hosted tools), A14 (runtime schema) and A15 (different API) stay manual and say so at their call sites. G10 was accepted as a trade rather than solved — see the phase |
> | **4 — Payoff** | ◧ T4 (spend cap) shipped; USD costing shipped as an engine with no screen. T3, T5, T6 and the meter's UI are open |
> | **5 — Toggle debt** | ◧ T2 shipped server-side (the switch's client half is still owed). **T1 is blocked on 10.16, not on this migration** — the request it would gate does not exist, and a switch that governs nothing is what the tab already refuses to ship |
>
> **The one thing with a deadline** is the `replace` in `go.mod`: until SchemaFlux is tagged, this
> repository builds only on a machine with the sibling checkout beside it. CI cannot build it and
> neither can a fresh clone.

**Decision gate before Phase 1 starts.** SchemaFlux is a new third-party dependency, and
`CONTRIBUTING.md:53` says every one of those is asked about "without exception". Nothing below gets
built until that is a yes. What it actually adds to the tree is in §6a — ArticleFlux already carries
a full OpenTelemetry stack, so the overlap is larger and the additions smaller than they look.

Status legend is `docs/FEATURES.md`'s: ✅ shipped · ◧ partial · ⚙ engine only · ○ planned.

---

# Part I — The inventory

## 1. Smart+ — the LLM-backed features

Fifteen paid paths. Fourteen go through `internal/llm` (OpenAI Responses API, one client); the
fifteenth is speech, which is a different API and a different package.

| # | Feature | Entry point | Spec | Status | Toggle |
|---|---|---|---|---|---|
| A1 | Rerank My Feed | `internal/smart/interest.go:151` `RerankCandidates` | §18.8b, F80 | ✅ | `feed.smartPlus` |
| A2 | Named-entity extraction | `internal/smart/interest.go:309` `ExtractEntities` | §18.8b | ✅ | `feed.smartPlus` |
| A3 | Topic naming | `internal/smart/interest.go:416` `LabelTopic` | §18.8b | ✅ | `feed.smartPlus` |
| A4 | Theme from a description | `internal/smart/theme.go:226` `Compose` | §20.16.3, F23a | ✅ | none — key-gated action |
| A5 | Theme from what you read | `internal/smart/theme.go:242` `Attune` | §20.16.3, F23b | ✅ | `ui.attune.smart` |
| A6 | Interface translation | `internal/smart/translate.go:131` `Catalog` | §10.5, F29 | ✅ | none — key-gated action |
| A7 | Feed category suggestion | `internal/smart/categorize.go:121` `Suggest` | F8 | ✅ | `smart.categorize` |
| A8 | Scrape rule from HTML | `internal/smart/scrape.go:139` `Propose` | §11 rung 5, §14.2, F9a | ✅ | `smart.follow` |
| A9 | Field paths from a JSON API | `internal/smart/scrapejson.go:47` `ProposeJSON` | §11.2b rung 5b | ✅ | `smart.follow` |
| A10 | Web-search site discovery | `internal/smart/websearch.go:86` `Find` | §18.7 rung 5 | ✅ | `discover.smartPlus` |
| A11 | Candidate relevance check | `internal/smart/relevance.go:158` `Check` | §18.7 rung 5 | ✅ | `discover.smartPlus` |
| A12 | Summarise before reading | `internal/smart/digest.go:141` `Speakable` | §10.7, F21 | ✅ | `tts.digest` |
| A13 | Broadcast scripting (FluxCast) | `internal/smart/podcast.go:646`, `:1278`, `podcastbeat.go:56` | §29.7, F22a, F25a | ✅ | `tts.podcast` |
| A14 | Article classification | `internal/smart/classify.go:125` `Enrich` | §27.4, F78 | ◧ engine built, never runs | `smart.classify` (no UI) |
| A15 | Spoken audio | `internal/tts` | §10.7, F20 | ✅ | `tts.smartPlus` |

Supporting code, no feature of its own: `taste.go` (randomised few-shot liked/disliked headlines for
A10), `distill.go` / `distilljson.go` (page → 6–12 KB outline before egress), `direction.go`
(FluxCast format intent → prompt text), `llmclient.go` (the two-method test seam).

## 2. Smart — deterministic, on-machine, free

Not in scope for the migration; listed because the boundary is the whole product argument and a
migration that blurs it is a regression. Nothing here calls a model, and nothing here should start.

| Package | Lines | What it does |
|---|---|---|
| `internal/derive` | 5,490 | Engagement log → interest layer |
| `internal/fluxcast` | 6,366 | Broadcast planning: beats, timing, format cascade |
| `internal/classify` | 2,346 | 26-category lexicon scorer, on by default |
| `internal/recommendjob` + `internal/recommend` | 2,575 | Candidate site scoring |
| `internal/textvec` | 1,690 | Vectors, no model, no service |
| `internal/rundown` | 1,530 | Ranked list → running order |
| `internal/rank` | 1,150 | Item score against a reader's signals |
| `internal/signals` | 1,034 | 25 observation kinds |
| `internal/topics` | 770 | Engaged items → named clusters |

`README.md:122` states the split for the one case where both halves touch the same screen: the
rundown is Smart, the broadcast is Smart+.

---

# Part II — Toggles

## 3. What exists

| Key | Scope | Default | Governs | Surface |
|---|---|---|---|---|
| `smart.openai_api_key` | system | unset | Everything Smart+ | Settings → Smart+ ✅ |
| `smart.model` | system | `gpt-5-mini` | Default model | Settings → Smart+ ✅ |
| `smart.model.small` / `.mid` / `.large` | system | unset | Per-tier model | ⚙ no picker |
| `smart.classify` | system | off | A14 egress consent | ⚙ switch renders **disabled** (`client/view/classifysettings.go:36`) |
| `smart.ui_translation.<locale>` | system | — | A6 catalog cache, not a toggle | — |
| `feed.smartPlus` | per reader | off | A1, A2, A3 | Settings → My Feed ✅ |
| `discover.smartPlus` | per reader | off | A10, A11 | Discover ✅ |
| `smart.follow` | per reader | off | A8, A9 | Add-a-feed ✅ |
| `smart.categorize` | per reader | off | A7 | Add-a-feed ✅ |
| `tts.smartPlus` | per reader | off | A15 | Settings → Listening ✅ |
| `tts.digest` | per reader | off | A12 | Settings → Listening ✅ |
| `tts.podcast` | per reader | off | A13 | Settings → Listening ✅ |
| `tts.podcastVibe` / `.podcastBed` / `tts.rate` | per reader | — | A13/A15 shape, not consent | Settings → Listening / FluxCast ✅ |
| `ui.attune.smart` | per reader | off | A5 | Settings → Appearance ✅ |

Every one of them is default-off. That is the property to preserve through the migration, and it is
the one a refactor most easily breaks: a toggle that becomes on-by-default spends money and egresses
content that nobody agreed to send.

## 4. What is missing

| # | Toggle | Why it is needed |
|---|---|---|
| T1 | `feed.smartPlusLabels` | Named in `internal/smart/classify.go:50` as the per-reader consent for the reader's own vocabulary leaving. **It does not exist anywhere in the code.** A14 cannot ship honestly without it |
| T2 | `SetSmartConfig` field for `smart.classify` | The key is read but no RPC writes it, which is why the Classification tab's two switches render disabled |
| T3 | Model-tier picker | `smart.model.small/.mid/.large` are read by the code and settable by nothing |
| T4 | Spend cap | There is a spend *meter* and no *limit*. One loop in a job bills without a ceiling. **Cheaper than this entry assumed:** SchemaFlux ships `mw.Budget(limit, …)` as middleware, enforced before the call, with an injectable cost function (§6b) — so this is wiring rather than building, once the dependency lands |
| T5 | `smart.provider` | Only meaningful after the migration, and the main reason to do it: a provider that is not OpenAI, including a local one |
| T6 | Per-feature kill switch | Today the only global off is deleting the key. An operator with a runaway feature has no smaller instrument |

T1 and T2 are prerequisites for A14 becoming ✅ and are worth doing regardless of whether SchemaFlux
lands. T4/T5/T6 are the payoff of it.

---

# Part III — Does SchemaFlux fit

## 5. What it gives

| | |
|---|---|
| **W1** | Cost tracking in USD, with cached and reasoning tokens broken out (`pricing.GetCostSummary`). Strictly better than `llm.Client.Usage()`, which counts tokens since process start and says so |
| **W2** | Retries, backoff, request and correlation IDs, structured `slog` output — all of which ArticleFlux either hand-rolls or does without |
| **W3** | A `Provider` interface, so a second provider is a registration rather than fourteen edits |
| **W4** | A typed operation vocabulary that lines up with what `internal/smart` already does by hand: `Ranking`, `Classifying`, `Choosing`, `Scoring`, `Summarizing`, `Rewriting`, `Translating`, `Extracting`, `Clustering` |
| **W5** | Its OpenAI provider already targets `POST /v1/responses`, so the API surface matches |

## 6. What it costs — the gaps, in the order they bite

| # | Gap | Detail | Severity |
|---|---|---|---|
| **G1** | ⚠ **still open, and still the one that can leak a key** (2026-08-07) | `client.go` calls `ops.SetDefaultProvider` in five places. ArticleFlux is multi-tenant with a per-instance encrypted key resolved per call from `ctx`, so calling `Init` or `WithProviderConfig` on a request path would leak one tenant's key into every other tenant's call. **What changed is the escape route, not the hazard:** the middleware seam (§6b) makes "a provider assembled per call and never registered globally" a first-class path rather than a workaround. The rule is unchanged — the fluent builders are off limits on a request path — but §7's shape is now the library's own idiom instead of a way around it | **Blocker for the fluent builders** |
| **G2** | ✅ **CLOSED** — strict JSON schema is sent | *Was:* SchemaFlux sent `text.format = {"type":"json_object"}` and strengthened the prompt. *Now* (`internal/llm/provider.go:190-201`, read 2026-08-07): when `CompletionRequest.JSONSchema` is set it emits `{"type":"json_schema", name, strict:true, schema}`, falling back to `json_object` only when no schema was given. `JSONSchema` and `SchemaName` are per-REQUEST fields, which is what the fourteen features need. This is P2.1, done upstream | ~~Blocker~~ |
| **G3** | ✅ **CLOSED** — `store` is sent, and defaults to off | *Now* `"store": provider.config.Store` (`provider.go:321`), with the zero value meaning retention off and `Store(true)` opt-in. Its comment makes exactly P2.2's argument, unprompted: "that is a surprising default for a library whose whole job is running arbitrary user records ... through a model". **One difference from what P2.2 asked for:** it is a PROVIDER-level setting, not `CompletionRequest.Store *bool`. Harmless for ArticleFlux, which wants `false` on every call and now gets it by default — but a caller needing per-request retention has no way to ask | ~~Blocker~~ |
| **G4** | ⚠ **still open** (2026-08-07) | A10 sends `tools: [{"type":"web_search"}]`. `CompletionRequest` has no `Tools` field and the Responses body has no `tools` key. `internal/tools/http.go`'s `web_search` is a local function-calling tool, not the hosted one. P2.3 is the outstanding one | **Blocker for A10** |
| **G5** | ⚠ **still open** (2026-08-07) — model comes from env, by speed tier | `config.GetModel(intelligence, provider)`, now reading `SCHEMAFLUX_MODEL` and the per-tier variables. ArticleFlux resolves the model per instance from the database, and the tier keys already exist for it | High |
| **G6** | ⚠ **still open** (2026-08-07) — effort inferred from the model name | `reasoningEffort(model)` at `provider.go:210-212`, plus a forced `verbosity: "low"`, and no `CompletionRequest` field to override either. ArticleFlux sets effort per request, deliberately low for structural questions and default for the rest | Medium |
| **G7** | ◧ **narrowed** (2026-08-07) | The per-call client is gone — it is one `http.Client` per provider now, with a comment saying why. `ProviderConfig` still has no `HTTPClient`/`RoundTripper` field, so P2.5 stands. But the reason it mattered — "what stops the test suite from billing" — is answered a different way: **`schemafluxtest/` now ships `provider.go`, `install.go` and `cassette.go`**, a record/replay provider. Worth reading before writing another fake | Low |
| **G8** | ◧ **reframed** (2026-08-07) | Still no breaker, but there is now a **middleware seam** — `mw.Handler = llm.Provider`, `mw.Middleware func(Handler) Handler`, `mw.Chain(base, …)` — so a breaker is a decorator rather than a gap. `internal/llm/breaker.go`'s `Guard` can wrap the provider instead of the call. See §6b | Low |
| **G9** | ⚠ **still open** (2026-08-07), and fine | No `Models()`/`ListModels` anywhere. `llm.Client.Models()` populates the Settings model picker and filters out embeddings, whisper, tts, dall-e and image models. No equivalent | Low — keep ours |
| **G10** | ⚠ **still open** (2026-08-07) — `Extracting[T any](input any)` accepts anything | Unchanged at `fluent.go:70` and `internal/api/fluent/entrypoints.go:14`. The egress boundary in this repo is enforced by *types*: `llm.RankPayload`, `ClassifyPayload`, `RelevancePayload`, `ThemePayload`, `WebSearchPayload` have fields only for what may leave, assembled in exactly one package. A builder that marshals an arbitrary `any` deletes that guarantee at the call site | **Blocker by policy** |

## 6a. What it actually adds to the dependency tree

**Both of the concerns this section was written about are gone.** Re-read 2026-08-07:

ArticleFlux already requires `go.opentelemetry.io/otel` v1.44.0 with the SDK, metric, trace,
Prometheus and OTLP-over-HTTP exporters — see `go.mod`. This section used to say SchemaFlux pinned
**otel v1.38.0** and would have to compile against a version six minor releases newer than its own
pin, and called that "the first thing to test, not the last". **It now pins v1.44.0** for `otel`,
`otel/sdk`, `otel/trace` and `otel/metric` — the same versions, so there is nothing to resolve and
nothing to test. Only the three exporters below are still on 1.38.0, and they are additive.

| Added | Note |
|---|---|
| `github.com/sashabaranov/go-openai` v1.20.4 | The real new dependency, and still the only one. SchemaFlux's Responses path hand-rolls the HTTP call anyway; the SDK is used for the chat-completions providers |
| ~~`otel/exporters/jaeger` v1.17.0~~ | ✅ **gone.** It was the strongest objection here — deprecated and archived upstream, a module that would never get another security fix, arriving for a feature ArticleFlux would never turn on. It is no longer in `go.mod` at all |
| `otel/exporters/otlp/otlptrace` + `otlptracegrpc` v1.38.0 | ArticleFlux uses the **HTTP** OTLP exporter. This adds the gRPC one alongside it |
| `otel/exporters/stdout/stdouttrace` v1.38.0 | Debug exporter |
| `gopkg.in/yaml.v3`, `dustin/go-humanize`, `google/uuid` | Indirect |

So the dependency argument is now considerably weaker than when P0.1 was written, and P0.1 should be
re-put on those terms: one real third-party module, and an OTel overlap that is a straight win.

The interaction with ArticleFlux's own telemetry is still a hazard rather than a benefit, and it is
tracked as **OTEL-15** in `TODO.md`. Re-checked and unchanged: `telemetry/tracing.go:140-141`
(the entry says `:138`, which has since moved) calls `otel.SetTracerProvider` and
`otel.SetTextMapPropagator`, which would silently replace the providers `internal/telemetry` built.
It is opt-in, so the rule is simply never to call it.

## 6b. What arrived while this document sat — the middleware seam

Not in the original survey, and it changes more of the plan than the closed gaps do.
`mw/` is a decorator seam over the provider interface itself:

```go
type Handler = llm.Provider
type Middleware func(next Handler) Handler
func Chain(base Handler, mws ...Middleware) Handler
```

Because `Handler` IS `llm.Provider`, ArticleFlux's own `sfprovider` can be the base and everything
else composes around it, per call, never touching the global registry that G1 is about. Shipped
middleware: `retry.go`, `ratelimit.go`, `fallback.go`, `cache.go`, `redact.go` and — the one that
matters most here — **`budget.go`**:

```go
func Budget(limit float64, opts ...BudgetOption) Middleware
func WithBudgetCostFunc(calc func(usage *types.TokenUsage, model, provider string) *types.CostInfo) BudgetOption
```

That is **T4**, the spend cap, which §4 lists as missing and P4.2 schedules as work. It enforces
before the call and takes an injectable cost function, so it can be pointed at whatever pricing
table this instance believes. P4.2 should be re-scoped from "build it" to "wire `mw.Budget` and
decide where the limit is configured".

`CompletionRequest` also grew `PromptCacheKey`, which is worth reading against **Risk 4**: cache
keys are the thing a careless migration re-bills a reader's whole back catalogue over, and the
library now has an opinion about them.

## 7. The shape that resolves all ten

**SchemaFlux goes underneath `internal/llm`, never above `internal/smart`.**

```
internal/smart/*        assembles llm.*Payload           ← unchanged, still the only egress point
        ↓
internal/llm            Request{Schema, Tools, Effort}   ← unchanged public API, Guard, Models()
        ↓
internal/llm/sfprovider implements schemaflux llm.Provider  ← new: key from ctx, model from settings
        ↓
mw.Chain(sfprovider, …)  retry, ratelimit, budget, redact  ← per call, never ops.SetDefaultProvider
        ↓
schemaflux              retries, IDs, logging, pricing
```

`Provider.Complete(ctx, req)` takes a `context.Context`, and ArticleFlux already carries the tenant
scope in `ctx` — that is exactly how `llm.KeyFunc` works today. So a provider that reads key, model
and tier out of `ctx` on every call resolves G1 and G5 without touching a single caller, and keeps
G7's transport seam because we own the HTTP client inside it.

G10 is resolved by not using the fluent builders at the `internal/smart` layer at all, or by using
them only with a `llm.*Payload` as the input type. Which of those two we do is the one genuinely
open design question in this document — see Phase 3.

~~G2, G3,~~ **G4 and G6** need changes in SchemaFlux itself, and are down from four to two: G2 and G3
landed upstream on their own (§6, Phase 2). Both remaining ones are small and independently useful,
and only G4 blocks a feature.

The `mw.Chain` line is new in this revision and it is the part that makes the whole shape the
library's own idiom rather than a way around it. It is also where G8's breaker goes: `Guard` becomes
a `mw.Middleware` instead of a wrapper around the call site.

---

# Part IV — The path

## Phase 0 — Decide

**Decided 2026-08-07.** All three, by Cam, in one instruction: *"it's still in dev but the api shape
shouldn't change too aggressively, use the local version for now but later we will go get from
github, let's create a class for smart and use schemaflux to handle all smart features."*

- [x] **P0.1** Approved.
- [x] **P0.2** A `replace` against the sibling checkout, becoming an ordinary `go get` when it is
      tagged. The directive in `go.mod` says so and says why, because a replace is a build that only
      works on a machine with the sibling checkout — CI and anybody else's clone cannot build this
      until it goes. That is the one thing about this phase with a deadline.
- [x] **P0.3** **Answered twice.** First, 2026-08-07: SchemaFlux confined to `internal/llm`, on the
      argument that an API still moving weekly is one to depend on narrowly, and that the
      four-method `Provider` interface plus `mw.Chain` is its most stable surface.
      **Reversed 2026-08-08 by Cam:** "full use of schemaflux to replace the manual prompting unless
      directly needed". The features call the typed operations directly now, and Phase 3 happened as
      written apart from the three that cannot. The first answer was not wrong about the risk — the
      library's working tree broke this repository's build three times during the migration, which
      is exactly the exposure it warned about, and the reason the verification procedure in `go.mod`
      builds against `git archive HEAD` rather than the sibling working tree.

## Phase 1 — Foundation, zero behaviour change

Goal: SchemaFlux is in the tree and carrying every request, and no feature behaves differently.

- [x] **P1.1** Added, with a `replace` against the sibling checkout and a comment saying what has to
      happen before anyone else can build it. The otel half needed nothing: it pins v1.44.0 itself
- [x] **P1.1b** Guarded. `InitTracing` is one of the ten names the structural guard refuses — see
      P1.6, which is the same guard, because it is the same class of mistake
- [x] **P1.2** `internal/llm/sfprovider`. It adapts this repo's own audited call rather than
      wrapping SchemaFlux's OpenAI provider — the file's doc gives the three reasons, of which the
      load-bearing one is that the egress guarantees are §18.8 promises and a promise kept by a
      dependency is re-audited on every upgrade of it.
      **The assertion worth knowing about:** `mw.Handler` is a type ALIAS for a `Provider` in
      SchemaFlux's `internal/llm`, a package the toolchain forbids this repo from importing. The
      whole shape is buildable only because the root re-exports it with `=`. A test asserts both
      interfaces, so the day that becomes a definition instead of an alias, it says so
- [x] **P1.3** `Do` builds a `schemaflux.CompletionRequest` for the middleware to read and calls
      `mw.Chain(base, …)`; the audited request moved to `send`, unchanged. `Request` and every
      sentinel are untouched, and llm_test.go's existing cases passed without an edit — which is the
      actual evidence for "no feature behaves differently"
- [x] **P1.4** `WithGuard` installs it, outside the chain. Retry inside, breaker outside — a breaker
      that counted each retry separately would trip at a third of its threshold.
      **Not installed by default**, which is the honest state: TODO 11.15 records that `Guard` has
      been built and unused since it was written, and turning it on is a behaviour change that wants
      its own decision rather than arriving inside a migration
- [x] **P1.5** Untouched. G9 says keep ours and there was no reason to revisit it
- [x] **P1.6** Stronger than asked: a **structural guard** (`internal/tools/guards`) rather than a
      test, so it runs in `lint` over every file in the repository instead of over one package. It
      refuses ten names — `Init`, `SetDefaultClient`, `GetDefaultClient`, `InitWithEnv`,
      `SetDefaultProvider`, the four `WithProvider*` methods, and `InitTracing` — in any file that
      imports SchemaFlux, tests included. Nine cases of its own, including one proving it catches
      every name and one proving it does **not** flag the per-call shape, since a guard that forbade
      the fix would be worse than none
- [ ] **Gate:** `go test ./internal/llm/... ./internal/smart/...` green with the fakes, no live call.
      Per standing rule, no provider-backed test runs in verification

## Phase 2 — Upstream SchemaFlux

Five changes, each with its own tests in the SchemaFlux repo. **Two of them already landed** —
not from this document, which nobody upstream has read, but because they were the right thing
independently. Re-checked 2026-08-07.

- [x] **P2.1** `CompletionRequest.JSONSchema` / `SchemaName` → emits
      `text.format = {"type":"json_schema", name, strict:true, schema}` (G2). Per request, which is
      what the fourteen features need
- [x] **P2.2** `store` is emitted (G3), defaulting to `false` — the same argument this entry made,
      arrived at independently. **Landed as a PROVIDER setting rather than
      `CompletionRequest.Store *bool`.** That is enough for ArticleFlux, which wants `false` on every
      call; if a per-request override is ever wanted it is still owed
- [ ] **P2.3** `CompletionRequest.Tools []string` → emit the hosted `tools` array (G4). **The only
      one blocking a feature** — A10 cannot migrate without it
- [ ] **P2.4** `CompletionRequest.ReasoningEffort` overrides the name-derived default (G6). Still
      `reasoningEffort(model)` plus a forced `verbosity: "low"`
- [ ] **P2.5** Optional: `ProviderConfig.HTTPClient` so callers can inject a transport (G7). Less
      pressing than it was — `schemafluxtest`'s cassette provider covers the offline-testing reason
      this existed for, and `sfprovider` owns the client anyway

## Phase 3 — Migrate features, cheapest first

> **Done, 2026-08-08.** Every paid path that CAN run on a typed operation now does. The three that
> cannot are named at the bottom with the reason, and each carries the same note at its call site.
>
> This reverses the P0.3 answer recorded here on 2026-08-07, on Cam's instruction: "full use of
> schemaflux to replace the manual prompting unless directly needed". The earlier answer confined
> SchemaFlux to `internal/llm`, which bought the middleware chain, the retry policy, the budget and
> the pricing tables for every feature at once without a line changing in any of them. What it did
> not buy was W4 — the typed operation vocabulary — because reaching it means the call site hands an
> `any` to the library rather than an `llm.*Payload`, which is **G10**: the egress boundary in this
> repo was enforced by TYPES, and `llm.RankPayload` and its siblings have fields only for what may
> leave.
>
> **G10 was accepted as a trade, not solved.** The boundary is now enforced by the reply types and
> the assembly functions instead — `podcastInput`, `segmentGroupInput`, `rerankInput` and their
> siblings are still the only places an outbound body is built, and they still take plain structs.
> What is gone is the compiler's guarantee that nothing else can be marshalled. What replaced it is
> a test per feature asserting what actually reached the provider (`requestSent`), which is a weaker
> guarantee about the code and a stronger one about the wire.

### What each feature runs on now

| # | Feature | Operation | Notes |
|---|---------|-----------|-------|
| A1 | Rerank | `Extracting[rerankReply]` `.Smart()` | Not `Ranking`: the reply carries a REASON per pick, and the ids are ordinals into this request only |
| A2 | Entities | `Extracting[entityReply]` `.Fast()` | |
| A3 | Topic label | `Generating[string]` `.Fast()` | Answers with the bare label; an empty one is mapped back to the unusable-label error |
| A4/A5 | Themes | `Generating[paletteReply]` `.Smart().Strict()` | AA repair still server-side, unchanged |
| A6 | Translation | `Extracting[translationBatch]` | Batched by key; `text` is a pointer so one untranslatable string does not fail sixty |
| A7 | Category | `Choosing[string](options).By(rules).Steer(about)` | With a `Generating[string]` invent path for the new-name case |
| A8/A9 | Scrape rules | `Extracting[scrapeAnswer]` / `Extracting[jsonAnswer]` | Still fed the distilled outline, never raw HTML |
| A11 | Relevance | `Extracting[relevanceVerdict]` | `Relevant *bool`, so absent and false stay different answers |
| A12 | Digest | `Summarizing(...).Steer(brief).MaxLength(...)` | An all-noise answer still resolves to `ErrNothingToSummarise` |
| A13 | Broadcast | `Summarizing` per segment, `Extracting[podcastGroupReply]` grouped | Cache keys and prompt versions deliberately unchanged |

### Three things learned doing it, worth not rediscovering

- **A derived schema makes every field required, and an empty string fails it.** Four fields in this
  repo are genuinely optional — `rerankPick.Why`, `entityMention.Label`, `translationEntry.Text`,
  `relevanceVerdict.Relevant` — and all four are pointers now, which is the library's documented
  remedy and also the more honest shape: absent and empty are different answers.
- **`.Strict()` is not a synonym for "careful".** It rejects a field the schema does not name, which
  SchemaFlux's own note calls "exactly wrong for an operation whose contract permits one". A1, A2,
  A6 and A13 tolerate extra fields on purpose and say so at the call site; A4/A5 keep Strict.
- **Caller steering never reaches the system prompt.** SchemaFlux routes it into the user prompt and
  refuses steering that tries to cross (`verifyTrustBoundary`). Tests can assert "the brief reached
  the model", never "it reached it as a system instruction".

### What stays manual, and why

- **A10 web-search discovery.** Hosted tools. The model calls a search tool server-side and the
  answer is a tool transcript, not a typed value — there is no operation whose contract that is.
- **A14 classification.** `registry.Build` composes the schema at RUNTIME from the reader's own
  categories. There is no Go type to derive one from, which is the whole premise of `Extracting[T]`.
- **A15 speech.** A different API entirely. SchemaFlux has no speech surface; `internal/tts` is
  untouched.

`internal/llm.Manual` names these three in one place so the list cannot rot silently.

### Verification

`go test ./...` (one pre-existing failure, `TestEveryRPCIsServedOrDeclaredUnserved`, from
unrelated in-flight `auth.proto` work), `scripts/make.ps1 lint`, `scripts/make.ps1 wasmtest`.
No provider-backed test runs in verification: `schemafluxtest.Install` answers every call
in-process, and the live tests stay behind `AF_LIVE=1`.

## Phase 4 — Collect the payoff

- [◧] **P4.1** The engine half is done and the screen is not. `llm.Client.Cost()` reports USD,
      computed per call with `pricing.CalculateCost` against the model that actually answered.
      **Not** `pricing.GetCostSummary`: that reads a package-level history in SchemaFlux, and a
      process-global cost store in a multi-tenant instance mixes one tenant's spend into another's —
      the same shape of problem as G1, arriving through the accounting rather than the credential.
      Accumulating per client keeps the number attributable.
      The distinction that has to reach the screen: a model the rate tables do not know returns
      `Priced: false`, and `Cost` carries `Priced`/`Unpriced` COUNTS beside the dollars so the meter
      can say "not priced" rather than "$0.00". Those are different claims and only one of them is
      true. **Still owed:** the Settings → Smart+ meter itself, and OTEL-2's metrics
- [x] **P4.2** **T4** spend cap, shipped 2026-08-07 — and **not** with `mw.Budget`, for one reason
      worth recording: its limit is a `float64` taken at construction, and the chain is built once at
      boot, so the ceiling would be whatever the setting said at startup for the life of the process.
      A cap whose entire purpose is to be raised by somebody watching a job hit it cannot be that.
      `llm.Client.Budget(CapFunc)` is ArticleFlux middleware — it composes in the same chain — and
      asks per call. `store.KeySmartBudgetUSD` holds it; absent, unparseable and "0" all mean no
      ceiling, because a malformed row must not silently become the strictest possible limit.
      Two honest limitations, both tested: the call that CROSSES the cap is allowed to finish (the
      ceiling is on what has been spent, not on an estimate of what is about to be — an estimate that
      is wrong refuses affordable work, which is how a cap gets switched off and left off), and
      concurrent calls can overshoot by however many are in flight. An exact cap needs a reservation
      taken before the call and released after, which is what `mw.Budget` does and is worth adopting
      the day its limit can be read per call.
      **No UI yet.** The key is settable only by whatever writes system settings, so this is an
      operator's instrument rather than a screen — P4.4's tab is where it belongs
- [ ] **P4.3** **T6** per-feature kill switches, one system key per A-number
- [ ] **P4.4** **T3** model-tier picker for the keys that already exist
- [ ] **P4.5** **T5** `smart.provider`, and prove it with one non-OpenAI provider end to end. This is
      the item that justifies the whole migration

## Phase 5 — Toggle debt, independent of all the above

- [ ] **P5.1** **T1** `feed.smartPlusLabels` — **blocked, and not by this migration.**
      Looked at 2026-08-07 and deliberately not built. The key gates whether a reader's own
      vocabulary may leave, and the request it would gate is **10.16's per-user labelling pass**,
      which does not exist. Building the key alone produces a switch that governs nothing — the
      thing the Classification tab's own comment says it refuses to ship, and rightly.
      *Done when: 10.16 lands. Not before.*
- [x] **P5.2** **T2** done 2026-08-07. `SetSmartConfigRequest.classify_enabled` (optional) writes
      `smart.classify`, and `GetSmartConfigResponse.classify_enabled` reads it back. Optional
      matters: a field that could only carry true or false would turn every unrelated save — a
      model change, a new key — into a decision about egress that nobody made, and the direction it
      would drift is the one that spends. Five cases on the server, including that one.
      The same pass added `budget_usd` (T4's writer) and `cost_usd` / `priced_calls` /
      `unpriced_calls` (P4.1's numbers).
      **Still owed:** the Classification tab's switch is still rendered disabled. The server can
      take the value now; the client has not been changed to send it, so A14 remains ◧ until it is.
- [ ] **P5.3** Update `docs/FEATURES.md` §78 and the Classification tab copy once they are real

---

# Part V — Risks

1. **The global default provider (G1) is the one that can leak a key across tenants.** No request
   path may call `Init`, `WithProvider`, `WithProviderConfig` or `WithProviderInstance`. P1.6 exists
   to make that a test failure rather than a code review.
2. **Losing strict schemas (G2) degrades quietly.** `json_object` plus prompt-strengthening mostly
   works, which is worse than failing — the failures will look like bad prompts and land as
   intermittent nulls in the interest layer.
3. **`store: false` (G3) is a one-line omission with a permanent consequence.** Article text retained
   provider-side contradicts what the Smart+ egress copy tells readers.
4. **Cache keys.** Digests and broadcast segments are cached forever, keyed by prompt version. Any
   migration that changes a prompt re-bills every reader's back catalogue.
5. ~~**Dependency weight and version skew.**~~ **Largely retired 2026-08-07** — see §6a. The version
   skew is gone (SchemaFlux pins otel v1.44.0, the same as ArticleFlux) and so is the archived
   Jaeger exporter. What remains is one real third-party module, `go-openai`, and three additive
   1.38.0 trace exporters. This was the second-strongest argument against the migration and it has
   mostly evaporated; the strongest, Risk 6, has not.
   **The new risk in its place is that the target moves.** Two blockers closed in three days
   without anyone here asking. That is good, and it means a plan written against a version is a
   plan with a shelf life — pin a commit before Phase 1, or re-read §6 at the start of it.
6. **Nothing here is a reader-visible improvement.** The migration buys T4, T5 and T6 and better cost
   accounting; it buys no feature. That is a reasonable trade only if T5 is actually wanted.

# Open questions

1. ~~**P0.3** — builders at the `smart` layer, or SchemaFlux confined to `internal/llm`?~~
   **Answered: builders at the `smart` layer** (2026-08-08). The type-enforced egress boundary was
   traded for W4 deliberately; see Phase 3 for what replaced it.
2. Is a non-OpenAI provider (T5) genuinely wanted, or is it hypothetical? If hypothetical, Phase 1
   plus Phase 5 delivers most of the value without the dependency.
3. Should A15 (speech) eventually move too, which would mean SchemaFlux growing a speech API?
