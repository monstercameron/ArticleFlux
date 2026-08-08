#!/bin/sh
# ArticleFlux — the one way anything on this box reaches a human.
#
#   articleflux-alert "<subject>" "<body>"
#   articleflux-alert --unit articleflux-backup.service    # systemd OnFailure=
#
# Installed at /usr/local/bin/articleflux-alert. Configured, if at all, by
# /etc/articleflux/alert.env.
#
# # Why this exists
#
# Every failure path on this box ended at `logger`. The watchdog's "RESTART DID
# NOT HELP", a nightly backup that failed, a deploy that rolled itself back —
# all of them wrote a line into journald on a droplet nobody logs into, and
# stayed there. Mean time to detection was "when Cam opens the reader", which
# for a full disk is hours and for a failed backup is however long it takes to
# need one.
#
# So this is not a monitoring system. It is the missing edge: something that
# leaves the machine. What it sends to is deliberately unspecified — a webhook
# is one line of configuration and works with ntfy, Slack, Discord, Gotify and
# anything else that accepts a POST — because the alternative is picking a
# provider on somebody's behalf and having them not use it.
#
# # Why it always exits 0
#
# It is wired as `OnFailure=` on units whose failure is the thing being
# reported. A notifier that can itself fail turns one problem into two, and the
# second one is invisible because the thing that would have reported it is the
# thing that failed. Every path here logs and returns success; the worst case is
# that the alert is only in journald, which is exactly where it was before.
#
# # Configuration
#
#   /etc/articleflux/alert.env, chmod 600, owned by root:
#     ALERT_WEBHOOK_URL=https://ntfy.sh/some-secret-topic-name
#     ALERT_WEBHOOK_FORMAT=text        # text (default) | slack | discord | json
#     ALERT_EMAIL=you@example.com      # optional, needs a working sendmail
#     ALERT_HOSTNAME=feed.example.com  # optional, defaults to `hostname`
#
# An unconfigured box logs "no alert channel configured" and exits 0. That is a
# deliberate non-failure: an operator who has not set one up has not broken
# anything, and refusing to start the backup timer over it would be worse than
# the gap it is filling.
set -u

CONF="${ARTICLEFLUX_ALERT_CONF:-/etc/articleflux/alert.env}"
# shellcheck source=/dev/null
[ -r "$CONF" ] && . "$CONF"

HOST="${ALERT_HOSTNAME:-$(hostname 2>/dev/null || echo unknown)}"
FORMAT="${ALERT_WEBHOOK_FORMAT:-text}"

# --unit turns a systemd OnFailure= into a subject and a body.
#
# systemd passes the failed unit's name as %n, and the useful body is the tail
# of its journal — which is the thing somebody would go and look up anyway, at
# the moment it is still there. Fifteen lines: enough to show the error, short
# enough for a phone notification.
if [ "${1:-}" = "--unit" ]; then
	unit="${2:-unknown.service}"
	subject="ArticleFlux: $unit FAILED on $HOST"
	body=$(systemctl status --no-pager --lines=0 "$unit" 2>/dev/null | head -5)
	body="$body

--- last 15 log lines ---
$(journalctl -u "$unit" --no-pager --lines=15 -o cat 2>/dev/null)"
else
	subject="${1:-ArticleFlux alert on $HOST}"
	body="${2:-}"
fi

# journald ALWAYS, and first.
#
# Not a fallback — the local record is the one that survives the webhook being
# wrong, the network being the thing that broke, and the operator changing
# providers. Sending is additive to it.
logger -t articleflux-alert "$subject"

sent=0

if [ -n "${ALERT_WEBHOOK_URL:-}" ]; then
	# The body is embedded in JSON for three of the four formats, so it has to
	# be escaped. sed rather than jq: jq is not installed on a bare Ubuntu box,
	# and this script must work on the day the box is first provisioned.
	# Backslashes first, or the escaping escapes its own escapes.
	esc=$(printf '%s' "$subject
$body" |
		sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/\t/\\t/g' |
		awk 'BEGIN{ORS=""} {print sep $0; sep="\\n"}')

	case "$FORMAT" in
		slack)   payload="{\"text\":\"$esc\"}" ;;
		discord) payload="{\"content\":\"$esc\"}" ;;
		json)    payload="{\"subject\":\"$(printf '%s' "$subject" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')\",\"body\":\"$esc\",\"host\":\"$HOST\"}" ;;
		*)       payload="" ;;
	esac

	if [ "$FORMAT" = "text" ]; then
		# ntfy and Gotify take the body as plain text with the subject in a
		# header, which is the shape that renders best on a phone.
		curl -fsS --max-time 15 -X POST \
			-H "Title: $subject" \
			-H "Content-Type: text/plain" \
			--data-binary "$body" \
			"$ALERT_WEBHOOK_URL" >/dev/null 2>&1 && sent=1
	else
		curl -fsS --max-time 15 -X POST \
			-H "Content-Type: application/json" \
			--data "$payload" \
			"$ALERT_WEBHOOK_URL" >/dev/null 2>&1 && sent=1
	fi

	[ "$sent" = 1 ] || logger -t articleflux-alert "the webhook POST failed; this alert is only in journald"
fi

if [ -n "${ALERT_EMAIL:-}" ] && command -v sendmail >/dev/null 2>&1; then
	printf 'To: %s\nSubject: %s\n\n%s\n' "$ALERT_EMAIL" "$subject" "$body" |
		sendmail -t >/dev/null 2>&1 && sent=1
fi

if [ "$sent" = 0 ] && [ -z "${ALERT_WEBHOOK_URL:-}" ] && [ -z "${ALERT_EMAIL:-}" ]; then
	logger -t articleflux-alert "no alert channel configured in $CONF — this alert reached journald only"
fi

# Always. See the header.
exit 0
