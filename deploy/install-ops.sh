#!/bin/sh
# install-ops.sh — install the ops layer (backup, restore drill, watchdog, alerting) as ONE
# unit of work, and refuse to report success until every piece of it can actually run.
#
#   sudo sh deploy/install-ops.sh                               # systemd runtime
#   sudo sh deploy/install-ops.sh --runtime docker --keep 3     # the container (deploy/docker)
#
# Options (defaults in brackets):
#   --runtime systemd|docker   what serves the reader [systemd]
#   --bin PATH                 the articleflux the backup and drill run
#                              [systemd: /opt/articleflux/bin/articleflux;
#                               docker:  /usr/local/bin/articleflux-ctr]
#   --data DIR                 the data directory [/var/lib/articleflux]
#   --backups DIR              where backups go [/var/backups/articleflux]
#   --repo DIR                 the checkout, for the watchdog's diagnose.sh [this one]
#   --keep N                   nightly backups retained [the unit's own, 14]
#   --user NAME                the service account [articleflux]
#   --compose FILE             docker only: the compose file articleflux-ctr reads
#                              [/opt/articleflux/compose.yaml]
#
# # Why this exists
#
# Each unit's header used to carry its own `install` line, and the README's backup step
# installed the backup unit and its timer — not the script its ExecStart names, not the
# alert template its OnFailure= names, and with ARTICLEFLUX_BIN still at this file's
# example path. Done by hand on the production box, that is exactly what happened: the
# unit was enabled, ran nightly, exited 203/EXEC because /usr/local/bin/articleflux-backup
# did not exist, and its OnFailure= started a template that did not exist either — so the
# one job whose failure is invisible until it is needed failed invisibly, for three months.
#
# Nothing in systemd catches that at install time: a unit may name an ExecStart that is
# not there and an OnFailure= unit that is not there, and `enable` succeeds. So the
# checks are here, and the script's exit status is the only verdict: it either installs
# everything and proves the binary the backup will run actually runs, or it says what is
# missing and exits non-zero. deploy/install.sh (a fresh box) and deploy/update.sh (every
# deploy) CALL this rather than carrying a copy of it — two copies is how the installed
# units and the repo's had drifted apart — and it is also the piece that can be re-run on
# its own: after a hand install, after a move to the container, after a move back.
set -eu

die() { printf 'install-ops: ERROR: %s\n' "$1" >&2; exit 1; }
say() { printf 'install-ops: %s\n' "$1"; }

[ "$(id -u)" = 0 ] || die "run as root (sudo sh $0 ...)"

HERE=$(cd "$(dirname "$0")" && pwd)
RUNTIME=systemd
BIN=""
DATA=/var/lib/articleflux
BACKUPS=/var/backups/articleflux
REPO=$(dirname "$HERE")
KEEP=""
OWNER=articleflux
COMPOSE=/opt/articleflux/compose.yaml

while [ $# -gt 0 ]; do
	case "$1" in
	--runtime) RUNTIME="${2:?--runtime needs systemd or docker}"; shift 2 ;;
	--bin) BIN="${2:?--bin needs a path}"; shift 2 ;;
	--data) DATA="${2:?--data needs a directory}"; shift 2 ;;
	--backups) BACKUPS="${2:?--backups needs a directory}"; shift 2 ;;
	--repo) REPO="${2:?--repo needs a directory}"; shift 2 ;;
	--keep) KEEP="${2:?--keep needs a number}"; shift 2 ;;
	--user) OWNER="${2:?--user needs a name}"; shift 2 ;;
	--compose) COMPOSE="${2:?--compose needs a file}"; shift 2 ;;
	-h | --help) sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) die "unknown option: $1 (try --help)" ;;
	esac
done

case "$RUNTIME" in
systemd) BIN="${BIN:-/opt/articleflux/bin/articleflux}" ;;
docker) BIN="${BIN:-/usr/local/bin/articleflux-ctr}" ;;
*) die "--runtime must be systemd or docker, not $RUNTIME" ;;
esac
case "$KEEP" in
"" | *[!0-9]*) [ -z "$KEEP" ] || die "--keep must be a whole number" ;;
esac
id "$OWNER" >/dev/null 2>&1 || die "no user $OWNER on this box"

UNITDIR=/etc/systemd/system
# A re-run without --keep keeps the retention the last run chose. update.sh calls this on
# every deploy and does not know the number, and the drop-in below is rewritten each time:
# without this, one update after `--keep 3` put the box back on the unit's 14 — fourteen
# copies of a 2.7 GB database against 18 GB free.
if [ -z "$KEEP" ] && [ -f "$UNITDIR/articleflux-backup.service.d/install-ops.conf" ]; then
	KEEP=$(sed -n 's/^Environment=ARTICLEFLUX_KEEP=\([0-9][0-9]*\)$/\1/p' "$UNITDIR/articleflux-backup.service.d/install-ops.conf" | head -n 1)
fi
SCRIPTS="articleflux-health articleflux-alert articleflux-backup articleflux-restore-drill"
UNITS="articleflux-health.service articleflux-health.timer articleflux-backup.service articleflux-backup.timer articleflux-restore-drill.service articleflux-restore-drill.timer articleflux-alert@.service"

# --- 1. scripts ------------------------------------------------------------------------
for s in $SCRIPTS; do
	[ -f "$HERE/$s.sh" ] || die "$HERE/$s.sh is missing — is this a complete checkout?"
	install -m 0755 "$HERE/$s.sh" "/usr/local/bin/$s"
done
if [ "$RUNTIME" = docker ]; then
	install -m 0755 "$HERE/docker/articleflux-ctr" /usr/local/bin/articleflux-ctr
fi
say "scripts installed in /usr/local/bin"

# --- 2. units, with this box's paths -----------------------------------------------------
# The files in deploy/ carry the by-hand layout's paths. ARTICLEFLUX_BIN is rewritten as
# a whole line rather than by prefix, because it is the one value that differs by
# runtime and not just by layout.
for u in $UNITS; do
	[ -f "$HERE/$u" ] || die "$HERE/$u is missing — is this a complete checkout?"
	sed -e "s#^Environment=ARTICLEFLUX_BIN=.*#Environment=ARTICLEFLUX_BIN=$BIN#" \
		-e "s#/var/lib/articleflux#$DATA#g" \
		-e "s#/var/backups/articleflux#$BACKUPS#g" \
		"$HERE/$u" >"$UNITDIR/$u.tmp"
	mv "$UNITDIR/$u.tmp" "$UNITDIR/$u"
	chmod 0644 "$UNITDIR/$u"
done
say "units installed in $UNITDIR"

# --- 3. drop-ins: what differs by runtime ------------------------------------------------
# One file per unit, named for this script, rewritten on every run — so switching runtime
# is re-running this with the other --runtime, and nothing from the previous one lingers.
dropin() { # unit, then the body on stdin
	mkdir -p "$UNITDIR/$1.d"
	cat >"$UNITDIR/$1.d/install-ops.conf"
}

{
	echo "# Written by deploy/install-ops.sh --runtime $RUNTIME. Re-run it rather than editing."
	echo "[Service]"
	echo "Environment=ARTICLEFLUX_RUNTIME=$RUNTIME"
	echo "Environment=ARTICLEFLUX_REPO=$REPO"
} | dropin articleflux-health.service

RUN_AS=""
if [ "$RUNTIME" = docker ]; then
	[ -f "$COMPOSE" ] || die "$COMPOSE does not exist — install the compose file first (deploy/docker/CUTOVER.md)"
	RUN_AS=$(grep -oE 'user:[[:space:]]*"[^"]+"' "$COMPOSE" | head -n1 | sed 's/.*"\(.*\)"/\1/')
	[ -n "$RUN_AS" ] || die "$COMPOSE has no quoted user: line; the backup container would run as the image's nonroot user and could not read a bind-mounted database"
fi

for u in articleflux-backup.service articleflux-restore-drill.service; do
	{
		echo "# Written by deploy/install-ops.sh --runtime $RUNTIME. Re-run it rather than editing."
		echo "[Service]"
		# Also set here, not only by the sed above. That rewrite is a no-op if the unit's
		# own ARTICLEFLUX_BIN line is ever renamed or removed, and a drop-in cannot be
		# clobbered by something copying the template over the unit — so the binary the
		# backup runs is named in the one place nothing else edits.
		echo "Environment=ARTICLEFLUX_BIN=$BIN"
		[ -z "$KEEP" ] || echo "Environment=ARTICLEFLUX_KEEP=$KEEP"
		if [ "$RUNTIME" = docker ]; then
			# The binary runs in a container started by the docker CLI, so the file I/O is
			# the DAEMON's, outside this unit's sandbox — the sandbox stops protecting the
			# data and starts breaking the CLI. Root, because talking to the docker socket
			# is root-equivalent and pretending otherwise by adding the service account to
			# the docker group would only make that account root-equivalent too. No private
			# /tmp, because articleflux-ctr mounts the host's /tmp into the container and
			# the drill's work directory has to be on that side. The container itself still
			# runs as $RUN_AS, which is what keeps every file it writes owned correctly.
			echo "User=root"
			echo "Group=root"
			echo "PrivateTmp=no"
			echo "ProtectSystem=full"
			echo "ProtectHome=read-only"
			echo "Environment=AF_COMPOSE_FILE=$COMPOSE"
			echo "Environment=ARTICLEFLUX_RUN_AS=$RUN_AS"
		fi
	} | dropin "$u"
done

systemctl daemon-reload

# --- 4. prove it -------------------------------------------------------------------------
# Every check below is a failure that has happened or that `systemctl enable` would have
# accepted without a word.
problems=0
miss() { printf 'install-ops: MISSING: %s\n' "$1" >&2; problems=$((problems + 1)); }

for s in $SCRIPTS; do
	[ -x "/usr/local/bin/$s" ] || miss "/usr/local/bin/$s"
done
# Every OnFailure= in every unit names the alert template; it has to exist.
[ -f "$UNITDIR/articleflux-alert@.service" ] || miss "$UNITDIR/articleflux-alert@.service (every OnFailure= names it)"
if [ -f "$UNITDIR/articleflux.service" ] && ! grep -q '^OnFailure=articleflux-alert@' "$UNITDIR/articleflux.service"; then
	say "note: $UNITDIR/articleflux.service has no OnFailure= alert line"
fi

[ -d "$BACKUPS" ] || install -d -o "$OWNER" -g "$OWNER" -m 0700 "$BACKUPS"
[ -d "$DATA" ] || miss "$DATA (the data directory)"

# The binary the backup will run, run the way the backup will run it. `version` opens
# nothing, so this is safe against a live database; what it proves is that the path
# exists, is executable, and — for docker — that the image in the compose file can be
# started at all (pulled, or pullable without a login).
if [ ! -x "$BIN" ]; then
	miss "$BIN (ARTICLEFLUX_BIN for the backup and the drill)"
elif [ "$RUNTIME" = docker ]; then
	command -v docker >/dev/null 2>&1 || miss "the docker CLI"
	if ! out=$(AF_COMPOSE_FILE="$COMPOSE" "$BIN" version 2>&1); then
		miss "a runnable image via $BIN: $out"
	else
		say "backup will run: $out"
	fi
else
	if ! out=$(runuser -u "$OWNER" -- "$BIN" version 2>&1); then
		miss "a binary $OWNER can run at $BIN: $out"
	else
		say "backup will run: $out"
	fi
fi

for u in $UNITS; do
	case "$u" in *@.service) continue ;; esac
	if ! out=$(systemd-analyze verify "$UNITDIR/$u" 2>&1); then
		printf 'install-ops: systemd-analyze verify %s:\n%s\n' "$u" "$out" >&2
		problems=$((problems + 1))
	fi
done

[ "$problems" -eq 0 ] || die "$problems problem(s) above; nothing was enabled"

systemctl enable --now articleflux-health.timer articleflux-backup.timer articleflux-restore-drill.timer
say "enabled: health every 2 min, backup nightly, restore drill weekly ($RUNTIME runtime)"
say "prove the backup now rather than at 03:30:  systemctl start articleflux-backup && journalctl -u articleflux-backup -n 20 --no-pager"
