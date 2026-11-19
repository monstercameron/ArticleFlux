# ArticleFlux for Windows

The desktop edition is the same reader — the same server, the same wasm client,
the same SQLite file — running on one person's laptop as a Windows app, with no
login. The decision and its alternatives are `plan.md` **A45** and **§4a**; this
document is how it works and how to build it.

## Build and run

```powershell
./scripts/make.ps1 desktop              # bin/desktop/ArticleFlux.exe + bin/desktop/web/
./scripts/make.ps1 desktop -Arch arm64  # a native ARM build (see "Architectures")
.\bin\desktop\ArticleFlux.exe
```

`bin/desktop` is the whole install: the executable and, beside it, `web/` — the
client assembled exactly as `./scripts/make.ps1 wasm` assembles `bin/web`, but into
its own folder so building the app never replaces what a running dev server is
serving. Zip the folder as it stands to move it to another machine.

| Flag | Default | |
|---|---|---|
| `-data <dir>` | `%LOCALAPPDATA%\ArticleFlux` | where the database and its caches live |
| `-web <dir>` | `web\` beside the executable | the assembled client |
| `-poll <duration>` | `15m` | feed polling while the window is open; negative turns it off |
| `-devtools` | off | allows F12 in the window |

**Prerequisites.** To build: the Go toolchain `go.mod` names. Nothing else — no C
compiler (the Windows backend is CGO-free), no Node, no global Wails CLI; the
first build downloads the Go modules. To run: Windows 10 or 11 with the
**WebView2 runtime**, which ships with Windows 11 and with current Edge on
Windows 10 (otherwise Microsoft's Evergreen installer).

## How it fits together

```
ArticleFlux.exe
├─ Wails v3 (GWC's pinned fork) ── native window, menus, tray, dialogs
│    └─ WebView2 ── loads http://127.0.0.1:<port>/__desktop/launch?t=<token>
│                        │  (303 → /, cookie set)
└─ host.Server ──────────┘
     ├─ host.Gate         Host pin, launch-token cookie, same-origin, no /debug/
     └─ internal/app      app.Open → migrations → the real handler, poller, workers
          └─ %LOCALAPPDATA%\ArticleFlux\articleflux.db (+ -wal, keys, *-cache\)
```

`desktop/` is a Go module of its own (`desktop/go.mod`) so the window toolkit's
dependency graph never enters the root module: the root `go build ./...`, the
tests, govulncheck and the wasm size ratchet are untouched by it. It reaches the
server through `internal/app`'s public surface — `app.Open`, `Handler`,
`EnsureDevUser`, `Preflight`, `StartWorkers`/`StartPoller`, `Repo`, `Service`,
`Close` — which a nested module under this repository's path may import.

- **`desktop/host`** has no window toolkit and holds everything with logic in it:
  data-folder resolution, the instance lock, port selection, the gate, the page
  message policy, window-bounds validation, start and clean shutdown. It is
  tested headless.
- **`desktop/cmd/articleflux-desktop`** is the wiring between that and Wails.

### Storage

One SQLite database, `articleflux.db`, in `%LOCALAPPDATA%\ArticleFlux` (local,
not roaming: a live WAL and hundreds of megabytes of caches should not be synced
file-by-file by a profile service). It is created on first run and migrated on
every start by the server's own runner. Beside it, written by `internal/app`
itself: `secrets.key`, `proxy.key`, `speech.key`, the five caches
(`speech-cache`, `asset-cache`, `podcast-cache`, `page-cache`, `digest-cache`)
and the log spill `articleflux.db.log.jsonl`. Back up the folder, not just the
`.db` — `articleflux backup` explains why the keys matter.

The shell's own two settings — the window's position and the port — are rows in
the same `user_prefs` table the client's settings use (`desktop.window`,
`desktop.port`), written through `ReaderRepo.SetPrefs`. There is no JSON file.

Two more things live in the folder and are not ours: `webview2\` is WebView2's
browser profile (cookies, the Service Worker cache, the client's localStorage),
and `desktop.lock` is the instance lock. Deleting the folder is a complete reset.

**A database from a newer build** is refused by the migration runner, as it is on
a server; the app shows that refusal verbatim in a dialog and exits, rather than
crashing. **A clean quit** closes both connection pools, which checkpoints the
WAL into the database and deletes `-wal`; the smoke test asserts it.

### One copy at a time

Two layers. Wails' single-instance check runs first and brings the existing
window to the front when the icon is clicked again. Under it, `desktop.lock` is
opened with no sharing, so a second process — however it was started — cannot
claim the same data folder; the kernel releases it when the process dies, so a
crash never leaves a stale lock. The ID both use includes the data folder, so a
copy pointed elsewhere with `-data` is a different app.

### The port, the origin, and the gate

The server runs in **DevMode** — one local account, no login — on **127.0.0.1**
only. The first launch takes an ephemeral port from the OS (never 9000, which is
the dev server's) and remembers it; later launches ask for it back. That keeps
the page's **origin** stable, which matters because the client's per-device
choices (the interface language among them) live in the origin's localStorage,
and a new origin per launch would also leave a new Service Worker cache of the
wasm module behind each time.

DevMode was built for a developer's port and trusts whoever reaches the socket,
so `host.Gate` stands in front of it (`desktop/host/gate.go` has the reasoning):

1. `Host` must be exactly `127.0.0.1:<port>` — the DNS-rebinding defence.
2. `/debug/` is not served: DevMode mounts the e2e suite's reset/ingest endpoints.
3. A per-launch token: the window opens `/__desktop/launch?t=…`, which sets an
   HttpOnly, SameSite=Strict cookie; every other request must carry it. A web
   page in a browser on the same machine, or another Windows account, gets 403.
4. WebSocket upgrades and state-changing requests must come from this origin.

The tunnel's own origin allowlist (`app.Config.AllowedOrigins`) is not set: it
is read inside `app.Open`, and the port is a row in the database `app.Open` is
what opens. The tunnel therefore uses its built-in same-origin rule, which with
the gate's pinned `Host` admits exactly the one origin the allowlist would name.

### The web assets: beside the executable, not embedded

`internal/app` serves the client from a directory (`Config.WebRoot`), computes
its CSP hashes from the files in it, and picks `.br`/`.gz` siblings off disk. An
embedded bundle would have to be extracted to disk on every launch to be served
by that code — a second copy with its own versioning problem — or be served by a
second static handler that would drift from the server's. Beside the executable
is the same layout the server already ships with.

### Desktop integration

| | How | Notes |
|---|---|---|
| Title follows the article | the client sets `document.title`; the shell copies it to the native title bar | |
| Links open in the browser | `window.open` and cross-origin `<a>` clicks go to the default browser | only `http(s)` to another origin; never a WebView2 popup |
| Menu: File → Add feed (Ctrl+N), Import/Export OPML; View → Refresh, Settings (Ctrl+,), Reload; Help → Open data folder, About | | Add feed and Settings use the client's address grammar (`<place>/add`, `/settings/reading`); Refresh is its `r` key |
| Tray: Show, Refresh, Quit | | |
| OPML import/export | native file dialogs, then the same `reader.Service` calls the RPCs make, with the RPC's size limit | the window reloads after an import |
| Window position | remembered in `user_prefs`; ignored if no screen shows it any more | |
| Permissions | camera, microphone and location are denied | Wails otherwise grants every permission without asking |
| Notifications for new articles | **not built** | see "Hooks" |

Title, links and the menu's commands need a little script in the page, and the
client has none (A26). The **shell** injects it into its own window after each
navigation (`desktop/host/shim.js`, through Wails' `ExecJS`); it is not served,
not in `web/` and not in the wasm bundle, and it checks the page's origin before
installing itself. Messages it posts back are validated by
`host.ParseMessage` — dropped unless they come from the reader's own origin. It
is a stopgap with a named replacement, below.

## Architectures

GoWebComponents' desktop support is verified on **windows/amd64**, which runs
under emulation on Windows on ARM (that is how GWC verified it, on this
laptop). `-Arch arm64` builds a native binary — the pinned Wails has arm64 code
paths — but GWC does not yet certify it. amd64 is the default for that reason.

## Testing

```powershell
go -C desktop test ./...     # unit tests + the headless smoke test
go -C desktop vet ./...
```

The smoke test (`desktop/host/server_test.go`) points `LOCALAPPDATA` at a
temporary folder, starts the real server component, follows the launch link,
checks `/healthz`, `/readyz` and `/`, that a request without the cookie is
refused, that `/debug/` is gone, that a second instance is locked out, that the
window settings round-trip through the database, then shuts down and asserts
the WAL is gone and that a second launch gets the same origin back. It uses a
stand-in web root; set `ARTICLEFLUX_DESKTOP_WEB=bin\desktop\web` to run it
against the real client. It never touches the real `%LOCALAPPDATA%`.

What it does not cover is the window itself: WebView2, the menus and the page
script need a person (or a desktop UI test) in front of the built executable.

## Hooks — what would make the shell smaller

Each of these would let the desktop use GoWebComponents' own desktop bridge
(`github.com/monstercameron/GoWebComponents/v7/desktop`) from the client, instead
of a script injected from outside it:

1. **`client/platform.SetTitle` / `OpenExternal`** — when `desktop.Connect()`
   reports `WindowControls` / external-URL support, call
   `ControlWindow(set-title)` and the bridge's external-URL operation instead of
   `document.title` and `window.open`. Removes shim jobs 1 and 2.
2. **A command channel into `Reader`** — the client subscribing to a desktop
   topic (`desktop.Subscribe`) for `add-feed`, `settings`, `refresh`, so the menu
   does not depend on the address grammar and the keyboard map. Removes job 3.
3. **New-article notifications** — `internal/app`'s event bus is unexported.
   An exported subscription on `*app.App` (or the client raising them through
   the bridge's notification capability, opt-in from Settings) is what a
   notification needs; polling the database from the shell would be a second
   implementation of "what is new".
4. **The GWC Wails adapter** (`desktop/wails` in the GWC repository) is not yet
   published as a module and its own README calls the current form
   contributor-linked; using it today would mean a `replace` to a sibling
   checkout, which `AGENTS.md` rules out. Once it is published, the shell should
   host `desktop.NativeHost` with it rather than calling Wails directly.
