-- Reverse #2136: drop posture targets and their audit trail.

DROP TABLE IF EXISTS framework_posture_target_audit_log;
DROP FUNCTION IF EXISTS framework_posture_target_audit_log_append_only();
DROP TABLE IF EXISTS framework_posture_targets;
