# ADR 0003 — OAuth 2.0 Authorization Server with JWT access tokens carrying tenant-in-claim

**Status:** Accepted · Scaffolding shipped (slice 187, 2026-05-20)

**Date:** 2026-05-20

**Resolves:** [`Plans/canvas/11-open-questions.md`](../../Plans/canvas/11-open-questions.md) #21 (Reading D)

**Implements through:** [`docs/issues/187-oauth-as-scaffolding-jwt-signing.md`](../issues/187-oauth-as-scaffolding-jwt-signing.md) (foundation) · slices 188-192 (token endpoint · PKCE · revocation+introspection+R2 middleware · SDK migration · multi-tenant switch via token-exchange)

---

## Context

The auth substrate on `main` through slice 173 is bearer-token-only: every authenticated request carries an opaque high-entropy secret (slice 034's `api_keys.token_hash`), the server looks it up in the credstore, and a thin context decorator (`tenancymw`) sets the `app.current_tenant` GUC for the request's lifetime. The atlas-as-OIDC-RP path (slice 034 + the `atlas_session` cookie) authenticates HUMANS into the platform via an external IdP, but the resulting cookie is read only by side-handlers (`/auth/*`, `/v1/me`) — it does NOT carry identity onto the request hot path. Every `/v1/*` request authenticates via the bearer credstore lookup.

Slice 141 surfaced the consequence: a multi-tenant login + tenant-switcher needs the request hot path to know **who** the requester is (OIDC subject + IdP issuer) AND **which tenant** they are operating under, atomically per request. The bearer credstore lookup gives "which tenant" (because tenants own credentials), but it does not give "who is the human" because the bearer itself is anonymous. Bolting per-request identity onto a bespoke session model would have meant building credential-shape-aware lookup machinery, a session-eviction protocol, and a tenant-switch verb — none of them standards-based, all of them eventually wrong if the platform's customer surface ever federates with an enterprise IdP.

The canvas resolved OQ #21 with the maintainer rationale: **"we have no users yet; I don't want to implement something just to replace it."** Pre-PMF is the cheapest time to commit to the standards-based architecture rather than ship a bespoke session model that we'd later need to migrate away from. The platform is also a security product — "we use the same OAuth + JWT standards your IdP issues" is a stronger trust signal than any custom session model could be.

## Decision

**Build an internal OAuth 2.0 Authorization Server inside atlas that issues JWT access tokens carrying tenant-in-claim.**

The AS is layered cleanly on top of slice 034's OIDC RP: the RP authenticates the human via the external IdP (atlas-as-OIDC-RP); the new AS layer mints the atlas JWT (atlas-as-issuer). Two distinct roles, one server process.

Wire protocol remains `Authorization: Bearer <opaque-looking-token>` — JWTs are bearer tokens. Slice 191 migrates SDK acquisition flows (API key → OAuth `client_credentials` grant); existing API keys continue to work during a 90-day deprecation window per OQ #9+#17 governance.

## Token shape (locked)

Every JWT atlas mints carries:

```jsonc
{
  // RFC 7519 standard claims
  "iss": "https://atlas.example.test", // atlas's external URL
  "sub": "user:<idp-subject>", // OIDC subject (humans) or "client:<client-id>" (machine clients)
  "aud": ["https://atlas.example.test/api"], // atlas API audience
  "exp": 1748016000, // access tokens: iat + 1h
  "iat": 1748012400,
  "jti": "0e3b4a7e-...", // unique-per-token identifier
  "nbf": 1748012400, // not-before (= iat by default)

  // atlas custom claims (locked at canvas OQ #21 Reading D)
  "atlas:idp_issuer": "https://idp.example.test", // for OIDC-authenticated humans
  "atlas:current_tenant_id": "<uuid>", // the tenant this token is bound to
  "atlas:available_tenants": ["<uuid>", "<uuid>"], // every tenant the subject can switch into
  "atlas:roles": {
    // per-tenant role list
    "<tenant-uuid>": ["admin"],
    "<other-tenant-uuid>": ["reader"]
  },
  "atlas:super_admin": false // global escalation flag (single-tenant deployments use false)
}
```

The custom `atlas:*` claims ARE the multi-tenant model in JWT form. The slice-190 R2 middleware becomes a pure claim check: validate `iss` + `aud` + `exp` + JWS signature against JWKS, then read `current_tenant_id` from the claim and `SET LOCAL app.current_tenant = $1` on the request's DB transaction. The slice-192 tenant switch is `grant_type=urn:ietf:params:oauth:grant-type:token-exchange` (RFC 8693): exchange the current JWT for one with a different `current_tenant_id` from `available_tenants`.

## Standards committed

- **RFC 6749** — OAuth 2.0 Authorization Framework (core protocol)
- **RFC 7515** — JSON Web Signature (JWS)
- **RFC 7517** — JSON Web Key (JWK)
- **RFC 7518** — JSON Web Algorithms (JWA) — ES256 is §3.4
- **RFC 7519** — JSON Web Token (JWT)
- **RFC 7636** — Proof Key for Code Exchange (PKCE) — slice 189
- **RFC 7009** — OAuth 2.0 Token Revocation — slice 190
- **RFC 7662** — OAuth 2.0 Token Introspection — slice 190
- **RFC 8414** — OAuth 2.0 Authorization Server Metadata (discovery) — slice 187
- **RFC 8628** — OAuth 2.0 Device Authorization Grant — slice 191 (CLI)
- **RFC 8693** — OAuth 2.0 Token Exchange — slice 192 (tenant switch verb)
- **RFC 8725** — JSON Web Token Best Current Practices — algorithm allowlist on `ParseSigned`
- **RFC 9068** — JWT Profile for OAuth 2.0 Access Tokens — every token atlas mints is a 9068-profile JWT
- **OIDC Discovery 1.0** — `.well-known/openid-configuration` shape

**Optional v3 layering:** RFC 9449 (DPoP) for token binding. Out of scope through slice 192; can be added without breaking the slice-187 contract.

## ES256 rationale

Slice 187 commits to ES256 (ECDSA P-256, RFC 7518 §3.4) as the only signing algorithm.

- **Signature size:** ~64 bytes vs ~256 bytes for RS256. Matters when JWTs flow through query strings during PKCE.
- **Signing speed:** faster than RSA-2048 on commodity hardware. Matters for the `/oauth/token` hot path that slice 188 will own.
- **Library coverage:** Go stdlib has native P-256 support; go-jose/v4 supports ES256 directly; every credible OIDC verifier (Auth0, Okta, Google IdP, Microsoft Entra ID, Keycloak) accepts ES256 today.
- **Forward-compatibility:** JWKS publishes `alg` per-key. A future slice can add a second key signed with a different algorithm (RS256, EdDSA) without breaking existing ES256 verifiers.

The decision favors the modern default over the conservative one because atlas has no legacy verifiers to support — the entire consumer surface is being designed in slices 188-192.

## Key rotation strategy

Slice 187 ships multi-key data structures + multi-key JWKS support so rotation can be added without refactoring. The end-to-end rotation flow itself is a follow-on slice. The designed-shape decisions (recorded here so the follow-on slice has a starting point):

- **Cadence:** 90 days. NIST SP 800-57 recommends 1-2 years for signing keys; AWS KMS defaults to 365d; GCP KMS supports 30d/90d/365d. 90d is a conservative midpoint operators can lengthen at config time.
- **Overlap window:** 24 hours. Access tokens default to 1h TTL (slice 188); 24h is 24× that — sufficient time for every refresh-token holder to obtain a new access token under the new key before the old key sunsets.
- **JWKS cache TTL:** 1 hour (`Cache-Control: max-age=3600` on JWKS responses). Verifiers re-fetch hourly; during the 24h overlap they will see both keys ~24×.
- **Existing token treatment:** tokens signed with the rotated-out key keep working until natural expiry. NO revocation on rotation. Rotation is for forward security (defense against undiscovered key exposure), not for incident response — incident response uses `/oauth/revoke` (slice 190).

## Endpoint exemptions (auth middleware)

JWKS and OIDC discovery MUST be reachable WITHOUT an auth context per RFC 8414 §3:

> "The configuration information is intended to be retrieved without authentication."

The slice-187 `internal/api/httpserver.go` change adds `/.well-known/` to the bearer-exempt set AND the authz-exempt set. `/oauth/` is also exempt — the 501-stub handlers (and the future real grant handlers) terminate auth at the OAuth client-authentication layer (`client_secret_basic` / `client_secret_post`), not via the platform bearer middleware.

The slice-190 R2 middleware that gates `/v1/*` MUST honor the same exemption list.

## Consequences

**Positive:**

- Standards-based authn/authz from day one. "We use the same OAuth + JWT standards your IdP issues" is the right trust signal for a security product.
- The slice-190 R2 middleware becomes a pure claim check — no credstore lookup, no per-request DB query for tenant membership. JWT validation is O(1) signature + claim shape.
- Multi-tenant tenant-switch becomes a standardised flow (RFC 8693 token-exchange) instead of a bespoke verb.
- SDKs ×4 absorb the JWT acquisition change at one well-known integration point (the `client_credentials` grant flow). API keys remain valid during a 90-day deprecation window.
- The atlas AS can later federate (act as a downstream resource server for an enterprise customer's IdP-issued tokens) without re-architecting.

**Negative:**

- Significant scaffolding to build (six slices). The cryptographic foundation is non-trivial (keystore, key rotation discipline, JWKS exposure, JWS signing).
- JWT compromise has a wider blast radius than opaque-token compromise — a leaked JWT carries claims that disclose tenant membership and role assignments. Mitigations: short access-token TTL (1h), revocation endpoint (slice 190 — operators can revoke specific JTIs), per-request audit log of JWT consumption (slice 190).
- Self-host operators must manage one additional secret (the keystore filesystem path); operators must ensure backups and access controls treat keystore material with the same gravity as the DB encryption key.

## Alternatives considered

**Reading A — opaque session token with side-band tenant claim (rejected).** Build per-request identity into the existing bearer credstore by adding `idp_issuer`/`idp_subject` columns to `api_keys`, plus a tenant-switch verb that mints a new bearer with a different tenant scope. Rejected because it would lock atlas into a bespoke session model that doesn't compose with future OIDC RP federation, and because the work to build it is comparable to the OAuth AS spine while ending in a non-standard place.

**Reading B — delegate AS to an external OSS project (Hydra, Authelia, Keycloak) (rejected).** Run an external AS as a peer process; atlas becomes a resource server consuming external-AS-issued JWTs. Rejected because the self-host story is the load-bearing v1 thesis — adding a second process to every self-host deployment is a friction tax that erodes the open-source pitch. Future work may revisit this for customers who already run one of these (Keycloak federation is a credible v3 conversation).

**Reading C — interim bespoke session work; OAuth AS later (rejected).** Ship bespoke per-request identity now (to unblock slice 141), commit to the OAuth AS path on a longer timeline. Rejected because (a) building two substrates in series wastes effort, (b) the bespoke work would be discarded, and (c) we have no production users — the constraint that would make C attractive (existing consumers with migration debt) does not apply.

**Reading D — internal OAuth 2.0 Authorization Server with JWT access tokens carrying tenant-in-claim (chosen).** See above.

## Slice 188 addendum — `/oauth/token` endpoint + token-exchange invariants

Slice 188 (2026-05-21) lit up `POST /oauth/token` with two grants — `client_credentials` (RFC 6749 §4.4) and token-exchange (RFC 8693). The slice locks four invariants on top of the slice-187 scaffolding:

**1. Token-exchange super_admin non-elevation (load-bearing safety).** The token-exchange handler MUST copy `atlas:super_admin` from the verified subject_token; it MUST NOT compute it, infer it from form parameters, or accept it from the request body. The exchange path is a tenant-swap verb, NOT a privilege-grant verb. `super_admin=true` is granted exclusively at OIDC login time (slice 142). P0-188-4 enforces this; AC-15 covers it with an integration test (`TestTokenEndpoint_TokenExchange_NeverElevatesSuperAdmin`).

**2. Signature-before-allowlist.** The token-exchange handler MUST verify the subject_token's JWS signature against the local keystore BEFORE reading any claim (including `atlas:available_tenants`) from the token. An unverified subject_token can claim arbitrary allowlists; only a signature-verified token is trusted as the basis for the tenant gate. P0-188-5 enforces this; the unit test `TestTokenEndpoint_TokenExchange_RejectsBadSignature` demonstrates the negative case (a foreign-signed token cannot influence the allowlist gate).

**3. Per-client rate limit (DoS mitigation).** The token endpoint runs a token-bucket limiter keyed on `client_id`. Default 60/min/client; configurable via `ATLAS_OAUTH_TOKEN_RATE_PER_MIN`. Returns 429 + `Retry-After`. The limit MUST be per-client, NOT per-IP — IP-based limits are bypassable behind NAT. P0-188-9 enforces this.

**4. Audit-log append-only invariant.** Every successful token-exchange writes one row to `oauth_token_exchanges` (append-only via two-policy RLS scoped to the target tenant — matches slice 030's `decisions_audit` precedent). The write is best-effort post-sign — the JWT response does not block on the audit-write commit (D3, slice 188 decisions log). The audit row is forensically airtight (jti + iss + sub + exchanged_at + ip_address); the absence of an UPDATE/DELETE policy under FORCE RLS makes mutation impossible from atlas_app.

**Discovery doc updates.** `grant_types_supported` advertises `["client_credentials", "urn:ietf:params:oauth:grant-type:token-exchange"]` exactly when a TokenEndpoint is wired. `token_endpoint_auth_methods_supported` is tightened to `["client_secret_post"]` — slice 188 does NOT implement HTTP Basic auth; advertising what we don't accept would mislead clients (a follow-on slice can re-add Basic when operator demand surfaces).

## References

- [`Plans/canvas/11-open-questions.md`](../../Plans/canvas/11-open-questions.md) #21 — resolution block
- [`Plans/canvas/09-tech-stack.md`](../../Plans/canvas/09-tech-stack.md) — Authorization Server row
- [ADR-0002 — Bearer-token storage](./0002-bearer-token-storage.md) — the predecessor decision the AS layer composes with, not replaces (`api_keys.token_hash` retains its HMAC-keyed shape during the deprecation window)
- [`docs/issues/187-oauth-as-scaffolding-jwt-signing.md`](../issues/187-oauth-as-scaffolding-jwt-signing.md) — foundation slice
- [`docs/audit-log/187-oauth-as-scaffolding-decisions.md`](../audit-log/187-oauth-as-scaffolding-decisions.md) — D1-D5 decisions log
- [`docs/issues/188-oauth-token-endpoint-token-exchange.md`](../issues/188-oauth-token-endpoint-token-exchange.md) — slice 188 spec
- [`docs/audit-log/188-oauth-token-endpoint-decisions.md`](../audit-log/188-oauth-token-endpoint-decisions.md) — slice 188 D1-D4 decisions log
- RFC 9068 — https://datatracker.ietf.org/doc/html/rfc9068
- RFC 8693 — https://datatracker.ietf.org/doc/html/rfc8693

---

[← ADR 0002 — Bearer-token storage](./0002-bearer-token-storage.md) · [ADR 0004 — Control detail 404 empty state →](./0004-control-detail-404-empty-state.md)
