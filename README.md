# Tidings

A self-hosted feed reader. Go all the way down: the server is Go, the client is Go compiled to
WebAssembly, the CSS is Go, and the only JavaScript that ships is a fifteen-line bootstrap.

```powershell
./make.ps1 dev      # build the client and serve on http://127.0.0.1:9000
./make.ps1 e2e      # Playwright, desktop + phone, against a real server
./make.ps1 test     # go test ./...
./make.ps1 lint     # go vet, buf lint, and the four structural guards
```

First run:

```powershell
./make.ps1 build
./bin/tidings.exe seed          # subscribe to a starter set and fetch it
./make.ps1 dev
```

## What it does today

Three panes — feeds, items, article — with Google Reader's key map (`j` `k` `o` `s` `r` `u`), full-text
search over FTS5, starring, mark-all-read, subscribe by URL, and a background poller. It reads real
feeds; the starter set is eight of them, chosen to exercise the parser rather than to recommend
reading.

## How it is put together

```
cmd/tidings        the binary: serve · seed · poll · version
internal/store     ALL SQL lives here. Two pools: many readers, one writer (A24)
internal/feed      fetch + normalise. Every fetch goes through the SSRF guard
internal/reader    the service layer — one place that knows what "mark read" means
internal/transport gRPC, thin: message translation and the error taxonomy
client/            the wasm app. design/ (tokens + stylesheet), view/, data/, platform/
proto/             the contract all transports share
```

Four decisions shape most of the code:

- **Sources and items are global and deduplicated.** One popular feed is polled once no matter how
  many people subscribe. That is why read/star state lives in its own table, and why global rows are
  never hard-deleted — a cascade from `items` would destroy every other tenant's history.
- **Two connection pools.** SQLite allows many readers and exactly one writer; modelling that as two
  pools makes the constraint structural instead of a race.
- **All CSS is Go.** There is no `.css` file in this repository and CI fails if one appears.
- **`syscall/js` lives in exactly one package**, so every other client package compiles — and is
  testable — natively.

Each is enforced by a guard in `internal/tools/guards`, not by convention.

## The documents

`plan.md` is the spec of record, `TODO.md` the dependency-ordered build, `FLOWS.md` the diagrams, and
`design/` the visual specifications (hand-written HTML that nobody ports — the reader is rebuilt from
them in Go). They are kept in sync with the code deliberately: a finding in code gets written back
into the section it affects.

## Notes for anyone picking this up

- **GoWebComponents v5 was never tagged**, so `go.mod` carries a `replace` to a sibling checkout.
- **FTS5 is not compiled into `ncruces/go-sqlite3`.** It is a loadable extension that must be
  registered on every connection — see `internal/store.Open`.
- **The dev server runs without a login**, and only ever on a loopback bind. Binding a real interface
  turns that off, because an internet-facing instance with it on would be an open reader.
