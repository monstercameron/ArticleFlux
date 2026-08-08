#!/bin/sh
# ArticleFlux — prove the newest backup actually restores.
#
# Run weekly by articleflux-restore-drill.timer. Reports through the same alert
# path as everything else.
#
# # Why a drill and not just an integrity check
#
# `articleflux backup` already runs `PRAGMA integrity_check` on the copy it
# writes, so the file is known to be a well-formed SQLite database. That is a
# smaller claim than it sounds. A backup can be structurally perfect and still
# not restore: the binary may have moved forward past a migration the copy
# predates, the keys may not have been copied beside it, the file may have been
# truncated by a full disk after the check, or the thing being restored to may
# have a schema newer than the archive (store.Migrate refuses that now, which is
# a REFUSAL somebody should meet in a drill rather than in an emergency).
#
# An untested backup is a belief. This turns it into a fact once a week, which
# is the cadence deploy/README.md's restore instructions describe and nothing
# was performing.
#
# # Why it restores into a temp directory and never touches the live database
#
# The one way to make a restore drill dangerous is to let it write anywhere near
# production. Everything here happens under a mktemp directory that is removed
# on every exit path, and the live DB path is only ever READ — never opened for
# writing, never passed to a command that migrates.
set -eu

OUT="${ARTICLEFLUX_BACKUPS:-/var/backups/articleflux}"
BIN="${ARTICLEFLUX_BIN:-/opt/articleflux/bin/articleflux}"
ALERT="${ALERT:-/usr/local/bin/articleflux-alert}"

say() { logger -t articleflux-restore-drill "$*"; }

fail() {
	say "DRILL FAILED: $*"
	# Non-zero, so the unit fails and its OnFailure= sends the alert. A backup
	# that does not restore is the single most important thing on this box to
	# find out about while it is still hypothetical.
	exit 1
}

[ -x "$BIN" ] || fail "no articleflux binary at $BIN"

newest=$(ls -1t "$OUT"/articleflux-*.db 2>/dev/null | head -1)
[ -n "$newest" ] || fail "there are no backups in $OUT to drill against"

age_days=$(( ( $(date +%s) - $(date -r "$newest" +%s) ) / 86400 ))
if [ "$age_days" -gt 2 ]; then
	# The nightly timer is Persistent=true, so a box that was down still runs
	# it on the way back. A newest backup older than two days means the job has
	# not been succeeding, which the drill would otherwise mask by cheerfully
	# restoring a stale copy.
	fail "the newest backup in $OUT is $age_days days old; the nightly job is not running"
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM

cp "$newest" "$work/articleflux.db" || fail "could not copy $newest into the drill directory"
# The keys travel with it, because a restore without secrets.key is a server
# that refuses to start — and finding that out here is the entire point.
for k in secrets.key proxy.key speech.key recovery-codes.key; do
	[ -f "$OUT/$k" ] && cp "$OUT/$k" "$work/"
done
[ -f "$work/secrets.key" ] || say "NOTE: no secrets.key beside the backup. If this instance sets ARTICLEFLUX_SECRET_KEY that is correct; otherwise a restore cannot read any stored credential"

# Open it the way a real restore does: migrate to this binary's schema. That is
# the step that catches a backup the current build can no longer read, in both
# directions — an old archive missing migrations, and one written by a newer
# build than this (store.Migrate refuses the second, loudly, which is a pass for
# the drill's purposes only if somebody reads the message).
if ! out=$("$BIN" migrate -db "$work/articleflux.db" 2>&1); then
	fail "the newest backup does not migrate cleanly: $out"
fi

# And that there is something IN it. A zero-row database migrates perfectly.
if ! rows=$("$BIN" audit -db "$work/articleflux.db" -n 1 2>&1); then
	fail "the restored copy migrated but could not be read: $rows"
fi

say "drill passed: $(basename "$newest") restores and opens ($(du -h "$newest" | cut -f1), ${age_days}d old)"
exit 0
