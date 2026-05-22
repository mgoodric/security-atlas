# 189 — OAuth `/oauth/authorize` + PKCE + frontend OAuth client integration

**Cluster:** Backend / Auth + Frontend
**Estimate:** 2-3d
**Type:** JUDGMENT
**Status:** `not-ready` (third slot in the auth-substrate-v2 spine; gate: 188 merged)

## Narrative

Slices 187 (foundation) + 188 (`/oauth/token` + token-exchange + client-credentials) covered the **machine-client** half of the OAuth AS. Slice 189 lights up the **browser** half: `GET /oauth/authorize` (RFC 6749 §4.1 Authorization Code grant) hardened with **PKCE** (RFC 7636) for public clients (the Next.js frontend), plus the frontend integration that drives it.

This slice is what turns the atlas web UI from "authenticates via OIDC and gets a bearer token from a side handler" into "authenticates via OIDC then completes an OAuth Authorization Code + PKCE flow to receive a JWT it'll use on every `/v1/*` call". The bearer-token-from-OIDC-callback shape from slice 034 still works (slice 190 ships the JWT middleware that REPLACES it); this slice introduces the new path alongside the old one.

**What this slice ships:**

1. **`GET /oauth/authorize` HTTP handler** at `internal/api/oauth/authorize.go` accepting RFC 6749 §4.1.1 parameters: `response_type=code`, `client_id`, `redirect_uri`, `scope`, `state`, `code_challenge`, `code_challenge_method=S256` (PKCE per RFC 7636 §4.3 — `plain` rejected). Validates `client_id` against `oauth_clients` + the `redirect_uri` matches the client's registered URIs. If the request lacks an active atlas session cookie (`atlas_session` from slice 034 OIDC RP), redirect to OIDC login flow; on return, complete authorization. On success, generate an `auth_code` (32B random + base64url), store in `oauth_auth_codes` with PKCE challenge + redirect_uri + caller's user identity + 60-second TTL, redirect browser to `redirect_uri?code=<code>&state=<state>`.
2. **Auth-code redemption** extends slice 188's `/oauth/token` handler with a NEW `grant_type=authorization_code` path. Form params: `code`, `code_verifier`, `redirect_uri`, `client_id`. Validates: code exists + not expired + not previously used (one-shot); `code_verifier` SHA256-base64url matches `code_challenge`; `redirect_uri` matches the one used at authorize-time; `client_id` matches. On success, mint JWT carrying the authenticated user's identity (resolved via slice 034 OIDC-RP session → user → atlas user record → roles + tenants). Mark the code consumed.
3. **`oauth_auth_codes` table** (NEW migration): `code TEXT PK · client_id TEXT · redirect_uri TEXT · code_challenge TEXT · code_challenge_method TEXT · user_id UUID · idp_issuer TEXT · idp_subject TEXT · current_tenant_id UUID · available_tenants UUID[] · roles JSONB · super_admin BOOL · created_at · consumed_at NULL · expires_at`. NOT tenant-scoped (auth codes are platform-global, short-lived). Index `(expires_at)` for cleanup.
4. **Background sweeper** in `cmd/atlas/main.go` (or as a goroutine in the auth package) DELETEs auth codes where `expires_at < now() - interval '1 hour'` every 5 minutes. Codes are one-shot anyway; this just bounds table growth.
5. **Frontend OAuth client** at `web/lib/auth/oauth-client.ts`: TypeScript module that drives the PKCE Authorization Code flow from the browser. Generates `code_verifier` (32B random) + `code_challenge = base64url(sha256(code_verifier))`. Redirects to `/oauth/authorize` with PKCE parameters. On `/oauth/callback` (slice 034's existing callback route, extended), exchanges the code for a JWT via `POST /oauth/token`. Stores the JWT in a httpOnly session cookie (slice 034's `atlas_session` is reused as the JWT carrier OR a new cookie name — engineer picks at impl, document the choice).
6. **Frontend session-cookie shape decision**: D1 will lock whether the OAuth-issued JWT replaces the slice-034 cookie payload OR rides alongside it. The simplest path: rename existing cookie OR extend it. Document the deprecation path for slice 190 (which retires the legacy cookie reads).
7. **Discovery doc update**: slice 187's `openid-configuration` advertises `authorization_endpoint`, `response_types_supported = ["code"]`, `code_challenge_methods_supported = ["S256"]`, `grant_types_supported` extended with `authorization_code`.

**SCOPE DISCIPLINE — what's deliberately out:**

- JWT validation middleware on `/v1/*` (the new tokens still have no hot-path consumer) — slice 190.
- `/oauth/revoke` + `/oauth/introspect` — slice 190.
- SDK migration to use the OAuth endpoints — slice 191.
- Frontend tenant-switcher dropdown — slice 192.
- Refresh-token grant — v3 deferred (per slice 188 P0-188-6).
- Implicit grant (RFC 6749 §4.2) — explicitly NOT shipping; PKCE-only browser flow per modern OAuth 2.1 guidance.
- `response_type=code id_token` / `code id_token token` hybrid flows — out of scope; we're OAuth, not OIDC-provider.
- mTLS client authentication — v3 deferred.

## Threat model

**S — Spoofing.** Caller forges an auth code or replays one.

- Mitigation: codes are 32B random (256 bits entropy). One-shot via `consumed_at` mark in a SELECT FOR UPDATE row. Replay → 400 + `error=invalid_grant`.

**T — Tampering.** Caller swaps `redirect_uri` between authorize-time and redeem-time to exfiltrate the code to an attacker-controlled URL.

- Mitigation: `redirect_uri` stored at code issuance + validated at redemption. Mismatch → 400.
- Mitigation: registered redirect URIs (per-`client_id`) checked at authorize-time. Only registered URIs accepted. Initial allowed list: the atlas instance's web frontend URL + `http://localhost:*` for self-host dev.

**T — Tampering.** PKCE `code_verifier` tampered between request initiation + redemption.

- Mitigation: `code_challenge_method=S256` enforced (the `plain` method is rejected — accepted only when verifier transport is secure, but we require S256 always per OAuth 2.1 §4.5). Verifier hashed at redemption + compared to stored challenge.

**R — Repudiation.** Authorization-code issuance + redemption need audit trail.

- Mitigation: every redemption (success OR failure) writes to an extension of slice 188's `oauth_token_exchanges` table OR a new `oauth_authorization_events` table. Engineer picks at impl (decisions log D2). Lean toward extending: reduces table proliferation.

**I — Information disclosure.** Auth code leaked via referer header to attacker.

- Mitigation: redirect URI uses POST or fragment instead of query string... NO — RFC 6749 §4.1.2 specifies query string for the auth code. Mitigation is short TTL (60s) + one-shot consumption + HTTPS-only for the redirect_uri (registered URIs validated to start with `https://` OR `http://localhost`).

**D — Denial of service.** Authorize endpoint hammered.

- Mitigation: per-IP rate limit at the handler (token bucket; default 60/min/IP). Returns 429.
- Mitigation: auth codes table bounded by sweeper + short TTL.

**E — Elevation of privilege.** A user's auth code is intercepted + redeemed by an attacker who didn't go through OIDC.

- Mitigation: PKCE verifier is generated in the user's browser and never leaves it until redemption. Even if the code is intercepted, the verifier isn't. Verifier mismatch → 400.

**Verdict:** `has-mitigations`. PKCE is the load-bearing primitive for public-client safety; AC-9 + AC-10 cover.

## Acceptance criteria

### `/oauth/authorize` HTTP handler

- **AC-1.** NEW handler `internal/api/oauth/authorize.go` mounted at `GET /oauth/authorize` (unauthenticated entry point; uses session cookie). Accepts RFC 6749 §4.1.1 query parameters: `response_type` (MUST be `"code"`), `client_id`, `redirect_uri`, `scope` (ignored for v1; reserved), `state`, `code_challenge`, `code_challenge_method` (MUST be `"S256"`; `"plain"` rejected with 400 + `error=invalid_request`).
- **AC-2.** Validates `client_id` against `oauth_clients` (slice 188's table). Unknown → 400 + `error=unauthorized_client`. Disabled → 400 + `error=unauthorized_client`.
- **AC-3.** Validates `redirect_uri` against a NEW `oauth_client_redirect_uris` table (NEW migration; columns: `client_id TEXT · redirect_uri TEXT · created_at`; UNIQUE on `(client_id, redirect_uri)`). Mismatch → 400 + `error=invalid_request`. **MUST validate before the browser redirect** — never redirect to an unregistered URI even if the request asks for one (open-redirect prevention).
- **AC-4.** If request lacks `atlas_session` cookie OR the cookie is expired: redirect to OIDC login flow (slice 034 entry point). On OIDC return, the OAuth flow resumes at the authorize endpoint with the original parameters (preserved via a short-lived `oauth_resume_token` cookie or query-state encoding; engineer picks at impl).
- **AC-5.** On success: generate `auth_code` = 32B random base64url. Insert row in `oauth_auth_codes` with all the user's identity claims resolved from the OIDC session. Redirect 302 to `<redirect_uri>?code=<auth_code>&state=<state>`. TTL on the code: 60 seconds (per OAuth 2.1 §4.1.3 recommendation).

### Auth-code redemption (extends slice 188's `/oauth/token`)

- **AC-6.** Slice 188's `/oauth/token` handler extended with `grant_type=authorization_code` path.
- **AC-7.** Validates form params: `code` (must exist + not expired + `consumed_at IS NULL`), `code_verifier`, `redirect_uri` (must match the one used at authorize), `client_id` (must match the one used at authorize).
- **AC-8.** Marks code consumed via `UPDATE oauth_auth_codes SET consumed_at = now() WHERE code = $1 AND consumed_at IS NULL RETURNING *` — RETURNING captures the one-shot semantics. If RETURNING returns no rows, the code was already consumed → 400 + `error=invalid_grant`.
- **AC-9.** PKCE verification: `code_challenge_method` MUST be `S256`. Compute `expected_challenge = base64url(sha256(code_verifier))`. Constant-time compare to stored `code_challenge`. Mismatch → 400 + `error=invalid_grant`.
- **AC-10.** PKCE is REQUIRED for browser clients (the default — no `client_secret` accepted in this path). For machine clients with secrets, `client_credentials` path is the correct flow. Document this in ADR addendum.
- **AC-11.** Mints JWT via slice 187's `tokensign.Sign` carrying the user's identity from the consumed `oauth_auth_codes` row: `sub` = user UUID, `atlas:idp_issuer`, `atlas:current_tenant_id`, `atlas:available_tenants`, `atlas:roles`, `atlas:super_admin`. Response shape per slice 188 AC-8.

### `oauth_auth_codes` + `oauth_client_redirect_uris` migrations

- **AC-12.** NEW migration creates `oauth_auth_codes` (columns per Narrative §3). Index `(expires_at)`. NOT tenant-scoped. NOT RLS-protected (auth codes are platform-global, short-lived).
- **AC-13.** NEW migration creates `oauth_client_redirect_uris` (per AC-3). UNIQUE constraint on `(client_id, redirect_uri)`. Reversible.
- **AC-14.** CLI command `atlas oauth add-redirect-uri <client_id> <redirect_uri>` for operator-side URI registration. Rejects URIs without `https://` prefix UNLESS the URI starts with `http://localhost` (self-host dev allowance, documented).

### Auth-code sweeper

- **AC-15.** Goroutine in `cmd/atlas/main.go` (started after auth subsystem init) DELETEs auth codes where `created_at < now() - interval '1 hour'` every 5 minutes. 1-hour grace beyond the 60s TTL avoids races with in-flight redemptions. Logged at INFO with count deleted.

### Frontend OAuth client

- **AC-16.** NEW TypeScript module `web/lib/auth/oauth-client.ts` with: `generateCodeVerifier() string` (32B random base64url), `generateCodeChallenge(verifier) string` (base64url(sha256(verifier))), `initLoginFlow() void` (redirects browser to `/oauth/authorize` with PKCE), `completeLoginFlow(code, verifier) Promise<JWT>` (POSTs to `/oauth/token`).
- **AC-17.** Frontend callback route `web/app/oauth/callback/route.ts` (Next.js route handler) consumes `code` + `state` from query string, retrieves the stored `code_verifier` (from sessionStorage), POSTs to `/oauth/token`, stores the resulting JWT in a httpOnly cookie via `Set-Cookie` from the route handler.
- **AC-18.** D1 (decisions log): JWT cookie name + lifetime. Engineer picks: extend `atlas_session` (reuses slice 034 infrastructure; deprecation path is clear) OR new `atlas_jwt` cookie (cleaner separation; needs migration in slice 190). Document the trade-off.

### Discovery doc

- **AC-19.** Slice 187's `openid-configuration` handler advertises `authorization_endpoint`, `response_types_supported = ["code"]`, `code_challenge_methods_supported = ["S256"]`, and adds `authorization_code` to `grant_types_supported`.

### Tests

- **AC-20.** Go integration tests under `internal/api/oauth/`: full GET `/oauth/authorize` → `oauth_auth_codes` insert → redirect 302 path. Authenticated session via slice 034 test helpers.
- **AC-21.** Go integration tests for the `authorization_code` redemption path on `/oauth/token`: PKCE happy path (matching verifier) + PKCE rejection (mismatched verifier) + code reuse rejection (`consumed_at IS NOT NULL`) + redirect_uri mismatch rejection.
- **AC-22.** Frontend vitest tests for `web/lib/auth/oauth-client.ts`: verifier/challenge round-trip, S256 hashing, sessionStorage round-trip.
- **AC-23.** Playwright e2e test for the full browser flow: OIDC login → authorize → callback → JWT cookie set → can call `/v1/*` (gated on slice 190's middleware; spec marked `test.skip` until slice 190 lands).

### Decisions log + ADR + docs

- **AC-24.** JUDGMENT decisions log at `docs/audit-log/189-oauth-authorize-pkce-decisions.md`: D1 (cookie name + lifetime), D2 (extend audit table vs new audit table), D3 (PKCE-only enforcement rationale), D4 (registered redirect URIs — initial allowed list).
- **AC-25.** ADR-0003 addendum: authorize endpoint shape; PKCE-required policy; redirect-URI registration discipline.
- **AC-26.** `CHANGELOG.md` entry. `docs/openapi.yaml` updated.

## Constitutional invariants honored

- **Tenant isolation at DB layer** (invariant #6): `oauth_auth_codes` is non-tenant by design (auth codes are platform-global); the JWT minted from a redemption carries the tenant scope per the user's identity. Validation that the user's `available_tenants` is respected happens at redemption time and at the JWT validation middleware (slice 190).
- **Audit trail** for security-sensitive events: every authorize + redeem event logged.

## Canvas references

- `Plans/canvas/11-open-questions.md` OQ #21 RESOLVED (Reading D).
- `docs/adr/0003-oauth-authorization-server.md` (slice 187 foundation).
- Slice 034 (OIDC RP) — this slice extends the OIDC-session-to-OAuth-code bridge.

## Dependencies

- **#187** OAuth AS scaffolding. MERGED at `ac42517`.
- **#188** `/oauth/token` + token-exchange. **Gate: 188 must be `merged` before 189 flips to `ready`.**
- **#034** OIDC RP + sessions. MERGED. Slice 189 reuses the session cookie + login flow.
- **#002** Slice 002 integration test helpers (for new tables).

## Anti-criteria (P0 — block merge)

- **P0-189-1.** Does NOT support `code_challenge_method=plain`. PKCE S256 only. Browser clients ship S256; the `plain` method exists for legacy compat only.
- **P0-189-2.** Does NOT skip redirect_uri registration validation. Open redirects are a critical OAuth vulnerability. The handler MUST reject unregistered redirect URIs at authorize-time.
- **P0-189-3.** Does NOT allow auth code reuse. The `consumed_at IS NULL → UPDATE RETURNING` pattern enforces one-shot semantics.
- **P0-189-4.** Does NOT introduce an Implicit grant (`response_type=token`). OAuth 2.1 deprecates implicit; we ship PKCE-only.
- **P0-189-5.** Does NOT implement a refresh-token grant. v3 deferred.
- **P0-189-6.** Does NOT introduce JWT validation middleware on `/v1/*` routes. Slice 190 owns that.
- **P0-189-7.** Does NOT alter slice 188's `/oauth/token` `client_credentials` or `token-exchange` paths beyond ADDING the new `authorization_code` dispatch. Existing AC-5-AC-15 from slice 188 stay green.
- **P0-189-8.** Frontend stores `code_verifier` in `sessionStorage`, NOT `localStorage`. Verifier must not persist beyond the tab session.
- **P0-189-9.** The frontend JWT-bearing cookie MUST be `HttpOnly`, `Secure` (production), `SameSite=Lax`. No JS access.
- **P0-189-10.** Does NOT modify slice 187's keystore + tokensign packages. All signing goes through `tokensign.Sign` unchanged.

## Skill mix (3-5)

- `grill-with-docs`
- `tdd`
- `database-designer`
- `security-review` (OAuth + PKCE is high-risk surface)
- `simplify`

## Notes for the implementing agent

### PKCE is the load-bearing primitive for public clients

The Next.js frontend has no `client_secret` it can safely hold. PKCE is what makes public-client OAuth safe. Verifier mismatch MUST 400 — never minimize this check.

### Coordinate with slice 034's session model

Slice 034 has an `atlas_session` cookie carrying the OIDC subject. Slice 189 has a choice (D1): reuse that cookie as the JWT carrier OR introduce a new cookie. Either works; the trade-off is in slice 190's middleware design. Lean toward "extend existing cookie" — fewer concepts to retire later.

### Redirect URI registration discipline

The `oauth_client_redirect_uris` table is the open-redirect gate. Register the atlas instance's web URL during operator setup. The CLI command + a one-time bootstrap step at `just self-host-up` should auto-register `http://localhost:3000` and the configured instance URL. Don't ship without this — an unregistered redirect URI request is a silent security regression.

### Spillover candidates

- Refresh-token grant (RFC 6749 §6). Out of scope by P0-189-5.
- Dynamic client registration (RFC 7591). Out of scope; v3+.
- `prompt=none` silent-renewal flow. Out of scope; not needed without refresh tokens.

### Provenance

Filed 2026-05-21 as auth-substrate-v2 spine slot 3.
