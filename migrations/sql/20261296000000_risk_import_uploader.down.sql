-- Reverses 20261296000000_risk_import_uploader.sql.
ALTER TABLE risk_import_batches
    DROP COLUMN IF EXISTS uploader_user_id;
