# 208 — Next.js rewrites for /v1/, /health, /metrics (deployment-portable backend routing)

**Cluster:** Frontend / Deploy
**Estimate:** 0.25d
**Type:** AFK
**Status:** `ready`
**Parent:** maintainer-surfaced 2026-05-22 during atlas-edge provisioning. Discovered that **production atlas.home.gmoney.sh has never had functional `/v1/*` routing** — NPM forwards everything to web (Next.js) and Next.js has no route at `/v1/*`. Slice 206 fixed the redirect-loop symptom; this slice fixes the underlying routing gap.

## Narrative

The atlas deployment runs two services: `atlas` (Go backend, internal port 8080) and `web` (Next.js BFF + UI, internal port 3000). Browsers + curl callers need to reach BOTH:

- `/login`, `/dashboard`, `/admin/*`, `/api/*` (BFF routes), `/oauth/*` → web Next.js
- `/v1/*`, `/health`, `/metrics` → atlas Go backend

In every existing deployment (stable + the brand-new edge), the reverse proxy (NPM, Cloudflare, K8s Ingress, whatever) forwards EVERYTHING to web. No path-based split. Net result: when the browser-side dashboard JS fetches `/v1/me`, it hits Next.js's catch-all and returns 404 (or pre-slice-206, the `proxy.ts` middleware caught it first and 307'd to `/login`). **The dashboard's data layer has never worked end-to-end in deployment.**

The maintainer's atlas-edge provisioning surfaced this. NPM was configured with path-based routing (`/v1/` → atlas:8180, `/metrics` → atlas:8180, default → web:3180) as a workaround, and the dashboard then loaded correctly. But that workaround requires every deployment topology (Cloudflare Tunnel, K8s Ingress, Caddy, raw `docker compose up`) to replicate the same path-routing config — and the docker-compose.bundled.yml + docker-compose.edge.yml templates expose no such config.

**This slice ships the in-repo fix**: Next.js rewrites in `web/next.config.ts`. Browser fetches `/v1/me` → Next.js dev/prod server proxies internally to `${ATLAS_HTTP_URL}/v1/me`. `ATLAS_HTTP_URL` is already an env var (the compose templates set it to `http://atlas:8080`), so this generalizes existing knowledge without introducing new config.

After this lands + Watchtower pulls the new `:edge` image, atlas-edge.home.gmoney.sh's dashboard becomes fully functional WITHOUT the NPM path-routing workaround. The next v1.16.0 release will fix the same thing on stable.

**Scope discipline (what is OUT):**

- **NPM/reverse-proxy migration** — operators who already configured path-routing can keep it (it's harmless redundant overhead, ~1 fewer hop). Document the option to simplify the reverse-proxy config but don't require migration.
- **`/api/*` rewrites** — those are BFF routes, served by Next.js. NOT proxied to atlas.
- **`/oauth/*` rewrites** — same, Next.js-served.
- **HTTPS / certificate management** — operator's reverse proxy handles TLS.
- **WebSocket support** — atlas doesn't currently expose any; if added later, that's a follow-on.

## Threat model

| STRIDE                | Threat                                                                                                                                                                                                     | Mitigation                                                                                                                                                                                                                                                    |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | Rewrite could be exploited to access atlas on behalf of unauthenticated callers if Next.js fails to forward credentials correctly.                                                                         | Next.js rewrites preserve request headers including cookies + Authorization by default (next.js framework guarantee). AC-4: integration test verifies an authenticated browser request that includes `atlas_jwt` cookie reaches atlas with the cookie intact. |
| **T** Tampering       | Operator could set `ATLAS_HTTP_URL` to a malicious upstream.                                                                                                                                               | AC-5: rewrite target is read from env at server-startup; not request-time. Misconfiguration affects ALL backend calls (BFF routes already do this, so the new attack surface is zero).                                                                        |
| **R** Repudiation     | None — audit-log writes happen on atlas; this is request routing only.                                                                                                                                     | n/a                                                                                                                                                                                                                                                           |
| **I** Info disclosure | None — rewrites preserve the existing auth boundary; atlas already enforces 401 on unauthenticated `/v1/*` calls (just confirmed live: `curl -sI http://atlas-edge.home.gmoney.sh/v1/anchors` → 401 JSON). | AC-6 regression check: unauthenticated `/v1/me` via the rewritten path returns 401 JSON, not 200.                                                                                                                                                             |
| **D** DoS             | Rewriting adds one in-process hop per request. Negligible (~1ms LAN).                                                                                                                                      | n/a                                                                                                                                                                                                                                                           |
| **E** EoP             | None.                                                                                                                                                                                                      | n/a                                                                                                                                                                                                                                                           |

## Acceptance criteria

- [ ] **AC-1**: `web/next.config.ts` adds an `async rewrites()` returning three rules: `/v1/:path*` → `${ATLAS_HTTP_URL}/v1/:path*`, `/health` → `${ATLAS_HTTP_URL}/health`, `/metrics` → `${ATLAS_HTTP_URL}/metrics`.
- [ ] **AC-2**: `ATLAS_HTTP_URL` is read from env at startup. Local-dev fallback to `http://localhost:8080` when unset (matches the existing `web/lib/api/bff.ts` BFF pattern; engineer confirms the existing fallback's exact host:port via D1).
- [ ] **AC-3**: vitest regression in `web/lib/next-config.test.ts` (or analog) confirming the rewrites config shape. Specifically: with `ATLAS_HTTP_URL=http://atlas:8080` set, `rewrites()` returns the three expected rules.
- [ ] **AC-4**: Playwright e2e: after authenticated login, `GET /v1/me` from the browser context returns the user JSON (200, not 404 from Next.js's catch-all and not 401 because the session cookie should be present). Extends `web/e2e/auth-flow.spec.ts` or analog.
- [ ] **AC-5**: Backend regression check (Playwright or integration test): unauthenticated `GET /v1/anchors` via the rewritten path returns 401 JSON. Verifies the rewrite doesn't accidentally bypass atlas's auth boundary.
- [ ] **AC-6**: Health check: `GET /health` via the rewritten path returns `{"status":"ok","db":"ok"}` (or whatever atlas's /health currently emits) when atlas is reachable.
- [ ] **AC-7**: Updates `docs/operations/edge-deploy.md` to remove the manual NPM path-routing recommendation. Add a one-line note: "If you previously added path-routing in your reverse proxy for /v1/, /health, /metrics, you can remove it — Next.js now handles this internally."
- [ ] **AC-8**: CHANGELOG entry under "Changed" describing the deployment behavior change. Operator note: existing path-routing configs continue to work (harmless redundant overhead).

## Constitutional invariants honored

- **No new auth surface**: rewrites preserve existing cookies + Authorization headers; atlas's JWT validation remains authoritative.
- **No backend code change**: atlas Go server unchanged.
- **AI-assist boundary**: n/a (deploy/wiring change).

## Canvas references

- None — operational wiring.

## Dependencies

- **#206** (BFF cookie migration) — merged. The `proxy.ts` middleware exempts `/v1/*` already (slice 206 AC-3); without that exemption, the new rewrites would never get a chance to fire.
- **#072** (version string in UI) — merged. Provides the VersionFooter that lets operators verify which commit is running.

## Anti-criteria (P0 — block merge)

- **P0-A1**: DOES NOT rewrite `/api/*` to atlas. Those are Next.js BFF routes; keep them server-side at web.
- **P0-A2**: DOES NOT rewrite `/oauth/*` or any auth-callback paths. Those are Next.js-served per slice 198.
- **P0-A3**: DOES NOT bypass atlas's authentication. The rewritten request must include the same cookies/headers as a direct request would.
- **P0-A4**: DOES NOT change `ATLAS_HTTP_URL` semantics. Same env var, same value, same expected shape (`http://host:port`, no trailing slash).
- **P0-A5**: DOES NOT modify the operator's reverse-proxy config. The change is in-repo only; deployments don't need to re-configure NPM/Cloudflare/etc.
- **P0-A6**: DOES NOT use vendor-prefixed test fixture tokens.

## Skill mix

- `web/next.config.ts` editor
- vitest spec author (config-shape test)
- Playwright spec author (browser-context fetch through the rewrite)

## Notes for the implementing agent

This is a **one-file fix** with regression tests. Keep the diff minimal.

**D1 — local-dev fallback.** `ATLAS_HTTP_URL` is unset in `web/` local dev (you run `npm run dev` separately from `cd .. && go run cmd/atlas/main.go`). Pick a fallback:

- `http://localhost:8080` (atlas default port from `cmd/atlas/main.go`)
- Read from `.env.development.local` if it exists
- Or fail loudly if unset

Recommended: fallback to `http://localhost:8080` matching atlas's default + log a warning on startup if unset.

**D2 — CI-delta scan (per slice 202 D2 honest-scan precedent; NOT slice 143 D8 / slice 205 D7 false-positive class).** Specifically verify:

- `npm run build` works (next.config rewrites must serialize correctly)
- `npm run lint` clean (typecheck the rewrite return shape)
- `npm run test` clean (the new vitest spec)
- `npm run test:e2e` — the new Playwright spec exercises the rewrite end-to-end

**D3 — operator migration note.** Operators with existing NPM/reverse-proxy path-routing can keep it (one fewer hop, marginal perf benefit) OR remove it (simpler config). CHANGELOG + docs/operations/edge-deploy.md should make this explicit so operators don't panic.

**Live verification post-merge:**

- Watchtower on Unraid (atlas-edge.home.gmoney.sh) auto-pulls `:edge` ~5 min after PR #N merges
- Maintainer browses to atlas-edge.home.gmoney.sh/dashboard → dashboard loads with real data (slice 206 + slice 208 = full end-to-end fix)
- Next v1.16.0 stable release applies the same fix to atlas.home.gmoney.sh

Provenance: filed 2026-05-22 immediately after live-verifying that NPM path-routing on atlas-edge resolves the /v1/\* gap. Choosing the Next.js-rewrite approach over a per-deployment reverse-proxy fix because it ships portable across ALL deployment topologies (docker-compose / K8s / Cloudflare Tunnel / Caddy / etc.) with zero operator config.
