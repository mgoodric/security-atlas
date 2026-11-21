-- Reverses 20261285000000_risk_import_row_flags.sql.
ALTER TABLE risk_import_rows DROP CONSTRAINT IF EXISTS risk_import_rows_flags_chk;
ALTER TABLE risk_import_rows DROP COLUMN IF EXISTS flags;
