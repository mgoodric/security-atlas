# 663 — Risk creation is a dead end on a fresh tenant (mitigate requires controls that don't exist)

**Cluster:** Risks
**Estimate:** S (0.5-1d)
**Type:** JUDGMENT (default treatment / when to relax the linked-controls requirement)
**Status:** `ready` — surfaced by the 2026-06-10 empty-tenant UI audit (ATLAS-006).

## Narrative

The Add-risk form (`/risks/new`) defaults **Treatment = "mitigate"**, which makes **Linked
controls** required. On a fresh tenant there are no instantiated controls ("No active
controls in this tenant yet"), so a mitigate-treatment risk **cannot be created** — the
primary create flow dead-ends for a brand-new operator. Re-verified on `main` build `2a3805b`.

## Threat model

No security surface — form validation / empty-state UX. No data/scope/wire change.

## Acceptance criteria

- [ ] **AC-1.** A new tenant with zero instantiated controls can create an initial risk
      through the default flow (no unsatisfiable required field).
- [ ] **AC-2.** JUDGMENT (decisions log): choose the fix — (a) default Treatment to a value
      that does not require linked controls (e.g. "accept"/"assess"), or (b) relax the
      linked-controls requirement to optional when zero controls exist in the tenant, or
      (c) both. Record the choice + rationale.
- [ ] **AC-3.** When controls DO exist, mitigate-treatment risks still encourage/allow
      linking controls (the requirement is only relaxed in the genuine zero-control case, if
      (b) is chosen) — the fix must not silently drop the control-linkage affordance for
      populated tenants.
- [ ] **AC-4.** Test coverage: risk-create succeeds on an empty tenant; the chosen default/
      relaxation is asserted.

## Anti-criteria

- Does NOT remove control-linkage from risks generally (only addresses the fresh-tenant
  dead-end).
- Does NOT auto-create placeholder controls to satisfy the requirement.

## Dependencies

- The risk-create form + validation (`web/app/(authed)/risks/new`, slice 105) and the risk
  create API (`internal/api/risks`).

## Notes

Source: 2026-06-10 empty-tenant browser audit, item **ATLAS-006** (priority medium /
severity major). Re-tested open on build `2a3805b`.
