# syntax=docker/dockerfile:1
#
# The ArticleFlux image — the server binary and the assembled wasm client, on distroless.
# How to RUN it is docs/INSTALL.md; this file is only about how it is BUILT.
#
# THE BUILD CONTEXT IS THIS REPO'S ROOT, and nothing beside it. It used to be the
# directory ABOVE, holding this checkout and GoWebComponents as siblings, because go.mod
# resolved GWC through a relative replace. GWC is an ordinary published requirement now,
# so the build stage downloads it with every other module. What the context may contain
# is decided by the root .dockerignore, which is an allowlist — read it before adding a
# directory the build needs.
#
#   docker build -f deploy/docker/Dockerfile .
#
# PATH TRANSPARENCY IS THE DESIGN RULE for this image: the binary lives at
# /opt/articleflux/bin/articleflux and the client at /opt/articleflux/bin/web, and the
# database stays at /var/lib/articleflux — the same paths the systemd deployment used —
# so every ops script (backup, restore drill, health) keeps meaning what it meant, and
# `articleflux-ctr` (the docker-run wrapper) needs no path translation at all.

# Patch-level, and the same patch CI and release.yml set as GO_VERSION. The official
# image sets GOTOOLCHAIN=local, so a base older than go.mod's `go` line does not fetch a
# newer toolchain — it refuses to build — and a floating `1.27` would be a different
# compiler from the one the verify job just tested.
ARG GO_VERSION=1.27.4

# --- base: the source and its modules, on the BUILDER's platform -------------------------
#
# --platform=$BUILDPLATFORM is what makes the arm64 image cheap. Without it buildx runs
# this whole stage under QEMU for every non-native platform, and the wasm compile —
# already the slowest step here, and the one that needs ~2 GB — would run emulated at a
# fraction of native speed. Nothing in it has to: the client is GOOS=js GOARCH=wasm on
# every platform, and the server is pure Go (see CGO below), so the native toolchain
# cross-compiles it. The runtime stage has no RUN at all, so no instruction anywhere in
# this file ever executes on the target architecture.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS base

# Still /src/ArticleFlux, though nothing sits beside it any more: the COPY --from lines
# below name this path, and a shorter one would be a change with no purpose.
WORKDIR /src/ArticleFlux

# CGO off is required, not preferred: the runtime image is distroless/static, and it is
# what makes cross-compiling the server a one-variable change. The SQLite driver is
# ncruces/go-sqlite3 — SQLite compiled to WebAssembly, run by wazero — so it is pure Go
# already. (wazero JIT-compiles to executable pages at runtime; Docker's default seccomp
# profile permits that, unlike the systemd unit which had to carve out
# MemoryDenyWriteExecute for the same reason.)
ENV CGO_ENABLED=0

# Modules first, in their own layer, keyed on go.mod and go.sum alone. A source change
# then reuses this layer instead of downloading ~every dependency again. Deliberately NOT
# a cache mount: the release workflow's cache (type=gha) exports layers and does not
# export cache mounts, so on a fresh CI runner a mount would be empty and only a layer
# actually saves the download.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# --- web: the client, built once ----------------------------------------------------------
#
# `make wasm`, not a copy of its recipe: it also stages index.html, sw.js, the fonts,
# the manifest and icons and the screenshots, writes wasm_exec.js from THIS toolchain,
# and precompresses the lot — every step of which has a comment in the Makefile
# explaining the outage its absence caused. Two copies of that recipe is how one of them
# goes stale. This stage never reads TARGETARCH, so for a multi-platform build BuildKit
# solves it once and both images share the result.
#
# The go-build cache mount speeds up rebuilds on a developer's machine. In CI it starts
# empty (see above), which costs nothing over not having it.
FROM base AS web
RUN --mount=type=cache,target=/root/.cache/go-build make wasm

# --- server: the binary, cross-compiled for the image being built -------------------------
#
# The same `go build` line `make build` runs first. Not `make build` itself: that target
# also compiles ./... and the client natively as a CHECK, which the release workflow's
# verify job has already done before this build starts, and which here would be two
# whole-tree compiles per platform for nothing.
#
# buildver.Commit is stamped here, as deploy/update.sh stamps it, and for the same reason:
# /readyz answers with it as X-ArticleFlux-Commit, which is the only way anything outside
# the box can tell WHICH build is serving (the day a green deploy kept serving the old
# one is in internal/buildver). Without it a container reports no commit at all, and
# every "did the release land?" check has nothing to compare. REVISION arrives from
# release.yml as the full commit sha; a local build passes none and gets the honest
# empty value, which /readyz leaves out. It is declared in THIS stage only, so the
# `web` stage's cache never keys on a commit.
FROM base AS server
ARG TARGETOS
ARG TARGETARCH
ARG REVISION
RUN --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
      -ldflags "-X github.com/monstercameron/ArticleFlux/internal/buildver.Commit=${REVISION}" \
      -o /out/articleflux ./cmd/articleflux

# The data directory, created here because the runtime image has no shell to create it.
# It has to EXIST in the image, owned by the runtime user, before the VOLUME line:
# Docker seeds a fresh named volume from the image's directory at the mount point —
# ownership and mode included — and a VOLUME over a path the image does not have is
# created root-owned. The server would then fail its own preflight ("data directory is
# not writable") on the very first `docker run`. 0700 for the systemd install's reason:
# the database is a complete record of what someone reads.
RUN mkdir -p /out/data && chmod 0700 /out/data

# --- runtime ------------------------------------------------------------------------------
#
# static-debian12 rather than `static`, so the Debian base under the CA bundle and tzdata
# changes when this line does and not when the distroless project retags.
FROM gcr.io/distroless/static-debian12:nonroot

# Stamped by release.yml. The defaults are what a local `docker build` honestly is.
ARG VERSION=dev
ARG REVISION=unknown
# image.source is not decoration: it is how GHCR links the package to this repository.
LABEL org.opencontainers.image.title="ArticleFlux" \
      org.opencontainers.image.description="A self-hosted feed reader: one Go binary, one SQLite file, a Go/wasm client" \
      org.opencontainers.image.source="https://github.com/monstercameron/ArticleFlux" \
      org.opencontainers.image.documentation="https://github.com/monstercameron/ArticleFlux/blob/main/docs/INSTALL.md" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

COPY --from=server /out/articleflux /opt/articleflux/bin/articleflux
COPY --from=web /src/ArticleFlux/bin/web /opt/articleflux/bin/web
COPY --from=server --chown=65532:65532 /out/data /var/lib/articleflux

# Two of serve's flag defaults are wrong inside a container, and both have environment
# spellings (cmd/articleflux/main.go), so the image sets those rather than baking flags
# into CMD. A user who adds a flag on the command line then keeps these instead of
# silently replacing them:
#   ARTICLEFLUX_ADDR  — the default 127.0.0.1:9000 is the container's own loopback,
#                       unreachable through a published port. Where the boundary lives
#                       now is the PUBLISH address (`-p 127.0.0.1:9000:9000`).
#   ARTICLEFLUX_WEB   — the default bin/web is relative to a source checkout.
# Anything set with -e or env_file still wins, and a flag beats both.
#
# PATH gains the binary's directory so `docker exec <container> articleflux adduser …`
# works as written. `docker exec` does not go through ENTRYPOINT, and without this every
# operator command in docs/INSTALL.md would need the full /opt/articleflux/bin path.
ENV ARTICLEFLUX_ADDR=:9000 \
    ARTICLEFLUX_WEB=/opt/articleflux/bin/web \
    PATH=/opt/articleflux/bin:/usr/local/bin:/usr/bin:/bin

# The working directory IS the data directory, and that is what makes every subcommand
# safe to run here without arguments. -db has no environment spelling; its default is
# the relative `articleflux.db`, which from here is the volume's database. Anywhere else,
# `docker exec articleflux articleflux adduser -user sam` would have created a second,
# empty database and reported success. The consequence worth knowing: serve also reads
# `.env` from its working directory, so a .env placed in the volume is read — and the
# boot log names every key it applied.
WORKDIR /var/lib/articleflux
VOLUME ["/var/lib/articleflux"]

EXPOSE 9000

# distroless's nonroot user. The production compose overrides it with the host's
# articleflux uid:gid so a bind-mounted database keeps one owner (the portfolio
# cutover's lesson); a named volume needs no override, because it is seeded with this
# owner by the COPY above.
USER 65532:65532

# The binary asks its own /healthz — the same endpoint articleflux-health.sh gates on —
# because distroless has no shell or curl (see cmd/articleflux/healthcheckcmd.go). It
# probes 127.0.0.1:9000, so an ARTICLEFLUX_ADDR on another port needs a matching
# healthcheck override.
#
# start-period is two minutes because boot runs every pending migration and one ranking
# derivation BEFORE the listener opens (serve: app.Open, then DeriveDue), and on a
# multi-gigabyte database a release with a migration can take a while. Failures inside
# the window do not count; start-interval probes every 5 s inside it so a fast boot is
# reported healthy in seconds rather than at the first 30 s tick.
HEALTHCHECK --interval=30s --timeout=6s --start-period=120s --start-interval=5s --retries=3 \
    CMD ["/opt/articleflux/bin/articleflux", "healthcheck"]

ENTRYPOINT ["/opt/articleflux/bin/articleflux"]
# Just `serve`: the address and web root come from ENV above, the database from WORKDIR,
# and everything that is deployment policy — -origin, -behind-proxy — from the
# environment the operator gives it (docs/INSTALL.md). -behind-proxy is deliberately NOT
# a default here, unlike the old CMD: it makes the server believe X-Forwarded-For, which
# any client can send, so it is safe only when a proxy is known to be in front.
CMD ["serve"]
