#!/usr/bin/env bash
# ArticleFlux — pull, build, migrate, restart. With a way back.
#
#   sudo /opt/ArticleFlux/deploy/update.sh            # update to origin/main
#   sudo /opt/ArticleFlux/deploy/update.sh --ref v1.2 # a tag or branch
#   sudo /opt/ArticleFlux/deploy/update.sh --force    # rebuild even if current
#   sudo /opt/ArticleFlux/deploy/update.sh --verbose  # stream build output
#
# The shape of this script is decided by one fact: it replaces a running server
# that somebody is reading in a browser right now. So it builds BEFORE it stops
# anything, backs up the database BEFORE it migrates, and keeps the outgoing
# binary until the incoming one has answered a health check. If any of that
# fails, the old binary goes back and the service comes up on it — an update
# that cannot be undone is not an update, it is a gamble.
set -euo pipefail

# Recorded verbatim in the failure report: how the script was invoked is half of
# what a reproduction needs, and it is the half nobody remembers.
SCRIPT_ARGS="$*"
ORIG_ARGS=("$@")
SELF_HASH=$(sha256sum "$0" 2>/dev/null | cut -d" " -f1)

REPO="${ARTICLEFLUX_REPO:-/opt/ArticleFlux}"
GWC="${GWC_REPO:-/opt/GoWebComponents}"
DB="${ARTICLEFLUX_DB:-/var/lib/articleflux/articleflux.db}"
HEALTH="${ARTICLEFLUX_HEALTH_URL:-http://127.0.0.1:9000/healthz}"
# The APPLICATION, addressed directly, not through nginx. Step 8 asks two
# different questions and they need two different addresses: "is this file in
# the build I just deployed" is a question for the app, and asking nginx instead
# means the answer changes when the edge config changes. It did, and it cost
# every deploy from 2026-07-29 onward.
APP="${ARTICLEFLUX_APP_URL:-http://127.0.0.1:9000}"
SERVICE=articleflux
OWNER="${ARTICLEFLUX_USER:-articleflux}"
BACKUPS="${ARTICLEFLUX_BACKUPS:-/var/backups/articleflux}"
GO="${GO:-/usr/local/go/bin/go}"

REF=""
FORCE=""
STEP_TOTAL=8
while [ $# -gt 0 ]; do
	case "$1" in
		--ref) REF="${2:?--ref needs a branch or tag}"; shift 2 ;;
		--force) FORCE=1; shift ;;
		--verbose|-v) VERBOSE=1; shift ;;
		-h|--help) sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) echo "unknown option: $1 (try --help)" >&2; exit 2 ;;
	esac
done

. "$(cd "$(dirname "$0")" && pwd)/lib.sh"
need_root

printf '%s%sArticleFlux update%s %s%s%s\n\n' "$C_BLD" "$C_CYN" "$C_OFF" "$C_DIM" "$(date -Is)" "$C_OFF"

# --- 1. preflight ------------------------------------------------------------
step "Checking the box is in a state worth updating"
[ -d "$REPO/.git" ] || { echo "no git checkout at $REPO"; exit 1; }
[ -x "$GO" ] || { echo "no Go toolchain at $GO — set GO=/path/to/go"; exit 1; }
command -v git >/dev/null || { echo "git is not installed"; exit 1; }
systemctl cat "$SERVICE" >/dev/null 2>&1 || { echo "$SERVICE is not installed — run install.sh first"; exit 1; }

# Space, checked before anything is written rather than discovered halfway
# through linking a 50 MB binary. A full disk during a build leaves a truncated
# binary in place, which is the one failure this script cannot roll back from
# cleanly if it has already replaced the old one.
avail_mb=$(df -Pm "$REPO" | awk 'NR==2 {print $4}')
[ "$avail_mb" -gt 1500 ] || { echo "only ${avail_mb}MB free on $REPO — a build needs ~1.5GB"; exit 1; }
note "$(free -m | awk 'NR==2 {print $2"MB RAM, "$7"MB available"}'), ${avail_mb}MB disk free"

# A deployment checkout is not a workspace, but people edit files on it anyway —
# a quick sed to unblock something at 3am, a chmod +x, a config tweak — and then
# every future deploy dies on "your local changes would be overwritten by merge"
# with the fix buried in git's advice rather than the script's.
#
# Stashed, not discarded. The change is recoverable by name and the deploy goes
# through, which is the only combination that is both safe and unattended.
stash_repo() {
	if ! git -C "$1" diff --quiet || ! git -C "$1" diff --cached --quiet; then
		msg="update.sh auto-stash $(date -Is)"
		git -C "$1" stash push -m "$msg" >> "$LOG" 2>&1 || true
		warn "$(basename "$1") had local changes — stashed as \"$msg\""
		note "recover them with: git -C $1 stash list"
	fi
}
stash_repo "$REPO"
[ -d "$GWC/.git" ] && stash_repo "$GWC"
note "current: $(cd "$REPO" && git log -1 --format='%h %s' 2>/dev/null | cut -c1-64)"
done_ok "ready"

# --- 2. fetch ----------------------------------------------------------------
step "Fetching the latest code"
old_sha=$(cd "$REPO" && git rev-parse HEAD)

# The sibling library first: go.mod points at it with a replace directive, so a
# server built against a stale checkout of it is a server built from code that
# is not what this commit says it is.
if [ -d "$GWC/.git" ]; then
	run git -C "$GWC" fetch --quiet --all --prune
	gwc_branch=$(git -C "$GWC" rev-parse --abbrev-ref HEAD)
	run git -C "$GWC" merge --ff-only "origin/$gwc_branch" || warn "GoWebComponents is not fast-forwardable — left on $(git -C "$GWC" log -1 --format=%h)"
	note "GoWebComponents: $(git -C "$GWC" log -1 --format='%h %s' | cut -c1-56)"
fi

run git -C "$REPO" fetch --quiet --all --prune --tags
if [ -n "$REF" ]; then
	run git -C "$REPO" checkout --quiet "$REF"
	run git -C "$REPO" pull --quiet --ff-only || true
else
	branch=$(git -C "$REPO" rev-parse --abbrev-ref HEAD)
	# --ff-only, never a merge. This checkout is a deployment, not a workspace:
	# if it cannot fast-forward then somebody has committed on the server, and
	# quietly merging their work into a release is how a droplet ends up running
	# code that exists nowhere else.
	run git -C "$REPO" merge --ff-only "origin/$branch"
fi

new_sha=$(cd "$REPO" && git rev-parse HEAD)
if [ "$old_sha" = "$new_sha" ] && [ -z "$FORCE" ]; then
	done_ok "already at $(git -C "$REPO" log -1 --format='%h %s' | cut -c1-56)"
	say ""
	say "Nothing to do. Use --force to rebuild anyway."
	finish "Up to date"
	exit 0
fi
# This script is one of the files it just pulled, and bash reads a script from
# disk as it runs — so a run that updates update.sh keeps executing the old
# copy, and the fix it just downloaded does not apply until somebody runs it
# again. That is not theoretical: the commit that added the wasm_exec.js check
# deployed a broken client anyway, because the check arrived in the same pull.
#
# Re-exec into the new version, once. AF_REEXEC stops a loop if the hash somehow
# keeps changing, and exec replaces the process so the old copy's traps go with
# it. The log restarts too, which is the price of running the right code.
if [ "${AF_REEXEC:-}" != "1" ] && [ -n "$SELF_HASH" ]; then
	if [ "$(sha256sum "$0" | cut -d' ' -f1)" != "$SELF_HASH" ]; then
		note "update.sh itself changed in this pull — re-running the new version"
		trap - EXIT INT TERM ERR
		AF_REEXEC=1 exec bash "$0" "${ORIG_ARGS[@]}"
	fi
fi

changed=$(git -C "$REPO" diff --name-only "$old_sha" "$new_sha" | wc -l)
note "$(git -C "$REPO" log -1 --format='%h %s' | cut -c1-64)"
note "${changed} file(s) changed since ${old_sha:0:8}"
done_ok "updated to ${new_sha:0:8}"

# --- 3. keep the way back ----------------------------------------------------
step "Backing up the binary and the database"
mkdir -p "$BACKUPS"
stamp=$(date +%Y%m%d-%H%M%S)
prev_bin="$BACKUPS/articleflux-$stamp.bin"
prev_web="$BACKUPS/web-$stamp"
cp -a "$REPO/bin/articleflux" "$prev_bin"
cp -a "$REPO/bin/web" "$prev_web"

# .backup, not cp: SQLite in WAL mode has committed data living in a sidecar
# file, and copying the .db alone captures a database that is missing whatever
# was written since the last checkpoint. sqlite3 may not be installed, in which
# case say so plainly rather than pretending there is a backup.
db_backup="$BACKUPS/articleflux-$stamp.db"
if command -v sqlite3 >/dev/null 2>&1; then
	run sqlite3 "$DB" ".backup '$db_backup'"
	note "database: $db_backup ($(du -h "$db_backup" | cut -f1))"
else
	warn "sqlite3 not installed — NO database backup was taken (apt install sqlite3)"
	db_backup=""
fi

# Armed from here on. Anything that fails below puts the old binary back and
# starts the service on it.
ROLLBACK_CMD="cp -a '$prev_bin' '$REPO/bin/articleflux'; rm -rf '$REPO/bin/web'; cp -a '$prev_web' '$REPO/bin/web'; chown -R $OWNER:$OWNER '$REPO/bin'; systemctl reset-failed $SERVICE || true; systemctl restart $SERVICE"

# Ten of each, so a bad week of deploys does not fill the disk with rollbacks.
#
# The `|| true` is load-bearing. `ls` of a glob that matches nothing exits 2, and
# under `set -e` with `pipefail` that took down the whole update on the first run
# of this script — a deploy aborted by its own housekeeping, on a box whose only
# fault was not having ten old backups yet.
prune() { ls -1dt $1 2>/dev/null | tail -n +11 | xargs -r rm -rf || true; }
prune "$BACKUPS/articleflux-*.bin"
prune "$BACKUPS/web-*"
prune "$BACKUPS/articleflux-*.db"
done_ok "rollback point saved"

# --- 4/5. build --------------------------------------------------------------
# Built into .new siblings and moved into place at the end. A build that dies
# halfway then leaves the running binary untouched, and the move is atomic, so
# there is no instant where bin/articleflux is a partial file.
step "Building the WebAssembly client"
export HOME="${HOME:-/root}"
export GOCACHE="${GOCACHE:-/var/cache/articleflux-go}"
export GOFLAGS="-trimpath"
mkdir -p "$GOCACHE"
rm -rf "$REPO/bin/web.new"
cp -a "$REPO/web" "$REPO/bin/web.new"
( cd "$REPO" && export GOOS=js GOARCH=wasm && run "$GO" build -ldflags="-s -w" -o "$REPO/bin/web.new/app.wasm" ./client/app )
# wasm_exec.js comes from the TOOLCHAIN, not the repo, and it is not optional:
# without it the page loads, the module downloads, and the app dies at
# "Go is not defined" behind a boot error that blames the server. This script
# shipped exactly that to production, because copying web/ looks like it copies
# everything the client needs.
#
# It must also come from the same Go that built the module — a copy from an
# older toolchain fails at instantiate with an import mismatch that reads like a
# corrupt binary.
goroot=$("$GO" env GOROOT)
exec_js="$goroot/lib/wasm/wasm_exec.js"
[ -f "$exec_js" ] || exec_js="$goroot/misc/wasm/wasm_exec.js"
[ -f "$exec_js" ] || { echo "wasm_exec.js not found under $goroot — the client cannot boot without it"; exit 1; }
cp "$exec_js" "$REPO/bin/web.new/wasm_exec.js"
run gzip -9 -kf "$REPO/bin/web.new/app.wasm"
run gzip -9 -kf "$REPO/bin/web.new/wasm_exec.js"
note "client: $(du -h "$REPO/bin/web.new/app.wasm" | cut -f1) raw, $(du -h "$REPO/bin/web.new/app.wasm.gz" | cut -f1) gzipped"
note "boot shim: wasm_exec.js from $goroot"

# The service worker caches by version string, so a client that ships without a
# fresh stamp is a client browsers keep the old copy of — the update lands and
# nobody sees it.
if [ -f "$REPO/bin/web.new/sw.js" ]; then
	sed -i "s/__BUILD__/${new_sha:0:12}/g" "$REPO/bin/web.new/sw.js" || true
fi
done_ok "client built"

for f in app.wasm app.wasm.gz wasm_exec.js index.html; do
	[ -s "$REPO/bin/web.new/$f" ] || { echo "the built client is missing $f — refusing to deploy it"; exit 1; }
done
done_ok "client complete"

step "Building the server"
( cd "$REPO" && run "$GO" build -o "$REPO/bin/articleflux.new" ./cmd/articleflux )
note "server: $(du -h "$REPO/bin/articleflux.new" | cut -f1)"
done_ok "server built"

# --- 6. migrate --------------------------------------------------------------
step "Applying database migrations"
# Migrations run with the NEW binary and the service still up on the old one.
# Additive migrations are safe that way and non-additive ones are not, which is
# why the backup above is taken first and unconditionally.
chown "$OWNER:$OWNER" "$REPO/bin/articleflux.new"
chmod 755 "$REPO/bin/articleflux.new"
run sudo -u "$OWNER" "$REPO/bin/articleflux.new" migrate -db "$DB"
note "$(sudo -u "$OWNER" "$REPO/bin/articleflux.new" migrate -db "$DB" 2>&1 | tail -1 | cut -c1-90)"
done_ok "schema current"

# --- 7. swap and restart -----------------------------------------------------
step "Swapping in the new build and restarting"
mv -f "$REPO/bin/articleflux.new" "$REPO/bin/articleflux"
rm -rf "$REPO/bin/web.old"
[ -d "$REPO/bin/web" ] && mv "$REPO/bin/web" "$REPO/bin/web.old"
mv "$REPO/bin/web.new" "$REPO/bin/web"
rm -rf "$REPO/bin/web.old"
chown -R "$OWNER:$OWNER" "$REPO/bin"

# Unit files travel with the repo, so a change to them lands on the next update
# instead of waiting for somebody to remember. Copied only when different, so an
# unchanged deploy does not churn systemd.
for u in articleflux-health.service articleflux-health.timer articleflux-backup.service articleflux-backup.timer; do
	if [ -f "$REPO/deploy/$u" ] && ! cmp -s "$REPO/deploy/$u" "/etc/systemd/system/$u"; then
		cp "$REPO/deploy/$u" "/etc/systemd/system/$u"
		note "updated unit: $u"
		reload_units=1
	fi
done
if [ -f "$REPO/deploy/articleflux-health.sh" ] && ! cmp -s "$REPO/deploy/articleflux-health.sh" /usr/local/bin/articleflux-health; then
	install -m 755 "$REPO/deploy/articleflux-health.sh" /usr/local/bin/articleflux-health
	note "updated the health watchdog"
fi
# articleflux.service itself is deliberately NOT copied: the deployed copy holds
# this box's address, paths and flags, and the repo's is a template with
# reader.example.com in it. Overwriting it here once took the service down with
# a 203/EXEC that looked like a broken binary. Diff it by hand when it changes.
if ! cmp -s "$REPO/deploy/articleflux.service" /etc/systemd/system/articleflux.service; then
	note "deploy/articleflux.service differs from the installed unit (expected — it holds this box's paths)"
fi
[ -n "${reload_units:-}" ] && run systemctl daemon-reload

run systemctl reset-failed "$SERVICE" || true
run systemctl restart "$SERVICE"
done_ok "restarted"

# --- 8. prove it -----------------------------------------------------------
step "Checking the server actually came back"
if ! wait_healthy "$HEALTH" 120; then
	echo "the new build did not answer $HEALTH within 120s"
	exit 1
fi
# The files the CLIENT needs, asked of the APP. /healthz is a server-side
# question and it passed happily while the deployed client was missing its boot
# shim and dying at "Go is not defined" in every browser. A deploy check that
# cannot see that is not checking the thing that matters.
#
# On $APP and not through nginx, which is a correction rather than a preference.
# This loop used to fetch http://127.0.0.1$f, and when the site moved to TLS the
# edge began answering a bare-IP request with `301 -> https://feed.earlcameron.com`.
# 301 is not 200, so the check failed; it failed AFTER the restart, so a
# perfectly good build was rolled back every time; and it failed identically no
# matter what was in the build, because nothing in the build could change it.
# Between 2026-07-29 and 2026-07-30 every deploy died here and the box sat on a
# binary from before the TLS change. A check that a deploy cannot pass is not a
# safety net, it is a stuck door.
for f in / /app.wasm.gz /wasm_exec.js; do
	c=$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 "$APP$f" 2>/dev/null)
	[ "$c" = "200" ] || { echo "the deployed client is missing $f (HTTP $c) — it will not boot in a browser"; exit 1; }
done
note "client assets served: index, app.wasm.gz, wasm_exec.js"

# Then the edge, the way a reader reaches it: -L, because the canonical redirect
# to https:// IS the correct answer to a plaintext request and following it is
# what a browser does. A server that answers on loopback and 502s through the
# proxy is still an outage, and it is the version a reader sees.
#
# A warning and not a failure, deliberately. The build has already proven itself
# above; DNS, the certificate and hairpin routing back to the box are none of its
# doing, and rolling a good binary back because the box could not resolve its own
# public name would be this check causing the outage it exists to catch.
if systemctl is-active --quiet nginx; then
	edge=$(curl -sS -L --max-time 20 -o /dev/null -w '%{http_code}' http://127.0.0.1/ 2>/dev/null || echo 000)
	final=$(curl -sS -L --max-time 20 -o /dev/null -w '%{url_effective}' http://127.0.0.1/ 2>/dev/null || echo "?")
	if [ "$edge" = "200" ]; then
		note "nginx is serving it too — $final"
	else
		warn "nginx answered $edge for / (followed to $final) — check /var/log/nginx/error.log"
		warn "the build is good and is running; this is the edge, not the deploy"
	fi
fi
trap - EXIT INT TERM   # past the point of no return; the new build works
ROLLBACK_CMD=""
done_ok "answering"

say ""
say "  version   $(git -C "$REPO" log -1 --format='%h %s' | cut -c1-60)"
say "  service   $(systemctl is-active $SERVICE), $(systemctl show -p NRestarts --value $SERVICE) restarts since boot"
say "  rollback  sudo $REPO/deploy/rollback.sh $(basename "$prev_bin")"
[ -n "$db_backup" ] && say "  database  $db_backup"
finish "Updated"
