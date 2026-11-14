-- security-atlas — applicability.carry_over workflow-log action (issue #1793,
-- Statement of Applicability #1744).
--
-- When a framework version moves from N to N+1, a tenant copies its
-- applicability decisions for requirements whose carry-over a super_admin
-- approved in the version-migration queue (ADR 0019) into its own draft
-- revision on N+1 (internal/frameworkscope.Store.CarryOverDecisions). Each
-- copied decision is logged under its own action so an auditor can tell a
-- carried-over decision from one set by hand; the row's `after` image names
-- the source version and revision.
--
-- Widens the action CHECK only. Every existing value is kept.

ALTER TABLE framework_scope_workflow_log
    DROP CONSTRAINT framework_scope_workflow_log_action_check;
ALTER TABLE framework_scope_workflow_log
    ADD CONSTRAINT framework_scope_workflow_log_action_check
    CHECK (action IN ('create', 'predicate.update', 'applicability.update', 'applicability.carry_over', 'submit', 'approve', 'activate', 'supersede'));
