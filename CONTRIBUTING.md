# Contributing to ArticleFlux

This document is short on purpose. Almost everything about how this repository works is already
written down in `plan.md`; what follows is the part you need before your first change.

## Before you write anything

```powershell
./scripts/make.ps1 tools     # buf and the protoc plugins
./scripts/make.ps1 build
./bin/ArticleFlux.exe seed
./scripts/make.ps1 dev       # http://127.0.0.1:9000
```

You need Go 1.26+ and a sibling checkout of
[GoWebComponents](https://github.com/monstercameron/GoWebComponents) — v5.0.0 exists in that
project's changelog but was never tagged, so `go.mod` carries a `replace` directive to `../GoWebComponents`.
That is the one piece of setup that is not in the box, and it disappears the day the tag is pushed.

Node is required only for the e2e suite. The application has no JavaScript toolchain at all.

## The document set, and which one wins

Four documents, one specification. Each owns something the others must not duplicate, because
duplicated facts drift and drifted facts get implemented.

| Doc | Owns |
|---|---|
| **`plan.md`** | The spec of record — decisions (`A#`), open questions (`D#`), risks (`R#`), schema, services, milestones (`M#`), tests (`T#`) |
| **`TODO.md`** | Build order — dependency-ordered tiers, the five gates, and the page / settings / component / flow inventories |
| **`FLOWS.md`** | The nine paths that are easy to get subtly wrong, drawn so the wrong version is visibly wrong |
| **`design/`** | The visual spec — palette, type, layout, interaction. Mockups, **not** source |

`plan.md` wins. If `TODO.md` or `FLOWS.md` contradicts it, they are wrong and get fixed.
**If the implementation contradicts `plan.md`, the plan is wrong — and it gets corrected in the same
change, not later.** A spec that has quietly drifted from the code is worse than no spec, because it
still gets trusted.

Refer to things by their stable identifiers (`A14`, `D2`, `R4`, `§20.6`, `G1`, `8.4a`) rather than by
prose. They are all greppable, which is the point.

## When the spec is silent

The plan is deliberately detailed and it is still not complete — no plan is. This is the rule for the
edge, and it exists because **work produced by guessing looks finished.** A wrong variable name is
visible in review; a wrongly-invented table is not.

| Situation | What to do |
|---|---|
| An open **`D`** covers it | **Stop. Ask.** Record the answer as a resolution. Never pick one and proceed |
| No `D`, and the choice is **local** — a name, an error string, a fixture shape, a helper's signature | **Decide it and move on.** Note it in the commit message |
| No `D`, and the choice is **structural** — a table, a column, an RPC, a capability, a transport, an index | **Stop and ask.** It becomes a new `D` or a new `A`, written down before the code |
| A **new third-party dependency** | **Always ask**, without exception. Every dependency is a permanent decision made in thirty seconds |
| The implementation contradicts the plan | The plan is wrong. Fix it *in the same change* |
| A **gate** (`G1`–`G5`) hasn't been run | Run it. Its whole purpose is that the answer cannot be reasoned to |

**Two things are never silent-decidable**, whatever the pressure: anything touching **tenant
isolation** (§6.1) and anything touching the **LLM egress boundary** (§18.8). Both fail silently,
both are security properties, and both have a test that must be extended alongside any change.

## The four structural guards

`./scripts/make.ps1 lint` runs `internal/tools/guards`, which fails the build on any of:

1. **A `.css` file anywhere in the tree.** All styling is authored in Go through the GWC `css`
   package. There is no escape hatch, because one stylesheet becomes ten.
2. **Application JavaScript.** `web/index.html` carries the boot shim and nothing else.
3. **SQL outside `internal/store`.** One package knows the schema. A query in a handler is how a
   schema becomes undocumented.
4. **`syscall/js` outside `client/platform`.** Everything else in `client/` must compile and be
   testable natively, which is what makes the client testable at all.

Each guard corresponds to a decision that is otherwise only written down. If a guard is in your way,
the conversation is about the decision, not about the guard.

## Changing the contract

`proto/ArticleFlux/v1` is the only contract, and both ends are generated from it.

```powershell
./scripts/make.ps1 gen       # buf generate
./scripts/make.ps1 lint      # buf lint + buf breaking
```

**Additive-only within v1.** The client is cached by a Service Worker, so old clients exist in the
wild by construction — a renumbered or repurposed field is a v2, and `buf breaking` in CI is what
enforces that rather than discipline. New mutable fields on an update RPC are optional, with unset
meaning *leave it alone*, so a client that knows about half the fields cannot blank the other half by
omitting them.

## Tests

```powershell
./scripts/make.ps1 test      # go test ./...
./scripts/make.ps1 e2e       # Playwright, desktop + phone, against a real server
```

- Native Go tests are the default. `syscall/js` is quarantined precisely so most of the client can be
  tested without a browser.
- Anything visual, anything about scroll, focus, or event delivery — **e2e, in a real browser.** Two
  of the most expensive bugs in this project's history were invisible to every test that did not open
  one: a style that silently did nothing, and an event handler receiving the wrong argument.
- The e2e suite runs on Windows, natively. There is no Docker and no WSL in the loop.
- The wasm bundle is ratcheted against `wasm-baseline.txt`. Growing it more than 5% fails the build.
  If the growth is deliberate, bump the baseline in the same commit and say why.

## Commit messages

Look at `git log` before writing one. The house style is a plain subject line followed by **prose
that explains the reasoning**, not a bullet list of what changed — the diff already says what
changed. What it cannot say is why the interval floor is politeness rather than performance, or why
five identical grey dots were deleted.

- Subject: imperative, no ticket prefix, no `feat:`/`fix:` tag.
- Body: paragraphs. Cite decisions by id. If you measured something, put the numbers in.
- If the change corrects `plan.md`, say so and say what was wrong.
- If you made a local decision because the spec was silent, record it here. That is what makes it
  reviewable later.

## Pull requests

Fill in the template. It asks four things, and all four are things a reviewer cannot recover from the
diff: which decision this implements, what you decided that the spec did not cover, how it was
verified, and whether the contract moved.

Run `./scripts/make.ps1 lint` and `./scripts/make.ps1 test` before opening it. CI runs both, plus the e2e suite,
staticcheck, `buf breaking` and the size ratchet — but finding it locally is faster for everyone.
