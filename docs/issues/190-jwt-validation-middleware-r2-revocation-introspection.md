# 190 — JWT validation middleware on `/v1/*` + R2 eviction + `/oauth/revoke` + `/oauth/introspect`

**Cluster:** Backend / Auth
**Estimate:** 2-3d
**Type:** JUDGMENT
**Status:** `not-ready` (fourth slot in the auth-substrate-v2 spine; gate: 189 merged)

## Narrative

Slices 187 (foundation), 188 (`/oauth/token` for machine + token-exchange), 189 (`/oauth/authorize` + PKCE + frontend client) shipped the **issuance** half of the OAuth AS. Slice 190 ships the **consumption** half: JWT validation middleware on every `/v1/*` route + the `/oauth/revoke` (RFC 7009) + `/oauth/introspect` (RFC 7662) endpoints + the R2 eviction primitive.

**This is the cutover slice.** Before slice 190, the new JWTs minted by 188-189 have no hot-path consumer; `/v1/*` routes still use slice 034's bearer-token middleware (the legacy `credstore.Credential` path). After slice 190, `/v1/*` routes accept the OAuth JWTs as the primary auth path — the legacy bearer-token path can stay as a parallel auth method during the migration window (slice 191 retires it).

The **R2 eviction** primitive replaces what slice 141 was supposed to ship in a much heavier shape. Under the OAuth AS commitment, R2 becomes a **claim check**: middleware reads the JWT's `atlas:current_tenant_id` + `atlas:available_tenants[]` claims and decides whether to allow the request based purely on those claims — no DB lookup per request. If a tenant is removed from a user's `available_tenants[]` (e.g., they get removed from a tenant by an admin), the response is "eventual" — the user's existing tokens still work until they expire (1h). To force-evict immediately, the admin must explicitly revoke the user's tokens via `/oauth/revoke`. This is the standard OAuth eviction shape.

**What this slice ships:**

1. **JWT validation middleware** at `internal/auth/jwtmw/middleware.go` that:

   - Reads bearer token from `Authorization: Bearer <jwt>` header OR JWT-bearing cookie (from slice 189).
   - Verifies signature via slice 187's `tokensign.Verify`.
   - Validates claims via slice 187's `jwt.Validate` (exp, iat, iss, aud, current_tenant in available_tenants).
   - Checks revocation list (per AC-7 below).
   - Sets `app.current_tenant` Postgres GUC for RLS — bridges to existing `internal/tenancy/` plumbing.
   - Sets request context with the verified `jwt.AtlasClaims`.
   - Returns 401 + `WWW-Authenticate: Bearer realm="atlas", error="invalid_token"` on any failure.

2. **Middleware mounted on `/v1/*`** in `internal/api/httpserver.go`. Coexists with slice 034's bearer-token middleware (the legacy path) for the migration window. Resolution priority: JWT first, then legacy bearer token. Slice 191 retires the legacy path.

3. **`/oauth/revoke` endpoint** (RFC 7009) at `internal/api/oauth/revoke.go`. POST form params: `token` + `token_type_hint=access_token`. Authenticates the revoker (client_credentials OR the token's own subject can revoke their own tokens). Inserts the token's `jti` into a `oauth_revoked_tokens` table with `revoked_at` + `expires_at`. Returns 200 on success per RFC 7009 §2.2 (even for unknown tokens — silent acknowledgment, no information disclosure).

4. **`/oauth/introspect` endpoint** (RFC 7662) at `internal/api/oauth/introspect.go`. POST form params: `token`. Authenticates the inspector via client_credentials. Returns RFC 7662 §2.2 response: `active: bool`, `sub`, `aud`, `iss`, `exp`, `iat`, `client_id`, `username`, and the custom `atlas:*` claims. Returns `{"active": false}` for revoked / expired / unknown tokens.

5. **`oauth_revoked_tokens` table** (NEW migration): `jti TEXT PK · revoked_at TIMESTAMPTZ NOT NULL · expires_at TIMESTAMPTZ NOT NULL · revoked_by TEXT NOT NULL`. NOT tenant-scoped (revocation is identity-scoped, not tenant-scoped — a single user can be in multiple tenants but their token is global). Index `(expires_at)` for sweeper.

6. **Revocation sweeper** in `cmd/atlas/main.go`: every 5 minutes, DELETE rows where `expires_at < now()`. After token expiry, the rejection is automatic via `jwt.Validate`'s `exp` check; the revocation table only needs to cover the period between revocation and natural expiry.

7. **R2 eviction integration test** (load-bearing): test setup creates a user in tenants A + B, mints a JWT scoped to A, removes the user from A via direct DB update (simulating an admin action), asserts that subsequent requests with the existing JWT still return 200 until the token expires (eventual eviction). Then revokes the JWT via `/oauth/revoke` and asserts subsequent requests return 401. This is the slice's load-bearing semantic.

8. **Discovery doc update**: slice 187's `openid-configuration` advertises `revocation_endpoint`, `introspection_endpoint`, `revocation_endpoint_auth_methods_supported = ["client_secret_post"]`.

**SCOPE DISCIPLINE — what's deliberately out:**

- SDK migration to use OAuth tokens — slice 191.
- CLI device-code flow — slice 191.
- Frontend tenant-switcher dropdown — slice 192.
- Refresh-token grant — v3 deferred.
- mTLS client authentication — v3 deferred.
- Retiring the slice 034 legacy bearer-token middleware — slice 191. This slice's middleware adds JWT validation; the old path stays.
- DPoP (RFC 9449) — v3 optional.

## Threat model

**S — Spoofing.** Forged JWT, expired JWT replay, swapped-signature JWT.

- Mitigation: signature verification via `tokensign.Verify` (slice 187's RFC 8725 §2.1 algorithm allowlist on parse). Expired/forged → 401.

**T — Tampering.** JWT mutated in flight.

- Mitigation: signature validation rejects any mutation. Standard.

**R — Repudiation.** Revocation events need audit trail.

- Mitigation: every `/oauth/revoke` call writes to `oauth_revoked_tokens` with `revoked_by`. Successful revocations also written to slice 188's `oauth_token_exchanges`-style audit (or a new `oauth_revocation_events` table; engineer picks at impl).

**I — Information disclosure.** Introspection endpoint reveals token contents to attacker.

- Mitigation: introspection requires `client_credentials` authentication. Only registered OAuth clients can introspect. Document this as the "resource server" model in ADR.

**D — Denial of service.** Revocation list bloats indefinitely.

- Mitigation: sweeper deletes rows past `expires_at`. Even at 10K tokens/day with 1-hour expiry, the table holds ~415 rows at steady state.
- Mitigation: middleware checks revocation via PK lookup (`jti TEXT PK`) — index hit, O(1).

**E — Elevation of privilege.** Middleware accepts a JWT whose `current_tenant_id` is NOT in `available_tenants[]`.

- Mitigation: slice 187's `jwt.Validate` already checks this (the `current_tenant_id != Nil → must be in available_tenants` invariant). Middleware MUST call Validate; AC-5 enforces.

**E — Elevation of privilege.** Middleware accepts a JWT with `super_admin=true` from a token-exchange that elevated.

- Mitigation: slice 188 P0-188-4 forbids elevation. The trust boundary is the issuance side; the middleware trusts what the verified signature says.

**Verdict:** `has-mitigations`. The middleware is conceptually simple (verify signature → check revocation → set GUC → set ctx). The complexity is in the integration test surface.

## Acceptance criteria

### JWT validation middleware

- **AC-1.** NEW package `internal/auth/jwtmw/` with `Middleware(keystore, revocationStore, opts) func(http.Handler) http.Handler`. Reads bearer from `Authorization: Bearer <jwt>` header OR a configurable cookie name (default: `atlas_session` per slice 189 D1).
- **AC-2.** Verifies signature via `tokensign.Verify`. On failure: 401 + `WWW-Authenticate: Bearer realm="atlas", error="invalid_token"`.
- **AC-3.** Validates claims via `jwt.Validate(token, expectedAudience, now)`. On failure: 401.
- **AC-4.** Checks `revocationStore.IsRevoked(jti)`. On revoked: 401.
- **AC-5.** Sets request context with verified `*jwt.AtlasClaims`. Downstream handlers access via `jwtmw.FromContext(ctx)`.
- **AC-6.** Sets `app.current_tenant` Postgres GUC via existing `internal/tenancy/` `WithTenant` wrapper before passing the request to the chi handler. Skips for `current_tenant_id == Nil` (machine clients without tenant context).

### Middleware mount on `/v1/*`

- **AC-7.** `internal/api/httpserver.go` mounts the new middleware on `/v1/*` ROUTES. Coexists with slice 034 bearer-token middleware. Resolution order: JWT first (if `Authorization: Bearer eyJ*` shape OR cookie present), then legacy. If JWT is present + invalid → 401 (do NOT fall through to legacy). If no JWT → try legacy.
- **AC-8.** Legacy bearer-token path stays unchanged in this slice. Slice 191 retires it.
- **AC-9.** Unauthenticated routes (`/oauth/*`, `/.well-known/*`, `/health`, `/healthz`) bypass both middlewares. List in `internal/api/httpserver.go` is the source of truth.

### Revocation store

- **AC-10.** NEW migration creates `oauth_revoked_tokens` (jti PK, revoked_at, expires_at, revoked_by). Index `(expires_at)`. NOT tenant-scoped. NOT RLS-protected.
- **AC-11.** NEW package `internal/auth/revocation/` with `Store` interface: `Revoke(ctx, jti, expires_at, revoked_by) error` · `IsRevoked(ctx, jti) (bool, error)` · `Sweep(ctx) (deletedCount int, err error)`.
- **AC-12.** Postgres-backed implementation. PK lookup for `IsRevoked` — index-only scan.

### `/oauth/revoke` endpoint (RFC 7009)

- **AC-13.** NEW handler `internal/api/oauth/revoke.go` mounted at `POST /oauth/revoke`. Accepts `application/x-www-form-urlencoded`.
- **AC-14.** Form params: `token` (required) + `token_type_hint` (optional; advisory).
- **AC-15.** Authenticates the revoker: client_credentials via `Authorization: Basic` header (RFC 6749 §2.3.1) OR `client_id` + `client_secret` form params. Either method valid. Unauthenticated → 401.
- **AC-16.** Self-revocation also permitted: caller presents a valid JWT in `Authorization: Bearer <jwt>` AND the `token` in the form body matches the bearer's `jti` (RFC 7009 §2 — token revocation by the token's owner). This lets a user log out without holding a client_secret.
- **AC-17.** On success: extract `jti` + `exp` from the provided token. Call `revocationStore.Revoke(jti, exp, revoked_by)`. Return 200 with empty body per RFC 7009 §2.2. Even unknown tokens return 200 (silent — RFC mandate).

### `/oauth/introspect` endpoint (RFC 7662)

- **AC-18.** NEW handler `internal/api/oauth/introspect.go` mounted at `POST /oauth/introspect`. Accepts `application/x-www-form-urlencoded`.
- **AC-19.** Form params: `token` (required) + `token_type_hint` (optional).
- **AC-20.** Authenticates the inspector via client_credentials (Basic OR form). Unauthenticated → 401.
- **AC-21.** Verifies the token's signature + expiry + revocation. Returns `{"active": false}` if any check fails (including revoked).
- **AC-22.** For active tokens, returns RFC 7662 §2.2 response: `active: true` + `sub` + `aud` + `iss` + `exp` + `iat` + `jti` + `token_type: "Bearer"` + the custom `atlas:*` claims (current_tenant_id, available_tenants, roles, super_admin, idp_issuer).
- **AC-23.** Response is `application/json` per RFC 7662 §2.2.

### Sweeper

- **AC-24.** Goroutine in `cmd/atlas/main.go` (started after auth subsystem init) calls `revocationStore.Sweep` every 5 minutes. Logs count deleted at INFO.

### R2 eviction integration test (load-bearing)

- **AC-25.** Integration test in `internal/api/jwtmw/integration_test.go` (NEW directory):
  - Setup: create user in tenants A + B via slice 034 helpers. Mint JWT scoped to A via slice 188 token-exchange.
  - First request to `/v1/<some-route>` with the JWT — 200.
  - Direct DB update: remove user from tenant A's user_roles. (Simulates admin action.)
  - Second request with same JWT — STILL returns 200 (eventual eviction; JWT claims are trusted until expiry).
  - Revoke the JWT via `POST /oauth/revoke` with the user's own JWT in the Authorization header.
  - Third request with same JWT — 401.
- **AC-26.** Integration test for the cross-tenant JWT rejection: mint JWT with `current_tenant_id = tenant_A`; assert middleware rejects request with `current_tenant_id` swap attempt (e.g., a malicious proxy adding `X-Atlas-Tenant: tenant_B` header — middleware MUST ignore non-JWT-claim tenant overrides).

### Tests + docs

- **AC-27.** Go unit tests for `jwtmw` middleware: bearer parsing, cookie parsing, claim validation pass/fail, revocation hit/miss.
- **AC-28.** Go integration tests for `/oauth/revoke` + `/oauth/introspect` end-to-end.
- **AC-29.** Discovery doc updated to advertise `revocation_endpoint` + `introspection_endpoint`.
- **AC-30.** `CHANGELOG.md` entry. `docs/openapi.yaml` updated.
- **AC-31.** JUDGMENT decisions log at `docs/audit-log/190-jwt-middleware-r2-decisions.md`: D1 (cookie vs header preference order), D2 (separate revocation_events audit table vs reuse existing), D3 (legacy-bearer coexistence cutover semantics), D4 (sweeper interval).

## Constitutional invariants honored

- **Tenant isolation at DB layer** (invariant #6): middleware sets `app.current_tenant` GUC before any handler runs; RLS enforces. The JWT's `current_tenant_id` claim is the only source — header-level overrides are rejected (AC-26).
- **Append-only audit** for security events: revocation_events captured (AC-31 D2 decides table location).
- **No bypass middleware** for `/v1/*` except the documented unauthenticated route list (AC-9).

## Canvas references

- OQ #21 RESOLVED (Reading D).
- `docs/adr/0003-oauth-authorization-server.md` (slice 187).
- Slice 034 (OIDC RP / sessions) — middleware coexists with legacy bearer-token path during the migration window.

## Dependencies

- **#187** OAuth AS scaffolding. MERGED at `ac42517`.
- **#188** `/oauth/token` + token-exchange. **Gate: 188 must be `merged`.**
- **#189** `/oauth/authorize` + PKCE + frontend OAuth client. **Gate: 189 must be `merged`.**
- **#034** OIDC RP + sessions (the legacy path coexists during cutover).

## Anti-criteria (P0 — block merge)

- **P0-190-1.** Does NOT retire the slice 034 bearer-token middleware. Both paths active during migration window. Slice 191 retires legacy.
- **P0-190-2.** Middleware MUST verify signature BEFORE checking revocation. A revocation hit on a forged JTI is meaningless — verify first.
- **P0-190-3.** Middleware MUST set `app.current_tenant` GUC from the verified JWT claim, NOT from any request header. Header-level tenant overrides are a classic privilege escalation vector.
- **P0-190-4.** `/oauth/revoke` returns 200 for unknown tokens (RFC 7009 §2.2). Do NOT return 404 — that's an information-disclosure regression.
- **P0-190-5.** `/oauth/introspect` returns `{"active":false}` for revoked/expired tokens, NOT a 401. The endpoint's purpose is to ANSWER "is this token active" — 401 confuses authentication-of-inspector with token-validity-of-introspectee.
- **P0-190-6.** Does NOT modify slice 187's keystore + tokensign packages. All signing/verifying goes through them unchanged.
- **P0-190-7.** Does NOT implement a refresh-token grant. v3 deferred.
- **P0-190-8.** R2 integration test (AC-25) MUST cover the eventual-eviction-then-revoke pattern. The slice cannot ship without that test green.
- **P0-190-9.** Middleware MUST be wired on `/v1/*` paths, NOT `/oauth/*` or `/.well-known/*` or `/health*`.
- **P0-190-10.** Does NOT bypass slice 187's keystore for any path.
- **P0-190-11.** Header `WWW-Authenticate: Bearer realm="atlas", error="invalid_token"` MUST be returned on 401 (RFC 6750 §3).

## Skill mix (3-5)

- `grill-with-docs`
- `tdd`
- `database-designer` (revoked_tokens migration)
- `security-review` (this is the production cutover slice — high stakes)
- `simplify`
- `ship-gate`

## Notes for the implementing agent

### This is the cutover slice — read slice 034's bearer path carefully

Slice 034's `internal/api/httpserver.go` handler chain has the bearer-token middleware. Your job is to add JWT validation as the PRIMARY path with the legacy path as fallback. Don't refactor — extend. Slice 191 does the legacy retirement.

### The R2 semantic is "eventual"

This is a deliberate OAuth design choice. Tokens are valid until expiry; immediate eviction requires explicit revocation. Document this clearly in the operator-facing docs (the maintainer will get questions from security reviewers about "what happens when I remove a user from a tenant — are their tokens immediately invalid?"). The honest answer: "no, until their next token-refresh or you call /oauth/revoke".

### Coexistence with slice 034 is delicate

There's a JWT? Validate as JWT, FAIL CLOSED on JWT errors (don't fall through to legacy). No JWT but there's a legacy bearer? Try legacy. Neither? 401. Get this right or you ship an auth bypass.

### Document the cutover plan

ADR addendum should sketch slice 191's retirement work. Future-you needs to know what the legacy path does so you can safely remove it.

### Spillover candidates

- Refresh-token grant (RFC 6749 §6).
- DPoP (RFC 9449).
- mTLS client auth (RFC 8705).
- All v3 deferred per slice 187 + slice 188 spillover policies.

### Provenance

Filed 2026-05-21 as auth-substrate-v2 spine slot 4.
