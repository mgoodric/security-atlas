-- security-atlas — #2841: one scoring methodology per risk import batch,
-- and the inherent and residual scores staged from the register's columns
-- (spec #2812, Q16).
--
-- risk_import_batches.methodology is the batch's one methodology. Every risk
-- the batch creates is created with it. The import reads scores for the
-- three methodologies with a defined score shape: likelihood and impact for
-- nist_800_30 and qualitative_5x5, loss event frequency and loss magnitude
-- for fair. Batches staged before this migration had no scores and default
-- to nist_800_30, the methodology their risks were created with.
--
-- risk_import_rows.inherent_score / residual_score hold the staged scores
-- in that shape, NULL for an unscored row. A residual never stands without
-- an inherent, as on a single create.
--
-- A row whose score cells make no valid score is rejected with the new
-- invalid_score reason.

ALTER TABLE risk_import_batches
    ADD COLUMN IF NOT EXISTS methodology TEXT NOT NULL DEFAULT 'nist_800_30';
ALTER TABLE risk_import_batches
    DROP CONSTRAINT IF EXISTS risk_import_batches_methodology_chk;
ALTER TABLE risk_import_batches
    ADD CONSTRAINT risk_import_batches_methodology_chk
        CHECK (methodology IN ('nist_800_30', 'qualitative_5x5', 'fair'));

ALTER TABLE risk_import_rows
    ADD COLUMN IF NOT EXISTS inherent_score JSONB NULL,
    ADD COLUMN IF NOT EXISTS residual_score JSONB NULL;
ALTER TABLE risk_import_rows
    DROP CONSTRAINT IF EXISTS risk_import_rows_residual_needs_inherent_chk;
ALTER TABLE risk_import_rows
    ADD CONSTRAINT risk_import_rows_residual_needs_inherent_chk
        CHECK (residual_score IS NULL OR inherent_score IS NOT NULL);

ALTER TABLE risk_import_rejected_rows
    DROP CONSTRAINT IF EXISTS risk_import_rejected_rows_reason_chk;
ALTER TABLE risk_import_rejected_rows
    ADD CONSTRAINT risk_import_rejected_rows_reason_chk
        CHECK (reason IN ('missing_required_field', 'duplicate_external_ref',
                          'invalid_external_ref', 'unknown_category',
                          'invalid_review_due', 'invalid_score'));
