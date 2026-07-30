# Shared shell furniture for install.sh and update.sh.
# Sourced, never executed:  . "$(dirname "$0")/lib.sh"
#
# Everything here exists to answer one question at 2am: WHICH STEP BROKE, AND
# WHAT DID IT SAY. A deploy script that prints a wall of build output and then
# "Error: exit 1" has told you nothing you can act on, so every step announces
# itself, keeps its own output, and on failure prints its tail with the line
# number that ran it.

set -eu

# --- appearance --------------------------------------------------------------
# Colour only when a terminal is watching. Piped into a log or read back by
# journalctl, escape codes are noise that breaks grep.
if [ -t 1 ] && [ "${TERM:-dumb}" != "dumb" ] && [ -z "${NO_COLOR:-}" ]; then
	C_DIM=$(printf '\033[2m');   C_RED=$(printf '\033[31m')
	C_GRN=$(printf '\033[32m');  C_YEL=$(printf '\033[33m')
	C_CYN=$(printf '\033[36m');  C_BLD=$(printf '\033[1m')
	C_OFF=$(printf '\033[0m')
else
	C_DIM=''; C_RED=''; C_GRN=''; C_YEL=''; C_CYN=''; C_BLD=''; C_OFF=''
fi

STEP_N=0
STEP_TOTAL="${STEP_TOTAL:-0}"
STEP_NAME='(starting up)'
STEP_T0=0
SCRIPT_T0=$(date +%s)

# LOG is the full transcript: every command's stdout and stderr, unabridged,
# with the step banners interleaved so a line in the middle can be traced to the
# step that produced it.
LOG="${LOG:-/var/log/articleflux/$(basename "$0" .sh)-$(date +%Y%m%d-%H%M%S).log}"
mkdir -p "$(dirname "$LOG")"
: > "$LOG"

say()  { printf '%s\n' "$*"; printf '%s\n' "$*" >> "$LOG"; }
note() { printf '%s   %s%s\n' "$C_DIM" "$*" "$C_OFF"; printf '     %s\n' "$*" >> "$LOG"; }
warn() { printf '%s   ! %s%s\n' "$C_YEL" "$*" "$C_OFF"; printf '   ! %s\n' "$*" >> "$LOG"; }

step() {
	STEP_N=$((STEP_N + 1))
	STEP_NAME="$1"
	STEP_T0=$(date +%s)
	if [ "$STEP_TOTAL" -gt 0 ]; then
		printf '%s[%d/%d]%s %s%s%s\n' "$C_CYN" "$STEP_N" "$STEP_TOTAL" "$C_OFF" "$C_BLD" "$1" "$C_OFF"
	else
		printf '%s[%d]%s %s%s%s\n' "$C_CYN" "$STEP_N" "$C_OFF" "$C_BLD" "$1" "$C_OFF"
	fi
	printf '\n===== [%s] step %d: %s =====\n' "$(date -Is)" "$STEP_N" "$1" >> "$LOG"
}

# done_ok closes a step with how long it took. Durations are not decoration: a
# build that usually takes 40s and today took 6 minutes is the first symptom of
# a box that has started swapping.
done_ok() {
	secs=$(( $(date +%s) - STEP_T0 ))
	printf '%s   ✓%s %s %s(%ss)%s\n' "$C_GRN" "$C_OFF" "${1:-done}" "$C_DIM" "$secs" "$C_OFF"
	printf '   OK: %s (%ss)\n' "${1:-done}" "$secs" >> "$LOG"
}

# run executes a command with its output captured to the log and hidden from the
# terminal unless it fails or VERBOSE is set. The failure path is the whole
# point: the tail of what it actually printed, not a generic message about it.
run() {
	printf '$ %s\n' "$*" >> "$LOG"
	if [ -n "${VERBOSE:-}" ]; then
		# pipefail matters here: without it the exit status is tee's, which is
		# always zero, and every failing command in verbose mode would look
		# like a success.
		set -o pipefail
		"$@" 2>&1 | tee -a "$LOG"
		return $?
	fi
	"$@" >> "$LOG" 2>&1
}


# --- observability -----------------------------------------------------------
#
# When one of these scripts fails on a box nobody is watching, the person — or
# the agent — who picks it up gets a terminal that has already scrolled away.
# So every failure writes a self-contained report to a STABLE path, and the
# report is JSON because the thing most likely to read it next is not a person.
#
# The rule for what goes in: everything needed to diagnose without shell access.
# Which step, which command, which exit code, what it printed, what the service
# is doing now, what the box looks like, which commit is deployed. A report that
# says "build failed, exit 1" has moved the problem, not described it.
REPORT_DIR="${REPORT_DIR:-/var/log/articleflux}"
REPORT="$REPORT_DIR/last-failure.json"

# JSON string escaping in POSIX shell. Order matters: backslashes first, or the
# escapes added afterwards get escaped again and the report is not valid JSON —
# which defeats the entire point of writing one for a machine to read.
json_str() {
	# One awk program rather than a chain of sed expressions. The first version
	# of this was sed, and the shell quoting needed to make sed emit a backslash
	# did not survive being written by a script: it shipped as
	# an unterminated `s` command — so every escaped field in every
	# report came out empty, and the reports stayed valid JSON while saying
	# nothing. awk takes its escapes in a string literal, where they are legible.
	printf '%s' "$1" | awk '
		BEGIN { ORS = "" }
		{
			gsub(/\\/, "\\\\")   # backslashes first, or the escapes below get escaped
			gsub(/"/,  "\\\"")
			gsub(/\t/, "\\t")
			gsub(/\r/, "")
			if (NR > 1) print "\\n"
			print
		}'
}

# json_file emits a file's tail as one escaped JSON string.
json_file() { json_str "$(tail -n "${2:-60}" "$1" 2>/dev/null || echo '(no log)')"; }

write_report() {
	code="$1"; line="$2"
	mkdir -p "$REPORT_DIR"
	repo="${REPO:-${ARTICLEFLUX_REPO:-/opt/ArticleFlux}}"
	sha=$(git -C "$repo" rev-parse HEAD 2>/dev/null || echo unknown)
	subject=$(git -C "$repo" log -1 --format=%s 2>/dev/null || echo unknown)
	gwc_sha=$(git -C "${GWC:-/opt/GoWebComponents}" rev-parse HEAD 2>/dev/null || echo unknown)

	{
		printf '{
'
		printf '  "schema": "articleflux.failure/1",
'
		printf '  "when": "%s",
' "$(date -Is)"
		printf '  "host": "%s",
' "$(hostname)"
		printf '  "script": "%s",
' "$0"
		printf '  "args": "%s",
' "$(json_str "${SCRIPT_ARGS:-}")"
		printf '  "exit_code": %s,
' "$code"
		printf '  "failed_at_line": %s,
' "$line"
		printf '  "step_number": %s,
' "$STEP_N"
		printf '  "step_total": %s,
' "$STEP_TOTAL"
		printf '  "step_name": "%s",
' "$(json_str "$STEP_NAME")"
		printf '  "last_command": "%s",
' "$(json_str "$LAST_CMD")"
		printf '  "log_path": "%s",
' "$LOG"
		printf '  "deployed": {
'
		printf '    "repo": "%s",
' "$repo"
		printf '    "commit": "%s",
' "$sha"
		printf '    "subject": "%s",
' "$(json_str "$subject")"
		printf '    "gowebcomponents_commit": "%s",
' "$gwc_sha"
		printf '    "dirty": %s
' "$(git -C "$repo" diff --quiet 2>/dev/null && echo false || echo true)"
		printf '  },
'
		printf '  "service": {
'
		printf '    "active": "%s",
' "$(systemctl is-active articleflux 2>/dev/null || true)"
		printf '    "enabled": "%s",
' "$(systemctl is-enabled articleflux 2>/dev/null || true)"
		printf '    "restarts": "%s",
' "$(systemctl show -p NRestarts --value articleflux 2>/dev/null || true)"
		printf '    "health_url": "%s",
' "${HEALTH:-http://127.0.0.1:9000/healthz}"
		printf '    "health_http_code": "%s",
' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "${HEALTH:-http://127.0.0.1:9000/healthz}" 2>/dev/null || echo 000)"
		printf '    "nginx_active": "%s",
' "$(systemctl is-active nginx 2>/dev/null || true)"
		printf '    "nginx_http_code": "%s",
' "$(curl -s -L -o /dev/null -w '%{http_code}' --max-time 8 http://127.0.0.1/ 2>/dev/null || echo 000)"
		printf '    "nginx_final_url": "%s"
' "$(json_str "$(curl -s -L -o /dev/null -w '%{url_effective}' --max-time 8 http://127.0.0.1/ 2>/dev/null || echo '')")"
		printf '  },
'
		printf '  "box": {
'
		printf '    "os": "%s",
' "$(json_str "$(grep -m1 PRETTY_NAME /etc/os-release 2>/dev/null | cut -d= -f2- | tr -d '\"')")"
		printf '    "kernel": "%s",
' "$(uname -r)"
		printf '    "go": "%s",
' "$(/usr/local/go/bin/go version 2>/dev/null || echo absent)"
		printf '    "mem_mb_total": %s,
' "$(free -m | awk 'NR==2{print $2}')"
		printf '    "mem_mb_available": %s,
' "$(free -m | awk 'NR==2{print $7}')"
		printf '    "swap_mb": %s,
' "$(free -m | awk 'NR==3{print $2}')"
		printf '    "disk_mb_free": %s,
' "$(df -Pm "$repo" 2>/dev/null | awk 'NR==2{print $4}' || echo 0)"
		printf '    "load": "%s"
' "$(cut -d" " -f1-3 /proc/loadavg)"
		printf '  },
'
		printf '  "log_tail": "%s",
' "$(json_file "$LOG" 80)"
		printf '  "journal_tail": "%s"
' "$(json_str "$(journalctl -u articleflux -n 40 --no-pager -o short-iso 2>/dev/null || echo '(no journal)')")"
		printf '}
'
	} > "$REPORT.tmp" 2>/dev/null && mv -f "$REPORT.tmp" "$REPORT"

	# A dated copy beside it, because last-failure.json is overwritten by the
	# next failure and the interesting one is often the first, not the last.
	cp -f "$REPORT" "$REPORT_DIR/failure-$(date +%Y%m%d-%H%M%S).json" 2>/dev/null || true
	ls -1t "$REPORT_DIR"/failure-*.json 2>/dev/null | tail -n +21 | xargs -r rm -f
}

# --- failure -----------------------------------------------------------------
# One trap, and it says the four things worth knowing: which step, which line,
# which exit code, and what the log's last lines were.
on_error() {
	# The exit code is passed in, not read from $? here. $? reflects the LAST
	# command run, and by the time this function body starts that is the trap
	# wrapper's own bookkeeping — an assignment, which always succeeds. That is
	# how a failed deploy came back as exit 0: the script reported FAILED, then
	# exited zero, and the webhook that ran it recorded a successful deploy.
	code=${2:-$?}
	line=${1:-?}
	printf '\n%s%s✗ FAILED%s during step %s%d: %s%s\n' "$C_RED" "$C_BLD" "$C_OFF" "$C_BLD" "$STEP_N" "$STEP_NAME" "$C_OFF"
	printf '%s  exit %s at line %s of %s%s\n' "$C_DIM" "$code" "$line" "$0" "$C_OFF"
	printf '\n===== FAILED: step %d (%s), exit %s, line %s =====\n' "$STEP_N" "$STEP_NAME" "$code" "$line" >> "$LOG"
	printf '\n%s  last 25 lines of output:%s\n' "$C_DIM" "$C_OFF"
	tail -n 25 "$LOG" | sed 's/^/    /'
	printf '\n%s  full log: %s%s\n' "$C_DIM" "$LOG" "$C_OFF"
	if command -v systemctl >/dev/null 2>&1; then
		printf '%s  service:  systemctl status articleflux --no-pager%s\n' "$C_DIM" "$C_OFF"
	fi
	# Scripts that need to put something back register a rollback here. It runs
	# before the exit, and only on the failure path.
	if [ -n "${ROLLBACK_CMD:-}" ]; then
		printf '\n%s  rolling back…%s\n' "$C_YEL" "$C_OFF"
		eval "$ROLLBACK_CMD" >> "$LOG" 2>&1 || warn "rollback itself failed — see $LOG"
	fi
	exit "$code"
}
# ERR, not just EXIT. An EXIT trap fires wherever the shell happens to leave —
# which is the trap's own line — so the first failure this library reported said
# "line 1 of update.sh" and sent the reader to the shebang. ERR fires at the
# command that actually failed. EXIT stays as a backstop for the paths ERR does
# not cover (an explicit `exit 1`, an unset variable under `set -u`), and
# REPORTED keeps the two from writing the report twice.
REPORTED=''
set -E
# $? is captured as the FIRST thing the wrapper does and threaded through. Every
# statement after it — the guard, the assignment — resets $?, so reading it any
# later reads the trap's own success instead of the failure that fired it.
on_err_trap() {
	__code=$?
	[ -n "$REPORTED" ] && return
	REPORTED=1
	[ "$__code" = "0" ] && __code=1
	on_error "$1" "$__code"
}
trap 'on_err_trap $LINENO' ERR
trap 'on_err_trap $LINENO' INT TERM
trap 'on_err_trap ${LINENO}' EXIT

# finish disarms the trap. Call it on the success path or the trap fires on a
# clean exit and reports a failure that did not happen.
finish() {
	REPORTED=1
	trap - EXIT INT TERM ERR
	secs=$(( $(date +%s) - SCRIPT_T0 ))
	printf '\n%s%s✓ %s%s %sin %ss — log: %s%s\n' "$C_GRN" "$C_BLD" "${1:-Done}" "$C_OFF" "$C_DIM" "$secs" "$LOG" "$C_OFF"
	printf '\n===== SUCCESS: %s (%ss) =====\n' "${1:-Done}" "$secs" >> "$LOG"
}

need_root() {
	[ "$(id -u)" = "0" ] || { printf '%sThis script needs root. Try: sudo %s%s\n' "$C_RED" "$0" "$C_OFF"; exit 1; }
}

# wait_healthy polls the server's own health endpoint rather than asking
# systemd whether the process exists. The two are different questions, and only
# one of them is what a reader experiences.
wait_healthy() {
	url="${1:-http://127.0.0.1:9000/healthz}"
	deadline=$(( $(date +%s) + ${2:-90} ))
	while :; do
		if curl -fsS --max-time 5 -o /dev/null "$url" 2>/dev/null; then
			return 0
		fi
		if [ "$(date +%s)" -ge "$deadline" ]; then
			printf 'health check never passed at %s\n' "$url" >> "$LOG"
			journalctl -u articleflux -n 40 --no-pager >> "$LOG" 2>&1 || true
			return 1
		fi
		sleep 2
	done
}
