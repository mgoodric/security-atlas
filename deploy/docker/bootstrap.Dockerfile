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
# It is short-lived: it runs deploy/docker/bootstrap/bootstrap.sh, seeds
# the deployment, and exits 0. The atlas service waits on it
# (depends_on: ... condition: service_completed_successfully).
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
FROM postgres:16-alpine

# postgres:16-alpine ships psql, sh, and wget. No extra packages needed.
COPY --from=builder /out/atlas-cli /usr/local/bin/atlas-cli

# bootstrap.sh + seed.sql land in /bootstrap. migrations/ + controls/ get
# baked into /repo so the image is self-contained — no host bind-mount
# required. REPO_ROOT defaults to /repo (see bootstrap.sh).
COPY deploy/docker/bootstrap/ /bootstrap/
COPY migrations/                /repo/migrations/
COPY controls/                  /repo/controls/
RUN chmod +x /bootstrap/bootstrap.sh

# Run as the postgres image's unprivileged `postgres` user, not root.
USER postgres

ENTRYPOINT ["/bootstrap/bootstrap.sh"]
