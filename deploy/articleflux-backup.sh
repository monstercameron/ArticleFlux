#!/bin/sh
# ArticleFlux — take the nightly backup, then get a copy off this machine.
#
# Run by articleflux-backup.service. Configured by /etc/articleflux/backup.env.
#
# # The gap this closes
#
# The backup itself was already correct: `articleflux backup` is VACUUM INTO
# plus an integrity check on the copy, not a file copy of a database in WAL
# mode. What it was not was OFF-BOX. Fourteen verified backups, the database
# they came from, and secrets.key all lived on the same DigitalOcean volume, so
# the single event this is all insurance against — losing the volume — took the
# database, every backup and the key that decrypts them in one stroke.
#
# A backup on the same disk as its source is a defence against `rm`, not against
# loss. This adds the other half.
#
# # Why the copy is encrypted before it leaves
#
# The backup directory holds secrets.key, proxy.key and speech.key alongside the
# database, because a restore without them produces a server that will not
# start. That is correct and it means the archive is, in one file, the whole
# instance: every stored credential, every mailbox password, and the Smart+ API
# key. Sending that to object storage in the clear would be handing a third
# party the instance.
#
# So encryption is REQUIRED for off-box shipping, and the absence of a recipient
# is a refusal rather than a downgrade. `age` is the tool because it is one
# static binary and one public key — the reason somebody skips encryption is
# that setting it up was a project, and this is not one.
#
# # Configuration — /etc/articleflux/backup.env, chmod 600, root
#
#   OFFSITE_AGE_RECIPIENT=age1ql3z...      # required for any shipping
#   OFFSITE_RCLONE_REMOTE=b2:my-bucket/af  # rclone destination, or
#   OFFSITE_RSYNC_TARGET=user@host:/path   # rsync-over-ssh destination
#   OFFSITE_KEEP=30                        # copies to keep remotely (rclone only)
#
# Nothing configured means "local only", which is where this started and is a
# legitimate choice for somebody whose droplet snapshots cover it. It is said in
# the log every night rather than assumed, because "I thought that was set up"
# is the specific belief this exists to stop.
set -eu

CONF="${ARTICLEFLUX_BACKUP_CONF:-/etc/articleflux/backup.env}"
# shellcheck source=/dev/null
[ -r "$CONF" ] && . "$CONF"

DB="${ARTICLEFLUX_DB:-/var/lib/articleflux/articleflux.db}"
OUT="${ARTICLEFLUX_BACKUPS:-/var/backups/articleflux}"
BIN="${ARTICLEFLUX_BIN:-/opt/articleflux/bin/articleflux}"
KEEP="${ARTICLEFLUX_KEEP:-14}"
ALERT="${ALERT:-/usr/local/bin/articleflux-alert}"

say() { logger -t articleflux-backup "$*"; }

fail() {
	say "FAILED: $*"
	# Exit non-zero so the unit reaches `failed` and its OnFailure= fires. The
	# alert is not sent from here for that reason: one notifier, wired once.
	exit 1
}

[ -x "$BIN" ] || fail "no articleflux binary at $BIN"

# --- 1. the backup itself, unchanged -----------------------------------------
mkdir -p "$OUT"
"$BIN" backup -db "$DB" -out "$OUT/" -keep "$KEEP" || fail "articleflux backup returned non-zero"

newest=$(ls -1t "$OUT"/articleflux-*.db 2>/dev/null | head -1)
[ -n "$newest" ] || fail "the backup reported success but wrote no articleflux-*.db into $OUT"
say "local backup ok: $newest ($(du -h "$newest" | cut -f1))"

# --- 2. off-box ---------------------------------------------------------------
if [ -z "${OFFSITE_RCLONE_REMOTE:-}${OFFSITE_RSYNC_TARGET:-}" ]; then
	say "LOCAL ONLY: no off-box target configured in $CONF — a volume loss takes the database and all $KEEP backups together"
	exit 0
fi

if [ -z "${OFFSITE_AGE_RECIPIENT:-}" ]; then
	fail "an off-box target is set but OFFSITE_AGE_RECIPIENT is not; this archive contains secrets.key and will not be sent unencrypted"
fi
command -v age >/dev/null 2>&1 || fail "age is not installed and OFFSITE_AGE_RECIPIENT is set; install age or clear the off-box target"

stamp=$(basename "$newest" .db)
work=$(mktemp -d)
# The tarball is removed whatever happens: it is a plaintext copy of the whole
# instance sitting in a temp directory, and leaving one behind after a failure
# would be a worse leak than the one encryption is preventing.
trap 'rm -rf "$work"' EXIT INT TERM

# The database AND the keys, because a restore without them produces a server
# that refuses to start — the same reason `articleflux backup` copies them into
# $OUT in the first place.
archive="$work/$stamp.tar"
tar -cf "$archive" -C "$OUT" "$(basename "$newest")" 2>/dev/null || fail "could not tar $newest"
for k in secrets.key proxy.key speech.key recovery-codes.key; do
	[ -f "$OUT/$k" ] && tar -rf "$archive" -C "$OUT" "$k"
done
gzip -f "$archive"
age -r "$OFFSITE_AGE_RECIPIENT" -o "$work/$stamp.tar.gz.age" "$archive.gz" ||
	fail "age refused the archive; check OFFSITE_AGE_RECIPIENT is a public key"
rm -f "$archive.gz"

sealed="$work/$stamp.tar.gz.age"

if [ -n "${OFFSITE_RCLONE_REMOTE:-}" ]; then
	command -v rclone >/dev/null 2>&1 || fail "OFFSITE_RCLONE_REMOTE is set but rclone is not installed"
	rclone copy --quiet "$sealed" "$OFFSITE_RCLONE_REMOTE" || fail "rclone copy to $OFFSITE_RCLONE_REMOTE"
	say "shipped $stamp.tar.gz.age to $OFFSITE_RCLONE_REMOTE"
	# Remote pruning is rclone-only: rsync over ssh would mean running `find
	# -delete` on somebody else's box, which is a lot of authority to take for
	# a housekeeping job.
	if [ -n "${OFFSITE_KEEP:-}" ]; then
		rclone delete --quiet --min-age "${OFFSITE_KEEP}d" "$OFFSITE_RCLONE_REMOTE" 2>/dev/null ||
			say "could not prune copies older than ${OFFSITE_KEEP}d at $OFFSITE_RCLONE_REMOTE"
	fi
fi

if [ -n "${OFFSITE_RSYNC_TARGET:-}" ]; then
	rsync -q --timeout=600 "$sealed" "$OFFSITE_RSYNC_TARGET" || fail "rsync to $OFFSITE_RSYNC_TARGET"
	say "shipped $stamp.tar.gz.age to $OFFSITE_RSYNC_TARGET"
fi

exit 0
