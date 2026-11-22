-- Reverses 20261294000000_risk_import_parent_links.sql.
DROP INDEX IF EXISTS risk_import_rows_waiting_parent_idx;
ALTER TABLE risk_import_rows
    DROP CONSTRAINT IF EXISTS risk_import_rows_parent_link_chk;
ALTER TABLE risk_import_rows
    DROP COLUMN IF EXISTS parent_risk_id,
    DROP COLUMN IF EXISTS parent_link_error,
    DROP COLUMN IF EXISTS parent_link_status,
    DROP COLUMN IF EXISTS parent_ref;
