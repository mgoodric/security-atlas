# #1798 — coverage and traversal count the tenant's overlay edges

Parent: #1735 (tenant overlay edges). Builds on #1796 (the
`effective_fw_to_scf_edges` view) and #1797 (`fw_to_scf_overlay_edges`).

detection_tier_actual: none
detection_tier_target: none

## What changed

- Migration `20261106000000_effective_edges_overlay_union.sql` rebuilds
  `effective_fw_to_scf_edges` as the catalog edges `UNION ALL` the caller
  tenant's active overlay edges, and appends an `origin` column
  (`catalog` | `overlay`).
- The five UCF traversal queries project `origin`; the coverage wire shapes
  (`anchors[]` on requirement coverage, `requirements[]` on control coverage
  and anchor traversal) carry it.
- `internal/api/ucfcoverage` reads the view inside the request's tenant
  transaction. It read through the bare pool before, which was correct for
  catalog-only edges and would hide every overlay edge now.

Posture, its trend, the dashboard export, MCP `list_framework_posture`,
scope contradictions, the SoA linked-controls column, control-import
suggestions and the anchor-list framework badges all already read the view
inside a tenant transaction, so they count overlay edges with no change.

## Decisions

**D1 — Tenant isolation is enforced twice.** The view stays
`security_invoker`, so `fw_to_scf_overlay_edges`' FORCE RLS binds on the
caller. The overlay arm also filters `current_tenant_matches(tenant_id)`,
so a role that bypasses RLS still sees only the tenant in
`app.current_tenant`, and a read with no tenant context sees catalog edges
only. `TestOverlayCoverage_ViewHoldsTenantFilterWithoutRLS` was run against
the view without that predicate and failed (2 rows for A, 2 with no
context), then passed with it.

**D2 — The mark is a new `origin` column, not `source_attribution` alone.**
Overlay rows carry `source_attribution = 'org_internal'`, the existing
provenance value for an organisation's mapping. A catalog edge may carry
`org_internal` too (an importer can write it), so that value cannot tell an
overlay from a catalog row. `origin` can.

**D3 — Overlay rows carry `mapping_tier = 'verified'`.** ADR 0018's tiers do
not apply to an overlay edge (#1797). The view's column is a non-null enum,
and every consumer drops `rejected`, so the arm needs a tier that counts.
`verified` is the honest one for an attested org assertion; `origin` says
where the row came from.

**D4 — One path per pair.** The API refuses an overlay on a pair with a live
catalog edge (#1797, `catalog_edge_exists`), but a later catalog import can
add the pair after the overlay exists. The overlay arm leaves out a pair
the catalog maps with a live edge (not `no_relationship`, not `rejected`),
so a pair is never counted or listed twice. Where the catalog edge is
rejected or `no_relationship`, the overlay row is the live path.

**D5 — `GET /v1/requirements/{id}/anchors` (anchors handler) is unchanged.**
It reads through the bare pool, so it lists catalog edges only, as before.
It is the crosswalk inspector, not a coverage view, and the ticket does not
name it.

**D6 — Down migration drops and recreates the view.** `CREATE OR REPLACE
VIEW` cannot remove a column. No other view depends on this one. It
restores the 20261014000000 shape (#1971: live catalog edges only).

**D7 — Built on #1971's retired catalog edges.** The catalog arm keeps
`retired_at IS NULL`, and the one-path-per-pair check counts only an
unretired catalog edge, so an overlay edge is the live path for a pair whose
catalog edge was retired. `TestOverlayCoverage_RetiredCatalogEdgeYieldsToOverlay`
failed against the view without the retired check in the dedupe (no path,
33.33%) and passes with it.
