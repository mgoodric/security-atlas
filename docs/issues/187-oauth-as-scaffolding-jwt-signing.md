# 187 — OAuth Authorization Server scaffolding (JWT signing + JWKS + discovery)

**Cluster:** Backend / Auth
**Estimate:** 2-3d
**Type:** JUDGMENT
**Status:** `ready` (foundation slice for the auth-substrate-v2 spine; gated on no other slices)

## Narrative

Canvas OQ #21 resolved 2026-05-20: security-atlas commits to a **standards-based OAuth 2.0 Authorization Server with JWT access tokens carrying tenant-in-claim** (Reading D — RFC 9068 + RFC 8693). Maintainer rationale: "we have no users yet; I don't want to implement something just to replace it." Pre-PMF is the right time to commit to the standards-based architecture instead of shipping a bespoke session model.

Slice 187 ships the **scaffolding** of the OAuth Authorization Server — the cryptographic foundation that the rest of the auth-substrate-v2 spine (slices 188-192) builds on. Foundation-only; no consumer-facing endpoints in this slice beyond the standards-mandated discovery + JWKS surfaces.

**What this slice ships:**

1. **JWT signing keypair** — RS256 (RSA-2048) OR ES256 (ECDSA-P256). Engineer picks at impl time; record in decisions log. Keypair generated at first boot if absent; stored in a keystore abstraction (filesystem for self-host MVP; pluggable for future KMS / HSM integrations per v3).
2. **JWKS endpoint** (`GET /.well-known/jwks.json`) — RFC 7517 + RFC 9068 §3.1 mandated. Returns the public-key half of the signing keypair(s) in JWK Set format. Multi-key support from day one to enable key rotation without downtime.
3. **OIDC discovery document** (`GET /.well-known/openid-configuration`) — RFC 8414 + OIDC Discovery. Advertises the issuer URL, `jwks_uri`, supported grant types (filled in as slices 188-192 land), supported scopes, supported claims, and `token_endpoint`/`authorization_endpoint`/`revocation_endpoint`/`introspection_endpoint` URLs (all stub-returning 501 until subsequent spine slices land).
4. **Internal token-signing service** at `internal/auth/tokensign/` — Go package wrapping the keystore + JWT-encode/decode primitives. Pure library; no HTTP handlers. Used by slices 188-192.
5. **Key rotation pattern** — documented in decisions log; not implemented end-to-end in this slice (rotation involves an old + new key both being live in JWKS for a window). Slice 187 ships the multi-key data structures + JWKS multi-key support so future rotation is a config change, not a refactor.
6. **`internal/auth/jwt/` package** — claim shape types (issuer, sub, aud, exp, iat, jti, plus the locked `atlas:*` custom claims from OQ #21 resolution). Pure types + validation primitives; no signing logic (that's tokensign).
7. **ADR-0003** at `docs/adr/0003-oauth-authorization-server.md` — full architectural rationale + standards committed + token shape + key rotation strategy + threat-model summary.

**SCOPE DISCIPLINE — what's deliberately out:**

- `/oauth/token` endpoint — slice 188.
- `/oauth/authorize` + PKCE — slice 189.
- `/oauth/revoke` + `/oauth/introspect` — slice 190.
- JWT validation middleware on `/v1/*` — slice 190.
- Frontend OAuth client integration — slice 189.
- SDK migration to `client_credentials` — slice 191.
- Multi-tenant switch via token-exchange — slice 192.
- KMS / HSM integration — v3 deferred; filesystem keystore is v1 MVP.
- DPoP (RFC 9449) — v3 layering; optional.
- mTLS / SPIFFE for connector flows — separate v3 design conversation.
- Replacing the existing bearer-token credstore — slice 190 (when JWT validation middleware lands). API keys keep working unchanged through this slice.

## Threat model

**S — Spoofing.** A malicious caller forges a JWT claiming arbitrary identity / tenant.

- Mitigation: every JWT carries an `iss` claim matching the atlas instance URL + is signed with the private key. JWKS serves only the public-key half. Forgery requires the private key.
- The keystore is filesystem-backed; OS-level file permissions are the trust root (mode 0600, owned by the atlas binary's run user).
- Threat: filesystem keystore is read by another process on the same host. Mitigation: container/k8s deployment with the keystore mounted from a Secret; self-host deploys document the file-permissions baseline in `docs/RELEASE_READINESS.md` and CONTRIBUTING.

**T — Tampering.** A forwarded JWT is mutated by an intermediary.

- Mitigation: JWT signature validation rejects any mutation. Standard.

**R — Repudiation.** JWT issuance + validation needs to be logged for forensic recoverability.

- Slice 187 doesn't issue tokens (that's 188) but ships the keystore. Mitigation: keystore reads are logged at INFO; key rotation events logged at WARN.

**I — Information disclosure.** Private signing key leaked via filesystem, debug logs, or backup.

- Mitigations: keystore file mode 0600; key material never logged (only key IDs); explicit `.gitignore` entries for the keystore path; CI integration test asserts no key bytes appear in logs or git history.

**D — Denial of service.** JWKS endpoint hammered to generate signing-key load.

- JWKS is read-only public-key material; serving it is `O(1)` and key load is zero (no signing on read). Standard caching headers (Cache-Control: max-age=3600, ETag).
- OIDC discovery doc same shape.

**E — Elevation of privilege.** A caller induces the AS to mint a token with elevated claims.

- Slice 187 doesn't have a token endpoint — no minting surface yet. Subsequent slices honor the elevation guards.

**Verdict.** `has-mitigations` — filesystem keystore is the load-bearing trust root; document the file-permissions baseline; CI pins key material absence from logs + git.

## Acceptance criteria

### Keystore + signing infrastructure

- **AC-1.** NEW package `internal/auth/keystore/` with interface: `Get(ctx) (signingKey, []verificationKey, error)` + `Rotate(ctx) error` (slice 187 ships interface only; rotation no-ops in v1 — stub for future slice).
- **AC-2.** NEW filesystem-backed implementation `internal/auth/keystore/fsstore/` reading from a configurable path (default `/var/lib/security-atlas/keys/`). First-boot generates a keypair if absent. File mode 0600 enforced + asserted in integration test.
- **AC-3.** Keypair algorithm: engineer picks RS256 vs ES256 in decisions log (D1). Both are RFC 9068 §3 compliant; ES256 has smaller signatures + faster signing; RS256 has wider library compatibility. Engineer's call.
- **AC-4.** NEW package `internal/auth/tokensign/` wrapping the keystore + a JWT-encode/decode primitives layer (uses a vetted JWT library — `github.com/golang-jwt/jwt/v5` is the recommended Go standard, but engineer verifies it covers RFC 9068 + RFC 8693 needs).

### JWT claim types

- **AC-5.** NEW package `internal/auth/jwt/` with strongly-typed claim structs:
  - `RegisteredClaims` (RFC 7519 standard: iss, sub, aud, exp, iat, nbf, jti)
  - `AtlasClaims` extending RegisteredClaims with `idp_issuer string`, `current_tenant_id uuid.UUID`, `available_tenants []uuid.UUID`, `roles map[uuid.UUID][]string`, `super_admin bool`
  - Validation primitives: `Validate(token, expectedAudience, now) error` checks signature + exp + iat + iss + aud per RFC 7519 + RFC 9068
- **AC-6.** Unit tests cover: signature mismatch → reject · expired token → reject · audience mismatch → reject · issuer mismatch → reject · `current_tenant_id` not in `available_tenants` → reject (this is the load-bearing tenant-isolation check that R2 middleware will use in slice 190).

### JWKS + OIDC discovery endpoints

- **AC-7.** NEW handler `GET /.well-known/jwks.json` (RFC 7517 + RFC 9068 §3.1). Returns public-key half(s) as `{"keys": [...]}`. Supports multi-key for future rotation. Cache-Control header. Integration test asserts validity against a vetted JWK validator library.
- **AC-8.** NEW handler `GET /.well-known/openid-configuration` (RFC 8414 + OIDC Discovery). Advertises:
  - `issuer` = atlas's external URL (from config)
  - `jwks_uri` = `<issuer>/.well-known/jwks.json`
  - `token_endpoint` = `<issuer>/oauth/token` (handler returns 501 in slice 187; slice 188 fills in)
  - `authorization_endpoint` = `<issuer>/oauth/authorize` (501 stub; slice 189)
  - `revocation_endpoint` = `<issuer>/oauth/revoke` (501 stub; slice 190)
  - `introspection_endpoint` = `<issuer>/oauth/introspect` (501 stub; slice 190)
  - `grant_types_supported`: `[]` (filled in as slices 188+ land)
  - `id_token_signing_alg_values_supported`: `["RS256"]` or `["ES256"]` per D1
  - `subject_types_supported`: `["public"]`
  - `scopes_supported`: `["openid"]` (atlas-specific scopes added in slice 188)
  - `claims_supported`: `["iss", "sub", "aud", "exp", "iat", "jti", "atlas:idp_issuer", "atlas:current_tenant_id", "atlas:available_tenants", "atlas:roles", "atlas:super_admin"]`
- **AC-9.** Stub `/oauth/token` + `/oauth/authorize` + `/oauth/revoke` + `/oauth/introspect` handlers all return `501 Not Implemented` with body `{"error":"slice_pending","slice":"NNN"}` — makes the discovery document honest about what's not yet live.

### Tests

- **AC-10.** Integration test: keystore generates keypair on first boot; subsequent boots reuse the existing pair.
- **AC-11.** Integration test: JWKS round-trip — sign a test JWT with the private key, fetch JWKS, verify against the public key.
- **AC-12.** Integration test: OIDC discovery doc validates against the OIDC Discovery 1.0 spec schema.
- **AC-13.** Integration test: filesystem keystore mode is 0600 after first-boot generation; test fails if mode is wider.
- **AC-14.** Integration test: log scraping — no key material appears in any log line at any level (use a stdlib-style structured-logger sink and assert).

### Decisions log

- **AC-15.** `docs/audit-log/187-oauth-as-scaffolding-decisions.md` with D1 (RS256 vs ES256) · D2 (filesystem keystore path default) · D3 (key rotation window TBD) · D4 (issuer URL config source) · D5..DN as surfaced.

### ADR

- **AC-16.** NEW `docs/adr/0003-oauth-authorization-server.md` capturing:
  - Context (slice 141 escalation; OQ #21 resolution)
  - Decision (Reading D — internal OAuth AS with JWT access tokens carrying tenant-in-claim)
  - Token shape (the `atlas:*` custom claims from OQ #21)
  - Standards committed (full RFC list)
  - Consequences (consumer migration story; spine of 6 slices; positions the project for standards-based authn/authz)
  - Alternatives considered (Readings A, B, C from OQ #21 with rejection rationale)
  - References (OQ #21 resolution block + slice 141 escalation context)

### Documentation

- **AC-17.** CHANGELOG entry under `[Unreleased] / Added`: "OAuth Authorization Server scaffolding — JWT signing infrastructure + JWKS endpoint + OIDC discovery document (#187). Foundation slice for the auth-substrate-v2 spine."
- **AC-18.** README.md gains a brief "Authentication" section pointing to ADR-0003 and the canvas OQ #21 resolution.
- **AC-19.** Canvas §9 (tech stack) updated to add a row: "OAuth AS — internal AS layer issuing JWT access tokens (RFC 9068); JWKS + OIDC discovery from slice 187."
- **AC-20.** CLAUDE.md "Tech stack (locked-in)" table updated to add row: "Authorization Server (internal) — JWT access tokens (RS256 or ES256) per RFC 9068 + RFC 8693 token exchange. Slice 187 foundation; full spine 187-192."

## Constitutional invariants honored

- **Invariant #6 — tenant isolation at DB layer via RLS.** Slice 187 ships no DB changes that affect RLS. Future slices (190) will set `app.current_tenant` GUC from the JWT `atlas:current_tenant_id` claim instead of from the bearer-token credstore — equivalent semantics, cleaner mechanism.
- **AI-assist boundary (CLAUDE.md).** JWTs let the platform encode `ai_assisted=true` as a token-level claim for AI-driven requests; the schema-level invariant `ai_assisted=true → human_approver` continues to gate audit-binding artifacts.
- **OIDC RP at slice 034** stays — atlas-AS-as-OIDC-RP authenticates the human via the external IdP; atlas-AS-as-issuer mints the atlas JWT. Layered cleanly.

## Canvas references

- `Plans/canvas/11-open-questions.md` #21 (resolved 2026-05-20; Reading D)
- `Plans/canvas/09-tech-stack.md` (to be updated in this slice per AC-19)
- `~/.claude/MEMORY/STATE/continuous-batch-escalation.md` (slice 141 E-1 escalation analysis — historical context)
- `docs/issues/141-multi-tenant-login-and-switcher.md` (the slice this OAuth AS commitment unblocks)

## Dependencies

- **#034** (OIDC RP + local users) — `merged`. The new AS layer wraps slice 034's external-IdP RP flow; slice 187 ships scaffolding only (no auth flow yet), so no runtime dependency.
- No other slice dependencies.

## Anti-criteria (P0 — block merge)

- **P0-187-1.** Does NOT implement `/oauth/token`, `/oauth/authorize`, `/oauth/revoke`, or `/oauth/introspect` beyond the 501 stubs. Those are slices 188-190.
- **P0-187-2.** Does NOT implement JWT validation middleware on `/v1/*`. That's slice 190. Bearer auth via existing credstore stays untouched in this slice.
- **P0-187-3.** Does NOT touch frontend / web/. That's slice 189 + 192.
- **P0-187-4.** Does NOT migrate any SDK. That's slice 191.
- **P0-187-5.** Filesystem keystore MUST enforce 0600 file mode; CI test asserts (AC-13).
- **P0-187-6.** Key material MUST NOT appear in any log line at any level (AC-14).
- **P0-187-7.** JWT library choice MUST cover RFC 9068 + RFC 8693 + RFC 7636 + RFC 8628 needs at minimum. If a candidate library is missing functionality, escalate to maintainer — DO NOT roll a custom JWT implementation.
- **P0-187-8.** Multi-key JWKS support from day one. Rotation flow doesn't have to be implemented end-to-end, but the data structures + JWKS handler MUST handle a list, not a single key.
- **P0-187-9.** Discovery document MUST be honest about what's stubbed — `grant_types_supported` empty + `501` responses on subsequent slice's endpoints. NO advertising of unsupported grants.
- **P0-187-10.** Does NOT introduce any DB schema changes. The auth-substrate-v2 spine's DB work lands in slice 192 (token-exchange + multi-tenant switch).
- **P0-187-11.** Neutral test-fixture tokens only.

## Skill mix (3-5)

1. **JWT library evaluation** — pick `github.com/golang-jwt/jwt/v5` or a more comprehensive alternative; verify RFC 9068 + RFC 8693 + RFC 7636 + RFC 8628 coverage at evaluation time
2. **Cryptographic key generation + rotation discipline** — RSA-2048 or ECDSA-P256; multi-key JWKS shape
3. **OIDC Discovery / RFC 8414 compliance** — discovery doc validates against the spec
4. **ADR authorship** — follow `docs/adr/0001-framework-scope-workflow.md` shape; ADR-0003 captures full architectural rationale
5. **Filesystem keystore security** — file-permissions baseline; log redaction

## Notes for the implementing agent

### This is a v2-spine foundation slice

The auth-substrate-v2 spine is 6 slices (187 → 188 → 189 → 190 → 191 → 192). Slice 187 ships the cryptographic + discovery scaffolding. **Do NOT bundle work from later slices.** The spine's structural integrity depends on each slice landing the layer it owns.

### Why the JWT claim shape matters

The locked custom claims (`atlas:current_tenant_id`, `atlas:available_tenants`, `atlas:roles`, `atlas:super_admin`, `atlas:idp_issuer`) ARE the multi-tenant model in JWT form. Slice 190's R2 middleware becomes a pure claim check. Slice 192's tenant switch is `grant_type=token-exchange` exchanging the current JWT for one with a different `current_tenant_id`. Get the claim shape right in 187; the rest of the spine builds on it.

### Document key rotation strategy in ADR-0003

Slice 187 doesn't implement end-to-end rotation, but the strategy needs to be designed now (even if implementation lands in a follow-on slice). Concrete questions to answer in ADR-0003:

- Rotation cadence (90 days? 1 year? on-demand?)
- Old-key sunset window (sign-with-new + verify-with-both → verify-with-new-only)
- JWKS cache TTL implications
- Re-issuance: do existing tokens (signed with the old key) keep working until they expire naturally, or are they revoked on rotation? Default: keep working — JWTs are short-lived (1h access; 30d refresh) so natural-expiry handles it.

### Spillover candidates the spec surfaces

If during this slice an out-of-scope finding emerges:

- KMS / HSM integration design — file as a v3 follow-on slice
- DPoP (RFC 9449) integration — v3 layering
- Federation with external OAuth ASes (Auth0, Okta, Keycloak as the issuer instead of atlas) — explicitly NOT in this slice's scope; potential future slice but the whole point of OQ #21's Reading D is atlas-as-AS
- mTLS / SPIFFE for connector flows — separate v3 design conversation

### Provenance

Filed 2026-05-20 immediately after OQ #21 resolved Reading D. Maintainer rationale ("no users yet; don't implement to replace") locked the standards-based path; slice 187 is the foundation that the rest of the spine (188-192) builds on. Slice 141 is parked `not-ready` gated on slice 192 (the auth-substrate-v2 spine's terminal slice).
