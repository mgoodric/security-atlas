# 620 — Map an unmapped vendor claim to an SCF anchor (operator mapping UX)

**Cluster:** evidence-pipeline (OSCAL) / UI
**Estimate:** M (2-3d)
**Type:** JUDGMENT (the anchor-picker UX + the mapping-write validation are
subjective calls)
**Status:** `blocked` — depends on #589 (vendor-claim read + disposition)
landing the read surface + the `unmapped` flag.
**Parent:** #589. Spun off from slice 589's decisions-log D8 — that slice
surfaces the `unmapped` flag (the slice-512 `scf_anchor_id IS NULL` case) but
defers the write-side anchor-mapping flow.

## Narrative

Slice 512 lands vendor claims with a NULLABLE `scf_anchor_id`: NULL means the
importer found no deterministic SCF crosswalk for the claim's target control —
it needs operator mapping (the claim is never dropped for being unmappable).
Slice 589's vendor-claims view SHOWS which claims are unmapped (an "Unmapped to
SCF" badge) but provides no affordance to map one.

This slice adds the operator mapping: from the vendor-claims view, an operator
maps an unmapped claim to a canonical SCF anchor (requirement → SCF anchor
only, invariant #7). The mapping is the human-approved crosswalk; once set, it
is canonical for that claim.

## Deliverable

- An SCF-anchor picker (search the bundled `scf_anchors` catalog).
- A write endpoint, e.g. `PATCH /v1/oscal/component-claims/{id}/scf-anchor`
  with `{scf_anchor_id}` — `grc_engineer`-gated; tenant-scoped (RLS); append a
  mapping-audit row.
- The view updates the claim's `unmapped` flag + the mapping badge on success.

## Anti-criteria (P0)

- Requirement → SCF anchor only (invariant #7): no claim → claim mapping.
- Does NOT let mapping fabricate control coverage (the claim stays a claim;
  this only sets the crosswalk).
- Tenant isolation (RLS); no cross-tenant or anonymous mapping.

## Dependencies

- #589 (vendor-claim read + disposition) — must land first.
