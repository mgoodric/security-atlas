# ADR 0011 — Tenant isolation at the database layer via PostgreSQL Row-Level Security

**Status:** Accepted — **retrospective** record of a founding invariant (CLAUDE.md
architecture invariant #6). The decision was made and shipped long before this
ADR; this record reconstructs the trade-off context and the rejected
alternative after the fact. It does NOT re-open the question.

**Date:** 2026-06-04

**Records:** CLAUDE.md architecture invariant **#6** ("Tenant isolation is
enforced at the database layer via PostgreSQL Row-Level Security on every
tenant-scoped table. Not application code. RLS denies on missing context.").

**Canvas:** [`Plans/canvas/05-scopes.md`](../../Plans/canvas/05-scopes.md) §5.4.

**Implementation reference (canonical — cited, not restated):**
[`docs/architecture/rls.md`](../architecture/rls.md) (the contributor-facing
companion) and the integration suite at
[`internal/db/rls_integration_test.go`](../../internal/db/rls_integration_test.go).

---

## Context

security-atlas is multi-tenant from day one: a single self-hosted Postgres
instance can serve several tenants, and the SaaS shape gives each tenant its
own RLS context (canvas §5.4). Every tenant-scoped primitive — Control, Risk,
Evidence, Scope, Framework, Policy — carries a `tenant_id` and must never leak
across the tenant boundary.

The primary user is a solo security leader whose own customers will diligence
the diligence tool. "Tenant A's evidence never reaches Tenant B" is therefore
not a hygiene property — it is a load-bearing security claim a reviewer will
probe directly. The question this record answers is: **where does that boundary
live, and what happens when the request forgets to assert its tenant?**

Two facts shape the answer:

1. **Application-code correctness is the wrong thing to bet a tenant boundary
   on.** A single handler that issues a query without a `WHERE tenant_id = ...`
   clause is a cross-tenant disclosure. Over a codebase's lifetime, with many
   handlers and many contributors, "every query remembers the filter" is a
   guarantee that erodes — and erodes silently, because the missing filter
   still returns a valid-looking result set.
2. **The failure mode must be deny, not pass.** When tenant context is absent
   (an unauthenticated path, a forgotten middleware, a background job that
   never set the GUC), the safe behavior is to return nothing, not everything.
   A fail-open boundary is worse than no boundary because it looks like it
   works in every test that happens to set context.

## Decision

**Enforce tenant isolation in the database via PostgreSQL Row-Level Security,
not in application code.** Every tenant-scoped table carries a `tenant_id UUID`
column, `ENABLE ROW LEVEL SECURITY` + `FORCE ROW LEVEL SECURITY`, and at least
one `CREATE POLICY` whose `USING` predicate compares `tenant_id` to the
per-connection `app.current_tenant` GUC. The boundary is two cooperating
halves (see `docs/architecture/rls.md` "The two halves of the guarantee"):

- **Schema half** — the policy + `FORCE ROW LEVEL SECURITY` on every
  tenant-scoped table.
- **Runtime half** — every request sets `app.current_tenant` from the
  authenticated credential's tenant id, before any tenant-scoped SQL runs.

**Deny-on-missing-context is structural, not conventional.** The policy
predicate routes through the helper `current_tenant_matches(row_tenant uuid)`,
which evaluates `row_tenant::text = current_setting('app.current_tenant',
true)`. The `true` ("missing OK") flag makes `current_setting` return NULL
when the GUC is unset; comparing anything to NULL yields NULL, which a `USING`
clause treats as false. **There is no default-allow path** — an unset context
returns zero rows, not every row. This is the deny-on-missing-context property
invariant #6 names, and it is a property of the predicate, not of any
application check that could be forgotten.

The role model (`atlas_migrate` owns DDL; the application connects as a
least-privileged service role that is subject to `FORCE ROW LEVEL SECURITY`)
is documented canonically in `docs/architecture/rls.md`; this ADR cites it
rather than restating it, so the two cannot drift.

## Consequences

**Positive:**

- The tenant boundary holds even when application code forgets a filter — the
  one failure that a hand-rolled `WHERE` clause cannot survive is exactly the
  one RLS makes impossible.
- Deny-on-missing-context means a forgotten middleware degrades to "zero rows"
  (a loud, debuggable empty result) rather than "all tenants' rows" (a silent
  breach).
- A single Postgres instance can safely serve multiple self-hosted tenants —
  no per-tenant database sprawl required for the solo-leader shape.
- The boundary is auditable in one place: a reviewer reads the policy
  predicate and the `FORCE ROW LEVEL SECURITY` grant, not every handler.

**Negative / accepted trade-offs:**

- **Every new tenant-scoped table is a checklist item.** Forgetting the policy
  on a new table silently disables RLS for it. Mitigated by the slice-033
  audit script (`just audit-rls`), which fails CI when a tenant-scoped table
  lacks the policy + `FORCE` — the schema half is machine-checked, not
  trusted to review.
- **The GUC must be set on every code path**, including background jobs and
  migrations. Mitigated by the `internal/api/tenancymw` middleware (the
  runtime half) and by deny-on-missing-context: a path that forgets returns
  nothing rather than leaking.
- **RLS predicates add per-query overhead.** Accepted: the helper is declared
  `STABLE PARALLEL SAFE` so the planner can hoist it; the cost is negligible
  against the value of an un-forgettable boundary.
- **Testing a database-level boundary needs real Postgres**, not mocks. This
  is why the integration tier exists and why `internal/db/rls_integration_test.go`
  is a first-class part of the merge gate.

## Alternatives considered (rejected — recorded retrospectively)

- **Application-layer tenant filtering (every query carries `WHERE tenant_id
= $current`).** Rejected. This is the dominant failure mode of multi-tenant
  systems: it depends on every query, written by every contributor, over the
  whole life of the codebase, never forgetting the filter — and it fails
  silently (a forgotten filter returns a plausible result set). It also
  fails open: a path with no tenant context returns everything. RLS makes the
  boundary independent of application-code correctness and fail-closed by
  construction. (CLAUDE.md invariant #6 names this rejection explicitly: "Not
  application code.")
- **Database-per-tenant (separate Postgres database or schema per tenant).**
  Rejected for v1. It is a strong boundary, but it imposes per-tenant
  provisioning, migration fan-out, and connection-pool sprawl that is absurd
  for the solo-leader self-host shape (one VM, one Postgres). RLS gives the
  same "one tenant cannot see another" guarantee on a single instance. (The
  SaaS shape can still partition physically later if a customer demands it;
  RLS does not preclude that.)
- **Connection-pooled `SET ROLE` per tenant with per-tenant DB roles.**
  Rejected. It multiplies the number of database roles by the number of
  tenants and couples role provisioning to tenant lifecycle, without buying a
  stronger guarantee than a single service role under `FORCE ROW LEVEL
SECURITY` + the `app.current_tenant` GUC already provides.

## Related decisions

- Composes with **ADR-0001** (FrameworkScope predicate lifecycle): the approver
  role is tenant-scoped via RLS like every other primitive.
- Composes with **ADR-0014** (multidimensional scope + FrameworkScope
  intersection): scope cells and framework-scope predicates are themselves
  tenant-scoped rows under the same RLS guarantee.
- Implementation companion: `docs/architecture/rls.md`; audit machinery: slice
  033 (`just audit-rls`, `internal/api/tenancymw`).
