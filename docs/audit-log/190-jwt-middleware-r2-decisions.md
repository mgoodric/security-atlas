# Slice 190 — JWT validation middleware + revoke + introspect: decisions log

Slice 190 is the OAuth AS **cutover slice**. It composes the
issuance-side primitives shipped in slices 187-189 (signing keys,
`/oauth/token`, PKCE authorize) into the consumption side: a JWT
validation middleware on `/v1/*`, the `/oauth/revoke` (RFC 7009) +
`/oauth/introspect` (RFC 7662) endpoints, a revocation list +
audit log, and a sweeper goroutine.

The four engineer decisions enumerated in the slice spec are
resolved below. Confidence is annotated per decision.

---

## D1 — Cookie vs header precedence in middleware

**Decision:** `Authorization: Bearer <jwt>` header takes precedence
over the cookie when both are present.

**Rationale:**

- The Authorization header is an EXPLICIT client signal. The cookie
  is implicit — set by the browser at OAuth completion (slice 189)
  and replayed automatically on every same-origin request. When an
  API client + browser session both target the same origin, the
  API client's header is the more recent, more deliberate
  intention.
- RFC 6750 §2.1 endorses the Authorization header as the
  canonical bearer-token transport. The cookie path is an
  atlas-specific convenience; it should not override the
  standards-aligned path.
- If both are present and one is invalid, fall back to the OTHER
  rather than rejecting outright? **No** — D3 (below) commits to
  fail-closed when the JWT-shaped path is taken; precedence
  resolution happens BEFORE the verify step, so this concern is
  scoped to "which token do we try first" and the answer is
  always "header".

**Confidence:** HIGH. The shape matches RFC 6750 + every other
production OAuth resource server.

**Implementation:** `internal/auth/jwtmw/middleware.go::extractJWT`
checks the Authorization header first; only falls back to the
cookie when the header is absent OR the header value is not
JWT-shaped (`Bearer atlas_*` opaque tokens leave the header path
untouched).

---

## D2 — Audit table location (extend `oauth_token_exchanges` vs new table)

**Decision:** NEW `oauth_revocation_events` table, separate from
`oauth_token_exchanges`.

**Rationale:**

- Orthogonal semantics. `oauth_token_exchanges` records the
  CREATION of new tokens via RFC 8693 grant; `oauth_revocation_events`
  records the DESTRUCTION of existing tokens via RFC 7009. The two
  streams have different operational meanings; an operator running
  "show me last hour's revocations" should not need to filter
  exchange rows.
- Different tenancy semantics. `oauth_token_exchanges` is
  tenant-scoped (the row's `tenant_id` is the TARGET tenant of the
  exchange — see slice 188 migration header). `oauth_revocation_events`
  is NOT tenant-scoped because revocation is identity-scoped
  (slice 190 spec). Forcing both into the same table would create
  a schema with optional tenant_id, weakening the slice 188 RLS
  story.
- Append-only invariant is preserved for both, independently.
- Slice 030 (`decisions_audit`) and slice 036 (`artifact_access_log`)
  already establish the pattern of "one append-only table per
  audit stream"; this slice continues the pattern.

**Confidence:** HIGH. The clean separation makes incident response
queries simpler — both tables are append-only, both have indices on
their natural lookup keys, and neither's RLS policy needs to know
about the other.

**Implementation:** `migrations/sql/20260521000050_oauth_revoked_tokens.sql`
creates both tables. `internal/auth/revocation/revocation.go::Revoke`
writes BOTH the hot-path `oauth_revoked_tokens` row AND the
append-only `oauth_revocation_events` row in one transaction.

---

## D3 — Legacy-bearer coexistence cutover semantics

**Decision:** JWT first, legacy as fall-through. The resolution
order is:

1. If `Authorization: Bearer eyJ*` header is present OR a JWT-
   bearing cookie is present, the JWT middleware runs. On success,
   the request proceeds with JWT-derived context. On failure, the
   request returns 401 — NO fall-through to legacy.
2. If neither a JWT-shaped header nor a cookie is present, the JWT
   middleware is a no-op pass-through. The legacy bearer middleware
   then handles the request (`Bearer atlas_*` opaque tokens).
3. If both auth methods fail, the legacy bearer middleware's 401 is
   what the client sees.

**Rationale:**

- **Fail-closed on JWT-shaped failure** (P0-190-1 prevention). The
  classic "tries any auth method until one succeeds" anti-pattern
  lets a forged JWT shape bypass via "JWT path fails → try legacy →
  legacy fails for OTHER reasons → return some confused 401". By
  committing to "JWT shape → JWT path is dispositive", we eliminate
  the bypass window.
- The opaque-bearer shape (`atlas_<32-char-b32>`) is
  unambiguously NOT a JWT — no dots, no `eyJ` prefix. Static shape
  detection is reliable.
- The cookie path opt-in is gated on a configurable cookie name
  (default `atlas_session`); when absent in the request the JWT
  middleware never tries to read it.
- Slice 191 retires the legacy bearer middleware; this coexistence
  is a migration-window contract, not a permanent invariant.

**Operational implication:** A request with BOTH a JWT cookie AND
a legacy bearer header is treated as a JWT request (the cookie is
checked when the header is absent OR not JWT-shaped). A request
with BOTH a `Bearer atlas_*` (legacy) header AND a JWT cookie
follows the legacy path (the legacy header is not JWT-shaped, so
the JWT middleware ignores it AT THE HEADER STEP, then falls
through to check the cookie). When the cookie IS JWT-shaped, the
JWT path wins — the cookie's intent is explicit.

The lone edge case is a request that has a legacy `Bearer atlas_*`
header AND a JWT cookie. Decision: cookie wins (JWT path). This
matches the user expectation of "I logged in via the browser; my
cookie is the active session" over "this client also happens to
hold an API key". Operators wanting opaque-only auth should clear
the cookie.

**Confidence:** MEDIUM-HIGH. The edge-case "both a legacy header
and a JWT cookie" is rare in practice; documenting it explicitly
satisfies the discoverability concern.

**Implementation:** `internal/auth/jwtmw/middleware.go::extractJWT`
shape-filters the header; `internal/api/httpserver.go::httpAuthMiddlewareWithExemptions`
checks `jwtmw.FromContext` first and skips the legacy verify when
JWT context is already populated.

---

## D4 — Sweeper interval default (5 minutes)

**Decision:** 5-minute interval.

**Rationale:**

- Matches slice 189's `runAuthCodeSweeper` interval. Operators
  expect the two oauth sweepers to have similar cadence;
  consistency reduces operational surprise.
- DB load is negligible. A revocation row at steady state has a
  ~1 hour lifetime (matches token expiry); even at 10K
  revocations/day the table holds ~415 rows. The DELETE is an
  index-range scan on `(expires_at)`.
- The shorter the interval, the more responsive the cleanup. 1
  minute would be slightly more responsive but the marginal
  benefit is small — expired tokens are already rejected by the
  JWT validator's `exp` check, so the revocation row is only
  meaningful when `expires_at > now()`.
- Configurable in a future slice if operators report friction. For
  v1 of the cutover the constant suffices.

**Confidence:** HIGH.

**Implementation:** `cmd/atlas/main.go::runRevokedTokenSweeper`
hard-codes `const interval = 5 * time.Minute`. A
`ATLAS_OAUTH_REVOKED_SWEEP_MIN` env var can graduate this to
configurable in slice 191 if needed.

---

## Cross-cutting notes

### What this slice deliberately does NOT do

- **Refresh-token grant** — explicitly deferred per P0-190-7. Slice
  spec does not allow it; v3 deferred per slice 187 spillover.
- **Slice 034 legacy bearer middleware retirement** — explicitly
  deferred per P0-190-1. Slice 191 retires it.
- **DPoP, mTLS client auth** — explicitly deferred per slice 187
  spillover.

### What needed to happen at the integration boundary that wasn't in the spec

- The JWT middleware synthesizes a `credstore.Credential` from the
  verified claims and attaches it via `authctx.WithCredential`.
  Rationale: downstream chi middleware (`tenancymw`, `authzmw`)
  reads credential-shaped context to perform tenant + RBAC checks.
  Without the synthesized credential, every JWT-authenticated
  request would default-deny at the authz layer. The synthesized
  credential is in-memory only — never persisted, never reachable
  via the bearer-auth resolver paths.

### Schema-grant change vs spec

The slice spec described `GRANT SELECT, INSERT, DELETE` on
`oauth_revoked_tokens` — no UPDATE. The original Go layer
`Revoke()` implementation used `ON CONFLICT (jti) DO UPDATE` for
idempotency, which Postgres requires UPDATE privilege for. The
fix was to change to `ON CONFLICT (jti) DO NOTHING` (the first
revocation wins; subsequent calls are silent no-ops on the
hot-path table but still append audit rows). The spec's grant
shape was correct; the implementation was originally wrong. Now
both align as defense in depth.

### Where to look if this needs revisiting

- Coexistence cutover: `internal/api/httpserver.go::httpAuthMiddlewareWithExemptions`
- Shape filter: `internal/auth/jwtmw/middleware.go::extractJWT`
- Idempotency: `internal/auth/revocation/revocation.go::Revoke`
- Discovery doc auth methods: `internal/api/oauth/oauth.go::discoveryDocument`
- Sweeper: `cmd/atlas/main.go::runRevokedTokenSweeper`

---

**Filed during slice 190 build, 2026-05-21.**
