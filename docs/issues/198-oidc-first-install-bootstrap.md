# 198 — OIDC first-install bootstrap (closes slice 192 AC-11/AC-12)

**Cluster:** Backend / Auth
**Estimate:** 0.5-1d
**Type:** JUDGMENT
**Status:** `ready` (spillover from slice 192's partial-cutover; gate: 192 merged)

## Narrative

Slice 192 shipped the multi-tenant switch + frontend switcher and deferred the
first-install bootstrap (AC-11/AC-12) to a follow-on slice per the 191 D6
partial-cutover-with-spillover pattern. After slice 192 + slice 197 landed,
the OIDC callback no longer creates a "Default Tenant" on first install — a
fresh install where the operator runs through the OIDC flow lands at 403
"Contact your administrator" with no administrator to contact.

Slice 198 closes the gap. It adds the bootstrap branch to the OIDC callback:
when the `tenants` table is empty, the callback atomically creates a Default
Tenant, the OIDC user row, a `super_admin` grant, a `tenant_admin` role, and
an audit-log row capturing who got the keys to the kingdom. The branch is
serialized via slice 144's existing `idx_tenants_bootstrap_singleton` partial
UNIQUE index — the loser of a concurrent first-install race retries the
`count(*)` check and falls through to the established-install path.

**The spine context.** Slice 144 already provisioned the load-bearing
primitive: a `tenants` table with `is_bootstrap_tenant BOOLEAN NOT NULL
DEFAULT false` + the `idx_tenants_bootstrap_singleton` partial UNIQUE index on
`is_bootstrap_tenant = true`. Slice 144's migration header explicitly leaves
the column inert for slice 198 to flip it on. Slice 198 is the slice that
writes to it.

**The super_admin storage gap.** Through v2, `super_admin` exists only as a
JWT claim and a column on the `oauth_auth_codes` / `oauth_device_codes`
snapshot rows — there is no persistent storage. Slice 192's user_resolver
explicitly defers this: `super_admin = false (no super_admins table at v2;
spillover slice 198 ships the OIDC-first-install bootstrap path)`. Slice 198
introduces the `super_admins` table — minimal: one row per global-admin
identity, append-only-by-RLS. The DBUserResolver now consults the table and
populates the JWT claim accordingly.

## Threat model

**S — Spoofing.** An attacker fronts the OIDC redirect before the legitimate
first-installer.

- Mitigation: bootstrap branch grants super_admin to the FIRST user to
  successfully complete the OIDC flow. Operators are advised in the operator
  docs to complete first-install behind a perimeter (e.g., before exposing
  the install URL publicly). This is the OAuth-standard "first user wins"
  shape; documented honestly.

**T — Tampering.** A subsequent user attempts to set `is_bootstrap_tenant =
true` to escalate.

- Mitigation: `atlas_app` is granted `INSERT` only on `tenants` (no UPDATE on
  `is_bootstrap_tenant`); the column has no application-side mutation site;
  `idx_tenants_bootstrap_singleton` ensures at most one row carries the flag
  even if the migration grant drifts.

**R — Repudiation.** Who got the keys?

- Mitigation: bootstrap branch writes a `me_audit_log` row with
  `action = 'bootstrap_first_install'`, the user_id of the grantee, and the
  before/after JSON snapshot. The row is append-only by RLS (slice 108
  invariant).

**I — Information disclosure.** The `super_admins` table holds platform-wide
identity rows.

- Mitigation: SELECT-only grant to `atlas_app` (read-side); INSERT only via
  the BYPASSRLS `atlas_migrate` pool (bootstrap branch uses authPool, not the
  per-tenant app pool). No tenant-scoped access; the table holds at most a
  handful of identities.

**D — Denial of service.** Concurrent first-installers race.

- Mitigation: `idx_tenants_bootstrap_singleton` UNIQUE constraint serializes.
  The loser raises `unique_violation` (Postgres SQLSTATE 23505); the
  bootstrap branch catches the sentinel, retries the `count(*)` check, and
  falls through to the normal-login path.

**E — Elevation of privilege.** A normal-login user (post-first-install)
attempts to trigger the bootstrap branch.

- Mitigation: the bootstrap branch is gated on `count(*) FROM tenants == 0`.
  After the first row lands, the count is non-zero forever; the gate falls
  through to the normal-login path. No application-side flag is needed.

**Verdict:** `has-mitigations`. The risk surface is dominated by the
"first-installer wins" shape, which is the OAuth-standard semantics and is
documented for operators.

## Acceptance criteria

### Schema

- **AC-1.** New migration `20260521020000_super_admins.sql` adds:
  - `super_admins (user_id UUID PRIMARY KEY, granted_at TIMESTAMPTZ NOT NULL
DEFAULT now(), granted_via TEXT NOT NULL)` — append-only platform-global
    grant table. No tenant_id (super_admin is platform-wide by definition).
    `granted_via` records the provenance (`'bootstrap_first_install'` is the
    only v2 value; future maintainer-CLI grants would add their own values).
  - `granted_via` CHECK constraint: at least `'bootstrap_first_install'`.
  - Grants: `SELECT` to `atlas_app` (read-side via DBUserResolver);
    `SELECT, INSERT` to `atlas_migrate` (bootstrap branch writes via authPool
    since the platform-global table cannot be RLS-bound on tenant_id).
  - **No RLS on this table.** It is platform-global by design. The
    application-layer write surface is the bootstrap branch, which writes
    exactly once per install lifetime.
- **AC-2.** The same migration extends `me_audit_log.action` CHECK to include
  `'bootstrap_first_install'`. The bootstrap branch writes one row capturing
  the grant. Per slice 144 precedent for `'tenant_rename'`.

### Bootstrap branch

- **AC-3.** New method `users.Store.BootstrapFirstInstallOrUpsert(ctx,
BootstrapInput) (User, BootstrapResult, error)`. The method:
  1. Begins a transaction on the BYPASSRLS auth pool (the standard `users`
     RLS context cannot apply when no tenant exists yet).
  2. Runs `SELECT COUNT(*) FROM tenants`. If non-zero, returns
     `BootstrapResult{Bootstrapped: false}` — caller falls through to
     existing UpsertOIDC path.
  3. If zero: inserts `tenants(id, name, is_bootstrap_tenant) VALUES
(gen_random_uuid(), 'Default Tenant', true)`. The
     `idx_tenants_bootstrap_singleton` UNIQUE partial index serializes.
  4. Inserts `users` row keyed on (idp_issuer, idp_subject) under the new
     tenant_id.
  5. Inserts `user_roles(tenant_id, user_id, role)` row with `role='admin'`
     (the `'tenant_admin'` analog under the existing slice 035 enum).
  6. Inserts `super_admins(user_id, granted_via='bootstrap_first_install')`.
  7. Inserts `me_audit_log` row with `action='bootstrap_first_install'`,
     `tenant_id=<new>`, `user_id=<new>`, `before='{}'`,
     `after={"role":"admin","super_admin":true,"granted_via":"bootstrap_first_install"}`.
  8. Commits. Returns `BootstrapResult{Bootstrapped: true, TenantID:
<newID>}`.
- **AC-4.** On `unique_violation` (SQLSTATE 23505) raised by the bootstrap
  singleton index, rollback + retry the count check once + fall through.
  This handles the concurrent-first-installer race.

### Handler wiring

- **AC-5.** `Handler.OIDCCallback` extended: AFTER `HandleCallback` succeeds
  but BEFORE the existing `UpsertOIDC` call, invoke
  `BootstrapFirstInstallOrUpsert`. If the result indicates `Bootstrapped:
true`, the user is the new bootstrap user; the existing UpsertOIDC call is
  skipped (the bootstrap branch already created the row). If `Bootstrapped:
false`, fall through to the existing UpsertOIDC path unchanged.
- **AC-6.** The `tenantID` query parameter is OPTIONAL on the bootstrap
  branch. The existing handler requires it; the bootstrap branch synthesizes
  the new tenant ID and uses it instead. When count is non-zero AND
  `tenant_id` is missing, return 400 (unchanged behavior).

### DBUserResolver lookup

- **AC-7.** `DBUserResolver.ResolveForOAuth` queries `super_admins` by
  user_id (via authPool when present) and sets the `super_admin` claim
  accordingly. The `// super_admin = false (no super_admins table at v2)`
  comment is removed; the implementation now reads the table.

### Tests + docs

- **AC-8.** Integration test for the bootstrap path (file:
  `internal/auth/users/bootstrap_integration_test.go`):
  - **AC-8a.** Bootstrap on empty tenants table: `BootstrapResult.Bootstrapped
== true`; one row in `tenants` with `is_bootstrap_tenant=true`; one row
    in `users`; one row in `user_roles` with `role='admin'`; one row in
    `super_admins`; one row in `me_audit_log` with
    `action='bootstrap_first_install'`.
  - **AC-8b.** Second call after first succeeds: `BootstrapResult.Bootstrapped
== false`; no new rows created beyond what the caller's
    UpsertOIDC writes (caller path handled separately).
  - **AC-8c.** Concurrent-first-install race: two goroutines call
    BootstrapFirstInstallOrUpsert with different OIDC subjects in parallel.
    Exactly one row in `tenants` with `is_bootstrap_tenant=true`; both
    callers return without error; second caller's `Bootstrapped == false`.
- **AC-9.** CHANGELOG entry under `[Unreleased]` documenting the bootstrap
  path + the AC-11/AC-12 closure of slice 192.
- **AC-10.** Decisions log at
  `docs/audit-log/198-oidc-first-install-bootstrap-decisions.md` capturing
  the JUDGMENT calls: race-handling shape (D1), super_admin storage shape
  (D2), atomicity transaction shape (D3), any CI-delta discovered (D4).

## Constitutional invariants honored

- **Tenant isolation at DB layer** (invariant #6): `super_admins` is
  platform-global by design (not tenant-scoped). The bootstrap branch's
  per-tenant writes (users, user_roles, me_audit_log) flow through the
  BYPASSRLS auth pool ONLY during the bootstrap moment; once the first
  tenant exists, the normal RLS-bound path resumes.
- **AI-assist boundary:** not touched.

## Canvas references

- OQ #21 RESOLVED (Reading D, 2026-05-20).
- Slice 141 (PARKED) — bootstrap shape originally specified there; closed
  via 192 spine completion + 198 spillover.
- Slice 144 — provisioned `idx_tenants_bootstrap_singleton` for this slice's
  use.
- Slice 192 AC-11/AC-12 — closed by this slice.

## Dependencies

- **#144** — `tenants` table + `idx_tenants_bootstrap_singleton` partial UNIQUE
  index. **Merged at `dd2e876`.**
- **#192** — multi-tenant spine completion. **Merged at `b0b5280`.** (Gate.)
- **#196** — bootstrap OAuth migration. **Merged at `3b8f0f1`.**
- **#197** — bearer-middleware retirement. **Merged at `00a682c`.**

## Anti-criteria (P0 — block merge)

- **P0-198-1.** Bootstrap branch MUST be atomic within a single transaction.
  Partial state (tenant created but no super_admin grant) is forbidden.
- **P0-198-2.** Bootstrap branch MUST be gated on `count(*) FROM tenants ==
0`. The branch MUST NOT fire when any tenant exists, regardless of
  `is_bootstrap_tenant` value (defense in depth against operator error).
- **P0-198-3.** Bootstrap branch MUST be serialized against concurrent
  first-installers via the slice 144 partial UNIQUE index. Two parallel
  callers MUST produce exactly one `is_bootstrap_tenant=true` row.
- **P0-198-4.** `me_audit_log` row MUST be written in the same transaction as
  the grants. The bootstrap event is auditable forever.
- **P0-198-5.** `super_admins` table MUST NOT be tenant-scoped (no
  `tenant_id` column). super_admin is platform-global; the table reflects
  that shape.

## Skill mix (3-5)

- `tdd` (backend integration)
- `security-review` (privilege-grant surface)
- `simplify`
- `ship-gate`

## Notes for the implementing agent

### Schema verification before coding

Before writing the migration, confirm:

- `users.super_admin BOOLEAN` does NOT exist. ✓ (no column).
- `user_global_roles` does NOT exist. ✓ (no table).
- `tenants.is_bootstrap_tenant` column + `idx_tenants_bootstrap_singleton`
  partial UNIQUE index DO exist (provisioned by slice 144). ✓.
- `me_audit_log.action` CHECK is the slice 144 extension that already
  includes 14 values; this slice extends it to 15. ✓.

### Race-handling JUDGMENT (D1 in the decisions log)

Two candidate shapes:

1. **LOWER(name) UNIQUE collision retry** — relies on slice 144's existing
   primitive (`idx_tenants_bootstrap_singleton`). Zero new schema. Retry on
   SQLSTATE 23505.
2. **Advisory lock + count recheck** — Postgres `pg_advisory_xact_lock` on a
   well-known key; first writer holds the lock through transaction commit;
   second writer blocks then sees the inserted row on count.

**Recommend #1.** Reasoning:

- Zero new infrastructure; reuses slice 144's existing partial UNIQUE index.
- Slice 144's migration header explicitly says "future slice 198 (OIDC-first-
  install bootstrap) can switch it on without a migration round-trip" — the
  primitive is provisioned for exactly this use case.
- Advisory locks add a second mechanism the maintainer has to reason about;
  the partial UNIQUE index is purely declarative.

### super_admin storage JUDGMENT (D2 in the decisions log)

Three candidate shapes:

1. **Dedicated `super_admins` table** (one row per global-admin identity).
2. **Boolean column on `users`** — adds `users.super_admin BOOLEAN`.
3. **Role `'super_admin'` in existing `user_roles` table** — extends the
   CHECK constraint.

**Recommend #1.** Reasoning:

- `users` is tenant-scoped. super_admin is platform-global. Storing the flag
  on the per-tenant users row creates an awkward N-row representation of a
  single identity (one row per tenant the user has logged into).
- `user_roles` has the same tenant-scoped shape problem + a CHECK enum that
  has a clear "per-tenant role" semantic. Adding `'super_admin'` to the enum
  would muddle the meaning.
- A dedicated table cleanly models "platform-global grant"; one row per
  global-admin identity. Future maintainer-CLI grants extend
  `granted_via`.

### Atomicity transaction JUDGMENT (D3 in the decisions log)

The bootstrap branch must write seven rows across four tables (`tenants`,
`users`, `user_roles`, `super_admins`, `me_audit_log`). Either:

1. **Single BYPASSRLS transaction** — atlas_migrate pool wraps the lot in one
   BEGIN/COMMIT. RLS doesn't apply since atlas_migrate bypasses it.
2. **Multi-step with compensating writes** — separate transactions for
   tenants vs users vs user_roles; on partial failure, run cleanup.

**Recommend #1.** Reasoning: option #2 is a distributed-systems pattern for
when multi-statement atomicity isn't available. Postgres gives us
multi-statement atomicity for free. The transaction is short (5 INSERTs); no
long-running locks; commits in milliseconds. Compensating writes are a
maintenance liability with no benefit here.

### CI-delta scan (per batch 96 learning)

Integration tests run against the standard CI Postgres+migrations harness
with NO seeded tenants. The test must work on a completely empty `tenants`
table. The harness does NOT seed `tenants` from `fixtures/e2e/*.sql` (those
are frontend e2e fixtures, not Go integration fixtures). Confirmed in the
ci.yml integration job: the only psql calls are migrations from
`migrations/sql/` + `migrations/bootstrap/01-roles.sql`. No tenant seed.

Cleanup: the test must delete its bootstrap row + cascaded child rows after
each sub-test so subsequent sub-tests start clean (the integration test
process runs `go test -p 1` so tests within a file run sequentially, but each
sub-test must restore the empty state).

### Provenance

Filed 2026-05-21 as slice 192's spillover. Closes slice 192's AC-11/AC-12.
The "Bootstrap tenant + super_admin grant" section of slice 192 explicitly
deferred to this slice per the 191 D6 partial-cutover-with-spillover pattern.
