# #1777 — Crosswalk-edge remap proposals: decisions log

detection_tier_actual: integration
detection_tier_target: integration

Importing a new SCF release now queues every live catalog crosswalk edge (`fw_to_scf_edges`) still on an anchor of an older release. A super-admin decides each proposal at `/v1/admin/crosswalk-edge-remap-proposals`. This is the edge half of #1718's remap queue. The control half is #1775 and #1776.

## D1 — Only global edges; tenant overlays are #2360

#1735's tenant overlay edges (`fw_to_scf_overlay_edges`) exist on `main`. The ticket said to include them as a tenant-scoped subject type. No acceptance criterion covered them, though, and they need a tenant-keyed table with RLS and tenant-admin authz, which would roughly double the change. With the dispatcher's agreement, this change covers global edges only, and #2360 adds overlays.

The extension point: an overlay queue is a sibling table keyed on `tenant_id` under RLS, shaped like `scf_remap_proposals`. It is not a widening of `scf_edge_remap_proposals`, which stays tenant-free. It classifies with the same `scfimport.ClassifyLineage`. The migration header says so.

## D2 — The legacy edge is left exactly as it is

Approval inserts a new edge on the chosen anchor and changes nothing on the legacy row: not `retired_at`, not `updated_at`, not the tier. The acceptance criterion says "A's edge is unchanged". The dispatcher's first brief said to retire it and then withdrew that. Retiring isn't needed for coverage: since #2203 the default read pins to the current release, so the legacy edge already stops counting there, and `?scf_release=<old>` still reads it. The coverage test asserts both reads.

## D3 — The copy carries every content column, and its own `crosswalk_source`

The new edge copies `relationship_type`, `strength`, `rationale`, `source_attribution` and `mapping_tier` from the legacy edge. It sets `crosswalk_source = 'scf-remap:<proposal id>'`.

The crosswalk importer retires only edges whose `crosswalk_source` equals its own (#1971, `soc2import.ownsEdge`). If the copy kept the legacy edge's source, re-importing the old crosswalk would see an edge it no longer lists and retire the approved remap. With its own source, the remap survives a re-import. A later crosswalk that lists the same pair in the new release updates the row in place and takes ownership, as it would for any pre-existing edge.

## D4 — An approval onto an anchor the requirement already maps to is refused

`fw_to_scf_edges` is unique on (requirement, anchor). If the requirement already has an edge on the chosen anchor, for example because a crosswalk was re-imported in the new release or #2315's carry-forward ran first, approval returns 409 `edge exists` and changes nothing. Bulk approval skips that proposal and reports the reason. We considered overwriting the existing edge and rejected it: that edge may have its own reviewed content and tier, and silently replacing it is the rewrite #1718 forbids. The reviewer can keep the proposal legacy.

## D5 — The database admits the app role only through a function

The edges are global, so the tables carry no `tenant_id` and no RLS. The database cannot see the super-admin claim, so that gate lives in the handler and OPA (D6). The database still narrows what the app role can do:

- **Tables:** `SELECT` on `scf_edge_remap_proposals` and `scf_edge_remap_decisions`. No `INSERT`, `UPDATE` or `DELETE` on either, and no `INSERT` on `fw_to_scf_edges`.
- **`scf_edge_remap_decide(proposal, decision, scf_id, reason, decided_by, bulk)`** (`SECURITY DEFINER`, `search_path` pinned, `EXECUTE` revoked from `PUBLIC`). It locks the proposal and checks the status, that the legacy edge is still live and on its anchor, that the code resolves in the target release, and that no edge already exists. It then writes the edge, the proposal status and the history row in one statement. Errors carry a stable `edge-remap: <kind>` prefix, which `crosswalkremap` maps to typed errors.
- **`scf_edge_remap_lock_open_unchanged()`** takes bulk approval's `FOR UPDATE NOWAIT` lock. The app role holds no `UPDATE` privilege, so it cannot lock the rows itself. A second concurrent bulk run gets 409 rather than queueing.

Both functions also refuse to run when `app.current_tenant` is set (review, the #2362 guard): a tenant-scoped transaction has no business deciding global edges. `TestEdgeRemap_FunctionsRefuseATenantContext` calls each under `set_config('app.current_tenant', …)`, and with the guard removed it fails. The super-admin store never sets a tenant context. Two proposals approved onto the same (requirement, anchor) at once can both pass the existence check; the second insert then hits `fw_to_scf_edges`' unique constraint, and the store maps SQLSTATE 23505 to `ErrEdgeExists` (409) as well.

A compromised app credential therefore cannot insert arbitrary global edges, forge history, or flip a status without a matching decision row. `TestEdgeRemap_AppRoleCannotWriteAroundTheDecideFunction` asserts `permission denied` for each direct write.

We considered `GRANT INSERT ON fw_to_scf_edges`, like the column-level `UPDATE(mapping_tier)` that slice 483 granted, and rejected it. It would let the app role write any edge, not just a reviewed copy.

## D6 — Super-admin only, list included; enforced in OPA and the handler

All five routes require the verified JWT's `atlas:super_admin` claim. That includes the list, because it is the review surface for global writes. Tenants see the outcome through coverage and anchor reads.

- **OPA:** `super_admin.rego` adds `super_admin_only_resource` for the `crosswalk-edge-remap-proposals` admin segment and admits a super-admin there for `read`, `write` and `approve`. `admin.rego`'s blanket allow and `grc_engineer.rego`'s blanket read both gain `not super_admin_only_resource`. Without that, a tenant admin would pass OPA and only the handler would stop them. This is the first super-admin-only admin segment. Existing super-admin surfaces such as `/v1/admin/crosswalk-edges/{id}/tier` still rely on the handler alone; moving them is out of scope here.
- **Handler:** `requireSuperAdmin` reads the claim, as `admincrosswalktier` does. A per-tenant admin API key never carries it.
- **Decider:** recorded as `credstore.PrincipalOf`. A super-admin claim always belongs to a person's JWT, so the control path's `approval.Check` people-only rule holds by construction.

## D7 — Classification is shared, not forked

`RemapClassFor` (controls) now delegates to `ClassifyLineage(AnchorLineage)`. The edge queue fills the same `AnchorLineage` from a query whose lineage columns match `ListControlsOnLegacySCFAnchors`. The existing #1775 classification tests still pass unchanged.

## D8 — History cascades with the catalog rows it describes

Unlike `scf_remap_decisions` (RESTRICT, #1776 R2), the edge history cascades from its proposal, edge and anchors. The catalog never deletes edges in production: it retires them (#1971) and freezes an imported release's anchors. So cascade only matters to test and reseed wipes. Those wipes (`truncateCatalog`, `scfseed`, the ucfcoverage and soc2import cleanups) `DELETE FROM fw_to_scf_edges` across many packages, and with RESTRICT every one of them would need to learn about this table first.

## D9 — No CLI or web UI in this change

The acceptance criteria name the queue, the apply path and the API gate. #1776 shipped `atlas-cli catalog remap` for controls. An `atlas-cli catalog edge-remap` and the review page (#1718 stories 18 to 26) belong in follow-ups.

## Bug found on the way

The first migration also stored `framework_requirement_id` on the proposal. `soc2import`'s `TestImport_NoDirectRequirementToRequirementTableExists` (invariant 1: `fw_to_scf_edges` is the only catalog bridge into `framework_requirements`) failed on it. The column was dropped, and the requirement is read through the edge.
