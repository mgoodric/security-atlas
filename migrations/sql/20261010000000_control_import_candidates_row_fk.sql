-- security-atlas — #2038: control import suggestion follow-ups.
--
--   control_import_candidates.row_id
--       The FK to control_import_rows was row_id alone, so a candidate could
--       name a row of another batch (or another tenant) while carrying this
--       batch's tenant_id and batch_id. It becomes composite
--       (tenant_id, batch_id, row_id), the shape the batch FK already has.
--
--   idx_control_import_batches_pending
--       The stale-suggestions sweeper looks for batches left pending past a
--       cutoff. A partial index keeps that read off the full table.

DO $$
BEGIN
    ALTER TABLE control_import_rows
        ADD CONSTRAINT control_import_rows_tenant_batch_id_unique
        UNIQUE (tenant_id, batch_id, id);
EXCEPTION WHEN duplicate_object OR duplicate_table THEN NULL;
END $$;

ALTER TABLE control_import_candidates
    DROP CONSTRAINT IF EXISTS control_import_candidates_row_id_fkey;

DO $$
BEGIN
    ALTER TABLE control_import_candidates
        ADD CONSTRAINT control_import_candidates_row_tenant_batch_fk
        FOREIGN KEY (tenant_id, batch_id, row_id)
        REFERENCES control_import_rows (tenant_id, batch_id, id)
        ON DELETE CASCADE;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS idx_control_import_batches_pending
    ON control_import_batches (created_at)
    WHERE suggestions_status = 'pending';
