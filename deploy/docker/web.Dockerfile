# syntax=docker/dockerfile:1
#
# web.Dockerfile — the security-atlas Next.js frontend (web/).
#
# Multi-stage:
#   1. deps    — install npm workspace dependencies
#   2. builder — `next build` with output: "standalone"
#   3. runtime — node:22-alpine, non-root, runs the traced standalone server
#
# The frontend is an npm workspace (`@security-atlas/web`); the build
# context is the repo root so the workspace resolves. next.config.ts sets
# `output: "standalone"` so the runtime stage copies only `.next/standalone`
# + `.next/static` + `public` — no full node_modules tree.
#
# Built + published by .github/workflows/container-publish.yml on release.
# Built locally by the docker-compose self-host bundle (slice 037).

# ----- Stage 1: dependencies -----
FROM node:22-alpine AS deps
WORKDIR /app

# Workspace manifests first so the dependency layer stays warm across
# source-only changes.
COPY package.json package-lock.json ./
COPY web/package.json web/package.json
RUN --mount=type=cache,target=/root/.npm \
    npm ci --no-audit --no-fund

# ----- Stage 2: build -----
FROM node:22-alpine AS builder
WORKDIR /app

COPY --from=deps /app/node_modules ./node_modules
COPY package.json package-lock.json ./
COPY web ./web

# Telemetry off in CI/build environments.
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build --workspace @security-atlas/web

# ----- Stage 3: runtime -----
FROM node:22-alpine AS runtime
WORKDIR /app

ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
# next start listens on PORT; compose maps host 3000 -> container 3000.
ENV PORT=3000

# node:alpine ships a `node` user (uid 1000). Run as it, not root.
USER node

# The standalone output is self-contained: server.js + the minimal traced
# node_modules. `.next/static` is copied alongside it (the standalone
# tracer intentionally excludes static assets).
#
# Slice 153 — `web/public/` must ALSO be copied. In a monorepo
# workspace the standalone tracer roots the output at the workspace
# layout (next.config.ts's `outputFileTracingRoot` resolves to the
# repo root), so server.js lives at `web/server.js` and the
# standalone server resolves `/public/*` request paths against
# `<server.js dir>/public/` — i.e. `./web/public/` from the runtime
# CWD. Without this COPY, requests for `/logo-light.svg`,
# `/logo-dark.svg`, `/og-image.png`, `/twitter-card.png`, the PWA
# icons, and `/apple-touch-icon.png` returned 404 even though
# `web/proxy.ts`'s PUBLIC_STATIC_FILES set (slice 123) correctly
# exempted them from the auth-redirect: the proxy let the request
# pass, but the standalone server had no file to serve. The
# operator-reported v1.10.0 logo-render bug surfaced exactly this
# gap. The previous comment ("There is no web/public directory in
# this project") pre-dated slice 123's introduction of the public/
# tree and was stale.
COPY --from=builder --chown=node:node /app/web/.next/standalone ./
COPY --from=builder --chown=node:node /app/web/.next/static ./web/.next/static
COPY --from=builder --chown=node:node /app/web/public ./web/public

EXPOSE 3000

# In a monorepo workspace the standalone tracer roots the output at the
# repo layout, so the server entrypoint lands at web/server.js.
CMD ["node", "web/server.js"]
