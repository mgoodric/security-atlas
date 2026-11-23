-- Reverses 20261299000000_risk_import_parent_links_tenant.sql.
DROP INDEX IF EXISTS risk_import_rows_pending_ref_idx;
DROP INDEX IF EXISTS risk_import_rows_waiting_parent_idx;
CREATE INDEX IF NOT EXISTS risk_import_rows_waiting_parent_idx
    ON risk_import_rows (batch_id, lower(parent_ref))
    WHERE parent_link_status = 'waiting';
ALTER TABLE risk_import_rows
    DROP CONSTRAINT IF EXISTS risk_import_rows_parent_link_chk;
-- Rows linked through parent_risk_ids have no parent_ref; the earlier
-- constraint needs them back at 'none'. Their links stay in
-- risk_aggregations.
UPDATE risk_import_rows
SET parent_link_status = 'none', parent_link_error = '', parent_risk_id = NULL
WHERE parent_ref = '' AND parent_link_status <> 'none';
ALTER TABLE risk_import_rows
    DROP COLUMN IF EXISTS parent_risk_ids;
ALTER TABLE risk_import_rows
    ADD CONSTRAINT risk_import_rows_parent_link_chk
        CHECK (parent_link_status IN ('none', 'waiting', 'linked', 'failed')
               AND (parent_ref <> '' OR parent_link_status = 'none')
               AND (parent_link_status = 'none' OR status = 'approved')
               AND ((parent_link_status = 'linked') = (parent_risk_id IS NOT NULL))
               AND ((parent_link_status = 'failed') = (parent_link_error <> '')));
