-- Down migration for 20260608060000_oscal_component_claim_scf_mapping.sql
-- (slice 620). Drops the claim-event-log generalization columns + restores
-- the slice-589 unconditional disposition-only to_status CHECK.

ALTER TABLE imported_component_claim_dispositions
    DROP CONSTRAINT IF EXISTS iccd_scf_mapping_anchor_chk;

ALTER TABLE imported_component_claim_dispositions
    DROP CONSTRAINT IF EXISTS iccd_to_status_chk;
ALTER TABLE imported_component_claim_dispositions
    ADD CONSTRAINT iccd_to_status_chk
        CHECK (to_status IN ('accepted', 'rejected', 'needs_info'));

ALTER TABLE imported_component_claim_dispositions
    DROP CONSTRAINT IF EXISTS iccd_event_kind_chk;

ALTER TABLE imported_component_claim_dispositions
    DROP COLUMN IF EXISTS to_scf_anchor_id,
    DROP COLUMN IF EXISTS from_scf_anchor_id,
    DROP COLUMN IF EXISTS event_kind;
