-- Reverses 20261124000000_scf_remap_proposals_rebind_record.sql (#2236).
REVOKE UPDATE (status, target_anchor_id, source) ON scf_remap_proposals FROM atlas_app;
REVOKE INSERT ON scf_remap_proposals FROM atlas_app;
DROP POLICY IF EXISTS tenant_update ON scf_remap_proposals;
DROP POLICY IF EXISTS tenant_insert ON scf_remap_proposals;
ALTER TABLE scf_remap_proposals
    DROP CONSTRAINT IF EXISTS scf_remap_proposals_tenant_control_fk,
    DROP CONSTRAINT IF EXISTS scf_remap_proposals_target_matches_status,
    DROP CONSTRAINT IF EXISTS scf_remap_proposals_source_check,
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS target_anchor_id;
