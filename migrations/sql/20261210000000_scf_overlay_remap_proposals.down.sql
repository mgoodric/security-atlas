-- Reverses 20261210000000_scf_overlay_remap_proposals.sql (#1804). Overlay
-- edges an approval created or retired stay as they are; only the queue and
-- its history go.
DROP TABLE IF EXISTS scf_overlay_remap_decisions;
DROP TABLE IF EXISTS scf_overlay_remap_proposals;
ALTER TABLE fw_to_scf_overlay_edges DROP CONSTRAINT IF EXISTS fw_to_scf_overlay_edges_tenant_id_uniq;
