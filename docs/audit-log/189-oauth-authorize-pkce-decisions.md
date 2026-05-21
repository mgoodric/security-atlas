# Slice 189 JUDGMENT decisions

> Per the slice-189 spec, the engineer makes four subjective build-time
> calls during implementation and records them here. The maintainer
> reviews post-deployment and iterates if a decision warrants revision;
> the merge does NOT block on human sign-off (Amendment 1 / slice-073
> JUDGMENT-as-process).

**Slice:** [189-oauth-authorize-pkce-frontend.md](../issues/189-oauth-authorize-pkce-frontend.md)
**Engineer:** Claude (Opus 4.7 1M)
**Build date:** 2026-05-21
**PR:** TBD (filed on push)

---

## D1 — JWT cookie name + lifetime

**Decision:** Introduce a NEW `atlas_jwt` cookie alongside the existing
slice-034 `atlas_session` opaque session-id cookie. Lifetime = 1 hour
(matches the JWT `exp` from `oauth.AccessTokenLifetime`). Attributes:
`HttpOnly` + `Secure` (production) + `SameSite=Lax` + `Path=/`.

**Why not extend `atlas_session` to carry the JWT:**

- The slice-034 cookie is an **opaque random session id** read
  server-side from the `sessions` table. Swapping its meaning to a JWT
  changes the on-the-wire shape AND the read path (`sessions.Store.Read`
  via `sessions.CookieName`); every consumer would need a flag day.
- The slice-034 reads are still load-bearing for `/v1/me/sessions*`
  (slice 108/110). Reusing the cookie name with a different value type
  would silently break those reads.

**Why a new cookie now (vs. wait for slice 190):**

- Slice 190 retires the legacy `atlas_session` middleware on `/v1/*`
  and ships JWT validation. The cookie name choice is load-bearing for
  what the slice 190 middleware reads.
- Introducing the new cookie name NOW (with no consumer yet) gives
  slice 190 a clean migration target without breaking slice 034.

**Confidence:** HIGH. Two distinct cookies with distinct semantics is
the OAuth-RP-as-RP convention (see Okta / Auth0 / Keycloak — all keep
session and access-token cookies separate when they coexist).

---

## D2 — Audit log shape: extend `oauth_token_exchanges` vs new table

**Decision:** Reuse the slice-188 `oauth_token_exchanges` table for
`authorization_code` redemptions. Write ONE row per successful
redemption with:

- `subject_token_jti` = the redeemed auth code (forensic surrogate; the
  code is one-shot and unique)
- `from_tenant_id` = NULL (initial mint, no prior tenant)
- `to_tenant_id` = the user's `current_tenant_id`
- `subject_token_iss` = the user's `idp_issuer`
- `subject_token_sub` = `user:<uuid>` (mirrors the JWT `sub` claim)

**Why not a new `oauth_authorization_events` table:**

- Doubles the audit-table surface area without a forensic gain. Both
  tables would answer "which tenant did this caller acquire access to?"
  — that's the same question.
- The slice-188 audit query surface (admin SSO console, future
  RFC 7662 introspection) already SELECTs from `oauth_token_exchanges`;
  a new table fragments the read path.
- Slice 190's broader observability surface can extend
  `oauth_token_exchanges` with discriminator columns (`event_type`)
  rather than splitting tables. v1 ships the simpler shape.

**What we DO NOT audit at slice 189:**

- Auth-code **issuance** (the authorize-side event). Issuance is
  half of a redemption; without a matching redemption, the issuance
  was either superseded (rare — user retried login) or expired (sweeper
  cleaned). Slice 190's full observability work captures both halves.

**Confidence:** HIGH. The trade-off favors a single table; slice 190
gets to elaborate.

---

## D3 — PKCE-required (not configurable)

**Decision:** PKCE S256 is **mandatory** for browser clients. No env
var, no per-client opt-out, no `plain` method support.

**Why:**

- The Next.js frontend has no `client_secret` it can safely hold (any
  secret in JS-accessible code is browser-extractable).
- PKCE is the load-bearing primitive for public-client safety per
  OAuth 2.1 §4.5. Making it optional invites silent misconfiguration —
  an operator who flips the flag for "convenience" weakens every flow.
- The DB CHECK constraint enforces `code_challenge_method = 'S256'` —
  even a hand-crafted SQL insert cannot bypass the check.
- `plain` is rejected at three layers: application (`authorize.go`),
  DB CHECK (`oauth_auth_codes_method_s256_only`), and the discovery
  document (only `S256` advertised).

**What about future non-PKCE machine flows:**

- `client_credentials` (slice 188) does NOT use PKCE — it uses
  argon2id-verified client secrets. Those continue to work unchanged.
- The S256-only enforcement applies ONLY to the
  `grant_type=authorization_code` path.

**Confidence:** HIGH. OAuth 2.1 baseline; the cost of optionality
exceeds any benefit.

---

## D4 — Initial redirect-URI registry bootstrap

**Decision:** Operators register redirect URIs via the new
`atlas-cli oauth add-redirect-uri <client_id> <redirect_uri>`
command. No auto-registration on first boot.

**Rationale:**

- Auto-registering `http://localhost:3000` at first boot ties the
  registry to the docker-compose bring-up shape — operators running on
  Kubernetes (Helm) would inherit a localhost URI that's never used.
- The CLI command is small (≤30 lines net), runs in the same `oauth`
  command tree as the slice-188 `issue-client` operator surface, and
  composes cleanly with the bootstrap container.
- The CLI rejects non-HTTPS URIs unless prefixed `http://localhost` —
  this is the only mode that prevents accidental plain-HTTP
  registration without forcing operators to hand-edit `oauth_clients`
  rows.

**Operator workflow at first boot:**

```
atlas-cli oauth issue-client web-frontend
# returns: client_id=... client_secret=...
atlas-cli oauth add-redirect-uri <client_id> https://atlas.example.com/oauth/callback
```

**Self-host dev workflow:**

```
atlas-cli oauth add-redirect-uri <client_id> http://localhost:3000/oauth/callback
```

**Slice 191 will likely:** add an `ATLAS_OAUTH_DEFAULT_REDIRECT_URI`
env var the bootstrap container honors so the docker-compose bring-up
auto-registers the configured URI. v1 keeps the CLI-only path.

**Confidence:** MEDIUM-HIGH. There's a real operator-ergonomics
argument for auto-registration; deferring it is the smaller surface.

---

## Provenance

- Slice spec: `docs/issues/189-oauth-authorize-pkce-frontend.md`
- Predecessor: slice 188 (`/oauth/token` + RFC 8693)
- ADR: `docs/adr/0003-oauth-authorization-server.md` (slice 187
  foundation; addended below for slice 189)
- Migrations: `migrations/sql/20260521000030_oauth_auth_codes.sql` +
  `migrations/sql/20260521000040_oauth_client_redirect_uris.sql`
