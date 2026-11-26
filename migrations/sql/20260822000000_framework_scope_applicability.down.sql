-- Down migration for 20260822000000_framework_scope_applicability.sql (#1787).
-- Drops the decisions table and its trigger functions, removes the
-- applicability rows from the workflow log, and restores the log's action
-- CHECK to its #1786 shape.

DROP TABLE IF EXISTS framework_scope_applicability;
DROP FUNCTION IF EXISTS framework_scope_applicability_guard();
DROP FUNCTION IF EXISTS framework_scope_applicability_reset_approval();

DELETE FROM framework_scope_workflow_log WHERE action = 'applicability.update';
ALTER TABLE framework_scope_workflow_log
    DROP CONSTRAINT framework_scope_workflow_log_action_check;
ALTER TABLE framework_scope_workflow_log
    ADD CONSTRAINT framework_scope_workflow_log_action_check
    CHECK (action IN ('create', 'predicate.update', 'submit', 'approve', 'activate', 'supersede'));
