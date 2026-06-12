# 682 — Demo seed: anchor controls to SCF + give the demo framework requirements/STRM edges so "Framework posture" renders

**Cluster:** Demo-seed / UCF
**Estimate:** M (1-2d)
**Type:** JUDGMENT (how the demo framework gets requirements + STRM edges)
**Status:** `ready` — spillover from slice 671 (closes the posture half of UI-audit ATLAS-037).

## Narrative

Slice 671 made a seeded demo tenant show real per-control STATE / FRESHNESS by
driving the evaluator after the seed. But the dashboard "Framework posture"
tiles still render "No active framework versions yet" — and slice 671 proved
(decisions log D4) that this is INDEPENDENT of evaluation: even with
`control_evaluations` fully populated, the posture query returns zero rows for
the demo tenant.

**Root cause (verified against `internal/db/queries/dashboard.sql`
`FrameworkPosture`).** Posture computes coverage through the SCF-anchor spine
(constitutional invariant #1):

```
framework_versions (status='current')
  -> framework_requirements
    -> fw_to_scf_edges (STRM, non-no_relationship)
      -> scf_anchors
        <- controls.scf_anchor_id  (the tenant's active controls)
```

The demo seed breaks this chain in two places:

1. **Demo controls never set `scf_anchor_id`.** `writeControls`
   (`internal/demoseed/writers.go`) inserts `scf_id` (a free-form TEXT label)
   but NOT `scf_anchor_id` (the FK the posture query joins on). The
   `covering_control` CTE finds zero controls.
2. **The demo framework has no `framework_requirements` and no
   `fw_to_scf_edges`.** When the global SCF catalog is absent, the seed
   synthesizes a bare `frameworks` + `framework_versions` pair with no
   requirements and no STRM edges, so `version_reqs` is empty.

## Threat model

No new data surface. The seed already runs `BYPASSRLS` with the correct
tenant_id; any new anchor/requirement/edge rows must stay tenant-scoped (or use
the global catalog's `tenant_id IS NULL` rows by reference, never duplicating
per-framework controls — invariant #1, #7).

## Acceptance criteria

- [x] **AC-1.** A seeded demo tenant's controls carry `scf_anchor_id` values
      that resolve against the catalog (real SCF anchors when the global SCF
      catalog is loaded; a tenant-scoped anchor set when it is not).
- [x] **AC-2.** The demo framework version carries `framework_requirements` +
      `fw_to_scf_edges` (STRM) so `FrameworkPosture`'s `version_reqs` and
      `req_anchor` CTEs are non-empty — OR the seed adopts the global SCF
      catalog's requirements/edges when present.
- [x] **AC-3.** The dashboard "Framework posture" tiles render with a non-zero
      coverage_pct for the seeded tenant (resolves the posture half of
      ATLAS-037). Coverage need not be 100% — it must be REAL.
- [x] **AC-4.** JUDGMENT (decisions log): whether to (a) require the global SCF
      catalog be loaded and anchor demo controls to it, or (b) synthesize a
      self-contained tenant-scoped anchor + requirement + edge set. Record the
      trade-off (global-catalog dependency vs. demo self-containment).
      Resolved (b)+adopt-when-present; see
      `docs/audit-log/682-demo-posture-decisions.md` D1.
- [x] **AC-5.** Integration test: a seeded tenant produces ≥1 `FrameworkPosture`
      row with coverage_pct > 0; idempotent re-seed does not duplicate edges.

## Anti-criteria

- Does NOT duplicate controls per framework (invariant #1).
- Does NOT map requirement → requirement directly (invariant #7 — mappings go
  through SCF anchors).
- Does NOT change the production posture query.

## Dependencies

- Slice 671 (post-seed evaluation) — on `main` once 671 lands.
- `internal/demoseed` (slice 205) + the UCF graph (`internal/ucf`, slice 008) +
  the SCF catalog importer (slice 006).

## Notes

Source: slice 671 decisions log D4. The posture gap was explicitly deferred
from 671 (which owns evaluation wiring) to keep that slice's scope tight. AC-4
is the load-bearing call: the global-SCF-catalog-vs-self-contained decision
shapes whether the demo seed gains an SCF-catalog precondition.
