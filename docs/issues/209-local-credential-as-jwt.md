# 209 — Local-credential AS: email/password → atlas_jwt (no external IdP required)

**Cluster:** Auth (backend + frontend)
**Estimate:** ~1d
**Type:** STANDARD
**Status:** `ready`
**Parent:** spillover surfaced 2026-05-22 during atlas-edge sign-in validation. Slice 197 removed the legacy bearer middleware (correct security move), but the only remaining unauthed entry points to atlas_jwt are (a) the OIDC callback flow and (b) `/v1/test/issue-jwt` (Playwright-only). Self-hosted operators who don't want to stand up an external IdP have NO working sign-in path. This slice closes that gap by promoting the existing `/auth/local/login` (which already verifies bcrypt-hashed credentials and creates a session) into a JWT issuer — atlas itself acts as the authorization server for non-SSO users.

## Narrative

The pattern is the standard "self-hosted AS" shape used by Gitea, Grafana, Mattermost, and most OSS server tools: the platform ships the AS plumbing in-process so non-SSO operators sign in with email/password without standing up a separate IdP. atlas already has all the pieces:

- **Credential verification** lives in `internal/auth/users/users.go:VerifyLocalLogin` (bcrypt, DB-backed, no oracle on failure).
- **Bootstrap user provisioning** is wired via `ATLAS_DEFAULT_USER_EMAIL` / `ATLAS_DEFAULT_USER_PASSWORD` env at startup.
- **Session storage** is the slice-034 `atlas_session` cookie + `auth_sessions` table.
- **JWT signing** is `tokensign.Signer`, the same instance the OAuth /token endpoint and the slice-201 test endpoint already use.

What's missing is the bridge: `LocalLogin` verifies credentials and creates a session, but never mints a JWT. Subsequent `/v1/*` API calls go through the slice-190 JWT middleware, which reads `atlas_jwt` cookie (or `Authorization: Bearer <jwt>` header). The session is invisible to the API surface. So an operator signing in via email/password gets a session cookie that authenticates nothing.

This slice mirrors the OAuth-callback finalize pattern (slice 189, `web/app/oauth/callback/route.ts:44`): atlas issues the JWT in the response body, the web app's server action sets `atlas_jwt` cookie. The cookie-setting authority stays in the web layer (consistent with the slice 197/198 architecture move).

### What ships in this slice

**Backend (atlas Go):**

- `authapi.Handler` gains `jwtSigner *tokensign.Signer` + `userResolver oauthapi.UserResolver` fields.
- `LocalLogin` after `VerifyLocalLogin` succeeds: calls `userResolver.ResolveForOAuth(ctx, usr.ID, usr.TenantID)` to capture roles + super_admin, builds `jwt.AtlasClaims` matching the shape `buildAtlasClaimsForUser` (`internal/api/oauth/pkce.go`) produces for the OAuth code-redemption path, signs via `jwtSigner.Sign(...)`, and includes the JWT in the response body as `{"token": "<JWT>"}`.
- `cmd/atlas/main.go` wires the existing `signer` + a `UserResolver` into `authapi.New(...)`. Both are already constructed when the DB pool is wired (the OAuth handler uses them).
- The existing `atlas_session` cookie still gets set — additive change, no breaking modification.

**Frontend (Next.js web):**

- `web/app/login/page.tsx` adds an email/password form ABOVE the existing bearer-paste field. The bearer paste stays for developer use cases (e.g. CI fixtures).
- New server action `signInLocal` in `web/app/login/actions.ts` — POSTs to `/auth/local/login`, reads `token` from response, sets `atlas_jwt` cookie via `cookies().set(SESSION_COOKIE, token, ...)` (mirrors slice 189's OAuth callback finalize endpoint at `web/app/oauth/callback/finalize/route.ts`).
- Tenant resolution for single-tenant deploys: the form reads `/v1/install-state` to determine the bootstrap tenant_id; multi-tenant tenant-picker UX is slice 141, explicitly out of scope here.

### What's out of scope

- **Multi-tenant tenant picker** → slice 141.
- **Password reset / recovery flow** → separate slice (no scope creep into account-recovery surface).
- **MFA (TOTP, WebAuthn)** → separate slice. The LocalLogin handler is the natural insertion point; adding it later doesn't break this slice's contract.
- **Refresh tokens** → mirrors OAuth flow today (no refresh; re-sign in at expiry). A refresh-token slice can cover both this and OAuth uniformly.
- **`/v1/test/issue-jwt`** → stays Playwright-gated. This slice doesn't change that surface.
- **Bearer-paste UI removal** → still useful for `atlas-cli credentials issue` workflows; defer to a later cleanup slice once OAuth + local cover the operator paths.

## Threat model

| STRIDE                | Threat                                                                                                                     | Mitigation                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| --------------------- | -------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | A submitted email+password could be guessed via brute force.                                                               | `users.VerifyLocalLogin` already uses bcrypt with the cost factor set at user-creation time. No oracle: failure path returns `ErrInvalidCredentials` whether the email exists or not. The `signInLocal` action surfaces a single generic "invalid credentials" error on the redirect. AC-7 verifies the no-oracle property end-to-end. _Future slice_ (out of scope here): per-IP rate limiting via the existing slice-188 token-bucket — the LocalLogin route is a natural insertion point. |
| **T** Tampering       | A submitted JWT could be a forged token claiming admin scope.                                                              | The JWT is minted server-side by `tokensign.Sign(...)` using the active ES256 key from the keystore — the client never proposes claims. The token's `sub` is set to `"user:" + usr.ID`, `CurrentTenantID` is read from the verified user row, and `Roles` / `SuperAdmin` come from `userResolver.ResolveForOAuth(...)` (which queries `user_roles` + `super_admins` under tenant-context RLS). No client-supplied data enters the claim set.                                                 |
| **R** Repudiation     | A signed-in user could deny they signed in.                                                                                | Same audit hook as the slice-188 OAuth token-exchange path: a row goes into `oauth_token_exchanges` with `from_tenant_id=NULL`, `to_tenant_id=usr.TenantID`, `subject_token_sub="user:<uuid>"`. AC-9 verifies the audit row materializes. The IP + UA still get captured to `auth_sessions` via the existing session-create call.                                                                                                                                                            |
| **I** Info disclosure | The JSON response now contains a token. A logged response body would leak the bearer.                                      | The bearer is short-lived (1h, matches `oauthapi.AccessTokenLifetime`). The standard HTTP access log captures method+path+status only, not bodies — verified against the existing slog middleware. The web layer's `signInLocal` action handles the response and discards it after extracting `token`; no client-side JS sees the token (the server action runs in Node, never sent to the browser).                                                                                         |
| **D** DoS             | bcrypt is CPU-heavy; an attacker could pin a server CPU by spamming `/auth/local/login`.                                   | The existing slice-188 rate-limit middleware does NOT cover `/auth/local/login`. This is a pre-existing exposure (LocalLogin was already vulnerable) — calling it out explicitly: this slice does NOT regress the situation, but a follow-on slice should add per-IP rate-limit specifically to the auth surface. Filed as deferred risk in the next-actions section below.                                                                                                                  |
| **E** EoP             | The biggest risk class: a request to `/auth/local/login` returns a JWT, and that JWT bypasses some part of the auth chain. | The JWT goes through the exact same `jwtmw.Middleware` validation as OAuth-issued tokens. AC-5 explicitly verifies: a JWT minted via this path is accepted by `/v1/me` AND validates with the same iss/aud/exp/nbf checks. AC-6 verifies a JWT minted with `SuperAdmin=false` is rejected by an endpoint requiring `IsAdmin` (proves the claim flow is honest about authorization scope).                                                                                                    |

## Acceptance criteria

- [ ] AC-1: `authapi.Handler` struct adds `jwtSigner *tokensign.Signer` and `userResolver oauthapi.UserResolver` fields. `New(...)` constructor takes both as new parameters (added at the end of the signature to minimize callsite churn). When either is `nil`, `LocalLogin` falls back to the pre-slice-209 behavior (sets `atlas_session` only, returns no `token` field). This preserves the unit-test surface that constructs Handler without OAuth wiring.
- [ ] AC-2: `LocalLogin` on a successful `VerifyLocalLogin` AND non-nil `jwtSigner`+`userResolver`: calls `userResolver.ResolveForOAuth(ctx, usr.ID, usr.TenantID)`, builds `jwt.AtlasClaims` matching the `buildAtlasClaimsForUser` shape (subject `"user:" + usr.ID.String()`, current tenant, available tenants, roles, super_admin, 1h expiry, atlas-edge issuer + audience), calls `h.jwtSigner.Sign(ctx, claims)`, includes `"token": "<JWT>"` in the response JSON. Backward compat: the `display`, `tenant_id`, `user_id` fields stay; `token` is additive.
- [ ] AC-3: `cmd/atlas/main.go` calls `authapi.New(oidcAuth, userStore, sessionStore, secureCookies, signer, oauthapi.NewDBUserResolverWithAuthPool(pool, resolverAuthPool))` when the DB pool is wired. The unit/in-memory path keeps passing `nil, nil` for the new params (AC-1's nil-fallback applies).
- [ ] AC-4: Unit test `internal/api/auth/http_test.go:TestLocalLoginIssuesJWT` exercises the success path: stubs `users.Store.VerifyLocalLogin` to return a User, stubs `UserResolver.ResolveForOAuth` to return roles + super_admin, verifies the response body contains a `token` field whose verified claims match the expected subject + tenant + roles.
- [ ] AC-5: Unit test `TestLocalLoginJWTAcceptedByJWTMiddleware` (or integration test) — wires the real `jwtmw.Middleware` against the issued JWT and verifies a subsequent `/v1/me`-shaped request is admitted with the right credential on context.
- [ ] AC-6: Unit test `TestLocalLoginJWTRespectsSuperAdminClaim` — a User without super_admin gets a JWT whose `SuperAdmin=false`. A `requireSuperAdmin` middleware (or equivalent OPA decision) on the verified claims rejects the request. Verifies claim → authorization integrity.
- [ ] AC-7: Unit test `TestLocalLoginNoOracle` — `VerifyLocalLogin` returning `ErrInvalidCredentials` produces the same response shape and status (401, body `{"error":"invalid credentials"}`) regardless of whether the email row exists or the password mismatched. Web-side equivalent verified in AC-12.
- [ ] AC-8: Unit test `TestLocalLoginSessionCookieStillSet` — the `atlas_session` cookie continues to be set on success (back-compat with code paths that read it for non-API uses).
- [ ] AC-9: Audit-row test (integration) — a successful `/auth/local/login` writes one row to `oauth_token_exchanges` with `from_tenant_id=NULL`, `to_tenant_id=usr.TenantID`, `subject_token_sub="user:<uuid>"`. Failures (401) do NOT write a row (no negative-info leakage).
- [ ] AC-10: `web/app/login/page.tsx` renders an email + password form ABOVE the bearer-paste field. The form's `action` is the new `signInLocal` server action. The form auto-populates `tenant_id` from a server-side `/v1/install-state` fetch when `first_install: true`.
- [ ] AC-11: New server action `signInLocal` in `web/app/login/actions.ts` — POSTs `{tenant_id, email, password}` to `${apiBaseURL()}/auth/local/login`. On 200, reads `token` from JSON body, sets `atlas_jwt` cookie (`SESSION_COOKIE` constant from `@/lib/auth`) with `HttpOnly`, `SameSite=Lax`, `secure` per `shouldUseSecureCookie(headers)`, `path: "/"`, `maxAge: 60 * 60` (matches `ATLAS_JWT_COOKIE_LIFETIME_SECONDS` from `web/app/oauth/callback/route.ts:50`). Redirects to `safeRedirectTarget(formData.get("from") ?? "/dashboard")`.
- [ ] AC-12: `signInLocal` on 401 from API: redirects to `/login?error=invalid+credentials&from=<safe-target>`. Generic message — no oracle (mirrors the existing bearer-paste error path).
- [ ] AC-13: Vitest `web/login/actions.test.ts` — mocks `fetch` for `/auth/local/login`, verifies the 200 path sets `atlas_jwt` cookie and redirects; verifies the 401 path redirects to the error page without setting the cookie.
- [ ] AC-14: Playwright e2e `web/e2e/local-credential-signin.spec.ts` — submits the form with `ATLAS_DEFAULT_USER_EMAIL` / `ATLAS_DEFAULT_USER_PASSWORD` against a docker-compose self-host fixture, lands on `/dashboard`, asserts `request.get('/v1/me')` returns 200 + the expected user identity.

## Decisions

- **D1: Cookie-setting authority.** atlas backend issues the JWT in response body; web app's server action sets `atlas_jwt`. Mirrors slice 189's OAuth callback finalize pattern. _Rejected alternative:_ atlas sets the cookie directly. _Reason:_ atlas's role is API + token issuance; cookie management is a browser-context concern owned by the web layer. Keeping that boundary clean is consistent with slice 197/198's architecture stance.
- **D2: JWT TTL = 1 hour.** Same as `oauthapi.AccessTokenLifetime`. Mismatched lifetimes would create surprising UX divergence between OAuth-signed-in and credential-signed-in users; uniformity is the simpler invariant.
- **D3: Tenant_id auto-resolution from `/v1/install-state`.** For single-tenant first-install deploys (where this slice is primarily useful), forcing the operator to type a UUID is hostile UX. The form fetches `/v1/install-state`; when `first_install: true`, populates the hidden `tenant_id` field from the install metadata. Multi-tenant picker is slice 141.
- **D4: Audit through `oauth_token_exchanges`.** Reuses the slice-188 table rather than inventing a new audit destination. The `from_tenant_id` column is nullable; we use that NULL value as the "initial mint" signal, identical to how OAuth-code-redemption is logged.
- **D5: Nil-signer fallback preserves test surface.** The unit-test harness in `internal/api/server_testing.go` constructs Handlers without OAuth wiring. Forcing those to wire `jwtSigner` would ripple through ~12 test files. Falling back to the pre-slice-209 behavior (no token in response) when signer is nil keeps the test surface intact AND documents the all-OIDC / no-credential-AS deployment shape as a first-class config (just don't wire the signer).

## Next actions (out of slice scope)

- **Rate-limit `/auth/local/login` per IP.** Slice 188's token-bucket lives at `internal/api/oauth/oauth_token_rate.go`; the same primitive can wrap the auth route. File when this slice merges.
- **Password reset flow.** Today the bootstrap user's password is set via `ATLAS_DEFAULT_USER_PASSWORD` env at startup. Operators who want to rotate it have no in-app path. Separate slice.
- **MFA enrollment.** TOTP (RFC 6238) is the lowest-risk first step; WebAuthn is the production goal. Both insert at the `LocalLogin` post-verify hook.
- **Refresh tokens.** Today both OAuth and (after this slice) local-credential users re-sign in at 1h expiry. A refresh slice can cover both uniformly.
