-- Reverses 20261288000000_risk_import_treatment.sql. Rejected rows carrying
-- the invalid_treatment or invalid_acceptance reason are deleted so the
-- prior reason CHECK holds.
DELETE FROM risk_import_rejected_rows WHERE reason IN ('invalid_treatment', 'invalid_acceptance');
ALTER TABLE risk_import_rejected_rows
    DROP CONSTRAINT IF EXISTS risk_import_rejected_rows_reason_chk;
ALTER TABLE risk_import_rejected_rows
    ADD CONSTRAINT risk_import_rejected_rows_reason_chk
        CHECK (reason IN ('missing_required_field', 'duplicate_external_ref',
                          'invalid_external_ref', 'unknown_category',
                          'invalid_review_due', 'invalid_score'));

ALTER TABLE risk_import_rows
    DROP CONSTRAINT IF EXISTS risk_import_rows_transfer_instrument_chk,
    DROP CONSTRAINT IF EXISTS risk_import_rows_mitigate_links_chk,
    DROP CONSTRAINT IF EXISTS risk_import_rows_accept_proposal_chk,
    DROP CONSTRAINT IF EXISTS risk_import_rows_requested_treatment_chk,
    DROP CONSTRAINT IF EXISTS risk_import_rows_treatment_chk;
ALTER TABLE risk_import_rows
    DROP COLUMN IF EXISTS unresolved_controls,
    DROP COLUMN IF EXISTS linked_control_ids,
    DROP COLUMN IF EXISTS instrument_reference,
    DROP COLUMN IF EXISTS historical_accepter,
    DROP COLUMN IF EXISTS proposed_until,
    DROP COLUMN IF EXISTS acceptance_justification,
    DROP COLUMN IF EXISTS requested_treatment,
    DROP COLUMN IF EXISTS treatment;
