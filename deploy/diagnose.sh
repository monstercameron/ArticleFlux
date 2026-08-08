#!/usr/bin/env bash
# ArticleFlux — what is this box doing, and what is wrong with it.
#
#   sudo /opt/ArticleFlux/deploy/diagnose.sh          # checks, in words
#   sudo /opt/ArticleFlux/deploy/diagnose.sh --json   # the same state, for an agent
#   sudo /opt/ArticleFlux/deploy/diagnose.sh --agent  # a ready-made handoff prompt
#
# Exits non-zero when a check fails, so it can be the thing a monitor calls.
#
# The failure reports written by install.sh and update.sh describe a moment that
# has passed. This describes now, and it exists because the first question asked
# of a sick server is always the same list — is the process up, is it answering,
# is nginx passing WebSockets, is the disk full, what did the log say — and
# typing that list from memory at 2am is how steps get skipped.
set -uo pipefail

# The checkout, FOUND rather than assumed.
#
# install.sh puts it at /opt/ArticleFlux and deploy/README.md's by-hand
# instructions put it at /opt/src/ArticleFlux. A default that names only one of
# those is wrong on the other kind of box, and the symptom is this script
# reporting "GoWebComponents checkout: FAIL" and a blank deployed-commit line
# about a perfectly healthy install — a diagnostic inventing the fault it was
# run to find, which is the same failure the ORIGIN guess below already caused
# once.
if [ -z "${ARTICLEFLUX_REPO:-}" ]; then
	for candidate in /opt/ArticleFlux /opt/src/ArticleFlux /opt/articleflux; do
		if [ -d "$candidate/.git" ]; then ARTICLEFLUX_REPO="$candidate"; break; fi
	done
fi
REPO="${ARTICLEFLUX_REPO:-/opt/ArticleFlux}"
if [ -z "${GWC_REPO:-}" ]; then
	for candidate in /opt/GoWebComponents /opt/src/GoWebComponents; do
		if [ -d "$candidate/.git" ]; then GWC_REPO="$candidate"; break; fi
	done
fi
GWC="${GWC_REPO:-/opt/GoWebComponents}"
HEALTH="${ARTICLEFLUX_HEALTH_URL:-http://127.0.0.1:9000/healthz}"
# Readiness, which is a different question: /healthz says the process answers,
# /readyz says it can still read AND write. See internal/app/diskhealth.go.
READY="${ARTICLEFLUX_READY_URL:-$(printf '%s' "$HEALTH" | sed 's|/healthz$|/readyz|')}"
# The app's OWN allowlist, read off the running unit rather than guessed.
#
# The server compares a browser's Origin against the `-origin` it was started
# with, so any other value here tests a request no browser will ever send. This
# used to guess `http://<box-ip>`, and from the day the site moved to
# https://feed.earlcameron.com it reported "WebSocket upgrade through nginx: 403"
# on a box whose WebSockets were entirely fine — a diagnostic inventing the
# outage it was run to find, in the one script somebody reads at 2am.
#
# The IP guess stays as the last resort, for a box where the unit is not
# installed yet and there is nothing better to ask.
unit_origin() {
	systemctl show -p ExecStart --value articleflux 2>/dev/null |
		awk '{for (i = 1; i < NF; i++) if ($i == "-origin") { print $(i + 1); exit }}'
}
ORIGIN="${ARTICLEFLUX_ORIGIN:-$(unit_origin)}"
ORIGIN="${ORIGIN:-http://$(hostname -I | awk '{print $1}')}"
SCRIPT_ARGS="$*"

MODE=human
case "${1:-}" in
	--json) MODE=json ;;
	--agent) MODE=agent ;;
	-h|--help) sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
esac

# Sourced for json_str/write_report — and then comprehensively disarmed. lib.sh
# exists to turn a failing command into a reported failure, which is the exact
# opposite of what this script does: a failing check here IS the output. With
# the traps left armed the first red check aborted the run and reported itself
# as a crash, so the checks that mattered most never ran.
LOG="${LOG:-/tmp/articleflux-diagnose.$$.log}"
. "$(cd "$(dirname "$0")" && pwd)/lib.sh"
trap - EXIT INT TERM ERR
set +eE

if [ "$MODE" = json ] || [ "$MODE" = agent ]; then
	REPORT_DIR=$(mktemp -d)
	REPORT="$REPORT_DIR/state.json"
	STEP_NAME='(diagnose, no failure)'
	journalctl -u articleflux -n 60 --no-pager -o short-iso > "$LOG" 2>/dev/null || true
	write_report 0 0
	if [ "$MODE" = json ]; then
		cat "$REPORT"
	else
		cat <<EOF
Fix this ArticleFlux server. It is a self-hosted Go + WebAssembly feed reader:
a single binary behind nginx, SQLite at /var/lib/articleflux, systemd unit
"articleflux" with Restart=always and a health watchdog timer. The checkout is
at $REPO and its sibling library at $GWC (go.mod uses a replace directive, so
both must be present to build).

Useful commands: journalctl -u articleflux -n 100 --no-pager
                 systemctl status articleflux --no-pager
                 sudo $REPO/deploy/update.sh --verbose
                 sudo $REPO/deploy/rollback.sh --list

Current state follows as JSON. Any previous deploy failure is at
/var/log/articleflux/last-failure.json.

$(cat "$REPORT")
EOF
	fi
	rm -rf "$REPORT_DIR" "$LOG"
	exit 0
fi

# --- human ------------------------------------------------------------------
fails=0
check() { # check <label> <ok?> <detail>
	if [ "$2" = "0" ]; then
		printf '  %s✓%s %-34s %s%s%s\n' "$C_GRN" "$C_OFF" "$1" "$C_DIM" "${3:-}" "$C_OFF"
	else
		printf '  %s✗%s %-34s %s%s%s\n' "$C_RED" "$C_OFF" "$1" "$C_YEL" "${3:-}" "$C_OFF"
		fails=$((fails + 1))
	fi
}

printf '\n%s%sArticleFlux%s on %s %s%s%s\n\n' "$C_BLD" "$C_CYN" "$C_OFF" "$(hostname)" "$C_DIM" "$(date -Is)" "$C_OFF"

state=$(systemctl is-active articleflux 2>/dev/null)
[ "$state" = active ]; check "service running" $? "$state, $(systemctl show -p NRestarts --value articleflux 2>/dev/null) restarts, up $(systemctl show -p ActiveEnterTimestamp --value articleflux 2>/dev/null | cut -c1-19)"

[ "$(systemctl is-enabled articleflux 2>/dev/null)" = enabled ]; check "starts at boot" $? "$(systemctl show -p Restart --value articleflux 2>/dev/null) on exit"

[ "$(systemctl is-active articleflux-health.timer 2>/dev/null)" = active ]; check "health watchdog armed" $? "$(systemctl list-timers articleflux-health.timer --no-pager 2>/dev/null | awk 'NR==2{print "next in "$3" "$4}')"

code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$HEALTH" 2>/dev/null)
[ "$code" = 200 ]; check "server answers /healthz" $? "HTTP $code on $HEALTH"

# The one that catches a full disk. /healthz deliberately does not touch the
# database, so it stays green through the read-works-write-fails state this box
# is most likely to reach; /readyz probes the data directory and refuses.
rcode=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$READY" 2>/dev/null)
[ "$rcode" = 200 ]
check "server answers /readyz" $? "HTTP $rcode on $READY$([ "$rcode" = 503 ] && printf '%s' ' — reads work, writes may not; check the data directory below')"

if systemctl is-active --quiet nginx; then
	# -L, because the canonical redirect to https:// IS the correct answer to a
	# plaintext request on :80 and following it is what a browser does. Without it
	# this reported "HTTP 301" as a failure from the moment TLS was configured.
	code=$(curl -s -L -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1/ 2>/dev/null)
	final=$(curl -s -L -o /dev/null -w '%{url_effective}' --max-time 10 http://127.0.0.1/ 2>/dev/null)
	[ "$code" = 200 ]; check "nginx serves the client" $? "HTTP $code at ${final:-:80}"

	# The upgrade, tested rather than assumed. Every RPC rides this one socket,
	# so a proxy that answers 200 for / and drops Upgrade produces a page that
	# loads perfectly and does nothing at all — the hardest failure to see.
	ws=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 \
		-H 'Connection: Upgrade' -H 'Upgrade: websocket' \
		-H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
		-H "Origin: $ORIGIN" http://127.0.0.1/grpc 2>/dev/null)
	[ "$ws" = 101 ]; check "WebSocket upgrade through nginx" $? "HTTP $ws (want 101) with Origin: $ORIGIN"
	nginx -t >/dev/null 2>&1; check "nginx config valid" $? "$(nginx -v 2>&1)"
else
	check "nginx running" 1 "inactive — nothing is serving :80"
fi

# Two disks, two questions, and only one of them was being asked.
#
# The check here was `df` on $REPO against a build-sized threshold, which
# answers "can I compile" — a question that matters during a deploy and not at
# 2am when the reader is misbehaving. The question that matters then is "can the
# database WRITE", and on a box where /opt and /var are separate filesystems the
# first answer says nothing about the second. Even where they are the same
# volume the thresholds differ by an order of magnitude: a build wants a
# gigabyte and a database wants enough for a WAL checkpoint.
#
# Both are reported, and the data directory's is the one that fails a run.
data_dir=$(dirname "${ARTICLEFLUX_DB:-/var/lib/articleflux/articleflux.db}")
data_free_mb=$(df -Pm "$data_dir" 2>/dev/null | awk 'NR==2{print $4}')
data_free_mb="${data_free_mb:-0}"
[ "$data_free_mb" -gt 256 ]
check "data directory has room" $? "${data_free_mb}MB free at $data_dir (writes start failing near zero; /readyz refuses below 256MB)"

# What is taking it, when it is going. The five caches share this volume with
# the database and had no total ceiling until the server started sweeping them
# — so a data directory filling up has one likely culprit and it is worth naming
# rather than making somebody go and find it.
if [ -d "$data_dir" ]; then
	cache_mb=$(du -sm "$data_dir"/*-cache 2>/dev/null | awk '{s+=$1} END{print s+0}')
	db_mb=$(du -sm "${ARTICLEFLUX_DB:-/var/lib/articleflux/articleflux.db}" 2>/dev/null | cut -f1)
	printf '%s  data      %sMB of caches, %sMB of database%s\n' \
		"$C_DIM" "${cache_mb:-0}" "${db_mb:-?}" "$C_OFF"
fi

# And the build disk, which is a different filesystem on some layouts.
free_mb=$(df -Pm "$REPO" 2>/dev/null | awk 'NR==2{print $4}')
free_mb="${free_mb:-0}"
[ "$free_mb" -gt 500 ]; check "build disk has room" $? "${free_mb}MB free at $REPO (a build needs ~1500MB)"

avail_mb=$(free -m | awk 'NR==2{print $7}')
[ "$avail_mb" -gt 150 ]; check "memory available" $? "${avail_mb}MB available, $(free -m | awk 'NR==3{print $3}')MB swap used"

db="${ARTICLEFLUX_DB:-/var/lib/articleflux/articleflux.db}"
[ -f "$db" ]; check "database present" $? "$([ -f "$db" ] && du -h "$db" | cut -f1) at $db"

[ -d "$GWC/.git" ]; check "GoWebComponents checkout" $? "$(git -C "$GWC" log -1 --format='%h %s' 2>/dev/null | cut -c1-40)"

printf '\n%s  deployed  %s%s\n' "$C_DIM" "$(git -C "$REPO" log -1 --format='%h %s' 2>/dev/null | cut -c1-58)" "$C_OFF"
printf '%s  built     %s%s\n' "$C_DIM" "$(date -r "$REPO/bin/articleflux" '+%Y-%m-%d %H:%M' 2>/dev/null)" "$C_OFF"

errs=$(journalctl -u articleflux --since '1 hour ago' -p err --no-pager -o cat 2>/dev/null | wc -l)
if [ "$errs" -gt 0 ]; then
	printf '\n%s  %s error line(s) in the last hour:%s\n' "$C_YEL" "$errs" "$C_OFF"
	journalctl -u articleflux --since '1 hour ago' -p err --no-pager -o cat 2>/dev/null | tail -5 | sed 's/^/    /'
fi

if [ -f /var/log/articleflux/last-failure.json ]; then
	printf '\n%s  a previous deploy failed: /var/log/articleflux/last-failure.json (%s)%s\n' \
		"$C_YEL" "$(date -r /var/log/articleflux/last-failure.json '+%Y-%m-%d %H:%M')" "$C_OFF"
fi

if [ "$fails" -gt 0 ]; then
	printf '\n%s%s%s check(s) failed.%s Hand the state to an agent with:\n' "$C_RED" "$C_BLD" "$fails" "$C_OFF"
	printf '  %ssudo %s --agent%s\n\n' "$C_DIM" "$0" "$C_OFF"
	exit 1
fi
printf '\n%s%sAll checks passed.%s\n\n' "$C_GRN" "$C_BLD" "$C_OFF"
