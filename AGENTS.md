# AGENTS.md — orientation for agents working on ArticleFlux

This file is a **router, not a specification.** `CONTRIBUTING.md` says four documents form one
spec and each owns something the others must not duplicate, "because duplicated facts drift and
drifted facts get implemented." That applies to this file too, so nothing here is restated from
elsewhere — it tells you which document answers which question, and stops.

## Read these, in this order

1. **`CONTRIBUTING.md`** — how to work here: the document set and which one wins, what to do when
   the spec is silent, the structural guards, i18n rules, contract changes, tests, branches,
   commit messages, pull requests. Start here every time.
2. **`plan.md`** — the spec of record, and it **wins**. Decisions (`A#`), open questions (`D#`),
   risks (`R#`), schema, services, milestones (`M#`), tests (`T#`). If the implementation
   contradicts it, the plan is wrong and gets corrected *in the same change*.
3. **`TODO.md`** — build order: dependency-ordered tiers, the five gates, the page / settings /
   component / flow inventories.
4. **`FLOWS.md`** — the nine paths that are easy to get subtly wrong.
5. **`design/`** — the visual spec. Mockups, **not** source.

Refer to things by their stable identifiers (`A14`, `D2`, `R4`, `§20.6`, `G1`) — they are
greppable, which is the point.

## The things agents get wrong here

- **Branches: `dev` for work, `main` for releases, nothing else.** No feature branches.
  Promotion is a release that **deploys**, and it is Cam's call — never promote on your own
  initiative. Mechanics and the reasoning: `CONTRIBUTING.md` → "Branches".
- **`go build ./...` does not compile the client.** The wasm packages are behind `//go:build js`
  and a native build skips them entirely. `ci.yml` documents the batch where the wasm build stayed
  broken while every native build and test was green.
- **`go test ./...` does not test `client/view`.** That package is `//go:build js && wasm` with
  zero untagged files, so plain `go test` never compiles it. It runs under Node in the `wasmtest`
  job.
- **A known-red pin is deliberate, and there are none right now.** When a confirmed defect has to
  stay visible without holding the gate, `ci.yml` excludes the named test and runs it again,
  non-blocking. Do not "fix" one by adding `t.Skip()` — silencing it at the source decides a
  product question that is not yours to decide — and remove the exclusion the moment the test
  goes green; `ci.yml`'s `test` job records what leaving one in place cost.
- **GoWebComponents is an ordinary requirement in `go.mod`** — a published version, no `replace`,
  no sibling checkout to keep in step. A `replace` added to try a local GWC change must not be
  committed: CI, the image build and the deploy scripts all build from this repository alone.
- **Never commit secrets or the database.** `*.key`, `.env`, `articleflux.db*` and the various
  `*-cache/` directories are working state, not source.

## Verifying your work

`./scripts/make.ps1 lint` and `./scripts/make.ps1 test` before you claim anything is done. CI runs
those plus the e2e suite, staticcheck, govulncheck, the race detector, `buf breaking` and the
wasm-size ratchet — but finding it locally is faster, and "CI will tell me" is not verification.
