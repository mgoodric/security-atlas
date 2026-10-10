# syntax=docker/dockerfile:1
#
# bootstrap.Dockerfile — the security-atlas one-shot first-boot container
# (slice 037).
#
# Unlike atlas.Dockerfile / atlas-cli.Dockerfile (distroless — no shell,
# no psql), the bootstrap container needs a POSIX shell and the `psql`
# client to apply migrations and run the seed script. It is built on
# postgres:16-alpine (which already ships psql + sh + wget) with the
# statically-linked atlas-cli binary copied in.
#
# This image carries TWO entrypoint scripts and is used by TWO compose
# services (slice 473):
#
#   * /bootstrap/migrate.sh  — run by the always-run `atlas-migrate[-edge]`
#     service. Idempotent, fail-closed; applies roles + forward migrations
#     + the atlas_app password on EVERY bring-up, then exits. The atlas
#     backend `depends_on` this with `condition:
#     service_completed_successfully` so it never serves a partial schema.
#
#   * /bootstrap/bootstrap.sh — run by the one-shot `atlas-bootstrap[-edge]`
#     service (the default ENTRYPOINT). First-boot-only seed + SCF import +
#     control-bundle upload. Exits 0 on success.
#
# Both are short-lived. The compose files select migrate.sh via an
# `entrypoint:` override on the `atlas-migrate[-edge]` service; the default
# ENTRYPOINT below stays bootstrap.sh so the bundled `build:` and the
# pulled `image:` both keep the existing bootstrap behavior unchanged.
#
# The repo tree (migrations/, controls/, bootstrap scripts) is baked into
# the image at /repo at build time. Tagged releases carry their own
# matched migrations + control bundles, so a Watchtower-driven upgrade
# (pull new image -> restart) brings new migrations along automatically.
# The legacy `-v $repo:/repo:ro` bind-mount path is no longer required
# (and is omitted from the GHCR-image self-host bundle).

# ----- Stage 1: build atlas-cli -----
FROM golang:1.26 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/atlas-cli ./cmd/atlas-cli

# ----- Stage 2: runtime -----
FROM postgres:18-alpine

# postgres:16-alpine ships psql, sh, and wget. No extra packages needed.
COPY --from=builder /out/atlas-cli /usr/local/bin/atlas-cli

# bootstrap.sh + migrate.sh + seed.sql land in /bootstrap. migrations/ +
# controls/ get baked into /repo so the image is self-contained — no host
# bind-mount required. REPO_ROOT defaults to /repo (see both scripts).
COPY deploy/docker/bootstrap/ /bootstrap/
COPY migrations/                /repo/migrations/
COPY controls/                  /repo/controls/
RUN chmod +x /bootstrap/bootstrap.sh /bootstrap/migrate.sh

# Slice 196: pre-create /var/lib/atlas-bootstrap owned by the postgres
# user so the docker-compose named volume `atlas-bootstrap-data`
# mounted at this path inherits the postgres ownership. Without this,
# the volume is root-owned and bootstrap.sh's
# `oauth-bootstrap-credentials.json` write at phase 5a fails with
# "permission denied". The runtime-mounted volume is the only place
# the client_secret ever lives (P0-196-2: never baked into image
# layers).
RUN mkdir -p /var/lib/atlas-bootstrap \
    && chown postgres:postgres /var/lib/atlas-bootstrap \
    && chmod 0700 /var/lib/atlas-bootstrap

# Run as the postgres image's unprivileged `postgres` user, not root.
USER postgres

ENTRYPOINT ["/bootstrap/bootstrap.sh"]
