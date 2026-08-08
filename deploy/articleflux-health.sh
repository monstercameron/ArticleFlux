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
# Readiness is a DIFFERENT question and is checked separately below: /healthz
# says the process is answering, /readyz says it can still read AND write. A
# full disk leaves the first green forever.
READY_URL="${ARTICLEFLUX_READY_URL:-$(printf '%s' "$URL" | sed 's|/healthz$|/readyz|')}"
UNIT=articleflux
REPORT_DIR="${REPORT_DIR:-/var/log/articleflux}"
ALERT="${ALERT:-/usr/local/bin/articleflux-alert}"

# Where the checkout is, found rather than assumed.
#
# This was hardcoded to /opt/ArticleFlux, which is where install.sh puts it —
# and deploy/README.md's by-hand instructions put it at /opt/src/ArticleFlux
# instead. On a README-installed box the path did not resolve, the `[ -x ]`
# guard below quietly skipped the pre-restart snapshot, and the one moment the
# evidence existed passed in silence. Silently, because a missing diagnostic is
# indistinguishable from a diagnostic that found nothing.
#
# Candidates in the order they are likely, and a loud line when none of them is
# there — see the note at the snapshot itself.
if [ -z "${DIAGNOSE:-}" ]; then
	# ARTICLEFLUX_REPO first when the unit sets it, then the two layouts this
	# repository actually documents, then the install prefix. The empty-REPO case
	# would probe "/deploy/diagnose.sh" and is skipped rather than tested.
	for candidate in "${ARTICLEFLUX_REPO:-}/deploy/diagnose.sh" /opt/ArticleFlux/deploy/diagnose.sh /opt/src/ArticleFlux/deploy/diagnose.sh /opt/articleflux/deploy/diagnose.sh; do
		# An `if`, not `[ … ] && continue`: `set -e` is on, and an AND-list whose
		# test is false exits the script rather than skipping the iteration.
		if [ "$candidate" = "/deploy/diagnose.sh" ]; then continue; fi
		if [ -x "$candidate" ]; then DIAGNOSE="$candidate"; break; fi
	done
fi
DIAGNOSE="${DIAGNOSE:-}"

# alert leaves the box, if anything is configured to. Never fatal: see the
# script's own header for why it always exits 0.
alert() {
	if [ -x "$ALERT" ]; then
		"$ALERT" "$1" "${2:-}" || true
	fi
}

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
	if [ -n "$DIAGNOSE" ] && [ -x "$DIAGNOSE" ]; then
		mkdir -p "$REPORT_DIR"
		"$DIAGNOSE" --json > "$REPORT_DIR/last-watchdog-restart.json" 2>/dev/null || true
		cp -f "$REPORT_DIR/last-watchdog-restart.json" 			"$REPORT_DIR/watchdog-$(date +%Y%m%d-%H%M%S).json" 2>/dev/null || true
		ls -1t "$REPORT_DIR"/watchdog-*.json 2>/dev/null | tail -n +11 | xargs -r rm -f
		logger -t articleflux-health "state captured to $REPORT_DIR/last-watchdog-restart.json"
	else
		# Said out loud. The old version of this branch was a silent skip, which
		# is how a box can go a year taking watchdog restarts and capturing
		# nothing while the log implies otherwise.
		logger -t articleflux-health "NO STATE CAPTURED: diagnose.sh not found (set DIAGNOSE= or ARTICLEFLUX_REPO= in the unit)"
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
		# THE line this whole alerting path exists for. A watchdog that restarts
		# into the same wedge every two minutes forever, silently, converts a
		# hard failure somebody would notice into a soft one nobody does — and
		# that sentence was in this script for months while the only place it
		# went was journald.
		alert "ArticleFlux: RESTART DID NOT HELP" 			"$UNIT was restarted by the watchdog and is still not answering $URL.
Reason: $reason
State captured: $REPORT_DIR/last-watchdog-restart.json

--- last 20 log lines ---
$(journalctl -u "$UNIT" --no-pager --lines=20 -o cat 2>/dev/null)"
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
	# Answering, but is it WELL? /healthz says the process is up; /readyz says
	# it can read AND write. The gap between them is the failure this box is
	# actually engineered towards: the caches used to grow without a ceiling
	# into the volume the database is on, and SQLite meeting a full disk returns
	# SQLITE_FULL on every write while every read keeps working. The reader
	# loads, articles appear, and nothing anybody does is remembered.
	#
	# ALERTED, NOT RESTARTED, and that distinction is the whole point of doing
	# it here rather than making /readyz the probe above. A restart does not
	# create disk space. A watchdog that restarted on this would take a
	# degraded-but-usable instance and cycle it every two minutes forever, which
	# is worse than the condition — and it would drop every reader's tunnel each
	# time, over something only a human can fix.
	#
	# Two consecutive failures, like the liveness probe, for the same reason: a
	# readiness check that touches the database can lose one sample to a slow
	# query without that meaning anything.
	if ! curl -fsS --max-time 10 -o /dev/null "$READY_URL" 2>/dev/null; then
		sleep 5
		if ! curl -fsS --max-time 10 -o /dev/null "$READY_URL" 2>/dev/null; then
			logger -t articleflux-health "NOT READY: $UNIT answers $URL but $READY_URL says unready — reads work, writes may not"
			alert "ArticleFlux: serving but NOT READY on $(hostname 2>/dev/null)" \
				"$UNIT is answering $URL but $READY_URL reports unready.

This is the read-works-write-fails shape: most often a full or read-only data
directory. A restart will NOT fix it and the watchdog deliberately has not tried.

$(df -h /var/lib/articleflux 2>/dev/null)

--- last 20 log lines ---
$(journalctl -u $UNIT --no-pager --lines=20 -o cat 2>/dev/null)"
		fi
	fi
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
