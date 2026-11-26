-- Down migration for 20261011000000_framework_scope_applicability_carry_over.sql
-- (#1793). Removes the carry-over rows from the workflow log and restores the
-- action CHECK to its #1787 shape.

DELETE FROM framework_scope_workflow_log WHERE action = 'applicability.carry_over';
ALTER TABLE framework_scope_workflow_log
    DROP CONSTRAINT framework_scope_workflow_log_action_check;
ALTER TABLE framework_scope_workflow_log
    ADD CONSTRAINT framework_scope_workflow_log_action_check
    CHECK (action IN ('create', 'predicate.update', 'applicability.update', 'submit', 'approve', 'activate', 'supersede'));
