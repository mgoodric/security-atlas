-- Reverse #1749: drop the risk placement audit trail.

DROP TRIGGER IF EXISTS risk_audit_log_append_only_trg ON risk_audit_log;
DROP FUNCTION IF EXISTS risk_audit_log_append_only();
DROP TABLE IF EXISTS risk_audit_log;
