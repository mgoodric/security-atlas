# syntax=docker/dockerfile:1
#
# atlas-cli.Dockerfile — the security-atlas CLI (cmd/atlas-cli).
#
# Same multi-stage / distroless pattern as atlas.Dockerfile. The CLI is
# the tool the docker-compose self-host bundle's atlas-bootstrap one-shot
# container runs to:
#   - import the SCF catalog   (atlas-cli catalog import-scf <path>)
#   - upload the 50 control bundles (atlas-cli controls upload <dir>)
#
# Built + published by .github/workflows/container-publish.yml on release.

# ----- Stage 1: build -----
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
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/atlas-cli /usr/local/bin/atlas-cli

ENTRYPOINT ["/usr/local/bin/atlas-cli"]
