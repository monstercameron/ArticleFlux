# Moving feed.earlcameron.com from systemd to the container

A runbook for one box in one state, not a general guide — the general container install is
[`docs/INSTALL.md`](../../docs/INSTALL.md). Written against the droplet as inspected on
2026-11-19:

- **Serving:** `articleflux.service` (systemd), `/opt/ArticleFlux/bin/articleflux serve
  -addr 127.0.0.1:9000 -db /var/lib/articleflux/articleflux.db -web /opt/ArticleFlux/bin/web
  -origin https://feed.earlcameron.com -behind-proxy -poll 15m`, user `articleflux`
  (997:987), with `ExecStartPre=… migrate`. nginx proxies `/`, `/grpc`, `/healthz`,
  `/readyz` to `127.0.0.1:9000`.
- **Data:** `/var/lib/articleflux` — a 2.76 GB `articleflux.db` with `-wal`/`-shm`,
  `articleflux.db.log.jsonl{,.1}`, `asset-cache/`, `digest-cache/`, `podcast-cache/`,
  `speech-cache/`, `pre-onboarding/`, and `proxy.key`, `secrets.key`, `speech.key` (0600).
  About 18 GB free.
- **Half-staged on 2026-08-16:** the `af-reader-*` scripts and `articleflux-ctr` are in
  `/usr/local/bin`, `af-reader-autoupdate.timer` exists and is disabled,
  `/opt/articleflux/compose.yaml` pins the placeholder `v0.0.0`, and **no image has ever
  been published**. No docker login on the box.
- **Backups have been failing since then** (last good one 2026-08-19): the backup unit was
  installed without its script and without the alert template, so it exited `203/EXEC`
  nightly and nothing said so. Step 6 replaces that installation with one that cannot be
  half-applied — see the header of [`deploy/install-ops.sh`](../install-ops.sh).

**The container changes no path.** It mounts the same `/var/lib/articleflux` at the same
path, runs as the same 997:987, listens on the same `127.0.0.1:9000`, so nginx is not
touched and every file in the data directory — keys and `pre-onboarding/` included — is
simply there. What it does change is the binary: whatever release is published in step 1
replaces the August build, and **if that release carries a migration, the August binary
will refuse the database afterwards.** That is why step 4 takes a backup with the server
stopped, and why the rollback section has two cases.

Everything below runs as root on the droplet unless it says GitHub. Set the tag once:

```bash
TAG=vX.Y.Z      # the release published in step 1
```

## 0. Look before touching anything (read-only, no downtime)

```bash
id articleflux                                   # expect uid=997 gid=987
df -h /var/lib/articleflux /var/backups/articleflux
du -sh /var/lib/articleflux /var/backups/articleflux
ls -la /var/lib/articleflux
find /var/lib/articleflux ! -user articleflux -ls   # must print nothing — see step 4
/opt/ArticleFlux/bin/articleflux version
systemctl cat articleflux | grep -E '^(User|ExecStart)'
docker version --format '{{.Server.Version}}'
docker compose version                           # needs v2.24+: the compose files use env_file "required: false"
grep -rln 'af-reader-autoupdate' /etc 2>/dev/null   # is a deploy hook wired to the updater?
grep -rn 'alias\|/internal/deploy-hook' /etc/nginx/sites-enabled/   # where /shots/ is read from; is the hook routed?
```

The last line matters for step 1. The Release workflow POSTs a deploy hook once the image
is pushed. If something on this box answers it by running `af-reader-autoupdate.sh`, the
tag push will try to start the container while systemd still holds port 9000. That fails
at the port bind — before the server starts, so before any migration — but it leaves the
compose file pinned and a failed run in the log. If the grep finds a hook, either expect
that noise or remove the repository's `AF_READER_DEPLOY_HOOK_SECRET` until step 7; the
workflow skips the POST when it is unset.

The `alias` lines matter for step 5: nginx serves `/shots/` straight off disk, and after
the cutover the web root lives inside the image. If the alias names a directory under
`/opt/ArticleFlux/bin/web/`, it keeps working (the checkout's `bin/` is untracked and is
never deleted by this runbook) but serves the August screenshots from now on. If it names
`/opt/articleflux/bin/web/shots/` — the path in the repository's `nginx.conf` — **that
directory does not exist on this box and never will**, and the homepage's pictures are a
404 after step 5 until the two `location /shots/` blocks are deleted (the server serves
the same files itself).

## 0b. The first backup — before anything else changes

Every later step moves something; this one does not, and it uses the binary that is
running right now, which is guaranteed to read this database. `backup` is `VACUUM INTO`:
one consistent point in time while the server keeps serving, integrity-checked before it
reports success. It takes a few minutes and about 2.7 GB. Step 4 takes a second one with
the server stopped; this is the one that exists if anything between here and there goes
wrong.

```bash
install -d -o articleflux -g articleflux -m 0700 /var/backups/articleflux/pre-docker
runuser -u articleflux -- /opt/ArticleFlux/bin/articleflux backup \
    -db /var/lib/articleflux/articleflux.db -out /var/backups/articleflux/pre-docker/
ls -la /var/backups/articleflux/pre-docker/      # one articleflux-<UTC timestamp>.db, plus keys if this build copies them
```

A non-zero exit here **stops the runbook**: nothing has been touched, so there is nothing
to undo — find out why before going on. (`df` in step 0 said whether there is room for this
copy and step 4's; with 18 GB free there is.)

## 1. Publish an image (GitHub, the owner)

**Dry run first.** Actions → *Release* → *Run workflow* on `dev`. It verifies the tree and
builds both platforms without pushing anything — the first time the Dockerfile has ever
been built, so let it fail here rather than on a tag. If it fails, nothing outside GitHub
has changed: fix it on `dev` and run it again. Only when it is green:

Promote `dev` to `main` (Actions → *Promote dev to main*), then tag the promoted commit:

```bash
git fetch origin && git tag "$TAG" origin/main && git push origin "$TAG"
```

The Release workflow verifies, builds `linux/amd64` and `linux/arm64`, and pushes
`ghcr.io/monstercameron/articleflux:$TAG`, `:latest` and `:sha-<commit>`. If the run goes
red, nothing was pushed and the box has not been touched; never move or delete a pushed
tag — fix `dev`, promote, and tag the next patch version, and use that as `TAG`.

**Then make the package public**, once: GitHub → the profile's *Packages* → `articleflux`
→ *Package settings* → *Change visibility* → *Public*. A first push from Actions is
private, and this box has no registry login, so until it is public every pull here is
`denied`. Check from the droplet — it needs no login when the package is public:

```bash
docker manifest inspect "ghcr.io/monstercameron/articleflux:$TAG" >/dev/null && echo published
docker pull "ghcr.io/monstercameron/articleflux:$TAG"   # pre-pull, so step 5 does not wait on it
```

**Then run the image once, beside production, on a scratch volume and another port** — this
is the first time anything built from the Dockerfile has executed, and it is better to learn
what is wrong with it while the old server is still serving. It touches nothing of
production's (its own anonymous volume, port 19000 on loopback, no `/var/lib/articleflux`):

```bash
docker run -d --name af-smoke -p 127.0.0.1:19000:9000 "ghcr.io/monstercameron/articleflux:$TAG"
for i in $(seq 1 20); do s=$(docker inspect -f '{{.State.Health.Status}}' af-smoke); echo "$i $s"; [ "$s" = healthy ] && break; sleep 5; done
docker logs af-smoke 2>&1 | grep -E 'MODE=|UNCLAIMED|listening|ERROR|WARN'   # production mode, no refusal
curl -fsS -D - -o /dev/null http://127.0.0.1:19000/readyz | grep -i 'x-articleflux-commit'   # the tag's commit, in full
curl -fsSI -H 'Accept-Encoding: br' http://127.0.0.1:19000/app.wasm | grep -iE '^(HTTP|content-encoding)'   # 200, br
curl -fsSI http://127.0.0.1:19000/wasm_exec.js | head -n1
curl -fsSI http://127.0.0.1:19000/audio/late-night-patchcord.mp3 | head -n1    # the music beds shipped
curl -fsSI http://127.0.0.1:19000/welcome | head -n1
docker exec af-smoke /opt/articleflux/bin/articleflux version
docker rm -fv af-smoke                                                         # -v: its volume goes too
```

Every request above should answer 200 (the `-f` makes `curl` say so by failing). The fresh
instance is unclaimed, which is why it is bound to loopback and removed straight away.
`healthy` appears within about 30 seconds; if it never does, `docker logs af-smoke` is the
answer, and this is where the runbook stops.

## 2. Bring the checkout and the container files up to date (no downtime)

`/opt/ArticleFlux` is the checkout `af-reader-autoupdate.sh` reads tags from and runs
`af-reader-deploy-release.sh` out of. Moving it to `main` does not touch `bin/` (untracked),
so the systemd unit keeps running the binary it is running.

```bash
git -C /opt/ArticleFlux rev-parse --short HEAD          # write this down: undoing this step is a reset --hard to it
git -C /opt/ArticleFlux fetch -q --tags origin
git -C /opt/ArticleFlux reset -q --hard origin/main
git -C /opt/ArticleFlux log -1 --format='%h %s'

install -d -m 0755 /opt/articleflux
install -m 0644 /opt/ArticleFlux/deploy/docker/compose.production.yaml /opt/articleflux/compose.yaml
install -m 0755 /opt/ArticleFlux/deploy/docker/af-reader-autoupdate.sh /usr/local/bin/af-reader-autoupdate.sh
install -m 0755 /opt/ArticleFlux/deploy/docker/articleflux-ctr        /usr/local/bin/articleflux-ctr
install -m 0644 /opt/ArticleFlux/deploy/docker/af-reader-autoupdate.service \
                /opt/ArticleFlux/deploy/docker/af-reader-autoupdate.timer /etc/systemd/system/
systemctl daemon-reload
grep -E 'image:|user:' /opt/articleflux/compose.yaml     # v0.0.0 and "997:987" for now
```

(`/usr/local/bin/af-reader-deploy-release.sh` from August is not used by anything — the
updater runs the checkout's copy — and can be deleted.)

## 3. Know which rollback you would face, then stop the server

Before the downtime, answer the question the rollback turns on: does `$TAG` carry
migrations the running binary has never seen? `version` prints the commit it was built
from, and a new file under `migrations/` between that commit and the tag is a migration
the container will apply on its first boot.

```bash
OLD=$(/opt/ArticleFlux/bin/articleflux version | sed -n 's/.*(\([0-9a-f]*\)).*/\1/p'); echo "$OLD"
git -C /opt/ArticleFlux diff --name-status "$OLD" "$TAG" -- migrations/
```

No output: rolling back is restarting the old unit. Any `A migrations/…` line: rolling
back means restoring step 4's backup and losing what was written in between — still fine,
but decide it now rather than mid-incident. (If `OLD` comes out empty, the August build
was not stamped with its commit, and if `git diff` answers `bad object`, that commit was
never on `main`; either way treat the answer as "yes".)

```bash
systemctl stop articleflux-health.timer     # the watchdog sits out the switch
systemctl stop articleflux
systemctl is-active articleflux             # "inactive"
```

## 4. The second backup, with the server stopped

Step 0b's copy is a few minutes old and misses whatever was written since. This one is
taken with the server stopped, so it holds every write up to the last request served — a
rollback from it loses nothing that happened under systemd — and it is the newest file in
`pre-docker/`, which is the one the rollback below restores. Taken with the **August**
binary, which is guaranteed to read this database. The key files are copied explicitly as
well, in case that build predates `backup` copying them itself.

```bash
install -d -o articleflux -g articleflux -m 0700 /var/backups/articleflux/pre-docker
runuser -u articleflux -- /opt/ArticleFlux/bin/articleflux backup \
    -db /var/lib/articleflux/articleflux.db -out /var/backups/articleflux/pre-docker/
install -o articleflux -g articleflux -m 0600 /var/lib/articleflux/secrets.key \
    /var/lib/articleflux/proxy.key /var/lib/articleflux/speech.key /var/backups/articleflux/pre-docker/
ls -la /var/backups/articleflux/pre-docker/
```

Expect two `articleflux-<UTC timestamp>.db` (step 0b's and this one; at most 2.76 GB each —
`VACUUM INTO` compacts) and the three keys. `backup` integrity-checks the copy before it reports success; a
non-zero exit here **stops the cutover** — start the unit again and find out why.

The subdirectory is deliberate: the nightly job's `-keep` prunes `articleflux-*.db` in
`/var/backups/articleflux` itself and never descends, so this copy stays until somebody
deletes it on purpose — after the container has run through at least one release.

If step 0 found files not owned by `articleflux`, fix them now, while nothing has them
open — the container runs as 997:987 and cannot write a root-owned `-wal`:

```bash
chown -R articleflux:articleflux /var/lib/articleflux
```

Now take the unit out of reach. Moved aside rather than disabled: a disabled unit can
still be started, and `deploy/update.sh` — still what the old deploy hook runs on a
promotion — would start it, running its `ExecStartPre` migration against the live
database and then failing to bind a port the container holds. With the file gone,
`update.sh` refuses at its first check instead.

```bash
systemctl disable articleflux
mv /etc/systemd/system/articleflux.service /etc/systemd/system/articleflux.service.pre-docker
systemctl daemon-reload
```

## 5. Start the container

```bash
sh /opt/ArticleFlux/deploy/docker/af-reader-deploy-release.sh "$TAG"
```

That pins `$TAG` into `/opt/articleflux/compose.yaml` (recording `v0.0.0` as the previous
tag, which is meaningless here — the rollback below does not use it), runs `compose up -d`,
and blocks until Docker reports the container **healthy**, for up to five minutes. The
server runs any pending migrations before it listens; the image's healthcheck allows two
minutes for that before failures count. If it reports unhealthy or times out, it prints
the last 50 log lines — go to [Rollback](#rollback).

Then check it end to end:

```bash
docker inspect -f '{{.State.Health.Status}} {{.Config.User}}' articleflux   # healthy 997:987
curl -fsS http://127.0.0.1:9000/healthz; echo
curl -fsS http://127.0.0.1:9000/readyz;  echo                             # ready — it can WRITE
docker logs articleflux 2>&1 | grep -E 'migrations applied|MODE=|listening'
docker exec articleflux /opt/articleflux/bin/articleflux version
curl -fsS -o /dev/null -w '%{http_code}\n' https://feed.earlcameron.com/healthz
curl -fsSI "https://feed.earlcameron.com/shots/$(ls /opt/ArticleFlux/bin/web/shots | head -n1)" | head -n1
```

The last line asks nginx for a homepage screenshot, naming a file from the August build's
copy (the image has no shell to list its own). A 404 is step 0's `alias` finding coming
true: delete the two `location /shots/` blocks from the nginx site, `nginx -t`, and
`systemctl reload nginx`; the server then serves the same files itself.

`MODE=production` must name `https://feed.earlcameron.com` and `trusts_forwarded_for=true`.
Then sign in at https://feed.earlcameron.com and watch the connection dot go green — the
`/grpc` WebSocket through nginx is the one thing the probes above do not exercise.
Downtime ends here.

## 6. Point backup, drill and watchdog at the container

```bash
sh /opt/ArticleFlux/deploy/install-ops.sh --runtime docker --keep 3 \
   --repo /opt/ArticleFlux --compose /opt/articleflux/compose.yaml
```

It installs all four scripts, all seven units and the alert template together; writes
the drop-ins that differ for the container (the watchdog's `ARTICLEFLUX_RUNTIME=docker`;
the backup and drill running `articleflux-ctr` as root with the host's `/tmp`, the
container itself still as 997:987); and refuses to enable anything unless every
`ExecStart` and the alert template exist, `systemd-analyze verify` passes, and
`articleflux-ctr version` actually starts the pinned image. If it refuses, the container is
serving and nothing was enabled: read what it printed, fix that, and run it again — it is
safe to re-run as often as it takes.

**`--keep 3`, not the unit's 14**, because of the arithmetic: fourteen copies of a
2.7 GB database is ~38 GB against 18 GB free, and every run needs room for one more copy
before it prunes. The first nightly run with `-keep 3` also prunes the August backups
in the top-level directory, which is correct now that `pre-docker/` exists.

Prove each piece now rather than at 03:30 and on Sunday:

```bash
systemctl start articleflux-backup.service; systemctl status articleflux-backup --no-pager
ls -lt /var/backups/articleflux | head -n 5
systemctl start articleflux-restore-drill.service; journalctl -t articleflux-restore-drill -n 5 --no-pager
systemctl start articleflux-health.service; journalctl -t articleflux-health -n 5 --no-pager
/usr/local/bin/articleflux-alert "ArticleFlux test" "the container cutover's alert check"
systemctl list-timers 'articleflux-*' --no-pager
```

The backup line in the journal says `LOCAL ONLY` until `/etc/articleflux/backup.env` names
an off-box target — `deploy/README.md` § Getting a copy off the box. The drill copies the
newest backup, ~2.7 GB, into `/tmp`; it removes it on exit. The alert reaches journald
only, until `/etc/articleflux/alert.env` names a channel (`deploy/README.md` § Alerting).

## 7. Let releases deploy themselves, and retire the old path

```bash
systemctl enable --now af-reader-autoupdate.timer
sh /usr/local/bin/af-reader-autoupdate.sh        # expect: up to date ($TAG)
```

From here a pushed `v*` tag deploys — through the hook if one is wired, or within a day
through the timer — and a promotion to `main` alone deploys nothing. The old deployhook
target for ArticleFlux (`/opt/ArticleFlux/deploy/update.sh` on a promotion) now fails at
its first check on every promotion; remove that target from deployhook's config so a
promotion is not reported as a failed deploy:

```bash
systemctl cat deployhook | grep -o -- '-config [^ ]*'    # the file to edit
# delete the "monstercameron/ArticleFlux" target from it, then:
systemctl restart deployhook
```

If step 0 found a hook and the secret was removed, restore `AF_READER_DEPLOY_HOOK_SECRET`.

One more thing that goes red after this, by design of the old flow: the **Verify the
deploy** workflow starts on every *Promote dev to main* and waits twenty minutes for
`/readyz` to report the promoted commit. A promotion no longer deploys, so from now on it
fails every time. Disable that workflow (Actions → *Verify the deploy* → ⋯ → *Disable
workflow*) or repoint it at the Release run — a release tag is now what puts a commit on
this box, and the image reports its commit in the same `X-ArticleFlux-Commit` header.

## Rollback

Which case you are in is decided by one question: **has the container applied a
migration the August binary does not have?** Step 3 answered it for `$TAG`; for a
rollback after later releases have deployed, ask it again against the tag now pinned:

```bash
git -C /opt/ArticleFlux diff --name-status "$OLD" \
    "$(grep -oE 'articleflux:v[^[:space:]]+' /opt/articleflux/compose.yaml | cut -d: -f2)" -- migrations/
```

(`$OLD` is step 3's; in a new shell, set it again the same way.) Do not settle it by starting the old binary and seeing what happens: a build from before
the newer-schema refusal existed would start on a database it cannot read correctly.

**No migration ran** — the database is still one the August binary reads:

```bash
systemctl disable --now af-reader-autoupdate.timer
docker compose -f /opt/articleflux/compose.yaml down
mv /etc/systemd/system/articleflux.service.pre-docker /etc/systemd/system/articleflux.service
systemctl daemon-reload
systemctl enable --now articleflux
curl -fsS http://127.0.0.1:9000/healthz; echo
sh /opt/ArticleFlux/deploy/install-ops.sh --runtime systemd --keep 3 \
   --bin /opt/ArticleFlux/bin/articleflux --repo /opt/ArticleFlux
```

Nothing is lost: both servers used the same files in place.

**A migration ran** — a current build refuses such a database at boot ("migrated by a
newer build"); the August one may refuse it or, if it predates that check, misread it.
Either way the database cannot go back. Rolling back is restoring the step-4 backup, and **every
write made under the container is lost**: whatever was read, starred or subscribed since
step 5. Do everything in the first case up to and including the `mv` and
`daemon-reload`, then, before starting the unit:

```bash
aside=/var/lib/articleflux/post-docker-aside; install -d -o articleflux -g articleflux -m 0700 "$aside"
mv /var/lib/articleflux/articleflux.db /var/lib/articleflux/articleflux.db-wal \
   /var/lib/articleflux/articleflux.db-shm "$aside"/ 2>/dev/null || true
install -o articleflux -g articleflux -m 0600 \
   "$(ls -1t /var/backups/articleflux/pre-docker/articleflux-*.db | head -n1)" \
   /var/lib/articleflux/articleflux.db
systemctl enable --now articleflux
```

The `-wal` and `-shm` go aside with the database because they belong to it: left in
place, SQLite would replay the container's last writes into the restored file. The key
files stay where they are — this cutover does not rotate them, so the live ones and the
backed-up ones are identical. Then re-run `install-ops.sh --runtime systemd` as above.
Keep `post-docker-aside/` until you are sure; it is the only copy of what was written
under the container.
