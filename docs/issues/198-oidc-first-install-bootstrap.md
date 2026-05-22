# 198 — OIDC first-install bootstrap (slice 192 follow-on for AC-11/AC-12)

**Cluster:** Backend / Auth
**Estimate:** 0.5-1d
**Type:** AFK
**Status:** `ready` (spillover from slice 192; gates 192 + 197 merged)

## Provenance

Surfaced during slice 192 (multi-tenant switch + token-exchange + frontend) as deferred ACs (AC-11 + AC-12) per the partial-cutover-with-spillover pattern (191 D6 precedent). Slice 192 shipped the multi-tenant session model + tenant-switch endpoint + frontend switcher, but the OIDC callback's first-install bootstrap branch was deferred to its own slice to keep slice 192's scope tractable.

After slice 192 + slice 197 (slice 034 bearer-middleware retirement), the OIDC callback at `internal/api/auth/http.go` no longer creates a default tenant when the atlas instance has zero tenants — first-install OIDC sign-ins fall through to the existing "user has no tenant memberships" branch and return 403 "Contact your administrator", which is wrong for the bootstrap case where there ARE no administrators yet.

## Narrative

Two scenarios this slice serves:

1. **First-install (`count(*) FROM tenants == 0`)**: the OIDC callback creates a "Default Tenant" row, creates the user row, grants the user `super_admin = true` (global) + `tenant_admin` role in the new tenant, writes the `user_tenants` row, and proceeds with normal login (1 tenant → auto-select). The minted JWT carries `current_tenant_id = <default-tenant-id>`, `available_tenants = [<default-tenant-id>]`, `super_admin = true`.
2. **Subsequent OIDC logins (any user, established install)**: behavior unchanged from slice 192. If the user exists + has ≥1 tenant in `user_roles`, skip the bootstrap branch and proceed to normal login (single-tenant auto-select OR picker if ≥2 tenants).

The bootstrap branch is a one-shot: it fires exactly once in the instance's lifetime, on the first OIDC callback to land. Subsequent callbacks see `count(*) FROM tenants > 0` and skip the branch entirely.

### Race-condition consideration

If two OIDC callbacks land concurrently on a fresh install:

- Both see `count(*) FROM tenants == 0`.
- Both try to create "Default Tenant".
- One succeeds; the other gets a uniqueness constraint violation on the LOWER(name) UNIQUE index from slice 144.
- The losing call must retry — see `count(*) > 0` now, take the normal-login path.

The retry must NOT escalate to bootstrap a second time. AC enforces this.

## Acceptance criteria

- **AC-1.** OIDC callback handler at `internal/api/auth/http.go` (or wherever the OIDC callback lives post-slice-197 — verify the path) checks `count(*) FROM tenants` early. If zero:
  - Create "Default Tenant" via `INSERT INTO tenants (name) VALUES ('Default Tenant') RETURNING id`.
  - Create user row from OIDC claims (`sub`, `email`).
  - Grant `super_admin = true` on the user (global, NOT per-tenant).
  - Grant `tenant_admin` role in the new tenant via `user_roles` insert.
  - Write `user_tenants` (oidc_issuer, oidc_subject, tenant_id, joined_at) row.
  - Mint JWT with `current_tenant_id = <default-tenant-id>`, `available_tenants = [<default-tenant-id>]`, `super_admin = true`.
  - Wrap all of the above in a single `BEGIN ... COMMIT` transaction (atomicity).
- **AC-2.** If `count(*) FROM tenants > 0`, skip the bootstrap branch entirely. Existing slice 192 paths handle the case (user exists with ≥1 tenant → normal login; user exists with 0 tenants → 403; user doesn't exist → create user-only-no-tenant + 403).
- **AC-3.** Race-safe: if two concurrent callbacks both see `count(*) == 0`, exactly one bootstraps. The loser's INSERT into `tenants` (LOWER(name) UNIQUE constraint from slice 144 collides on "Default Tenant") returns ErrDuplicateName; the loser retries the count check and falls through to the normal-login path.
- **AC-4.** Audit log: the bootstrap event writes a `decision_audit_log` row with `actor_id = <new-user-id>`, `event = 'bootstrap_first_install'`, `payload` containing `{tenant_id, user_id, granted_roles: ['super_admin','tenant_admin']}`. Auditors can detect "who got the keys to the kingdom".
- **AC-5.** Integration test at `internal/api/auth/oidc_bootstrap_test.go` (NEW) covering:
  - First OIDC callback on empty DB → tenant + user + roles + JWT all created atomically.
  - Subsequent OIDC callback (different user, same install) → bootstrap branch skipped; user gets 403 if no membership.
  - Race test: two concurrent goroutines both calling the bootstrap path → exactly one bootstraps; the other's tenant INSERT loses + retries.
- **AC-6.** Slice 192's frontend tenant-switcher renders correctly after bootstrap (single tenant → tenant-switcher hidden per canvas §11 #13 invariant).
- **AC-7.** Decisions log at `docs/audit-log/198-oidc-first-install-bootstrap-decisions.md` captures: race-handling choice (LOWER(name) UNIQUE collision retry vs advisory lock vs SELECT FOR UPDATE), how super_admin is stored (column vs role row vs user_global_roles table — verify against existing schema), atomicity transaction shape.

## Constitutional invariants honored

- **Invariant #6** (tenant isolation via RLS): the bootstrap branch uses the `atlas_app` role + `app.current_tenant` GUC EXCEPT for the `count(*) FROM tenants` check, which must use the existing `atlas_auth` non-RLS role from slice 141/192. Document explicitly.
- **AI-assist boundary**: this slice does NOT touch AI-assist surfaces. No `ai_assisted` flag interactions.
- **OIDC RP-only**: slice 198 stays an OIDC RP consumer; does not issue new OIDC credentials.

## Anti-criteria (P0 — block merge)

- **P0-198-1.** Does NOT create a "Default Tenant" row unless `count(*) FROM tenants == 0` (no idempotent re-creation; one-shot only). Subsequent callbacks must NOT re-bootstrap.
- **P0-198-2.** Does NOT grant `super_admin = true` to any user other than the first-install user. Subsequent admins must be granted via a different mechanism (future slice).
- **P0-198-3.** Does NOT bypass RLS for any DB write other than the bootstrap transaction. The tenants/users/user_roles/user_tenants writes are all in a single transaction; other domain writes continue to use the normal RLS path.
- **P0-198-4.** Does NOT make `count(*) FROM tenants` queryable by un-authenticated callers. The check fires inside the authenticated OIDC callback context only.
- **P0-198-5.** Race-safe: does NOT allow concurrent OIDC callbacks to both create a "Default Tenant" row. Exactly one wins; the others retry normal-login path.

## Dependencies

- **#192** (merged) — multi-tenant session model + tenant-switch endpoint + frontend tenant-switcher. Slice 198 closes 192's deferred AC-11/AC-12.
- **#197** (merged) — slice 034 bearer-middleware retirement. The OIDC callback handler post-197 is the integration point.
- **#144** (merged) — `tenants` table with LOWER(name) UNIQUE index. Provides the race-handling primitive.
- **#141** (merged-via-spine-completion) — multi-tenant login + tenant picker. Slice 198's bootstrap branch was originally part of 141's design.

## Skill mix (3-5)

- `tdd` (red — write race test first; green — implement; refactor — minimize handler surface)
- `simplify`
- `Security` (Phase 3 — STRIDE: who gets super_admin? race conditions; audit log shape)
- `ship-gate`

## Notes for the implementing agent

The race-handling AC (AC-3) is the load-bearing JUDGMENT. Two plausible shapes:

1. **LOWER(name) UNIQUE collision retry** (recommended): rely on slice 144's existing UNIQUE index. Loser's INSERT fails with `ErrDuplicateName`; retry the count check + fall through. Zero new infrastructure.
2. **PostgreSQL advisory lock** on a constant key (e.g. `pg_advisory_xact_lock(0xBADBADBAD)`). Forces serialization at the DB layer. More overhead but explicit.

Recommend #1 — slice 144's UNIQUE constraint is the primitive; rely on it.

The `super_admin` storage shape needs verification: does `users.super_admin BOOLEAN` exist? Or is it stored in `user_global_roles`? Slice 192's AC-11 description says "grant the user `super_admin = true`" which suggests a boolean column — but the actual schema may have evolved. Read `migrations/sql/` to confirm the storage shape before writing the bootstrap logic.

The audit log `decision_audit_log` row is load-bearing for the "who got the keys" auditability story. The `actor_id` should be the new user's UUID (NOT NULL), and the payload should include enough detail that an auditor reviewing the row 6 months later can reconstruct "the first user on this install was X; they got super_admin + tenant_admin in tenant Y at time Z."

### CI-delta scan (per batch 96 learning)

This slice touches the OIDC callback path. Verify:

- Does the CI Go integration test job have OIDC mocks set up? (likely yes via slice 034's test infrastructure)
- Does the Playwright job depend on first-install-already-done? If so, slice 198's bootstrap path may break Playwright if the test fixture state changes. Check `fixtures/e2e/*.sql` for tenant pre-seeding.

### Provenance

Filed 2026-05-21 by orchestrator as part of batch 97 setup. Slice 192's AC-11/AC-12 were explicitly deferred to this slice per the partial-cutover-with-spillover pattern. Slice 198's spec authoring was held until slice 197 + 201 cleared (both merged 2026-05-21), since both impact the OIDC callback's integration surface.
