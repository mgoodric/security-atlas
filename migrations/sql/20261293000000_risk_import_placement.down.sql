-- Reverses 20261293000000_risk_import_placement.sql. Rejected rows carrying
-- a placement reason are deleted so the prior reason CHECK holds.
DELETE FROM risk_import_rejected_rows
WHERE reason IN ('invalid_level', 'unknown_org_unit', 'ambiguous_org_unit', 'level_mismatch');
ALTER TABLE risk_import_rejected_rows
    DROP CONSTRAINT IF EXISTS risk_import_rejected_rows_reason_chk;
ALTER TABLE risk_import_rejected_rows
    ADD CONSTRAINT risk_import_rejected_rows_reason_chk
        CHECK (reason IN ('missing_required_field', 'duplicate_external_ref',
                          'invalid_external_ref', 'unknown_category',
                          'invalid_review_due', 'invalid_score',
                          'invalid_treatment', 'invalid_acceptance'));

ALTER TABLE risk_import_rows
    DROP CONSTRAINT IF EXISTS risk_import_rows_org_unit_chk;
ALTER TABLE risk_import_rows
    DROP COLUMN IF EXISTS org_unit_id,
    DROP COLUMN IF EXISTS org_unit_value,
    DROP COLUMN IF EXISTS level;
