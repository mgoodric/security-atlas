# #1804 — Overlay-edge remap proposals: decisions log

detection_tier_actual: none
detection_tier_target: integration

Importing a new SCF release now queues every tenant's active overlay edge (`fw_to_scf_overlay_edges`, #1735) still on an anchor of an older release, in that edge's tenant. The tenant's own writers decide each proposal at `/v1/mapping-overlays/remap-proposals`. This is the third subject type of #1718's remap queue, after controls (#1775, #1776) and catalog crosswalk edges (#1777). It fills the extension point #1777's D1 left.

#2360, filed during #1777, describes the same work. This change covers it. Its one difference is noted in D3.

## D1 — A sibling table under RLS, not a widening of #1777's

`scf_overlay_remap_proposals` and `scf_overlay_remap_decisions` carry `tenant_id`, ENABLE and FORCE RLS, and `tenant_read` / `tenant_update` (with WITH CHECK) / `tenant_insert` policies. Their foreign keys are composite `(tenant_id, …)` (#2336). A proposal or decision can therefore name only overlay edges of its own tenant, and a row moved to another tenant fails both the FK and the WITH CHECK. The overlay table gains `UNIQUE (tenant_id, id)` as the FK target. `scf_edge_remap_proposals` stays tenant-free, as #1777's migration header promised.

History cascades from the overlay edges and anchors, like #1777's and unlike #1776's RESTRICT. An overlay edge is deleted only by a cascade from its catalog requirement or anchor, and the catalog retires rather than deletes. RESTRICT would only break the test harness's catalog wipe.

## D2 — No SECURITY DEFINER anywhere

**Decide** runs as `atlas_app` under the caller's `app.current_tenant`. The app role holds SELECT and UPDATE on proposals, SELECT and INSERT on decisions (append-only), and already holds INSERT and UPDATE on overlay edges. RLS alone keeps a tenant to its own rows: another tenant's proposal id is not found (404). The integration test checks this at the SQL level (B sees 0 of A's rows, its UPDATE touches 0 rows, and moving its own row into A fails) and through the API.

**Queue** is one act over every tenant. It runs as the migrate role (BYPASSRLS), as `QueueRemapProposals` (#1775) and `QueueEdgeRemapProposals` (#1777) already do, from `atlas-cli catalog import-scf`. Each proposal's `tenant_id` is read from its overlay edge, and the composite FK refuses any other value. A definer function would have been needed only if the app role queued. It does not: there is no INSERT grant or policy for it, and the test asserts INSERT and DELETE are denied.

We did not reuse #1777's "refused under a tenant context" guard. It exists because those rows are global; these are tenant-owned, so a tenant context is required, not refused.

## D3 — Approval retires the legacy overlay edge

This differs from #1777's D2, where the legacy catalog edge is left untouched, and from #2360's text ("the legacy overlay edge stays untouched as history"). #1804's acceptance criterion says to retire it with reason `remapped`, and the ticket wins. History is not lost: a retired overlay edge keeps its row, actor and reason, and both changes write the overlay audit trail (`create` for the new edge, `retire` for the old). Retiring also matters for the overlay list: an active overlay edge on a superseded anchor would otherwise stay listed as live forever.

Order inside the transaction: lock the proposal, lock the legacy edge, insert the new edge, retire the old. The active-pair unique index is on (tenant, requirement, anchor), and the two anchors differ, so the order cannot trip it.

## D4 — The copy carries the assertion; the actor is the decider's credential

The new edge takes the legacy edge's requirement, `relationship_type`, `strength`, `rationale` and `source_ref`. `created_by` on it, `retired_by` on the old one, and the audit `actor` are the deciding credential's id, the same actor every other overlay write records (#2375). The decision row's `decided_by` is `credstore.PrincipalOf` (a users(id) uuid), as for control remap decisions (#1776). We did not copy the legacy `created_by`, as #1803's carry-over does: that is a system act, and this one has a person to record.

## D5 — Refusals mirror Create

An approval is refused, and changes nothing, when:

- the tenant already has an active overlay edge on the target pair (409 `overlay_edge_exists`, from the unique index);
- an active catalog edge already joins the target pair (409 `catalog_edge_exists`). This is `Create`'s rule: such an overlay adds nothing, and the effective-edges view would shadow it anyway. The reviewer can keep the proposal legacy or retire the old overlay by hand;
- the legacy edge was retired after queueing (409 `remap_edge_changed`).

A split or no-successor proposal needs `scf_id` (400), and the code must exist in the target release (400). `scf_id` on reject or keep-legacy is a 400 rather than being ignored.

## D6 — Authz is the existing `mapping-overlays` resource

The routes live under `/v1/mapping-overlays/remap-proposals`, so OPA derives resource `mapping-overlays`, which admin and grc_engineer already write. No rego changes. Viewer, auditor and control_owner are refused, list included, as they already are for overlay edges. `internal/authz/overlay_remap_1804_test.go` pins this. The handler re-checks the role (`requireOverlayWrite`) and, as #1776 does, refuses a machine credential: a remap decision is a person's (#2231).

## D7 — No bulk approve-unchanged yet

Controls and catalog edges have `approve-unchanged`. The acceptance criteria don't ask for it, and a tenant's overlay queue is small (overlays are hand-made assertions). It can follow as its own claim if a tenant's queue grows.
