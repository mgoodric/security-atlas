-- security-atlas — slice 126 reversal.
--
-- Drops the audit_sink_failures table created by
-- 20260518000000_audit_sink_failures.sql.
--
-- Order: policies + grants are torn down implicitly by DROP TABLE.

DROP TABLE IF EXISTS audit_sink_failures;
