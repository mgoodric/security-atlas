-- Reverse of 20260511000011_exceptions.sql.
--
-- Drop order: dependent table first (exception_audit_log has no FK to
-- exceptions, but the dependency-order convention keeps the audit log
-- explicitly named first as the "soft dependent"). CASCADE is intentionally
-- NOT used so a buggy down migration that leaves orphans surfaces as an
-- error rather than silently dropping unrelated rows.

DROP TABLE IF EXISTS exception_audit_log;
DROP TABLE IF EXISTS exceptions;
