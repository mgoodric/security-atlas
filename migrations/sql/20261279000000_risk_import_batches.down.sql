-- Reverses 20261279000000_risk_import_batches.sql.
DROP TABLE IF EXISTS risk_import_audit_log;
DROP FUNCTION IF EXISTS risk_import_audit_log_append_only();
DROP TABLE IF EXISTS risk_import_rejected_rows;
DROP TABLE IF EXISTS risk_import_rows;
DROP TABLE IF EXISTS risk_import_batches;
