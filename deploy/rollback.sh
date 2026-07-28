#!/usr/bin/env bash
# ArticleFlux — put the previous build back.
#
#   sudo /opt/ArticleFlux/deploy/rollback.sh              # the most recent backup
#   sudo /opt/ArticleFlux/deploy/rollback.sh --list       # what is available
#   sudo /opt/ArticleFlux/deploy/rollback.sh articleflux-20260728-141500.bin
#
# update.sh rolls itself back when a deploy fails outright. This is for the other
# case: the deploy succeeded, the health check passed, and an hour later it is
# clear the new build is wrong. Nothing automatic can catch that, and hunting for
# the old binary while the reader is broken is not the moment to be improvising.
#
# The database is deliberately NOT rolled back. A schema migration that ran an
# hour ago has had an hour of writes on top of it, and restoring the pre-deploy
# copy would throw them away — usually a worse outcome than the bug being rolled
# back. The .db backups are listed so a human can make that call themselves.
set -euo pipefail

# Recorded verbatim in the failure report: how the script was invoked is half of
# what a reproduction needs, and it is the half nobody remembers.
SCRIPT_ARGS="$*"

REPO="${ARTICLEFLUX_REPO:-/opt/ArticleFlux}"
BACKUPS="${ARTICLEFLUX_BACKUPS:-/var/backups/articleflux}"
HEALTH="${ARTICLEFLUX_HEALTH_URL:-http://127.0.0.1:9000/healthz}"
OWNER="${ARTICLEFLUX_USER:-articleflux}"
SERVICE=articleflux
STEP_TOTAL=3

. "$(cd "$(dirname "$0")" && pwd)/lib.sh"

if [ "${1:-}" = "--list" ]; then
	printf '%sAvailable rollback points in %s%s\n\n' "$C_BLD" "$BACKUPS" "$C_OFF"
	ls -1t "$BACKUPS"/articleflux-*.bin 2>/dev/null | while read -r f; do
		printf '  %-46s %s  %s\n' "$(basename "$f")" "$(du -h "$f" | cut -f1)" "$(date -r "$f" '+%Y-%m-%d %H:%M')"
	done || echo "  (none)"
	printf '\n%sDatabase snapshots (restore by hand, see the header of this script)%s\n' "$C_DIM" "$C_OFF"
	ls -1t "$BACKUPS"/articleflux-*.db 2>/dev/null | head -5 | sed 's/^/  /' || true
	trap - EXIT INT TERM
	exit 0
fi

need_root

if [ -n "${1:-}" ]; then
	BIN="$BACKUPS/$(basename "$1")"
else
	BIN=$(ls -1t "$BACKUPS"/articleflux-*.bin 2>/dev/null | head -1 || true)
fi
[ -n "$BIN" ] && [ -f "$BIN" ] || { echo "no such backup: ${1:-<none found in $BACKUPS>}"; echo "try: $0 --list"; exit 1; }

stamp=$(basename "$BIN" .bin); stamp=${stamp#articleflux-}
WEB="$BACKUPS/web-$stamp"

printf '%s%sArticleFlux rollback%s to %s%s%s\n\n' "$C_BLD" "$C_YEL" "$C_OFF" "$C_BLD" "$(basename "$BIN")" "$C_OFF"

step "Putting the previous build in place"
note "from: $(du -h "$REPO/bin/articleflux" | cut -f1) built $(date -r "$REPO/bin/articleflux" '+%Y-%m-%d %H:%M')"
note "to:   $(du -h "$BIN" | cut -f1) built $(date -r "$BIN" '+%Y-%m-%d %H:%M')"
cp -a "$BIN" "$REPO/bin/articleflux.rollback"
mv -f "$REPO/bin/articleflux.rollback" "$REPO/bin/articleflux"
if [ -d "$WEB" ]; then
	rm -rf "$REPO/bin/web.rollback"
	cp -a "$WEB" "$REPO/bin/web.rollback"
	rm -rf "$REPO/bin/web"
	mv "$REPO/bin/web.rollback" "$REPO/bin/web"
	note "client assets restored from $(basename "$WEB")"
else
	warn "no client backup for $stamp — the wasm on disk is still the new one"
fi
chown -R "$OWNER:$OWNER" "$REPO/bin"
done_ok "restored"

step "Restarting"
run systemctl reset-failed "$SERVICE" || true
run systemctl restart "$SERVICE"
done_ok "restarted"

step "Checking it came back"
wait_healthy "$HEALTH" 120 || { echo "the ROLLED BACK build is not answering either — this is not a bad deploy, look at the box"; exit 1; }
done_ok "answering"

say ""
say "  Rolled back to $(basename "$BIN")."
say "  The git checkout at $REPO is UNCHANGED — it still points at the new commit."
say "  Fix forward and run update.sh --force, or: git -C $REPO reset --hard <good-sha> && update.sh --force"
finish "Rolled back"
