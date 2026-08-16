# Container build for ArticleFlux — third app on the box to move to the AFF pattern
# (AnimeFeedFlux 2026-08-15, the portfolio 2026-08-16, now this).
#
# THE BUILD CONTEXT IS THE WORKSPACE ROOT, NOT THIS REPO. go.mod pins GoWebComponents/v5
# by relative replace (../GoWebComponents), so the context must hold both checkouts as
# siblings — the exact layout .github/actions/gwc materialises for CI:
#
#   <context>/ArticleFlux/         this repo
#   <context>/GoWebComponents/     at the ref .github/actions/gwc pins
#
# The release workflow builds with `-f ArticleFlux/deploy/docker/Dockerfile .`.
#
# PATH TRANSPARENCY IS THE DESIGN RULE for this image: the binary lives at
# /opt/articleflux/bin/articleflux and the client at /opt/articleflux/bin/web, and the
# database stays at /var/lib/articleflux — the same paths the systemd deployment used —
# so every ops script (backup, restore drill, health) keeps meaning what it meant, and
# `articleflux-ctr` (the docker-run wrapper) needs no path translation at all.

FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY GoWebComponents/ GoWebComponents/
COPY ArticleFlux/ ArticleFlux/

WORKDIR /src/ArticleFlux

# CGO off is required, not preferred: the runtime image is distroless/static. The SQLite
# driver is ncruces/go-sqlite3 — SQLite compiled to WebAssembly, run by wazero — so it is
# pure Go already. (wazero JIT-compiles to executable pages at runtime; Docker's default
# seccomp profile permits that, unlike the systemd unit which had to carve out
# MemoryDenyWriteExecute for the same reason.)
ENV CGO_ENABLED=0
RUN make build && make wasm

FROM gcr.io/distroless/static:nonroot

COPY --from=build /src/ArticleFlux/bin/articleflux /opt/articleflux/bin/articleflux
COPY --from=build /src/ArticleFlux/bin/web /opt/articleflux/bin/web

# The database directory — compose bind-mounts the host's /var/lib/articleflux here, at
# the SAME path, so WAL files, the speech cache, and every backup script see one truth.
VOLUME ["/var/lib/articleflux"]

EXPOSE 9000

# compose sets `user:` to the host's articleflux uid:gid (image default nonroot would not
# be able to open the existing database — the portfolio cutover's lesson, pre-paid here).
USER 65532:65532

# The binary asks its own /healthz — the same endpoint articleflux-health.sh gates on —
# because distroless has no shell or curl (see cmd/articleflux/healthcheckcmd.go).
HEALTHCHECK --interval=30s --timeout=6s --start-period=15s --retries=3 \
    CMD ["/opt/articleflux/bin/articleflux", "healthcheck"]

# serve flags come from compose's `command:` — they are deployment policy (origin, poll
# cadence), not image facts, and the live values belong in one editable place on the box.
ENTRYPOINT ["/opt/articleflux/bin/articleflux"]
CMD ["serve", "-addr", ":9000", "-db", "/var/lib/articleflux/articleflux.db", "-web", "/opt/articleflux/bin/web", "-behind-proxy", "-poll", "15m"]
