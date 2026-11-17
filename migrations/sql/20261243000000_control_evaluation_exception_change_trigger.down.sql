-- Reverses 20261243000000_control_evaluation_exception_change_trigger.sql.
--
-- Fails while any exception_change row exists: the ledger is append-only,
-- so those rows are not rewritten to make the narrower CHECK fit.

ALTER TABLE control_evaluations
    DROP CONSTRAINT control_evaluations_trigger_chk;

ALTER TABLE control_evaluations
    ADD CONSTRAINT control_evaluations_trigger_chk
        CHECK (trigger IN ('ingest', 'scheduled', 'manual', 'replay'));
