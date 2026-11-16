DROP INDEX IF EXISTS idx_control_import_batches_pending;
ALTER TABLE control_import_candidates
    DROP CONSTRAINT IF EXISTS control_import_candidates_row_tenant_batch_fk;
DO $$
BEGIN
    ALTER TABLE control_import_candidates
        ADD CONSTRAINT control_import_candidates_row_id_fkey
        FOREIGN KEY (row_id) REFERENCES control_import_rows (id) ON DELETE CASCADE;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
ALTER TABLE control_import_rows
    DROP CONSTRAINT IF EXISTS control_import_rows_tenant_batch_id_unique;
