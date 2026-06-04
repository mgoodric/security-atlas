# 208 — Next.js rewrites for `/v1/*`, `/health`, `/metrics`

**Cluster:** Frontend (deploy / routing)
**Estimate:** ~0.5d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** spillover surfaced 2026-05-22 during live atlas-edge provisioning. NPM path-based routing works as an operator workaround but requires every deployment topology to re-add three NPM `Location` blocks. Slice 206 exempted `/v1/*` + `/metrics` in `web/proxy.ts` so the Next.js redirect-to-login gate fires after the path check — but Next.js itself still has no route at `/v1/*`, so the dashboard's browser-side data fetches return 404 from the Next.js catch-all. The fix is in-repo and portable.

## Narrative

The architectural shape post-slice-206:

1. The browser loads `https://atlas-edge.home.gmoney.sh/dashboard` (Next.js page).
2. The dashboard's client-side `fetch('/v1/me')` hits the SAME ORIGIN — the Next.js host.
3. Next.js routes `/v1/me` through `web/proxy.ts`. Slice 206 added `pathname.startsWith("/v1/")` to the exempt set, so the redirect-to-login does NOT fire.
4. After the proxy returns `next()`, Next.js looks for a route handler at `app/v1/me/route.ts`. There is none. So the server returns 404 (the catch-all).

The dashboard panel renders "Could not load this panel" because the BFF JSON parse throws on the 404 HTML.

**Path-based NPM workaround (operator-provisioned):** add three `Location` blocks to the reverse proxy: `/v1/` + `/health` + `/metrics` → atlas Go backend. Maintainer verified this live on atlas-edge 2026-05-22; it works. But it requires every operator to replicate the config in their reverse proxy of choice (NPM, Caddy, Traefik, nginx, Kubernetes Ingress, etc.).

**Next.js rewrites (this slice):** `web/next.config.ts` declares an `async rewrites()` that forwards the three path prefixes to `${ATLAS_HTTP_URL}/...`. The env var is already wired by `deploy/docker/docker-compose.yml` + `docker-compose.edge.yml` + read by `web/lib/api.ts:apiBaseURL()` for server-side BFF calls. Generalizing the same env to browser-side requests through rewrites means:

- One in-repo line of config makes every deployment topology Just Work.
- No operator-side proxy config required.
- The reverse proxy in front of the deployment becomes a pure TLS terminator + hostname router; path-routing is the application's job.

## Threat model

| STRIDE                | Threat                                                                                                                                                                                                                                                                                                                                                                                                                                                             | Mitigation                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | A rewrite that forwards to an attacker-controlled origin would let the dashboard's data layer leak cookies/tokens. The rewrite destination is hard-coded to `${ATLAS_HTTP_URL}` — an operator-controlled env var, not a request-derived value. Open-redirect class threats do not apply (the destination is not derived from query/path params).                                                                                                                   | P0-A4: no new env var. `ATLAS_HTTP_URL` already governs the BFF→backend hop; reusing it preserves the existing operator threat model.                                                                                                                                                                                                                                                                                                                                                                 |
| **T** Tampering       | A rewrite preserves cookies + headers verbatim — the browser still sees the same origin, so the `atlas_jwt` cookie attaches to the rewritten request. Tampering would require the operator to set `ATLAS_HTTP_URL` to a hostile origin — explicit, observable, and out of scope.                                                                                                                                                                                   | n/a — the rewrite destination is operator-controlled at deploy time.                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| **R** Repudiation     | The atlas Go backend's audit log still captures every `/v1/*` hit with the verified JWT subject + tenant. The rewrite is invisible to the audit layer.                                                                                                                                                                                                                                                                                                             | n/a                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| **I** Info disclosure | `/health` is intentionally public (slice-052 contract); `/metrics` is the slice-121 OTel surface (deliberate operator-visibility). Rewriting these does not expose new data — they were already public on the atlas backend port.                                                                                                                                                                                                                                  | The rewrite preserves whatever the backend serves at `/health` and `/metrics`. If the atlas backend ever locks `/metrics` behind auth (operator-network-only is the current shape), the rewrite continues to forward without modification — the auth decision stays on the backend.                                                                                                                                                                                                                   |
| **D** DoS             | An unauthenticated requester can hammer `/v1/anchors` through the Next.js host and reach atlas. Without rewrites, the request 404s at Next.js (cheaper) — but then the dashboard doesn't work. The tradeoff is intentional: the atlas backend already has its own rate limiting + RLS gate; pushing requests through is the contract.                                                                                                                              | n/a — atlas-side rate limit (slice-XXX) is the authority.                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| **E** EoP             | The critical bug class: if a rewrite ever bypasses atlas auth, that's an auth-substrate escape. **It doesn't.** The rewrite preserves cookies + headers, and atlas's slice-190 `jwtmw` middleware gates every `/v1/*` request. AC-5 explicitly verifies: an unauthenticated `fetch('/v1/anchors')` through the rewrite returns 401 from atlas (not 404 from Next.js, not 200 from a bypass). P0-A1 forbids rewriting `/api/*` (BFF) or `/oauth/*` (OIDC callback). | P0-A1 + P0-A2 + P0-A3: rewrites NEVER cover `/api/*` (BFF routes stay server-side) NOR `/oauth/*` (OIDC callback stays server-side). The three rewrite rules are exact-path-prefix and well-known: `/v1/:path*`, `/health` (equality), `/metrics` (equality). The atlas backend remains the auth authority for everything under `/v1/*`. AC-4 + AC-5 exercise both the positive (authed `/v1/me` reaches atlas, returns 200) and the negative (unauth `/v1/anchors` reaches atlas, returns 401 JSON). |

## Acceptance criteria

- [ ] AC-1: `web/next.config.ts` declares `async rewrites()` returning exactly three rules:
  - `{ source: "/v1/:path*",  destination: "${ATLAS_HTTP_URL}/v1/:path*" }`
  - `{ source: "/health",     destination: "${ATLAS_HTTP_URL}/health" }`
  - `{ source: "/metrics",    destination: "${ATLAS_HTTP_URL}/metrics" }`
- [ ] AC-2: `ATLAS_HTTP_URL` env read at config load. D1 fallback to `http://localhost:8080` if unset. A warning is logged via `console.warn` at config load when the fallback is in play, so devs running `next dev` outside docker-compose see the heads-up.
- [ ] AC-3: vitest regression `web/next-config.test.ts` covers the rewrite shape: with `ATLAS_HTTP_URL=http://atlas:8080` set, `rewrites()` returns the expected 3-rule array; with the env unset, `rewrites()` falls back to `http://localhost:8080` AND `console.warn` is called once.
- [ ] AC-4: Playwright e2e (new spec `web/e2e/nextjs-rewrites.spec.ts`) — authenticated browser context `request.get("/v1/me")` returns 200 + JSON containing the demo user (proves the rewrite forwards cookies + atlas auth verifies them).
- [ ] AC-5: Same spec — unauthenticated `request.get("/v1/anchors", { maxRedirects: 0 })` returns 401 JSON from atlas (NOT 404 from Next.js, NOT 307 to /login). Verifies the rewrite does not bypass atlas's slice-190 `jwtmw` middleware.
- [ ] AC-6: Same spec — `request.get("/health")` returns 200 JSON shaped `{status, ...}` (whatever atlas's /health endpoint emits). Verifies the literal-path rewrite works.
- [ ] AC-7: `docs/operations/edge-deploy.md` updated — the existing reverse-proxy section's path-routing recommendation gets a one-paragraph note explaining that operators with existing NPM/Caddy/etc. path-routing can either keep it (one fewer hop, marginal benefit) or remove it (simpler config); both work.
- [ ] AC-8: CHANGELOG entry under "Changed" — operator-facing behavior change note.

## P0 hard rules

- **P0-A1:** NO rewrite for `/api/*` (Next.js BFF routes — server-side credential handling stays Next.js-managed).
- **P0-A2:** NO rewrite for `/oauth/*` (Next.js OIDC callback — server-side cookie writing stays Next.js-managed).
- **P0-A3:** NO bypass of atlas auth. The rewrites preserve cookies + headers; atlas's slice-190 `jwtmw` middleware continues to gate every `/v1/*` request. AC-5 verifies this end-to-end.
- **P0-A4:** NO new env var. Reuse `ATLAS_HTTP_URL` — already wired by docker-compose and read by `web/lib/api.ts:apiBaseURL()` for the server-side BFF→backend hop.
- **P0-A5:** NO modification of reverse-proxy / NPM config in the repo. `deploy/` stays untouched apart from the optional docs note (AC-7).
- **P0-A6:** No vendor-prefixed test fixture tokens (`ghp_*`, `sk_*`, `AKIA*`, etc.). The Playwright spec uses the slice-201 global-setup-minted JWT via the existing `authedPage` fixture.

## Judgment decisions

Three decisions belong in the decisions log (`docs/audit-log/208-nextjs-rewrites-decisions.md`):

- **D1** — local-dev fallback for `ATLAS_HTTP_URL`. Recommendation: `http://localhost:8080` matches `cmd/atlas/main.go`'s default. Log a `console.warn` when the fallback fires so devs running `next dev` outside docker-compose see the heads-up.
- **D2** — honest CI-delta scan (per slice 202 D2; NOT the slice 143 D8 / slice 205 D7 false-positive class).
- **D3** — operator migration note for existing reverse-proxy path-routing setups.

## Out of scope (deferred)

- Migrating the BFF route handlers to share the same `ATLAS_HTTP_URL` resolution surface as the rewrites (already done implicitly — `apiBaseURL()` reads the same env).
- Rewrite rules for `/proto` or `/grpc` (atlas's gRPC surface is on a separate port, never traversed by Next.js).
- Per-tenant rewrite destinations (single-tenant atlas backend is the v1 invariant).

## See also

- [`web/proxy.ts`](../../web/proxy.ts) — slice 206's exemption set (the rewrites depend on the redirect-to-login NOT firing on `/v1/*`).
- [`web/lib/api.ts`](../../web/lib/api.ts) — the existing `ATLAS_HTTP_URL` consumer for server-side BFF calls.
- [`docs/operations/edge-deploy.md`](../operations/edge-deploy.md) — operator runbook updated by AC-7.
- [Next.js rewrites docs](https://nextjs.org/docs/app/api-reference/config/next-config-js/rewrites) — the API the implementation uses.
