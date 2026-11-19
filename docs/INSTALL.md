# Installing ArticleFlux

ArticleFlux is one Go binary, one SQLite file and a wasm client the binary serves. The
**container is the first-class way to run it**: one image, one volume, nothing to build.
Building from source is the second path, and is what you want for working on it.

This document owns the container install and its day-two operations. It links rather than
repeats: [`.env.example`](../.env.example) owns what every environment variable means,
[`deploy/nginx.conf`](../deploy/nginx.conf) owns what a reverse proxy in front of this
server has to do and why, and [`deploy/README.md`](../deploy/README.md) owns the
systemd install on a bare Ubuntu box and the reference production deployment.

- [Try it on your own machine](#try-it-on-your-own-machine)
- [The first login](#the-first-login)
- [Put it on a server](#put-it-on-a-server)
- [Day two](#day-two) — upgrading, rolling back, backups, accounts, logs, health, resources
- [From source](#from-source)

---

## Try it on your own machine

All you need is Docker. Either the compose file at the root of this repository — it is
self-contained, so it works downloaded on its own (it needs Compose **v2.24 or newer**, for
the optional `env_file` it uses; `docker compose version` says which you have, and the
`docker run` form below needs nothing newer than Docker itself):

```bash
curl -fsSLO https://raw.githubusercontent.com/monstercameron/ArticleFlux/main/compose.yaml
docker compose up -d
docker compose ps        # STATUS should reach "healthy" within a few seconds
```

…or the same thing as one command:

```bash
docker run -d --name articleflux --restart unless-stopped \
  -p 127.0.0.1:9000:9000 \
  -v articleflux-data:/var/lib/articleflux \
  --cap-drop ALL --security-opt no-new-privileges:true \
  ghcr.io/monstercameron/articleflux:latest
```

Then open **http://127.0.0.1:9000**.

Three choices in those lines are deliberate, and each is the safe default rather than the
convenient one:

- **`127.0.0.1:9000`, not `9000`.** Only this machine can reach the reader, which matters
  most in the minutes before it has an owner — see [the first login](#the-first-login).
  Docker's published ports also go around host firewalls such as `ufw`, so the publish
  address is the boundary; nothing behind it will stop a `0.0.0.0` publish.
- **A named volume for `/var/lib/articleflux`.** That directory is the whole instance:
  `articleflux.db` and its `-wal`/`-shm`, the key files `secrets.key`, `proxy.key` and
  `speech.key`, the server's on-disk log `articleflux.db.log.jsonl`, and the caches
  (`asset-cache/`, `page-cache/`, `speech-cache/`, `digest-cache/`, `podcast-cache/`).
  Everything the server writes is derived from the database's path, so one volume holds
  all of it. Deleting the volume deletes the instance.
- **No `-dev`.** The no-login development mode is refused on any bind but loopback, and
  inside a container the server binds `:9000` — so it cannot be switched on here, by
  flag, `ARTICLEFLUX_DEV`, or a stray `.env`. A container always asks for a password.

The image is published to `ghcr.io/monstercameron/articleflux` for every release tag
(`vX.Y.Z`, plus `latest`), for `linux/amd64` and `linux/arm64`. If `docker pull` says
*not found* or *denied*, the release you asked for has not been published — build it
yourself from a clone with `docker build -f deploy/docker/Dockerfile -t articleflux:local .`
and use `articleflux:local` as the image.

## The first login

A fresh instance has no account, and it does not refuse to start over that. It serves a
**setup screen** in place of the login screen: you choose the first account's username
and password, and it will not let you past until you have saved the account's recovery
codes. It can be claimed exactly once, and the boot log says so loudly while it is
unclaimed (`this instance has no account and is UNCLAIMED`).

**The setup screen is the way this install claims an instance, on a laptop and on a
server alike**, and it is chosen over the command line on purpose. `articleflux init`
also creates the first account, but an account made that way has no recovery codes, and
there is no control in the app yet to generate a sheet afterwards — so the shell route
leaves the owner with no way back in short of the shell. The browser route does not.

What the setup screen costs is a window: until somebody claims it, whoever reaches it
first can. On your own machine the loopback publish closes that window. On a server, the
order in [Put it on a server](#put-it-on-a-server) keeps it to the minute between the
proxy coming up and you opening the page. If you cannot be at a browser when the server
becomes reachable, claim it from the shell first and accept the missing recovery sheet:

```bash
docker compose exec articleflux articleflux init -user you@example.com
```

## Put it on a server

The server speaks plain HTTP on `127.0.0.1:9000` and expects a reverse proxy on the same
host to terminate TLS. Two things change from the local setup, and both are configuration
rather than a different command. Put them in `articleflux.env` next to `compose.yaml`
(the compose file reads it if it exists; with `docker run`, pass each as `-e`):

```sh
ARTICLEFLUX_ORIGIN=https://reader.example.com
ARTICLEFLUX_BEHIND_PROXY=1
```

- **`ARTICLEFLUX_ORIGIN`** is the exact origin a browser shows — scheme included, no
  trailing slash. The client does all its work over one WebSocket at `/grpc`, and the
  server refuses that upgrade from any page origin not on this list. Wrong, and the page
  loads while nothing ever arrives.
- **`ARTICLEFLUX_BEHIND_PROXY=1`** makes the server believe `X-Forwarded-For`, which it
  needs to log, rate-limit and lock out by the real client address instead of the
  proxy's. It is a header any client can send, which is why it is off unless you say a
  proxy is in front — and why it must not be set when one is not.

Then, in this order:

1. Point the domain's DNS at the box, and wait until it resolves.
2. `docker compose up -d` with `articleflux.env` in place. Confirm the boot line reads
   `MODE=production — login required` with your origin:
   `docker compose logs articleflux | grep MODE=`.
3. Bring up the proxy — Caddy or nginx, below.
4. **Open `https://reader.example.com` straight away and claim it** on the setup screen.

**Caddy** is the shorter path: it obtains and renews the certificate itself, and its
defaults already handle the WebSocket. [`deploy/docker/Caddyfile`](../deploy/docker/Caddyfile)
is the whole site — replace the hostname, then:

```bash
sudo cp deploy/docker/Caddyfile /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

**nginx** uses [`deploy/nginx.conf`](../deploy/nginx.conf) and certbot, as
[`deploy/README.md` § TLS](../deploy/README.md#7-tls) describes, with **one change for
the container**: delete the `location /shots/` blocks (there are two). They serve the
homepage's screenshots straight off `/opt/articleflux/bin/web/shots/`, which is a path
inside the image, not on the host — left in, every screenshot is a 404. Without them the
requests fall through to the server, which serves the same files itself. The `/grpc`
block is the part that must not change; its comments say why each of its lines is there.

Either proxy must run **on the host**, because it reaches the server at
`127.0.0.1:9000`. A proxy in another container reaches it by service name instead
(`articleflux:9000` on a shared compose network), and then needs no published port.

A Smart+ key, if you want the paid voice and translation, goes in `articleflux.env` as
`OPENAI_API_KEY` or into **Settings › Smart+** in the running app; the second is the
usual path, and `.env.example` explains how the two interact. The live page view
(`ARTICLEFLUX_PROXY_STREAM`) is not available in the image: it drives a Chromium on the
box, the image has none, and the server says so at boot and leaves the control out.

---

## Day two

Every command here is written for the compose file; with `docker run`, replace
`docker compose exec articleflux` with `docker exec -it articleflux` and
`docker compose logs articleflux` with `docker logs articleflux`. The image puts the
binary on `PATH` and makes the data directory its working directory, so the CLI's default
`-db articleflux.db` *is* the volume's database — no subcommand here needs `-db`.

### Upgrading

Read the release's **Upgrade notes** in [`CHANGELOG.md`](../CHANGELOG.md) first — some
releases need an action nothing on screen will prompt for. Then take a backup (below),
change the tag in `compose.yaml`, and:

```bash
docker compose pull
docker compose up -d       # recreates the container on the new image; the volume stays
docker compose ps          # wait for "healthy"
```

The server applies any pending migrations itself as it starts, before it listens, which
is why the image's healthcheck allows two minutes for the first answer. Browsers cache the
client hard; hard-refresh once before deciding a change did not land.

### Rolling back

**Migrations are forward-only, and a database a newer build has migrated is refused by an
older one** — at boot, with a message naming the problem, rather than a server that
starts and quietly misreads its own tables. So what a rollback means depends on whether
the release you are leaving ran a migration:

- **It did not:** put the previous tag back in `compose.yaml` and `docker compose up -d`.
  Nothing else changes.
- **It did:** the previous image will not open this database. Rolling back is restoring
  the backup you took before upgrading, and losing whatever was written since. That is
  the whole reason for the backup step above.

### Backups and restore

Not a copy of the volume while the server runs: the database is in WAL mode, and copying
its files copies them at different instants. `articleflux backup` uses SQLite's
`VACUUM INTO`, which is one consistent point in time taken while the server keeps
serving, integrity-checks the copy, and puts the key files beside it — a database
restored without its `secrets.key` cannot read any stored credential, and the server
refuses to start on it. Run it inside the container, into the volume, then copy it out:

```bash
docker compose exec articleflux articleflux backup -out /var/lib/articleflux/backups/ -keep 3
docker compose cp articleflux:/var/lib/articleflux/backups ./articleflux-backups
```

The first command writes `backups/articleflux-<UTC timestamp>.db` plus the key files and
keeps the newest three. The second is the step that makes it a backup: a copy on the same
disk as its source defends against a mistake, not against losing the disk. The directory
you copied holds `secrets.key`, which seals every stored credential, so keep it where you
would keep that key. `deploy/README.md` § Backups covers retention, off-box encryption
and the restore drill for a server that runs this nightly.

To restore, stop the server and put the backup in place of the database — removing the
old `-wal` and `-shm`, which belong to the database being replaced and would be replayed
into the restored one. The image has no shell, so a throwaway container does the copying
(run this beside the backup directory you copied out):

```bash
docker compose stop articleflux
docker run --rm --volumes-from "$(docker compose ps -aq articleflux)" \
  -v "$PWD/articleflux-backups:/restore:ro" busybox sh -c '
    set -e; cd /var/lib/articleflux
    rm -f articleflux.db-wal articleflux.db-shm
    cp "$(ls -1t /restore/articleflux-*.db | head -n1)" articleflux.db
    for k in secrets.key proxy.key speech.key; do
      if [ -f "/restore/$k" ]; then cp "/restore/$k" "$k"; chmod 600 "$k"; fi
    done
    chmod 600 articleflux.db; chown -R 65532:65532 .'
docker compose start articleflux
```

`65532` is the image's user; if you run the container with a different `user:`, use that.
The restored server re-runs any migrations the backup predates as it starts.

### Accounts

```bash
docker compose exec articleflux articleflux adduser -user sam -role member
docker compose exec articleflux articleflux passwd  -user sam
docker compose exec articleflux articleflux reset   -user sam -origin https://reader.example.com
docker compose exec articleflux articleflux audit   -alerts -since 168h
```

`adduser`, `passwd` and `init` prompt for the password on a hidden terminal prompt —
`exec` allocates one — and never take it as a flag, so it stays out of shell history and
process listings. `passwd` signs that account out everywhere. `reset` is the one to use
when the person locked out is not the person with the shell: it prints a single-use link,
valid for an hour, for them to choose their own. `articleflux help` lists every
subcommand.

### Logs and health

```bash
docker compose logs -f articleflux
```

The log is human-readable text; set `ARTICLEFLUX_LOG_FORMAT=json` in `articleflux.env`
when something indexes it rather than displays it, and `ARTICLEFLUX_LOG_LEVEL=debug` for
more. The compose file caps Docker's copy at three 10 MB files. The **Server** tab in
Settings shows recent log lines, the build, the schema version and the database size
without a shell at all.

The image's healthcheck runs `articleflux healthcheck`, which asks the server's own
`/healthz` — distroless has no curl to ask with. `docker compose ps` shows the result,
and `restart: unless-stopped` restarts a container that exits. `/readyz` is the stricter
probe (it checks the database can be read and written; a full disk fails it while
`/healthz` stays green) and is the one to point an external monitor at:
`curl -fsS http://127.0.0.1:9000/readyz`.

### Resources

- **Memory:** the plan holds the server to under 400 MB resident at rest (§22.12). A
  1 GB host runs it; building the image yourself needs about 2 GB, because of the wasm
  compile.
- **Disk:** the database, plus the on-disk caches, which share the volume and are held to
  1 GB in total by default (`ARTICLEFLUX_CACHE_BUDGET_MB`), plus whatever backups you keep
  in the volume. `/readyz` fails when the disk does.
- **CPU:** idle between polls (every 15 minutes by default). Logins are deliberately
  expensive — the password hash is tuned to take a noticeable fraction of a second on the
  box it runs on.

---

## From source

For working on ArticleFlux, or for a box without Docker:

- **Developing** — [`CONTRIBUTING.md`](../CONTRIBUTING.md#before-you-write-anything):
  the Go toolchain `go.mod` names, `./scripts/make.ps1 build` (or `make build`), and the
  no-login `dev` mode, which only exists outside a container.
- **A server without Docker** — [`deploy/README.md`](../deploy/README.md): a bare Ubuntu
  box to TLS with a hardened systemd unit, nginx, certbot and nightly verified backups.
