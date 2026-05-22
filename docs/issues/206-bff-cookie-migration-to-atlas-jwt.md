# 206 — BFF auth cookie migration from `sa_session_token` to `atlas_jwt` (slice 197 follow-on)

**Cluster:** Frontend / Auth
**Estimate:** 1d
**Type:** AFK (mechanical migration with one design call at the call-site shape)
**Status:** `ready`
**Parent:** maintainer-surfaced 2026-05-22 via live debugging of the v1.14.0 deployed UI. Slice 197 ("final bearer retirement") removed the backend slice-034 `httpAuthMiddleware` mount but **did NOT migrate the BFF layer** off the legacy `sa_session_token` cookie. The auth-substrate-v2 spine (slices 187-198) intended the JWT-cookie end-state but the BFF call sites were never touched. Result: post-deploy, every authenticated UI surface is broken because the BFF reads the wrong cookie.

## Narrative

Three cookies are in play on a v1.14.0 deployment:

1. **`sa_session_token`** (legacy slice 034 api_keys bearer) — `SESSION_COOKIE` constant in `web/lib/auth.ts:5`. Read by `web/proxy.ts` (line 99) + ~30 BFF route handlers and layouts. Slice 197 retired the backend that issued this cookie; the BFF was never updated. **Effectively dead cookie that all BFF reads still depend on.**
2. **`atlas_session`** (legacy slice 034 OIDC session-id cookie) — `OIDC_SESSION_COOKIE` constant in `web/lib/auth.ts:15`. Backend `internal/auth/sessions/sessions.go:30` `CookieName = "atlas_session"` reads it. Marked for retirement per the `web/app/oauth/callback/route.ts:42-43` comment ("Slice 190 will retire atlas_session cleanly"). Currently NOT used by either the BFF or the new JWT flow — it's vestigial.
3. **`atlas_jwt`** (current, post-slice-187) — `ATLAS_JWT_COOKIE` constant in `web/app/oauth/callback/route.ts:44`. Set by the OIDC callback after slice 198's bootstrap completes. **This is the canonical session cookie going forward.** Holds the JWT access token issued by the slice 187-192 authorization server.

The break chain on v1.14.0 (reproduced live by maintainer + reproduced via direct `curl` against `192.168.1.246:3015` during this slice's design):

1. User completes OIDC login → callback writes `atlas_jwt` cookie ✅
2. Browser navigates to `/dashboard` → `proxy.ts` runs
3. `proxy.ts` checks `SESSION_COOKIE` (= `sa_session_token`) — **NOT present** (slice 197 retired its issuer; only `atlas_jwt` was issued by the callback)
4. `proxy.ts` 307s to `/login?from=/dashboard`
5. `/login` renders Next.js HTML 200 ✅
6. User attempts to access dashboard, gets bounced again — **infinite login loop**
7. Any direct curl to a `/v1/*` API endpoint (e.g. `/v1/me`, `/v1/anchors`) ALSO 307s because the matcher catches everything except `_next/*`. The browser's JS-side `fetch('/v1/me')` after a soft navigation returns the HTML login page, which `JSON.parse()` rejects, and the React error boundary surfaces a generic error UI — the "every component returns 500" symptom the maintainer reported.

**This slice ships the BFF migration.** Scope:

1. **Rename + retarget** the BFF auth cookie from `sa_session_token` to `atlas_jwt`. The cleanest implementation:
   - Update `web/lib/auth.ts`: change `SESSION_COOKIE = "sa_session_token"` to `SESSION_COOKIE = "atlas_jwt"` AND export `JWT_COOKIE = "atlas_jwt"` as the canonical name. Keep the old `SESSION_COOKIE` name as a back-compat alias for the migration period, OR rename it fully in a separate cleanup slice.
   - The 30+ call sites that import `SESSION_COOKIE` continue to compile + work; they now read the JWT cookie.
2. **Update `web/proxy.ts`** to ALSO exempt `/v1/*` + `/metrics` paths from the redirect-to-login behavior. These are API + telemetry surfaces, not HTML pages — they should fall through to whatever Next.js does (typically a rewrite or proxy to the Go backend). The proxy's job is to gate **HTML page access**, not API access (the Go backend's JWT validation is authoritative for API auth).
3. **Update BFF call sites** that pass the cookie value to the Go backend. The legacy pattern was `headers: { Cookie: \`${SESSION_COOKIE}=${bearer}\` }`. The post-slice-197 pattern uses `Authorization: Bearer <jwt>`. The implementing engineer should pick + document which pattern the BFF uses going forward (D1).
4. **Update tests** that hard-code `sa_session_token` (`web/proxy.test.ts:170, 239` etc.).
5. **Re-deploy + sanity test** that the user-reported infinite-login-loop is resolved.

**Scope discipline (what is OUT):**

- **Retiring `atlas_session` cookie** — that's slice 190's job per the existing comment. Not this slice.
- **Removing the legacy `sa_session_token` issuer** — already done by slice 197.
- **JSON-401-vs-HTML-307 on API routes** — out of scope for the FIRST iteration. If `proxy.ts` exempts `/v1/*` and they fall through to the Go backend, the backend returns 401 JSON. That's the right behavior; we don't need new code in proxy.ts to produce JSON 401. If a later iteration surfaces that something still returns HTML on a missing-auth API call, file as spillover.
- **Renaming `SESSION_COOKIE` to `JWT_COOKIE` semantically** — the import sites compile either way; defer the cosmetic rename to a separate cleanup slice to keep this PR diff narrow.

## Threat model

This is a **deployment-blocking bug fix**; threat model is dominated by the fact that the current state breaks all authenticated access (i.e., the bug ITSELF is a denial-of-service against legitimate users).

| STRIDE                | Threat                                                                                                                                                                                      | Mitigation                                                                                                                                                                                                                                                                                                            |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | The cookie rename could accidentally accept ANY cookie value as a valid session (lazy `if (cookie)` check that doesn't validate the JWT).                                                   | AC-6: `proxy.ts` MUST continue to delegate JWT validation to the Go backend; it does NOT independently parse or trust the cookie value. The proxy only checks "is the cookie present?" as a gate to load the HTML shell — the actual auth check happens on every API call to the Go backend (already implemented).    |
| **T** Tampering       | Migrating without a back-compat path could break already-deployed instances that have users with stale `sa_session_token` cookies in browsers.                                              | Documented in the operator notes: existing users will need to log in once after deploying this slice. Their `sa_session_token` cookie becomes inert. Force re-login is acceptable — slice 197 already retired the backend that would validate `sa_session_token`, so existing sessions are already broken in v1.14.0. |
| **R** Repudiation     | None — audit-log writes don't change.                                                                                                                                                       | n/a                                                                                                                                                                                                                                                                                                                   |
| **I** Info disclosure | If `proxy.ts` exempts `/v1/*` but the Go backend doesn't independently validate auth on every endpoint, this slice could accidentally expose authenticated APIs to unauthenticated callers. | AC-7: explicit verification that the Go backend rejects `GET /v1/me` with 401 (not 200) when called WITHOUT any JWT. This validates that the backend's auth boundary holds independent of the BFF's redirect behavior. Already-tested behavior per slice 191 — this AC is a regression check, not new behavior.       |
| **D** DoS             | n/a (existing state is already a self-DoS).                                                                                                                                                 | n/a                                                                                                                                                                                                                                                                                                                   |
| **E** EoP             | None.                                                                                                                                                                                       | n/a                                                                                                                                                                                                                                                                                                                   |

## Acceptance criteria

- [ ] **AC-1**: `web/lib/auth.ts`: `SESSION_COOKIE` constant value changed from `"sa_session_token"` to `"atlas_jwt"`. All 30+ existing import sites compile without modification.
- [ ] **AC-2**: `web/proxy.ts:99` reads `atlas_jwt` cookie (via the updated `SESSION_COOKIE` constant — no source change at proxy.ts:99 itself if the constant rename is done in lib/auth.ts).
- [ ] **AC-3**: `web/proxy.ts` exemptions extended to include `pathname.startsWith("/v1/")` AND `pathname === "/metrics"`. These paths fall through to Next.js's catch-all behavior (which proxies to the Go backend). Implementing engineer documents in D2 the exact set of path prefixes exempted.
- [ ] **AC-4**: BFF route handlers + layouts that pass the cookie value to the Go backend (`web/app/admin/layout.tsx`, `web/app/audit-log/layout.tsx`, `web/app/audit/layout.tsx`, `web/app/(authed)/layout.tsx`, `web/app/api/metrics/**`, `web/app/(authed)/controls/[id]/attest/page.tsx`, and any others surfaced by `grep -rn SESSION_COOKIE web/`) — the engineer picks + documents in D1 whether to keep the `Cookie: atlas_jwt=<value>` shape OR migrate to `Authorization: Bearer <value>`. The decision applies uniformly across all call sites.
- [ ] **AC-5**: `web/proxy.test.ts` updated — replace `sa_session_token` fixtures with `atlas_jwt`. All proxy tests pass.
- [ ] **AC-6**: Other vitest specs updated — `web/lib/api/bff.test.ts`, `web/app/api/metrics/*.test.ts`, any other spec that exercises the BFF cookie shape.
- [ ] **AC-7**: Backend regression check (integration test or doc): `curl -i http://localhost:<port>/v1/me` with NO Authorization header + NO cookie returns 401 JSON (not 307 HTML). This verifies the backend's auth boundary holds — already implemented; this AC ensures the slice's proxy.ts changes don't accidentally bypass it.
- [ ] **AC-8**: Playwright e2e: extend `web/e2e/auth-flow.spec.ts` (or analog) to verify the OIDC callback → `/dashboard` flow no longer infinitely loops. Specifically: after the callback sets `atlas_jwt`, navigation to `/dashboard` reaches the dashboard (200 + dashboard chrome rendered) instead of being 307'd back to `/login`.
- [ ] **AC-9**: CHANGELOG.md entry under "Fixed" describing the visible-to-operator behavior change ("authenticated UI now loads after OIDC login").
- [ ] **AC-10**: Operator notes in CHANGELOG: existing users must log in once after this deploys; their old `sa_session_token` cookies are inert.
- [ ] **AC-11**: Decisions log at `docs/audit-log/206-bff-cookie-migration-decisions.md` covering: D1 (cookie-vs-Authorization-header for the BFF→backend pattern); D2 (exact path-prefix exemption set in proxy.ts); D3 (CI-delta scan).

## Constitutional invariants honored

- **#6 RLS at DB layer**: not touched. Backend auth/RLS context-setting is unchanged.
- **No new auth surface**: this slice closes a migration gap, not opens new auth flow.
- **AI-assist boundary**: n/a — frontend wiring change.

## Canvas references

- None directly — this is post-canvas, slice 197-198 follow-on.

## Dependencies

- **#197** (final bearer retirement) — merged. The backend state this slice migrates the BFF onto.
- **#198** (OIDC first-install bootstrap) — merged. The OIDC callback is the source of `atlas_jwt`.
- **#187-192** (auth-substrate-v2 spine) — all merged. The end-to-end auth flow this slice completes the BFF migration into.

## Anti-criteria (P0 — block merge)

- **P0-A1**: DOES NOT introduce a new authentication mechanism. This slice only switches WHICH cookie name the existing JWT-bearer flow uses.
- **P0-A2**: DOES NOT bypass the Go backend's JWT validation on any `/v1/*` route. The backend's auth check remains authoritative; the BFF's role is to gate HTML page access, not API access.
- **P0-A3**: DOES NOT touch `atlas_session` cookie behavior — that's slice 190's scope (currently filed but not implemented). The implementing engineer leaves `atlas_session`-handling code unchanged.
- **P0-A4**: DOES NOT remove the legacy `SESSION_COOKIE` constant name — the rename is value-only. The constant identifier stays for back-compat across the 30+ import sites (a cosmetic rename can ship as a follow-up).
- **P0-A5**: DOES NOT use vendor-prefixed test fixture tokens.
- **P0-A6**: DOES NOT add new dependencies.

## Skill mix

- `web/proxy.ts` editor (Next.js middleware)
- BFF route handler editor (`web/app/**/route.ts`)
- Layout editor (`web/app/**/layout.tsx`)
- vitest spec author
- Playwright spec author (auth flow regression)

## Notes for the implementing agent

This is a **deployment-blocking bug fix.** The maintainer is actively trying to use the deployed v1.14.0 product and cannot get past the login screen because of the cookie-name drift this slice fixes. Prioritize correctness + clarity over polish — a follow-up cleanup slice can refine.

**D1 — cookie shape to send to backend.** Pre-slice-197 the BFF passed `Cookie: sa_session_token=<value>` to the Go backend, and the backend's slice-034 middleware extracted the cookie. Post-slice-197 the backend expects JWT in `Authorization: Bearer <value>`. The BFF call sites need to be updated to match. Recommended: switch to `Authorization: Bearer <jwt>` uniformly. Document the choice in D1; the diff is mechanical once decided.

**D2 — exact path exemptions in proxy.ts.** Currently exempts: `/login*`, `/_next*`, `/api/version`, `/api/install-state`, PUBLIC_STATIC_FILES. The slice MUST also exempt `/v1/*` (or `/v1/`). Engineer may also exempt `/metrics` (slice 121 OTel runtime). `/api/health` if it exists. Document exact list in D2.

**D3 — CI-delta scan honest discipline (per slice 202's correction pattern, NOT slice 143's D8 false-positive).** Recent regressions on D-claims about lint scans are documented in `~/.claude/projects/.../memory/feedback_engineer_d_decision_false_positive.md`. The engineer MUST run `pre-commit run --all-files` + `go vet ./...` + `cd web && npm run lint` + `cd web && npm run test` locally before claiming the scan clean. Specifically watch for:

- `errcheck` on any new `_, err := r.Cookies.Get(...)` pattern
- `unused` on any legacy SESSION_COOKIE import that becomes unreachable after the migration
- `staticcheck QF1001` on any boolean conditions touched

**Live deployment validation.** After this slice merges, the maintainer will pull main + redeploy v1.14.x. The success criterion: user can navigate `https://atlas.home.gmoney.sh/dashboard` post-OIDC-login without infinite redirect. This is the binary fix-confirmation gate.

**Spillover candidates** (do NOT fix in this PR — file as separate slices if surfaced):

- /v1/\* endpoints returning HTML on missing JWT (Go backend should return JSON 401; verify post-merge)
- atlas_session cookie cleanup (slice 190 scope)
- SESSION_COOKIE constant rename to JWT_COOKIE (cosmetic; defer)
- Additional BFF route handlers missed by the grep sweep

Provenance: surfaced 2026-05-22 via live debugging of `https://atlas.home.gmoney.sh` after the maintainer reported "almost every component returns 500" on v1.14.0. Direct curl reproduction during the debug session at `192.168.1.246:3015/v1/me` returned 307 → /login (instead of 401 JSON), confirming the BFF middleware as the root cause.
