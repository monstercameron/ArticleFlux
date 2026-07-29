#!/usr/bin/env bash
# Deploy earlcameron.com. Installed as /usr/local/bin/earlcameron-deploy.
#
# An adapter, not a deploy system. The portfolio repo is growing its own
# deploy/update.sh — with a releases directory and a `current` symlink — and when
# that lands it becomes the real thing. This exists because the webhook needs a
# stable, root-owned, allow-listable path to call TODAY, and because a sudoers
# entry pointing into a checkout that a deploy can rewrite is a way to hand root
# to whoever can push.
#
# So: if the repo ships its own update script, run that. Otherwise do the minimum
# correctly — pull all three checkouts, build, swap, restart, and prove the site
# came back before declaring victory.
set -euo pipefail

ROOT="${SITE_ROOT:-/opt/earlcameron}"
REPO="$ROOT/PersonalWebsiteMid2026"
HEALTH="${SITE_HEALTH_URL:-http://127.0.0.1:8095/healthz}"
PUBLIC="${SITE_PUBLIC_URL:-http://127.0.0.1:8080}"
OWNER="${SITE_USER:-earlcameron}"
GO="${GO:-/usr/local/go/bin/go}"
SERVICE=earlcameron

say() { printf '==> %s\n' "$*"; }

die() { printf 'xx  %s\n' "$*" >&2; exit 1; }

# --- PULL FIRST, THEN DECIDE ---------------------------------------------------
#
# This check used to run against whatever was already on disk, which is wrong the
# one time it matters: on a box that has never deployed, the repo's own update.sh
# has not been fetched yet, so the check failed, this script took its own path,
# and the handover never happened. It "self-healed" only because that path pulled
# the file as a side effect — meaning the FIRST deploy on any fresh box silently
# ran a different deploy implementation from every subsequent one.
#
# That is not academic. The two implementations had different REF POLICIES: the
# built-in path fast-forwards each checkout to origin/<whatever branch it is on>,
# while update.sh honours the pinned GWC_REF. A GoWebComponents checkout left on
# `v4` therefore stayed on v4 indefinitely, and the site could not build against
# its own go.mod — which is exactly how this failed on 2026-07-29.
#
# So: fetch the site repo, THEN look for its script. One fetch is cheap insurance
# against running the wrong deployer.
[ -d "$REPO/.git" ] || die "no checkout at $REPO — run the site's deploy/install.sh first"
say "refreshing $REPO to see which deployer to use"
if command -v runuser >/dev/null 2>&1 && id "$OWNER" >/dev/null 2>&1; then
	runuser -u "$OWNER" -- git -C "$REPO" fetch --quiet --all --prune 2>/dev/null \
		|| say "fetch failed — continuing; update.sh reports properly if this matters"
else
	git -C "$REPO" fetch --quiet --all --prune 2>/dev/null || true
fi
_branch=$(git -C "$REPO" rev-parse --abbrev-ref HEAD 2>/dev/null || echo HEAD)
[ "$_branch" = "HEAD" ] || git -C "$REPO" merge --ff-only "origin/$_branch" >/dev/null 2>&1 || true

# Prefer the repo's own script. Handing over is the point of this file.
#
# `-f`, not `-x`: a checkout whose exec bit did not survive — a clone made on
# Windows, a tarball, a restored backup — is a script that exists and would run
# perfectly under bash. Refusing to delegate over a permission bit is how a box
# silently falls back to a deployer nobody has exercised in months.
if [ -f "$REPO/deploy/update.sh" ]; then
	say "delegating to the repo's deploy/update.sh"
	exec bash "$REPO/deploy/update.sh" "$@"
fi

# --- the built-in path is now a LAST RESORT, and says so ------------------------
#
# Reaching here means the site repo genuinely ships no deploy script. That was
# true once and is not any more, so it now far more likely means a broken or
# half-restored checkout. It still deploys — refusing outright would turn a
# recoverable state into an outage — but it is LOUD, because a deploy that
# quietly uses the wrong ref policy is worse than one that fails.
say "WARNING: $REPO/deploy/update.sh is missing — using the built-in fallback"
say "WARNING: the fallback tracks each checkout's CURRENT BRANCH and ignores the"
say "WARNING: pinned refs in the repo's deploy/lib.sh. If GoWebComponents is not"
say "WARNING: on the tag the site's go.mod requires, this build will fail."
export HOME="${HOME:-/root}" GOCACHE="${GOCACHE:-/var/cache/earlcameron-go}"
mkdir -p "$GOCACHE"

# All three checkouts move together: go.mod resolves GoWebComponents and CashFlux
# through directory replace directives, so building the site against a stale
# sibling builds something other than what this commit describes.
for d in "$ROOT/GoWebComponents" "$ROOT/CashFlux" "$REPO"; do
	[ -d "$d/.git" ] || continue
	br=$(git -C "$d" rev-parse --abbrev-ref HEAD)
	# A deployment checkout people have hand-edited would otherwise block every
	# future deploy on "your local changes would be overwritten". Stashed by name:
	# recoverable, and the deploy proceeds unattended.
	if ! git -C "$d" diff --quiet; then
		git -C "$d" stash push -m "earlcameron-deploy auto-stash $(date -Is)" >/dev/null || true
		say "$(basename "$d") had local changes — stashed"
	fi
	git -C "$d" fetch --quiet --all --prune
	git -C "$d" merge --ff-only "origin/$br" >/dev/null 2>&1 \
		|| say "$(basename "$d") [$br] would not fast-forward — left at $(git -C "$d" log -1 --format=%h)"
	say "$(basename "$d") [$br] $(git -C "$d" log -1 --format='%h %s' | cut -c1-48)"
done

say "keeping a rollback point"
mkdir -p /var/backups/earlcameron
stamp=$(date +%Y%m%d-%H%M%S)
cp -a "$REPO/bin/server" "/var/backups/earlcameron/server-$stamp.bin" 2>/dev/null || true
cp -a "$REPO/web/static" "/var/backups/earlcameron/static-$stamp" 2>/dev/null || true
# ls of a glob matching nothing exits 2, which under set -e would abort the
# deploy over housekeeping. That exact bug took down a deploy on this box once.
( ls -1dt /var/backups/earlcameron/server-*.bin 2>/dev/null | tail -n +11 | xargs -r rm -f ) || true
( ls -1dt /var/backups/earlcameron/static-* 2>/dev/null | tail -n +11 | xargs -r rm -rf ) || true

restore() {
	say "ROLLING BACK to server-$stamp"
	cp -a "/var/backups/earlcameron/server-$stamp.bin" "$REPO/bin/server" 2>/dev/null || true
	rm -rf "$REPO/web/static"
	cp -a "/var/backups/earlcameron/static-$stamp" "$REPO/web/static" 2>/dev/null || true
	chown -R "$OWNER:$OWNER" "$REPO/bin" "$REPO/web"
	systemctl reset-failed "$SERVICE" || true
	systemctl restart "$SERVICE" || true
}

say "building the wasm client"
rm -rf "$REPO/web/static.new"
mkdir -p "$REPO/web/static.new"
( cd "$REPO" && GOOS=js GOARCH=wasm "$GO" build -o "$REPO/web/static.new/app.wasm" ./client )

# wasm_exec.js comes from the toolchain, not the repo, and from the SAME
# toolchain that built the module. Missing, the page loads and dies at "Go is not
# defined"; stale, it fails at instantiate in a way that reads like a corrupt
# binary. The sister deployment on this box shipped without it for an hour while
# every server-side check stayed green.
goroot=$("$GO" env GOROOT)
exec_js="$goroot/lib/wasm/wasm_exec.js"
[ -f "$exec_js" ] || exec_js="$goroot/misc/wasm/wasm_exec.js"
[ -f "$exec_js" ] || { echo "wasm_exec.js not found under $goroot"; exit 1; }
cp "$exec_js" "$REPO/web/static.new/wasm_exec.js"
for f in app.wasm wasm_exec.js; do
	[ -s "$REPO/web/static.new/$f" ] || { echo "built client is missing $f — refusing to deploy"; exit 1; }
done

say "building the server"
( cd "$REPO" && "$GO" build -o "$REPO/bin/server.new" ./cmd/server )

say "swapping and restarting"
mv -f "$REPO/bin/server.new" "$REPO/bin/server"
rm -rf "$REPO/web/static.old"
[ -d "$REPO/web/static" ] && mv "$REPO/web/static" "$REPO/web/static.old"
mv "$REPO/web/static.new" "$REPO/web/static"
rm -rf "$REPO/web/static.old"
chown -R "$OWNER:$OWNER" "$REPO/bin" "$REPO/web"
systemctl reset-failed "$SERVICE" || true
systemctl restart "$SERVICE"

say "checking it came back"
deadline=$(( $(date +%s) + 120 ))
until curl -fsS --max-time 5 -o /dev/null "$HEALTH" 2>/dev/null; do
	if [ "$(date +%s)" -ge "$deadline" ]; then
		echo "the new build never answered $HEALTH"
		journalctl -u "$SERVICE" -n 40 --no-pager || true
		restore
		exit 1
	fi
	sleep 2
done

# Server-side health is not the same question as "does it work in a browser".
for f in / /static/app.wasm /static/wasm_exec.js; do
	c=$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 "$PUBLIC$f" 2>/dev/null)
	if [ "$c" != "200" ]; then
		echo "the deployed client is missing $f (HTTP $c) — it will not boot in a browser"
		restore
		exit 1
	fi
done
say "client assets served: index, app.wasm, wasm_exec.js"

ws=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
	-H 'Connection: Upgrade' -H 'Upgrade: websocket' -H 'Sec-WebSocket-Version: 13' \
	-H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' -H "Origin: $PUBLIC" "$PUBLIC/socket" 2>/dev/null)
[ "$ws" = "101" ] && say "WebSocket upgrade on /socket: 101" || say "WARNING: /socket returned $ws, expected 101"

say "deployed $(git -C "$REPO" log -1 --format='%h %s' | cut -c1-56)"
