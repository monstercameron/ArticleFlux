#!/bin/sh
# ArticleFlux health check and self-recovery.
#
# Installed at /usr/local/bin/articleflux-health and run by
# articleflux-health.timer every two minutes. Three states, three answers:
#
#   answering            -> say nothing, exit 0
#   running, wedged      -> restart it
#   failed (gave up)     -> clear the failed state and start it
#   stopped by an operator -> leave it alone, and say so
#
# The third is the one that matters most. articleflux.service gives up after
# ten failures in five minutes so a broken binary does not restart forever, and
# a unit that has given up stays given up. On a box nobody logs into, that is
# the difference between an outage measured in minutes and one measured in
# however long it takes somebody to notice their reader is gone.
set -eu

URL="${ARTICLEFLUX_HEALTH_URL:-http://127.0.0.1:9000/healthz}"
UNIT=articleflux
REPORT_DIR="${REPORT_DIR:-/var/log/articleflux}"
DIAGNOSE="${DIAGNOSE:-/opt/ArticleFlux/deploy/diagnose.sh}"

probe() {
	# --max-time, not just --connect-timeout: a wedged server accepts the
	# connection and then never writes, which is precisely the failure this
	# exists to catch and precisely the one a connect timeout misses.
	curl -fsS --max-time 10 -o /dev/null "$URL"
}

recover() {
	reason="$1"
	logger -t articleflux-health "restarting $UNIT: $reason"
	# reset-failed first, unconditionally. If the unit tripped its start limit,
	# `restart` alone is refused with "start request repeated too quickly" and
	# this script would report success while changing nothing.
	systemctl reset-failed "$UNIT" 2>/dev/null || true

	# Snapshot BEFORE the restart. A restart is also an evidence-destroying
	# event: the wedged process is gone, its goroutine dump with it, and the
	# journal rolls on. Whatever made this necessary is only visible now.
	if [ -x "$DIAGNOSE" ]; then
		mkdir -p "$REPORT_DIR"
		"$DIAGNOSE" --json > "$REPORT_DIR/last-watchdog-restart.json" 2>/dev/null || true
		cp -f "$REPORT_DIR/last-watchdog-restart.json" 			"$REPORT_DIR/watchdog-$(date +%Y%m%d-%H%M%S).json" 2>/dev/null || true
		ls -1t "$REPORT_DIR"/watchdog-*.json 2>/dev/null | tail -n +11 | xargs -r rm -f
		logger -t articleflux-health "state captured to $REPORT_DIR/last-watchdog-restart.json"
	fi

	systemctl restart "$UNIT"

	# Say whether the cure worked. A watchdog that restarts a service into the
	# same wedge every two minutes forever, silently, is worse than no watchdog:
	# it converts a hard failure somebody would notice into a soft one nobody
	# does.
	sleep 10
	if curl -fsS --max-time 10 -o /dev/null "$URL" 2>/dev/null; then
		logger -t articleflux-health "restart succeeded — $UNIT is answering again"
	else
		logger -t articleflux-health "RESTART DID NOT HELP — $UNIT still not answering $URL; see $REPORT_DIR/last-watchdog-restart.json"
	fi
}

# Failed, and only failed. This is the crash-loop case: Restart=always means a
# process that dies is restarted, so the only way the unit reaches "failed" is
# by exhausting its start limit — exactly the state that never clears itself.
if systemctl is-failed --quiet "$UNIT"; then
	recover "unit is failed (start limit exhausted)"
	exit 0
fi

# Inactive is deliberate, and it is left alone. An operator who runs `systemctl
# stop articleflux` to take a backup or swap a binary has said what they want,
# and a watchdog that restarts the service thirty seconds later is not helping
# them — it is arguing with them, from cron, invisibly. The first version of this
# script did exactly that. `systemctl stop` means stopped; `systemctl start` is
# how it comes back.
if ! systemctl is-active --quiet "$UNIT"; then
	state=$(systemctl is-active "$UNIT" 2>/dev/null) || true
	logger -t articleflux-health "unit is ${state:-unknown} and not failed — stopped on purpose, leaving it alone"
	exit 0
fi

if probe; then
	exit 0
fi

# One failed probe is not evidence. The poller fetches publishers on the same
# process, and a restart costs every reader their open tunnel and their place in
# the article they were reading — so the bar is two failures thirty seconds
# apart, not one unlucky sample.
sleep 30
if probe; then
	logger -t articleflux-health "first probe failed, second succeeded — not restarting"
	exit 0
fi

recover "no answer from $URL after two probes"
