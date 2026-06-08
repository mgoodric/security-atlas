-- Down migration for 20260608050000_oscal_component_claim_disposition.sql
-- (slice 589). Drops the disposition audit table + the disposition metadata
-- columns + restores the slice-512 three-value claim_status CHECK.

DROP TABLE IF EXISTS imported_component_claim_dispositions;

ALTER TABLE imported_component_claims
    DROP COLUMN IF EXISTS disposition_note,
    DROP COLUMN IF EXISTS dispositioned_at,
    DROP COLUMN IF EXISTS dispositioned_by;

ALTER TABLE imported_component_claims
    DROP CONSTRAINT imported_component_claims_status_chk;
ALTER TABLE imported_component_claims
    ADD CONSTRAINT imported_component_claims_status_chk
        CHECK (claim_status IN ('asserted', 'accepted', 'rejected'));
