-- security-atlas -- #2269: the exception_change evaluation trigger.
--
-- Activating, revoking or expiring an exception now re-evaluates the
-- control it covers, once the state change has committed. Those runs record
-- trigger = 'exception_change', so the ledger tells them apart from ingest,
-- scheduled, manual and replay runs.
--
-- The CHECK is widened: every row it accepted before it still accepts. An
-- exception_change run carries no triggered_by; the #2479 CHECK keeps that
-- column to manual runs.

ALTER TABLE control_evaluations
    DROP CONSTRAINT control_evaluations_trigger_chk;

ALTER TABLE control_evaluations
    ADD CONSTRAINT control_evaluations_trigger_chk
        CHECK (trigger IN ('ingest', 'scheduled', 'manual', 'replay', 'exception_change'));
