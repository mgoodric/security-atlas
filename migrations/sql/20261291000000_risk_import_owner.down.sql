-- Reverses 20261291000000_risk_import_owner.sql.
ALTER TABLE risk_import_rows
    DROP CONSTRAINT IF EXISTS risk_import_rows_owner_chk;
ALTER TABLE risk_import_rows
    DROP COLUMN IF EXISTS owner_unlinked,
    DROP COLUMN IF EXISTS owner_user_id;
