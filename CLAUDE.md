# CLAUDE.md

**`AGENTS.md` is canonical — read it first**, and follow where it points. This file exists only
because Claude Code loads `CLAUDE.md` by name; it deliberately duplicates nothing, per the
no-duplicated-facts rule in `CONTRIBUTING.md`.

Two reminders that are cheap to state and expensive to forget:

- **Branch: `dev`.** Never commit to `main`, and never promote `dev` → `main` on your own
  initiative — promoting deploys the live site, and it is Cam's call.
- **Verify before claiming done.** A native `go build`/`go test` proves nothing about the wasm
  client; see `AGENTS.md` → "The things agents get wrong here".
