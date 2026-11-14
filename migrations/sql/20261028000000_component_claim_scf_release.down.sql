-- Reverses 20261028000000_component_claim_scf_release.sql. Dropping the
-- column loses which SCF release each mapped vendor claim's code was bound
-- in; the claims and their codes stay.

ALTER TABLE imported_component_claims
    DROP CONSTRAINT IF EXISTS imported_component_claims_scf_release_needs_anchor_chk;

ALTER TABLE imported_component_claims
    DROP COLUMN IF EXISTS scf_release;
