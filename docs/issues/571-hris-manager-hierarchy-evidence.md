# 571 — HRIS connectors: manager-hierarchy evidence (Rippling + BambooHR)

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + identity boundary)
**Status:** `blocked` (depends on #491 — base HRIS connectors — merged first)

## Narrative

Slice 491 shipped the Rippling + BambooHR HRIS connectors emitting
`hris.worker_lifecycle.v1` (worker roster + joiner/mover/leaver facts). Each
record already carries the worker's direct `manager_assignment_id` (the opaque
manager worker id) — enough to know who a worker reports to, but not the full
reporting tree.

Access-review routing wants the **manager hierarchy** as first-class evidence: to
auto-route a worker's entitlement review to the right approver chain, and to
detect orphaned reports (a worker whose manager is terminated). This slice adds a
manager-hierarchy evidence surface (still opaque assignment ids only — NEVER
manager personal contact detail, preserving the slice-491 PII boundary) derived
from the same read-only directory reads.

The shared evidence-kind-vs-extend-existing call is a JUDGMENT for the
implementing agent (likely a new `hris.manager_hierarchy.v1` rather than bloating
the per-worker lifecycle record). The platform-side wire stays push (invariant
#3); the field set stays opaque-id-only (no new PII).

## Dependencies

- **#491** (base HRIS connectors) — must merge first; the auth packages,
  read-only scope, and over-collection guard are reused unchanged.

## Anti-criteria (P0)

- Does NOT widen the platform-side wire — push only (invariant #3).
- Does NOT collect any field beyond opaque worker/manager assignment ids +
  lifecycle facts — NO manager personal contact detail, and none of the
  slice-491 excluded sensitive-PII fields.
- Does NOT require a full-PII / write HRIS scope — read-only minimal-field only.

Parent: #491.
