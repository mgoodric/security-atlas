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
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/atlas ./cmd/atlas

# ----- Stage 2: runtime -----
FROM gcr.io/distroless/static-debian12:nonroot

# distroless/static-debian12:nonroot runs as uid 65532 (no root, no shell).
COPY --from=builder /out/atlas /usr/local/bin/atlas

# gRPC (Evidence + Admin + Connectors) and HTTP (anchors/frameworks + /health).
EXPOSE 8080 50051

ENTRYPOINT ["/usr/local/bin/atlas"]
