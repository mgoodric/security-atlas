-- security-atlas -- #2118: auditor assignment on an audit period is logged.
--
-- POST /v1/audit-periods/{id}/auditors and
-- DELETE /v1/audit-periods/{id}/auditors/{userID} change the
-- auditor_assignments rows that grant an auditor read of one period. Each
-- change writes one audit_period_audit_log row in the same transaction.
--
-- The list from 20260511000020_audit_periods.sql, plus auditor_assigned
-- and auditor_removed.

ALTER TABLE audit_period_audit_log
    DROP CONSTRAINT audit_period_audit_log_action_chk;

ALTER TABLE audit_period_audit_log
    ADD CONSTRAINT audit_period_audit_log_action_chk
    CHECK (action IN (
        'period_created',
        'period_frozen',
        'freeze_rejected_already_frozen',
        'population_attached',
        'auditor_assigned',
        'auditor_removed'
    ));
