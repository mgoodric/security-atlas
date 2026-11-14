-- security-atlas — #1798: coverage and traversal count the tenant's overlay
-- edges (the coverage-union step of #1735).
--
-- effective_fw_to_scf_edges (#1796) is the one place requirement -> SCF
-- anchor edge eligibility is decided for coverage and traversal. It becomes
-- the catalog edges plus the CALLER tenant's ACTIVE overlay edges
-- (fw_to_scf_overlay_edges, #1797). Every consumer of the view (framework
-- posture and its trend, the dashboard export, MCP program reads,
-- per-requirement / per-control coverage, UCF traversal, scope
-- contradictions, the SoA linked-controls column) picks overlay paths up
-- with no change to its own SQL.
--
-- Tenant isolation is enforced twice:
--   * The view is security_invoker (set by #1796, kept by CREATE OR
--     REPLACE), so fw_to_scf_overlay_edges' FORCE RLS binds on the caller,
--     not on the view owner.
--   * The overlay arm also filters current_tenant_matches(tenant_id)
--     itself, so a caller that bypasses RLS (atlas_migrate, a superuser)
--     still sees only the tenant in app.current_tenant, and a read with no
--     tenant context sees catalog edges only.
--
-- Retired overlay edges never count (status = 'active').
--
-- Overlay rows in the view:
--   * origin = 'overlay' (catalog rows are 'catalog'): the mark callers
--     surface so an org mapping is never mistaken for a catalog edge.
--   * source_attribution = 'org_internal': the existing provenance value
--     for an organisation's own mapping.
--   * mapping_tier = 'verified': ADR 0018's tiers do not apply to an
--     overlay edge (it is the organisation's attested assertion, #1797),
--     and consumers drop 'rejected' edges, so the arm carries the tier that
--     lets it count. origin, not the tier, says where it came from.
--
-- One path per pair: an overlay edge is left out where the catalog already
-- has a live edge (not no_relationship, not rejected) for the same
-- requirement and anchor, so a redundant org mapping never doubles a
-- path. Where the catalog edge is rejected or no_relationship, the overlay
-- row is the live path and the catalog row stays as it was (consumers
-- filter it).
--
-- The view gains one column, appended last (CREATE OR REPLACE VIEW may only
-- add columns at the end).

CREATE OR REPLACE VIEW effective_fw_to_scf_edges
WITH (security_invoker = true) AS
SELECT
    e.id,
    e.framework_requirement_id,
    e.scf_anchor_id,
    e.relationship_type,
    e.strength,
    e.source_attribution,
    e.rationale,
    e.created_at,
    e.updated_at,
    e.mapping_tier,
    'catalog'::text AS origin
FROM fw_to_scf_edges e
UNION ALL
SELECT
    o.id,
    o.framework_requirement_id,
    o.scf_anchor_id,
    o.relationship_type,
    o.strength,
    'org_internal'::crosswalk_source_attribution,
    o.rationale,
    o.created_at,
    o.updated_at,
    'verified'::crosswalk_mapping_tier,
    'overlay'::text
FROM fw_to_scf_overlay_edges o
WHERE o.status = 'active'
  AND current_tenant_matches(o.tenant_id)
  AND NOT EXISTS (
      SELECT 1
      FROM fw_to_scf_edges c
      WHERE c.framework_requirement_id = o.framework_requirement_id
        AND c.scf_anchor_id = o.scf_anchor_id
        AND c.relationship_type <> 'no_relationship'
        AND c.mapping_tier <> 'rejected'
  );

GRANT SELECT ON effective_fw_to_scf_edges TO atlas_app;
