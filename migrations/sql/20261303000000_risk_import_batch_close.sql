-- security-atlas — #3002: an approver closes a risk import batch nobody is
-- going to finish.
--
-- risk_import_batches.status gains 'closed', the third and last state. A
-- batch is staged until either its last pending row is decided (completed)
-- or an approver closes it (closed). Closing rejects every pending row with
-- the closer's reason, as a per-row rejection does, so the rows waiting on
-- them are settled; a closed batch takes no further decision or mapping.
--
--   closed_by     the users(id) who closed it, never a machine credential
--   closed_at     when
--   close_reason  why, kept on the batch and on each row it rejected
--
-- There is no expiry: every rejection is a person's decision (#2881), and
-- a sweeper has no person to name (decisions log 3002, D2).

ALTER TABLE risk_import_batches
    ADD COLUMN IF NOT EXISTS closed_by TEXT NULL,
    ADD COLUMN IF NOT EXISTS closed_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS close_reason TEXT NULL;

ALTER TABLE risk_import_batches
    DROP CONSTRAINT IF EXISTS risk_import_batches_status_chk;
ALTER TABLE risk_import_batches
    ADD CONSTRAINT risk_import_batches_status_chk
        CHECK (status IN ('staged', 'completed', 'closed'));

ALTER TABLE risk_import_batches
    DROP CONSTRAINT IF EXISTS risk_import_batches_closed_chk;
ALTER TABLE risk_import_batches
    ADD CONSTRAINT risk_import_batches_closed_chk
        CHECK ((status = 'closed') = (closed_at IS NOT NULL)
               AND (status <> 'closed' OR (length(coalesce(closed_by, '')) > 0
                                          AND length(coalesce(close_reason, '')) > 0)));
