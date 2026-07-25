# Slice 478 — Decisions Log

Super-admin user↔tenant↔role assignment API (incl. self-assignment).

JUDGMENT slice. Per the project workflow, the implementing agent makes the
subjective build-time calls and records them here rather than blocking the merge
on a human sign-off. The maintainer iterates post-deployment.

---

## Context discovered during grill-with-docs

- `internal/api/adminusers` **already exists** (slice 062): `GET /v1/admin/users`,
  `GET /v1/admin/users/{id}`, `PATCH /v1/admin/users/{id}/roles` — all
  **within-tenant only** (tenant read from `cred.TenantID`, never the wire). This
  is the slice-060.5 partial. Slice 478 ADDS the cross-tenant super-admin surface
  - the assign/revoke verbs + the local-auth membership mechanism, reusing this
    package.
- `available_tenants` is computed in `internal/api/oauth/user_resolver.go`
  `enumerateMemberships`: `SELECT DISTINCT tenant_id, id FROM users WHERE
idp_issuer=$1 AND idp_subject=$2 AND status='active'`. A user's tenant
  membership IS a `users` row per tenant. Roles come from `user_roles`
  (slice-035 enum).
- `internal/api/admintenants` (slice 143) is the closest analog: its
  `creator_joins_as='admin'` path INSERTs a `users` row (copying the actor's
  `(idp_issuer, idp_subject)` verbatim) + a `user_roles` row, atomically, via the
  **BYPASSRLS authPool**, with a dual `super_admin_audit_log` + `me_audit_log`
  write and a slice-126 sink fanout. This is the membership-write template.
- `requireSuperAdmin` reads `jwtmw.FromContext().SuperAdmin` (the load-bearing
  gate); `actorFromContext` reads the JWT `sub`. Super_admin is JWT-only at v1.
- `adminsuperadmins` P0-SA-3 explicitly does NOT touch memberships — it documents
  that "the granted identity must already have a user_roles row in some tenant via
  the standard /v1/admin/users surface to acquire tenant-write authority." Slice
  478 is the surface that creates those memberships.

---

## Decisions made

### D1 — Local-auth identity semantics (THE load-bearing call). Confidence: HIGH.

**Problem.** `enumerateMemberships` keys on `(idp_issuer, idp_subject)`. Local
operators have **empty** `('','')`. If a second-tenant membership row copies the
empty tuple, `enumerateMemberships('','')` matches EVERY local user across ALL
tenants → catastrophic cross-tenant over-match (P0-478-2 / the slice-476 hazard).
The `users_idp_principal_unique` partial index is `WHERE idp_issuer <> '' AND
idp_subject <> ''`, so empty tuples are NOT uniqueness-constrained — many empty
rows already legitimately coexist (one local user per tenant). The resolver
relies on `r.authPool != nil && idpIssuer != "" && idpSubject != ""` to even
ATTEMPT cross-tenant enumeration, so today a local user is single-tenant by
construction and never over-matches — precisely because the empty-tuple branch is
skipped.

**Chosen mechanism: (a) a stable synthetic per-user local-identity key.**
When the assign target is a local user (empty IdP tuple) being added to a tenant
they are not already in, the handler mints a synthetic, stable, per-user IdP
tuple and writes it to BOTH the new membership row AND (backfill) the origin row,
in the same transaction:

- `idp_issuer  = 'urn:atlas:local'` (a fixed, reserved synthetic issuer)
- `idp_subject = '<origin_user_id>'` (the local user's existing `users.id` UUID
  string in their home tenant — globally unique, stable, already minted)

**Why this is correct and non-over-matching:**

1. The pair is now **non-empty**, so it satisfies the resolver's `idpIssuer != ""
&& idpSubject != ""` guard and the user becomes multi-tenant-visible — exactly
   the goal.
2. The pair is **unique per local user** (the subject is that user's origin
   UUID), so `enumerateMemberships('urn:atlas:local', '<originUUID>')` matches
   ONLY that one user's rows — never another local user's. The over-match is
   impossible by construction: two different local users get two different
   synthetic subjects.
3. The synthetic pair flows through the EXISTING `users_idp_principal_unique`
   partial index (both non-empty), so a second tenant cannot accidentally
   duplicate the same principal — no schema change to the index.
4. **Backfilling the origin row** is required: if only the new row carried the
   synthetic pair, the resolver invoked from the user's HOME-tenant session would
   read the origin row's STILL-empty tuple and skip enumeration, never surfacing
   the new tenant. Backfill makes the synthetic identity the user's canonical
   cross-tenant key from any session. The backfill is idempotent (only fires when
   the origin row is still empty).
5. **No `local_credentials` change.** The synthetic IdP tuple is purely an
   enumeration key; the user still authenticates locally via `local_credentials`
   keyed on `user_id`. Local login is unchanged — the synthetic issuer is not an
   OIDC issuer and is never used for token exchange.

**Rejected alternative (b): a dedicated `memberships` table decoupled from the
IdP tuple.** Cleaner in the abstract, but it would require rewriting
`enumerateMemberships` (the slice-192 hot path on every `/oauth/authorize`),
re-pointing the slice-198 super_admin lookup, and dual-maintaining membership
truth (the table AND the `users` rows that handlers/RLS already read). The
synthetic-key mechanism reuses the existing resolver, index, and RLS unchanged —
strictly less surface area, strictly less risk on an elevation surface. Recorded
as the v2 revisit if a first-class membership concept is ever needed for reasons
beyond enumeration.

**Proof obligation (AC-5 test):** assign two DISTINCT local users (each with an
empty-tuple home row) to a shared second tenant; assert each one's
`enumerateMemberships`-equivalent query returns ONLY its own two memberships, and
that neither synthetic subject matches the other user's rows. Also assert the
origin row was backfilled.

### D2 — Reuse the slice-143 BYPASSRLS authPool for cross-tenant writes. Confidence: HIGH.

Assign/revoke target a tenant that is, by definition, NOT the actor's session
tenant (cross-tenant is the whole point for a super-admin). The four-policy RLS on
`users` + `user_roles` would block an `atlas_app` INSERT whose `tenant_id` ≠ the
GUC. So the write path uses `authPool` (BYPASSRLS), exactly like
`admintenants.Create`. The handler enforces the super_admin gate at the
application layer (defense-in-depth with the OPA gate). When `authPool` is nil
(unit-server harness without DATABASE_URL) the write endpoints return 503 — the
slice-143 precedent. RLS is NOT weakened: the BYPASSRLS pool is reachable ONLY
behind `requireSuperAdmin`, and the within-tenant tenant-admin path still runs
under RLS via `atlas_app` (see D3).

### D3 — Two authority tiers: super_admin (cross-tenant) vs tenant-admin (within-tenant). Confidence: HIGH.

- **Cross-tenant** list/assign/revoke (target tenant ≠ session tenant, or the
  cross-tenant list) require `jwtmw.FromContext().SuperAdmin`. Enforced by
  `requireSuperAdmin`.
- **Within-tenant** assign/revoke (target tenant == session tenant) are allowed
  for a tenant-`admin` (the existing `cred.IsAdmin` defense-in-depth + the
  slice-035 OPA admin gate), running under RLS via `atlas_app`. A tenant-admin can
  NEVER name a tenant other than their own session tenant: the handler rejects any
  `tenant_id` in the body that ≠ session tenant unless the caller is super_admin
  (P0-478-1). This is the "cannot grant beyond your authority" guard, tested with
  a DENIED case (AC-6).
- A tenant-admin cannot grant a role they could not already grant — the role set
  is the slice-035 canonical enum for both tiers (no new roles, no super_admin
  grant here — that stays in `adminsuperadmins`). P0-478-1 holds: assigning the
  `admin` role within your own tenant is within a tenant-admin's authority; there
  is no role in the enum that exceeds it except super_admin, which this surface
  never grants.

### D4 — Self-assign is the same assign path; no new global power; no auto-switch. Confidence: HIGH.

A super-admin self-assigning to a tenant is just `assign(target=self,
tenant=X, roles=[...])` where the synthetic/IdP identity is the actor's own. It
grants navigability (the tenant appears in `available_tenants` on next token
issuance), not new authority — the super_admin flag already authorized
everything. The handler does NOT mutate the actor's current session/tenant
(P0-478-5: no auto-switch); the new tenant becomes reachable only after the next
`/oauth/authorize` re-mints the JWT. AC-4 proves the end-to-end chain by querying
the resolver after the assign.

### D5 — Atomic, idempotent assign. Confidence: HIGH.

One BYPASSRLS transaction wraps: (optional origin backfill) → upsert membership
`users` row → upsert `user_roles` rows → audit writes. Idempotent on re-assign:
the membership uses `ON CONFLICT (idp_issuer, idp_subject)`-aware logic via a
lookup-then-insert under the per-actor advisory lock; `user_roles` uses
`ON CONFLICT (tenant_id, user_id, role) DO NOTHING`. Re-assigning the same
(user, tenant, roles) is a no-op that still returns 200 with the current state.

### D6 — Audit every assign/revoke/self-assign (AC-7). Confidence: HIGH.

Reuse the slice-142 dual-write: one `super_admin_audit_log` row (platform-global
forensic anchor) + one `me_audit_log` row (tenant-scoped to the actor's session
tenant, surfaced by the slice-124 aggregator) + a slice-126 sink fanout. New
action values: `user_tenant_assign`, `user_tenant_revoke`. These extend BOTH the
`me_audit_log_action_check` and `super_admin_audit_log_action_chk` CHECK
constraints (superset migration; reversible down).

For the within-tenant tenant-admin path (no super_admin), the audit row is still
written but ONLY to `me_audit_log` (the actor is not a super_admin, so a
`super_admin_audit_log` row would mis-attribute platform-global authority). The
`super_admin_audit_log` write fires only on the super_admin (cross-tenant) path.

### D7 — Revoke removes user_roles for the (user, tenant); membership row policy. Confidence: MEDIUM.

Revoke deletes the `user_roles` rows for (tenant, user). It does NOT delete the
`users` membership row by default (a role-less membership is harmless and keeps
the identity stable for re-grant; deleting it would orphan `local_credentials`
and `sessions` via cascade for the home tenant). A `?remove_membership=true`
query flag additionally sets the membership row `status='disabled'` (soft, not
hard delete — preserves audit/session integrity and matches the existing
`status` CHECK). Revisit-in-use: confirm operators want soft-disable vs hard
removal of the membership; soft is the conservative default on an elevation
surface.

### D8 — No vendor-prefixed test fixtures (P0-478-6). Confidence: HIGH.

All test tokens/fixtures use neutral `test-*` strings. No `ghp_`/`eyJ`/`sk_live_`/
`AKIA` prefixes (GitGuardian flags these even in tests).

---

## Revisit-once-in-use

- **D1 backfill timing.** The origin-row backfill mutates the user's home-tenant
  row the first time they are cross-assigned. If an operator has external tooling
  that asserts local users always have empty IdP tuples, that assumption breaks.
  Documented; acceptable because the empty tuple was never a contract, only an
  implementation detail of "single-tenant local user."
- **D7 membership removal semantics** (soft-disable vs hard delete).
- **D3 tenant-admin self-assign within own tenant** — currently allowed (it is
  within authority); confirm the UI (slice 479) surfaces it sensibly.

---

## Detection-tier classification

- `detection_tier_actual`: none (no bug surfaced during the slice; the local-auth
  over-match hazard was caught at DESIGN time in the grill, before any code).
- `detection_tier_target`: integration (the over-match, had it shipped, would be
  an integration-tier RLS/identity correctness failure — the AC-5 integration
  test is the guard that would catch a regression).
