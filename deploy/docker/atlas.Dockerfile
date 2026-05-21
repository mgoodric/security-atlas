# syntax=docker/dockerfile:1
#
# atlas.Dockerfile — the security-atlas platform server (cmd/atlas).
#
# Multi-stage:
#   1. builder  — golang:1.26 toolchain, compiles a static binary
#   2. runtime  — gcr.io/distroless/static-debian12:nonroot, ~2 MB base,
#                 no shell, no package manager, runs as uid 65532
#
# Per CLAUDE.md container norms: distroless base, multi-stage build,
# non-root runtime. The binary is CGO-disabled and fully static so the
# distroless/static base (which has no libc) is sufficient.
#
# Built + published by .github/workflows/container-publish.yml on release.
# Built locally by the docker-compose self-host bundle (slice 037).

# ----- Stage 1: build -----
FROM golang:1.26 AS builder

WORKDIR /src

# Cache module downloads separately from the source tree so dependency
# layers stay warm across source-only changes. security-atlas is a single
# Go module (go.work is just `use .`).
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

# CGO_ENABLED=0 → static binary, no libc dependency, runs on distroless/static.
# -trimpath + -ldflags "-s -w" → smaller, reproducible binary.
#
# Slice 072: bake all three version fields into the binary so
# GET /v1/version reports build metadata. Single source of truth for the
# JSON endpoint, the atlas-cli `version` subcommand, the human-readable
# banner, AND the OCI image labels below.
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_TIME}" \
    -o /out/atlas ./cmd/atlas

# Slice 196: pre-create the runtime data directories with nonroot
# ownership (uid 65532, the distroless `nonroot` user) so the
# downstream COPY --chown lands them in the distroless image with the
# right ownership. The slice 187 OAuth keystore writes to
# /var/lib/atlas/keys at first boot, the slice 073 bootstrap-token
# file lands at /var/lib/atlas/bootstrap-token, and the docker-compose
# bundle mounts a named volume at /var/lib/atlas (so reboots preserve
# both). Distroless has no shell, so we mkdir + chown in the builder
# stage and COPY across.
RUN mkdir -p /out/var/lib/atlas/keys \
    && chown -R 65532:65532 /out/var/lib/atlas

# ----- Stage 2: runtime -----
FROM gcr.io/distroless/static-debian12:nonroot

# distroless/static-debian12:nonroot runs as uid 65532 (no root, no shell).
COPY --from=builder /out/atlas /usr/local/bin/atlas
COPY --from=builder --chown=nonroot:nonroot /out/var/lib/atlas /var/lib/atlas

# Slice 072: OCI image annotations. Standard names from
# https://github.com/opencontainers/image-spec/blob/main/annotations.md
# so every registry browser and image scanner reads them without bespoke
# knowledge of security-atlas. Same values that get baked into the
# binary's ldflags above — single source of truth.
ARG VERSION
ARG COMMIT
ARG BUILD_TIME
LABEL org.opencontainers.image.title="security-atlas"
LABEL org.opencontainers.image.description="Open-source GRC platform — control graph + evidence pipeline"
LABEL org.opencontainers.image.source="https://github.com/mgoodric/security-atlas"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.revision="${COMMIT}"
LABEL org.opencontainers.image.created="${BUILD_TIME}"

# gRPC (Evidence + Admin + Connectors) and HTTP (anchors/frameworks + /health + /v1/version).
EXPOSE 8080 50051

ENTRYPOINT ["/usr/local/bin/atlas"]
