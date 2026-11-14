-- Reverse #1798: effective_fw_to_scf_edges is the catalog edges again.
-- DROP + CREATE because CREATE OR REPLACE VIEW cannot drop the origin
-- column. No other view depends on this one.

DROP VIEW IF EXISTS effective_fw_to_scf_edges;

CREATE VIEW effective_fw_to_scf_edges
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
