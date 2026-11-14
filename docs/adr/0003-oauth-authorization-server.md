# ADR 0003 — OAuth 2.0 Authorization Server with JWT access tokens carrying tenant-in-claim

**Status:** Accepted · Scaffolding shipped (slice 187, 2026-05-20) · Key rotation implemented (slice 366, 2026-05-29)

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

**Status: implemented end-to-end (slice 366, 2026-05-29).** Slice 187 shipped the multi-key data structures + multi-key JWKS support; slice 366 lit up the orchestration (`fsstore.Store.Rotate` / `Prune` / `List`, the `atlas-cli keys` commands, the scheduled rotation job in `cmd/atlas`, and the operational runbook). Closes the slice-327 security audit finding M-1.

The shipped shape:

- **Cadence:** annual default (`ATLAS_KEY_ROTATION_INTERVAL`, Go duration string). The slice-366 spec landed on annual as the v1 default — NIST SP 800-57 recommends 1-3 years for ES256 signing keys, and annual is comfortably inside that band for a forward-security rotation (operators on high-security deployments shorten via the env var, e.g. `2160h` for 90 days). This supersedes the 90-day figure penciled in the original draft of this section; the discrepancy and its rationale are recorded in `docs/audit-log/366-jwt-key-rotation-decisions.md` (D2).
- **Overlap window:** 24 hours (`fsstore.DefaultRotationOverlap`). Access tokens default to 1h TTL (slice 188); 24h is 24× that — sufficient time for every refresh-token holder to obtain a new access token under the new key before the old key sunsets. The prune cutoff is `now − AccessTokenLifetime − overlap`, so no JWT in flight is ever invalidated mid-validation (P0-366-3).
- **JWKS cache TTL:** 1 hour (`Cache-Control: max-age=3600` on JWKS responses). Verifiers re-fetch hourly; during the 24h overlap they will see both keys ~24×.
- **Existing token treatment:** tokens signed with the rotated-out key keep working until natural expiry. NO revocation on rotation. Rotation is for forward security (defense against undiscovered key exposure), not for incident response — incident response uses `/oauth/revoke` (slice 190) or an emergency prune-with-`--overlap 0` (see the runbook).
- **Audit trail:** every rotation writes a structured platform-level audit log line (`audit_event=key_rotation`) carrying the previous + new signing KeyIDs. Key material never appears in any log (P0-366-1 / P0-187-6).
- **Active-key protection:** the active signing key is never pruned regardless of age (P0-366-2).

**Operational guide:** `docs/runbooks/jwt-key-rotation.md` covers routine annual rotation, manual rotation, emergency (suspected-compromise) rotation, key recovery, and JWKS publication impact.

**Revisit (deferred to v3):** a KMS/HSM-backed keystore backend (AWS KMS, GCP KMS, PKCS#11 HSM) where the private key never leaves the security module and rotation is the KMS's responsibility. The `keystore.KeyStore` interface was designed (slice 187) so a KMS backend slots in without changing any caller; the v1 filesystem backend is the self-host-friendly default. Tracked as a separate v3 ADR per the slice-366 decisions log.

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

## Slice 189 addendum — `/oauth/authorize` + PKCE + redirect-URI registry

Slice 189 (2026-05-21) lit up `GET /oauth/authorize` (RFC 6749 §4.1 Authorization Code grant) hardened with PKCE S256 (RFC 7636) and extended `/oauth/token` with the `authorization_code` grant. Four invariants land on top of the slice-187/188 scaffolding:

**1. PKCE S256 is mandatory for the browser flow.** The `code_challenge_method` parameter is restricted to `S256` at three layers: the application handler (`authorize.go` rejects `plain` with 400), the DB CHECK constraint (`oauth_auth_codes_method_s256_only`), and the discovery document (`code_challenge_methods_supported = ["S256"]`). `plain` is forbidden because the Next.js frontend cannot safely hold a `client_secret`, and PKCE is the load-bearing primitive for public-client safety per OAuth 2.1 §4.5. P0-189-1 enforces this; the unit test `TestComputePKCEChallengeS256` exercises the RFC 7636 Appendix B vector; the integration test `TestIntegrationAuthorizeFlow_PlainPKCERejected` covers the negative path.

**2. Redirect-URI registration is the open-redirect gate.** The `oauth_client_redirect_uris` table (UNIQUE on `(client_id, redirect_uri)`) is the source of truth — the authorize handler validates the requested `redirect_uri` against this registry BEFORE issuing any code OR generating any browser redirect. Unregistered URIs return 400 with no `Location` header set, so an attacker cannot use the authorize endpoint as an open redirector. P0-189-2 enforces this; the integration test `TestIntegrationAuthorizeFlow_UnregisteredRedirectURIRejected` explicitly verifies the absence of a leak. Operators register URIs via `atlas-cli oauth add-redirect-uri <client_id> <redirect_uri>` (rejects non-https non-localhost URIs at the CLI layer).

**3. Authorization codes are one-shot.** The `oauth_auth_codes` table has a nullable `consumed_at` column; the redemption path uses a `SELECT … FOR UPDATE` + `UPDATE … WHERE consumed_at IS NULL RETURNING …` pattern inside one transaction. A second redemption attempt returns 0 rows and is collapsed to `invalid_grant`. Codes expire 60 seconds after issuance; the sweeper goroutine DELETEs rows older than 1 hour (grace beyond the TTL avoids races with in-flight redemptions). P0-189-3 enforces this; the integration test `TestIntegrationAuthorizeCodeRedemption_CodeReuse` exercises the path.

**4. Frontend verifier never persists beyond the tab session.** The Next.js `web/lib/auth/oauth-client.ts` module stores the PKCE `code_verifier` in `sessionStorage` (NOT `localStorage` — P0-189-8). The verifier is cleared after a successful redemption. The JWT-bearing `atlas_jwt` cookie minted by the callback route is HttpOnly + Secure (production) + SameSite=Lax + Path=/ (P0-189-9). The vitest unit test verifies the localStorage-bypass + sessionStorage-write discipline.

**Cookie strategy (D1 slice 189):** the OAuth flow mints a NEW `atlas_jwt` cookie carrying the JWT directly. The slice-034 `atlas_session` opaque session-id cookie continues to exist alongside (slice 190 retires it). This is the cleanest migration path — `atlas_session` reads (slice 108/110 `/v1/me/sessions*`) keep working unchanged while slice 190's JWT validation middleware reads from `atlas_jwt`.

**Discovery doc updates.** When BOTH a `TokenEndpoint` AND an `AuthorizeEndpoint` are wired, `grant_types_supported` adds `"authorization_code"` to the slice-188 list. `response_types_supported` is unchanged (`["code"]` — slice 187 set it). `code_challenge_methods_supported` is unchanged (`["S256"]` — slice 187 set it).

## Slice 192 addendum — spine completion: multi-tenant switch + frontend tenant switcher

Slice 192 (2026-05-21) closes the auth-substrate-v2 spine. With slice 192 merged, OQ #21 Reading D is fully implemented end-to-end:

- 187 ✅ keystore + JWT signing + JWKS + discovery
- 188 ✅ `/oauth/token` + RFC 8693 token-exchange
- 189 ✅ `/oauth/authorize` + PKCE + frontend OAuth client
- 190 ✅ JWT validation middleware + revoke + introspect (cutover)
- 191 ✅ SDK migration + RFC 8628 device-code + slice 034 partial retirement
- **192 ✅ multi-tenant switch + frontend tenant switcher**

**Slice 192 ships:**

- **`GET /v1/me/tenants` handler** (`internal/api/me/tenants.go`) — reads the caller's verified JWT claim `atlas:available_tenants[]` and enriches with tenant names via a PK-bounded `SELECT id, name FROM tenants WHERE id = ANY(...)` on the BYPASSRLS pool. No full table scan; the bounded set is the JWT claim's tenant list (P0-192-2).
- **Frontend tenant-switcher dropdown** (`web/components/auth/tenant-switcher.tsx`) — mounted in the persistent header `TopBar`. Hidden when `tenants.length <= 1` (canvas §11 #13 commitment, P0-192-1).
- **`switchTenant()` client function** (`web/lib/auth/switch-tenant.ts`) — calls a BFF route which in turn POSTs to `/oauth/token` with `grant_type=urn:ietf:params:oauth:grant-type:token-exchange` (slice 188's primitive). The BFF rotates the `atlas_jwt` cookie on success; the frontend calls `router.refresh()` so server components re-render.
- **Login picker page** (`web/app/oauth/select-tenant/page.tsx`) — destination for operators with ≥2 tenants; defensive single-tenant redirect to `/dashboard`.
- **Membership-removed UX banner** — surfaces when the periodic re-fetch (60s, D1) reveals the current tenant has been removed from the operator's available set. Default action: switch to the first available alternative (P0-192-7).
- **DBUserResolver expansion** (`internal/api/oauth/user_resolver.go`) — the OAuth authorize flow's user-identity snapshot now enumerates ALL tenants the OIDC subject belongs to via a cross-tenant `users` lookup on the BYPASSRLS pool. The JWT minted at code-redemption carries an honest `atlas:available_tenants[]`.

**Eventual eviction invariant.** Slice 192 documents the OAuth standard contract: when an admin removes a user from a tenant, the user's existing JWTs continue to validate until expiry. To force immediate eviction, the admin calls `/oauth/revoke` (slice 190). The operator-facing doc at `docs-site/docs/tenant-membership.md` explains the contract; P0-192-8 commits to documenting it rather than apologising for it.

**Closure of slice 141 (P0-192-11).** Slice 141 (the original "multi-tenant login + switcher" spec, PARKED 2026-05-20 when OQ #21 Reading D resolved the substrate ambiguity at the canvas level) is closed atomically with this slice's merge. Its `_STATUS.md` row flips from `not-ready` to a new status `merged-via-spine-completion` — the historical fact is that slice 141's intent shipped, but via slices 187-192 rather than via the original schema rewrite spec.

**Spine completion summary.** A vCISO hosting atlas for N client tenants can:

1. Sign in once via OIDC.
2. Receive a JWT carrying `atlas:available_tenants[]` with all N tenant UUIDs.
3. Use the persistent header dropdown to switch between client tenants without re-authenticating — each switch is one RFC 8693 token-exchange call against `/oauth/token` (slice 188).
4. See the membership-removed banner when an admin removes them from a tenant.

The binary vCISO success criterion from slice 141 is fulfilled.

## Amendment (2026-11-13, #1851): super_admin on install-wide admin routes

A per-tenant admin API key sets `cred.IsAdmin` but never carries
`atlas:super_admin`. Two `/v1/admin` routes had install-wide effects behind
`cred.IsAdmin` alone, so any tenant's admin could reach them. They now use the
verified-JWT `atlas:super_admin` check that #1757 put on the global catalog
write routes (re-read per request since #1853):

- **`POST /v1/admin/demo/seed` and `POST /v1/admin/demo/teardown`** create or
  delete a whole tenant through the BYPASSRLS pool. They require
  `atlas:super_admin`, in addition to the OPA admin-role check. No fallback.
- **`POST /v1/admin/install/reset-bootstrap`** clears the install-wide
  `first_signin_at` and `bootstrap_token_consumed_at` markers and rewrites the
  install's `bootstrap-token` file. It admits `atlas:super_admin`, **or** an
  admin credential while the install has exactly one tenant.

**Why the reset keeps an admin-key fallback.** The reset is the recovery path
for an install whose sign-in is broken, run from `atlas-cli credentials issue
--reset-bootstrap`. Then there may be no way to get a JWT, and the operator
holds only an admin API key. On a single-tenant install that key's tenant is
the whole install, so admitting it is no cross-tenant effect. Once a second
tenant exists, a tenant admin key is refused (403) and the operator signs in
as a super_admin and passes that JWT as `--token`. The tenant count is read
on each request through the BYPASSRLS pool (`platform.Status.TenantCount`).

**The CLI presents the operator's credential.** `--reset-bootstrap` now sends
the caller's `--token` as the bearer for the reset POST. It used to send the
key it had just minted, which is a non-admin key and could never pass the
gate.

**The token is validated.** The request's `token` must authenticate as a
live (unrevoked, unexpired) credential in the server's credential store, the
store the CLI's `AdminCredentialsService.Issue` call mints into. A
caller-chosen string is refused with 400 before anything is written, because
the login page points operators at that file. Authenticating the token is
stricter than checking its shape: a well-formed string that was never issued
is refused too.

(Superseded by the #1885 amendment below: the endpoint no longer takes a
token.)

## Amendment (2026-11-13, #1885): recovery mints an admin bootstrap credential

**What the #1851 token check proved.** It accepted any live, unrevoked
credential in the store: every rehydrated `api_keys` row, of any tenant and
any privilege. It proved the token was live. It did not prove the token was
an admin credential, or bound to the caller's tenant.

**The re-issued token was not an admin credential.** `--reset-bootstrap`
minted the token through `AdminCredentialsService.Issue` → `admin.Service.Issue`
→ `credstore.Store.Issue`, which mints non-admin keys only. The cold-start
bootstrap credential is an admin credential (`IssueBootstrapFixedAdminCredential`),
and the login page stores the pasted token as the session bearer, so signing
in with a recovered token gave a non-admin session. The recovery path did not
recover admin access.

**Decision: recovery mints an admin credential, server-side, behind the
gate.** `POST /v1/admin/install/reset-bootstrap` no longer accepts a token.
After `authorizeResetBootstrap` admits the caller (super_admin, or an admin
credential on a single-tenant install) and the foot-gun gate passes, the
handler mints an admin credential (`credstore.Store.IssueAdmin`, no expiry,
like the cold-start credential) for the caller's tenant, writes its bearer
to the bootstrap-token file, and returns it once in the response.

- **Reachable only through the gate.** No other runtime path mints an admin
  credential for the file. The admin gRPC `Issue` RPC still mints non-admin
  keys only; it is not widened. A non-gated caller (non-admin key, missing
  credential, tenant admin key on a multi-tenant install) is refused before
  anything is minted, and a reset refused by the foot-gun gate (409) mints
  nothing, because the mint runs after the status reset.
- **Tenant-bound.** The credential is for the caller's own tenant. The CLI
  sends `--tenant` as `tenant_id`; a value other than the caller's tenant is
  refused with 400 rather than ignored. The credential is a tenant admin
  credential, not super_admin: it carries `IsAdmin`, never
  `atlas:super_admin`. On a single-tenant install that is the whole install,
  and it passes this same gate again for a later recovery.
- **Caller tokens are ignored.** A `token` field sent by an older CLI is
  dropped; the file never holds a caller-chosen string. This retires the
  #1851 token check rather than tightening it.
- **No orphaned admin key.** If the file write fails after the mint, the
  credential is revoked, since its bearer is not returned on that path.

**Rejected: keep the non-admin re-issue and document it.** Recovery exists
for an operator who has lost admin access. A recovered non-admin session
cannot reach admin pages or re-run this gate, so the operator would still
need an admin key from elsewhere, which is the situation recovery is for.

(The "no expiry" above is superseded by the #1892 amendment below.)

## Amendment (2026-11-13, #1892): the recovery key is bounded, marked and traceable

The #1885 key had the right privilege but no end. It never expired, it
outlived the next reset (the file was overwritten, the key was not
revoked), and nothing recorded who minted it.

**Decision.**

- **Bounded TTL.** The recovery key expires 24h after issue by default.
  The operator can set `ttl_seconds` (CLI `--ttl`) up to 7d. Zero or
  absent means the default. A negative value, a value above 7d, and a
  non-expiring key are refused with 400. 24h is longer than the 8h session
  cookie the bearer-paste sign-in sets, so the session it opens is not cut
  short, and short enough that a forgotten key dies the next day. 7d covers
  a recovery that waits over a weekend.
- **TTL is enforced in memory.** `credstore.Store.Authenticate` and `List`
  now reject a key past `IssuedAt + TTL`. Until now the in-memory store
  applied TTL only by not reloading expired rows at boot, while the
  DB-backed `apikeystore` already enforced `expires_at`. `ttl=0` still
  means no expiry, which the env `ATLAS_BOOTSTRAP_TOKEN` key, the
  `ATLAS_BOOTSTRAP_TENANT` key and the chaos and CI keys (`--ttl 0`) rely on.
- **Marked, durably.** The key is minted with `api_keys.purpose =
'bootstrap-recovery'` (migration `20260814000000_api_keys_purpose`). It is
  reloaded at boot, and a rotated successor inherits it. Revocation finds
  recovery keys by this marker and the credential id, never by the bearer.
  The env `ATLAS_BOOTSTRAP_TOKEN` key is memory-only and unmarked, and no
  other issue path sets the marker, so neither revocation below can reach
  any key but a recovery key.
- **Each reset revokes the recovery keys earlier resets minted.** This runs
  after the new bearer is in the file, so the file only ever names a live
  key.
- **First sign-in by a different credential revokes it.** When
  `mark-first-signin` flips the marker, it revokes every recovery key except
  the caller's own. When the caller _is_ the recovery key (the bearer-paste
  sign-in uses the pasted key as the session), it is kept live until it
  expires, because revoking it would sign the operator straight back out.
  The response says which happened (`revoked_credential_ids`,
  `recovery_key_kept`). The documented step is to sign in another way and
  revoke it: `atlas-cli credentials revoke --id <credential_id>`.
  `mark-first-signin` requires an admin credential (#1906), so a
  low-privilege key cannot call it first and revoke the recovery key out
  from under the operator. The recovery key is itself an admin key.
- **Rotation keeps the bound and the marker.** `Rotate` gives a recovery
  key's successor the same TTL, counted from the successor's own issue
  time, so it gets a fresh window. The successor keeps the
  `bootstrap-recovery` marker, so the next reset or first sign-in still
  revokes it.
- **Revocation is install-wide.** Both revocations above match every live
  recovery key on the install, across tenants, not only the caller's
  tenant. The reset clears install-wide markers and rewrites the
  install's one bootstrap-token file, so its recovery keys are an
  install-wide set.
- **Traceable.** The mint is logged at Info with `credential_id`,
  `tenant_id`, `expires_at`, the caller's credential id and subject, and the
  ids it revoked. The bearer is never logged, in whole or in part. A caller
  that is a user (a JWT `user:<uuid>` subject, or a key issued by a user) is
  recorded in a new column, `api_keys.minted_by`, and on the credential as
  `MintedBy`. Both are attribution only, and nothing authorizes on them. An
  API-key caller with no user records no minter; the log line names its
  credential id. There is no separate admin audit-event sink. The OPA
  decision audit already records the request.
- **The recovery key never acts as its minter.** The ticket asked for
  `api_keys.issued_by`, but the platform reads `issued_by` as the user the
  key belongs to. `credentialFromRow` maps it to the key's `UserID`, which
  `/v1/me` and approval records (`approver_user_id`) resolve. The
  policy-acknowledgment denominators count distinct `issued_by` over admin
  keys. Writing the minter there would make the recovery key act as, and
  count as a key of, the operator who minted it. So a recovery key keeps
  `issued_by` NULL and its `UserID` equal to its own credential id, before
  and after a restart.
- **The all-zeros tenant is refused.** A super_admin JWT with no current
  tenant carries `00000000-…`, which passed the empty-tenant check. It is now
  refused with 400 before the reset, as the empty tenant is.
- **The CLI no longer drops flags.** `--reset-bootstrap` refuses `--scope`
  and `--kinds`, which do not apply to an admin key, and sends `--ttl` as
  `ttl_seconds`.

**Rejected: track reset-minted ids in memory.** A restart between the reset
and the sign-in would lose the id, and the next reset would silently stop
revoking. The marker costs one nullable column.

## Amendment (2026-11-13, #1947): admin-minted keys outlive their minter; revocation holds on every replica

Two gaps were found in the #1915 review. First, a key an admin mints through
`POST /v1/admin/credentials` or gRPC `AdminCredentialsService.Issue` had no
link to that admin. `issued_by` is NULL on these keys, so they kept working
after the admin was disabled, and nothing recorded that this was intended.
Second, `RevokeUserInMemory` reaches only the replica that ran the
deactivation. Other replicas relied on the per-request user gate. A SCIM
reprovision reopens that gate, so a key revoked in `api_keys` authenticated
again on those replicas until restart.

**Decision.**

- **Admin-minted keys are machine keys and outlive the admin who minted
  them.** These keys belong to connectors and CI jobs. If disabling an
  admin revoked every key they minted, offboarding them would silently stop
  evidence ingestion, which is an audit-continuity failure. The keys keep
  `issued_by` NULL and `UserID` equal to their own credential id, as the
  recovery key does. Disabling, deprovisioning or deleting the minting
  admin leaves them working.
- **The minter is recorded, for attribution only.** Both issue paths now
  write the calling user to `api_keys.minted_by`, the #1892 column, and to
  `Credential.MintedBy` (`credstore.MinterUserID` reads the caller). A
  rotated successor inherits it. Nothing authorizes on it. An API-key caller
  with no user records no minter.
- **Operators revoke these keys deliberately.** When an admin leaves, list
  the keys they minted (`api_keys.minted_by`) and revoke the ones that
  should not outlive them: `atlas-cli credentials revoke --id
<credential_id>` or `POST /v1/admin/credentials/{id}/revoke`. A follow-up
  ticket adds an admin-UI warning on the user-disable screen that lists that
  user's still-live minted keys.
- **A durable revocation holds on every replica.** For a user-owned key
  with an `api_keys` row, the per-request user gate now also reads that row.
  It refuses the key when `revoked_at` is set or the row is missing, before
  checking the owner's status. Reprovisioning the user therefore cannot
  bring a revoked key back on any replica. The cost is one primary-key read
  per authentication of a user-owned key. Machine keys skip the gate as
  before.

**Rejected: revoke minted keys when the minter is disabled.** The model is
simpler, but it turns staff offboarding into an ingestion outage, and the
operator gets no chance to rotate the keys first.

**Rejected: cross-replica revocation sync.** The gate already reads the
database on every user-owned key authentication, so checking `revoked_at`
in that read closes the gap without a new channel.

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

See also: [`docs-site/docs/oauth-grants.md`](../../docs-site/docs/oauth-grants.md) — per-grant endpoint-to-file landing map for `internal/api/oauth/` (slice 325).

---

[← ADR 0002 — Bearer-token storage](./0002-bearer-token-storage.md) · [ADR 0004 — Control detail 404 empty state →](./0004-control-detail-404-empty-state.md)
