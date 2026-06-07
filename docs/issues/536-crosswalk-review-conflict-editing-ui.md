# 536 — Crosswalk-review / conflict editing UI (STRM mapping review)

**Cluster:** Catalog
**Estimate:** L (2-3d) — editing surface + approval workflow + conflict detection

**Type:** JUDGMENT (the conflict-surfacing heuristics + the approve/reject UX
are subjective product calls)

**Status:** `deferred` (read+visualize landed in slice 482; the editing surface
waits until a maintainer wants to curate the `community_draft` crosswalks
in-product rather than by editing the YAML fixtures)

> Filed 2026-06-07 as a spillover of slice 482 (coverage-strength rollup +
> visualization). Parent: slice 482. Slice 482 was explicitly the **read +
> visualize** slice; its P0-482-1 carved out the crosswalk-review/conflict
> editing UI as a separate slice. Canvas §10.2 names "crosswalk validation
> tooling (UI for reviewing STRM mappings, surfacing conflicts)" as phase-2
> work distinct from the read-side rollup. This is that slice.

## Narrative

**Why.** Slice 482 surfaces _how strongly_ a requirement is covered (the
rollup) and slice 438 loads the STRM edges (`requirement → SCF anchor`, each
with a `relationship_type` + `strength`) from `community_draft` YAML crosswalks.
But there is no in-product surface to **review, edit, or approve** those
mappings — today a maintainer curates a crosswalk by hand-editing
`data/crosswalks/*.yaml` and re-running the importer. The canvas §3.2 model
explicitly carries auditor judgment in the `strength` field and the
`source_attribution` ladder (`community_draft` → reviewed → authoritative); the
review UI is where a human promotes a draft mapping to an approved one and
where two conflicting mappings get reconciled.

**What.** A crosswalk-review surface that:

- Lists a framework's requirement → anchor edges with their STRM type +
  strength + `source_attribution` + rationale.
- Lets a reviewer **edit** the strength / relationship-type / rationale and
  **approve** an edge (promoting `source_attribution`), with the AI-assist
  boundary respected: a suggested mapping (AI or import) is never
  audit-binding until a human one-click approves it (CLAUDE.md AI-assist
  boundary; the `ai_assisted=true ↔ human_approver` schema invariant).
- **Surfaces conflicts** — e.g. two edges from the same requirement to anchors
  in the same SCF family with contradictory strengths, or an edge whose
  relationship-type disagrees with the reverse direction.

**Scope discipline.** This is the _editing/approving_ tool. It does NOT change
the read-side rollup (slice 482) or the importer (slice 438). It does NOT ship
the cross-framework comparison matrix (slice 537).

## Threat model (STRIDE)

This slice adds a **write** surface to catalog mapping data — a higher-risk
posture than slice 482's read-only field.

**S — Spoofing.** New write endpoints behind the existing bearer/role gate;
edit/approve requires an elevated role (mapping-curator), not the viewer role
the read path uses.
**Mitigation:** gate writes behind a dedicated authz capability; never the
default viewer role.

**T — Tampering.** A crafted request must not approve a mapping the reviewer
didn't see, nor set an arbitrary `source_attribution` (e.g. jump straight to
`authoritative`). Strength is a bounded [0,1] value validated server-side.
**Mitigation:** server validates the strength range + the
`source_attribution` transition ladder; the approver id is taken from the
session, never the request body.

**R — Repudiation.** Mapping edits ARE an auditable change to catalog
semantics (they move coverage scores). Every edit + approval is logged with
the actor, the before/after diff, and timestamp (the slice-329 audit-log
discipline).
**Mitigation:** append an audit-log row per edit/approve; the importer's 438
import log remains the provenance baseline.

**I — Information disclosure.** Crosswalk edges are **global catalog** data
(not tenant-scoped) — the review surface itself leaks nothing tenant-
confidential. BUT the conflict-detection heuristics must NOT fold in any
tenant's evaluated coverage (that would re-introduce the slice-482 threat-model
I concern on a write path). Conflict detection runs over catalog edges only.
**Mitigation:** conflict detection reads `fw_to_scf_edges` only; no tenant
control/state rows enter the review computation.

**D — Denial of service.** Bounded edits over one framework's edge set; no
unbounded scan.
**Mitigation:** paginate the edge list per framework_version.

**E — Elevation of privilege.** The approve action promotes
`source_attribution`, which raises a mapping's weight in the rollup. Only the
mapping-curator role may approve; a viewer cannot.
**Mitigation:** the approve capability is a distinct authz grant.

## Constitutional invariants honored

- **#7 — Mappings go requirement → SCF anchor.** The editor edits existing
  `requirement → anchor` edges and creates none of the forbidden
  requirement → requirement kind.
- **AI-assist boundary (hard).** A suggested/imported mapping is never
  audit-binding without one-click human approval; `ai_assisted=true` records
  require `human_approver`.

## Dependencies

- **#482** (coverage-strength rollup + visualization) — establishes the
  read-side surface this slice adds editing to.
- **#438** (generic crosswalk loader) — establishes the edge data model.

## Canvas references

- `Plans/canvas/10-roadmap.md` §10.2 — "crosswalk validation tooling (UI for
  reviewing STRM mappings, surfacing conflicts)" named in phase-2.
- `Plans/canvas/03-ucf.md` §3.2 — the STRM `strength` + `source_attribution`
  model the reviewer curates.
