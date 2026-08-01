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

- Requests and responses are real protobuf messages, marshalled through the same code, so the read
  cache's `proto.Marshal` round trip is real.
- Errors are real gRPC status codes, so `Classify` sees what it would see from a server.

`client/demodata` carries **no build tag** — nothing in it touches the DOM — so it is tested by an
ordinary `go test ./client/demodata/...`, through the generated client stubs, and that test runs in
CI on every pull request that touches the client.

### A new RPC is not a compile error, and this file said it was

Three comments in the package and a bullet here claimed that adding a method to the proto broke this
package's build. It never did. Each service embeds its generated `pb.UnimplementedXServiceServer` —
the generated interfaces require it — and that embed answers every method nobody wrote, forever,
with `Unimplemented`. Drift was therefore **silent**, and by 2026-08-01 it had already happened three
times: Discover (§18.7), OPML import/export and the edit-history disclosure had all shipped to the
public URL answering `Unimplemented`, with Discover's screen sitting on *"Couldn't load
recommendations"* for anybody who opened it.

`client/demodata/served_test.go` is what actually catches it now. It calls **every method on every
registered `ServiceDesc`**, and a method that answers `Unimplemented` must appear in that file's
`notServed` map **with a reason**. So a new RPC is neither served nor declared, and the test fails on
the pull request that adds it, naming the method. The author's two options are to serve it or to
write down why the demo cannot — and the second one is cheap, which is the point.

Deleting a line from `notServed` is checked too: a method that has since been implemented, still
listed as unserved, is a failure rather than a stale comment.

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
| 1 article with an edit history | so §10.1's "this changed since you read it" has a headline that really did lose a number |
| 9 harvested candidates | so Discover has a list — and three of them are refused, which is the half worth showing |

Everything a reader does works and none of it is stored: marking, starring, rating, notes, tags,
categories, feed settings, search, subscribe, unsubscribe, mark-all-read and its undo, OPML import
and export, and Discover's accept and dismiss. Closing the tab is the undo button for all of it.

### Discover runs the real scorer

The fixtures for §18.7 are **candidates** — what a harvest observed — and `internal/recommend.Score`
turns them into cards in `client/demodata/discover.go`, with the same defaulted `Thresholds{}`
`internal/recommendjob` passes on a server. So the evidence sentence under each card is written by
`describe()`, the ordering is the scorer's, and the three candidates that never appear are refused by
`gate()` — a site silent for fourteen months, a firehose at 900 posts a week, and one linked nine
times with no feed behind it. A fixture of finished cards would have rendered identically and
demonstrated nothing, because the argument of the feature is that the evidence is derived and the
gate refuses more than it accepts.

The observations are seeded to be consistent with the reading fixture next door, which is what makes
them checkable: a card that says *three writers you read linked here* names writers who are in the
rail. What the demo genuinely cannot do is **harvest** — rungs 1–3 read outlinks, aggregator
engagement and blogrolls out of a store this instance does not have, and rungs 4–5 are Smart+.

## What it cannot do, and how it says so

Three things need a server, and each refuses through the same API the real server would use — so the
UI explains it with the copy it already has, rather than looking broken:

- **Smart+** (`GetSmartConfig` → `configured: false`, `can_store_secrets: false`). There is nowhere
  to put an OpenAI key that is not "published on a static page".
- **UI translation** (`FailedPrecondition`). A language switch that appeared to work and changed
  nothing would be the worst possible failure for that feature.
- **The page proxy and text-to-speech** (`proxy_url` empty; `/speech` is not there). Both fetch on
  the server's behalf, over the public internet.

Two more that answer rather than refuse, because the honest answer is "none":

- **FluxCast's music beds** (`ListAudioTracks` → an empty list). The beds are files beside the binary
  and are fetched a track at a time precisely so they are *not* in the module; a 6 MB demo shipping
  megabytes of MP3 to play under narration it cannot generate would be paying for the wrong half of
  the feature. A deployment without the audio directory answers exactly this way, and the picker
  already reads an empty list as "offers silence" rather than as a failure.
- **Everything else that needs a key** (`ListModels`, `SuggestTheme`, `ComposeTheme` →
  `FailedPrecondition` with a sentence). Left unimplemented they would answer `Unimplemented`, which
  on this API means *"this server is older than your client"* — a version skew that is not happening.

The five auth RPCs and `ScrollLiveView` are the only methods left genuinely unserved, and
`served_test.go` holds the list and the reason for each.

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

## The one file the demo does not ship verbatim

`sw.js` — the Service Worker — keys its cache on a `VERSION` constant and serves the wasm module
**cache-first**, because within a build that URL's contents never change. On the server that is
exactly right: `VERSION` is `buildver.Version`, a release changes the constant, and the worker's
`activate` drops every older cache.

The demo is published from a **tag**, and a tag does not change `buildver`. A second demo release
under an unchanged constant would leave every returning visitor on the module their browser cached
the first time — publishing an update that nobody who has already looked can see, which is the worst
way for a demo to be wrong, because it is invisible from the outside and permanent.

So `make demo` stamps the copy with the build's own version and fails if it finds no line to stamp,
and the workflow re-checks the stamp before publishing. `web/sw.js` itself is untouched, so
`internal/buildver`'s test still pins the source file to the constant.

## Publishing

A tag publishes:

```bash
git tag -a v1.0.0 -m 'the first public demo'
git push origin v1.0.0
```

The tag name becomes the version the build reports on its settings screen (`-X main.version`).
Running the workflow by hand from the Actions tab does the same thing with a version somebody types,
and has a **deploy** switch for building without publishing. A pull request that touches `client/`,
`web/index.html` or the workflow **builds and verifies** the demo and does not deploy it.

Nothing has to be set up first: the deploy job runs `actions/configure-pages` with `enablement: true`,
which turns Pages on and points it at Actions if nobody has. (If that ever fails, the manual
equivalent is **Settings → Pages → Build and deployment → Source → GitHub Actions**.)

### Nothing is published that has not been proved

Between `make demo` and the upload, the workflow does two things that are the whole reason to trust
it. The demo is the only build of this application that strangers see, and it is the one nobody is
watching when it breaks.

**It verifies the artifact.** Three files present and non-empty; no raw `app.wasm` (publishing 28 MB
is the thing the compressed module exists to prevent); `gzip -t` for CRC integrity, which is what
catches a compressor that died halfway; the decompressed bytes actually start `\0asm`; the module is
not a stub; and `index.html` fetches its assets *relatively* and carries the `DecompressionStream`
path. Every one of those is invisible in a green `make demo` and fatal on the public URL.

**It boots it.** `e2e/demo-smoke.mjs` serves `bin/demo` from a static server as unhelpful as Pages is
— no compression negotiation, a 404 for anything not on disk — and drives Chromium until the rail
lists all seven seeded subscriptions, clicking a list row changes the article being read, and
**Discover renders suggestions with evidence on them**. It watches the boot shim's own failure state,
so a broken module fails in about a second with the browser's message rather than in three minutes
with a timeout.

Discover is checked here rather than left to the Go tests for a reason worth stating: its RPCs
shipped to the public URL unserved, and every other check passed while they were. The bundle was
well-formed, it booted, the rail was right and an article opened — and the newest screen in the
application said *"Couldn't load recommendations"* to every stranger who opened it. A demo failure
invisible from the outside is the expensive kind, and the cheap way to make this one visible is to
open the screen.

It runs locally too, against the directory or against a deployed URL:

```bash
node e2e/demo-smoke.mjs bin/demo
node e2e/demo-smoke.mjs https://monstercameron.github.io/ArticleFlux/
```

### The D0 wrinkle

`go.mod` replaces `github.com/monstercameron/GoWebComponents/v5` with a **sibling directory**,
because v5.0.0 was never tagged — and `actions/checkout` refuses any path outside the workspace, so
the obvious `path: ../GoWebComponents` fails at the second step of every job. `.github/actions/gwc`
is the four lines that make it work: check out inside, move next door, and assert the checkout really
is the v5 module rather than letting a wrong major version surface later as an unresolvable import.
Both workflows use it, so there is one place to fix rather than four to keep in step.

That is also what lets the repository's own replace directive, `make deps` and CI agree without
editing `go.mod` in the pipeline — an edited `go.mod` is a build that is not the build anybody runs.

## Files

| | |
|---|---|
| `client/demo/main.go` | the binary: build the instance, dial it, render |
| `client/demodata/` | the instance — dispatcher, model, RPCs, fixtures, tests |
| `client/demodata/served_test.go` | every RPC is served or declared unserved with a reason — the drift check |
| `client/demodata/discover.go` | §18.7's candidates, scored by `internal/recommend` |
| `client/demodata/opml.go` | import and export, through `internal/opml` — the one pair a demo can honour completely |
| `client/data/demo_wasm.go` | `DialDemo` — the `Client` over a connection that is not one |
| `client/view/demo.go` | `DemoRoot`, and the dismissible note in the corner |
| `client/i18n/en_demo.go` | that note's four strings, in the catalog like everything else |
| `e2e/demo-smoke.mjs` | serves the bundle and proves it boots — the last gate before publishing |
| `.github/workflows/pages.yml` | build · verify · boot · publish |
| `.github/actions/gwc` | the D0 sibling checkout, in one place for both workflows |
