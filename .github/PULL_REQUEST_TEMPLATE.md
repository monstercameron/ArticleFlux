<!--
Four questions. All four are things a reviewer cannot recover from the diff.
Delete a section only if it genuinely does not apply, and say why.
-->

## What this does

<!-- The subject line of the commit, expanded. The reasoning belongs in the commit message. -->

## Which decision it implements

<!--
Cite by id — A14, D2, R4, §20.6, TODO 8.5. If this implements no decision, say that: it is not
disqualifying, it just means the decision is being made here and should be written down.
-->

## What the spec did not cover

<!--
Local calls you made because plan.md was silent: names, error strings, fixture shapes, helper
signatures. Structural calls (a table, a column, an RPC, an index, a dependency) should have been
asked about before the code — if one is here, say so.

If this change corrects plan.md, say what was wrong. The plan is corrected in the same change,
never later.
-->

## How it was verified

<!--
Not "tests pass". What did you actually run, and what did you see?
Anything visual, or about scroll, focus or event delivery, needs a real browser — the two most
expensive bugs in this project were invisible to every test that did not open one.
-->

- [ ] `./scripts/make.ps1 test`
- [ ] `./scripts/make.ps1 lint` — vet, buf lint, and the five structural guards
- [ ] `./scripts/make.ps1 e2e` — if this touches the client
- [ ] Opened it in a browser — if this touches anything visual
- [ ] Numbers included above, if this claims a performance change

## Contract and data

- [ ] No `.proto` change — **or** the change is additive-only and `buf breaking` is green
- [ ] No migration — **or** the migration is additive and the rollback is described below
- [ ] Does not touch tenant isolation (§6.1) or the model egress boundary (§18.8) — **or** the test
      for it is extended in this change
- [ ] `wasm-baseline.txt` unchanged — **or** deliberately bumped, with the reason in the commit
