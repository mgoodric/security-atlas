-- Reverse #2172: drop member module access and its audit log.

DROP TABLE IF EXISTS member_module_access_audit_log;
DROP FUNCTION IF EXISTS member_module_access_audit_log_append_only();
DROP TABLE IF EXISTS member_module_access;
