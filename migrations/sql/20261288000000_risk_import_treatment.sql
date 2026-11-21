-- security-atlas — #2842: treatment and acceptance on staged risk import
-- rows, under the rules a single create follows (spec #2812, Q14).
--
-- risk_import_rows gains what approval creates the risk with:
--
--   treatment                 the treatment the risk is created with. A
--                             mitigate row whose linked controls resolve to
--                             none of the tenant's, and a transfer row with
--                             no instrument reference, stage as untreated
--                             with the treatment_downgraded flag;
--   requested_treatment       the register's treatment when it was
--                             downgraded, NULL otherwise;
--   acceptance_justification  an accepted row's proposal. The risk is
--   proposed_until            created with a proposed acceptance, never an
--                             approved one (#2371, ADR-0024);
--   historical_accepter       who the register says accepted the risk. Kept
--                             as history and never the risk's accepter;
--   instrument_reference      a transfer's instrument (policy number, SOW);
--   linked_control_ids        the tenant's controls the linked-controls cell
--                             resolved to, by control id or bundle id;
--   unresolved_controls       the refs that resolved to none.
--
-- Rows staged before this migration had no treatment column and were
-- created untreated, which the default keeps.
--
-- Two new rejection reasons: invalid_treatment (a value that is no
-- treatment) and invalid_acceptance (an accepted row without the
-- justification or until-date its proposal needs, or an until-date that is
-- not a date).

ALTER TABLE risk_import_rows
    ADD COLUMN IF NOT EXISTS treatment                TEXT   NOT NULL DEFAULT 'untreated',
    ADD COLUMN IF NOT EXISTS requested_treatment      TEXT   NULL,
    ADD COLUMN IF NOT EXISTS acceptance_justification TEXT   NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS proposed_until           DATE   NULL,
    ADD COLUMN IF NOT EXISTS historical_accepter      TEXT   NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS instrument_reference     TEXT   NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS linked_control_ids       UUID[] NOT NULL DEFAULT '{}'::UUID[],
    ADD COLUMN IF NOT EXISTS unresolved_controls      TEXT[] NOT NULL DEFAULT '{}'::TEXT[];

ALTER TABLE risk_import_rows
    DROP CONSTRAINT IF EXISTS risk_import_rows_treatment_chk,
    DROP CONSTRAINT IF EXISTS risk_import_rows_requested_treatment_chk,
    DROP CONSTRAINT IF EXISTS risk_import_rows_accept_proposal_chk,
    DROP CONSTRAINT IF EXISTS risk_import_rows_mitigate_links_chk,
    DROP CONSTRAINT IF EXISTS risk_import_rows_transfer_instrument_chk;

ALTER TABLE risk_import_rows
    ADD CONSTRAINT risk_import_rows_treatment_chk
        CHECK (treatment IN ('untreated', 'accept', 'mitigate', 'transfer', 'avoid')),
    -- Only a mitigate or transfer is ever downgraded, and only to untreated.
    ADD CONSTRAINT risk_import_rows_requested_treatment_chk
        CHECK (requested_treatment IS NULL
               OR (requested_treatment IN ('mitigate', 'transfer') AND treatment = 'untreated')),
    ADD CONSTRAINT risk_import_rows_accept_proposal_chk
        CHECK (treatment <> 'accept'
               OR (length(acceptance_justification) > 0 AND proposed_until IS NOT NULL)),
    ADD CONSTRAINT risk_import_rows_mitigate_links_chk
        CHECK (treatment <> 'mitigate' OR cardinality(linked_control_ids) > 0),
    ADD CONSTRAINT risk_import_rows_transfer_instrument_chk
        CHECK (treatment <> 'transfer' OR length(instrument_reference) > 0);

ALTER TABLE risk_import_rejected_rows
    DROP CONSTRAINT IF EXISTS risk_import_rejected_rows_reason_chk;
ALTER TABLE risk_import_rejected_rows
    ADD CONSTRAINT risk_import_rejected_rows_reason_chk
        CHECK (reason IN ('missing_required_field', 'duplicate_external_ref',
                          'invalid_external_ref', 'unknown_category',
                          'invalid_review_due', 'invalid_score',
                          'invalid_treatment', 'invalid_acceptance'));
