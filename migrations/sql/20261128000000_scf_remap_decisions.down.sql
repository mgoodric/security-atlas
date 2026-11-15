-- Reverses 20261128000000_scf_remap_decisions.sql (#1776). Decided
-- proposals return to open, so the narrower CHECKs hold again.
DROP TABLE IF EXISTS scf_remap_decisions;
ALTER TABLE scf_remap_proposals
    DROP CONSTRAINT IF EXISTS scf_remap_proposals_tenant_id_uniq;
UPDATE scf_remap_proposals
SET status = 'open', target_anchor_id = NULL
WHERE status IN ('approved', 'rejected', 'kept_legacy');
ALTER TABLE scf_remap_proposals
    DROP CONSTRAINT IF EXISTS scf_remap_proposals_target_matches_status;
ALTER TABLE scf_remap_proposals
    ADD CONSTRAINT scf_remap_proposals_target_matches_status
    CHECK ((status = 'rebound') = (target_anchor_id IS NOT NULL)) NOT VALID;
ALTER TABLE scf_remap_proposals
    DROP CONSTRAINT IF EXISTS scf_remap_proposals_status_check;
ALTER TABLE scf_remap_proposals
    ADD CONSTRAINT scf_remap_proposals_status_check
    CHECK (status IN ('open', 'rebound'));
