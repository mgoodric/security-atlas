# 678 — Demo seed completeness: org_units, questionnaires, ack-role users, framework posture

**Cluster:** Demo-seed
**Estimate:** M (1-2d)
**Type:** JUDGMENT (how much breadth the demo should demonstrate)
**Status:** `ready` — clusters the demo-seed coverage gaps (ATLAS-028 + ATLAS-037).

## Narrative

A user touring the seeded demo hits empty/zero states on several headline features because
the seed does not populate them. Re-verified on `main` build `2a3805b`. Orchestrator-confirmed
against `internal/demoseed` (the seed's `INSERT INTO` set has no `org_units`, no
`questionnaires`).

| Sub           | Gap                                                                                                                                                                                                                                                                                                                                                               |
| ------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **ATLAS-028** | `/risks/hierarchy` Org tree, Theme heatmap, and Decision timeline are all empty ("No org_units yet" / "No themed risks yet" / "No decisions recorded yet"). 20 risks show owners (CISO, DevSecOps) but those aren't linked to **org_unit** entities (none seeded), so the hierarchy is empty and the per-risk "View in hierarchy" link (`?focus=<id>`) dead-ends. |
| **ATLAS-037** | Dashboard "Framework posture" empty; `/questionnaires` empty ("Upload your first…"); all 5 policies show "no required-role users" for acknowledgment (no role-holder users seeded → ack feature inert).                                                                                                                                                           |

The framework-posture half overlaps slice 671 (posture needs evaluation to run); this slice
owns the **seed-data** gaps (org_units + risk→org_unit linkage, a questionnaire, ack-role users,
themed risks / a decision-timeline entry).

## Threat model

Seed writes BYPASSRLS with correct tenant_id (existing pattern); new rows must carry the
tenant_id and respect the slice-205 `demo_only` forensic mark + `PopulatedRowCap` guard. No
new evidence_kind or wire change.

## Acceptance criteria

- [ ] **AC-1 (028).** Seed `org_units` + link the seeded risks' owners to org_unit entities so
      `/risks/hierarchy` renders an org tree; seed themed risks so the heatmap populates and at
      least one decision so the timeline is non-empty. `?focus=<id>` highlights the focused risk.
- [ ] **AC-2 (037).** Seed at least one **questionnaire** so `/questionnaires` is demonstrable.
- [ ] **AC-3 (037).** Seed the role-holder **users** that policy acknowledgment requires, so the
      5 seeded policies show a real ack roster (not "no required-role users").
- [ ] **AC-4 (037 posture).** Coordinate with slice 671 so "Framework posture" tiles populate
      (this slice ensures the framework_versions are active/seeded; 671 runs the evaluation).
- [ ] **AC-5.** JUDGMENT (decisions log): scope of demo breadth — which features MUST be
      demonstrable for v1 (the binary "diligence the diligence tool" tour) vs deferred. Keep the
      seed coherent + within `PopulatedRowCap`.

## Anti-criteria

- Does NOT seed real PII or violate the slice-491/205 PII boundaries (use fictional
  `@demo.example` identities).
- Does NOT bypass the demo forensic mark / populated-tenant guard.

## Dependencies

- `internal/demoseed` (slice 205) + the org_unit / questionnaire / policy-ack schemas.
- Posture half pairs with slice 671; raw-UUID hierarchy labels pair with slice 670/AC-6.

## Notes

Source: 2026-06-10 demo-tenant audit, items **ATLAS-028 (medium/major), ATLAS-037 (medium/major)**.
Re-tested open on `2a3805b`. The seed undersells the product — this closes the empty-state tour gaps.
