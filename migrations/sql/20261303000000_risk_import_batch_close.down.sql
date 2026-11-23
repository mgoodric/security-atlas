-- Reverses 20261303000000_risk_import_batch_close.sql. A closed batch has
-- no pending row left, so it goes back to completed; its rows keep their
-- rejections.
ALTER TABLE risk_import_batches
    DROP CONSTRAINT IF EXISTS risk_import_batches_closed_chk;
ALTER TABLE risk_import_batches
    DROP CONSTRAINT IF EXISTS risk_import_batches_status_chk;
UPDATE risk_import_batches SET status = 'completed' WHERE status = 'closed';
ALTER TABLE risk_import_batches
    ADD CONSTRAINT risk_import_batches_status_chk
        CHECK (status IN ('staged', 'completed'));
ALTER TABLE risk_import_batches
    DROP COLUMN IF EXISTS close_reason,
    DROP COLUMN IF EXISTS closed_at,
    DROP COLUMN IF EXISTS closed_by;
