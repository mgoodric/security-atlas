-- Issue #1796 (prefactor for #1735, tenant overlay edges).
--
-- effective_fw_to_scf_edges is the SINGLE PLACE where requirement -> SCF
-- anchor edge eligibility is decided for coverage and traversal. Framework
-- posture (dashboard coverage %, 90-day trend, dashboard exports, MCP program
-- reads), per-requirement coverage, per-control coverage and UCF traversal
-- all read edges through this view, never fw_to_scf_edges directly.
--
-- Today the view is exactly the catalog edges, so every result is unchanged.
-- Adding tenant overlay edges (#1735) or excluding rejected catalog edges
-- (#1755) is a change to this view and nothing else.
--
-- Catalog administration (importers, the mapping-tier state machine, the
-- canonical catalog export) still reads and writes fw_to_scf_edges itself:
-- those manage the catalog, they do not compute coverage.
--
-- security_invoker so that a future tenant-scoped arm is filtered by the
-- caller's RLS context rather than the view owner's.

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
    e.mapping_tier
FROM fw_to_scf_edges e;

GRANT SELECT ON effective_fw_to_scf_edges TO atlas_app;
