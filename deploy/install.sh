#!/usr/bin/env bash
# ArticleFlux — turn a bare Ubuntu box into a running reader.
#
#   curl -fsSL https://raw.githubusercontent.com/monstercameron/ArticleFlux/main/deploy/install.sh | sudo bash
#   sudo ./install.sh --origin http://203.0.113.10        # or a domain, later
#   sudo ./install.sh --no-nginx                          # bring your own proxy
#
# Written to be run twice. Every step checks for what it is about to create and
# skips it if it is already there, so a run that dies at step 7 is fixed by
# fixing the cause and running it again — not by unpicking half an install.
#
# What it leaves behind:
#   /opt/ArticleFlux            the checkout, and bin/ built from it
#   /opt/GoWebComponents        the sibling library go.mod's replace points at
#   /var/lib/articleflux        the database, the keys, the asset cache
#   /var/backups/articleflux    rollback points, written by update.sh
#   articleflux.service         the server, restarted on failure and at boot
#   articleflux-health.timer    the watchdog that restarts it when it wedges
#   nginx on :80                the only thing the internet can reach
set -euo pipefail

# Recorded verbatim in the failure report: how the script was invoked is half of
# what a reproduction needs, and it is the half nobody remembers.
SCRIPT_ARGS="$*"

REPO_URL="${ARTICLEFLUX_REPO_URL:-https://github.com/monstercameron/ArticleFlux.git}"
GWC_URL="${GWC_REPO_URL:-https://github.com/monstercameron/GoWebComponents.git}"
REPO="${ARTICLEFLUX_REPO:-/opt/ArticleFlux}"
GWC="${GWC_REPO:-/opt/GoWebComponents}"
DATA="${ARTICLEFLUX_DATA:-/var/lib/articleflux}"
BACKUPS="${ARTICLEFLUX_BACKUPS:-/var/backups/articleflux}"
OWNER="${ARTICLEFLUX_USER:-articleflux}"
PORT="${ARTICLEFLUX_PORT:-9000}"
GO_VERSION="${GO_VERSION:-1.26.5}"
BRANCH="${ARTICLEFLUX_BRANCH:-main}"

ORIGIN=""
WITH_NGINX=1
STEP_TOTAL=9
while [ $# -gt 0 ]; do
	case "$1" in
		--origin) ORIGIN="${2:?--origin needs a URL}"; shift 2 ;;
		--branch) BRANCH="${2:?--branch needs a name}"; shift 2 ;;
		--no-nginx) WITH_NGINX=0; shift ;;
		--verbose|-v) VERBOSE=1; shift ;;
		-h|--help) sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) echo "unknown option: $1 (try --help)" >&2; exit 2 ;;
	esac
done

LIB="$(cd "$(dirname "$0")" 2>/dev/null && pwd)/lib.sh"
if [ -f "$LIB" ]; then . "$LIB"; else
	# Piped from curl, so there is no sibling lib.sh yet. Fetch the repo first
	# and re-enter from the checkout rather than duplicating the library here.
	echo "bootstrapping: cloning $REPO_URL to $REPO"
	command -v git >/dev/null || { apt-get update -qq && apt-get install -y -qq git; }
	[ -d "$REPO/.git" ] || git clone --quiet --branch "$BRANCH" "$REPO_URL" "$REPO"
	exec bash "$REPO/deploy/install.sh" "$@"
fi
need_root

# The origin is what the browser types, and the server refuses WebSocket
# upgrades whose Origin header does not match it. Guessing wrong produces a
# client stuck in TRANSIENT_FAILURE with a green service, so ask now rather than
# debug it later.
if [ -z "$ORIGIN" ]; then
	ip=$(curl -fsS --max-time 5 http://169.254.169.254/metadata/v1/interfaces/public/0/ipv4/address 2>/dev/null || true)
	[ -n "$ip" ] || ip=$(hostname -I | awk '{print $1}')
	ORIGIN="http://$ip"
fi

printf '%s%sArticleFlux install%s %s%s%s\n' "$C_BLD" "$C_CYN" "$C_OFF" "$C_DIM" "$(date -Is)" "$C_OFF"
printf '%s  origin %s · port %s · branch %s%s\n\n' "$C_DIM" "$ORIGIN" "$PORT" "$BRANCH" "$C_OFF"

# --- 1. packages -------------------------------------------------------------
step "Installing packages"
export DEBIAN_FRONTEND=noninteractive
run apt-get update -qq
# sqlite3 is not optional here even though the server embeds its own: update.sh
# uses it to take a real .backup before migrating, and a box without it silently
# deploys with no database backup.
run apt-get install -y -qq git curl ca-certificates gzip sqlite3 ufw
done_ok "packages present"

# --- 2. swap -----------------------------------------------------------------
step "Making sure the box can link a Go binary"
mem_mb=$(free -m | awk 'NR==2 {print $2}')
swap_mb=$(free -m | awk 'NR==3 {print $2}')
if [ "$mem_mb" -lt 2048 ] && [ "$swap_mb" -lt 1024 ]; then
	# The linker is the peak, not the compiler. On a 1GB droplet with no swap the
	# OOM killer takes the build at the very end, which reads as a mysterious
	# "signal: killed" with no other symptom.
	if [ ! -f /swapfile ]; then
		run fallocate -l 2G /swapfile
		chmod 600 /swapfile
		run mkswap /swapfile
		run swapon /swapfile
		grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
		note "added a 2GB swapfile (${mem_mb}MB RAM is not enough to link without one)"
	fi
else
	note "${mem_mb}MB RAM, ${swap_mb}MB swap — enough"
fi
done_ok "memory ready"

# --- 3. go -------------------------------------------------------------------
step "Installing Go $GO_VERSION"
if [ -x /usr/local/go/bin/go ] && /usr/local/go/bin/go version | grep -q "go$GO_VERSION"; then
	note "already installed: $(/usr/local/go/bin/go version)"
else
	arch=$(dpkg --print-architecture)
	case "$arch" in amd64) goarch=amd64 ;; arm64) goarch=arm64 ;; *) echo "unsupported arch: $arch"; exit 1 ;; esac
	tgz="/tmp/go$GO_VERSION.linux-$goarch.tar.gz"
	run curl -fsSL -o "$tgz" "https://go.dev/dl/go$GO_VERSION.linux-$goarch.tar.gz"
	rm -rf /usr/local/go
	run tar -C /usr/local -xzf "$tgz"
	rm -f "$tgz"
	note "$(/usr/local/go/bin/go version)"
fi
grep -q '/usr/local/go/bin' /etc/profile.d/go.sh 2>/dev/null || echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
export PATH="$PATH:/usr/local/go/bin"
done_ok "Go ready"

# --- 4. user and directories -------------------------------------------------
step "Creating the service account and its directories"
if ! id "$OWNER" >/dev/null 2>&1; then
	run useradd --system --home-dir "$DATA" --shell /usr/sbin/nologin "$OWNER"
	note "created system user $OWNER (no shell, no password)"
fi
install -d -o "$OWNER" -g "$OWNER" -m 0700 "$DATA"
install -d -o root -g root -m 0750 "$BACKUPS"
install -d -m 0755 /var/log/articleflux
install -d -o "$OWNER" -g "$OWNER" -m 0755 /var/cache/articleflux-go
done_ok "directories ready"

# --- 5. source ---------------------------------------------------------------
step "Cloning the source"
[ -d "$REPO/.git" ] || run git clone --quiet --branch "$BRANCH" "$REPO_URL" "$REPO"
# GoWebComponents is a hard requirement, not an optional extra: go.mod resolves
# it through `replace ... => ../GoWebComponents`, and without the sibling
# checkout the build fails at module load with a path that means nothing to
# somebody who has not read go.mod.
[ -d "$GWC/.git" ] || run git clone --quiet "$GWC_URL" "$GWC"
run git -C "$REPO" fetch --quiet --all
note "ArticleFlux:      $(git -C "$REPO" log -1 --format='%h %s' | cut -c1-56)"
note "GoWebComponents:  $(git -C "$GWC" log -1 --format='%h %s' | cut -c1-56)"
done_ok "source ready"

# --- 6. build ----------------------------------------------------------------
step "Building the server and the client"
export GOCACHE=/var/cache/articleflux-go
export HOME="${HOME:-/root}"
mkdir -p "$REPO/bin"
rm -rf "$REPO/bin/web"
cp -a "$REPO/web" "$REPO/bin/web"
( cd "$REPO" && export GOOS=js GOARCH=wasm && run /usr/local/go/bin/go build -trimpath -ldflags="-s -w" -o "$REPO/bin/web/app.wasm" ./client/app )
# From the toolchain, not the repo — see the note in update.sh. Its absence is a
# client that boots to "Go is not defined" while every server-side check passes.
goroot=$(/usr/local/go/bin/go env GOROOT)
exec_js="$goroot/lib/wasm/wasm_exec.js"
[ -f "$exec_js" ] || exec_js="$goroot/misc/wasm/wasm_exec.js"
[ -f "$exec_js" ] || { echo "wasm_exec.js not found under $goroot"; exit 1; }
cp "$exec_js" "$REPO/bin/web/wasm_exec.js"
run gzip -9 -kf "$REPO/bin/web/app.wasm"
run gzip -9 -kf "$REPO/bin/web/wasm_exec.js"
note "client: $(du -h "$REPO/bin/web/app.wasm" | cut -f1) raw, $(du -h "$REPO/bin/web/app.wasm.gz" | cut -f1) gzipped"
( cd "$REPO" && run /usr/local/go/bin/go build -trimpath -o "$REPO/bin/articleflux" ./cmd/articleflux )
note "server: $(du -h "$REPO/bin/articleflux" | cut -f1)"
chown -R "$OWNER:$OWNER" "$REPO/bin"
done_ok "built"

# --- 7. service --------------------------------------------------------------
step "Installing the service, the watchdog and the backup timer"
# The unit in deploy/ is a template with example.com in it. The install is where
# that becomes this box: its paths, its port, its origin.
sed -e "s#/opt/articleflux#$REPO#g" \
    -e "s#https://reader.example.com#$ORIGIN#" \
    -e "s#127.0.0.1:9000#127.0.0.1:$PORT#" \
    -e "s#/var/lib/articleflux#$DATA#g" \
    "$REPO/deploy/articleflux.service" > /etc/systemd/system/articleflux.service

for u in articleflux-health.service articleflux-health.timer articleflux-backup.service articleflux-backup.timer; do
	[ -f "$REPO/deploy/$u" ] && cp "$REPO/deploy/$u" "/etc/systemd/system/$u"
done
install -m 755 "$REPO/deploy/articleflux-health.sh" /usr/local/bin/articleflux-health
install -m 755 "$REPO/deploy/update.sh" /usr/local/bin/articleflux-update 2>/dev/null || true

run systemctl daemon-reload
run sudo -u "$OWNER" "$REPO/bin/articleflux" migrate -db "$DATA/articleflux.db"
run systemctl enable --now articleflux
run systemctl enable --now articleflux-health.timer
[ -f /etc/systemd/system/articleflux-backup.timer ] && run systemctl enable --now articleflux-backup.timer
note "restart=always, enabled at boot, health-checked every 2 minutes"
done_ok "service running"

# --- 8. nginx ----------------------------------------------------------------
step "Putting nginx in front"
if [ "$WITH_NGINX" = "0" ]; then
	note "skipped (--no-nginx). The server listens on 127.0.0.1:$PORT only."
	done_ok "skipped"
else
	command -v nginx >/dev/null || run apt-get install -y -qq nginx
	sed -e "s#127.0.0.1:9000#127.0.0.1:$PORT#g" "$REPO/deploy/nginx.conf" > /etc/nginx/sites-available/articleflux
	ln -sf /etc/nginx/sites-available/articleflux /etc/nginx/sites-enabled/articleflux
	rm -f /etc/nginx/sites-enabled/default
	run nginx -t
	run systemctl enable nginx
	run systemctl reload nginx
	# The upgrade headers are the whole reason this proxy needs a config rather
	# than a default: every RPC in the application rides one WebSocket, and a
	# proxy that drops Upgrade serves a page that loads and then does nothing.
	note "proxying :80 → 127.0.0.1:$PORT, WebSocket upgrade included"
	done_ok "nginx serving"
fi

# --- 9. firewall and proof ---------------------------------------------------
step "Checking the whole path end to end"
if command -v ufw >/dev/null; then
	run ufw allow OpenSSH || true
	[ "$WITH_NGINX" = "1" ] && { run ufw allow 'Nginx HTTP' || true; }
	ufw status | grep -q '^Status: active' || note "ufw is installed but inactive — enable it with: ufw --force enable"
fi

wait_healthy "http://127.0.0.1:$PORT/healthz" 120 || { echo "the server never answered on 127.0.0.1:$PORT"; exit 1; }
note "server answers on 127.0.0.1:$PORT"

if [ "$WITH_NGINX" = "1" ]; then
	code=$(curl -fsS -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1/ || echo 000)
	[ "$code" = "200" ] || { echo "nginx returned $code for / — see /var/log/nginx/error.log"; exit 1; }
	note "nginx returns 200 for /"
	# The tunnel, proved rather than assumed: a 101 here is the difference
	# between a working reader and a page that loads and never connects.
	ws=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 \
		-H 'Connection: Upgrade' -H 'Upgrade: websocket' \
		-H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
		-H "Origin: $ORIGIN" http://127.0.0.1/grpc || echo 000)
	[ "$ws" = "101" ] && note "WebSocket upgrade through nginx: 101" || warn "WebSocket upgrade returned $ws, expected 101 — check the Origin ($ORIGIN) matches what the browser will send"
fi
done_ok "verified"

say ""
say "  Open        $ORIGIN"
say "  First run   the setup screen claims the instance — the first person to set a password owns it"
say "  Logs        journalctl -u articleflux -f"
say "  Update      sudo $REPO/deploy/update.sh"
say "  Roll back   sudo $REPO/deploy/rollback.sh --list"
finish "Installed"
