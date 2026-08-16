#!/bin/sh
# af-reader-autoupdate.sh — deploy ArticleFlux's newest v* release tag when it changes.
#
# The AFF pattern: the deploy webhook (primary) and a daily systemd timer (fallback for a
# lost delivery) both run this. It compares the newest v* tag on the repo against the tag
# pinned in /opt/articleflux/compose.yaml and, when they differ AND the image is actually
# published, hands off to af-reader-deploy-release.sh, whose rollout is health-gated.
#
# NOTE this replaces the old deployhook.service flow for ArticleFlux: deploys now key off
# TAG pushes, not "CI green on main" — a release is a deliberate act, and a docs commit to
# main no longer restarts the reader.
#
# Idempotent and safe to run by hand: `sh /usr/local/bin/af-reader-autoupdate.sh`.
set -eu

SRC="${AF_SRC_DIR:-/opt/ArticleFlux}"
APP="${AF_APP_DIR:-/opt/articleflux}"
IMAGE="ghcr.io/monstercameron/articleflux"

log() { printf '[af-reader-autoupdate] %s\n' "$1"; }

[ -d "$SRC/.git" ] || { log "ERROR: $SRC is not a git checkout"; exit 1; }
[ -f "$APP/compose.yaml" ] || { log "ERROR: $APP/compose.yaml missing — install deploy/docker/compose.yaml"; exit 1; }

git -C "$SRC" fetch -q --tags origin
git -C "$SRC" reset -q --hard origin/main

latest=$(git -C "$SRC" tag -l 'v*' --sort=-v:refname | head -n1)
if [ -z "$latest" ]; then
    log "no v* tags exist yet — nothing to deploy"
    exit 0
fi

current=$(grep -oE 'image:.*/articleflux:[^[:space:]]+' "$APP/compose.yaml" | sed 's/^.*://' || true)
if [ "$current" = "$latest" ]; then
    log "up to date ($current)"
    exit 0
fi

if ! docker manifest inspect "$IMAGE:$latest" >/dev/null 2>&1; then
    log "tag $latest exists but $IMAGE:$latest is not published yet — retrying next tick"
    exit 0
fi

log "updating: ${current:-none} -> $latest"
exec sh "$SRC/deploy/docker/af-reader-deploy-release.sh" "$latest" "$APP"
