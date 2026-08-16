#!/bin/sh
# af-reader-deploy-release.sh — pin an ArticleFlux image tag and roll it out.
#
# Same shape as AnimeFeedFlux's deploy-release.sh and the portfolio's ec-deploy-release.sh:
# write the tag into compose.yaml, pull, recreate, block until the container's healthcheck
# says healthy, fail loudly otherwise. .previous-tag records the rollback point BEFORE the
# attempt. A rollback IS a release to an older tag.
#
#   sh af-reader-deploy-release.sh v1.3.0
set -eu

TAG="${1:-}"
APP_DIR="${2:-/opt/articleflux}"
HEALTH_RETRIES="${AF_HEALTH_RETRIES:-30}"
HEALTH_INTERVAL="${AF_HEALTH_INTERVAL:-10}"
CONTAINER_NAME="${AF_CONTAINER_NAME:-articleflux}"
IMAGE_BASENAME="articleflux"

log() { printf '[af-reader-deploy-release] %s\n' "$1"; }
die() { printf '[af-reader-deploy-release] ERROR: %s\n' "$1" >&2; exit 1; }

[ -n "$TAG" ] || die "usage: af-reader-deploy-release.sh <image-tag> [app-dir]"
case "$TAG" in
    latest) die "refusing to deploy 'latest' — pin an immutable v<semver> or sha-<commit> tag" ;;
esac

COMPOSE_FILE="$APP_DIR/compose.yaml"
[ -f "$COMPOSE_FILE" ] || die "$COMPOSE_FILE missing — install deploy/docker/compose.yaml first"

cd "$APP_DIR"
log "deploying tag: $TAG"

prev=$(grep -oE "image:.*/${IMAGE_BASENAME}:[^[:space:]]+" "$COMPOSE_FILE" | sed 's/^.*://' || true)
if [ -n "$prev" ] && [ "$prev" != "$TAG" ]; then
    echo "$prev" > "$APP_DIR/.previous-tag"
    log "recorded previous tag for rollback: $prev"
fi

sed -i "s|\(image:[[:space:]]*ghcr\.io/[^:]*/${IMAGE_BASENAME}\):.*|\1:${TAG}|" "$COMPOSE_FILE"
grep -q "/${IMAGE_BASENAME}:${TAG}" "$COMPOSE_FILE" \
    || die "failed to write tag $TAG into $COMPOSE_FILE"
log "compose.yaml now pins $TAG"

log "pulling image..."
docker compose -f "$COMPOSE_FILE" pull

log "recreating container..."
docker compose -f "$COMPOSE_FILE" up -d --remove-orphans

log "waiting for healthy (up to $((HEALTH_RETRIES * HEALTH_INTERVAL))s)..."
i=0
while [ "$i" -lt "$HEALTH_RETRIES" ]; do
    status=$(docker inspect -f '{{.State.Health.Status}}' "$CONTAINER_NAME" 2>/dev/null || echo "starting")
    case "$status" in
        healthy)
            log "healthy after $((i * HEALTH_INTERVAL))s"
            log "deploy of $TAG complete"
            exit 0
            ;;
        unhealthy)
            log "container reported UNHEALTHY — last 50 log lines:"
            docker compose -f "$COMPOSE_FILE" logs --tail=50 || true
            die "release $TAG never became healthy — roll back with: sh $0 \$(cat $APP_DIR/.previous-tag)"
            ;;
    esac
    i=$((i + 1))
    sleep "$HEALTH_INTERVAL"
done

log "timed out waiting for healthy — last 50 log lines:"
docker compose -f "$COMPOSE_FILE" logs --tail=50 || true
die "release $TAG never became healthy within $((HEALTH_RETRIES * HEALTH_INTERVAL))s — roll back with: sh $0 \$(cat $APP_DIR/.previous-tag)"
