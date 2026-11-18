# ADR 0026 — RLS Is the Tenant Boundary; an Explicit `tenant_id` Predicate Is an Allowed Second Leg

**Status:** Accepted

**Date:** 2026-11-17

**Decides:** whether a query on a tenant-scoped table may carry an explicit `WHERE tenant_id = $n` predicate alongside Row-Level Security.

**Ticket:** [#2337](https://github.com/mgoodric/security-atlas/issues/2337), found while reviewing #2314. Decision by Matt on the ticket, 2026-11-15.

---

## Context

Two rules were written down for the same table:

- `internal/db/queries/ucf_traversal.sql` (slice 008) said no app-level `WHERE tenant_id = ?` clause was "permitted" on `controls` reads, citing constitutional invariant 6.
- `internal/db/queries/controls.sql` (slice 009) carries `WHERE tenant_id = $1` as "belt-and-suspenders alongside the GUC-driven RLS policy", also citing invariant 6. `fw_to_scf_overlay_edges.sql` (#1797) and `audited_under.sql` (#2314) call that second leg the house style.

Invariant 6 (`AGENTS.md`, canvas §5.4, slice 033) says isolation is enforced by RLS, not application code. It forbids _depending_ on an application filter for isolation. It says nothing against _adding_ one.

## Decision

1. **RLS is the tenant boundary.** Every tenant-scoped table has an RLS policy that denies on missing context. That policy, not any query text, is what isolates tenants.
2. **An explicit `tenant_id = $n` predicate is an allowed second leg**, and preferred where the tenant id is already at hand (a handler that resolved it, a store method that takes it).
3. **The predicate can only narrow.** It never replaces RLS, and no query may depend on it for isolation. A query that takes no tenant parameter relies on RLS alone, and that is equally correct.
4. **Tests prove isolation without it.** An integration test of tenant isolation must still hold if the predicate is removed: it runs as the non-owner app role (`$DATABASE_URL_APP`), where RLS binds.

## Consequences

- `ucf_traversal.sql` and `internal/api/ucfcoverage` now say their reads rely on RLS and take no tenant parameter, instead of saying invariant 6 forbids the predicate. No query changed.
- `controls.sql`, `fw_to_scf_overlay_edges.sql` and `audited_under.sql` already follow this rule; their comments stand.
- A reviewer does not ask for a `tenant_id` predicate to be removed, and does not accept one as the only isolation a new table has.
