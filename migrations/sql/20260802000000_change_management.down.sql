DROP TRIGGER IF EXISTS change_audit_log_no_update_trg ON change_audit_log;
DROP FUNCTION IF EXISTS change_audit_log_append_only();
DROP TABLE IF EXISTS change_audit_log;

DROP TABLE IF EXISTS change_controls;

DROP TRIGGER IF EXISTS changes_status_transition_trg ON changes;
DROP FUNCTION IF EXISTS changes_status_transition_guard();
DROP TABLE IF EXISTS changes;
