# 188 — OAuth `/oauth/token` endpoint + RFC 8693 token exchange

**Cluster:** Backend / Auth
**Estimate:** 2-3d
**Type:** JUDGMENT
**Status:** `ready` (second slot in the auth-substrate-v2 spine; dep #187 merged)

## Narrative

Slice 187 shipped the OAuth AS scaffolding (keystore + tokensign + JWT claim types + JWKS endpoint + OIDC discovery + ADR-0003) but left the four consumer-facing endpoints as 501 stubs. Slice 188 lights up the **first real grant endpoint**: `POST /oauth/token` implementing two RFCs:

1. **RFC 6749 §4.4 — Client Credentials grant** for machine clients (the canonical flow for the v2 SDK migration in slice 191). Caller authenticates with `client_id` + `client_secret`; AS returns a JWT with `sub = client_id`, no `atlas:current_tenant_id` (machine tokens are tenant-free by default; the caller scopes a tenant via the audience parameter `aud`).

2. **RFC 8693 — Token Exchange** for tenant switching. Caller presents an existing JWT + a target `atlas:current_tenant_id`; AS validates the target tenant is in caller's `atlas:available_tenants[]`, then issues a NEW JWT with the swapped `current_tenant_id`. This is the load-bearing primitive that closes slice 141's "switch tenants mid-session" vCISO success criterion (full closure ships in slice 192 with the frontend wiring).

**What this slice ships:**

1. **`POST /oauth/token` HTTP handler** at `internal/api/oauth/token.go` accepting `application/x-www-form-urlencoded` per RFC 6749 §3.2. Dispatches on `grant_type` form parameter to client-credentials or token-exchange code paths. Returns RFC 6749 §5.1 standard response shape (`access_token`, `token_type=Bearer`, `expires_in`, `scope`).
2. **Client registration table** `oauth_clients` (NEW migration): `id UUID PK · client_id TEXT UNIQUE · client_secret_hash TEXT NOT NULL (argon2id) · name TEXT · created_at · disabled_at NULL`. NOT tenant-scoped (machine clients are platform-global). Read at token issuance to validate `client_id` + `client_secret`.
3. **Client-credentials issuance** (`grant_type=client_credentials`): validate `client_id` + `client_secret` against `oauth_clients`; on match, mint a JWT with `sub = "oauth_client:<id>"`, `aud = <atlas-instance-issuer>`, `exp = iat + 1h`, `atlas:idp_issuer = "atlas-oauth-client"`, NO `atlas:current_tenant_id` (caller passes via separate slice 191 wire convention), `atlas:available_tenants = []`, `atlas:roles = {}`, `atlas:super_admin = false`.
4. **Token-exchange issuance** (`grant_type=urn:ietf:params:oauth:grant-type:token-exchange`): per RFC 8693, accept `subject_token` (existing JWT) + `subject_token_type=urn:ietf:params:oauth:token-type:jwt` + custom `actor_token` carrying the target tenant ID (or use a custom `atlas:target_tenant_id` form param). Validate the subject_token signature + expiry against the keystore (via slice 187's `tokensign.Verify`). Resolve target tenant: MUST be in subject_token's `atlas:available_tenants[]` OR caller is `super_admin=true`. Mint NEW JWT preserving `sub`, `atlas:idp_issuer`, `atlas:available_tenants`, `atlas:roles`, `atlas:super_admin` but with the swapped `atlas:current_tenant_id`. Audit log entry written to `oauth_token_exchanges` (append-only).
5. **Audit log table** `oauth_token_exchanges` (append-only, two-policy RLS): `id · tenant_id (the target tenant; scopes RLS) · subject_token_jti · from_tenant_id NULL · to_tenant_id · subject_token_iss · subject_token_sub · exchanged_at · ip_address NULL`. Slice 138's exception-flow append-only pattern applies (only `tenant_read` + `tenant_write` policies under FORCE RLS).
6. **CLI client-credentials issuance helper** `atlas oauth issue-client <name>` (CMD: `cmd/atlas-cli/cmd/oauth_issue_client.go`). Generates a UUID `client_id` + 32-byte random `client_secret`, writes argon2id hash to `oauth_clients`, prints the plaintext secret ONCE to stdout. Operator records the secret (no recovery — must rotate by issuing a new client).
7. **Discovery doc update**: slice 187's `openid-configuration` handler updates to advertise `token_endpoint = <issuer>/oauth/token`, `grant_types_supported = ["client_credentials", "urn:ietf:params:oauth:grant-type:token-exchange"]`.

**SCOPE DISCIPLINE — what's deliberately out:**

- `/oauth/authorize` + PKCE browser flow — slice 189.
- `/oauth/revoke` + `/oauth/introspect` — slice 190.
- JWT validation middleware on `/v1/*` (no consumer of these new tokens on the request hot path yet) — slice 190.
- SDK migration to use this endpoint — slice 191.
- Frontend tenant-switcher dropdown — slice 192.
- Refresh-token grant (RFC 6749 §6) — v3 deferred; access tokens are short-lived (1h) + clients re-acquire.
- Device-code grant (RFC 8628) — slice 191 (CLI flow).
- Token-exchange `actor_token` semantics beyond tenant switching (delegation, impersonation) — v3 deferred.

## Threat model

**S — Spoofing.** Forged `client_id` + `client_secret`.

- Mitigation: argon2id hash compare with constant-time comparison. `client_secret` is 32 random bytes (256 bits entropy); brute-force is infeasible.

**T — Tampering.** Token-exchange request mutated by intermediary to swap to a tenant caller doesn't own.

- Mitigation: subject_token signature validated against keystore BEFORE the tenant-allowlist check. Target tenant MUST be in `atlas:available_tenants[]` of the subject_token's verified claims — not in the request body.

**R — Repudiation.** Token-exchange events need audit trail.

- Mitigation: every successful exchange writes to `oauth_token_exchanges` with the subject_token's `jti` + `sub` + `iss`. RLS-protected on the target `tenant_id`; cross-tenant exchanges visible to both tenants' admins (each side sees its own row).

**I — Information disclosure.** Plaintext `client_secret` leaked.

- Mitigation: secret printed to stdout ONCE at `oauth issue-client`. No DB column stores plaintext. CLI documents that secret is unrecoverable.

**D — Denial of service.** Token endpoint hammered for free JWT signing (CPU expensive).

- Mitigation: per-client rate limit (token bucket; default 60/min/client) at the handler. Configurable via env var `ATLAS_OAUTH_TOKEN_RATE_PER_MIN`. Returns 429 with `Retry-After`. The handler honors RFC 6585 §4.

**E — Elevation of privilege.** Caller token-exchanges to a tenant + `super_admin=true` they don't have.

- Mitigation: token-exchange NEVER elevates `atlas:super_admin`. The minted token's `super_admin` claim copies from the subject_token; it cannot be set true via the exchange path. (super_admin grant happens at OIDC login time only; slice 142 ships that flow.) Document this invariant in ADR-0003 addendum.

**Verdict:** `has-mitigations`. The token-exchange tenant-allowlist check is the load-bearing authz primitive — covered by AC-13 + AC-14 below.

## Acceptance criteria

### Client registration + secret hashing

- **AC-1.** NEW migration `migrations/sql/<NNN>_oauth_clients.sql` creating `oauth_clients` with columns above. NOT tenant-scoped; no RLS. UNIQUE on `client_id`. Reversible `.down.sql`.
- **AC-2.** NEW Go package `internal/auth/oauthclient/` with: `Issue(ctx, name) (id, secret, error)` (generates UUID + 32B random + argon2id hash, INSERTs). `Verify(ctx, client_id, secret_plaintext) (*Client, error)` (constant-time hash compare). Argon2id parameters: time=2, memory=64MB, threads=4 (OWASP-recommended; tune via env if too slow on commodity hardware).
- **AC-3.** CLI command `atlas oauth issue-client <name>` (handler at `cmd/atlas-cli/cmd/oauth_issue_client.go`). Prints `client_id: <uuid>` + `client_secret: <base64-32B>` to stdout. EXIT 1 on duplicate name.

### `/oauth/token` HTTP handler

- **AC-4.** NEW handler `internal/api/oauth/token.go` mounted at `POST /oauth/token` (unauthenticated — clients authenticate via form body). Accepts `application/x-www-form-urlencoded`; rejects other content types with 400 + `error=invalid_request`.
- **AC-5.** Dispatches on `grant_type` form parameter:
  - `client_credentials` → AC-6 path
  - `urn:ietf:params:oauth:grant-type:token-exchange` → AC-10 path
  - any other value → 400 + `error=unsupported_grant_type`
- **AC-6.** Client-credentials path validates `client_id` + `client_secret` against `oauth_clients`. On match, mint JWT via slice 187's `tokensign.Sign`. Claims:
  - `iss` = atlas issuer URL
  - `sub` = `oauth_client:<client_id>`
  - `aud` = atlas issuer URL (or form-param `audience` if provided per RFC 8693)
  - `exp` = `iat + 3600` (1 hour)
  - `iat` = now, `jti` = UUID
  - `atlas:idp_issuer` = `"atlas-oauth-client"`
  - `atlas:current_tenant_id` = NULL (zero-UUID)
  - `atlas:available_tenants` = `[]`
  - `atlas:roles` = `{}`
  - `atlas:super_admin` = `false`
- **AC-7.** Client-credentials path on mismatch returns 401 + `error=invalid_client`. RFC 6749 §5.2.
- **AC-8.** Token-endpoint response shape per RFC 6749 §5.1: `{"access_token":"<jwt>","token_type":"Bearer","expires_in":3600,"scope":""}`. `Content-Type: application/json`. `Cache-Control: no-store`.
- **AC-9.** Per-client rate limit: default 60 requests/min/client_id. Token bucket. Returns 429 + `Retry-After` header + `error=invalid_request` body. Configurable via `ATLAS_OAUTH_TOKEN_RATE_PER_MIN`. Documented in ADR addendum.

### Token-exchange path (RFC 8693)

- **AC-10.** Token-exchange path accepts form params: `subject_token` (existing JWT) + `subject_token_type=urn:ietf:params:oauth:token-type:jwt` + `atlas:target_tenant_id` (custom param; document the choice in decisions log — option to use standard `actor_token` discussed under D2).
- **AC-11.** Validates `subject_token` via `tokensign.Verify` (signature check) + `jwt.Validate` (exp + iss + aud check). Returns 401 + `error=invalid_token` on any failure.
- **AC-12.** Resolves target tenant: MUST be in subject_token's `atlas:available_tenants[]` array OR subject_token's `atlas:super_admin == true`. Otherwise returns 403 + `error=invalid_target`.
- **AC-13.** Load-bearing tenant-isolation check: integration test asserts that a JWT issued for `tenant A` cannot be exchanged for `tenant B` unless either (a) `B in available_tenants` OR (b) `super_admin=true`. Test produces both the negative case (rejected) and the positive case (accepted).
- **AC-14.** Mints new JWT: copies `sub`, `atlas:idp_issuer`, `atlas:available_tenants`, `atlas:roles`, `atlas:super_admin` from subject_token; swaps `atlas:current_tenant_id` to target; new `jti`, new `iat`, new `exp = iat + 3600`. Response shape per AC-8.
- **AC-15.** `atlas:super_admin` NEVER elevates via token-exchange. If subject_token's `super_admin=false`, minted token's `super_admin=false`. Integration test covers.

### Audit log

- **AC-16.** NEW migration extends slice 187's migration set (or NEW migration; engineer picks at impl). Creates `oauth_token_exchanges` table with append-only two-policy RLS. Columns: `id UUID PK · tenant_id UUID NOT NULL · subject_token_jti TEXT NOT NULL · from_tenant_id UUID NULL · to_tenant_id UUID NOT NULL · subject_token_iss TEXT NOT NULL · subject_token_sub TEXT NOT NULL · exchanged_at TIMESTAMPTZ NOT NULL DEFAULT now() · ip_address INET NULL`. Index `(tenant_id, exchanged_at DESC)`.
- **AC-17.** Every successful token-exchange writes one row to `oauth_token_exchanges` in the SAME transaction as the JWT signing (or via subsequent best-effort write; engineer picks — document trade-off in decisions log D3).

### Discovery doc

- **AC-18.** Slice 187's `openid-configuration` handler updated to advertise `token_endpoint`, `grant_types_supported`, `token_endpoint_auth_methods_supported = ["client_secret_post"]`. Existing AC-7/AC-8 from slice 187 stay green.

### Tests

- **AC-19.** Go unit tests under `internal/auth/oauthclient/`: argon2id round-trip, constant-time compare, duplicate-name rejection.
- **AC-20.** Go integration tests under `internal/api/oauth/`: full HTTP flow per AC-5 through AC-15. Real Postgres via slice 002 integration harness. Includes the AC-13 cross-tenant rejection assertion.
- **AC-21.** CLI test for `atlas oauth issue-client <name>` under `cmd/atlas-cli/cmd/oauth_issue_client_test.go`.

### Decisions log

- **AC-22.** JUDGMENT slice — engineer writes `docs/audit-log/188-oauth-token-endpoint-decisions.md` with at least: D1 (argon2id parameters chosen) · D2 (custom `atlas:target_tenant_id` form param vs RFC 8693 standard `actor_token`) · D3 (audit log write same-tx vs best-effort) · D4 (per-client rate limit default) · confidence per decision.

### ADR

- **AC-23.** ADR-0003 addendum (or new section) covers: token-exchange tenant-allowlist semantics; super_admin non-elevation invariant; rate-limit shape.

### Documentation

- **AC-24.** `CHANGELOG.md` entry under `[Unreleased]`.
- **AC-25.** `docs/openapi.yaml` updated to include `/oauth/token` (generator `cmd/atlas-openapi`). `Frontend · install + build` (lint) stays green.

## Constitutional invariants honored

- **Tenant isolation at DB layer** (invariant #6): `oauth_token_exchanges` RLS-protected on `tenant_id`. The token-exchange tenant-allowlist check is application-layer; RLS-on-audit-table is the safety net.
- **Append-only evidence ledger** (invariant #2): `oauth_token_exchanges` is append-only via two-policy RLS (slice 138 precedent).
- **AI-assist boundary** (constitutional): NOT touched by this slice. OAuth tokens are not AI-assist artifacts.

## Canvas references

- `Plans/canvas/11-open-questions.md` OQ #21 RESOLVED — Reading D, OAuth AS commitment.
- `Plans/canvas/09-tech-stack.md` — Authorization Server row (added when slice 187 merged).
- `docs/adr/0003-oauth-authorization-server.md` — slice 187's foundational ADR.

## Dependencies

- **#187** OAuth AS scaffolding (keystore + tokensign + jwt + JWKS + discovery). MERGED at `ac42517`. Required: this slice consumes `tokensign.Sign` + `tokensign.Verify` + `jwt.AtlasClaims`.
- **#002** Schema + migrations (slice 002 integration test helpers for new tables).
- **#036** Append-only RLS pattern (slice 036 precedent for `oauth_token_exchanges`).

## Anti-criteria (P0 — block merge)

- **P0-188-1.** Does NOT implement `/oauth/authorize`, `/oauth/revoke`, or `/oauth/introspect` beyond their slice-187 501 stubs. Those are slices 189-190.
- **P0-188-2.** Does NOT introduce JWT validation middleware on `/v1/*` routes. The new tokens this slice mints have no consumer yet — slice 190 wires consumers.
- **P0-188-3.** Does NOT store `client_secret` in plaintext anywhere — DB, logs, error responses, audit log.
- **P0-188-4.** Token-exchange MUST NOT elevate `atlas:super_admin`. The minted token's `super_admin` claim equals the subject_token's `super_admin` — never `true` unless the subject_token already had it.
- **P0-188-5.** Token-exchange MUST validate the subject_token's signature BEFORE the tenant-allowlist check. Order matters: an unverified token can claim arbitrary `available_tenants`.
- **P0-188-6.** Does NOT introduce a refresh-token grant. v3 deferred.
- **P0-188-7.** Does NOT introduce a device-code grant. That's slice 191's surface.
- **P0-188-8.** The `oauth_token_exchanges` table MUST be append-only via two-policy RLS (`tenant_read` + `tenant_write`, no update/delete). Append-only is constitutional for audit-grade tables (invariant #2).
- **P0-188-9.** Rate-limit MUST be per-client (keyed on `client_id`), NOT per-IP. IP-based limits are bypassable via NAT.
- **P0-188-10.** Does NOT bypass slice 187's keystore — all JWT signing goes through `tokensign.Sign`.
- **P0-188-11.** Integration tests MUST cover the cross-tenant token-exchange rejection path (AC-13). The slice cannot ship without this test green.

## Skill mix (3-5)

- `grill-with-docs` (gate at design phase)
- `tdd` (per AC)
- `database-designer` (oauth_clients + oauth_token_exchanges migrations)
- `security-review` (token endpoint touches AI-assist + RLS + cryptographic primitives)
- `simplify`
- `ship-gate`

## Notes for the implementing agent

### This is spine slot 2 of 6

Slice 187 = foundation (keystore + signing). Slice 188 = first grant endpoint (`/oauth/token`). The remaining spine slices (189-192) build on this. Stay tightly in scope — every line of "while I'm here, let me also..." pulls scope from a later slice.

### The token-exchange tenant-allowlist is THE load-bearing primitive

Slice 192 wires the frontend tenant-switcher to call `/oauth/token` with `grant_type=token-exchange`. The semantic correctness of THAT feature (the vCISO use case from slice 141) depends entirely on this slice's AC-13 holding. Lean on integration tests; don't trust a unit-test mock of the validator.

### Make the decisions log substantive

JUDGMENT slice. The argon2id params (D1), the form-param shape for target_tenant_id (D2), the audit log write semantics (D3), and the rate-limit defaults (D4) are all calls future-you will second-guess. Document the rationale + revisit-once-in-use.

### Spillover candidates the spec surfaces

- Refresh-token grant (RFC 6749 §6). Out of scope by P0-188-6. v3 spillover when access-token lifetime starts hurting operators.
- mTLS client authentication (RFC 8705). Out of scope. v3 spillover for high-security operators.
- Token introspection endpoint (RFC 7662). Slice 190.

### Provenance

Filed 2026-05-21 as auth-substrate-v2 spine slot 2 per slice 187's roadmap. OQ #21 Reading D commitment from 2026-05-20.
