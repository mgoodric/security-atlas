# 518 — HIPAA FrameworkScope ePHI-environment example

**Cluster:** Scope
**Estimate:** M (1-2d)
**Type:** JUDGMENT + design (FrameworkScope predicate authoring)
**Status:** `deferred` (pairs with the phase-3 covered-entity workflow, #517)

## Narrative

Slice 481 shipped the HIPAA Security Rule catalog and noted the
**FrameworkScope** tie-in (canvas invariant #5, §5.5) but deliberately did NOT
ship a HIPAA FrameworkScope ePHI-environment example — that pairs with the
deferred covered-entity workflow (P0-481-7). This slice ships the worked example
proving the FrameworkScope intersection for HIPAA: the HIPAA covered-systems /
ePHI environment is a distinct framework scope from the SOC 2 system boundary
and the PCI CDE, so `effective_scope(control, hipaa) = applicability_expr ∩
hipaa_framework_scope.predicate` (canvas §5.5).

The point the example proves: a control that is in-scope for SOC 2's system
boundary may be out-of-scope for HIPAA's ePHI environment (and vice versa) — the
graph computes the intersection per (control, framework) pair, it does NOT
assume one global scope. PCI CDE ≠ HIPAA covered systems ≠ SOC 2 system.

**Dependency note:** this slice is most meaningful alongside the covered-entity
workflow (#517) because the ePHI-environment predicate authoring UX is part of
that workflow's FrameworkScope ownership flow (open question:
"FrameworkScope ownership workflow UX … decide before PCI/HIPAA modules ship").
File together; sequence #517 design first.

## Threat model

Inherits the FrameworkScope (slice 018) threat model: scope predicates are
tenant-scoped configuration that gate which controls a framework evaluates. A
malformed or over-broad ePHI predicate could include or exclude the wrong
controls from a HIPAA evaluation — a correctness/compliance risk, not a
confidentiality leak.

- **S — Spoofing.** No new unauthenticated endpoint; scope authoring is an
  authenticated, RBAC-gated tenant operation.
- **T — Tampering.** The ePHI predicate is tenant-scoped mutable state — must be
  RLS-enforced (invariant #6); a tampered predicate changes which controls HIPAA
  evaluates, so changes must be audit-logged.
- **R — Repudiation.** Scope-predicate edits must be auditable (who changed the
  ePHI-environment boundary, when).
- **I — Information disclosure.** The example uses synthetic scope cells, not
  real ePHI; tenant isolation on scope config is the relevant control.
- **D — Denial of service.** Scope intersection is a bounded set operation over
  the tenant's scope cells.
- **E — Elevation of privilege.** Scope authoring composes with RBAC; no new
  role; no implicit cross-tenant scope reuse.

## Acceptance criteria

- [ ] **AC-1.** A worked HIPAA FrameworkScope example: an ePHI-environment
      predicate distinct from the SOC 2 system boundary and the PCI CDE.
- [ ] **AC-2.** An integration test proving `effective_scope(control, hipaa) =
applicability_expr ∩ hipaa_framework_scope.predicate` — a control in-scope
      for SOC 2 but out-of-scope for HIPAA (and vice versa).
- [ ] **AC-3.** Tenant isolation on the scope predicate (RLS, invariant #6).
- [ ] **AC-4.** Decisions log + changelog.

## Dependencies

- **#481** (HIPAA catalog) — merged.
- **#018** (FrameworkScope) — the intersection primitive.
- **#517** (covered-entity workflow) — design the predicate-authoring UX there
  first; sequence after.

## Anti-criteria (P0)

- Does NOT use real ePHI in the example (synthetic scope cells only).
- Must enforce tenant isolation at the DB layer (RLS, invariant #6).
- Does NOT assume a single global scope — proves the per-framework intersection.
