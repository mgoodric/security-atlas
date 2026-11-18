-- Reverses 20261253000000_scf_remap_proposals_indexes.sql (#2325).

DROP INDEX IF EXISTS idx_scf_remap_proposals_tenant_page;
DROP INDEX IF EXISTS idx_scf_remap_proposals_target_version;
DROP INDEX IF EXISTS idx_scf_remap_proposals_legacy_anchor;
