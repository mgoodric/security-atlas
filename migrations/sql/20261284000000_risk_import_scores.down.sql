-- Reverses 20261284000000_risk_import_scores.sql. Rejected rows carrying
-- the invalid_score reason are deleted so the prior reason CHECK holds.
DELETE FROM risk_import_rejected_rows WHERE reason = 'invalid_score';
ALTER TABLE risk_import_rejected_rows
    DROP CONSTRAINT IF EXISTS risk_import_rejected_rows_reason_chk;
ALTER TABLE risk_import_rejected_rows
    ADD CONSTRAINT risk_import_rejected_rows_reason_chk
        CHECK (reason IN ('missing_required_field', 'duplicate_external_ref',
                          'invalid_external_ref', 'unknown_category',
                          'invalid_review_due'));

ALTER TABLE risk_import_rows
    DROP CONSTRAINT IF EXISTS risk_import_rows_residual_needs_inherent_chk;
ALTER TABLE risk_import_rows
    DROP COLUMN IF EXISTS residual_score,
    DROP COLUMN IF EXISTS inherent_score;

ALTER TABLE risk_import_batches
    DROP CONSTRAINT IF EXISTS risk_import_batches_methodology_chk;
ALTER TABLE risk_import_batches
    DROP COLUMN IF EXISTS methodology;
