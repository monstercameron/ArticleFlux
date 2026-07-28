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

REPO="${ARTICLEFLUX_REPO:-/opt/ArticleFlux}"
GWC="${GWC_REPO:-/opt/GoWebComponents}"
DB="${ARTICLEFLUX_DB:-/var/lib/articleflux/articleflux.db}"
HEALTH="${ARTICLEFLUX_HEALTH_URL:-http://127.0.0.1:9000/healthz}"
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
run gzip -9 -kf "$REPO/bin/web.new/app.wasm"
note "client: $(du -h "$REPO/bin/web.new/app.wasm" | cut -f1) raw, $(du -h "$REPO/bin/web.new/app.wasm.gz" | cut -f1) gzipped"

# The service worker caches by version string, so a client that ships without a
# fresh stamp is a client browsers keep the old copy of — the update lands and
# nobody sees it.
if [ -f "$REPO/bin/web.new/sw.js" ]; then
	sed -i "s/__BUILD__/${new_sha:0:12}/g" "$REPO/bin/web.new/sw.js" || true
fi
done_ok "client built"

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
# Through nginx as well as on loopback, when nginx is here: a server that
# answers on 127.0.0.1 and 502s through the proxy is still an outage, and it is
# the version a reader sees.
if systemctl is-active --quiet nginx; then
	if curl -fsS --max-time 10 -o /dev/null http://127.0.0.1/ 2>/dev/null; then
		note "nginx is serving it too"
	else
		warn "nginx is running but did not serve / — check /var/log/nginx/error.log"
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
