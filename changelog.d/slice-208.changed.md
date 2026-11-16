- **web:** slice 208 — Next.js rewrites for `/v1/*`, `/health`,
  `/metrics`. `web/next.config.ts` adds `async rewrites()` forwarding
  the three well-known path prefixes to the atlas Go backend at
  `${ATLAS_HTTP_URL}` (default `http://localhost:8080` with a one-shot
  `console.warn` when the env is unset). Closes the post-slice-206
  gap where the browser-side `fetch('/v1/me')` reached Next.js — past
  the `proxy.ts` redirect-to-login exemption — and 404'd at the
  catch-all (no route handler at `app/v1/*`). The dashboard's data
  layer now Just Works on any deployment topology that sets
  `ATLAS_HTTP_URL`, without operator-side reverse-proxy path-routing
  config. Auth contract preserved: rewrites forward cookies + headers
  verbatim, atlas's slice-190 `jwtmw` middleware remains the auth
  authority (verified by AC-5 — unauthenticated `/v1/anchors` returns
  401 JSON from atlas, NOT 404 from Next.js, NOT 307 to /login).
  `/api/*` (BFF route handlers) and `/oauth/*` (OIDC callback) are
  intentionally NOT rewritten — those stay Next.js-server-managed.
  Operator note: deployments with existing NPM/Caddy/Traefik
  path-routing for `/v1/`, `/health`, `/metrics` can keep the config
  (harmless extra hop) or remove it (simpler);
  `docs/operations/edge-deploy.md` documents both paths.
  Regression: vitest `web/next-config.test.ts` (5 cases — env-set
  shape, env-unset fallback + warn, empty-string fallback, P0-A1+A2
  no-rewrite-of-/api+/oauth, slice-037 standalone-output invariant)
  - Playwright `web/e2e/nextjs-rewrites.spec.ts` (3 cases — authed
    `/v1/me` 200, unauth `/v1/anchors` 401, `/health` 200). Decisions
    log: `docs/audit-log/208-nextjs-rewrites-decisions.md`.
