# 033 — Postgres RLS enforcement on every tenant-scoped table + tenancy context plumbing

**Cluster:** Multi-tenancy / auth
**Estimate:** 2d
**Type:** AFK

## Narrative

Audit every tenant-scoped table from slice 002 and verify RLS policies are in place and effective. Land the tenancy context plumbing into every request path: middleware sets `app.current_tenant` GUC at connection check-out; every database call inherits the right tenant context. Integration tests verify that cross-tenant SELECTs return zero rows even when application code "forgets" the WHERE clause. The slice delivers value because the multi-tenant guarantee is enforced by Postgres, not by application code — closing the most-common-leak vector before any feature shipping.

## Acceptance criteria

- [ ] AC-1: Audit script `just audit-rls` lists every table with `tenant_id` and confirms an RLS policy exists; fails CI on any tenant table without a policy
- [ ] AC-2: Middleware `tenancy.Middleware` sets `app.current_tenant` from the authenticated session
- [ ] AC-3: Integration test: with `app.current_tenant=A`, an INSERT under tenant A succeeds; SELECT under `app.current_tenant=B` returns zero rows
- [ ] AC-4: Integration test: a developer mistake (forgetting `WHERE tenant_id = ?` in a query) does NOT leak data — RLS denies the row
- [ ] AC-5: Service-account / admin / cross-tenant queries (audit log read) use an explicit `SET LOCAL ROLE service_account` pattern, not bypass of RLS
- [ ] AC-6: Documentation in `docs/architecture/rls.md` explains the pattern for contributors

## Constitutional invariants honored

- **Invariant 6 (RLS at DB layer):** the entire premise of this slice
- **Anti-pattern rejected:** application code is not the trust boundary; RLS is

## Canvas references

- `Plans/canvas/05-scopes.md` §5.4 (Postgres RLS named explicitly)
- `CLAUDE.md` (Invariant 6 + tech-stack lock)

## Dependencies

- #002

## Anti-criteria (P0)

- Does NOT add a service-account "bypass RLS" role without policy controls
- Does NOT permit any tenant-scoped table without an RLS policy
- Does NOT trust application WHERE clauses for tenancy isolation

## Skill mix (3–5)

- Postgres RLS + SET ROLE patterns
- Go connection-pool middleware
- sqlc with role/GUC awareness
- Negative-test discipline (integration tests that try to leak)
- Audit tooling (CI check)
