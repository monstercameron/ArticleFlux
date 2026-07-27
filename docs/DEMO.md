# The demo

A build of the reader with the server inside it, published as a static page.

ArticleFlux is self-hosted, which means "have a look at it" otherwise costs a Go toolchain, two
clones, a WebAssembly build and a running server. That is a lot to ask of somebody who has not
decided whether they care yet. The demo removes every step: one URL, no account, no install.

- **Where it lives:** GitHub Pages, published by [`.github/workflows/pages.yml`](../.github/workflows/pages.yml).
- **What it runs:** `client/demo` — the same reader as `client/app`, with the tunnel replaced by an
  in-memory instance (`client/demodata`).
- **Locally:** `./scripts/make.ps1 demo` (or `make demo`) → `bin/demo`, byte-for-byte what gets
  published.

---

## What it is not

It is not a second product, and the moment it becomes one it should be deleted. Everything below the
root component is the shipping application: the same `Reader`, the same panes, the same
`client/data.Client`, the same generated gRPC stubs, the same styles. Two things are different and
both are one level below the UI:

1. **The connection.** `data.DialDemo` builds a `Client` over a `grpc.ClientConnInterface` that
   answers out of memory instead of over a WebSocket tunnel.
2. **The root.** `view.DemoRoot` is `view.Root` with the login screen and the `WhoAmI` check removed,
   because there are no accounts to check against.

That is the whole of it. There is no branch anywhere else in the client that asks "am I the demo?",
and there should never be one — the first `if demo {}` inside a pane is the point at which the demo
stops being evidence about the application.

## Why it speaks gRPC to itself

The obvious shortcut is a fake `data.Client` with the same thirty methods. It is the wrong seam
twice over: the fake drifts from the real client the first time a method changes, and — worse — the
demo would no longer exercise the client at all. The auth interceptor, the error taxonomy in
`Classify`, the read cache, the outbox: all of it would be skipped, so a demo that worked would be
evidence about a code path nobody ships.

So `client/demodata` implements the **generated server interfaces** (`pb.ReaderServiceServer` and
friends) and routes `Invoke` through the generated `ServiceDesc` handlers. Consequences worth having:

- A new RPC in the proto is a **compile error** here, not a demo that silently returns nothing.
- Requests and responses are real protobuf messages, marshalled through the same code, so the read
  cache's `proto.Marshal` round trip is real.
- Errors are real gRPC status codes, so `Classify` sees what it would see from a server.

`client/demodata` carries **no build tag** — nothing in it touches the DOM — so it is tested by an
ordinary `go test ./client/demodata/...`, through the generated client stubs, and that test runs in
CI on every pull request that touches the client.

## What the sample instance contains

Everything is invented: the publications do not exist, the bylines are not anybody's, and every
address is under a reserved `.example` domain. That is deliberate. A demo seeded with real
publishers puts words in their mouths on a page carrying somebody else's project name, and the one
thing a reader cannot check on a static demo is whether the article they are reading was really
written.

It is chosen to be representative rather than pretty:

| | |
|---|---|
| 7 sources | the size of the hand-picked hue palette the rail is built on |
| 3 categories, 3 tags | so "where a feed lives" versus "what you say about it" is visible, not described |
| 26 articles, 20 minutes to 11 days old | so relative time renders at every scale it has a format for |
| read / unread / starred / rated / annotated | so no screen is empty on arrival |
| 2 articles over 900 words | so the long-read clamp and a real reading-time estimate have something to work on |
| 1 feed failing for two days | so the dormant-feed nudge has something to nudge about |
| 4 articles held back | so **Refresh** finds something, twice, and then honestly finds nothing |

Everything a reader does works and none of it is stored: marking, starring, rating, notes, tags,
categories, feed settings, search, subscribe, unsubscribe, mark-all-read and its undo. Closing the
tab is the undo button for all of it.

## What it cannot do, and how it says so

Three things need a server, and each refuses through the same API the real server would use — so the
UI explains it with the copy it already has, rather than looking broken:

- **Smart+** (`GetSmartConfig` → `configured: false`, `can_store_secrets: false`). There is nowhere
  to put an OpenAI key that is not "published on a static page".
- **UI translation** (`FailedPrecondition`). A language switch that appeared to work and changed
  nothing would be the worst possible failure for that feature.
- **The page proxy and text-to-speech** (`proxy_url` empty; `/speech` is not there). Both fetch on
  the server's behalf, over the public internet.

Cosmetic and known: feed favicons come from the server's `/favicon` endpoint, which a static host
does not have, so those requests 404 and the rail shows its hue dots alone. The dots are the
primary identity by design — the icon was always the faster-recognition layer on top — so the rail
reads correctly; it is only the network tab that looks untidy.

## The 30 MB problem

The module is ~28 MB raw and ~6 MB gzipped, and GitHub Pages does not compress `application/wasm`.
Publishing the raw module would hand a stranger a 30 MB download to look at a demo.

So the demo publishes **only** `app.wasm.gz`, and the boot shim in `web/index.html` decompresses it
with `DecompressionStream`. The ordering there is deliberate: it tries `app.wasm` first, which a real
server always has (and serves with a proper `Content-Encoding` when the browser asks), and only
falls back to the compressed module when that 404s. The server's boot path is therefore unchanged —
the demo pays one 404 per load for it.

`make demo` deletes the raw module after compressing for the same reason: if `app.wasm` sat next to
the `.gz` locally, the compressed path would never be taken until it ran on the public URL.

## Publishing

Once, by hand, in the repository settings: **Settings → Pages → Build and deployment → Source →
GitHub Actions**. Nothing in a workflow can do this, and without it the deploy step fails with a 404
that reads like a permissions problem.

Then a tag publishes:

```bash
git tag -a v1.0.0 -m 'the first public demo'
git push origin v1.0.0
```

The tag name becomes the version the build reports on its settings screen (`-X main.version`).
Running the workflow by hand from the Actions tab does the same thing with a version somebody types.
A pull request that touches `client/`, `web/index.html` or the workflow **builds** the demo and does
not deploy it.

### The D0 wrinkle

`go.mod` replaces `github.com/monstercameron/GoWebComponents/v5` with a **sibling directory**,
because v5.0.0 was never tagged. `actions/checkout` refuses a path outside the workspace, so the
workflow checks GoWebComponents out inside and then moves it next door. That is what lets the
repository's own replace directive, `make deps`, and CI all agree without editing `go.mod` in the
pipeline — an edited `go.mod` is a build that is not the build anybody runs.

## Files

| | |
|---|---|
| `client/demo/main.go` | the binary: build the instance, dial it, render |
| `client/demodata/` | the instance — dispatcher, model, RPCs, fixtures, tests |
| `client/data/demo_wasm.go` | `DialDemo` — the `Client` over a connection that is not one |
| `client/view/demo.go` | `DemoRoot`, and the dismissible note in the corner |
| `client/i18n/en_demo.go` | that note's four strings, in the catalog like everything else |
| `.github/workflows/pages.yml` | build and publish |
