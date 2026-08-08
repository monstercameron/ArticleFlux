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
#
# THE CASE THAT FOLLOWS FROM THAT: if the build being rolled back ran a
# migration, the old binary now faces a schema it does not know. The server
# refuses to start and says so — "this database is at schema NNNN ... it was
# migrated by a newer version" (store.Migrate). That refusal is the correct
# answer and not a fault in this script: an old binary reading a forward schema
# starts fine, looks healthy, and then writes against columns it does not
# understand.
#
# So if the rollback below ends with the service failing to come up on exactly
# that message, this is the fork:
#
#   - roll FORWARD instead (fix the bug in a new build), or
#   - restore the .db snapshot taken before the deploy, accepting the lost
#     writes — the trade the paragraph above is describing.
#
# There is no third option. Down-migrations do not exist here by design (A23).
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
	ls -1t "$BACKUPS"/articleflux-*.db 2>/dev/null | head -5 | sed 's/^/  /' || echo "  (none)"
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
	# The live directory is moved ASIDE rather than deleted, and only removed
	# once the replacement is in place — the same order update.sh uses for the
	# same swap.
	#
	# This read `rm -rf web` immediately before the `mv`. A directory cannot be
	# replaced atomically, so some window is unavoidable, but deleting first
	# makes it as wide as the tree is large and leaves nothing to fall back on:
	# an `rm -rf` that fails partway aborts here under `set -e` with a
	# half-deleted web directory, and the `mv` that would have repaired it never
	# runs. That is the state you least want to reach on the script you are
	# running BECAUSE something already went wrong.
	rm -rf "$REPO/bin/web.rollback" "$REPO/bin/web.old"
	cp -a "$WEB" "$REPO/bin/web.rollback"
	[ -d "$REPO/bin/web" ] && mv "$REPO/bin/web" "$REPO/bin/web.old"
	mv "$REPO/bin/web.rollback" "$REPO/bin/web"
	rm -rf "$REPO/bin/web.old"
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
