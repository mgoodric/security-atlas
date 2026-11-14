-- security-atlas — #1771: a vendor claim records the SCF release its
-- scf_anchor_id code belongs to.
--
-- imported_component_claims.scf_anchor_id is a free-form SCF code
-- ("IAC-06"), and the SCF reuses codes between releases for different
-- controls. A code on its own stops meaning one anchor once a second SCF
-- release is loaded. The importer now binds each claim's code within a
-- declared release (atlas-oscal --scf-release, or an "scf-release" prop on
-- the document's metadata) and records that release here, so the
-- (scf_release, scf_anchor_id) pair names one anchor for good.
--
-- NULL: the claim is unmapped (scf_anchor_id IS NULL), or it was mapped
-- before this column existed. Existing rows are not backfilled: the release
-- they were bound in is not knowable after the fact, and guessing "current"
-- would be the silent re-binding this change exists to stop.
--
-- No RLS change: the table's tenant policies (20260608010000) cover the new
-- column (invariant #6).

ALTER TABLE imported_component_claims
    ADD COLUMN IF NOT EXISTS scf_release TEXT NULL;

ALTER TABLE imported_component_claims
    DROP CONSTRAINT IF EXISTS imported_component_claims_scf_release_needs_anchor_chk;

-- A release without a code names nothing.
ALTER TABLE imported_component_claims
    ADD CONSTRAINT imported_component_claims_scf_release_needs_anchor_chk
    CHECK (scf_release IS NULL OR scf_anchor_id IS NOT NULL);
