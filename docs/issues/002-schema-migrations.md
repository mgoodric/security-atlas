# 002 — Schema + migrations for six primitives + FrameworkScope + tenancy plumbing

**Cluster:** Spine
**Estimate:** 3d
**Type:** AFK

## Narrative

Land the foundational Postgres schema for all seven entities (`controls`, `risks`, `evidence_records`, `scopes`, `frameworks` + `framework_versions`, `policies`, `framework_scopes`) plus their relationship tables, indexes, and constraints. Use Atlas declarative HCL for migrations. Every tenant-scoped table includes `tenant_id` and a Row-Level Security policy that reads `current_setting('app.current_tenant', true)`. Tenancy context plumbing: a small Go helper sets and reads the GUC for each request/connection. Slice delivers value because subsequent feature slices have a stable, RLS-enforced data substrate they can rely on; an integration test verifies cross-tenant SELECTs return zero rows.

## Acceptance criteria

- [ ] AC-1: `just migrate up` applies all migrations cleanly to an empty Postgres 16 database
- [ ] AC-2: `just migrate down` reverses cleanly (no dangling objects)
- [ ] AC-3: All 7 entities exist with documented columns matching `Plans/canvas/02-primitives.md` and §5.5 (FrameworkScope)
- [ ] AC-4: RLS is enabled on every `tenant_id`-bearing table; `\d <table>` shows the policy
- [ ] AC-5: Integration test inserts a record under `app.current_tenant='A'`, then queries under `app.current_tenant='B'` — returns zero rows
- [ ] AC-6: `framework_scopes` table includes `predicate`, `effective_from`, `effective_to`, `approved_by`, `approval_evidence`
- [ ] AC-7: `tenancy.WithTenant(ctx, tenantID)` returns a context whose database connection has `app.current_tenant` set

## Constitutional invariants honored

- **Invariant 6 (RLS at DB layer):** every tenant-scoped table gets an RLS policy from day zero — not retrofitted later
- **Invariant 4 (multidimensional scope):** schema for `scopes` accommodates the dimension tuple
- **Invariant 5 (FrameworkScope intersection):** `framework_scopes` table lands now so phase-2 frameworks inherit

## Canvas references

- `Plans/canvas/02-primitives.md` §2.1–2.6 (entity field tables)
- `Plans/canvas/05-scopes.md` §5.4 (RLS), §5.5 (FrameworkScope)

## Dependencies

- #001

## Anti-criteria (P0)

- Does NOT add a table without an RLS policy if it carries tenant data
- Does NOT use Postgres-specific features that break under sqlc codegen (e.g., custom domains incompatible with codegen — verify)
- Does NOT seed any data beyond what's needed to verify migration round-trip

## Skill mix (3–5)

- Atlas (declarative migrations)
- Postgres 16 (RLS, JSONB, schemas)
- sqlc (verify codegen compatibility)
- Go (tenancy context helper)
- Integration testing with testcontainers-go
