# Hosting ArticleFlux on an Ubuntu droplet

Bare DigitalOcean droplet to a reader on TLS. Around twenty minutes, most of it
waiting on `go build`.

This is the **A9 remote-deployment** path. Everything here assumes Ubuntu 24.04
LTS, nginx, and certbot; nothing here assumes Docker, because there is none in
this project's loop.

---

## What you need first

| | |
|---|---|
| Droplet | 1 GB RAM minimum. The wasm build peaks near 2 GB — see [Building on a 1 GB droplet](#building-on-a-1-gb-droplet). |
| A domain | An `A` record pointing at the droplet's IP, resolving **before** you run certbot. |
| The GWC checkout | **D0**: `go.mod` replaces `GoWebComponents/v5` with `../GoWebComponents`, because v5.0.0 was never tagged. That sibling directory is not optional. |

Everything runs as an unprivileged `articleflux` user. Nothing in this guide
needs the app to touch anything outside `/opt/articleflux`,
`/var/lib/articleflux`, and `/var/backups/articleflux`.

---

## 1. The box

```bash
sudo apt update && sudo apt upgrade -y
sudo apt install -y git nginx certbot python3-certbot-nginx build-essential

# Go from the archive, NOT from apt. Ubuntu's golang-go is several releases
# behind what go.mod requires, and the failure is a confusing one: the toolchain
# reports an unsupported go directive rather than "your Go is old".
GO_VERSION=1.26.3
curl -fsSLO "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
export PATH=$PATH:/usr/local/go/bin
go version   # expect go1.26.3
```

Firewall — do this **before** exposing anything. Note that `9000` is absent on
purpose: the app binds loopback only, and nginx is the sole way in.

```bash
sudo ufw allow OpenSSH
sudo ufw allow 'Nginx Full'
sudo ufw --force enable
```

## 2. The user and the directories

```bash
sudo useradd --system --home /opt/articleflux --shell /usr/sbin/nologin articleflux

sudo mkdir -p /opt/articleflux /var/lib/articleflux /var/backups/articleflux /etc/articleflux
sudo chown -R articleflux:articleflux /opt/articleflux /var/lib/articleflux /var/backups/articleflux

# 0700, not 0755. The database is a complete record of what someone reads, and
# every other account on the box has no business listing it.
sudo chmod 700 /var/lib/articleflux /var/backups/articleflux
```

## 3. The source

Both checkouts, side by side. The sibling layout is what the `replace` directive
in `go.mod` points at (D0).

```bash
sudo mkdir -p /opt/src && sudo chown "$USER" /opt/src
cd /opt/src
git clone <your-articleflux-remote> ArticleFlux
git clone <your-gwc-remote>         GoWebComponents

cd /opt/src/ArticleFlux
make deps     # verifies Go and the sibling checkout, and says so if either is missing
```

## 4. Build

```bash
make linux    # server + client; ~3-5 min on a 2 vCPU droplet, most of it wasm

sudo mkdir -p /opt/articleflux/bin
sudo cp -r bin/articleflux bin/web /opt/articleflux/bin/
sudo chown -R articleflux:articleflux /opt/articleflux
```

## 5. Create the first account

**Do not skip this.** Without an account there is nothing to log in as, and the
server refuses to start rather than presenting a login screen nobody can get
past. `init` runs once and refuses to run again.

```bash
# The password is read from stdin, so it never lands in shell history or `ps`.
sudo -u articleflux /opt/articleflux/bin/articleflux init \
  -db /var/lib/articleflux/articleflux.db -user cam
# Password: ...
# Confirm:  ...
```

Minimum twelve characters, no composition rules. There is no account lockout yet
(TODO 6.1) — the login rate limiter and this minimum are what stand in for it,
which makes the length load-bearing.

## 6. The service

```bash
sudo make install-service
sudo nano /etc/systemd/system/articleflux.service   # set -origin to your domain
sudo systemctl enable --now articleflux
systemctl status articleflux
curl -s localhost:9000/healthz    # ok
curl -s localhost:9000/readyz     # ready
```

If it will not start, the boot checks name the reason in one line —
`journalctl -u articleflux -n 30`. They cover the three failures that otherwise
surface hours later: no account, a missing web root, an unwritable data
directory.

## 7. TLS

```bash
sudo cp deploy/nginx.conf /etc/nginx/sites-available/articleflux
sudo sed -i 's/reader\.example\.com/YOUR-DOMAIN/g' /etc/nginx/sites-available/articleflux
sudo ln -sf /etc/nginx/sites-available/articleflux /etc/nginx/sites-enabled/
sudo rm -f /etc/nginx/sites-enabled/default

sudo certbot --nginx -d YOUR-DOMAIN    # writes the ssl_certificate lines
sudo nginx -t && sudo systemctl reload nginx
```

Open `https://YOUR-DOMAIN`. You should get the login screen.

Certbot installs its own renewal timer. Confirm it: `systemctl list-timers | grep certbot`.

## 8. Backups

Not optional, and not `cp`. See the comment at the top of
`articleflux-backup.service` for why a file copy of a WAL-mode database produces
a backup that restores cleanly and is silently missing a transaction.

```bash
sudo install -m 0644 deploy/articleflux-backup.service /etc/systemd/system/
sudo install -m 0644 deploy/articleflux-backup.timer   /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now articleflux-backup.timer

sudo systemctl start articleflux-backup    # run one now
ls -lh /var/backups/articleflux/
```

**What a backup consists of.** `articleflux backup` writes the database *and*
copies the instance's key files — `secrets.key`, `proxy.key`, `speech.key` —
into the same directory, because the database is not the whole instance.
`secrets.key` seals the Smart+ API key and every mailbox password, it lives
beside the database rather than in it, and it is never regenerated in place.
**A `.db` restored without it produces a server that refuses to start**, with a
preflight error telling you to go and find a file the backup had never been
asked to keep. Keep the whole directory, not the `.db` files out of it.

If this instance sets `ARTICLEFLUX_SECRET_KEY` instead of using the file, the
backup says so and copies nothing — that value is yours to keep somewhere else,
and it is as irreplaceable as the file would have been.

**A backup nobody has restored is a belief, not a backup.** Restore once, now,
while nothing is wrong — and restore it the way a real recovery would, into its
own directory with the keys beside it:

```bash
sudo -u articleflux mkdir -p /tmp/restore-test
sudo -u articleflux cp /var/backups/articleflux/articleflux-*.db /tmp/restore-test/articleflux.db
sudo -u articleflux cp /var/backups/articleflux/*.key           /tmp/restore-test/
sudo -u articleflux /opt/articleflux/bin/articleflux migrate -db /tmp/restore-test/articleflux.db

# The step that matters. `migrate` never opens a sealed setting, so it passes on
# a backup that cannot boot; `serve` runs the preflight that does. It should
# report the port it is listening on — Ctrl-C once it does.
sudo -u articleflux /opt/articleflux/bin/articleflux serve \
    -db /tmp/restore-test/articleflux.db -addr 127.0.0.1:9999 -poll 0
```

The old version of this drill copied the `.db` alone and ran `migrate` against
it, which is exactly the rehearsal that passes while the real thing fails.

These files leave the droplet only if you copy them off it. A droplet snapshot
is not a backup of the database — it is a backup of a *running* WAL, with the
same hazard.

---

## Do not copy a development `.env` to the droplet

`articleflux serve` reads `.env` from its working directory, and `ARTICLEFLUX_DEV=1` in that file
is the same switch as `-dev`: **no login at all**. The systemd unit sets `WorkingDirectory=`, so a
`.env` dropped in `/opt/articleflux` would be read.

Two things stop that becoming the vulnerability this whole change existed to remove, and it is
worth knowing both rather than relying on either:

- The unit passes `-behind-proxy`, and `-dev` together with `-behind-proxy` is **refused at boot**.
  A proxy in front of a loopback bind is a published instance, which is precisely the case `-dev`
  must never apply to.
- Anything already in the environment wins over the file, so `EnvironmentFile=/etc/articleflux/articleflux.env`
  cannot be overridden by a stray `.env`.

The right place for server configuration is the unit and `EnvironmentFile=`. `.env` is a
development convenience; leave it on the development machine.

## Updating

```bash
cd /opt/src/ArticleFlux && git pull
cd /opt/src/GoWebComponents && git pull    # D0: keep the sibling in step
cd /opt/src/ArticleFlux

make linux
sudo systemctl stop articleflux
sudo cp -r bin/articleflux bin/web /opt/articleflux/bin/
sudo chown -R articleflux:articleflux /opt/articleflux
sudo systemctl start articleflux
```

`ExecStartPre` runs migrations before the server takes traffic. Take a backup
first anyway if the release touches the schema — migrations here are forward-only.

Browsers cache the wasm bundle aggressively. After an update, hard-refresh
(Ctrl-Shift-R) before concluding a change did not land.

---

## Operating it

```bash
journalctl -u articleflux -f              # logs
systemctl restart articleflux             # restart
sudo -u articleflux /opt/articleflux/bin/articleflux \
  passwd -db /var/lib/articleflux/articleflux.db -user cam    # reset a password
sudo -u articleflux /opt/articleflux/bin/articleflux \
  adduser -db /var/lib/articleflux/articleflux.db -user sam -role member
```

`passwd` revokes every live session for that account, which is the point of
resetting one.

The settings screen inside the app shows recent logs and per-RPC latency, so most
questions are answerable without SSH.

### The Smart+ voice (optional)

Off unless configured, and the server cannot egress at all without a key.

```bash
sudo tee /etc/articleflux/articleflux.env >/dev/null <<'EOF'
OPENAI_API_KEY=sk-...
EOF
sudo chown articleflux:articleflux /etc/articleflux/articleflux.env
sudo chmod 600 /etc/articleflux/articleflux.env
sudo systemctl restart articleflux
```

---

## Troubleshooting

**The connection dot never goes green; the reader shows nothing.**
The WebSocket upgrade is failing. Almost always one of the four settings in the
`/grpc` block — check `proxy_http_version 1.1` and the `Upgrade`/`Connection`
headers are present, and that `-origin` in the unit matches your real scheme and
host exactly (`https://reader.example.com`, no trailing slash).

**It works, then drops every 60 seconds.**
`proxy_read_timeout` is at its default. The tunnel is idle whenever nobody is
clicking, so nginx severs it on a timer; the client reconnects, which is why it
presents as "the reader refreshes randomly" rather than as a proxy error.

**`no such module: fts5` in the log.**
Search is not wired on a connection. This is G1 and there is a permanent test for
it (`TestFTS5OnEveryPooledConnection`); if you are seeing it in production,
something is opening the database with `sql.Open` instead of
`driver.Open(dsn, fts5.Register)`.

**The login screen rejects a password you are sure is right.**
Ten failures a minute triggers the limiter, and it refuses the correct password
too while the window is open — a limiter that lets the right password through is
one an attacker walks straight past. Wait a minute. `journalctl` logs each
failure with the username.

**`database is locked`.**
Two processes have the file open. The most common cause is running `init`,
`adduser`, or `backup` against the live database as `root` rather than as
`articleflux`, leaving root-owned `-wal`/`-shm` siblings the service then cannot
write. Fix: `sudo chown articleflux:articleflux /var/lib/articleflux/*`.

### Building on a 1 GB droplet

The wasm compile peaks near 2 GB and gets OOM-killed on a 1 GB box — the symptom
is `make wasm` dying with `signal: killed` and no other explanation. Add swap:

```bash
sudo fallocate -l 2G /swapfile && sudo chmod 600 /swapfile
sudo mkswap /swapfile && sudo swapon /swapfile
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
```

---

## What is not here yet

Honest list, so nothing above reads as a stronger promise than it is:

- **No account lockout, recovery codes, or reset tokens** (TODO 6.1). There is a
  per-username and per-source rate limiter and revocable sessions. That is the
  floor for the public internet, not the ceiling.
- **Per-IP rate limiting collapses behind the proxy.** RPCs arrive multiplexed
  over one WebSocket, so every user shares nginx's address as far as the limiter
  can see. The per-username limiter is the one doing the work (TODO 7.3d).
- **No capability map** (TODO 6.2). Roles are stored and are not yet enforced
  per-method, so today every authenticated account can do everything its tenant
  can. Do not hand out `-role viewer` believing it restricts anything.
- **No version-skew handshake** (TODO 7.8). A browser holding a cached bundle
  from before an update can talk to the new server without being told.
- **Single tenant.** Usernames are unique per tenant, so login refuses outright
  if a username ever matches in two — see D12.
